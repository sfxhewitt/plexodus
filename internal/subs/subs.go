// Package subs finds a video's subtitle tracks and turns them into ASS for
// the browser player, which renders them with libass (JASSUB). Embedded
// tracks and the fonts an anime release carries as Matroska attachments are
// pulled out with ffmpeg; sidecar files next to the video are read as they
// are.
//
// Nothing here runs during a scan. Work starts only when someone opens the
// subtitle menu or the player picks a track to show, and the results are
// cached under the data directory so a file is read for its subtitles at
// most once.
package subs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// Track is one subtitle track a player can offer.
type Track struct {
	// ID is "s<stream index>" for an embedded track, "x<n>" for a sidecar file.
	ID       string `json:"id"`
	Codec    string `json:"codec"`
	Language string `json:"language,omitempty"`
	Title    string `json:"title,omitempty"`
	// Format is "ass" (styled ASS/SSA), "text" (plain text such as SRT) or
	// "image" (PGS/VobSub bitmaps, which the browser player can't show).
	Format   string `json:"format"`
	Default  bool   `json:"default,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
	External bool   `json:"external,omitempty"`
	FileName string `json:"fileName,omitempty"`
}

// Font is a font file shipped with the video.
type Font struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	file string
}

// EmbeddedStream is the part of a probed stream this package needs.
type EmbeddedStream struct {
	Index    int
	Codec    string
	Language string
	Title    string
	Default  bool
	Forced   bool
}

var textCodecs = map[string]bool{"subrip": true, "srt": true, "mov_text": true, "webvtt": true, "text": true}
var assCodecs = map[string]bool{"ass": true, "ssa": true}

// FormatOf classifies a subtitle codec.
func FormatOf(codec string) string {
	switch {
	case assCodecs[codec]:
		return "ass"
	case textCodecs[codec]:
		return "text"
	default:
		return "image"
	}
}

var sidecarExt = map[string]string{".ass": "ass", ".ssa": "ass", ".srt": "text", ".vtt": "text"}

// Sidecars lists subtitle files that belong to the video at path: files in
// the same folder whose name starts with the video's name, and anything in a
// Subs/Subtitles folder beside it (per video, or shared when the folder
// holds a single video). One directory listing, no file reads.
func Sidecars(path string) []Track {
	dir := filepath.Dir(path)
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	var names []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	videos := 0
	var subDirs []string
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() {
			if l := strings.ToLower(n); l == "subs" || l == "subtitles" {
				subDirs = append(subDirs, n)
			}
			continue
		}
		ext := strings.ToLower(filepath.Ext(n))
		if _, ok := sidecarExt[ext]; ok && strings.HasPrefix(n, base) {
			names = append(names, n)
		}
		if isVideo(ext) {
			videos++
		}
	}
	for _, sd := range subDirs {
		for _, rel := range []string{sd, filepath.Join(sd, base)} {
			es, err := os.ReadDir(filepath.Join(dir, rel))
			if err != nil {
				continue
			}
			for _, e := range es {
				n := e.Name()
				if e.IsDir() {
					continue
				}
				if _, ok := sidecarExt[strings.ToLower(filepath.Ext(n))]; !ok {
					continue
				}
				// Loose files in Subs/ only count if they name this video,
				// or if the folder has just this one video.
				if rel == sd && !strings.HasPrefix(n, base) && videos > 1 {
					continue
				}
				names = append(names, filepath.Join(rel, n))
			}
		}
	}
	sort.Strings(names)
	var out []Track
	for i, n := range names {
		ext := strings.ToLower(filepath.Ext(n))
		lang, title, forced := sidecarInfo(strings.TrimSuffix(filepath.Base(n), filepath.Ext(n)), base)
		out = append(out, Track{
			ID: "x" + strconv.Itoa(i), Codec: strings.TrimPrefix(ext, "."), Format: sidecarExt[ext],
			Language: lang, Title: title, Forced: forced, External: true, FileName: n,
		})
	}
	return out
}

func isVideo(ext string) bool {
	switch ext {
	case ".mkv", ".mp4", ".m4v", ".avi", ".mov", ".webm", ".ts", ".m2ts", ".wmv":
		return true
	}
	return false
}

var langCode = regexp.MustCompile(`^[a-z]{2,3}(-[A-Za-z]{2,4})?$`)

// sidecarInfo reads "Video.en.forced.srt" style suffixes.
func sidecarInfo(name, base string) (lang, title string, forced bool) {
	rest := strings.TrimPrefix(name, base)
	rest = strings.Trim(rest, ". _-")
	if rest == "" {
		return "", "", false
	}
	var other []string
	for _, p := range strings.FieldsFunc(rest, func(r rune) bool { return r == '.' || r == '_' }) {
		l := strings.ToLower(p)
		switch {
		case l == "forced":
			forced = true
		case l == "sdh" || l == "cc" || l == "hi" || l == "default":
			other = append(other, p)
		case lang == "" && langCode.MatchString(l):
			lang = l
		default:
			other = append(other, p)
		}
	}
	return lang, strings.Join(other, " "), forced
}

// Manager extracts and caches subtitle tracks and fonts.
type Manager struct {
	ffmpeg, ffprobe, dir string
	limit                int64

	mu       sync.Mutex
	inflight map[string]*job
	sem      chan struct{}
}

type job struct {
	done chan struct{}
	err  error
}

func New(ffmpeg, ffprobe, dir string) *Manager {
	return &Manager{ffmpeg: ffmpeg, ffprobe: ffprobe, dir: dir, limit: 4 << 30,
		inflight: map[string]*job{}, sem: make(chan struct{}, 2)}
}

// Available reports whether ffmpeg is installed (needed for embedded tracks
// and for converting SRT).
func (m *Manager) Available() bool {
	_, err := exec.LookPath(m.ffmpeg)
	return err == nil
}

type manifest struct {
	Size      int64           `json:"size"`
	MTime     int64           `json:"mtime"`
	Tracks    map[string]bool `json:"tracks"`
	Fonts     []fontEntry     `json:"fonts"`
	FontsDone bool            `json:"fontsDone"`
}

type fontEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	File string `json:"file"`
}

func (m *Manager) fileDir(fileID int64) string {
	return filepath.Join(m.dir, strconv.FormatInt(fileID, 10))
}

// load returns the cache manifest for a file, clearing the cache if the
// video has changed since.
func (m *Manager) load(fileID int64, fi fs.FileInfo) *manifest {
	dir := m.fileDir(fileID)
	var mf manifest
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err == nil && json.Unmarshal(b, &mf) == nil && mf.Size == fi.Size() && mf.MTime == fi.ModTime().Unix() {
		if mf.Tracks == nil {
			mf.Tracks = map[string]bool{}
		}
		now := time.Now()
		os.Chtimes(dir, now, now)
		return &mf
	}
	if err == nil {
		// The video changed (or the manifest is damaged): start over.
		os.RemoveAll(dir)
	}
	return &manifest{Size: fi.Size(), MTime: fi.ModTime().Unix(), Tracks: map[string]bool{}}
}

func (m *Manager) save(fileID int64, mf *manifest) error {
	dir := m.fileDir(fileID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, _ := json.Marshal(mf)
	tmp := filepath.Join(dir, "manifest.json.tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, "manifest.json"))
}

// once runs fn for key, letting concurrent callers wait for the same result.
func (m *Manager) once(ctx context.Context, key string, fn func() error) error {
	m.mu.Lock()
	j, ok := m.inflight[key]
	if !ok {
		j = &job{done: make(chan struct{})}
		m.inflight[key] = j
		go func() {
			m.sem <- struct{}{}
			j.err = fn()
			<-m.sem
			m.mu.Lock()
			delete(m.inflight, key)
			m.mu.Unlock()
			close(j.done)
		}()
	}
	m.mu.Unlock()
	select {
	case <-j.done:
		return j.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Embedded returns an embedded track as ASS. The first request for a file
// extracts every text track in that file in one pass (ffmpeg has to read
// the whole file to collect subtitle packets), so switching tracks later
// is instant.
func (m *Manager) Embedded(ctx context.Context, fileID int64, path string, all []EmbeddedStream, index int) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	id := "s" + strconv.Itoa(index)
	out := filepath.Join(m.fileDir(fileID), id+".ass")
	m.mu.Lock()
	mf := m.load(fileID, fi)
	m.mu.Unlock()
	if mf.Tracks[id] {
		if b, err := os.ReadFile(out); err == nil {
			return b, nil
		}
	}
	err = m.once(ctx, fmt.Sprintf("%d:tracks", fileID), func() error {
		return m.extractAll(fileID, path, fi, all)
	})
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(out)
	if err != nil {
		return nil, errors.New("this subtitle track couldn't be read")
	}
	return b, nil
}

func (m *Manager) extractAll(fileID int64, path string, fi fs.FileInfo, all []EmbeddedStream) error {
	dir := m.fileDir(fileID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	args := []string{"-nostdin", "-hide_banner", "-v", "error", "-y", "-i", path}
	var ids []string
	for _, s := range all {
		f := FormatOf(s.Codec)
		if f == "image" {
			continue
		}
		id := "s" + strconv.Itoa(s.Index)
		codec := "ass"
		if f == "ass" {
			codec = "copy"
		}
		args = append(args, "-map", "0:"+strconv.Itoa(s.Index), "-c:s", codec, "-f", "ass", filepath.Join(dir, id+".ass.tmp"))
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return errors.New("this file has no text subtitle tracks")
	}
	start := time.Now()
	cmd := exec.CommandContext(ctx, m.ffmpeg, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg couldn't extract the subtitles: %s", lastLine(stderr.String(), err))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	mf := m.load(fileID, fi)
	for _, id := range ids {
		if os.Rename(filepath.Join(dir, id+".ass.tmp"), filepath.Join(dir, id+".ass")) == nil {
			mf.Tracks[id] = true
		}
	}
	log.Printf("subs: extracted %d track(s) from %s in %s", len(ids), filepath.Base(path), time.Since(start).Round(time.Millisecond))
	err := m.save(fileID, mf)
	go m.trim()
	return err
}

func lastLine(s string, err error) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if s == "" {
		return err.Error()
	}
	return s
}

// Sidecar returns a sidecar subtitle file as ASS.
func (m *Manager) Sidecar(ctx context.Context, path string, t Track) ([]byte, error) {
	p := filepath.Join(filepath.Dir(path), t.FileName)
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 32<<20))
	if err != nil {
		return nil, err
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	if t.Format == "ass" {
		if !utf8.Valid(b) {
			b = latin1(b)
		}
		return b, nil
	}
	if !m.Available() {
		return nil, errors.New("ffmpeg is not installed on the server, so SRT subtitles can't be converted")
	}
	format := "srt"
	if t.Codec == "vtt" {
		format = "webvtt"
	}
	args := []string{"-nostdin", "-hide_banner", "-v", "error"}
	if !utf8.Valid(b) {
		args = append(args, "-sub_charenc", "CP1252")
	}
	args = append(args, "-f", format, "-i", "pipe:0", "-c:s", "ass", "-f", "ass", "pipe:1")
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, m.ffmpeg, args...)
	cmd.Stdin = bytes.NewReader(b)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("couldn't convert %s: %s", filepath.Base(p), lastLine(stderr.String(), err))
	}
	return stdout.Bytes(), nil
}

func latin1(b []byte) []byte {
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = rune(c)
	}
	return []byte(string(r))
}

// Fonts returns the fonts attached to the video (Matroska attachments) and
// any in a Fonts folder beside it. Attachments sit at the start of the file,
// so this only reads the file's head.
func (m *Manager) Fonts(ctx context.Context, fileID int64, path string) ([]Font, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	mf := m.load(fileID, fi)
	m.mu.Unlock()
	if !mf.FontsDone {
		err := m.once(ctx, fmt.Sprintf("%d:fonts", fileID), func() error {
			return m.extractFonts(fileID, path, fi)
		})
		if err != nil {
			return nil, err
		}
		m.mu.Lock()
		mf = m.load(fileID, fi)
		m.mu.Unlock()
	}
	var out []Font
	for _, f := range mf.Fonts {
		out = append(out, Font{ID: f.ID, Name: f.Name, file: f.File})
	}
	return out, nil
}

// FontPath returns the cached file for a font ID, or "" if unknown.
func (m *Manager) FontPath(fileID int64, id string) string {
	b, err := os.ReadFile(filepath.Join(m.fileDir(fileID), "manifest.json"))
	if err != nil {
		return ""
	}
	var mf manifest
	if json.Unmarshal(b, &mf) != nil {
		return ""
	}
	for _, f := range mf.Fonts {
		if f.ID == id {
			return f.File
		}
	}
	return ""
}

var fontExt = map[string]bool{".ttf": true, ".otf": true, ".ttc": true, ".otc": true, ".woff": true, ".woff2": true}

func isFont(name, mime string) bool {
	if fontExt[strings.ToLower(filepath.Ext(name))] {
		return true
	}
	mime = strings.ToLower(mime)
	return strings.Contains(mime, "font") || strings.Contains(mime, "truetype") || strings.Contains(mime, "opentype")
}

func (m *Manager) extractFonts(fileID int64, path string, fi fs.FileInfo) error {
	dir := m.fileDir(fileID)
	fontDir := filepath.Join(dir, "fonts")
	if err := os.MkdirAll(fontDir, 0o755); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var fonts []fontEntry

	if strings.EqualFold(filepath.Ext(path), ".mkv") || strings.EqualFold(filepath.Ext(path), ".mka") {
		// List attachments, then dump each to a name we choose (an
		// attachment's own file name could contain a path).
		out, err := exec.CommandContext(ctx, m.ffprobe, "-v", "error", "-select_streams", "t",
			"-show_entries", "stream=index:stream_tags=filename,mimetype", "-of", "json", path).Output()
		if err == nil {
			var p struct {
				Streams []struct {
					Index int               `json:"index"`
					Tags  map[string]string `json:"tags"`
				} `json:"streams"`
			}
			json.Unmarshal(out, &p)
			args := []string{"-nostdin", "-hide_banner", "-v", "error", "-y"}
			for _, s := range p.Streams {
				name := s.Tags["filename"]
				if !isFont(name, s.Tags["mimetype"]) {
					continue
				}
				ext := strings.ToLower(filepath.Ext(name))
				if !fontExt[ext] {
					ext = ".ttf"
				}
				id := "a" + strconv.Itoa(s.Index)
				file := filepath.Join(fontDir, id+ext)
				args = append(args, "-dump_attachment:"+strconv.Itoa(s.Index), file)
				fonts = append(fonts, fontEntry{ID: id, Name: filepath.Base(name), File: file})
			}
			if len(fonts) > 0 {
				args = append(args, "-i", path)
				// ffmpeg dumps attachments as it opens the input, then
				// complains there's no output; that error is expected.
				exec.CommandContext(ctx, m.ffmpeg, args...).Run()
				kept := fonts[:0]
				for _, f := range fonts {
					if st, err := os.Stat(f.File); err == nil && st.Size() > 0 {
						kept = append(kept, f)
					}
				}
				fonts = kept
			}
		}
	}

	// Some releases ship fonts loose in a Fonts folder.
	for _, sub := range []string{"Fonts", "fonts", "Attachments"} {
		es, err := os.ReadDir(filepath.Join(filepath.Dir(path), sub))
		if err != nil {
			continue
		}
		for _, e := range es {
			if e.IsDir() || !fontExt[strings.ToLower(filepath.Ext(e.Name()))] {
				continue
			}
			info, err := e.Info()
			if err != nil || info.Size() > 64<<20 {
				continue
			}
			fonts = append(fonts, fontEntry{ID: "f" + strconv.Itoa(len(fonts)), Name: e.Name(),
				File: filepath.Join(filepath.Dir(path), sub, e.Name())})
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	mf := m.load(fileID, fi)
	mf.Fonts, mf.FontsDone = fonts, true
	return m.save(fileID, mf)
}

// trim keeps the cache under its size limit, dropping the files whose
// subtitles were used longest ago.
func (m *Manager) trim() {
	es, err := os.ReadDir(m.dir)
	if err != nil {
		return
	}
	type ent struct {
		path string
		size int64
		at   time.Time
	}
	var all []ent
	var total int64
	for _, e := range es {
		if !e.IsDir() {
			continue
		}
		p := filepath.Join(m.dir, e.Name())
		var size int64
		filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				if i, err := d.Info(); err == nil {
					size += i.Size()
				}
			}
			return nil
		})
		info, err := e.Info()
		if err != nil {
			continue
		}
		all = append(all, ent{p, size, info.ModTime()})
		total += size
	}
	if total <= m.limit {
		return
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
	for _, e := range all {
		if total <= m.limit*9/10 {
			break
		}
		m.mu.Lock()
		os.RemoveAll(e.path)
		m.mu.Unlock()
		total -= e.size
	}
}
