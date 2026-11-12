package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestDescribeClient(t *testing.T) {
	cases := []struct{ ua, device, app string }{
		{"Dalvik/2.1.0 (Linux; U; Android 11; SHIELD Android TV Build/RQ1A.210105.003)", "Nvidia Shield", "Plexodus app"},
		{"Dalvik/2.1.0 (Linux; U; Android 9; AFTGAZL Build/PS7624)", "Fire TV Cube", "Plexodus app"},
		{"Mozilla/5.0 (Linux; Android 11; SHIELD Android TV Build/RQ1A; wv) AppleWebKit/537.36 Chrome/120 Safari/537.36 Plexodus/1.4 Android TV", "Nvidia Shield", "Plexodus app"},
		{"VLC/3.0.20 LibVLC/3.0.20", "Android device", "Plexodus app"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1", "iPhone", "Safari"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36", "Windows PC", "Chrome"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15", "Mac", "Safari"},
		{"", "Unknown device", "Unknown player"},
	}
	for _, c := range cases {
		d, a := describeClient(c.ua)
		if d != c.device || a != c.app {
			t.Errorf("%q: got %q/%q, want %q/%q", c.ua, d, a, c.device, c.app)
		}
	}
}

func activity(t *testing.T, s *Server) []NowPlaying {
	t.Helper()
	w := httptest.NewRecorder()
	s.handleActivity(w, httptest.NewRequest("GET", "/api/activity", nil))
	var out struct {
		Streams []NowPlaying `json:"streams"`
		Recent  []RecentPlay `json:"recent"`
	}
	if w.Code != 200 {
		t.Fatalf("activity: %d %s", w.Code, w.Body)
	}
	json.Unmarshal(w.Body.Bytes(), &out)
	return out.Streams
}

func TestNowPlaying(t *testing.T) {
	s, admin := testServer(t)
	s.nowPlaying = newNowPlaying()
	s.db.W.Exec(`INSERT INTO media_files(id, item_id, library_id, path, size, container, video_codec, audio_codec, width, height, duration, bitrate, mtime)
		VALUES (5, 10, 1, '/m/heat.mkv', 100, 'mkv', 'hevc', 'eac3', 3840, 2160, 600, 20000000, 0)`)

	shield := "Dalvik/2.1.0 (Linux; U; Android 11; SHIELD Android TV Build/RQ1A.210105.003)"
	mk := func(ua string) *http.Request {
		r := httptest.NewRequest("POST", "/", nil)
		r.Header.Set("User-Agent", ua)
		r.RemoteAddr = "192.168.0.246:5000"
		return r
	}

	// The app streams the file (libVLC) and reports progress (its own client).
	done := s.nowPlaying.direct(mk("VLC/3.0.20 LibVLC/3.0.20"), admin.ID, 10, 5)
	s.nowPlaying.progress(mk(shield), admin.ID, 10, 120, 600, "playing")
	got := activity(t, s)
	if len(got) != 1 {
		t.Fatalf("streams: %+v", got)
	}
	np := got[0]
	if np.Title != "Heat" || np.Username != "daniel" || np.Device != "Nvidia Shield" || np.Method != "direct" ||
		np.State != "playing" || np.VideoCodec != "hevc" || np.Height != 2160 || !np.Local || np.Position < 120 {
		t.Fatalf("stream: %+v", np)
	}
	done(4096)

	// Paused stays listed as paused.
	s.nowPlaying.progress(mk(shield), admin.ID, 10, 130, 600, "paused")
	if got = activity(t, s); len(got) != 1 || got[0].State != "paused" || got[0].Position != 130 || got[0].BytesServed != 4096 {
		t.Fatalf("paused: %+v", got)
	}

	// Stopped drops it, and a stray range request right after doesn't bring it back.
	s.nowPlaying.progress(mk(shield), admin.ID, 10, 130, 600, "stopped")
	s.nowPlaying.direct(mk("VLC/3.0.20 LibVLC/3.0.20"), admin.ID, 10, 5)(0)
	if got = activity(t, s); len(got) != 0 {
		t.Fatalf("after stop: %+v", got)
	}

	// A browser remux shows as remux; an old player that goes quiet drops off.
	s.nowPlaying.remuxStart(mk("Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) Safari/604.1"), admin.ID, 20, 7)
	s.nowPlaying.progress(mk(""), admin.ID, 20, 10, 100, "")
	if got = activity(t, s); len(got) != 1 || got[0].Method != "remux" || got[0].Device != "iPhone" || got[0].Title != "Akira" {
		t.Fatalf("remux: %+v", got)
	}
	s.nowPlaying.mu.Lock()
	for _, x := range s.nowPlaying.m {
		x.lastSeen = time.Now().Add(-npIdleFor - time.Second)
	}
	s.nowPlaying.mu.Unlock()
	if got = activity(t, s); len(got) != 0 {
		t.Fatalf("idle: %+v", got)
	}
}
