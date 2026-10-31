package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/sfxhewitt/plexodus/internal/db"
)

// Agent matches pending items against TMDB in the background.
type Agent struct {
	db   *db.DB
	keys keyHolder
	Art  *ArtCache
	kick chan struct{}
}

func NewAgent(d *db.DB, key string, art *ArtCache) *Agent {
	a := &Agent{db: d, Art: art, kick: make(chan struct{}, 1)}
	a.keys.set(key)
	return a
}

func (a *Agent) SetKey(key string) {
	a.keys.set(key)
	a.Kick()
}

func (a *Agent) Enabled() bool { return a.keys.get().Enabled() }

func (a *Agent) TMDB() *TMDB { return a.keys.get() }

func (a *Agent) Kick() {
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

func (a *Agent) Run(ctx context.Context) {
	for {
		n := 0
		if a.Enabled() {
			n = a.pass(ctx)
		}
		if ctx.Err() != nil {
			return
		}
		if n > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-a.kick:
		case <-time.After(15 * time.Minute):
		}
	}
}

type pendingItem struct {
	id    int64
	kind  string
	title string
	year  int
}

// pass handles one batch and returns how many items it changed.
func (a *Agent) pass(ctx context.Context) int {
	rows, err := a.db.R.QueryContext(ctx, `SELECT id, kind, title, COALESCE(year, 0) FROM items
		WHERE meta_state = 'pending' AND kind IN ('movie', 'show') ORDER BY added_at DESC LIMIT 50`)
	if err != nil {
		log.Printf("metadata: %v", err)
		return 0
	}
	var items []pendingItem
	for rows.Next() {
		var p pendingItem
		rows.Scan(&p.id, &p.kind, &p.title, &p.year)
		items = append(items, p)
	}
	rows.Close()

	done := 0
	sem := make(chan struct{}, 4)
	results := make(chan bool, len(items))
	for _, it := range items {
		sem <- struct{}{}
		go func(it pendingItem) {
			defer func() { <-sem }()
			results <- a.matchItem(ctx, it)
		}(it)
	}
	for range items {
		if <-results {
			done++
		}
	}

	// New seasons/episodes of shows that are already matched.
	srows, err := a.db.R.QueryContext(ctx, `SELECT DISTINCT s.id, s.tmdb_id FROM items e
		JOIN items s ON s.id = (CASE WHEN e.kind = 'season' THEN e.parent_id
			ELSE (SELECT parent_id FROM items WHERE id = e.parent_id) END)
		WHERE e.meta_state = 'pending' AND e.kind IN ('season', 'episode')
			AND s.meta_state = 'matched' AND s.tmdb_id IS NOT NULL LIMIT 50`)
	if err == nil {
		type show struct{ id, tmdb int64 }
		var shows []show
		for srows.Next() {
			var s show
			srows.Scan(&s.id, &s.tmdb)
			shows = append(shows, s)
		}
		srows.Close()
		for _, s := range shows {
			if err := a.updateSeasons(ctx, s.id, s.tmdb, true); err != nil {
				log.Printf("metadata: seasons for show %d: %v", s.id, err)
				// Avoid retrying forever on a broken show.
				a.db.W.ExecContext(ctx, `UPDATE items SET meta_state = 'error' WHERE meta_state = 'pending'
					AND (parent_id = ? OR parent_id IN (SELECT id FROM items WHERE parent_id = ?))`, s.id, s.id)
			}
			done++
		}
	}
	return done
}

func (a *Agent) matchItem(ctx context.Context, it pendingItem) bool {
	t := a.TMDB()
	kind := "movie"
	if it.kind == "show" {
		kind = "tv"
	}
	results, err := t.Search(ctx, kind, it.title, it.year)
	if err == nil && len(results) == 0 && it.year > 0 {
		// Years in filenames are often off by one; retry without it.
		results, err = t.Search(ctx, kind, it.title, 0)
		results = preferYear(results, it.year)
	}
	if err != nil {
		if ctx.Err() == nil {
			log.Printf("metadata: search %q: %v", it.title, err)
			a.setState(ctx, it.id, "error")
		}
		return true
	}
	if len(results) == 0 {
		a.setState(ctx, it.id, "unmatched")
		return true
	}
	if err := a.Apply(ctx, it.id, it.kind, results[0].ID); err != nil {
		log.Printf("metadata: apply %q: %v", it.title, err)
		a.setState(ctx, it.id, "error")
	}
	return true
}

func preferYear(rs []SearchResult, year int) []SearchResult {
	for i, r := range rs {
		y, _ := strconv.Atoi(r.Year())
		if y != 0 && (y == year || y == year-1 || y == year+1) {
			return append([]SearchResult{r}, append(rs[:i:i], rs[i+1:]...)...)
		}
	}
	return rs
}

func (a *Agent) setState(ctx context.Context, id int64, state string) {
	a.db.W.ExecContext(ctx, `UPDATE items SET meta_state = ?, meta_at = ? WHERE id = ?`, state, db.Now(), id)
}

func genresJSON(gs []Genre) string {
	names := make([]string, 0, len(gs))
	for _, g := range gs {
		names = append(names, g.Name)
	}
	b, _ := json.Marshal(names)
	return string(b)
}

func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func yearOf(date string) sql.NullInt64 {
	if len(date) >= 4 {
		if y, err := strconv.Atoi(date[:4]); err == nil {
			return sql.NullInt64{Int64: int64(y), Valid: true}
		}
	}
	return sql.NullInt64{}
}

// Apply fetches full details for a TMDB id and stores them on the item.
// It is also used for manual "fix match".
func (a *Agent) Apply(ctx context.Context, itemID int64, kind string, tmdbID int64) error {
	t := a.TMDB()
	if !t.Enabled() {
		return errors.New("TMDB API key not configured")
	}
	switch kind {
	case "movie":
		m, err := t.Movie(ctx, tmdbID)
		if err != nil {
			return err
		}
		_, err = a.db.W.ExecContext(ctx, `UPDATE items SET title = ?, sort_title = ?, original_title = ?, year = COALESCE(?, year),
			overview = ?, tagline = ?, genres = ?, rating = ?, runtime = ?, release_date = ?, tmdb_id = ?, imdb_id = ?,
			poster = ?, backdrop = ?, logo = ?, meta_state = 'matched', meta_at = ?, updated_at = ? WHERE id = ?`,
			m.Title, sortTitle(m.Title), nullStr(m.OriginalTitle), yearOf(m.ReleaseDate), nullStr(m.Overview), nullStr(m.Tagline),
			genresJSON(m.Genres), m.VoteAverage, m.Runtime, nullStr(m.ReleaseDate), m.ID, nullStr(m.ImdbID),
			nullStr(m.PosterPath), nullStr(m.BackdropPath), nullStr(m.Images.BestLogo()), db.Now(), db.Now(), itemID)
		if err == nil {
			a.Art.Prefetch(m.PosterPath, "w342")
			a.Art.Prefetch(m.BackdropPath, "w1280")
		}
		return err
	case "show":
		s, err := t.Show(ctx, tmdbID)
		if err != nil {
			return err
		}
		runtime := 0
		if len(s.EpisodeRunTime) > 0 {
			runtime = s.EpisodeRunTime[0]
		}
		_, err = a.db.W.ExecContext(ctx, `UPDATE items SET title = ?, sort_title = ?, original_title = ?, year = COALESCE(?, year),
			overview = ?, tagline = ?, genres = ?, rating = ?, runtime = ?, release_date = ?, tmdb_id = ?, imdb_id = ?,
			poster = ?, backdrop = ?, logo = ?, meta_state = 'matched', meta_at = ?, updated_at = ? WHERE id = ?`,
			s.Name, sortTitle(s.Name), nullStr(s.OriginalName), yearOf(s.FirstAirDate), nullStr(s.Overview), nullStr(s.Tagline),
			genresJSON(s.Genres), s.VoteAverage, runtime, nullStr(s.FirstAirDate), s.ID, nullStr(s.ExternalIDs.ImdbID),
			nullStr(s.PosterPath), nullStr(s.BackdropPath), nullStr(s.Images.BestLogo()), db.Now(), db.Now(), itemID)
		if err != nil {
			return err
		}
		a.Art.Prefetch(s.PosterPath, "w342")
		a.Art.Prefetch(s.BackdropPath, "w1280")
		return a.updateSeasons(ctx, itemID, s.ID, false)
	}
	return errors.New("only movies and shows can be matched")
}

// updateSeasons fills season and episode metadata for a matched show.
func (a *Agent) updateSeasons(ctx context.Context, showID, tmdbID int64, onlyPending bool) error {
	q := `SELECT id, season_number FROM items WHERE parent_id = ? AND kind = 'season'`
	if onlyPending {
		q += ` AND (meta_state = 'pending' OR EXISTS (SELECT 1 FROM items e WHERE e.parent_id = items.id AND e.meta_state = 'pending'))`
	}
	rows, err := a.db.R.QueryContext(ctx, q, showID)
	if err != nil {
		return err
	}
	type season struct {
		id int64
		n  int
	}
	var seasons []season
	for rows.Next() {
		var s season
		rows.Scan(&s.id, &s.n)
		seasons = append(seasons, s)
	}
	rows.Close()

	t := a.TMDB()
	for _, se := range seasons {
		sd, err := t.Season(ctx, tmdbID, se.n)
		if err != nil {
			if strings.Contains(err.Error(), "404") {
				a.db.W.ExecContext(ctx, `UPDATE items SET meta_state = 'unmatched' WHERE id = ? OR parent_id = ?`, se.id, se.id)
				continue
			}
			return err
		}
		err = a.db.Tx(ctx, func(tx *sql.Tx) error {
			title := sd.Name
			if title == "" {
				title = "Season " + strconv.Itoa(se.n)
			}
			if _, err := tx.Exec(`UPDATE items SET title = ?, overview = ?, poster = ?, release_date = ?, year = ?,
				meta_state = 'matched', meta_at = ? WHERE id = ?`,
				title, nullStr(sd.Overview), nullStr(sd.PosterPath), nullStr(sd.AirDate), yearOf(sd.AirDate), db.Now(), se.id); err != nil {
				return err
			}
			for _, e := range sd.Episodes {
				if _, err := tx.Exec(`UPDATE items SET title = ?, sort_title = ?, overview = ?, backdrop = ?, release_date = ?,
					runtime = ?, rating = ?, meta_state = 'matched', meta_at = ? WHERE parent_id = ? AND episode_number = ?`,
					e.Name, sortTitle(e.Name), nullStr(e.Overview), nullStr(e.StillPath), nullStr(e.AirDate),
					e.Runtime, e.VoteAverage, db.Now(), se.id, e.EpisodeNumber); err != nil {
					return err
				}
			}
			// Episodes TMDB doesn't know about keep their parsed names.
			_, err := tx.Exec(`UPDATE items SET meta_state = 'unmatched' WHERE parent_id = ? AND meta_state = 'pending'`, se.id)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func sortTitle(t string) string {
	l := strings.ToLower(t)
	for _, p := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(l, p) && len(l) > len(p) {
			return l[len(p):]
		}
	}
	return l
}
