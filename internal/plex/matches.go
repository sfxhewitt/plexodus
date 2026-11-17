package plex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"time"

	"github.com/sfxhewitt/plexodus/internal/db"
)

// Match sync carries "Fix match" (and other re-matches) made in Plex over
// to Plexodus. The import copies Plex's metadata once; when a title is later
// re-matched in Plex its guid changes and Plex bumps its updatedAt, so each
// pass reads the movies and shows Plex updated since the last pass and
// re-copies the ones whose match is different from what Plexodus has.
// Plex also bumps updatedAt on routine metadata refreshes, so nothing is
// written unless the match itself changed.

const (
	matchInterval = time.Hour           // how often (checked after each watch sync)
	matchFirstRun = 30 * 24 * time.Hour // how far back the very first pass looks
	matchOverlap  = 10 * time.Minute    // re-read a little before the cursor
)

// UpdatedSince pages through one type of item in a section, most recently
// updated first, stopping at the first item updated before since.
func (c *Client) UpdatedSince(ctx context.Context, section string, typ int, since int64, fn func([]Metadata) error) error {
	const size = 200
	for start := 0; ; start += size {
		var out struct {
			MediaContainer struct {
				Metadata []Metadata `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		q := url.Values{
			"type": {fmt.Sprint(typ)}, "includeGuids": {"1"}, "sort": {"updatedAt:desc"},
			"X-Plex-Container-Start": {fmt.Sprint(start)}, "X-Plex-Container-Size": {fmt.Sprint(size)},
		}
		// The updatedAt filter is an optimisation; the loop also stops by itself.
		raw := q.Encode() + "&updatedAt>=" + strconv.FormatInt(since, 10)
		if err := c.getRaw(ctx, "/library/sections/"+section+"/all", raw, &out); err != nil {
			return err
		}
		page := out.MediaContainer.Metadata
		stop := len(page) < size
		var keep []Metadata
		for _, m := range page {
			if m.UpdatedAt != 0 && m.UpdatedAt < since {
				stop = true
				break
			}
			keep = append(keep, m)
		}
		if len(keep) > 0 {
			if err := fn(keep); err != nil {
				return err
			}
		}
		if stop || start > 100_000 {
			return nil
		}
	}
}

// Leaves lists every episode of a show.
func (c *Client) Leaves(ctx context.Context, showKey string) ([]Metadata, error) {
	var out struct {
		MediaContainer struct {
			Metadata []Metadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	err := c.get(ctx, "/library/metadata/"+showKey+"/allLeaves", url.Values{"includeGuids": {"1"}}, &out)
	return out.MediaContainer.Metadata, err
}

func (s *Syncer) matchesDue(ctx context.Context) bool {
	last, _ := strconv.ParseInt(s.db.Setting(ctx, "plex_matches_checked"), 10, 64)
	return time.Now().Unix()-last >= int64(matchInterval/time.Second)
}

// syncMatches runs at most once an hour, in the background after a watch sync.
func (s *Syncer) syncMatches(ctx context.Context, c *Client, pm PathMap) error {
	now := time.Now()
	if !s.matchesDue(ctx) {
		return nil
	}
	since, _ := strconv.ParseInt(s.db.Setting(ctx, "plex_matches_cursor"), 10, 64)
	if since == 0 {
		since = now.Add(-matchFirstRun).Unix()
	} else {
		since -= int64(matchOverlap / time.Second)
	}
	n, err := s.SyncMatches(ctx, c, pm, since)
	if err != nil {
		return fmt.Errorf("matches: %w", err)
	}
	if n > 0 {
		log.Printf("plex match sync: %d titles re-matched from Plex", n)
	}
	if err := s.db.SetSetting(ctx, "plex_matches_cursor", strconv.FormatInt(now.Unix(), 10)); err != nil {
		return err
	}
	return s.db.SetSetting(ctx, "plex_matches_checked", strconv.FormatInt(now.Unix(), 10))
}

// SyncMatches re-copies every movie and show Plex updated since the given
// time whose match changed, and reports how many Plexodus titles changed.
func (s *Syncer) SyncMatches(ctx context.Context, c *Client, pm PathMap, since int64) (int, error) {
	sections, err := c.Sections(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, sec := range sections {
		typ := TypeMovie
		switch sec.Type {
		case "movie":
		case "show":
			typ = TypeShow
		default:
			continue
		}
		err := c.UpdatedSince(ctx, sec.Key, typ, since, func(page []Metadata) error {
			n, err := s.applyMatches(ctx, c, page, pm)
			total += n
			return err
		})
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// matchRow is what Plexodus knows about a linked title's match.
type matchRow struct {
	id       int64
	kind     string
	tmdb     int64
	imdb     string
	plexGUID string // Plex guid when last seen; "" = not recorded yet
	manual   bool   // matched by hand in Plexodus
}

// rematch reports whether Plex's item m is now matched to a different
// title from the one Plexodus shows.
func rematch(m *Metadata, r matchRow) bool {
	if m.PlexGUID == "" {
		return false // unmatched in Plex: nothing better to offer
	}
	if r.plexGUID != "" {
		return r.plexGUID != m.PlexGUID
	}
	// First time this title is seen since the guid was recorded: compare
	// external IDs, unless the title was fixed by hand here.
	if r.manual {
		return false
	}
	if t := tmdbID(m); t.Valid {
		return t.Int64 != r.tmdb
	}
	if imdb := m.ExternalID("imdb"); imdb != "" {
		return imdb != r.imdb
	}
	return false
}

func (s *Syncer) matchRows(ctx context.Context, m *Metadata, pm PathMap) ([]matchRow, bool, error) {
	ids, link, err := s.lookupItems(ctx, m, pm)
	if err != nil || len(ids) == 0 {
		return nil, false, err
	}
	var rows []matchRow
	for _, id := range ids {
		r := matchRow{id: id}
		err := s.db.R.QueryRowContext(ctx, `SELECT i.kind, COALESCE(i.tmdb_id, 0), COALESCE(i.imdb_id, ''),
				COALESCE(l.plex_guid, ''), i.match_manual
			FROM items i LEFT JOIN plex_links l ON l.item_id = i.id WHERE i.id = ?`, id).
			Scan(&r.kind, &r.tmdb, &r.imdb, &r.plexGUID, &r.manual)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, false, err
		}
		if r.kind == "movie" || r.kind == "show" {
			rows = append(rows, r)
		}
	}
	return rows, link, nil
}

type matchChange struct {
	row  matchRow
	key  string    // Plex rating key
	meta *Metadata // Plex's new match
	link bool      // found by path; needs a plex_links row
}

// applyMatches compares with reads only and writes just what changed.
func (s *Syncer) applyMatches(ctx context.Context, c *Client, items []Metadata, pm PathMap) (int, error) {
	var changes, guids []matchChange
	var fetch []string
	for i := range items {
		m := &items[i]
		if m.Type != "movie" && m.Type != "show" {
			continue
		}
		rows, link, err := s.matchRows(ctx, m, pm)
		if err != nil {
			return 0, err
		}
		for _, r := range rows {
			if rematch(m, r) {
				changes = append(changes, matchChange{row: r, key: m.RatingKey, meta: m, link: link})
				fetch = append(fetch, m.RatingKey)
			} else if r.plexGUID != m.PlexGUID {
				// Same title: just remember Plex's guid for next time.
				guids = append(guids, matchChange{row: r, key: m.RatingKey, meta: m, link: link})
			}
		}
	}
	if len(guids) > 0 {
		if err := s.db.Tx(ctx, func(tx *sql.Tx) error {
			for _, g := range guids {
				if _, err := tx.Exec(`INSERT INTO plex_links(item_id, rating_key, plex_guid) VALUES(?, ?, ?)
					ON CONFLICT(item_id) DO UPDATE SET rating_key = excluded.rating_key, plex_guid = excluded.plex_guid`,
					g.row.id, g.key, g.meta.PlexGUID); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return 0, err
		}
	}
	if len(changes) == 0 {
		return 0, nil
	}
	// The section listing leaves out some fields (genres, logos); read the
	// re-matched titles in full.
	full := map[string]*Metadata{}
	for start := 0; start < len(fetch); start += 50 {
		end := min(start+50, len(fetch))
		got, err := c.Items(ctx, fetch[start:end])
		if err != nil {
			return 0, err
		}
		for i := range got {
			full[got[i].RatingKey] = &got[i]
		}
	}
	episodes := map[string][]Metadata{}
	for _, ch := range changes {
		if ch.meta != nil && ch.meta.Type == "show" {
			if _, ok := episodes[ch.meta.RatingKey]; !ok {
				eps, err := c.Leaves(ctx, ch.meta.RatingKey)
				if err != nil {
					return 0, err
				}
				episodes[ch.meta.RatingKey] = eps
			}
		}
	}

	var done []matchChange
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, ch := range changes {
			m := ch.meta
			if f := full[ch.key]; f != nil {
				m = f
			}
			if err := rematchItem(tx, ch.row.id, m); err != nil {
				return err
			}
			if m.Type == "show" {
				if err := rematchEpisodes(tx, episodes[ch.key]); err != nil {
					return err
				}
			}
			// The rating key is unchanged by a re-match in Plex.
			if _, err := tx.Exec(`INSERT INTO plex_links(item_id, rating_key, plex_guid) VALUES(?, ?, ?)
				ON CONFLICT(item_id) DO UPDATE SET rating_key = excluded.rating_key, plex_guid = excluded.plex_guid`,
				ch.row.id, ch.key, m.PlexGUID); err != nil {
				return err
			}
			r := ch.row
			r.tmdb = tmdbID(m).Int64
			done = append(done, matchChange{row: r, key: ch.key, meta: m})
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, ch := range done {
		log.Printf("plex match sync: item %d is now %q (%d) as matched in Plex", ch.row.id, ch.meta.Title, ch.meta.Year)
		if s.Rematched != nil {
			s.Rematched(ctx, ch.row.id, ch.row.kind, ch.row.tmdb)
		}
	}
	return len(done), nil
}

// rematchItem replaces a title's metadata with Plex's new match. Unlike the
// import it overwrites the IDs (the old ones belong to the wrong title) and
// clears anything that came from the old match.
func rematchItem(tx *sql.Tx, id int64, m *Metadata) error {
	if _, err := tx.Exec(`UPDATE items SET tmdb_id = NULL, imdb_id = NULL, genres = NULL, poster = NULL, backdrop = NULL,
		logo = NULL, collection_id = NULL, content_rating = NULL, age = NULL, match_manual = 0 WHERE id = ?`, id); err != nil {
		return err
	}
	return applyMetadata(tx, id, m, false)
}

// rematchEpisodes re-copies the episode titles, summaries and stills of a
// re-matched show.
func rematchEpisodes(tx *sql.Tx, eps []Metadata) error {
	for i := range eps {
		m := &eps[i]
		m.Year = 0
		ids, err := queryIDs(tx, `SELECT l.item_id FROM plex_links l JOIN items i ON i.id = l.item_id
			WHERE l.rating_key = ? AND i.kind = 'episode'`, m.RatingKey)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := applyMetadata(tx, id, m, true); err != nil {
				return err
			}
		}
	}
	return nil
}

// RecordManualMatch marks a title as matched by hand in Plexodus, so the
// match sync only overrides it if the title is later re-matched in Plex.
func RecordManualMatch(ctx context.Context, d *db.DB, id int64) error {
	_, err := d.W.ExecContext(ctx, `UPDATE items SET match_manual = 1 WHERE id = ?`, id)
	return err
}
