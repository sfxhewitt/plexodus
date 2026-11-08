package api

import (
	"bytes"
	_ "embed"
	"net/http"
	"time"

	"github.com/sfxhewitt/plexodus/internal/db"
	"github.com/sfxhewitt/plexodus/internal/stream"
)

// Lip sync: a per-file audio offset, saved from the player's "Audio sync"
// control so a badly muxed file only has to be fixed once. Players add
// their own device offset (TV processing, soundbars) on top.

// fileOffset returns the saved audio offset for a file, in ms.
func (s *Server) fileOffset(r *http.Request, fileID int64) int {
	var ms int
	s.db.R.QueryRowContext(r.Context(), `SELECT offset_ms FROM av_offsets WHERE file_id = ?`, fileID).Scan(&ms)
	return ms
}

func (s *Server) handleGetAudioOffset(w http.ResponseWriter, r *http.Request) {
	fileID, ok := s.visibleFile(w, r)
	if !ok {
		return
	}
	writeJSON(w, map[string]int{"offsetMs": s.fileOffset(r, fileID)})
}

func (s *Server) handleSetAudioOffset(w http.ResponseWriter, r *http.Request) {
	fileID, ok := s.visibleFile(w, r)
	if !ok {
		return
	}
	var b struct {
		OffsetMs int `json:"offsetMs"`
	}
	if err := readJSON(r, &b); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	ms := clampOffset(b.OffsetMs)
	var err error
	if ms == 0 {
		_, err = s.db.W.ExecContext(r.Context(), `DELETE FROM av_offsets WHERE file_id = ?`, fileID)
	} else {
		_, err = s.db.W.ExecContext(r.Context(), `INSERT INTO av_offsets(file_id, offset_ms, updated_by, updated_at) VALUES(?, ?, ?, ?)
			ON CONFLICT(file_id) DO UPDATE SET offset_ms = excluded.offset_ms, updated_by = excluded.updated_by, updated_at = excluded.updated_at`,
			fileID, ms, currentUser(r).ID, db.Now())
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]int{"offsetMs": ms})
}

// visibleFile reads {fileId} and checks the user may play that file.
func (s *Server) visibleFile(w http.ResponseWriter, r *http.Request) (int64, bool) {
	fileID, ok := pathID(r, "fileId")
	if !ok {
		writeErr(w, 400, "bad id")
		return 0, false
	}
	var itemID, libID int64
	err := s.db.R.QueryRowContext(r.Context(), `SELECT item_id, library_id FROM media_files WHERE id = ?`, fileID).Scan(&itemID, &libID)
	if err != nil || !currentUser(r).CanSee(libID) || !s.itemVisible(r, itemID) {
		writeErr(w, 404, "file not found")
		return 0, false
	}
	return fileID, true
}

func clampOffset(ms int) int {
	if ms > stream.MaxAudioShiftMs {
		return stream.MaxAudioShiftMs
	}
	if ms < -stream.MaxAudioShiftMs {
		return -stream.MaxAudioShiftMs
	}
	return ms
}

// The calibration clip: every 2 s a bar reaches the centre line, the screen
// flashes and a 1 kHz beep (AC-3, so it takes the same passthrough path as
// films) plays, all at the same instant in the file. Any gap you see and
// hear is the device's own delay. It's public: there's nothing private in
// it, and the app's player can't send the session cookie.
//
//go:embed assets/lipsync-calibration.mkv
var calibrationClip []byte

var calibrationTime = time.Now()

func (s *Server) handleCalibrationClip(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "video/x-matroska")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	http.ServeContent(w, r, "lipsync-calibration.mkv", calibrationTime, bytes.NewReader(calibrationClip))
}
