package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/auth"
)

// fakeDirectory stands in for the Supabase invite directory.
func fakeDirectory(t *testing.T) (*directory, map[string]string) {
	var mu sync.Mutex
	codes := map[string]string{}
	owner := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var a map[string]string
		json.NewDecoder(r.Body).Decode(&a)
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/rpc/plexodus_register":
			if o, ok := owner[a["p_code"]]; ok && o != a["p_secret"] {
				w.Write([]byte("false"))
				return
			}
			codes[a["p_code"]], owner[a["p_code"]] = a["p_url"], a["p_secret"]
			w.Write([]byte("true"))
		case "/rpc/plexodus_revoke":
			if owner[a["p_code"]] == a["p_secret"] {
				delete(codes, a["p_code"])
			}
			w.Write([]byte("true"))
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(srv.Close)
	return &directory{url: srv.URL, key: "k", client: srv.Client()}, codes
}

func TestInviteCodes(t *testing.T) {
	s, admin := testServer(t)
	ctx := context.Background()
	dir, codes := fakeDirectory(t)
	s.directory = dir
	s.db.SetSetting(ctx, "public_url", "https://media.example")
	limiter.hits = map[string][]time.Time{}

	call := func(method, path, pathKey, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "203.0.113.7:1000"
		r.Host = "192.168.0.5:8484"
		if pathKey != "" {
			r.SetPathValue("token", pathKey)
		}
		r = r.WithContext(auth.WithUser(r.Context(), admin))
		w := httptest.NewRecorder()
		switch {
		case method == "POST" && path == "/api/invites":
			s.handleCreateInvite(w, r)
		case method == "GET":
			s.handleGetInvite(w, r)
		case method == "DELETE":
			s.handleDeleteInvite(w, r)
		default:
			s.handleAcceptInvite(w, r)
		}
		return w
	}
	create := func(body string) map[string]string {
		w := call("POST", "/api/invites", "", body)
		var out map[string]string
		json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code != 200 || out["code"] == "" || out["codeError"] != "" {
			t.Fatalf("create: %d %s", w.Code, w.Body)
		}
		return out
	}

	// A single-use code is published, works typed in any case with or
	// without the dash, and is taken down once used.
	one := create(`{"allLibraries":false,"libraries":[2]}`)
	raw := strings.ReplaceAll(one["code"], "-", "")
	if codes[raw] != "https://media.example" {
		t.Fatalf("not published: %v", codes)
	}
	typed := strings.ToLower(one["code"])
	if w := call("GET", "/", typed, ""); w.Code != 200 {
		t.Fatalf("lookup by code: %d %s", w.Code, w.Body)
	}
	if w := call("POST", "/accept", typed, `{"username":"sam","password":"password1"}`); w.Code != 200 {
		t.Fatalf("accept: %d %s", w.Code, w.Body)
	}
	u := userNamed(t, s, "sam")
	if u.AllLibraries || len(u.Libraries) != 1 || u.Libraries[0] != 2 {
		t.Fatalf("sam's access: %+v", u)
	}
	if w := call("POST", "/accept", raw, `{"username":"eve","password":"password1"}`); w.Code != 404 {
		t.Fatalf("second use: %d", w.Code)
	}
	waitFor(t, func() bool { return codes[raw] == "" })

	// An "anyone with the code" invite (an app store reviewer's) works
	// repeatedly until revoked.
	many := create(`{"allLibraries":true,"maxUses":0,"days":365}`)
	for _, name := range []string{"reviewer1", "reviewer2"} {
		if w := call("POST", "/accept", many["code"], `{"username":"`+name+`","password":"password1"}`); w.Code != 200 {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	call("DELETE", "/", many["token"], "")
	if w := call("GET", "/", many["code"], ""); w.Code != 404 {
		t.Fatalf("revoked code still works: %d", w.Code)
	}
	waitFor(t, func() bool { return codes[strings.ReplaceAll(many["code"], "-", "")] == "" })

	// Without a public address the invite still works as a link.
	s.db.SetSetting(ctx, "public_url", "")
	w := call("POST", "/api/invites", "", `{"allLibraries":true}`)
	if !strings.Contains(w.Body.String(), "codeError") {
		t.Fatalf("no address: %s", w.Body)
	}
}

func userNamed(t *testing.T, s *Server, name string) auth.User {
	t.Helper()
	var id int64
	if err := s.db.R.QueryRow(`SELECT id FROM users WHERE username = ?`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	u, err := s.auth.ByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for range 100 {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func TestNormalizeCode(t *testing.T) {
	for in, want := range map[string]string{"k7qm-2xpa": "K7QM2XPA", " K7QM 2XPA ": "K7QM2XPA", "abc": "", "K7QM-2XP0": "", "0123456789abcdef": ""} {
		if got := normalizeCode(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}
