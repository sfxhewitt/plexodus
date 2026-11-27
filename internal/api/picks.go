package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sfxhewitt/plexodus/internal/db"
	"github.com/sfxhewitt/plexodus/internal/mood"
)

// Picks: Reel Pick inside the film libraries. The library page's filters gain
// mood, feel and content (libfilter.go); this file adds the "Best match" sort,
// "Surprise me" (one suggestion at a time from whatever is filtered), "Help
// me choose" (a short quiz, three picks with reasons) and the memory behind
// them:
//
//   - a suggested film rests for 30 days before it is suggested again,
//   - each skip is a strike, and the third hides the film for a year,
//   - "Not interested" hides it for a year at once,
//   - likes, dislikes, skips and the watchlist shape a taste profile.
//
// Everything is worked out from the database and an in-memory index of the
// films; nothing touches the media files.

const (
	pickCooldown = 30 * 86400
	pickBan      = 365 * 86400
	pickStrikes  = 3
	moodMaxAge   = 5 * time.Minute
)

// moodCache holds the film index, rebuilt every few minutes.
type moodCache struct {
	mu      sync.Mutex
	ix      *mood.Index
	at      time.Time
	loading bool
}

// ensureMoods makes sure every film has a mood profile row (the filters'
// SQL reads them) and returns the index. A stale index is served while a
// fresh one loads in the background.
func (s *Server) ensureMoods(ctx context.Context) *mood.Index {
	c := &s.moods
	c.mu.Lock()
	if c.ix != nil {
		if time.Since(c.at) > moodMaxAge && !c.loading {
			c.loading = true
			go func() {
				ix, err := s.loadMoods(s.baseCtx)
				c.mu.Lock()
				if err == nil {
					c.ix, c.at = ix, time.Now()
				}
				c.loading = false
				c.mu.Unlock()
			}()
		}
		ix := c.ix
		c.mu.Unlock()
		return ix
	}
	defer c.mu.Unlock()
	ix, err := s.loadMoods(ctx)
	if err != nil {
		log.Printf("moods: %v", err)
		return mood.NewIndex(nil)
	}
	c.ix, c.at = ix, time.Now()
	return ix
}

func moodSig(tmdb int, genres string) string { return strconv.Itoa(tmdb) + "|" + genres }

func toPoints(d mood.Dims) []any {
	out := make([]any, mood.NDims)
	for i, x := range d {
		out[i] = int(x*100 + .5)
	}
	return out
}

// loadMoods reads every film, writes mood rows for new or re-matched ones,
// and builds the index.
func (s *Server) loadMoods(ctx context.Context) (*mood.Index, error) {
	start := time.Now()
	rows, err := s.db.R.QueryContext(ctx, `SELECT i.id, i.title, COALESCE(i.year, 0), COALESCE(i.runtime, 0), COALESCE(i.rating, 0),
		COALESCE(i.tmdb_id, 0), COALESCE(i.genres, ''), COALESCE(m.sig, '') FROM items i
		LEFT JOIN item_moods m ON m.item_id = i.id WHERE i.kind = 'movie'`)
	if err != nil {
		return nil, err
	}
	var films []*mood.Film
	type stale struct {
		f   *mood.Film
		sig string
		src int
	}
	var todo []stale
	for rows.Next() {
		f := &mood.Film{}
		var tmdb int
		var genres, sig string
		if err := rows.Scan(&f.ID, &f.Title, &f.Year, &f.Runtime, &f.Rating, &tmdb, &genres, &sig); err != nil {
			rows.Close()
			return nil, err
		}
		json.Unmarshal([]byte(genres), &f.Genres)
		if d, votes, kw, ok := mood.Lookup(tmdb); ok && tmdb > 0 {
			f.D, f.Votes, f.Kw, f.Scored = d, votes, kw, true
		} else {
			f.D = mood.Heuristic(f.Genres)
		}
		if want := moodSig(tmdb, genres); want != sig {
			todo = append(todo, stale{f, want, boolInt(f.Scored)})
		}
		films = append(films, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// The SQL filters work in whole points, so the index uses the same
	// rounded scores: a film the filter lets through always fits the mood.
	for _, f := range films {
		for i, x := range f.D {
			f.D[i] = float64(int(x*100+.5)) / 100
		}
	}
	for len(todo) > 0 {
		n := min(len(todo), 500)
		tx, err := s.db.W.BeginTx(ctx, nil)
		if err != nil {
			return nil, err
		}
		for _, t := range todo[:n] {
			args := append([]any{t.f.ID}, toPoints(t.f.D)...)
			args = append(args, t.src, t.sig)
			if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO item_moods(item_id, `+strings.Join(mood.Columns[:], ", ")+`, src, sig)
				VALUES (?`+strings.Repeat(", ?", mood.NDims+2)+`)`, args...); err != nil {
				tx.Rollback()
				return nil, err
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		todo = todo[n:]
	}
	ix := mood.NewIndex(films)
	if d := time.Since(start); d > 2*time.Second {
		log.Printf("moods: %d films indexed in %s", len(films), d.Round(time.Millisecond))
	}
	return ix, nil
}

// ---- memory ----

type pickRow struct {
	shownAt, banUntil int64
	strikes, vote     int
}

func (p pickRow) eligible(now int64) bool {
	return p.banUntil <= now && p.shownAt+pickCooldown <= now
}

func (s *Server) pickRows(ctx context.Context, uid int64) map[int64]pickRow {
	out := map[int64]pickRow{}
	rows, err := s.db.R.QueryContext(ctx, `SELECT item_id, shown_at, strikes, ban_until, vote FROM pick_state WHERE user_id = ?`, uid)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var p pickRow
		rows.Scan(&id, &p.shownAt, &p.strikes, &p.banUntil, &p.vote)
		out[id] = p
	}
	return out
}

// tasteProfile turns the viewer's feedback into a profile: likes and the
// watchlist pull towards a film, dislikes, hidden films and skips push away.
func (s *Server) tasteProfile(ctx context.Context, uid int64, ix *mood.Index, state map[int64]pickRow) *mood.Profile {
	var ev []mood.Event
	now := db.Now()
	for id, p := range state {
		switch {
		case p.vote > 0:
			ev = append(ev, mood.Event{ID: id, W: 1})
		case p.vote < 0 || p.banUntil > now:
			ev = append(ev, mood.Event{ID: id, W: -1})
		case p.strikes > 0:
			ev = append(ev, mood.Event{ID: id, W: -0.3 * float64(p.strikes)})
		}
	}
	if rows, err := s.db.R.QueryContext(ctx, `SELECT item_id FROM watchlist WHERE user_id = ?`, uid); err == nil {
		for rows.Next() {
			var id int64
			rows.Scan(&id)
			if state[id].vote == 0 {
				ev = append(ev, mood.Event{ID: id, W: 1})
			}
		}
		rows.Close()
	}
	// Map order would make the sums differ in the last bits from run to run.
	sort.Slice(ev, func(i, j int) bool { return ev[i].ID < ev[j].ID })
	return ix.Rocchio(ev)
}

// ---- ranking a library ----

// filterCtx is the ranking context the library's mood and feel filters imply.
func filterCtx(q url.Values) *mood.Ctx {
	c := &mood.Ctx{Target: mood.Target{}}
	if p, ok := mood.Mood(q.Get("mood")); ok {
		c.Target.Merge(p.Target())
		c.MoodLabel = p.Label
	}
	feel := parseFeel(q.Get("feel"))
	for d, n := range feel {
		c.Target[d] = float64(n) / 100
	}
	if c.MoodLabel == "" && len(feel) > 0 {
		c.MoodLabel = "your sliders"
	}
	return c
}

// libraryFilms returns the library's films that pass the request's filters,
// as index entries. extra is more SQL for the WHERE (starting " AND").
func (s *Server) libraryFilms(r *http.Request, id int64, q url.Values, ix *mood.Index, extra string) ([]*mood.Film, error) {
	where, args := libraryWhere(r, id, q, "")
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT i.id FROM items i LEFT JOIN watch_state w ON w.item_id = i.id AND w.user_id = ?1 `+
		where+` AND i.kind = 'movie'`+extra, append([]any{currentUser(r).ID}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*mood.Film
	for rows.Next() {
		var fid int64
		rows.Scan(&fid)
		if f := ix.Get(fid); f != nil {
			out = append(out, f)
		}
	}
	return out, rows.Err()
}

// matchOrder ranks the filtered library for the "Best match" sort: the
// closest fits first, the top two dozen spread out for variety.
func (s *Server) matchOrder(r *http.Request, id int64, q url.Values) ([]mood.Ranked, error) {
	ix := s.ensureMoods(r.Context())
	films, err := s.libraryFilms(r, id, q, ix, "")
	if err != nil {
		return nil, err
	}
	uid := currentUser(r).ID
	ctx := filterCtx(q)
	ctx.Profile = s.tasteProfile(r.Context(), uid, ix, s.pickRows(r.Context(), uid))
	ranked := ctx.Rank(films)
	head := mood.MMR(ranked, min(24, len(ranked)), 0.75, 150)
	in := map[int64]bool{}
	for _, h := range head {
		in[h.F.ID] = true
	}
	for _, x := range ranked {
		if !in[x.F.ID] {
			head = append(head, x)
		}
	}
	return head, nil
}

// cardsByID fetches cards and returns them keyed by id.
func (s *Server) cardsByID(r *http.Request, ids []int64) (map[int64]Card, error) {
	out := map[int64]Card{}
	if len(ids) == 0 {
		return out, nil
	}
	cs, err := s.cards(r, `WHERE i.id IN (`+idList(ids)+`)`)
	if err != nil {
		return nil, err
	}
	for _, c := range cs {
		out[c.ID] = c
	}
	return out, nil
}

// matchPage serves a page of the "Best match" order.
func (s *Server) matchPage(w http.ResponseWriter, r *http.Request, id int64, q url.Values, limit, offset int) {
	ranked, err := s.matchOrder(r, id, q)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	total := len(ranked)
	best := 0.0
	for i, x := range ranked {
		if i < 50 && x.Score > best {
			best = x.Score
		}
	}
	page := ranked[min(offset, total):min(offset+limit, total)]
	ids := make([]int64, len(page))
	for i, x := range page {
		ids[i] = x.F.ID
	}
	byID, err := s.cardsByID(r, ids)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	// "87% match" only means something when a mood or feel was asked for.
	asked := q.Get("mood") != "" || q.Get("feel") != ""
	cards := []Card{}
	for _, x := range page {
		if c, ok := byID[x.F.ID]; ok {
			if asked {
				c.Match = mood.MatchPct(x.Score, best)
			}
			cards = append(cards, c)
		}
	}
	writeJSON(w, map[string]any{"items": cards, "total": total, "offset": offset})
}

// Pick is one suggestion with its reason.
type Pick struct {
	Card
	Why    string   `json:"why"`
	Moods  []string `json:"moods"`
	Genres []string `json:"genres"`
	Blurb  string   `json:"blurb,omitempty"`
}

func (s *Server) picksOut(r *http.Request, ctx *mood.Ctx, ranked []mood.Ranked) ([]Pick, error) {
	ids := make([]int64, len(ranked))
	for i, x := range ranked {
		ids[i] = x.F.ID
	}
	byID, err := s.cardsByID(r, ids)
	if err != nil {
		return nil, err
	}
	blurbs := map[int64]string{}
	if len(ids) > 0 {
		if rows, err := s.db.R.QueryContext(r.Context(), `SELECT id, COALESCE(overview, '') FROM items WHERE id IN (`+idList(ids)+`)`); err == nil {
			for rows.Next() {
				var id int64
				var o string
				rows.Scan(&id, &o)
				blurbs[id] = o
			}
			rows.Close()
		}
	}
	best := 0.0
	for _, x := range ranked {
		best = max(best, x.Score)
	}
	out := []Pick{}
	for _, x := range ranked {
		c, ok := byID[x.F.ID]
		if !ok {
			continue
		}
		c.Match = mood.MatchPct(x.Score, best)
		out = append(out, Pick{Card: c, Why: ctx.Why(x), Moods: mood.MoodsOf(x.F.D), Genres: x.F.Genres, Blurb: blurbs[x.F.ID]})
	}
	return out, nil
}

// The sizes the apps show a pick's pictures at: the TV's
// poster and backdrop, then the phone's.
var pickPosterSizes, pickBackdropSizes = []string{"w500", "w342"}, []string{"w1280", "w780"}

// fetchArt makes sure one picture, from TMDB or Plex, is in the local cache.
func (s *Server) fetchArt(ctx context.Context, path, size string) {
	switch {
	case path == "":
	case strings.HasPrefix(path, "plex:"):
		s.plexArt(ctx, size, path[5:], false)
	default:
		s.art.Get(ctx, size, path)
	}
}

// warmPickArt fetches the pictures of suggested films into the local cache
// before the app asks for them: a pick's poster is otherwise downloaded the
// moment the film comes on screen, and arrives a second or two after its
// title. The first `now` picks are waited for (briefly), so the reply is sent
// with their pictures ready; the rest follow in the background, in the order
// they will be shown. backdrops is false where only posters are shown.
func (s *Server) warmPickArt(picks []Pick, now int, backdrops bool) {
	type job struct{ path, size string }
	jobs := func(ps []Pick) (out []job) {
		for _, p := range ps {
			for _, size := range pickPosterSizes {
				out = append(out, job{p.Poster, size})
			}
			if backdrops {
				for _, size := range pickBackdropSizes {
					out = append(out, job{p.Backdrop, size})
				}
			}
		}
		return out
	}
	run := func(js []job) {
		ctx, cancel := context.WithTimeout(s.baseCtx, 30*time.Second)
		defer cancel()
		var wg sync.WaitGroup
		slots := make(chan struct{}, 6)
		for _, j := range js {
			wg.Add(1)
			slots <- struct{}{}
			go func() {
				defer wg.Done()
				s.fetchArt(ctx, j.path, j.size)
				<-slots
			}()
		}
		wg.Wait()
	}
	now = min(now, len(picks))
	first := make(chan struct{})
	go func() {
		run(jobs(picks[:now]))
		close(first)
		run(jobs(picks[now:]))
	}()
	select {
	case <-first:
	case <-time.After(1200 * time.Millisecond):
	}
}

// fresh keeps the films the viewer hasn't seen and that aren't resting or hidden.
func fresh(films []*mood.Film, state map[int64]pickRow, exclude map[int64]bool) []*mood.Film {
	now := db.Now()
	out := films[:0:0]
	for _, f := range films {
		if !exclude[f.ID] && state[f.ID].eligible(now) {
			out = append(out, f)
		}
	}
	return out
}

const unwatchedSQL = ` AND COALESCE(w.watched, 0) = 0`

// handlePicks is "Surprise me": up to n suggestions from the library as it
// is filtered, best first with some variety and a little luck so every press
// is different. The app shows them one at a time and reports each as shown.
func (s *Server) handlePicks(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok || !currentUser(r).CanSee(id) {
		writeErr(w, 404, "not found")
		return
	}
	q := r.URL.Query()
	ix := s.ensureMoods(r.Context())
	films, err := s.libraryFilms(r, id, q, ix, unwatchedSQL)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	uid := currentUser(r).ID
	state := s.pickRows(r.Context(), uid)
	matching := len(films)
	films = fresh(films, state, nil)
	ctx := filterCtx(q)
	ctx.Profile = s.tasteProfile(r.Context(), uid, ix, state)
	ranked := ctx.Rank(films)
	pool := ranked[:min(80, len(ranked))]
	for i := range pool {
		pool[i].Score += rand.Float64() * 0.08
	}
	sort.SliceStable(pool, func(i, j int) bool { return pool[i].Score > pool[j].Score })
	picks, err := s.picksOut(r, ctx, mood.MMR(pool, queryInt(r, "n", 20, 40), 0.7, 80))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.warmPickArt(picks, 2, true)
	// matching - fresh = films left out because they are resting or hidden.
	writeJSON(w, map[string]any{"picks": picks, "matching": matching, "fresh": len(films)})
}

type quizAnswers struct {
	Feel    string  `json:"feel"`  // stressed | low | bored | happy | calm | tired | ""
	Mode    string  `json:"mode"`  // match | change
	Who     string  `json:"who"`   // solo | date | family | friends
	Time    string  `json:"time"`  // short | mid | long | ""
	Era     string  `json:"era"`   // classic | mid | new | ""
	Taste   string  `json:"taste"` // classic | gem | ""
	Exclude []int64 `json:"exclude"`
}

// handleQuiz is "Help me choose": the answers become a target mood, a few
// hard limits and a pool of picks, best first. The app shows three and
// backfills from the rest as picks are dismissed.
func (s *Server) handleQuiz(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok || !currentUser(r).CanSee(id) {
		writeErr(w, 404, "not found")
		return
	}
	var a quizAnswers
	if err := readJSON(r, &a); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	ix := s.ensureMoods(r.Context())
	all, err := s.libraryFilms(r, id, url.Values{}, ix, unwatchedSQL)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	uid := currentUser(r).ID
	state := s.pickRows(r.Context(), uid)
	exclude := map[int64]bool{}
	for _, x := range a.Exclude {
		exclude[x] = true
	}
	ctx := &mood.Ctx{Target: mood.Target{}, Profile: s.tasteProfile(r.Context(), uid, ix, state), Prefer: a.Taste}
	if f, ok := mood.FeelingByKey(a.Feel); ok {
		fl := strings.ToLower(f.Label)
		if a.Mode == "change" {
			ctx.Target.Merge(f.Repair)
			ctx.Lead = "You're " + fl + " and want a change, so this aims to lift the mood"
		} else {
			ctx.Target.Merge(f.Match)
			ctx.Lead = "You're " + fl + " and want to stay there, so this matches it"
		}
	}
	switch a.Who {
	case "date":
		if _, set := ctx.Target[mood.Romance]; !set {
			ctx.Target[mood.Romance] = .6
		}
	case "friends":
		for d, v := range map[int]float64{mood.Humour: .6, mood.Pace: .7} {
			if _, set := ctx.Target[d]; !set {
				ctx.Target[d] = v
			}
		}
	}
	keep := func(f *mood.Film) bool {
		d := f.D
		switch {
		case a.Who == "family" && !(d[mood.Family] >= .6 && d[mood.Scary] < .4 && d[mood.Violence] < .45):
			return false
		case a.Who == "date" && d[mood.Scary] >= .6:
			return false
		case a.Era == "classic" && !(f.Year > 0 && f.Year < 2000),
			a.Era == "mid" && !(f.Year >= 2000 && f.Year <= 2017),
			a.Era == "new" && f.Year < 2018:
			return false
		case a.Time == "short" && !(f.Runtime > 0 && f.Runtime < 90),
			a.Time == "mid" && !(f.Runtime > 0 && f.Runtime <= 120),
			a.Time == "long" && f.Runtime <= 120:
			return false
		}
		return true
	}
	minVotes := 400
	if a.Taste == "gem" {
		minVotes = 150
	}
	var cands, loose []*mood.Film
	for _, f := range fresh(all, state, exclude) {
		if !keep(f) {
			continue
		}
		loose = append(loose, f)
		// Films with too few votes to trust wait in reserve (a count of 0
		// means it isn't known, not that nobody voted).
		if f.Votes == 0 || f.Votes >= minVotes {
			cands = append(cands, f)
		}
	}
	if len(cands) < 3 {
		cands = loose
	}
	picks, err := s.picksOut(r, ctx, mood.MMR(ctx.Rank(cands), 15, 0.7, 120))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.warmPickArt(picks, 3, false)
	writeJSON(w, map[string]any{"picks": picks, "found": len(cands)})
}

// handlePickFeedback records what the viewer did with suggestions:
// shown, skip, notint, like, dislike, clear (the like or dislike) or unhide.
func (s *Server) handlePickFeedback(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs  []int64 `json:"ids"`
		Type string  `json:"type"`
	}
	if err := readJSON(r, &body); err != nil || len(body.IDs) == 0 || len(body.IDs) > 100 {
		writeErr(w, 400, "bad request")
		return
	}
	uid, now := currentUser(r).ID, db.Now()
	var set string
	var args []any
	switch body.Type {
	case "shown":
		set, args = "shown_at = ?3", []any{now}
	case "skip":
		// The third skip hides the film for a year.
		set = fmt.Sprintf(`ban_until = CASE WHEN strikes + 1 >= %d THEN ?3 ELSE ban_until END,
			ban_why = CASE WHEN strikes + 1 >= %d THEN 'skips' ELSE ban_why END,
			strikes = CASE WHEN strikes + 1 >= %d THEN 0 ELSE strikes + 1 END`, pickStrikes, pickStrikes, pickStrikes)
		args = []any{now + pickBan}
	case "notint":
		set, args = "ban_until = ?3, ban_why = 'notint', strikes = 0", []any{now + pickBan}
	case "like":
		set, args = "vote = ?3", []any{1}
	case "dislike":
		set, args = "vote = ?3", []any{-1}
	case "clear":
		set, args = "vote = ?3", []any{0}
	case "unhide":
		set, args = "ban_until = ?3, ban_why = NULL, strikes = 0", []any{0}
	default:
		writeErr(w, 400, "unknown type")
		return
	}
	out := map[string]any{"ok": true}
	for _, id := range body.IDs {
		if !s.itemVisible(r, id) {
			continue
		}
		if _, err := s.db.W.ExecContext(r.Context(), `INSERT INTO pick_state(user_id, item_id) VALUES (?1, ?2) ON CONFLICT(user_id, item_id) DO NOTHING`, uid, id); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if _, err := s.db.W.ExecContext(r.Context(), `UPDATE pick_state SET `+set+` WHERE user_id = ?1 AND item_id = ?2`, append([]any{uid, id}, args...)...); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		if body.Type == "skip" {
			var strikes int
			var until int64
			s.db.W.QueryRowContext(r.Context(), `SELECT strikes, ban_until FROM pick_state WHERE user_id = ? AND item_id = ?`, uid, id).Scan(&strikes, &until)
			out["strikes"], out["hidden"] = strikes, until > now
		}
	}
	writeJSON(w, out)
}

type genrePrefs struct {
	Exclude []string `json:"exclude"`
	Require []string `json:"require"`
}

func (s *Server) genrePrefs(ctx context.Context, uid int64) genrePrefs {
	g := genrePrefs{Exclude: []string{}, Require: []string{}}
	rows, err := s.db.R.QueryContext(ctx, `SELECT genre, mode FROM pick_genres WHERE user_id = ? ORDER BY genre`, uid)
	if err != nil {
		return g
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var mode int
		rows.Scan(&name, &mode)
		if mode == 2 {
			g.Require = append(g.Require, name)
		} else {
			g.Exclude = append(g.Exclude, name)
		}
	}
	return g
}

// handlePickState reports the viewer's saved genres and how much the picks
// remember, for the filter panel and its reset buttons.
func (s *Server) handlePickState(w http.ResponseWriter, r *http.Request) {
	uid, now := currentUser(r).ID, db.Now()
	var resting, hidden, struck, votes int
	s.db.R.QueryRowContext(r.Context(), `SELECT COALESCE(SUM(shown_at + ?2 > ?3), 0), COALESCE(SUM(ban_until > ?3), 0),
		COALESCE(SUM(strikes > 0), 0), COALESCE(SUM(vote != 0), 0) FROM pick_state WHERE user_id = ?1`, uid, pickCooldown, now).
		Scan(&resting, &hidden, &struck, &votes)
	writeJSON(w, map[string]any{"genres": s.genrePrefs(r.Context(), uid), "resting": resting, "hidden": hidden, "struck": struck, "votes": votes})
}

// handlePickGenres saves the genres the viewer never wants (exclude) or insists on (require).
func (s *Server) handlePickGenres(w http.ResponseWriter, r *http.Request) {
	var g genrePrefs
	if err := readJSON(r, &g); err != nil || len(g.Exclude)+len(g.Require) > 100 {
		writeErr(w, 400, "bad request")
		return
	}
	uid := currentUser(r).ID
	tx, err := s.db.W.BeginTx(r.Context(), nil)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer tx.Rollback()
	tx.ExecContext(r.Context(), `DELETE FROM pick_genres WHERE user_id = ?`, uid)
	add := func(names []string, mode int) {
		for _, n := range names {
			if n = strings.TrimSpace(n); n != "" && len(n) <= 60 {
				tx.ExecContext(r.Context(), `INSERT OR REPLACE INTO pick_genres(user_id, genre, mode) VALUES (?, ?, ?)`, uid, n, mode)
			}
		}
	}
	add(g.Exclude, 1)
	add(g.Require, 2)
	if err := tx.Commit(); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, s.genrePrefs(r.Context(), uid))
}

// handlePickReset forgets the rests and hidden films ("rules"), the likes
// and dislikes ("taste"), or both ("all").
func (s *Server) handlePickReset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		What string `json:"what"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	uid := currentUser(r).ID
	var q string
	switch body.What {
	case "rules":
		q = `UPDATE pick_state SET shown_at = 0, strikes = 0, ban_until = 0, ban_why = NULL WHERE user_id = ?`
	case "taste":
		q = `UPDATE pick_state SET vote = 0 WHERE user_id = ?`
	case "all":
		q = `DELETE FROM pick_state WHERE user_id = ?`
	default:
		writeErr(w, 400, "unknown reset")
		return
	}
	if _, err := s.db.W.ExecContext(r.Context(), q, uid); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.db.W.ExecContext(r.Context(), `DELETE FROM pick_state WHERE user_id = ? AND shown_at = 0 AND strikes = 0 AND ban_until = 0 AND vote = 0`, uid)
	writeJSON(w, map[string]bool{"ok": true})
}

// handlePickExport lists what the viewer liked, disliked and hid, by title.
func (s *Server) handlePickExport(w http.ResponseWriter, r *http.Request) {
	out := map[string][]string{"liked": {}, "disliked": {}, "hidden": {}}
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT i.title, COALESCE(i.year, 0), p.vote, p.ban_until FROM pick_state p
		JOIN items i ON i.id = p.item_id WHERE p.user_id = ? ORDER BY i.sort_title`, currentUser(r).ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	now := db.Now()
	for rows.Next() {
		var title string
		var year, vote int
		var until int64
		rows.Scan(&title, &year, &vote, &until)
		if year > 0 {
			title = fmt.Sprintf("%s (%d)", title, year)
		}
		if vote > 0 {
			out["liked"] = append(out["liked"], title)
		} else if vote < 0 {
			out["disliked"] = append(out["disliked"], title)
		}
		if until > now {
			out["hidden"] = append(out["hidden"], title)
		}
	}
	w.Header().Set("Content-Disposition", `attachment; filename="plexodus-picks.json"`)
	writeJSON(w, out)
}

// dimLabels are the bars on a film's page, in order.
var dimLabels = []struct {
	dim       int
	label     string
	low, high string
}{
	{mood.V, "Mood", "Bleak", "Uplifting"}, {mood.A, "Intensity", "Low-key", "Intense"}, {mood.Dark, "Tone", "Light", "Dark"},
	{mood.Humour, "Humour", "Serious", "Funny"}, {mood.Complex, "Complexity", "Easy watch", "Demanding"},
	{mood.Pace, "Pace", "Slow-burn", "Fast"}, {mood.Weight, "Tearjerker", "Dry-eyed", "Tissues"},
	{mood.Comfort, "Comfort", "Challenging", "Comforting"},
}

// handleItemMood is a film's mood profile for its page: the bars, the moods
// it fits, content notes, and the viewer's like or dislike.
func (s *Server) handleItemMood(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok || !s.itemVisible(r, id) {
		writeErr(w, 404, "not found")
		return
	}
	var tmdb int
	var genres, kind string
	if err := s.db.R.QueryRowContext(r.Context(), `SELECT kind, COALESCE(tmdb_id, 0), COALESCE(genres, '') FROM items WHERE id = ?`, id).
		Scan(&kind, &tmdb, &genres); err != nil || kind != "movie" {
		writeErr(w, 404, "not found")
		return
	}
	var gs []string
	json.Unmarshal([]byte(genres), &gs)
	d, scored := mood.For(tmdb, gs)
	type bar struct {
		Key   string `json:"key"`
		Label string `json:"label"`
		Low   string `json:"low"`
		High  string `json:"high"`
		Value int    `json:"value"`
	}
	bars := []bar{}
	for _, l := range dimLabels {
		bars = append(bars, bar{mood.Columns[l.dim], l.label, l.low, l.high, int(d[l.dim]*100 + .5)})
	}
	notes := []string{}
	for _, f := range mood.Flags {
		if f.Key != "romance" && f.Test(d) {
			notes = append(notes, f.Label)
		}
	}
	warn := []string{}
	if d[mood.Violence] >= .5 {
		warn = append(warn, "Violence")
	}
	if d[mood.Scary] >= .4 {
		warn = append(warn, "Scary")
	}
	var vote int
	var until int64
	err := s.db.R.QueryRowContext(r.Context(), `SELECT vote, ban_until FROM pick_state WHERE user_id = ? AND item_id = ?`, currentUser(r).ID, id).Scan(&vote, &until)
	if err != nil && err != sql.ErrNoRows {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"bars": bars, "moods": mood.MoodsOf(d), "notes": notes, "warnings": warn, "scored": scored,
		"vote": vote, "hidden": until > db.Now()})
}
