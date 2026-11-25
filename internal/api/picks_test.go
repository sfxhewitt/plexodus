package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/db"
)

// pickLibrary fills library 1 with films of clearly different moods.
// 157336 is Interstellar's TMDB id, which the mood catalogue has scored.
func pickLibrary(t *testing.T) (*Server, auth.User, context.Context) {
	t.Helper()
	s, admin := testServer(t)
	now := db.Now()
	for _, q := range []string{
		`DELETE FROM items WHERE library_id = 1`,
		`INSERT INTO items(id, library_id, kind, guid, title, sort_title, year, rating, runtime, tmdb_id, genres, overview, added_at, updated_at) VALUES
			(301, 1, 'movie', 'p1', 'Laugh Riot', 'laugh riot', 2015, 7.4, 88, 0, '["Comedy","Family"]', 'Funny.', ?1, 0),
			(302, 1, 'movie', 'p2', 'Night Terror', 'night terror', 1985, 7.0, 95, 0, '["Horror"]', 'Scary.', ?1, 0),
			(303, 1, 'movie', 'p3', 'Interstellar', 'interstellar', 2014, 8.5, 169, 157336, '["Adventure","Drama","Science Fiction"]', 'Space.', ?1, 0),
			(304, 1, 'movie', 'p4', 'Love Actually-ish', 'love', 2003, 7.2, 110, 0, '["Comedy","Romance"]', 'Sweet.', ?1, 0),
			(305, 1, 'movie', 'p5', 'Fast Car', 'fast car', 2020, 6.9, 100, 0, '["Action"]', 'Vroom.', ?1, 0)`,
	} {
		if _, err := s.db.W.Exec(q, now); err != nil {
			t.Fatal(err)
		}
	}
	return s, admin, auth.WithUser(context.Background(), admin)
}

func pickCall(t *testing.T, ctx context.Context, h http.HandlerFunc, method, path, body string, out any) int {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(ctx)
	r.SetPathValue("id", "1")
	w := httptest.NewRecorder()
	h(w, r)
	if out != nil {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("%s: %v: %s", path, err, w.Body.String())
		}
	}
	return w.Code
}

func cardIDs(cs []Card) []int64 {
	ids := []int64{}
	for _, c := range cs {
		ids = append(ids, c.ID)
	}
	return ids
}

func has(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func TestMoodFiltersAndBestMatch(t *testing.T) {
	s, _, ctx := pickLibrary(t)
	items := func(qs string) []Card {
		var out struct{ Items []Card }
		pickCall(t, ctx, s.handleLibraryItems, "GET", "/api/libraries/1/items?"+qs, "", &out)
		return out.Items
	}
	// Every film got a profile; the scored one came from the catalogue.
	var rows, scored int
	got := items("")
	s.db.R.QueryRow(`SELECT COUNT(*), SUM(src) FROM item_moods`).Scan(&rows, &scored)
	if len(got) != 5 || rows != 6 || scored != 1 {
		t.Fatalf("items %d, mood rows %d, scored %d", len(got), rows, scored)
	}
	if ids := cardIDs(items("mood=la")); !has(ids, 301) || has(ids, 302) || has(ids, 303) {
		t.Fatalf("Laugh = %v", ids)
	}
	if ids := cardIDs(items("mood=mb")); len(ids) != 1 || ids[0] != 303 {
		t.Fatalf("Mind-bender = %v", ids)
	}
	if ids := cardIDs(items("content=noscares,romance")); len(ids) != 1 || ids[0] != 304 {
		t.Fatalf("no scares + romance = %v", ids)
	}
	if ids := cardIDs(items("feel=scary:95")); len(ids) != 1 || ids[0] != 302 {
		t.Fatalf("feel scary = %v", ids)
	}
	if ids := cardIDs(items("yfrom=2000&yto=2014")); len(ids) != 2 || !has(ids, 303) || !has(ids, 304) {
		t.Fatalf("years = %v", ids)
	}
	// Best match puts the closest fit first and says how close.
	got = items("sort=match&mood=la")
	if len(got) != 2 || got[0].Match < 90 {
		t.Fatalf("best match = %+v", got)
	}
	// Paging the match order doesn't repeat or lose films.
	a, b := items("sort=match&limit=3"), items("sort=match&limit=3&offset=3")
	if len(a) != 3 || len(b) != 2 || has(cardIDs(a), b[0].ID) || a[0].Match != 0 {
		t.Fatalf("pages = %v + %v", cardIDs(a), cardIDs(b))
	}

	// Facets count the moods and the content flags.
	var f struct {
		Total  int
		Facets []struct {
			Key     string
			Multi   bool
			Options []struct {
				V     string
				Count int
			}
		}
	}
	pickCall(t, ctx, s.handleLibraryFacets, "GET", "/api/libraries/1/facets?mood=la", "", &f)
	if f.Facets[0].Key != "mood" || f.Facets[1].Key != "content" || !f.Facets[1].Multi {
		t.Fatalf("facets start with %s, %s", f.Facets[0].Key, f.Facets[1].Key)
	}
	for _, o := range f.Facets[0].Options {
		if o.V == "mb" && o.Count != 1 {
			t.Fatalf("mind-bender count with Laugh chosen = %d, want 1 (counted without the mood filter)", o.Count)
		}
	}

	// Saved genres: never Horror, everywhere.
	var g genrePrefs
	if code := pickCall(t, ctx, s.handlePickGenres, "PUT", "/api/picks/genres", `{"exclude":["Horror"],"require":[]}`, &g); code != 200 || len(g.Exclude) != 1 {
		t.Fatalf("save genres: %d %+v", code, g)
	}
	if ids := cardIDs(items("")); len(ids) != 4 || has(ids, 302) {
		t.Fatalf("with Horror ruled out = %v", ids)
	}
	pickCall(t, ctx, s.handlePickGenres, "PUT", "/api/picks/genres", `{"exclude":[],"require":["Action"]}`, &g)
	if ids := cardIDs(items("")); len(ids) != 1 || ids[0] != 305 {
		t.Fatalf("must be Action = %v", ids)
	}
}

func TestSurpriseRules(t *testing.T) {
	s, admin, ctx := pickLibrary(t)
	type res struct {
		Picks    []Pick
		Matching int
		Fresh    int
	}
	picks := func(qs string) res {
		var out res
		if code := pickCall(t, ctx, s.handlePicks, "GET", "/api/libraries/1/picks?"+qs, "", &out); code != 200 {
			t.Fatalf("picks: %d", code)
		}
		return out
	}
	ids := func(r res) []int64 {
		out := []int64{}
		for _, p := range r.Picks {
			out = append(out, p.ID)
		}
		return out
	}
	fb := func(typ string, id int64) map[string]any {
		out := map[string]any{}
		body, _ := json.Marshal(map[string]any{"ids": []int64{id}, "type": typ})
		if code := pickCall(t, ctx, s.handlePickFeedback, "POST", "/api/picks/feedback", string(body), &out); code != 200 {
			t.Fatalf("feedback %s: %d", typ, code)
		}
		return out
	}
	all := picks("")
	if len(all.Picks) != 5 || all.Matching != 5 || all.Picks[0].Why == "" {
		t.Fatalf("first surprise = %+v", all)
	}
	// The mood filter carries over, and the reason names it.
	la := picks("mood=la")
	if !has(ids(la), 301) || has(ids(la), 302) || !strings.HasPrefix(la.Picks[0].Why, "Fits laugh") {
		t.Fatalf("laugh picks = %v %q", ids(la), la.Picks[0].Why)
	}
	// Shown: rests for 30 days.
	fb("shown", 301)
	if r := picks(""); has(ids(r), 301) || r.Fresh != 4 || r.Matching != 5 {
		t.Fatalf("after shown = %v fresh %d", ids(r), r.Fresh)
	}
	s.db.W.Exec(`UPDATE pick_state SET shown_at = shown_at - 31*86400 WHERE item_id = 301`)
	if r := picks(""); !has(ids(r), 301) {
		t.Fatal("back after 30 days")
	}
	// Three skips hide a film for a year; two don't.
	fb("skip", 302)
	if out := fb("skip", 302); out["hidden"] != false || out["strikes"] != float64(2) {
		t.Fatalf("second skip = %v", out)
	}
	if r := picks(""); !has(ids(r), 302) {
		t.Fatal("two skips shouldn't hide")
	}
	if out := fb("skip", 302); out["hidden"] != true {
		t.Fatalf("third skip = %v", out)
	}
	// Not interested hides at once; watched films are never suggested.
	fb("notint", 305)
	s.db.W.Exec(`INSERT INTO watch_state(user_id, item_id, watched, updated_at) VALUES (?, 304, 1, 0)`, admin.ID)
	if r := picks(""); len(r.Picks) != 2 || has(ids(r), 302) || has(ids(r), 305) || has(ids(r), 304) {
		t.Fatalf("after skips, not interested and watched = %v", ids(r))
	}
	var st struct{ Hidden, Resting, Votes int }
	fb("like", 303)
	pickCall(t, ctx, s.handlePickState, "GET", "/api/picks/state", "", &st)
	if st.Hidden != 2 || st.Votes != 1 {
		t.Fatalf("state = %+v", st)
	}
	// Reset the rules: hidden films come back, the like stays.
	pickCall(t, ctx, s.handlePickReset, "POST", "/api/picks/reset", `{"what":"rules"}`, nil)
	pickCall(t, ctx, s.handlePickState, "GET", "/api/picks/state", "", &st)
	if r := picks(""); len(r.Picks) != 4 || st.Hidden != 0 || st.Votes != 1 {
		t.Fatalf("after reset = %v %+v", ids(r), st)
	}
	// The film's page shows its profile and the like.
	var m struct {
		Bars []struct {
			Key   string
			Value int
		}
		Scored bool
		Vote   int
	}
	r := httptest.NewRequest("GET", "/api/items/303/mood", nil).WithContext(ctx)
	r.SetPathValue("id", "303")
	w := httptest.NewRecorder()
	s.handleItemMood(w, r)
	json.Unmarshal(w.Body.Bytes(), &m)
	if len(m.Bars) != 8 || !m.Scored || m.Vote != 1 {
		t.Fatalf("item mood = %s", w.Body.String())
	}
}

func TestQuiz(t *testing.T) {
	s, _, ctx := pickLibrary(t)
	quiz := func(body string) (ids []int64, why string) {
		var out struct{ Picks []Pick }
		if code := pickCall(t, ctx, s.handleQuiz, "POST", "/api/libraries/1/quiz", body, &out); code != 200 {
			t.Fatalf("quiz: %d", code)
		}
		for _, p := range out.Picks {
			ids = append(ids, p.ID)
		}
		if len(out.Picks) > 0 {
			why = out.Picks[0].Why
		}
		return
	}
	// Family night: nothing scary or violent.
	if ids, _ := quiz(`{"who":"family"}`); len(ids) != 1 || ids[0] != 301 {
		t.Fatalf("family = %v", ids)
	}
	// Low and wanting a change: the comedy leads, and the reason says why.
	ids, why := quiz(`{"feel":"low","mode":"change"}`)
	if len(ids) != 5 || (ids[0] != 301 && ids[0] != 304) || !strings.HasPrefix(why, "You're low / sad and want a change") {
		t.Fatalf("low+change = %v %q", ids, why)
	}
	// Era, length and dismissed picks are hard limits.
	if ids, _ := quiz(`{"era":"classic"}`); len(ids) != 1 || ids[0] != 302 {
		t.Fatalf("classics = %v", ids)
	}
	if ids, _ := quiz(`{"time":"short"}`); len(ids) != 1 || ids[0] != 301 {
		t.Fatalf("short = %v", ids)
	}
	if ids, _ := quiz(`{"time":"short","exclude":[301]}`); len(ids) != 0 {
		t.Fatalf("excluded = %v", ids)
	}
}
