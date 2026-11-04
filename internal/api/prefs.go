package api

import (
	"net/http"
	"strconv"
	"strings"
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
