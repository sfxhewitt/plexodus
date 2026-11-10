package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sfxhewitt/plexodus/internal/metadata"
)

// Ambient extras for the TV interface: trailers that play behind a title's
// art, and the idle screensaver. Both read only the database and TMDB; no
// media file is touched.

// ---- trailers ----

type trailerEntry struct {
	key string
	at  time.Time
}

var trailerCache = struct {
	sync.Mutex
	m map[int64]trailerEntry
}{m: map[int64]trailerEntry{}}

const (
	trailerHitTTL   = 7 * 24 * time.Hour
	trailerMissTTL  = 24 * time.Hour
	trailerCacheMax = 20000
)

// handleTrailer returns the YouTube key of a film's or show's trailer from
// TMDB: {"youtube": "<key>"}, or an empty key when there isn't one.
func (s *Server) handleTrailer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var kind, imdb string
	var tmdb int64
	err := s.db.R.QueryRowContext(r.Context(), `SELECT i.kind, COALESCE(i.tmdb_id, 0), COALESCE(i.imdb_id, '') FROM items i`+
		aclJoin(currentUser(r), "i")+` WHERE i.id = ?`, id).Scan(&kind, &tmdb, &imdb)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	if kind != "movie" && kind != "show" {
		writeJSON(w, map[string]string{"youtube": ""})
		return
	}

	trailerCache.Lock()
	e, hit := trailerCache.m[id]
	trailerCache.Unlock()
	if hit {
		ttl := trailerMissTTL
		if e.key != "" {
			ttl = trailerHitTTL
		}
		if time.Since(e.at) < ttl {
			writeTrailer(w, e.key)
			return
		}
	}

	t := s.agent.TMDB()
	if !t.Enabled() {
		writeJSON(w, map[string]string{"youtube": ""})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	key, err := lookupTrailer(ctx, t, kind, tmdb, imdb)
	if err != nil {
		// Not cached: TMDB may just be slow right now.
		writeJSON(w, map[string]string{"youtube": ""})
		return
	}
	trailerCache.Lock()
	if len(trailerCache.m) >= trailerCacheMax {
		trailerCache.m = map[int64]trailerEntry{}
	}
	trailerCache.m[id] = trailerEntry{key: key, at: time.Now()}
	trailerCache.Unlock()
	writeTrailer(w, key)
}

func writeTrailer(w http.ResponseWriter, key string) {
	w.Header().Set("Content-Type", "application/json")
	// The answer rarely changes: let the TV keep it for a day.
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write([]byte(`{"youtube":` + strconv.Quote(key) + "}\n"))
}

func lookupTrailer(ctx context.Context, t *metadata.TMDB, kind string, tmdb int64, imdb string) (string, error) {
	tk := "movie"
	if kind == "show" {
		tk = "tv"
	}
	if tmdb == 0 && strings.HasPrefix(imdb, "tt") {
		m, sh, err := t.FindIMDb(ctx, imdb)
		if err != nil {
			return "", err
		}
		if kind == "show" {
			tmdb = sh
		} else {
			tmdb = m
		}
	}
	if tmdb == 0 {
		return "", nil
	}
	vs, err := t.Videos(ctx, tk, tmdb)
	if err != nil {
		return "", err
	}
	return metadata.BestTrailer(vs), nil
}

// ---- screensaver ----

// handleScreensaver returns a random handful of films and shows with
// full-screen art, from the libraries the user can see and keeps on Home.
func (s *Server) handleScreensaver(w http.ResponseWriter, r *http.Request) {
	u := currentUser(r)
	acl := ""
	if !u.IsAdmin && !u.AllLibraries {
		acl = " AND library_id IN (" + idList(u.Libraries) + ")"
	}
	n := queryInt(r, "n", 30, 60)
	pick := func(extra string) ([]Card, error) {
		return s.cards(r, `WHERE i.id IN (SELECT id FROM items m WHERE kind IN ('movie', 'show') AND backdrop IS NOT NULL`+
			acl+notInLibs(s.hiddenHomeLibraries(r))+extra+` ORDER BY RANDOM() LIMIT `+strconv.Itoa(n)+`) ORDER BY RANDOM()`)
	}
	// The screensaver suggests something to play, so it skips titles the
	// viewer has seen or is watching (unless that leaves nothing to show).
	var cards []Card
	started, err := s.startedShows(r.Context(), u.ID)
	if err == nil {
		cards, err = pick(" AND " + unseenAt("m", started))
	}
	if err == nil && len(cards) < 5 {
		cards, err = pick("")
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if cards == nil {
		cards = []Card{}
	}
	writeJSON(w, cards)
}
