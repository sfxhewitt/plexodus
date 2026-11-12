package api

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/auth"
)

// A friend can delete their own account (store policy); admins can't delete themselves.
func TestDeleteMe(t *testing.T) {
	s, admin := testServer(t)
	ctx := context.Background()
	friend, err := s.auth.CreateUser(ctx, "friend", "password123", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.W.Exec(`INSERT INTO watch_state(user_id, item_id, position, duration, updated_at) VALUES(?, 10, 5, 100, 0)`, friend.ID); err != nil {
		t.Fatal(err)
	}
	del := func(u auth.User) int {
		r := httptest.NewRequest("DELETE", "/api/me", nil).WithContext(auth.WithUser(ctx, u))
		w := httptest.NewRecorder()
		s.handleDeleteMe(w, r)
		return w.Code
	}
	if c := del(admin); c != 403 {
		t.Fatalf("admin deleting itself: %d, want 403", c)
	}
	if c := del(friend); c != 200 {
		t.Fatalf("friend deleting itself: %d", c)
	}
	var n int
	s.db.R.QueryRow(`SELECT COUNT(*) FROM users WHERE id = ?`, friend.ID).Scan(&n)
	if n != 0 {
		t.Fatal("account still there")
	}
	s.db.R.QueryRow(`SELECT COUNT(*) FROM watch_state WHERE user_id = ?`, friend.ID).Scan(&n)
	if n != 0 {
		t.Fatal("watch state kept")
	}
	s.db.R.QueryRow(`SELECT COUNT(*) FROM users WHERE id = ?`, admin.ID).Scan(&n)
	if n != 1 {
		t.Fatal("admin gone")
	}
}
