package plex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The Plex Watchlist, copied into Plexodus.
//
// The Watchlist belongs to the Plex account, not the Plex server, so it is
// read from plex.tv's Discover service with the server's Plex token (the
// owner's). Each film or show on it is added to the Plexodus watchlist of
// the account that receives the owner's watch state, once: taking it off
// the list in Plexodus afterwards sticks. Titles that aren't in the library
// yet are remembered and added as soon as they arrive. Nothing is ever
// written to Plex.

const (
	watchlistCheckedKey = "plex_watchlist_checked"
	watchlistStatusKey  = "plex_watchlist_status"
	// How often Plex is asked for the list (one small request).
	watchlistInterval = 15 * time.Minute
	// How often the Plex server is asked again about a title that isn't in
	// the library; in between, only the Plexodus database is checked.
	watchlistLookAgain = 24 * time.Hour
	// After Plex refuses the token, wait this long before asking again.
	watchlistRefusedWait = 6 * time.Hour
	watchlistPage        = 100
	watchlistWaitingMax  = 60
)

// WatchlistStatus is what the last copy of the Plex Watchlist did.
type WatchlistStatus struct {
	At      int64    `json:"at,omitempty"`      // unix seconds
	OnPlex  int      `json:"onPlex"`            // titles on the Plex Watchlist
	Copied  int      `json:"copied"`            // of those, found in the library (ever)
	Added   int      `json:"added"`             // added to the watchlist by the last run
	Waiting []string `json:"waiting,omitempty"` // not in the library yet ("Heat (1995)")
	Error   string   `json:"error,omitempty"`
}

// WatchItem is one film or show on a Plex Watchlist.
type WatchItem struct {
	Key   string // Discover id (see DiscoverKey)
	GUID  string // plex://movie/<Key>
	Type  string // movie | show
	Title string
	Year  int
	IDs   []string // external ids, when Plex sends them
}

type discoverMeta struct {
	GUID  string `json:"guid"`
	Type  string `json:"type"`
	Title string `json:"title"`
	Year  int    `json:"year"`
	Guids []struct {
		ID string `json:"id"`
	} `json:"Guid"`
}

func (m discoverMeta) item() (WatchItem, bool) {
	k := DiscoverKey(m.GUID)
	if k == "" || (m.Type != "movie" && m.Type != "show") {
		return WatchItem{}, false
	}
	w := WatchItem{Key: k, GUID: m.GUID, Type: m.Type, Title: m.Title, Year: m.Year}
	for _, g := range m.Guids {
		w.IDs = append(w.IDs, g.ID)
	}
	return w, true
}

// Watchlist reads the token's whole Plex Watchlist, most recently added first.
func (a Account) Watchlist(ctx context.Context, token string) ([]WatchItem, error) {
	var items []WatchItem
	seen := map[string]bool{}
	for start := 0; ; start += watchlistPage {
		var out struct {
			MediaContainer struct {
				TotalSize int            `json:"totalSize"`
				Metadata  []discoverMeta `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		q := url.Values{
			"sort": {"watchlistedAt:desc"}, "includeGuids": {"1"},
			"X-Plex-Container-Start": {fmt.Sprint(start)}, "X-Plex-Container-Size": {fmt.Sprint(watchlistPage)},
		}
		if _, err := a.discover(ctx, "GET", "/library/sections/watchlist/all", token, q, &out); err != nil {
			return nil, err
		}
		fresh := 0
		for _, m := range out.MediaContainer.Metadata {
			if w, ok := m.item(); ok && !seen[w.Key] {
				seen[w.Key] = true
				items = append(items, w)
				fresh++
			}
		}
		// Stop at the end, and if Plex ignores the paging and repeats itself.
		n := len(out.MediaContainer.Metadata)
		if n == 0 || fresh == 0 || n < watchlistPage ||
			(out.MediaContainer.TotalSize > 0 && start+n >= out.MediaContainer.TotalSize) || start > 20000 {
			return items, nil
		}
	}
}

// DiscoverIDs returns the external ids (tmdb://949, imdb://tt0113277) Plex
// lists for a Discover film or show.
func (a Account) DiscoverIDs(ctx context.Context, token, key string) ([]string, error) {
	var out struct {
		MediaContainer struct {
			Metadata []discoverMeta `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if _, err := a.discover(ctx, "GET", "/library/metadata/"+url.PathEscape(key), token, url.Values{"includeGuids": {"1"}}, &out); err != nil {
		return nil, err
	}
	var ids []string
	for _, m := range out.MediaContainer.Metadata {
		for _, g := range m.Guids {
			ids = append(ids, g.ID)
		}
	}
	return ids, nil
}

// ByGUID finds the server's own copies of a film or show from its Plex GUID
// (plex://movie/…).
func (c *Client) ByGUID(ctx context.Context, guid string) ([]Metadata, error) {
	var out struct {
		MediaContainer struct {
			Metadata []Metadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := c.get(ctx, "/library/all", url.Values{"guid": {guid}, "includeGuids": {"1"}}, &out); err != nil {
		return nil, err
	}
	return out.MediaContainer.Metadata, nil
}

// WatchlistStatus reports the last copy of the Plex Watchlist.
func (s *Syncer) WatchlistStatus(ctx context.Context) *WatchlistStatus {
	raw := s.db.Setting(ctx, watchlistStatusKey)
	if raw == "" {
		return nil
	}
	var st WatchlistStatus
	if json.Unmarshal([]byte(raw), &st) != nil {
		return nil
	}
	return &st
}

func (s *Syncer) watchlistDue(ctx context.Context) bool {
	s.mu.Lock()
	now := s.watchlistNow
	s.watchlistNow = false
	s.mu.Unlock()
	if now {
		return true
	}
	next, _ := strconv.ParseInt(s.db.Setting(ctx, watchlistCheckedKey), 10, 64)
	return time.Now().Unix() >= next
}

// syncWatchlist copies the Plex Watchlist. Its failures are its own: they
// are shown beside the watchlist in Settings and don't hold up the other
// copies from Plex.
func (s *Syncer) syncWatchlist(ctx context.Context, c *Client, pm PathMap) {
	if s.Account == nil {
		return
	}
	wait := watchlistInterval
	st, err := s.copyWatchlist(ctx, c, pm)
	if err != nil {
		prev := s.WatchlistStatus(ctx)
		if prev != nil {
			st = *prev
		}
		st.Added = 0
		st.Error = err.Error()
		if errors.Is(err, ErrBadToken) {
			st.Error = "Plex didn't accept this server's Plex token for the Watchlist. Save a fresh token in Settings."
			wait = watchlistRefusedWait
		}
		if prev == nil || prev.Error != st.Error {
			log.Printf("plex watchlist: %v", err)
		}
	}
	st.At = time.Now().Unix()
	if b, err := json.Marshal(st); err == nil {
		s.db.SetSetting(ctx, watchlistStatusKey, string(b))
	}
	s.db.SetSetting(ctx, watchlistCheckedKey, strconv.FormatInt(time.Now().Add(wait).Unix(), 10))
}

type watchRow struct {
	key, kind, title string
	year             int
	ids              sql.NullString
	lookedAt         int64
	pos              int
}

func (r watchRow) guid() string { return "plex://" + r.kind + "/" + r.key }

func (r watchRow) label() string {
	if r.year > 0 {
		return fmt.Sprintf("%s (%d)", r.title, r.year)
	}
	return r.title
}

func (s *Syncer) copyWatchlist(ctx context.Context, c *Client, pm PathMap) (WatchlistStatus, error) {
	var st WatchlistStatus
	uid, err := s.syncUser(ctx)
	if err != nil {
		return st, err
	}
	acct := s.Account(ctx)
	list, err := acct.Watchlist(ctx, c.token)
	if err != nil {
		return st, err
	}
	st.OnPlex = len(list)

	// Remember what is on the list, and forget what has left it (so putting
	// a title back on the Plex Watchlist later copies it again).
	err = s.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE plex_watchlist SET pos = -1 WHERE user_id = ?`, uid); err != nil {
			return err
		}
		for i, w := range list {
			var ids any
			if len(w.IDs) > 0 {
				ids = strings.Join(w.IDs, " ")
			}
			if _, err := tx.Exec(`INSERT INTO plex_watchlist(user_id, plex_key, kind, title, year, ids, pos) VALUES(?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(user_id, plex_key) DO UPDATE SET kind = excluded.kind, title = excluded.title, year = excluded.year,
					ids = COALESCE(excluded.ids, plex_watchlist.ids), pos = excluded.pos`,
				uid, w.Key, w.Type, w.Title, w.Year, ids, i); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`DELETE FROM plex_watchlist WHERE user_id = ? AND pos < 0`, uid)
		return err
	})
	if err != nil {
		return st, err
	}

	// Everything not yet matched to a title in the library.
	rows, err := s.db.R.QueryContext(ctx, `SELECT plex_key, kind, title, COALESCE(year, 0), ids, looked_at, pos
		FROM plex_watchlist WHERE user_id = ? AND item_id IS NULL ORDER BY pos`, uid)
	if err != nil {
		return st, err
	}
	var pending []watchRow
	for rows.Next() {
		var r watchRow
		if err := rows.Scan(&r.key, &r.kind, &r.title, &r.year, &r.ids, &r.lookedAt, &r.pos); err != nil {
			rows.Close()
			return st, err
		}
		pending = append(pending, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return st, err
	}

	now := time.Now().Unix()
	var firstErr error
	for _, r := range pending {
		if ctx.Err() != nil {
			return st, ctx.Err()
		}
		id, err := s.matchWatch(ctx, c, acct, pm, uid, &r, now)
		if err != nil {
			if errors.Is(err, ErrBadToken) {
				return st, err
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if id == 0 {
			continue
		}
		// Keep Plex's order: the most recently watchlisted sorts first.
		err = s.db.Tx(ctx, func(tx *sql.Tx) error {
			res, err := tx.Exec(`INSERT INTO watchlist(user_id, item_id, added_at) VALUES(?, ?, ?)
				ON CONFLICT(user_id, item_id) DO NOTHING`, uid, id, now-int64(r.pos))
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				st.Added++
			}
			_, err = tx.Exec(`UPDATE plex_watchlist SET item_id = ? WHERE user_id = ? AND plex_key = ?`, id, uid, r.key)
			return err
		})
		if err != nil {
			return st, err
		}
	}

	s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM plex_watchlist WHERE user_id = ? AND item_id IS NOT NULL`, uid).Scan(&st.Copied)
	if wr, err := s.db.R.QueryContext(ctx, `SELECT title, COALESCE(year, 0) FROM plex_watchlist
		WHERE user_id = ? AND item_id IS NULL ORDER BY pos LIMIT ?`, uid, watchlistWaitingMax); err == nil {
		for wr.Next() {
			var r watchRow
			if wr.Scan(&r.title, &r.year) == nil {
				st.Waiting = append(st.Waiting, r.label())
			}
		}
		wr.Close()
	}
	if st.Added > 0 {
		log.Printf("plex watchlist: added %d (%d on Plex, %d in the library, %d not in the library yet)",
			st.Added, st.OnPlex, st.Copied, st.OnPlex-st.Copied)
		if s.WatchlistChanged != nil {
			s.WatchlistChanged(uid)
		}
	}
	return st, firstErr
}

// matchWatch finds the library title for one Watchlist entry, or 0. It
// looks in the Plexodus database first; the Plex server (and plex.tv, for
// the entry's TMDB and IMDb ids) is only asked once a day per title.
func (s *Syncer) matchWatch(ctx context.Context, c *Client, acct Account, pm PathMap, uid int64, r *watchRow, now int64) (int64, error) {
	if id := s.watchLocal(ctx, r, false); id != 0 {
		return id, nil
	}
	if now-r.lookedAt < int64(watchlistLookAgain/time.Second) {
		return s.watchLocal(ctx, r, true), nil
	}

	var id int64
	found, err := c.ByGUID(ctx, r.guid())
	if err != nil {
		return 0, fmt.Errorf("asking Plex for %s: %w", r.label(), err)
	}
	if len(found) > 0 {
		err = s.db.Tx(ctx, func(tx *sql.Tx) error {
			for i := range found {
				m := &found[i]
				if m.Type != r.kind {
					continue
				}
				ids, err := matchItems(tx, m, pm)
				if err != nil {
					return err
				}
				for _, cand := range ids {
					var kind string
					if tx.QueryRow(`SELECT kind FROM items WHERE id = ?`, cand).Scan(&kind) == nil && kind == r.kind {
						id = cand
						return nil
					}
				}
			}
			return nil
		})
		if err != nil {
			return 0, err
		}
	}
	if id == 0 && !r.ids.Valid {
		ids, err := acct.DiscoverIDs(ctx, c.token, r.key)
		if err != nil {
			if errors.Is(err, ErrBadToken) {
				return 0, err
			}
			// Plex has no details for it; carry on with the title and year.
			ids = nil
		}
		r.ids = sql.NullString{String: strings.Join(ids, " "), Valid: true}
		if _, err := s.db.W.ExecContext(ctx, `UPDATE plex_watchlist SET ids = ? WHERE user_id = ? AND plex_key = ?`, r.ids.String, uid, r.key); err != nil {
			return 0, err
		}
	}
	if _, err := s.db.W.ExecContext(ctx, `UPDATE plex_watchlist SET looked_at = ? WHERE user_id = ? AND plex_key = ?`, now, uid, r.key); err != nil {
		return 0, err
	}
	if id == 0 {
		id = s.watchLocal(ctx, r, true)
	}
	return id, nil
}

// watchLocal looks for the entry in the Plexodus database: by the Plex GUID
// of a linked title, then its TMDB or IMDb id, then (byTitle) the same
// title and year. Of duplicate copies it takes the oldest.
func (s *Syncer) watchLocal(ctx context.Context, r *watchRow, byTitle bool) int64 {
	var id int64
	one := func(q string, args ...any) bool {
		return s.db.R.QueryRowContext(ctx, q, args...).Scan(&id) == nil && id != 0
	}
	if one(`SELECT i.id FROM plex_links l JOIN items i ON i.id = l.item_id
		WHERE l.plex_guid = ? AND i.kind = ? ORDER BY i.id LIMIT 1`, r.guid(), r.kind) {
		return id
	}
	for _, x := range strings.Fields(r.ids.String) {
		switch {
		case strings.HasPrefix(x, "tmdb://"):
			if n, err := strconv.ParseInt(strings.TrimPrefix(x, "tmdb://"), 10, 64); err == nil && n > 0 &&
				one(`SELECT id FROM items WHERE tmdb_id = ? AND kind = ? ORDER BY id LIMIT 1`, n, r.kind) {
				return id
			}
		case strings.HasPrefix(x, "imdb://"):
			if v := strings.TrimPrefix(x, "imdb://"); v != "" &&
				one(`SELECT id FROM items WHERE imdb_id = ? AND kind = ? ORDER BY id LIMIT 1`, v, r.kind) {
				return id
			}
		}
	}
	if !byTitle || r.year == 0 || r.title == "" {
		return 0
	}
	// Same title and year, compared loosely (case, punctuation, "The").
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, title FROM items WHERE kind = ? AND year = ? ORDER BY id`, r.kind, r.year)
	if err != nil {
		return 0
	}
	defer rows.Close()
	want := normTitle(r.title)
	for rows.Next() {
		var cand int64
		var title string
		if rows.Scan(&cand, &title) == nil && normTitle(title) == want {
			return cand
		}
	}
	return 0
}
