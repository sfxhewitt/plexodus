package db

import (
	"path/filepath"
	"testing"
)

// Plays from before watch_state.played_at existed reach History once:
// rows with a resume point or a length, and finished plays in
// watch_history; rows marked watched by hand don't.
func TestSeedPlayedAt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	d, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO users(id, username, password_hash, is_admin, created_at) VALUES (1, 'a', 'x', 1, 0)`,
		`INSERT INTO libraries(id, name, kind, paths, created_at) VALUES (1, 'M', 'movies', '[]', 0)`,
		`INSERT INTO items(id, library_id, kind, guid, title, sort_title, added_at, updated_at) VALUES
			(1, 1, 'movie', 'a', 'A', 'a', 0, 0), (2, 1, 'movie', 'b', 'B', 'b', 0, 0), (3, 1, 'movie', 'c', 'C', 'c', 0, 0)`,
		`INSERT INTO watch_state(user_id, item_id, position, duration, watched, updated_at) VALUES
			(1, 1, 30, 6000, 0, 100), (1, 2, 0, 0, 1, 200), (1, 3, 0, 0, 1, 300)`,
		`INSERT INTO watch_history(user_id, item_id, watched_at) VALUES (1, 3, 250)`,
		// As if the column had just been added.
		`UPDATE watch_state SET played_at = 0`,
		`DELETE FROM settings WHERE key = 'played_at_seeded'`,
	} {
		if _, err := d.W.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	d.Close()
	if d, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	want := map[int64]int64{1: 100, 2: 0, 3: 250}
	for id, w := range want {
		var got int64
		d.R.QueryRow(`SELECT played_at FROM watch_state WHERE item_id = ?`, id).Scan(&got)
		if got != w {
			t.Errorf("item %d played_at = %d, want %d", id, got, w)
		}
	}
}
