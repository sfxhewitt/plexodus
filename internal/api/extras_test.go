package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/metadata"
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

// A person's page splits their credits into the copies you have (one card
// per title, matched by kind and TMDB id) and the rest of their work.
func TestPersonPage(t *testing.T) {
	s, admin := testServer(t)
	if _, err := s.db.W.Exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, tmdb_id, added_at, updated_at) VALUES
		(301, 1, 'movie', 'p1', 'Big Film', 'big film', 11, 0, 0),
		(302, 1, 'movie', 'p2', 'Big Film', 'big film', 11, 0, 0),
		(303, 1, 'show', 'p3', 'A Show', 'a show', 22, 0, 0),
		(304, 1, 'show', 'p4', 'Same Id Show', 'same id show', 33, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	peopleCache.Lock()
	peopleCache.m[7] = peopleEntry{&metadata.PersonDetails{ID: 7, Name: "Someone", Credits: []metadata.Credit{
		{TMDB: 11, Kind: "movie", Title: "Big Film", Character: "Hero"},
		{TMDB: 33, Kind: "movie", Title: "A Film With A Show's Id"},
		{TMDB: 22, Kind: "show", Title: "A Show", Character: "Guest"},
		{TMDB: 44, Kind: "movie", Title: "Not Here"},
	}}, time.Now()}
	peopleCache.Unlock()

	r := httptest.NewRequest("GET", "/api/people/7", nil).WithContext(auth.WithUser(context.Background(), admin))
	r.SetPathValue("id", "7")
	w := httptest.NewRecorder()
	s.handlePerson(w, r)
	var out struct {
		Person    metadata.PersonDetails
		InLibrary []Card
		Roles     map[string]string
		Elsewhere []metadata.Credit
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err, w.Body.String())
	}
	var have, rest []string
	for _, c := range out.InLibrary {
		have = append(have, c.Title)
	}
	for _, c := range out.Elsewhere {
		rest = append(rest, c.Title)
	}
	if out.Person.Name != "Someone" || len(have) != 2 || have[0] != "Big Film" || have[1] != "A Show" ||
		len(rest) != 2 || rest[0] != "A Film With A Show's Id" || rest[1] != "Not Here" || out.Roles["303"] != "Guest" {
		t.Fatalf("have %v, rest %v, roles %v (%s)", have, rest, out.Roles, w.Body.String())
	}
}
