package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/sfxhewitt/plexodus/internal/metadata"
)

// Episode guide: every episode of a show TMDB knows about, aired or not, set
// against the library, so a show's page can say "8 of 10 episodes", grey out
// what's missing and show what's coming. A show's guide is fetched from TMDB
// the first time someone opens it (never in bulk), kept in SQLite and memory,
// and refreshed in the background once it's stale, so pages never wait on
// TMDB twice.

const (
	guideTTL      = 12 * time.Hour      // shows still airing
	guideEndedTTL = 30 * 24 * time.Hour // ended or cancelled
	guideMemMax   = 1000
)

type guideEntry struct {
	g  *metadata.Guide
	at time.Time
}

type guideStore struct {
	mu       sync.Mutex
	mem      map[int64]guideEntry
	inflight map[int64]chan struct{}
}

var guides = &guideStore{mem: map[int64]guideEntry{}, inflight: map[int64]chan struct{}{}}

// fetchGuide is TMDB's ShowGuide; tests swap it.
var fetchGuide = func(s *Server, ctx context.Context, tmdbID int64) (*metadata.Guide, error) {
	t := s.agent.TMDB()
	if !t.Enabled() {
		return nil, errNoTMDBGuide
	}
	return t.ShowGuide(ctx, tmdbID)
}

func (e guideEntry) fresh(now time.Time) bool {
	ttl := guideTTL
	if e.g.Ended() {
		ttl = guideEndedTTL
	}
	return now.Sub(e.at) < ttl
}

// guide returns a show's guide: from memory, else SQLite, else TMDB. Stale
// copies are returned at once and refreshed in the background.
func (s *Server) guide(ctx context.Context, tmdbID int64) (*metadata.Guide, error) {
	now := time.Now()
	guides.mu.Lock()
	e, hit := guides.mem[tmdbID]
	guides.mu.Unlock()
	if !hit {
		var data string
		var at int64
		if s.db.R.QueryRowContext(ctx, `SELECT data, fetched_at FROM tmdb_guides WHERE tmdb_id = ?`, tmdbID).Scan(&data, &at) == nil {
			var g metadata.Guide
			if json.Unmarshal([]byte(data), &g) == nil {
				e, hit = guideEntry{&g, time.Unix(at, 0)}, true
				s.rememberGuide(tmdbID, e)
			}
		}
	}
	if hit {
		if !e.fresh(now) {
			go s.refreshGuide(tmdbID)
		}
		return e.g, nil
	}
	if err := s.refreshGuide(tmdbID); err != nil {
		return nil, err
	}
	guides.mu.Lock()
	e, hit = guides.mem[tmdbID]
	guides.mu.Unlock()
	if !hit {
		return nil, errGuideLookup
	}
	return e.g, nil
}

func (s *Server) rememberGuide(tmdbID int64, e guideEntry) {
	guides.mu.Lock()
	if len(guides.mem) >= guideMemMax {
		guides.mem = map[int64]guideEntry{}
	}
	guides.mem[tmdbID] = e
	guides.mu.Unlock()
}

// refreshGuide fetches a guide from TMDB and stores it. Concurrent callers
// for the same show share one fetch.
func (s *Server) refreshGuide(tmdbID int64) error {
	guides.mu.Lock()
	if ch, busy := guides.inflight[tmdbID]; busy {
		guides.mu.Unlock()
		<-ch
		return nil
	}
	ch := make(chan struct{})
	guides.inflight[tmdbID] = ch
	guides.mu.Unlock()
	defer func() {
		guides.mu.Lock()
		delete(guides.inflight, tmdbID)
		guides.mu.Unlock()
		close(ch)
	}()

	ctx, cancel := context.WithTimeout(s.baseCtx, 20*time.Second)
	defer cancel()
	g, err := fetchGuide(s, ctx, tmdbID)
	if err != nil {
		if err != errNoTMDBGuide {
			log.Printf("guide: tmdb %d: %v", tmdbID, err)
			return errGuideLookup
		}
		return err
	}
	now := time.Now()
	s.rememberGuide(tmdbID, guideEntry{g, now})
	if data, err := json.Marshal(g); err == nil {
		s.db.W.ExecContext(ctx, `INSERT INTO tmdb_guides(tmdb_id, data, fetched_at) VALUES (?, ?, ?)
			ON CONFLICT(tmdb_id) DO UPDATE SET data = excluded.data, fetched_at = excluded.fetched_at`, tmdbID, string(data), now.Unix())
	}
	return nil
}

type guideErr string

func (e guideErr) Error() string { return string(e) }

const (
	errNoTMDBGuide guideErr = "The full episode list needs a TMDB key in Settings"
	errGuideLookup guideErr = "Couldn't reach TMDB for the episode list"
)

// ---- the API ----

// GuideSeason is one season as the page shows it.
type GuideSeason struct {
	Season  int    `json:"season"`
	Name    string `json:"name"`
	AirDate string `json:"airDate,omitempty"`
	Poster  string `json:"poster,omitempty"`
	// ItemID is the library's season, 0 when you have none of it.
	ItemID int64 `json:"itemId,omitempty"`
	// Total is every episode TMDB lists, Aired those out by today, Have the
	// ones in the library. Total 0 with Have > 0: TMDB numbers this season
	// differently, so nothing is called missing.
	Total int `json:"total"`
	Aired int `json:"aired"`
	Have  int `json:"have"`
	// Next unaired episode's date, "" if none is announced.
	NextAirDate string `json:"nextAirDate,omitempty"`
	// State: "have" (every aired episode), "partial", "missing" (none),
	// "upcoming" (nothing has aired yet).
	State string `json:"state"`
}

// GuideEpisode is one episode; ItemID 0 means it isn't in the library.
type GuideEpisode struct {
	metadata.GuideEpisode
	ItemID int64 `json:"itemId,omitempty"`
	// "have", "missing" (aired) or "upcoming".
	State string `json:"state"`
}

type GuideResponse struct {
	Available bool          `json:"available"`
	Message   string        `json:"message,omitempty"`
	Status    string        `json:"status,omitempty"`
	Total     int           `json:"total"` // regular seasons only
	Aired     int           `json:"aired"`
	Have      int           `json:"have"`
	Next      *GuideEpisode `json:"next,omitempty"` // next episode to air
	Seasons   []GuideSeason `json:"seasons"`
	// Episodes of one season: the one asked for (?season=N on a show, or a
	// season's own id). Empty when TMDB numbers it differently.
	Season   *int           `json:"season,omitempty"`
	Episodes []GuideEpisode `json:"episodes"`
}

// handleGuide: GET /api/items/{id}/guide for a show (add ?season=N for that
// season's episodes) or a season.
func (s *Server) handleGuide(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok || !s.itemVisible(r, id) {
		writeErr(w, 404, "not found")
		return
	}
	var kind string
	var parent sql.NullInt64
	var seasonNo sql.NullInt64
	if s.db.R.QueryRowContext(r.Context(), `SELECT kind, parent_id, season_number FROM items WHERE id = ?`, id).Scan(&kind, &parent, &seasonNo) != nil {
		writeErr(w, 404, "not found")
		return
	}
	showID := id
	want := -1
	switch kind {
	case "show":
		if v := r.URL.Query().Get("season"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n >= 0 {
				want = n
			}
		}
	case "season":
		showID = parent.Int64
		want = int(seasonNo.Int64)
	default:
		writeErr(w, 400, "not a show or season")
		return
	}
	var tmdbID int64
	s.db.R.QueryRowContext(r.Context(), `SELECT COALESCE(tmdb_id, 0) FROM items WHERE id = ? AND kind = 'show'`, showID).Scan(&tmdbID)
	out := GuideResponse{Seasons: []GuideSeason{}, Episodes: []GuideEpisode{}}
	if want >= 0 {
		out.Season = &want
	}
	if tmdbID <= 0 {
		out.Message = "This show isn't matched to TMDB yet"
		writeJSON(w, out)
		return
	}
	g, err := s.guide(r.Context(), tmdbID)
	if err != nil {
		out.Message = err.Error()
		writeJSON(w, out)
		return
	}
	buildGuide(&out, g, s.localEpisodes(r.Context(), showID), want, time.Now().Format("2006-01-02"))
	writeJSON(w, out)
}

type localSeason struct {
	id       int64
	episodes map[int]int64 // episode number → item id
}

// localEpisodes is the show's seasons and episodes in the library.
func (s *Server) localEpisodes(ctx context.Context, showID int64) map[int]*localSeason {
	out := map[int]*localSeason{}
	rows, err := s.db.R.QueryContext(ctx, `SELECT se.id, COALESCE(se.season_number, 0), e.id, e.episode_number
		FROM items se LEFT JOIN items e ON e.parent_id = se.id AND e.kind = 'episode'
		WHERE se.parent_id = ? AND se.kind = 'season'`, showID)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var sid int64
		var sn int
		var eid, en sql.NullInt64
		if rows.Scan(&sid, &sn, &eid, &en) != nil {
			continue
		}
		ls := out[sn]
		if ls == nil {
			ls = &localSeason{id: sid, episodes: map[int]int64{}}
			out[sn] = ls
		}
		if eid.Valid && en.Valid {
			if _, dup := ls.episodes[int(en.Int64)]; !dup {
				ls.episodes[int(en.Int64)] = eid.Int64
			}
		}
	}
	return out
}

// numberedAlike says the library's episode numbers fit TMDB's for a season.
// Anime numbered from the first episode ever (S01E500) doesn't, and then
// nothing in that season is called missing.
func numberedAlike(ls *localSeason, eps []metadata.GuideEpisode) bool {
	if ls == nil || len(ls.episodes) == 0 {
		return true
	}
	if len(eps) == 0 {
		return false
	}
	known := map[int]bool{}
	for _, e := range eps {
		known[e.Episode] = true
	}
	stray := 0
	for n := range ls.episodes {
		if !known[n] {
			stray++
		}
	}
	return stray <= 2 && stray*4 <= len(ls.episodes)
}

func buildGuide(out *GuideResponse, g *metadata.Guide, local map[int]*localSeason, want int, today string) {
	out.Available = true
	out.Status = g.Status
	aired := func(d string) bool { return d != "" && d <= today }
	for _, gs := range g.Seasons {
		ls := local[gs.Season]
		// Specials are an odd mix (extras, shorts, webisodes); only show
		// them when the library has some.
		if gs.Season == 0 && ls == nil {
			continue
		}
		sea := GuideSeason{Season: gs.Season, Name: gs.Name, AirDate: gs.AirDate, Poster: gs.Poster}
		if ls != nil {
			sea.ItemID = ls.id
		}
		alike := numberedAlike(ls, gs.Episodes)
		var eps []GuideEpisode
		for _, e := range gs.Episodes {
			ge := GuideEpisode{GuideEpisode: e}
			if ls != nil {
				ge.ItemID = ls.episodes[e.Episode]
			}
			switch {
			case ge.ItemID != 0:
				ge.State = "have"
			case aired(e.AirDate):
				ge.State = "missing"
			default:
				ge.State = "upcoming"
				if sea.NextAirDate == "" && e.AirDate != "" {
					sea.NextAirDate = e.AirDate
				}
				if gs.Season > 0 && (out.Next == nil || e.AirDate != "" && (out.Next.AirDate == "" || e.AirDate < out.Next.AirDate)) {
					n := ge
					out.Next = &n
				}
			}
			if aired(e.AirDate) || ge.ItemID != 0 {
				sea.Aired++
			}
			if ge.ItemID != 0 {
				sea.Have++
			}
			eps = append(eps, ge)
		}
		sea.Total = len(gs.Episodes)
		if !alike {
			// Numbering doesn't line up: report what's here, claim nothing missing.
			sea.Total, sea.Aired, sea.Have = 0, 0, len(ls.episodes)
			sea.NextAirDate = ""
			eps = nil
		}
		switch {
		case !alike || sea.Have > 0 && sea.Have >= sea.Aired:
			sea.State = "have"
		case sea.Have > 0:
			sea.State = "partial"
		case sea.Aired > 0:
			sea.State = "missing"
		default:
			sea.State = "upcoming"
		}
		if gs.Season > 0 && alike {
			out.Total += sea.Total
			out.Aired += sea.Aired
			out.Have += sea.Have
		}
		if gs.Season == want && eps != nil {
			out.Episodes = eps
		}
		out.Seasons = append(out.Seasons, sea)
	}
	// Seasons in the library that TMDB doesn't list keep their place.
	for n, ls := range local {
		found := false
		for _, gs := range out.Seasons {
			if gs.Season == n {
				found = true
				break
			}
		}
		if !found {
			out.Seasons = append(out.Seasons, GuideSeason{Season: n, Name: seasonName(n), ItemID: ls.id, Have: len(ls.episodes), State: "have"})
		}
	}
	sort.SliceStable(out.Seasons, func(i, j int) bool {
		a, b := out.Seasons[i].Season, out.Seasons[j].Season
		if (a == 0) != (b == 0) {
			return b == 0 // specials last, like the library
		}
		return a < b
	})
}

func seasonName(n int) string {
	if n == 0 {
		return "Specials"
	}
	return "Season " + strconv.Itoa(n)
}
