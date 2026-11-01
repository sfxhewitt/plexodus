// Package stream is the browser fallback for files a browser can't play as
// they are. It never re-encodes video: ffmpeg copies the video stream into
// fragmented-MP4 HLS segments (a remux), and only converts the audio to AAC
// when the browser can't decode the original track. Apps and TVs keep
// direct-playing the original file; this is only used by web browsers.
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
)

type Options struct {
	Path       string  // media file
	Start      float64 // seconds into the file
	AudioIndex int     // absolute stream index of the audio track, -1 for none
	AudioMode  string  // "copy" or "aac"
	VideoCodec string  // from the probe; hevc gets the hvc1 tag Safari needs
}

type Session struct {
	ID       string
	UserID   int64
	FileID   int64
	Opts     Options
	Dir      string
	cmd      *exec.Cmd
	done     chan struct{}
	err      error
	mu       sync.Mutex
	lastUsed time.Time
	stopped  time.Time
}

func (s *Session) touch() {
	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()
}

// Running reports whether ffmpeg is still producing segments.
func (s *Session) Running() bool {
	select {
	case <-s.done:
		return false
	default:
		return true
	}
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

// Start launches ffmpeg for one playback position. A user gets one session
// per file: starting another (a seek outside the buffered range) stops the
// previous one so a single viewer never runs two remuxes of the same file.
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

	args := []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}
	if o.Start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(o.Start, 'f', 3, 64))
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
	args = append(args,
		"-sn", "-dn", "-map_metadata", "-1", "-map_chapters", "-1",
		"-f", "hls",
		"-hls_time", strconv.Itoa(segmentSeconds),
		"-hls_playlist_type", "event",
		"-hls_segment_type", "fmp4",
		"-hls_flags", "independent_segments+temp_file",
		"-hls_fmp4_init_filename", "init.mp4",
		"-hls_segment_filename", filepath.Join(dir, "seg%05d.m4s"),
		filepath.Join(dir, "index.m3u8"),
	)
	cmd := exec.Command(m.ffmpeg, args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	s := &Session{ID: id, UserID: userID, FileID: fileID, Opts: o, Dir: dir, cmd: cmd, done: make(chan struct{}), lastUsed: time.Now()}
	go func() {
		err := cmd.Wait()
		if err != nil && stderr.Len() > 0 {
			err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		s.mu.Lock()
		s.err = err
		s.stopped = time.Now()
		s.mu.Unlock()
		close(s.done)
		if err != nil && !strings.Contains(err.Error(), "signal: killed") {
			log.Printf("stream %s (%s): ffmpeg: %v", id, filepath.Base(o.Path), err)
		}
	}()

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

func (s *Session) stop() {
	if s.Running() && s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
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

// Stop ends a session; the client calls this when the player closes.
func (m *Manager) Stop(id string, userID int64) {
	if s := m.Get(id, userID); s != nil {
		s.stop()
	}
}

// File waits for ffmpeg to produce the named playlist or segment and
// returns its path. It gives up when ffmpeg exits without producing it.
func (s *Session) File(ctx context.Context, name string, wait time.Duration) (string, error) {
	if !safeName.MatchString(name) {
		return "", os.ErrNotExist
	}
	p := filepath.Join(s.Dir, name)
	deadline := time.Now().Add(wait)
	for {
		if st, err := os.Stat(p); err == nil && st.Size() > 0 {
			if name != "index.m3u8" || playlistReady(p) {
				return p, nil
			}
		}
		if !s.Running() {
			// One last look: the file may have landed just before exit.
			if st, err := os.Stat(p); err == nil && st.Size() > 0 {
				return p, nil
			}
			s.mu.Lock()
			err := s.err
			s.mu.Unlock()
			if err != nil {
				return "", err
			}
			return "", os.ErrNotExist
		}
		if time.Now().After(deadline) {
			return "", context.DeadlineExceeded
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// playlistReady is true once the playlist lists at least one segment, so
// the player doesn't start on an empty list and give up.
func playlistReady(p string) bool {
	b, err := os.ReadFile(p)
	return err == nil && strings.Contains(string(b), ".m4s")
}

// Playlist returns the playlist text, with a start tag so players begin at
// the beginning of the (growing) stream instead of near its live edge.
func (s *Session) Playlist(p string) ([]byte, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	txt := string(b)
	if !strings.Contains(txt, "#EXT-X-START") {
		txt = strings.Replace(txt, "#EXTM3U\n", "#EXTM3U\n#EXT-X-START:TIME-OFFSET=0,PRECISE=YES\n", 1)
	}
	return []byte(txt), nil
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
			stopped := s.stopped
			s.mu.Unlock()
			if s.Running() && idle > idleTimeout {
				s.stop()
				continue
			}
			if !s.Running() && idle > keepDir && !stopped.IsZero() {
				delete(m.sessions, id)
				os.RemoveAll(s.Dir)
			}
		}
		m.mu.Unlock()
	}
}
