package api

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sfxhewitt/plexodus/internal/db"
)

type accessBody struct {
	AllLibraries bool    `json:"allLibraries"`
	Libraries    []int64 `json:"libraries"`
}

func (s *Server) handleUserAccess(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var b accessBody
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.auth.SetAccess(r.Context(), id, b.AllLibraries, b.Libraries); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- invites ----

const inviteTTL = 7 * 24 * time.Hour

type Invite struct {
	Token        string  `json:"token"`
	Note         string  `json:"note,omitempty"`
	AllLibraries bool    `json:"allLibraries"`
	Libraries    []int64 `json:"libraries"`
	CreatedAt    int64   `json:"createdAt"`
	ExpiresAt    int64   `json:"expiresAt"`
	UsedBy       string  `json:"usedBy,omitempty"`
}

func (s *Server) handleListInvites(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT i.token, COALESCE(i.note, ''), i.all_libraries, i.libraries, i.created_at,
		i.expires_at, COALESCE(u.username, '') FROM invites i LEFT JOIN users u ON u.id = i.used_by
		WHERE i.used_by IS NOT NULL OR i.expires_at > ? ORDER BY i.created_at DESC LIMIT 50`, db.Now())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []Invite{}
	for rows.Next() {
		var inv Invite
		var libs string
		rows.Scan(&inv.Token, &inv.Note, &inv.AllLibraries, &libs, &inv.CreatedAt, &inv.ExpiresAt, &inv.UsedBy)
		json.Unmarshal([]byte(libs), &inv.Libraries)
		out = append(out, inv)
	}
	writeJSON(w, out)
}

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	var b struct {
		accessBody
		Note string `json:"note"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if !b.AllLibraries && len(b.Libraries) == 0 {
		writeErr(w, 400, "choose at least one library to share")
		return
	}
	if b.Libraries == nil {
		b.Libraries = []int64{}
	}
	libs, _ := json.Marshal(b.Libraries)
	tok := randomHex(16)
	now := time.Now()
	if _, err := s.db.W.ExecContext(r.Context(), `INSERT INTO invites(token, created_by, note, all_libraries, libraries, created_at, expires_at)
		VALUES(?, ?, ?, ?, ?, ?, ?)`, tok, currentUser(r).ID, b.Note, b.AllLibraries, string(libs), now.Unix(), now.Add(inviteTTL).Unix()); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]string{"token": tok, "url": externalBase(r) + "/invite/" + tok})
}

func (s *Server) handleDeleteInvite(w http.ResponseWriter, r *http.Request) {
	s.db.W.ExecContext(r.Context(), `DELETE FROM invites WHERE token = ? AND used_by IS NULL`, r.PathValue("token"))
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) openInvite(r *http.Request) (inviter string, all bool, libs []int64, ok bool) {
	var raw string
	err := s.db.R.QueryRowContext(r.Context(), `SELECT u.username, i.all_libraries, i.libraries FROM invites i
		JOIN users u ON u.id = i.created_by WHERE i.token = ? AND i.used_by IS NULL AND i.expires_at > ?`,
		r.PathValue("token"), db.Now()).Scan(&inviter, &all, &raw)
	if err != nil {
		return "", false, nil, false
	}
	json.Unmarshal([]byte(raw), &libs)
	return inviter, all, libs, true
}

// handleGetInvite is public: it tells the invite page whether the link works.
func (s *Server) handleGetInvite(w http.ResponseWriter, r *http.Request) {
	inviter, _, _, ok := s.openInvite(r)
	if !ok {
		writeErr(w, 404, "this invite link is invalid, expired or already used")
		return
	}
	writeJSON(w, map[string]string{"inviter": inviter})
}

// handleAcceptInvite creates the account with the invite's library access and signs in.
func (s *Server) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	_, all, libs, ok := s.openInvite(r)
	if !ok {
		writeErr(w, 404, "this invite link is invalid, expired or already used")
		return
	}
	var c credentials
	if err := readJSON(r, &c); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// Claim the invite first so two people can't use one link.
	res, err := s.db.W.ExecContext(r.Context(), `UPDATE invites SET used_at = ?, used_by = (SELECT created_by FROM invites WHERE token = ?)
		WHERE token = ? AND used_by IS NULL`, db.Now(), r.PathValue("token"), r.PathValue("token"))
	if n, _ := res.RowsAffected(); err != nil || n == 0 {
		writeErr(w, 409, "this invite was just used")
		return
	}
	u, err := s.auth.CreateUser(r.Context(), c.Username, c.Password, false)
	if err != nil {
		s.db.W.ExecContext(r.Context(), `UPDATE invites SET used_at = NULL, used_by = NULL WHERE token = ?`, r.PathValue("token"))
		writeErr(w, 400, err.Error())
		return
	}
	s.db.W.ExecContext(r.Context(), `UPDATE invites SET used_by = ? WHERE token = ?`, u.ID, r.PathValue("token"))
	if err := s.auth.SetAccess(r.Context(), u.ID, all, libs); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.handleLoginWith(w, r, c)
}

// loginLimiter slows password guessing now that the server faces the
// internet: 10 failed attempts per IP per 10 minutes.
type loginLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

var limiter = &loginLimiter{hits: map[string][]time.Time{}}

// clientIP trusts X-Forwarded-For only from a local reverse proxy, so remote
// clients can't dodge the limiter by forging the header.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			parts := strings.Split(fwd, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	return host
}

func (l *loginLimiter) recent(ip string) []time.Time {
	cutoff := time.Now().Add(-10 * time.Minute)
	keep := l.hits[ip][:0]
	for _, t := range l.hits[ip] {
		if t.After(cutoff) {
			keep = append(keep, t)
		}
	}
	l.hits[ip] = keep
	return keep
}

func (l *loginLimiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(ip)) >= 10
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	l.hits[ip] = append(l.recent(ip), time.Now())
	l.mu.Unlock()
}
