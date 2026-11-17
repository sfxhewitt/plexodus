package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/db"
)

// Library filters combine, and the facet counts say what each choice would leave.
func TestLibraryFiltersAndFacets(t *testing.T) {
	s, admin := testServer(t)
	ctx := auth.WithUser(context.Background(), admin)
	now := db.Now()
	if _, err := s.db.W.Exec(`DELETE FROM items WHERE library_id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.W.Exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, year, rating, runtime, age, genres, added_at, updated_at) VALUES
		(201, 1, 'movie', 'f1', 'Alien', 'alien', 1979, 8.5, 117, 17, '["Horror","Science Fiction"]', ?1, 0),
		(202, 1, 'movie', 'f2', 'Arrival', 'arrival', 2016, 7.6, 116, 13, '["Drama","Science Fiction"]', ?1, 0),
		(203, 1, 'movie', 'f3', 'Up', 'up', 2009, 8.0, 96, 8, '["Animation","Family"]', ?2, 0),
		(204, 1, 'movie', 'f4', 'Heat', 'heat', 1995, 7.9, 170, 17, '["Crime","Drama"]', ?2, 0)`, now, now-200*86400); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.W.Exec(`INSERT INTO media_files(item_id, library_id, path, size, mtime, width, height) VALUES
		(201, 1, '/m/alien.mkv', 1, 0, 3840, 1600), (202, 1, '/m/arrival.mkv', 1, 0, 1920, 1080), (204, 1, '/m/heat.mkv', 1, 0, 1280, 536)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.W.Exec(`INSERT INTO watch_state(user_id, item_id, watched, position, updated_at) VALUES (?, 204, 1, 0, 0), (?, 202, 0, 600, 0)`, admin.ID, admin.ID); err != nil {
		t.Fatal(err)
	}
	items := func(qs string) []int64 {
		r := httptest.NewRequest("GET", "/api/libraries/1/items?"+qs, nil).WithContext(ctx)
		r.SetPathValue("id", "1")
		w := httptest.NewRecorder()
		s.handleLibraryItems(w, r)
		var out struct{ Items []Card }
		json.Unmarshal(w.Body.Bytes(), &out)
		ids := []int64{}
		for _, c := range out.Items {
			ids = append(ids, c.ID)
		}
		return ids
	}
	eq := func(name string, got []int64, want ...int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("%s = %v, want %v", name, got, want)
			}
		}
	}
	eq("scifi", items("genre=Science%20Fiction"), 201, 202)
	eq("4k", items("res=4k"), 201)
	eq("1080", items("res=1080"), 202)
	eq("unwatched 8+", items("filter=unwatched&rating=8"), 201, 203)
	eq("watched", items("filter=watched"), 204)
	eq("in progress", items("filter=inprogress"), 202)
	eq("1990s long", items("decade=1990&length=long"), 204)
	eq("kids", items("age=kids"), 203)
	eq("adults", items("age=adult"), 201, 204)
	eq("new this month", items("added=30"), 201, 202)
	eq("by runtime", items("sort=runtime&order=asc"), 203, 202, 201, 204)
	if a, b := items("sort=random&seed=7"), items("sort=random&seed=7"); len(a) != 4 || a[0] != b[0] || a[3] != b[3] {
		t.Fatalf("shuffle not stable: %v vs %v", a, b)
	}

	r := httptest.NewRequest("GET", "/api/libraries/1/facets?genre=Drama", nil).WithContext(ctx)
	r.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	s.handleLibraryFacets(w, r)
	var f struct {
		Total  int
		Facets []facetOut
		Genres []facetCount
	}
	if err := json.Unmarshal(w.Body.Bytes(), &f); err != nil {
		t.Fatal(err, w.Body.String())
	}
	if f.Total != 2 {
		t.Fatalf("total = %d, want 2 (%s)", f.Total, w.Body.String())
	}
	count := func(key, v string) int {
		for _, fo := range f.Facets {
			if fo.Key == key {
				for _, o := range fo.Options {
					if o.V == v {
						return o.Count
					}
				}
			}
		}
		return -1
	}
	// Among the dramas: one watched, one in progress, one 1080p, one 720p.
	if count("filter", "watched") != 1 || count("filter", "inprogress") != 1 || count("res", "1080") != 1 || count("res", "720") != 1 || count("res", "4k") != 0 {
		t.Fatalf("facets wrong: %s", w.Body.String())
	}
	// Genres ignore the genre choice itself: all five genres are offered.
	if len(f.Genres) != 6 {
		t.Fatalf("genres = %+v", f.Genres)
	}
}
