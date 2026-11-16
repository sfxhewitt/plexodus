package api

import (
	"net/http"
	"strconv"
)

// HistoryCard is a film or episode on History, with when it was last played.
type HistoryCard struct {
	Card
	PlayedAt int64 `json:"playedAt"`
}

const (
	historyPage    = 150
	historyPageMax = 500
)

// handleHistory lists everything the signed-in profile has played, however
// briefly, here or in Plex: newest first, one entry per film or episode,
// with its resume point. before (a playedAt) pages further back.
func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := historyPage
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		limit = min(n, historyPageMax)
	}
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	if before <= 0 {
		before = 1 << 62
	}
	uid := currentUser(r).ID
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT w.item_id, w.played_at FROM watch_state w
		JOIN items i ON i.id = w.item_id AND i.kind IN ('movie', 'episode')
		WHERE w.user_id = ? AND w.played_at > 0 AND w.played_at < ?
		ORDER BY w.played_at DESC, w.item_id DESC LIMIT ?`, uid, before, limit+1)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var ids []int64
	at := map[int64]int64{}
	for rows.Next() {
		var id, t int64
		if err := rows.Scan(&id, &t); err != nil {
			rows.Close()
			writeErr(w, 500, err.Error())
			return
		}
		ids = append(ids, id)
		at[id] = t
	}
	rows.Close()
	more := len(ids) > limit
	if more {
		ids = ids[:limit]
	}
	var next int64
	if more && len(ids) > 0 {
		next = at[ids[len(ids)-1]]
	}
	// cardsByIDs keeps the order and drops what this profile can't see.
	cards, err := s.cardsByIDs(r, ids)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := make([]HistoryCard, len(cards))
	for i, c := range cards {
		out[i] = HistoryCard{c, at[c.ID]}
	}
	writeJSON(w, map[string]any{"items": out, "more": more, "before": next})
}

// handleHistoryRemove takes one film or episode off History. Its resume
// point and watched state stay; playing it again brings it back.
func (s *Server) handleHistoryRemove(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	if _, err := s.db.W.ExecContext(r.Context(), `UPDATE watch_state SET played_at = 0 WHERE user_id = ? AND item_id = ?`,
		currentUser(r).ID, id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
