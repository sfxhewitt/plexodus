// Package api serves the JSON API, media streams, artwork and the web UI.
package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/config"
	"github.com/sfxhewitt/plexodus/internal/db"
	"github.com/sfxhewitt/plexodus/internal/metadata"
	"github.com/sfxhewitt/plexodus/internal/probe"
	"github.com/sfxhewitt/plexodus/internal/scanner"
)

type Server struct {
	cfg     *config.Config
	db      *db.DB
	auth    *auth.Auth
	scanner *scanner.Scanner
	agent   *metadata.Agent
	art     *metadata.ArtCache
	prober  *probe.Worker
	web     fs.FS
	signKey []byte
	baseCtx context.Context
}

func New(ctx context.Context, cfg *config.Config, d *db.DB, sc *scanner.Scanner, agent *metadata.Agent,
	art *metadata.ArtCache, prober *probe.Worker, web fs.FS) *Server {
	s := &Server{cfg: cfg, db: d, auth: auth.New(d), scanner: sc, agent: agent, art: art, prober: prober, web: web, baseCtx: ctx}
	key := d.Setting(ctx, "stream_sign_key")
	if key == "" {
		key = randomHex(32)
		d.SetSetting(ctx, "stream_sign_key", key)
	}
	s.signKey, _ = hex.DecodeString(key)
	if d.Setting(ctx, "api_key") == "" {
		d.SetSetting(ctx, "api_key", randomHex(16))
	}
	return s
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Public
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("POST /api/setup", s.handleSetup)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	mux.HandleFunc("GET /art/{size}/{file}", s.handleArt)
	// Signed URLs, so <video> and external players need no cookie.
	mux.HandleFunc("GET /api/stream/{id}/{name}", s.handleStream)
	mux.HandleFunc("HEAD /api/stream/{id}/{name}", s.handleStream)
	// Admin session or API key (for Sonarr/Radarr/autoscan hooks).
	mux.HandleFunc("POST /api/scan", s.handleScanPath)

	// Signed in
	user := func(p string, h http.HandlerFunc) { mux.Handle(p, s.requireUser(h)) }
	admin := func(p string, h http.HandlerFunc) { mux.Handle(p, s.requireAdmin(h)) }

	user("GET /api/me", s.handleMe)
	user("PUT /api/me/password", s.handleMyPassword)
	user("GET /api/home", s.handleHome)
	user("GET /api/libraries", s.handleLibraries)
	user("GET /api/libraries/{id}/items", s.handleLibraryItems)
	user("GET /api/libraries/{id}/genres", s.handleLibraryGenres)
	user("GET /api/items/{id}", s.handleItem)
	user("POST /api/items/{id}/progress", s.handleProgress)
	user("POST /api/items/{id}/watched", s.handleWatched)
	user("GET /api/play/{fileId}", s.handlePlay)
	user("GET /api/search", s.handleSearch)
	user("GET /api/scan/status", s.handleScanStatus)

	admin("POST /api/libraries", s.handleCreateLibrary)
	admin("PUT /api/libraries/{id}", s.handleUpdateLibrary)
	admin("DELETE /api/libraries/{id}", s.handleDeleteLibrary)
	admin("POST /api/libraries/{id}/scan", s.handleScanLibrary)
	admin("GET /api/items/{id}/match", s.handleMatchCandidates)
	admin("POST /api/items/{id}/match", s.handleMatch)
	admin("POST /api/items/{id}/refresh", s.handleRefresh)
	admin("GET /api/settings", s.handleGetSettings)
	admin("PUT /api/settings", s.handlePutSettings)
	admin("GET /api/users", s.handleUsers)
	admin("POST /api/users", s.handleCreateUser)
	admin("DELETE /api/users/{id}", s.handleDeleteUser)
	admin("PUT /api/users/{id}/password", s.handleUserPassword)
	admin("GET /api/fs", s.handleBrowseFS)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not found")
	})
	mux.Handle("/", s.webHandler())
	return logRequests(mux)
}

func (s *Server) webHandler() http.Handler {
	if s.cfg.DevProxy != "" {
		u, err := url.Parse(s.cfg.DevProxy)
		if err == nil {
			return httputil.NewSingleHostReverseProxy(u)
		}
	}
	files := http.FileServer(http.FS(s.web))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" {
			if _, err := fs.Stat(s.web, p); err == nil {
				if strings.HasPrefix(p, "assets/") {
					// Vite fingerprints asset names, so they never change.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		// SPA fallback: client-side routes all load index.html.
		w.Header().Set("Cache-Control", "no-cache")
		b, err := fs.ReadFile(s.web, "index.html")
		if err != nil {
			http.Error(w, "web UI not built: run `make web`", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(b)
	})
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(c int) { w.code = c; w.ResponseWriter.WriteHeader(c) }

func logRequests(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, code: 200}
		h.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") && os.Getenv("PLEXODUS_LOG_REQUESTS") != "" {
			log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.code, time.Since(start).Round(time.Microsecond))
		}
	})
}

func (s *Server) requireUser(h http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := s.auth.Lookup(r.Context(), auth.TokenFrom(r))
		if !ok {
			writeErr(w, http.StatusUnauthorized, "sign in required")
			return
		}
		h(w, r.WithContext(auth.WithUser(r.Context(), u)))
	})
}

func (s *Server) requireAdmin(h http.HandlerFunc) http.Handler {
	return s.requireUser(func(w http.ResponseWriter, r *http.Request) {
		if u, _ := auth.UserFrom(r.Context()); !u.IsAdmin {
			writeErr(w, http.StatusForbidden, "admin only")
			return
		}
		h(w, r)
	})
}

func currentUser(r *http.Request) auth.User {
	u, _ := auth.UserFrom(r.Context())
	return u
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return errors.New("invalid JSON body")
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}

func queryInt(r *http.Request, name string, def, max int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || v < 0 {
		return def
	}
	if max > 0 && v > max {
		return max
	}
	return v
}

// ---- auth handlers ----

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{
		"name":          "Plexodus",
		"setupRequired": !s.auth.HasUsers(r.Context()),
	})
}

func (s *Server) setCookie(w http.ResponseWriter, r *http.Request, tok string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: auth.CookieName, Value: tok, Path: "/", HttpOnly: true, MaxAge: maxAge,
		SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if s.auth.HasUsers(r.Context()) {
		writeErr(w, http.StatusConflict, "already set up")
		return
	}
	var c credentials
	if err := readJSON(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.auth.CreateUser(r.Context(), c.Username, c.Password, true); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.handleLoginWith(w, r, c)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if err := readJSON(r, &c); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.handleLoginWith(w, r, c)
}

func (s *Server) handleLoginWith(w http.ResponseWriter, r *http.Request, c credentials) {
	tok, u, err := s.auth.Login(r.Context(), c.Username, c.Password)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err.Error())
		return
	}
	s.setCookie(w, r, tok, 90*24*3600)
	writeJSON(w, u)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.Logout(r.Context(), auth.TokenFrom(r))
	s.setCookie(w, r, "", -1)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, currentUser(r))
}

func (s *Server) handleMyPassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	u := currentUser(r)
	if _, _, err := s.auth.Login(r.Context(), u.Username, body.Current); err != nil {
		writeErr(w, http.StatusForbidden, "current password is wrong")
		return
	}
	if err := s.auth.SetPassword(r.Context(), u.ID, body.New); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- artwork ----

func (s *Server) handleArt(w http.ResponseWriter, r *http.Request) {
	local, err := s.art.Get(r.Context(), r.PathValue("size"), "/"+r.PathValue("file"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, local)
}
