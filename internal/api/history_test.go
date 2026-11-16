package api

import (
	"context"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/auth"
)

// History lists every film or episode played, however briefly, newest
// first and per profile; marking watched by hand doesn't add to it.
func TestHistory(t *testing.T) {
	s, u := testServer(t)
	ids := func(who auth.User, query string) []int64 {
		t.Helper()
		var h struct {
			Items []HistoryCard `json:"items"`
			More  bool          `json:"more"`
		}
		call(t, s, who, s.handleHistory, "GET", "/api/history"+query, "", nil, &h)
		var out []int64
		for _, c := range h.Items {
			if c.PlayedAt == 0 {
				t.Fatalf("card %d has no playedAt", c.ID)
			}
			out = append(out, c.ID)
		}
		return out
	}
	progress := func(who auth.User, id, body string) {
		t.Helper()
		if code := call(t, s, who, s.handleProgress, "POST", "/api/items/"+id+"/progress", body, map[string]string{"id": id}, nil); code != 200 {
			t.Fatalf("progress %s = %d", id, code)
		}
	}

	// A few seconds of Heat, then Akira marked watched by hand.
	progress(u, "10", `{"position": 12, "duration": 6000}`)
	call(t, s, u, s.handleWatched, "POST", "/api/items/20/watched", `{"watched":true}`, map[string]string{"id": "20"}, nil)
	if got := ids(u, ""); len(got) != 1 || got[0] != 10 {
		t.Fatalf("history = %v, want [10]", got)
	}
	var pos float64
	s.db.R.QueryRow(`SELECT position FROM watch_state WHERE user_id = ? AND item_id = 10`, u.ID).Scan(&pos)
	if pos != 12 {
		t.Fatalf("resume point = %v", pos)
	}

	// Play Akira later: it goes on top.
	s.db.W.Exec(`UPDATE watch_state SET played_at = played_at - 100 WHERE item_id = 10`)
	progress(u, "20", `{"position": 3, "duration": 6000}`)
	if got := ids(u, ""); len(got) != 2 || got[0] != 20 || got[1] != 10 {
		t.Fatalf("history = %v, want [20 10]", got)
	}
	// Paging.
	if got := ids(u, "?limit=1"); len(got) != 1 || got[0] != 20 {
		t.Fatalf("page 1 = %v", got)
	}
	var top int64
	s.db.R.QueryRow(`SELECT played_at FROM watch_state WHERE user_id = ? AND item_id = 20`, u.ID).Scan(&top)
	if got := ids(u, "?limit=1&before="+itoa(top)); len(got) != 1 || got[0] != 10 {
		t.Fatalf("page 2 = %v", got)
	}

	// Another profile has its own history.
	ctx := context.Background()
	name := "Mia"
	p, err := s.auth.CreateProfile(ctx, u.ID, auth.ProfileInput{Name: &name})
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := s.auth.Login(ctx, "daniel", "password123")
	if err != nil {
		t.Fatal(err)
	}
	me, _ := s.auth.Lookup(ctx, tok)
	mia, err := s.auth.SwitchProfile(ctx, tok, me, p.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(mia, ""); len(got) != 0 {
		t.Fatalf("profile history = %v, want none", got)
	}

	progress(mia, "10", `{"position": 40, "duration": 6000}`)
	if got := ids(mia, ""); len(got) != 1 || got[0] != 10 {
		t.Fatalf("profile history = %v, want [10]", got)
	}

	// Remove from History keeps the resume point.
	call(t, s, u, s.handleHistoryRemove, "DELETE", "/api/history/10", "", map[string]string{"id": "10"}, nil)
	if got := ids(u, ""); len(got) != 1 || got[0] != 20 {
		t.Fatalf("after remove = %v", got)
	}
	s.db.R.QueryRow(`SELECT position FROM watch_state WHERE user_id = ? AND item_id = 10`, u.ID).Scan(&pos)
	if pos != 12 {
		t.Fatalf("resume point after remove = %v", pos)
	}
}
