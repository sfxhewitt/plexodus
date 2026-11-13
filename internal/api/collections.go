package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
)

// CollectionCard is a film collection (franchise) as a grid tile.
type CollectionCard struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Poster   string `json:"poster,omitempty"`
	Backdrop string `json:"backdrop,omitempty"`
	Count    int    `json:"count"`
	Watched  int    `json:"watched"`
}

// collectionRows lists collections with at least min films the user can see;
// only collection cid when cid > 0, and only names containing like when it
// isn't empty.
//
// It counts from the items_collection_films index, a few thousand entries,
// and only then looks up the collections. Every lookup by collection_id says
// collection_id > 0 so SQLite can use that partial index: without it the
// artwork fallback for a collection with no poster of its own scanned all
// ~770k items, once per collection, and on Daniel's library the Collections
// tab never finished.
func (s *Server) collectionRows(r *http.Request, cid int64, like string, min int) ([]CollectionCard, error) {
	u := currentUser(r)
	args := []any{u.ID, min}
	films, names := "", ""
	if cid > 0 {
		args = append(args, cid)
		films = " AND i.collection_id = ?" + strconv.Itoa(len(args))
	}
	if like != "" {
		args = append(args, like)
		names = " WHERE c.name LIKE ?" + strconv.Itoa(len(args)) + " ESCAPE '\\'"
	}
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT c.id, c.name,
		COALESCE(c.poster, (SELECT i2.poster FROM items i2 WHERE i2.collection_id = c.id AND i2.collection_id > 0 AND i2.poster IS NOT NULL ORDER BY i2.year LIMIT 1), ''),
		COALESCE(c.backdrop, (SELECT i2.backdrop FROM items i2 WHERE i2.collection_id = c.id AND i2.collection_id > 0 AND i2.backdrop IS NOT NULL ORDER BY i2.year LIMIT 1), ''),
		f.n, f.watched
		FROM (SELECT i.collection_id AS cid, COUNT(*) AS n,
			SUM(EXISTS (SELECT 1 FROM watch_state w WHERE w.user_id = ?1 AND w.item_id = i.id AND w.watched = 1)) AS watched
			FROM items i`+aclJoin(u, "i")+`
			WHERE i.collection_id > 0 AND i.kind = 'movie'`+kidsCond(u, "i")+films+`
			GROUP BY i.collection_id HAVING COUNT(*) >= ?2) f
		JOIN collections c ON c.id = f.cid`+names+`
		ORDER BY c.sort_name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CollectionCard{}
	for rows.Next() {
		var c CollectionCard
		if err := rows.Scan(&c.ID, &c.Name, &c.Poster, &c.Backdrop, &c.Count, &c.Watched); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// handleCollections lists collections with two or more films in the
// libraries the user can see; ?q= filters by name.
func (s *Server) handleCollections(w http.ResponseWriter, r *http.Request) {
	like := ""
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		like = likePattern(q)
	}
	cs, err := s.collectionRows(r, 0, like, 2)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var pending int
	s.db.R.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM items WHERE kind = 'movie' AND tmdb_id > 0 AND collection_id IS NULL`).Scan(&pending)
	writeJSON(w, map[string]any{"collections": cs, "pending": pending})
}

// handleCollection is one collection: its films in release order.
func (s *Server) handleCollection(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	cs, err := s.collectionRows(r, id, "", 1)
	if err != nil || len(cs) == 0 {
		writeErr(w, 404, "not found")
		return
	}
	films, err := s.cards(r, `WHERE i.collection_id = ?2 AND i.kind = 'movie' ORDER BY i.release_date IS NULL, i.release_date, i.year, i.sort_title`, id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"collection": cs[0], "items": films})
}

// itemCollection is the collection a film is in, when it has another film
// the user can see.
func (s *Server) itemCollection(r *http.Request, itemID int64) *CollectionCard {
	var cid sql.NullInt64
	s.db.R.QueryRowContext(r.Context(), `SELECT collection_id FROM items WHERE id = ?`, itemID).Scan(&cid)
	if !cid.Valid || cid.Int64 <= 0 {
		return nil
	}
	cs, err := s.collectionRows(r, cid.Int64, "", 2)
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
	cs, err := s.collectionRows(r, 0, likePattern(q), 2)
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
