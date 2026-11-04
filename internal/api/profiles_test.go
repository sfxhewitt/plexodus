package api

import (
	"context"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/db"
)

func TestProfilesAndKidsMode(t *testing.T) {
	s, admin := testServer(t)
	ctx := context.Background()
	now := db.Now()
	// Heat is R, Akira unrated; add a G film and a TV-Y show with an episode.
	for _, q := range []string{
		`UPDATE items SET content_rating = 'R', age = 17 WHERE id = 10`,
		`INSERT INTO items(id, library_id, kind, guid, title, sort_title, poster, backdrop, overview, rating, added_at, updated_at, content_rating, age) VALUES
			(30, 1, 'movie', 'm30', 'Toy Story', 'toy story', '/p.jpg', '/b.jpg', 'x', 8, ?1, ?1, 'G', 0),
			(40, 1, 'show', 's40', 'Bluey', 'bluey', '/p.jpg', '/b.jpg', 'x', 8, ?1, ?1, 'TV-Y', 0)`,
		`INSERT INTO items(id, library_id, kind, guid, parent_id, title, sort_title, season_number, added_at, updated_at) VALUES
			(41, 1, 'season', 's41', 40, 'Season 1', 'season 1', 1, ?1, ?1)`,
		`INSERT INTO items(id, library_id, kind, guid, parent_id, title, sort_title, season_number, episode_number, added_at, updated_at) VALUES
			(42, 1, 'episode', 'e42', 41, 'Magic Xylophone', 'magic', 1, 1, ?1, ?1)`,
	} {
		if _, err := s.db.W.Exec(q, now); err != nil {
			t.Fatal(err)
		}
	}

	tok, _, err := s.auth.Login(ctx, "daniel", "password123")
	if err != nil {
		t.Fatal(err)
	}
	str := func(v string) *string { return &v }
	yes := true
	age := 8
	kid, err := s.auth.CreateProfile(ctx, admin.ID, auth.ProfileInput{Name: str("Mia"), Avatar: str("🦊"), Kids: &yes, MaxAge: &age})
	if err != nil {
		t.Fatal(err)
	}
	if !kid.Kids || kid.MaxAge == nil || *kid.MaxAge != 8 {
		t.Fatalf("kid profile = %+v", kid)
	}
	// Lock the main profile with a PIN.
	if err := s.auth.UpdateProfile(ctx, admin.ID, admin.ID, auth.ProfileInput{PIN: str("1234")}); err != nil {
		t.Fatal(err)
	}
	if err := s.auth.UpdateProfile(ctx, admin.ID, admin.ID, auth.ProfileInput{Kids: &yes}); err == nil {
		t.Fatal("the main profile must not become a kids profile")
	}
	// Profiles can't sign in.
	if _, _, err := s.auth.Login(ctx, "Mia", ""); err == nil {
		t.Fatal("profile signed in")
	}

	me, _ := s.auth.Lookup(ctx, tok)
	// The account switches to its kids profile without a PIN.
	k, err := s.auth.SwitchProfile(ctx, tok, me, kid.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if k.ID != kid.ID || !k.Kids || k.IsAdmin || k.AccountID != admin.ID || k.CanManageProfiles() {
		t.Fatalf("switched user = %+v", k)
	}

	// Kids see only rated, suitable titles, including the show's episodes.
	var page struct {
		Items []Card `json:"items"`
		Total int    `json:"total"`
	}
	call(t, s, k, s.handleLibraryItems, "GET", "/api/libraries/1/items", "", map[string]string{"id": "1"}, &page)
	if page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("kids library = %d %+v", page.Total, page.Items)
	}
	for id, want := range map[int64]bool{10: false, 20: false, 30: true, 40: true, 41: true, 42: true} {
		if got := s.canView(ctx, k, id); got != want {
			t.Errorf("kids canView(%d) = %v, want %v", id, got, want)
		}
		if !s.canView(ctx, me, id) {
			t.Errorf("account can't see %d", id)
		}
	}
	var search map[string][]Card
	call(t, s, k, s.handleSearch, "GET", "/api/search?q=heat", "", nil, &search)
	if len(search["movies"]) != 0 {
		t.Fatalf("kids search found %+v", search["movies"])
	}
	// A kids profile can't manage profiles.
	if code := call(t, s, k, s.handleCreateProfile, "POST", "/api/profiles", `{"name":"x"}`, nil, nil); code != 403 {
		t.Fatalf("kid created a profile: %d", code)
	}
	// Getting back to the main profile needs its PIN.
	if _, err := s.auth.SwitchProfile(ctx, tok, k, admin.ID, "0000"); err != auth.ErrPIN {
		t.Fatalf("wrong PIN: %v", err)
	}
	back, err := s.auth.SwitchProfile(ctx, tok, k, admin.ID, "1234")
	if err != nil || back.ID != admin.ID || !back.IsAdmin {
		t.Fatalf("switch back = %+v, %v", back, err)
	}

	// Watch state is per profile.
	call(t, s, k, s.handleWatched, "POST", "/api/items/30/watched", `{"watched":true}`, map[string]string{"id": "30"}, nil)
	var n int
	s.db.R.QueryRow(`SELECT COUNT(*) FROM watch_state WHERE user_id = ? AND watched = 1`, admin.ID).Scan(&n)
	if n != 0 {
		t.Fatal("kid's watch state leaked to the account")
	}

	// Profiles stay out of the admin user list, and deleting one keeps the
	// session working on the account.
	var users []auth.User
	call(t, s, admin, s.handleUsers, "GET", "/api/users", "", nil, &users)
	if len(users) != 1 {
		t.Fatalf("users = %+v", users)
	}
	s.auth.SwitchProfile(ctx, tok, back, kid.ID, "")
	if err := s.auth.DeleteProfile(ctx, admin.ID, kid.ID); err != nil {
		t.Fatal(err)
	}
	if u, ok := s.auth.Lookup(ctx, tok); !ok || u.ID != admin.ID {
		t.Fatalf("after delete = %+v %v", u, ok)
	}
	// Stream links signed for a profile re-check with the account's access.
	if u, err := s.auth.ByID(ctx, admin.ID); err != nil || !u.IsAdmin {
		t.Fatalf("ByID = %+v %v", u, err)
	}
}
