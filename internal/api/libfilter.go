package api

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/sfxhewitt/plexodus/internal/db"
	"github.com/sfxhewitt/plexodus/internal/mood"
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
//	rt      Rotten Tomatoes: 90 | fresh | rotten (critics) | audience (80%+)
//	age     kids | 13 | 16 | adult (content rating, see internal/rating)
//	length  short (under 90 min) | medium (90–120) | long (over 2 h)
//	added   added in the last N days: 7, 30 or 90
//
// Films also have a mood profile (internal/mood), which adds:
//
//	mood     fg | th | cr | la | mb | ch | ad (Feel-good, Thriller, ...)
//	content  any of noviolence, noscares, family, nosex, romance, comma-separated
//	feel     where the film should sit on a dimension, within 25 points:
//	         "v:80,dark:20" (v a dark humour complex pace weight comfort, 0–100)
//	yfrom, yto  release year range
//
// and the viewer's saved "never show" and "must be" genres (pick_genres)
// always apply.

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

// optWheres holds, by cond, the condition to use in the item list instead of
// cond rewritten (mood columns are joined into t but not into the list).
var optWheres = map[string]string{}

type facetDef struct {
	Key    string
	Label  string
	Movies bool // only for film libraries
	Multi  bool // several choices at once, comma-separated; all must hold
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
	{Key: "rt", Label: "Rotten Tomatoes", opts: []facetOpt{
		{"90", "Critics 90%+", "t.rt_critic >= 90"},
		{"fresh", "Fresh · critics 60%+", "t.rt_critic >= 60"},
		{"rotten", "Rotten · critics under 60%", "t.rt_critic < 60"},
		{"audience", "Audience 80%+", "t.rt_audience >= 80"},
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

// moodExists is a condition on a film's mood profile, for the item list.
func moodExists(conds []string) string {
	return "EXISTS (SELECT 1 FROM item_moods m WHERE m.item_id = i.id AND " + strings.Join(conds, " AND ") + ")"
}

// moodOpt builds a choice from conditions written against item_moods as m:
// counted over t's joined mood columns (t.m_dark), listed with an EXISTS.
func moodOpt(v, l string, conds []string) facetOpt {
	cond := strings.ReplaceAll(strings.Join(conds, " AND "), "m.", "t.m_")
	optWheres[cond] = moodExists(conds)
	return facetOpt{v, l, cond}
}

// moodCols selects every mood column into the derived table t as m_<name>.
var moodCols = func() string {
	var cols []string
	for _, c := range mood.Columns {
		cols = append(cols, "m."+c+" m_"+c)
	}
	return strings.Join(cols, ", ")
}()

func init() {
	moods := facetDef{Key: "mood", Label: "Mood", Movies: true}
	for _, p := range mood.Moods {
		var conds []string
		for _, r := range p.R {
			if r.Lo > 0 {
				conds = append(conds, fmt.Sprintf("m.%s >= %d", mood.Columns[r.Dim], int(r.Lo*100+.5)))
			}
			if r.Hi < 1 {
				conds = append(conds, fmt.Sprintf("m.%s <= %d", mood.Columns[r.Dim], int(r.Hi*100+.5)))
			}
		}
		moods.opts = append(moods.opts, moodOpt(p.Key, p.Label+" · "+p.Sub, conds))
	}
	content := facetDef{Key: "content", Label: "Content", Movies: true, Multi: true}
	for _, f := range mood.Flags {
		op := ">="
		if f.Below {
			op = "<"
		}
		content.opts = append(content.opts, moodOpt(f.Key, f.Label, []string{fmt.Sprintf("m.%s %s %d", mood.Columns[f.Dim], op, int(f.At*100+.5))}))
	}
	// Mood first: it is what people come to these filters for.
	facetDefs = append([]facetDef{moods, content}, facetDefs...)
}

// feelTolerance is how far (in points) a film may sit from a "feel" slider.
const feelTolerance = 25

// parseFeel reads "v:80,dark:20" into dimension → 0–100.
func parseFeel(s string) map[int]int {
	out := map[int]int{}
	for _, part := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(part, ":")
		d, known := mood.DimIndex(k)
		n, err := strconv.Atoi(v)
		if ok && known && err == nil && n >= 0 && n <= 100 {
			out[d] = n
		}
	}
	return out
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
	return tReplacer().Replace(c)
}

// optWhere is a choice's condition for the item list.
func optWhere(o facetOpt) string {
	if w, ok := optWheres[o.cond]; ok {
		return w
	}
	return tCond(o.cond)
}

func tReplacer() *strings.Replacer {
	return strings.NewReplacer(
		"t.watched", "COALESCE(w.watched, 0)", "t.position", "COALESCE(w.position, 0)",
		"t.res", resExpr, "t.", "i.", "%NOW%", strconv.FormatInt(db.Now(), 10))
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
		if f.Multi {
			for _, v := range strings.Split(q.Get(f.Key), ",") {
				if _, o, ok := findOpt(f.Key, v); ok {
					where += " AND " + optWhere(o)
				}
			}
			continue
		}
		if _, o, ok := findOpt(f.Key, q.Get(f.Key)); ok {
			where += " AND " + optWhere(o)
		}
	}
	if feel := parseFeel(q.Get("feel")); len(feel) > 0 {
		var conds []string
		for d := 0; d < mood.NDims; d++ { // in order, so the SQL is stable
			if n, ok := feel[d]; ok {
				conds = append(conds, fmt.Sprintf("m.%s BETWEEN %d AND %d", mood.Columns[d], n-feelTolerance, n+feelTolerance))
			}
		}
		where += " AND " + moodExists(conds)
	}
	if y, err := strconv.Atoi(q.Get("yfrom")); err == nil && y > 0 {
		where += fmt.Sprintf(" AND i.year >= %d", y)
	}
	if y, err := strconv.Atoi(q.Get("yto")); err == nil && y > 0 {
		where += fmt.Sprintf(" AND i.year > 0 AND i.year <= %d", y)
	}
	// The viewer's saved genres: none of the "never" ones and, if they insist
	// on any, at least one of those.
	where += ` AND NOT EXISTS (SELECT 1 FROM pick_genres pg, json_each(i.genres) pj WHERE pg.user_id = ?1 AND pg.mode = 1 AND pj.value = pg.genre)
		AND (NOT EXISTS (SELECT 1 FROM pick_genres pg WHERE pg.user_id = ?1 AND pg.mode = 2)
			OR EXISTS (SELECT 1 FROM pick_genres pg, json_each(i.genres) pj WHERE pg.user_id = ?1 AND pg.mode = 2 AND pj.value = pg.genre))`
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
	Multi   bool         `json:"multi,omitempty"`
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
	if movies {
		s.ensureMoods(r.Context())
	}

	res, mcols, mjoin := "NULL", "", ""
	if movies {
		res = resExpr
		mcols, mjoin = ", "+moodCols, " LEFT JOIN item_moods m ON m.item_id = i.id"
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
		rows, err := s.db.R.QueryContext(r.Context(), `SELECT `+strings.Join(sums, ", ")+` FROM (SELECT i.year, i.rating, i.rt_critic, i.rt_audience, i.age, i.runtime, i.added_at,
			COALESCE(w.watched, 0) watched, COALESCE(w.position, 0) position, `+res+` res`+mcols+`
			FROM items i LEFT JOIN watch_state w ON w.item_id = i.id AND w.user_id = ?1`+mjoin+` `+where+`) t`, append([]any{uid}, args...)...)
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
		if _, _, ok := findOpt(f.Key, q.Get(f.Key)); ok && !f.Multi {
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
		fo := facetOut{Key: f.Key, Label: f.Label, Multi: f.Multi, Options: []facetCount{}}
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
