package api

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Automatic updates. The server can't replace its own binary or restart
// itself (it runs unprivileged), so the installer adds a root systemd timer
// that runs deploy/update.sh every 15 minutes. The two talk through small
// files in the data directory:
//
//   update-status.json  written here every 15 s: whether automatic updates
//                       are on and how many streams are open, so the updater
//                       never restarts the server in the middle of a film.
//   update-result.json  written by the updater: when it last checked, what
//                       it did and the version it installed.
//   update-now          written here when an admin presses "Update now";
//                       a systemd path unit starts the updater at once.

const (
	autoUpdateSetting = "auto_update"
	updateStatusFile  = "update-status.json"
	updateResultFile  = "update-result.json"
	updateNowFile     = "update-now"
	updateStatusEvery = 15 * time.Second
)

// updateStatus is what the updater reads before installing anything.
type updateStatus struct {
	AutoUpdate bool   `json:"autoUpdate"`
	Streams    int    `json:"streams"`
	Version    string `json:"version"`
	At         int64  `json:"at"`
}

// updateResult is what the updater last did (see deploy/update.sh).
type updateResult struct {
	State     string `json:"state"` // installed, up-to-date, waiting, updated, failed, off
	Message   string `json:"message,omitempty"`
	CheckedAt int64  `json:"checkedAt,omitempty"`
	UpdatedAt int64  `json:"updatedAt,omitempty"`
	Version   string `json:"version,omitempty"`
}

// autoUpdateFromEnv reports whether PLEXODUS_AUTO_UPDATE turns updates off.
func (s *Server) autoUpdateFromEnv() bool {
	switch strings.ToLower(strings.TrimSpace(s.cfg.AutoUpdate)) {
	case "off", "0", "false", "no":
		return true
	}
	return false
}

// autoUpdateEnabled: on unless switched off in Settings or the environment.
func (s *Server) autoUpdateEnabled(ctx context.Context) bool {
	return !s.autoUpdateFromEnv() && s.db.Setting(ctx, autoUpdateSetting) != "off"
}

// openStreams counts streams that are playing or paused. A paused film
// still counts: restarting would drop the player's connection.
func (s *Server) openStreams() int {
	return len(s.nowPlaying.snapshot(time.Now()))
}

// writeUpdateStatus refreshes update-status.json.
func (s *Server) writeUpdateStatus(ctx context.Context) error {
	st := updateStatus{AutoUpdate: s.autoUpdateEnabled(ctx), Streams: s.openStreams(), Version: s.cfg.Version, At: time.Now().Unix()}
	b, _ := json.Marshal(st)
	return writeFileAtomic(filepath.Join(s.cfg.DataDir, updateStatusFile), b)
}

// RunUpdateStatus keeps update-status.json current until ctx ends.
func (s *Server) RunUpdateStatus(ctx context.Context) {
	if err := s.writeUpdateStatus(ctx); err != nil {
		log.Printf("auto-update: %v", err)
	}
	t := time.NewTicker(updateStatusEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.writeUpdateStatus(ctx)
		}
	}
}

func (s *Server) readUpdateResult() (updateResult, bool) {
	var r updateResult
	b, err := os.ReadFile(filepath.Join(s.cfg.DataDir, updateResultFile))
	if err != nil || json.Unmarshal(b, &r) != nil {
		return r, false
	}
	return r, true
}

// autoUpdateSettings is the "Updates" part of GET /api/settings.
func (s *Server) autoUpdateSettings(ctx context.Context) map[string]any {
	out := map[string]any{
		"enabled": s.autoUpdateEnabled(ctx),
		"fromEnv": s.autoUpdateFromEnv(),
		"version": s.cfg.Version,
		// The updater writes its first result when it's installed, so
		// without one this server was installed some other way (an old
		// install.sh, Docker, or by hand) and nothing updates it.
		"installed": false,
	}
	if r, ok := s.readUpdateResult(); ok {
		out["installed"] = true
		out["last"] = r
	}
	if _, err := os.Stat(filepath.Join(s.cfg.DataDir, updateNowFile)); err == nil {
		out["pending"] = true
	}
	return out
}

// setAutoUpdate saves the Settings switch and tells the updater straight away.
func (s *Server) setAutoUpdate(ctx context.Context, on bool) {
	v := "on"
	if !on {
		v = "off"
	}
	s.db.SetSetting(ctx, autoUpdateSetting, v)
	s.writeUpdateStatus(ctx)
}

// requestUpdateNow asks the updater to check and install now, even with
// streams open (an admin pressed the button, so they know).
func (s *Server) requestUpdateNow() error {
	return writeFileAtomic(filepath.Join(s.cfg.DataDir, updateNowFile), []byte("now\n"))
}

func writeFileAtomic(path string, b []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
