package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
)

// CollectionCard is a collection as a grid tile: a TMDB franchise ("The
// Matrix Collection") or any collection from Plex, films or shows.
type CollectionCard struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Poster   string `json:"poster,omitempty"`
	Backdrop string `json:"backdrop,omitempty"`
	Count    int    `json:"count"`
	Watched  int    `json:"watched"`
	Source   string `json:"source"` // tmdb | plex
	Kind     string `json:"kind"`   // movie | show
	Summary  string `json:"summary,omitempty"`
	Group    string `json:"group"`
}

// collectionQuery picks which collections collectionRows lists.
type collectionQuery struct {
	id      int64  // just this one
	like    string // names matching this LIKE pattern
	minTMDB int    // franchises need this many films the viewer can see
}

// collectionRows lists collections the user can see something in: TMDB
// franchises with at least minTMDB films, and Plex collections with at
// least one title. A Plex collection that is the same franchise as a TMDB
// one (Plex makes those itself) replaces it.
//
// It counts from indexes, never the whole items table: the
// items_collection_films index for franchises (every lookup by collection_id
// says collection_id > 0 so SQLite can use that partial index) and
// collection_items for Plex. Without that the artwork fallback scanned all
// ~770k items once per collection, and on Daniel's library the Collections
// tab never finished.
func (s *Server) collectionRows(r *http.Request, cq collectionQuery) ([]CollectionCard, error) {
	u := currentUser(r)
	args := []any{u.ID, cq.minTMDB}
	films, plexOnly, names := "", "", ""
	if cq.id > 0 {
		args = append(args, cq.id)
		n := strconv.Itoa(len(args))
		films = " AND i.collection_id = ?" + n
		plexOnly = " AND ci.collection_id = ?" + n
	}
	if cq.like != "" {
		args = append(args, cq.like)
		names = " WHERE c.name LIKE ?" + strconv.Itoa(len(args)) + " ESCAPE '\\'"
	}
	watched := `SUM(EXISTS (SELECT 1 FROM watch_state w WHERE w.user_id = ?1 AND w.item_id = i.id AND w.watched = 1))`
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT c.id, c.name, c.source, c.kind, COALESCE(c.summary, ''),
		COALESCE(c.poster, CASE WHEN c.source = 'plex'
			THEN (SELECT i2.poster FROM collection_items ci2 JOIN items i2 ON i2.id = ci2.item_id WHERE ci2.collection_id = c.id AND i2.poster IS NOT NULL ORDER BY ci2.position LIMIT 1)
			ELSE (SELECT i2.poster FROM items i2 WHERE i2.collection_id = c.id AND i2.collection_id > 0 AND i2.poster IS NOT NULL ORDER BY i2.year LIMIT 1) END, ''),
		COALESCE(c.backdrop, CASE WHEN c.source = 'plex'
			THEN (SELECT i2.backdrop FROM collection_items ci2 JOIN items i2 ON i2.id = ci2.item_id WHERE ci2.collection_id = c.id AND i2.backdrop IS NOT NULL ORDER BY ci2.position LIMIT 1)
			ELSE (SELECT i2.backdrop FROM items i2 WHERE i2.collection_id = c.id AND i2.collection_id > 0 AND i2.backdrop IS NOT NULL ORDER BY i2.year LIMIT 1) END, ''),
		f.n, f.watched
		FROM (SELECT i.collection_id AS cid, COUNT(*) AS n, `+watched+` AS watched
			FROM items i`+aclJoin(u, "i")+`
			WHERE i.collection_id > 0 AND i.kind = 'movie'`+kidsCond(u, "i")+films+`
			GROUP BY i.collection_id HAVING COUNT(*) >= ?2
		UNION ALL
		SELECT ci.collection_id, COUNT(*), `+watched+`
			FROM collection_items ci JOIN items i ON i.id = ci.item_id`+aclJoin(u, "i")+`
			WHERE 1`+kidsCond(u, "i")+plexOnly+`
			GROUP BY ci.collection_id) f
		JOIN collections c ON c.id = f.cid`+names+`
		ORDER BY c.sort_name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CollectionCard{}
	for rows.Next() {
		var c CollectionCard
		if err := rows.Scan(&c.ID, &c.Name, &c.Source, &c.Kind, &c.Summary, &c.Poster, &c.Backdrop, &c.Count, &c.Watched); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return groupCollections(out), nil
}

// handleCollections lists every collection the user can see something in,
// sorted into groups (see collectionGroups); ?q= filters by name.
func (s *Server) handleCollections(w http.ResponseWriter, r *http.Request) {
	cq := collectionQuery{minTMDB: 2}
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		cq.like = likePattern(q)
	}
	cs, err := s.collectionRows(r, cq)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var pending int
	s.db.R.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM items WHERE kind = 'movie' AND tmdb_id > 0 AND collection_id IS NULL`).Scan(&pending)
	if cq.like != "" {
		writeJSON(w, map[string]any{"collections": cs, "pending": pending})
		return
	}
	// Each collection once, inside its group: on a big library the list is
	// thousands long, and the TV's WebView parses all of it.
	writeJSON(w, map[string]any{"groups": collectionShelves(cs), "count": len(cs), "pending": pending})
}

// handleCollection is one collection: franchises in release order, other
// Plex collections in the order Plex keeps them.
func (s *Server) handleCollection(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	cs, err := s.collectionRows(r, collectionQuery{id: id, minTMDB: 1})
	if err != nil || len(cs) == 0 {
		writeErr(w, 404, "not found")
		return
	}
	c := cs[0]
	var items []Card
	switch {
	case c.Source != "plex":
		items, err = s.cards(r, `WHERE i.collection_id = ?2 AND i.collection_id > 0 AND i.kind = 'movie' ORDER BY i.release_date IS NULL, i.release_date, i.year, i.sort_title`, id)
	case c.Group == groupFranchises:
		items, err = s.cards(r, `JOIN collection_items ci ON ci.item_id = i.id AND ci.collection_id = ?2 ORDER BY i.release_date IS NULL, i.release_date, i.year, ci.position`, id)
	default:
		items, err = s.cards(r, `JOIN collection_items ci ON ci.item_id = i.id AND ci.collection_id = ?2 ORDER BY ci.position`, id)
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"collection": c, "items": items})
}

// itemCollection is the franchise a film is in, when it has another film
// the user can see.
func (s *Server) itemCollection(r *http.Request, itemID int64) *CollectionCard {
	var cid sql.NullInt64
	s.db.R.QueryRowContext(r.Context(), `SELECT collection_id FROM items WHERE id = ?`, itemID).Scan(&cid)
	if !cid.Valid || cid.Int64 <= 0 {
		return nil
	}
	cs, err := s.collectionRows(r, collectionQuery{id: cid.Int64, minTMDB: 2})
	if err != nil || len(cs) == 0 {
		return nil
	}
	return &cs[0]
}

// handleWatchlistAll is the user's whole watchlist, newest first, watched
// titles included (the Home row shows only unwatched ones).
func (s *Server) handleWatchlistAll(w http.ResponseWriter, r *http.Request) {
	cards, err := s.cards(r, `JOIN watchlist wl2 ON wl2.item_id = i.id AND wl2.user_id = ?1 ORDER BY COALESCE(w.watched, 0), wl2.added_at DESC`)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"items": cards})
}

// searchCollections finds collections by name ("bond", "lord of the rings").
func (s *Server) searchCollections(r *http.Request, q string) []CollectionCard {
	q = strings.TrimSpace(q)
	if len([]rune(q)) < 2 {
		return []CollectionCard{}
	}
	cs, err := s.collectionRows(r, collectionQuery{like: likePattern(q), minTMDB: 2})
	if err != nil {
		return []CollectionCard{}
	}
	if len(cs) > 20 {
		cs = cs[:20]
	}
	return cs
}

// likePattern matches names containing q, for LIKE ... ESCAPE '\'.
func likePattern(q string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
}
