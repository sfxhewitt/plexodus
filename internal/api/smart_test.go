package api

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/db"
)

// smartLibrary adds a shows library with one show (two seasons of four
// episodes) and a set of films to testServer's database.
func smartLibrary(t *testing.T, s *Server) {
	t.Helper()
	old := db.Now() - 400*86400
	q := []string{
		`INSERT INTO libraries(id, name, kind, paths, created_at) VALUES (3, 'TV', 'shows', '[]', 0)`,
		`INSERT INTO items(id, library_id, kind, guid, title, sort_title, poster, genres, rating, added_at, updated_at) VALUES
			(300, 3, 'show', 's300', 'Time', 'time', '/p.jpg', '["Drama","Crime"]', 8, 0, 0),
			(301, 3, 'season', 's301', 'Season 1', 'season 1', NULL, NULL, NULL, 0, 0),
			(302, 3, 'season', 's302', 'Season 2', 'season 2', NULL, NULL, NULL, 0, 0)`,
		`UPDATE items SET parent_id = 300, season_number = id - 300 WHERE id IN (301, 302)`,
	}
	for _, x := range q {
		if _, err := s.db.W.Exec(x); err != nil {
			t.Fatal(err)
		}
	}
	for se := 1; se <= 2; se++ {
		for e := 1; e <= 4; e++ {
			id := 310 + se*10 + e
			if _, err := s.db.W.Exec(`INSERT INTO items(id, library_id, kind, guid, parent_id, title, sort_title, season_number, episode_number, runtime, added_at, updated_at)
				VALUES (?, 3, 'episode', ?, ?, 'Ep', 'ep', ?, ?, 50, 0, 0)`, id, fmt.Sprint("e", id), 300+se, se, e); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Films 400-419: short and well rated, old, in Drama/Crime; 420 is long.
	for i := 0; i < 21; i++ {
		rt := 90
		if i == 20 {
			rt = 150
		}
		if _, err := s.db.W.Exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, poster, genres, rating, runtime, year, added_at, updated_at)
			VALUES (?, 1, 'movie', ?, ?, ?, '/p.jpg', '["Drama","Crime"]', 8, ?, 2000, ?, ?)`, 400+i, fmt.Sprint("m", 400+i), fmt.Sprint("Film ", i), fmt.Sprint("film ", i), rt, old, old); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSmartRows(t *testing.T) {
	s, u := testServer(t)
	smartLibrary(t, s)
	now := db.Now()
	// Watched S1E1-E3 and film 400.
	for _, id := range []int{321, 322, 323, 400} {
		if _, err := s.db.W.Exec(`INSERT INTO watch_state(user_id, item_id, position, duration, watched, play_count, updated_at) VALUES (?, ?, 0, 3000, 1, 1, ?)`, u.ID, id, now-3600); err != nil {
			t.Fatal(err)
		}
	}
	var h struct {
		Rows []Row `json:"rows"`
	}
	start := time.Now()
	call(t, s, u, s.handleHome, "GET", "/api/home", "", nil, &h)
	t.Logf("home took %s", time.Since(start))
	rows := map[string]Row{}
	for _, r := range h.Rows {
		rows[r.Smart] = r
	}

	season := rows["season"]
	if len(season.Items) != 1 || season.Items[0].ID != 301 || season.Items[0].Title != "Time" || season.Items[0].Note != "Season 1 · last episode · 50m" {
		t.Errorf("finish a season = %+v", season.Items)
	}
	short := rows["short"]
	if len(short.Items) == 0 {
		t.Fatal("no Under 100 Minutes row")
	}
	for _, c := range short.Items {
		if c.ID == 400 || c.ID == 420 || c.Runtime > 100 {
			t.Errorf("Under 100 Minutes has %d (%d min)", c.ID, c.Runtime)
		}
	}
	because := rows["because"]
	if because.Title != "Because You Watched Film 0" || len(because.Items) < 4 {
		t.Errorf("because row = %q with %d items", because.Title, len(because.Items))
	}
	gems := rows["gems"]
	for _, c := range gems.Items {
		if c.ID == 400 || c.ID == 300 {
			t.Errorf("Forgotten Gems has watched/started title %d", c.ID)
		}
	}
	if len(gems.Items) < 6 {
		t.Errorf("Forgotten Gems has %d items", len(gems.Items))
	}

	// Picks are stable within a day.
	var again struct {
		Rows []Row `json:"rows"`
	}
	call(t, s, u, s.handleHome, "GET", "/api/home", "", nil, &again)
	for _, r := range again.Rows {
		if r.Smart == "short" {
			for i := range r.Items {
				if r.Items[i].ID != short.Items[i].ID {
					t.Fatal("Under 100 Minutes reshuffled between two loads")
				}
			}
		}
	}

	// Hiding the Movies library from Home drops its smart rows.
	if _, err := s.db.W.Exec(`INSERT INTO user_library_prefs(user_id, library_id, hide_home) VALUES (?, 1, 1)`, u.ID); err != nil {
		t.Fatal(err)
	}
	s.forgetHome(u.ID) // as saving library settings does
	var hidden struct {
		Rows []Row `json:"rows"`
	}
	call(t, s, u, s.handleHome, "GET", "/api/home", "", nil, &hidden)
	for _, r := range hidden.Rows {
		for _, c := range r.Items {
			if c.LibraryID == 1 && r.Smart != "" {
				t.Errorf("row %q shows a film from a library hidden from Home", r.Title)
			}
		}
	}
}

func TestProgressRecordsHistory(t *testing.T) {
	s, u := testServer(t)
	post := func(pos float64) {
		call(t, s, u, s.handleProgress, "POST", "/api/items/10/progress", fmt.Sprintf(`{"position": %v, "duration": 6000}`, pos), map[string]string{"id": "10"}, nil)
	}
	post(3000)
	post(5800)
	post(5900) // still in the credits: same play
	var n int
	var secs float64
	s.db.R.QueryRow(`SELECT COUNT(*), COALESCE(MAX(seconds), 0) FROM watch_history WHERE user_id = ? AND item_id = 10`, u.ID).Scan(&n, &secs)
	if n != 1 || secs != 6000 {
		t.Fatalf("history rows = %d (seconds %v), want 1 (6000)", n, secs)
	}
}

func TestWrappedSeason(t *testing.T) {
	cases := []struct {
		m    time.Month
		d    int
		year int
		open bool
	}{
		{time.November, 13, 0, false},
		{time.December, 25, 0, false},
		{time.December, 26, 2026, true},
		{time.December, 31, 2026, true},
		{time.January, 1, 2026, true},
		{time.January, 31, 2026, true},
		{time.February, 1, 0, false},
		{time.July, 4, 0, false},
	}
	for _, c := range cases {
		y := 2026
		if c.m < time.December {
			y = 2027
		}
		got, open := wrappedSeason(time.Date(y, c.m, c.d, 12, 0, 0, 0, time.UTC))
		if open != c.open || got != c.year {
			t.Errorf("%s %d: got (%d, %v), want (%d, %v)", c.m, c.d, got, open, c.year, c.open)
		}
	}
}

// Outside the season Wrapped is hidden: no Home tile and the API says so.
func TestWrappedOffSeason(t *testing.T) {
	s, u := testServer(t)
	smartLibrary(t, s)
	defer func(f func() time.Time) { wrappedNow = f }(wrappedNow)
	wrappedNow = func() time.Time { return time.Date(2025, 11, 13, 12, 0, 0, 0, time.UTC) }
	if err := db.RecordPlay(t.Context(), s.db.W, u.ID, 10, time.Date(2025, 6, 1, 20, 0, 0, 0, time.UTC).Unix(), 0, "plex"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/api/home", nil).WithContext(auth.WithUser(t.Context(), u))
	if tz := s.wrappedTeaser(r); tz != nil {
		t.Errorf("teaser in November = %v, want none", tz)
	}
	if code := call(t, s, u, s.handleWrapped, "GET", "/api/wrapped?year=2025", "", nil, nil); code != 404 {
		t.Errorf("wrapped in November = %d, want 404", code)
	}
	wrappedNow = func() time.Time { return time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC) }
	if tz := s.wrappedTeaser(r); tz == nil || tz["year"] != 2025 {
		t.Errorf("teaser in January = %v, want 2025", tz)
	}
}

func TestWrapped(t *testing.T) {
	s, u := testServer(t)
	defer func(f func() time.Time) { wrappedNow = f }(wrappedNow)
	wrappedNow = func() time.Time { return time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC) }
	smartLibrary(t, s)
	loc, _ := time.LoadLocation("Europe/London")
	at := func(m time.Month, d, h int) int64 { return time.Date(2025, m, d, h, 0, 0, 0, loc).Unix() }
	plays := []struct {
		item int64
		at   int64
	}{
		{321, at(3, 1, 22)}, {322, at(3, 1, 23)}, {323, at(3, 2, 0)}, // binge spans midnight: 2 on Mar 1, 1 on Mar 2
		{324, at(3, 2, 21)}, {331, at(3, 2, 22)}, {332, at(3, 2, 23)}, // 3 on Mar 2 + the 00:00 one = 4
		{400, at(3, 3, 20)},
		{10, at(6, 1, 20)},
		{401, time.Date(2024, 12, 31, 20, 0, 0, 0, loc).Unix()}, // other year
	}
	for _, p := range plays {
		if err := db.RecordPlay(t.Context(), s.db.W, u.ID, p.item, p.at, 0, "plex"); err != nil {
			t.Fatal(err)
		}
	}
	var wr Wrapped
	call(t, s, u, s.handleWrapped, "GET", "/api/wrapped?year=2025&tz=Europe/London", "", nil, &wr)
	if wr.Plays != 8 || wr.Episodes != 6 || wr.Movies != 2 || wr.Shows != 1 {
		t.Errorf("counts = %d plays, %d episodes, %d films, %d shows", wr.Plays, wr.Episodes, wr.Movies, wr.Shows)
	}
	// Episodes are 50 min (runtime); films have no runtime here (Heat) or 90.
	if want := float64(6*50*60 + 90*60 + guessMovie); wr.Seconds != want {
		t.Errorf("seconds = %v, want %v", wr.Seconds, want)
	}
	if wr.Streak.Days != 3 || wr.Streak.Start != "2025-03-01" || wr.Streak.End != "2025-03-03" {
		t.Errorf("streak = %+v", wr.Streak)
	}
	if wr.Binge == nil || wr.Binge.Episodes != 4 || wr.Binge.Date != "2025-03-02" || wr.Binge.Show.ID != 300 {
		t.Errorf("binge = %+v", wr.Binge)
	}
	if len(wr.TopShows) != 1 || wr.TopShows[0].Card.ID != 300 || wr.TopShows[0].Plays != 6 {
		t.Errorf("top shows = %+v", wr.TopShows)
	}
	if wr.PeakHour != 20 || wr.Persona != "The Prime-Timer" { // two films at 8pm outweigh two episodes at 10pm
		t.Errorf("peak hour %d, persona %q", wr.PeakHour, wr.Persona)
	}
	if len(wr.Years) != 2 || wr.Years[0] != 2025 {
		t.Errorf("years = %v", wr.Years)
	}
	if wr.First == nil || wr.First.Card.ID != 300 {
		t.Errorf("first = %+v", wr.First)
	}
	if len(wr.TopGenres) == 0 || wr.TopGenres[0].Name != "Crime" && wr.TopGenres[0].Name != "Drama" {
		t.Errorf("genres = %+v", wr.TopGenres)
	}
}

// Suggestion rows (smart rows, hero, Critically Acclaimed, screensaver)
// leave out titles the viewer has seen (watched now, or ever played per
// watch_history) or is part way through. Each profile counts on its own.
func TestSuggestionsSkipSeen(t *testing.T) {
	s, u := testServer(t)
	smartLibrary(t, s)
	if _, err := s.db.W.Exec(`UPDATE items SET backdrop = '/b.jpg', overview = 'x' WHERE kind IN ('movie', 'show')`); err != nil {
		t.Fatal(err)
	}
	now := db.Now()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.W.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// 401 watched, 402 half watched, 403 played once (history only, since
	// marked unwatched), show 300 played in the past (history only).
	exec(`INSERT INTO watch_state(user_id, item_id, position, duration, watched, play_count, updated_at) VALUES (?1, 401, 0, 3000, 1, 1, ?2), (?1, 402, 1200, 5400, 0, 0, ?2)`, u.ID, now-3600)
	exec(`INSERT INTO watch_history(user_id, item_id, watched_at, seconds, source) VALUES (?1, 403, ?2, 5400, 'plex'), (?1, 321, ?2, 3000, 'plex')`, u.ID, now-90*86400)
	seen := map[int64]bool{401: true, 402: true, 403: true, 300: true}

	var h struct {
		Hero []Card `json:"hero"`
		Rows []Row  `json:"rows"`
	}
	for i := 0; i < 5; i++ { // the hero and Critically Acclaimed are random
		s.forgetHome(u.ID)
		s.smart.forget(u.ID)
		call(t, s, u, s.handleHome, "GET", "/api/home", "", nil, &h)
		for _, c := range h.Hero {
			if seen[c.ID] {
				t.Errorf("hero has seen title %d", c.ID)
			}
		}
		for _, r := range h.Rows {
			if r.Smart == "" && r.Title != "Critically Acclaimed" {
				continue
			}
			for _, c := range r.Items {
				if seen[c.ID] {
					t.Errorf("row %q has seen title %d", r.Title, c.ID)
				}
			}
		}
		var saver []Card
		call(t, s, u, s.handleScreensaver, "GET", "/api/screensaver", "", nil, &saver)
		for _, c := range saver {
			if seen[c.ID] {
				t.Errorf("screensaver has seen title %d", c.ID)
			}
		}
	}
	if len(h.Hero) == 0 {
		t.Error("hero is empty")
	}
}
