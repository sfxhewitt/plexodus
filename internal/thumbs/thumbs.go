// Package thumbs makes the preview frames shown above the seek bar while
// scrubbing: one small frame every few seconds, packed 10x10 into JPEG
// sprite sheets.
//
// A file is only done after it has been played, a few minutes in, so rclone
// already holds it in its cache and the drives aren't read twice. Only
// keyframes are decoded, one file at a time, at low priority.
package thumbs

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

const (
	Cols  = 10
	Rows  = 10
	Width = 240 // px per frame
)

// Meta describes a file's sheets.
type Meta struct {
	Interval float64 `json:"interval"` // seconds between frames
	Count    int     `json:"count"`    // frames in all
	Cols     int     `json:"cols"`
	Rows     int     `json:"rows"`
	Width    int     `json:"width"` // one frame
	Height   int     `json:"height"`
	Sheets   int     `json:"sheets"`
}

type job struct {
	id       int64
	path     string
	duration float64
	at       time.Time
}

type Manager struct {
	ffmpeg string
	dir    string
	delay  time.Duration

	mu      sync.Mutex
	queued  map[int64]bool
	jobs    chan job
	started sync.Once
}

func New(ffmpeg, dir string) *Manager {
	return &Manager{ffmpeg: ffmpeg, dir: dir, delay: 3 * time.Minute, queued: map[int64]bool{}, jobs: make(chan job, 256)}
}

func (m *Manager) fileDir(id int64) string { return filepath.Join(m.dir, strconv.FormatInt(id, 10)) }

// Meta returns a file's sheets, if they've been made.
func (m *Manager) Meta(id int64) (*Meta, bool) {
	b, err := os.ReadFile(filepath.Join(m.fileDir(id), "meta.json"))
	if err != nil {
		return nil, false
	}
	var meta Meta
	if json.Unmarshal(b, &meta) != nil {
		return nil, false
	}
	return &meta, true
}

// Sheet is the path of sheet n (from 0).
func (m *Manager) Sheet(id int64, n int) string {
	return filepath.Join(m.fileDir(id), fmt.Sprintf("s%03d.jpg", n+1))
}

// Queue asks for a file's sheets once it has been playing a while.
func (m *Manager) Queue(ctx context.Context, id int64, path string, duration float64) {
	if duration < 60 || m.ffmpeg == "" {
		return
	}
	if _, ok := m.Meta(id); ok {
		return
	}
	m.mu.Lock()
	if m.queued[id] {
		m.mu.Unlock()
		return
	}
	m.queued[id] = true
	m.mu.Unlock()
	m.started.Do(func() { go m.run(ctx) })
	select {
	case m.jobs <- job{id, path, duration, time.Now().Add(m.delay)}:
	default: // a backlog; it'll be asked for again next play
		m.mu.Lock()
		delete(m.queued, id)
		m.mu.Unlock()
	}
}

func (m *Manager) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case j := <-m.jobs:
			if d := time.Until(j.at); d > 0 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(d):
				}
			}
			start := time.Now()
			if err := m.make(ctx, j); err != nil {
				log.Printf("thumbs: file %d: %v", j.id, err)
			} else {
				log.Printf("thumbs: file %d done in %s", j.id, time.Since(start).Round(time.Second))
			}
			m.mu.Lock()
			delete(m.queued, j.id)
			m.mu.Unlock()
		}
	}
}

// Interval keeps a film to at most 600 frames (6 sheets), and never closer than 10 s.
func Interval(duration float64) float64 {
	return math.Max(10, math.Ceil(duration/600))
}

func (m *Manager) make(ctx context.Context, j job) error {
	interval := Interval(j.duration)
	tmp := m.fileDir(j.id) + ".tmp"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	ctx, cancel := context.WithTimeout(ctx, 40*time.Minute)
	defer cancel()
	args := []string{"-nostdin", "-v", "error", "-skip_frame", "nokey", "-i", j.path, "-an", "-sn", "-dn",
		"-vf", fmt.Sprintf("fps=1/%g,scale=%d:-2,tile=%dx%d", interval, Width, Cols, Rows),
		"-q:v", "6", "-vsync", "vfr", filepath.Join(tmp, "s%03d.jpg")}
	cmd := exec.CommandContext(ctx, "nice", append([]string{"-n", "15", m.ffmpeg}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, tail(out))
	}
	sheets, _ := filepath.Glob(filepath.Join(tmp, "s*.jpg"))
	if len(sheets) == 0 {
		return fmt.Errorf("no frames")
	}
	f, err := os.Open(sheets[0])
	if err != nil {
		return err
	}
	cfg, _, err := image.DecodeConfig(f)
	f.Close()
	if err != nil {
		return err
	}
	meta := Meta{Interval: interval, Count: int(math.Ceil(j.duration / interval)), Cols: Cols, Rows: Rows,
		Width: cfg.Width / Cols, Height: cfg.Height / Rows, Sheets: len(sheets)}
	b, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(tmp, "meta.json"), b, 0o644); err != nil {
		return err
	}
	os.RemoveAll(m.fileDir(j.id))
	return os.Rename(tmp, m.fileDir(j.id))
}

func tail(b []byte) string {
	if len(b) > 300 {
		b = b[len(b)-300:]
	}
	return string(b)
}
