package plex

import (
	"context"
	"database/sql"
	"fmt"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/db"
)

func TestSyncMarkersForNewTitles(t *testing.T) {
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
	exec(`INSERT INTO libraries(id, name, kind, paths, created_at) VALUES(1, 'TV', 'shows', '[]', 0)`)
	for id := 1; id <= 5; id++ {
		exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, added_at, updated_at) VALUES(?, 1, 'episode', ?, 't', 't', 0, 0)`,
			id, "g"+string(rune('0'+id)))
	}
	// 1 is linked and has the right markers; 2 is linked with an old intro
	// and a detected credits marker; 3 arrived after the import (path only);
	// 4 Plex hasn't analysed; 5 was added before the window.
	exec(`INSERT INTO plex_links(item_id, rating_key, markers_at) VALUES(1, '201', 1), (2, '202', 1), (4, '204', NULL), (5, '205', NULL)`)
	exec(`INSERT INTO markers(item_id, type, start, "end", source) VALUES(1, 'intro', 10, 70, 'plex'), (2, 'intro', 5, 60, 'plex'), (2, 'credits', 1300, 1400, 'audio')`)
	exec(`INSERT INTO media_files(item_id, library_id, path, size, mtime) VALUES(3, 1, '/media/Show/S01E03.mkv', 1, 0)`)

	intro := func(s, e int64) map[string]any {
		return map[string]any{"type": "intro", "startTimeOffset": s, "endTimeOffset": e}
	}
	eps := []any{
		map[string]any{"ratingKey": "203", "type": "episode", "addedAt": now - 60, "Marker": []any{intro(0, 45000),
			map[string]any{"type": "credits", "startTimeOffset": 1300000, "endTimeOffset": 1380000}},
			"Media": []any{map[string]any{"Part": []any{map[string]any{"file": "/plex/Show/S01E03.mkv"}}}}},
		map[string]any{"ratingKey": "204", "type": "episode", "addedAt": now - 120},
		map[string]any{"ratingKey": "202", "type": "episode", "addedAt": now - 3600, "Marker": []any{intro(8000, 62000)}},
		map[string]any{"ratingKey": "201", "type": "episode", "addedAt": now - 7200, "Marker": []any{intro(10000, 70000)}},
		map[string]any{"ratingKey": "205", "type": "episode", "addedAt": now - 30*86400, "Marker": []any{intro(1000, 2000)}},
	}
	plexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var mc map[string]any
		switch r.URL.Path {
		case "/library/sections":
			mc = map[string]any{"Directory": []any{map[string]any{"key": "1", "type": "show", "title": "TV"}}}
		case "/library/sections/1/all":
			if r.URL.Query().Get("includeMarkers") != "1" || r.URL.Query().Get("type") != "4" {
				t.Errorf("query %s", r.URL.RawQuery)
			}
			mc = map[string]any{"Metadata": eps}
		default:
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"MediaContainer": mc})
	}))
	defer plexSrv.Close()

	c := NewClient(plexSrv.URL, "tok")
	pm := ParsePathMap("/plex=/media")
	s := NewSyncer(d, func(context.Context) (*Client, PathMap) { return c, pm }, nil)
	n, err := s.SyncMarkers(context.Background(), c, pm, now-14*86400)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("changed %d items, want 2 (items 2 and 3)", n)
	}
	type mk struct {
		typ, src   string
		start, end float64
	}
	markers := func(id int) []mk {
		rows, err := d.R.Query(`SELECT type, source, start, "end" FROM markers WHERE item_id = ? ORDER BY type, source`, id)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []mk
		for rows.Next() {
			var m mk
			rows.Scan(&m.typ, &m.src, &m.start, &m.end)
			out = append(out, m)
		}
		return out
	}
	if got := markers(2); len(got) != 2 || got[0] != (mk{"credits", "audio", 1300, 1400}) || got[1] != (mk{"intro", "plex", 8, 62}) {
		t.Errorf("item 2 markers: %+v", got)
	}
	if got := markers(3); len(got) != 2 || got[0] != (mk{"credits", "plex", 1300, 1380}) || got[1] != (mk{"intro", "plex", 0, 45}) {
		t.Errorf("item 3 markers: %+v", got)
	}
	if got := markers(5); len(got) != 0 {
		t.Errorf("item 5 (outside window) got markers: %+v", got)
	}
	var key string
	var at int64
	d.R.QueryRow(`SELECT rating_key, markers_at FROM plex_links WHERE item_id = 3`).Scan(&key, &at)
	if key != "203" || at == 0 {
		t.Errorf("item 3 link = %q at %d", key, at)
	}
	var unchanged int64
	d.R.QueryRow(`SELECT markers_at FROM plex_links WHERE item_id = 1`).Scan(&unchanged)
	if unchanged != 1 {
		t.Errorf("item 1 was rewritten (markers_at %d)", unchanged)
	}

	// A second pass writes nothing.
	if n, err := s.SyncMarkers(context.Background(), c, pm, now-14*86400); err != nil || n != 0 {
		t.Errorf("second pass changed %d, err %v", n, err)
	}
}

// An episode never linked to Plex (added after the import, outside the
// hourly window) and one whose Plex key went stale both get Plex's markers
// when one of the show's episodes is played.
func TestShowMarkersRelinksByPath(t *testing.T) {
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
	exec(`INSERT INTO libraries(id, name, kind, paths, created_at) VALUES(1, 'TV', 'shows', '[]', 0)`)
	exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, added_at, updated_at) VALUES(1, 1, 'show', 'show', 'Dexter', 'Dexter', 0, 0)`)
	exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, added_at, updated_at, parent_id) VALUES(2, 1, 'season', 's1', 'Season 1', 'Season 1', 0, 0, 1)`)
	for id := 10; id <= 13; id++ {
		exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, added_at, updated_at, parent_id) VALUES(?, 1, 'episode', ?, 'e', 'e', 0, 0, 2)`, id, fmt.Sprint("e", id))
		exec(`INSERT INTO media_files(item_id, library_id, path, size, mtime) VALUES(?, 1, ?, 1, 0)`, id, fmt.Sprintf("/media/Dexter/S01E%02d.mkv", id-9))
	}
	// 10 is linked with markers; 11 has a stale key; 12 and 13 were never linked.
	// The show's own key is stale too, so it's found through episode 10.
	exec(`INSERT INTO plex_links(item_id, rating_key, markers_at) VALUES(1, '999', NULL), (10, '310', 1), (11, '411', 1)`)
	exec(`INSERT INTO markers(item_id, type, start, "end", source) VALUES(10, 'intro', 100, 160, 'plex')`)

	leaf := func(key string, n int) map[string]any {
		return map[string]any{"ratingKey": key, "type": "episode",
			"Media": []any{map[string]any{"Part": []any{map[string]any{"file": fmt.Sprintf("/plex/Dexter/S01E%02d.mkv", n)}}}}}
	}
	withIntro := func(key string, s, e int64) map[string]any {
		return map[string]any{"ratingKey": key, "type": "episode",
			"Marker": []any{map[string]any{"type": "intro", "startTimeOffset": s, "endTimeOffset": e}}}
	}
	var batch string
	plexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var mc map[string]any
		switch r.URL.Path {
		case "/library/metadata/310":
			mc = map[string]any{"Metadata": []any{map[string]any{"ratingKey": "310", "grandparentRatingKey": "300"}}}
		case "/library/metadata/300/allLeaves":
			mc = map[string]any{"Metadata": []any{leaf("310", 1), leaf("311", 2), leaf("312", 3), leaf("313", 4)}}
		case "/library/metadata/311,312,313":
			batch = r.URL.Path
			if r.URL.Query().Get("includeMarkers") != "1" {
				t.Errorf("batch without markers: %s", r.URL.RawQuery)
			}
			mc = map[string]any{"Metadata": []any{withIntro("311", 120000, 180000), withIntro("312", 90000, 150000),
				map[string]any{"ratingKey": "313", "type": "episode"}}}
		default:
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"MediaContainer": mc})
	}))
	defer plexSrv.Close()

	c := NewClient(plexSrv.URL, "tok")
	pm := ParsePathMap("/plex=/media")
	s := NewSyncer(d, func(context.Context) (*Client, PathMap) { return c, pm }, nil)
	n, err := s.ShowMarkers(context.Background(), c, pm, 12)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || batch == "" {
		t.Fatalf("brought over markers for %d episodes (batch %q), want 2", n, batch)
	}
	links := map[int64]string{}
	rows, _ := d.R.Query(`SELECT item_id, rating_key FROM plex_links`)
	for rows.Next() {
		var id int64
		var k string
		rows.Scan(&id, &k)
		links[id] = k
	}
	rows.Close()
	want := map[int64]string{1: "300", 10: "310", 11: "311", 12: "312", 13: "313"}
	for id, k := range want {
		if links[id] != k {
			t.Errorf("item %d linked to %q, want %q", id, links[id], k)
		}
	}
	var start, end float64
	d.R.QueryRow(`SELECT start, "end" FROM markers WHERE item_id = 12 AND type = 'intro' AND source = 'plex'`).Scan(&start, &end)
	if start != 90 || end != 150 {
		t.Errorf("item 12 intro %v-%v", start, end)
	}
	var at sql.NullInt64
	d.R.QueryRow(`SELECT markers_at FROM plex_links WHERE item_id = 13`).Scan(&at)
	if !at.Valid {
		t.Errorf("item 13 (Plex has none yet) not marked as asked")
	}
	// The same show isn't looked at again straight away.
	batch = ""
	if n, err := s.ShowMarkers(context.Background(), c, pm, 13); err != nil || n != 0 || batch != "" {
		t.Errorf("second call: %d %v %q", n, err, batch)
	}
}
