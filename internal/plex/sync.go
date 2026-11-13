package plex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sfxhewitt/plexodus/internal/db"
)

// Watch sync keeps Plexodus' watched flags, resume points and Continue
// Watching in step with Plex, so anything watched in a Plex app shows up
// here too. It is one-way (Plex -> Plexodus) and incremental: each run reads
// Plex's recent play history and its On Deck list, re-reads those items'
// current state, and applies it only where Plex's state is newer than ours.

// HistoryEntry is one completed view in Plex's play history.
type HistoryEntry struct {
	RatingKey string `json:"ratingKey"`
	ViewedAt  int64  `json:"viewedAt"`
	AccountID int64  `json:"accountID"`
	Type      string `json:"type"`
}

// History pages through play history newest first, stopping at the first
// entry older than since.
func (c *Client) History(ctx context.Context, since int64, fn func([]HistoryEntry) error) error {
	const size = 200
	for start := 0; ; start += size {
		var out struct {
			MediaContainer struct {
				Metadata []HistoryEntry `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		q := url.Values{
			"sort":                   {"viewedAt:desc"},
			"X-Plex-Container-Start": {fmt.Sprint(start)},
			"X-Plex-Container-Size":  {fmt.Sprint(size)},
		}
		// The viewedAt filter is an optimisation only; the loop also stops
		// on its own once entries get older than since.
		raw := q.Encode() + "&viewedAt>=" + strconv.FormatInt(since, 10)
		if err := c.getRaw(ctx, "/status/sessions/history/all", raw, &out); err != nil {
			return err
		}
		page := out.MediaContainer.Metadata
		var keep []HistoryEntry
		stop := len(page) < size
		for _, h := range page {
			if h.ViewedAt < since {
				stop = true
				break
			}
			keep = append(keep, h)
		}
		if len(keep) > 0 {
			if err := fn(keep); err != nil {
				return err
			}
		}
		if stop || start > 50_000 {
			return nil
		}
	}
}

// OnDeck returns Plex's On Deck list: in-progress items and next episodes.
func (c *Client) OnDeck(ctx context.Context) ([]Metadata, error) {
	var out struct {
		MediaContainer struct {
			Metadata []Metadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	err := c.get(ctx, "/library/onDeck", url.Values{"X-Plex-Container-Size": {"200"}}, &out)
	return out.MediaContainer.Metadata, err
}

// Items fetches several items by rating key in one request.
func (c *Client) Items(ctx context.Context, keys []string) ([]Metadata, error) {
	var out struct {
		MediaContainer struct {
			Metadata []Metadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	err := c.get(ctx, "/library/metadata/"+strings.Join(keys, ","), nil, &out)
	return out.MediaContainer.Metadata, err
}

type SyncStatus struct {
	Enabled  bool      `json:"enabled"`
	Running  bool      `json:"running"`
	LastRun  time.Time `json:"lastRun,omitempty"`
	LastOK   time.Time `json:"lastOk,omitempty"`
	Checked  int       `json:"checked"`
	Updated  int       `json:"updated"`
	Error    string    `json:"error,omitempty"`
	Interval int       `json:"intervalMinutes"`
}

// Syncer runs the watch sync on a timer and on demand.
type Syncer struct {
	db       *db.DB
	client   func(context.Context) (*Client, PathMap)
	busy     func() bool // true while an import runs
	interval time.Duration
	kick     chan struct{}

	mu     sync.Mutex
	status SyncStatus
}

func NewSyncer(d *db.DB, client func(context.Context) (*Client, PathMap), busy func() bool) *Syncer {
	return &Syncer{db: d, client: client, busy: busy, interval: 5 * time.Minute, kick: make(chan struct{}, 1)}
}

// Enabled reports whether sync is switched on (the default).
func (s *Syncer) Enabled(ctx context.Context) bool { return s.db.Setting(ctx, "plex_sync") != "off" }

func (s *Syncer) Status(ctx context.Context) SyncStatus {
	s.mu.Lock()
	st := s.status
	s.mu.Unlock()
	st.Enabled = s.Enabled(ctx)
	st.Interval = int(s.interval / time.Minute)
	return st
}

// Kick asks for a sync now. It returns false if one is already running.
func (s *Syncer) Kick() bool {
	s.mu.Lock()
	running := s.status.Running
	s.mu.Unlock()
	if running {
		return false
	}
	select {
	case s.kick <- struct{}{}:
	default:
	}
	return true
}

// Run syncs shortly after startup, then every interval and whenever kicked.
func (s *Syncer) Run(ctx context.Context) {
	t := time.NewTimer(30 * time.Second)
	defer t.Stop()
	for {
		manual := false
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.kick:
			manual = true
			if !t.Stop() {
				select {
				case <-t.C:
				default:
				}
			}
		}
		if manual || s.Enabled(ctx) {
			s.once(ctx)
		}
		t.Reset(s.interval)
	}
}

func (s *Syncer) once(ctx context.Context) {
	c, pm := s.client(ctx)
	if !c.Configured() {
		return
	}
	if s.busy != nil && s.busy() {
		return
	}
	s.mu.Lock()
	s.status.Running = true
	s.status.LastRun = time.Now()
	s.mu.Unlock()

	sctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	checked, updated, err := s.sync(sctx, c, pm)
	cancel()
	if err == nil {
		mctx, mcancel := context.WithTimeout(ctx, 5*time.Minute)
		if merr := s.syncMarkers(mctx, c, pm); merr != nil {
			log.Printf("plex: %v", merr)
		}
		mcancel()
	}
	rctx, rcancel := context.WithTimeout(ctx, 10*time.Minute)
	if rerr := s.BackfillRatings(rctx, c); rerr != nil {
		log.Printf("plex: content ratings: %v", rerr)
	}
	rcancel()

	s.mu.Lock()
	s.status.Running = false
	s.status.Checked, s.status.Updated = checked, updated
	s.status.Error = ""
	if err != nil {
		s.status.Error = err.Error()
	} else {
		s.status.LastOK = time.Now()
	}
	s.mu.Unlock()
	if err != nil {
		log.Printf("plex watch sync: %v", err)
	} else if updated > 0 {
		log.Printf("plex watch sync: %d of %d items updated", updated, checked)
	}
}

// syncUser is the Plexodus account that receives the Plex owner's watch
// state: whoever ran the import, else the first admin.
func (s *Syncer) syncUser(ctx context.Context) (int64, error) {
	if id, err := strconv.ParseInt(s.db.Setting(ctx, "plex_sync_user"), 10, 64); err == nil {
		var ok int
		if s.db.R.QueryRowContext(ctx, `SELECT 1 FROM users WHERE id = ?`, id).Scan(&ok) == nil {
			return id, nil
		}
	}
	var id int64
	err := s.db.R.QueryRowContext(ctx, `SELECT id FROM users WHERE is_admin = 1 ORDER BY id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errors.New("no admin account yet")
	}
	return id, err
}

func (s *Syncer) sync(ctx context.Context, c *Client, pm PathMap) (checked, updated int, err error) {
	uid, err := s.syncUser(ctx)
	if err != nil {
		return 0, 0, err
	}
	started := time.Now().Unix()
	since, _ := strconv.ParseInt(s.db.Setting(ctx, "plex_sync_cursor"), 10, 64)
	if since == 0 {
		since = started - 30*86400
	} else {
		since -= 15 * 60 // overlap, in case history lands late
	}

	// Rating keys to look at, with the latest time we saw each one viewed.
	seen := map[string]int64{}
	var order []string
	add := func(key string, t int64) {
		if key == "" {
			return
		}
		if _, ok := seen[key]; !ok {
			order = append(order, key)
		}
		if t > seen[key] {
			seen[key] = t
		}
	}
	var plays []HistoryEntry
	if err := c.History(ctx, since, func(page []HistoryEntry) error {
		for _, h := range page {
			if h.Type == "" || h.Type == "movie" || h.Type == "episode" {
				if OwnerPlay(h) {
					add(h.RatingKey, h.ViewedAt)
					plays = append(plays, h)
				} else {
					// A friend's play only says the item is worth re-reading. Its
					// time must not make the owner's older Plex state look new, or
					// it rolls back progress made in Plexodus (seen 2026-11-13).
					add(h.RatingKey, 0)
				}
			}
		}
		return nil
	}); err != nil {
		return 0, 0, fmt.Errorf("history: %w", err)
	}
	deck, err := c.OnDeck(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("on deck: %w", err)
	}
	for _, m := range deck {
		if m.ViewOffset > 0 {
			add(m.RatingKey, m.LastViewedAt)
		}
	}

	// Re-read current state: history says something was played, not what
	// the owner's state is now, and other accounts' plays show up there too.
	for i := 0; i < len(order); i += 50 {
		end := min(i+50, len(order))
		items, err := c.Items(ctx, order[i:end])
		if err != nil {
			return checked, updated, fmt.Errorf("items: %w", err)
		}
		n, err := s.apply(ctx, uid, items, seen, pm)
		checked += len(items)
		updated += n
		if err != nil {
			return checked, updated, err
		}
	}
	// Items are linked by now (apply links new titles by path), so each
	// play can be logged for Wrapped.
	if err := s.recordPlays(ctx, uid, plays); err != nil {
		return checked, updated, err
	}
	if err := s.db.SetSetting(ctx, "plex_sync_cursor", strconv.FormatInt(started, 10)); err != nil {
		return checked, updated, err
	}
	return checked, updated, s.backfillHistory(ctx, c, uid)
}

// OwnerPlay reports whether a history entry is the server owner's own view
// (Plex numbers the owner's local account 1); friends' plays are left out
// of the owner's Wrapped.
func OwnerPlay(h HistoryEntry) bool { return h.AccountID == 0 || h.AccountID == 1 }

// recordPlays logs Plex views in watch_history, for items linked to Plex.
func (s *Syncer) recordPlays(ctx context.Context, uid int64, plays []HistoryEntry) error {
	if len(plays) == 0 {
		return nil
	}
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, h := range plays {
			ids, err := queryIDs(tx, `SELECT item_id FROM plex_links WHERE rating_key = ?`, h.RatingKey)
			if err != nil {
				return err
			}
			for _, id := range ids {
				if err := db.RecordPlay(ctx, tx, uid, id, h.ViewedAt, playLength(tx, id), "plex"); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// playLength is an item's length in seconds from what's already in the
// database (never the file): the probed duration, else the runtime.
func playLength(tx *sql.Tx, itemID int64) float64 {
	var secs float64
	tx.QueryRow(`SELECT COALESCE((SELECT MAX(duration) FROM media_files WHERE item_id = ?1),
		(SELECT runtime * 60 FROM items WHERE id = ?1), 0)`, itemID).Scan(&secs)
	return secs
}

// backfillHistory reads Plex's whole play history once (the regular sync
// only looks back from its cursor), so Wrapped covers the years before
// Plexodus. It only adds history rows; watch state is left to the sync.
func (s *Syncer) backfillHistory(ctx context.Context, c *Client, uid int64) error {
	if s.db.Setting(ctx, "plex_history_backfill") == "done" {
		return nil
	}
	var all []HistoryEntry
	if err := c.History(ctx, 1, func(page []HistoryEntry) error {
		for _, h := range page {
			if (h.Type == "" || h.Type == "movie" || h.Type == "episode") && OwnerPlay(h) {
				all = append(all, h)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("history backfill: %w", err)
	}
	for i := 0; i < len(all); i += 500 {
		if err := s.recordPlays(ctx, uid, all[i:min(i+500, len(all))]); err != nil {
			return err
		}
	}
	log.Printf("plex watch sync: %d past plays read for Wrapped", len(all))
	return s.db.SetSetting(ctx, "plex_history_backfill", "done")
}

// State is a Plex item's watch state as Plexodus stores it.
type State struct {
	Position  float64
	Duration  float64
	Watched   bool
	PlayCount int
	At        int64
}

// PlexState converts Plex's view fields to a watch state. ok is false when
// Plex has nothing to say (never played).
func PlexState(m *Metadata, seenAt int64) (st State, ok bool) {
	if m.ViewCount == 0 && m.ViewOffset == 0 {
		return st, false
	}
	st.At = max(m.LastViewedAt, seenAt)
	st.Duration = float64(m.Duration) / 1000
	st.PlayCount = m.ViewCount
	if m.ViewOffset > 0 {
		st.Position = float64(m.ViewOffset) / 1000
	} else {
		st.Watched = true
	}
	return st, true
}

func (s *Syncer) apply(ctx context.Context, uid int64, items []Metadata, seen map[string]int64, pm PathMap) (int, error) {
	n := 0
	err := s.db.Tx(ctx, func(tx *sql.Tx) error {
		for i := range items {
			m := &items[i]
			if m.Type != "movie" && m.Type != "episode" {
				continue
			}
			st, ok := PlexState(m, seen[m.RatingKey])
			if !ok {
				continue
			}
			ids, err := matchItems(tx, m, pm)
			if err != nil {
				return err
			}
			for _, id := range ids {
				changed, err := ApplyState(tx, uid, id, st)
				if err != nil {
					return err
				}
				if changed {
					n++
				}
			}
		}
		return nil
	})
	return n, err
}

// matchItems finds the Plexodus items for a Plex item: through the link the
// import made, else by file path (for titles that arrived after the import),
// remembering the link for next time.
func matchItems(tx *sql.Tx, m *Metadata, pm PathMap) ([]int64, error) {
	ids, err := queryIDs(tx, `SELECT item_id FROM plex_links WHERE rating_key = ?`, m.RatingKey)
	if err != nil || len(ids) > 0 {
		return ids, err
	}
	for _, media := range m.Media {
		for _, part := range media.Part {
			if part.File == "" {
				continue
			}
			found, err := queryIDs(tx, `SELECT item_id FROM media_files WHERE path = ?`, pm.Apply(part.File))
			if err != nil {
				return nil, err
			}
			for _, id := range found {
				if _, err := tx.Exec(`INSERT INTO plex_links(item_id, rating_key) VALUES(?, ?)
					ON CONFLICT(item_id) DO UPDATE SET rating_key = excluded.rating_key`, id, m.RatingKey); err != nil {
					return nil, err
				}
				ids = append(ids, id)
			}
			if len(ids) > 0 {
				return ids, nil
			}
		}
	}
	return nil, nil
}

func queryIDs(tx *sql.Tx, q string, args ...any) ([]int64, error) {
	rows, err := tx.Query(q, args...)
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

// ApplyState writes Plex's state for one item unless Plexodus has a newer
// (or equally new) record, so progress made here is never rolled back by an
// older play in Plex. It reports whether anything changed.
func ApplyState(tx *sql.Tx, uid, itemID int64, st State) (bool, error) {
	var cur struct {
		pos, dur  float64
		watched   bool
		plays     int
		updatedAt int64
	}
	err := tx.QueryRow(`SELECT position, duration, watched, play_count, updated_at FROM watch_state WHERE user_id = ? AND item_id = ?`,
		uid, itemID).Scan(&cur.pos, &cur.dur, &cur.watched, &cur.plays, &cur.updatedAt)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	at := st.At
	if at == 0 {
		if exists {
			return false, nil // can't tell which is newer; keep ours
		}
		at = db.Now()
	}
	if exists && cur.updatedAt >= at {
		return false, nil
	}
	dur := st.Duration
	if dur == 0 {
		dur = cur.dur
	}
	plays := max(cur.plays, st.PlayCount)
	// A rewatch in progress shows as in progress (as on Plex's On Deck);
	// play_count still records that it was watched before.
	_, err = tx.Exec(`INSERT INTO watch_state(user_id, item_id, position, duration, watched, play_count, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, item_id) DO UPDATE SET position = excluded.position, duration = excluded.duration,
			watched = excluded.watched, play_count = excluded.play_count, updated_at = excluded.updated_at`,
		uid, itemID, st.Position, dur, st.Watched, plays, at)
	return err == nil, err
}
