// Package ask turns a plain-English request such as "90s heist films under
// two hours I haven't seen" into a structured library query. Claude does the
// translation when an Anthropic API key is set; otherwise a small built-in
// parser handles the common patterns (decades, runtimes, genres, unwatched).
package ask

import (
	"regexp"
	"strconv"
	"strings"
)

// Query is the structured form of a request. Zero values mean "no filter".
type Query struct {
	// Kinds is any of "movie", "show"; empty means both.
	Kinds []string `json:"kinds"`
	// Genres: a title must have at least one of these (library genre names).
	Genres []string `json:"genres"`
	// ExcludeGenres: a title must have none of these.
	ExcludeGenres []string `json:"excludeGenres"`
	YearFrom      int      `json:"yearFrom"`
	YearTo        int      `json:"yearTo"`
	MinRuntime    int      `json:"minRuntime"` // minutes, films only
	MaxRuntime    int      `json:"maxRuntime"`
	MinRating     float64  `json:"minRating"` // 0-10
	// Watched is "any", "unwatched", "watched" or "inprogress".
	Watched string `json:"watched"`
	// Keywords are matched (any of them) against titles and plot summaries.
	Keywords []string `json:"keywords"`
	// SimilarTo names a title in the request ("something like Severance").
	SimilarTo string `json:"similarTo"`
	// Titles are specific suggestions that fit the request; the server shows
	// the ones that are in the library first.
	Titles []Title `json:"titles"`
	// Sort is "relevance", "rating", "newest", "oldest", "added" or "random".
	Sort string `json:"sort"`
	// Summary restates the request in a few words, for the results header.
	Summary string `json:"summary"`
}

type Title struct {
	Title string `json:"title"`
	Year  int    `json:"year"`
}

var (
	reDecade    = regexp.MustCompile(`\b(?:(19|20)?([0-9])0'?s)\b`)
	reYearRange = regexp.MustCompile(`\b(19[0-9]{2}|20[0-9]{2})\s*(?:-|–|to|and)\s*(19[0-9]{2}|20[0-9]{2})\b`)
	reAfter     = regexp.MustCompile(`\b(?:after|since|from|newer than|post)\s+(19[0-9]{2}|20[0-9]{2})\b`)
	reBefore    = regexp.MustCompile(`\b(?:before|pre|older than|until)\s+(19[0-9]{2}|20[0-9]{2})\b`)
	reYear      = regexp.MustCompile(`\b(?:in|from)\s+(19[0-9]{2}|20[0-9]{2})\b`)
	reUnder     = regexp.MustCompile(`\b(?:under|less than|shorter than|below|max(?:imum)?|at most|no more than|within)\s+(an?|one|two|three|1|2|3|[0-9]+(?:\.[0-9]+)?|half an?)\s*(hours?|hrs?|h|minutes?|mins?|m)\b`)
	reOver      = regexp.MustCompile(`\b(?:over|more than|longer than|at least)\s+(an?|one|two|three|1|2|3|[0-9]+(?:\.[0-9]+)?)\s*(hours?|hrs?|h|minutes?|mins?|m)\b`)
	reRating    = regexp.MustCompile(`\b(?:rated|rating)\s+(?:over|above|at least)?\s*([0-9](?:\.[0-9])?)\b`)
	reLike      = regexp.MustCompile(`\b(?:like|similar to|in the style of|reminds? me of)\s+(.+)$`)
)

var numWords = map[string]float64{"a": 1, "an": 1, "one": 1, "two": 2, "three": 3, "half a": 0.5, "half an": 0.5}

func amount(n, unit string) int {
	v, ok := numWords[n]
	if !ok {
		v, _ = strconv.ParseFloat(n, 64)
	}
	if strings.HasPrefix(unit, "h") {
		return int(v * 60)
	}
	return int(v)
}

// genreSynonyms maps everyday words to common TMDB/Plex genre names.
var genreSynonyms = map[string][]string{
	"scary": {"Horror"}, "horror": {"Horror"}, "spooky": {"Horror"}, "slasher": {"Horror"},
	"funny": {"Comedy"}, "comedy": {"Comedy"}, "comedies": {"Comedy"}, "sitcom": {"Comedy"}, "sitcoms": {"Comedy"},
	"romcom": {"Romance", "Comedy"}, "romantic": {"Romance"}, "romance": {"Romance"}, "love": {"Romance"},
	"scifi": {"Science Fiction", "Sci-Fi", "Sci-Fi & Fantasy"}, "sci-fi": {"Science Fiction", "Sci-Fi", "Sci-Fi & Fantasy"},
	"science": {"Science Fiction", "Sci-Fi"}, "space": {"Science Fiction", "Sci-Fi"},
	"fantasy": {"Fantasy", "Sci-Fi & Fantasy"}, "action": {"Action", "Action & Adventure"},
	"adventure": {"Adventure", "Action & Adventure"}, "thriller": {"Thriller"}, "thrillers": {"Thriller"},
	"suspense": {"Thriller", "Suspense"}, "crime": {"Crime"}, "gangster": {"Crime"}, "mafia": {"Crime"},
	"mystery": {"Mystery"}, "mysteries": {"Mystery"}, "whodunit": {"Mystery"}, "detective": {"Mystery", "Crime"},
	"drama": {"Drama"}, "dramas": {"Drama"}, "war": {"War", "War & Politics"}, "western": {"Western"}, "westerns": {"Western"},
	"animated": {"Animation"}, "animation": {"Animation"}, "cartoon": {"Animation"}, "cartoons": {"Animation"}, "anime": {"Animation", "Anime"},
	"documentary": {"Documentary"}, "documentaries": {"Documentary"}, "docs": {"Documentary"}, "doc": {"Documentary"},
	"family": {"Family"}, "kids": {"Family", "Kids", "Children"}, "musical": {"Music", "Musical"}, "music": {"Music"},
	"history": {"History"}, "historical": {"History"}, "biopic": {"Biography", "History"},
	"reality": {"Reality"}, "sport": {"Sport", "Sports"}, "sports": {"Sport", "Sports"},
}

// stopwords never become keywords.
var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`a an the and or but of in on at to for with without about from by i im i'm me my we
		our you your it its is are was were be been some something anything any show me find give want wanna would like
		to watch watching see seen something good great best top really very quite bit lot movie movies film films flick
		flicks show shows series tv season seasons episode episodes that which who what where when how haven't havent not
		never no have has had already yet unwatched watched new old under over less more than hour hours minutes mins
		short long longer shorter rated rating year years decade era made set please can could should recommend
		recommendations suggest night tonight mood feel feeling kind sort type ones one two three`) {
		stopwords[w] = true
	}
}

// Basic parses a request without Claude. genres are the library's genre
// names, used to recognise genre words in the request.
func Basic(text string, genres []string) Query {
	q := Query{Watched: "any", Sort: "relevance"}
	s := " " + strings.ToLower(strings.TrimSpace(text)) + " "
	s = strings.NewReplacer("’", "'", "?", " ", "!", " ", ",", " ", ".", " . ").Replace(s)
	s = strings.ReplaceAll(s, " show me ", " ")

	// "something like Severance": everything after "like" names a title.
	if m := reLike.FindStringSubmatchIndex(strings.TrimSpace(s)); m != nil {
		t := strings.TrimSpace(s)
		q.SimilarTo = strings.Trim(strings.TrimSpace(t[m[2]:m[3]]), ". ")
		s = " " + t[:m[0]] + " "
	}

	cut := func(re *regexp.Regexp, f func([]string)) {
		for _, m := range re.FindAllStringSubmatch(s, -1) {
			f(m)
		}
		s = re.ReplaceAllString(s, " ")
	}
	cut(reYearRange, func(m []string) { q.YearFrom, _ = strconv.Atoi(m[1]); q.YearTo, _ = strconv.Atoi(m[2]) })
	cut(reAfter, func(m []string) { q.YearFrom, _ = strconv.Atoi(m[1]) })
	cut(reBefore, func(m []string) { y, _ := strconv.Atoi(m[1]); q.YearTo = y - 1 })
	cut(reYear, func(m []string) { y, _ := strconv.Atoi(m[1]); q.YearFrom, q.YearTo = y, y })
	cut(reDecade, func(m []string) {
		d, _ := strconv.Atoi(m[2])
		century := 1900
		switch m[1] {
		case "20":
			century = 2000
		case "":
			if d <= 2 {
				century = 2000 // "00s", "10s", "20s"
			}
		}
		q.YearFrom, q.YearTo = century+d*10, century+d*10+9
	})
	cut(reUnder, func(m []string) { q.MaxRuntime = amount(m[1], m[2]) })
	cut(reOver, func(m []string) { q.MinRuntime = amount(m[1], m[2]) })
	cut(reRating, func(m []string) { q.MinRating, _ = strconv.ParseFloat(m[1], 64) })
	for _, p := range []string{"haven't seen", "havent seen", "not seen", "haven't watched", "havent watched", "not watched",
		"unwatched", "new to me", "never seen", "never watched", "i've not seen", "ive not seen"} {
		if strings.Contains(s, p) {
			q.Watched = "unwatched"
		}
	}
	for _, p := range []string{"rewatch", "seen before", "watched before", "already seen", "already watched"} {
		if strings.Contains(s, p) {
			q.Watched = "watched"
		}
	}
	for _, p := range []string{"in progress", "started", "half watched", "half-watched", "unfinished", "didn't finish"} {
		if strings.Contains(s, p) {
			q.Watched = "inprogress"
		}
	}
	if strings.Contains(s, " short ") && q.MaxRuntime == 0 {
		q.MaxRuntime = 100
	}
	if strings.Contains(s, "highly rated") || strings.Contains(s, " best ") || strings.Contains(s, "acclaimed") || strings.Contains(s, " top ") {
		q.Sort = "rating"
		if q.MinRating == 0 {
			q.MinRating = 7
		}
	}
	if strings.Contains(s, "recent") || strings.Contains(s, "newest") || strings.Contains(s, "latest") {
		q.Sort = "newest"
	}
	if strings.Contains(s, "recently added") || strings.Contains(s, "just added") {
		q.Sort = "added"
	}

	movie, show := false, false
	known := map[string]string{}
	for _, g := range genres {
		known[strings.ToLower(g)] = g
	}
	addGenre := func(names ...string) {
		for _, n := range names {
			if g, ok := known[strings.ToLower(n)]; ok && !contains(q.Genres, g) {
				q.Genres = append(q.Genres, g)
			}
		}
	}
	// Multi-word library genres ("science fiction", "tv movie") first.
	for lg, g := range known {
		if strings.Contains(lg, " ") && strings.Contains(s, " "+lg+" ") {
			addGenre(g)
			s = strings.ReplaceAll(s, " "+lg+" ", " ")
		}
	}
	for _, w := range strings.Fields(s) {
		w = strings.Trim(w, ".'\"")
		switch w {
		case "movie", "movies", "film", "films", "flick", "flicks":
			movie = true
			continue
		case "show", "shows", "series", "tv", "sitcom", "sitcoms", "miniseries":
			show = true
		}
		if syn, ok := genreSynonyms[w]; ok {
			before := len(q.Genres)
			addGenre(syn...)
			if len(q.Genres) > before {
				continue
			}
		}
		if g, ok := known[w]; ok {
			addGenre(g)
			continue
		}
		if g, ok := known[strings.TrimSuffix(w, "s")]; ok {
			addGenre(g)
			continue
		}
		if len(w) > 2 && !stopwords[w] && !isNumber(w) && !contains(q.Keywords, w) {
			q.Keywords = append(q.Keywords, w)
		}
	}
	if movie != show {
		if movie {
			q.Kinds = []string{"movie"}
		} else {
			q.Kinds = []string{"show"}
		}
	}
	if q.Sort == "relevance" && len(q.Keywords) == 0 {
		q.Sort = "rating"
	}
	return q
}

func isNumber(s string) bool {
	_, err := strconv.Atoi(s)
	return err == nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}

// Normalize fixes up a query from either source so the executor can trust it.
func (q *Query) Normalize() {
	kinds := q.Kinds[:0]
	for _, k := range q.Kinds {
		if k == "movie" || k == "show" {
			kinds = append(kinds, k)
		}
	}
	q.Kinds = kinds
	if len(q.Kinds) == 2 {
		q.Kinds = nil
	}
	switch q.Watched {
	case "unwatched", "watched", "inprogress":
	default:
		q.Watched = "any"
	}
	switch q.Sort {
	case "relevance", "rating", "newest", "oldest", "added", "random":
	default:
		q.Sort = "relevance"
	}
	if q.YearFrom != 0 && q.YearTo != 0 && q.YearFrom > q.YearTo {
		q.YearFrom, q.YearTo = q.YearTo, q.YearFrom
	}
	if len(q.Keywords) > 8 {
		q.Keywords = q.Keywords[:8]
	}
	if len(q.Titles) > 40 {
		q.Titles = q.Titles[:40]
	}
	if q.MinRating < 0 || q.MinRating > 10 {
		q.MinRating = 0
	}
}
