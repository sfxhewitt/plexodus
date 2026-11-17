package plex

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/db"
)

func TestRTScores(t *testing.T) {
	body := `[
		{"rating":9.3,"ratingImage":"rottentomatoes://image.rating.ripe","audienceRating":8.8,"audienceRatingImage":"rottentomatoes://image.rating.upright"},
		{"rating":2.1,"ratingImage":"rottentomatoes://image.rating.rotten","audienceRating":4.5,"audienceRatingImage":"rottentomatoes://image.rating.spilled"},
		{"audienceRating":7.9,"audienceRatingImage":"themoviedb://image.rating"},
		{"rating":7.1,"Rating":[{"image":"imdb://image.rating","value":7.1,"type":"audience"},
			{"image":"rottentomatoes://image.rating.ripe","value":8.7,"type":"critic"},
			{"image":"rottentomatoes://image.rating.upright","value":9,"type":"audience"}]}
	]`
	var ms []Metadata
	if err := json.Unmarshal([]byte(body), &ms); err != nil {
		t.Fatal(err)
	}
	want := []RTScores{{93, 88}, {21, 45}, {-1, -1}, {87, 90}}
	for i, w := range want {
		if got := ms[i].RT(); got != w {
			t.Errorf("item %d: got %+v want %+v", i, got, w)
		}
	}
}

func TestSyncRTCopiesScores(t *testing.T) {
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
	for i, k := range []string{"movie", "movie", "movie", "show"} {
		lib := 1
		if k == "show" {
			lib = 2
		}
		exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, added_at, updated_at) VALUES(?, ?, ?, ?, 't', 't', 0, 0)`,
			i+1, lib, k, fmt.Sprint("g", i))
	}
	// 3 already has a critics score that Plex no longer sends.
	exec(`UPDATE items SET rt_critic = 55 WHERE id = 3`)
	exec(`INSERT INTO plex_links(item_id, rating_key) VALUES(1, '11'), (2, '12'), (3, '13'), (4, '21')`)
	rt := func(key string, c, a float64) map[string]any {
		m := map[string]any{"ratingKey": key, "type": "movie"}
		if c > 0 {
			m["rating"], m["ratingImage"] = c, "rottentomatoes://image.rating.ripe"
		}
		if a > 0 {
			m["audienceRating"], m["audienceRatingImage"] = a, "rottentomatoes://image.rating.upright"
		}
		return m
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var mc map[string]any
		switch r.URL.Path {
		case "/library/sections":
			mc = map[string]any{"Directory": []any{
				map[string]any{"key": "1", "type": "movie", "title": "Movies"},
				map[string]any{"key": "2", "type": "show", "title": "TV"},
				map[string]any{"key": "3", "type": "artist", "title": "Music"}}}
		case "/library/sections/1/all":
			mc = map[string]any{"Metadata": []any{rt("11", 9.3, 8.8), rt("12", 0, 0), rt("13", 0, 6.1), rt("99", 5, 5)}}
		case "/library/sections/2/all":
			mc = map[string]any{"Metadata": []any{rt("21", 7.6, 0)}}
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"MediaContainer": mc})
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "tok")
	s := NewSyncer(d, func(context.Context) (*Client, PathMap) { return c, PathMap{} }, nil)
	ctx := context.Background()
	if !s.rtDue(ctx) {
		t.Fatal("first run should be due")
	}
	if err := s.syncRT(ctx, c); err != nil {
		t.Fatal(err)
	}
	if s.rtDue(ctx) {
		t.Error("should not be due again straight away")
	}
	want := map[int][2]sql.NullInt64{
		1: {{Int64: 93, Valid: true}, {Int64: 88, Valid: true}},
		2: {},
		3: {{Int64: 55, Valid: true}, {Int64: 61, Valid: true}},
		4: {{Int64: 76, Valid: true}, {}},
	}
	for id, w := range want {
		var got [2]sql.NullInt64
		d.R.QueryRow(`SELECT rt_critic, rt_audience FROM items WHERE id = ?`, id).Scan(&got[0], &got[1])
		if got != w {
			t.Errorf("item %d: got %v want %v", id, got, w)
		}
	}
	// A second pass with nothing new writes nothing.
	tx, _ := d.W.Begin()
	changed, err := SetRT(tx, 1, RTScores{93, 88})
	tx.Rollback()
	if err != nil || changed {
		t.Errorf("unchanged scores rewritten: %v %v", changed, err)
	}
}
