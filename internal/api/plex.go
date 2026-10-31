package api

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sfxhewitt/plexodus/internal/plex"
)

// Plex connection settings live in the settings table, falling back to the
// environment, so the token never has to pass through a chat or the UI.
func (s *Server) plexSettings(ctx context.Context) (url, token, pathMap string) {
	url = s.db.Setting(ctx, "plex_url")
	if url == "" {
		url = s.cfg.PlexURL
	}
	token = s.db.Setting(ctx, "plex_token")
	if token == "" {
		token = s.cfg.PlexToken
	}
	pathMap = s.db.Setting(ctx, "plex_path_map")
	if pathMap == "" {
		pathMap = s.cfg.PlexPathMap
	}
	return
}

func (s *Server) plexClient(ctx context.Context) (*plex.Client, plex.PathMap) {
	url, token, pm := s.plexSettings(ctx)
	return plex.NewClient(url, token), plex.ParsePathMap(pm)
}

func (s *Server) handlePlexGet(w http.ResponseWriter, r *http.Request) {
	url, token, pm := s.plexSettings(r.Context())
	writeJSON(w, map[string]any{
		"url":          url,
		"tokenSet":     token != "",
		"tokenFromEnv": s.cfg.PlexToken != "" && s.db.Setting(r.Context(), "plex_token") == "",
		"pathMap":      pm,
		"status":       s.importer.Status(),
	})
}

func (s *Server) handlePlexPut(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL     *string `json:"url"`
		Token   *string `json:"token"`
		PathMap *string `json:"pathMap"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if body.URL != nil {
		s.db.SetSetting(r.Context(), "plex_url", strings.TrimSpace(*body.URL))
	}
	if body.Token != nil && strings.TrimSpace(*body.Token) != "" {
		s.db.SetSetting(r.Context(), "plex_token", strings.TrimSpace(*body.Token))
	}
	if body.PathMap != nil {
		s.db.SetSetting(r.Context(), "plex_path_map", strings.TrimSpace(*body.PathMap))
	}
	s.handlePlexGet(w, r)
}

// handlePlexTest lists the Plex libraries and whether their folders are
// visible at the (mapped) paths on this machine.
func (s *Server) handlePlexTest(w http.ResponseWriter, r *http.Request) {
	c, pm := s.plexClient(r.Context())
	if !c.Configured() {
		writeErr(w, 400, "set the Plex URL and token first")
		return
	}
	sections, err := c.Sections(r.Context())
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	type loc struct {
		Plex   string `json:"plex"`
		Local  string `json:"local"`
		Exists bool   `json:"exists"`
	}
	type sec struct {
		Title     string `json:"title"`
		Type      string `json:"type"`
		Locations []loc  `json:"locations"`
	}
	out := []sec{}
	for _, x := range sections {
		if x.Type != "movie" && x.Type != "show" {
			continue
		}
		sc := sec{Title: x.Title, Type: x.Type, Locations: []loc{}}
		for _, l := range x.Location {
			local := pm.Apply(l.Path)
			fi, err := os.Stat(local)
			sc.Locations = append(sc.Locations, loc{l.Path, local, err == nil && fi.IsDir()})
		}
		out = append(out, sc)
	}
	writeJSON(w, out)
}

func (s *Server) handlePlexImport(w http.ResponseWriter, r *http.Request) {
	c, pm := s.plexClient(r.Context())
	if !c.Configured() {
		writeErr(w, 400, "set the Plex URL and token first")
		return
	}
	err := s.importer.Start(s.baseCtx, c, pm, currentUser(r).ID, func() {
		select {
		case s.scanner.Changed <- struct{}{}:
		default:
		}
	})
	if err != nil {
		writeErr(w, 409, err.Error())
		return
	}
	writeJSON(w, s.importer.Status())
}

var plexArtSizes = map[string][2]int{
	"w185": {185, 278}, "w342": {342, 513}, "w500": {500, 750}, "w780": {780, 780}, "w1280": {1280, 1280}, "original": {1920, 1920},
}

// handlePlexArt serves artwork from the Plex server, resized by Plex and
// cached on local disk.
func (s *Server) handlePlexArt(w http.ResponseWriter, r *http.Request) {
	size := r.PathValue("size")
	dims, ok := plexArtSizes[size]
	p := r.URL.Query().Get("p")
	if !ok || !strings.HasPrefix(p, "/library/") {
		http.Error(w, "bad image", http.StatusBadRequest)
		return
	}
	sum := sha1.Sum([]byte(p))
	h := hex.EncodeToString(sum[:])
	local := filepath.Join(s.cfg.ArtworkDir, "plex", size, h[:2], h+".jpg")
	if _, err := os.Stat(local); err != nil {
		c, _ := s.plexClient(r.Context())
		if !c.Configured() {
			http.Error(w, "plex not configured", http.StatusNotFound)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		resp, err := c.Image(ctx, p, dims[0], dims[1])
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			http.Error(w, resp.Status, http.StatusNotFound)
			return
		}
		os.MkdirAll(filepath.Dir(local), 0o755)
		tmp, err := os.CreateTemp(filepath.Dir(local), ".dl-*")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		_, err = io.Copy(tmp, io.LimitReader(resp.Body, 32<<20))
		tmp.Close()
		if err != nil {
			os.Remove(tmp.Name())
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		os.Rename(tmp.Name(), local)
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeFile(w, r, local)
}

type Marker struct {
	Type  string  `json:"type"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// handleMarkers returns intro/credits markers, fetching them from Plex the
// first time an imported item is played if the bulk import didn't include them.
func (s *Server) handleMarkers(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r, "id")
	if !s.itemVisible(r, id) {
		writeErr(w, 404, "not found")
		return
	}
	var key string
	var fetched sql.NullInt64
	err := s.db.R.QueryRowContext(r.Context(), `SELECT rating_key, markers_at FROM plex_links WHERE item_id = ?`, id).Scan(&key, &fetched)
	if err == nil && !fetched.Valid {
		if c, _ := s.plexClient(r.Context()); c.Configured() {
			ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			m, err := c.Item(ctx, key)
			cancel()
			if err == nil {
				s.db.Tx(r.Context(), func(tx *sql.Tx) error {
					if _, err := plex.SaveMarkers(tx, id, m.Marker); err != nil {
						return err
					}
					_, err := tx.Exec(`UPDATE plex_links SET markers_at = ? WHERE item_id = ?`, time.Now().Unix(), id)
					return err
				})
			}
		}
	}
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT type, start, "end" FROM markers WHERE item_id = ? ORDER BY start`, id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := []Marker{}
	for rows.Next() {
		var m Marker
		rows.Scan(&m.Type, &m.Start, &m.End)
		out = append(out, m)
	}
	writeJSON(w, out)
}
