package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/db"
)

// Card is the compact shape used in grids and rows.
type Card struct {
	ID        int64   `json:"id"`
	LibraryID int64   `json:"libraryId"`
	Kind      string  `json:"kind"`
	Title     string  `json:"title"`
	Year      int     `json:"year,omitempty"`
	Poster    string  `json:"poster,omitempty"`
	Backdrop  string  `json:"backdrop,omitempty"`
	Logo      string  `json:"logo,omitempty"`
	Rating    float64 `json:"rating,omitempty"`
	// Rotten Tomatoes Tomatometer and audience score, 0–100.
	RTCritic      *int    `json:"rtCritic,omitempty"`
	RTAudience    *int    `json:"rtAudience,omitempty"`
	Runtime       int     `json:"runtime,omitempty"`
	SeasonNumber  *int    `json:"seasonNumber,omitempty"`
	EpisodeNumber *int    `json:"episodeNumber,omitempty"`
	ParentID      int64   `json:"parentId,omitempty"`
	ShowID        int64   `json:"showId,omitempty"`
	ShowTitle     string  `json:"showTitle,omitempty"`
	ShowPoster    string  `json:"showPoster,omitempty"`
	ShowBackdrop  string  `json:"showBackdrop,omitempty"`
	AddedAt       int64   `json:"addedAt"`
	Position      float64 `json:"position,omitempty"`
	Duration      float64 `json:"duration,omitempty"`
	Watched       bool    `json:"watched,omitempty"`
	LeafCount     int     `json:"leafCount,omitempty"`
	ViewedLeaves  int     `json:"viewedLeafCount,omitempty"`
	Overview      string  `json:"overview,omitempty"`
	Watchlisted   bool    `json:"watchlisted,omitempty"`
	// Match is how well the title fits the mood asked for, 40–99 ("87% match").
	Match int `json:"match,omitempty"`
	// Note replaces the card's subtitle in smart rows ("Season 2 · 3 left").
	Note string `json:"note,omitempty"`
}

// cardCols selects card fields from items alias i, with the user's watch
// state (w) and, for episodes, the show (sh) joined in by cardFrom.
const cardCols = `i.id, i.library_id, i.kind, i.title, COALESCE(i.year, 0), COALESCE(i.poster, ''), COALESCE(i.backdrop, ''),
	COALESCE(i.logo, ''), COALESCE(i.rating, 0), COALESCE(i.runtime, 0), i.season_number, i.episode_number,
	COALESCE(i.parent_id, 0), COALESCE(sh.id, 0), COALESCE(sh.title, ''), COALESCE(sh.poster, ''), COALESCE(sh.backdrop, ''),
	i.added_at, COALESCE(w.position, 0), COALESCE(w.duration, 0), COALESCE(w.watched, 0),
	CASE WHEN i.kind = 'show' THEN (SELECT COUNT(*) FROM items s JOIN items e ON e.parent_id = s.id WHERE s.parent_id = i.id) ELSE 0 END,
	CASE WHEN i.kind = 'show' THEN (SELECT COUNT(*) FROM items s JOIN items e ON e.parent_id = s.id WHERE s.parent_id = i.id
		AND EXISTS (SELECT 1 FROM watch_state ws WHERE ws.user_id = ?1 AND ws.item_id = e.id AND ws.watched = 1)) ELSE 0 END,
	CASE WHEN i.kind = 'episode' THEN COALESCE(i.overview, '') ELSE '' END,
	EXISTS (SELECT 1 FROM watchlist wl WHERE wl.user_id = ?1 AND wl.item_id = i.id), i.rt_critic, i.rt_audience`

const cardFrom = `FROM items i
	LEFT JOIN watch_state w ON w.item_id = i.id AND w.user_id = ?1
	LEFT JOIN items se ON i.kind = 'episode' AND se.id = i.parent_id
	LEFT JOIN items sh ON sh.id = se.parent_id`

func scanCards(rows *sql.Rows) ([]Card, error) {
	defer rows.Close()
	out := []Card{}
	for rows.Next() {
		var c Card
		var sn, en, rc, ra sql.NullInt64
		if err := rows.Scan(&c.ID, &c.LibraryID, &c.Kind, &c.Title, &c.Year, &c.Poster, &c.Backdrop, &c.Logo, &c.Rating,
			&c.Runtime, &sn, &en, &c.ParentID, &c.ShowID, &c.ShowTitle, &c.ShowPoster, &c.ShowBackdrop, &c.AddedAt,
			&c.Position, &c.Duration, &c.Watched, &c.LeafCount, &c.ViewedLeaves, &c.Overview, &c.Watchlisted, &rc, &ra); err != nil {
			return nil, err
		}
		if rc.Valid {
			n := int(rc.Int64)
			c.RTCritic = &n
		}
		if ra.Valid {
			n := int(ra.Int64)
			c.RTAudience = &n
		}
		if sn.Valid {
			n := int(sn.Int64)
			c.SeasonNumber = &n
		}
		if en.Valid {
			n := int(en.Int64)
			c.EpisodeNumber = &n
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Server) cards(r *http.Request, where string, args ...any) ([]Card, error) {
	all := append([]any{currentUser(r).ID}, args...)
	from := strings.Replace(cardFrom, "FROM items i", "FROM items i"+aclJoin(currentUser(r), "i"), 1)
	rows, err := s.db.R.QueryContext(r.Context(), "SELECT "+cardCols+" "+from+" "+where, all...)
	if err != nil {
		return nil, err
	}
	return scanCards(rows)
}

// aclJoin restricts alias to the libraries a user can see and, for a kids
// profile, to titles rated for its age. Library ids are integers from the
// database, so building them into the SQL is safe.
func aclJoin(u auth.User, alias string) string {
	var conds []string
	if !u.IsAdmin && !u.AllLibraries {
		conds = append(conds, alias+".library_id IN ("+idList(u.Libraries)+")")
	}
	if k := kidsExpr(u, alias); k != "" {
		conds = append(conds, k)
	}
	if len(conds) == 0 {
		return ""
	}
	return " JOIN (SELECT 1) acl ON " + strings.Join(conds, " AND ")
}

func idList(ids []int64) string {
	if len(ids) == 0 {
		return "NULL"
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatInt(id, 10)
	}
	return strings.Join(parts, ",")
}

// ---- libraries ----

type Library struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	Paths         []string `json:"paths"`
	TrustDirMtime bool     `json:"trustDirMtime"`
	LastScanAt    int64    `json:"lastScanAt,omitempty"`
	Count         int      `json:"count"`
	FileCount     int      `json:"fileCount"`
	Unprobed      int      `json:"unprobed"`
	Pending       int      `json:"pendingMetadata"`
	TotalSize     int64    `json:"totalSize"`
	Backdrop      string   `json:"backdrop,omitempty"`
	// The signed-in user's display choices (see user_library_prefs).
	Hidden     bool `json:"hidden"`
	HiddenHome bool `json:"hiddenHome"`
}

func (s *Server) handleLibraries(w http.ResponseWriter, r *http.Request) {
	kids := kidsCond(currentUser(r), "i")
	// Only cheap, indexed lookups here: the TV's tab bar and every page
	// wait on this list. The file totals (a pass over every media file) come
	// from fileStats, which is cached.
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT l.id, l.name, l.kind, l.paths, l.trust_dir_mtime, COALESCE(l.last_scan_at, 0),
		(SELECT COUNT(*) FROM items i WHERE i.library_id = l.id AND i.kind IN ('movie', 'show')`+kids+`),
		(SELECT COUNT(*) FROM items i WHERE i.meta_state = 'pending' AND i.kind IN ('movie', 'show') AND i.library_id = l.id),
		COALESCE((SELECT backdrop FROM items i WHERE i.library_id = l.id AND i.kind IN ('movie', 'show') AND i.backdrop IS NOT NULL`+kids+`
			ORDER BY i.added_at DESC LIMIT 1), ''),
		COALESCE(p.hide_nav, 0), COALESCE(p.hide_home, 0)
		FROM libraries l LEFT JOIN user_library_prefs p ON p.library_id = l.id AND p.user_id = ?`+libACL(currentUser(r))+` ORDER BY l.name`,
		currentUser(r).ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := []Library{}
	for rows.Next() {
		var l Library
		var paths string
		rows.Scan(&l.ID, &l.Name, &l.Kind, &paths, &l.TrustDirMtime, &l.LastScanAt, &l.Count, &l.Pending, &l.Backdrop, &l.Hidden, &l.HiddenHome)
		json.Unmarshal([]byte(paths), &l.Paths)
		out = append(out, l)
	}
	rows.Close()
	stats := s.files.get(r.Context(), s.db)
	for i := range out {
		st := stats[out[i].ID]
		out[i].FileCount, out[i].Unprobed, out[i].TotalSize = st.files, st.unprobed, st.size
	}
	writeJSON(w, out)
}

// fileStats caches per-library media file totals (count, not yet probed,
// total size). Working them out reads every media_files row, which on a
// library of hundreds of thousands of files on a busy disk can take many
// seconds, so it happens at most once a minute and never holds up a request
// once there is a previous answer to give.
type fileStats struct {
	mu      sync.Mutex
	at      time.Time
	m       map[int64]libFiles
	running bool
}

type libFiles struct {
	files, unprobed int
	size            int64
}

const fileStatsTTL = time.Minute

func (f *fileStats) get(ctx context.Context, d *db.DB) map[int64]libFiles {
	f.mu.Lock()
	m, fresh := f.m, time.Since(f.at) < fileStatsTTL
	if m != nil {
		if !fresh && !f.running {
			f.running = true
			go f.refresh(context.Background(), d)
		}
		f.mu.Unlock()
		return m
	}
	f.mu.Unlock()
	// First time: wait for the answer (bounded by the request).
	return f.refresh(ctx, d)
}

func (f *fileStats) refresh(ctx context.Context, d *db.DB) map[int64]libFiles {
	m := map[int64]libFiles{}
	rows, err := d.R.QueryContext(ctx, `SELECT library_id, COUNT(*), SUM(probed_at IS NULL), COALESCE(SUM(size), 0)
		FROM media_files GROUP BY library_id`)
	if err == nil {
		for rows.Next() {
			var id int64
			var l libFiles
			if rows.Scan(&id, &l.files, &l.unprobed, &l.size) == nil {
				m[id] = l
			}
		}
		err = rows.Err()
		rows.Close()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = false
	if err != nil {
		log.Printf("libraries: file totals: %v", err)
		if f.m != nil {
			return f.m
		}
		return m
	}
	f.m, f.at = m, time.Now()
	return m
}

// forget makes the next request recount (after a scan or a library change).
func (f *fileStats) forget() {
	f.mu.Lock()
	f.at = time.Time{}
	f.mu.Unlock()
}

var sortColumns = map[string]string{
	"title":    "i.sort_title",
	"added":    "i.added_at",
	"year":     "i.year",
	"rating":   "i.rating",
	"critics":  "i.rt_critic",
	"audience": "i.rt_audience",
	"release":  "i.release_date",
	"runtime":  "i.runtime",
	"random":   "random", // replaced by a seeded order in handleLibraryItems
}

// itemVisible reports whether the current user may see an item.
func (s *Server) itemVisible(r *http.Request, id int64) bool {
	return s.canView(r.Context(), currentUser(r), id)
}

// canView reports whether u may see an item: its library is shared with
// them and, for a kids profile, it is rated for their age.
func (s *Server) canView(ctx context.Context, u auth.User, id int64) bool {
	var lib int64
	if s.db.R.QueryRowContext(ctx, `SELECT library_id FROM items i WHERE id = ?`+kidsCond(u, "i"), id).Scan(&lib) != nil {
		return false
	}
	return u.CanSee(lib)
}

func libACL(u auth.User) string {
	if u.IsAdmin || u.AllLibraries {
		return ""
	}
	return " WHERE l.id IN (" + idList(u.Libraries) + ")"
}

func (s *Server) handleLibraryItems(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok || !currentUser(r).CanSee(id) {
		writeErr(w, 404, "not found")
		return
	}
	q := r.URL.Query()
	col, ok := sortColumns[q.Get("sort")]
	if !ok {
		col = "i.sort_title"
	}
	dir := "ASC"
	if q.Get("order") == "desc" {
		dir = "DESC"
	}
	limit := queryInt(r, "limit", 100, 500)
	offset := queryInt(r, "offset", 0, 0)
	var libKind string
	s.db.R.QueryRowContext(r.Context(), `SELECT kind FROM libraries WHERE id = ?`, id).Scan(&libKind)
	if libKind == "movies" {
		s.ensureMoods(r.Context())
		if q.Get("sort") == "match" {
			s.matchPage(w, r, id, q, limit, offset)
			return
		}
	}

	where, args := libraryWhere(r, id, q, "")
	if col == "random" {
		// A shuffle that stays put while you scroll: the same seed gives the same order.
		seed := queryInt(r, "seed", 1, 1<<30)
		col = fmt.Sprintf("((i.id * 2654435761 + %d) %% 4294967291)", seed)
	}

	var total int
	countArgs := append([]any{currentUser(r).ID}, args...)
	s.db.R.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM items i LEFT JOIN watch_state w ON w.item_id = i.id AND w.user_id = ?1 `+where,
		countArgs...).Scan(&total)

	order := fmt.Sprintf(" ORDER BY %s IS NULL, %s %s, i.sort_title LIMIT %d OFFSET %d", col, col, dir, limit, offset)
	cards, err := s.cards(r, where+order, args...)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := map[string]any{"items": cards, "total": total, "offset": offset}
	if offset == 0 && col == "i.sort_title" && q.Get("letters") == "1" {
		out["letters"] = s.letterOffsets(r, where, dir, countArgs)
	}
	writeJSON(w, out)
}

// Letter is where titles starting with L begin in an A–Z list, for the TV's jump bar.
type Letter struct {
	L      string `json:"l"`
	Offset int    `json:"offset"`
}

// letterOffsets counts titles by first character in sort order and returns
// the offset of the first title under each letter ("#" for anything else).
func (s *Server) letterOffsets(r *http.Request, where, dir string, args []any) []Letter {
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT SUBSTR(i.sort_title, 1, 1) c, COUNT(*) FROM items i
		LEFT JOIN watch_state w ON w.item_id = i.id AND w.user_id = ?1 `+where+` GROUP BY c ORDER BY c `+dir, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []Letter{}
	seen := map[string]bool{}
	n := 0
	for rows.Next() {
		var c string
		var count int
		if rows.Scan(&c, &count) != nil {
			return nil
		}
		l := "#"
		if len(c) == 1 && c[0] >= 'a' && c[0] <= 'z' {
			l = strings.ToUpper(c)
		}
		if !seen[l] {
			seen[l] = true
			out = append(out, Letter{l, n})
		}
		n += count
	}
	return out
}

func (s *Server) handleLibraryGenres(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r, "id")
	if !currentUser(r).CanSee(id) {
		writeErr(w, 404, "not found")
		return
	}
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT j.value, COUNT(*) FROM items i, json_each(i.genres) j
		WHERE i.library_id = ? AND i.kind IN ('movie', 'show') AND i.genres IS NOT NULL`+kidsCond(currentUser(r), "i")+` GROUP BY j.value ORDER BY j.value`, id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	type genre struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	out := []genre{}
	for rows.Next() {
		var g genre
		rows.Scan(&g.Name, &g.Count)
		out = append(out, g)
	}
	writeJSON(w, out)
}

// ---- home ----

type Row struct {
	Title string `json:"title"`
	Link  string `json:"link,omitempty"`
	Kind  string `json:"kind"` // "poster" or "landscape"
	Items []Card `json:"items"`
	// Smart marks a "Tonight" row worked out for the user (season, because,
	// short, gems); the UIs label these rows.
	Smart string `json:"smart,omitempty"`
}

// withoutUpNextShows drops shows already in Up Next from Finish a Season
// (they're right above it), and the row itself if nothing is left. The smart
// rows are cached; this runs on every Home build, so it follows Up Next.
func withoutUpNextShows(rows []Row, upNext []Card) []Row {
	inUpNext := map[int64]bool{}
	for _, c := range upNext {
		if c.ShowID != 0 {
			inUpNext[c.ShowID] = true
		}
		if c.Kind == "show" {
			inUpNext[c.ID] = true
		}
	}
	out := make([]Row, 0, len(rows))
	for _, row := range rows {
		if row.Smart == "season" {
			kept := make([]Card, 0, len(row.Items))
			for _, c := range row.Items {
				if !inUpNext[c.ShowID] {
					kept = append(kept, c)
				}
			}
			if len(kept) == 0 {
				continue
			}
			row.Items = kept
		}
		out = append(out, row)
	}
	return out
}

func (s *Server) buildHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var rows []Row
	t0 := time.Now()

	cont, err := s.upNext(r)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	tCont := time.Since(t0)
	if len(cont) > 0 {
		rows = append(rows, Row{Title: "Up Next", Kind: "landscape", Items: cont})
	}
	wl, err := s.cards(r, `JOIN watchlist wl2 ON wl2.item_id = i.id AND wl2.user_id = ?1 WHERE COALESCE(w.watched, 0) = 0
		ORDER BY wl2.added_at DESC LIMIT 40`)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if len(wl) > 0 {
		rows = append(rows, Row{Title: "Watchlist", Kind: "poster", Items: wl})
	}

	// Libraries the user hid from Home get no rows there (Continue Watching
	// still includes them).
	hiddenHome := s.hiddenHomeLibraries(r)

	// "Tonight" smart rows (see smart.go): some go near the top, the rest
	// after the libraries' rows.
	tSmart0 := time.Now()
	early, later := s.smartRows(r, hiddenHome)
	rows = append(rows, withoutUpNextShows(early, cont)...)
	tSmart := time.Since(tSmart0)
	libs, err := s.db.R.QueryContext(ctx, `SELECT l.id, l.name, l.kind FROM libraries l`+libACL(currentUser(r))+` ORDER BY l.name`)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	type lib struct {
		id         int64
		name, kind string
	}
	var ls []lib
	for libs.Next() {
		var l lib
		libs.Scan(&l.id, &l.name, &l.kind)
		if !hiddenHome[l.id] {
			ls = append(ls, l)
		}
	}
	libs.Close()

	for _, l := range ls {
		kind := "movie"
		if l.kind == "shows" {
			kind = "show"
		}
		items, err := s.cards(r, `WHERE i.library_id = ?2 AND i.kind = ?3`+notRemoved("i.id")+` ORDER BY i.added_at DESC LIMIT 24`, l.id, kind)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if len(items) > 0 {
			rows = append(rows, Row{Title: "Recently Added in " + l.name, Link: fmt.Sprintf("/library/%d?sort=added&order=desc", l.id), Kind: "poster", Items: items})
		}
	}

	tLibs := time.Since(t0) - tCont - tSmart
	rows = append(rows, later...)

	// The hero and Critically Acclaimed are suggestions, so like the smart
	// rows they leave out anything the viewer has seen or is watching.
	started, err := s.startedShows(ctx, currentUser(r).ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	unseen := " AND " + unseenAt("m", started)

	// Hero: recent, well-rated titles that have artwork worth showing.
	hero, err := s.cards(r, `WHERE i.kind IN ('movie', 'show') AND i.backdrop IS NOT NULL AND i.overview IS NOT NULL
		AND i.id IN (SELECT id FROM items m WHERE kind IN ('movie', 'show') AND backdrop IS NOT NULL`+notInLibs(hiddenHome)+kidsTop(currentUser(r))+unseen+` ORDER BY added_at DESC LIMIT 60)
		ORDER BY RANDOM() LIMIT 6`)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for i := range hero {
		s.db.R.QueryRowContext(ctx, `SELECT COALESCE(overview, '') FROM items WHERE id = ?`, hero[i].ID).Scan(&hero[i].Overview)
	}

	// Top rated row for variety.
	top, _ := s.cards(r, `WHERE i.id IN (SELECT id FROM items m WHERE kind IN ('movie', 'show') AND rating >= 7.5 AND poster IS NOT NULL`+notInLibs(hiddenHome)+kidsTop(currentUser(r))+unseen+`
		ORDER BY RANDOM() LIMIT 24)`)
	if len(top) >= 6 {
		rows = append(rows, Row{Title: "Critically Acclaimed", Kind: "poster", Items: top})
	}
	if rows == nil {
		rows = []Row{}
	}
	if took := time.Since(t0); took > 500*time.Millisecond {
		log.Printf("home: %s (continue %s, smart rows %s, libraries %s)", took.Round(time.Millisecond), tCont.Round(time.Millisecond),
			tSmart.Round(time.Millisecond), tLibs.Round(time.Millisecond))
	}
	resp := map[string]any{"hero": hero, "rows": rows}
	// How many titles the viewer took off their suggestions, so Home can
	// offer to bring them back.
	if n := s.removedSuggestions(ctx, currentUser(r).ID); n > 0 {
		resp["removed"] = n
	}
	if t := s.wrappedTeaser(r); t != nil {
		resp["wrapped"] = t
	}
	writeJSON(w, resp)
}

// Up Next windows (see upNext).
const (
	upNextPartialDays = 7   // half-watched films and episodes
	upNextShowDays    = 60  // shows with an episode finished recently
	upNextReturnDays  = 365 // shows that may have come back with new episodes
	upNextNewDays     = 30  // ...if the new episode arrived this recently
	upNextMax         = 30

	// A film or episode joins Up Next only once this much of it has been
	// watched, so something played for a few seconds (a mis-click, a peek)
	// doesn't sit at the front of Home. Short items count once half
	// watched. Admins can change it in Settings (up_next_min_minutes).
	upNextMinMinutesDefault = 10
	upNextMinMinutesKey     = "up_next_min_minutes"
)

// upNextMinSeconds is how far into a film or episode someone must get
// before it shows in Up Next.
func (s *Server) upNextMinSeconds(ctx context.Context) int64 {
	return int64(s.upNextMinMinutes(ctx)) * 60
}

func (s *Server) upNextMinMinutes(ctx context.Context) int {
	if n, err := strconv.Atoi(s.db.Setting(ctx, upNextMinMinutesKey)); err == nil && n >= 0 && n <= 120 {
		return n
	}
	return upNextMinMinutesDefault
}

// upNext is the first row on Home, newest activity first:
//   - films and episodes stopped part way (at least 10 minutes in, by
//     default, or half way for short ones), played in the last 7 days;
//   - the next episode of each show with an episode finished in the last
//     60 days;
//   - shows that have come back: everything up to the latest season was
//     watched (within the last year) and a new episode arrived in the last
//     30 days. Those sort by when the new episode arrived.
//
// Items and shows removed with "Remove from Up Next" stay off until they
// are watched again.
func (s *Server) upNext(r *http.Request) ([]Card, error) {
	ctx := r.Context()
	uid := currentUser(r).ID
	now := db.Now()
	type entry struct {
		t    int64
		card Card
	}
	var out []entry

	// 1. Stopped part way, in the last week.
	type idT struct{ id, t int64 }
	scanIDT := func(q string, args ...any) ([]idT, error) {
		rows, err := s.db.R.QueryContext(ctx, q, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var list []idT
		for rows.Next() {
			var x idT
			if err := rows.Scan(&x.id, &x.t); err != nil {
				return nil, err
			}
			list = append(list, x)
		}
		return list, rows.Err()
	}
	partial, err := scanIDT(`SELECT w.item_id, w.updated_at FROM watch_state w JOIN items i ON i.id = w.item_id
		WHERE w.user_id = ?1 AND w.updated_at > ?2 AND w.watched = 0 AND i.kind IN ('movie', 'episode')
		AND w.position > 30 AND (w.position >= ?4 OR (w.duration > 0 AND w.position * 2 >= w.duration))
		AND NOT EXISTS (SELECT 1 FROM continue_hidden ch WHERE ch.user_id = ?1 AND ch.item_id = w.item_id AND ch.hidden_at >= w.updated_at)
		ORDER BY w.updated_at DESC LIMIT ?3`, uid, now-upNextPartialDays*86400, upNextMax, s.upNextMinSeconds(ctx))
	if err != nil {
		return nil, err
	}
	ids := make([]int64, len(partial))
	when := map[int64]int64{}
	for i, p := range partial {
		ids[i], when[p.id] = p.id, p.t
	}
	cards, err := s.cardsByIDs(r, ids)
	if err != nil {
		return nil, err
	}
	haveShow := map[int64]bool{}
	for _, c := range cards {
		out = append(out, entry{when[c.ID], c})
		if c.ShowID != 0 {
			haveShow[c.ShowID] = true
		}
	}

	// 2 and 3. Shows: the latest finished episode per show in the last year.
	// Recent ones always count; older ones only when a newer episode has
	// arrived since (checked below against the next episode itself).
	shows, err := scanIDT(`SELECT id, t FROM (SELECT sh.id, MAX(w.updated_at) AS t FROM watch_state w
		JOIN items e ON e.id = w.item_id AND e.kind = 'episode'
		JOIN items se ON se.id = e.parent_id JOIN items sh ON sh.id = se.parent_id
		WHERE w.user_id = ?1 AND w.watched = 1 AND w.updated_at > ?2 GROUP BY sh.id) x
		WHERE NOT EXISTS (SELECT 1 FROM continue_hidden ch WHERE ch.user_id = ?1 AND ch.item_id = x.id AND ch.hidden_at >= x.t)
		AND (x.t > ?3 OR EXISTS (SELECT 1 FROM items s2 JOIN items e2 ON e2.parent_id = s2.id
			WHERE s2.parent_id = x.id AND s2.kind = 'season' AND e2.added_at > x.t AND e2.added_at > ?4))
		ORDER BY t DESC LIMIT 200`,
		uid, now-upNextReturnDays*86400, now-upNextShowDays*86400, now-upNextNewDays*86400)
	if err != nil {
		return nil, err
	}
	for _, st := range shows {
		if haveShow[st.id] {
			continue
		}
		next, err := s.nextEpisode(r, st.id)
		if err != nil {
			return nil, err
		}
		if next == nil {
			continue
		}
		t := st.t
		if returned := next.AddedAt > st.t && next.AddedAt > now-upNextNewDays*86400; returned {
			t = next.AddedAt
		} else if st.t <= now-upNextShowDays*86400 {
			continue // finished a while ago and nothing new since
		}
		out = append(out, entry{t, *next})
	}

	sort.SliceStable(out, func(a, b int) bool { return out[a].t > out[b].t })
	if len(out) > upNextMax {
		out = out[:upNextMax]
	}
	res := make([]Card, len(out))
	for i, e := range out {
		res[i] = e.card
	}
	return res, nil
}

// nextEpisode returns the first unwatched episode after the last watched one.
func (s *Server) nextEpisode(r *http.Request, showID int64) (*Card, error) {
	// Drive from the show's seasons (items_parent index) rather than every
	// episode in the database: with a large library the latter took
	// seconds per show.
	cards, err := s.cards(r, `WHERE +i.kind = 'episode' AND i.parent_id IN (SELECT id FROM items WHERE parent_id = ?2 AND kind = 'season')
		AND COALESCE(w.watched, 0) = 0
		AND (i.season_number * 10000 + i.episode_number) > COALESCE((
			SELECT MAX(e2.season_number * 10000 + e2.episode_number) FROM items s2 JOIN items e2 ON e2.parent_id = s2.id
			WHERE s2.parent_id = ?2 AND e2.season_number > 0
			AND EXISTS (SELECT 1 FROM watch_state w2 WHERE w2.user_id = ?1 AND w2.item_id = e2.id AND w2.watched = 1)), -1)
		AND i.season_number > 0
		ORDER BY i.season_number, i.episode_number LIMIT 1`, showID)
	if err != nil || len(cards) == 0 {
		return nil, err
	}
	return &cards[0], nil
}

// ---- item detail ----

type MediaFile struct {
	ID         int64           `json:"id"`
	Path       string          `json:"path"`
	Name       string          `json:"name"`
	Size       int64           `json:"size"`
	Container  string          `json:"container,omitempty"`
	VideoCodec string          `json:"videoCodec,omitempty"`
	AudioCodec string          `json:"audioCodec,omitempty"`
	Width      int             `json:"width,omitempty"`
	Height     int             `json:"height,omitempty"`
	Duration   float64         `json:"duration,omitempty"`
	Bitrate    int64           `json:"bitrate,omitempty"`
	Streams    json.RawMessage `json:"streams,omitempty"`
	Probed     bool            `json:"probed"`
	ProbeError string          `json:"probeError,omitempty"`
}

type Detail struct {
	Card
	OriginalTitle string      `json:"originalTitle,omitempty"`
	Overview      string      `json:"overview,omitempty"`
	Tagline       string      `json:"tagline,omitempty"`
	Genres        []string    `json:"genres"`
	ReleaseDate   string      `json:"releaseDate,omitempty"`
	TmdbID        int64       `json:"tmdbId,omitempty"`
	ImdbID        string      `json:"imdbId,omitempty"`
	MetaState     string      `json:"metaState"`
	Files         []MediaFile `json:"files"`
	Children      []Card      `json:"children"`
	Parent        *Card       `json:"parent,omitempty"`
	Show          *Card       `json:"show,omitempty"`
	OnDeck        *Card       `json:"onDeck,omitempty"`
	Siblings      []Card      `json:"siblings,omitempty"`
	// The film's collection ("The Matrix Collection") and its other films.
	Collection      *CollectionCard `json:"collection,omitempty"`
	CollectionItems []Card          `json:"collectionItems,omitempty"`
}

func (s *Server) handleItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	cs, err := s.cards(r, `WHERE i.id = ?2`, id)
	if err != nil || len(cs) == 0 {
		writeErr(w, 404, "not found")
		return
	}
	d := Detail{Card: cs[0], Genres: []string{}, Files: []MediaFile{}, Children: []Card{}}
	var genres string
	s.db.R.QueryRowContext(r.Context(), `SELECT COALESCE(original_title, ''), COALESCE(overview, ''), COALESCE(tagline, ''),
		COALESCE(genres, '[]'), COALESCE(release_date, ''), COALESCE(tmdb_id, 0), COALESCE(imdb_id, ''), meta_state
		FROM items WHERE id = ?`, id).Scan(&d.OriginalTitle, &d.Overview, &d.Tagline, &genres, &d.ReleaseDate, &d.TmdbID, &d.ImdbID, &d.MetaState)
	json.Unmarshal([]byte(genres), &d.Genres)
	d.Card.Overview = ""

	rows, err := s.db.R.QueryContext(r.Context(), `SELECT id, path, size, COALESCE(container, ''), COALESCE(video_codec, ''),
		COALESCE(audio_codec, ''), COALESCE(width, 0), COALESCE(height, 0), COALESCE(duration, 0), COALESCE(bitrate, 0),
		COALESCE(streams, 'null'), probed_at IS NOT NULL, COALESCE(probe_error, '') FROM media_files WHERE item_id = ? ORDER BY size DESC`, id)
	if err == nil {
		for rows.Next() {
			var f MediaFile
			var streams string
			rows.Scan(&f.ID, &f.Path, &f.Size, &f.Container, &f.VideoCodec, &f.AudioCodec, &f.Width, &f.Height, &f.Duration,
				&f.Bitrate, &streams, &f.Probed, &f.ProbeError)
			if streams != "null" {
				f.Streams = json.RawMessage(streams)
			}
			f.Name = filepath.Base(f.Path)
			if !currentUser(r).IsAdmin {
				f.Path = ""
			}
			d.Files = append(d.Files, f)
		}
		rows.Close()
	}

	switch d.Kind {
	case "movie":
		if c := s.itemCollection(r, id); c != nil {
			d.Collection = c
			films, _ := s.cards(r, `WHERE i.collection_id = ?2 AND i.collection_id > 0 AND i.kind = 'movie'
				ORDER BY i.release_date IS NULL, i.release_date, i.year, i.sort_title`, c.ID)
			d.CollectionItems = s.onePerTitle(r, films, id)
		}
	case "show":
		d.Children, _ = s.cards(r, `WHERE i.parent_id = ?2 ORDER BY i.season_number = 0, i.season_number`, id)
		for i := range d.Children {
			// Episode counts per season.
			s.db.R.QueryRowContext(r.Context(), `SELECT COUNT(*), COALESCE(SUM(COALESCE(w.watched, 0)), 0) FROM items e
				LEFT JOIN watch_state w ON w.item_id = e.id AND w.user_id = ? WHERE e.parent_id = ?`,
				currentUser(r).ID, d.Children[i].ID).Scan(&d.Children[i].LeafCount, &d.Children[i].ViewedLeaves)
		}
		d.OnDeck, _ = s.nextEpisode(r, id)
		if d.OnDeck == nil {
			// Nothing watched yet: start at the first regular episode.
			first, _ := s.cards(r, `WHERE i.kind = 'episode' AND sh.id = ?2 AND i.season_number > 0
				ORDER BY i.season_number, i.episode_number LIMIT 1`, id)
			if len(first) > 0 && d.ViewedLeaves == 0 {
				d.OnDeck = &first[0]
			}
		}
	case "season":
		d.Children, _ = s.cards(r, `WHERE i.parent_id = ?2 ORDER BY i.episode_number`, id)
		if p, _ := s.cards(r, `WHERE i.id = ?2`, d.ParentID); len(p) > 0 {
			d.Show = &p[0]
		}
		d.Siblings, _ = s.cards(r, `WHERE i.parent_id = ?2 ORDER BY i.season_number = 0, i.season_number`, d.ParentID)
	case "episode":
		if p, _ := s.cards(r, `WHERE i.id = ?2`, d.ParentID); len(p) > 0 {
			d.Parent = &p[0]
		}
		if d.ShowID != 0 {
			if p, _ := s.cards(r, `WHERE i.id = ?2`, d.ShowID); len(p) > 0 {
				d.Show = &p[0]
			}
		}
		d.Siblings, _ = s.cards(r, `WHERE i.parent_id = ?2 ORDER BY i.episode_number`, d.ParentID)
	}
	writeJSON(w, d)
}

// ---- search ----

func ftsQuery(q string) string {
	var terms []string
	for _, f := range strings.FieldsFunc(q, func(r rune) bool {
		return !(r == '\'' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 127)
	}) {
		f = strings.ReplaceAll(f, `"`, "")
		if f != "" {
			terms = append(terms, `"`+f+`"*`)
		}
	}
	return strings.Join(terms, " ")
}

// How long a search waits for episode matches once its films and shows are
// ready. Episodes are nine tenths of the library and their index is the
// one that can be slow on a busy disk; titles never wait on it for long.
const episodeSearchWait = 1500 * time.Millisecond

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("q")
	q := ftsQuery(raw)
	res := map[string]any{"movies": []Card{}, "shows": []Card{}, "episodes": []Card{}, "collections": []CollectionCard{}}
	if q == "" {
		writeJSON(w, res)
		return
	}
	start := time.Now()
	// The search pages ask for the two parts separately (part=titles and
	// part=episodes), so films and shows are on screen without waiting for
	// episodes. Asked for both at once, episodes get a moment and no more.
	part := r.URL.Query().Get("part")
	epCtx, stopEpisodes := context.WithCancel(r.Context())
	defer stopEpisodes()
	epCh := make(chan []Card, 1)
	if part != "titles" {
		go func() { epCh <- s.searchEpisodes(r.WithContext(epCtx), q, 20) }()
	}
	if part != "episodes" {
		res["movies"], res["shows"] = s.searchTitles(r, raw, q, 40)
		res["collections"] = s.searchCollections(r, raw)
	}
	titled := time.Since(start)
	switch part {
	case "titles":
	case "episodes":
		res["episodes"] = <-epCh
	default:
		wait := time.NewTimer(episodeSearchWait)
		defer wait.Stop()
		select {
		case res["episodes"] = <-epCh:
		case <-wait.C:
			stopEpisodes()
			log.Printf("search: left episodes out for %q, still looking after %s", raw, time.Since(start).Round(time.Millisecond))
		}
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		log.Printf("search: %s (%s for films and shows) for %q, part %q", d.Round(time.Millisecond), titled.Round(time.Millisecond), raw, part)
	}
	writeJSON(w, res)
}

// searchTitles finds films and shows, best match first: a title that is
// exactly what was typed, then titles starting with it, then the rest by
// relevance (title matches outrank plot-summary matches).
func (s *Server) searchTitles(r *http.Request, raw, match string, limit int) (movies, shows []Card) {
	movies, shows = []Card{}, []Card{}
	fts := s.db.TitleFTS()
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT i.id, i.kind, i.title, COALESCE(i.original_title, '')
		FROM (SELECT rowid, bm25(`+fts+`, 10.0, 5.0, 1.0) AS score FROM `+fts+` WHERE `+fts+` MATCH ?1 ORDER BY score LIMIT 400) f
		JOIN items i ON i.id = f.rowid`+aclJoin(currentUser(r), "i")+`
		WHERE i.kind IN ('movie', 'show') ORDER BY f.score`, match)
	if err != nil {
		log.Printf("search: %q failed: %v", raw, err)
		return
	}
	type hit struct {
		id   int64
		kind string
		tier int
	}
	want := normTitle(raw)
	var hits []hit
	for rows.Next() {
		var h hit
		var t, ot string
		if rows.Scan(&h.id, &h.kind, &t, &ot) != nil {
			continue
		}
		nt, no := normTitle(t), normTitle(ot)
		switch {
		case want == "":
			h.tier = 2
		case nt == want || no == want:
			h.tier = 0
		case strings.HasPrefix(nt, want) || strings.HasPrefix(no, want):
			h.tier = 1
		default:
			h.tier = 2
		}
		hits = append(hits, h)
	}
	rows.Close()
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].tier < hits[b].tier })
	var ids []int64
	n := map[string]int{}
	for _, h := range hits {
		if n[h.kind] < limit {
			n[h.kind]++
			ids = append(ids, h.id)
		}
	}
	byID, err := s.cardsByID(r, ids)
	if err != nil {
		log.Printf("search: %q failed: %v", raw, err)
		return
	}
	for _, id := range ids {
		if c, ok := byID[id]; ok {
			if c.Kind == "movie" {
				movies = append(movies, c)
			} else {
				shows = append(shows, c)
			}
		}
	}
	return
}

// searchEpisodes finds episodes by their own title. (Plot summaries are
// left out: with hundreds of thousands of episodes nearly any word matches
// thousands of them.)
func (s *Server) searchEpisodes(r *http.Request, match string, limit int) []Card {
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT i.id
		FROM (SELECT rowid, bm25(items_fts, 10.0, 5.0, 1.0) AS score FROM items_fts WHERE items_fts MATCH ?1 ORDER BY score LIMIT 200) f
		JOIN items i ON i.id = f.rowid`+aclJoin(currentUser(r), "i")+`
		WHERE i.kind = 'episode' ORDER BY f.score LIMIT ?2`, `{title original_title} : (`+match+`)`, limit)
	if err != nil {
		return []Card{}
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	byID, err := s.cardsByID(r, ids)
	if err != nil {
		return []Card{}
	}
	out := make([]Card, 0, len(ids))
	for _, id := range ids {
		if c, ok := byID[id]; ok {
			out = append(out, c)
		}
	}
	return out
}

// ---- watch state ----

func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	// Saved even if the player has stopped waiting for the answer (it gives
	// up after a few seconds, and the last report of a film is sent as its
	// screen closes): a write that had to queue behind a scan or a sync used
	// to be dropped with the request, and the film stayed unwatched.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), time.Minute)
	defer cancel()
	r = r.WithContext(ctx)
	id, ok := pathID(r, "id")
	if !ok || !s.itemVisible(r, id) {
		writeErr(w, 400, "bad id")
		return
	}
	var body struct {
		Position float64 `json:"position"`
		Duration float64 `json:"duration"`
		// State is what the player is doing: playing, paused or stopped
		// (older players leave it out). It only feeds Now watching.
		State string `json:"state"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	uid := currentUser(r).ID
	// A player that couldn't work out the film's length still saves its
	// place: use the length on file, so the end still counts as watched.
	if body.Duration <= 0 {
		s.db.R.QueryRowContext(r.Context(), `SELECT COALESCE((SELECT MAX(duration) FROM media_files WHERE item_id = ?1),
			(SELECT runtime * 60 FROM items WHERE id = ?1), 0)`, id).Scan(&body.Duration)
	}
	s.nowPlaying.progress(r, uid, id, body.Position, body.Duration, body.State)
	began := time.Now()
	watched := s.finishedAt(ctx, id, body.Position, body.Duration)
	pos := body.Position
	if watched {
		pos = 0
	}
	now := db.Now()
	_, err := s.db.W.ExecContext(ctx, `INSERT INTO watch_state(user_id, item_id, position, duration, watched, play_count, updated_at, played_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, item_id) DO UPDATE SET position = excluded.position, duration = excluded.duration,
			play_count = watch_state.play_count + (excluded.watched AND NOT watch_state.watched),
			watched = MAX(watch_state.watched, excluded.watched),
			updated_at = excluded.updated_at, played_at = excluded.played_at`,
		uid, id, pos, body.Duration, watched, boolInt(watched), now, now)
	if err == nil && watched {
		err = db.RecordPlay(ctx, s.db.W, uid, id, db.Now(), body.Duration, "plexodus")
		s.forgetHome(uid)
	}
	if took := time.Since(began); err != nil || took > 3*time.Second {
		log.Printf("progress: item %d at %.0f of %.0f s for user %d: took %s, error: %v", id, body.Position, body.Duration, uid, took.Round(time.Millisecond), err)
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.homeChanged(uid) // resume points and Continue Watching
	// Near the end of an episode, get the next one's skip markers ready.
	if s.detector != nil && body.Duration > 0 && body.Position/body.Duration > 0.7 {
		s.detector.OnNearEnd(s.baseCtx, id)
	}
	writeJSON(w, map[string]any{"watched": watched})
}

// handlePlayerClosed logs how a sitting in the TV app's player went: where
// it stopped and what became of its progress reports. One line per sitting
// in the journal (`journalctl -u plexodus | grep "player closed"`), so a
// film that didn't get saved can be traced.
func (s *Server) handlePlayerClosed(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ItemID     int64   `json:"itemId"`
		LastItemID int64   `json:"lastItemId"`
		Position   float64 `json:"position"`
		Duration   float64 `json:"duration"`
		Ended      bool    `json:"ended"`
		Stats      *struct {
			Engine   string `json:"engine"`
			OK       int    `json:"ok"`
			Failed   int    `json:"failed"`
			Skipped  int    `json:"skipped"`
			Note     string `json:"note"`
			Started  bool   `json:"started"`
			Stalled  bool   `json:"stalled"`
			LengthMs int64  `json:"lengthMs"`
			TimeMs   int64  `json:"timeMs"`
		} `json:"stats"`
		Saved *struct {
			Position float64 `json:"position"`
			Watched  bool    `json:"watched"`
		} `json:"saved"`
		Fixed string `json:"fixed"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	id := body.LastItemID
	if id == 0 {
		id = body.ItemID
	}
	line := fmt.Sprintf("player closed: item %d at %.0f of %.0f s, ended=%v, user %d", id, body.Position, body.Duration, body.Ended, currentUser(r).ID)
	if st := body.Stats; st != nil {
		note := st.Note
		if len(note) > 200 {
			note = note[:200]
		}
		line += fmt.Sprintf("; %s reports ok=%d failed=%d skipped=%d started=%v stalled=%v length=%dms time=%dms %q",
			st.Engine, st.OK, st.Failed, st.Skipped, st.Started, st.Stalled, st.LengthMs, st.TimeMs, note)
	} else {
		line += "; no report counts (older app)"
	}
	if sv := body.Saved; sv != nil {
		line += fmt.Sprintf("; server had position=%.0f watched=%v", sv.Position, sv.Watched)
	}
	if body.Fixed != "" {
		f := body.Fixed
		if len(f) > 80 {
			f = f[:80]
		}
		line += fmt.Sprintf("; page %q", f)
	}
	log.Print(line)
	writeJSON(w, map[string]bool{"ok": true})
}

// finishedAt: past 90% (Plex's default), or into the closing credits, so
// pressing Back once the credits roll counts as watched and the title
// leaves Up Next. The TV and web UIs apply the same rule (homeHidden.ts).
func (s *Server) finishedAt(ctx context.Context, id int64, pos, dur float64) bool {
	if dur <= 0 {
		return false
	}
	if pos/dur >= 0.9 {
		return true
	}
	if pos < dur*0.7 {
		return false
	}
	var start sql.NullFloat64
	s.db.R.QueryRowContext(ctx, `SELECT MIN(start) FROM markers WHERE item_id = ? AND type = 'credits' AND start >= ?`, id, dur*0.7).Scan(&start)
	return start.Valid && pos >= start.Float64
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Server) handleWatched(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok || !s.itemVisible(r, id) {
		writeErr(w, 400, "bad id")
		return
	}
	var body struct {
		Watched bool `json:"watched"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	uid := currentUser(r).ID
	// Applies to the item itself and every playable descendant.
	_, err := s.db.W.ExecContext(r.Context(), `WITH RECURSIVE tree(id) AS (
			SELECT ? UNION ALL SELECT i.id FROM items i JOIN tree t ON i.parent_id = t.id)
		INSERT INTO watch_state(user_id, item_id, position, duration, watched, play_count, updated_at)
		SELECT ?, i.id, 0, 0, ?, ?, ? FROM items i JOIN tree t ON t.id = i.id WHERE i.kind IN ('movie', 'episode')
		ON CONFLICT(user_id, item_id) DO UPDATE SET watched = excluded.watched, position = 0,
			play_count = watch_state.play_count + excluded.play_count, updated_at = excluded.updated_at`,
		id, uid, body.Watched, boolInt(body.Watched), db.Now())
	s.forgetHome(uid)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
