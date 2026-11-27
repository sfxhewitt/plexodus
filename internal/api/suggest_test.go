package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/db"
)

// Remove from suggestions: the title leaves every suggestion on Home (and
// the screensaver) at once, stays out, and comes back when restored.
func TestRemoveFromSuggestions(t *testing.T) {
	s, u := testServer(t)
	smartLibrary(t, s)
	if _, err := s.db.W.Exec(`UPDATE items SET backdrop = '/b.jpg', overview = 'x' WHERE kind IN ('movie', 'show')`); err != nil {
		t.Fatal(err)
	}
	// Film 420 was watched, so there is a Because You Watched row.
	if _, err := s.db.W.Exec(`INSERT INTO watch_state(user_id, item_id, position, duration, watched, play_count, updated_at) VALUES (?, 420, 0, 9000, 1, 1, ?)`, u.ID, db.Now()-60); err != nil {
		t.Fatal(err)
	}
	type home struct {
		Hero    []Card `json:"hero"`
		Rows    []Row  `json:"rows"`
		Removed int    `json:"removed"`
	}
	// where lists everywhere on Home (and the screensaver) the title shows.
	where := func(id int64) (out []string, h home) {
		t.Helper()
		if c := call(t, s, u, s.handleHome, "GET", "/api/home", "", nil, &h); c != 200 {
			t.Fatalf("home = %d", c)
		}
		for _, c := range h.Hero {
			if c.ID == id {
				out = append(out, "hero")
			}
		}
		for _, r := range h.Rows {
			for _, c := range r.Items {
				if c.ID == id {
					out = append(out, r.Title)
				}
			}
		}
		var saver []Card
		call(t, s, u, s.handleScreensaver, "GET", "/api/screensaver?n=60", "", nil, &saver)
		for _, c := range saver {
			if c.ID == id {
				out = append(out, "screensaver")
			}
		}
		return out, h
	}
	rowsWith := func(h home, title string) bool {
		for _, r := range h.Rows {
			if r.Title == title {
				return true
			}
		}
		return false
	}

	before, h := where(405)
	if len(before) < 3 {
		t.Fatalf("film 405 should be suggested in several places before, got %v", before)
	}
	for _, want := range []string{"Because You Watched Film 20", "Under 100 Minutes", "Forgotten Gems", "Recently Added in Movies"} {
		if !rowsWith(h, want) {
			t.Fatalf("home has no %q row; rows = %v", want, rowTitles(h.Rows))
		}
	}
	if h.Removed != 0 {
		t.Fatalf("removed = %d before anything was removed", h.Removed)
	}

	// Home is served straight after the removal (no waiting for a cache to
	// expire) and the film is gone from all of it, however often it's asked.
	if c := call(t, s, u, s.handleHideSuggestion, "POST", "/api/items/405/suggestions/hide", "", map[string]string{"id": "405"}, nil); c != 200 {
		t.Fatalf("hide = %d", c)
	}
	for i := 0; i < 4; i++ {
		got, h := where(405)
		if len(got) != 0 {
			t.Fatalf("film 405 still shows after removal: %v", got)
		}
		if h.Removed != 1 {
			t.Fatalf("removed = %d, want 1", h.Removed)
		}
		s.forgetHome(u.ID) // another day, another build: still gone
	}

	// It is listed, and can be put back.
	var list struct {
		Items []Card `json:"items"`
	}
	call(t, s, u, s.handleHiddenSuggestions, "GET", "/api/suggestions/hidden", "", nil, &list)
	if len(list.Items) != 1 || list.Items[0].ID != 405 {
		t.Fatalf("hidden list = %+v, want film 405", list.Items)
	}
	if c := call(t, s, u, s.handleRestoreSuggestions, "DELETE", "/api/suggestions/hidden/405", "", map[string]string{"id": "405"}, nil); c != 200 {
		t.Fatalf("restore = %d", c)
	}
	if got, h := where(405); len(got) == 0 || h.Removed != 0 {
		t.Fatalf("film 405 should be back after restoring: %v, removed %d", got, h.Removed)
	}

	// Another profile keeps its own suggestions.
	call(t, s, u, s.handleHideSuggestion, "POST", "/api/items/406/suggestions/hide", "", map[string]string{"id": "406"}, nil)
	if _, err := s.db.W.Exec(`INSERT INTO users(id, username, password_hash, is_admin, created_at) VALUES (77, 'other', 'x', 1, 0)`); err != nil {
		t.Fatal(err)
	}
	other := u
	other.ID, other.Username = 77, "other"
	var n int
	s.db.R.QueryRow(`SELECT COUNT(*) FROM suggest_hidden WHERE user_id = 77`).Scan(&n)
	if n != 0 || s.removedSuggestions(context.Background(), other.ID) != 0 {
		t.Fatal("a removal leaked to another user")
	}

	// Bring them all back.
	if c := call(t, s, u, s.handleRestoreSuggestions, "DELETE", "/api/suggestions/hidden", "", nil, nil); c != 200 {
		t.Fatalf("restore all = %d", c)
	}
	if got, _ := where(406); len(got) == 0 {
		t.Fatal("film 406 should be back after restoring everything")
	}
}

func rowTitles(rows []Row) (out []string) {
	for _, r := range rows {
		out = append(out, r.Title)
	}
	return
}

// A Finish a Season card is a season: removing it removes its show, which
// leaves the row (and other suggestions) for good.
func TestRemoveSeasonCardRemovesShow(t *testing.T) {
	s, u := testServer(t)
	smartLibrary(t, s)
	now := db.Now()
	for _, id := range []int{321, 322} {
		if _, err := s.db.W.Exec(`INSERT INTO watch_state(user_id, item_id, position, duration, watched, play_count, updated_at) VALUES (?, ?, 0, 3000, 1, 1, ?)`, u.ID, id, now-600); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest("GET", "/api/home", nil).WithContext(auth.WithUser(context.Background(), u))
	if cards, err := s.finishSeason(r, nil); err != nil || len(cards) != 1 || cards[0].ID != 301 {
		t.Fatalf("finish a season = %+v (%v), want season 1", cards, err)
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if c := call(t, s, u, s.handleHideSuggestion, "POST", "/api/items/301/suggestions/hide", "", map[string]string{"id": "301"}, &out); c != http.StatusOK || out.ID != 300 {
		t.Fatalf("hide season = %d, title %d; want 200 and show 300", c, out.ID)
	}
	if cards, err := s.finishSeason(r, nil); err != nil || len(cards) != 0 {
		t.Fatalf("finish a season after removal = %+v (%v), want none", cards, err)
	}
	// An episode stands for its show too; the season card restores it.
	if got := s.suggestionTitle(context.Background(), 323); got != 300 {
		t.Fatalf("episode's title = %d, want 300", got)
	}
	if c := call(t, s, u, s.handleRestoreSuggestions, "DELETE", "/api/suggestions/hidden/301", "", map[string]string{"id": "301"}, nil); c != 200 {
		t.Fatalf("restore = %d", c)
	}
	if cards, _ := s.finishSeason(r, nil); len(cards) != 1 {
		t.Fatalf("finish a season after restoring = %+v, want season 1 back", cards)
	}
}
