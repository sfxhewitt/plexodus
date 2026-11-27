package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	_ "image/jpeg"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sfxhewitt/plexodus/internal/config"
	"github.com/sfxhewitt/plexodus/internal/subs"
)

// testClip makes a short film with a keyframe every second.
func testClip(t *testing.T, seconds int) string {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("no ffmpeg")
	}
	p := filepath.Join(t.TempDir(), "clip.mkv")
	out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=1280x720:rate=10",
		"-t", strconv.Itoa(seconds), "-c:v", "mpeg4", "-g", "10", "-q:v", "8", p).CombinedOutput()
	if err != nil {
		t.Skipf("can't make a test clip: %v %s", err, out)
	}
	return p
}

func TestStillTimes(t *testing.T) {
	ts := stillTimes(600, 1200)
	if len(ts) != 40 || ts[0] != 607.5 || ts[39] != 1192.5 {
		t.Fatalf("times = %v", ts)
	}
	if got := stillTimes(600, 620); len(got) != 1 {
		t.Fatalf("short stretch = %v", got)
	}
}

func TestStills(t *testing.T) {
	clip := testClip(t, 70)
	m := newStillMaker("ffmpeg")
	shots := m.stills(context.Background(), clip, []float64{7.5, 22.5, 22.6, 37.5, 52.5, 500}, time.Minute)
	// 22.5 and 22.6 land on the same keyframe; 500 is past the end.
	if len(shots) != 4 {
		t.Fatalf("got %d stills", len(shots))
	}
	for i, want := range []float64{7, 22, 37, 52} {
		if shots[i].at != want {
			t.Errorf("still %d at %v, want the keyframe at %v", i, shots[i].at, want)
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(shots[i].jpeg))
		if err != nil || cfg.Width != stillWidth || cfg.Height != 432 {
			t.Errorf("still %d: %v %+v", i, err, cfg)
		}
	}
	if got := (*stillMaker)(nil).stills(context.Background(), clip, []float64{1}, time.Second); got != nil {
		t.Error("nil maker returned stills")
	}
	if got := newStillMaker("ffmpeg").stills(context.Background(), filepath.Join(t.TempDir(), "missing.mkv"), []float64{1, 2}, 10*time.Second); len(got) != 0 {
		t.Error("missing file returned stills")
	}
}

// Claude is shown the stills in order with the subtitle lines between
// them, and a stretch written before stills existed is written again.
func TestCompanionNotesWithStills(t *testing.T) {
	clip := testClip(t, 70)
	s, u := testServer(t)
	s.cfg = &config.Config{}
	s.nowPlaying = newNowPlaying()
	s.comp = newCompanion()
	s.comp.stills = newStillMaker("ffmpeg")
	s.subs = subs.New("", "", t.TempDir())
	os.WriteFile(strings.TrimSuffix(clip, ".mkv")+".en.ass", []byte(`[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:10.00,0:00:12.00,Default,,0,0,0,,First line
Dialogue: 0,0:00:30.00,0:00:32.00,Default,,0,0,0,,Second line
`), 0o644)
	if _, err := s.db.W.Exec(`INSERT INTO media_files(id, item_id, library_id, path, size, mtime, duration, streams) VALUES (5, 10, 1, ?, 1, 0, 70, '[]')`, clip); err != nil {
		t.Fatal(err)
	}
	// An old, subtitles-only stretch is already stored.
	if _, err := s.db.W.Exec(`INSERT INTO companion_notes (item_id, chunk, grounded, notes, created_at, v) VALUES (10, 0, 1, '[{"at":1,"kind":"trivia","title":"Old note","text":"From subtitles only."}]', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	s.claude = newTestClaude("sk-test", `{"notes":[{"at":22,"kind":"detail","title":"The headline","text":"Read the paper."}]}`, &sent)

	s.nowPlaying.progress(httptest.NewRequest("POST", "/", nil), u.ID, 10, 40, 70, "paused")
	var res companionResp
	call(t, s, u, s.handleCompanion, "GET", "/api/companion", "", nil, &res)
	if len(res.Notes) != 1 || res.Notes[0].Title != "Old note" || res.Pending {
		t.Fatalf("old notes should stay up while new ones are written: %+v", res)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		call(t, s, u, s.handleCompanion, "GET", "/api/companion", "", nil, &res)
		if len(res.Notes) == 1 && res.Notes[0].Title == "The headline" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(res.Notes) != 1 || res.Notes[0].Title != "The headline" {
		t.Fatalf("notes = %+v (notice %q)", res.Notes, res.Notice)
	}
	var v int
	s.db.R.QueryRow(`SELECT v FROM companion_notes WHERE item_id = 10 AND chunk = 0`).Scan(&v)
	if v != compVersion {
		t.Errorf("v = %d", v)
	}

	// The request: text, then stills each after its timestamp, with the
	// subtitle lines in between in time order.
	msgs := sent["messages"].([]any)
	content := msgs[0].(map[string]any)["content"].([]any)
	var order []string
	images := 0
	for _, c := range content {
		blk := c.(map[string]any)
		switch blk["type"] {
		case "image":
			images++
			src := blk["source"].(map[string]any)
			if src["media_type"] != "image/jpeg" || len(src["data"].(string)) < 500 {
				t.Errorf("bad image block: %v", src["media_type"])
			}
			order = append(order, "IMG")
		case "text":
			order = append(order, blk["text"].(string))
		}
	}
	if images != 5 { // 7, 22, 37, 52, 67
		t.Errorf("images = %d", images)
	}
	all := strings.Join(order, "\n")
	i1, i2 := strings.Index(all, "Still at [0:00:07]"), strings.Index(all, "First line")
	i3, i4 := strings.Index(all, "Still at [0:00:22]"), strings.Index(all, "Second line")
	i5 := strings.Index(all, "Still at [0:00:37]")
	if !(i1 >= 0 && i1 < i2 && i2 < i3 && i3 < i4 && i4 < i5) {
		t.Errorf("timeline out of order: %d %d %d %d %d\n%s", i1, i2, i3, i4, i5, all)
	}

	// Asking shows Claude the last minute too.
	s.claude = newTestClaude("sk-test", `{"answer":"It says so."}`, &sent)
	var ans struct{ Answer string }
	call(t, s, u, s.handleCompanionAsk, "POST", "/api/companion/ask", `{"question":"What does the paper say?"}`, nil, &ans)
	b, _ := json.Marshal(sent)
	if ans.Answer != "It says so." || strings.Count(string(b), `"image/jpeg"`) < 3 || !strings.Contains(string(b), "What does the paper say?") {
		t.Errorf("ask = %q, images %d", ans.Answer, strings.Count(string(b), `"image/jpeg"`))
	}
}
