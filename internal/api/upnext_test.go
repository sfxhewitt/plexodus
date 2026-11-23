package api

import (
	"fmt"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/db"
)

// Up Next: newest activity first; half-watched items only from the last
// week; shows finished long ago come back when a new episode arrives.
func TestUpNextRules(t *testing.T) {
	s, u := testServer(t)
	now := db.Now()
	day := int64(86400)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := s.db.W.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// show: id, season id, episodes ids (season 1), added at
	show := func(id int64, title string) {
		exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, added_at, updated_at) VALUES (?, 1, 'show', ?, ?, ?, 0, 0)`,
			id, "s"+title, title, title)
	}
	season := func(id, showID int64, n int) {
		exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, parent_id, season_number, added_at, updated_at)
			VALUES (?, 1, 'season', ?, 'Season', 'season', ?, ?, 0, 0)`, id, "x"+itoa(id), showID, n)
	}
	episode := func(id, seasonID int64, sn, en int, added int64) {
		exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, parent_id, season_number, episode_number, added_at, updated_at)
			VALUES (?, 1, 'episode', ?, 'Ep', 'ep', ?, ?, ?, ?, 0)`, id, "e"+itoa(id), seasonID, sn, en, added)
	}
	watched := func(id, at int64) {
		exec(`INSERT INTO watch_state(user_id, item_id, position, duration, watched, updated_at) VALUES (?, ?, 0, 3000, 1, ?)`, u.ID, id, at)
	}
	partial := func(id, at int64) {
		exec(`INSERT INTO watch_state(user_id, item_id, position, duration, watched, updated_at) VALUES (?, ?, 900, 3000, 0, ?)`, u.ID, id, at)
	}

	// A: watching now, finished S1E1 two days ago -> next is S1E2.
	show(100, "a")
	season(101, 100, 1)
	episode(102, 101, 1, 1, now-400*day)
	episode(103, 101, 1, 2, now-400*day)
	watched(102, now-2*day)
	// B: finished season 1 five months ago; season 2 arrived 3 days ago -> returned.
	show(200, "b")
	season(201, 200, 1)
	season(202, 200, 2)
	episode(203, 201, 1, 1, now-400*day)
	episode(204, 202, 2, 1, now-3*day)
	watched(203, now-150*day)
	// C: finished long ago, nothing new -> not shown.
	show(300, "c")
	season(301, 300, 1)
	episode(302, 301, 1, 1, now-400*day)
	episode(303, 301, 1, 2, now-400*day)
	watched(302, now-150*day)
	// Films: one paused yesterday (shown), one paused three weeks ago (not shown).
	partial(10, now-1*day)
	partial(20, now-21*day)
	// D: an episode stopped part way 10 days ago, show otherwise untouched -> not shown.
	show(400, "d")
	season(401, 400, 1)
	episode(402, 401, 1, 1, now-400*day)
	partial(402, now-10*day)

	var h struct {
		Rows []Row `json:"rows"`
	}
	call(t, s, u, s.handleHome, "GET", "/api/home", "", nil, &h)
	var got []int64
	for _, r := range h.Rows {
		if r.Title == "Up Next" {
			for _, c := range r.Items {
				got = append(got, c.ID)
			}
		}
	}
	// Film paused yesterday, then A's next episode (2 days), then B's new season (arrived 3 days ago).
	want := []int64{10, 103, 204}
	if len(got) != len(want) {
		t.Fatalf("up next = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("up next = %v, want %v", got, want)
		}
	}
}

func itoa(n int64) string { return idList([]int64{n}) }

// Up Next leaves out titles only played for a moment: a film or episode
// needs 10 minutes watched (or half of a short one), whether the progress
// came from Plexodus or the Plex sync. The minimum is a setting.
func TestUpNextMinimumWatched(t *testing.T) {
	s, u := testServer(t)
	now := db.Now()
	progress := func(id int64, pos, dur float64) {
		t.Helper()
		if _, err := s.db.W.Exec(`INSERT INTO watch_state(user_id, item_id, position, duration, watched, updated_at) VALUES (?, ?, ?, ?, 0, ?)`,
			u.ID, id, pos, dur, now-id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.W.Exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, added_at, updated_at) VALUES
		(30, 1, 'movie', 'm30', 'Ronin', 'ronin', 0, 0), (40, 1, 'movie', 'm40', 'Short', 'short', 0, 0), (50, 1, 'movie', 'm50', 'Odd', 'odd', 0, 0)`); err != nil {
		t.Fatal(err)
	}
	progress(10, 5, 6000)   // a few seconds: hidden
	progress(20, 300, 6000) // 5 minutes: hidden by default
	progress(30, 700, 6000) // past 10 minutes: shown
	progress(40, 200, 360)  // 6-minute short, over half: shown
	progress(50, 200, 0)    // unknown length, 3 minutes: hidden

	upNext := func() []int64 {
		t.Helper()
		s.allHomesChanged()
		s.homes.m = nil
		var h struct {
			Rows []Row `json:"rows"`
		}
		call(t, s, u, s.handleHome, "GET", "/api/home", "", nil, &h)
		var got []int64
		for _, r := range h.Rows {
			if r.Title == "Up Next" {
				for _, c := range r.Items {
					got = append(got, c.ID)
				}
			}
		}
		return got
	}
	same := func(got, want []int64) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("up next = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("up next = %v, want %v", got, want)
			}
		}
	}
	same(upNext(), []int64{30, 40})

	if err := s.db.SetSetting(t.Context(), upNextMinMinutesKey, "2"); err != nil {
		t.Fatal(err)
	}
	same(upNext(), []int64{20, 30, 40, 50})
}

// Finish a Season leaves out shows already in Up Next, and goes when empty.
func TestFinishSeasonSkipsUpNextShows(t *testing.T) {
	season := Row{Title: "Finish a Season This Weekend", Smart: "season", Items: []Card{{ID: 11, ShowID: 1}, {ID: 22, ShowID: 2}}}
	because := Row{Title: "Because You Watched X", Smart: "because", Items: []Card{{ID: 5}}}
	got := withoutUpNextShows([]Row{season, because}, []Card{{ID: 101, Kind: "episode", ShowID: 1}})
	if len(got) != 2 || len(got[0].Items) != 1 || got[0].Items[0].ShowID != 2 {
		t.Fatalf("got %+v", got)
	}
	got = withoutUpNextShows([]Row{season, because}, []Card{{ID: 101, ShowID: 1}, {ID: 2, Kind: "show"}})
	if len(got) != 1 || got[0].Smart != "because" {
		t.Fatalf("empty row kept: %+v", got)
	}
}

// Pressing Back once the closing credits roll counts as watched, as in Plex,
// so the title leaves Up Next even though it stopped short of 92%.
func TestProgressIntoCreditsIsWatched(t *testing.T) {
	s, u := testServer(t)
	if _, err := s.db.W.Exec(`INSERT INTO markers(item_id, type, start, "end", source) VALUES
		(10, 'intro', 60, 120, 'plex'), (10, 'credits', 5400, 6000, 'plex')`); err != nil {
		t.Fatal(err)
	}
	post := func(id string, pos float64) bool {
		t.Helper()
		var out struct {
			Watched bool `json:"watched"`
		}
		if code := call(t, s, u, s.handleProgress, "POST", "/api/items/"+id+"/progress",
			fmt.Sprintf(`{"position": %v, "duration": 6000}`, pos), map[string]string{"id": id}, &out); code != 200 {
			t.Fatalf("progress = %d", code)
		}
		return out.Watched
	}
	if post("10", 5300) {
		t.Fatal("before the credits: not watched yet")
	}
	if !post("10", 5410) {
		t.Fatal("into the credits: watched")
	}
	if post("20", 5350) {
		t.Fatal("no credits marker: 89% isn't watched")
	}
	if !post("20", 5400) {
		t.Fatal("90%, as in Plex: watched")
	}
}
