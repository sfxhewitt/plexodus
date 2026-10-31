package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sfxhewitt/plexodus/internal/auth"
)

// Plexodus only does direct play: files are streamed byte-for-byte with
// range support. The browser decides whether it can decode them; anything
// it can't is handed to an external player via a signed URL.

const streamTTL = 7 * 24 * time.Hour

func (s *Server) sign(fileID, userID, exp int64) string {
	m := hmac.New(sha256.New, s.signKey)
	fmt.Fprintf(m, "%d:%d:%d", fileID, userID, exp)
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil)[:18])
}

func (s *Server) streamPath(fileID, userID int64, name string) string {
	exp := time.Now().Add(streamTTL).Unix()
	return fmt.Sprintf("/api/stream/%d/%s?u=%d&e=%d&s=%s", fileID, url.PathEscape(name), userID, exp, s.sign(fileID, userID, exp))
}

func externalBase(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = strings.Split(p, ",")[0]
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = strings.Split(h, ",")[0]
	}
	return scheme + "://" + host
}

var mimeTypes = map[string]string{
	".mp4": "video/mp4", ".m4v": "video/mp4", ".mov": "video/quicktime", ".webm": "video/webm",
	".mkv": "video/x-matroska", ".avi": "video/x-msvideo", ".ts": "video/mp2t", ".m2ts": "video/mp2t",
	".wmv": "video/x-ms-wmv", ".mpg": "video/mpeg", ".mpeg": "video/mpeg", ".flv": "video/x-flv", ".ogv": "video/ogg",
}

func (s *Server) handlePlay(w http.ResponseWriter, r *http.Request) {
	fileID, ok := pathID(r, "fileId")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var path string
	var itemID, libID int64
	var probed bool
	err := s.db.R.QueryRowContext(r.Context(), `SELECT path, item_id, library_id, probed_at IS NOT NULL FROM media_files WHERE id = ?`, fileID).
		Scan(&path, &itemID, &libID, &probed)
	if err != nil || !currentUser(r).CanSee(libID) {
		writeErr(w, 404, "file not found")
		return
	}
	if !probed {
		// The browser needs codec info to decide if it can play the file.
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		s.prober.ProbeNow(ctx, fileID, path)
		cancel()
	}

	var f MediaFile
	var streams string
	s.db.R.QueryRowContext(r.Context(), `SELECT id, size, COALESCE(container, ''), COALESCE(video_codec, ''), COALESCE(audio_codec, ''),
		COALESCE(width, 0), COALESCE(height, 0), COALESCE(duration, 0), COALESCE(bitrate, 0), COALESCE(streams, 'null'),
		probed_at IS NOT NULL, COALESCE(probe_error, '') FROM media_files WHERE id = ?`, fileID).
		Scan(&f.ID, &f.Size, &f.Container, &f.VideoCodec, &f.AudioCodec, &f.Width, &f.Height, &f.Duration, &f.Bitrate, &streams, &f.Probed, &f.ProbeError)
	if streams != "null" {
		f.Streams = []byte(streams)
	}

	var position float64
	s.db.R.QueryRowContext(r.Context(), `SELECT position FROM watch_state WHERE user_id = ? AND item_id = ? AND watched = 0`,
		currentUser(r).ID, itemID).Scan(&position)

	name := filepath.Base(path)
	rel := s.streamPath(fileID, currentUser(r).ID, name)
	writeJSON(w, map[string]any{
		"itemId":      itemID,
		"file":        f,
		"fileName":    name,
		"mime":        mimeTypes[strings.ToLower(filepath.Ext(path))],
		"streamUrl":   rel,
		"externalUrl": externalBase(r) + rel,
		"position":    position,
	})
}

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	fileID, ok := pathID(r, "id")
	q := r.URL.Query()
	uid, _ := strconv.ParseInt(q.Get("u"), 10, 64)
	exp, _ := strconv.ParseInt(q.Get("e"), 10, 64)
	if !ok || time.Now().Unix() > exp || !hmac.Equal([]byte(q.Get("s")), []byte(s.sign(fileID, uid, exp))) {
		http.Error(w, "invalid or expired link", http.StatusForbidden)
		return
	}
	var path string
	var libID int64
	if err := s.db.R.QueryRowContext(r.Context(), `SELECT path, library_id FROM media_files WHERE id = ?`, fileID).Scan(&path, &libID); err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Re-check access so revoking a library also stops links already handed out.
	u := auth.User{ID: uid}
	if err := s.db.R.QueryRowContext(r.Context(), `SELECT is_admin FROM users WHERE id = ?`, uid).Scan(&u.IsAdmin); err != nil {
		http.Error(w, "invalid or expired link", http.StatusForbidden)
		return
	}
	if auth.LoadAccess(r.Context(), s.db.R, &u) != nil || !u.CanSee(libID) {
		http.Error(w, "invalid or expired link", http.StatusForbidden)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "file unavailable: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	if mt := mimeTypes[strings.ToLower(filepath.Ext(path))]; mt != "" {
		w.Header().Set("Content-Type", mt)
	}
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("Content-Disposition", fmt.Sprintf("inline; filename*=UTF-8''%s", url.PathEscape(fi.Name())))
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}
