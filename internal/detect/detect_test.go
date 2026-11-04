package detect

import (
	"math"
	"math/rand"
	"testing"
)

// synth makes deterministic, music-like audio: a few tones that change
// every quarter second, plus a little noise.
func synth(seed int64, seconds float64) []int16 {
	r := rand.New(rand.NewSource(seed))
	n := int(seconds * SampleRate)
	out := make([]int16, n)
	step := SampleRate / 4
	var freqs [3]float64
	var amps [3]float64
	for i := 0; i < n; i++ {
		if i%step == 0 {
			for k := range freqs {
				freqs[k] = 200 + r.Float64()*1800
				amps[k] = 1500 + r.Float64()*4000
			}
		}
		t := float64(i) / SampleRate
		v := r.NormFloat64() * 200
		for k := range freqs {
			v += amps[k] * math.Sin(2*math.Pi*freqs[k]*t)
		}
		out[i] = int16(max(-32767, min(32767, v)))
	}
	return out
}

// degrade simulates a different encode: gain change and added noise.
func degrade(s []int16, seed int64) []int16 {
	r := rand.New(rand.NewSource(seed))
	out := make([]int16, len(s))
	for i, v := range s {
		x := float64(v)*0.8 + r.NormFloat64()*300
		out[i] = int16(max(-32767, min(32767, x)))
	}
	return out
}

func secs(s float64) int { return int(s * SampleRate) }

func cat(parts ...[]int16) []int16 {
	var out []int16
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func fpOf(s []int16) []uint32 {
	f := NewFingerprinter()
	f.Samples(s)
	return f.Result()
}

func near(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.2f, want %.2f ± %.1f", what, got, want, tol)
	}
}

func TestFindIntro(t *testing.T) {
	intro := synth(1, 40)
	a := cat(synth(10, 12.3), intro, synth(11, 200))
	b := degrade(cat(synth(20, 31.7), intro, synth(21, 200)), 5)
	ia, ib, ok := FindIntro(fpOf(a), fpOf(b))
	if !ok {
		t.Fatal("no intro found")
	}
	near(t, "intro start", ia.Start, 12.3, 1.5)
	near(t, "intro end", ia.End, 52.3, 1.5)
	near(t, "other intro start", ib.Start, 31.7, 1.5)
}

func TestNoIntroInUnrelatedAudio(t *testing.T) {
	if _, _, ok := FindIntro(fpOf(synth(1, 120)), fpOf(synth(2, 120))); ok {
		t.Fatal("found an intro in unrelated audio")
	}
}

func TestFindCredits(t *testing.T) {
	credits := synth(3, 50)
	a := cat(synth(30, 300), credits)
	b := degrade(cat(synth(31, 280), credits, synth(32, 8)), 6)
	fa, fb := fpOf(a), fpOf(b)
	cs, ok := FindCredits(fa, 1000, 1000+float64(len(a))/SampleRate, fb, 2000)
	if !ok {
		t.Fatal("no credits found")
	}
	near(t, "credits start", cs.Start, 1300, 1.5)
	near(t, "credits end", cs.End, 1350, 0.5)
}

// An earlier episode, its intro, and a new episode that opens with clips
// from it ("Previously on…") before the shared intro.
func recapFixture() (prev []int16, prevIntro Span, intro []int16) {
	intro = synth(1, 40)
	prev = cat(synth(100, 20), intro, synth(101, 540))
	return prev, Span{20, 60}, intro
}

func TestFindRecap(t *testing.T) {
	prev, prevIntro, intro := recapFixture()
	// Five clips of the previous episode, with narration between them.
	clip := func(at, length float64) []int16 { return prev[secs(at):secs(at+length)] }
	recap := cat(synth(200, 2.1), clip(80, 6), synth(201, 0.7), clip(150.3, 5), clip(300, 7.4),
		synth(202, 1.2), clip(420.1, 6), clip(590, 5.5))
	recapLen := float64(len(recap)) / SampleRate
	ep := degrade(cat(recap, synth(203, 1.5), intro, synth(204, 300)), 9)
	introSpan := Span{recapLen + 1.5, recapLen + 41.5}

	rs, ok := FindRecap(fpOf(ep), &introSpan, []Ref{{FP: fpOf(prev), Exclude: []Span{prevIntro}}})
	if !ok {
		t.Fatal("no recap found")
	}
	near(t, "recap start", rs.Start, 0, 0.01)
	near(t, "recap end", rs.End, introSpan.Start, 0.01)
}

func TestNoRecapWhenOnlyTheIntroIsShared(t *testing.T) {
	prev, prevIntro, intro := recapFixture()
	ep := degrade(cat(synth(300, 45), intro, synth(301, 300)), 9)
	introSpan := Span{45, 85}
	if rs, ok := FindRecap(fpOf(ep), &introSpan, []Ref{{FP: fpOf(prev), Exclude: []Span{prevIntro}}}); ok {
		t.Fatalf("found a recap %v in an episode without one", rs)
	}
	// Even if the intro is unknown, a single shared stretch isn't a recap
	// when it's excluded on the earlier episode's side.
	if rs, ok := FindRecap(fpOf(ep), nil, []Ref{{FP: fpOf(prev), Exclude: []Span{prevIntro}}}); ok {
		t.Fatalf("found a recap %v from the intro", rs)
	}
}

func TestFingerprintRoundTrip(t *testing.T) {
	fp := fpOf(synth(7, 5))
	got := Decode(Encode(fp))
	if len(got) != len(fp) {
		t.Fatalf("len %d, want %d", len(got), len(fp))
	}
	for i := range fp {
		if got[i] != fp[i] {
			t.Fatalf("frame %d differs", i)
		}
	}
}

func TestClassifyChapter(t *testing.T) {
	cases := map[string]string{
		"Recap":               "recap",
		"Previously on Lost":  "recap",
		"Previously…":         "recap",
		"Intro":               "intro",
		"Opening Credits":     "intro",
		"OP":                  "intro",
		"End Credits":         "credits",
		"Ending":              "credits",
		"ED":                  "credits",
		"Chapter 01":          "",
		"Ed's Story":          "",
		"Opening Night":       "",
		"The Story Continues": "",
		"":                    "",
	}
	for in, want := range cases {
		if got := ClassifyChapter(in); got != want {
			t.Errorf("ClassifyChapter(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChapterMarkers(t *testing.T) {
	cs := []Chapter{
		{"Recap", 0, 45}, {"Opening", 45, 90}, {"Chapter 3", 90, 1500},
		{"End Credits", 1500, 1560}, {"Credits", 1560, 1580},
	}
	m := ChapterMarkers(cs, 1580)
	if m["recap"] != (Span{0, 45}) || m["intro"] != (Span{45, 90}) || m["credits"] != (Span{1500, 1580}) {
		t.Fatalf("got %v", m)
	}
	// A "credits" chapter at the start is not end credits.
	if m := ChapterMarkers([]Chapter{{"Credits", 0, 60}, {"Main", 60, 1500}}, 1500); len(m) != 0 {
		t.Fatalf("got %v", m)
	}
}
