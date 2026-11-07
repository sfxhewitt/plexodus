package api

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sfxhewitt/plexodus/internal/auth"
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
