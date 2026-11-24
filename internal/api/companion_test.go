package api

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sfxhewitt/plexodus/internal/config"
	"github.com/sfxhewitt/plexodus/internal/subs"
)

const testASS = `[Script Info]
ScriptType: v4.00+

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:05.00,0:00:07.00,Default,,0,0,0,,{\i1}Hello,{\i0} George.\NHow are you?
Dialogue: 0,0:00:07.00,0:00:08.00,Default,,0,0,0,,Hello, George. How are you?
Dialogue: 0,0:11:00.50,0:11:02.00,Default,,0,0,0,,He's making violent love to me, Mother!
`

func TestParseASS(t *testing.T) {
	ls := parseASS(testASS)
	if len(ls) != 2 {
		t.Fatalf("lines = %+v", ls)
	}
	if ls[0].text != "Hello, George. How are you?" || ls[0].start != 5 || ls[0].end != 8 {
		t.Errorf("first = %+v", ls[0])
	}
	if ls[1].start != 660.5 {
		t.Errorf("second start = %v", ls[1].start)
	}
}

func TestPickTrack(t *testing.T) {
	ts := []subs.Track{
		{ID: "s3", Language: "eng", Format: "image"},
		{ID: "s4", Language: "fre", Format: "text"},
		{ID: "s5", Language: "eng", Format: "text", Forced: true},
		{ID: "s6", Language: "eng", Format: "text"},
	}
	if p := pickTrack(ts); p == nil || p.ID != "s6" {
		t.Fatalf("picked %+v", p)
	}
	if p := pickTrack(ts[:2]); p != nil {
		t.Fatalf("picked %+v, want none", p)
	}
}

// The page follows what the account plays, writes notes for the current
// stretch from the subtitles, and only shows notes playback has reached.
func TestCompanionNotes(t *testing.T) {
	s, u := testServer(t)
	s.cfg = &config.Config{}
	s.nowPlaying = newNowPlaying()
	s.comp = newCompanion()
	s.subs = subs.New("", "", t.TempDir())
	dir := t.TempDir()
	video := filepath.Join(dir, "Its a Wonderful Life (1946).mkv")
	os.WriteFile(video, []byte("x"), 0o644)
	os.WriteFile(strings.TrimSuffix(video, ".mkv")+".en.ass", []byte(testASS), 0o644)
	if _, err := s.db.W.Exec(`INSERT INTO media_files(id, item_id, library_id, path, size, mtime, duration, streams) VALUES (5, 10, 1, ?, 1, 0, 7800, '[]')`, video); err != nil {
		t.Fatal(err)
	}
	var sent map[string]any
	s.claude = newTestClaude("sk-test", `{"notes":[
		{"at":662,"kind":"language","title":"Violent love, 1946 style","text":"Back then it meant ardent wooing."},
		{"at":900,"kind":"character","title":"Later note","text":"Not reached yet."}]}`, &sent)

	// Nothing playing yet.
	var res companionResp
	if call(t, s, u, s.handleCompanion, "GET", "/api/companion", "", nil, &res); res.Stream != nil || !res.Enabled {
		t.Fatalf("idle = %+v", res)
	}

	s.nowPlaying.progress(httptest.NewRequest("POST", "/", nil), u.ID, 10, 700, 7800, "paused")
	call(t, s, u, s.handleCompanion, "GET", "/api/companion", "", nil, &res)
	if res.Stream == nil || res.Stream.Title != "Heat" || !res.Pending {
		t.Fatalf("first = %+v", res)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		call(t, s, u, s.handleCompanion, "GET", "/api/companion", "", nil, &res)
		if !res.Pending || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(res.Notes) != 1 || res.Notes[0].At != 662 || !res.Grounded {
		t.Fatalf("notes = %+v (notice %q)", res.Notes, res.Notice)
	}
	// The prompt carried the subtitle line for this stretch.
	b, _ := json.Marshal(sent)
	if !strings.Contains(string(b), "making violent love") || !strings.Contains(string(b), "0:10:00 to 0:20:00") {
		t.Errorf("prompt missing subtitles: %s", b)
	}
}
