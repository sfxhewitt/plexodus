package api

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/auth"
)

// A stand-in for cloudflared that prints a quick-tunnel address the way the
// real one does (on stderr, inside a banner) and then stays up.
const fakeCloudflared = `#!/bin/sh
echo "INF Requesting new quick Tunnel on trycloudflare.com..." >&2
echo "INF |  https://brave-otter-tests.trycloudflare.com                                  |" >&2
exec sleep 30
`

func TestSecureLinkHandoff(t *testing.T) {
	s, u := testServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.baseCtx = ctx
	bin := filepath.Join(t.TempDir(), "cloudflared")
	os.WriteFile(bin, []byte(fakeCloudflared), 0o755)
	s.tunnel = newTunnel(t.TempDir())
	s.tunnel.bin = func(context.Context) (string, error) { return bin, nil }

	// Switch the account to a profile: the secure page should open on it too.
	kids := "Kids"
	p, err := s.auth.CreateProfile(ctx, u.ID, auth.ProfileInput{Name: &kids})
	if err != nil {
		t.Fatal(err)
	}
	viewer := u
	viewer.ID, viewer.IsProfile = p.ID, true

	var out struct{ URL string }
	call(t, s, viewer, s.handleSecureLink, "POST", "/api/companion/secure", "", nil, &out)
	const base = "https://brave-otter-tests.trycloudflare.com/api/handoff/"
	if !strings.HasPrefix(out.URL, base) {
		t.Fatalf("url = %q", out.URL)
	}
	code := strings.TrimPrefix(out.URL, base)

	// The second ask reuses the running tunnel.
	var again struct{ URL string }
	call(t, s, u, s.handleSecureLink, "POST", "/api/companion/secure", "", nil, &again)
	if !strings.HasPrefix(again.URL, base) || again.URL == out.URL {
		t.Fatalf("again = %q", again.URL)
	}

	hand := func(code string) (int, string, string) {
		r := httptest.NewRequest("GET", "/api/handoff/"+code, nil)
		r.Host = "brave-otter-tests.trycloudflare.com"
		r.Header.Set("X-Forwarded-Proto", "https")
		r.SetPathValue("code", code)
		w := httptest.NewRecorder()
		s.handleHandoff(w, r)
		tok := ""
		for _, c := range w.Result().Cookies() {
			if c.Name == auth.CookieName {
				tok = c.Value
				if !c.Secure {
					t.Error("cookie not Secure over https")
				}
			}
		}
		return w.Code, w.Header().Get("Location"), tok
	}
	code1, loc, tok := hand(code)
	if code1 != 303 || loc != "/scene" || tok == "" {
		t.Fatalf("handoff = %d %q %q", code1, loc, tok)
	}
	got, ok := s.auth.Lookup(ctx, tok)
	if !ok || got.ID != p.ID || got.AccountID != u.ID {
		t.Fatalf("session = %+v %v", got, ok)
	}
	// Codes work once.
	if _, _, tok2 := hand(code); tok2 != "" {
		t.Error("code reused")
	}
	if _, _, tok3 := hand("nope"); tok3 != "" {
		t.Error("bad code signed in")
	}
	// The temporary address is never published as the server's own.
	r := httptest.NewRequest("GET", "/", nil)
	r.Host = "brave-otter-tests.trycloudflare.com"
	if o := requestOrigin(r); o != "" {
		t.Errorf("requestOrigin = %q", o)
	}
}

func TestLocalTarget(t *testing.T) {
	for in, want := range map[string]string{":8484": "http://127.0.0.1:8484", "0.0.0.0:9000": "http://127.0.0.1:9000", "bad": "http://127.0.0.1:8484"} {
		if got := localTarget(in); got != want {
			t.Errorf("%q → %q", in, got)
		}
	}
}
