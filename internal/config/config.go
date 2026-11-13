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
	FFmpeg       string
	StreamDir    string
	ProbeWorkers int
	ScanWorkers  int
	ScanInterval time.Duration
	FullScanHour int
	ArtworkDir   string
	DBPath       string
	DevProxy     string
	PlexURL      string
	PlexToken    string
	PlexPathMap  string
	AnthropicKey string
	AskModel     string
	// AutoUpdate is PLEXODUS_AUTO_UPDATE: "off" stops the updater
	// (deploy/update.sh) installing new releases, whatever Settings says.
	AutoUpdate string
	// Version is the build's version, set by main.
	Version string
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
	flag.StringVar(&c.FFmpeg, "ffmpeg", env("PLEXODUS_FFMPEG", "ffmpeg"), "ffmpeg binary, used only to remux files a browser can't play as they are")
	flag.IntVar(&c.ProbeWorkers, "probe-workers", envInt("PLEXODUS_PROBE_WORKERS", 0), "concurrent background ffprobe jobs; 0 (default) probes only when playback starts, which avoids reading every file on cloud mounts")
	flag.IntVar(&c.ScanWorkers, "scan-workers", envInt("PLEXODUS_SCAN_WORKERS", 16), "concurrent directory listings during a scan")
	flag.DurationVar(&c.ScanInterval, "scan-interval", envDur("PLEXODUS_SCAN_INTERVAL", 15*time.Minute), "periodic scan interval (0 disables)")
	flag.IntVar(&c.FullScanHour, "full-scan-hour", envInt("PLEXODUS_FULL_SCAN_HOUR", 4), "local hour (0-23) for the nightly full scan of libraries that trust folder times; -1 disables")
	flag.StringVar(&c.DevProxy, "dev-proxy", env("PLEXODUS_DEV_PROXY", ""), "proxy non-API requests to a Vite dev server (development only)")
	flag.StringVar(&c.PlexURL, "plex-url", env("PLEX_URL", ""), "Plex server URL to import from, e.g. http://127.0.0.1:32400")
	flag.StringVar(&c.PlexToken, "plex-token", env("PLEX_TOKEN", ""), "Plex token for importing")
	flag.StringVar(&c.PlexPathMap, "plex-path-map", env("PLEX_PATH_MAP", ""), "rewrite Plex file paths: plexPrefix=localPrefix[,...]")
	flag.StringVar(&c.AnthropicKey, "anthropic-key", env("ANTHROPIC_API_KEY", ""), "Anthropic API key for plain-English search; can also be set in the UI")
	flag.StringVar(&c.AskModel, "ask-model", env("PLEXODUS_ASK_MODEL", ""), "Claude model for plain-English search (default claude-opus-5-5)")
	c.AutoUpdate = env("PLEXODUS_AUTO_UPDATE", "")
	flag.Parse()

	c.DBPath = filepath.Join(c.DataDir, "plexodus.db")
	c.ArtworkDir = filepath.Join(c.DataDir, "artwork")
	c.StreamDir = filepath.Join(c.DataDir, "stream")
	return c
}
