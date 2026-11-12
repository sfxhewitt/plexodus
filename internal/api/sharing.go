package api

import (
	"database/sql"
	"encoding/json"
	"log"
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

// handleUserAccess sets which libraries an account sees. For a Plex friend,
// followPlex=true goes back to mirroring their Plex share; otherwise the
// libraries given here stick and the hourly Plex check leaves them alone.
func (s *Server) handleUserAccess(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var b struct {
		accessBody
		FollowPlex bool `json:"followPlex"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ctx := r.Context()
	var plexID sql.NullInt64
	if err := s.db.R.QueryRowContext(ctx, `SELECT plex_id FROM users WHERE id = ? AND parent_id IS NULL`, id).Scan(&plexID); err != nil {
		writeErr(w, 404, "no such user")
		return
	}
	if b.FollowPlex {
		if !plexID.Valid {
			writeErr(w, 400, "this account isn't linked to Plex")
			return
		}
		if err := s.auth.SetAccessCustom(ctx, id, false); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		shares, err := s.plexShares(ctx)
		if err != nil {
			writeErr(w, 502, "saved, but couldn't read Plex sharing just now ("+err.Error()+"); it will catch up within the hour")
			return
		}
		sh, shared := findShare(shares, plexID.Int64, "")
		if !shared {
			err = s.auth.SetAccess(ctx, id, false, nil)
		} else {
			err = s.applyShare(ctx, id, sh)
		}
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	if err := s.auth.SetAccess(ctx, id, b.AllLibraries, b.Libraries); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if plexID.Valid {
		if err := s.auth.SetAccessCustom(ctx, id, true); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- invites ----

const (
	inviteTTL     = 7 * 24 * time.Hour
	maxInviteDays = 365
)

type Invite struct {
	Token        string  `json:"token"`
	Code         string  `json:"code,omitempty"` // "K7QM-2XPA"
	Note         string  `json:"note,omitempty"`
	AllLibraries bool    `json:"allLibraries"`
	Libraries    []int64 `json:"libraries"`
	CreatedAt    int64   `json:"createdAt"`
	ExpiresAt    int64   `json:"expiresAt"`
	// MaxUses 0 means any number of people can use it.
	MaxUses int    `json:"maxUses"`
	Uses    int    `json:"uses"`
	UsedBy  string `json:"usedBy,omitempty"`
}

func (s *Server) handleListInvites(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT i.token, COALESCE(i.code, ''), COALESCE(i.note, ''), i.all_libraries, i.libraries,
		i.created_at, i.expires_at, i.max_uses, i.uses, COALESCE(u.username, '') FROM invites i LEFT JOIN users u ON u.id = i.used_by
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
		rows.Scan(&inv.Token, &inv.Code, &inv.Note, &inv.AllLibraries, &libs, &inv.CreatedAt, &inv.ExpiresAt, &inv.MaxUses, &inv.Uses, &inv.UsedBy)
		inv.Code = formatCode(inv.Code)
		json.Unmarshal([]byte(libs), &inv.Libraries)
		out = append(out, inv)
	}
	writeJSON(w, out)
}

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	var b struct {
		accessBody
		Note string `json:"note"`
		// Days until it expires (default 7, at most a year).
		Days int `json:"days"`
		// MaxUses: 1 (default) for one friend; 0 for any number of people.
		MaxUses *int `json:"maxUses"`
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
	ttl := inviteTTL
	if b.Days > 0 {
		ttl = time.Duration(min(b.Days, maxInviteDays)) * 24 * time.Hour
	}
	maxUses := 1
	if b.MaxUses != nil && *b.MaxUses >= 0 {
		maxUses = *b.MaxUses
	}
	libs, _ := json.Marshal(b.Libraries)
	tok := randomHex(16)
	now := time.Now()
	expires := now.Add(ttl)
	var code string
	for range 5 {
		code = newInviteCode()
		_, err := s.db.W.ExecContext(r.Context(), `INSERT INTO invites(token, code, created_by, note, all_libraries, libraries, created_at, expires_at, max_uses)
			VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`, tok, code, currentUser(r).ID, b.Note, b.AllLibraries, string(libs), now.Unix(), expires.Unix(), maxUses)
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "UNIQUE") {
			writeErr(w, 500, err.Error())
			return
		}
		code = ""
	}
	if code == "" {
		writeErr(w, 500, "couldn't pick an invite code")
		return
	}
	out := map[string]any{"token": tok, "url": externalBase(r) + "/invite/" + tok, "code": formatCode(code)}
	// Publish the code so the app can find this server from it.
	addr := s.inviteAddress(r)
	if addr == "" {
		out["codeError"] = "The code needs this server's public address: set it under Sign in with Plex, or open Plexodus once from outside your home network. The link works meanwhile."
	} else if err := s.registerCode(r.Context(), code, addr, expires); err != nil {
		log.Printf("invites: couldn't publish code: %v", err)
		out["codeError"] = "Couldn't publish the code (" + err.Error() + "). The link works meanwhile."
	} else {
		out["address"] = addr
	}
	writeJSON(w, out)
}

func (s *Server) handleDeleteInvite(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("token")
	var code string
	s.db.R.QueryRowContext(r.Context(), `SELECT COALESCE(code, '') FROM invites WHERE token = ? AND used_by IS NULL`, key).Scan(&code)
	s.db.W.ExecContext(r.Context(), `DELETE FROM invites WHERE token = ? AND used_by IS NULL`, key)
	go s.revokeCode(code)
	writeJSON(w, map[string]bool{"ok": true})
}

// inviteToken finds the invite a link token or a typed code names.
func (s *Server) inviteToken(r *http.Request) string {
	key := r.PathValue("token")
	if code := normalizeCode(key); code != "" {
		var tok string
		if s.db.R.QueryRowContext(r.Context(), `SELECT token FROM invites WHERE code = ?`, code).Scan(&tok) == nil {
			return tok
		}
	}
	return key
}

// openInvite returns a usable invite: not expired and with uses left.
func (s *Server) openInvite(r *http.Request) (token, inviter string, all bool, libs []int64, ok bool) {
	var raw string
	token = s.inviteToken(r)
	err := s.db.R.QueryRowContext(r.Context(), `SELECT u.username, i.all_libraries, i.libraries FROM invites i
		JOIN users u ON u.id = i.created_by WHERE i.token = ? AND i.expires_at > ?
		AND (i.max_uses = 0 OR i.uses < i.max_uses) AND (i.max_uses <> 1 OR i.used_by IS NULL)`,
		token, db.Now()).Scan(&inviter, &all, &raw)
	if err != nil {
		return "", "", false, nil, false
	}
	json.Unmarshal([]byte(raw), &libs)
	return token, inviter, all, libs, true
}

const inviteGone = "this invite is invalid, expired or already used"

// handleGetInvite is public: it tells the invite page whether the link works.
func (s *Server) handleGetInvite(w http.ResponseWriter, r *http.Request) {
	if limiter.blocked(clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, try again in a few minutes")
		return
	}
	_, inviter, _, _, ok := s.openInvite(r)
	if !ok {
		limiter.fail(clientIP(r)) // slows guessing codes
		writeErr(w, 404, inviteGone)
		return
	}
	writeJSON(w, map[string]string{"inviter": inviter})
}

// handleAcceptInvite creates the account with the invite's library access and signs in.
func (s *Server) handleAcceptInvite(w http.ResponseWriter, r *http.Request) {
	if limiter.blocked(clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, try again in a few minutes")
		return
	}
	tok, _, all, libs, ok := s.openInvite(r)
	if !ok {
		limiter.fail(clientIP(r))
		writeErr(w, 404, inviteGone)
		return
	}
	var c credentials
	if err := readJSON(r, &c); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	// Claim a use first so two people can't share a single-use invite.
	res, err := s.db.W.ExecContext(r.Context(), `UPDATE invites SET used_at = ?, uses = uses + 1
		WHERE token = ? AND (max_uses = 0 OR uses < max_uses) AND (max_uses <> 1 OR used_by IS NULL)`, db.Now(), tok)
	if n, _ := res.RowsAffected(); err != nil || n == 0 {
		writeErr(w, 409, "this invite was just used")
		return
	}
	u, err := s.auth.CreateUser(r.Context(), c.Username, c.Password, false)
	if err != nil {
		s.db.W.ExecContext(r.Context(), `UPDATE invites SET uses = uses - 1 WHERE token = ?`, tok)
		writeErr(w, 400, err.Error())
		return
	}
	var code string
	var maxUses int
	s.db.R.QueryRowContext(r.Context(), `SELECT COALESCE(code, ''), max_uses FROM invites WHERE token = ?`, tok).Scan(&code, &maxUses)
	if maxUses == 1 {
		s.db.W.ExecContext(r.Context(), `UPDATE invites SET used_by = ? WHERE token = ?`, u.ID, tok)
		go s.revokeCode(code) // used up
	}
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
