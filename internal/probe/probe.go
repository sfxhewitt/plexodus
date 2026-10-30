// Package probe reads technical stream info (codecs, resolution, duration)
// with ffprobe. It runs as a slow background trickle because every probe
// reads part of the file, which on a cloud mount means network traffic.
package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sfxhewitt/plexodus/internal/db"
)

type Stream struct {
	Index    int    `json:"index"`
	Type     string `json:"type"`
	Codec    string `json:"codec"`
	Language string `json:"language,omitempty"`
	Title    string `json:"title,omitempty"`
	Channels int    `json:"channels,omitempty"`
	Default  bool   `json:"default,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
}

type Result struct {
	Container  string
	VideoCodec string
	AudioCodec string
	Width      int
	Height     int
	Duration   float64
	Bitrate    int64
	Streams    []Stream
}

type ffprobeOut struct {
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
		BitRate    string `json:"bit_rate"`
	} `json:"format"`
	Streams []struct {
		Index       int               `json:"index"`
		CodecType   string            `json:"codec_type"`
		CodecName   string            `json:"codec_name"`
		Width       int               `json:"width"`
		Height      int               `json:"height"`
		Channels    int               `json:"channels"`
		Tags        map[string]string `json:"tags"`
		Disposition map[string]int    `json:"disposition"`
	} `json:"streams"`
}

// Run probes a single file. The probe size is kept small so only the start
// of the file is fetched from the remote.
func Run(ctx context.Context, ffprobe, path string) (*Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, ffprobe, "-v", "error", "-probesize", "5M", "-analyzeduration", "5M",
		"-print_format", "json", "-show_format", "-show_streams", path)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("ffprobe: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	var p ffprobeOut
	if err := json.Unmarshal(out, &p); err != nil {
		return nil, err
	}
	r := &Result{Container: p.Format.FormatName}
	defaultAudio := false
	r.Duration, _ = strconv.ParseFloat(p.Format.Duration, 64)
	r.Bitrate, _ = strconv.ParseInt(p.Format.BitRate, 10, 64)
	for _, s := range p.Streams {
		switch s.CodecType {
		case "video":
			if s.Disposition["attached_pic"] == 1 {
				continue
			}
			if r.VideoCodec == "" {
				r.VideoCodec, r.Width, r.Height = s.CodecName, s.Width, s.Height
			}
		case "audio":
			if r.AudioCodec == "" || (s.Disposition["default"] == 1 && !defaultAudio) {
				r.AudioCodec = s.CodecName
				defaultAudio = s.Disposition["default"] == 1
			}
		case "subtitle":
		default:
			continue
		}
		r.Streams = append(r.Streams, Stream{
			Index: s.Index, Type: s.CodecType, Codec: s.CodecName,
			Language: s.Tags["language"], Title: s.Tags["title"], Channels: s.Channels,
			Default: s.Disposition["default"] == 1, Forced: s.Disposition["forced"] == 1,
		})
	}
	return r, nil
}

// Save stores a probe result (or error) for a media file.
func Save(ctx context.Context, d *db.DB, fileID int64, r *Result, perr error) error {
	if perr != nil {
		_, err := d.W.ExecContext(ctx, `UPDATE media_files SET probed_at = ?, probe_error = ? WHERE id = ?`,
			db.Now(), perr.Error(), fileID)
		return err
	}
	streams, _ := json.Marshal(r.Streams)
	_, err := d.W.ExecContext(ctx, `UPDATE media_files SET container = ?, video_codec = ?, audio_codec = ?,
		width = ?, height = ?, duration = ?, bitrate = ?, streams = ?, probed_at = ?, probe_error = NULL WHERE id = ?`,
		r.Container, r.VideoCodec, r.AudioCodec, r.Width, r.Height, r.Duration, r.Bitrate, string(streams), db.Now(), fileID)
	return err
}

type Worker struct {
	db       *db.DB
	ffprobe  string
	n        int
	kick     chan struct{}
	inflight sync.Map
}

func NewWorker(d *db.DB, ffprobe string, n int) *Worker {
	return &Worker{db: d, ffprobe: ffprobe, n: n, kick: make(chan struct{}, 1)}
}

func (w *Worker) Kick() {
	select {
	case w.kick <- struct{}{}:
	default:
	}
}

// ProbeNow probes one file immediately (used when playback starts on a file
// that the background worker has not reached yet).
func (w *Worker) ProbeNow(ctx context.Context, fileID int64, path string) error {
	if _, busy := w.inflight.LoadOrStore(fileID, true); busy {
		return nil
	}
	defer w.inflight.Delete(fileID)
	r, err := Run(ctx, w.ffprobe, path)
	return Save(ctx, w.db, fileID, r, err)
}

// Run processes unprobed files until ctx is cancelled.
func (w *Worker) Run(ctx context.Context) {
	if w.n <= 0 {
		return
	}
	for {
		processed := w.batch(ctx)
		if ctx.Err() != nil {
			return
		}
		if processed > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-w.kick:
		case <-time.After(10 * time.Minute):
		}
	}
}

func (w *Worker) batch(ctx context.Context) int {
	rows, err := w.db.R.QueryContext(ctx, `SELECT id, path FROM media_files WHERE probed_at IS NULL ORDER BY id DESC LIMIT 100`)
	if err != nil {
		log.Printf("probe: %v", err)
		return 0
	}
	type job struct {
		id   int64
		path string
	}
	var jobs []job
	for rows.Next() {
		var j job
		rows.Scan(&j.id, &j.path)
		jobs = append(jobs, j)
	}
	rows.Close()

	ch := make(chan job)
	var wg sync.WaitGroup
	var ok atomic.Int64
	for i := 0; i < w.n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range ch {
				if err := w.ProbeNow(ctx, j.id, j.path); err != nil {
					if ctx.Err() == nil {
						log.Printf("probe %s: %v", j.path, err)
					}
				} else {
					ok.Add(1)
				}
			}
		}()
	}
	for _, j := range jobs {
		select {
		case ch <- j:
		case <-ctx.Done():
		}
	}
	close(ch)
	wg.Wait()
	return int(ok.Load())
}
