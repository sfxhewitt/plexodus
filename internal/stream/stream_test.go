package stream

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func needFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
}

// makeMKV writes an H.264 (with B-frames) + AC-3 MKV of the given length
// with keyframes forced at the given times, the shape of file Safari can't
// play directly.
func makeMKV(t *testing.T, path string, seconds int, keyframes string) {
	t.Helper()
	gen := exec.Command("ffmpeg", "-nostdin", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=48000",
		"-t", strconv.Itoa(seconds), "-c:v", "libx264", "-preset", "ultrafast", "-bf", "3", "-g", "1000", "-sc_threshold", "0",
		"-force_key_frames", keyframes, "-c:a", "ac3", "-b:a", "96k", path)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("can't make test file: %v: %s", err, out)
	}
}

func TestKeyframesAndSegments(t *testing.T) {
	needFFmpeg(t)
	src := filepath.Join(t.TempDir(), "src.mkv")
	makeMKV(t, src, 30, "0,6,8,12,14,20,23,27")
	kf, err := ReadKeyframes(src)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{0, 6000, 8000, 12000, 14000, 20000, 23000, 27000}
	if fmt.Sprint(kf.TimesMs) != fmt.Sprint(want) {
		t.Errorf("keyframes %v, want %v", kf.TimesMs, want)
	}
	if kf.DurationMs < 29900 || kf.DurationMs > 30100 {
		t.Errorf("duration %d ms, want about 30000", kf.DurationMs)
	}
	// 4-second target: a new segment at the first keyframe 4 s or more
	// after the current one started.
	if got := fmt.Sprint(kf.Segments(4)); got != "[0 6000 12000 20000 27000]" {
		t.Errorf("segments %s", got)
	}
}

// A VOD session gives a complete playlist at once, serves segments as byte
// ranges of the remux, and a seek into an unmade part starts a new ffmpeg
// run whose fragments line up with the first run's timeline.
func TestVODSession(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	makeMKV(t, src, 30, "0,6,8,12,14,20,23,27")
	m := New("ffmpeg", filepath.Join(dir, "stream"))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s, err := m.Start(ctx, 1, 1, Options{Path: src, Start: 0, AudioIndex: 1, AudioMode: "aac", VideoCodec: "h264"})
	if err != nil {
		t.Fatal(err)
	}
	if !s.VOD() {
		t.Fatal("expected VOD mode for an MKV with cues")
	}
	pl, err := s.Playlist(ctx, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	txt := string(pl)
	for _, want := range []string{"#EXT-X-PLAYLIST-TYPE:VOD", "#EXT-X-MAP:URI=\"init.mp4\"", "#EXTINF:6.000,\nseg00000.m4s", "#EXTINF:8.000,\nseg00002.m4s", "seg00004.m4s\n#EXT-X-ENDLIST", "#EXT-X-TARGETDURATION:8"} {
		if !strings.Contains(txt, want) {
			t.Errorf("playlist missing %q:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "seg00005.m4s") {
		t.Errorf("too many segments:\n%s", txt)
	}
	read := func(name string) []byte {
		t.Helper()
		c, err := s.File(ctx, name, 30*time.Second)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		f, err := os.Open(c.Path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		b, err := io.ReadAll(io.NewSectionReader(f, c.Off, c.Size))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	init := read("init.mp4")
	if len(init) < 100 || string(init[4:8]) != "ftyp" {
		t.Fatalf("init segment doesn't start with ftyp (%d bytes)", len(init))
	}
	seg0 := read("seg00000.m4s")
	if string(seg0[4:8]) != "moof" {
		t.Fatal("segment doesn't start with moof")
	}
	// Segment 1 (6–12 s) spans two keyframes, so two fragments.
	seg1 := read("seg00001.m4s")
	if n := strings.Count(string(seg1), "moof"); n < 2 {
		t.Errorf("segment 1 has %d moof boxes, want 2", n)
	}
	// Wait for the first run to finish, then make a second run by asking
	// for the last segment of a fresh session that started at 20 s and
	// seeking back to 0: that part must come from a new run.
	s2, err := m.Start(ctx, 1, 1, Options{Path: src, Start: 21, AudioIndex: 1, AudioMode: "aac", VideoCodec: "h264"})
	if err != nil {
		t.Fatal(err)
	}
	s = s2
	segLast := read("seg00004.m4s")
	if len(s.runs) != 1 {
		t.Fatalf("%d runs after a forward read, want 1", len(s.runs))
	}
	seg0b := read("seg00000.m4s")
	if len(s.runs) != 2 {
		t.Fatalf("%d runs after seeking back, want 2", len(s.runs))
	}
	if len(seg0b) != len(seg0) {
		t.Errorf("segment 0 from a restarted run is %d bytes, first run gave %d", len(seg0b), len(seg0))
	}
	// Decode what we served: init + segments must play back as a continuous
	// stream whose timestamps match the file's.
	out := filepath.Join(dir, "served.mp4")
	os.WriteFile(out, append(append(append([]byte{}, init...), seg0b...), segLast...), 0o644)
	probe := exec.Command("ffprobe", "-v", "error", "-select_streams", "v", "-show_entries", "packet=pts_time,flags", "-of", "csv", out)
	pb, err := probe.Output()
	if err != nil {
		t.Fatal("ffprobe:", err)
	}
	var keys []string
	for _, l := range strings.Split(string(pb), "\n") {
		if strings.Contains(l, "K_") {
			keys = append(keys, strings.Split(l, ",")[1])
		}
	}
	// Served timeline = file time + the fixed 10 s offset (+ reorder delay).
	if len(keys) != 2 || !strings.HasPrefix(keys[0], "10.") || !strings.HasPrefix(keys[1], "37.") {
		t.Errorf("keyframes in served stream: %v, want one near 10 s and one near 37 s", keys)
	}
	if _, err := s.File(ctx, "../etc/passwd", time.Second); err == nil {
		t.Error("path traversal accepted")
	}
	if m.Get(s.ID, 2) != nil {
		t.Error("another user could see the session")
	}
	m.Stop(s.ID, 1)
}

// Files without a keyframe index fall back to ffmpeg's growing playlist.
func TestEventSession(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
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
	if s.VOD() {
		t.Fatal("an MP4 should use event mode")
	}
	b, err := s.Playlist(ctx, 30*time.Second)
	if err != nil {
		t.Fatal("playlist:", err)
	}
	txt := string(b)
	for _, want := range []string{"#EXT-X-START:TIME-OFFSET=0", "init.mp4", "seg00000.m4s", "#EXT-X-PLAYLIST-TYPE:EVENT"} {
		if !strings.Contains(txt, want) {
			t.Errorf("playlist missing %q:\n%s", want, txt)
		}
	}
	if _, err := s.File(ctx, "seg00000.m4s", 10*time.Second); err != nil {
		t.Fatal("segment:", err)
	}
	<-s.cur.done
	if s.cur.err != nil {
		t.Fatal("ffmpeg:", s.cur.err)
	}
	b, _ = s.Playlist(ctx, time.Second)
	if !strings.Contains(string(b), "#EXT-X-ENDLIST") {
		t.Error("finished playlist has no ENDLIST")
	}
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
	if _, err := s2.Playlist(ctx, 30*time.Second); err != nil {
		t.Fatal("second playlist:", err)
	}
	m.Stop(s2.ID, 1)
	<-s2.cur.done
}

func TestArgsAudioAuto(t *testing.T) {
	s := &Session{Opts: Options{Path: "x.mkv", AudioIndex: AudioAuto, AudioMode: "aac"}}
	args := strings.Join(s.args(0, false), " ")
	if !strings.Contains(args, "-map 0:a:0? -c:v copy -c:a aac") {
		t.Errorf("unexpected args: %s", args)
	}
	s.Opts.AudioIndex = -1
	if a := strings.Join(s.args(0, false), " "); strings.Contains(a, "-c:a") {
		t.Errorf("audio mapped with AudioIndex -1: %s", a)
	}
}

func TestArgsAudioShift(t *testing.T) {
	s := &Session{vod: true, Opts: Options{Path: "x.mkv", AudioIndex: 1, AudioMode: "copy", AudioShiftMs: -250}}
	args := strings.Join(s.args(12000, true), " ")
	want := "-ss 12.000 -copyts -i x.mkv -ss 12.000 -itsoffset -0.250 -i x.mkv -map 0:v:0 -map 1:1 -c:v copy -c:a copy"
	if !strings.Contains(args, want) {
		t.Errorf("args %s\nwant %s", args, want)
	}
	s.Opts.AudioIndex = -1
	if a := strings.Join(s.args(0, false), " "); strings.Contains(a, "itsoffset") {
		t.Errorf("second input without audio: %s", a)
	}
}

// A shifted session really moves the audio against the video in the
// served stream, both ways.
func TestAudioShiftTimestamps(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	makeMKV(t, src, 20, "0,6,12,18")
	for _, shift := range []int{300, -300} {
		m := New("ffmpeg", filepath.Join(dir, fmt.Sprint("stream", shift)))
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		s, err := m.Start(ctx, 1, 1, Options{Path: src, Start: 6, AudioIndex: 1, AudioMode: "copy", VideoCodec: "h264", AudioShiftMs: shift})
		if err != nil {
			t.Fatal(err)
		}
		<-s.cur.done
		if s.cur.err != nil {
			t.Fatal("ffmpeg:", s.cur.err)
		}
		// The scanner still finds the init part and the segments.
		if _, err := s.File(ctx, "init.mp4", 5*time.Second); err != nil {
			t.Fatal("init:", err)
		}
		if _, err := s.File(ctx, "seg00001.m4s", 5*time.Second); err != nil {
			t.Fatal("segment:", err)
		}
		out, err := exec.Command("ffprobe", "-v", "error", "-show_entries", "packet=stream_index,pts_time", "-of", "csv", s.cur.path).Output()
		if err != nil {
			t.Fatal(err)
		}
		first := map[string]float64{}
		for _, l := range strings.Split(string(out), "\n") {
			f := strings.Split(l, ",")
			if len(f) < 3 {
				continue
			}
			v, _ := strconv.ParseFloat(f[2], 64)
			if old, ok := first[f[1]]; !ok || v < old {
				first[f[1]] = v
			}
		}
		// Video starts at the 6 s keyframe (+10 s offset); audio read from
		// the same point lands shift ms away from it.
		diff := (first["1"] - first["0"]) * 1000
		if diff < float64(shift)-60 || diff > float64(shift)+60 {
			t.Errorf("shift %d: audio starts %.0f ms after video (%v)", shift, diff, first)
		}
		m.Stop(s.ID, 1)
		cancel()
	}
}

// Event mode (no keyframe index) takes the shift too.
func TestEventSessionShifted(t *testing.T) {
	needFFmpeg(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	gen := exec.Command("ffmpeg", "-nostdin", "-loglevel", "error", "-y",
		"-f", "lavfi", "-i", "testsrc=size=320x180:rate=25", "-f", "lavfi", "-i", "sine=frequency=440",
		"-t", "8", "-c:v", "libx264", "-preset", "ultrafast", "-g", "25", "-c:a", "ac3", src)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Skipf("can't make test file: %v: %s", err, out)
	}
	m := New("ffmpeg", filepath.Join(dir, "stream"))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s, err := m.Start(ctx, 1, 1, Options{Path: src, Start: 2, AudioIndex: 1, AudioMode: "copy", VideoCodec: "h264", AudioShiftMs: -200})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Playlist(ctx, 30*time.Second); err != nil {
		t.Fatal("playlist:", err)
	}
	<-s.cur.done
	if s.cur.err != nil {
		t.Fatal("ffmpeg:", s.cur.err)
	}
}
