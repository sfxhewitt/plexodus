package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sfxhewitt/plexodus/internal/config"
)

func readStatus(t *testing.T, s *Server) updateStatus {
	t.Helper()
	var st updateStatus
	b, err := os.ReadFile(filepath.Join(s.cfg.DataDir, updateStatusFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &st); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestAutoUpdateStatus(t *testing.T) {
	ctx := context.Background()
	s, admin := testServer(t)
	s.cfg = &config.Config{DataDir: t.TempDir(), Version: "main-test"}
	s.nowPlaying = newNowPlaying()

	if err := s.writeUpdateStatus(ctx); err != nil {
		t.Fatal(err)
	}
	if st := readStatus(t, s); !st.AutoUpdate || st.Streams != 0 || st.Version != "main-test" || st.At == 0 {
		t.Fatalf("idle: %+v", st)
	}

	// Someone starts watching: the updater must see the stream.
	s.nowPlaying.progress(httptest.NewRequest("POST", "/", nil), admin.ID, 10, 60, 600, "playing")
	s.writeUpdateStatus(ctx)
	if st := readStatus(t, s); st.Streams != 1 {
		t.Fatalf("watching: %+v", st)
	}
	// Paused still counts; stopped doesn't.
	s.nowPlaying.progress(httptest.NewRequest("POST", "/", nil), admin.ID, 10, 60, 600, "paused")
	s.writeUpdateStatus(ctx)
	if st := readStatus(t, s); st.Streams != 1 {
		t.Fatalf("paused: %+v", st)
	}
	s.nowPlaying.progress(httptest.NewRequest("POST", "/", nil), admin.ID, 10, 60, 600, "stopped")
	s.writeUpdateStatus(ctx)
	if st := readStatus(t, s); st.Streams != 0 {
		t.Fatalf("stopped: %+v", st)
	}

	// Switching it off in Settings is written straight away.
	s.setAutoUpdate(ctx, false)
	if st := readStatus(t, s); st.AutoUpdate {
		t.Fatalf("off: %+v", st)
	}
	s.setAutoUpdate(ctx, true)
	if st := readStatus(t, s); !st.AutoUpdate {
		t.Fatalf("on: %+v", st)
	}

	// A stream left "in flight" (a player that kept its connection open,
	// or a TV switched off mid-film) stops counting once it goes quiet.
	done := s.nowPlaying.direct(httptest.NewRequest("GET", "/", nil), admin.ID, 10, 5)
	s.nowPlaying.progress(httptest.NewRequest("POST", "/", nil), admin.ID, 10, 60, 600, "playing")
	if n := s.openStreams(); n != 1 {
		t.Fatalf("playing in flight: %d", n)
	}
	s.nowPlaying.mu.Lock()
	for _, st := range s.nowPlaying.m {
		st.lastProgress = st.lastProgress.Add(-5 * time.Minute)
		st.lastSeen = st.lastSeen.Add(-5 * time.Minute)
	}
	s.nowPlaying.mu.Unlock()
	if n := s.openStreams(); n != 0 {
		t.Fatalf("stale in-flight stream still blocks updates: %d", n)
	}
	// Paused counts for longer.
	s.nowPlaying.progress(httptest.NewRequest("POST", "/", nil), admin.ID, 10, 60, 600, "paused")
	s.nowPlaying.mu.Lock()
	for _, st := range s.nowPlaying.m {
		st.lastProgress = st.lastProgress.Add(-10 * time.Minute)
	}
	s.nowPlaying.mu.Unlock()
	if n := s.openStreams(); n != 1 {
		t.Fatalf("paused 10 min: %d", n)
	}
	done(0)
	s.nowPlaying.progress(httptest.NewRequest("POST", "/", nil), admin.ID, 10, 60, 600, "stopped")

	// The environment wins.
	s.cfg.AutoUpdate = "off"
	if s.autoUpdateEnabled(ctx) {
		t.Fatal("PLEXODUS_AUTO_UPDATE=off ignored")
	}
	s.cfg.AutoUpdate = ""

	// Settings: not installed until the updater has written a result.
	if got := s.autoUpdateSettings(ctx); got["installed"] != false {
		t.Fatalf("settings before install: %v", got)
	}
	os.WriteFile(filepath.Join(s.cfg.DataDir, updateResultFile), []byte(`{"state":"up-to-date","message":"Plexodus is up to date.","checkedAt":1,"updatedAt":0,"version":"main-test"}`), 0o644)
	got := s.autoUpdateSettings(ctx)
	if got["installed"] != true || got["last"].(updateResult).State != "up-to-date" {
		t.Fatalf("settings after install: %v", got)
	}

	// Update now.
	if err := s.requestUpdateNow(); err != nil {
		t.Fatal(err)
	}
	if got := s.autoUpdateSettings(ctx); got["pending"] != true {
		t.Fatalf("update now not pending: %v", got)
	}
}
