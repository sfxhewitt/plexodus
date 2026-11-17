package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sfxhewitt/plexodus/internal/db"
)

// Library filters, shared by the item list and the live counts the TV's
// filter panel shows next to every choice ("facets").
//
// Query parameters (all optional, combinable):
//
//	filter  unwatched | inprogress | watched
//	genre   a genre name
//	decade  2020, 2010, ... 1950, or 0 for anything older
//	res     4k | 1080 | 720 | sd (films; from the file's width/height)
//	rating  minimum TMDB rating: 5, 6, 7 or 8
//	age     kids | 13 | 16 | adult (content rating, see internal/rating)
//	length  short (under 90 min) | medium (90–120) | long (over 2 h)
//	added   added in the last N days: 7, 30 or 90

// resExpr buckets a film by its best file's picture size. A wide 1920×800
// scope film is still 1080p, so width counts as much as height.
const resExpr = `(SELECT CASE WHEN MAX(mf.width) >= 3200 OR MAX(mf.height) >= 1700 THEN '4k'
	WHEN MAX(mf.width) >= 1600 OR MAX(mf.height) >= 900 THEN '1080'
	WHEN MAX(mf.width) >= 1000 OR MAX(mf.height) >= 600 THEN '720'
	WHEN MAX(mf.width) > 0 THEN 'sd' END FROM media_files mf WHERE mf.item_id = i.id)`

type facetOpt struct {
	V    string // query value
	L    string // label
	cond string // SQL over the derived table t (see facetsQuery)
}

type facetDef struct {
	Key    string
	Label  string
	Movies bool // only for film libraries
	opts   []facetOpt
}

var decadeOpts = func() []facetOpt {
	var o []facetOpt
	for d := 2020; d >= 1950; d -= 10 {
		o = append(o, facetOpt{strconv.Itoa(d), fmt.Sprintf("%ds", d), fmt.Sprintf("t.year BETWEEN %d AND %d", d, d+9)})
	}
	return append(o, facetOpt{"0", "Before 1950", "t.year > 0 AND t.year < 1950"})
}()

// facetDefs in the order the TV shows them. Genres are their own query.
var facetDefs = []facetDef{
	{Key: "filter", Label: "Watched", opts: []facetOpt{
		{"unwatched", "Unwatched", "t.watched = 0"},
		{"inprogress", "In progress", "t.position > 0 AND t.watched = 0"},
		{"watched", "Watched", "t.watched = 1"},
	}},
	{Key: "added", Label: "Recently added", opts: []facetOpt{
		{"7", "This week", "t.added_at >= %NOW% - 7*86400"},
		{"30", "This month", "t.added_at >= %NOW% - 30*86400"},
		{"90", "Last 3 months", "t.added_at >= %NOW% - 90*86400"},
	}},
	{Key: "decade", Label: "Decade", opts: decadeOpts},
	{Key: "rating", Label: "Rating", opts: []facetOpt{
		{"8", "8+ Outstanding", "t.rating >= 8"},
		{"7", "7+ Great", "t.rating >= 7"},
		{"6", "6+ Good", "t.rating >= 6"},
		{"5", "5+", "t.rating >= 5"},
	}},
	{Key: "res", Label: "Quality", Movies: true, opts: []facetOpt{
		{"4k", "4K", "t.res = '4k'"},
		{"1080", "1080p", "t.res = '1080'"},
		{"720", "720p", "t.res = '720'"},
		{"sd", "SD", "t.res = 'sd'"},
	}},
	{Key: "length", Label: "Length", Movies: true, opts: []facetOpt{
		{"short", "Under 90 min", "t.runtime > 0 AND t.runtime < 90"},
		{"medium", "90 min – 2 h", "t.runtime BETWEEN 90 AND 120"},
		{"long", "Over 2 h", "t.runtime > 120"},
	}},
	{Key: "age", Label: "Age rating", opts: []facetOpt{
		{"kids", "Kids · G, PG, U", "t.age IS NOT NULL AND t.age <= 8"},
		{"13", "Up to 13 · PG-13, 12A", "t.age IS NOT NULL AND t.age <= 13"},
		{"16", "Up to 16 · 15, TV-14", "t.age IS NOT NULL AND t.age <= 16"},
		{"adult", "Adults · R, 18", "t.age >= 17"},
	}},
}

func findOpt(key, v string) (facetDef, facetOpt, bool) {
	for _, f := range facetDefs {
		if f.Key != key {
			continue
		}
		for _, o := range f.opts {
			if o.V == v {
				return f, o, true
			}
		}
	}
	return facetDef{}, facetOpt{}, false
}

// tCond rewrites a facet condition written against the derived table t so it
// runs directly on the items/watch_state join.
func tCond(c string) string {
	r := strings.NewReplacer(
		"t.watched", "COALESCE(w.watched, 0)", "t.position", "COALESCE(w.position, 0)",
		"t.res", resExpr, "t.", "i.", "%NOW%", strconv.FormatInt(db.Now(), 10))
	return r.Replace(c)
}

// libraryWhere builds the WHERE clause for a library's films and shows with
// the request's filters, leaving out the `skip` facet (for its own counts).
// ?1 is the user id and ?2 the library id; the returned args start at ?2.
func libraryWhere(r *http.Request, id int64, q url.Values, skip string) (string, []any) {
	where := `WHERE i.library_id = ?2 AND i.kind IN ('movie', 'show')` + kidsCond(currentUser(r), "i")
	args := []any{id}
	if g := q.Get("genre"); g != "" && skip != "genre" {
		where += fmt.Sprintf(` AND EXISTS (SELECT 1 FROM json_each(i.genres) WHERE value = ?%d)`, len(args)+2)
		args = append(args, g)
	}
	for _, f := range facetDefs {
		if f.Key == skip {
			continue
		}
		if _, o, ok := findOpt(f.Key, q.Get(f.Key)); ok {
			where += " AND " + tCond(o.cond)
		}
	}
	return where, args
}

type facetCount struct {
	V     string `json:"v"`
	L     string `json:"l"`
	Count int    `json:"count"`
}

type facetOut struct {
	Key     string       `json:"key"`
	Label   string       `json:"label"`
	Options []facetCount `json:"options"`
}

// handleLibraryFacets returns how many titles each filter choice would leave,
// given the other filters already chosen, plus the current total.
func (s *Server) handleLibraryFacets(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok || !currentUser(r).CanSee(id) {
		writeErr(w, 404, "not found")
		return
	}
	q := r.URL.Query()
	var kind string
	s.db.R.QueryRowContext(r.Context(), `SELECT kind FROM libraries WHERE id = ?`, id).Scan(&kind)
	movies := kind != "shows"
	uid := currentUser(r).ID

	res := "NULL"
	if movies {
		res = resExpr
	}
	// One pass per distinct WHERE: every facet without a choice shares the
	// fully filtered set; each facet with a choice is counted without it.
	counts := map[string]map[string]int{}
	run := func(skip string, defs []facetDef) error {
		where, args := libraryWhere(r, id, q, skip)
		var sums []string
		var keys [][2]string
		for _, f := range defs {
			for _, o := range f.opts {
				sums = append(sums, "COALESCE(SUM("+strings.ReplaceAll(o.cond, "%NOW%", strconv.FormatInt(db.Now(), 10))+"), 0)")
				keys = append(keys, [2]string{f.Key, o.V})
			}
		}
		sums = append(sums, "COUNT(*)")
		rows, err := s.db.R.QueryContext(r.Context(), `SELECT `+strings.Join(sums, ", ")+` FROM (SELECT i.year, i.rating, i.age, i.runtime, i.added_at,
			COALESCE(w.watched, 0) watched, COALESCE(w.position, 0) position, `+res+` res
			FROM items i LEFT JOIN watch_state w ON w.item_id = i.id AND w.user_id = ?1 `+where+`) t`, append([]any{uid}, args...)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		vals := make([]int, len(sums))
		ptrs := make([]any, len(sums))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if rows.Next() {
			if err := rows.Scan(ptrs...); err != nil {
				return err
			}
		}
		for i, k := range keys {
			if counts[k[0]] == nil {
				counts[k[0]] = map[string]int{}
			}
			counts[k[0]][k[1]] = vals[i]
		}
		if skip == "" {
			counts[""] = map[string]int{"total": vals[len(vals)-1]}
		}
		return rows.Err()
	}

	var shared []facetDef
	var visible []facetDef
	for _, f := range facetDefs {
		if f.Movies && !movies {
			continue
		}
		visible = append(visible, f)
		if _, _, ok := findOpt(f.Key, q.Get(f.Key)); ok {
			if err := run(f.Key, []facetDef{f}); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
		} else {
			shared = append(shared, f)
		}
	}
	if err := run("", shared); err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	out := []facetOut{}
	for _, f := range visible {
		fo := facetOut{Key: f.Key, Label: f.Label, Options: []facetCount{}}
		for _, o := range f.opts {
			n := counts[f.Key][o.V]
			// Empty decades only clutter the list; keep the others, at 0, so
			// the list doesn't jump around as filters change.
			if n == 0 && f.Key == "decade" && q.Get("decade") != o.V {
				continue
			}
			fo.Options = append(fo.Options, facetCount{o.V, o.L, n})
		}
		out = append(out, fo)
	}

	// Genres, counted with every other filter applied.
	where, args := libraryWhere(r, id, q, "genre")
	genres := []facetCount{}
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT j.value, COUNT(*) FROM items i
		LEFT JOIN watch_state w ON w.item_id = i.id AND w.user_id = ?1, json_each(i.genres) j `+where+` AND i.genres IS NOT NULL
		GROUP BY j.value ORDER BY j.value`, append([]any{uid}, args...)...)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	for rows.Next() {
		var g facetCount
		rows.Scan(&g.V, &g.Count)
		g.L = g.V
		if g.Count > 0 || g.V == q.Get("genre") {
			genres = append(genres, g)
		}
	}
	writeJSON(w, map[string]any{"total": counts[""]["total"], "facets": out, "genres": genres})
}
