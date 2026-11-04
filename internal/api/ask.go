package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/sfxhewitt/plexodus/internal/ask"
)

// ---- plain-English search ("Ask") ----

// anthropicKey is the key saved in Settings, else ANTHROPIC_API_KEY.
func (s *Server) anthropicKey(ctx context.Context) string {
	if k := s.db.Setting(ctx, "anthropic_key"); k != "" {
		return k
	}
	return s.cfg.AnthropicKey
}

type genreCache struct {
	mu   sync.Mutex
	list []string
	at   time.Time
}

// libraryGenres lists every genre name in use, refreshed every 15 minutes.
func (s *Server) libraryGenres(ctx context.Context) []string {
	s.genres.mu.Lock()
	defer s.genres.mu.Unlock()
	if s.genres.list != nil && time.Since(s.genres.at) < 15*time.Minute {
		return s.genres.list
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT DISTINCT j.value FROM items i, json_each(i.genres) j
		WHERE i.kind IN ('movie', 'show') AND i.genres IS NOT NULL ORDER BY j.value`)
	if err != nil {
		return s.genres.list
	}
	defer rows.Close()
	list := []string{}
	for rows.Next() {
		var g string
		if rows.Scan(&g) == nil && g != "" {
			list = append(list, g)
		}
	}
	s.genres.list, s.genres.at = list, time.Now()
	return list
}

type askResult struct {
	Summary string    `json:"summary"`
	Engine  string    `json:"engine"` // "claude" or "basic"
	Notice  string    `json:"notice,omitempty"`
	Query   ask.Query `json:"query"`
	Picks   []Card    `json:"picks"`
	Matches []Card    `json:"matches"`
}

func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	text := strings.TrimSpace(r.URL.Query().Get("q"))
	if text == "" {
		writeErr(w, 400, "ask something")
		return
	}
	if len(text) > 300 {
		text = text[:300]
	}
	start := time.Now()
	genres := s.libraryGenres(r.Context())
	res := askResult{Engine: "basic"}
	if s.claude.Enabled() {
		q, err := s.claude.Translate(r.Context(), text, genres)
		if err == nil {
			res.Query, res.Engine = q, "claude"
		} else {
			log.Printf("ask: Claude failed for %q: %v", text, err)
			if errors.Is(err, context.Canceled) {
				return
			}
			res.Notice = "Claude couldn't answer (" + err.Error() + "), so this used basic search."
		}
	}
	if res.Engine == "basic" {
		res.Query = ask.Basic(text, genres)
		res.Query.Normalize()
	}
	q := res.Query
	res.Summary = q.Summary
	if res.Summary == "" {
		res.Summary = text
	}
	translated := time.Since(start)

	res.Picks = s.askPicks(r, q)
	seen := map[int64]bool{}
	for _, c := range res.Picks {
		seen[c.ID] = true
	}
	res.Matches = []Card{}
	matches, err := s.askMatches(r, q)
	if err != nil {
		log.Printf("ask: query failed for %q: %v", text, err)
	}
	for _, c := range dedupeCards(matches) {
		if !seen[c.ID] && len(res.Matches) < 60 {
			res.Matches = append(res.Matches, c)
		}
	}
	if d := time.Since(start); d > 500*time.Millisecond {
		log.Printf("ask: %s (%s translate via %s) for %q", d.Round(time.Millisecond), translated.Round(time.Millisecond), res.Engine, text)
	}
	writeJSON(w, res)
}

// normTitle compares titles loosely: case, punctuation and a leading "The"
// don't matter.
func normTitle(t string) string {
	t = strings.ToLower(strings.ReplaceAll(t, "&", " and "))
	t = strings.TrimPrefix(strings.TrimSpace(t), "the ")
	var b strings.Builder
	for _, r := range t {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func titleTokens(t string) []string {
	return strings.FieldsFunc(strings.ToLower(t), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// findTitle finds a film or show by title (and roughly by year when given).
func (s *Server) findTitle(r *http.Request, title string, year int, kinds []string) (int64, bool) {
	toks := titleTokens(title)
	if len(toks) == 0 {
		return 0, false
	}
	want := normTitle(title)
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT i.id, i.kind, i.title, COALESCE(i.original_title, ''), COALESCE(i.year, 0)
		FROM items_fts f JOIN items i ON i.id = f.rowid`+aclJoin(currentUser(r), "i")+`
		WHERE items_fts MATCH ? AND i.kind IN ('movie', 'show') LIMIT 50`,
		`{title original_title} : "`+strings.Join(toks, " ")+`"`)
	if err != nil {
		return 0, false
	}
	defer rows.Close()
	var best int64
	bestScore := -1
	for rows.Next() {
		var id int64
		var kind, t, ot string
		var y int
		if rows.Scan(&id, &kind, &t, &ot, &y) != nil {
			continue
		}
		if len(kinds) > 0 && !containsStr(kinds, kind) {
			continue
		}
		if normTitle(t) != want && normTitle(ot) != want {
			continue
		}
		score := 10
		if year != 0 && y != 0 {
			d := y - year
			if d < 0 {
				d = -d
			}
			if d > 1 {
				continue
			}
			score -= d
		}
		if score > bestScore {
			best, bestScore = id, score
		}
	}
	return best, bestScore >= 0
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// askPicks returns Claude's suggested titles that are in the library.
func (s *Server) askPicks(r *http.Request, q ask.Query) []Card {
	out := []Card{}
	if len(q.Titles) == 0 {
		return out
	}
	ids := []int64{}
	have := map[int64]bool{}
	for _, t := range q.Titles {
		if id, ok := s.findTitle(r, t.Title, t.Year, q.Kinds); ok && !have[id] {
			have[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return out
	}
	cards, err := s.cards(r, `WHERE i.id IN (`+idList(ids)+`)`)
	if err != nil {
		return out
	}
	byID := map[int64]Card{}
	for _, c := range cards {
		byID[c.ID] = c
	}
	for _, id := range ids {
		if c, ok := byID[id]; ok && watchedMatches(c, q.Watched) {
			out = append(out, c)
		}
	}
	return dedupeCards(out)
}

func watchedMatches(c Card, want string) bool {
	switch want {
	case "unwatched":
		if c.Kind == "show" {
			return c.ViewedLeaves == 0
		}
		return !c.Watched
	case "watched":
		if c.Kind == "show" {
			return c.ViewedLeaves > 0
		}
		return c.Watched
	case "inprogress":
		if c.Kind == "show" {
			return c.ViewedLeaves > 0 && c.ViewedLeaves < c.LeafCount
		}
		return c.Position > 0 && !c.Watched
	}
	return true
}

// dedupeCards drops the same title showing up from two libraries (e.g.
// Movies and Movies 4K), keeping the first.
func dedupeCards(cards []Card) []Card {
	seen := map[string]bool{}
	out := cards[:0:0]
	for _, c := range cards {
		k := fmt.Sprintf("%s|%s|%d", c.Kind, normTitle(c.Title), c.Year)
		if !seen[k] {
			seen[k] = true
			out = append(out, c)
		}
	}
	return out
}

const (
	// Shows count as watched/unwatched through their episodes. These drive
	// from items_parent and probe watch_state by primary key.
	showAnyWatched = `EXISTS (SELECT 1 FROM items sn JOIN items ep ON ep.parent_id = sn.id
		WHERE sn.parent_id = i.id AND EXISTS (SELECT 1 FROM watch_state ws WHERE ws.user_id = ?1 AND ws.item_id = ep.id AND ws.watched = 1))`
	showAnyUnwatched = `EXISTS (SELECT 1 FROM items sn JOIN items ep ON ep.parent_id = sn.id
		WHERE sn.parent_id = i.id AND NOT EXISTS (SELECT 1 FROM watch_state ws WHERE ws.user_id = ?1 AND ws.item_id = ep.id AND ws.watched = 1))`
)

// askMatches runs the structured filters. It picks ids first and only then
// builds cards, so the per-show counts in cardCols run for the final rows only.
func (s *Server) askMatches(r *http.Request, q ask.Query) ([]Card, error) {
	args := []any{currentUser(r).ID}
	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("?%d", len(args))
	}
	list := func(vals []string) string {
		ph := make([]string, len(vals))
		for i, v := range vals {
			ph[i] = arg(strings.ToLower(v))
		}
		return strings.Join(ph, ", ")
	}

	join := ""
	where := []string{}
	if len(q.Kinds) == 1 {
		where = append(where, "i.kind = "+arg(q.Kinds[0]))
	} else {
		where = append(where, "i.kind IN ('movie', 'show')")
	}
	order := "i.rating IS NULL, i.rating DESC, i.sort_title"

	genres := q.Genres
	minGenreOverlap := 1
	if q.SimilarTo != "" {
		if id, ok := s.findTitle(r, q.SimilarTo, 0, nil); ok {
			where = append(where, "i.id != "+arg(id))
			if len(genres) == 0 {
				var kind, gs string
				s.db.R.QueryRowContext(r.Context(), `SELECT kind, COALESCE(genres, '[]') FROM items WHERE id = ?`, id).Scan(&kind, &gs)
				rows, err := s.db.R.QueryContext(r.Context(), `SELECT value FROM json_each(?)`, gs)
				if err == nil {
					for rows.Next() {
						var g string
						rows.Scan(&g)
						genres = append(genres, g)
					}
					rows.Close()
				}
				if len(genres) >= 2 {
					minGenreOverlap = 2
				}
				if len(q.Kinds) == 0 && kind != "" {
					where[0] = "i.kind = " + arg(kind)
				}
			}
		}
	}
	if len(genres) > 0 {
		where = append(where, fmt.Sprintf(`(SELECT COUNT(*) FROM json_each(i.genres) j WHERE lower(j.value) IN (%s)) >= %d`, list(genres), minGenreOverlap))
	}
	if len(q.ExcludeGenres) > 0 {
		where = append(where, fmt.Sprintf(`NOT EXISTS (SELECT 1 FROM json_each(i.genres) j WHERE lower(j.value) IN (%s))`, list(q.ExcludeGenres)))
	}
	if q.YearFrom > 0 {
		where = append(where, "i.year >= "+arg(q.YearFrom))
	}
	if q.YearTo > 0 {
		where = append(where, "i.year <= "+arg(q.YearTo))
	}
	if q.MaxRuntime > 0 {
		where = append(where, "(i.kind = 'show' OR (i.runtime > 0 AND i.runtime <= "+arg(q.MaxRuntime)+"))")
	}
	if q.MinRuntime > 0 {
		where = append(where, "(i.kind = 'show' OR i.runtime >= "+arg(q.MinRuntime)+")")
	}
	if q.MinRating > 0 {
		where = append(where, "i.rating >= "+arg(q.MinRating))
	}
	switch q.Watched {
	case "unwatched":
		where = append(where, "CASE WHEN i.kind = 'show' THEN NOT "+showAnyWatched+" ELSE COALESCE(w.watched, 0) = 0 END")
	case "watched":
		where = append(where, "CASE WHEN i.kind = 'show' THEN "+showAnyWatched+" ELSE w.watched = 1 END")
	case "inprogress":
		where = append(where, "CASE WHEN i.kind = 'show' THEN "+showAnyWatched+" AND "+showAnyUnwatched+
			" ELSE w.position > 0 AND COALESCE(w.watched, 0) = 0 END")
	}
	if fq := ftsAny(q.Keywords); fq != "" {
		join = ` JOIN (SELECT rowid, bm25(items_fts, 10.0, 5.0, 1.0) AS score FROM items_fts WHERE items_fts MATCH ` + arg(fq) + `) f ON f.rowid = i.id`
		if q.Sort == "relevance" {
			order = "f.score"
		}
	}
	switch q.Sort {
	case "newest":
		order = "i.year IS NULL, i.year DESC, i.added_at DESC"
	case "oldest":
		order = "i.year IS NULL, i.year ASC"
	case "added":
		order = "i.added_at DESC"
	case "random":
		order = "RANDOM()"
	}
	if len(where) == 1 && join == "" && q.Sort != "added" && q.Sort != "random" && q.Sort != "newest" {
		// Nothing understood: don't list the whole library by rating.
		return nil, nil
	}

	sqlText := `SELECT i.id FROM items i` + aclJoin(currentUser(r), "i") + `
		LEFT JOIN watch_state w ON w.item_id = i.id AND w.user_id = ?1` + join + `
		WHERE ` + strings.Join(where, " AND ") + ` ORDER BY ` + order + ` LIMIT 150`
	rows, err := s.db.R.QueryContext(r.Context(), sqlText, args...)
	if err != nil {
		return nil, err
	}
	ids := []int64{}
	for rows.Next() {
		var id int64
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	if len(ids) == 0 {
		return nil, rows.Err()
	}
	cards, err := s.cards(r, `WHERE i.id IN (`+idList(ids)+`)`)
	if err != nil {
		return nil, err
	}
	byID := map[int64]Card{}
	for _, c := range cards {
		byID[c.ID] = c
	}
	out := make([]Card, 0, len(ids))
	for _, id := range ids {
		if c, ok := byID[id]; ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// ftsAny matches any of the words (prefix match for longer words).
func ftsAny(words []string) string {
	var terms []string
	for _, w := range words {
		for _, t := range titleTokens(w) {
			if len(t) >= 5 {
				terms = append(terms, `"`+t+`"*`)
			} else if len(t) >= 2 {
				terms = append(terms, `"`+t+`"`)
			}
		}
	}
	return strings.Join(terms, " OR ")
}
