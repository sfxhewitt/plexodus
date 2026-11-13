package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
