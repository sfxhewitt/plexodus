// Package detect finds intro, credits and recap segments in episodes so the
// players can offer "Skip intro", "Skip recap" and "Next episode".
//
// It works in two tiers, cheapest first:
//
//  1. Chapter names (read with ffprobe from the file header): "Recap",
//     "Previously on", "Opening", "End credits" and the like.
//  2. Audio fingerprints, compared between episodes of the same show. An
//     intro or closing theme is audio two neighbouring episodes share; a
//     recap is the start of an episode whose audio also occurs in the
//     episodes before it.
//
// Nothing here runs during a library scan. Detection starts when someone
// plays an episode, one job at a time, so cloud mounts only ever read files
// that are being watched (or are about to be).
package detect

import (
	"encoding/binary"
	"math"
	"math/bits"
	"math/cmplx"
)

// Audio is decoded to mono at this rate; the fingerprint only looks at
// 300–2000 Hz, so this is plenty and keeps decoding cheap.
const (
	SampleRate = 5512
	frameSize  = 2048 // ~0.37 s analysis window
	hopSize    = 256  // ~46 ms between sub-fingerprints
	numBands   = 33
	// FrameSec is the time between consecutive sub-fingerprints.
	FrameSec = float64(hopSize) / SampleRate
)

// silenceRMS is the frame loudness (out of 32768) below which a frame
// carries no information; such frames get hash 0, which matching ignores.
const silenceRMS = 40

// Fingerprinter turns PCM into one 32-bit sub-fingerprint per hop, after
// Haitsma & Kalker's robust audio hash: each bit says whether the energy
// difference between two adjacent frequency bands went up or down compared
// with the previous frame. Re-encoded copies of the same audio give mostly
// identical bits; unrelated audio differs in about half of them.
type Fingerprinter struct {
	buf    []float64
	window []float64
	edges  [numBands + 1]int
	fft    *fft
	prev   [numBands]float64
	have   bool
	out    []uint32
}

func NewFingerprinter() *Fingerprinter {
	f := &Fingerprinter{window: make([]float64, frameSize), fft: newFFT(frameSize)}
	for i := range f.window {
		f.window[i] = 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(frameSize-1))
	}
	lo, hi := 300.0, 2000.0
	for b := 0; b <= numBands; b++ {
		hz := lo * math.Pow(hi/lo, float64(b)/numBands)
		f.edges[b] = int(math.Round(hz * frameSize / SampleRate))
	}
	return f
}

// Write feeds little-endian signed 16-bit mono samples.
func (f *Fingerprinter) Write(p []byte) (int, error) {
	for i := 0; i+1 < len(p); i += 2 {
		f.buf = append(f.buf, float64(int16(binary.LittleEndian.Uint16(p[i:]))))
	}
	f.drain()
	return len(p) - len(p)%2, nil
}

// Samples feeds samples directly (used by tests).
func (f *Fingerprinter) Samples(s []int16) {
	for _, v := range s {
		f.buf = append(f.buf, float64(v))
	}
	f.drain()
}

func (f *Fingerprinter) drain() {
	start := 0
	for len(f.buf)-start >= frameSize {
		f.out = append(f.out, f.frame(f.buf[start:start+frameSize]))
		start += hopSize
	}
	f.buf = append(f.buf[:0], f.buf[start:]...)
}

func (f *Fingerprinter) frame(s []float64) uint32 {
	var sq float64
	in := f.fft.in
	for i, v := range s {
		sq += v * v
		in[i] = complex(v*f.window[i], 0)
	}
	spec := f.fft.run()
	var e [numBands]float64
	for b := 0; b < numBands; b++ {
		for k := f.edges[b]; k < f.edges[b+1]; k++ {
			a := cmplx.Abs(spec[k])
			e[b] += a * a
		}
	}
	var h uint32
	if f.have {
		for b := 0; b < 32; b++ {
			if (e[b]-e[b+1])-(f.prev[b]-f.prev[b+1]) > 0 {
				h |= 1 << b
			}
		}
	}
	f.prev, f.have = e, true
	if math.Sqrt(sq/float64(len(s))) < silenceRMS {
		return 0
	}
	if h == 0 {
		h = 1 // 0 is reserved for "no information"
	}
	return h
}

// Result returns the fingerprint so far. Frame i covers the audio starting
// at i*FrameSec seconds after the first sample.
func (f *Fingerprinter) Result() []uint32 { return f.out }

// hamming is the number of differing bits.
func hamming(a, b uint32) int { return bits.OnesCount32(a ^ b) }

// Encode and Decode store fingerprints as little-endian blobs.
func Encode(fp []uint32) []byte {
	b := make([]byte, 4*len(fp))
	for i, v := range fp {
		binary.LittleEndian.PutUint32(b[4*i:], v)
	}
	return b
}

func Decode(b []byte) []uint32 {
	fp := make([]uint32, len(b)/4)
	for i := range fp {
		fp[i] = binary.LittleEndian.Uint32(b[4*i:])
	}
	return fp
}

// fft is an in-place iterative radix-2 FFT with precomputed twiddles.
type fft struct {
	n   int
	in  []complex128
	tw  []complex128
	rev []int
}

func newFFT(n int) *fft {
	f := &fft{n: n, in: make([]complex128, n), tw: make([]complex128, n/2), rev: make([]int, n)}
	for i := range f.tw {
		f.tw[i] = cmplx.Exp(complex(0, -2*math.Pi*float64(i)/float64(n)))
	}
	logN := bits.TrailingZeros(uint(n))
	for i := range f.rev {
		f.rev[i] = int(bits.Reverse(uint(i)) >> (bits.UintSize - logN))
	}
	return f
}

func (f *fft) run() []complex128 {
	a := f.in
	for i, j := range f.rev {
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for size := 2; size <= f.n; size <<= 1 {
		half, step := size/2, f.n/size
		for start := 0; start < f.n; start += size {
			for k := 0; k < half; k++ {
				t := f.tw[k*step] * a[start+k+half]
				a[start+k+half] = a[start+k] - t
				a[start+k] += t
			}
		}
	}
	return a
}
