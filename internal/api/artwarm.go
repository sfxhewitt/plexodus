package api

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"
)

// WarmArtwork fills the artwork cache in the background so browsing never
// waits on TMDB or on Plex's image resizer. Posters first (every card), then
// backdrops for the newest titles (hero and landscape rows). It skips files
// already on disk, so repeated runs are cheap.
func (s *Server) WarmArtwork(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(15 * time.Second):
	}
	for {
		start := time.Now()
		n := s.warmOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if n > 0 {
			log.Printf("artwork: cached %d images in %s", n, time.Since(start).Round(time.Second))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(6 * time.Hour):
		}
	}
}

func (s *Server) warmOnce(ctx context.Context) int {
	type job struct{ path, size string }
	jobs := make(chan job, 256)
	var done int64
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if ctx.Err() != nil {
					continue
				}
				var err error
				if strings.HasPrefix(j.path, "plex:") {
					_, err = s.plexArt(ctx, j.size, strings.TrimPrefix(j.path, "plex:"))
				} else {
					_, err = s.art.Get(ctx, j.size, j.path)
				}
				if err == nil {
					mu.Lock()
					done++
					mu.Unlock()
				}
			}
		}()
	}
	push := func(query string, size string) {
		rows, err := s.db.R.QueryContext(ctx, query)
		if err != nil {
			return
		}
		defer rows.Close()
		for rows.Next() {
			var p string
			if rows.Scan(&p) == nil && p != "" {
				select {
				case jobs <- job{p, size}:
				case <-ctx.Done():
					return
				}
			}
		}
	}
	// Backdrops for the newest titles come first: they're the hero and the
	// landscape "continue watching" cards, the first thing on the screen.
	push(`SELECT backdrop FROM items WHERE kind IN ('movie', 'show') AND backdrop IS NOT NULL ORDER BY added_at DESC LIMIT 200`, "w1280")
	push(`SELECT backdrop FROM items WHERE kind IN ('movie', 'show') AND backdrop IS NOT NULL ORDER BY added_at DESC LIMIT 200`, "w780")
	push(`SELECT poster FROM items WHERE kind IN ('movie', 'show') AND poster IS NOT NULL ORDER BY added_at DESC`, "w342")
	push(`SELECT backdrop FROM items WHERE kind IN ('movie', 'show') AND backdrop IS NOT NULL ORDER BY added_at DESC LIMIT 5000`, "w780")
	close(jobs)
	wg.Wait()
	return int(done)
}
