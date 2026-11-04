package api

import (
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sfxhewitt/plexodus/internal/probe"
	"github.com/sfxhewitt/plexodus/internal/subs"
)

// Subtitles for the browser player. Tracks are listed from the probe data
// plus one listing of the video's folder; reading a track or the fonts
// happens only when the player asks for them. See internal/subs.

// subFile loads a media file the user can see, probing it first if it has
// no track list yet (files imported from Plex).
func (s *Server) subFile(w http.ResponseWriter, r *http.Request) (int64, string, []probe.Stream, bool) {
	fileID, ok := pathID(r, "fileId")
	if !ok {
		writeErr(w, 400, "bad id")
		return 0, "", nil, false
	}
	var path, streams string
	var libID int64
	err := s.db.R.QueryRowContext(r.Context(), `SELECT path, library_id, COALESCE(streams, 'null') FROM media_files WHERE id = ?`, fileID).
		Scan(&path, &libID, &streams)
	if err != nil || !currentUser(r).CanSee(libID) {
		writeErr(w, 404, "file not found")
		return 0, "", nil, false
	}
	if streams == "null" {
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		s.prober.ProbeNow(ctx, fileID, path)
		cancel()
		s.db.R.QueryRowContext(r.Context(), `SELECT COALESCE(streams, 'null') FROM media_files WHERE id = ?`, fileID).Scan(&streams)
	}
	var ss []probe.Stream
	json.Unmarshal([]byte(streams), &ss)
	return fileID, path, ss, true
}

func embeddedTracks(ss []probe.Stream) ([]subs.Track, []subs.EmbeddedStream) {
	var tracks []subs.Track
	var es []subs.EmbeddedStream
	for _, st := range ss {
		if st.Type != "subtitle" {
			continue
		}
		tracks = append(tracks, subs.Track{
			ID: "s" + strconv.Itoa(st.Index), Codec: st.Codec, Language: st.Language, Title: st.Title,
			Format: subs.FormatOf(st.Codec), Default: st.Default, Forced: st.Forced,
		})
		es = append(es, subs.EmbeddedStream{Index: st.Index, Codec: st.Codec, Language: st.Language, Title: st.Title, Default: st.Default, Forced: st.Forced})
	}
	return tracks, es
}

func (s *Server) handleSubtitleTracks(w http.ResponseWriter, r *http.Request) {
	_, path, ss, ok := s.subFile(w, r)
	if !ok {
		return
	}
	tracks, _ := embeddedTracks(ss)
	tracks = append(tracks, subs.Sidecars(path)...)
	if tracks == nil {
		tracks = []subs.Track{}
	}
	writeJSON(w, map[string]any{"tracks": tracks, "ffmpeg": s.subs.Available()})
}

func (s *Server) handleSubtitleTrack(w http.ResponseWriter, r *http.Request) {
	fileID, path, ss, ok := s.subFile(w, r)
	if !ok {
		return
	}
	id := strings.TrimSuffix(r.PathValue("track"), ".ass")
	var body []byte
	var err error
	switch {
	case strings.HasPrefix(id, "s"):
		idx, perr := strconv.Atoi(id[1:])
		tracks, es := embeddedTracks(ss)
		var t *subs.Track
		for i := range tracks {
			if tracks[i].ID == id {
				t = &tracks[i]
			}
		}
		if perr != nil || t == nil {
			writeErr(w, 404, "no such subtitle track")
			return
		}
		if t.Format == "image" {
			writeErr(w, 415, "picture-based subtitles (PGS/VobSub) can't be shown in the browser")
			return
		}
		if !s.subs.Available() {
			writeErr(w, 501, "ffmpeg is not installed on the server, so embedded subtitles can't be read")
			return
		}
		body, err = s.subs.Embedded(r.Context(), fileID, path, es, idx)
	case strings.HasPrefix(id, "x"):
		var t *subs.Track
		for _, st := range subs.Sidecars(path) {
			if st.ID == id {
				st := st
				t = &st
			}
		}
		if t == nil {
			writeErr(w, 404, "no such subtitle track")
			return
		}
		body, err = s.subs.Sidecar(r.Context(), path, *t)
	default:
		writeErr(w, 404, "no such subtitle track")
		return
	}
	if err != nil {
		if r.Context().Err() != nil {
			return
		}
		writeErr(w, 502, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Write(body)
}

func (s *Server) handleSubtitleFonts(w http.ResponseWriter, r *http.Request) {
	fileID, path, _, ok := s.subFile(w, r)
	if !ok {
		return
	}
	fonts, err := s.subs.Fonts(r.Context(), fileID, path)
	if err != nil {
		if r.Context().Err() == nil {
			writeErr(w, 502, err.Error())
		}
		return
	}
	type font struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	out := []font{}
	for _, f := range fonts {
		out = append(out, font{f.Name, fmt.Sprintf("/api/subs/%d/fonts/%s", fileID, f.ID)})
	}
	writeJSON(w, map[string]any{"fonts": out})
}

func (s *Server) handleSubtitleFont(w http.ResponseWriter, r *http.Request) {
	fileID, ok := pathID(r, "fileId")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var libID int64
	if err := s.db.R.QueryRowContext(r.Context(), `SELECT library_id FROM media_files WHERE id = ?`, fileID).Scan(&libID); err != nil || !currentUser(r).CanSee(libID) {
		writeErr(w, 404, "file not found")
		return
	}
	p := s.subs.FontPath(fileID, r.PathValue("font"))
	if p == "" {
		writeErr(w, 404, "font not found")
		return
	}
	f, err := os.Open(p)
	if err != nil {
		writeErr(w, 404, "font not found")
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		writeErr(w, 404, "font not found")
		return
	}
	ct := mime.TypeByExtension(filepath.Ext(p))
	if ct == "" {
		ct = "font/ttf"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}
