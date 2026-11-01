package stream

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Makes a 12-second H.264 + AC-3 MKV, the shape of file Safari can't play,
// and checks a remux session produces a playable playlist from an offset.
func TestRemuxSession(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	gen := exec.Command("ffmpeg", "-nostdin", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x180:rate=25", "-f", "lavfi", "-i", "sine=frequency=440",
		"-t", "12", "-c:v", "libx264", "-preset", "ultrafast", "-g", "25", "-c:a", "ac3", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("can't make test file: %v: %s", err, out)
	}

	m := New("ffmpeg", filepath.Join(dir, "stream"))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s, err := m.Start(ctx, 1, 1, Options{Path: src, Start: 5, AudioIndex: 1, AudioMode: "aac", VideoCodec: "h264"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.File(ctx, "index.m3u8", 30*time.Second)
	if err != nil {
		t.Fatal("playlist:", err)
	}
	b, _ := s.Playlist(p)
	txt := string(b)
	for _, want := range []string{"#EXT-X-START:TIME-OFFSET=0", "init.mp4", "seg00000.m4s", "#EXT-X-PLAYLIST-TYPE:EVENT"} {
		if !strings.Contains(txt, want) {
			t.Errorf("playlist missing %q:\n%s", want, txt)
		}
	}
	if _, err := s.File(ctx, "init.mp4", 10*time.Second); err != nil {
		t.Fatal("init:", err)
	}
	if _, err := s.File(ctx, "seg00000.m4s", 10*time.Second); err != nil {
		t.Fatal("segment:", err)
	}
	if _, err := s.File(ctx, "../etc/passwd", time.Second); err == nil {
		t.Error("path traversal accepted")
	}
	<-s.done
	if s.err != nil {
		t.Fatal("ffmpeg:", s.err)
	}
	b, _ = os.ReadFile(p)
	if !strings.Contains(string(b), "#EXT-X-ENDLIST") {
		t.Error("finished playlist has no ENDLIST")
	}
	// Roughly 7 seconds remain after the 5 s offset.
	var total float64
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "#EXTINF:") {
			d, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(l, "#EXTINF:"), ","), 64)
			if err == nil {
				total += d
			}
		}
	}
	if total < 5 || total > 9 {
		t.Errorf("remuxed %.1fs, want about 7s", total)
	}

	// Starting again for the same user+file stops the first session.
	s2, err := m.Start(ctx, 1, 1, Options{Path: src, Start: 0, AudioIndex: 1, AudioMode: "copy", VideoCodec: "h264"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s2.File(ctx, "index.m3u8", 30*time.Second); err != nil {
		t.Fatal("second playlist:", err)
	}
	if m.Get(s.ID, 2) != nil {
		t.Error("another user could see the session")
	}
	m.Stop(s2.ID, 1)
	<-s2.done
}
