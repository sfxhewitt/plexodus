package plex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/db"
)

func TestSyncMatchesCopiesPlexFixMatch(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	now := db.Now()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := d.W.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO libraries(id, name, kind, paths, created_at) VALUES(1, 'Movies', 'movies', '[]', 0), (2, 'TV', 'shows', '[]', 0)`)
	add := func(id, lib int, kind, title string, tmdb int64, parent any) {
		exec(`INSERT INTO items(id, library_id, kind, guid, parent_id, title, sort_title, tmdb_id, poster, logo, meta_state, added_at, updated_at)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, '/old.jpg', '/old-logo.png', 'matched', 0, 0)`,
			id, lib, kind, "g"+title, parent, title, strings.ToLower(title), tmdb)
	}
	// 1: mismatched at import, since fixed in Plex (the Splash case).
	add(1, 1, "movie", "Talk Dirty to Me Part III", 99001, nil)
	// 2: Plex refreshed it but the match is the same.
	add(2, 1, "movie", "Heat", 949, nil)
	// 3: fixed by hand in Plexodus; Plex still has its old match.
	add(3, 1, "movie", "Right Title", 500, nil)
	exec(`UPDATE items SET match_manual = 1 WHERE id = 3`)
	// 4: a show re-matched in Plex, with one episode.
	add(4, 2, "show", "Wrong Show", 7001, nil)
	add(5, 2, "season", "Season 1", 0, 4)
	add(6, 2, "episode", "Wrong Episode", 0, 5)
	exec(`INSERT INTO plex_links(item_id, rating_key) VALUES(1, '301'), (2, '302'), (3, '303'), (4, '401'), (6, '402')`)

	movies := []any{
		map[string]any{"ratingKey": "301", "type": "movie", "title": "Splash", "year": 1984, "updatedAt": now - 60,
			"guid": "plex://movie/splash", "Guid": []any{map[string]any{"id": "tmdb://2619"}, map[string]any{"id": "imdb://tt0088161"}}},
		map[string]any{"ratingKey": "302", "type": "movie", "title": "Heat", "year": 1995, "updatedAt": now - 120,
			"guid": "plex://movie/heat", "Guid": []any{map[string]any{"id": "tmdb://949"}}},
		map[string]any{"ratingKey": "303", "type": "movie", "title": "Wrong Title", "year": 2001, "updatedAt": now - 180,
			"guid": "plex://movie/wrong", "Guid": []any{map[string]any{"id": "tmdb://12"}}},
	}
	shows := []any{
		map[string]any{"ratingKey": "401", "type": "show", "title": "Right Show", "year": 2010, "updatedAt": now - 30,
			"guid": "plex://show/right", "Guid": []any{map[string]any{"id": "tmdb://8001"}}},
	}
	full := map[string]any{
		"301": map[string]any{"ratingKey": "301", "type": "movie", "title": "Splash", "year": 1984, "summary": "A mermaid.",
			"thumb": "/library/metadata/301/thumb/2", "guid": "plex://movie/splash",
			"Guid": []any{map[string]any{"id": "tmdb://2619"}, map[string]any{"id": "imdb://tt0088161"}},
			"Genre": []any{map[string]any{"tag": "Comedy"}}},
		"401": map[string]any{"ratingKey": "401", "type": "show", "title": "Right Show", "year": 2010,
			"guid": "plex://show/right", "Guid": []any{map[string]any{"id": "tmdb://8001"}}},
	}
	var fetched []string
	plexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var mc map[string]any
		switch {
		case r.URL.Path == "/library/sections":
			mc = map[string]any{"Directory": []any{
				map[string]any{"key": "1", "type": "movie", "title": "Movies"},
				map[string]any{"key": "2", "type": "show", "title": "TV"}}}
		case r.URL.Path == "/library/sections/1/all":
			if r.URL.Query().Get("sort") != "updatedAt:desc" || r.URL.Query().Get("type") != "1" {
				t.Errorf("query %s", r.URL.RawQuery)
			}
			mc = map[string]any{"Metadata": movies}
		case r.URL.Path == "/library/sections/2/all":
			mc = map[string]any{"Metadata": shows}
		case r.URL.Path == "/library/metadata/401/allLeaves":
			mc = map[string]any{"Metadata": []any{map[string]any{"ratingKey": "402", "type": "episode", "title": "Right Episode",
				"index": 1, "parentIndex": 1, "thumb": "/library/metadata/402/thumb/1"}}}
		case strings.HasPrefix(r.URL.Path, "/library/metadata/"):
			keys := strings.TrimPrefix(r.URL.Path, "/library/metadata/")
			fetched = append(fetched, keys)
			var out []any
			for _, k := range strings.Split(keys, ",") {
				out = append(out, full[k])
			}
			mc = map[string]any{"Metadata": out}
		default:
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"MediaContainer": mc})
	}))
	defer plexSrv.Close()

	c := NewClient(plexSrv.URL, "tok")
	pm := ParsePathMap("")
	s := NewSyncer(d, func(context.Context) (*Client, PathMap) { return c, pm }, nil)
	var rematched []int64
	s.Rematched = func(_ context.Context, id int64, kind string, tmdb int64) {
		rematched = append(rematched, id, tmdb)
	}
	n, err := s.SyncMatches(context.Background(), c, pm, now-86400)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("re-matched %d titles, want 2 (Splash and the show)", n)
	}
	if want := []int64{1, 2619, 4, 8001}; !equal(rematched, want) {
		t.Errorf("Rematched calls %v, want %v", rematched, want)
	}
	if strings.Join(fetched, "|") != "301|401" {
		t.Errorf("fetched %v, want only the re-matched titles", fetched)
	}

	type row struct {
		title, imdb, poster, logo string
		tmdb, year                int64
	}
	get := func(id int) row {
		var r row
		if err := d.R.QueryRow(`SELECT title, COALESCE(imdb_id, ''), COALESCE(poster, ''), COALESCE(logo, ''),
				COALESCE(tmdb_id, 0), COALESCE(year, 0) FROM items WHERE id = ?`, id).
			Scan(&r.title, &r.imdb, &r.poster, &r.logo, &r.tmdb, &r.year); err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := get(1); r.title != "Splash" || r.tmdb != 2619 || r.imdb != "tt0088161" || r.year != 1984 ||
		r.poster != "plex:/library/metadata/301/thumb/2" || r.logo != "" {
		t.Errorf("item 1 = %+v, want Splash (1984) with the new IDs, poster and no old logo", r)
	}
	if r := get(2); r.title != "Heat" || r.poster != "/old.jpg" {
		t.Errorf("unchanged match was rewritten: %+v", r)
	}
	if r := get(3); r.title != "Right Title" || r.tmdb != 500 {
		t.Errorf("manual match was overridden: %+v", r)
	}
	if r := get(4); r.title != "Right Show" || r.tmdb != 8001 {
		t.Errorf("show = %+v", r)
	}
	if r := get(6); r.title != "Right Episode" {
		t.Errorf("episode = %+v", r)
	}
	guid := func(id int) string {
		var g string
		d.R.QueryRow(`SELECT COALESCE(plex_guid, '') FROM plex_links WHERE item_id = ?`, id).Scan(&g)
		return g
	}
	for id, want := range map[int]string{1: "plex://movie/splash", 2: "plex://movie/heat", 3: "plex://movie/wrong", 4: "plex://show/right"} {
		if g := guid(id); g != want {
			t.Errorf("item %d plex_guid %q, want %q", id, g, want)
		}
	}

	// A second pass with nothing new in Plex writes nothing.
	rematched, fetched = nil, nil
	if n, err := s.SyncMatches(context.Background(), c, pm, now-86400); err != nil || n != 0 || len(fetched) != 0 {
		t.Errorf("second pass: %d re-matched, err %v, fetched %v", n, err, fetched)
	}

	// Now the manually fixed title is re-matched in Plex too: Plex wins.
	movies[2] = map[string]any{"ratingKey": "303", "type": "movie", "title": "Plex Pick", "year": 2002, "updatedAt": now,
		"guid": "plex://movie/pick", "Guid": []any{map[string]any{"id": "tmdb://13"}}}
	full["303"] = movies[2]
	if n, err := s.SyncMatches(context.Background(), c, pm, now-86400); err != nil || n != 1 {
		t.Fatalf("third pass: %d re-matched, err %v", n, err)
	}
	if r := get(3); r.title != "Plex Pick" || r.tmdb != 13 {
		t.Errorf("item 3 after re-match in Plex = %+v", r)
	}
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
