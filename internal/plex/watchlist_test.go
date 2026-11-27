package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/db"
)

func TestCopyPlexWatchlist(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.W.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id, username, password_hash, is_admin, created_at) VALUES(1, 'admin', 'x', 1, 0)`)
	exec(`INSERT INTO libraries(id, name, kind, paths, created_at) VALUES(1, 'Movies', 'movies', '[]', 0), (2, 'TV', 'shows', '[]', 0)`)
	add := func(id, lib int, kind, title string, year int, tmdb int64) {
		exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, year, tmdb_id, meta_state, added_at, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, NULLIF(?, 0), 'matched', 0, 0)`, id, lib, kind, fmt.Sprint("g", id), title, strings.ToLower(title), year, tmdb)
	}
	add(1, 1, "movie", "Heat", 1995, 949)          // linked to Plex by rating key
	add(2, 1, "movie", "About Time", 2013, 122906) // not linked; found by TMDB id
	add(3, 2, "show", "Dexter", 2006, 1405)        // linked show
	add(4, 1, "movie", "The Goonies", 1985, 0)     // no ids anywhere; found by title and year
	add(5, 1, "movie", "Splash", 1984, 2619)       // found by file path
	add(6, 2, "show", "Heat", 1995, 0)             // same name, wrong kind: must not match
	add(7, 1, "movie", "Already Here", 2020, 7)    // on the Plexodus watchlist before the copy
	exec(`INSERT INTO plex_links(item_id, rating_key) VALUES(1, '301'), (3, '401')`)
	exec(`INSERT INTO media_files(id, item_id, library_id, path, size, mtime) VALUES(1, 5, 1, '/media/Splash (1984)/Splash.mkv', 1, 1)`)
	exec(`INSERT INTO watchlist(user_id, item_id, added_at) VALUES(1, 7, 5)`)

	type entry struct {
		key, typ, title string
		year            int
	}
	list := []entry{
		{"heat", "movie", "Heat", 1995},
		{"abouttime", "movie", "About Time", 2013},
		{"dexter", "show", "Dexter", 2006},
		{"goonies", "movie", "The Goonies", 1985},
		{"splash", "movie", "Splash", 1984},
		{"dune3", "movie", "Dune: Part Three", 2026}, // not in the library
		{"already", "movie", "Already Here", 2020},
	}
	local := map[string][]any{
		"plex://movie/heat":  {map[string]any{"ratingKey": "301", "type": "movie", "title": "Heat"}},
		"plex://show/dexter": {map[string]any{"ratingKey": "401", "type": "show", "title": "Dexter"}},
		"plex://movie/splash": {map[string]any{"ratingKey": "305", "type": "movie", "title": "Splash",
			"Media": []any{map[string]any{"Part": []any{map[string]any{"file": "/media/Splash (1984)/Splash.mkv"}}}}}},
	}
	ids := map[string][]any{
		"abouttime": {map[string]any{"id": "imdb://tt2194499"}, map[string]any{"id": "tmdb://122906"}},
		"dune3":     {map[string]any{"id": "tmdb://999999"}},
		"already":   {map[string]any{"id": "tmdb://7"}},
	}
	var localReqs, metaReqs int
	token := "tok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != token {
			w.WriteHeader(401)
			return
		}
		var md []any
		total := 0
		switch {
		case r.URL.Path == "/library/sections/watchlist/all":
			start, _ := strconv.Atoi(r.URL.Query().Get("X-Plex-Container-Start"))
			size, _ := strconv.Atoi(r.URL.Query().Get("X-Plex-Container-Size"))
			total = len(list)
			for i := start; i < len(list) && i < start+size; i++ {
				e := list[i]
				md = append(md, map[string]any{"guid": "plex://" + e.typ + "/" + e.key, "ratingKey": e.key, "type": e.typ, "title": e.title, "year": e.year})
			}
		case strings.HasPrefix(r.URL.Path, "/library/metadata/"):
			metaReqs++
			md = []any{map[string]any{"Guid": ids[strings.TrimPrefix(r.URL.Path, "/library/metadata/")]}}
		case r.URL.Path == "/library/all":
			localReqs++
			md = local[r.URL.Query().Get("guid")]
		default:
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{"totalSize": total, "Metadata": md}})
	}))
	defer srv.Close()
	old := DiscoverBase
	DiscoverBase = srv.URL
	defer func() { DiscoverBase = old }()

	ctx := context.Background()
	c := NewClient(srv.URL, token)
	s := NewSyncer(d, func(context.Context) (*Client, PathMap) { return c, nil }, nil)
	s.Account = func(context.Context) Account { return Account{ClientID: "test"} }
	var changed []int64
	s.WatchlistChanged = func(uid int64) { changed = append(changed, uid) }

	onList := func() string {
		t.Helper()
		rows, err := d.R.Query(`SELECT item_id FROM watchlist WHERE user_id = 1 ORDER BY added_at DESC, item_id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var id int
			rows.Scan(&id)
			out = append(out, fmt.Sprint(id))
		}
		return strings.Join(out, ",")
	}

	s.syncWatchlist(ctx, c, nil)
	st := s.WatchlistStatus(ctx)
	if st == nil || st.Error != "" {
		t.Fatalf("status %+v", st)
	}
	// Plex's order, newest first; the title already there keeps its own date.
	if got, want := onList(), "1,2,3,4,5,7"; got != want {
		t.Errorf("watchlist %s, want %s", got, want)
	}
	if st.OnPlex != 7 || st.Copied != 6 || st.Added != 5 || len(st.Waiting) != 1 || st.Waiting[0] != "Dune: Part Three (2026)" {
		t.Errorf("status %+v", st)
	}
	if len(changed) != 1 || changed[0] != 1 {
		t.Errorf("WatchlistChanged calls %v", changed)
	}

	// Taken off the list in Plexodus: the next copy leaves it off, and
	// doesn't ask the Plex server about the missing title again so soon.
	exec(`DELETE FROM watchlist WHERE user_id = 1 AND item_id = 1`)
	l0, m0 := localReqs, metaReqs
	s.syncWatchlist(ctx, c, nil)
	if got, want := onList(), "2,3,4,5,7"; got != want {
		t.Errorf("after removing here: watchlist %s, want %s", got, want)
	}
	if localReqs != l0 || metaReqs != m0 {
		t.Errorf("second copy asked Plex again: %d server, %d discover requests", localReqs-l0, metaReqs-m0)
	}
	if st := s.WatchlistStatus(ctx); st.Added != 0 || st.Copied != 6 {
		t.Errorf("second status %+v", st)
	}

	// The missing film arrives in the library: it is added by itself.
	add(8, 1, "movie", "Dune Part Three", 2026, 999999)
	s.syncWatchlist(ctx, c, nil)
	if got := onList(); !strings.Contains(got, "8") {
		t.Errorf("arrived title not added: %s", got)
	}
	if st := s.WatchlistStatus(ctx); st.Added != 1 || st.Copied != 7 || len(st.Waiting) != 0 {
		t.Errorf("third status %+v", st)
	}

	// Removed from the Plex Watchlist, then put back: copied again.
	saved := list
	list = list[1:]
	s.syncWatchlist(ctx, c, nil)
	list = saved
	s.syncWatchlist(ctx, c, nil)
	if got := onList(); !strings.HasPrefix(got, "1,") {
		t.Errorf("re-added on Plex but not copied: %s", got)
	}

	// A token plex.tv refuses is reported, and the list is left alone.
	before := onList()
	token = "other"
	s.syncWatchlist(ctx, c, nil)
	if st := s.WatchlistStatus(ctx); !strings.Contains(st.Error, "token") || st.OnPlex != 7 {
		t.Errorf("refused token status %+v", st)
	}
	if onList() != before {
		t.Errorf("watchlist changed after a refused token")
	}
}

func TestWatchlistPaging(t *testing.T) {
	const n = 230
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start, _ := strconv.Atoi(r.URL.Query().Get("X-Plex-Container-Start"))
		size, _ := strconv.Atoi(r.URL.Query().Get("X-Plex-Container-Size"))
		var md []any
		for i := start; i < n && i < start+size; i++ {
			md = append(md, map[string]any{"guid": fmt.Sprintf("plex://movie/k%d", i), "type": "movie", "title": fmt.Sprint("Film ", i)})
		}
		json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{"totalSize": n, "Metadata": md}})
	}))
	defer srv.Close()
	old := DiscoverBase
	DiscoverBase = srv.URL
	defer func() { DiscoverBase = old }()
	got, err := Account{ClientID: "t"}.Watchlist(context.Background(), "tok")
	if err != nil || len(got) != n || got[0].Key != "k0" || got[n-1].Key != "k229" {
		t.Fatalf("got %d items, err %v", len(got), err)
	}
}
