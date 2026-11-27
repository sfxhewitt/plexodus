package api

import (
	"context"
	"net/http"

	"github.com/sfxhewitt/plexodus/internal/db"
)

// "Remove from suggestions": hold OK on a card on Home (or long-press it on a
// phone) and the title stops being suggested to that user or profile. It is
// left out of the Tonight rows, Because You Watched, Critically Acclaimed,
// Recently Added, the featured titles at the top of Home and the screensaver.
// It stays in its library and in search, and Up Next still shows it if they
// start watching it after all.

// notRemoved is the SQL condition that the user (?1) hasn't taken the film or
// show whose id is the expression idExpr off their suggestions.
func notRemoved(idExpr string) string {
	return ` AND NOT EXISTS (SELECT 1 FROM suggest_hidden sh WHERE sh.user_id = ?1 AND sh.item_id = ` + idExpr + `)`
}

func (s *Server) removedSuggestions(ctx context.Context, uid int64) (n int) {
	s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM suggest_hidden WHERE user_id = ?`, uid).Scan(&n)
	return n
}

// suggestionTitle is the film or show a card stands for: an episode or a
// season (Finish a Season cards) stands for its show.
func (s *Server) suggestionTitle(ctx context.Context, id int64) int64 {
	var kind string
	var parent, grand int64
	if err := s.db.R.QueryRowContext(ctx, `SELECT i.kind, COALESCE(i.parent_id, 0), COALESCE(p.parent_id, 0)
		FROM items i LEFT JOIN items p ON p.id = i.parent_id WHERE i.id = ?`, id).Scan(&kind, &parent, &grand); err != nil {
		return 0
	}
	switch kind {
	case "movie", "show":
		return id
	case "season":
		return parent
	case "episode":
		return grand
	}
	return 0
}

// handleHideSuggestion takes a title off the user's suggestions.
func (s *Server) handleHideSuggestion(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok || !s.itemVisible(r, id) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	title := s.suggestionTitle(r.Context(), id)
	if title == 0 {
		writeErr(w, http.StatusBadRequest, "only films and shows can be removed from suggestions")
		return
	}
	uid := currentUser(r).ID
	if _, err := s.db.W.ExecContext(r.Context(), `INSERT INTO suggest_hidden(user_id, item_id, hidden_at) VALUES(?, ?, ?)
		ON CONFLICT(user_id, item_id) DO UPDATE SET hidden_at = excluded.hidden_at`, uid, title, db.Now()); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.forgetHome(uid)
	writeJSON(w, map[string]any{"ok": true, "id": title})
}

// handleHiddenSuggestions lists what the user removed, newest first.
func (s *Server) handleHiddenSuggestions(w http.ResponseWriter, r *http.Request) {
	cards, err := s.cards(r, `JOIN suggest_hidden sh2 ON sh2.item_id = i.id AND sh2.user_id = ?1 ORDER BY sh2.hidden_at DESC, i.id DESC LIMIT 500`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cards == nil {
		cards = []Card{}
	}
	writeJSON(w, map[string]any{"items": cards})
}

// handleRestoreSuggestions lets one title (or, with no id, all of them) be
// suggested again.
func (s *Server) handleRestoreSuggestions(w http.ResponseWriter, r *http.Request) {
	uid := currentUser(r).ID
	var err error
	if r.PathValue("id") == "" {
		_, err = s.db.W.ExecContext(r.Context(), `DELETE FROM suggest_hidden WHERE user_id = ?`, uid)
	} else {
		id, ok := pathID(r, "id")
		if !ok {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		// The card removed may have been a season or episode of the show.
		_, err = s.db.W.ExecContext(r.Context(), `DELETE FROM suggest_hidden WHERE user_id = ? AND item_id IN (?, ?)`,
			uid, id, s.suggestionTitle(r.Context(), id))
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.forgetHome(uid)
	writeJSON(w, map[string]bool{"ok": true})
}
