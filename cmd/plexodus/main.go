// Command plexodus runs the media server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/sfxhewitt/plexodus/internal/api"
	"github.com/sfxhewitt/plexodus/internal/config"
	"github.com/sfxhewitt/plexodus/internal/db"
	"github.com/sfxhewitt/plexodus/internal/metadata"
	"github.com/sfxhewitt/plexodus/internal/probe"
	"github.com/sfxhewitt/plexodus/internal/scanner"
	"github.com/sfxhewitt/plexodus/internal/stream"
	"github.com/sfxhewitt/plexodus/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	for _, a := range os.Args[1:] {
		if a == "-version" || a == "--version" {
			fmt.Println(version)
			return
		}
	}
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	d, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer d.Close()

	tmdbKey := d.Setting(ctx, "tmdb_key")
	if tmdbKey == "" {
		tmdbKey = cfg.TMDBKey
	}
	art := metadata.NewArtCache(cfg.ArtworkDir)
	agent := metadata.NewAgent(d, tmdbKey, art)
	prober := probe.NewWorker(d, cfg.FFprobe, cfg.ProbeWorkers)
	sc := scanner.New(d, cfg.ScanWorkers)
	streams := stream.New(cfg.FFmpeg, cfg.StreamDir)
	srv := api.New(ctx, cfg, d, sc, agent, art, prober, streams, web.FS())
	go streams.Reap(ctx)
	go srv.WarmArtwork(ctx)
	go srv.RunPlexSync(ctx)
	go srv.RunPlexFriends(ctx)
	go srv.RunDetector(ctx)

	go agent.Run(ctx)
	go prober.Run(ctx)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-sc.Changed:
				agent.Kick()
				prober.Kick()
			}
		}
	}()
	trustBigLibraries(ctx, d)
	go scheduleScans(ctx, d, sc, cfg.ScanInterval, cfg.FullScanHour)

	httpSrv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		httpSrv.Shutdown(shutdown)
	}()
	if !agent.Enabled() {
		log.Printf("no TMDB API key: titles come from filenames only (add one in Settings)")
	}
	log.Printf("Plexodus %s listening on %s (data: %s)", version, cfg.Addr, cfg.DataDir)
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// bigLibraryFiles is the size from which a library switches to trusting
// folder times on its regular scans (see trustBigLibraries).
const bigLibraryFiles = 50000

// trustBigLibraries turns on "Trust folder times" once for libraries of at
// least bigLibraryFiles files. Re-listing every folder of a 340k-file
// library every 15 minutes kept the database disk busy for minutes at a
// time and stalled Home; the nightly full scan still catches anything a
// folder time misses. It runs once, so switching it back off sticks.
func trustBigLibraries(ctx context.Context, d *db.DB) {
	const key = "trust_big_libraries_v1"
	if d.Setting(ctx, key) != "" {
		return
	}
	res, err := d.W.ExecContext(ctx, `UPDATE libraries SET trust_dir_mtime = 1 WHERE trust_dir_mtime = 0
		AND (SELECT COUNT(*) FROM media_files m WHERE m.library_id = libraries.id) >= ?`, bigLibraryFiles)
	if err != nil {
		log.Printf("scan: trust folder times for big libraries: %v", err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		log.Printf("scan: %d big libraries now trust folder times (a full scan still runs nightly)", n)
	}
	d.SetSetting(ctx, key, "1")
}

// scheduleScans scans every library at startup and then on an interval,
// one library at a time so the mount isn't flooded. Libraries that trust
// folder times only re-list folders whose time changed, so once a night
// (at fullHour, local time) they get a full scan that lists everything.
func scheduleScans(ctx context.Context, d *db.DB, sc *scanner.Scanner, every time.Duration, fullHour int) {
	// No full scan straight after a restart: the first one is tonight's.
	lastFull := time.Now().Format("2006-01-02")
	run := func() {
		full := false
		if now := time.Now(); fullHour >= 0 && now.Hour() >= fullHour && now.Format("2006-01-02") != lastFull {
			full, lastFull = true, now.Format("2006-01-02")
		}
		rows, err := d.R.QueryContext(ctx, `SELECT id, trust_dir_mtime FROM libraries`)
		if err != nil {
			return
		}
		type lib struct {
			id    int64
			trust bool
		}
		var libs []lib
		for rows.Next() {
			var l lib
			rows.Scan(&l.id, &l.trust)
			libs = append(libs, l)
		}
		rows.Close()
		if full {
			log.Printf("scan: nightly full scan")
		}
		for _, l := range libs {
			if ctx.Err() != nil {
				return
			}
			sc.Scan(ctx, l.id, scanner.Options{Full: full && l.trust})
		}
	}
	run()
	if every <= 0 {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
