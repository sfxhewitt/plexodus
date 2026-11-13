package thumbs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMake(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "in.mkv")
	// 25 minutes of test pattern at 1 fps, a keyframe every 5 s.
	if out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc=size=640x360:rate=1", "-t", "1500",
		"-g", "5", "-c:v", "libx264", "-preset", "ultrafast", src).CombinedOutput(); err != nil {
		t.Fatalf("make source: %v %s", err, out)
	}
	m := New(ff, filepath.Join(dir, "thumbs"))
	if err := m.make(context.Background(), job{id: 7, path: src, duration: 1500}); err != nil {
		t.Fatal(err)
	}
	meta, ok := m.Meta(7)
	if !ok {
		t.Fatal("no meta")
	}
	// 1500 s at 10 s = 150 frames: two sheets, 240x136 each frame (even height).
	if meta.Interval != 10 || meta.Count != 150 || meta.Sheets != 2 || meta.Width != 240 || meta.Height != 136 {
		t.Fatalf("meta = %+v", meta)
	}
	if _, err := os.Stat(m.Sheet(7, 1)); err != nil {
		t.Fatal(err)
	}
}
