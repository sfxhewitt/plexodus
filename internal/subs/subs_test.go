package subs

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const testASS = `[Script Info]
ScriptType: v4.00+
PlayResX: 1920
PlayResY: 1080

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Sign,Test Font,60,&H0000FFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,0,8,10,10,10,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:00.50,0:00:01.50,Sign,,0,0,0,,{\pos(960,100)}Typeset sign
`

const testSRT = "1\n00:00:00,500 --> 00:00:01,500\nHello there\n"

func TestSidecarInfo(t *testing.T) {
	for _, c := range []struct {
		name, lang, title string
		forced            bool
	}{
		{"Show S01E01.en", "en", "", false},
		{"Show S01E01.eng.forced", "eng", "", true},
		{"Show S01E01.pt-BR.SDH", "pt-BR", "SDH", false},
		{"Show S01E01", "", "", false},
		{"Show S01E01.Signs & Songs", "", "Signs & Songs", false},
	} {
		lang, title, forced := sidecarInfo(c.name, "Show S01E01")
		if !strings.EqualFold(lang, c.lang) || title != c.title || forced != c.forced {
			t.Errorf("%q: got %q %q %v", c.name, lang, title, forced)
		}
	}
}

func TestSidecars(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"Ep 01.mkv", "Ep 02.mkv", "Ep 01.en.srt", "Ep 01.ass", "Ep 02.en.srt", "Subs/Ep 01.fr.ass", "Subs/Other.srt", "Subs/Ep 01/2_English.ass"} {
		p := filepath.Join(dir, f)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte("x"), 0o644)
	}
	got := Sidecars(filepath.Join(dir, "Ep 01.mkv"))
	var names []string
	for _, tr := range got {
		names = append(names, tr.FileName)
	}
	want := "Ep 01.ass|Ep 01.en.srt|Subs/Ep 01.fr.ass|Subs/Ep 01/2_English.ass"
	if strings.Join(names, "|") != want {
		t.Fatalf("got %v", names)
	}
	if got[1].Language != "en" || got[1].Format != "text" || got[0].Format != "ass" {
		t.Fatalf("bad tracks %+v", got)
	}
}

func TestExtract(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	ass, srt, font := filepath.Join(dir, "a.ass"), filepath.Join(dir, "b.srt"), filepath.Join(dir, "TestFont.ttf")
	os.WriteFile(ass, []byte(testASS), 0o644)
	os.WriteFile(srt, []byte(testSRT), 0o644)
	os.WriteFile(font, []byte("not really a font"), 0o644)
	video := filepath.Join(dir, "Ep 01.mkv")
	out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=c=black:s=64x36:d=2", "-i", ass, "-i", srt,
		"-attach", font, "-metadata:s:t", "mimetype=application/x-truetype-font",
		"-map", "0", "-map", "1", "-map", "2", "-c:v", "mpeg4", "-c:s:0", "ass", "-c:s:1", "srt",
		"-metadata:s:s:0", "language=eng", "-metadata:s:s:1", "language=jpn", video).CombinedOutput()
	if err != nil {
		t.Fatalf("make mkv: %v %s", err, out)
	}
	m := New("ffmpeg", "ffprobe", filepath.Join(dir, "cache"))
	streams := []EmbeddedStream{{Index: 1, Codec: "ass"}, {Index: 2, Codec: "subrip"}}
	b, err := m.Embedded(context.Background(), 7, video, streams, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `{\pos(960,100)}Typeset sign`) || !strings.Contains(string(b), "Test Font") {
		t.Fatalf("ASS track lost its styling:\n%s", b)
	}
	b, err = m.Embedded(context.Background(), 7, video, streams, 2)
	if err != nil || !strings.Contains(string(b), "Hello there") || !strings.Contains(string(b), "[Events]") {
		t.Fatalf("SRT track not converted: %v\n%s", err, b)
	}
	fonts, err := m.Fonts(context.Background(), 7, video)
	if err != nil || len(fonts) != 1 || fonts[0].Name != "TestFont.ttf" {
		t.Fatalf("fonts: %v %+v", err, fonts)
	}
	fb, _ := os.ReadFile(m.FontPath(7, fonts[0].ID))
	if string(fb) != "not really a font" {
		t.Fatalf("font content %q", fb)
	}

	// Sidecar SRT in a legacy encoding is converted to ASS.
	os.WriteFile(filepath.Join(dir, "Ep 01.fr.srt"), []byte("1\n00:00:00,500 --> 00:00:01,500\nCaf\xe9\n"), 0o644)
	var side Track
	for _, tr := range Sidecars(video) {
		if tr.Language == "fr" {
			side = tr
		}
	}
	b, err = m.Sidecar(context.Background(), video, side)
	if err != nil || !strings.Contains(string(b), "Café") {
		t.Fatalf("sidecar: %v\n%s", err, b)
	}

	// A changed video invalidates the cache.
	os.WriteFile(video, []byte("replaced"), 0o644)
	if _, err := m.Embedded(context.Background(), 7, video, streams, 1); err == nil {
		t.Fatal("expected an error after the video was replaced")
	}
}
