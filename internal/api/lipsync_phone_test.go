package api

import (
	"bytes"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/config"
)

func TestLipsyncPhone(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	s, u := testServer(t)
	s.cfg = &config.Config{FFmpeg: "ffmpeg", DataDir: t.TempDir()}

	// A "phone video": the clip with its sound 120 ms late.
	rec := filepath.Join(t.TempDir(), "rec.mp4")
	if b, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-y", "-i", "assets/lipsync-calibration.mkv",
		"-af", "adelay=120:all=1", "-c:v", "libx264", "-preset", "veryfast", "-c:a", "aac", rec).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, b)
	}

	var start struct{ Code, URL string }
	if code := call(t, s, u, s.handleLipsyncStart, "POST", "http://tv.local:8484/api/lipsync/sessions", `{"deviceMs":40}`, nil, &start); code != 200 {
		t.Fatal(code)
	}
	if len(start.Code) != 6 || start.URL != "http://tv.local:8484/sync/"+start.Code {
		t.Fatalf("start: %+v", start)
	}
	pv := map[string]string{"code": start.Code}

	// QR code for the TV.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/x", nil)
	r.SetPathValue("code", start.Code)
	s.handleLipsyncQR(w, r)
	if w.Code != 200 || !bytes.HasPrefix(w.Body.Bytes(), []byte("\x89PNG")) {
		t.Fatalf("qr: %d", w.Code)
	}

	// Unknown codes are refused on the phone side.
	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/x", bytes.NewReader([]byte("x")))
	r.SetPathValue("code", "000000")
	s.handleLipsyncUpload(w, r)
	if w.Code != 404 {
		t.Fatalf("unknown code: %d", w.Code)
	}

	body, _ := os.ReadFile(rec)
	w = httptest.NewRecorder()
	r = httptest.NewRequest("POST", "/x", bytes.NewReader(body))
	r.SetPathValue("code", start.Code)
	s.handleLipsyncUpload(w, r)
	if w.Code != 202 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}

	var st phoneSession
	for i := 0; i < 100; i++ {
		call(t, s, u, s.handleLipsyncPoll, "GET", "/x?deviceMs=40", "", pv, &st)
		if st.State == "done" || st.State == "failed" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if st.State != "done" || st.Result == nil || st.Round != 1 {
		t.Fatalf("state: %+v", st)
	}
	if d := st.Result.OffsetMs - 120; d < -15 || d > 15 {
		t.Fatalf("measured %d ms, want ~120", st.Result.OffsetMs)
	}
	if st.NewDeviceMs != 40-st.Result.OffsetMs {
		t.Fatalf("new device offset %d from %d", st.NewDeviceMs, st.Result.OffsetMs)
	}
	call(t, s, u, s.handleLipsyncApplied, "POST", "/x", `{"deviceMs":-80}`, pv, nil)
	call(t, s, auth.User{}, s.handleLipsyncPhoneState, "GET", "/x", "", pv, &st)
	if st.AppliedMs == nil || *st.AppliedMs != -80 {
		t.Fatalf("applied: %+v", st)
	}
	if left, _ := os.ReadDir(filepath.Join(s.cfg.DataDir, "lipsync")); len(left) != 0 {
		t.Fatalf("recording not deleted: %v", left)
	}
}
