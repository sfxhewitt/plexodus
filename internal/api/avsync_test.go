package api

import (
	"net/http/httptest"
	"testing"
)

func TestAudioOffset(t *testing.T) {
	s, u := testServer(t)
	if _, err := s.db.W.Exec(`INSERT INTO media_files(id, item_id, library_id, path, size, mtime) VALUES (5, 10, 1, '/m/heat.mkv', 1, 0)`); err != nil {
		t.Fatal(err)
	}
	get := func() int {
		var o struct{ OffsetMs int }
		if code := call(t, s, u, s.handleGetAudioOffset, "GET", "/x", "", map[string]string{"fileId": "5"}, &o); code != 200 {
			t.Fatalf("get: %d", code)
		}
		return o.OffsetMs
	}
	if get() != 0 {
		t.Fatal("new file has an offset")
	}
	call(t, s, u, s.handleSetAudioOffset, "PUT", "/x", `{"offsetMs":-175}`, map[string]string{"fileId": "5"}, nil)
	if got := get(); got != -175 {
		t.Fatalf("offset %d, want -175", got)
	}
	call(t, s, u, s.handleSetAudioOffset, "PUT", "/x", `{"offsetMs":99999}`, map[string]string{"fileId": "5"}, nil)
	if got := get(); got != 5000 {
		t.Fatalf("offset %d, want clamped to 5000", got)
	}
	call(t, s, u, s.handleSetAudioOffset, "PUT", "/x", `{"offsetMs":0}`, map[string]string{"fileId": "5"}, nil)
	if got := get(); got != 0 {
		t.Fatalf("offset %d after reset", got)
	}
	if code := call(t, s, u, s.handleSetAudioOffset, "PUT", "/x", `{"offsetMs":10}`, map[string]string{"fileId": "999"}, nil); code != 404 {
		t.Fatalf("unknown file: %d", code)
	}
}

func TestCalibrationClip(t *testing.T) {
	s := &Server{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/lipsync-calibration.mkv", nil)
	r.Header.Set("Range", "bytes=0-3")
	s.handleCalibrationClip(w, r)
	if w.Code != 206 || w.Body.String() != "\x1a\x45\xdf\xa3" {
		t.Fatalf("got %d %q", w.Code, w.Body.String())
	}
}
