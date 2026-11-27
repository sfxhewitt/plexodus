// Package subfetch finds subtitles online for a video that came without any,
// and picks the ones most likely to be timed for that exact copy.
//
// The source is OpenSubtitles, through the two doors that need no account:
// its REST search (which also knows which subtitles were uploaded for a file
// with a given fingerprint) and, if that is down, the mirror Stremio runs.
// A copy is matched, best first, by:
//
//  1. its fingerprint (OpenSubtitles' "movie hash": the file size plus the
//     first and last 64 KB, so two short reads, never the whole file),
//  2. frame rate and running time (a PAL DVD's subtitles drift on a Blu-ray),
//  3. how alike the release names are (source, group, cut),
//  4. how well used and trusted the upload is.
//
// Whatever is downloaded is checked against the video's length before it is
// kept, and the adverts OpenSubtitles puts in free downloads are removed.
package subfetch

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// Query describes the video subtitles are wanted for.
type Query struct {
	IMDB    string // digits only, the film's or (for an episode) the show's
	Title   string // film or show title, used when there is no IMDB id
	Year    int
	Season  int // with Episode, for an episode
	Episode int
	IsShow  bool
	Hash    string // OpenSubtitles movie hash, "" if it couldn't be read
	Size    int64
	Lang    string // ISO 639-2, e.g. "eng"

	FileName string  // the video's file name, for release matching
	Duration float64 // seconds, 0 if unknown
	FPS      float64 // 0 if unknown
}

// Candidate is one subtitle file on offer.
type Candidate struct {
	ID        string  `json:"id"`
	Provider  string  `json:"provider"`
	Release   string  `json:"release"`
	FileName  string  `json:"fileName"`
	URL       string  `json:"-"`
	HashMatch bool    `json:"hashMatch"`
	FPS       float64 `json:"fps,omitempty"`
	LastSec   float64 `json:"lastSec,omitempty"`
	Downloads int     `json:"downloads,omitempty"`
	HI        bool    `json:"hi,omitempty"`
	Trusted   bool    `json:"trusted,omitempty"`
	Auto      bool    `json:"auto,omitempty"`
	Score     float64 `json:"score"`
}

// ErrQuota means the day's free downloads are used up.
var ErrQuota = errors.New("the day's free subtitle downloads are used up")

// Client talks to the subtitle sources.
type Client struct {
	HTTP    *http.Client
	Legacy  string // OpenSubtitles REST base
	Stremio string // Stremio's OpenSubtitles add-on base
	UA      string
}

func New() *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: 30 * time.Second},
		Legacy:  "https://rest.opensubtitles.org",
		Stremio: "https://opensubtitles-v3.strem.io",
		UA:      "Plexodus v1",
	}
}

// Hash computes OpenSubtitles' movie hash: the size plus the 64-bit sums of
// the first and last 64 KB.
func Hash(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	const chunk = 65536
	size := fi.Size()
	if size < chunk {
		return "", size, errors.New("file too small to fingerprint")
	}
	sum := uint64(size)
	buf := make([]byte, chunk)
	for _, off := range []int64{0, size - chunk} {
		if _, err := f.ReadAt(buf, off); err != nil {
			return "", size, err
		}
		for i := 0; i < chunk; i += 8 {
			sum += binary.LittleEndian.Uint64(buf[i:])
		}
	}
	return fmt.Sprintf("%016x", sum), size, nil
}

func (c *Client) get(ctx context.Context, u string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UA)
	req.Header.Set("Accept", "*/*")
	return c.HTTP.Do(req)
}

func (c *Client) getJSON(ctx context.Context, u string, v any) error {
	resp, err := c.get(ctx, u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 || resp.StatusCode == 407 {
		return ErrQuota
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s answered %d", hostOf(u), resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(v)
}

func hostOf(u string) string {
	if p, err := url.Parse(u); err == nil {
		return p.Host
	}
	return "subtitle source"
}

// Search returns every usable candidate, best first. An error is returned
// only when no source answered.
func (c *Client) Search(ctx context.Context, q Query) ([]Candidate, error) {
	cands, err := c.searchLegacy(ctx, q)
	if len(cands) == 0 && q.IMDB != "" {
		more, err2 := c.searchStremio(ctx, q)
		if err2 == nil {
			err = nil
		}
		cands = more
	}
	if len(cands) == 0 {
		return nil, err
	}
	Rank(cands, q)
	return cands, nil
}

// legacySub is one row of OpenSubtitles' REST search (every value a string).
type legacySub struct {
	MatchedBy           string
	IDSubtitleFile      string
	SubFileName         string
	SubLastTS           string
	SubFormat           string
	SubSumCD            string
	SubBad              string
	SubDownloadsCnt     string
	MovieReleaseName    string
	MovieFPS            string
	IDMovieImdb         string
	SeriesIMDBParent    string
	SeriesSeason        string
	SeriesEpisode       string
	MovieName           string
	MovieYear           string
	MovieKind           string
	SubHearingImpaired  string
	SubFromTrusted      string
	SubAutoTranslation  string
	SubForeignPartsOnly string
	SubLanguageID       string
	SubDownloadLink     string
}

func (c *Client) searchLegacy(ctx context.Context, q Query) ([]Candidate, error) {
	lang := "sublanguageid-" + url.PathEscape(q.Lang)
	// Parameters go in alphabetical order, as the API asks.
	var searches []string
	if q.Hash != "" {
		searches = append(searches, fmt.Sprintf("moviebytesize-%d/moviehash-%s/%s", q.Size, q.Hash, lang))
	}
	ep := q.IsShow && q.Episode > 0
	switch {
	case q.IMDB != "" && ep:
		searches = append(searches, fmt.Sprintf("episode-%d/imdbid-%s/season-%d/%s", q.Episode, q.IMDB, q.Season, lang))
	case q.IMDB != "":
		searches = append(searches, fmt.Sprintf("imdbid-%s/%s", q.IMDB, lang))
	case q.Title != "" && ep:
		searches = append(searches, fmt.Sprintf("episode-%d/query-%s/season-%d/%s", q.Episode, legacyEscape(q.Title), q.Season, lang))
	case q.Title != "":
		searches = append(searches, fmt.Sprintf("query-%s/%s", legacyEscape(q.Title), lang))
	}
	var out []Candidate
	seen := map[string]bool{}
	var firstErr error
	answered := false
	for _, s := range searches {
		var rows []legacySub
		if err := c.getJSON(ctx, c.Legacy+"/search/"+s, &rows); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		answered = true
		for _, r := range rows {
			if seen[r.IDSubtitleFile] || r.IDSubtitleFile == "" || r.SubDownloadLink == "" {
				continue
			}
			if !strings.EqualFold(r.SubFormat, "srt") || r.SubSumCD != "1" || r.SubBad == "1" || r.SubForeignPartsOnly == "1" {
				continue
			}
			if r.SubLanguageID != "" && r.SubLanguageID != q.Lang {
				continue
			}
			if !sameTitle(r, q) {
				// Fingerprints collide and get mis-filed; only trust a
				// match that is also for the right title.
				continue
			}
			seen[r.IDSubtitleFile] = true
			fps, _ := strconv.ParseFloat(r.MovieFPS, 64)
			n, _ := strconv.Atoi(r.SubDownloadsCnt)
			out = append(out, Candidate{
				ID: r.IDSubtitleFile, Provider: "opensubtitles", Release: strings.TrimSpace(r.MovieReleaseName), FileName: r.SubFileName,
				URL: r.SubDownloadLink, HashMatch: r.MatchedBy == "moviehash", FPS: fps, LastSec: clockSeconds(r.SubLastTS),
				Downloads: n, HI: r.SubHearingImpaired == "1", Trusted: r.SubFromTrusted == "1", Auto: r.SubAutoTranslation == "1",
			})
		}
	}
	if !answered {
		return nil, firstErr
	}
	return out, nil
}

func legacyEscape(s string) string {
	return strings.ReplaceAll(url.PathEscape(strings.ToLower(s)), "+", "%20")
}

// sameTitle checks a search row is for the title asked for.
func sameTitle(r legacySub, q Query) bool {
	trim := func(s string) string { return strings.TrimLeft(s, "0") }
	ep := q.IsShow && q.Episode > 0
	if ep {
		s, _ := strconv.Atoi(r.SeriesSeason)
		e, _ := strconv.Atoi(r.SeriesEpisode)
		if s != q.Season || e != q.Episode {
			return false
		}
	}
	if q.IMDB != "" {
		want := trim(q.IMDB)
		if ep {
			return trim(r.SeriesIMDBParent) == want
		}
		return trim(r.IDMovieImdb) == want
	}
	if ep {
		// `"Breaking Bad" Bit by a Dead Bee`
		name := r.MovieName
		if i := strings.Index(name, `"`); i >= 0 {
			if j := strings.Index(name[i+1:], `"`); j >= 0 {
				name = name[i+1 : i+1+j]
			}
		}
		return plain(name) == plain(q.Title)
	}
	if r.MovieKind != "" && r.MovieKind != "movie" {
		return false
	}
	if q.Year > 0 {
		y, _ := strconv.Atoi(r.MovieYear)
		if y < q.Year-1 || y > q.Year+1 {
			return false
		}
	}
	return plain(r.MovieName) == plain(q.Title)
}

var notWord = regexp.MustCompile(`[^a-z0-9]+`)

func plain(s string) string {
	s = notWord.ReplaceAllString(strings.ToLower(s), " ")
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "the ")
	return strings.TrimSuffix(s, " the")
}

func clockSeconds(s string) float64 {
	p := strings.Split(strings.TrimSpace(s), ":")
	if len(p) != 3 {
		return 0
	}
	h, e1 := strconv.Atoi(p[0])
	m, e2 := strconv.Atoi(p[1])
	sec, e3 := strconv.ParseFloat(strings.Replace(p[2], ",", ".", 1), 64)
	if e1 != nil || e2 != nil || e3 != nil {
		return 0
	}
	return float64(h*3600+m*60) + sec
}

func (c *Client) searchStremio(ctx context.Context, q Query) ([]Candidate, error) {
	id := "tt" + fmt.Sprintf("%07s", q.IMDB)
	kind := "movie"
	if q.IsShow && q.Episode > 0 {
		kind = "series"
		id += fmt.Sprintf(":%d:%d", q.Season, q.Episode)
	}
	u := c.Stremio + "/subtitles/" + kind + "/" + url.PathEscape(id)
	if q.Hash != "" {
		u += fmt.Sprintf("/videoHash=%s&videoSize=%d", q.Hash, q.Size)
	}
	var res struct {
		Subtitles []struct {
			ID               string `json:"id"`
			URL              string `json:"url"`
			Lang             string `json:"lang"`
			M                string `json:"m"`
			SubtitleFileName string `json:"subtitleFileName"`
			MovieReleaseName string `json:"movieReleaseName"`
			FPSMilli         int    `json:"fpsMilli"`
		} `json:"subtitles"`
	}
	if err := c.getJSON(ctx, u+".json", &res); err != nil {
		return nil, err
	}
	var out []Candidate
	for _, s := range res.Subtitles {
		if s.Lang != q.Lang || s.URL == "" {
			continue
		}
		if n := strings.ToLower(s.SubtitleFileName); strings.Contains(n, "cd1") || strings.Contains(n, "cd2") || !strings.HasSuffix(n, ".srt") {
			continue
		}
		out = append(out, Candidate{
			ID: "s" + s.ID, Provider: "stremio", Release: s.MovieReleaseName, FileName: s.SubtitleFileName, URL: s.URL,
			HashMatch: s.M == "h", FPS: float64(s.FPSMilli) / 1000,
		})
	}
	return out, nil
}

// ---- ranking ----

var sources = map[string]string{
	"bluray": "bluray", "blu": "bluray", "bdrip": "bluray", "brrip": "bluray", "bdremux": "bluray", "remux": "bluray", "uhd": "bluray", "bd": "bluray", "hddvd": "bluray",
	"web": "web", "webrip": "web", "webdl": "web", "amzn": "web", "nf": "web", "dsnp": "web", "hmax": "web", "atvp": "web", "hulu": "web",
	"dvd": "dvd", "dvdrip": "dvd", "dvdscr": "dvd", "xvid": "dvd", "divx": "dvd",
	"hdtv": "tv", "pdtv": "tv", "dsr": "tv",
	"hdrip": "rip", "cam": "cam", "ts": "cam", "hdcam": "cam",
}

var cuts = map[string]bool{"extended": true, "unrated": true, "theatrical": true, "remastered": true, "imax": true, "uncut": true, "redux": true, "dc": true, "directors": true, "final": true, "criterion": true}

// noise are tokens that say nothing about which copy this is.
var noise = map[string]bool{
	"x264": true, "x265": true, "h264": true, "h265": true, "hevc": true, "avc": true, "10bit": true, "8bit": true,
	"aac": true, "ac3": true, "dts": true, "dd5": true, "ddp5": true, "truehd": true, "atmos": true, "ma": true, "hd": true, "5": true, "1": true, "7": true, "2": true, "0": true,
	"1080p": true, "720p": true, "2160p": true, "480p": true, "576p": true, "4k": true, "hdr": true, "dv": true, "sdr": true, "hdr10": true,
	"mkv": true, "mp4": true, "avi": true, "srt": true, "eng": true, "en": true, "english": true, "multi": true, "dual": true, "subs": true, "sub": true, "the": true, "a": true, "of": true, "and": true,
}

func tokens(s string) []string {
	s = strings.ToLower(s)
	s = strings.NewReplacer("web-dl", "webdl", "blu-ray", "bluray", "director's", "directors", "h.264", "h264", "h.265", "h265").Replace(s)
	return strings.Fields(notWord.ReplaceAllString(s, " "))
}

type release struct {
	source string
	cuts   map[string]bool
	group  string
	words  map[string]bool
}

func parseRelease(name string) release {
	r := release{cuts: map[string]bool{}, words: map[string]bool{}}
	// The group is what follows the last dash: "...x264-ESiR".
	base := strings.TrimSuffix(name, ".srt")
	for _, ext := range []string{".mkv", ".mp4", ".avi", ".m4v", ".ts", ".m2ts"} {
		base = strings.TrimSuffix(base, ext)
	}
	if i := strings.LastIndex(base, "-"); i > 0 && i < len(base)-1 {
		g := strings.ToLower(strings.TrimSpace(base[i+1:]))
		if j := strings.IndexAny(g, " .[("); j > 0 {
			g = g[:j]
		}
		if len(g) >= 2 && len(g) <= 16 && !strings.ContainsAny(g, " .") && sources[g] == "" && !noise[g] {
			r.group = g
		}
	}
	for _, t := range tokens(name) {
		if s := sources[t]; s != "" {
			if r.source == "" || t != "ts" {
				if !(t == "ts" && r.source != "") {
					r.source = s
				}
			}
			continue
		}
		if cuts[t] {
			r.cuts[t] = true
			continue
		}
		if !noise[t] {
			r.words[t] = true
		}
	}
	return r
}

// Rank scores the candidates for this copy and sorts them best first.
func Rank(cands []Candidate, q Query) {
	file := parseRelease(q.FileName)
	for i := range cands {
		c := &cands[i]
		var s float64
		if c.HashMatch {
			s += 1000
		}
		if c.Auto {
			s -= 200
		}
		// Frame rate: 23.976 and 24 are close enough to be mislabelled, a
		// PAL 25 is a different speed and drifts by minutes.
		if q.FPS > 0 && c.FPS > 0 {
			switch d := math.Abs(q.FPS - c.FPS); {
			case d < 0.05:
				s += 150
			case d > 0.5 && d < 2:
				s -= 300
			}
		}
		// The last line of a subtitle falls in the closing minutes of the
		// film it was timed for, never after it.
		if q.Duration > 0 && c.LastSec > 0 {
			gap := q.Duration - c.LastSec
			switch {
			case gap < -45:
				s -= 500
			case gap <= 12*60:
				s += 100
			case gap <= 25*60:
				s += 100 * (25*60 - gap) / (13 * 60)
			default:
				s -= 150 // probably one half of a two-part upload, or another cut
			}
		}
		rel := parseRelease(c.Release + " " + c.FileName)
		if file.source != "" && rel.source != "" {
			if file.source == rel.source {
				s += 80
			} else {
				s -= 80
			}
		}
		if file.group != "" && (rel.group == file.group || rel.words[file.group]) {
			s += 120
		}
		for k := range file.cuts {
			if rel.cuts[k] {
				s += 60
			} else {
				s -= 40
			}
		}
		for k := range rel.cuts {
			if !file.cuts[k] {
				s -= 40
			}
		}
		s += math.Min(60, 14*math.Log10(float64(c.Downloads)+1))
		if c.Trusted {
			s += 20
		}
		if c.HI {
			s -= 10
		}
		c.Score = math.Round(s*10) / 10
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Score > cands[j].Score })
}

// ---- download ----

// Download fetches a candidate and returns it as clean UTF-8 SRT.
func (c *Client) Download(ctx context.Context, cand Candidate) ([]byte, error) {
	resp, err := c.get(ctx, cand.URL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 429 || resp.StatusCode == 407 {
		return nil, ErrQuota
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s answered %d", hostOf(cand.URL), resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if len(b) > 2 && b[0] == 0x1f && b[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		if b, err = io.ReadAll(io.LimitReader(zr, 8<<20)); err != nil {
			return nil, err
		}
	}
	if resp.Header.Get("Download-Quota") == "0" && len(b) < 2000 {
		return nil, ErrQuota
	}
	cues := ParseSRT(decodeText(b))
	if len(cues) == 0 {
		return nil, errors.New("the download wasn't a subtitle file")
	}
	return FormatSRT(cues), nil
}

func decodeText(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("\xef\xbb\xbf")):
		b = b[3:]
	case bytes.HasPrefix(b, []byte("\xff\xfe")), bytes.HasPrefix(b, []byte("\xfe\xff")):
		be := b[0] == 0xfe
		u := make([]uint16, 0, len(b)/2)
		for i := 2; i+1 < len(b); i += 2 {
			if be {
				u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
			} else {
				u = append(u, uint16(b[i+1])<<8|uint16(b[i]))
			}
		}
		return string(utf16.Decode(u))
	}
	if utf8.Valid(b) {
		return string(b)
	}
	// Old Western European uploads: Windows-1252.
	r := make([]rune, len(b))
	for i, c := range b {
		if c >= 0x80 && c <= 0x9f {
			r[i] = cp1252[c-0x80]
		} else {
			r[i] = rune(c)
		}
	}
	return string(r)
}

var cp1252 = [32]rune{0x20AC, 0x81, 0x201A, 0x0192, 0x201E, 0x2026, 0x2020, 0x2021, 0x02C6, 0x2030, 0x0160, 0x2039, 0x0152, 0x8d, 0x017D, 0x8f,
	0x90, 0x2018, 0x2019, 0x201C, 0x201D, 0x2022, 0x2013, 0x2014, 0x02DC, 0x2122, 0x0161, 0x203A, 0x0153, 0x9d, 0x017E, 0x0178}

// Cue is one subtitle line.
type Cue struct {
	Start, End float64
	Text       string
}

var timing = regexp.MustCompile(`(\d+):(\d\d):(\d\d)[,.](\d{1,3})\s*-->\s*(\d+):(\d\d):(\d\d)[,.](\d{1,3})`)

// adverts are the lines free downloads carry, and uploader plugs.
var adverts = regexp.MustCompile(`(?i)opensubtitles|osdb\.link|become (a )?vip member|advertise your product|subtitles? (downloaded )?from|downloaded from|yts\.|yify|addic7ed|subscene|podnapisi|www\.[a-z0-9-]+\.[a-z]{2,}|https?://|do you want subtitles for any video|api\.[a-z]+\.com`)

// ParseSRT reads SRT, dropping adverts and empty lines.
func ParseSRT(s string) []Cue {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(s, "\n")
	var cues []Cue
	for i := 0; i < len(lines); i++ {
		m := timing.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		at := func(o int) float64 {
			h, _ := strconv.Atoi(m[o])
			mi, _ := strconv.Atoi(m[o+1])
			sec, _ := strconv.Atoi(m[o+2])
			ms, _ := strconv.Atoi((m[o+3] + "00")[:3])
			return float64(h*3600+mi*60+sec) + float64(ms)/1000
		}
		c := Cue{Start: at(1), End: at(5)}
		var text []string
		for i+1 < len(lines) && strings.TrimSpace(lines[i+1]) != "" && !timing.MatchString(lines[i+1]) {
			i++
			text = append(text, strings.TrimSpace(lines[i]))
		}
		// A bare number before the next timing line is that cue's counter.
		if n := len(text); n > 0 && i+1 < len(lines) && timing.MatchString(lines[i+1]) {
			if _, err := strconv.Atoi(text[n-1]); err == nil {
				text = text[:n-1]
			}
		}
		c.Text = strings.Join(text, "\n")
		if c.Text == "" || c.End <= c.Start || adverts.MatchString(c.Text) {
			continue
		}
		cues = append(cues, c)
	}
	sort.SliceStable(cues, func(i, j int) bool { return cues[i].Start < cues[j].Start })
	return cues
}

func stamp(t float64) string {
	ms := int64(math.Round(t * 1000))
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

// FormatSRT writes cues back out as SRT.
func FormatSRT(cues []Cue) []byte {
	var b bytes.Buffer
	for i, c := range cues {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, stamp(c.Start), stamp(c.End), c.Text)
	}
	return b.Bytes()
}

// Fits reports whether subtitles could have been timed for a video of this
// length: enough lines, and none after it ends.
func Fits(srt []byte, duration float64) error {
	cues := ParseSRT(string(srt))
	if len(cues) < 5 {
		return errors.New("too few lines")
	}
	if duration <= 0 {
		return nil
	}
	last := cues[len(cues)-1].Start
	if last > duration+45 {
		return fmt.Errorf("runs %.0f s past the end of the video", last-duration)
	}
	if duration > 20*60 && last < duration*0.6 {
		return errors.New("stops well before the video does")
	}
	return nil
}
