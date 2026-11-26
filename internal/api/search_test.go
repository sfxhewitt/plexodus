package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sfxhewitt/plexodus/internal/ask"
	"github.com/sfxhewitt/plexodus/internal/db"
)

type searchResult struct{ Movies, Shows, Episodes []Card }

// The film whose title is what was typed comes first, ahead of films that
// merely mention the words, and episodes match on their own title only.
func TestSearchTitleFirst(t *testing.T) {
	s, u := testServer(t)
	now := db.Now()
	if _, err := s.db.W.Exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, overview, parent_id, added_at, updated_at) VALUES
		(40, 1, 'movie', 'm40', 'It''s About Time Again', 'its about time again', 'About time. About time. A film about time.', NULL, ?1, ?1),
		(41, 1, 'movie', 'm41', 'About Time', 'about time', 'A young man can travel.', NULL, ?1, ?1),
		(42, 1, 'movie', 'm42', 'Clockwork', 'clockwork', 'A story about a time machine.', NULL, ?1, ?1),
		(50, 1, 'show', 's50', 'Time Team', 'time team', 'All about digging.', NULL, ?1, ?1),
		(51, 1, 'season', 'x51', 'Season 1', 's', NULL, 50, ?1, ?1),
		(52, 1, 'episode', 'e52', 'About Time', 'e', 'x', 51, ?1, ?1),
		(53, 1, 'episode', 'e53', 'Pilot', 'e', 'Mostly about time.', 51, ?1, ?1)`, now); err != nil {
		t.Fatal(err)
	}
	var res searchResult
	call(t, s, u, s.handleSearch, "GET", "/api/search?q=About+time", "", nil, &res)
	if got := strings.Join(titles(res.Movies), ","); got != "About Time,It's About Time Again,Clockwork" {
		t.Errorf("movies = %s", got)
	}
	if got := strings.Join(titles(res.Shows), ","); got != "Time Team" {
		t.Errorf("shows = %s", got)
	}
	if got := strings.Join(titles(res.Episodes), ","); got != "About Time" {
		t.Errorf("episodes = %s", got)
	}
	// Still found as you type.
	call(t, s, u, s.handleSearch, "GET", "/api/search?q=abou+ti", "", nil, &res)
	if len(res.Movies) != 3 || res.Movies[0].Title != "About Time" {
		t.Errorf("prefix search = %v", titles(res.Movies))
	}
	// Renamed and deleted titles follow.
	s.db.W.Exec(`UPDATE items SET title = 'Before Time' WHERE id = 41`)
	s.db.W.Exec(`DELETE FROM items WHERE id = 42`)
	call(t, s, u, s.handleSearch, "GET", "/api/search?q=About+time", "", nil, &res)
	if got := strings.Join(titles(res.Movies), ","); got != "It's About Time Again" {
		t.Errorf("after rename and delete, movies = %s", got)
	}
}

// A library from before the films-and-shows index gets it built, and search
// works both before and after.
func TestSearchIndexBuiltForOldLibrary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	d, err := db.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Make it look like a library from before: titles the index never saw.
	for _, q := range []string{
		`INSERT INTO libraries(id, name, kind, paths, created_at) VALUES (1, 'Movies', 'movies', '[]', 0)`,
		`DROP TRIGGER titles_fts_ai`,
		`INSERT INTO items(id, library_id, kind, guid, title, sort_title, overview, added_at, updated_at) VALUES
			(41, 1, 'movie', 'm41', 'About Time', 'about time', 'A young man can travel.', 0, 0)`,
		`DELETE FROM settings WHERE key = 'titles_fts_v1'`,
	} {
		if _, err := d.W.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	d.Close()

	s, u := testServerAt(t, path)
	if s.db.TitleFTS() != "items_fts" {
		t.Fatalf("index in use before the build = %s", s.db.TitleFTS())
	}
	var res searchResult
	call(t, s, u, s.handleSearch, "GET", "/api/search?q=About+time", "", nil, &res)
	if got := strings.Join(titles(res.Movies), ","); got != "About Time" {
		t.Errorf("before the build, movies = %s", got)
	}
	if err := s.db.BuildTitleIndex(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s.db.TitleFTS() != "titles_fts" {
		t.Fatalf("index in use after the build = %s", s.db.TitleFTS())
	}
	call(t, s, u, s.handleSearch, "GET", "/api/search?q=About+time", "", nil, &res)
	if got := strings.Join(titles(res.Movies), ","); got != "About Time" {
		t.Errorf("after the build, movies = %s", got)
	}
}

// Pressing Enter on a title that is in the library answers with that title
// without asking Claude; and when Claude is slow the search gives up on it
// and answers with the built-in parser.
func TestAskTitleSkipsClaudeAndSlowClaudeFallsBack(t *testing.T) {
	s, askq := askServer(t)
	var calls atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.Copy(io.Discard, r.Body)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	s.claude = askNewClaude("sk-test", srv.URL)

	res := askq("reservoir dogs")
	if res.Engine != "title" || strings.Join(titles(res.Picks), ",") != "Reservoir Dogs" || calls.Load() != 0 {
		t.Fatalf("engine = %s, picks = %v, Claude calls = %d", res.Engine, titles(res.Picks), calls.Load())
	}

	old := ask.TranslateTimeout
	ask.TranslateTimeout = 300 * time.Millisecond
	t.Cleanup(func() { ask.TranslateTimeout = old })
	start := time.Now()
	res = askq("90s heist films under two hours I haven't seen")
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("slow Claude held the search for %s", took)
	}
	if res.Engine != "basic" || !strings.Contains(res.Notice, "took too long") || len(res.Matches) != 1 {
		t.Errorf("engine = %s, notice = %q, matches = %v", res.Engine, res.Notice, titles(res.Matches))
	}
}

// The search pages ask for titles and episodes separately.
func TestSearchParts(t *testing.T) {
	s, u := testServer(t)
	if _, err := s.db.W.Exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, parent_id, added_at, updated_at) VALUES
		(50, 1, 'show', 's50', 'Heat Wave', 'heat wave', NULL, 0, 0),
		(51, 1, 'season', 'x51', 'Season 1', 's', 50, 0, 0),
		(52, 1, 'episode', 'e52', 'Heat', 'e', 51, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	var res searchResult
	call(t, s, u, s.handleSearch, "GET", "/api/search?q=heat&part=titles", "", nil, &res)
	if len(res.Movies) != 1 || len(res.Shows) != 1 || len(res.Episodes) != 0 {
		t.Errorf("titles part = %v %v %v", titles(res.Movies), titles(res.Shows), titles(res.Episodes))
	}
	res = searchResult{}
	call(t, s, u, s.handleSearch, "GET", "/api/search?q=heat&part=episodes", "", nil, &res)
	if len(res.Movies) != 0 || len(res.Shows) != 0 || len(res.Episodes) != 1 {
		t.Errorf("episodes part = %v %v %v", titles(res.Movies), titles(res.Shows), titles(res.Episodes))
	}
	res = searchResult{}
	call(t, s, u, s.handleSearch, "GET", "/api/search?q=heat", "", nil, &res)
	if len(res.Movies) != 1 || len(res.Shows) != 1 || len(res.Episodes) != 1 {
		t.Errorf("whole search = %v %v %v", titles(res.Movies), titles(res.Shows), titles(res.Episodes))
	}
}
