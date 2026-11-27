package api

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Stills for the scene companion: so notes can be about what's on screen
// (a newspaper headline, a prop, a wordless scene) and not only what's said,
// Claude is shown a small still every few seconds of the stretch it writes
// about.
//
// Each still is one quick ffmpeg seek to the keyframe at or before the
// wanted moment, so only a few megabytes of the file are read per still
// (the stretch is about to be played anyway, so rclone is fetching it).
// Encoders tend to put keyframes on cuts, so these land on new shots.

const (
	// stillEvery is the gap between stills in a stretch of notes.
	stillEvery = 15
	// stillWidth is wide enough to read a headline or a sign.
	stillWidth = 768
	// stillBudget caps how long one stretch's stills may take.
	stillBudget = 75 * time.Second
)

type still struct {
	at   float64 // seconds into the file the frame is really from
	jpeg []byte
}

type stillMaker struct {
	ffmpeg string
	sem    chan struct{}

	mu sync.Mutex
	// hdr: files seen to be HDR, whose stills need tone mapping to look
	// right. noTonemap: this ffmpeg can't do it (no zscale).
	hdr       map[string]bool
	noTonemap bool
}

func newStillMaker(ffmpeg string) *stillMaker {
	return &stillMaker{ffmpeg: ffmpeg, sem: make(chan struct{}, 3), hdr: map[string]bool{}}
}

var (
	ptsRe = regexp.MustCompile(`pts_time:\s*(-?[0-9.]+)`)
	hdrRe = regexp.MustCompile(`smpte2084|arib-std-b67`)
)

const tonemapFilter = "zscale=t=linear:npl=100,format=gbrpf32le,zscale=p=bt709,tonemap=hable:desat=0,zscale=t=bt709:m=bt709:r=tv,format=yuv420p,"

// one grabs the keyframe at or before at.
func (m *stillMaker) one(ctx context.Context, path string, at float64) (still, error) {
	select {
	case m.sem <- struct{}{}:
		defer func() { <-m.sem }()
	case <-ctx.Done():
		return still{}, ctx.Err()
	}
	m.mu.Lock()
	tone := m.hdr[path] && !m.noTonemap
	m.mu.Unlock()
	st, sawHDR, err := m.run(ctx, path, at, tone)
	if err != nil && tone && ctx.Err() == nil {
		// This ffmpeg can't tone map: washed-out stills are still readable.
		m.mu.Lock()
		m.noTonemap = true
		m.mu.Unlock()
		st, _, err = m.run(ctx, path, at, false)
	} else if err == nil && !tone && sawHDR {
		m.mu.Lock()
		m.hdr[path] = true
		can := !m.noTonemap
		m.mu.Unlock()
		if can {
			if st2, _, err2 := m.run(ctx, path, at, true); err2 == nil {
				st = st2
			} else if ctx.Err() == nil {
				m.mu.Lock()
				m.noTonemap = true
				m.mu.Unlock()
			}
		}
	}
	return st, err
}

func (m *stillMaker) run(ctx context.Context, path string, at float64, tone bool) (still, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	vf := fmt.Sprintf("scale=%d:-2:flags=bicubic,showinfo", stillWidth)
	if tone {
		vf = tonemapFilter + vf
	}
	args := []string{"-n", "15", m.ffmpeg, "-nostdin", "-hide_banner", "-v", "info",
		"-ss", strconv.FormatFloat(at, 'f', 2, 64), "-noaccurate_seek", "-copyts", "-i", path,
		"-map", "0:v:0", "-an", "-sn", "-dn", "-frames:v", "1", "-vf", vf,
		"-c:v", "mjpeg", "-q:v", "5", "-f", "image2pipe", "pipe:1"}
	cmd := exec.CommandContext(ctx, "nice", args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return still{}, false, fmt.Errorf("%v: %s", err, lastLine(errb.String()))
	}
	if out.Len() < 200 {
		return still{}, false, fmt.Errorf("no frame at %s", clock(at))
	}
	real := at
	if mm := ptsRe.FindStringSubmatch(errb.String()); mm != nil {
		if v, err := strconv.ParseFloat(mm[1], 64); err == nil {
			// A frame from long before the ask means the seek ran off the
			// end of the file: a wrongly timed still is worse than none.
			if v < at-60 {
				return still{}, false, fmt.Errorf("no frame at %s", clock(at))
			}
			if v <= at+1 {
				real = v // the keyframe's own time
			}
		}
	}
	return still{at: real, jpeg: out.Bytes()}, hdrRe.MatchString(errb.String()), nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if len(s) > 200 {
		s = s[len(s)-200:]
	}
	return s
}

// stills grabs frames at the given moments, in order, dropping repeats (two
// asks that land on the same keyframe) and whatever isn't back in time.
func (m *stillMaker) stills(ctx context.Context, path string, times []float64, budget time.Duration) []still {
	if m == nil || m.ffmpeg == "" || path == "" || len(times) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	// The first one alone: it finds out whether the file is HDR, and warms
	// the file's index for the rest.
	res := make([]*still, len(times))
	if st, err := m.one(ctx, path, times[0]); err == nil {
		res[0] = &st
	}
	var wg sync.WaitGroup
	for i := 1; i < len(times); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if st, err := m.one(ctx, path, times[i]); err == nil {
				res[i] = &st
			}
		}(i)
	}
	wg.Wait()
	var out []still
	for _, st := range res {
		if st != nil {
			out = append(out, *st)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].at < out[j].at })
	dedup := out[:0]
	for _, st := range out {
		if n := len(dedup); n > 0 && st.at-dedup[n-1].at < 1 {
			continue
		}
		dedup = append(dedup, st)
	}
	return dedup
}

// stillTimes spreads moments across a stretch: one every stillEvery seconds.
func stillTimes(start, end float64) []float64 {
	var out []float64
	for t := start + stillEvery/2.0; t < end-1; t += stillEvery {
		out = append(out, t)
	}
	return out
}
