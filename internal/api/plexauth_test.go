package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/auth"
	"github.com/sfxhewitt/plexodus/internal/config"
	"github.com/sfxhewitt/plexodus/internal/plex"
)

// fakePlex is plex.tv plus a Plex Media Server, for Sign in with Plex.
func fakePlex(t *testing.T) (*httptest.Server, *string) {
	custom := "https://other.example:9000"
	mux := http.NewServeMux()
	users := map[string]map[string]any{
		"owner-token":    {"id": 1, "username": "danny"},
		"friend-token":   {"id": 2, "username": "sam"},
		"stranger-token": {"id": 3, "username": "eve"},
		"partial-token":  {"id": 4, "username": "kim"},
	}
	mux.HandleFunc("GET /api/v2/user", func(w http.ResponseWriter, r *http.Request) {
		u, ok := users[r.Header.Get("X-Plex-Token")]
		if !ok {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(u)
	})
	mux.HandleFunc("GET /api/v2/resources", func(w http.ResponseWriter, r *http.Request) {
		tok := r.Header.Get("X-Plex-Token")
		res := []map[string]any{{"name": "Someone else", "clientIdentifier": "other", "provides": "server", "owned": false}}
		switch tok {
		case "owner-token":
			res = append(res, map[string]any{"name": "Danny's Plex", "clientIdentifier": "machine-1", "provides": "server", "owned": true})
		case "friend-token", "partial-token":
			res = append(res, map[string]any{"name": "Danny's Plex", "clientIdentifier": "machine-1", "provides": "server", "owned": false})
		}
		json.NewEncoder(w).Encode(res)
	})
	mux.HandleFunc("GET /api/servers/machine-1/shared_servers", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "owner-token" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(`<MediaContainer>
			<SharedServer userID="2" username="sam" allLibraries="1"></SharedServer>
			<SharedServer userID="4" username="kim" allLibraries="0">
				<Section key="7" title="Anime" type="movie" shared="1"/>
				<Section key="8" title="Movies" type="movie" shared="0"/>
			</SharedServer>
		</MediaContainer>`))
	})
	// The Plex Media Server.
	mux.HandleFunc("GET /identity", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"machineIdentifier":"machine-1"}}`))
	})
	mux.HandleFunc("GET /library/sections", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"MediaContainer":{"Directory":[
			{"key":"7","title":"Anime","type":"movie","Location":[{"path":"/media/anime"}]},
			{"key":"8","title":"Movies","type":"movie","Location":[{"path":"/media/movies"}]}]}}`))
	})
	mux.HandleFunc("GET /:/prefs", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{"Setting": []map[string]any{
			{"id": "FriendlyName", "value": "x"}, {"id": "customConnections", "value": custom}}}})
	})
	mux.HandleFunc("PUT /:/prefs", func(w http.ResponseWriter, r *http.Request) {
		custom = r.URL.Query().Get("customConnections")
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	old := plex.TVBase
	plex.TVBase = srv.URL
	t.Cleanup(func() { plex.TVBase = old })
	return srv, &custom
}

func TestSignInWithPlex(t *testing.T) {
	s, admin := testServer(t)
	ctx := context.Background()
	fp, custom := fakePlex(t)
	s.cfg = &config.Config{}
	s.db.SetSetting(ctx, "plex_url", fp.URL)
	s.db.SetSetting(ctx, "plex_token", "owner-token")
	s.db.W.Exec(`UPDATE libraries SET paths = '["/media/movies"]' WHERE id = 1`)
	s.db.W.Exec(`UPDATE libraries SET paths = '["/media/anime/"]' WHERE id = 2`)
	plexIDCache.id = ""
	sharesCache.m = nil

	signIn := func(token string) (int, auth.User, string) {
		r := httptest.NewRequest("POST", "/api/auth/plex", strings.NewReader(`{"token":"`+token+`"}`))
		r.RemoteAddr = "203.0.113.9:1234"
		w := httptest.NewRecorder()
		s.handlePlexToken(w, r)
		var u auth.User
		json.Unmarshal(w.Body.Bytes(), &u)
		return w.Code, u, w.Body.String()
	}

	// The Plex owner is the admin.
	code, u, body := signIn("owner-token")
	if code != 200 || u.AccountID != admin.ID || !u.IsAdmin {
		t.Fatalf("owner: %d %s", code, body)
	}
	// A friend with everything shared gets a new account with all libraries.
	code, u, body = signIn("friend-token")
	if code != 200 || u.IsAdmin || u.Username != "sam" || !u.AllLibraries {
		t.Fatalf("friend: %d %s", code, body)
	}
	samID := u.ID
	// Signing in again reuses the account.
	if _, u, _ = signIn("friend-token"); u.ID != samID {
		t.Fatalf("second sign-in made a new account: %d vs %d", u.ID, samID)
	}
	// A friend with only Anime shared sees only Anime (matched by folder).
	code, u, body = signIn("partial-token")
	if code != 200 || u.AllLibraries || len(u.Libraries) != 1 || u.Libraries[0] != 2 {
		t.Fatalf("partial: %d %s", code, body)
	}
	kimID := u.ID

	// An admin overrides Kim's libraries in Plexodus: the hourly Plex check
	// and signing in again both leave the choice alone.
	setAccess := func(body string) int {
		r := httptest.NewRequest("PUT", "/api/users/x/access", strings.NewReader(body))
		r.SetPathValue("id", strconv.FormatInt(kimID, 10))
		w := httptest.NewRecorder()
		s.handleUserAccess(w, r)
		return w.Code
	}
	kimLibs := func() []int64 {
		k, _ := s.auth.ByID(ctx, kimID)
		return k.Libraries
	}
	if code := setAccess(`{"allLibraries":false,"libraries":[1,2]}`); code != 200 {
		t.Fatalf("override: %d", code)
	}
	s.refreshPlexFriends(ctx)
	if _, u, _ = signIn("partial-token"); len(u.Libraries) != 2 || len(kimLibs()) != 2 {
		t.Fatalf("override lost: %v / %v", u.Libraries, kimLibs())
	}
	// Following Plex again puts the Plex share back straight away.
	if code := setAccess(`{"followPlex":true}`); code != 200 {
		t.Fatalf("follow plex: %d", code)
	}
	if l := kimLibs(); len(l) != 1 || l[0] != 2 {
		t.Fatalf("follow plex: %v", l)
	}
	// Following Plex is refused for an account that isn't linked to Plex.
	r := httptest.NewRequest("PUT", "/api/users/x/access", strings.NewReader(`{"followPlex":true}`))
	r.SetPathValue("id", strconv.FormatInt(admin.ID, 10))
	w := httptest.NewRecorder()
	s.db.W.Exec(`UPDATE users SET plex_id = NULL WHERE id = ?`, admin.ID)
	s.handleUserAccess(w, r)
	if w.Code != 400 {
		t.Fatalf("follow plex without plex: %d", w.Code)
	}
	s.db.W.Exec(`UPDATE users SET plex_id = 1 WHERE id = ?`, admin.ID)

	// Someone without access is turned away.
	if code, _, body = signIn("stranger-token"); code != 403 || !strings.Contains(body, "doesn't have access") {
		t.Fatalf("stranger: %d %s", code, body)
	}
	if code, _, _ = signIn("bogus"); code != 403 {
		t.Fatalf("bad token: %d", code)
	}

	// Removing the share in Plex signs the friend out.
	tok, _, _ := s.auth.SignIn(ctx, samID)
	sharesCache.m = map[int64]plex.Share{4: {UserID: 4, Sections: []string{"7"}}}
	s.refreshPlexFriends(ctx)
	if _, ok := s.auth.Lookup(ctx, tok); ok {
		t.Fatal("unshared friend still signed in")
	}

	// Turning it off.
	s.db.SetSetting(ctx, "plex_signin", "off")
	if code, _, _ = signIn("friend-token"); code != 403 {
		t.Fatalf("sign-in while off: %d", code)
	}
	s.db.SetSetting(ctx, "plex_signin", "on")

	// Publishing the address keeps Plex's other custom URLs.
	s.db.SetSetting(ctx, "public_url_seen", "http://media.example:8104")
	s.publishToPlex(ctx)
	if *custom != "https://other.example:9000,http://media.example:8104" {
		t.Fatalf("published: %q", *custom)
	}
	s.db.SetSetting(ctx, "public_url", "https://plexodus.example")
	s.publishToPlex(ctx)
	if *custom != "https://other.example:9000,https://plexodus.example" {
		t.Fatalf("moved: %q", *custom)
	}
	s.db.SetSetting(ctx, "plex_publish", "off")
	s.publishToPlex(ctx)
	if *custom != "https://other.example:9000" {
		t.Fatalf("unpublished: %q", *custom)
	}
}

func TestRequestOrigin(t *testing.T) {
	for host, want := range map[string]string{
		"ftl19.seedit4.me:8104": "http://ftl19.seedit4.me:8104",
		"localhost:8484":        "",
		"192.168.0.251:5173":    "",
		"127.0.0.1:8484":        "",
		"plexodus":              "",
		"193.163.71.152:8104":   "http://193.163.71.152:8104",
	} {
		r := httptest.NewRequest("GET", "/api/me", nil)
		r.Host = host
		if got := requestOrigin(r); got != want {
			t.Errorf("%s: %q, want %q", host, got, want)
		}
	}
}
