package detect

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Chapter is one named chapter of a file.
type Chapter struct {
	Title      string
	Start, End float64
}

// Chapters reads chapter names from the file header. It's the same cheap
// read ffprobe does when playback starts.
func Chapters(ctx context.Context, ffprobe, path string) ([]Chapter, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, ffprobe, "-v", "error", "-probesize", "5M", "-analyzeduration", "0",
		"-print_format", "json", "-show_chapters", path).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("ffprobe: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	var p struct {
		Chapters []struct {
			Start string            `json:"start_time"`
			End   string            `json:"end_time"`
			Tags  map[string]string `json:"tags"`
		} `json:"chapters"`
	}
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, err
	}
	var cs []Chapter
	for _, c := range p.Chapters {
		s, _ := strconv.ParseFloat(c.Start, 64)
		e, _ := strconv.ParseFloat(c.End, 64)
		cs = append(cs, Chapter{Title: c.Tags["title"], Start: s, End: e})
	}
	return cs, nil
}

var (
	reRecap   = regexp.MustCompile(`(?i)\b(recap|previously|last time|story so far|summary)\b`)
	reIntro   = regexp.MustCompile(`(?i)^(intro(duction)?|opening( credits| titles| theme| sequence)?|title sequence|main titles?|theme song|op)$`)
	reCredits = regexp.MustCompile(`(?i)^(credits|end credits|ending( credits| theme)?|closing credits|end titles|outro|ed)$`)
)

// ClassifyChapter maps a chapter name to a marker type, or "".
func ClassifyChapter(title string) string {
	t := strings.Trim(strings.TrimSpace(title), ".:-")
	switch {
	case t == "":
		return ""
	case reRecap.MatchString(t):
		return "recap"
	case reIntro.MatchString(t):
		return "intro"
	case reCredits.MatchString(t):
		return "credits"
	}
	return ""
}

// ChapterMarkers turns named chapters into markers, merging consecutive
// chapters of the same type. Credits chapters only count near the end and
// recaps and intros only in the first part of the file.
func ChapterMarkers(cs []Chapter, duration float64) map[string]Span {
	out := map[string]Span{}
	for i := 0; i < len(cs); i++ {
		typ := ClassifyChapter(cs[i].Title)
		if typ == "" {
			continue
		}
		s := Span{cs[i].Start, cs[i].End}
		for i+1 < len(cs) && ClassifyChapter(cs[i+1].Title) == typ {
			i++
			s.End = cs[i].End
		}
		if s.Len() < 5 || s.Len() > 10*60 {
			continue
		}
		if duration <= 0 {
			duration = cs[len(cs)-1].End
		}
		switch typ {
		case "recap", "intro":
			if s.Start > duration/3 {
				continue
			}
		case "credits":
			if s.Start < duration*0.6 {
				continue
			}
		}
		if _, dup := out[typ]; !dup {
			out[typ] = s
		}
	}
	return out
}

// Extract decodes the first audio track between start and start+length
// seconds (length <= 0: to the end) and fingerprints it.
func Extract(ctx context.Context, ffmpeg, path string, start, length float64) ([]uint32, error) {
	timeout := 3 * time.Minute
	if length <= 0 {
		timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{"-nostdin", "-v", "error"}
	if start > 0 {
		args = append(args, "-ss", strconv.FormatFloat(start, 'f', 2, 64))
	}
	args = append(args, "-i", path)
	if length > 0 {
		args = append(args, "-t", strconv.FormatFloat(length, 'f', 2, 64))
	}
	args = append(args, "-map", "0:a:0", "-vn", "-sn", "-dn", "-ac", "1", "-ar", strconv.Itoa(SampleRate), "-f", "s16le", "-")
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	fp := NewFingerprinter()
	cmd.Stdout = fp
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("ffmpeg: %v %s", err, msg)
	}
	return fp.Result(), nil
}
