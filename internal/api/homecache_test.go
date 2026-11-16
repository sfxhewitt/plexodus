package api

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/config"
)

// A client that stops waiting must not throw the Home build away: it
// finishes in the background and the next visit gets it straight away.
func TestHomeBuildSurvivesClientGivingUp(t *testing.T) {
	s, u := testServer(t)
	ctx, cancel := context.WithCancel(auth.WithUser(context.Background(), u))
	cancel() // the client has already gone
	r := httptest.NewRequest("GET", "/api/home", nil).WithContext(ctx)
	s.handleHome(httptest.NewRecorder(), r)

	deadline := time.Now().Add(5 * time.Second)
	for {
		s.homes.mu.Lock()
		e := s.homes.m[homeKey(r)]
		done := e != nil && e.body != nil && e.building == nil
		s.homes.mu.Unlock()
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("home was never built after the client gave up")
		}
		time.Sleep(10 * time.Millisecond)
	}

	var h struct {
		Rows []Row `json:"rows"`
	}
	if code := call(t, s, u, s.handleHome, "GET", "/api/home", "", nil, &h); code != 200 {
		t.Fatalf("home = %d", code)
	}

	// A change marks it dirty; the next visit waits for the rebuilt one.
	s.homeChanged(u.ID)
	s.homes.mu.Lock()
	dirty := s.homes.m[homeKey(r)].dirty
	s.homes.mu.Unlock()
	if !dirty {
		t.Fatal("homeChanged didn't mark Home out of date")
	}
	if code := call(t, s, u, s.handleHome, "GET", "/api/home", "", nil, &h); code != 200 {
		t.Fatalf("home after a change = %d", code)
	}
}

// After a restart the first visit gets the Home saved by the last run at once,
// instead of waiting for a cold build.
func TestHomeSurvivesRestart(t *testing.T) {
	s, u := testServer(t)
	s.cfg = &config.Config{DataDir: t.TempDir()}
	var h struct {
		Rows []Row `json:"rows"`
	}
	if code := call(t, s, u, s.handleHome, "GET", "/api/home", "", nil, &h); code != 200 {
		t.Fatalf("home = %d", code)
	}
	r := httptest.NewRequest("GET", "/api/home", nil).WithContext(auth.WithUser(context.Background(), u))
	key := homeKey(r)
	f := s.homeFile(key)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(f); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("home was never saved")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// A fresh server (as after an update) finds it without building.
	s2 := &Server{db: s.db, auth: s.auth, baseCtx: s.baseCtx, cfg: s.cfg}
	body, at := s2.loadSavedHome(key)
	if len(body) == 0 || at.IsZero() {
		t.Fatal("saved home not loaded")
	}
	rec := httptest.NewRecorder()
	s2.handleHome(rec, r)
	if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), body) {
		t.Fatalf("restarted home = %d, want the saved one", rec.Code)
	}
}

// A change that lands while Home is being built (the TV player's last
// progress report racing the Home refetch when it closes) must leave Home
// out of date, so the next visit rebuilds it: a finished film used to stay
// in Up Next with its old progress.
func TestHomeChangeDuringBuildIsKept(t *testing.T) {
	s, u := testServer(t)
	r := httptest.NewRequest("GET", "/api/home", nil).WithContext(auth.WithUser(context.Background(), u))
	key := homeKey(r)
	e := &homeEntry{building: make(chan struct{})}
	s.homes.mu.Lock()
	s.homes.m = map[string]*homeEntry{key: e}
	gen := e.gen
	s.homes.mu.Unlock()

	s.homeChanged(u.ID) // arrives after the build has read the database
	s.buildHomeFor(key, e, r, gen)

	s.homes.mu.Lock()
	dirty, built := e.dirty, e.body != nil
	s.homes.mu.Unlock()
	if !built {
		t.Fatal("home wasn't built")
	}
	if !dirty {
		t.Fatal("a change made during the build was forgotten; Home would stay stale")
	}

	// A build with nothing changing meanwhile leaves it fresh.
	s.homes.mu.Lock()
	e.building = make(chan struct{})
	gen = e.gen
	s.homes.mu.Unlock()
	s.buildHomeFor(key, e, r, gen)
	s.homes.mu.Lock()
	dirty = e.dirty
	s.homes.mu.Unlock()
	if dirty {
		t.Fatal("home still out of date after a clean rebuild")
	}
}
