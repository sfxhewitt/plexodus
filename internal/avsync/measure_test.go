package avsync

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

const clip = "../api/assets/lipsync-calibration.mkv"

// fakeRecording films the clip "on a TV" the way a phone would: small in a
// noisy frame, washed out, 30 fps with each frame gathering light over its
// interval, room noise under the sound, and the sound shifted by offsetMs
// (positive = late).
func fakeRecording(t *testing.T, offsetMs, fps int) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), fmt.Sprintf("rec%d.mp4", offsetMs))
	shift := fmt.Sprintf("adelay=%d:all=1", offsetMs)
	if offsetMs < 0 {
		shift = fmt.Sprintf("atrim=start=%f,asetpts=PTS-STARTPTS", float64(-offsetMs)/1000)
	}
	cmd := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-y", "-stream_loop", "1", "-i", clip,
		"-f", "lavfi", "-i", "color=c=0x404040:s=640x360:r=120:d=14",
		"-f", "lavfi", "-i", "anoisesrc=a=0.02:c=pink:d=14:r=48000",
		"-filter_complex", "[0:v]scale=200:112,fps=120,eq=contrast=0.6:brightness=0.05[tv];"+
			"[1:v]noise=alls=12:allf=t[bg];[bg][tv]overlay=250:100:shortest=1,tmix=frames=4,setpts=PTS-3/120/TB,"+
			fmt.Sprintf("fps=%d,trim=0.3:11,setpts=PTS-STARTPTS[v];", fps)+
			"[0:a]"+shift+",volume=0.3,aresample=48000[a0];[a0][2:a]amix=inputs=2:duration=first,atrim=0.3:11,asetpts=PTS-STARTPTS[a]",
		"-map", "[v]", "-map", "[a]", "-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p", "-c:a", "aac", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, b)
	}
	return out
}

func TestMeasure(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	// The clip itself: flash and beep together.
	r, err := Measure(context.Background(), "ffmpeg", clip)
	if err != nil || abs(r.OffsetMs) > 10 || r.Pairs < 4 {
		t.Fatalf("clip: %+v %v", r, err)
	}
	for _, c := range []struct{ off, fps int }{{0, 30}, {-150, 30}, {90, 30}, {300, 60}} {
		r, err := Measure(context.Background(), "ffmpeg", fakeRecording(t, c.off, c.fps))
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		if abs(r.OffsetMs-c.off) > 20 || r.Pairs < 3 {
			t.Errorf("%+v: measured %+v", c, r)
		}
	}
}

func TestMeasureNoTV(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	out := filepath.Join(t.TempDir(), "room.mp4")
	b, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-y",
		"-f", "lavfi", "-i", "color=c=0x404040:s=320x180:r=30:d=6", "-f", "lavfi", "-i", "anoisesrc=a=0.02:c=pink:d=6",
		"-vf", "noise=alls=10:allf=t", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", out).CombinedOutput()
	if err != nil {
		t.Fatalf("ffmpeg: %v %s", err, b)
	}
	if _, err := Measure(context.Background(), "ffmpeg", out); err != ErrNoFlashes {
		t.Fatalf("want ErrNoFlashes, got %v", err)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
