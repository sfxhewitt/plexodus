package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sfxhewitt/plexodus/internal/probe"
	"github.com/sfxhewitt/plexodus/internal/stream"
)

// Browser fallback: when a browser can't play a file as it is (Safari and an
// MKV, say), the web player asks for an HLS remux. Video is always copied;
// audio is copied unless the browser can't decode it, in which case it
// becomes AAC. See internal/stream.

func (s *Server) handleHLSStart(w http.ResponseWriter, r *http.Request) {
	var b struct {
		FileID     int64   `json:"fileId"`
		Start      float64 `json:"start"`
		AudioIndex *int    `json:"audioIndex"`
		AudioMode  string  `json:"audioMode"`
	}
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	if !s.streams.Available() {
		writeErr(w, 501, "ffmpeg is not installed on the server, so this file can't be converted for the browser")
		return
	}
	var path, vcodec, streams string
	var libID int64
	err := s.db.R.QueryRowContext(r.Context(), `SELECT path, library_id, COALESCE(video_codec, ''), COALESCE(streams, 'null')
		FROM media_files WHERE id = ?`, b.FileID).Scan(&path, &libID, &vcodec, &streams)
	if err != nil || !currentUser(r).CanSee(libID) {
		writeErr(w, 404, "file not found")
		return
	}
	audio := -1
	if b.AudioIndex != nil {
		audio = *b.AudioIndex
	} else {
		var ss []probe.Stream
		json.Unmarshal([]byte(streams), &ss)
		for _, st := range ss {
			if st.Type == "audio" && (audio < 0 || st.Default) {
				audio = st.Index
			}
		}
	}
	sess, err := s.streams.Start(r.Context(), currentUser(r).ID, b.FileID, stream.Options{
		Path: path, Start: b.Start, AudioIndex: audio, AudioMode: b.AudioMode, VideoCodec: vcodec,
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"id": sess.ID, "url": "/api/hls/" + sess.ID + "/index.m3u8", "start": sess.Opts.Start})
}

func (s *Server) handleHLSPing(w http.ResponseWriter, r *http.Request) {
	s.streams.Get(r.PathValue("sid"), currentUser(r).ID)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleHLSStop(w http.ResponseWriter, r *http.Request) {
	s.streams.Stop(r.PathValue("sid"), currentUser(r).ID)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleHLSFile(w http.ResponseWriter, r *http.Request) {
	sess := s.streams.Get(r.PathValue("sid"), currentUser(r).ID)
	if sess == nil {
		http.Error(w, "stream ended", http.StatusNotFound)
		return
	}
	name := r.PathValue("name")
	// The first playlist and the first segments can take a while on cold
	// network storage; later segments arrive well ahead of the player.
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	p, err := sess.File(ctx, name, 90*time.Second)
	if err != nil {
		switch {
		case errors.Is(err, os.ErrNotExist):
			http.Error(w, "not found", http.StatusNotFound)
		case errors.Is(err, context.DeadlineExceeded):
			http.Error(w, "segment not ready", http.StatusGatewayTimeout)
		default:
			http.Error(w, "conversion failed: "+err.Error(), http.StatusBadGateway)
		}
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch {
	case strings.HasSuffix(name, ".m3u8"):
		b, err := sess.Playlist(p)
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Write(b)
	case name == "init.mp4":
		w.Header().Set("Content-Type", "video/mp4")
		http.ServeFile(w, r, p)
	default:
		w.Header().Set("Content-Type", "video/iso.segment")
		http.ServeFile(w, r, p)
	}
}
