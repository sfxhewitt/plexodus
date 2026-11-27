package subfetch

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHash(t *testing.T) {
	// 3 × 64 KB of a known pattern: size + sum of first and last chunks.
	p := filepath.Join(t.TempDir(), "v.mkv")
	b := make([]byte, 3*65536)
	for i := 0; i < len(b); i += 8 {
		binary.LittleEndian.PutUint64(b[i:], uint64(i/8))
	}
	os.WriteFile(p, b, 0o644)
	var want uint64 = uint64(len(b))
	for i := 0; i < 8192; i++ {
		want += uint64(i) + uint64(i+2*8192)
	}
	h, size, err := Hash(p)
	if err != nil || size != int64(len(b)) || h != strings.ToLower(hex16(want)) {
		t.Fatalf("hash = %s size %d err %v, want %s", h, size, err, hex16(want))
	}
	os.WriteFile(p, []byte("tiny"), 0o644)
	if _, _, err := Hash(p); err == nil {
		t.Fatal("tiny file should not hash")
	}
}

func hex16(v uint64) string {
	const d = "0123456789abcdef"
	out := make([]byte, 16)
	for i := 15; i >= 0; i-- {
		out[i] = d[v&15]
		v >>= 4
	}
	return string(out)
}

const sampleSRT = "\xef\xbb\xbf1\r\n00:00:06,000 --> 00:00:12,074\r\nDo you want subtitles for any video?\r\n-=[ ai.OpenSubtitles.com ]=-\r\n\r\n" +
	"2\r\n00:03:21,330 --> 00:03:24,920\r\nAn article, which I believe appeared\r\nin a newspaper, asked why,\r\n\r\n" +
	"3\r\n01:55:25,770 --> 01:55:28,850\r\n- What will you do then?\r\n- I think I'll have a drink.\r\n\r\n" +
	"4\r\n01:55:29,305 --> 01:56:29,861\r\nSupport us and become VIP member \r\nto remove all ads from www.OpenSubtitles.org\r\n"

func TestParseSRTDropsAdverts(t *testing.T) {
	cues := ParseSRT(decodeText([]byte(sampleSRT)))
	if len(cues) != 2 {
		t.Fatalf("cues = %+v", cues)
	}
	if cues[0].Start != 201.33 || cues[1].Text != "- What will you do then?\n- I think I'll have a drink." {
		t.Errorf("cues = %+v", cues)
	}
	out := string(FormatSRT(cues))
	if !strings.HasPrefix(out, "1\n00:03:21,330 --> 00:03:24,920\nAn article") || strings.Contains(strings.ToLower(out), "opensubtitles") {
		t.Errorf("out = %q", out)
	}
}

func TestDecodeText(t *testing.T) {
	if got := decodeText([]byte("caf\xe9 \x93hi\x94")); got != "café “hi”" {
		t.Errorf("cp1252 = %q", got)
	}
	if got := decodeText([]byte("\xff\xfeh\x00i\x00")); got != "hi" {
		t.Errorf("utf16 = %q", got)
	}
}

func TestFits(t *testing.T) {
	var cues []Cue
	for i := 0; i < 50; i++ {
		cues = append(cues, Cue{Start: float64(i * 130), End: float64(i*130 + 2), Text: "line"})
	}
	srt := FormatSRT(cues) // last line at 6370 s
	if err := Fits(srt, 7000); err != nil {
		t.Errorf("should fit: %v", err)
	}
	if err := Fits(srt, 6000); err == nil {
		t.Error("runs past the end: should not fit")
	}
	if err := Fits(srt, 12000); err == nil {
		t.Error("stops halfway: should not fit")
	}
	if err := Fits(FormatSRT(cues[:3]), 0); err == nil {
		t.Error("three lines: should not fit")
	}
}

func TestRank(t *testing.T) {
	q := Query{FileName: "The.Untouchables.1987.1080p.BluRay.x264.DTS-FGT.mkv", Duration: 7164, FPS: 23.976}
	cands := []Candidate{
		{ID: "pal", Release: "The.Untouchables.(1987).DVDRip.XViD.AC3-pITo", FPS: 25, LastSec: 6860, Downloads: 90000},
		{ID: "half", Release: "The Untouchables CD1", FPS: 23.976, LastSec: 3464, Downloads: 90000},
		{ID: "bluray", Release: "720p.BluRay.DTS.x264-ESiR", FPS: 23.976, LastSec: 6925, Downloads: 2000},
		{ID: "long", Release: "Untouchables BluRay", FPS: 23.976, LastSec: 7900, Downloads: 90000},
		{ID: "same-group", Release: "The.Untouchables.1987.720p.BluRay.x264-FGT", FPS: 23.976, LastSec: 6925, Downloads: 10},
		{ID: "hash", Release: "whatever dvdrip", FPS: 25, HashMatch: true},
	}
	Rank(cands, q)
	var order []string
	for _, c := range cands {
		order = append(order, c.ID)
	}
	if got := strings.Join(order[:3], " "); got != "hash same-group bluray" {
		t.Fatalf("order = %v (%+v)", order, cands)
	}
	for _, c := range cands[3:] {
		if c.Score > 100 {
			t.Errorf("%s should score low, got %v", c.ID, c.Score)
		}
	}
}

func TestParseRelease(t *testing.T) {
	r := parseRelease("Blade.Runner.1982.The.Final.Cut.2160p.UHD.BluRay.REMUX.HDR.HEVC.Atmos-EPSiLON.mkv")
	if r.source != "bluray" || r.group != "epsilon" || !r.cuts["final"] {
		t.Errorf("release = %+v", r)
	}
	if r := parseRelease("Show.S01E02.1080p.AMZN.WEB-DL.DDP5.1.H.264-NTb.mkv"); r.source != "web" || r.group != "ntb" {
		t.Errorf("release = %+v", r)
	}
	if r := parseRelease("The Untouchables (1987).mkv"); r.source != "" || r.group != "" {
		t.Errorf("release = %+v", r)
	}
}

// A fake OpenSubtitles: the fingerprint search returns the right film and a
// mis-filed one; the IMDB search returns more; downloads are gzipped.
func fakeSource(t *testing.T, hits *[]string) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*hits = append(*hits, r.URL.Path)
		row := func(id, by, imdb, rel, last string) map[string]string {
			return map[string]string{"IDSubtitleFile": id, "MatchedBy": by, "IDMovieImdb": imdb, "MovieReleaseName": rel, "SubLastTS": last,
				"SubFormat": "srt", "SubSumCD": "1", "SubBad": "0", "SubDownloadsCnt": "100", "MovieFPS": "23.976", "MovieKind": "movie",
				"MovieName": "The Untouchables", "MovieYear": "1987", "SubLanguageID": "eng", "SubDownloadLink": srv.URL + "/dl/" + id + ".gz"}
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/search/moviebytesize-"):
			json.NewEncoder(w).Encode([]map[string]string{
				row("7", "moviehash", "94226", "exact copy", "01:55:25"),
				row("8", "moviehash", "86998", "Breakin'", "01:55:25"), // collision: another film
			})
		case strings.HasPrefix(r.URL.Path, "/search/imdbid-0094226/"):
			two := row("10", "imdbid", "94226", "two discs", "00:57:00")
			two["SubSumCD"] = "2"
			json.NewEncoder(w).Encode([]map[string]string{row("7", "imdbid", "94226", "exact copy", "01:55:25"), row("9", "imdbid", "94226", "BluRay-ESiR", "01:55:25"), two})
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			w.Header().Set("Download-Quota", "150")
			zw := gzip.NewWriter(w)
			zw.Write([]byte(sampleSRT))
			zw.Close()
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSearchAndDownload(t *testing.T) {
	var hits []string
	srv := fakeSource(t, &hits)
	c := New()
	c.Legacy, c.Stremio = srv.URL, srv.URL
	q := Query{IMDB: "0094226", Hash: "8e245d9679d31e12", Size: 12345, Lang: "eng", FileName: "The Untouchables (1987).mkv", Duration: 7164, FPS: 23.976}
	cands, err := c.Search(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 || cands[0].ID != "7" || !cands[0].HashMatch || cands[1].ID != "9" {
		t.Fatalf("candidates = %+v", cands)
	}
	if hits[0] != "/search/moviebytesize-12345/moviehash-8e245d9679d31e12/sublanguageid-eng" || hits[1] != "/search/imdbid-0094226/sublanguageid-eng" {
		t.Errorf("requests = %v", hits)
	}
	b, err := c.Download(context.Background(), cands[0])
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(bytes.ToLower(b), []byte("opensubtitles")) || !bytes.Contains(b, []byte("I think I'll have a drink.")) {
		t.Errorf("download = %q", b)
	}
}

func TestSameTitle(t *testing.T) {
	ep := Query{IMDB: "0903747", IsShow: true, Season: 2, Episode: 3}
	if !sameTitle(legacySub{SeriesIMDBParent: "903747", SeriesSeason: "2", SeriesEpisode: "3"}, ep) {
		t.Error("episode by show id")
	}
	if sameTitle(legacySub{SeriesIMDBParent: "903747", SeriesSeason: "2", SeriesEpisode: "4"}, ep) {
		t.Error("wrong episode")
	}
	byName := Query{Title: "Breaking Bad", IsShow: true, Season: 2, Episode: 3}
	if !sameTitle(legacySub{MovieName: `"Breaking Bad" Bit by a Dead Bee`, SeriesSeason: "2", SeriesEpisode: "3"}, byName) {
		t.Error("episode by show name")
	}
	film := Query{Title: "The Untouchables", Year: 1987}
	if !sameTitle(legacySub{MovieName: "The Untouchables", MovieYear: "1987", MovieKind: "movie"}, film) ||
		sameTitle(legacySub{MovieName: `"The Untouchables" The Empty Chair`, MovieYear: "1959", MovieKind: "episode"}, film) ||
		sameTitle(legacySub{MovieName: "The Untouchables", MovieYear: "1993", MovieKind: "movie"}, film) {
		t.Error("film by name and year")
	}
}
