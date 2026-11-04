package detect

import "sort"

// Run is a stretch where fingerprint a[AStart:AEnd] matches b starting at
// BStart (frame indices).
type Run struct {
	AStart, AEnd, BStart int
}

func (r Run) Len() int { return r.AEnd - r.AStart }

const (
	berWindow    = 32   // frames (~1.5 s) averaged per match decision
	berThreshold = 0.30 // bit error rate below which a window matches
	maxBucket    = 48   // ignore hashes this common in b (silence, tones)
	maxOffsets   = 160  // candidate alignments checked per comparison
	minVotes     = 3
)

// Matches finds every stretch of at least minFrames where a and b carry the
// same audio, at any alignment. Frames with hash 0 (silence) are neutral.
func Matches(a, b []uint32, minFrames int) []Run {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	// Vote for alignments (offset = position in b - position in a) using
	// exact and one-bit-off hash hits.
	idx := make(map[uint32][]int32, len(b))
	for i, h := range b {
		if h != 0 {
			idx[h] = append(idx[h], int32(i))
		}
	}
	votes := map[int]int{}
	for i, h := range a {
		if h == 0 {
			continue
		}
		for k := -1; k < 32; k++ {
			v := h
			if k >= 0 {
				v ^= 1 << k
			}
			ps := idx[v]
			if len(ps) > maxBucket {
				continue
			}
			for _, p := range ps {
				votes[int(p)-i]++
			}
		}
	}
	type cand struct{ off, n int }
	var cands []cand
	for off, n := range votes {
		if n < minVotes {
			continue
		}
		// Keep local maxima only: neighbouring offsets are the same alignment.
		if votes[off-1] >= n || votes[off+1] > n {
			continue
		}
		cands = append(cands, cand{off, n})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].n != cands[j].n {
			return cands[i].n > cands[j].n
		}
		return cands[i].off < cands[j].off
	})
	if len(cands) > maxOffsets {
		cands = cands[:maxOffsets]
	}

	var runs []Run
	errs := make([]int, len(a))
	for _, c := range cands {
		lo, hi := max(0, -c.off), min(len(a), len(b)-c.off)
		if hi-lo < minFrames {
			continue
		}
		for i := lo; i < hi; i++ {
			x, y := a[i], b[i+c.off]
			if x == 0 || y == 0 {
				errs[i] = -1
			} else {
				errs[i] = hamming(x, y)
			}
		}
		runs = append(runs, scanRuns(errs[lo:hi], lo, c.off, minFrames)...)
	}
	return dedupe(runs)
}

// scanRuns marks frames whose surrounding window has a low bit error rate
// and returns the contiguous stretches of at least minFrames.
func scanRuns(errs []int, base, off, minFrames int) []Run {
	n := len(errs)
	ok := make([]bool, n)
	sum, cnt := 0, 0
	add := func(i, d int) {
		if i >= 0 && i < n && errs[i] >= 0 {
			sum += d * errs[i]
			cnt += d
		}
	}
	half := berWindow / 2
	for i := -half; i < half; i++ {
		add(i, 1)
	}
	limit := berThreshold * 32
	for i := 0; i < n; i++ {
		add(i+half, 1)
		ok[i] = cnt >= berWindow/4 && float64(sum)/float64(cnt) < limit
		add(i-half, -1)
	}
	// Close short gaps (a cut, a breath of silence).
	const gap = berWindow
	var runs []Run
	start, last := -1, -1
	flush := func() {
		if start >= 0 && last-start+1 >= minFrames {
			runs = append(runs, Run{AStart: base + start, AEnd: base + last + 1, BStart: base + start + off})
		}
	}
	for i := 0; i < n; i++ {
		if !ok[i] {
			continue
		}
		if start >= 0 && i-last > gap {
			flush()
			start = -1
		}
		if start < 0 {
			start = i
		}
		last = i
	}
	flush()
	return runs
}

// dedupe drops runs contained in a longer run at a nearby alignment.
func dedupe(runs []Run) []Run {
	sort.Slice(runs, func(i, j int) bool { return runs[i].Len() > runs[j].Len() })
	var out []Run
	for _, r := range runs {
		dup := false
		for _, o := range out {
			d := (r.BStart - r.AStart) - (o.BStart - o.AStart)
			if d >= -3 && d <= 3 && r.AStart >= o.AStart-berWindow && r.AEnd <= o.AEnd+berWindow {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AStart < out[j].AStart })
	return out
}

// Span is a time range in seconds.
type Span struct{ Start, End float64 }

func (s Span) Len() float64 { return s.End - s.Start }

// sec converts a frame index in a fingerprint that starts at offset seconds.
func sec(frame int, offset float64) float64 { return offset + float64(frame)*FrameSec }

func frames(seconds float64) int { return int(seconds / FrameSec) }

// blank zeroes the frames of fp (starting at offset seconds) inside each
// span, so they can't match anything.
func blank(fp []uint32, offset float64, spans ...Span) []uint32 {
	out := append([]uint32(nil), fp...)
	for _, s := range spans {
		if s.End <= s.Start {
			continue
		}
		lo := max(0, frames(s.Start-offset))
		hi := min(len(out), frames(s.End-offset)+1)
		for i := lo; i < hi; i++ {
			out[i] = 0
		}
	}
	return out
}

// FindIntro returns the longest shared stretch (15 s to 3 min) between the
// heads of two episodes, in a's time. Both heads start at 0.
func FindIntro(a, b []uint32) (Span, Span, bool) {
	var best Run
	for _, r := range Matches(a, b, frames(15)) {
		if r.Len() > best.Len() && r.Len() <= frames(180) {
			best = r
		}
	}
	if best.Len() == 0 {
		return Span{}, Span{}, false
	}
	ia := Span{sec(best.AStart, 0), sec(best.AEnd, 0)}
	ib := Span{sec(best.BStart, 0), sec(best.BStart+best.Len(), 0)}
	return ia, ib, true
}

// FindCredits returns the shared stretch closest to the end of two tails.
// aOff/bOff are the tails' start times; aDur is a's duration.
func FindCredits(a []uint32, aOff, aDur float64, b []uint32, bOff float64) (Span, bool) {
	var best Run
	found := false
	for _, r := range Matches(a, b, frames(15)) {
		// Shared audio near the end; take the one that starts latest-but-long.
		if !found || r.AEnd > best.AEnd || (r.AEnd == best.AEnd && r.Len() > best.Len()) {
			best, found = r, true
		}
	}
	if !found {
		return Span{}, false
	}
	s := Span{sec(best.AStart, aOff), sec(best.AEnd, aOff)}
	if aDur > 0 && aDur-s.End < 15 {
		s.End = aDur
	}
	return s, true
}

// Ref is an earlier episode a recap may quote: its full fingerprint and the
// parts of it that are not story (its own intro, recap and credits).
type Ref struct {
	FP      []uint32
	Exclude []Span
}

// FindRecap looks for a "previously on" segment at the start of an episode.
// head is the episode's first minutes (starting at 0); intro, if known, is
// its intro. A recap is a cluster of clips, early in the episode, whose
// audio also appears in one of the earlier episodes.
func FindRecap(head []uint32, intro *Span, refs []Ref) (Span, bool) {
	const searchSec = 8 * 60
	q := head
	if len(q) > frames(searchSec) {
		q = q[:frames(searchSec)]
	}
	if intro != nil {
		q = blank(q, 0, *intro)
	}
	var hits []Span
	for _, ref := range refs {
		b := blank(ref.FP, 0, ref.Exclude...)
		for _, r := range Matches(q, b, frames(2)) {
			hits = append(hits, Span{sec(r.AStart, 0), sec(r.AEnd, 0)})
		}
	}
	if len(hits) == 0 {
		return Span{}, false
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Start < hits[j].Start })
	// Cluster clips separated by short gaps (narration, clips from episodes
	// we didn't compare against).
	type cluster struct {
		Span
		matched float64
		clips   int
	}
	var cl []cluster
	for _, h := range hits {
		if n := len(cl); n > 0 && h.Start-cl[n-1].End <= 12 {
			c := &cl[n-1]
			if h.End > c.End {
				c.matched += h.End - max(h.Start, c.End)
				c.End = h.End
			}
			c.clips++
			continue
		}
		cl = append(cl, cluster{Span: h, matched: h.Len(), clips: 1})
	}
	introEnd := -1.0
	if intro != nil {
		introEnd = intro.End
	}
	for _, c := range cl {
		early := c.Start <= 120 || (introEnd > 0 && c.Start >= introEnd-1 && c.Start-introEnd <= 30)
		if !early {
			continue
		}
		if c.Len() < 20 || c.matched < 12 || c.matched < 0.4*c.Len() {
			continue
		}
		if c.clips < 2 && c.Len() < 30 {
			continue
		}
		s := c.Span
		// The "Previously on…" line before the first clip belongs to it.
		if introEnd > 0 && s.Start >= introEnd-1 && s.Start-introEnd <= 15 {
			s.Start = introEnd
		} else if s.Start <= 15 {
			s.Start = 0
		}
		if intro != nil && intro.Start >= s.End && intro.Start-s.End <= 20 {
			s.End = intro.Start
		}
		if s.Len() > 5*60 {
			s.End = s.Start + 5*60
		}
		return s, true
	}
	return Span{}, false
}
