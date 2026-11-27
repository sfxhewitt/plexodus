package api

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/subs"
)

func fakeSubtitleSource(t *testing.T, downloads *int) *httptest.Server {
	var srv *httptest.Server
	srt := func(marker string, lines int) string {
		var b strings.Builder
		for i := 0; i < lines; i++ {
			fmt.Fprintf(&b, "%d\n00:%02d:00,000 --> 00:%02d:02,000\n%s line %d\n\n", i+1, i, i, marker, i)
		}
		b.WriteString("99\n00:59:00,000 --> 01:00:00,000\nSupport us and become VIP member\nto remove all ads from www.OpenSubtitles.org\n")
		return b.String()
	}
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		row := func(id, rel string, n int) map[string]string {
			return map[string]string{"IDSubtitleFile": id, "MatchedBy": "imdbid", "IDMovieImdb": "113277", "MovieReleaseName": rel, "SubLastTS": "00:49:00",
				"SubFormat": "srt", "SubSumCD": "1", "SubBad": "0", "SubDownloadsCnt": fmt.Sprint(n), "MovieFPS": "23.976", "MovieKind": "movie",
				"MovieName": "Heat", "MovieYear": "1995", "SubLanguageID": "eng", "SubDownloadLink": srv.URL + "/dl/" + id}
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/search/imdbid-0113277/"):
			json.NewEncoder(w).Encode([]map[string]string{row("1", "Heat.1995.BluRay-AAA", 5000), row("2", "Heat.1995.BluRay-BBB", 50)})
		case strings.HasPrefix(r.URL.Path, "/search/"):
			w.Write([]byte("[]"))
		case strings.HasPrefix(r.URL.Path, "/dl/"):
			*downloads++
			zw := gzip.NewWriter(w)
			zw.Write([]byte(srt("sub"+strings.TrimPrefix(r.URL.Path, "/dl/"), 50)))
			zw.Close()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

type subsResp struct {
	Tracks []subs.Track `json:"tracks"`
	Online onlineStatus `json:"online"`
}

// A film with no English subtitles gets them from online the first time its
// tracks are asked for; "Try another" swaps to the next best; a film that
// has its own is left alone.
func TestOnlineSubtitles(t *testing.T) {
	s, u := testServer(t)
	s.comp = newCompanion()
	s.subs = subs.New("", "", t.TempDir())
	s.online = newOnlineSubs(t.TempDir())
	downloads := 0
	src := fakeSubtitleSource(t, &downloads)
	s.online.client.Legacy, s.online.client.Stremio = src.URL, src.URL
	s.cfg.FFprobe = "ffprobe-not-installed"

	dir := t.TempDir()
	video := filepath.Join(dir, "Heat (1995) BluRay-AAA.mkv")
	os.WriteFile(video, make([]byte, 200_000), 0o644)
	other := filepath.Join(t.TempDir(), "Akira.mkv")
	os.WriteFile(other, []byte("x"), 0o644)
	for _, q := range []string{
		`UPDATE items SET imdb_id = 'tt0113277', year = 1995 WHERE id = 10`,
		`INSERT INTO media_files(id, item_id, library_id, path, size, mtime, duration, streams) VALUES
			(5, 10, 1, '` + video + `', 200000, 0, 3000, '[{"index":2,"type":"subtitle","codec":"hdmv_pgs_subtitle","language":"eng"},{"index":3,"type":"subtitle","codec":"subrip","language":"fre"}]'),
			(6, 20, 2, '` + other + `', 1, 0, 3000, '[{"index":2,"type":"subtitle","codec":"subrip","language":"eng"}]')`,
	} {
		if _, err := s.db.W.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	get := func(file string) subsResp {
		var res subsResp
		if code := call(t, s, u, s.handleSubtitleTracks, "GET", "/api/subs/"+file, "", map[string]string{"fileId": file}, &res); code != 200 {
			t.Fatalf("tracks: %d", code)
		}
		return res
	}

	// Has English text already: no search.
	if res := get("6"); res.Online.State != "" || len(res.Tracks) != 1 {
		t.Fatalf("own subtitles = %+v", res)
	}

	res := get("5")
	if res.Online.State != "searching" || len(res.Tracks) != 2 {
		t.Fatalf("first ask = %+v", res)
	}
	s.waitOnline(t.Context(), 5, 5*time.Second)
	res = get("5")
	if res.Online.State != "found" || len(res.Tracks) != 3 {
		t.Fatalf("after search = %+v", res)
	}
	tr := res.Tracks[2]
	if tr.ID != onlineTrackID || !tr.Online || !tr.External || tr.Language != "eng" || tr.Format != "text" || tr.FileName != "Heat.1995.BluRay-AAA" {
		t.Errorf("track = %+v", tr)
	}
	// Kept under the data folder, adverts gone, nothing written beside the film.
	body, err := os.ReadFile(s.online.file(5, "eng"))
	if err != nil || !strings.Contains(string(body), "sub1 line 49") || strings.Contains(string(body), "OpenSubtitles") {
		t.Fatalf("saved file: %v %q", err, body)
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Errorf("media folder touched: %v", es)
	}

	// The TV app takes it as SRT.
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/subs/5/tracks/o0?raw=1", nil)
	r.SetPathValue("fileId", "5")
	r.SetPathValue("track", "o0")
	s.handleSubtitleTrack(w, r.WithContext(auth.WithUser(r.Context(), u)))
	if w.Code != 200 || !strings.HasPrefix(w.Body.String(), "1\n00:00:00,000 --> 00:00:02,000\nsub1 line 0") {
		t.Fatalf("raw track: %d %q", w.Code, w.Body.String()[:min(80, w.Body.Len())])
	}

	// Scene notes read the downloaded lines.
	lines, _ := s.subtitleLines(t.Context(), 5, time.Second)
	if len(lines) != 50 || lines[1].text != "sub1 line 1" || lines[1].start != 60 {
		t.Errorf("companion lines = %d %+v", len(lines), lines[:min(2, len(lines))])
	}

	// Asking again doesn't search or download again.
	get("5")
	if downloads != 1 {
		t.Errorf("downloads = %d, want 1", downloads)
	}

	// Try another: the next best replaces it.
	res = subsResp{}
	if code := call(t, s, u, s.handleOnlineSubs, "POST", "/api/subs/5/online", "", map[string]string{"fileId": "5"}, &res); code != 200 {
		t.Fatalf("try another: %d", code)
	}
	if res.Online.State != "found" || res.Online.Release != "Heat.1995.BluRay-BBB" {
		t.Fatalf("try another = %+v", res.Online)
	}
	if body, _ := os.ReadFile(s.online.file(5, "eng")); !strings.Contains(string(body), "sub2 line 0") {
		t.Errorf("second file = %q", body[:60])
	}
	// And once more: nothing left, so it says so and starts over next time.
	res = subsResp{}
	call(t, s, u, s.handleOnlineSubs, "POST", "/api/subs/5/online", "", map[string]string{"fileId": "5"}, &res)
	if res.Online.State != "none" || res.Online.Note == "" || len(res.Tracks) != 2 {
		t.Fatalf("exhausted = %+v", res)
	}
	res = subsResp{}
	call(t, s, u, s.handleOnlineSubs, "POST", "/api/subs/5/online", "", map[string]string{"fileId": "5"}, &res)
	if res.Online.State != "found" || res.Online.Release != "Heat.1995.BluRay-AAA" {
		t.Fatalf("start over = %+v", res.Online)
	}

	// Switched off: no search for a new file.
	s.db.SetSetting(t.Context(), onlineSubsKey, "off")
	s.db.W.Exec(`DELETE FROM online_subs`)
	if res := get("5"); res.Online.State != "off" || len(res.Tracks) != 2 {
		t.Fatalf("off = %+v", res)
	}
}

func TestHasText(t *testing.T) {
	ts := []subs.Track{{Format: "image", Language: "eng"}, {Format: "text", Language: "eng", Forced: true}, {Format: "text", Language: "fre"}}
	if hasText(ts, "eng") {
		t.Error("PGS, forced and French don't count as English text")
	}
	for _, l := range []string{"en", "eng", "en-GB", "", "und", "English"} {
		if !hasText([]subs.Track{{Format: "ass", Language: l}}, "eng") {
			t.Errorf("%q should count", l)
		}
	}
}
