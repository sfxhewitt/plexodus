package plex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sfxhewitt/plexodus/internal/db"
)

// Marker sync brings Plex's intro and credits markers over for titles added
// after the import. Plex finds intros in a background task that can run
// hours or days after a file arrives, so each pass looks back over a window
// of recent additions rather than from a cursor, and only writes the items
// whose markers are new or have changed.

const (
	markerWindow   = 14 * 24 * time.Hour // how far back additions are re-checked
	markerInterval = time.Hour           // how often (checked after each watch sync)
)

// AddedSince pages through one type of item in a section, newest first,
// with markers, stopping at the first item added before since.
func (c *Client) AddedSince(ctx context.Context, section string, typ int, since int64, fn func([]Metadata) error) error {
	const size = 200
	for start := 0; ; start += size {
		var out struct {
			MediaContainer struct {
				Metadata []Metadata `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		q := url.Values{
			"type": {fmt.Sprint(typ)}, "includeMarkers": {"1"}, "sort": {"addedAt:desc"},
			"X-Plex-Container-Start": {fmt.Sprint(start)}, "X-Plex-Container-Size": {fmt.Sprint(size)},
		}
		// The addedAt filter is an optimisation; the loop also stops by itself.
		raw := q.Encode() + "&addedAt>=" + strconv.FormatInt(since, 10)
		if err := c.getRaw(ctx, "/library/sections/"+section+"/all", raw, &out); err != nil {
			return err
		}
		page := out.MediaContainer.Metadata
		stop := len(page) < size
		var keep []Metadata
		for _, m := range page {
			if m.AddedAt != 0 && m.AddedAt < since {
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

func (s *Syncer) markersDue(ctx context.Context) bool {
	last, _ := strconv.ParseInt(s.db.Setting(ctx, "plex_markers_checked"), 10, 64)
	return time.Now().Unix()-last >= int64(markerInterval/time.Second)
}

// syncMarkers runs at most once an hour, in the background after a watch sync.
func (s *Syncer) syncMarkers(ctx context.Context, c *Client, pm PathMap) error {
	now := time.Now()
	if !s.markersDue(ctx) {
		return nil
	}
	n, err := s.SyncMarkers(ctx, c, pm, now.Add(-markerWindow).Unix())
	if err != nil {
		return fmt.Errorf("markers: %w", err)
	}
	if n > 0 {
		log.Printf("plex marker sync: intro/credits markers updated on %d items", n)
	}
	return s.db.SetSetting(ctx, "plex_markers_checked", strconv.FormatInt(now.Unix(), 10))
}

// SyncMarkers copies Plex's markers for everything added since the given
// time, and reports how many Plexodus items changed.
func (s *Syncer) SyncMarkers(ctx context.Context, c *Client, pm PathMap, since int64) (int, error) {
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
			typ = TypeEpisode
		default:
			continue
		}
		err := c.AddedSince(ctx, sec.Key, typ, since, func(page []Metadata) error {
			n, err := s.applyMarkers(ctx, page, pm)
			total += n
			return err
		})
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

type markerChange struct {
	itemID    int64
	ratingKey string
	link      bool // item matched by path and needs linking
	markers   []Marker
}

// applyMarkers works out what changed with reads only, then writes just
// those items in one short transaction, so the hourly pass doesn't hold the
// database's write lock while it compares thousands of unchanged episodes.
func (s *Syncer) applyMarkers(ctx context.Context, items []Metadata, pm PathMap) (int, error) {
	var changes []markerChange
	for i := range items {
		m := &items[i]
		want := plexMarkers(m.Marker)
		if len(want) == 0 {
			continue // Plex hasn't analysed it (yet); nothing to bring over
		}
		ids, link, err := s.lookupItems(ctx, m, pm)
		if err != nil {
			return 0, err
		}
		for _, id := range ids {
			same, err := s.sameMarkers(ctx, id, want)
			if err != nil {
				return 0, err
			}
			if !same || link {
				changes = append(changes, markerChange{itemID: id, ratingKey: m.RatingKey, link: link, markers: want})
			}
		}
	}
	if len(changes) == 0 {
		return 0, nil
	}
	now := db.Now()
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, ch := range changes {
			if ch.link {
				if _, err := tx.Exec(`INSERT INTO plex_links(item_id, rating_key) VALUES(?, ?)
					ON CONFLICT(item_id) DO UPDATE SET rating_key = excluded.rating_key`, ch.itemID, ch.ratingKey); err != nil {
					return err
				}
			}
			if err := ReplaceMarkers(tx, ch.itemID, ch.markers); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE plex_links SET markers_at = ? WHERE item_id = ?`, now, ch.itemID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return len(changes), nil
}

// lookupItems is matchItems without the write: link reports whether the
// items were found by path and still need a plex_links row.
func (s *Syncer) lookupItems(ctx context.Context, m *Metadata, pm PathMap) (ids []int64, link bool, err error) {
	ids, err = readIDs(ctx, s.db.R, `SELECT item_id FROM plex_links WHERE rating_key = ?`, m.RatingKey)
	if err != nil || len(ids) > 0 {
		return ids, false, err
	}
	for _, media := range m.Media {
		for _, part := range media.Part {
			if part.File == "" {
				continue
			}
			ids, err = readIDs(ctx, s.db.R, `SELECT item_id FROM media_files WHERE path = ?`, pm.Apply(part.File))
			if err != nil || len(ids) > 0 {
				return ids, len(ids) > 0, err
			}
		}
	}
	return nil, false, nil
}

func readIDs(ctx context.Context, r *sql.DB, q string, args ...any) ([]int64, error) {
	rows, err := r.QueryContext(ctx, q, args...)
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

// plexMarkers keeps the marker types Plexodus uses.
func plexMarkers(ms []Marker) []Marker {
	var out []Marker
	for _, m := range ms {
		if m.Type == "intro" || m.Type == "credits" {
			out = append(out, m)
		}
	}
	return out
}

// sameMarkers reports whether the item's stored Plex markers already match.
func (s *Syncer) sameMarkers(ctx context.Context, itemID int64, want []Marker) (bool, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT type, start, "end" FROM markers WHERE item_id = ? AND source = 'plex'`, itemID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	type key struct {
		typ        string
		start, end int64 // milliseconds
	}
	have := map[key]bool{}
	for rows.Next() {
		var typ string
		var start, end float64
		if err := rows.Scan(&typ, &start, &end); err != nil {
			return false, err
		}
		have[key{typ, int64(math.Round(start * 1000)), int64(math.Round(end * 1000))}] = true
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(have) != len(want) {
		return false, nil
	}
	for _, m := range want {
		if !have[key{m.Type, m.StartTime, m.EndTime}] {
			return false, nil
		}
	}
	return true, nil
}

// ReplaceMarkers swaps an item's Plex markers for a new set, leaving
// chapter- and audio-detected ones alone (Plex's still win when shown).
func ReplaceMarkers(tx *sql.Tx, itemID int64, ms []Marker) error {
	if _, err := tx.Exec(`DELETE FROM markers WHERE item_id = ? AND source = 'plex'`, itemID); err != nil {
		return err
	}
	for _, mk := range plexMarkers(ms) {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO markers(item_id, type, start, "end", source) VALUES(?, ?, ?, ?, 'plex')`,
			itemID, mk.Type, float64(mk.StartTime)/1000, float64(mk.EndTime)/1000); err != nil {
			return err
		}
	}
	return nil
}

// showMarkersEvery is how often ShowMarkers will look at the same show.
const showMarkersEvery = 6 * time.Hour

// ItemsWithMarkers fetches several items by rating key with their markers.
func (c *Client) ItemsWithMarkers(ctx context.Context, keys []string) ([]Metadata, error) {
	var out struct {
		MediaContainer struct {
			Metadata []Metadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	err := c.get(ctx, "/library/metadata/"+strings.Join(keys, ","), url.Values{"includeMarkers": {"1"}}, &out)
	return out.MediaContainer.Metadata, err
}

// ShowMarkers brings Plex's intro and credits markers over for every episode
// of the played episode's show that is missing them. It covers what the
// import and the hourly pass can't: episodes that were never linked to Plex
// (added or upgraded after the import and more than markerWindow before
// Plex analysed them), and links whose Plex rating key went stale when Plex
// re-added a file. Episodes are matched to Plex by path. It looks at each
// show at most every showMarkersEvery and reports how many episodes gained
// markers.
func (s *Syncer) ShowMarkers(ctx context.Context, c *Client, pm PathMap, episodeID int64) (int, error) {
	var showID int64
	err := s.db.R.QueryRowContext(ctx, `SELECT se.parent_id FROM items e JOIN items se ON se.id = e.parent_id
		WHERE e.id = ? AND e.kind = 'episode'`, episodeID).Scan(&showID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, err
	}
	s.showsMu.Lock()
	if s.showsChecked == nil {
		s.showsChecked = map[int64]time.Time{}
	}
	if t, ok := s.showsChecked[showID]; ok && time.Since(t) < showMarkersEvery {
		s.showsMu.Unlock()
		return 0, nil
	}
	s.showsChecked[showID] = time.Now()
	s.showsMu.Unlock()

	leaves, err := s.showLeaves(ctx, c, showID)
	if err != nil || len(leaves) == 0 {
		return 0, err
	}

	// Our episodes by Plex rating key, found by path.
	type ep struct {
		id      int64
		key     string
		relink  bool // no link, or a link to another rating key
		markers bool // already has Plex markers
	}
	var eps []ep
	seen := map[int64]bool{}
	for _, m := range leaves {
		for _, media := range m.Media {
			for _, part := range media.Part {
				if part.File == "" {
					continue
				}
				ids, err := readIDs(ctx, s.db.R, `SELECT f.item_id FROM media_files f JOIN items i ON i.id = f.item_id
					WHERE f.path = ? AND i.kind = 'episode'`, pm.Apply(part.File))
				if err != nil {
					return 0, err
				}
				for _, id := range ids {
					if seen[id] {
						continue
					}
					seen[id] = true
					var key string
					s.db.R.QueryRowContext(ctx, `SELECT rating_key FROM plex_links WHERE item_id = ?`, id).Scan(&key)
					var have bool
					s.db.R.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM markers WHERE item_id = ? AND source = 'plex')`, id).Scan(&have)
					eps = append(eps, ep{id: id, key: m.RatingKey, relink: key != m.RatingKey, markers: have})
				}
			}
		}
	}

	// Ask Plex for the markers of those that have none yet, 50 at a time.
	var keys []string
	wanted := map[string]bool{}
	for _, e := range eps {
		if !e.markers && !wanted[e.key] {
			wanted[e.key] = true
			keys = append(keys, e.key)
		}
	}
	got := map[string][]Marker{}
	for start := 0; start < len(keys); start += 50 {
		items, err := c.ItemsWithMarkers(ctx, keys[start:min(start+50, len(keys))])
		if err != nil {
			return 0, err
		}
		for _, m := range items {
			got[m.RatingKey] = plexMarkers(m.Marker)
		}
	}

	n := 0
	now := db.Now()
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, e := range eps {
			ms, asked := got[e.key]
			if !e.relink && !asked {
				continue
			}
			if e.relink {
				if _, err := tx.Exec(`INSERT INTO plex_links(item_id, rating_key) VALUES(?, ?)
					ON CONFLICT(item_id) DO UPDATE SET rating_key = excluded.rating_key`, e.id, e.key); err != nil {
					return err
				}
			}
			if !asked {
				continue
			}
			if len(ms) > 0 {
				if err := ReplaceMarkers(tx, e.id, ms); err != nil {
					return err
				}
				n++
			}
			if _, err := tx.Exec(`UPDATE plex_links SET markers_at = ? WHERE item_id = ?`, now, e.id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	if n > 0 {
		log.Printf("plex markers: show %d: intro/credits markers brought over for %d episodes", showID, n)
	}
	return n, nil
}

// showLeaves lists a show's episodes in Plex, finding the show by its own
// link or, failing that, through any of its linked episodes.
func (s *Syncer) showLeaves(ctx context.Context, c *Client, showID int64) ([]Metadata, error) {
	var key string
	if err := s.db.R.QueryRowContext(ctx, `SELECT rating_key FROM plex_links WHERE item_id = ?`, showID).Scan(&key); err == nil && key != "" {
		if leaves, err := c.Leaves(ctx, key); err == nil && len(leaves) > 0 {
			return leaves, nil
		} else if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	// The show's own key is missing or stale: ask Plex about a few of its
	// linked episodes for their show's key.
	rows, err := s.db.R.QueryContext(ctx, `SELECT l.rating_key FROM items se JOIN items e ON e.parent_id = se.id
		JOIN plex_links l ON l.item_id = e.id WHERE se.parent_id = ? AND e.kind = 'episode' ORDER BY e.id DESC LIMIT 3`, showID)
	if err != nil {
		return nil, err
	}
	var eks []string
	for rows.Next() {
		var k string
		rows.Scan(&k)
		eks = append(eks, k)
	}
	rows.Close()
	for _, k := range eks {
		m, err := c.Item(ctx, k)
		if err != nil || m.GrandparentRatingKey == "" || m.GrandparentRatingKey == key {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		leaves, err := c.Leaves(ctx, m.GrandparentRatingKey)
		if err != nil {
			return nil, err
		}
		if _, err := s.db.W.ExecContext(ctx, `INSERT INTO plex_links(item_id, rating_key) VALUES(?, ?)
			ON CONFLICT(item_id) DO UPDATE SET rating_key = excluded.rating_key`, showID, m.GrandparentRatingKey); err != nil {
			return nil, err
		}
		return leaves, nil
	}
	return nil, nil
}
