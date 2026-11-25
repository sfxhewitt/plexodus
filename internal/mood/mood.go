// Package mood gives every film a mood profile and picks films from it.
//
// It is Reel Pick's recommender, built into the library: each film has thirteen
// scores from 0 to 1 (how uplifting, intense, dark, funny... it is). About ten
// thousand well-known films carry scores worked out ahead of time
// (catalog.json.gz, keyed by TMDB id); every other film gets an estimate from
// its genres. On top of the scores sit the mood presets ("Feel-good",
// "Mind-bender"), the content flags, and the ranking in reco.go.
package mood

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"sync"
)

// The dimensions, in the order they are stored. V and A are valence and
// arousal on Russell's circumplex (how pleasant, how energetic).
const (
	V = iota
	A
	Dark
	Humour
	Complex
	Pace
	Weight
	Comfort
	Violence
	Scary
	Sex
	Family
	Romance
	NDims
)

// Columns are the item_moods column names, one per dimension.
var Columns = [NDims]string{"v", "a", "dark", "humour", "complex", "pace", "weight", "comfort", "violence", "scary", "sex", "family", "romance"}

// Dims is one film's scores, 0 to 1.
type Dims [NDims]float64

// nAffect dimensions describe how a film feels; the rest are content flags.
const nAffect = 8

// DimIndex finds a dimension by its column name.
func DimIndex(name string) (int, bool) {
	for i, c := range Columns {
		if c == name {
			return i, true
		}
	}
	return 0, false
}

//go:embed catalog.json.gz
var catalogGz []byte

type entry struct {
	Votes  int
	D      Dims
	Kw     []int32
	Scored bool
}

var (
	catOnce  sync.Once
	catalog  map[int]entry
	keywords []string
)

func loadCatalog() {
	catOnce.Do(func() {
		catalog = map[int]entry{}
		zr, err := gzip.NewReader(bytes.NewReader(catalogGz))
		if err != nil {
			return
		}
		var raw struct {
			Keywords []string            `json:"keywords"`
			Films    [][]json.RawMessage `json:"films"`
		}
		if json.NewDecoder(zr).Decode(&raw) != nil {
			return
		}
		keywords = raw.Keywords
		for _, f := range raw.Films {
			if len(f) < 5 {
				continue
			}
			var id, votes, scored int
			var d []int
			var kw []int32
			json.Unmarshal(f[0], &id)
			json.Unmarshal(f[1], &votes)
			json.Unmarshal(f[2], &d)
			json.Unmarshal(f[3], &kw)
			json.Unmarshal(f[4], &scored)
			if len(d) != NDims {
				continue
			}
			e := entry{Votes: votes, Kw: kw, Scored: scored == 1}
			for i, x := range d {
				e.D[i] = float64(x) / 100
			}
			catalog[id] = e
		}
	})
}

// Lookup returns the scored profile for a TMDB film id, if the catalogue has it.
func Lookup(tmdbID int) (d Dims, votes int, kw []int32, ok bool) {
	loadCatalog()
	e, ok := catalog[tmdbID]
	return e.D, e.Votes, e.Kw, ok
}

// CatalogSize is how many films carry worked-out scores.
func CatalogSize() int {
	loadCatalog()
	return len(catalog)
}

// genreAlias maps TMDB's genre names onto the short ones the profiles use.
var genreAlias = map[string]string{"Science Fiction": "Sci-Fi"}

func normGenre(g string) string {
	if a, ok := genreAlias[g]; ok {
		return a
	}
	return g
}

// Genre priors for films with no worked-out scores.
var genrePriors = map[string]map[int]float64{
	"Comedy":      {V: .75, Humour: .9, Comfort: .65, Dark: .2, A: .4},
	"Family":      {V: .8, Family: .95, Comfort: .75, Dark: .1, Violence: .05, Sex: .02},
	"Animation":   {V: .75, Family: .8, Comfort: .7, Humour: .55},
	"Romance":     {V: .65, Romance: .95, Comfort: .6, Weight: .45},
	"Music":       {V: .7, Comfort: .6},
	"Musical":     {V: .7, Comfort: .6},
	"Drama":       {Weight: .55, Complex: .5, Pace: .3, V: .45},
	"Thriller":    {A: .85, Dark: .65, V: .35, Pace: .7, Complex: .55, Comfort: .15},
	"Crime":       {Dark: .75, Violence: .6, A: .65, V: .35},
	"Horror":      {Scary: .95, A: .85, Dark: .85, V: .15, Violence: .7, Comfort: .05, Family: .02},
	"Mystery":     {Complex: .75, A: .6, Dark: .5},
	"Sci-Fi":      {Complex: .65, A: .65},
	"Action":      {A: .9, Pace: .9, Violence: .6, V: .6},
	"Adventure":   {A: .7, Pace: .7, V: .7, Family: .5},
	"War":         {Dark: .85, Violence: .8, Weight: .7, V: .2, A: .7},
	"History":     {Weight: .55, Complex: .55, Pace: .3},
	"Western":     {Violence: .55, Pace: .45},
	"Fantasy":     {V: .65, A: .6, Family: .5},
	"Documentary": {Complex: .55, Pace: .3, Humour: .1},
}

var basePrior = Dims{V: .5, A: .5, Dark: .35, Humour: .2, Complex: .4, Pace: .5, Weight: .3, Comfort: .35, Violence: .25, Scary: .1, Sex: .15, Family: .3, Romance: .2}

// Heuristic estimates a profile from genres: the mean of each genre's prior.
func Heuristic(genres []string) Dims {
	var sum Dims
	var n [NDims]int
	for _, g := range genres {
		for d, x := range genrePriors[normGenre(g)] {
			sum[d] += x
			n[d]++
		}
	}
	out := basePrior
	for d := range out {
		if n[d] > 0 {
			out[d] = sum[d] / float64(n[d])
		}
	}
	return out
}

// For returns a film's profile: worked out if the catalogue knows its TMDB
// id, otherwise estimated from its genres.
func For(tmdbID int, genres []string) (d Dims, scored bool) {
	if tmdbID > 0 {
		if d, _, _, ok := Lookup(tmdbID); ok {
			return d, true
		}
	}
	return Heuristic(genres), false
}

// Range is the band of one dimension a mood needs.
type Range struct {
	Dim    int
	Lo, Hi float64
}

// Preset is a mood: a label and the bands a film has to sit in.
type Preset struct {
	Key, Label, Sub string
	R               []Range
}

// Moods in the order they are offered.
var Moods = []Preset{
	{"fg", "Feel-good", "Warm & uplifting", []Range{{V, .62, 1}, {Dark, 0, .4}, {Comfort, .45, 1}}},
	{"th", "Thriller", "Edge of your seat", []Range{{A, .62, 1}, {Dark, .45, 1}, {Pace, .45, 1}}},
	{"cr", "Have a cry", "Bring tissues", []Range{{Weight, .6, 1}}},
	{"la", "Laugh", "Proper funny", []Range{{Humour, .65, 1}, {Dark, 0, .55}}},
	{"mb", "Mind-bender", "Twisty & clever", []Range{{Complex, .65, 1}}},
	{"ch", "Chill", "Cosy, low effort", []Range{{Comfort, .6, 1}, {A, 0, .5}, {Complex, 0, .5}}},
	{"ad", "Adrenaline", "Big, loud, fast", []Range{{A, .7, 1}, {Pace, .68, 1}}},
}

// Mood finds a preset by key.
func Mood(key string) (Preset, bool) {
	for _, m := range Moods {
		if m.Key == key {
			return m, true
		}
	}
	return Preset{}, false
}

// Fits reports whether a profile sits inside every band of the mood.
func (p Preset) Fits(d Dims) bool {
	for _, r := range p.R {
		if d[r.Dim] < r.Lo || d[r.Dim] > r.Hi {
			return false
		}
	}
	return true
}

// Target is where in each band a best match would sit.
func (p Preset) Target() Target {
	t := Target{}
	for _, r := range p.R {
		switch {
		case r.Lo == 0:
			t[r.Dim] = r.Hi / 2
		case r.Hi == 1:
			t[r.Dim] = (r.Lo + 1) / 2
		default:
			t[r.Dim] = (r.Lo + r.Hi) / 2
		}
	}
	return t
}

// MoodsOf lists the labels of every mood a profile fits.
func MoodsOf(d Dims) []string {
	out := []string{}
	for _, m := range Moods {
		if m.Fits(d) {
			out = append(out, m.Label)
		}
	}
	return out
}

// Flag is a content check ("No scares").
type Flag struct {
	Key, Label string
	Dim        int
	Below      bool // true: the score must be under At; false: at least At
	At         float64
}

// Flags in the order they are offered.
var Flags = []Flag{
	{"noviolence", "No graphic violence", Violence, true, .5},
	{"noscares", "No scares", Scary, true, .4},
	{"family", "Family-friendly", Family, false, .6},
	{"nosex", "No explicit sex", Sex, true, .4},
	{"romance", "Romance", Romance, false, .6},
}

// FlagByKey finds a content flag.
func FlagByKey(key string) (Flag, bool) {
	for _, f := range Flags {
		if f.Key == key {
			return f, true
		}
	}
	return Flag{}, false
}

// Test reports whether a profile passes the flag.
func (f Flag) Test(d Dims) bool {
	if f.Below {
		return d[f.Dim] < f.At
	}
	return d[f.Dim] >= f.At
}

// Feelings are the quiz's "how are you feeling" answers, and where a film
// should sit to match that mood or to change it (Zillmann's mood management:
// people pick films to sustain a good mood or repair a bad one).
type Feeling struct {
	Key, Label    string
	Match, Repair Target
}

var Feelings = []Feeling{
	{"stressed", "Stressed / wired", Target{A: .85, Dark: .6, Pace: .75}, Target{A: .25, V: .85, Comfort: .8, Dark: .2, Complex: .3}},
	{"low", "Low / sad", Target{Weight: .75, V: .3}, Target{V: .9, Humour: .6, Weight: .3, Dark: .2}},
	{"bored", "Bored / flat", Target{A: .3, Pace: .3, Complex: .55}, Target{A: .85, Pace: .85, Complex: .6}},
	{"happy", "Happy / buzzing", Target{V: .85, A: .7, Humour: .6}, Target{V: .8, A: .65, Humour: .55}},
	{"calm", "Calm / content", Target{Comfort: .8, A: .25, V: .75}, Target{A: .8, Pace: .8, V: .65}},
	{"tired", "Tired", Target{Comfort: .8, Complex: .2, A: .3}, Target{Humour: .7, Pace: .65, Complex: .25, V: .8}},
}

// FeelingByKey finds a quiz feeling.
func FeelingByKey(key string) (Feeling, bool) {
	for _, f := range Feelings {
		if f.Key == key {
			return f, true
		}
	}
	return Feeling{}, false
}
