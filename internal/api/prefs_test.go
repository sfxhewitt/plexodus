package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/db"
)

// testServer is a Server over a fresh database with one admin user, two
// libraries (1 Movies, 2 Anime) and a recently added film in each.
func testServer(t *testing.T) (*Server, auth.User) {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	ctx := context.Background()
	s := &Server{db: d, auth: auth.New(d), baseCtx: ctx}
	u, err := s.auth.CreateUser(ctx, "daniel", "password123", true)
	if err != nil {
		t.Fatal(err)
	}
	now := db.Now()
	for _, q := range []string{
		`INSERT INTO libraries(id, name, kind, paths, created_at) VALUES (1, 'Movies', 'movies', '[]', 0), (2, 'Anime', 'movies', '[]', 0)`,
		`INSERT INTO items(id, library_id, kind, guid, title, sort_title, poster, backdrop, overview, rating, added_at, updated_at) VALUES
			(10, 1, 'movie', 'm10', 'Heat', 'heat', '/p.jpg', '/b.jpg', 'x', 8, ?1, ?1),
			(20, 2, 'movie', 'm20', 'Akira', 'akira', '/p.jpg', '/b.jpg', 'x', 8, ?1, ?1)`,
	} {
		if _, err := d.W.Exec(q, now); err != nil {
			t.Fatal(err)
		}
	}
	return s, u
}

func call(t *testing.T, s *Server, u auth.User, h http.HandlerFunc, method, path, body string, pathVals map[string]string, out any) int {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for k, v := range pathVals {
		r.SetPathValue(k, v)
	}
	r = r.WithContext(auth.WithUser(r.Context(), u))
	w := httptest.NewRecorder()
	h(w, r)
	if out != nil && w.Code == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: %v: %s", method, path, err, w.Body.String())
		}
	}
	return w.Code
}

func TestLibraryPrefs(t *testing.T) {
	s, u := testServer(t)
	libs := func() map[int64]Library {
		var ls []Library
		call(t, s, u, s.handleLibraries, "GET", "/api/libraries", "", nil, &ls)
		m := map[int64]Library{}
		for _, l := range ls {
			m[l.ID] = l
		}
		return m
	}
	homeRows := func() []string {
		var h struct {
			Hero []Card `json:"hero"`
			Rows []Row  `json:"rows"`
		}
		call(t, s, u, s.handleHome, "GET", "/api/home", "", nil, &h)
		var titles []string
		for _, r := range h.Rows {
			titles = append(titles, r.Title)
		}
		for _, c := range h.Hero {
			titles = append(titles, "hero:"+c.Title)
		}
		return titles
	}

	if l := libs(); l[2].Hidden || l[2].HiddenHome {
		t.Fatalf("new libraries start visible: %+v", l[2])
	}
	if got := strings.Join(homeRows(), "|"); !strings.Contains(got, "Recently Added in Anime") {
		t.Fatalf("home should list Anime before hiding: %s", got)
	}

	// Hide Anime from the menu only.
	if c := call(t, s, u, s.handleLibraryPrefs, "PUT", "/api/me/libraries/2", `{"hidden":true}`, map[string]string{"id": "2"}, nil); c != 200 {
		t.Fatalf("PUT = %d", c)
	}
	if l := libs(); !l[2].Hidden || l[2].HiddenHome || l[1].Hidden {
		t.Fatalf("after hiding from menu: %+v %+v", l[1], l[2])
	}
	if got := strings.Join(homeRows(), "|"); !strings.Contains(got, "Recently Added in Anime") {
		t.Fatalf("hiding from the menu keeps Home rows: %s", got)
	}

	// Then from Home too; the menu choice is kept.
	call(t, s, u, s.handleLibraryPrefs, "PUT", "/api/me/libraries/2", `{"hiddenHome":true}`, map[string]string{"id": "2"}, nil)
	if l := libs(); !l[2].Hidden || !l[2].HiddenHome {
		t.Fatalf("after hiding from home: %+v", l[2])
	}
	got := strings.Join(homeRows(), "|")
	if strings.Contains(got, "Anime") || strings.Contains(got, "Akira") {
		t.Fatalf("Anime still on Home: %s", got)
	}
	if !strings.Contains(got, "Recently Added in Movies") {
		t.Fatalf("Movies should stay on Home: %s", got)
	}

	// And back.
	call(t, s, u, s.handleLibraryPrefs, "PUT", "/api/me/libraries/2", `{"hidden":false,"hiddenHome":false}`, map[string]string{"id": "2"}, nil)
	if l := libs(); l[2].Hidden || l[2].HiddenHome {
		t.Fatalf("after showing again: %+v", l[2])
	}

	if c := call(t, s, u, s.handleLibraryPrefs, "PUT", "/api/me/libraries/99", `{"hidden":true}`, map[string]string{"id": "99"}, nil); c != 404 {
		t.Fatalf("unknown library = %d, want 404", c)
	}
}
