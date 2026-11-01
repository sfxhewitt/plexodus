package plex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sfxhewitt/plexodus/internal/db"
	"github.com/sfxhewitt/plexodus/internal/scanner"
)

// PathMap rewrites Plex's file paths to the paths Plexodus sees, for when
// Plex runs in a container with different mount points.
type PathMap [][2]string

// ParsePathMap parses "plexPrefix=localPrefix,plexPrefix2=localPrefix2".
func ParsePathMap(s string) PathMap {
	var m PathMap
	for _, pair := range strings.Split(s, ",") {
		from, to, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if ok && from != "" {
			m = append(m, [2]string{filepath.Clean(from), filepath.Clean(to)})
		}
	}
	return m
}

func (m PathMap) Apply(p string) string {
	p = filepath.Clean(p)
	for _, r := range m {
		if p == r[0] || strings.HasPrefix(p, r[0]+string(filepath.Separator)) {
			return r[1] + p[len(r[0]):]
		}
	}
	return p
}

type Status struct {
	Running  bool      `json:"running"`
	Phase    string    `json:"phase"`
	Section  string    `json:"section,omitempty"`
	Items    int       `json:"items"`
	Files    int       `json:"files"`
	Watched  int       `json:"watched"`
	Markers  int       `json:"markers"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitempty"`
	Error    string    `json:"error,omitempty"`
}

type Importer struct {
	db *db.DB

	mu     sync.Mutex
	status Status
}

func NewImporter(d *db.DB) *Importer { return &Importer{db: d} }

func (im *Importer) Status() Status {
	im.mu.Lock()
	defer im.mu.Unlock()
	return im.status
}

func (im *Importer) update(fn func(*Status)) {
	im.mu.Lock()
	fn(&im.status)
	im.mu.Unlock()
}

var ErrRunning = errors.New("an import is already running")

// Start runs an import in the background. Watch history is recorded for userID.
func (im *Importer) Start(ctx context.Context, c *Client, pm PathMap, userID int64, done func()) error {
	im.mu.Lock()
	if im.status.Running {
		im.mu.Unlock()
		return ErrRunning
	}
	im.status = Status{Running: true, Phase: "Connecting to Plex", Started: time.Now()}
	im.mu.Unlock()
	go func() {
		err := im.run(ctx, c, pm, userID)
		im.update(func(s *Status) {
			s.Running = false
			s.Finished = time.Now()
			s.Phase = "Done"
			if err != nil {
				s.Error = err.Error()
				s.Phase = "Failed"
			}
		})
		log.Printf("plex import finished: %+v", im.Status())
		if done != nil {
			done()
		}
	}()
	return nil
}

type section struct {
	Section
	libID int64
	kind  string
	roots []string
}

func (im *Importer) run(ctx context.Context, c *Client, pm PathMap, userID int64) error {
	sections, err := c.Sections(ctx)
	if err != nil {
		return err
	}
	for _, s := range sections {
		if s.Type != "movie" && s.Type != "show" {
			continue
		}
		sec := section{Section: s, kind: map[string]string{"movie": "movies", "show": "shows"}[s.Type]}
		for _, l := range s.Location {
			sec.roots = append(sec.roots, pm.Apply(l.Path))
		}
		if len(sec.roots) == 0 {
			continue
		}
		if sec.libID, err = im.ensureLibrary(ctx, sec); err != nil {
			return err
		}
		im.update(func(st *Status) { st.Section = s.Title })
		if s.Type == "movie" {
			err = im.importMovies(ctx, c, pm, sec, userID)
		} else {
			err = im.importShows(ctx, c, pm, sec, userID)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", s.Title, err)
		}
	}
	return nil
}

// ensureLibrary reuses a library that shares a folder with the section (or,
// failing that, one with the same name and kind), so re-running the import
// never creates a second copy of a library. Otherwise it creates one named
// after the section.
func (im *Importer) ensureLibrary(ctx context.Context, s section) (int64, error) {
	for i, r := range s.roots {
		s.roots[i] = filepath.Clean(r)
	}
	rows, err := im.db.R.QueryContext(ctx, `SELECT id, name, kind, paths FROM libraries ORDER BY id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	byName := int64(0)
	for rows.Next() {
		var id int64
		var name, kind, paths string
		rows.Scan(&id, &name, &kind, &paths)
		if kind != s.kind {
			continue
		}
		var ps []string
		json.Unmarshal([]byte(paths), &ps)
		for _, p := range ps {
			for _, r := range s.roots {
				if filepath.Clean(p) == r {
					return id, nil
				}
			}
		}
		if byName == 0 && strings.EqualFold(strings.TrimSpace(name), strings.TrimSpace(s.Title)) {
			byName = id
		}
	}
	rows.Close()
	if byName != 0 {
		return byName, nil
	}
	paths, _ := json.Marshal(s.roots)
	res, err := im.db.W.ExecContext(ctx, `INSERT INTO libraries(name, kind, paths, created_at) VALUES(?, ?, ?, ?)`,
		s.Title, s.kind, string(paths), db.Now())
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func rootFor(roots []string, p string) string {
	for _, r := range roots {
		if strings.HasPrefix(p, r+string(filepath.Separator)) {
			return r
		}
	}
	return ""
}

func plexImg(p string) sql.NullString {
	if p == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: "plex:" + p, Valid: true}
}

func nullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: s != ""} }

func genres(m *Metadata) sql.NullString {
	if len(m.Genre) == 0 {
		return sql.NullString{}
	}
	names := make([]string, len(m.Genre))
	for i, g := range m.Genre {
		names[i] = g.Tag
	}
	b, _ := json.Marshal(names)
	return sql.NullString{String: string(b), Valid: true}
}

func rating(m *Metadata) float64 {
	if m.AudienceRating > 0 {
		return m.AudienceRating
	}
	return m.Rating
}

func tmdbID(m *Metadata) sql.NullInt64 {
	if id, err := strconv.ParseInt(m.ExternalID("tmdb"), 10, 64); err == nil {
		return sql.NullInt64{Int64: id, Valid: true}
	}
	return sql.NullInt64{}
}

func sortTitle(m *Metadata) string {
	if m.TitleSort != "" {
		return strings.ToLower(m.TitleSort)
	}
	return scanner.SortTitle(m.Title)
}

// applyMetadata copies Plex metadata onto an item.
func applyMetadata(tx *sql.Tx, id int64, m *Metadata, still bool) error {
	poster, backdrop := plexImg(m.Thumb), plexImg(m.Art)
	if still {
		// Episode thumbnails are 16:9 stills; show them as the backdrop.
		poster, backdrop = sql.NullString{}, plexImg(m.Thumb)
	}
	var year sql.NullInt64
	if m.Year > 0 {
		year = sql.NullInt64{Int64: int64(m.Year), Valid: true}
	}
	_, err := tx.Exec(`UPDATE items SET title = ?, sort_title = ?, original_title = ?, year = COALESCE(?, year), overview = ?,
		tagline = ?, genres = COALESCE(?, genres), rating = ?, runtime = ?, release_date = ?, tmdb_id = COALESCE(?, tmdb_id),
		imdb_id = COALESCE(?, imdb_id), poster = COALESCE(?, poster), backdrop = COALESCE(?, backdrop), logo = COALESCE(?, logo),
		added_at = CASE WHEN ? > 0 THEN ? ELSE added_at END, meta_state = 'matched', meta_at = ?, updated_at = ? WHERE id = ?`,
		m.Title, sortTitle(m), nullStr(m.OriginalTitle), year, nullStr(m.Summary), nullStr(m.Tagline), genres(m), rating(m),
		m.Duration/60000, nullStr(m.OriginallyAvailableAt), tmdbID(m), nullStr(m.ExternalID("imdb")),
		poster, backdrop, plexImg(m.ImageOf("clearLogo")), m.AddedAt, m.AddedAt, db.Now(), db.Now(), id)
	return err
}

var codecNames = map[string]string{"dca": "dts", "mpeg4": "mpeg4", "h265": "hevc"}

func codec(c string) string {
	if n, ok := codecNames[c]; ok {
		return n
	}
	return c
}

func (im *Importer) addFiles(tx *sql.Tx, itemID, libID int64, m *Metadata, pm PathMap) (int, error) {
	n := 0
	for _, media := range m.Media {
		for _, part := range media.Part {
			if part.File == "" {
				continue
			}
			var probed sql.NullInt64
			if media.VideoCodec != "" {
				probed = sql.NullInt64{Int64: db.Now(), Valid: true}
			}
			dur := float64(part.Duration)
			if dur == 0 {
				dur = float64(media.Duration)
			}
			// mtime is unknown; 0 lets the next scan fill it in without re-probing.
			_, err := tx.Exec(`INSERT INTO media_files(item_id, library_id, path, size, mtime, container, video_codec, audio_codec,
					width, height, duration, bitrate, probed_at) VALUES(?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?)
				ON CONFLICT(path) DO UPDATE SET item_id = excluded.item_id,
					container = COALESCE(media_files.container, excluded.container),
					video_codec = COALESCE(media_files.video_codec, excluded.video_codec),
					audio_codec = COALESCE(media_files.audio_codec, excluded.audio_codec),
					width = COALESCE(media_files.width, excluded.width), height = COALESCE(media_files.height, excluded.height),
					duration = COALESCE(media_files.duration, excluded.duration),
					bitrate = COALESCE(media_files.bitrate, excluded.bitrate),
					probed_at = COALESCE(media_files.probed_at, excluded.probed_at)`,
				itemID, libID, pm.Apply(part.File), part.Size, nullStr(part.Container), nullStr(codec(media.VideoCodec)),
				nullStr(codec(media.AudioCodec)), media.Width, media.Height, dur/1000, media.Bitrate*1000, probed)
			if err != nil {
				return n, err
			}
			n++
		}
	}
	return n, nil
}

func (im *Importer) addWatch(tx *sql.Tx, userID, itemID int64, m *Metadata) (bool, error) {
	if m.ViewCount == 0 && m.ViewOffset == 0 {
		return false, nil
	}
	ts := m.LastViewedAt
	if ts == 0 {
		ts = db.Now()
	}
	watched := m.ViewCount > 0 && m.ViewOffset == 0
	_, err := tx.Exec(`INSERT INTO watch_state(user_id, item_id, position, duration, watched, play_count, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, item_id) DO UPDATE SET position = excluded.position, duration = excluded.duration,
			watched = MAX(watch_state.watched, excluded.watched), play_count = MAX(watch_state.play_count, excluded.play_count),
			updated_at = MAX(watch_state.updated_at, excluded.updated_at)`,
		userID, itemID, float64(m.ViewOffset)/1000, float64(m.Duration)/1000, watched || m.ViewCount > 0, m.ViewCount, ts)
	return true, err
}

func (im *Importer) addLink(tx *sql.Tx, itemID int64, m *Metadata) (int, error) {
	var markersAt sql.NullInt64
	if m.Marker != nil {
		markersAt = sql.NullInt64{Int64: db.Now(), Valid: true}
	}
	if _, err := tx.Exec(`INSERT INTO plex_links(item_id, rating_key, markers_at) VALUES(?, ?, ?)
		ON CONFLICT(item_id) DO UPDATE SET rating_key = excluded.rating_key, markers_at = COALESCE(excluded.markers_at, plex_links.markers_at)`,
		itemID, m.RatingKey, markersAt); err != nil {
		return 0, err
	}
	return SaveMarkers(tx, itemID, m.Marker)
}

// SaveMarkers stores intro/credits markers for an item.
func SaveMarkers(tx *sql.Tx, itemID int64, ms []Marker) (int, error) {
	n := 0
	for _, mk := range ms {
		if mk.Type != "intro" && mk.Type != "credits" {
			continue
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO markers(item_id, type, start, "end") VALUES(?, ?, ?, ?)`,
			itemID, mk.Type, float64(mk.StartTime)/1000, float64(mk.EndTime)/1000); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

const pageSize = 500

func (im *Importer) importMovies(ctx context.Context, c *Client, pm PathMap, s section, userID int64) error {
	im.update(func(st *Status) { st.Phase = "Importing movies" })
	return c.Each(ctx, s.Key, TypeMovie, pageSize, func(page []Metadata) error {
		var items, files, watched, markers int
		err := im.db.Tx(ctx, func(tx *sql.Tx) error {
			for i := range page {
				m := &page[i]
				path := firstFile(m, pm)
				root := rootFor(s.roots, path)
				if root == "" {
					continue
				}
				title, year := scanner.MovieInfo(path, root)
				id, err := scanner.EnsureItem(tx, scanner.MovieGUID(s.libID, title, year), s.libID, "movie",
					sql.NullInt64{}, m.Title, m.Year, sql.NullInt64{}, sql.NullInt64{}, nonZero(m.AddedAt))
				if err != nil {
					return err
				}
				if err := applyMetadata(tx, id, m, false); err != nil {
					return err
				}
				n, err := im.addFiles(tx, id, s.libID, m, pm)
				if err != nil {
					return err
				}
				w, err := im.addWatch(tx, userID, id, m)
				if err != nil {
					return err
				}
				k, err := im.addLink(tx, id, m)
				if err != nil {
					return err
				}
				items++
				files += n
				markers += k
				if w {
					watched++
				}
			}
			return nil
		})
		im.update(func(st *Status) { st.Items += items; st.Files += files; st.Watched += watched; st.Markers += markers })
		return err
	})
}

func firstFile(m *Metadata, pm PathMap) string {
	for _, media := range m.Media {
		for _, p := range media.Part {
			if p.File != "" {
				return pm.Apply(p.File)
			}
		}
	}
	return ""
}

func nonZero(t int64) int64 {
	if t > 0 {
		return t
	}
	return db.Now()
}

func (im *Importer) importShows(ctx context.Context, c *Client, pm PathMap, s section, userID int64) error {
	im.update(func(st *Status) { st.Phase = "Reading shows" })
	shows := map[string]Metadata{}
	if err := c.Each(ctx, s.Key, TypeShow, pageSize, func(page []Metadata) error {
		for _, m := range page {
			shows[m.RatingKey] = m
		}
		return nil
	}); err != nil {
		return err
	}
	im.update(func(st *Status) { st.Phase = "Reading seasons" })
	seasons := map[string]Metadata{}
	if err := c.Each(ctx, s.Key, TypeSeason, pageSize, func(page []Metadata) error {
		for _, m := range page {
			m.Media = nil
			seasons[m.RatingKey] = m
		}
		return nil
	}); err != nil {
		return err
	}

	im.update(func(st *Status) { st.Phase = "Importing episodes" })
	doneShows := map[string]int64{}
	doneSeasons := map[string]int64{}
	return c.Each(ctx, s.Key, TypeEpisode, pageSize, func(page []Metadata) error {
		var items, files, watched, markers int
		err := im.db.Tx(ctx, func(tx *sql.Tx) error {
			for i := range page {
				m := &page[i]
				path := firstFile(m, pm)
				root := rootFor(s.roots, path)
				if root == "" {
					continue
				}
				show := shows[m.GrandparentRatingKey]
				if show.RatingKey == "" {
					continue
				}
				// Same show folder rule as the scanner, so later scans
				// attach new episodes to this show.
				rel, _ := filepath.Rel(root, path)
				parts := strings.Split(filepath.ToSlash(rel), "/")
				showDir := filepath.Join(root, "\x00"+strings.ToLower(show.Title))
				if len(parts) >= 2 {
					showDir = filepath.Join(root, parts[0])
				}

				showID, ok := doneShows[show.RatingKey+showDir]
				if !ok {
					var err error
					showID, err = scanner.EnsureItem(tx, scanner.ShowGUID(s.libID, showDir), s.libID, "show", sql.NullInt64{},
						show.Title, show.Year, sql.NullInt64{}, sql.NullInt64{}, nonZero(show.AddedAt))
					if err != nil {
						return err
					}
					if err := applyMetadata(tx, showID, &show, false); err != nil {
						return err
					}
					if _, err := im.addLink(tx, showID, &show); err != nil {
						return err
					}
					doneShows[show.RatingKey+showDir] = showID
				}

				sn := m.ParentIndex
				seasonKey := fmt.Sprintf("%d:%d", showID, sn)
				seasonID, ok := doneSeasons[seasonKey]
				if !ok {
					title := fmt.Sprintf("Season %d", sn)
					if sn == 0 {
						title = "Specials"
					}
					var err error
					seasonID, err = scanner.EnsureItem(tx, scanner.SeasonGUID(showID, sn), s.libID, "season",
						sql.NullInt64{Int64: showID, Valid: true}, title, 0, sql.NullInt64{Int64: int64(sn), Valid: true},
						sql.NullInt64{}, nonZero(m.AddedAt))
					if err != nil {
						return err
					}
					if se, ok := seasons[m.ParentRatingKey]; ok {
						if se.Title == "" {
							se.Title = title
						}
						se.Year = 0
						if err := applyMetadata(tx, seasonID, &se, false); err != nil {
							return err
						}
					}
					doneSeasons[seasonKey] = seasonID
				}

				id, err := scanner.EnsureItem(tx, scanner.EpisodeGUID(seasonID, m.Index), s.libID, "episode",
					sql.NullInt64{Int64: seasonID, Valid: true}, m.Title, 0, sql.NullInt64{Int64: int64(sn), Valid: true},
					sql.NullInt64{Int64: int64(m.Index), Valid: true}, nonZero(m.AddedAt))
				if err != nil {
					return err
				}
				m.Year = 0
				if err := applyMetadata(tx, id, m, true); err != nil {
					return err
				}
				n, err := im.addFiles(tx, id, s.libID, m, pm)
				if err != nil {
					return err
				}
				w, err := im.addWatch(tx, userID, id, m)
				if err != nil {
					return err
				}
				k, err := im.addLink(tx, id, m)
				if err != nil {
					return err
				}
				items++
				files += n
				markers += k
				if w {
					watched++
				}
			}
			// Shows list as recently added by their newest episode.
			_, err := tx.Exec(`UPDATE items SET added_at = (SELECT MAX(e.added_at) FROM items se JOIN items e ON e.parent_id = se.id
				WHERE se.parent_id = items.id) WHERE kind = 'show' AND library_id = ? AND id IN (SELECT value FROM json_each(?))`,
				s.libID, idsJSON(doneShows))
			return err
		})
		im.update(func(st *Status) { st.Items += items; st.Files += files; st.Watched += watched; st.Markers += markers })
		return err
	})
}

func idsJSON(m map[string]int64) string {
	ids := make([]int64, 0, len(m))
	for _, id := range m {
		ids = append(ids, id)
	}
	b, _ := json.Marshal(ids)
	return string(b)
}
