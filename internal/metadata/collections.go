package metadata

import (
	"context"
	"fmt"
	"log"
	"net/url"
	"strings"
	"unicode"

	"github.com/sfxhewitt/plexodus/internal/db"
)

// Collection is TMDB's belongs_to_collection on a film.
type Collection struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	PosterPath   string `json:"poster_path"`
	BackdropPath string `json:"backdrop_path"`
}

// MovieCollection asks TMDB which collection a film is in (nil for none).
func (t *TMDB) MovieCollection(ctx context.Context, id int64) (*Collection, error) {
	var m struct {
		Collection *Collection `json:"belongs_to_collection"`
	}
	err := t.get(ctx, fmt.Sprintf("/movie/%d", id), nil, &m)
	return m.Collection, err
}

// setCollection records a film's collection (or that it has none).
func (a *Agent) setCollection(ctx context.Context, itemID int64, c *Collection) {
	if c == nil || c.ID == 0 || c.Name == "" {
		a.db.W.ExecContext(ctx, `UPDATE items SET collection_id = 0 WHERE id = ?`, itemID)
		return
	}
	a.db.W.ExecContext(ctx, `INSERT INTO collections(id, name, sort_name, poster, backdrop, updated_at) VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, sort_name = excluded.sort_name,
			poster = COALESCE(excluded.poster, poster), backdrop = COALESCE(excluded.backdrop, backdrop), updated_at = excluded.updated_at`,
		c.ID, c.Name, sortTitle(c.Name), nullStr(c.PosterPath), nullStr(c.BackdropPath), db.Now())
	a.db.W.ExecContext(ctx, `UPDATE items SET collection_id = ? WHERE id = ?`, c.ID, itemID)
	a.Art.Prefetch(c.PosterPath, "w342")
}

// backfillCollections asks TMDB about films that haven't been checked yet,
// a batch at a time, newest first. About 30,000 films take a few hours at
// the shared TMDB rate limit, in the background.
func (a *Agent) backfillCollections(ctx context.Context) int {
	rows, err := a.db.R.QueryContext(ctx, `SELECT id, tmdb_id FROM items
		WHERE kind = 'movie' AND tmdb_id IS NOT NULL AND tmdb_id > 0 AND collection_id IS NULL
		ORDER BY added_at DESC LIMIT 40`)
	if err != nil {
		return 0
	}
	type todo struct{ id, tmdb int64 }
	var list []todo
	for rows.Next() {
		var t todo
		rows.Scan(&t.id, &t.tmdb)
		list = append(list, t)
	}
	rows.Close()
	t := a.TMDB()
	type res struct {
		id  int64
		c   *Collection
		err error
	}
	out := make(chan res, len(list))
	sem := make(chan struct{}, 4)
	for _, it := range list {
		sem <- struct{}{}
		go func(it todo) {
			defer func() { <-sem }()
			c, err := t.MovieCollection(ctx, it.tmdb)
			out <- res{it.id, c, err}
		}(it)
	}
	done := 0
	for range list {
		r := <-out
		if r.err != nil {
			if ctx.Err() == nil {
				log.Printf("metadata: collection for item %d: %v", r.id, r.err)
				// A film TMDB no longer has: don't ask again. Anything else
				// (network, server, rate limit) is asked again next run.
				if strings.Contains(r.err.Error(), ": 404 ") {
					a.db.W.ExecContext(ctx, `UPDATE items SET collection_id = 0 WHERE id = ? AND collection_id IS NULL`, r.id)
				}
			}
			continue
		}
		a.setCollection(ctx, r.id, r.c)
		done++
	}
	return done
}

// CollectionDetails is TMDB's /collection/{id}.
type CollectionDetails struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	PosterPath   string `json:"poster_path"`
	BackdropPath string `json:"backdrop_path"`
}

func (t *TMDB) CollectionDetails(ctx context.Context, id int64) (*CollectionDetails, error) {
	var c CollectionDetails
	err := t.get(ctx, fmt.Sprintf("/collection/%d", id), nil, &c)
	return &c, err
}

// FindCollection finds the TMDB collection with this name ("Alien",
// "The Alien Collection" and "Alien Saga" all find "Alien Collection"), or
// nil when no collection is clearly the same one.
func (t *TMDB) FindCollection(ctx context.Context, name string) (*CollectionDetails, error) {
	want := collectionWords(name)
	if want == "" {
		return nil, nil
	}
	var out struct {
		Results []CollectionDetails `json:"results"`
	}
	if err := t.get(ctx, "/search/collection", url.Values{"query": {name}}, &out); err != nil {
		return nil, err
	}
	for i := range out.Results {
		if collectionWords(out.Results[i].Name) == want {
			return &out.Results[i], nil
		}
	}
	return nil, nil
}

// collectionWords is a collection name reduced for comparing: lower case,
// letters and digits only, without "the" or words like "collection".
func collectionWords(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	var words []string
	for _, w := range strings.Fields(b.String()) {
		switch w {
		case "the", "collection", "collections", "saga", "series", "franchise", "trilogy", "films", "movies":
			continue
		}
		words = append(words, w)
	}
	return strings.Join(words, " ")
}

// collectionArt finds artwork for collections that have none of their own:
// TMDB's collection page for franchises whose films didn't carry it, and a
// TMDB search by name for Plex collections without a poster. Each is asked
// at most once a week.
func (a *Agent) collectionArt(ctx context.Context) int {
	week := db.Now() - 7*24*3600
	rows, err := a.db.R.QueryContext(ctx, `SELECT id, name, source FROM collections
		WHERE (poster IS NULL OR backdrop IS NULL) AND COALESCE(art_checked_at, 0) < ?
		ORDER BY art_checked_at IS NOT NULL, id LIMIT 40`, week)
	if err != nil {
		return 0
	}
	type todo struct {
		id           int64
		name, source string
	}
	var list []todo
	for rows.Next() {
		var t todo
		rows.Scan(&t.id, &t.name, &t.source)
		list = append(list, t)
	}
	rows.Close()
	t := a.TMDB()
	done := 0
	for _, c := range list {
		if ctx.Err() != nil {
			return done
		}
		var found *CollectionDetails
		var err error
		if c.source == "plex" {
			found, err = t.FindCollection(ctx, c.name)
		} else {
			found, err = t.CollectionDetails(ctx, c.id)
		}
		if err != nil && !strings.Contains(err.Error(), ": 404 ") {
			log.Printf("metadata: artwork for collection %q: %v", c.name, err)
			continue
		}
		poster, backdrop := "", ""
		if found != nil {
			poster, backdrop = found.PosterPath, found.BackdropPath
		}
		a.db.W.ExecContext(ctx, `UPDATE collections SET poster = COALESCE(poster, ?), backdrop = COALESCE(backdrop, ?), art_checked_at = ? WHERE id = ?`,
			nullStr(poster), nullStr(backdrop), db.Now(), c.id)
		if poster != "" {
			a.Art.Prefetch(poster, "w342")
		}
		done++
	}
	return done
}
