package metadata

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/sfxhewitt/plexodus/internal/rating"
)

// Content ratings (certifications) from TMDB, for kids profiles.

// ReleaseDates is TMDB's movie release_dates block.
type ReleaseDates struct {
	Results []struct {
		Country string `json:"iso_3166_1"`
		Dates   []struct {
			Certification string `json:"certification"`
		} `json:"release_dates"`
	} `json:"results"`
}

// ContentRatings is TMDB's TV content_ratings block.
type ContentRatings struct {
	Results []struct {
		Country string `json:"iso_3166_1"`
		Rating  string `json:"rating"`
	} `json:"results"`
}

// certPreference: US first (Plex uses US ratings unprefixed), then UK.
var certPreference = []string{"US", "GB", "CA", "AU", "IE", "NZ"}

func pickCert(byCountry map[string]string) string {
	for _, c := range certPreference {
		if v := byCountry[c]; v != "" {
			if _, ok := rating.Age(v); ok {
				if c == "US" {
					return v
				}
				return strings.ToLower(c) + "/" + v
			}
		}
	}
	for c, v := range byCountry {
		if _, ok := rating.Age(v); ok {
			return strings.ToLower(c) + "/" + v
		}
	}
	return ""
}

func (r ReleaseDates) Cert() string {
	m := map[string]string{}
	for _, res := range r.Results {
		for _, d := range res.Dates {
			if c := strings.TrimSpace(d.Certification); c != "" && m[res.Country] == "" {
				m[res.Country] = c
			}
		}
	}
	return pickCert(m)
}

func (r ContentRatings) Cert() string {
	m := map[string]string{}
	for _, res := range r.Results {
		if c := strings.TrimSpace(res.Rating); c != "" {
			m[res.Country] = c
		}
	}
	return pickCert(m)
}

func (t *TMDB) MovieCert(ctx context.Context, id int64) (string, error) {
	var r ReleaseDates
	err := t.get(ctx, fmt.Sprintf("/movie/%d/release_dates", id), nil, &r)
	return r.Cert(), err
}

func (t *TMDB) ShowCert(ctx context.Context, id int64) (string, error) {
	var r ContentRatings
	err := t.get(ctx, fmt.Sprintf("/tv/%d/content_ratings", id), nil, &r)
	return r.Cert(), err
}

// setCert stores a content rating. "" records that TMDB had none, so the
// backfill doesn't ask again; an existing rating is never replaced by "".
func (a *Agent) setCert(ctx context.Context, id int64, cert string) {
	var age any
	if n, ok := rating.Age(cert); ok {
		age = n
	}
	if cert == "" {
		a.db.W.ExecContext(ctx, `UPDATE items SET content_rating = COALESCE(content_rating, '') WHERE id = ?`, id)
		return
	}
	a.db.W.ExecContext(ctx, `UPDATE items SET content_rating = ?, age = ? WHERE id = ?`, cert, age, id)
}

// backfillCerts fetches ratings for matched films and shows that have
// none yet, a batch at a time. Titles imported from Plex wait until the
// one-off copy from Plex has run, which covers most of them for free.
func (a *Agent) backfillCerts(ctx context.Context) int {
	rows, err := a.db.R.QueryContext(ctx, `SELECT id, kind, tmdb_id FROM items i
		WHERE kind IN ('movie', 'show') AND tmdb_id IS NOT NULL AND content_rating IS NULL AND meta_state = 'matched'
		AND (NOT EXISTS (SELECT 1 FROM plex_links p WHERE p.item_id = i.id)
			OR EXISTS (SELECT 1 FROM settings WHERE key = 'plex_ratings_v1' AND value = '1'))
		LIMIT 40`)
	if err != nil {
		return 0
	}
	type todo struct {
		id, tmdb int64
		kind     string
	}
	var list []todo
	for rows.Next() {
		var t todo
		rows.Scan(&t.id, &t.kind, &t.tmdb)
		list = append(list, t)
	}
	rows.Close()
	t := a.TMDB()
	for _, it := range list {
		var cert string
		var err error
		if it.kind == "movie" {
			cert, err = t.MovieCert(ctx, it.tmdb)
		} else {
			cert, err = t.ShowCert(ctx, it.tmdb)
		}
		if err != nil {
			// Try again on a later pass rather than recording "no rating".
			if ctx.Err() == nil {
				log.Printf("metadata: rating for item %d: %v", it.id, err)
			}
			return 0
		}
		a.setCert(ctx, it.id, cert)
	}
	return len(list)
}
