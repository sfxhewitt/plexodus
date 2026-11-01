package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/db"
	"github.com/sfxhewitt/plexodus/internal/scanner"
)

// ---- libraries ----

type libraryBody struct {
	Name          string   `json:"name"`
	Kind          string   `json:"kind"`
	Paths         []string `json:"paths"`
	TrustDirMtime bool     `json:"trustDirMtime"`
}

func (b *libraryBody) validate() error {
	b.Name = strings.TrimSpace(b.Name)
	if b.Name == "" {
		return errors.New("name is required")
	}
	if b.Kind != "movies" && b.Kind != "shows" {
		return errors.New("kind must be movies or shows")
	}
	var clean []string
	for _, p := range b.Paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			return errors.New("paths must be absolute: " + p)
		}
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			return errors.New("folder not found: " + p)
		}
		clean = append(clean, filepath.Clean(p))
	}
	if len(clean) == 0 {
		return errors.New("add at least one folder")
	}
	b.Paths = clean
	return nil
}

func (s *Server) handleCreateLibrary(w http.ResponseWriter, r *http.Request) {
	var b libraryBody
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := b.validate(); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	paths, _ := json.Marshal(b.Paths)
	res, err := s.db.W.ExecContext(r.Context(), `INSERT INTO libraries(name, kind, paths, trust_dir_mtime, created_at) VALUES(?, ?, ?, ?, ?)`,
		b.Name, b.Kind, string(paths), b.TrustDirMtime, db.Now())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	id, _ := res.LastInsertId()
	s.StartScan(id, scanner.Options{})
	writeJSON(w, map[string]int64{"id": id})
}

func (s *Server) handleUpdateLibrary(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r, "id")
	var b libraryBody
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := b.validate(); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	paths, _ := json.Marshal(b.Paths)
	if _, err := s.db.W.ExecContext(r.Context(), `UPDATE libraries SET name = ?, kind = ?, paths = ?, trust_dir_mtime = ? WHERE id = ?`,
		b.Name, b.Kind, string(paths), b.TrustDirMtime, id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.StartScan(id, scanner.Options{Full: true})
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteLibrary(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r, "id")
	if _, err := s.db.W.ExecContext(r.Context(), `DELETE FROM libraries WHERE id = ?`, id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// StartScan runs a scan in the background, ignoring "already running".
func (s *Server) StartScan(libID int64, opts scanner.Options) {
	go func() {
		ctx, cancel := context.WithTimeout(s.baseCtx, 12*time.Hour)
		defer cancel()
		s.scanner.Scan(ctx, libID, opts)
	}()
}

// handleScanAll scans every library, one at a time so the mount isn't
// flooded, in the background.
func (s *Server) handleScanAll(w http.ResponseWriter, r *http.Request) {
	full := r.URL.Query().Get("full") == "1"
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT id FROM libraries ORDER BY id`)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	go func() {
		for _, id := range ids {
			ctx, cancel := context.WithTimeout(s.baseCtx, 12*time.Hour)
			s.scanner.Scan(ctx, id, scanner.Options{Full: full})
			cancel()
		}
	}()
	writeJSON(w, map[string]any{"started": len(ids)})
}

func (s *Server) handleScanLibrary(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r, "id")
	s.StartScan(id, scanner.Options{Full: r.URL.Query().Get("full") == "1"})
	writeJSON(w, map[string]bool{"started": true})
}

func (s *Server) handleScanStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.scanner.Status())
}

// handleScanPath scans just the given path(s) in whichever libraries contain
// them. Authenticate with an admin session or the API key from settings
// (header X-Api-Key or ?apikey=), so Sonarr/Radarr/autoscan can call it.
func (s *Server) handleScanPath(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("X-Api-Key")
	if key == "" {
		key = r.URL.Query().Get("apikey")
	}
	want := s.db.Setting(r.Context(), "api_key")
	authed := key != "" && subtle.ConstantTimeCompare([]byte(key), []byte(want)) == 1
	if !authed {
		u, ok := s.auth.Lookup(r.Context(), auth.TokenFrom(r))
		authed = ok && u.IsAdmin
	}
	if !authed {
		writeErr(w, 401, "admin session or API key required")
		return
	}

	paths := r.URL.Query()["path"]
	if r.Header.Get("Content-Type") == "application/json" {
		var body struct {
			Path  string   `json:"path"`
			Paths []string `json:"paths"`
		}
		readJSON(r, &body)
		if body.Path != "" {
			paths = append(paths, body.Path)
		}
		paths = append(paths, body.Paths...)
	}
	if len(paths) == 0 {
		writeErr(w, 400, "path is required")
		return
	}

	rows, err := s.db.R.QueryContext(r.Context(), `SELECT id FROM libraries`)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()

	started := []int64{}
	for _, id := range ids {
		lib, err := scanner.LoadLibrary(r.Context(), s.db.R, id)
		if err != nil {
			continue
		}
		var mine []string
		for _, p := range paths {
			p = filepath.Clean(p)
			for _, root := range lib.Roots {
				if p == root || strings.HasPrefix(p, root+string(filepath.Separator)) {
					mine = append(mine, p)
					break
				}
			}
		}
		if len(mine) > 0 {
			s.StartScan(id, scanner.Options{Paths: mine})
			started = append(started, id)
		}
	}
	writeJSON(w, map[string]any{"libraries": started})
}

// ---- matching ----

func (s *Server) handleMatchCandidates(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r, "id")
	var kind, title string
	var year int
	if err := s.db.R.QueryRowContext(r.Context(), `SELECT kind, title, COALESCE(year, 0) FROM items WHERE id = ?`, id).
		Scan(&kind, &title, &year); err != nil {
		writeErr(w, 404, "not found")
		return
	}
	if q := r.URL.Query().Get("q"); q != "" {
		title, year = q, queryInt(r, "year", 0, 3000)
	}
	t := s.agent.TMDB()
	if !t.Enabled() {
		writeErr(w, 400, "add a TMDB API key in Settings first")
		return
	}
	tk := "movie"
	if kind == "show" {
		tk = "tv"
	}
	res, err := t.Search(r.Context(), tk, title, year)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	type cand struct {
		ID       int64  `json:"tmdbId"`
		Title    string `json:"title"`
		Year     string `json:"year"`
		Poster   string `json:"poster"`
		Overview string `json:"overview"`
	}
	out := []cand{}
	for _, c := range res {
		out = append(out, cand{c.ID, c.DisplayTitle(), c.Year(), c.PosterPath, c.Overview})
	}
	writeJSON(w, out)
}

func (s *Server) handleMatch(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r, "id")
	var body struct {
		TmdbID int64 `json:"tmdbId"`
	}
	if err := readJSON(r, &body); err != nil || body.TmdbID == 0 {
		writeErr(w, 400, "tmdbId is required")
		return
	}
	var kind string
	s.db.R.QueryRowContext(r.Context(), `SELECT kind FROM items WHERE id = ?`, id).Scan(&kind)
	if err := s.agent.Apply(r.Context(), id, kind, body.TmdbID); err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r, "id")
	var kind string
	var tmdb int64
	s.db.R.QueryRowContext(r.Context(), `SELECT kind, COALESCE(tmdb_id, 0) FROM items WHERE id = ?`, id).Scan(&kind, &tmdb)
	if tmdb != 0 {
		if err := s.agent.Apply(r.Context(), id, kind, tmdb); err != nil {
			writeErr(w, 502, err.Error())
			return
		}
	} else {
		s.db.W.ExecContext(r.Context(), `UPDATE items SET meta_state = 'pending' WHERE id = ?`, id)
		s.agent.Kick()
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// handleRefreshLibrary re-fetches metadata for everything in a library:
// matched titles are re-applied from TMDB one at a time in the background,
// and anything unmatched is queued for matching again.
func (s *Server) handleRefreshLibrary(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r, "id")
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT id, kind, COALESCE(tmdb_id, 0) FROM items
		WHERE library_id = ? AND kind IN ('movie', 'show') ORDER BY added_at DESC`, id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	type it struct {
		id   int64
		kind string
		tmdb int64
	}
	var matched []it
	var pending []int64
	for rows.Next() {
		var x it
		rows.Scan(&x.id, &x.kind, &x.tmdb)
		if x.tmdb != 0 {
			matched = append(matched, x)
		} else {
			pending = append(pending, x.id)
		}
	}
	rows.Close()
	for _, pid := range pending {
		s.db.W.ExecContext(r.Context(), `UPDATE items SET meta_state = 'pending' WHERE id = ?`, pid)
	}
	s.agent.Kick()
	go func() {
		for _, x := range matched {
			if s.baseCtx.Err() != nil {
				return
			}
			ctx, cancel := context.WithTimeout(s.baseCtx, time.Minute)
			s.agent.Apply(ctx, x.id, x.kind, x.tmdb)
			cancel()
			time.Sleep(150 * time.Millisecond) // stay well inside TMDB's rate limit
		}
	}()
	writeJSON(w, map[string]any{"queued": len(matched) + len(pending)})
}

// ---- settings ----

func maskKey(k string) string {
	if len(k) <= 8 {
		return strings.Repeat("•", len(k))
	}
	return k[:4] + strings.Repeat("•", 8) + k[len(k)-4:]
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	tmdb := s.db.Setting(r.Context(), "tmdb_key")
	envKey := s.cfg.TMDBKey != "" && tmdb == ""
	if envKey {
		tmdb = s.cfg.TMDBKey
	}
	writeJSON(w, map[string]any{
		"tmdbKeySet":     tmdb != "",
		"tmdbKeyMasked":  maskKey(tmdb),
		"tmdbKeyFromEnv": envKey,
		"apiKey":         s.db.Setting(r.Context(), "api_key"),
		"probeWorkers":   s.cfg.ProbeWorkers,
		"scanInterval":   s.cfg.ScanInterval.String(),
	})
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TmdbKey     *string `json:"tmdbKey"`
		RegenApiKey bool    `json:"regenerateApiKey"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if body.TmdbKey != nil {
		k := strings.TrimSpace(*body.TmdbKey)
		s.db.SetSetting(r.Context(), "tmdb_key", k)
		if k == "" {
			k = s.cfg.TMDBKey
		}
		s.agent.SetKey(k)
		// Retry items that failed while no key was configured.
		s.db.W.ExecContext(r.Context(), `UPDATE items SET meta_state = 'pending' WHERE meta_state IN ('error', 'unmatched') AND kind IN ('movie', 'show')`)
	}
	if body.RegenApiKey {
		s.db.SetSetting(r.Context(), "api_key", randomHex(16))
	}
	s.handleGetSettings(w, r)
}

// ---- users ----

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT id, username, is_admin FROM users ORDER BY username`)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []auth.User{}
	for rows.Next() {
		var u auth.User
		rows.Scan(&u.ID, &u.Username, &u.IsAdmin)
		out = append(out, u)
	}
	rows.Close()
	for i := range out {
		auth.LoadAccess(r.Context(), s.db.R, &out[i])
	}
	writeJSON(w, out)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		credentials
		IsAdmin bool `json:"isAdmin"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	u, err := s.auth.CreateUser(r.Context(), body.Username, body.Password, body.IsAdmin)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, u)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r, "id")
	if id == currentUser(r).ID {
		writeErr(w, 400, "you can't delete yourself")
		return
	}
	s.db.W.ExecContext(r.Context(), `DELETE FROM users WHERE id = ?`, id)
	s.auth.Forget()
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleUserPassword(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r, "id")
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if err := s.auth.SetPassword(r.Context(), id, body.Password); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// handleBrowseFS lists subfolders so admins can pick library folders.
func (s *Server) handleBrowseFS(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		p = "/"
	}
	p = filepath.Clean(p)
	entries, err := os.ReadDir(p)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	dirs := []string{}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Strings(dirs)
	parent := filepath.Dir(p)
	if parent == p {
		parent = ""
	}
	writeJSON(w, map[string]any{"path": p, "parent": parent, "dirs": dirs})
}
