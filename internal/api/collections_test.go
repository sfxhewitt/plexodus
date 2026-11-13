package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
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
		var out struct{ Collections []CollectionCard }
		json.Unmarshal(w.Body.Bytes(), &out)
		return out.Collections
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
}
