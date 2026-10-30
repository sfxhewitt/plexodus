// Package config loads runtime settings from flags and environment variables.
package config

import (
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Config struct {
	Addr         string
	DataDir      string
	TMDBKey      string
	FFprobe      string
	ProbeWorkers int
	ScanWorkers  int
	ScanInterval time.Duration
	ArtworkDir   string
	DBPath       string
	DevProxy     string
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func envDur(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

// Load parses flags (which override environment variables).
func Load() *Config {
	c := &Config{}
	flag.StringVar(&c.Addr, "addr", env("PLEXODUS_ADDR", ":8484"), "listen address")
	flag.StringVar(&c.DataDir, "data", env("PLEXODUS_DATA", "./data"), "data directory (database and artwork cache)")
	flag.StringVar(&c.TMDBKey, "tmdb-key", env("TMDB_API_KEY", ""), "TMDB API key (v3 key or v4 read token); can also be set in the UI")
	flag.StringVar(&c.FFprobe, "ffprobe", env("PLEXODUS_FFPROBE", "ffprobe"), "ffprobe binary")
	flag.IntVar(&c.ProbeWorkers, "probe-workers", envInt("PLEXODUS_PROBE_WORKERS", 2), "concurrent background ffprobe jobs (0 disables background probing)")
	flag.IntVar(&c.ScanWorkers, "scan-workers", envInt("PLEXODUS_SCAN_WORKERS", 8), "concurrent directory listings during a scan")
	flag.DurationVar(&c.ScanInterval, "scan-interval", envDur("PLEXODUS_SCAN_INTERVAL", 30*time.Minute), "periodic scan interval (0 disables)")
	flag.StringVar(&c.DevProxy, "dev-proxy", env("PLEXODUS_DEV_PROXY", ""), "proxy non-API requests to a Vite dev server (development only)")
	flag.Parse()

	c.DBPath = filepath.Join(c.DataDir, "plexodus.db")
	c.ArtworkDir = filepath.Join(c.DataDir, "artwork")
	return c
}
