package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sfxhewitt/plexodus/internal/plex"
)

// Sign in with Plex.
//
// Anyone the server's Plex is shared with can sign in with their plex.tv
// account: plex.tv says which servers their token can reach, and if this
// server's Plex is one of them they get a Plexodus account that sees the
// same libraries the owner shares with them in Plex. The Plex owner's
// account is linked to the first admin.
//
// So apps can find the server without an address, it adds its own public
// address to its Plex server's "Custom server access URLs". plex.tv hands
// those to everyone the Plex server is shared with (and to the owner), and
// the app checks each one for a Plexodus server.

// errPlexSignIn is shown to the person signing in as is.
type errPlexSignIn struct{ msg string }

func (e errPlexSignIn) Error() string { return e.msg }

func (s *Server) plexAccount(ctx context.Context) plex.Account {
	id := s.db.Setting(ctx, "plex_client_id")
	if id == "" {
		id = "plexodus-" + randomHex(12)
		s.db.SetSetting(ctx, "plex_client_id", id)
	}
	return plex.Account{ClientID: id}
}

// plexSignInEnabled is cheap (no network), for /api/status.
func (s *Server) plexSignInEnabled(ctx context.Context) bool {
	if s.db.Setting(ctx, "plex_signin") == "off" {
		return false
	}
	c, _ := s.plexClient(ctx)
	return c.Configured()
}

// plexMachineID is this server's Plex identity, remembered for an hour.
func (s *Server) plexMachineID(ctx context.Context) (string, error) {
	plexIDMu.Lock()
	defer plexIDMu.Unlock()
	if plexIDCache.id != "" && time.Since(plexIDCache.at) < time.Hour {
		return plexIDCache.id, nil
	}
	c, _ := s.plexClient(ctx)
	if !c.Configured() {
		return "", errors.New("plex isn't set up")
	}
	id, err := c.Identity(ctx)
	if err != nil {
		if saved := s.db.Setting(ctx, "plex_machine_id"); saved != "" {
			return saved, nil
		}
		return "", err
	}
	plexIDCache.id, plexIDCache.at = id, time.Now()
	s.db.SetSetting(ctx, "plex_machine_id", id)
	return id, nil
}

var (
	plexIDMu    sync.Mutex
	plexIDCache struct {
		id string
		at time.Time
	}
)

// plexSignIn checks a plex.tv token and returns the Plexodus account it
// may use, creating one for a new friend.
func (s *Server) plexSignIn(ctx context.Context, token string) (int64, error) {
	if !s.plexSignInEnabled(ctx) {
		return 0, errPlexSignIn{"Sign in with Plex is turned off on this server."}
	}
	acct := s.plexAccount(ctx)
	me, err := acct.Me(ctx, token)
	if err != nil {
		if errors.Is(err, plex.ErrBadToken) {
			return 0, errPlexSignIn{"Plex didn't accept that sign-in. Try again."}
		}
		return 0, err
	}
	machine, err := s.plexMachineID(ctx)
	if err != nil {
		return 0, err
	}
	resources, err := acct.Resources(ctx, token)
	if err != nil {
		return 0, err
	}
	var res *plex.Resource
	for i := range resources {
		if resources[i].ClientIdentifier == machine {
			res = &resources[i]
		}
	}
	if res == nil {
		return 0, errPlexSignIn{"Your Plex account (" + me.Name() + ") doesn't have access to this server. Ask the owner to share their Plex server with you."}
	}

	if res.Owned {
		// The Plex owner: that's whoever set this server up.
		if a, ok := s.auth.ByPlex(ctx, me.ID); ok && a.IsAdmin {
			return a.UserID, nil
		}
		admin, err := s.auth.FirstAdmin(ctx)
		if err != nil {
			return 0, err
		}
		if err := s.auth.LinkPlex(ctx, admin, me.ID, me.Name()); err != nil {
			return 0, err
		}
		log.Printf("plex sign-in: linked Plex owner %s to the admin account", me.Name())
		return admin, nil
	}

	a, ok := s.auth.ByPlex(ctx, me.ID)
	if ok && a.IsAdmin {
		return a.UserID, nil
	}
	shares, shareErr := s.plexShares(ctx)
	share, shared := findShare(shares, me.ID, me.Username)
	if shareErr == nil && !shared {
		return 0, errPlexSignIn{"Your Plex account (" + me.Name() + ") doesn't have any libraries shared from this server."}
	}
	id := a.UserID
	if !ok {
		if id, err = s.auth.CreatePlexUser(ctx, me.Name(), me.ID); err != nil {
			return 0, err
		}
		log.Printf("plex sign-in: created an account for Plex friend %s", me.Name())
	}
	if s.auth.AccessCustom(ctx, id) {
		// An admin chose this friend's libraries in Plexodus.
		return id, nil
	}
	if shareErr != nil {
		// plex.tv confirmed they can reach the server, but not which
		// libraries: let them see everything rather than an empty app.
		log.Printf("plex sign-in: couldn't read Plex sharing (%v); giving %s all libraries", shareErr, me.Name())
		return id, s.auth.SetAccess(ctx, id, true, nil)
	}
	return id, s.applyShare(ctx, id, share)
}

// findShare looks a Plex friend up by plex.tv id, else by username.
func findShare(shares map[int64]plex.Share, id int64, username string) (plex.Share, bool) {
	if sh, ok := shares[id]; ok {
		return sh, true
	}
	if username != "" {
		for _, sh := range shares {
			if strings.EqualFold(sh.Username, username) {
				return sh, true
			}
		}
	}
	return plex.Share{}, false
}

// plexShares is who the Plex owner shares the server with, cached briefly.
func (s *Server) plexShares(ctx context.Context) (map[int64]plex.Share, error) {
	sharesMu.Lock()
	defer sharesMu.Unlock()
	if sharesCache.m != nil && time.Since(sharesCache.at) < 2*time.Minute {
		return sharesCache.m, nil
	}
	machine, err := s.plexMachineID(ctx)
	if err != nil {
		return nil, err
	}
	_, token, _ := s.plexSettings(ctx)
	m, err := s.plexAccount(ctx).Shares(ctx, token, machine)
	if err != nil {
		return nil, err
	}
	sharesCache.m, sharesCache.at = m, time.Now()
	return m, nil
}

var (
	sharesMu    sync.Mutex
	sharesCache struct {
		m  map[int64]plex.Share
		at time.Time
	}
)

// applyShare gives a Plex friend the Plexodus libraries matching the Plex
// libraries shared with them (by folder, else by name).
func (s *Server) applyShare(ctx context.Context, userID int64, sh plex.Share) error {
	if sh.AllLibraries {
		return s.auth.SetAccess(ctx, userID, true, nil)
	}
	libs, err := s.plexLibraryMap(ctx)
	if err != nil {
		return err
	}
	ids := []int64{}
	seen := map[int64]bool{}
	for _, key := range sh.Sections {
		for _, id := range libs[key] {
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return s.auth.SetAccess(ctx, userID, false, ids)
}

// plexLibraryMap maps Plex section keys to the Plexodus libraries holding
// the same folders (or, failing that, with the same name).
func (s *Server) plexLibraryMap(ctx context.Context) (map[string][]int64, error) {
	c, pm := s.plexClient(ctx)
	sections, err := c.Sections(ctx)
	if err != nil {
		return nil, err
	}
	type lib struct {
		id    int64
		name  string
		paths []string
	}
	var libs []lib
	rows, err := s.db.R.QueryContext(ctx, `SELECT id, name, paths FROM libraries`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var l lib
		var raw string
		rows.Scan(&l.id, &l.name, &raw)
		json.Unmarshal([]byte(raw), &l.paths)
		libs = append(libs, l)
	}
	rows.Close()
	out := map[string][]int64{}
	for _, sec := range sections {
		var match []int64
		for _, l := range libs {
			hit := false
			for _, loc := range sec.Location {
				local := filepath.Clean(pm.Apply(loc.Path))
				for _, p := range l.paths {
					if filepath.Clean(p) == local {
						hit = true
					}
				}
			}
			if hit {
				match = append(match, l.id)
			}
		}
		if len(match) == 0 {
			for _, l := range libs {
				if strings.EqualFold(strings.TrimSpace(l.name), strings.TrimSpace(sec.Title)) {
					match = append(match, l.id)
				}
			}
		}
		out[sec.Key] = match
	}
	return out, nil
}

// ---- handlers ----

type pendingPin struct {
	created time.Time
	strong  bool
}

var (
	pinsMu sync.Mutex
	pins   = map[int64]pendingPin{}
)

func (s *Server) handlePlexPin(w http.ResponseWriter, r *http.Request) {
	if !s.plexSignInEnabled(r.Context()) {
		writeErr(w, 404, "Sign in with Plex is turned off on this server")
		return
	}
	var body struct {
		Strong bool `json:"strong"`
	}
	readJSON(r, &body)
	pinsMu.Lock()
	for id, p := range pins {
		if time.Since(p.created) > 30*time.Minute {
			delete(pins, id)
		}
	}
	busy := len(pins) > 500
	pinsMu.Unlock()
	if busy {
		writeErr(w, 429, "too many sign-ins at once, try again in a few minutes")
		return
	}
	acct := s.plexAccount(r.Context())
	p, err := acct.NewPin(r.Context(), body.Strong)
	if err != nil {
		writeErr(w, 502, "couldn't reach plex.tv: "+err.Error())
		return
	}
	pinsMu.Lock()
	pins[p.ID] = pendingPin{created: time.Now(), strong: body.Strong}
	pinsMu.Unlock()
	writeJSON(w, map[string]any{"id": p.ID, "code": p.Code, "authUrl": acct.AuthURL(p.Code), "expiresIn": p.ExpiresIn})
}

// handlePlexPinCheck is polled by the sign-in screen until the PIN is
// approved, then signs in.
func (s *Server) handlePlexPinCheck(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	pinsMu.Lock()
	_, ok := pins[id]
	pinsMu.Unlock()
	if err != nil || !ok {
		writeErr(w, 404, "this sign-in code has expired, start again")
		return
	}
	p, err := s.plexAccount(r.Context()).CheckPin(r.Context(), id)
	if err != nil {
		writeErr(w, 502, "couldn't reach plex.tv: "+err.Error())
		return
	}
	if p.AuthToken == "" {
		writeJSON(w, map[string]bool{"pending": true})
		return
	}
	pinsMu.Lock()
	delete(pins, id)
	pinsMu.Unlock()
	s.finishPlexSignIn(w, r, p.AuthToken)
}

// handlePlexToken signs in with a plex.tv token the app got itself (it
// signs in to Plex before it knows which server to use).
func (s *Server) handlePlexToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Token) == "" {
		writeErr(w, 400, "token is required")
		return
	}
	if limiter.blocked(clientIP(r)) {
		writeErr(w, http.StatusTooManyRequests, "too many attempts, try again in a few minutes")
		return
	}
	s.finishPlexSignIn(w, r, strings.TrimSpace(body.Token))
}

func (s *Server) finishPlexSignIn(w http.ResponseWriter, r *http.Request, token string) {
	id, err := s.plexSignIn(r.Context(), token)
	if err != nil {
		var friendly errPlexSignIn
		if errors.As(err, &friendly) {
			limiter.fail(clientIP(r))
			writeErr(w, 403, friendly.msg)
			return
		}
		log.Printf("plex sign-in: %v", err)
		writeErr(w, 502, "couldn't check your Plex account: "+err.Error())
		return
	}
	tok, u, err := s.auth.SignIn(r.Context(), id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.setCookie(w, r, tok, 90*24*3600)
	writeJSON(w, u)
}

// ---- keeping friends' access in step with Plex ----

// RunPlexFriends re-checks Plex sharing hourly: friends get the libraries
// currently shared with them, and lose access when the share is removed.
// It also keeps this server's address published in Plex.
func (s *Server) RunPlexFriends(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(45 * time.Second):
	}
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		s.publishToPlex(ctx)
		s.refreshPlexFriends(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-publishKick:
		}
	}
}

var publishKick = make(chan struct{}, 1)

func kickPublish() {
	select {
	case publishKick <- struct{}{}:
	default:
	}
}

func (s *Server) refreshPlexFriends(ctx context.Context) {
	friends, err := s.auth.PlexFriends(ctx)
	if err != nil || len(friends) == 0 {
		return
	}
	if c, _ := s.plexClient(ctx); !c.Configured() {
		return
	}
	shares, err := s.plexShares(ctx)
	if err != nil {
		log.Printf("plex friends: couldn't read Plex sharing: %v", err)
		return
	}
	for userID, f := range friends {
		sh, ok := findShare(shares, f.PlexID, "")
		if !ok {
			if err := s.auth.SetAccess(ctx, userID, false, nil); err == nil {
				s.auth.EndSessions(ctx, userID)
				log.Printf("plex friends: user %d is no longer shared in Plex; signed out", userID)
			}
			continue
		}
		if f.Custom {
			continue // libraries chosen in Plexodus
		}
		if err := s.applyShare(ctx, userID, sh); err != nil {
			log.Printf("plex friends: user %d: %v", userID, err)
		}
	}
}

// ---- publishing the address in Plex ----

type publishState struct {
	URL   string    `json:"url"`
	OK    bool      `json:"ok"`
	Error string    `json:"error,omitempty"`
	At    time.Time `json:"at"`
}

var (
	publishMu   sync.Mutex
	lastPublish publishState
)

// publicURL is the address friends use: set in Settings, else the one an
// admin last opened the server at from outside the local network.
func (s *Server) publicURL(ctx context.Context) string {
	if u := s.db.Setting(ctx, "public_url"); u != "" {
		return u
	}
	return s.db.Setting(ctx, "public_url_seen")
}

// notePublicURL remembers the address an admin uses, when it's a public one.
func (s *Server) notePublicURL(r *http.Request) {
	if s.db.Setting(r.Context(), "public_url") != "" {
		return
	}
	u := requestOrigin(r)
	if u == "" || u == s.db.Setting(r.Context(), "public_url_seen") {
		return
	}
	s.db.SetSetting(r.Context(), "public_url_seen", u)
	kickPublish()
}

// requestOrigin is the scheme://host the request came in on, or "" when
// that host only works locally.
func requestOrigin(r *http.Request) string {
	h := r.Host
	if h == "" {
		return ""
	}
	host := h
	if hh, _, err := net.SplitHostPort(h); err == nil {
		host = hh
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") || strings.HasSuffix(strings.ToLower(host), ".local") || !strings.Contains(host, ".") && net.ParseIP(host) == nil {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()) {
		return ""
	}
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + h
}

// publishToPlex adds this server's address to its Plex server's custom
// connections (or takes it back out when publishing is off).
func (s *Server) publishToPlex(ctx context.Context) {
	c, _ := s.plexClient(ctx)
	if !c.Configured() {
		return
	}
	want := ""
	if s.db.Setting(ctx, "plex_publish") != "off" && s.plexSignInEnabled(ctx) {
		want = strings.TrimRight(s.publicURL(ctx), "/")
	}
	had := s.db.Setting(ctx, "plex_published_url")
	if want == "" && had == "" {
		return
	}
	st := publishState{URL: want, At: time.Now()}
	defer func() {
		publishMu.Lock()
		lastPublish = st
		publishMu.Unlock()
	}()
	cur, err := c.Pref(ctx, "customConnections")
	if err != nil {
		st.Error = err.Error()
		log.Printf("plex publish: %v", err)
		return
	}
	next := cur
	if had != "" && had != want {
		next = plex.WithConnection(next, had, false)
	}
	if want != "" {
		next = plex.WithConnection(next, want, true)
	}
	if next != cur {
		if err := c.SetPref(ctx, "customConnections", next); err != nil {
			st.Error = err.Error()
			log.Printf("plex publish: %v", err)
			return
		}
		log.Printf("plex publish: Plex custom server access URLs now %q", next)
	}
	s.db.SetSetting(ctx, "plex_published_url", want)
	st.OK = want != ""
}

func publishStatus() publishState {
	publishMu.Lock()
	defer publishMu.Unlock()
	return lastPublish
}

// handlePlexIdentity: Plex apps may try this server's address as one of
// the Plex server's connections; make sure they move on quickly.
func handlePlexIdentity(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "this is Plexodus, not Plex", http.StatusNotFound)
}
