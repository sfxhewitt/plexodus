package mood

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// The ranking, as in Reel Pick:
//   - how close a film sits to the target mood (mean distance over the asked dimensions),
//   - a Bayesian weighted rating (IMDb's formula) so ten votes of 9.5 don't beat a classic,
//   - similarity to the viewer's taste profile (Rocchio feedback over likes and skips),
//   - MMR (Carbonell & Goldstein) so a list of picks isn't five films of one kind,
//   - and a sentence saying why, built from the terms that drove the score.

// Target is the score wanted on some dimensions.
type Target map[int]float64

// Merge lays other over t and returns t.
func (t Target) Merge(other Target) Target {
	for k, v := range other {
		t[k] = v
	}
	return t
}

// Film is one film as the ranking sees it.
type Film struct {
	ID      int64
	Title   string
	Year    int
	Runtime int
	Rating  float64
	Votes   int // 0 = not known
	Genres  []string
	Kw      []int32 // catalogue keyword numbers
	D       Dims
	Scored  bool

	WR float64 // weighted rating, set by NewIndex
	tf map[string]float64
}

// unknownVotes stands in for films whose vote count isn't known.
const unknownVotes = 300

func (f *Film) votes() float64 {
	if f.Votes > 0 {
		return float64(f.Votes)
	}
	return unknownVotes
}

// Index holds every film with the library-wide numbers the ranking needs.
type Index struct {
	Films []*Film
	byID  map[int64]*Film
	idf   map[string]float64
	n     int
	m, c  float64 // Bayesian prior: strength and mean
}

func tokens(f *Film) []string {
	loadCatalog()
	t := make([]string, 0, len(f.Genres)+len(f.Kw))
	for _, g := range f.Genres {
		t = append(t, "g:"+normGenre(g))
	}
	for _, k := range f.Kw {
		if int(k) < len(keywords) {
			t = append(t, "k:"+keywords[k])
		}
	}
	return t
}

// NewIndex works out the TF-IDF vectors and weighted ratings for a set of films.
func NewIndex(films []*Film) *Index {
	ix := &Index{Films: films, byID: make(map[int64]*Film, len(films)), idf: map[string]float64{}, n: len(films)}
	df := map[string]int{}
	votes := make([]float64, 0, len(films))
	var tv, tr float64
	for _, f := range films {
		ix.byID[f.ID] = f
		seen := map[string]bool{}
		for _, t := range tokens(f) {
			if !seen[t] {
				seen[t] = true
				df[t]++
			}
		}
		v := f.votes()
		votes = append(votes, v)
		tv += v
		tr += f.Rating * v
	}
	n := float64(max(len(films), 1))
	for t, d := range df {
		ix.idf[t] = math.Log((n+1)/(float64(d)+1)) + 1
	}
	sort.Float64s(votes)
	ix.m = 50
	if len(votes) > 0 {
		ix.m = math.Max(50, votes[len(votes)/4])
	}
	ix.c = 6.8
	if tv > 0 {
		ix.c = tr / tv
	}
	for _, f := range films {
		f.tf = ix.vec(tokens(f))
		v := f.votes()
		f.WR = v/(v+ix.m)*f.Rating + ix.m/(v+ix.m)*ix.c
		if f.Rating == 0 {
			f.WR = 0
		}
	}
	return ix
}

func (ix *Index) vec(toks []string) map[string]float64 {
	m := map[string]float64{}
	for _, t := range toks {
		idf := ix.idf[t]
		if idf == 0 {
			idf = math.Log(float64(ix.n)+1) + 1
		}
		w := 1.0
		if t[0] == 'g' {
			w = 0.6
		}
		m[t] += idf * w
	}
	var n float64
	for _, v := range m {
		n += v * v
	}
	n = math.Sqrt(n)
	if n == 0 {
		n = 1
	}
	for k, v := range m {
		m[k] = v / n
	}
	return m
}

// Get finds a film by item id.
func (ix *Index) Get(id int64) *Film { return ix.byID[id] }

func affect(f *Film) [nAffect]float64 {
	var a [nAffect]float64
	for i := range a {
		a[i] = f.D[i] - 0.5
	}
	return a
}

func cosDense(a, b [nAffect]float64) float64 {
	var s, na, nb float64
	for i := range a {
		s += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return s / math.Sqrt(na*nb)
}

func cosSparse(a, b map[string]float64) float64 {
	if len(a) > len(b) {
		a, b = b, a
	}
	var s float64
	for k, v := range a {
		s += v * b[k]
	}
	return s
}

const wAffect, wText = 0.55, 0.45

// Similarity of two films, 0 to 1 or so: how they feel plus what they're about.
func Similarity(a, b *Film) float64 {
	return wAffect*cosDense(affect(a), affect(b)) + wText*cosSparse(a.tf, b.tf)
}

// Event is one piece of feedback on a film; W is its weight (like 1,
// watchlist 1, dislike -1, not interested -1, each skip -0.3).
type Event struct {
	ID int64
	W  float64
}

// Profile is the viewer's taste: where liked films sit, minus disliked ones.
type Profile struct {
	a     [nAffect]float64
	t     map[string]float64
	liked []*Film
}

const beta, gamma = 0.75, 0.25

// Rocchio builds a taste profile from feedback, or nil when there is none.
func (ix *Index) Rocchio(events []Event) *Profile {
	type side struct {
		a [nAffect]float64
		t map[string]float64
		n float64
	}
	pos, neg := side{t: map[string]float64{}}, side{t: map[string]float64{}}
	p := &Profile{t: map[string]float64{}}
	for _, e := range events {
		f := ix.byID[e.ID]
		if f == nil || e.W == 0 {
			continue
		}
		tgt, aw := &pos, e.W
		if e.W < 0 {
			tgt, aw = &neg, -e.W
		} else {
			p.liked = append(p.liked, f)
		}
		av := affect(f)
		for i, x := range av {
			tgt.a[i] += aw * x
		}
		for k, v := range f.tf {
			tgt.t[k] += aw * v
		}
		tgt.n += aw
	}
	if pos.n == 0 && neg.n == 0 {
		return nil
	}
	for i := range p.a {
		if pos.n > 0 {
			p.a[i] += beta * pos.a[i] / pos.n
		}
		if neg.n > 0 {
			p.a[i] -= gamma * neg.a[i] / neg.n
		}
	}
	for k, v := range pos.t {
		p.t[k] = beta * v / pos.n
	}
	for k, v := range neg.t {
		p.t[k] -= gamma * v / neg.n
	}
	var n float64
	for _, v := range p.t {
		n += v * v
	}
	if n = math.Sqrt(n); n == 0 {
		n = 1
	}
	for k, v := range p.t {
		p.t[k] = v / n
	}
	return p
}

func (p *Profile) sim(f *Film) float64 {
	if p == nil {
		return 0
	}
	return wAffect*cosDense(affect(f), p.a) + wText*cosSparse(f.tf, p.t)
}

// Ctx is what a ranking is for.
type Ctx struct {
	Target    Target
	Profile   *Profile
	Prefer    string // "classic" (safe bet), "gem" (something different) or ""
	MoodLabel string // "Feel-good", "your sliders"
	Lead      string // opening clause of the explanation (the quiz's mood line)
}

type term struct {
	dim  int
	v, d float64
}

// Ranked is a film with its score and what drove it.
type Ranked struct {
	F     *Film
	Score float64
	Fit   float64
	terms []term
	ps    float64
}

func (c *Ctx) relevance(f *Film) Ranked {
	r := Ranked{F: f, Fit: 0.5}
	if len(c.Target) > 0 {
		var sum float64
		for k, t := range c.Target {
			d := math.Abs(f.D[k] - t)
			sum += d
			r.terms = append(r.terms, term{k, f.D[k], d})
		}
		r.Fit = 1 - sum/float64(len(c.Target))
	}
	q := math.Max(0, math.Min(1, (f.WR-5)/3.5))
	r.ps = c.Profile.sim(f)
	qualW, gem := 0.25, 0.0
	switch c.Prefer {
	case "classic":
		qualW = 0.4
	case "gem":
		qualW = 0.1
		if f.Votes < 3000 && f.WR > 6.8 {
			gem = 0.15
		}
	}
	tw := 0.15
	if len(c.Target) > 0 {
		tw = 0.5
	}
	r.Score = tw*r.Fit + qualW*q + gem
	if c.Profile != nil {
		r.Score += 0.2 * (r.ps + 1) / 2
	}
	return r
}

// Rank scores every film and sorts best first (ties by id, so pages are stable).
func (c *Ctx) Rank(films []*Film) []Ranked {
	out := make([]Ranked, len(films))
	for i, f := range films {
		out[i] = c.relevance(f)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].F.ID < out[j].F.ID
	})
	return out
}

// MMR picks k films from the top `pool` of a ranking, trading a little score
// for variety: each pick is penalised by how like the earlier picks it is.
func MMR(ranked []Ranked, k int, lambda float64, pool int) []Ranked {
	if pool > len(ranked) {
		pool = len(ranked)
	}
	cand := append([]Ranked(nil), ranked[:pool]...)
	out := []Ranked{}
	if len(cand) == 0 {
		return out
	}
	lo, hi := cand[len(cand)-1].Score, cand[0].Score
	norm := func(s float64) float64 {
		if hi > lo {
			return (s - lo) / (hi - lo)
		}
		return 1
	}
	maxSim := make([]float64, len(cand))
	for len(out) < k && len(cand) > 0 {
		best, bestV := 0, math.Inf(-1)
		for i := range cand {
			if v := lambda*norm(cand[i].Score) - (1-lambda)*maxSim[i]; v > bestV {
				bestV, best = v, i
			}
		}
		pick := cand[best]
		out = append(out, pick)
		cand = append(cand[:best], cand[best+1:]...)
		maxSim = append(maxSim[:best], maxSim[best+1:]...)
		for i := range cand {
			if s := Similarity(cand[i].F, pick.F); s > maxSim[i] {
				maxSim[i] = s
			}
		}
	}
	return out
}

var words = map[int][2]string{
	V: {"bleak", "uplifting"}, A: {"low-key", "high-intensity"}, Dark: {"light in tone", "dark"},
	Humour: {"not played for laughs", "funny"}, Complex: {"easy to follow", "twisty and demanding"},
	Pace: {"slow-burn", "fast-paced"}, Weight: {"dry-eyed", "a tearjerker"}, Comfort: {"challenging", "comforting"},
	Family: {"grown-up", "family-friendly"},
}

func describe(dim int, v float64) string {
	w, ok := words[dim]
	if !ok {
		return ""
	}
	if v >= 0.5 {
		return fmt.Sprintf("%s (%d%%)", w[1], int(math.Round(v*100)))
	}
	return fmt.Sprintf("%s (%d%%)", w[0], int(math.Round((1-v)*100)))
}

// Why says in a sentence what put this film in front of the viewer.
func (c *Ctx) Why(r Ranked) string {
	var parts []string
	if c.Lead != "" {
		parts = append(parts, c.Lead)
	}
	good := []term{}
	for _, t := range r.terms {
		if t.d < 0.2 {
			good = append(good, t)
		}
	}
	sort.Slice(good, func(i, j int) bool {
		if good[i].d != good[j].d {
			return good[i].d < good[j].d
		}
		return good[i].dim < good[j].dim
	})
	var desc []string
	for _, t := range good {
		if s := describe(t.dim, t.v); s != "" && len(desc) < 2 {
			desc = append(desc, s)
		}
	}
	if len(desc) > 0 {
		lead := "Matches what you asked for: "
		if c.MoodLabel != "" {
			lead = "Fits " + strings.ToLower(c.MoodLabel) + ": "
		}
		parts = append(parts, lead+strings.Join(desc, ", "))
	}
	if c.Profile != nil && len(c.Profile.liked) > 0 {
		var best *Film
		bs := 0.62
		for _, l := range c.Profile.liked {
			if l.ID == r.F.ID {
				continue
			}
			if s := Similarity(r.F, l); s > bs {
				bs, best = s, l
			}
		}
		if best != nil {
			parts = append(parts, "similar to "+best.Title+", which you liked")
		} else if r.ps > 0.35 {
			parts = append(parts, "close to your taste")
		}
	}
	if r.F.WR > 0 {
		if r.F.Votes > 0 {
			parts = append(parts, fmt.Sprintf("%.1f weighted rating from %s votes", r.F.WR, fmtVotes(r.F.Votes)))
		} else {
			parts = append(parts, fmt.Sprintf("rated %.1f", r.F.Rating))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	s := strings.Join(parts, "; ")
	return strings.ToUpper(s[:1]) + s[1:] + "."
}

func fmtVotes(n int) string {
	switch {
	case n >= 10000:
		return fmt.Sprintf("%.0fk", float64(n)/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

// MatchPct turns a score into the "87% match" shown on a card, against the
// best score in the list.
func MatchPct(score, best float64) int {
	p := int(math.Round(55 + 44*score/math.Max(best, .01)))
	return max(40, min(99, p))
}
