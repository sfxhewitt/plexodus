package avsync

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

// makeFilm writes a 40 s "film": four moving patterns cut together at
// irregular times, with a soundtrack that keeps changing.
func makeFilm(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "film.mp4")
	run(t, "-f", "lavfi", "-i", "testsrc2=s=320x180:r=24000/1001:d=40",
		"-f", "lavfi", "-i", "mandelbrot=s=320x180:r=24000/1001",
		"-f", "lavfi", "-i", "life=s=320x180:r=24000/1001:mold=10:ratio=0.1",
		"-f", "lavfi", "-i", "cellauto=s=320x180:r=24000/1001:rule=110",
		"-f", "lavfi", "-i", "anoisesrc=c=brown:d=40:r=48000:a=0.3",
		"-f", "lavfi", "-i", "aevalsrc='0.3*sin(2*PI*(300+200*sin(2*PI*0.37*t))*t)*(0.5+0.5*sin(2*PI*1.3*t))':d=40:s=48000",
		"-filter_complex", "[0:v]format=yuv420p[a0];[1:v]trim=0:40,setpts=PTS-STARTPTS,format=yuv420p[a1];"+
			"[2:v]trim=0:40,setpts=PTS-STARTPTS,format=yuv420p[a2];[3:v]trim=0:40,setpts=PTS-STARTPTS,format=yuv420p[a3];"+
			"[a0][a1]blend=all_expr='if(lt(mod(T,5.3),2.1),A,B)'[b01];[a2][a3]blend=all_expr='if(lt(mod(T,3.7),1.6),A,B)'[b23];"+
			"[b01][b23]blend=all_expr='if(lt(mod(T,7.1),3.3),A,B)'[v];[4:a][5:a]amix=inputs=2,volume='0.6+0.4*sin(2*PI*0.9*t)':eval=frame[a]",
		"-map", "[v]", "-map", "[a]", "-c:v", "libx264", "-preset", "veryfast", "-g", "48", "-c:a", "aac", "-t", "40", out)
	return out
}

// filmIt records 10 s of the film from 20 s "on a TV" with its sound
// offsetMs late (negative: early), the way a phone would.
func filmIt(t *testing.T, film string, offsetMs int) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), fmt.Sprintf("crec%d.mp4", offsetMs))
	shift := fmt.Sprintf("adelay=%d:all=1", offsetMs)
	if offsetMs < 0 {
		shift = fmt.Sprintf("atrim=start=%f,asetpts=PTS-STARTPTS", float64(-offsetMs)/1000)
	}
	run(t, "-ss", "20", "-t", "12", "-i", film,
		"-f", "lavfi", "-i", "color=c=0x404040:s=640x360:r=120:d=12",
		"-f", "lavfi", "-i", "anoisesrc=a=0.02:c=pink:d=12:r=48000",
		"-filter_complex", "[0:v]scale=240:136,fps=120,eq=contrast=0.7[tv];[1:v]noise=alls=10:allf=t[bg];"+
			"[bg][tv]overlay=200:100:shortest=1,tmix=frames=4,setpts=PTS-3/120/TB,fps=30,trim=0.5:10.5,setpts=PTS-STARTPTS[v];"+
			"[0:a]"+shift+",highpass=f=150,lowpass=f=6000,volume=0.4,aresample=48000[a0];"+
			"[a0][2:a]amix=inputs=2:duration=first,atrim=0.5:10.5,asetpts=PTS-STARTPTS[a]",
		"-map", "[v]", "-map", "[a]", "-c:v", "libx264", "-preset", "veryfast", "-pix_fmt", "yuv420p", "-c:a", "aac", out)
	return out
}

func run(t *testing.T, args ...string) {
	t.Helper()
	if b, err := exec.Command("ffmpeg", append([]string{"-nostdin", "-v", "error", "-y"}, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, b)
	}
}

func TestMeasureContent(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	film := makeFilm(t)
	for _, off := range []int{0, 140, -90} {
		// Playback had reached 36 s when the phone sent it.
		r, err := MeasureContent(context.Background(), "ffmpeg", filmIt(t, film, off), film, 36)
		if err != nil {
			t.Fatalf("%d: %v", off, err)
		}
		if d := r.OffsetMs - off; d < -20 || d > 20 || r.At < 20.3 || r.At > 20.7 {
			t.Errorf("%d: measured %+v", off, r)
		}
	}
	// The flash-and-beep clip isn't in this film.
	if _, err := MeasureContent(context.Background(), "ffmpeg", clip, film, 36); err != ErrNoSoundMatch {
		t.Fatalf("unrelated recording: %v", err)
	}
}
