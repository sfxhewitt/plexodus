package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/db"
)

// "Tonight" smart rows on Home. Everything here is worked out from the
// database alone (runtimes, ratings, genres, watch state): no file reads and
// no network, so Home stays fast on an rclone mount.
//
// Picks are shuffled with a seed that changes once a day, so the rows don't
// reshuffle every time Home is opened but do change from day to day.

// smartCache keeps each user's smart rows for a while: working them out
// takes tens of milliseconds on a big library, Home is opened often, and the
// picks only change daily anyway. Watching or marking something drops the
// user's entry so rows never show a stale "unwatched" title.
type smartCache struct {
	mu sync.Mutex
	m  map[int64]smartEntry
}

type smartEntry struct {
	key          string
	at           time.Time
	early, later []Row
}

const smartTTL = 10 * time.Minute

func (c *smartCache) get(uid int64, key string) (smartEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[uid]
	if !ok || e.key != key || time.Since(e.at) > smartTTL || e.at.Unix()/86400 != time.Now().Unix()/86400 {
		return e, false
	}
	return e, true
}

func (c *smartCache) put(uid int64, e smartEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[int64]smartEntry{}
	}
	c.m[uid] = e
}

// forget drops a user's cached rows (after they watch or mark something).
func (c *smartCache) forget(uid int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, uid)
}

// smartRows returns the smart rows for Home: early ones (Finish a Season,
// Because You Watched) go under Continue Watching, later ones (Under 100
// Minutes, Forgotten Gems) after the libraries. The rows are worked out in
// parallel; one failing only loses that row.
func (s *Server) smartRows(r *http.Request, hidden map[int64]bool) (early, later []Row) {
	u := currentUser(r)
	hk := make([]int64, 0, len(hidden))
	for id := range hidden {
		hk = append(hk, id)
	}
	sort.Slice(hk, func(a, b int) bool { return hk[a] < hk[b] })
	key := idList(hk)
	if e, ok := s.smart.get(u.ID, key); ok {
		return e.early, e.later
	}

	started, err := s.startedShows(r.Context(), u.ID)
	if err != nil {
		log.Printf("home: started shows: %v", err)
		return nil, nil
	}
	var season, short, gems []Card
	var because []Row
	var wg sync.WaitGroup
	run := func(name string, f func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := f(); err != nil {
				log.Printf("home: %s: %v", name, err)
			}
		}()
	}
	run("finish a season", func() (err error) { season, err = s.finishSeason(r, hidden); return })
	run("because you watched", func() (err error) { because, err = s.becauseYouWatched(r, hidden, started); return })
	run("under 100 minutes", func() (err error) { short, err = s.shortFilms(r, hidden); return })
	run("forgotten gems", func() (err error) { gems, err = s.forgottenGems(r, hidden, started); return })
	wg.Wait()

	if len(season) > 0 {
		early = append(early, Row{Title: "Finish a Season This Weekend", Kind: "poster", Items: season, Smart: "season"})
	}
	early = append(early, because...)
	if len(short) >= 6 {
		later = append(later, Row{Title: "Under 100 Minutes", Kind: "poster", Items: short, Smart: "short"})
	}
	if len(gems) >= 6 {
		later = append(later, Row{Title: "Forgotten Gems", Kind: "poster", Items: gems, Smart: "gems"})
	}
	if r.Context().Err() == nil {
		s.smart.put(u.ID, smartEntry{key: key, at: time.Now(), early: early, later: later})
	}
	return early, later
}

// shuffle is an SQL ordering expression for alias's id under seed.
func shuffle(alias string) string {
	return "((" + alias + ".id * 2654435761 + ?2) % 1000003)"
}

func daySeed(uid int64) int64 { return (db.Now()/86400)*7919 + uid*104729 }

// libWhere restricts alias to libraries the user can see and hasn't hidden
// from Home (and, for a kids profile, to titles rated for its age). Ids come from the database, so building them in is safe.
func libWhere(u auth.User, alias string, hidden map[int64]bool) string {
	out := ""
	if !u.IsAdmin && !u.AllLibraries {
		out += " AND " + alias + ".library_id IN (" + idList(u.Libraries) + ")"
	}
	if len(hidden) > 0 {
		ids := make([]int64, 0, len(hidden))
		for id := range hidden {
			ids = append(ids, id)
		}
		out += " AND " + alias + ".library_id NOT IN (" + idList(ids) + ")"
	}
	return out + kidsCond(u, alias) // a kids profile only gets titles rated for its age
}

// cardsByIDs loads cards for ids, keeping their order.
func (s *Server) cardsByIDs(r *http.Request, ids []int64) ([]Card, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	cs, err := s.cards(r, `WHERE i.id IN (`+idList(ids)+`)`)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]Card, len(cs))
	for _, c := range cs {
		byID[c.ID] = c
	}
	out := make([]Card, 0, len(cs))
	for _, id := range ids {
		if c, ok := byID[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *Server) queryIDs(ctx context.Context, q string, args ...any) ([]int64, error) {
	rows, err := s.db.R.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// startedShows is every show the user has seen or is watching: an episode
// finished or part watched now, or played at any time per watch_history
// (so a title marked unwatched later, or seen in Plex long ago, still
// counts as seen).
func (s *Server) startedShows(ctx context.Context, uid int64) ([]int64, error) {
	return s.queryIDs(ctx, `SELECT se.parent_id FROM watch_state w
		JOIN items e ON e.id = w.item_id AND e.kind = 'episode'
		JOIN items se ON se.id = e.parent_id
		WHERE w.user_id = ?1 AND (w.watched = 1 OR w.position > 0)
		UNION
		SELECT se.parent_id FROM (SELECT DISTINCT item_id FROM watch_history WHERE user_id = ?1) h
		JOIN items e ON e.id = h.item_id AND e.kind = 'episode'
		JOIN items se ON se.id = e.parent_id`, uid)
}

// unseenMovieAt is the condition that the user (?1) hasn't watched, started
// or ever played the film alias.
func unseenMovieAt(alias string) string {
	return `NOT EXISTS (SELECT 1 FROM watch_state ws WHERE ws.user_id = ?1 AND ws.item_id = ` + alias + `.id AND (ws.watched = 1 OR ws.position > 0))
		AND NOT EXISTS (SELECT 1 FROM watch_history wh WHERE wh.user_id = ?1 AND wh.item_id = ` + alias + `.id)`
}

var unwatchedMovie = unseenMovieAt("m")

// unseenAt is the condition that the user (?1) hasn't watched or started
// alias (a film or show): suggestion rows leave out anything already seen
// or in progress. started is from startedShows.
func unseenAt(alias string, started []int64) string {
	show := alias + `.kind = 'show'`
	if len(started) > 0 {
		show = `(` + alias + `.kind = 'show' AND ` + alias + `.id NOT IN (` + idList(started) + `))`
	}
	return `((` + alias + `.kind = 'movie' AND ` + unseenMovieAt(alias) + `) OR ` + show + `)`
}

// unwatched is unseenAt for the alias m used by the smart row queries.
func unwatched(started []int64) string { return unseenAt("m", started) }

// shortFilms: unwatched, decently rated films that fit in an evening.
func (s *Server) shortFilms(r *http.Request, hidden map[int64]bool) ([]Card, error) {
	u := currentUser(r)
	ids, err := s.queryIDs(r.Context(), `SELECT m.id FROM items m
		WHERE m.kind = 'movie' AND m.runtime BETWEEN 60 AND 100 AND COALESCE(m.rating, 0) >= 6.5 AND m.poster IS NOT NULL
		AND `+unwatchedMovie+libWhere(u, "m", hidden)+`
		ORDER BY `+shuffle("m")+` LIMIT 24`, u.ID, daySeed(u.ID))
	if err != nil {
		return nil, err
	}
	return s.cardsByIDs(r, ids)
}

// forgottenGems: well-rated films and shows that have sat in the library
// for months without being watched.
func (s *Server) forgottenGems(r *http.Request, hidden map[int64]bool, started []int64) ([]Card, error) {
	u := currentUser(r)
	ids, err := s.queryIDs(r.Context(), `SELECT m.id FROM items m
		WHERE m.kind IN ('movie', 'show') AND m.added_at < ?3 AND COALESCE(m.rating, 0) >= 7.5 AND m.poster IS NOT NULL
		AND `+unwatched(started)+libWhere(u, "m", hidden)+`
		ORDER BY `+shuffle("m")+` LIMIT 24`, u.ID, daySeed(u.ID)+1, db.Now()-180*86400)
	if err != nil {
		return nil, err
	}
	return s.cardsByIDs(r, ids)
}

// finishSeason: seasons the user has been watching lately that are nearly
// done, as season cards with what's left ("3 left · 2h 10m").
func (s *Server) finishSeason(r *http.Request, hidden map[int64]bool) ([]Card, error) {
	ctx := r.Context()
	u := currentUser(r)
	rows, err := s.db.R.QueryContext(ctx, `WITH recent AS (
			SELECT e.parent_id AS season, MAX(w.updated_at) AS t FROM watch_state w
			JOIN items e ON e.id = w.item_id AND e.kind = 'episode'
			WHERE w.user_id = ?1 AND w.watched = 1 AND w.updated_at > ?2
			GROUP BY e.parent_id ORDER BY t DESC LIMIT 60)
		SELECT se.id, COUNT(*) AS total,
			SUM(COALESCE(ws.watched, 0)) AS seen,
			SUM(CASE WHEN COALESCE(ws.watched, 0) = 0 THEN COALESCE(e.runtime, 0) ELSE 0 END) AS left_min
		FROM recent JOIN items se ON se.id = recent.season AND COALESCE(se.season_number, 0) > 0
		JOIN items e ON e.parent_id = se.id AND e.kind = 'episode'
		LEFT JOIN watch_state ws ON ws.user_id = ?1 AND ws.item_id = e.id
		WHERE 1 = 1`+libWhere(u, "se", hidden)+`
		GROUP BY se.id HAVING seen >= 1 AND total - seen BETWEEN 1 AND 8
		ORDER BY MAX(recent.t) DESC LIMIT 20`, u.ID, db.Now()-45*86400)
	if err != nil {
		return nil, err
	}
	type left struct{ total, seen, mins int }
	info := map[int64]left{}
	var ids []int64
	for rows.Next() {
		var id int64
		var l left
		if err := rows.Scan(&id, &l.total, &l.seen, &l.mins); err != nil {
			rows.Close()
			return nil, err
		}
		info[id] = l
		ids = append(ids, id)
	}
	rows.Close()
	cards, err := s.cardsByIDs(r, ids)
	if err != nil {
		return nil, err
	}
	for i := range cards {
		c := &cards[i]
		l := info[c.ID]
		season := c.Title
		var showTitle, poster, backdrop string
		s.db.R.QueryRowContext(ctx, `SELECT id, title, COALESCE(poster, ''), COALESCE(backdrop, '') FROM items WHERE id = ?`, c.ParentID).
			Scan(&c.ShowID, &showTitle, &poster, &backdrop)
		c.ShowTitle, c.ShowPoster, c.ShowBackdrop = showTitle, poster, backdrop
		if showTitle != "" {
			c.Title = showTitle
		}
		c.LeafCount, c.ViewedLeaves = l.total, l.seen
		n := l.total - l.seen
		note := fmt.Sprintf("%s · %d left", season, n)
		if n == 1 {
			note = season + " · last episode"
		}
		if l.mins > 0 {
			note += " · " + fmtMinutes(l.mins)
		}
		c.Note = note
	}
	return cards, nil
}

func fmtMinutes(m int) string {
	if m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	if m%60 == 0 {
		return fmt.Sprintf("%dh", m/60)
	}
	return fmt.Sprintf("%dh %dm", m/60, m%60)
}

// becauseYouWatched returns up to two rows of titles like the film and the
// show the user finished most recently: shared genres first, then a similar
// era, then rating. Each row names what it's based on.
func (s *Server) becauseYouWatched(r *http.Request, hidden map[int64]bool, started []int64) ([]Row, error) {
	ctx := r.Context()
	u := currentUser(r)
	// The latest watched films and shows (an episode stands for its show).
	rows, err := s.db.R.QueryContext(ctx, `SELECT CASE WHEN i.kind = 'episode' THEN se.parent_id ELSE i.id END FROM watch_state w
		JOIN items i ON i.id = w.item_id AND i.kind IN ('movie', 'episode')
		LEFT JOIN items se ON i.kind = 'episode' AND se.id = i.parent_id
		WHERE w.user_id = ? AND w.watched = 1 ORDER BY w.updated_at DESC LIMIT 200`, u.ID)
	if err != nil {
		return nil, err
	}
	var recent []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		recent = append(recent, id)
	}
	rows.Close()

	var out []Row
	done := map[string]bool{}
	for _, id := range recent {
		if len(done) == 2 {
			break
		}
		var kind, title, genresJSON string
		var year, lib int
		if err := s.db.R.QueryRowContext(ctx, `SELECT kind, title, COALESCE(genres, '[]'), COALESCE(year, 0), library_id FROM items WHERE id = ?`, id).
			Scan(&kind, &title, &genresJSON, &year, &lib); err != nil {
			continue
		}
		if done[kind] || hidden[int64(lib)] {
			continue
		}
		var genres []string
		json.Unmarshal([]byte(genresJSON), &genres)
		if len(genres) == 0 {
			continue
		}
		done[kind] = true
		if len(genres) > 4 {
			genres = genres[:4]
		}
		// Score: one point per shared genre, a half for a similar era.
		args := []any{u.ID, kind, id, year}
		var parts []string
		for _, g := range genres {
			args = append(args, `"`+g+`"`)
			parts = append(parts, fmt.Sprintf("(instr(m.genres, ?%d) > 0)", len(args)))
		}
		score := "(" + strings.Join(parts, " + ") + ")"
		need := min(2, len(genres))
		q := `SELECT m.id FROM items m WHERE m.kind = ?2 AND m.id != ?3 AND m.poster IS NOT NULL AND m.genres IS NOT NULL
			AND ` + score + ` >= ` + fmt.Sprint(need) + ` AND ` + unwatched(started) + libWhere(u, "m", hidden) + `
			ORDER BY ` + score + ` + (?4 > 0 AND ABS(COALESCE(m.year, 0) - ?4) <= 8) * 0.5 DESC, COALESCE(m.rating, 0) DESC LIMIT 24`
		ids, err := s.queryIDs(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		cards, err := s.cardsByIDs(r, ids)
		if err != nil {
			return nil, err
		}
		if len(cards) >= 4 {
			out = append(out, Row{Title: "Because You Watched " + title, Kind: "poster", Items: cards, Smart: "because"})
		}
	}
	return out, nil
}
