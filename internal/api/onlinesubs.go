package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sfxhewitt/plexodus/internal/db"
	"github.com/sfxhewitt/plexodus/internal/probe"
	"github.com/sfxhewitt/plexodus/internal/subfetch"
	"github.com/sfxhewitt/plexodus/internal/subs"
)

// Online subtitles: when a video has no text subtitles in the wanted
// language (English unless changed), Plexodus looks for some on
// OpenSubtitles the first time it is played and keeps them in its own data
// folder, never beside the media. See internal/subfetch for how a match is
// chosen. The track then shows up in every player as "Downloaded".

const (
	onlineSubsKey     = "online_subs"      // "off" disables
	onlineSubsLangKey = "online_subs_lang" // ISO 639-2, default eng
	onlineTrackID     = "o0"
)

type onlineSubs struct {
	client *subfetch.Client
	dir    string

	mu       sync.Mutex
	inflight map[int64]chan struct{}
	// pausedUntil holds searches back after the day's downloads ran out.
	pausedUntil time.Time
	// one search at a time, so a season being played through doesn't
	// hammer the source.
	gate chan struct{}
}

func newOnlineSubs(dataDir string) *onlineSubs {
	return &onlineSubs{client: subfetch.New(), dir: filepath.Join(dataDir, "subs-online"), inflight: map[int64]chan struct{}{}, gate: make(chan struct{}, 1)}
}

func (o *onlineSubs) file(fileID int64, lang string) string {
	return filepath.Join(o.dir, fmt.Sprintf("%d.%s.srt", fileID, lang))
}

func (s *Server) onlineSubsEnabled(ctx context.Context) bool {
	return s.db.Setting(ctx, onlineSubsKey) != "off"
}

func (s *Server) onlineSubsLang(ctx context.Context) string {
	if l := s.db.Setting(ctx, onlineSubsLangKey); len(l) == 3 {
		return l
	}
	return "eng"
}

var lang2 = map[string]string{"eng": "en", "fre": "fr", "ger": "de", "spa": "es", "ita": "it", "dut": "nl", "por": "pt", "pol": "pl", "swe": "sv", "dan": "da", "nor": "no", "fin": "fi"}

// sameLang compares a track's language tag ("en", "eng", "en-GB", "English")
// with an ISO 639-2 code. Unlabelled tracks count as a match: an old file's
// single unnamed subtitle track is nearly always the one that is wanted.
func sameLang(tag, lang string) bool {
	t := strings.ToLower(strings.TrimSpace(tag))
	if t == "" || t == "und" {
		return true
	}
	t = strings.Split(t, "-")[0]
	if t == lang || t == lang2[lang] {
		return true
	}
	alt := map[string]string{"fra": "fre", "deu": "ger", "nld": "dut", "english": "eng", "french": "fre", "german": "ger", "spanish": "spa"}
	return alt[t] == lang
}

// hasText reports whether the video already has full text subtitles in lang.
func hasText(tracks []subs.Track, lang string) bool {
	for _, t := range tracks {
		if t.Format == "image" || t.Forced || strings.Contains(strings.ToLower(t.Title), "forced") {
			continue
		}
		if sameLang(t.Language, lang) {
			return true
		}
	}
	return false
}

type onlineRow struct {
	State    string
	SubID    string
	Release  string
	Matched  string
	Rejected []string
	Size     int64
	Note     string
	Checked  int64
}

func (s *Server) onlineRow(ctx context.Context, fileID int64, lang string) (*onlineRow, bool) {
	var r onlineRow
	var rej string
	err := s.db.R.QueryRowContext(ctx, `SELECT state, COALESCE(sub_id, ''), COALESCE(release, ''), COALESCE(matched, ''), rejected, size, COALESCE(note, ''), checked_at
		FROM online_subs WHERE file_id = ? AND lang = ?`, fileID, lang).Scan(&r.State, &r.SubID, &r.Release, &r.Matched, &rej, &r.Size, &r.Note, &r.Checked)
	if err != nil {
		return nil, false
	}
	json.Unmarshal([]byte(rej), &r.Rejected)
	return &r, true
}

func (s *Server) saveOnlineRow(fileID int64, lang string, r *onlineRow) {
	rej, _ := json.Marshal(r.Rejected)
	if r.Rejected == nil {
		rej = []byte("[]")
	}
	_, err := s.db.W.ExecContext(s.baseCtx, `INSERT INTO online_subs(file_id, lang, state, sub_id, release, matched, rejected, size, note, checked_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(file_id, lang) DO UPDATE SET state = excluded.state, sub_id = excluded.sub_id, release = excluded.release,
			matched = excluded.matched, rejected = excluded.rejected, size = excluded.size, note = excluded.note, checked_at = excluded.checked_at`,
		fileID, lang, r.State, r.SubID, r.Release, r.Matched, string(rej), r.Size, r.Note, db.Now())
	if err != nil {
		log.Printf("online subtitles: couldn't save state for file %d: %v", fileID, err)
	}
}

// onlineStatus is what the players are told about the search.
type onlineStatus struct {
	// State is off, searching, found, none, or "" (not needed).
	State   string `json:"state"`
	Matched string `json:"matched,omitempty"` // hash = made for this exact copy
	Release string `json:"release,omitempty"`
	Note    string `json:"note,omitempty"`
}

// onlineTrack returns the downloaded track for a file, if there is one.
func (s *Server) onlineTrack(ctx context.Context, fileID int64) (*subs.Track, *onlineRow) {
	lang := s.onlineSubsLang(ctx)
	r, ok := s.onlineRow(ctx, fileID, lang)
	if !ok || r.State != "found" {
		return nil, r
	}
	if _, err := os.Stat(s.online.file(fileID, lang)); err != nil {
		return nil, nil
	}
	title := "Downloaded"
	if r.Matched == "hash" {
		title = "Downloaded · exact match"
	}
	return &subs.Track{ID: onlineTrackID, Codec: "srt", Format: "text", Language: lang, Title: title, External: true, Online: true, FileName: r.Release}, r
}

// withOnline adds the downloaded track to a file's own tracks, starting a
// search if the file needs one, and says where that search stands.
func (s *Server) withOnline(ctx context.Context, fileID int64, tracks []subs.Track) ([]subs.Track, onlineStatus) {
	lang := s.onlineSubsLang(ctx)
	if t, r := s.onlineTrack(ctx, fileID); t != nil {
		return append(tracks, *t), onlineStatus{State: "found", Matched: r.Matched, Release: r.Release}
	}
	if hasText(tracks, lang) {
		return tracks, onlineStatus{}
	}
	if !s.onlineSubsEnabled(ctx) {
		return tracks, onlineStatus{State: "off"}
	}
	if s.startOnlineSearch(fileID, false) {
		return tracks, onlineStatus{State: "searching"}
	}
	st := onlineStatus{State: "none"}
	if r, ok := s.onlineRow(ctx, fileID, lang); ok {
		st.Note = r.Note
	}
	return tracks, st
}

// startOnlineSearch begins a background search unless one already ran
// recently (force skips that check). It reports whether one is running.
func (s *Server) startOnlineSearch(fileID int64, force bool) bool {
	o := s.online
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.inflight[fileID]; ok {
		return true
	}
	if time.Now().Before(o.pausedUntil) {
		return false
	}
	lang := s.onlineSubsLang(s.baseCtx)
	if r, ok := s.onlineRow(s.baseCtx, fileID, lang); ok && !force {
		age := time.Duration(db.Now()-r.Checked) * time.Second
		switch r.State {
		case "found":
			if _, err := os.Stat(o.file(fileID, lang)); err == nil {
				return false
			}
		case "none":
			if age < 14*24*time.Hour {
				return false
			}
		default:
			if age < 15*time.Minute {
				return false
			}
		}
	}
	done := make(chan struct{})
	o.inflight[fileID] = done
	go func() {
		defer func() {
			o.mu.Lock()
			delete(o.inflight, fileID)
			o.mu.Unlock()
			close(done)
		}()
		o.gate <- struct{}{}
		defer func() { <-o.gate }()
		ctx, cancel := context.WithTimeout(s.baseCtx, 3*time.Minute)
		defer cancel()
		s.searchOnline(ctx, fileID, lang)
	}()
	return true
}

// waitOnline waits for a running search, up to d.
func (s *Server) waitOnline(ctx context.Context, fileID int64, d time.Duration) {
	s.online.mu.Lock()
	ch := s.online.inflight[fileID]
	s.online.mu.Unlock()
	if ch == nil {
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ch:
	case <-t.C:
	case <-ctx.Done():
	}
}

// onlineQuery gathers what is known about a file for the search.
func (s *Server) onlineQuery(ctx context.Context, fileID int64, lang string) (subfetch.Query, string, error) {
	q := subfetch.Query{Lang: lang}
	var path, kind, title, imdb string
	var year, season, episode sql.NullInt64
	var parent sql.NullInt64
	err := s.db.R.QueryRowContext(ctx, `SELECT f.path, f.size, COALESCE(f.duration, 0), i.kind, i.title, i.year, COALESCE(i.imdb_id, ''),
			i.season_number, i.episode_number, i.parent_id
		FROM media_files f JOIN items i ON i.id = f.item_id WHERE f.id = ?`, fileID).
		Scan(&path, &q.Size, &q.Duration, &kind, &title, &year, &imdb, &season, &episode, &parent)
	if err != nil {
		return q, "", err
	}
	q.FileName = filepath.Base(path)
	q.Title, q.Year, q.IMDB = title, int(year.Int64), imdbDigits(imdb)
	if kind == "episode" {
		q.IsShow, q.Season, q.Episode = true, int(season.Int64), int(episode.Int64)
		q.IMDB = ""
		// episode → season → show
		id := parent.Int64
		for i := 0; i < 2 && id > 0; i++ {
			var k, t, im string
			var p sql.NullInt64
			var y sql.NullInt64
			if s.db.R.QueryRowContext(ctx, `SELECT kind, title, COALESCE(imdb_id, ''), parent_id, year FROM items WHERE id = ?`, id).Scan(&k, &t, &im, &p, &y) != nil {
				break
			}
			if k == "show" {
				q.Title, q.IMDB, q.Year = t, imdbDigits(im), int(y.Int64)
				break
			}
			id = p.Int64
		}
	}
	return q, path, nil
}

func imdbDigits(id string) string {
	id = strings.TrimPrefix(strings.TrimSpace(id), "tt")
	if _, err := strconv.Atoi(id); err != nil {
		return ""
	}
	return id
}

// frameRate asks ffprobe for the video's frame rate; 0 if it can't tell. It
// reads only the file's head, which a file being played has cached.
func (s *Server) frameRate(ctx context.Context, path string) float64 {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, s.cfg.FFprobe, "-v", "error", "-probesize", "5M", "-analyzeduration", "5M", "-select_streams", "v:0",
		"-show_entries", "stream=avg_frame_rate,r_frame_rate", "-of", "default=nw=1:nk=1", path).Output()
	if err != nil {
		return 0
	}
	for _, ln := range strings.Fields(string(out)) {
		p := strings.Split(ln, "/")
		if len(p) != 2 {
			continue
		}
		a, _ := strconv.ParseFloat(p[0], 64)
		b, _ := strconv.ParseFloat(p[1], 64)
		if a > 0 && b > 0 && a/b > 10 && a/b < 121 {
			return a / b
		}
	}
	return 0
}

func (s *Server) searchOnline(ctx context.Context, fileID int64, lang string) {
	o := s.online
	prev, _ := s.onlineRow(ctx, fileID, lang)
	row := &onlineRow{State: "none"}
	if prev != nil {
		row.Rejected = prev.Rejected
	}
	q, path, err := s.onlineQuery(ctx, fileID, lang)
	if err != nil {
		return
	}
	row.Size = q.Size
	if prev != nil && prev.Size != 0 && prev.Size != q.Size {
		row.Rejected = nil // a different copy now: old verdicts don't apply
	}
	if h, size, err := subfetch.Hash(path); err == nil {
		q.Hash, q.Size = h, size
	} else {
		log.Printf("online subtitles: no fingerprint for file %d: %v", fileID, err)
	}
	q.FPS = s.frameRate(ctx, path)

	cands, err := o.client.Search(ctx, q)
	if err != nil {
		s.onlineFailed(fileID, lang, row, err)
		return
	}
	rejected := map[string]bool{}
	for _, id := range row.Rejected {
		rejected[id] = true
	}
	tried := 0
	var lastErr error
	for _, c := range cands {
		if rejected[c.ID] {
			continue
		}
		if tried++; tried > 4 {
			break
		}
		body, err := o.client.Download(ctx, c)
		if err == nil {
			err = subfetch.Fits(body, q.Duration)
		}
		if err != nil {
			lastErr = err
			log.Printf("online subtitles: file %d: passed over %q (%s): %v", fileID, c.Release, c.ID, err)
			if errors.Is(err, subfetch.ErrQuota) || ctx.Err() != nil {
				s.onlineFailed(fileID, lang, row, err)
				return
			}
			continue
		}
		if err := os.MkdirAll(o.dir, 0o755); err != nil {
			s.onlineFailed(fileID, lang, row, err)
			return
		}
		tmp := o.file(fileID, lang) + ".tmp"
		if err := os.WriteFile(tmp, body, 0o644); err != nil {
			s.onlineFailed(fileID, lang, row, err)
			return
		}
		if err := os.Rename(tmp, o.file(fileID, lang)); err != nil {
			s.onlineFailed(fileID, lang, row, err)
			return
		}
		row.State, row.SubID, row.Release, row.Matched = "found", c.ID, c.Release, "name"
		if row.Release == "" {
			row.Release = c.FileName
		}
		if c.HashMatch {
			row.Matched = "hash"
		}
		s.saveOnlineRow(fileID, lang, row)
		s.comp.forgetSubs(fileID)
		log.Printf("online subtitles: file %d (%s): saved %q, matched by %s, score %.0f, %d on offer", fileID, q.FileName, row.Release, row.Matched, c.Score, len(cands))
		return
	}
	os.Remove(o.file(fileID, lang))
	if len(cands) > 0 && tried == 0 {
		// Every one was turned down: start again from the top next time.
		row.Rejected = nil
		row.Note = "No other subtitles to try"
	} else if lastErr != nil {
		row.Note = "The subtitles on offer didn't fit this video"
	}
	s.saveOnlineRow(fileID, lang, row)
	log.Printf("online subtitles: file %d (%s): nothing suitable (%d on offer)", fileID, q.FileName, len(cands))
}

func (s *Server) onlineFailed(fileID int64, lang string, row *onlineRow, err error) {
	row.State, row.Note = "error", "The subtitle service couldn't be reached"
	if errors.Is(err, subfetch.ErrQuota) {
		row.Note = "Today's subtitle downloads are used up; it will try again tomorrow"
		s.online.mu.Lock()
		s.online.pausedUntil = time.Now().Add(6 * time.Hour)
		s.online.mu.Unlock()
	}
	os.Remove(s.online.file(fileID, lang))
	s.saveOnlineRow(fileID, lang, row)
	log.Printf("online subtitles: file %d: %v", fileID, err)
}

// queueOnlineSubs starts the search as playback begins, so the subtitles
// are there by the time anyone opens the menu.
func (s *Server) queueOnlineSubs(fileID int64) {
	go func() {
		ctx, cancel := context.WithTimeout(s.baseCtx, 10*time.Second)
		defer cancel()
		if !s.onlineSubsEnabled(ctx) {
			return
		}
		var path, streams string
		if s.db.R.QueryRowContext(ctx, `SELECT path, COALESCE(streams, 'null') FROM media_files WHERE id = ?`, fileID).Scan(&path, &streams) != nil || streams == "null" {
			return
		}
		var ss []probe.Stream
		json.Unmarshal([]byte(streams), &ss)
		tracks, _ := embeddedTracks(ss)
		tracks = append(tracks, subs.Sidecars(path)...)
		if !hasText(tracks, s.onlineSubsLang(ctx)) {
			s.startOnlineSearch(fileID, false)
		}
	}()
}

// handleOnlineSubs is the "Find subtitles" / "Try another" button: it turns
// down the current download, if any, and looks again.
func (s *Server) handleOnlineSubs(w http.ResponseWriter, r *http.Request) {
	fileID, path, ss, ok := s.subFile(w, r)
	if !ok {
		return
	}
	lang := s.onlineSubsLang(r.Context())
	if row, ok := s.onlineRow(r.Context(), fileID, lang); ok && row.State == "found" && row.SubID != "" {
		row.Rejected = append(row.Rejected, row.SubID)
		row.State, row.SubID, row.Note = "none", "", ""
		os.Remove(s.online.file(fileID, lang))
		s.saveOnlineRow(fileID, lang, row)
		s.comp.forgetSubs(fileID)
	}
	s.startOnlineSearch(fileID, true)
	s.waitOnline(r.Context(), fileID, 40*time.Second)

	tracks, _ := embeddedTracks(ss)
	tracks = append(tracks, subs.Sidecars(path)...)
	st := onlineStatus{State: "none"}
	if t, row := s.onlineTrack(r.Context(), fileID); t != nil {
		tracks = append(tracks, *t)
		st = onlineStatus{State: "found", Matched: row.Matched, Release: row.Release}
	} else if row != nil {
		st.Note = row.Note
	}
	s.online.mu.Lock()
	if _, busy := s.online.inflight[fileID]; busy {
		st = onlineStatus{State: "searching"}
	}
	s.online.mu.Unlock()
	if tracks == nil {
		tracks = []subs.Track{}
	}
	writeJSON(w, map[string]any{"tracks": tracks, "ffmpeg": s.subs.Available(), "online": st})
}

// onlineSRT returns the downloaded subtitles for a file.
func (s *Server) onlineSRT(ctx context.Context, fileID int64) ([]byte, error) {
	return os.ReadFile(s.online.file(fileID, s.onlineSubsLang(ctx)))
}
