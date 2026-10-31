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
	srv := api.New(ctx, cfg, d, sc, agent, art, prober, web.FS())

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
	go scheduleScans(ctx, d, sc, cfg.ScanInterval)

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

// scheduleScans scans every library at startup and then on an interval,
// one library at a time so the mount isn't flooded. Scans are cheap when
// nothing changed: only directory listings are compared with the database,
// and with rclone's dir cache those listings come from memory.
func scheduleScans(ctx context.Context, d *db.DB, sc *scanner.Scanner, every time.Duration) {
	run := func() {
		rows, err := d.R.QueryContext(ctx, `SELECT id FROM libraries`)
		if err != nil {
			return
		}
		var ids []int64
		for rows.Next() {
			var id int64
			rows.Scan(&id)
			ids = append(ids, id)
		}
		rows.Close()
		for _, id := range ids {
			if ctx.Err() != nil {
				return
			}
			sc.Scan(ctx, id, scanner.Options{})
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
