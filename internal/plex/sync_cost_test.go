package plex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sfxhewitt/plexodus/internal/db"
)

// The watch sync must stay small and must not look busy while the slower
// collection copy runs in the background.
func TestWatchSyncIsSmallAndExtrasDontHoldItUp(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if _, err := d.W.Exec(`INSERT INTO users(id, username, password_hash, is_admin, created_at) VALUES(1, 'admin', 'x', 1, 0)`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	// Markers are fresh, so only collections are due.
	d.SetSetting(context.Background(), "plex_markers_checked", "9999999999")
	d.SetSetting(context.Background(), ratingsDoneKey, "1")

	release := make(chan struct{})
	var metadataReqs, collectionReqs atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var md []any
		switch {
		case r.URL.Path == "/status/sessions/history/all":
		case r.URL.Path == "/library/onDeck":
			// 200 items on deck, none played since long before the cursor.
			for i := 0; i < 200; i++ {
				md = append(md, map[string]any{"ratingKey": "9" + string(rune('0'+i%10)), "type": "episode",
					"viewOffset": 1000, "lastViewedAt": now - 90*86400})
			}
		case strings.HasPrefix(r.URL.Path, "/library/metadata/"):
			metadataReqs.Add(1)
		case r.URL.Path == "/library/sections":
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{
				"Directory": []any{map[string]any{"key": "1", "type": "movie", "title": "Movies"}}}})
			return
		case strings.HasSuffix(r.URL.Path, "/collections"):
			collectionReqs.Add(1)
			<-release // a slow Plex
		default:
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{"Metadata": md}})
	}))
	defer srv.Close()

	s := NewSyncer(d, func(context.Context) (*Client, PathMap) { return NewClient(srv.URL, "tok"), nil }, nil)
	d.SetSetting(context.Background(), "plex_sync_cursor", "0")
	ctx := context.Background()

	// A first run (no cursor) is allowed to read the deck; a later run should not.
	if !s.once(ctx) {
		t.Fatalf("first sync failed: %+v", s.Status(ctx))
	}
	metadataReqs.Store(0)
	if !s.once(ctx) {
		t.Fatalf("second sync failed: %+v", s.Status(ctx))
	}
	st := s.Status(ctx)
	if st.Requests != 2 || metadataReqs.Load() != 0 {
		t.Errorf("steady-state sync made %d requests (%d item re-reads), want 2 and 0", st.Requests, metadataReqs.Load())
	}

	s.startExtras(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for collectionReqs.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	st = s.Status(ctx)
	if st.Running || !st.Extras.Running || st.Extras.Phase != "collections" {
		t.Errorf("while collections copy: running=%v extras=%+v", st.Running, st.Extras)
	}
	// The watch sync still runs, and finishes, while collections are copying.
	if !s.once(ctx) || s.Status(ctx).Running {
		t.Error("watch sync blocked by the collection copy")
	}
	close(release)
	for s.Status(ctx).Extras.Running && time.Now().Before(deadline.Add(5*time.Second)) {
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSyncInterval(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	s := NewSyncer(d, nil, nil)
	ctx := context.Background()
	if got := s.Interval(ctx); got != 5*time.Minute {
		t.Errorf("default %v", got)
	}
	if err := s.SetInterval(ctx, 15); err != nil || s.Interval(ctx) != 15*time.Minute {
		t.Errorf("set 15: %v %v", err, s.Interval(ctx))
	}
	if err := s.SetInterval(ctx, 2); err == nil {
		t.Error("2 minutes accepted")
	}
}
