package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/auth"
)

// Collections list only those with two or more films the viewer can see,
// and a film's detail carries its collection.
func TestCollections(t *testing.T) {
	s, admin := testServer(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO collections(id, name, sort_name, poster, updated_at) VALUES (1, 'Heat Collection', 'heat collection', '/c.jpg', 0), (2, 'Solo Collection', 'solo collection', NULL, 0)`,
		`INSERT INTO items(id, library_id, kind, guid, title, sort_title, year, release_date, collection_id, added_at, updated_at) VALUES
			(11, 1, 'movie', 'm11', 'Heat 2', 'heat 2', 2026, '2026-01-01', 1, 0, 0),
			(12, 2, 'movie', 'm12', 'Solo', 'solo', 2000, '2000-01-01', 2, 0, 0)`,
		`UPDATE items SET collection_id = 1, release_date = '1995-12-15' WHERE id = 10`,
	} {
		if _, err := s.db.W.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	list := func(u auth.User) []CollectionCard {
		r := httptest.NewRequest("GET", "/api/collections", nil).WithContext(auth.WithUser(ctx, u))
		w := httptest.NewRecorder()
		s.handleCollections(w, r)
		var out struct{ Groups []CollectionShelf }
		json.Unmarshal(w.Body.Bytes(), &out)
		return shelved(out.Groups)
	}
	cs := list(admin)
	if len(cs) != 1 || cs[0].Name != "Heat Collection" || cs[0].Count != 2 || cs[0].Poster != "/c.jpg" {
		t.Fatalf("collections = %+v", cs)
	}

	r := httptest.NewRequest("GET", "/api/collections/1", nil).WithContext(auth.WithUser(ctx, admin))
	r.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	s.handleCollection(w, r)
	var one struct {
		Items []Card
	}
	json.Unmarshal(w.Body.Bytes(), &one)
	if len(one.Items) != 2 || one.Items[0].Title != "Heat" {
		t.Fatalf("collection items = %+v (%d %s)", one.Items, w.Code, w.Body.String())
	}

	r = httptest.NewRequest("GET", "/api/items/10", nil).WithContext(auth.WithUser(ctx, admin))
	r.SetPathValue("id", "10")
	w = httptest.NewRecorder()
	s.handleItem(w, r)
	var d Detail
	json.Unmarshal(w.Body.Bytes(), &d)
	if d.Collection == nil || d.Collection.ID != 1 || len(d.CollectionItems) != 2 {
		t.Fatalf("item collection = %+v %+v", d.Collection, d.CollectionItems)
	}

	// A friend who can only see library 1 sees one Heat film: no collection.
	friend, err := s.auth.CreateUser(ctx, "friend", "password123", false)
	if err != nil {
		t.Fatal(err)
	}
	friend.AllLibraries = false
	friend.Libraries = []int64{2}
	if cs := list(friend); len(cs) != 0 {
		t.Fatalf("friend collections = %+v", cs)
	}

	// Searching by name, and artwork from the earliest film when the
	// collection has none of its own.
	if _, err := s.db.W.Exec(`UPDATE collections SET poster = NULL WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.W.Exec(`UPDATE items SET year = 1995, poster = '/heat.jpg' WHERE id = 10`); err != nil {
		t.Fatal(err)
	}
	var found struct{ Collections []CollectionCard }
	call(t, s, admin, s.handleCollections, "GET", "/api/collections?q=HEAT", "", nil, &found)
	if len(found.Collections) != 1 || found.Collections[0].Poster != "/heat.jpg" {
		t.Fatalf("q=HEAT: %+v", found.Collections)
	}
	call(t, s, admin, s.handleCollections, "GET", "/api/collections?q=100%25", "", nil, &found)
	if len(found.Collections) != 0 {
		t.Fatalf("q=100%%: %+v", found.Collections)
	}
	if cs := s.searchCollections(httptest.NewRequest("GET", "/", nil).WithContext(auth.WithUser(ctx, admin)), "heat"); len(cs) != 1 {
		t.Fatalf("search heat: %+v", cs)
	}
}

func TestCollectionGroups(t *testing.T) {
	plex := func(name, kind string) CollectionCard { return CollectionCard{Name: name, Source: "plex", Kind: kind} }
	for _, tc := range []struct {
		c    CollectionCard
		want string
	}{
		{CollectionCard{Name: "Star Wars Collection", Source: "tmdb", Kind: "movie"}, groupFranchises},
		{plex("Star Wars Collection", "movie"), groupFranchises},
		{plex("Marvel Cinematic Universe", "movie"), groupFranchises},
		{plex("Pixar", "movie"), groupStudios},
		{plex("Oscar Best Picture Winners", "movie"), groupAwards},
		{plex("IMDb Top 250", "movie"), groupCharts},
		{plex("Top Rated", "movie"), groupCharts},
		{plex("1980s", "movie"), groupDecades},
		{plex("Best of the 90s", "movie"), groupDecades},
		{plex("Christmas Movies", "movie"), groupHolidays},
		{plex("Directed by Christopher Nolan", "movie"), groupPeople},
		{plex("Zombies", "movie"), groupGenres},
		{plex("HBO", "show"), groupStudios},
		{plex("Daniel's picks", "show"), groupShows},
		{plex("Daniel's picks", "movie"), groupOther},
		{plex("Award", "movie"), groupAwards},
	} {
		if got := collectionGroup(tc.c, false); got != tc.want {
			t.Errorf("%q (%s): %q, want %q", tc.c.Name, tc.c.Kind, got, tc.want)
		}
	}
	// A Plex collection named after a TMDB franchise is that franchise
	// ("Halloween" is a film series, not a holiday), and replaces it.
	got := groupCollections([]CollectionCard{
		{ID: 1, Name: "Halloween Collection", Source: "tmdb", Kind: "movie"},
		plex("Halloween", "movie"),
	})
	if len(got) != 1 || got[0].Source != "plex" || got[0].Group != groupFranchises {
		t.Fatalf("halloween = %+v", got)
	}
}

// Plex collections show with all the TMDB franchises, grouped, for films
// and shows the viewer can see, each in Plex's order.
func TestPlexCollections(t *testing.T) {
	s, admin := testServer(t)
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO libraries(id, name, kind, paths, created_at) VALUES (3, 'TV', 'shows', '[]', 0)`,
		`INSERT INTO items(id, library_id, kind, guid, title, sort_title, year, poster, collection_id, added_at, updated_at) VALUES
			(11, 1, 'movie', 'm11', 'Heat 2', 'heat 2', 2026, '/h2.jpg', 1, 0, 0),
			(12, 1, 'movie', 'm12', 'Elf', 'elf', 2003, '/elf.jpg', 0, 0, 0),
			(13, 3, 'show', 's13', 'Chernobyl', 'chernobyl', 2019, '/ch.jpg', NULL, 0, 0)`,
		`UPDATE items SET collection_id = 1, year = 1995 WHERE id = 10`,
		`INSERT INTO collections(id, name, sort_name, poster, updated_at) VALUES (1, 'Heat Collection', 'heat collection', '/c.jpg', 0)`,
		`INSERT INTO collections(id, name, sort_name, source, kind, poster, smart, updated_at) VALUES
			(1000000000501, 'Christmas', 'christmas', 'plex', 'movie', NULL, 1, 0),
			(1000000000502, 'HBO Miniseries', 'hbo miniseries', 'plex', 'show', 'plex:/p.jpg', 0, 0),
			(1000000000503, 'Akira and Elf', 'akira and elf', 'plex', 'movie', NULL, 0, 0)`,
		`INSERT INTO collection_items(collection_id, item_id, position) VALUES
			(1000000000501, 12, 0), (1000000000502, 13, 0), (1000000000503, 12, 1), (1000000000503, 20, 0)`,
		`INSERT INTO watch_state(user_id, item_id, watched, updated_at) VALUES (1, 10, 1, 0)`,
	} {
		if _, err := s.db.W.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var out struct {
		Collections []CollectionCard
		Groups      []CollectionShelf
	}
	call(t, s, admin, s.handleCollections, "GET", "/api/collections", "", nil, &out)
	out.Collections = shelved(out.Groups)
	names := map[string]CollectionCard{}
	for _, c := range out.Collections {
		names[c.Name] = c
	}
	if len(out.Collections) != 4 || names["Christmas"].Poster != "/elf.jpg" || names["Christmas"].Group != groupHolidays ||
		names["HBO Miniseries"].Kind != "show" || names["HBO Miniseries"].Group != groupStudios || names["HBO Miniseries"].Count != 1 ||
		names["Heat Collection"].Watched != 1 || names["Akira and Elf"].Count != 2 {
		t.Fatalf("collections = %+v", out.Collections)
	}
	var shelves []string
	for _, g := range out.Groups {
		shelves = append(shelves, g.Name)
	}
	if want := []string{groupContinue, groupFranchises, groupStudios, groupHolidays, groupOther}; strings.Join(shelves, "|") != strings.Join(want, "|") {
		t.Fatalf("shelves = %v, want %v", shelves, want)
	}

	var one struct {
		Collection CollectionCard
		Items      []Card
	}
	call(t, s, admin, s.handleCollection, "GET", "/api/collections/1000000000503", "", map[string]string{"id": "1000000000503"}, &one)
	if len(one.Items) != 2 || one.Items[0].Title != "Akira" || one.Items[1].Title != "Elf" {
		t.Fatalf("plex collection items = %+v", one.Items)
	}

	// A friend who can only see Anime gets the one title they can see.
	friend, err := s.auth.CreateUser(ctx, "friend", "password123", false)
	if err != nil {
		t.Fatal(err)
	}
	friend.AllLibraries = false
	friend.Libraries = []int64{2}
	out.Groups = nil
	call(t, s, friend, s.handleCollections, "GET", "/api/collections", "", nil, &out)
	out.Collections = shelved(out.Groups)
	if len(out.Collections) != 1 || out.Collections[0].Name != "Akira and Elf" || out.Collections[0].Count != 1 {
		t.Fatalf("friend = %+v", out.Collections)
	}
}

// shelved is every collection on the page once (the "pick up" shelf repeats some).
func shelved(groups []CollectionShelf) []CollectionCard {
	var out []CollectionCard
	for _, g := range groups {
		if g.Name != groupContinue {
			out = append(out, g.Collections...)
		}
	}
	return out
}

// A film in two libraries (Movies and Movies 4K) is one film in its
// collection: counted once, listed once, watched if either copy is.
func TestCollectionCopiesOnce(t *testing.T) {
	s, admin := testServer(t)
	for _, q := range []string{
		`INSERT INTO collections(id, name, sort_name, updated_at) VALUES (1, 'The Accountant Collection', 'accountant collection', 0)`,
		`UPDATE items SET collection_id = 1, tmdb_id = 302946, year = 2016, title = 'The Accountant' WHERE id = 10`,
		`UPDATE items SET collection_id = 1, tmdb_id = 302946, year = 2016, title = 'The Accountant' WHERE id = 20`,
		`INSERT INTO items(id, library_id, kind, guid, title, sort_title, year, tmdb_id, collection_id, added_at, updated_at) VALUES
			(11, 1, 'movie', 'm11', 'The Accountant 2', 'accountant 2', 2025, 870028, 1, 0, 0),
			(21, 2, 'movie', 'm21', 'The Accountant 2', 'accountant 2', 2025, 870028, 1, 0, 0)`,
		`INSERT INTO watch_state(user_id, item_id, watched, updated_at) VALUES (1, 21, 1, 0)`,
	} {
		if _, err := s.db.W.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var one struct {
		Collection CollectionCard
		Items      []Card
	}
	call(t, s, admin, s.handleCollection, "GET", "/api/collections/1", "", map[string]string{"id": "1"}, &one)
	if one.Collection.Count != 2 || one.Collection.Watched != 1 || len(one.Items) != 2 ||
		one.Items[0].Title != "The Accountant" || one.Items[1].ID != 21 || !one.Items[1].Watched {
		t.Fatalf("collection = %+v, items = %+v", one.Collection, one.Items)
	}
	// On a film's page the row keeps that film's own copy.
	var d Detail
	call(t, s, admin, s.handleItem, "GET", "/api/items/11", "", map[string]string{"id": "11"}, &d)
	if len(d.CollectionItems) != 2 || d.CollectionItems[1].ID != 11 || !d.CollectionItems[1].Watched {
		t.Fatalf("film row = %+v", d.CollectionItems)
	}
}
