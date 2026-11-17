package api

import (
	"context"
	"crypto/rand"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"

	"github.com/sfxhewitt/plexodus/internal/avsync"
)

// Lip sync with a phone: the TV loops the flash-and-beep clip and shows a
// QR code; the phone films the TV for ten seconds and uploads the video;
// the server measures how far the beeps land from the flashes; the TV picks
// the result up and moves its own sound by exactly that much. Nobody has to
// work out "earlier or later".
//
// Sessions live in memory for 20 minutes. The TV side needs a signed-in
// user; the phone side only needs the session's code, which exists only
// while a TV is showing it.

const (
	phoneSessionTTL = 20 * time.Minute
	maxPhoneVideo   = 600 << 20 // a 4K60 iPhone clip of 15 s is ~150 MB
	maxPhoneSession = 20
)

type phoneSession struct {
	Code   string         `json:"code"`
	State  string         `json:"state"` // waiting, uploading, analysing, done, failed
	Error  string         `json:"error,omitempty"`
	Result *avsync.Result `json:"result,omitempty"`
	// DeviceMs: the TV's own offset when the video was filmed, and NewDeviceMs
	// the one that cancels what was measured. AppliedMs: what the TV saved.
	DeviceMs    int  `json:"deviceMs"`
	NewDeviceMs int  `json:"newDeviceMs"`
	AppliedMs   *int `json:"appliedMs,omitempty"`
	Round       int  `json:"round"` // counts uploads, so the TV applies each once
	// Content: filming a film instead of the test clip (the player's
	// "Check with my phone"); then the file and where playback is.
	Content bool                  `json:"content"`
	Check   *avsync.ContentResult `json:"check,omitempty"`

	created time.Time
	tvMs    int // latest offset the TV reported
	fileID  int64
	fileMs  int     // the file's own saved fix, already in what's heard
	posSec  float64 // playback position the TV last reported
	posAt   time.Time
	busy    bool
}

type phoneSessions struct {
	mu sync.Mutex
	m  map[string]*phoneSession
}

func (p *phoneSessions) get(code string) *phoneSession {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sweep()
	return p.m[code]
}

func (p *phoneSessions) sweep() {
	for k, v := range p.m {
		if time.Since(v.created) > phoneSessionTTL {
			delete(p.m, k)
		}
	}
}

// snapshot copies a session for JSON while holding the lock.
func (p *phoneSessions) snapshot(s *phoneSession) phoneSession {
	p.mu.Lock()
	defer p.mu.Unlock()
	c := *s
	return c
}

func newCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(900000))
	return strconv.FormatInt(n.Int64()+100000, 10)
}

// POST /api/lipsync/sessions {deviceMs}: the TV starts a session.
func (s *Server) handleLipsyncStart(w http.ResponseWriter, r *http.Request) {
	var b struct {
		DeviceMs   int   `json:"deviceMs"`
		FileID     int64 `json:"fileId"`
		FileMs     int   `json:"fileMs"`
		PositionMs int64 `json:"positionMs"`
	}
	readJSON(r, &b)
	if b.FileID > 0 {
		var libID int64
		var itemID int64
		err := s.db.R.QueryRowContext(r.Context(), `SELECT item_id, library_id FROM media_files WHERE id = ?`, b.FileID).Scan(&itemID, &libID)
		if err != nil || !currentUser(r).CanSee(libID) || !s.itemVisible(r, itemID) {
			writeErr(w, 404, "file not found")
			return
		}
	}
	p := &s.phone
	p.mu.Lock()
	if p.m == nil {
		p.m = map[string]*phoneSession{}
	}
	p.sweep()
	if len(p.m) >= maxPhoneSession {
		p.mu.Unlock()
		writeErr(w, http.StatusTooManyRequests, "too many lip sync sessions; try again in a few minutes")
		return
	}
	code := newCode()
	for p.m[code] != nil {
		code = newCode()
	}
	sess := &phoneSession{Code: code, State: "waiting", created: time.Now(), tvMs: b.DeviceMs, DeviceMs: b.DeviceMs,
		Content: b.FileID > 0, fileID: b.FileID, fileMs: b.FileMs, posSec: float64(b.PositionMs) / 1000, posAt: time.Now()}
	p.m[code] = sess
	p.mu.Unlock()
	writeJSON(w, map[string]string{"code": code, "url": phoneURL(r, code)})
}

// phoneURL is the page the QR code opens, on the address the TV itself uses.
func phoneURL(r *http.Request, code string) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s/sync/%s", scheme, r.Host, code)
}

// GET /api/lipsync/sessions/{code}?deviceMs=N: the TV polls for a result.
func (s *Server) handleLipsyncPoll(w http.ResponseWriter, r *http.Request) {
	sess := s.phone.get(r.PathValue("code"))
	if sess == nil {
		writeErr(w, 404, "session expired")
		return
	}
	q := r.URL.Query()
	s.phone.mu.Lock()
	if v, err := strconv.Atoi(q.Get("deviceMs")); err == nil {
		sess.tvMs = v
	}
	if v, err := strconv.ParseInt(q.Get("positionMs"), 10, 64); err == nil && v > 0 {
		sess.posSec, sess.posAt = float64(v)/1000, time.Now()
	}
	s.phone.mu.Unlock()
	writeJSON(w, s.phone.snapshot(sess))
}

// POST /api/lipsync/sessions/{code}/applied {deviceMs}: the TV saved the fix.
func (s *Server) handleLipsyncApplied(w http.ResponseWriter, r *http.Request) {
	sess := s.phone.get(r.PathValue("code"))
	if sess == nil {
		writeErr(w, 404, "session expired")
		return
	}
	var b struct {
		DeviceMs int `json:"deviceMs"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	s.phone.mu.Lock()
	sess.AppliedMs = &b.DeviceMs
	sess.tvMs = b.DeviceMs
	s.phone.mu.Unlock()
	writeJSON(w, map[string]bool{"ok": true})
}

// GET /api/lipsync/sessions/{code}/qr.png: the QR code the TV shows.
func (s *Server) handleLipsyncQR(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	if s.phone.get(code) == nil {
		writeErr(w, 404, "session expired")
		return
	}
	png, err := qrcode.Encode(phoneURL(r, code), qrcode.Medium, 360)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png)
}

// GET /api/lipsync/phone/{code}: the phone page polls (public, by code).
func (s *Server) handleLipsyncPhoneState(w http.ResponseWriter, r *http.Request) {
	sess := s.phone.get(r.PathValue("code"))
	if sess == nil {
		writeErr(w, 404, "That code has expired. Start again from the TV: Settings, Lip sync, Fix with my phone.")
		return
	}
	writeJSON(w, s.phone.snapshot(sess))
}

// POST /api/lipsync/phone/{code}/video: the phone uploads its recording
// (the raw file as the body). It's measured in the background; the phone
// and TV both poll for the answer.
func (s *Server) handleLipsyncUpload(w http.ResponseWriter, r *http.Request) {
	sess := s.phone.get(r.PathValue("code"))
	if sess == nil {
		writeErr(w, 404, "That code has expired. Start again from the TV.")
		return
	}
	s.phone.mu.Lock()
	if sess.busy {
		s.phone.mu.Unlock()
		writeErr(w, http.StatusConflict, "Still working on the last video.")
		return
	}
	sess.busy = true
	sess.State, sess.Error, sess.Result, sess.Check = "uploading", "", nil, nil
	deviceMs := sess.tvMs
	// The recording ended before the upload began; playback has moved on
	// since the TV last said where it was.
	endSec := sess.posSec + time.Since(sess.posAt).Seconds()
	s.phone.mu.Unlock()

	fail := func(code int, msg string) {
		s.phone.mu.Lock()
		sess.busy = false
		sess.State, sess.Error = "failed", msg
		s.phone.mu.Unlock()
		writeErr(w, code, msg)
	}
	dir := filepath.Join(s.cfg.DataDir, "lipsync")
	os.MkdirAll(dir, 0o755)
	f, err := os.CreateTemp(dir, "rec-*")
	if err != nil {
		fail(500, "Couldn't save the video on the server.")
		return
	}
	path := f.Name()
	_, err = io.Copy(f, http.MaxBytesReader(w, r.Body, maxPhoneVideo))
	f.Close()
	if err != nil {
		os.Remove(path)
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			fail(413, "That video is too big. Ten seconds is plenty.")
		} else {
			fail(400, "The upload didn't finish. Try again.")
		}
		return
	}
	s.phone.mu.Lock()
	sess.State = "analysing"
	s.phone.mu.Unlock()
	go s.measurePhoneVideo(sess, path, deviceMs, endSec)
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]string{"state": "analysing"})
}

func (s *Server) measurePhoneVideo(sess *phoneSession, path string, deviceMs int, endSec float64) {
	ctx, cancel := context.WithTimeout(s.baseCtx, 3*time.Minute)
	defer cancel()
	start := time.Now()
	var res avsync.Result
	var check *avsync.ContentResult
	var err error
	if sess.Content {
		var src string
		if err = s.db.R.QueryRowContext(ctx, `SELECT path FROM media_files WHERE id = ?`, sess.fileID).Scan(&src); err == nil {
			var c avsync.ContentResult
			if c, err = avsync.MeasureContent(ctx, s.cfg.FFmpeg, path, src, endSec); err == nil {
				check = &c
				// What's heard already includes the file's own saved fix; only
				// the rest is the TV's, and that's what the device offset is for.
				res = avsync.Result{OffsetMs: c.OffsetMs - sess.fileMs, Pairs: 1}
			}
		}
	} else {
		res, err = avsync.Measure(ctx, s.cfg.FFmpeg, path)
	}
	os.Remove(path)
	s.phone.mu.Lock()
	defer s.phone.mu.Unlock()
	sess.busy = false
	if err != nil {
		log.Printf("lipsync phone: %s: %v", sess.Code, err)
		sess.State, sess.Error = "failed", phoneError(err)
		return
	}
	if check != nil {
		log.Printf("lipsync phone: %s: film %d at %.1fs: sound %+d ms vs picture (file fix %+d, sound match %.0f, picture %.2f, %v)",
			sess.Code, sess.fileID, check.At, check.OffsetMs, sess.fileMs, check.Sound, check.Picture, time.Since(start).Round(time.Millisecond))
	} else {
		log.Printf("lipsync phone: %s: sound %+d ms vs picture (%d flashes, spread %d ms, %v)", sess.Code, res.OffsetMs, res.Pairs, res.SpreadMs, time.Since(start).Round(time.Millisecond))
	}
	sess.Result = &res
	sess.Check = check
	sess.DeviceMs = deviceMs
	// Sound late by X: play it X earlier than it was when filmed.
	sess.NewDeviceMs = clampDevice(deviceMs - res.OffsetMs)
	sess.AppliedMs = nil
	sess.Round++
	sess.State = "done"
}

// clampDevice keeps within the app's per-device range (Prefs.MAX_DELAY).
func clampDevice(ms int) int {
	const max = 5000
	if ms > max {
		return max
	}
	if ms < -max {
		return -max
	}
	return ms
}

func phoneError(err error) string {
	switch {
	case errors.Is(err, avsync.ErrNoFlashes):
		return "I couldn't see the screen flashing. Keep the whole TV in the shot and film for about 10 seconds."
	case errors.Is(err, avsync.ErrNoBeeps):
		return "I couldn't hear the beeps. Turn the TV up a bit, keep the room quiet, and try again."
	case errors.Is(err, avsync.ErrShort):
		return "That video is too short. Film for about 10 seconds (five beeps)."
	case errors.Is(err, avsync.ErrNoSoundMatch):
		return "I couldn't match the sound you filmed to the film. Film it again with the sound clearly audible, within a minute or two of sending it."
	case errors.Is(err, avsync.ErrNoPictureMatch):
		return "I couldn't match the picture to the film. Keep the whole TV in the shot and hold the phone still; a scene with some movement or a cut works best."
	case errors.Is(err, avsync.ErrUnclear):
		return "The beeps and flashes didn't line up the same way each time. Hold the phone still, keep it quiet, and try again."
	}
	return "Couldn't read that video. Try again."
}

//go:embed assets/lipsync-phone.html
var phonePage []byte

// GET /sync and /sync/{code}: the phone page.
func (s *Server) handleLipsyncPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(phonePage)
}
