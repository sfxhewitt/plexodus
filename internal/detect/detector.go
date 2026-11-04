package detect

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/sfxhewitt/plexodus/internal/db"
)

// Version is bumped when detection improves enough to be worth re-running on
// items it has already looked at.
const Version = 1

const (
	headSec = 10 * 60 // intros and recaps are looked for in the first 10 minutes
	tailSec = 6 * 60  // closing credits in the last 6
	maxRefs = 2       // earlier episodes a recap is compared against
	// SettingKey turns detection off when set to "off".
	SettingKey = "detect_markers"
)

// Detector runs detection jobs one at a time in the background.
type Detector struct {
	db      *db.DB
	ffmpeg  string
	ffprobe string

	mu      sync.Mutex
	queue   []int64
	queued  map[int64]bool
	running int64
	asked   map[int64]time.Time // items already requested this run
	kick    chan struct{}
}

func New(d *db.DB, ffmpeg, ffprobe string) *Detector {
	return &Detector{db: d, ffmpeg: ffmpeg, ffprobe: ffprobe, queued: map[int64]bool{}, asked: map[int64]time.Time{}, kick: make(chan struct{}, 1)}
}

func (d *Detector) Enabled(ctx context.Context) bool {
	return d.db.Setting(ctx, SettingKey) != "off"
}

// OnPlay asks for markers for an item that has just started playing. It
// goes to the front of the queue so a recap or intro can still be skipped.
func (d *Detector) OnPlay(ctx context.Context, itemID int64) {
	d.request(ctx, itemID, true)
}

// OnNearEnd prepares the next episode while this one finishes, so its
// markers are ready when it starts (and this episode's audio is already in
// the mount's cache).
func (d *Detector) OnNearEnd(ctx context.Context, itemID int64) {
	if next := d.nextEpisode(ctx, itemID); next != 0 {
		d.request(ctx, next, false)
	}
}

// Pending reports whether detection for the item is queued or running.
func (d *Detector) Pending(itemID int64) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.queued[itemID] || d.running == itemID
}

func (d *Detector) request(ctx context.Context, itemID int64, front bool) {
	if !d.Enabled(ctx) {
		return
	}
	d.mu.Lock()
	if t, ok := d.asked[itemID]; ok && time.Since(t) < time.Hour {
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()
	if !d.needed(ctx, itemID) {
		return
	}
	d.mu.Lock()
	d.asked[itemID] = time.Now()
	if !d.queued[itemID] && d.running != itemID {
		d.queued[itemID] = true
		if front {
			d.queue = append([]int64{itemID}, d.queue...)
		} else {
			d.queue = append(d.queue, itemID)
		}
	}
	d.mu.Unlock()
	select {
	case d.kick <- struct{}{}:
	default:
	}
}

// needed is false once the item was looked at by this version, unless a
// marker is still missing and a week has passed (new episodes may have
// arrived that it can be compared with).
func (d *Detector) needed(ctx context.Context, itemID int64) bool {
	var version, doneAt int64
	err := d.db.R.QueryRowContext(ctx, `SELECT version, done_at FROM detect_runs WHERE item_id = ?`, itemID).Scan(&version, &doneAt)
	if err != nil {
		return true
	}
	if version < Version {
		return true
	}
	if time.Since(time.Unix(doneAt, 0)) < 7*24*time.Hour {
		return false
	}
	var kinds int
	d.db.R.QueryRowContext(ctx, `SELECT COUNT(DISTINCT type) FROM markers WHERE item_id = ?`, itemID).Scan(&kinds)
	return kinds < 3
}

// Run processes jobs until ctx is cancelled.
func (d *Detector) Run(ctx context.Context) {
	for {
		d.mu.Lock()
		var id int64
		if len(d.queue) > 0 {
			id = d.queue[0]
			d.queue = d.queue[1:]
			delete(d.queued, id)
			d.running = id
		}
		d.mu.Unlock()
		if id == 0 {
			select {
			case <-ctx.Done():
				return
			case <-d.kick:
			}
			continue
		}
		start := time.Now()
		note, err := d.Detect(ctx, id)
		d.mu.Lock()
		d.running = 0
		d.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			note = "error: " + err.Error()
			log.Printf("detect item %d: %v", id, err)
		} else {
			log.Printf("detect item %d: %s (%.1fs)", id, note, time.Since(start).Seconds())
		}
		d.db.W.ExecContext(ctx, `INSERT INTO detect_runs(item_id, version, done_at, note) VALUES(?, ?, ?, ?)
			ON CONFLICT(item_id) DO UPDATE SET version = excluded.version, done_at = excluded.done_at, note = excluded.note`,
			id, Version, db.Now(), note)
	}
}

type file struct {
	id       int64
	path     string
	size     int64
	duration float64
}

type episode struct {
	id     int64
	season int
	file   file
}

func (d *Detector) itemFile(ctx context.Context, itemID int64) (file, error) {
	var f file
	err := d.db.R.QueryRowContext(ctx, `SELECT id, path, size, COALESCE(duration, 0) FROM media_files WHERE item_id = ? ORDER BY id LIMIT 1`, itemID).
		Scan(&f.id, &f.path, &f.size, &f.duration)
	return f, err
}

// showEpisodes lists the show's episodes in watch order.
func (d *Detector) showEpisodes(ctx context.Context, itemID int64) ([]episode, int, error) {
	rows, err := d.db.R.QueryContext(ctx, `
		SELECT e.id, COALESCE(s.season_number, 0), f.id, f.path, f.size, COALESCE(f.duration, 0)
		FROM items x
		JOIN items xs ON xs.id = x.parent_id
		JOIN items s ON s.parent_id = xs.parent_id AND s.kind = 'season'
		JOIN items e ON e.parent_id = s.id AND e.kind = 'episode'
		JOIN media_files f ON f.id = (SELECT MIN(id) FROM media_files WHERE item_id = e.id)
		WHERE x.id = ? AND x.kind = 'episode'
		ORDER BY s.season_number, e.episode_number, e.id`, itemID)
	if err != nil {
		return nil, -1, err
	}
	defer rows.Close()
	var eps []episode
	at := -1
	for rows.Next() {
		var e episode
		if err := rows.Scan(&e.id, &e.season, &e.file.id, &e.file.path, &e.file.size, &e.file.duration); err != nil {
			return nil, -1, err
		}
		if e.id == itemID {
			at = len(eps)
		}
		eps = append(eps, e)
	}
	return eps, at, rows.Err()
}

func (d *Detector) nextEpisode(ctx context.Context, itemID int64) int64 {
	eps, at, err := d.showEpisodes(ctx, itemID)
	if err != nil || at < 0 || at+1 >= len(eps) {
		return 0
	}
	return eps[at+1].id
}

// markerSet is an item's markers by type.
type markerSet map[string]struct {
	Span
	source string
}

func (d *Detector) markers(ctx context.Context, itemID int64) markerSet {
	out := markerSet{}
	rows, err := d.db.R.QueryContext(ctx, `SELECT type, start, "end", source FROM markers WHERE item_id = ? ORDER BY start`, itemID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var t, src string
		var s Span
		rows.Scan(&t, &s.Start, &s.End, &src)
		if cur, ok := out[t]; ok && sourceRank(cur.source) >= sourceRank(src) {
			continue
		}
		out[t] = struct {
			Span
			source string
		}{s, src}
	}
	return out
}

// sourceRank orders marker sources by trust: Plex, then chapter names, then
// our own audio matching.
func sourceRank(src string) int {
	switch src {
	case "plex":
		return 3
	case "chapter":
		return 2
	case "audio":
		return 1
	}
	return 0
}

func (d *Detector) save(ctx context.Context, itemID int64, typ, source string, s Span) error {
	return d.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`DELETE FROM markers WHERE item_id = ? AND type = ? AND source = ?`, itemID, typ, source); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT OR REPLACE INTO markers(item_id, type, start, "end", source) VALUES(?, ?, ?, ?, ?)`,
			itemID, typ, round(s.Start), round(s.End), source)
		return err
	})
}

func round(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

func (d *Detector) haveFFmpeg() bool {
	_, err := exec.LookPath(d.ffmpeg)
	return err == nil
}

// Detect looks for intro, credits and recap markers for one item.
func (d *Detector) Detect(ctx context.Context, itemID int64) (string, error) {
	var kind string
	if err := d.db.R.QueryRowContext(ctx, `SELECT kind FROM items WHERE id = ?`, itemID).Scan(&kind); err != nil {
		return "", err
	}
	f, err := d.itemFile(ctx, itemID)
	if err != nil {
		return "no media file", nil
	}
	var found []string

	// 1. Chapter names.
	if cs, err := Chapters(ctx, d.ffprobe, f.path); err == nil {
		have := d.markers(ctx, itemID)
		for typ, s := range ChapterMarkers(cs, f.duration) {
			if cur, ok := have[typ]; ok && cur.source == "plex" {
				continue
			}
			if err := d.save(ctx, itemID, typ, "chapter", s); err != nil {
				return "", err
			}
			found = append(found, typ+" (chapter)")
		}
	} else if ctx.Err() != nil {
		return "", ctx.Err()
	}

	// 2. Audio, for episodes.
	if kind != "episode" || !d.haveFFmpeg() {
		return summary(found), nil
	}
	eps, at, err := d.showEpisodes(ctx, itemID)
	if err != nil || at < 0 {
		return summary(found), err
	}
	me := eps[at]
	if me.file.duration <= 0 {
		me.file.duration = f.duration
	}
	// The neighbour shares the intro and closing theme: the previous
	// episode in the same season, else the next one.
	var nb *episode
	if at > 0 && eps[at-1].season == me.season {
		nb = &eps[at-1]
	} else if at+1 < len(eps) && eps[at+1].season == me.season {
		nb = &eps[at+1]
	}

	have := d.markers(ctx, itemID)
	if _, ok := have["intro"]; !ok && nb != nil {
		a, _, err := d.head(ctx, me.file)
		if err != nil {
			return summary(found), err
		}
		b, _, err := d.head(ctx, nb.file)
		if err != nil {
			return summary(found), err
		}
		// Intros sit in the first part of an episode; on short episodes the
		// head would otherwise reach the closing theme.
		a, b = a[:min(len(a), frames(0.45*me.file.duration))], b[:min(len(b), frames(0.45*nb.file.duration))]
		if ia, ib, ok := FindIntro(a, b); ok {
			if err := d.save(ctx, me.id, "intro", "audio", ia); err != nil {
				return "", err
			}
			found = append(found, fmt.Sprintf("intro %.0f-%.0fs", ia.Start, ia.End))
			if _, ok := d.markers(ctx, nb.id)["intro"]; !ok {
				d.save(ctx, nb.id, "intro", "audio", ib)
			}
		}
	}
	if _, ok := have["credits"]; !ok && nb != nil && me.file.duration > tailSec && nb.file.duration > tailSec {
		a, aOff, err := d.tail(ctx, me.file)
		if err != nil {
			return summary(found), err
		}
		b, bOff, err := d.tail(ctx, nb.file)
		if err != nil {
			return summary(found), err
		}
		if cs, ok := FindCredits(a, aOff, me.file.duration, b, bOff); ok {
			if err := d.save(ctx, me.id, "credits", "audio", cs); err != nil {
				return "", err
			}
			found = append(found, fmt.Sprintf("credits %.0f-%.0fs", cs.Start, cs.End))
		}
	}

	// 3. Recap: compare the start with the episodes before it.
	have = d.markers(ctx, itemID)
	if _, ok := have["recap"]; !ok && at > 0 {
		head, _, err := d.head(ctx, me.file)
		if err != nil {
			return summary(found), err
		}
		var intro *Span
		if m, ok := have["intro"]; ok {
			intro = &m.Span
		}
		// Compare with the previous episode, reading it once if needed (when
		// binge-watching it was fingerprinted while it finished). Older
		// episodes are only used if they are already fingerprinted, so a
		// show without recaps costs at most one extra read per episode.
		var refs []Ref
		for i := at - 1; i >= 0 && i >= at-maxRefs; i-- {
			var fp []uint32
			if i == at-1 {
				fp, err = d.full(ctx, eps[i].file)
			} else if c, _, ok := d.cached(ctx, eps[i].file, "full"); ok {
				fp = c
			} else {
				continue
			}
			if err != nil {
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				log.Printf("detect: fingerprint %s: %v", eps[i].file.path, err)
				continue
			}
			var ex []Span
			for _, m := range d.markers(ctx, eps[i].id) {
				ex = append(ex, m.Span)
			}
			refs = append(refs, Ref{FP: fp, Exclude: ex})
		}
		if rs, ok := FindRecap(head, intro, refs); ok {
			if err := d.save(ctx, me.id, "recap", "audio", rs); err != nil {
				return "", err
			}
			found = append(found, fmt.Sprintf("recap %.0f-%.0fs", rs.Start, rs.End))
		}
	}
	return summary(found), nil
}

func summary(found []string) string {
	if len(found) == 0 {
		return "nothing new"
	}
	return "found " + strings.Join(found, ", ")
}

// ---- fingerprint cache ----

func (d *Detector) cached(ctx context.Context, f file, part string) ([]uint32, float64, bool) {
	var off float64
	var data []byte
	err := d.db.R.QueryRowContext(ctx, `SELECT offset, data FROM fingerprints WHERE file_id = ? AND part = ? AND size = ?`, f.id, part, f.size).
		Scan(&off, &data)
	if err != nil {
		return nil, 0, false
	}
	return Decode(data), off, true
}

func (d *Detector) store(ctx context.Context, f file, part string, off float64, fp []uint32) {
	d.db.W.ExecContext(ctx, `INSERT OR REPLACE INTO fingerprints(file_id, part, offset, size, data, created_at) VALUES(?, ?, ?, ?, ?, ?)`,
		f.id, part, off, f.size, Encode(fp), db.Now())
}

var errEmpty = errors.New("no audio decoded")

func (d *Detector) head(ctx context.Context, f file) ([]uint32, float64, error) {
	if fp, _, ok := d.cached(ctx, f, "full"); ok {
		return fp[:min(len(fp), frames(headSec))], 0, nil
	}
	if fp, off, ok := d.cached(ctx, f, "head"); ok {
		return fp, off, nil
	}
	fp, err := Extract(ctx, d.ffmpeg, f.path, 0, headSec)
	if err != nil {
		return nil, 0, err
	}
	if len(fp) == 0 {
		return nil, 0, errEmpty
	}
	d.store(ctx, f, "head", 0, fp)
	return fp, 0, nil
}

func (d *Detector) tail(ctx context.Context, f file) ([]uint32, float64, error) {
	start := max(0, f.duration-tailSec)
	if fp, _, ok := d.cached(ctx, f, "full"); ok {
		lo := min(len(fp), frames(start))
		return fp[lo:], sec(lo, 0), nil
	}
	if fp, off, ok := d.cached(ctx, f, "tail"); ok {
		return fp, off, nil
	}
	fp, err := Extract(ctx, d.ffmpeg, f.path, start, 0)
	if err != nil {
		return nil, 0, err
	}
	if len(fp) == 0 {
		return nil, 0, errEmpty
	}
	d.store(ctx, f, "tail", start, fp)
	return fp, start, nil
}

func (d *Detector) full(ctx context.Context, f file) ([]uint32, error) {
	if fp, _, ok := d.cached(ctx, f, "full"); ok {
		return fp, nil
	}
	fp, err := Extract(ctx, d.ffmpeg, f.path, 0, 0)
	if err != nil {
		return nil, err
	}
	if len(fp) == 0 {
		return nil, errEmpty
	}
	d.store(ctx, f, "full", 0, fp)
	// The full print replaces the partial ones.
	d.db.W.ExecContext(ctx, `DELETE FROM fingerprints WHERE file_id = ? AND part IN ('head', 'tail')`, f.id)
	return fp, nil
}
