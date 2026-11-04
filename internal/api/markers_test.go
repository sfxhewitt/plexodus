package api

import (
	"testing"

	"github.com/sfxhewitt/plexodus/internal/detect"
)

func TestMarkersPreferPlexAndGateRecaps(t *testing.T) {
	s, u := testServer(t)
	s.detector = detect.New(s.db, "ffmpeg-missing", "ffprobe-missing")
	s.db.SetSetting(s.baseCtx, detect.SettingKey, "off") // no background work in this test
	for _, q := range []string{
		`INSERT INTO markers(item_id, type, start, "end", source) VALUES
			(10, 'intro', 30, 70, 'plex'), (10, 'intro', 31, 71, 'audio'),
			(10, 'credits', 5000, 5100, 'chapter'), (10, 'credits', 5010, 5100, 'audio'),
			(10, 'recap', 0, 28, 'audio')`,
	} {
		if _, err := s.db.W.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	get := func(path string) map[string]Marker {
		var ms []Marker
		if code := call(t, s, u, s.handleMarkers, "GET", path, "", map[string]string{"id": "10"}, &ms); code != 200 {
			t.Fatalf("status %d", code)
		}
		out := map[string]Marker{}
		for _, m := range ms {
			if _, dup := out[m.Type]; dup {
				t.Fatalf("two %s markers: %v", m.Type, ms)
			}
			out[m.Type] = m
		}
		return out
	}
	old := get("/api/items/10/markers")
	if _, ok := old["recap"]; ok {
		t.Error("recap sent to a client that didn't ask for it")
	}
	if old["intro"].Start != 30 || old["credits"].Start != 5000 {
		t.Errorf("wrong sources won: %+v", old)
	}
	if m := get("/api/items/10/markers?recap=1"); m["recap"].End != 28 {
		t.Errorf("recap missing: %+v", m)
	}
}
