package api

import (
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
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	s.db.W.Exec(`ANALYZE`)
	t.Logf("built %d items", id)
	s.db.W.Exec(`UPDATE items SET age = (id % 4) * 6 WHERE kind IN ('movie','show')`)
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
