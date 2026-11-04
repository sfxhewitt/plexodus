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

type ws struct {
	pos     float64
	watched bool
	plays   int
	at      int64
}

func TestSyncAppliesNewerPlexState(t *testing.T) {
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
	exec(`INSERT INTO users(id, username, password_hash, is_admin, created_at) VALUES(1, 'admin', 'x', 1, 0)`)
	exec(`INSERT INTO libraries(id, name, kind, paths, created_at) VALUES(1, 'Movies', 'movies', '[]', 0)`)
	for id := 1; id <= 5; id++ {
		exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, added_at, updated_at) VALUES(?, 1, 'movie', ?, 't', 't', 0, 0)`,
			id, "g"+string(rune('0'+id)))
	}
	// 1-4 linked by the import; 5 arrived later and only matches by path.
	exec(`INSERT INTO plex_links(item_id, rating_key) VALUES(1, '101'), (2, '102'), (3, '103'), (4, '104')`)
	exec(`INSERT INTO media_files(item_id, library_id, path, size, mtime) VALUES(5, 1, '/media/new.mkv', 1, 0)`)
	// Plexodus already has newer progress on 3 than Plex does.
	exec(`INSERT INTO watch_state(user_id, item_id, position, duration, watched, play_count, updated_at) VALUES(1, 3, 600, 7200, 0, 0, ?)`, now)
	// 4 was watched here a while ago, then partly rewatched in Plex.
	exec(`INSERT INTO watch_state(user_id, item_id, position, duration, watched, play_count, updated_at) VALUES(1, 4, 0, 7200, 1, 1, ?)`, now-7200)

	items := map[string]map[string]any{
		"101": {"ratingKey": "101", "type": "movie", "viewCount": 1, "lastViewedAt": now - 60, "duration": 7200000},
		"102": {"ratingKey": "102", "type": "movie", "viewOffset": 1800000, "lastViewedAt": now - 30, "duration": 7200000},
		"103": {"ratingKey": "103", "type": "movie", "viewOffset": 60000, "lastViewedAt": now - 3600, "duration": 7200000},
		"104": {"ratingKey": "104", "type": "movie", "viewCount": 1, "viewOffset": 900000, "lastViewedAt": now - 100, "duration": 7200000},
		"105": {"ratingKey": "105", "type": "movie", "viewCount": 2, "lastViewedAt": now - 10, "duration": 7200000,
			"Media": []any{map[string]any{"Part": []any{map[string]any{"file": "/plex/new.mkv"}}}}},
	}
	plexSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var md []any
		switch {
		case r.URL.Path == "/status/sessions/history/all":
			md = []any{
				map[string]any{"ratingKey": "101", "viewedAt": now - 60, "type": "movie"},
				map[string]any{"ratingKey": "105", "viewedAt": now - 10, "type": "movie"},
			}
		case r.URL.Path == "/library/onDeck":
			md = []any{items["102"], items["103"], items["104"]}
		case strings.HasPrefix(r.URL.Path, "/library/metadata/"):
			for _, k := range strings.Split(strings.TrimPrefix(r.URL.Path, "/library/metadata/"), ",") {
				md = append(md, items[k])
			}
		default:
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{"Metadata": md}})
	}))
	defer plexSrv.Close()

	c := NewClient(plexSrv.URL, "tok")
	s := NewSyncer(d, func(context.Context) (*Client, PathMap) { return c, ParsePathMap("/plex=/media") }, nil)
	checked, updated, err := s.sync(context.Background(), c, ParsePathMap("/plex=/media"))
	if err != nil {
		t.Fatal(err)
	}
	if checked != 5 || updated != 4 {
		t.Errorf("checked %d updated %d, want 5 and 4", checked, updated)
	}
	get := func(id int) ws {
		var w ws
		d.R.QueryRow(`SELECT position, watched, play_count, updated_at FROM watch_state WHERE user_id = 1 AND item_id = ?`, id).
			Scan(&w.pos, &w.watched, &w.plays, &w.at)
		return w
	}
	if w := get(1); !w.watched || w.at != now-60 {
		t.Errorf("item 1 (watched in Plex): %+v", w)
	}
	if w := get(2); w.watched || w.pos != 1800 || w.at != now-30 {
		t.Errorf("item 2 (in progress in Plex): %+v", w)
	}
	if w := get(3); w.pos != 600 || w.at != now {
		t.Errorf("item 3 kept newer local progress: %+v", w)
	}
	if w := get(4); w.watched || w.pos != 900 || w.plays != 1 {
		t.Errorf("item 4 (rewatch in progress): %+v", w)
	}
	if w := get(5); !w.watched || w.plays != 2 {
		t.Errorf("item 5 (matched by path): %+v", w)
	}
	var key string
	d.R.QueryRow(`SELECT rating_key FROM plex_links WHERE item_id = 5`).Scan(&key)
	if key != "105" {
		t.Errorf("item 5 link = %q", key)
	}

	// A second run changes nothing.
	if _, updated, err = s.sync(context.Background(), c, ParsePathMap("/plex=/media")); err != nil || updated != 0 {
		t.Errorf("second run updated %d, err %v", updated, err)
	}
}
