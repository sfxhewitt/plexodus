// Package scanner keeps the database in sync with library folders.
//
// It is designed for slow, high-latency filesystems such as rclone mounts of
// cloud storage: it never opens media files, only lists directories and
// compares each file's size and mtime against what is already stored.
// Directories are listed concurrently because each listing on a remote mount
// is dominated by round-trip latency, not bandwidth.
package scanner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sfxhewitt/plexodus/internal/db"
)

type Scanner struct {
	db      *db.DB
	workers int
	// Changed is signalled (non-blocking) after a scan adds or changes files.
	Changed chan struct{}

	mu      sync.Mutex
	running map[int64]*Progress
	last    map[int64]Progress
}

type Progress struct {
	LibraryID int64     `json:"libraryId"`
	Started   time.Time `json:"started"`
	Finished  time.Time `json:"finished,omitempty"`
	Running   bool      `json:"running"`
	Dirs      int64     `json:"dirs"`
	DirsSkip  int64     `json:"dirsSkipped"`
	Files     int64     `json:"files"`
	Added     int64     `json:"added"`
	Updated   int64     `json:"updated"`
	Removed   int64     `json:"removed"`
	Error     string    `json:"error,omitempty"`
}

func New(d *db.DB, workers int) *Scanner {
	if workers < 1 {
		workers = 1
	}
	return &Scanner{
		db: d, workers: workers,
		Changed: make(chan struct{}, 1),
		running: map[int64]*Progress{},
		last:    map[int64]Progress{},
	}
}

// Status returns the progress of running scans and the result of the last
// finished scan for each library.
func (s *Scanner) Status() []Progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Progress{}
	for id, p := range s.last {
		if _, ok := s.running[id]; !ok {
			out = append(out, p)
		}
	}
	for _, p := range s.running {
		cp := Progress{
			LibraryID: p.LibraryID, Started: p.Started, Running: true,
			Dirs: atomic.LoadInt64(&p.Dirs), DirsSkip: atomic.LoadInt64(&p.DirsSkip),
			Files: atomic.LoadInt64(&p.Files), Added: atomic.LoadInt64(&p.Added),
			Updated: atomic.LoadInt64(&p.Updated), Removed: atomic.LoadInt64(&p.Removed),
		}
		out = append(out, cp)
	}
	return out
}

type Library struct {
	ID            int64
	Kind          string
	Roots         []string
	TrustDirMtime bool
}

func LoadLibrary(ctx context.Context, d *sql.DB, id int64) (*Library, error) {
	var lib Library
	var paths string
	err := d.QueryRowContext(ctx, `SELECT id, kind, paths, trust_dir_mtime FROM libraries WHERE id = ?`, id).
		Scan(&lib.ID, &lib.Kind, &paths, &lib.TrustDirMtime)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(paths), &lib.Roots); err != nil {
		return nil, err
	}
	for i, r := range lib.Roots {
		lib.Roots[i] = filepath.Clean(r)
	}
	return &lib, nil
}

type Options struct {
	// Paths limits the scan to these folders or files inside the library
	// (used by Sonarr/Radarr/autoscan style "scan this path" triggers).
	Paths []string
	// Full ignores remembered directory mtimes and lists everything.
	Full bool
}

var ErrBusy = errors.New("scan already running for this library")

type knownFile struct {
	id    int64
	size  int64
	mtime int64
}

type foundFile struct {
	path  string
	root  string
	size  int64
	mtime int64
}

type foundDir struct {
	path, parent string
	mtime        int64
}

type task struct {
	dir, root string
}

func under(path, prefix string) bool {
	return path == prefix || strings.HasPrefix(path, prefix+string(filepath.Separator))
}

func skipDir(name string) bool {
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "@") || name == "lost+found" || name == "$RECYCLE.BIN" {
		return true
	}
	l := strings.ToLower(name)
	switch l {
	case "extras", "featurettes", "behind the scenes", "deleted scenes", "trailers", "interviews", "shorts", "samples", "sample":
		return true
	}
	return false
}

// Scan scans one library. It returns ErrBusy if that library is already
// being scanned.
func (s *Scanner) Scan(ctx context.Context, libID int64, opts Options) (Progress, error) {
	s.mu.Lock()
	if _, busy := s.running[libID]; busy {
		s.mu.Unlock()
		return Progress{}, ErrBusy
	}
	prog := &Progress{LibraryID: libID, Started: time.Now(), Running: true}
	s.running[libID] = prog
	s.mu.Unlock()

	err := s.scan(ctx, libID, opts, prog)

	s.mu.Lock()
	delete(s.running, libID)
	res := Progress{
		LibraryID: libID, Started: prog.Started, Finished: time.Now(),
		Dirs: prog.Dirs, DirsSkip: prog.DirsSkip, Files: prog.Files,
		Added: prog.Added, Updated: prog.Updated, Removed: prog.Removed,
	}
	if err != nil {
		res.Error = err.Error()
	}
	s.last[libID] = res
	s.mu.Unlock()

	if res.Added+res.Updated+res.Removed > 0 {
		select {
		case s.Changed <- struct{}{}:
		default:
		}
	}
	log.Printf("scan library %d: %d dirs (%d skipped), %d files, +%d ~%d -%d in %s (err=%v)",
		libID, res.Dirs, res.DirsSkip, res.Files, res.Added, res.Updated, res.Removed,
		res.Finished.Sub(res.Started).Round(time.Millisecond), err)
	return res, err
}

func (s *Scanner) scan(ctx context.Context, libID int64, opts Options, prog *Progress) error {
	lib, err := LoadLibrary(ctx, s.db.R, libID)
	if err != nil {
		return err
	}

	// Work out which (root, start dir) pairs to walk.
	var starts []task
	if len(opts.Paths) == 0 {
		for _, r := range lib.Roots {
			starts = append(starts, task{dir: r, root: r})
		}
	} else {
		for _, p := range opts.Paths {
			p = filepath.Clean(p)
			for _, r := range lib.Roots {
				if under(p, r) {
					if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
						p = filepath.Dir(p)
					}
					starts = append(starts, task{dir: p, root: r})
					break
				}
			}
		}
		if len(starts) == 0 {
			return fmt.Errorf("none of the paths are inside library %d", libID)
		}
	}

	// A root that is missing or unexpectedly empty usually means the rclone
	// mount dropped. Never delete anything under such a root.
	reachable := map[string]bool{}
	for _, st := range starts {
		entries, err := os.ReadDir(st.dir)
		reachable[st.dir] = err == nil && (len(entries) > 0 || st.dir != st.root)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("scan: %s unreachable: %v", st.dir, err)
		}
		if errors.Is(err, os.ErrNotExist) && st.dir != st.root {
			// A sub-path that no longer exists was deleted; its files go.
			reachable[st.dir] = true
		}
	}

	known, err := s.loadKnown(ctx, libID, starts)
	if err != nil {
		return err
	}
	byDir := map[string][]string{}
	for p := range known {
		byDir[filepath.Dir(p)] = append(byDir[filepath.Dir(p)], p)
	}
	knownDirs := map[string]int64{}
	childDirs := map[string][]string{}
	if lib.TrustDirMtime && !opts.Full {
		rows, err := s.db.R.QueryContext(ctx, `SELECT path, COALESCE(parent, ''), mtime FROM directories WHERE library_id = ?`, libID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var p, parent string
			var m int64
			rows.Scan(&p, &parent, &m)
			knownDirs[p] = m
			childDirs[parent] = append(childDirs[parent], p)
		}
		rows.Close()
	}

	seen := make(map[string]bool, len(known))
	var seenMu sync.Mutex
	files := make(chan foundFile, 256)
	dirs := make(chan foundDir, 256)

	// Walk with a bounded pool; pending counts queued-but-unfinished dirs.
	queue := make(chan task, 1<<16)
	var pending sync.WaitGroup
	var walkErr atomic.Value
	push := func(t task) {
		pending.Add(1)
		select {
		case queue <- t:
		default:
			// Queue full: process inline in a goroutine to avoid deadlock.
			go func() { queue <- t }()
		}
	}
	visit := func(t task) {
		defer pending.Done()
		if ctx.Err() != nil {
			return
		}
		fi, err := os.Stat(t.dir)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				walkErr.Store(err)
				// Treat everything under an unreadable dir as still present.
				seenMu.Lock()
				for p := range known {
					if under(p, t.dir) {
						seen[p] = true
					}
				}
				seenMu.Unlock()
			}
			return
		}
		mtime := fi.ModTime().Unix()
		parent := filepath.Dir(t.dir)
		if t.dir == t.root {
			parent = ""
		}
		dirs <- foundDir{path: t.dir, parent: parent, mtime: mtime}

		if m, ok := knownDirs[t.dir]; ok && m == mtime {
			// Unchanged directory: trust our records, skip the listing.
			atomic.AddInt64(&prog.DirsSkip, 1)
			seenMu.Lock()
			for _, p := range byDir[t.dir] {
				seen[p] = true
			}
			seenMu.Unlock()
			atomic.AddInt64(&prog.Files, int64(len(byDir[t.dir])))
			for _, c := range childDirs[t.dir] {
				push(task{dir: c, root: t.root})
			}
			return
		}

		atomic.AddInt64(&prog.Dirs, 1)
		f, err := os.Open(t.dir)
		if err != nil {
			walkErr.Store(err)
			return
		}
		// Readdir (not ReadDir) fetches file info in the same pass; on FUSE
		// mounts rclone answers these from its directory cache.
		infos, err := f.Readdir(-1)
		f.Close()
		if err != nil {
			walkErr.Store(err)
			seenMu.Lock()
			for p := range known {
				if under(p, t.dir) {
					seen[p] = true
				}
			}
			seenMu.Unlock()
			return
		}
		for _, info := range infos {
			name := info.Name()
			full := filepath.Join(t.dir, name)
			if info.IsDir() {
				if !skipDir(name) {
					push(task{dir: full, root: t.root})
				}
				continue
			}
			if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
				continue
			}
			if !IsVideo(name) {
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				if target, err := os.Stat(full); err == nil {
					info = target
				} else {
					continue
				}
			}
			atomic.AddInt64(&prog.Files, 1)
			seenMu.Lock()
			seen[full] = true
			seenMu.Unlock()
			ff := foundFile{path: full, root: t.root, size: info.Size(), mtime: info.ModTime().Unix()}
			if k, ok := known[full]; ok && k.size == ff.size && k.mtime == ff.mtime {
				continue
			}
			files <- ff
		}
	}

	var workers sync.WaitGroup
	for i := 0; i < s.workers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for t := range queue {
				visit(t)
			}
		}()
	}

	// Collectors: write changed files in batches, remember directory mtimes.
	collectErr := make(chan error, 1)
	go func() { collectErr <- s.collect(ctx, lib, files, known, prog) }()
	dirErr := make(chan error, 1)
	go func() { dirErr <- s.collectDirs(ctx, libID, dirs) }()

	for _, st := range starts {
		push(st)
	}
	pending.Wait()
	close(queue)
	workers.Wait()
	close(files)
	close(dirs)
	if err := <-collectErr; err != nil {
		return err
	}
	if err := <-dirErr; err != nil {
		return err
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Remove files that disappeared, but only under reachable starts.
	var gone []int64
	for p, k := range known {
		if seen[p] {
			continue
		}
		for _, st := range starts {
			if under(p, st.dir) && reachable[st.dir] {
				gone = append(gone, k.id)
				break
			}
		}
	}
	if len(gone) > 0 {
		if err := s.removeFiles(ctx, gone); err != nil {
			return err
		}
		atomic.AddInt64(&prog.Removed, int64(len(gone)))
	}

	if len(opts.Paths) == 0 {
		s.db.W.ExecContext(ctx, `UPDATE libraries SET last_scan_at = ? WHERE id = ?`, db.Now(), libID)
	}
	if e, ok := walkErr.Load().(error); ok && e != nil {
		return fmt.Errorf("some folders could not be read (last error: %w)", e)
	}
	return nil
}

func (s *Scanner) loadKnown(ctx context.Context, libID int64, starts []task) (map[string]knownFile, error) {
	known := map[string]knownFile{}
	for _, st := range starts {
		// Range query on the path prefix uses the UNIQUE(path) index.
		lo := st.dir + string(filepath.Separator)
		hi := st.dir + string(rune(filepath.Separator+1))
		rows, err := s.db.R.QueryContext(ctx,
			`SELECT id, path, size, mtime FROM media_files WHERE library_id = ? AND path >= ? AND path < ?`, libID, lo, hi)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k knownFile
			var p string
			if err := rows.Scan(&k.id, &p, &k.size, &k.mtime); err != nil {
				rows.Close()
				return nil, err
			}
			known[p] = k
		}
		rows.Close()
	}
	return known, nil
}

func (s *Scanner) collectDirs(ctx context.Context, libID int64, dirs <-chan foundDir) error {
	var batch []foundDir
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := s.db.Tx(ctx, func(tx *sql.Tx) error {
			st, err := tx.Prepare(`INSERT INTO directories(library_id, path, parent, mtime) VALUES(?, ?, NULLIF(?, ''), ?)
				ON CONFLICT(library_id, path) DO UPDATE SET mtime = excluded.mtime, parent = excluded.parent`)
			if err != nil {
				return err
			}
			defer st.Close()
			for _, d := range batch {
				if _, err := st.Exec(libID, d.path, d.parent, d.mtime); err != nil {
					return err
				}
			}
			return nil
		})
		batch = batch[:0]
		return err
	}
	var firstErr error
	for d := range dirs {
		batch = append(batch, d)
		if len(batch) >= 500 {
			if err := flush(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	if err := flush(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (s *Scanner) collect(ctx context.Context, lib *Library, files <-chan foundFile, known map[string]knownFile, prog *Progress) error {
	var batch []foundFile
	var firstErr error
	flush := func() {
		if len(batch) == 0 {
			return
		}
		err := s.db.Tx(ctx, func(tx *sql.Tx) error {
			for _, f := range batch {
				if k, ok := known[f.path]; ok {
					// Same path, new size/mtime: file was replaced; re-probe it.
					if _, err := tx.Exec(`UPDATE media_files SET size = ?, mtime = ?, probed_at = NULL, probe_error = NULL WHERE id = ?`,
						f.size, f.mtime, k.id); err != nil {
						return err
					}
					atomic.AddInt64(&prog.Updated, 1)
					continue
				}
				itemID, err := upsertItemFor(tx, lib, f)
				if err != nil {
					log.Printf("scan: %s: %v", f.path, err)
					continue
				}
				if itemID == 0 {
					continue
				}
				if _, err := tx.Exec(`INSERT INTO media_files(item_id, library_id, path, size, mtime) VALUES(?, ?, ?, ?, ?)
					ON CONFLICT(path) DO UPDATE SET item_id = excluded.item_id, library_id = excluded.library_id,
						size = excluded.size, mtime = excluded.mtime, probed_at = NULL`,
					itemID, lib.ID, f.path, f.size, f.mtime); err != nil {
					return err
				}
				atomic.AddInt64(&prog.Added, 1)
			}
			return nil
		})
		if err != nil && firstErr == nil {
			firstErr = err
		}
		batch = batch[:0]
	}
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case f, ok := <-files:
			if !ok {
				flush()
				return firstErr
			}
			batch = append(batch, f)
			if len(batch) >= 200 {
				flush()
			}
		case <-tick.C:
			// Flush periodically so new items show up during long scans.
			flush()
		}
	}
}

func addedAt(mtime int64) int64 {
	now := db.Now()
	if mtime <= 0 || mtime > now {
		return now
	}
	return mtime
}

// ensureItem inserts an item by guid if missing and returns its id.
func ensureItem(tx *sql.Tx, guid string, libID int64, kind string, parent sql.NullInt64,
	title string, year int, season, episode sql.NullInt64, added int64) (int64, error) {
	var y sql.NullInt64
	if year > 0 {
		y = sql.NullInt64{Int64: int64(year), Valid: true}
	}
	var id int64
	err := tx.QueryRow(`INSERT INTO items(library_id, kind, guid, parent_id, title, sort_title, year,
			season_number, episode_number, added_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(guid) DO UPDATE SET added_at = MAX(items.added_at, excluded.added_at)
		RETURNING id`,
		libID, kind, guid, parent, title, SortTitle(title), y, season, episode, added, db.Now()).Scan(&id)
	return id, err
}

func upsertItemFor(tx *sql.Tx, lib *Library, f foundFile) (int64, error) {
	added := addedAt(f.mtime)
	switch lib.Kind {
	case "movies":
		title, year := MovieInfo(f.path, f.root)
		guid := fmt.Sprintf("movie:%d:%s:%d", lib.ID, strings.ToLower(title), year)
		return ensureItem(tx, guid, lib.ID, "movie", sql.NullInt64{}, title, year, sql.NullInt64{}, sql.NullInt64{}, added)
	case "shows":
		ep, ok := EpisodeFromPath(f.path, f.root)
		if !ok {
			return 0, nil
		}
		showID, err := ensureItem(tx, fmt.Sprintf("show:%d:%s", lib.ID, ep.ShowDir), lib.ID, "show",
			sql.NullInt64{}, ep.ShowTitle, ep.ShowYear, sql.NullInt64{}, sql.NullInt64{}, added)
		if err != nil {
			return 0, err
		}
		seasonTitle := fmt.Sprintf("Season %d", ep.Season)
		if ep.Season == 0 {
			seasonTitle = "Specials"
		}
		seasonID, err := ensureItem(tx, fmt.Sprintf("season:%d:%d", showID, ep.Season), lib.ID, "season",
			sql.NullInt64{Int64: showID, Valid: true}, seasonTitle, 0,
			sql.NullInt64{Int64: int64(ep.Season), Valid: true}, sql.NullInt64{}, added)
		if err != nil {
			return 0, err
		}
		return ensureItem(tx, fmt.Sprintf("episode:%d:%d", seasonID, ep.Episode), lib.ID, "episode",
			sql.NullInt64{Int64: seasonID, Valid: true}, fmt.Sprintf("Episode %d", ep.Episode), 0,
			sql.NullInt64{Int64: int64(ep.Season), Valid: true}, sql.NullInt64{Int64: int64(ep.Episode), Valid: true}, added)
	}
	return 0, fmt.Errorf("unknown library kind %q", lib.Kind)
}

func (s *Scanner) removeFiles(ctx context.Context, ids []int64) error {
	return s.db.Tx(ctx, func(tx *sql.Tx) error {
		for _, id := range ids {
			if _, err := tx.Exec(`DELETE FROM media_files WHERE id = ?`, id); err != nil {
				return err
			}
		}
		// Prune items left without media, bottom-up.
		stmts := []string{
			`DELETE FROM items WHERE kind IN ('movie', 'episode') AND NOT EXISTS (SELECT 1 FROM media_files m WHERE m.item_id = items.id)`,
			`DELETE FROM items WHERE kind = 'season' AND NOT EXISTS (SELECT 1 FROM items c WHERE c.parent_id = items.id)`,
			`DELETE FROM items WHERE kind = 'show' AND NOT EXISTS (SELECT 1 FROM items c WHERE c.parent_id = items.id)`,
		}
		for _, q := range stmts {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		return nil
	})
}
