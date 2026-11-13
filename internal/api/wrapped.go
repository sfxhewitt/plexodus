package api

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"time"
	_ "time/tzdata" // the static binary has no system zoneinfo to rely on
)

// Plexodus Wrapped: a user's year in watching, worked out from
// watch_history (plays here plus Plex's play history).

type WrappedTitle struct {
	Card    Card    `json:"card"`
	Plays   int     `json:"plays"`
	Seconds float64 `json:"seconds"`
}

type WrappedGenre struct {
	Name    string  `json:"name"`
	Seconds float64 `json:"seconds"`
}

type WrappedDay struct {
	Date    string  `json:"date"` // YYYY-MM-DD in the viewer's time zone
	Plays   int     `json:"plays"`
	Seconds float64 `json:"seconds"`
}

type Wrapped struct {
	Year     int     `json:"year"`
	Years    []int   `json:"years"`
	Plays    int     `json:"plays"`
	Movies   int     `json:"movies"`
	Episodes int     `json:"episodes"`
	Shows    int     `json:"shows"`
	Seconds  float64 `json:"seconds"`
	Days     int     `json:"days"` // days with at least one play

	TopShows  []WrappedTitle `json:"topShows"`
	TopMovies []WrappedTitle `json:"topMovies"`
	TopGenres []WrappedGenre `json:"topGenres"`

	Hours    [24]float64 `json:"hours"`    // seconds watched by hour of day
	Weekdays [7]float64  `json:"weekdays"` // Sunday first
	Months   [12]float64 `json:"months"`
	PeakHour int         `json:"peakHour"`
	Persona  string      `json:"persona"`
	Blurb    string      `json:"blurb"`

	// Longest run of consecutive days with something watched.
	Streak struct {
		Days  int    `json:"days"`
		Start string `json:"start,omitempty"`
		End   string `json:"end,omitempty"`
	} `json:"streak"`
	// Most episodes of one show in a single day.
	Binge *struct {
		Show     Card    `json:"show"`
		Episodes int     `json:"episodes"`
		Date     string  `json:"date"`
		Seconds  float64 `json:"seconds"`
	} `json:"binge,omitempty"`
	BiggestDay *WrappedDay `json:"biggestDay,omitempty"`
	First      *struct {
		Card Card  `json:"card"`
		At   int64 `json:"at"`
	} `json:"first,omitempty"`
}

// Lengths used when nothing in the database says how long an item is.
const (
	guessEpisode = 45 * 60
	guessMovie   = 110 * 60
)

func (s *Server) wrappedYears(r *http.Request, uid int64) []int {
	years := []int{}
	rows, err := s.db.R.QueryContext(r.Context(), `SELECT DISTINCT CAST(strftime('%Y', watched_at, 'unixepoch') AS INTEGER) AS y
		FROM watch_history WHERE user_id = ? ORDER BY y DESC`, uid)
	if err != nil {
		return years
	}
	defer rows.Close()
	for rows.Next() {
		var y int
		rows.Scan(&y)
		years = append(years, y)
	}
	return years
}

func (s *Server) handleWrapped(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	ctx := r.Context()
	loc, err := time.LoadLocation(r.URL.Query().Get("tz"))
	if err != nil || r.URL.Query().Get("tz") == "" {
		loc = time.Local
	}
	// Judged by the server clock, as the Home tile is, so the two agree.
	season, open := wrappedSeason(wrappedNow())
	if !open {
		writeErr(w, 404, "Plexodus Wrapped is back on 26 December.")
		return
	}
	// Only finished (or finishing) years: in January, not the one just begun.
	years := []int{}
	for _, y := range s.wrappedYears(r, u.ID) {
		if y <= season {
			years = append(years, y)
		}
	}
	year, _ := strconv.Atoi(r.URL.Query().Get("year"))
	if year == 0 || year > season {
		year = season
	}
	out := Wrapped{Year: year, Years: years, TopShows: []WrappedTitle{}, TopMovies: []WrappedTitle{}, TopGenres: []WrappedGenre{}}
	from := time.Date(year, 1, 1, 0, 0, 0, 0, loc).Unix()
	to := time.Date(year+1, 1, 1, 0, 0, 0, 0, loc).Unix()

	rows, err := s.db.R.QueryContext(ctx, `SELECT h.item_id, h.watched_at, i.kind,
			COALESCE(NULLIF(h.seconds, 0), (SELECT MAX(duration) FROM media_files f WHERE f.item_id = h.item_id), i.runtime * 60, 0),
			COALESCE(sh.id, 0), COALESCE(CASE WHEN i.kind = 'episode' THEN sh.genres ELSE i.genres END, '[]')
		FROM watch_history h JOIN items i ON i.id = h.item_id`+aclJoin(u, "i")+`
		LEFT JOIN items se ON i.kind = 'episode' AND se.id = i.parent_id
		LEFT JOIN items sh ON sh.id = se.parent_id
		WHERE h.user_id = ? AND h.watched_at >= ? AND h.watched_at < ?
		ORDER BY h.watched_at`, u.ID, from, to)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	type play struct {
		item, at, show int64
		kind           string
		secs           float64
		genres         string
	}
	var plays []play
	for rows.Next() {
		var p play
		if err := rows.Scan(&p.item, &p.at, &p.kind, &p.secs, &p.show, &p.genres); err != nil {
			rows.Close()
			writeErr(w, 500, err.Error())
			return
		}
		if p.secs <= 0 {
			p.secs = guessMovie
			if p.kind == "episode" {
				p.secs = guessEpisode
			}
		}
		plays = append(plays, p)
	}
	rows.Close()

	type tally struct {
		plays int
		secs  float64
	}
	shows := map[int64]*tally{}
	movies := map[int64]*tally{}
	genres := map[string]float64{}
	days := map[string]*WrappedDay{}
	showDay := map[[2]string]*tally{} // (show id, date) -> episodes that day
	for _, p := range plays {
		t := time.Unix(p.at, 0).In(loc)
		out.Plays++
		out.Seconds += p.secs
		out.Hours[t.Hour()] += p.secs
		out.Weekdays[int(t.Weekday())] += p.secs
		out.Months[int(t.Month())-1] += p.secs
		date := t.Format("2006-01-02")
		d := days[date]
		if d == nil {
			d = &WrappedDay{Date: date}
			days[date] = d
		}
		d.Plays++
		d.Seconds += p.secs
		var gs []string
		json.Unmarshal([]byte(p.genres), &gs)
		for _, g := range gs {
			genres[g] += p.secs
		}
		switch {
		case p.kind == "episode" && p.show != 0:
			out.Episodes++
			if shows[p.show] == nil {
				shows[p.show] = &tally{}
			}
			shows[p.show].plays++
			shows[p.show].secs += p.secs
			k := [2]string{strconv.FormatInt(p.show, 10), date}
			if showDay[k] == nil {
				showDay[k] = &tally{}
			}
			showDay[k].plays++
			showDay[k].secs += p.secs
		case p.kind == "movie":
			out.Movies++
			if movies[p.item] == nil {
				movies[p.item] = &tally{}
			}
			movies[p.item].plays++
			movies[p.item].secs += p.secs
		}
	}
	out.Shows = len(shows)
	out.Days = len(days)

	top := func(m map[int64]*tally, n int) ([]int64, map[int64]*tally) {
		ids := make([]int64, 0, len(m))
		for id := range m {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(a, b int) bool {
			if m[ids[a]].secs != m[ids[b]].secs {
				return m[ids[a]].secs > m[ids[b]].secs
			}
			return ids[a] < ids[b]
		})
		if len(ids) > n {
			ids = ids[:n]
		}
		return ids, m
	}
	titles := func(ids []int64, m map[int64]*tally) []WrappedTitle {
		cards, _ := s.cardsByIDs(r, ids)
		res := []WrappedTitle{}
		for _, c := range cards {
			res = append(res, WrappedTitle{Card: c, Plays: m[c.ID].plays, Seconds: m[c.ID].secs})
		}
		return res
	}
	out.TopShows = titles(top(shows, 5))
	out.TopMovies = titles(top(movies, 5))

	for name, secs := range genres {
		out.TopGenres = append(out.TopGenres, WrappedGenre{Name: name, Seconds: secs})
	}
	sort.Slice(out.TopGenres, func(a, b int) bool {
		if out.TopGenres[a].Seconds != out.TopGenres[b].Seconds {
			return out.TopGenres[a].Seconds > out.TopGenres[b].Seconds
		}
		return out.TopGenres[a].Name < out.TopGenres[b].Name
	})
	if len(out.TopGenres) > 5 {
		out.TopGenres = out.TopGenres[:5]
	}

	// Peak hour and the persona that goes with it.
	for h := range out.Hours {
		if out.Hours[h] > out.Hours[out.PeakHour] {
			out.PeakHour = h
		}
	}
	out.Persona, out.Blurb = persona(out.PeakHour, out.Plays)

	// Streak, biggest day.
	dates := make([]string, 0, len(days))
	for d := range days {
		dates = append(dates, d)
	}
	sort.Strings(dates)
	run, runStart := 0, ""
	var prev time.Time
	for _, ds := range dates {
		d, _ := time.ParseInLocation("2006-01-02", ds, loc)
		if run > 0 && d.Sub(prev) <= 25*time.Hour { // DST days are 23-25 hours
			run++
		} else {
			run, runStart = 1, ds
		}
		prev = d
		if run > out.Streak.Days {
			out.Streak.Days, out.Streak.Start, out.Streak.End = run, runStart, ds
		}
		day := days[ds]
		if out.BiggestDay == nil || day.Seconds > out.BiggestDay.Seconds {
			out.BiggestDay = day
		}
	}

	// Binge record: most episodes of one show in a day (at least three).
	var bk [2]string
	var best *tally
	for k, t := range showDay {
		if best == nil || t.plays > best.plays || (t.plays == best.plays && (t.secs > best.secs || (t.secs == best.secs && k[1] < bk[1]))) {
			bk, best = k, t
		}
	}
	if best != nil && best.plays >= 3 {
		id, _ := strconv.ParseInt(bk[0], 10, 64)
		if cs, _ := s.cardsByIDs(r, []int64{id}); len(cs) == 1 {
			out.Binge = &struct {
				Show     Card    `json:"show"`
				Episodes int     `json:"episodes"`
				Date     string  `json:"date"`
				Seconds  float64 `json:"seconds"`
			}{cs[0], best.plays, bk[1], best.secs}
		}
	}

	// The year's first play (an episode stands for its show).
	if len(plays) > 0 {
		id := plays[0].item
		if plays[0].show != 0 {
			id = plays[0].show
		}
		if cs, _ := s.cardsByIDs(r, []int64{id}); len(cs) == 1 {
			out.First = &struct {
				Card Card  `json:"card"`
				At   int64 `json:"at"`
			}{cs[0], plays[0].at}
		}
	}
	writeJSON(w, out)
}

func persona(hour, plays int) (string, string) {
	if plays == 0 {
		return "", ""
	}
	switch {
	case hour >= 5 && hour < 11:
		return "The Early Bird", "Most of your watching happened before lunch. Coffee and credits."
	case hour >= 11 && hour < 17:
		return "The Matinee Idol", "Afternoons were yours: the screen was on while everyone else was at work."
	case hour >= 17 && hour < 22:
		return "The Prime-Timer", "Evenings, sofa, something good on. A classic."
	default:
		return "The Night Owl", "Your peak was after dark. The best stories start late."
	}
}

// wrappedTeaser says whether Home should offer Wrapped: the user has plays
// in the current year (or, in early January, last year).
func (s *Server) wrappedTeaser(r *http.Request) map[string]int {
	year, ok := wrappedSeason(wrappedNow())
	if !ok {
		return nil
	}
	from := time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	to := time.Date(year+1, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	var n int
	s.db.R.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM watch_history
		WHERE user_id = ? AND watched_at >= ? AND watched_at < ?`, currentUser(r).ID, from, to).Scan(&n)
	if n == 0 {
		return nil
	}
	return map[string]int{"year": year, "plays": n}
}

// wrappedNow is the clock Wrapped's season is judged by (tests replace it).
var wrappedNow = time.Now

// wrappedSeason says whether Wrapped is open at t and which year it looks
// back on. It is only around in a short window at the turn of the year:
// from 26 December (showing the year that is ending) to the end of January
// (showing the year just gone). The rest of the year it is hidden.
func wrappedSeason(t time.Time) (year int, open bool) {
	switch {
	case t.Month() == time.December && t.Day() >= 26:
		return t.Year(), true
	case t.Month() == time.January:
		return t.Year() - 1, true
	}
	return 0, false
}
