package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/db"
)

// Card is the compact shape used in grids and rows.
type Card struct {
	ID            int64   `json:"id"`
	LibraryID     int64   `json:"libraryId"`
	Kind          string  `json:"kind"`
	Title         string  `json:"title"`
	Year          int     `json:"year,omitempty"`
	Poster        string  `json:"poster,omitempty"`
	Backdrop      string  `json:"backdrop,omitempty"`
	Logo          string  `json:"logo,omitempty"`
	Rating        float64 `json:"rating,omitempty"`
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
}

// cardCols selects card fields from items alias i, with the user's watch
// state (w) and, for episodes, the show (sh) joined in by cardFrom.
const cardCols = `i.id, i.library_id, i.kind, i.title, COALESCE(i.year, 0), COALESCE(i.poster, ''), COALESCE(i.backdrop, ''),
	COALESCE(i.logo, ''), COALESCE(i.rating, 0), COALESCE(i.runtime, 0), i.season_number, i.episode_number,
	COALESCE(i.parent_id, 0), COALESCE(sh.id, 0), COALESCE(sh.title, ''), COALESCE(sh.poster, ''), COALESCE(sh.backdrop, ''),
	i.added_at, COALESCE(w.position, 0), COALESCE(w.duration, 0), COALESCE(w.watched, 0),
	CASE WHEN i.kind = 'show' THEN (SELECT COUNT(*) FROM items s JOIN items e ON e.parent_id = s.id WHERE s.parent_id = i.id) ELSE 0 END,
	CASE WHEN i.kind = 'show' THEN (SELECT COUNT(*) FROM items s JOIN items e ON e.parent_id = s.id
		JOIN watch_state ws ON ws.item_id = e.id AND ws.user_id = ?1 AND ws.watched = 1 WHERE s.parent_id = i.id) ELSE 0 END,
	CASE WHEN i.kind = 'episode' THEN COALESCE(i.overview, '') ELSE '' END`

const cardFrom = `FROM items i
	LEFT JOIN watch_state w ON w.item_id = i.id AND w.user_id = ?1
	LEFT JOIN items se ON i.kind = 'episode' AND se.id = i.parent_id
	LEFT JOIN items sh ON sh.id = se.parent_id`

func scanCards(rows *sql.Rows) ([]Card, error) {
	defer rows.Close()
	out := []Card{}
	for rows.Next() {
		var c Card
		var sn, en sql.NullInt64
		if err := rows.Scan(&c.ID, &c.LibraryID, &c.Kind, &c.Title, &c.Year, &c.Poster, &c.Backdrop, &c.Logo, &c.Rating,
			&c.Runtime, &sn, &en, &c.ParentID, &c.ShowID, &c.ShowTitle, &c.ShowPoster, &c.ShowBackdrop, &c.AddedAt,
			&c.Position, &c.Duration, &c.Watched, &c.LeafCount, &c.ViewedLeaves, &c.Overview); err != nil {
			return nil, err
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

// aclJoin restricts alias to the libraries a user can see. Library ids are
// integers from the database, so building them into the SQL is safe.
func aclJoin(u auth.User, alias string) string {
	if u.IsAdmin || u.AllLibraries {
		return ""
	}
	return " JOIN (SELECT 1) acl ON " + alias + ".library_id IN (" + idList(u.Libraries) + ")"
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
}

func (s *Server) handleLibraries(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT l.id, l.name, l.kind, l.paths, l.trust_dir_mtime, COALESCE(l.last_scan_at, 0),
		(SELECT COUNT(*) FROM items i WHERE i.library_id = l.id AND i.kind IN ('movie', 'show')),
		(SELECT COUNT(*) FROM media_files m WHERE m.library_id = l.id),
		(SELECT COUNT(*) FROM media_files m WHERE m.library_id = l.id AND m.probed_at IS NULL),
		(SELECT COUNT(*) FROM items i WHERE i.library_id = l.id AND i.kind IN ('movie', 'show') AND i.meta_state = 'pending'),
		(SELECT COALESCE(SUM(size), 0) FROM media_files m WHERE m.library_id = l.id),
		COALESCE((SELECT backdrop FROM items i WHERE i.library_id = l.id AND i.kind IN ('movie', 'show') AND i.backdrop IS NOT NULL
			ORDER BY i.added_at DESC LIMIT 1), '')
		FROM libraries l`+libACL(currentUser(r))+` ORDER BY l.name`)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []Library{}
	for rows.Next() {
		var l Library
		var paths string
		rows.Scan(&l.ID, &l.Name, &l.Kind, &paths, &l.TrustDirMtime, &l.LastScanAt, &l.Count, &l.FileCount, &l.Unprobed, &l.Pending, &l.TotalSize, &l.Backdrop)
		json.Unmarshal([]byte(paths), &l.Paths)
		out = append(out, l)
	}
	writeJSON(w, out)
}

var sortColumns = map[string]string{
	"title":   "i.sort_title",
	"added":   "i.added_at",
	"year":    "i.year",
	"rating":  "i.rating",
	"release": "i.release_date",
}

// itemVisible reports whether the current user may see an item.
func (s *Server) itemVisible(r *http.Request, id int64) bool {
	var lib int64
	if s.db.R.QueryRowContext(r.Context(), `SELECT library_id FROM items WHERE id = ?`, id).Scan(&lib) != nil {
		return false
	}
	return currentUser(r).CanSee(lib)
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

	where := `WHERE i.library_id = ?2 AND i.kind IN ('movie', 'show')`
	args := []any{id}
	if g := q.Get("genre"); g != "" {
		where += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM json_each(i.genres) WHERE value = ?%d)`, len(args)+2)
		args = append(args, g)
	}
	switch q.Get("filter") {
	case "unwatched":
		where += ` AND COALESCE(w.watched, 0) = 0`
	case "inprogress":
		where += ` AND w.position > 0 AND COALESCE(w.watched, 0) = 0`
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
	writeJSON(w, map[string]any{"items": cards, "total": total, "offset": offset})
}

func (s *Server) handleLibraryGenres(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r, "id")
	if !currentUser(r).CanSee(id) {
		writeErr(w, 404, "not found")
		return
	}
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT j.value, COUNT(*) FROM items i, json_each(i.genres) j
		WHERE i.library_id = ? AND i.kind IN ('movie', 'show') AND i.genres IS NOT NULL GROUP BY j.value ORDER BY j.value`, id)
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
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var rows []Row

	cont, err := s.continueWatching(r)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if len(cont) > 0 {
		rows = append(rows, Row{Title: "Continue Watching", Kind: "landscape", Items: cont})
	}

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
		ls = append(ls, l)
	}
	libs.Close()

	for _, l := range ls {
		kind := "movie"
		if l.kind == "shows" {
			kind = "show"
		}
		items, err := s.cards(r, `WHERE i.library_id = ?2 AND i.kind = ?3 ORDER BY i.added_at DESC LIMIT 24`, l.id, kind)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if len(items) > 0 {
			rows = append(rows, Row{Title: "Recently Added in " + l.name, Link: fmt.Sprintf("/library/%d?sort=added&order=desc", l.id), Kind: "poster", Items: items})
		}
	}

	// Hero: recent, well-rated titles that have artwork worth showing.
	hero, err := s.cards(r, `WHERE i.kind IN ('movie', 'show') AND i.backdrop IS NOT NULL AND i.overview IS NOT NULL
		AND i.id IN (SELECT id FROM items WHERE kind IN ('movie', 'show') AND backdrop IS NOT NULL ORDER BY added_at DESC LIMIT 60)
		ORDER BY RANDOM() LIMIT 6`)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	for i := range hero {
		s.db.R.QueryRowContext(ctx, `SELECT COALESCE(overview, '') FROM items WHERE id = ?`, hero[i].ID).Scan(&hero[i].Overview)
	}

	// Top rated row for variety.
	top, _ := s.cards(r, `WHERE i.id IN (SELECT id FROM items WHERE kind IN ('movie', 'show') AND rating >= 7.5 AND poster IS NOT NULL
		ORDER BY RANDOM() LIMIT 24)`)
	if len(top) >= 6 {
		rows = append(rows, Row{Title: "Critically Acclaimed", Kind: "poster", Items: top})
	}
	if rows == nil {
		rows = []Row{}
	}
	writeJSON(w, map[string]any{"hero": hero, "rows": rows})
}

// continueWatching merges in-progress items with the next unwatched episode
// of shows the user has been watching.
func (s *Server) continueWatching(r *http.Request) ([]Card, error) {
	uid := currentUser(r).ID
	inProgress, err := s.cards(r, `WHERE w.position > 30 AND w.watched = 0 AND i.kind IN ('movie', 'episode')
		ORDER BY w.updated_at DESC LIMIT 20`)
	if err != nil {
		return nil, err
	}
	seenShows := map[int64]bool{}
	for _, c := range inProgress {
		if c.ShowID != 0 {
			seenShows[c.ShowID] = true
		}
	}

	// Most recently finished episode per show in the last 60 days.
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT sh.id, MAX(w.updated_at) AS t FROM watch_state w
		JOIN items e ON e.id = w.item_id AND e.kind = 'episode'
		JOIN items se ON se.id = e.parent_id JOIN items sh ON sh.id = se.parent_id
		WHERE w.user_id = ? AND w.watched = 1 AND w.updated_at > ? GROUP BY sh.id ORDER BY t DESC LIMIT 20`,
		uid, db.Now()-60*86400)
	if err != nil {
		return nil, err
	}
	type showT struct{ id, t int64 }
	var shows []showT
	for rows.Next() {
		var st showT
		rows.Scan(&st.id, &st.t)
		shows = append(shows, st)
	}
	rows.Close()

	out := inProgress
	for _, st := range shows {
		if seenShows[st.id] {
			continue
		}
		next, err := s.nextEpisode(r, st.id)
		if err != nil {
			return nil, err
		}
		if next != nil {
			out = append(out, *next)
		}
	}
	return out, nil
}

// nextEpisode returns the first unwatched episode after the last watched one.
func (s *Server) nextEpisode(r *http.Request, showID int64) (*Card, error) {
	cards, err := s.cards(r, `WHERE i.kind = 'episode' AND sh.id = ?2 AND COALESCE(w.watched, 0) = 0
		AND (i.season_number * 10000 + i.episode_number) > COALESCE((
			SELECT MAX(e2.season_number * 10000 + e2.episode_number) FROM items e2
			JOIN items s2 ON s2.id = e2.parent_id
			JOIN watch_state w2 ON w2.item_id = e2.id AND w2.user_id = ?1 AND w2.watched = 1
			WHERE s2.parent_id = ?2 AND e2.season_number > 0), -1)
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

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := ftsQuery(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, map[string]any{"movies": []Card{}, "shows": []Card{}, "episodes": []Card{}})
		return
	}
	search := func(kind string, limit int) []Card {
		// Title matches outrank overview matches.
		c, err := s.cards(r, fmt.Sprintf(`JOIN (SELECT rowid, bm25(items_fts, 10.0, 5.0, 1.0) AS score FROM items_fts
			WHERE items_fts MATCH ?2 ORDER BY score LIMIT 400) f ON f.rowid = i.id
			WHERE i.kind = ?3 ORDER BY f.score LIMIT %d`, limit), q, kind)
		if err != nil {
			return []Card{}
		}
		return c
	}
	writeJSON(w, map[string]any{
		"movies":   search("movie", 40),
		"shows":    search("show", 40),
		"episodes": search("episode", 20),
	})
}

// ---- watch state ----

func (s *Server) handleProgress(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok || !s.itemVisible(r, id) {
		writeErr(w, 400, "bad id")
		return
	}
	var body struct {
		Position float64 `json:"position"`
		Duration float64 `json:"duration"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	watched := body.Duration > 0 && body.Position/body.Duration > 0.92
	pos := body.Position
	if watched {
		pos = 0
	}
	_, err := s.db.W.ExecContext(r.Context(), `INSERT INTO watch_state(user_id, item_id, position, duration, watched, play_count, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, item_id) DO UPDATE SET position = excluded.position, duration = excluded.duration,
			play_count = watch_state.play_count + (excluded.watched AND NOT watch_state.watched),
			watched = MAX(watch_state.watched, excluded.watched),
			updated_at = excluded.updated_at`,
		currentUser(r).ID, id, pos, body.Duration, watched, boolInt(watched), db.Now())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"watched": watched})
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
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
