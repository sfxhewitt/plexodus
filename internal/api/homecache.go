package api

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Home is cached per user. On Daniel's seedbox the database shares a disk
// with the rclone cache, and while a big library is being scanned a Home
// build can take minutes. Before this cache, a client that gave up waiting
// cancelled the build, so nothing was ever kept and every visit started from
// scratch: the TV just spun. Now a build always runs to completion in the
// background and is kept, and later visits get the last good Home at once
// while a fresh one is built behind it.
//
// Each good Home is also saved under <data>/home-cache, so a restart (the
// server updates itself) doesn't leave the next visit staring at a spinner
// while a cold build runs: it gets the saved Home at once and a fresh one is
// built behind it.

const (
	homeFresh      = time.Minute     // served as is
	homeBuildLimit = 5 * time.Minute // a build is abandoned after this
	// After a change (something watched, hidden, watchlisted) a visit waits
	// this long for the updated Home before settling for the previous one.
	homeDirtyWait = 4 * time.Second
)

type homeEntry struct {
	body  []byte
	at    time.Time
	dirty bool
	// gen counts changes; a build only clears dirty if nothing changed
	// while it ran, so a change that lands mid-build (the player's last
	// progress report racing the Home refetch) isn't lost.
	gen      int
	building chan struct{} // closed when the current build finishes
}

type homeCache struct {
	mu sync.Mutex
	m  map[string]*homeEntry
}

func homeKey(r *http.Request) string {
	u := currentUser(r)
	key := idList([]int64{u.ID})
	if u.MaxAge != nil {
		key += "/" + idList([]int64{int64(*u.MaxAge)})
	}
	return key
}

func (s *Server) handleHome(w http.ResponseWriter, r *http.Request) {
	key := homeKey(r)
	c := &s.homes
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]*homeEntry{}
	}
	e := c.m[key]
	if e == nil {
		e = &homeEntry{}
		e.body, e.at = s.loadSavedHome(key)
		c.m[key] = e
	}
	stale := e.body == nil || e.dirty || time.Since(e.at) > homeFresh || e.at.YearDay() != time.Now().YearDay()
	if stale && e.building == nil {
		e.building = make(chan struct{})
		go s.buildHomeFor(key, e, r, e.gen)
	}
	body, building, dirty := e.body, e.building, e.dirty
	c.mu.Unlock()

	if body != nil && !stale {
		writeHomeBody(w, body)
		return
	}
	if body != nil && !dirty {
		writeHomeBody(w, body) // the fresh one is on its way for next time
		return
	}
	var wait <-chan time.Time // nil: wait as long as the client does
	if body != nil {
		wait = time.After(homeDirtyWait)
	}
	for {
		select {
		case <-building:
			c.mu.Lock()
			body = e.body
			// The build began before the latest change (it was already
			// running when Remove from Up Next or the player's last report
			// landed): its Home is out of date too, so build again and wait
			// for that one, still within the same time limit.
			if wait != nil && e.dirty && e.body != nil {
				if e.building == nil {
					e.building = make(chan struct{})
					go s.buildHomeFor(key, e, r, e.gen)
				}
				building = e.building
				c.mu.Unlock()
				continue
			}
			c.mu.Unlock()
			if body == nil {
				writeErr(w, http.StatusInternalServerError, "home could not be loaded, see the server log")
				return
			}
		case <-wait:
		case <-r.Context().Done():
			return
		}
		break
	}
	writeHomeBody(w, body)
}

// buildHomeFor builds Home on a context of its own, so it finishes (and is
// kept) even when the client that asked has stopped waiting.
func (s *Server) buildHomeFor(key string, e *homeEntry, r *http.Request, gen int) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), homeBuildLimit)
	defer cancel()
	start := time.Now()
	rec := httptest.NewRecorder()
	s.buildHome(rec, r.Clone(ctx))
	c := &s.homes
	c.mu.Lock()
	defer c.mu.Unlock()
	if rec.Code == http.StatusOK {
		e.body, e.at = rec.Body.Bytes(), start
		e.dirty = e.gen != gen
		go s.saveHome(key, e.body)
	} else {
		log.Printf("home: build failed after %s: %d %s", time.Since(start).Round(time.Millisecond), rec.Code, bytes.TrimSpace(rec.Body.Bytes()))
	}
	close(e.building)
	e.building = nil
}

func (s *Server) homeFile(key string) string {
	if s.cfg == nil || s.cfg.DataDir == "" {
		return ""
	}
	name := strings.NewReplacer("/", "_", ",", "-").Replace(key)
	return filepath.Join(s.cfg.DataDir, "home-cache", name+".json")
}

// loadSavedHome returns the Home saved by an earlier run, if any. Its time is
// when it was saved, so it is served at once and rebuilt behind the scenes.
func (s *Server) loadSavedHome(key string) ([]byte, time.Time) {
	f := s.homeFile(key)
	if f == "" {
		return nil, time.Time{}
	}
	st, err := os.Stat(f)
	if err != nil {
		return nil, time.Time{}
	}
	b, err := os.ReadFile(f)
	if err != nil || len(b) == 0 {
		return nil, time.Time{}
	}
	return b, st.ModTime()
}

func (s *Server) saveHome(key string, body []byte) {
	f := s.homeFile(key)
	if f == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(f), 0o750); err != nil {
		log.Printf("home: save: %v", err)
		return
	}
	tmp := f + ".tmp"
	if err := os.WriteFile(tmp, body, 0o640); err != nil {
		log.Printf("home: save: %v", err)
		return
	}
	if err := os.Rename(tmp, f); err != nil {
		log.Printf("home: save: %v", err)
	}
}

func writeHomeBody(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// forgetHome marks a user's Home and smart rows out of date after they
// watch, hide or watchlist something.
func (s *Server) forgetHome(uid int64) {
	s.smart.forget(uid)
	s.homeChanged(uid)
}

// homeChanged marks a user's Home out of date (Continue Watching moved) but
// keeps the smart rows, which only depend on what was finished.
func (s *Server) homeChanged(uid int64) {
	c := &s.homes
	c.mu.Lock()
	defer c.mu.Unlock()
	prefix := idList([]int64{uid})
	for k, e := range c.m {
		if k == prefix || len(k) > len(prefix) && k[:len(prefix)+1] == prefix+"/" {
			e.dirty = true
			e.gen++
		}
	}
}

// allHomesChanged marks every cached Home out of date, after a setting that
// changes what Home shows for everyone.
func (s *Server) allHomesChanged() {
	c := &s.homes
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range c.m {
		e.dirty = true
		e.gen++
	}
}
