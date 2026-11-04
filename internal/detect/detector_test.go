package detect

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/db"
)

// writeEpisode encodes PCM to an AC-3 .mkv, optionally with chapters.
func writeEpisode(t *testing.T, dir, name string, pcm []int16, chapters []Chapter) string {
	t.Helper()
	raw := filepath.Join(dir, name+".pcm")
	b := make([]byte, 2*len(pcm))
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
	}
	if err := os.WriteFile(raw, b, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, name+".mkv")
	args := []string{"-v", "error", "-y", "-f", "s16le", "-ar", fmt.Sprint(SampleRate), "-ac", "1", "-i", raw}
	if len(chapters) > 0 {
		meta := ";FFMETADATA1\n"
		for _, c := range chapters {
			meta += fmt.Sprintf("[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=%s\n", int(c.Start*1000), int(c.End*1000), c.Title)
		}
		mf := filepath.Join(dir, name+".meta")
		os.WriteFile(mf, []byte(meta), 0o644)
		args = append(args, "-i", mf, "-map_metadata", "1", "-map_chapters", "1")
	}
	args = append(args, "-map", "0:a", "-ar", "48000", "-ac", "2", "-c:a", "ac3", "-b:a", "192k", out)
	if o, err := exec.Command("ffmpeg", args...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, o)
	}
	return out
}

// TestDetectEpisodes runs the whole pipeline on three encoded episodes:
// audio matching finds the intro, credits and recap of episode 2, and
// chapter names give episode 3's markers.
func TestDetectEpisodes(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	intro, credits := synth(1, 40), synth(2, 45)
	ep1 := cat(synth(10, 20), intro, synth(11, 420), credits)
	clip := func(at, length float64) []int16 { return ep1[secs(at):secs(at+length)] }
	recap := cat(synth(20, 2), clip(90, 6), synth(21, 0.8), clip(200.4, 5), clip(330, 7), synth(22, 1), clip(410, 6))
	recapLen := float64(len(recap)) / SampleRate
	ep2 := cat(recap, synth(23, 2), intro, synth(24, 400), credits, synth(25, 5))
	ep3 := cat(synth(30, 30), intro, synth(31, 300))

	d, err := db.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx := context.Background()
	exec1 := func(q string, args ...any) {
		if _, err := d.W.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec1(`INSERT INTO libraries(id, name, kind, paths, created_at) VALUES(1, 'TV', 'shows', '[]', 0)`)
	exec1(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, added_at, updated_at) VALUES(1, 1, 'show', 'show', 'S', 's', 0, 0)`)
	exec1(`INSERT INTO items(id, library_id, kind, guid, parent_id, title, sort_title, season_number, added_at, updated_at) VALUES(2, 1, 'season', 's1', 1, 'S1', 's1', 1, 0, 0)`)
	eps := [][]int16{ep1, ep2, ep3}
	var chapters = [][]Chapter{nil, nil, {{"Previously on", 0, 30}, {"Opening", 30, 70}, {"Part 1", 70, 370}}}
	for i, pcm := range eps {
		id := 10 + i
		path := writeEpisode(t, dir, fmt.Sprintf("e%d", i+1), pcm, chapters[i])
		fi, _ := os.Stat(path)
		exec1(`INSERT INTO items(id, library_id, kind, guid, parent_id, title, sort_title, season_number, episode_number, added_at, updated_at)
			VALUES(?, 1, 'episode', ?, 2, 'E', 'e', 1, ?, 0, 0)`, id, fmt.Sprintf("e%d", i), i+1)
		exec1(`INSERT INTO media_files(item_id, library_id, path, size, mtime, duration) VALUES(?, 1, ?, ?, 0, ?)`,
			id, path, fi.Size(), float64(len(pcm))/SampleRate)
	}

	det := New(d, "ffmpeg", "ffprobe")
	note, err := det.Detect(ctx, 11)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(note)
	m := det.markers(ctx, 11)
	introStart := recapLen + 2
	near(t, "intro start", m["intro"].Start, introStart, 1.5)
	near(t, "intro end", m["intro"].End, introStart+40, 1.5)
	near(t, "recap start", m["recap"].Start, 0, 0.01)
	near(t, "recap end", m["recap"].End, m["intro"].Start, 0.01)
	end2 := float64(len(ep2)) / SampleRate
	near(t, "credits start", m["credits"].Start, end2-50, 1.5)
	near(t, "credits end", m["credits"].End, end2, 0.5) // trailing seconds after the theme are included
	if m["recap"].source != "audio" {
		t.Errorf("recap source %q", m["recap"].source)
	}
	// Episode 1 got its intro as the neighbour, and has no recap.
	m1 := det.markers(ctx, 10)
	near(t, "ep1 intro start", m1["intro"].Start, 20, 1.5)
	if _, ok := m1["recap"]; ok {
		t.Error("episode 1 has a recap")
	}

	// Episode 3: chapter names win over audio, and no audio recap is
	// invented (its start isn't from earlier episodes).
	if _, err := det.Detect(ctx, 12); err != nil {
		t.Fatal(err)
	}
	m3 := det.markers(ctx, 12)
	if m3["recap"].source != "chapter" || m3["recap"].Span != (Span{0, 30}) {
		t.Errorf("ep3 recap %+v", m3["recap"])
	}
	if m3["intro"].source != "chapter" || m3["intro"].Span != (Span{30, 70}) {
		t.Errorf("ep3 intro %+v", m3["intro"])
	}

	// Fingerprints are cached: episode 1's full print is stored.
	var n int
	d.R.QueryRow(`SELECT COUNT(*) FROM fingerprints WHERE part = 'full'`).Scan(&n)
	if n == 0 {
		t.Error("no full fingerprint cached")
	}
}
