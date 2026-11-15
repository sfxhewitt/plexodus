package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/config"
	"github.com/sfxhewitt/plexodus/internal/plex"
)

// fakeDiscover is plex.tv (PINs, accounts), Plex Discover and a Plex Media
// Server, enough for Share to Plex.
func fakeDiscover(t *testing.T) (*httptest.Server, *sync.Map) {
	watchlist := &sync.Map{} // "token|key" -> true
	mux := http.NewServeMux()
	approved := false
	mux.HandleFunc("POST /api/v2/pins", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":77,"code":"ABCD","expiresIn":900}`))
	})
	mux.HandleFunc("GET /api/v2/pins/77", func(w http.ResponseWriter, r *http.Request) {
		if approved {
			w.Write([]byte(`{"id":77,"code":"ABCD","authToken":"dad-token"}`))
			return
		}
		approved = true // approved on the next poll
		w.Write([]byte(`{"id":77,"code":"ABCD","authToken":null}`))
	})
	mux.HandleFunc("GET /api/v2/user", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "dad-token" {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"id":555,"username":"bigdave"}`))
	})
	auth := func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("X-Plex-Token") != "dad-token" {
			w.WriteHeader(401)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /library/search", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		q := r.URL.Query()
		if q.Get("query") == "Akira" && q.Get("searchTypes") == "movies" {
			w.Write([]byte(`{"MediaContainer":{"SearchResults":[{"id":"external","SearchResult":[
				{"Metadata":{"guid":"plex://movie/akira2","type":"movie","title":"Akira","year":2030}},
				{"Metadata":{"guid":"plex://movie/akira1","type":"movie","title":"Akira","year":1988}}]}]}}`))
			return
		}
		w.Write([]byte(`{"MediaContainer":{"SearchResults":[]}}`))
	})
	mux.HandleFunc("GET /library/metadata/{key}/userState", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		at := 0
		if _, ok := watchlist.Load("dad-token|" + r.PathValue("key")); ok {
			at = 1700000000
		}
		json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{"UserState": []map[string]any{{"watchlistedAt": at}}}})
	})
	mux.HandleFunc("PUT /actions/addToWatchlist", func(w http.ResponseWriter, r *http.Request) {
		if !auth(w, r) {
			return
		}
		watchlist.Store("dad-token|"+r.URL.Query().Get("ratingKey"), true)
	})
	// The owner's Plex Media Server.
	mux.HandleFunc("GET /library/metadata/{key}", func(w http.ResponseWriter, r *http.Request) {
		switch r.PathValue("key") {
		case "100":
			w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"100","type":"movie","title":"Heat","guid":"plex://movie/heat95"}]}}`))
		case "300":
			w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"300","type":"episode","title":"Pilot","grandparentRatingKey":"301","guid":"plex://episode/x"}]}}`))
		case "301":
			w.Write([]byte(`{"MediaContainer":{"Metadata":[{"ratingKey":"301","type":"show","title":"Severance","guid":"plex://show/sev"}]}}`))
		default:
			w.WriteHeader(404)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	oldTV, oldD := plex.TVBase, plex.DiscoverBase
	plex.TVBase, plex.DiscoverBase = srv.URL, srv.URL
	t.Cleanup(func() { plex.TVBase, plex.DiscoverBase = oldTV, oldD })
	return srv, watchlist
}

func TestShareToPlexWatchlist(t *testing.T) {
	s, admin := testServer(t)
	ctx := context.Background()
	fp, watchlist := fakeDiscover(t)
	s.cfg = &config.Config{}
	s.db.SetSetting(ctx, "plex_url", fp.URL)
	s.db.SetSetting(ctx, "plex_token", "owner-token")
	for _, q := range []string{
		`INSERT INTO plex_links(item_id, rating_key) VALUES (10, '100')`,
		`INSERT INTO items(id, library_id, kind, guid, title, sort_title, year, added_at, updated_at) VALUES
			(30, 1, 'show', 's30', 'Severance', 'severance', 2022, 0, 0),
			(31, 1, 'season', 's31', 'Season 1', 's1', NULL, 0, 0),
			(32, 1, 'episode', 's32', 'Pilot', 'pilot', NULL, 0, 0)`,
		`UPDATE items SET parent_id = 30 WHERE id = 31`,
		`UPDATE items SET parent_id = 31 WHERE id = 32`,
		`UPDATE items SET year = 1988 WHERE id = 20`,
		`INSERT INTO plex_links(item_id, rating_key) VALUES (32, '300')`,
		`INSERT INTO media_files(id, item_id, library_id, path, size, mtime) VALUES (900, 32, 1, '/x/pilot.mkv', 1, 0)`,
	} {
		if _, err := s.db.W.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}

	// No one linked yet.
	var targets struct {
		Targets []shareTarget `json:"targets"`
	}
	call(t, s, admin, s.handleShareTargets, "GET", "/api/share/targets", "", nil, &targets)
	if len(targets.Targets) != 0 {
		t.Fatalf("targets before linking: %+v", targets)
	}
	if code := call(t, s, admin, s.handleShareWatchlist, "POST", "/", `{"itemId":10}`, nil, nil); code != 404 {
		t.Fatalf("share with nobody linked = %d", code)
	}

	// Link Dad: a plex.tv/link code, approved on the second poll.
	var pin struct {
		ID   int64  `json:"id"`
		Code string `json:"code"`
	}
	call(t, s, admin, s.handleShareLinkStart, "POST", "/", `{"name":"Dad"}`, nil, &pin)
	if pin.Code != "ABCD" {
		t.Fatalf("pin: %+v", pin)
	}
	var check struct {
		Done   bool        `json:"done"`
		Target shareTarget `json:"target"`
	}
	call(t, s, admin, s.handleShareLinkCheck, "GET", "/", "", map[string]string{"id": "77"}, &check)
	if check.Done {
		t.Fatal("done before approval")
	}
	call(t, s, admin, s.handleShareLinkCheck, "GET", "/", "", map[string]string{"id": "77"}, &check)
	if !check.Done || check.Target.Name != "Dad" || check.Target.PlexUser != "bigdave" || check.Target.Token != "" {
		t.Fatalf("link: %+v", check)
	}
	call(t, s, admin, s.handleShareTargets, "GET", "/api/share/targets", "", nil, &targets)
	if len(targets.Targets) != 1 || targets.Targets[0].Token != "" || targets.Targets[0].ID != "555" {
		t.Fatalf("targets: %+v", targets)
	}

	share := func(body string) (int, map[string]any) {
		var out map[string]any
		code := call(t, s, admin, s.handleShareWatchlist, "POST", "/", body, nil, &out)
		return code, out
	}
	// A film from Plex: exact match through the owner's Plex.
	if code, out := share(`{"target":"555","itemId":10}`); code != 200 || out["already"] != false || out["title"] != "Heat" {
		t.Fatalf("heat: %d %v", code, out)
	}
	if _, ok := watchlist.Load("dad-token|heat95"); !ok {
		t.Fatal("Heat not on Dad's watchlist")
	}
	// Again: already there.
	if code, out := share(`{"target":"555","itemId":10}`); code != 200 || out["already"] != true {
		t.Fatalf("heat again: %d %v", code, out)
	}
	// An episode playing: its show goes on the list (via the episode's Plex link).
	if code, out := share(`{"target":"555","fileId":900}`); code != 200 || out["title"] != "Severance" || out["kind"] != "show" {
		t.Fatalf("episode: %d %v", code, out)
	}
	if _, ok := watchlist.Load("dad-token|sev"); !ok {
		t.Fatal("Severance not on Dad's watchlist")
	}
	// Not from Plex: found by title and year.
	if code, out := share(`{"target":"555","itemId":20}`); code != 200 {
		t.Fatalf("akira: %d %v", code, out)
	}
	if _, ok := watchlist.Load("dad-token|akira1"); !ok {
		t.Fatal("wrong Akira")
	}

	// Friends don't see anyone to share with.
	friend := admin
	friend.IsAdmin = false
	call(t, s, friend, s.handleShareTargets, "GET", "/api/share/targets", "", nil, &targets)
	if len(targets.Targets) != 0 {
		t.Fatal("friend sees share targets")
	}

	// An expired sign-in says so.
	var raw string
	raw = s.db.Setting(ctx, shareTargetsKey)
	s.db.SetSetting(ctx, shareTargetsKey, strings.Replace(raw, "dad-token", "old-token", 1))
	if code, out := share(`{"target":"555","itemId":10}`); code != 409 {
		t.Fatalf("expired: %d %v", code, out)
	}

	// Removing.
	call(t, s, admin, s.handleShareTargetDelete, "DELETE", "/", "", map[string]string{"id": "555"}, &targets)
	if len(targets.Targets) != 0 {
		t.Fatal("not removed")
	}
}

func TestBestMatch(t *testing.T) {
	rs := []plex.DiscoverResult{
		{Key: "a", Type: "movie", Title: "The Thing", Year: 2011},
		{Key: "b", Type: "movie", Title: "The Thing", Year: 1982, IDs: []string{"tmdb://1091"}},
		{Key: "c", Type: "show", Title: "The Thing", Year: 1982},
	}
	if m, _ := plex.BestMatch(rs, "movie", "Thing", 0, 1091, ""); m.Key != "b" {
		t.Fatalf("by tmdb: %+v", m)
	}
	if m, _ := plex.BestMatch(rs, "movie", "The Thing", 2012, 0, ""); m.Key != "a" {
		t.Fatalf("year off by one: %+v", m)
	}
	if _, ok := plex.BestMatch(rs, "movie", "The Thing", 0, 0, ""); ok {
		t.Fatal("ambiguous title matched")
	}
	if m, _ := plex.BestMatch(rs, "show", "the thing", 1982, 0, ""); m.Key != "c" {
		t.Fatalf("show: %+v", m)
	}
}
