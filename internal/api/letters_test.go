package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/auth"
)

// An A–Z library list says where each letter starts, in either direction.
func TestLibraryLetters(t *testing.T) {
	s, admin := testServer(t)
	ctx := context.Background()
	if _, err := s.db.W.Exec(`DELETE FROM items WHERE library_id = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.W.Exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, added_at, updated_at) VALUES
		(101, 1, 'movie', 'a', '1917', '1917', 0, 0),
		(102, 1, 'movie', 'b', 'Alien', 'alien', 0, 0),
		(103, 1, 'movie', 'c', 'Aliens', 'aliens', 0, 0),
		(104, 1, 'movie', 'd', 'The Batman', 'batman', 0, 0),
		(105, 1, 'movie', 'e', 'Zodiac', 'zodiac', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	get := func(order string) []Letter {
		r := httptest.NewRequest("GET", "/api/libraries/1/items?sort=title&letters=1&order="+order, nil).WithContext(auth.WithUser(ctx, admin))
		r.SetPathValue("id", "1")
		w := httptest.NewRecorder()
		s.handleLibraryItems(w, r)
		var out struct{ Letters []Letter }
		json.Unmarshal(w.Body.Bytes(), &out)
		return out.Letters
	}
	if got, want := get("asc"), []Letter{{"#", 0}, {"A", 1}, {"B", 3}, {"Z", 4}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("asc letters = %+v, want %+v", got, want)
	}
	if got, want := get("desc"), []Letter{{"Z", 0}, {"B", 1}, {"A", 2}, {"#", 4}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("desc letters = %+v, want %+v", got, want)
	}
}
