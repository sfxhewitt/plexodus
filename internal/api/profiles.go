package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/rating"
)

// ---- kids filtering ----

// kidsExpr is an SQL condition keeping items (alias) rated at or below a
// kids profile's age limit; "" for everyone else. Seasons and episodes go
// by their show's rating, and unrated titles are hidden.
func kidsExpr(u auth.User, alias string) string {
	if u.MaxAge == nil {
		return ""
	}
	return fmt.Sprintf(`COALESCE(CASE %[1]s.kind
		WHEN 'season' THEN (SELECT k1.age FROM items k1 WHERE k1.id = %[1]s.parent_id)
		WHEN 'episode' THEN (SELECT k2.age FROM items k1 JOIN items k2 ON k2.id = k1.parent_id WHERE k1.id = %[1]s.parent_id)
		ELSE %[1]s.age END, 99) <= %[2]d`, alias, *u.MaxAge)
}

// kidsCond is kidsExpr as an " AND ..." fragment.
func kidsCond(u auth.User, alias string) string {
	if e := kidsExpr(u, alias); e != "" {
		return " AND " + e
	}
	return ""
}

// kidsTop filters a query over films and shows only (unaliased items).
func kidsTop(u auth.User) string {
	if u.MaxAge == nil {
		return ""
	}
	return fmt.Sprintf(" AND COALESCE(age, 99) <= %d", *u.MaxAge)
}

// ---- profiles ----

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	ps, err := s.auth.Profiles(r.Context(), u.AccountID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{
		"profiles":  ps,
		"current":   u.ID,
		"canManage": u.CanManageProfiles(),
		"levels":    rating.Levels,
		"max":       auth.MaxProfiles,
	})
}

// requireManager allows profile changes only from the account itself, so a
// kids profile can't edit itself or others.
func requireManager(w http.ResponseWriter, r *http.Request) bool {
	if !currentUser(r).CanManageProfiles() {
		writeErr(w, http.StatusForbidden, "switch to the main profile to manage profiles")
		return false
	}
	return true
}

func (s *Server) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	if !requireManager(w, r) {
		return
	}
	var in auth.ProfileInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	p, err := s.auth.CreateProfile(r.Context(), currentUser(r).AccountID, in)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, p)
}

func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	if !requireManager(w, r) {
		return
	}
	id, _ := pathID(r, "id")
	var in auth.ProfileInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.auth.UpdateProfile(r.Context(), currentUser(r).AccountID, id, in); err != nil {
		code := 400
		if errors.Is(err, auth.ErrNoProfile) {
			code = 404
		}
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	if !requireManager(w, r) {
		return
	}
	id, _ := pathID(r, "id")
	if err := s.auth.DeleteProfile(r.Context(), currentUser(r).AccountID, id); err != nil {
		writeErr(w, 404, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// handleSwitchProfile makes a profile active on this device: {"pin": "1234"}.
func (s *Server) handleSwitchProfile(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r, "id")
	var body struct {
		PIN string `json:"pin"`
	}
	readJSON(r, &body)
	ip := clientIP(r)
	if limiter.blocked(ip) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, try again in a few minutes")
		return
	}
	u, err := s.auth.SwitchProfile(r.Context(), auth.TokenFrom(r), currentUser(r), id, body.PIN)
	switch {
	case errors.Is(err, auth.ErrPIN):
		limiter.fail(ip)
		writeErr(w, http.StatusForbidden, err.Error())
	case errors.Is(err, auth.ErrNoProfile):
		writeErr(w, 404, err.Error())
	case err != nil:
		writeErr(w, 500, err.Error())
	default:
		writeJSON(w, u)
	}
}
