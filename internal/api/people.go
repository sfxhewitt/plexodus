package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/sfxhewitt/plexodus/internal/metadata"
)

// A person's filmography changes rarely; keep it for a day.
var peopleCache = struct {
	sync.Mutex
	m map[int64]peopleEntry
}{m: map[int64]peopleEntry{}}

type peopleEntry struct {
	p  *metadata.PersonDetails
	at time.Time
}

const maxElsewhere = 60

// handlePerson is a cast member's page: who they are, what of theirs is in
// your libraries (playable), then the rest of their best-known work.
func (s *Server) handlePerson(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "id")
	if !ok || id <= 0 {
		writeErr(w, 400, "bad id")
		return
	}
	p, err := s.person(r.Context(), id)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	u := currentUser(r)

	// Which credits you have: kind + TMDB id to the library copies you can see.
	var tmdbIDs []int64
	for _, c := range p.Credits {
		tmdbIDs = append(tmdbIDs, c.TMDB)
	}
	type key struct {
		kind string
		tmdb int64
	}
	local := map[key][]int64{}
	if len(tmdbIDs) > 0 {
		rows, err := s.db.R.QueryContext(r.Context(), `SELECT i.id, i.kind, i.tmdb_id FROM items i`+aclJoin(u, "i")+
			` WHERE i.tmdb_id IN (`+idList(tmdbIDs)+`) AND i.kind IN ('movie', 'show')`)
		if err == nil {
			for rows.Next() {
				var iid, t int64
				var k string
				if rows.Scan(&iid, &k, &t) == nil {
					local[key{k, t}] = append(local[key{k, t}], iid)
				}
			}
			rows.Close()
		}
	}
	var ids []int64
	roles := map[int64]string{}
	elsewhere := []metadata.Credit{}
	for _, c := range p.Credits {
		if have := local[key{c.Kind, c.TMDB}]; len(have) > 0 {
			ids = append(ids, have...)
			for _, iid := range have {
				roles[iid] = c.Character
			}
			continue
		}
		// A kids profile sees only what's in its own libraries.
		if u.MaxAge == nil && len(elsewhere) < maxElsewhere {
			elsewhere = append(elsewhere, c)
		}
	}
	cards, _ := s.cardsByIDs(r, ids)
	cards = s.onePerTitle(r, cards, 0)
	if cards == nil {
		cards = []Card{}
	}
	keep := map[int64]string{}
	for _, c := range cards {
		if roles[c.ID] != "" {
			keep[c.ID] = roles[c.ID]
		}
	}
	info := *p
	info.Credits = nil
	writeJSON(w, map[string]any{"person": info, "inLibrary": cards, "roles": keep, "elsewhere": elsewhere})
}

func (s *Server) person(ctx context.Context, id int64) (*metadata.PersonDetails, error) {
	peopleCache.Lock()
	e, hit := peopleCache.m[id]
	peopleCache.Unlock()
	if hit && time.Since(e.at) < extrasTTL {
		return e.p, nil
	}
	t := s.agent.TMDB()
	if !t.Enabled() {
		return nil, errNoTMDB
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	p, err := t.Person(ctx, id)
	if err != nil {
		return nil, errPersonLookup
	}
	peopleCache.Lock()
	if len(peopleCache.m) > 2000 {
		peopleCache.m = map[int64]peopleEntry{}
	}
	peopleCache.m[id] = peopleEntry{p, time.Now()}
	peopleCache.Unlock()
	return p, nil
}

type personErr string

func (e personErr) Error() string { return string(e) }

const (
	errNoTMDB       personErr = "Cast pages need a TMDB key in Settings"
	errPersonLookup personErr = "Couldn't reach TMDB for this person. Try again in a moment"
)
