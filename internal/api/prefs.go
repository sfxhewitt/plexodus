package api

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/sfxhewitt/plexodus/internal/db"
)

// ---- per-user library display choices ----

// handleLibraryPrefs updates the signed-in user's choices for one library:
// {"hidden": true} hides it from the menu, {"hiddenHome": true} keeps its
// titles off Home. Fields left out are unchanged.
func (s *Server) handleLibraryPrefs(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok || !currentUser(r).CanSee(id) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var body struct {
		Hidden     *bool `json:"hidden"`
		HiddenHome *bool `json:"hiddenHome"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var exists int
	s.db.R.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM libraries WHERE id = ?`, id).Scan(&exists)
	if exists == 0 {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	// NULL keeps the stored value (or the default, 0, for a new row).
	nb := func(b *bool) any {
		if b == nil {
			return nil
		}
		return boolInt(*b)
	}
	_, err := s.db.W.ExecContext(r.Context(), `INSERT INTO user_library_prefs(user_id, library_id, hide_nav, hide_home)
		VALUES(?1, ?2, COALESCE(?3, 0), COALESCE(?4, 0))
		ON CONFLICT(user_id, library_id) DO UPDATE SET
			hide_nav = COALESCE(?3, hide_nav), hide_home = COALESCE(?4, hide_home)`,
		currentUser(r).ID, id, nb(body.Hidden), nb(body.HiddenHome))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// hiddenHomeLibraries returns the libraries the user chose to keep off Home.
func (s *Server) hiddenHomeLibraries(r *http.Request) map[int64]bool {
	out := map[int64]bool{}
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT library_id FROM user_library_prefs WHERE user_id = ? AND hide_home = 1`, currentUser(r).ID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		out[id] = true
	}
	return out
}

// notInLibs is an SQL fragment excluding items in the given libraries (ids
// come from the database, so building them into the SQL is safe).
func notInLibs(libs map[int64]bool) string {
	if len(libs) == 0 {
		return ""
	}
	ids := make([]string, 0, len(libs))
	for id := range libs {
		ids = append(ids, strconv.FormatInt(id, 10))
	}
	return " AND library_id NOT IN (" + strings.Join(ids, ",") + ")"
}

// ---- watchlist and Continue Watching ----

// handleWatchlist adds ({"on": true}) or removes ({"on": false}) a film or show.
func (s *Server) handleWatchlist(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok || !s.itemVisible(r, id) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	var body struct {
		On bool `json:"on"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	var err error
	if body.On {
		_, err = s.db.W.ExecContext(r.Context(), `INSERT INTO watchlist(user_id, item_id, added_at) VALUES(?, ?, ?)
			ON CONFLICT(user_id, item_id) DO NOTHING`, currentUser(r).ID, id, db.Now())
	} else {
		_, err = s.db.W.ExecContext(r.Context(), `DELETE FROM watchlist WHERE user_id = ? AND item_id = ?`, currentUser(r).ID, id)
	}
	s.smart.forget(currentUser(r).ID) // cards carry the watchlist flag
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// handleHideContinue takes a film or episode (and, for an episode, its show)
// off Continue Watching. It comes back once it's watched again.
func (s *Server) handleHideContinue(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok || !s.itemVisible(r, id) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	ids := []int64{id}
	var showID int64
	s.db.R.QueryRowContext(r.Context(), `SELECT COALESCE(se.parent_id, 0) FROM items e JOIN items se ON se.id = e.parent_id
		WHERE e.id = ? AND e.kind = 'episode'`, id).Scan(&showID)
	if showID != 0 {
		ids = append(ids, showID)
	}
	now := db.Now()
	for _, x := range ids {
		if _, err := s.db.W.ExecContext(r.Context(), `INSERT INTO continue_hidden(user_id, item_id, hidden_at) VALUES(?, ?, ?)
			ON CONFLICT(user_id, item_id) DO UPDATE SET hidden_at = excluded.hidden_at`, currentUser(r).ID, x, now); err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, map[string]bool{"ok": true})
}
