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

	created time.Time
	tvMs    int // latest offset the TV reported
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
		DeviceMs int `json:"deviceMs"`
	}
	readJSON(r, &b)
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
	sess := &phoneSession{Code: code, State: "waiting", created: time.Now(), tvMs: b.DeviceMs, DeviceMs: b.DeviceMs}
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
	if v, err := strconv.Atoi(r.URL.Query().Get("deviceMs")); err == nil {
		s.phone.mu.Lock()
		sess.tvMs = v
		s.phone.mu.Unlock()
	}
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
	sess.State, sess.Error, sess.Result = "uploading", "", nil
	deviceMs := sess.tvMs
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
	go s.measurePhoneVideo(sess, path, deviceMs)
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]string{"state": "analysing"})
}

func (s *Server) measurePhoneVideo(sess *phoneSession, path string, deviceMs int) {
	ctx, cancel := context.WithTimeout(s.baseCtx, 3*time.Minute)
	defer cancel()
	start := time.Now()
	res, err := avsync.Measure(ctx, s.cfg.FFmpeg, path)
	os.Remove(path)
	s.phone.mu.Lock()
	defer s.phone.mu.Unlock()
	sess.busy = false
	if err != nil {
		log.Printf("lipsync phone: %s: %v", sess.Code, err)
		sess.State, sess.Error = "failed", phoneError(err)
		return
	}
	log.Printf("lipsync phone: %s: sound %+d ms vs picture (%d flashes, spread %d ms, %v)", sess.Code, res.OffsetMs, res.Pairs, res.SpreadMs, time.Since(start).Round(time.Millisecond))
	sess.Result = &res
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
