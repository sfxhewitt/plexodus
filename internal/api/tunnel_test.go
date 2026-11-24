package api

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	checks := 0
	s.tunnel.check = func(_ context.Context, u string) bool {
		checks++
		return checks >= 3 // not in DNS yet for the first two tries
	}

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
	if checks != 3 {
		t.Errorf("checks = %d, want the link handed out only once it answered", checks)
	}

	// The second ask reuses the running tunnel.
	var again struct{ URL string }
	call(t, s, u, s.handleSecureLink, "POST", "/api/companion/secure", "", nil, &again)
	if !strings.HasPrefix(again.URL, base) || again.URL == out.URL || checks != 3 {
		t.Fatalf("again = %q (checks %d)", again.URL, checks)
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

// A tunnel whose address never answers is replaced by a new one.
func TestSecureLinkRestartsDeadTunnel(t *testing.T) {
	s, u := testServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	s.baseCtx = ctx
	dir := t.TempDir()
	bin := filepath.Join(dir, "cloudflared")
	// Each start prints a new address: one-, two-, ...trycloudflare.com.
	os.WriteFile(bin, []byte(`#!/bin/sh
n=$(cat `+dir+`/n 2>/dev/null || echo 0); n=$((n+1)); echo $n > `+dir+`/n
echo "INF |  https://tunnel-$n.trycloudflare.com |" >&2
exec sleep 30
`), 0o755)
	s.tunnel = newTunnel(dir)
	s.tunnel.bin = func(context.Context) (string, error) { return bin, nil }
	s.tunnel.check = func(_ context.Context, u string) bool { return strings.Contains(u, "tunnel-2.") }
	defer func(w time.Duration) { tunnelCheckWait = w }(tunnelCheckWait)
	tunnelCheckWait = 1500 * time.Millisecond

	var out struct{ URL string }
	call(t, s, u, s.handleSecureLink, "POST", "/api/companion/secure", "", nil, &out)
	if !strings.HasPrefix(out.URL, "https://tunnel-2.trycloudflare.com/api/handoff/") {
		t.Fatalf("url = %q", out.URL)
	}
}
