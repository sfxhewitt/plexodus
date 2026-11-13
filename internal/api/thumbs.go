package api

import (
	"fmt"
	"net/http"
	"os"
	"strconv"
)

// fileVisible says whether the user may see a media file.
func (s *Server) fileVisible(r *http.Request, fileID int64) bool {
	var itemID, libID int64
	if s.db.R.QueryRowContext(r.Context(), `SELECT item_id, library_id FROM media_files WHERE id = ?`, fileID).Scan(&itemID, &libID) != nil {
		return false
	}
	return currentUser(r).CanSee(libID) && s.itemVisible(r, itemID)
}

// handleThumbs describes a file's scrub preview sheets, or 404 until they're made.
func (s *Server) handleThumbs(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok || !s.fileVisible(r, id) {
		writeErr(w, 404, "not found")
		return
	}
	meta, ok := s.thumbs.Meta(id)
	if !ok {
		writeErr(w, 404, "not made yet")
		return
	}
	urls := make([]string, meta.Sheets)
	for i := range urls {
		urls[i] = fmt.Sprintf("/api/files/%d/thumbs/%d", id, i)
	}
	writeJSON(w, map[string]any{"interval": meta.Interval, "count": meta.Count, "cols": meta.Cols, "rows": meta.Rows,
		"width": meta.Width, "height": meta.Height, "sheets": urls})
}

func (s *Server) handleThumbSheet(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	n, err := strconv.Atoi(r.PathValue("n"))
	if !ok || err != nil || n < 0 || n > 999 || !s.fileVisible(r, id) {
		writeErr(w, 404, "not found")
		return
	}
	f, err := os.Open(s.thumbs.Sheet(id, n))
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	st, _ := f.Stat()
	http.ServeContent(w, r, "", st.ModTime(), f)
}
