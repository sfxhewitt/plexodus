package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sfxhewitt/plexodus/internal/metadata"
)

// Cast and recommendations change rarely; keep them for a day per title.
var extrasCache = struct {
	sync.Mutex
	m map[int64]extrasEntry
}{m: map[int64]extrasEntry{}}

type extrasEntry struct {
	e  *metadata.Extras
	at time.Time
}

const extrasTTL = 24 * time.Hour

// handleExtras is the bottom of a film's or show's page: who's in it, and
// more like it from your own libraries (TMDB's recommendations first, then
// the best-rated titles sharing its genres).
func (s *Server) handleExtras(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var kind, genres string
	var tmdb int64
	err := s.db.R.QueryRowContext(r.Context(), `SELECT i.kind, COALESCE(i.tmdb_id, 0), COALESCE(i.genres, '[]') FROM items i`+
		aclJoin(currentUser(r), "i")+` WHERE i.id = ?`, id).Scan(&kind, &tmdb, &genres)
	if err != nil {
		writeErr(w, 404, "not found")
		return
	}
	cast := []metadata.Person{}
	similar := []Card{}
	if kind != "movie" && kind != "show" {
		writeJSON(w, map[string]any{"cast": cast, "similar": similar})
		return
	}
	var ex *metadata.Extras
	if tmdb > 0 {
		ex = s.extras(r.Context(), id, kind, tmdb)
	}
	if ex != nil {
		cast = append(cast, ex.Cast...)
		if len(ex.Recommendations) > 0 {
			// TMDB's order, best match first, mapped to titles you have.
			rows, err := s.db.R.QueryContext(r.Context(), `SELECT id, tmdb_id FROM items WHERE kind = ? AND id != ? AND tmdb_id IN (`+
				idList(ex.Recommendations)+`)`, kind, id)
			if err == nil {
				local := map[int64]int64{}
				for rows.Next() {
					var iid, t int64
					if rows.Scan(&iid, &t) == nil {
						if _, dup := local[t]; !dup {
							local[t] = iid
						}
					}
				}
				rows.Close()
				var ids []int64
				for _, t := range ex.Recommendations {
					if iid, ok := local[t]; ok {
						ids = append(ids, iid)
						delete(local, t)
					}
				}
				if cs, err := s.cardsByIDs(r, ids); err == nil {
					similar = append(similar, cs...)
				}
			}
		}
	}
	if len(similar) < 12 {
		var gs []string
		json.Unmarshal([]byte(genres), &gs)
		if len(gs) > 2 {
			gs = gs[:2]
		}
		if len(gs) > 0 {
			have := []int64{id}
			for _, c := range similar {
				have = append(have, c.ID)
			}
			// ?1 is the user, ?2 the kind, then the genres.
			args := []any{kind}
			var ph []string
			for i, g := range gs {
				args = append(args, g)
				ph = append(ph, fmt.Sprintf("?%d", i+3))
			}
			more, _ := s.cards(r, `WHERE i.kind = ?2 AND i.id NOT IN (`+idList(have)+`)
				AND EXISTS (SELECT 1 FROM json_each(i.genres) WHERE value IN (`+strings.Join(ph, ",")+`))
				AND i.rating IS NOT NULL ORDER BY i.rating DESC LIMIT `+fmt.Sprint(12-len(similar)), args...)
			similar = append(similar, more...)
		}
	}
	writeJSON(w, map[string]any{"cast": cast, "similar": similar})
}

// extras returns TMDB's cast and recommendations for an item, cached.
func (s *Server) extras(ctx context.Context, id int64, kind string, tmdb int64) *metadata.Extras {
	extrasCache.Lock()
	e, hit := extrasCache.m[id]
	extrasCache.Unlock()
	if hit && time.Since(e.at) < extrasTTL {
		return e.e
	}
	t := s.agent.TMDB()
	if !t.Enabled() {
		return nil
	}
	tk := "movie"
	if kind == "show" {
		tk = "tv"
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ex, err := t.Extras(ctx, tk, tmdb)
	if err != nil {
		return nil // not cached: TMDB may just be slow right now
	}
	extrasCache.Lock()
	if len(extrasCache.m) > 5000 {
		extrasCache.m = map[int64]extrasEntry{}
	}
	extrasCache.m[id] = extrasEntry{ex, time.Now()}
	extrasCache.Unlock()
	return ex
}
