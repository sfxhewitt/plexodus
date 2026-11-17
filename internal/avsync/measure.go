// Package avsync measures a device's lip sync from a phone video of the
// calibration clip playing on it.
//
// The clip (internal/api/assets/lipsync-calibration.mkv) flashes the whole
// screen white and beeps at 1 kHz at the same instant, every 2 seconds. A
// phone filming the TV from the sofa records both the picture and the sound
// as the viewer gets them, so the gap between each flash and its beep in the
// recording is the delay the TV, soundbar or receiver adds. Nobody has to
// judge "earlier or later" by eye.
package avsync

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Result of one recording.
type Result struct {
	// OffsetMs: how late the sound arrives after the picture. Positive means
	// the sound is late (move it earlier); negative means it's early.
	OffsetMs int `json:"offsetMs"`
	// Pairs: flashes matched with a beep. SpreadMs: how much those
	// measurements disagree (max - min); small means a clean result.
	Pairs    int `json:"pairs"`
	SpreadMs int `json:"spreadMs"`
}

// Errors the phone page explains in plain words.
var (
	ErrNoFlashes = errors.New("couldn't see the flashes")
	ErrNoBeeps   = errors.New("couldn't hear the beeps")
	ErrUnclear   = errors.New("the flashes and beeps didn't line up consistently")
	ErrShort     = errors.New("the video is too short")
)

const (
	period    = 2.0 // seconds between flashes in the clip
	gridW     = 32  // frames are shrunk to this grid of cells
	gridH     = 18
	audioRate = 48000
	block     = 96 // audio samples per envelope step (2 ms)
)

// Measure analyses a recording with ffmpeg.
func Measure(ctx context.Context, ffmpeg, path string) (Result, error) {
	times, frames, err := readVideo(ctx, ffmpeg, path)
	if err != nil {
		return Result{}, err
	}
	env, start, err := readAudio(ctx, ffmpeg, path)
	if err != nil {
		return Result{}, err
	}
	return analyse(times, frames, env, start)
}

var ptsRe = regexp.MustCompile(`pts_time:\s*(-?[0-9.]+)`)

// readVideo returns each frame's time and its luma, gridW x gridH cells.
func readVideo(ctx context.Context, ffmpeg, path string) ([]float64, [][]byte, error) {
	return readVideoArgs(ctx, ffmpeg, []string{"-i", path})
}

// readVideoArgs is readVideo with any input options (a seek, a length).
func readVideoArgs(ctx context.Context, ffmpeg string, input []string) ([]float64, [][]byte, error) {
	args := append([]string{"-nostdin", "-hide_banner", "-loglevel", "info"}, input...)
	args = append(args, "-map", "0:v:0", "-an", "-sn",
		"-vf", fmt.Sprintf("scale=%d:%d:flags=area,format=gray,showinfo", gridW, gridH),
		"-vsync", "passthrough", "-f", "rawvideo", "-pix_fmt", "gray", "pipe:1")
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, nil, fmt.Errorf("reading video: %v: %s", err, tail(stderr.String()))
	}
	var times []float64
	sc := bufio.NewScanner(&stderr)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, "Parsed_showinfo") || !strings.Contains(line, " n:") {
			continue
		}
		if m := ptsRe.FindStringSubmatch(line); m != nil {
			t, _ := strconv.ParseFloat(m[1], 64)
			times = append(times, t)
		}
	}
	size := gridW * gridH
	n := len(out) / size
	if n > len(times) {
		n = len(times)
	}
	frames := make([][]byte, n)
	for i := range frames {
		frames[i] = out[i*size : (i+1)*size]
	}
	return times[:n], frames, nil
}

// readAudio returns the 1 kHz band's loudness every 2 ms, and the time of
// the first value. ffmpeg puts audio and video on the same clock.
func readAudio(ctx context.Context, ffmpeg, path string) ([]float64, float64, error) {
	cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", path, "-map", "0:a:0", "-vn", "-sn",
		"-af", fmt.Sprintf("aresample=%d,pan=mono|c0=c0,highpass=f=700,highpass=f=700,lowpass=f=1400,lowpass=f=1400,"+
			"asetnsamples=n=%d:p=0,astats=metadata=1:reset=1,ametadata=print:key=lavfi.astats.Overall.RMS_level:file=-", audioRate, block),
		"-f", "null", "-")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if strings.Contains(stderr.String(), "matches no streams") {
			return nil, 0, ErrNoBeeps
		}
		return nil, 0, fmt.Errorf("reading audio: %v: %s", err, tail(stderr.String()))
	}
	var env []float64
	start := math.NaN()
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		line := sc.Text()
		if m := ptsRe.FindStringSubmatch(line); m != nil {
			if math.IsNaN(start) {
				start, _ = strconv.ParseFloat(m[1], 64)
			}
			continue
		}
		if i := strings.Index(line, "RMS_level="); i >= 0 {
			db, err := strconv.ParseFloat(strings.TrimSpace(line[i+len("RMS_level="):]), 64)
			if err != nil || math.IsInf(db, 0) || math.IsNaN(db) {
				db = -150
			}
			env = append(env, math.Pow(10, db/20))
		}
	}
	if math.IsNaN(start) {
		start = 0
	}
	return env, start, nil
}

func analyse(times []float64, frames [][]byte, env []float64, audioStart float64) (Result, error) {
	if len(times) < 10 || times[len(times)-1]-times[0] < period+0.5 {
		return Result{}, ErrShort
	}
	flashes := findFlashes(times, frames)
	if len(flashes) == 0 {
		return Result{}, ErrNoFlashes
	}
	beeps := findBeeps(env, audioStart)
	if len(beeps) == 0 {
		return Result{}, ErrNoBeeps
	}
	// Each flash's own beep is the nearest one: the gap is far less than
	// the 2 s between flashes.
	var gaps []float64
	for _, f := range flashes {
		best := math.Inf(1)
		for _, b := range beeps {
			if d := b - f; math.Abs(d) < math.Abs(best) {
				best = d
			}
		}
		if math.Abs(best) < period/2-0.1 {
			gaps = append(gaps, best)
		}
	}
	if len(gaps) == 0 {
		return Result{}, ErrUnclear
	}
	sort.Float64s(gaps)
	med := median(gaps)
	// Drop a stray (someone talking over a beep, a reflection on the TV).
	var kept []float64
	for _, g := range gaps {
		if math.Abs(g-med) <= 0.06 {
			kept = append(kept, g)
		}
	}
	if len(kept) < 2 || len(kept)*2 < len(gaps) {
		return Result{}, ErrUnclear
	}
	off := median(kept)
	return Result{
		OffsetMs: int(math.Round(off * 1000)),
		Pairs:    len(kept),
		SpreadMs: int(math.Round((kept[len(kept)-1] - kept[0]) * 1000)),
	}, nil
}

// findFlashes returns when each flash starts. It looks for the cells that
// jump the most (the TV, wherever it is in the shot), follows their
// brightness and times each jump at the halfway point between frames.
func findFlashes(times []float64, frames [][]byte) []float64 {
	n, size := len(frames), gridW*gridH
	// How much each cell rises, frame to frame; the flash makes the biggest rises.
	rise := make([]float64, n)
	for k := 1; k < n; k++ {
		var s float64
		for c := 0; c < size; c++ {
			if d := int(frames[k][c]) - int(frames[k-1][c]); d > 0 {
				s += float64(d)
			}
		}
		rise[k] = s
	}
	peak := 0
	for k := range rise {
		if rise[k] > rise[peak] {
			peak = k
		}
	}
	if rise[peak] < float64(size)*0.5 { // nothing really flashed
		return nil
	}
	// The cells that took part in the biggest flash.
	var cells []int
	maxD := 0
	for c := 0; c < size; c++ {
		if d := int(frames[peak][c]) - int(frames[peak-1][c]); d > maxD {
			maxD = d
		}
	}
	for c := 0; c < size; c++ {
		if d := int(frames[peak][c]) - int(frames[peak-1][c]); d >= maxD/3 && d >= 12 {
			cells = append(cells, c)
		}
	}
	if len(cells) == 0 {
		return nil
	}
	lum := make([]float64, n)
	for k := range frames {
		var s float64
		for _, c := range cells {
			s += float64(frames[k][c])
		}
		lum[k] = s / float64(len(cells))
	}
	// Steps up in that brightness, at least 1 s apart.
	jump := make([]float64, n)
	maxJ := 0.0
	for k := 1; k < n; k++ {
		// Up to two frames: a flash that starts mid-exposure is split over two.
		j := lum[k] - lum[k-1]
		if k >= 2 {
			j = math.Max(j, lum[k]-lum[k-2])
		}
		jump[k] = j
		maxJ = math.Max(maxJ, j)
	}
	if maxJ < 12 {
		return nil
	}
	var out []float64
	for k := 1; k < n; k++ {
		if jump[k] < maxJ*0.4 || lum[k]-lum[k-1] <= 0 {
			continue
		}
		// Only the frame where this step peaks, and not again within a second.
		if k+1 < n && jump[k+1] > jump[k] {
			continue
		}
		if len(out) > 0 && times[k]-out[len(out)-1] < 1.0 {
			continue
		}
		// The level before the flash, and at its brightest just after.
		base := lum[k-1]
		if k >= 2 && lum[k-2] < base {
			base = lum[k-2]
		}
		top := lum[k]
		for i := k + 1; i < n && times[i]-times[k] < 0.1; i++ {
			top = math.Max(top, lum[i])
		}
		half := base + (top-base)/2
		// First frame at or past halfway; interpolate between it and the one before.
		i := k
		for i > 1 && lum[i-1] >= half {
			i--
		}
		t := times[i]
		if i > 0 && lum[i] != lum[i-1] {
			f := (half - lum[i-1]) / (lum[i] - lum[i-1])
			f = math.Max(0, math.Min(1, f))
			t = times[i-1] + f*(times[i]-times[i-1])
		}
		// A frame gathers light over its whole interval, so it reads half
		// bright when the flash began half a frame after the frame started.
		if i > 0 {
			t += (times[i] - times[i-1]) / 2
		}
		out = append(out, t)
	}
	return out
}

// findBeeps returns when each beep starts: where the 1 kHz band climbs
// halfway from the room's noise to the beep's level.
func findBeeps(env []float64, start float64) []float64 {
	if len(env) < 10 {
		return nil
	}
	sorted := append([]float64(nil), env...)
	sort.Float64s(sorted)
	noise := sorted[len(sorted)/4]
	loud := sorted[len(sorted)*99/100]
	if loud < noise*4 || loud < 1e-4 { // under ~12 dB above the room: no beep
		return nil
	}
	dt := float64(block) / audioRate
	thresh := noise + (loud-noise)/2
	var out []float64
	last := math.Inf(-1)
	for i := 1; i < len(env); i++ {
		if env[i] < thresh || env[i-1] >= thresh {
			continue
		}
		t := start + float64(i)*dt
		if t-last < 1.0 {
			continue
		}
		// It must stay loud for a while (the beep is 80 ms), not a click.
		hold := 0
		for j := i; j < len(env) && j < i+int(0.05/dt); j++ {
			if env[j] >= thresh {
				hold++
			}
		}
		if hold < int(0.03/dt) {
			continue
		}
		f := (thresh - env[i-1]) / (env[i] - env[i-1])
		// Each value covers a 2 ms block; time it at the block's middle.
		out = append(out, start+(float64(i-1)+0.5+f)*dt)
		last = t
	}
	return out
}

func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 300 {
		s = s[len(s)-300:]
	}
	return s
}
