package plex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/db"
)

// Every Plex collection comes over, smart and hand-made, films and shows,
// matched to Plexodus titles; ones deleted in Plex go away, and artwork
// Plexodus found for a collection is kept while Plex still has none.
func TestSyncCollections(t *testing.T) {
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
	exec(`INSERT INTO libraries(id, name, kind, paths, created_at) VALUES(1, 'Movies', 'movies', '[]', 0), (2, 'TV', 'shows', '[]', 0)`)
	exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, tmdb_id, added_at, updated_at) VALUES
		(1, 1, 'movie', 'a', 'Alien', 'alien', 348, 0, 0), (2, 1, 'movie', 'b', 'Aliens', 'aliens', 679, 0, 0),
		(3, 1, 'movie', 'c', 'Elf', 'elf', 10719, 0, 0), (4, 2, 'show', 'd', 'Chernobyl', 'chernobyl', 87108, 0, 0)`)
	exec(`INSERT INTO plex_links(item_id, rating_key) VALUES(1, '11'), (4, '14')`)
	exec(`INSERT INTO media_files(item_id, library_id, path, size, mtime) VALUES(2, 1, '/media/Aliens.mkv', 1, 0)`)
	// Left over from an earlier sync; Plex has since deleted it.
	exec(`INSERT INTO collections(id, name, sort_name, source, plex_key, updated_at) VALUES(?, 'Gone', 'gone', 'plex', '99', 0)`, PlexCollectionBase+99)
	exec(`INSERT INTO collection_items(collection_id, item_id) VALUES(?, 1)`, PlexCollectionBase+99)
	// A TMDB franchise stays.
	exec(`INSERT INTO collections(id, name, sort_name, updated_at) VALUES(8091, 'Alien Collection', 'alien collection', 0)`)

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body any
		mc := func(v any) any { return map[string]any{"MediaContainer": v} }
		switch r.URL.Path {
		case "/library/sections":
			body = mc(map[string]any{"Directory": []any{
				map[string]any{"key": "1", "type": "movie", "title": "Movies"},
				map[string]any{"key": "2", "type": "show", "title": "TV"},
				map[string]any{"key": "3", "type": "artist", "title": "Music"},
			}})
		case "/library/sections/1/collections":
			body = mc(map[string]any{"Metadata": []any{
				map[string]any{"ratingKey": "501", "title": "Alien", "titleSort": "Alien", "subtype": "movie",
					"thumb": "/library/collections/501/composite/123", "summary": "In space…"},
				map[string]any{"ratingKey": "502", "title": "Christmas Movies", "subtype": "movie", "smart": "1",
					"thumb": "/library/metadata/502/thumb/9", "art": "/library/metadata/502/art/9"},
			}})
		case "/library/sections/2/collections":
			body = mc(map[string]any{"Metadata": []any{
				map[string]any{"ratingKey": "601", "title": "HBO Miniseries", "subtype": "show", "smart": true},
			}})
		case "/library/collections/501/children":
			body = mc(map[string]any{"Metadata": []any{
				map[string]any{"ratingKey": "11", "type": "movie"},
				map[string]any{"ratingKey": "12", "type": "movie", "Media": []any{map[string]any{"Part": []any{map[string]any{"file": "/media/Aliens.mkv"}}}}},
				map[string]any{"ratingKey": "13", "type": "movie", "Guid": []any{map[string]any{"id": "tmdb://999999"}}}, // not in Plexodus
			}})
		case "/library/collections/502/children":
			body = mc(map[string]any{"Metadata": []any{
				map[string]any{"ratingKey": "15", "type": "movie", "Guid": []any{map[string]any{"id": "tmdb://10719"}}},
			}})
		case "/library/collections/601/children":
			body = mc(map[string]any{"Metadata": []any{map[string]any{"ratingKey": "14", "type": "show"}}})
		default:
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(body)
	}))
	defer srv.Close()

	s := NewSyncer(d, nil, nil)
	c := NewClient(srv.URL, "tok")
	ctx := context.Background()
	n, err := s.SyncCollections(ctx, c, nil)
	if err != nil || n != 3 {
		t.Fatalf("sync = %d, %v", n, err)
	}
	type row struct {
		name, kind, poster, backdrop string
		smart                        bool
		items                        string
	}
	read := func(key int64) row {
		t.Helper()
		var r row
		if err := d.R.QueryRow(`SELECT name, kind, COALESCE(poster, ''), COALESCE(backdrop, ''), smart,
			COALESCE((SELECT group_concat(item_id, ',') FROM (SELECT item_id FROM collection_items WHERE collection_id = c.id ORDER BY position)), '')
			FROM collections c WHERE id = ?`, PlexCollectionBase+key).Scan(&r.name, &r.kind, &r.poster, &r.backdrop, &r.smart, &r.items); err != nil {
			t.Fatalf("collection %d: %v", key, err)
		}
		return r
	}
	if r := read(501); r.name != "Alien" || r.poster != "" || r.items != "1,2" || r.smart {
		t.Fatalf("Alien = %+v (a composite is no poster; 13 isn't here)", r)
	}
	if r := read(502); r.poster != "plex:/library/metadata/502/thumb/9" || r.backdrop == "" || r.items != "3" || !r.smart {
		t.Fatalf("Christmas = %+v", r)
	}
	if r := read(601); r.kind != "show" || r.items != "4" || !r.smart {
		t.Fatalf("HBO = %+v", r)
	}
	var left int
	d.R.QueryRow(`SELECT COUNT(*) FROM collections WHERE id = ? OR id = 8091`, PlexCollectionBase+99).Scan(&left)
	if left != 1 {
		t.Fatalf("collections left = %d, want just the TMDB one", left)
	}
	d.R.QueryRow(`SELECT COUNT(*) FROM collection_items WHERE collection_id = ?`, PlexCollectionBase+99).Scan(&left)
	if left != 0 {
		t.Fatalf("items of deleted collection = %d", left)
	}

	// Artwork found on TMDB survives the next sync.
	exec(`UPDATE collections SET poster = '/tmdb.jpg' WHERE id = ?`, PlexCollectionBase+501)
	if _, err := s.SyncCollections(ctx, c, nil); err != nil {
		t.Fatal(err)
	}
	if r := read(501); r.poster != "/tmdb.jpg" {
		t.Fatalf("poster after resync = %q", r.poster)
	}
	// It runs every six hours, not every watch sync.
	before := calls
	if err := s.syncCollections(ctx, c, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := s.syncCollections(ctx, c, nil, false); err != nil {
		t.Fatal(err)
	}
	if calls-before != 6 {
		t.Fatalf("plex calls for two runs = %d, want one run's 6", calls-before)
	}
}
