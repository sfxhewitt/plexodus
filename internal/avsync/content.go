package avsync

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/cmplx"
	"os/exec"
	"strconv"
	"strings"
)

// Checking real content: the phone films whatever is playing, and both the
// sound and the picture in that recording are found in the file itself.
// The sound is matched to the file's soundtrack (cross-correlation), the
// picture to the file's frames (cuts and movement), each giving where in
// the file the recording sits. The difference between the two is how late
// the sound reaches the viewer. Unlike the flash-and-beep test this runs
// at the film's own frame rate and the TV's film settings.

var (
	ErrNoSoundMatch   = errors.New("couldn't find the recorded sound in the film")
	ErrNoPictureMatch = errors.New("couldn't match the picture to the film")
)

const (
	corrRate   = 8000 // Hz, for matching the sound
	searchBack = 150.0
	searchFwd  = 10.0
)

// ContentResult of checking real content.
type ContentResult struct {
	OffsetMs int     `json:"offsetMs"` // sound later than the picture by this much, vs the file
	At       float64 `json:"at"`       // where in the file the recording starts, seconds
	Sound    float64 `json:"sound"`    // match strength of the sound (peak to noise)
	Picture  float64 `json:"picture"`  // correlation of the picture's movement, 0..1
}

// MeasureContent finds a phone recording in the source file. approxEnd is
// roughly where in the file playback was when the recording ended (it
// ended at or before then); the search covers a few minutes before it.
func MeasureContent(ctx context.Context, ffmpeg, recording, source string, approxEnd float64) (ContentResult, error) {
	phone, phoneStart, err := readPCM(ctx, ffmpeg, []string{"-i", recording})
	if err != nil {
		return ContentResult{}, err
	}
	if len(phone) < corrRate*4 {
		return ContentResult{}, ErrShort
	}
	from := math.Max(0, approxEnd-searchBack)
	src, _, err := readPCM(ctx, ffmpeg, []string{"-ss", ftoa(from), "-t", ftoa(approxEnd + searchFwd - from), "-i", source})
	if err != nil {
		return ContentResult{}, err
	}
	if len(src) < len(phone) {
		return ContentResult{}, ErrNoSoundMatch
	}
	lag, strength := gccPhat(src, phone)
	if strength < 12 { // noise alone peaks around 5-6 over a few minutes
		return ContentResult{}, ErrNoSoundMatch
	}
	// Phone sound at time a is the file's sound at a + kA.
	kA := from + lag/corrRate - phoneStart

	pt, pf, err := readVideo(ctx, ffmpeg, recording)
	if err != nil {
		return ContentResult{}, err
	}
	if len(pt) < 10 {
		return ContentResult{}, ErrShort
	}
	// The picture is within a second or so of the sound: read the file's
	// frames for that stretch only.
	vFrom := math.Max(0, pt[0]+kA-1.5)
	vLen := pt[len(pt)-1] - pt[0] + 3
	st, sf, err := readVideoArgs(ctx, ffmpeg, []string{"-ss", ftoa(vFrom), "-t", ftoa(vLen), "-i", source})
	if err != nil {
		return ContentResult{}, err
	}
	if len(st) < 10 {
		return ContentResult{}, ErrNoPictureMatch
	}
	for i := range st {
		st[i] += vFrom
	}
	kV, corr := matchMotion(pt, pf, st, sf, kA, 1.0)
	if corr < 0.45 {
		return ContentResult{}, ErrNoPictureMatch
	}
	// An event at file time s is heard at s - kA and seen at s - kV.
	return ContentResult{
		OffsetMs: int(math.Round((kV - kA) * 1000)),
		At:       pt[0] + kA,
		Sound:    math.Round(strength*10) / 10,
		Picture:  math.Round(corr*100) / 100,
	}, nil
}

func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', 3, 64) }

// readPCM decodes the first audio track to mono float samples at corrRate,
// and says when the first sample is (on the same clock as readVideo).
func readPCM(ctx context.Context, ffmpeg string, input []string) ([]float64, float64, error) {
	// ashowinfo logs each block's time (info level); the first gives the start.
	args := append([]string{"-nostdin", "-hide_banner", "-loglevel", "info"}, input...)
	args = append(args, "-map", "0:a:0", "-vn", "-sn", "-ac", "1", "-ar", strconv.Itoa(corrRate),
		"-af", "ashowinfo", "-f", "f32le", "pipe:1")
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if strings.Contains(stderr.String(), "matches no streams") {
			return nil, 0, ErrNoBeeps
		}
		return nil, 0, fmt.Errorf("reading sound: %v: %s", err, tail(stderr.String()))
	}
	start := 0.0
	if i := strings.Index(stderr.String(), "Parsed_ashowinfo"); i >= 0 {
		if m := ptsRe.FindStringSubmatch(stderr.String()[i:]); m != nil {
			start, _ = strconv.ParseFloat(m[1], 64)
		}
	} else if m := ptsRe.FindStringSubmatch(stderr.String()); m != nil {
		start, _ = strconv.ParseFloat(m[1], 64)
	}
	n := len(out) / 4
	pcm := make([]float64, n)
	for i := 0; i < n; i++ {
		pcm[i] = float64(math.Float32frombits(binary.LittleEndian.Uint32(out[i*4:])))
	}
	return pcm, start, nil
}

// gccPhat finds where needle sits in hay: the lag (in samples, with a
// fraction) and how clearly it stands out (peak over the noise's RMS).
// Phase-transform weighting keeps it sharp through speakers and a room.
func gccPhat(hay, needle []float64) (float64, float64) {
	n := 1
	for n < len(hay)+len(needle) {
		n <<= 1
	}
	a := make([]complex128, n)
	b := make([]complex128, n)
	for i, v := range hay {
		a[i] = complex(v, 0)
	}
	for i, v := range needle {
		b[i] = complex(v, 0)
	}
	fft(a, false)
	fft(b, false)
	for i := range a {
		x := a[i] * cmplx.Conj(b[i])
		if m := cmplx.Abs(x); m > 1e-12 {
			x /= complex(m, 0)
		}
		a[i] = x
	}
	fft(a, true)
	// Only lags where the whole needle fits in the hay.
	maxLag := len(hay) - len(needle)
	best, bi := math.Inf(-1), 0
	var sum2 float64
	for i := 0; i <= maxLag; i++ {
		v := real(a[i])
		sum2 += v * v
		if v > best {
			best, bi = v, i
		}
	}
	rms := math.Sqrt(sum2 / float64(maxLag+1))
	lag := float64(bi)
	if bi > 0 && bi < maxLag {
		y0, y1, y2 := real(a[bi-1]), real(a[bi]), real(a[bi+1])
		if d := y0 - 2*y1 + y2; d != 0 {
			lag += 0.5 * (y0 - y2) / d
		}
	}
	if rms == 0 {
		return lag, 0
	}
	return lag, best / rms
}

// fft is an in-place radix-2 transform; len(x) must be a power of two.
func fft(x []complex128, inverse bool) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	sign := -1.0
	if inverse {
		sign = 1
	}
	for size := 2; size <= n; size <<= 1 {
		w := cmplx.Exp(complex(0, sign*2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			wk := complex(1, 0)
			for k := 0; k < size/2; k++ {
				u := x[start+k]
				v := x[start+k+size/2] * wk
				x[start+k] = u + v
				x[start+k+size/2] = u - v
				wk *= w
			}
		}
	}
	if inverse {
		for i := range x {
			x[i] /= complex(float64(n), 0)
		}
	}
}

// motion is how much the picture changes into each frame, at that frame's
// time: a cut is a big spike, movement smaller ones. A change that lands
// mid-exposure splits over two frames, so its centre of mass still falls
// at the moment it happened.
func motion(times []float64, frames [][]byte) ([]float64, []float64) {
	var t, m []float64
	for k := 1; k < len(frames); k++ {
		var s float64
		for c := range frames[k] {
			d := int(frames[k][c]) - int(frames[k-1][c])
			if d < 0 {
				d = -d
			}
			s += float64(d)
		}
		t = append(t, times[k])
		m = append(m, s)
	}
	return t, m
}

// matchMotion finds kV (phone video time t is file time t + kV) within
// guess ± span seconds, comparing the two movement signals on a 1 ms grid.
func matchMotion(pt []float64, pf [][]byte, st []float64, sf [][]byte, guess, span float64) (float64, float64) {
	ptm, pm := motion(pt, pf)
	stm, sm := motion(st, sf)
	const step = 0.001
	const sigma = 0.012
	grid := func(times, vals []float64, from, to float64) []float64 {
		n := int((to-from)/step) + 1
		g := make([]float64, n)
		r := int(4 * sigma / step)
		for i, t := range times {
			c := int(math.Round((t - from) / step))
			for j := c - r; j <= c+r; j++ {
				if j >= 0 && j < n {
					x := (float64(j)*step + from - t) / sigma
					g[j] += vals[i] * math.Exp(-0.5*x*x)
				}
			}
		}
		// Normalise: compare shapes, not brightness or how big the TV is.
		var mean, sd float64
		for _, v := range g {
			mean += v
		}
		mean /= float64(n)
		for i := range g {
			g[i] -= mean
			sd += g[i] * g[i]
		}
		sd = math.Sqrt(sd / float64(n))
		if sd > 0 {
			for i := range g {
				g[i] /= sd
			}
		}
		return g
	}
	p0, p1 := ptm[0], ptm[len(ptm)-1]
	P := grid(ptm, pm, p0, p1)
	s0 := stm[0]
	S := grid(stm, sm, s0, stm[len(stm)-1])
	score := func(k float64) float64 {
		off := int(math.Round((p0 + k - s0) / step))
		var sum float64
		var cnt int
		for i, v := range P {
			j := i + off
			if j >= 0 && j < len(S) {
				sum += v * S[j]
				cnt++
			}
		}
		if cnt < len(P)/2 {
			return -1
		}
		return sum / float64(cnt)
	}
	n := int(span / step)
	best, bk := math.Inf(-1), 0
	scores := make([]float64, 2*n+1)
	for i := -n; i <= n; i++ {
		v := score(guess + float64(i)*step)
		scores[i+n] = v
		if v > best {
			best, bk = v, i
		}
	}
	k := guess + float64(bk)*step
	if j := bk + n; j > 0 && j < 2*n {
		y0, y1, y2 := scores[j-1], scores[j], scores[j+1]
		if d := y0 - 2*y1 + y2; d != 0 {
			k += 0.5 * (y0 - y2) / d * step
		}
	}
	return k, best
}
