package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/auth"
)

// Without TMDB, "more like this" falls back to the best-rated titles that
// share a genre, never the title itself or a different kind.
func TestExtrasSimilarByGenre(t *testing.T) {
	s, admin := testServer(t)
	if _, err := s.db.W.Exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, genres, rating, added_at, updated_at) VALUES
		(201, 1, 'movie', 'x1', 'Heist One', 'heist one', '["Crime","Thriller"]', 7.0, 0, 0),
		(202, 1, 'movie', 'x2', 'Heist Two', 'heist two', '["Crime"]', 8.5, 0, 0),
		(203, 1, 'movie', 'x3', 'Romance', 'romance', '["Romance"]', 9.0, 0, 0),
		(204, 1, 'show', 'x4', 'Crime Show', 'crime show', '["Crime"]', 9.5, 0, 0),
		(205, 1, 'movie', 'x5', 'Thriller', 'thriller', '["Thriller"]', 6.0, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/items/201/extras", nil).WithContext(auth.WithUser(context.Background(), admin))
	r.SetPathValue("id", "201")
	w := httptest.NewRecorder()
	s.handleExtras(w, r)
	var out struct {
		Cast    []any
		Similar []Card
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	var titles []string
	for _, c := range out.Similar {
		titles = append(titles, c.Title)
	}
	if len(titles) != 2 || titles[0] != "Heist Two" || titles[1] != "Thriller" || out.Cast == nil {
		t.Fatalf("similar = %v, cast = %v (%s)", titles, out.Cast, w.Body.String())
	}
}
