package api

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/db"
)

func TestScaleHome(t *testing.T) {
	if os.Getenv("PLEXODUS_SCALE") == "" {
		t.Skip()
	}
	s, u := scaleServer(t)
	kid := u
	age := 13
	kid.MaxAge = &age
	for _, who := range []auth.User{u, kid} {
		u := who
		for i := 0; i < 2; i++ {
			start := time.Now()
			call(t, s, u, s.handleLibraries, "GET", "/api/libraries", "", nil, nil)
			t.Logf("libraries took %s", time.Since(start))
			start = time.Now()
			call(t, s, u, s.handleHome, "GET", "/api/home", "", nil, nil)
			t.Logf("home took %s (kids=%v)", time.Since(start), u.MaxAge != nil)
		}
	}
}

// scaleServer is a library the size of Daniel's: 30k films (one in three
// in one of 4,000 TMDB collections, half without their own artwork) and
// 13k shows with 676k episodes.
//
// PLEXODUS_SCALE_DB=<file> keeps the built library there and reuses it on
// later runs, which saves the few minutes it takes to build.
func scaleServer(t *testing.T) (*Server, auth.User) {
	if keep := os.Getenv("PLEXODUS_SCALE_DB"); keep != "" {
		if _, err := os.Stat(keep); err == nil {
			d, err := db.Open(keep)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { d.Close() })
			s := &Server{db: d, auth: auth.New(d), baseCtx: context.Background()}
			_, u, err := s.auth.Login(context.Background(), "daniel", "password123")
			if err != nil {
				t.Fatal(err)
			}
			return s, u
		}
	}
	s, u := testServer(t)
	tx, _ := s.db.W.Begin()
	tx.Exec(`INSERT INTO libraries(id, name, kind, paths, created_at) VALUES (3, 'TV', 'shows', '[]', 0)`)
	now := db.Now()
	rnd := rand.New(rand.NewSource(1))
	id := int64(1000)
	genres := []string{`["Drama","Crime"]`, `["Comedy"]`, `["Action","Thriller"]`, `["Animation"]`}
	var eps, movies []int64
	for i := 0; i < 30000; i++ {
		id++
		tx.Exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, poster, backdrop, genres, rating, runtime, year, added_at, updated_at, meta_state, tmdb_id)
			VALUES (?, 1, 'movie', ?, ?, ?, '/p.jpg', '/b.jpg', ?, ?, ?, ?, ?, ?, 'matched', ?)`, id, fmt.Sprint("m", id), fmt.Sprint("Film ", i), fmt.Sprint("film ", i), genres[i%4], 5+rnd.Float64()*4, 70+rnd.Intn(90), 1950+rnd.Intn(75), now-int64(rnd.Intn(3000))*86400, now, id)
		tx.Exec(`INSERT INTO media_files(item_id, library_id, path, size, mtime) VALUES (?, 1, ?, 1000, 0)`, id, fmt.Sprint("/m/", id))
		movies = append(movies, id)
	}
	for sh := 0; sh < 13000; sh++ {
		id++
		show := id
		tx.Exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, poster, backdrop, genres, rating, added_at, updated_at, meta_state, tmdb_id)
			VALUES (?, 3, 'show', ?, ?, ?, '/p.jpg', '/b.jpg', ?, ?, ?, ?, 'matched', ?)`, show, fmt.Sprint("s", show), fmt.Sprint("Show ", sh), fmt.Sprint("show ", sh), genres[sh%4], 5+rnd.Float64()*4, now-int64(rnd.Intn(3000))*86400, now, id)
		for se := 1; se <= 4; se++ {
			id++
			season := id
			tx.Exec(`INSERT INTO items(id, library_id, kind, guid, parent_id, title, sort_title, season_number, added_at, updated_at) VALUES (?, 3, 'season', ?, ?, ?, ?, ?, 0, 0)`, season, fmt.Sprint("x", season), show, fmt.Sprint("Season ", se), "s", se)
			for e := 1; e <= 13; e++ {
				id++
				tx.Exec(`INSERT INTO items(id, library_id, kind, guid, parent_id, title, sort_title, season_number, episode_number, runtime, added_at, updated_at) VALUES (?, 3, 'episode', ?, ?, 'Ep', 'ep', ?, ?, 45, 0, 0)`, id, fmt.Sprint("e", id), season, se, e)
				tx.Exec(`INSERT INTO media_files(item_id, library_id, path, size, mtime) VALUES (?, 3, ?, 1000, 0)`, id, fmt.Sprint("/e/", id))
				eps = append(eps, id)
			}
		}
	}
	for i := 0; i < 15000; i++ {
		it := eps[rnd.Intn(len(eps))]
		if i%3 == 0 {
			it = movies[rnd.Intn(len(movies))]
		}
		at := now - int64(rnd.Intn(800))*86400
		tx.Exec(`INSERT OR REPLACE INTO watch_state(user_id, item_id, position, duration, watched, play_count, updated_at) VALUES (?, ?, 0, 3000, 1, 1, ?)`, u.ID, it, at)
		tx.Exec(`INSERT INTO watch_history(user_id, item_id, watched_at, seconds, source) VALUES (?, ?, ?, 3000, 'plex')`, u.ID, it, at)
	}
	for c := 1; c <= 4000; c++ {
		var poster any
		if c%2 == 0 {
			poster = "/c.jpg"
		}
		tx.Exec(`INSERT INTO collections(id, name, sort_name, poster, backdrop, updated_at) VALUES (?, ?, ?, ?, ?, 0)`, c, fmt.Sprint("Saga ", c), fmt.Sprint("saga ", c), poster, poster)
	}
	for i, m := range movies {
		if i%3 == 0 {
			tx.Exec(`UPDATE items SET collection_id = ? WHERE id = ?`, 1+rnd.Intn(4000), m)
		} else if i%3 == 1 {
			tx.Exec(`UPDATE items SET collection_id = 0 WHERE id = ?`, m)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	s.db.W.Exec(`UPDATE items SET age = (id % 4) * 6 WHERE kind IN ('movie','show')`)
	s.db.W.Exec(`ANALYZE`)
	t.Logf("built %d items", id)
	if keep := os.Getenv("PLEXODUS_SCALE_DB"); keep != "" {
		if _, err := s.db.W.Exec(`VACUUM INTO ?`, keep); err != nil {
			t.Fatal(err)
		}
	}
	return s, u
}

func TestScaleCollections(t *testing.T) {
	if os.Getenv("PLEXODUS_SCALE") == "" {
		t.Skip()
	}
	s, u := scaleServer(t)
	kid := u
	age := 13
	kid.MaxAge = &age
	for _, who := range []auth.User{u, kid} {
		u := who
		for i := 0; i < 2; i++ {
			start := time.Now()
			var out struct {
				Collections []CollectionCard
				Pending     int
			}
			call(t, s, u, s.handleCollections, "GET", "/api/collections", "", nil, &out)
			t.Logf("collections took %s: %d collections, %d pending (kids=%v)", time.Since(start), len(out.Collections), out.Pending, u.MaxAge != nil)
		}
	}
}
