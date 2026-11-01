// Package stream is the browser fallback for files a browser can't play as
// they are. It never re-encodes video: ffmpeg copies the video stream into
// fragmented-MP4 HLS segments (a remux), and only converts the audio to AAC
// when the browser can't decode the original track. Apps and TVs keep
// direct-playing the original file; this is only used by web browsers.
//
// Two modes:
//
// VOD (Matroska files with a cue index): the keyframe times are read from
// the file up front, so the server can hand the player a complete playlist
// with exact segment durations before a single byte has been remuxed. The
// player then behaves like it would with a plain file: the full duration,
// native seeking anywhere, and resuming by setting currentTime. ffmpeg
// writes one fragment per keyframe into a single growing file with
// absolute timestamps; segments are byte ranges of it. Seeking into a part
// that hasn't been produced yet restarts ffmpeg from that keyframe, and
// since every run shares one timeline, fragments from different runs fit
// together.
//
// Event (everything else): ffmpeg's own growing playlist from the requested
// start, and the player restarts the stream to seek outside it.
package stream

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	segmentSeconds = 4
	idleTimeout    = 2 * time.Minute  // no fetch or ping: stop ffmpeg
	keepDir        = 10 * time.Minute // after that, delete the segments
	tsOffsetMs     = 10_000           // keeps every output timestamp positive
	lookaheadMs    = 16_000           // a run this close to a wanted segment is left to reach it
	maxKeyframes   = 100_000          // beyond this (all-intra video) VOD mode makes no sense
)

type Options struct {
	Path       string  // media file
	Start      float64 // seconds into the file
	AudioIndex int     // absolute stream index of the audio track, -1 for none
	AudioMode  string  // "copy" or "aac"
	VideoCodec string  // from the probe; hevc gets the hvc1 tag Safari needs
}

// Chunk is a served file, or a byte range of one when Size > 0.
type Chunk struct {
	Path      string
	Off, Size int64
}

// run is one ffmpeg process.
type run struct {
	target  int64 // ms; the keyframe it was asked to start from
	path    string
	cmd     *exec.Cmd
	done    chan struct{}
	err     error
	started time.Time
	f       *os.File
	scan    fmp4Scan
	logged  bool
	stopped time.Time
}

func (r *run) running() bool {
	select {
	case <-r.done:
		return false
	default:
		return true
	}
}

func (r *run) kill() {
	if r.running() && r.cmd.Process != nil {
		r.cmd.Process.Kill()
	}
}

// position is how far the run has got: the time of its newest fragment,
// or where it was asked to start before the first one lands.
func (r *run) position() int64 {
	if n := len(r.scan.frags); n > 0 && r.scan.frags[n-1].ptsMs > r.target {
		return r.scan.frags[n-1].ptsMs
	}
	return r.target
}

type Session struct {
	ID     string
	UserID int64
	FileID int64
	Opts   Options
	Dir    string

	vod       bool
	segStarts []int64 // ms; VOD only
	durMs     int64

	mu       sync.Mutex
	runs     []*run // VOD: every run so far, oldest first; event: one
	cur      *run
	lastUsed time.Time
}

func (s *Session) touch() {
	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()
}

// VOD reports whether the playlist is complete up front.
func (s *Session) VOD() bool { return s.vod }

// Running reports whether ffmpeg is producing segments right now.
func (s *Session) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur != nil && s.cur.running()
}

// Finished reports whether the stream needs no process: in event mode
// ffmpeg completed the whole file; in VOD mode segments are made on demand
// so the client never has to restart anything.
func (s *Session) Finished() bool {
	if s.vod {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur != nil && !s.cur.running() && s.cur.err == nil
}

type Manager struct {
	ffmpeg   string
	dir      string
	mu       sync.Mutex
	sessions map[string]*Session
}

func New(ffmpeg, dir string) *Manager {
	m := &Manager{ffmpeg: ffmpeg, dir: dir, sessions: map[string]*Session{}}
	os.RemoveAll(dir)
	os.MkdirAll(dir, 0o755)
	return m
}

// Available reports whether ffmpeg can be found.
func (m *Manager) Available() bool {
	_, err := exec.LookPath(m.ffmpeg)
	return err == nil
}

var safeName = regexp.MustCompile(`^(index\.m3u8|init\.mp4|seg[0-9]{5}\.m4s)$`)

// Start opens a session for one file. A user gets one session per file:
// starting another stops the previous one so a single viewer never runs
// two remuxes of the same file.
func (m *Manager) Start(ctx context.Context, userID, fileID int64, o Options) (*Session, error) {
	if !m.Available() {
		return nil, errors.New("ffmpeg is not installed on the server")
	}
	if o.Start < 0 {
		o.Start = 0
	}
	if o.AudioMode != "aac" {
		o.AudioMode = "copy"
	}
	b := make([]byte, 8)
	rand.Read(b)
	id := hex.EncodeToString(b)
	dir := filepath.Join(m.dir, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	s := &Session{ID: id, UserID: userID, FileID: fileID, Opts: o, Dir: dir, lastUsed: time.Now()}

	if kf := m.keyframes(o.Path); kf != nil {
		s.vod = true
		s.segStarts = kf.Segments(segmentSeconds)
		s.durMs = kf.DurationMs
		k := s.segmentAt(int64(o.Start * 1000))
		if _, err := s.startRun(m.ffmpeg, s.segStarts[k], s.segStarts[k] == s.segStarts[0]); err != nil {
			os.RemoveAll(dir)
			return nil, err
		}
	} else {
		if _, err := s.startEvent(m.ffmpeg); err != nil {
			os.RemoveAll(dir)
			return nil, err
		}
	}

	m.mu.Lock()
	for _, old := range m.sessions {
		if old.UserID == userID && old.FileID == fileID {
			old.stop()
		}
	}
	m.sessions[id] = s
	m.mu.Unlock()
	return s, nil
}

// keyframes returns the cue index for files VOD mode can handle.
func (m *Manager) keyframes(path string) *Keyframes {
	if !strings.EqualFold(filepath.Ext(path), ".mkv") {
		return nil
	}
	t := time.Now()
	kf, err := ReadKeyframes(path)
	if err != nil {
		log.Printf("stream: %s: no keyframe index (%v), using event mode", filepath.Base(path), err)
		return nil
	}
	if len(kf.TimesMs) > maxKeyframes {
		return nil
	}
	log.Printf("stream: %s: %d keyframes read in %s", filepath.Base(path), len(kf.TimesMs), time.Since(t).Round(time.Millisecond))
	return kf
}

// segmentAt returns the index of the segment containing ms.
func (s *Session) segmentAt(ms int64) int {
	k := 0
	for i, st := range s.segStarts {
		if st <= ms {
			k = i
		}
	}
	return k
}

// args builds the ffmpeg command line shared by both modes.
func (s *Session) args(startMs int64, seek bool) []string {
	o := s.Opts
	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}
	if seek {
		// Input seeking lands on the keyframe at or before the time; the
		// fragments before the wanted keyframe are ignored by the scanner.
		args = append(args, "-ss", strconv.FormatFloat(float64(startMs)/1000, 'f', 3, 64))
	}
	if s.vod {
		args = append(args, "-copyts")
	}
	args = append(args, "-i", o.Path, "-map", "0:v:0")
	if o.AudioIndex >= 0 {
		args = append(args, "-map", "0:"+strconv.Itoa(o.AudioIndex))
	}
	args = append(args, "-c:v", "copy")
	if o.VideoCodec == "hevc" {
		args = append(args, "-tag:v", "hvc1")
	}
	if o.AudioIndex >= 0 {
		if o.AudioMode == "aac" {
			args = append(args, "-c:a", "aac", "-ac", "2", "-b:a", "192k")
		} else {
			args = append(args, "-c:a", "copy")
		}
	}
	return append(args, "-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1")
}

func (s *Session) launch(ffmpeg string, args []string, target int64) (*run, error) {
	cmd := exec.Command(ffmpeg, args...)
	cmd.Dir = s.Dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	r := &run{target: target, cmd: cmd, done: make(chan struct{}), started: time.Now()}
	r.scan.offsetMs = tsOffsetMs
	go func() {
		err := cmd.Wait()
		if err != nil && stderr.Len() > 0 {
			err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		s.mu.Lock()
		r.err = err
		r.stopped = time.Now()
		if r.f != nil {
			// Final scan so the last fragment is seen.
			r.scan.scan(r.f)
		}
		s.mu.Unlock()
		close(r.done)
		if err != nil && !strings.Contains(err.Error(), "signal: killed") {
			log.Printf("stream %s (%s): ffmpeg: %v", s.ID, filepath.Base(s.Opts.Path), err)
		}
	}()
	return r, nil
}

// startRun (VOD) starts ffmpeg from a keyframe, replacing the current run.
// Caller holds no lock.
func (s *Session) startRun(ffmpeg string, targetMs int64, fromStart bool) (*run, error) {
	s.mu.Lock()
	n := len(s.runs)
	if s.cur != nil {
		s.cur.kill()
	}
	s.mu.Unlock()
	path := filepath.Join(s.Dir, fmt.Sprintf("run%03d.mp4", n))
	args := append(s.args(targetMs, !fromStart),
		"-output_ts_offset", strconv.Itoa(tsOffsetMs/1000),
		"-avoid_negative_ts", "disabled",
		"-f", "mp4",
		"-movflags", "empty_moov+frag_keyframe+default_base_moof+frag_discont+skip_trailer",
		path)
	r, err := s.launch(ffmpeg, args, targetMs)
	if err != nil {
		return nil, err
	}
	r.path = path
	s.mu.Lock()
	s.runs = append(s.runs, r)
	s.cur = r
	s.mu.Unlock()
	return r, nil
}

// startEvent starts ffmpeg writing its own HLS playlist from Opts.Start.
func (s *Session) startEvent(ffmpeg string) (*run, error) {
	startMs := int64(s.Opts.Start * 1000)
	args := append(s.args(startMs, startMs > 0),
		"-f", "hls",
		"-hls_time", strconv.Itoa(segmentSeconds),
		"-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4",
		"-hls_flags", "independent_segments+temp_file",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(s.Dir, "seg%05d.m4s"),
		filepath.Join(s.Dir, "index.m3u8"),
	)
	r, err := s.launch(ffmpeg, args, startMs)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.runs = []*run{r}
	s.cur = r
	s.mu.Unlock()
	return r, nil
}

func (s *Session) stop() {
	s.mu.Lock()
	for _, r := range s.runs {
		r.kill()
	}
	s.mu.Unlock()
}

// Get returns a live session owned by the user.
func (m *Manager) Get(id string, userID int64) *Session {
	m.mu.Lock()
	s := m.sessions[id]
	m.mu.Unlock()
	if s == nil || s.UserID != userID {
		return nil
	}
	s.touch()
	return s
}

// Stop ends a session's ffmpeg; the client calls this when the player closes.
func (m *Manager) Stop(id string, userID int64) {
	if s := m.Get(id, userID); s != nil {
		s.stop()
	}
}

// Playlist returns the playlist text. In VOD mode it is built from the
// keyframe index and available at once; in event mode it waits for ffmpeg
// to list a first segment, with a start tag so players begin at the start
// of the growing stream instead of near its live edge.
func (s *Session) Playlist(ctx context.Context, wait time.Duration) ([]byte, error) {
	if s.vod {
		return []byte(s.vodPlaylist()), nil
	}
	c, err := s.File(ctx, "index.m3u8", wait)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(c.Path)
	if err != nil {
		return nil, err
	}
	txt := string(b)
	if !strings.Contains(txt, "#EXT-X-START") {
		txt = strings.Replace(txt, "#EXTM3U\n", "#EXTM3U\n#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n", 1)
	}
	return []byte(txt), nil
}

func (s *Session) vodPlaylist() string {
	var b strings.Builder
	maxDur := int64(0)
	durs := make([]int64, len(s.segStarts))
	for k := range s.segStarts {
		end := s.durMs
		if k+1 < len(s.segStarts) {
			end = s.segStarts[k+1]
		}
		durs[k] = end - s.segStarts[k]
		if durs[k] > maxDur {
			maxDur = durs[k]
		}
	}
	fmt.Fprintf(&b, "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-TARGETDURATION:%d\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-INDEPENDENT-SEGMENTS\n#EXT-X-MAP:URI=\"init.mp4\"\n", (maxDur+999)/1000)
	for k, d := range durs {
		fmt.Fprintf(&b, "#EXTINF:%d.%03d,\nseg%05d.m4s\n", d/1000, d%1000, k)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

// File waits for ffmpeg to produce the named playlist, init or segment and
// returns where to read it. It gives up when ffmpeg exits without it.
func (s *Session) File(ctx context.Context, name string, wait time.Duration) (Chunk, error) {
	if !safeName.MatchString(name) {
		return Chunk{}, os.ErrNotExist
	}
	if s.vod {
		return s.vodFile(ctx, name, wait)
	}
	p := filepath.Join(s.Dir, name)
	deadline := time.Now().Add(wait)
	for {
		if st, err := os.Stat(p); err == nil && st.Size() > 0 {
			if name != "index.m3u8" || playlistReady(p) {
				s.logFirst()
				return Chunk{Path: p}, nil
			}
		}
		s.mu.Lock()
		r := s.cur
		s.mu.Unlock()
		if !r.running() {
			// One last look: the file may have landed just before exit.
			if st, err := os.Stat(p); err == nil && st.Size() > 0 {
				return Chunk{Path: p}, nil
			}
			s.mu.Lock()
			err := r.err
			s.mu.Unlock()
			if err != nil {
				return Chunk{}, err
			}
			return Chunk{}, os.ErrNotExist
		}
		if time.Now().After(deadline) {
			return Chunk{}, context.DeadlineExceeded
		}
		select {
		case <-ctx.Done():
			return Chunk{}, ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

func (s *Session) logFirst() {
	s.mu.Lock()
	r := s.cur
	if r != nil && !r.logged {
		r.logged = true
		log.Printf("stream %s (%s): first segment ready %s after start (from %ds)", s.ID, filepath.Base(s.Opts.Path), time.Since(r.started).Round(100*time.Millisecond), r.target/1000)
	}
	s.mu.Unlock()
}

// playlistReady is true once the playlist lists at least one segment, so
// the player doesn't start on an empty list and give up.
func playlistReady(p string) bool {
	b, err := os.ReadFile(p)
	return err == nil && strings.Contains(string(b), ".m4s")
}

// vodFile serves the init segment or a segment of the VOD playlist,
// starting a new ffmpeg run when the wanted part isn't coming otherwise.
func (s *Session) vodFile(ctx context.Context, name string, wait time.Duration) (Chunk, error) {
	if name == "index.m3u8" {
		return Chunk{}, os.ErrNotExist
	}
	k := -1
	if name != "init.mp4" {
		n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, "seg"), ".m4s"))
		if n < 0 || n >= len(s.segStarts) {
			return Chunk{}, os.ErrNotExist
		}
		k = n
	}
	deadline := time.Now().Add(wait)
	restarted := false
	for {
		s.mu.Lock()
		for _, r := range s.runs {
			s.scanRun(r)
		}
		var c Chunk
		var ok bool
		if k < 0 {
			c, ok = s.initChunk()
		} else {
			c, ok = s.segmentChunk(k)
		}
		cur := s.cur
		var curErr error
		if cur != nil && !cur.running() {
			curErr = cur.err
		}
		s.mu.Unlock()
		if ok {
			return c, nil
		}
		if k >= 0 && !restarted {
			lo := s.segStarts[k]
			// Let the current run reach the segment when it is about to;
			// otherwise start from this segment's keyframe.
			far := cur == nil || !cur.running() || cur.target > lo || cur.position() < lo-lookaheadMs
			if far {
				if curErr != nil && cur.target == lo {
					return Chunk{}, curErr
				}
				restarted = true
				if _, err := s.startRun(s.ffmpegPath(), lo, lo == s.segStarts[0]); err != nil {
					return Chunk{}, err
				}
				continue
			}
		} else if curErr != nil {
			// The run we were waiting on died.
			return Chunk{}, curErr
		}
		if time.Now().After(deadline) {
			return Chunk{}, context.DeadlineExceeded
		}
		select {
		case <-ctx.Done():
			return Chunk{}, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// scanRun reads newly written fragments of a run. Caller holds s.mu.
func (s *Session) scanRun(r *run) {
	if r.f == nil {
		f, err := os.Open(r.path)
		if err != nil {
			return
		}
		r.f = f
	}
	before := len(r.scan.frags)
	r.scan.scan(r.f)
	if before == 0 && len(r.scan.frags) > 0 && !r.logged {
		r.logged = true
		log.Printf("stream %s (%s): first segment ready %s after start (from %ds)", s.ID, filepath.Base(s.Opts.Path), time.Since(r.started).Round(100*time.Millisecond), r.target/1000)
	}
}

func (s *Session) ffmpegPath() string {
	return s.runs[0].cmd.Path
}

// initChunk returns the init segment from any run that has written one.
// Caller holds s.mu.
func (s *Session) initChunk() (Chunk, bool) {
	for _, r := range s.runs {
		if r.scan.initSize > 0 {
			return Chunk{Path: r.path, Off: 0, Size: r.scan.initSize}, true
		}
	}
	return Chunk{}, false
}

// segmentChunk finds a run holding every fragment of segment k. Caller
// holds s.mu.
func (s *Session) segmentChunk(k int) (Chunk, bool) {
	lo := s.segStarts[k]
	hi := int64(-1)
	if k+1 < len(s.segStarts) {
		hi = s.segStarts[k+1]
	}
	const slack = 2 // ms; cue times and fragment times are both whole ms
	for _, r := range s.runs {
		if r.target > lo+slack {
			continue
		}
		first, last := -1, -1
		complete := false
		for i, fr := range r.scan.frags {
			if fr.ptsMs < r.target-slack || fr.ptsMs < lo-slack {
				continue // pre-roll from the input seek, or an earlier segment
			}
			if hi >= 0 && fr.ptsMs >= hi-slack {
				complete = true
				break
			}
			if first < 0 {
				first = i
			}
			last = i
		}
		if first < 0 {
			continue
		}
		if !complete && !(!r.running() && r.err == nil) {
			continue // the run is still inside this segment
		}
		fs := r.scan.frags
		return Chunk{Path: r.path, Off: fs[first].off, Size: fs[last].off + fs[last].size - fs[first].off}, true
	}
	return Chunk{}, false
}

// Reap runs until ctx ends, stopping idle sessions and deleting old
// segment directories.
func (m *Manager) Reap(ctx context.Context) {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			m.mu.Lock()
			for _, s := range m.sessions {
				s.stop()
			}
			m.mu.Unlock()
			return
		case <-t.C:
		}
		now := time.Now()
		m.mu.Lock()
		for id, s := range m.sessions {
			s.mu.Lock()
			idle := now.Sub(s.lastUsed)
			running := s.cur != nil && s.cur.running()
			s.mu.Unlock()
			if running && idle > idleTimeout {
				s.stop()
				continue
			}
			if !running && idle > keepDir {
				delete(m.sessions, id)
				s.mu.Lock()
				for _, r := range s.runs {
					if r.f != nil {
						r.f.Close()
					}
				}
				s.mu.Unlock()
				os.RemoveAll(s.Dir)
			}
		}
		m.mu.Unlock()
	}
}
