package api

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/sfxhewitt/plexodus/internal/auth"
)

// Secure link: phones only let a web page keep the screen on over https,
// and most home servers are plain http. So on request the server opens a
// free Cloudflare quick tunnel (cloudflared, downloaded on first use) that
// gives it a temporary https://<random>.trycloudflare.com address, and hands
// the signed-in viewer over to it with a one-time code, so they don't have
// to sign in again. Nothing to set up; the address changes whenever the
// tunnel restarts, and the http page simply asks for the current one.

var tunnelURLRe = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

const (
	tunnelStartWait = 45 * time.Second
	// cloudflared prints the address before Cloudflare has published it in
	// DNS; a phone sent there too early caches "not found". So the server
	// opens the address itself (through public DNS) before handing it out.
	handoffTTL = 10 * time.Minute
)

// tunnelCheckWait: how long a new address gets to start answering.
var tunnelCheckWait = 45 * time.Second

type handoff struct {
	account, profile int64
	exp              time.Time
}

type tunnel struct {
	mu      sync.Mutex
	url     string
	running bool
	ready   chan struct{} // closed once url is known or the start failed
	err     error
	codes   map[string]handoff
	stop    context.CancelFunc // ends the running cloudflared
	checked time.Time          // when url last answered as this server

	// check reports whether url answers as this server; swapped out in tests.
	check func(ctx context.Context, url string) bool

	// bin finds or fetches cloudflared; swapped out in tests.
	bin func(ctx context.Context) (string, error)
}

func newTunnel(dataDir string) *tunnel {
	return &tunnel{codes: map[string]handoff{}, check: tunnelAnswers,
		bin: func(ctx context.Context) (string, error) { return cloudflaredPath(ctx, dataDir) }}
}

// isTunnelHost reports whether a request came in over the quick tunnel.
func isTunnelHost(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.HasSuffix(strings.ToLower(host), ".trycloudflare.com")
}

// URL starts the tunnel if needed and returns its https address once
// it's reachable from the internet.
func (t *tunnel) URL(ctx, base context.Context, target string) (string, error) {
	for attempt := 0; ; attempt++ {
		u, err := t.start(ctx, base, target)
		if err != nil {
			return "", err
		}
		t.mu.Lock()
		fresh := time.Since(t.checked) < 30*time.Second && t.url == u
		t.mu.Unlock()
		if fresh {
			return u, nil
		}
		wait := tunnelCheckWait
		if attempt == 0 && t.wasChecked(u) {
			wait = 8 * time.Second // a tunnel that worked before answers fast
		}
		if t.waitReachable(ctx, u, wait) {
			return u, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		log.Printf("secure link: %s isn't reachable, starting a new one", u)
		t.restart(u)
		if attempt >= 1 {
			return "", errors.New("the secure link isn't reachable from the internet yet, try again in a minute")
		}
	}
}

func (t *tunnel) wasChecked(u string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.url == u && !t.checked.IsZero()
}

// waitReachable polls the address until it answers as this server.
func (t *tunnel) waitReachable(ctx context.Context, u string, wait time.Duration) bool {
	deadline := time.Now().Add(wait)
	for {
		cctx, cancel := context.WithTimeout(ctx, 6*time.Second)
		ok := t.check(cctx, u)
		cancel()
		if ok {
			t.mu.Lock()
			if t.url == u {
				t.checked = time.Now()
			}
			t.mu.Unlock()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(time.Second):
		}
	}
}

// restart ends the tunnel at u (if it's still the current one).
func (t *tunnel) restart(u string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.url == u && t.stop != nil {
		t.stop()
		t.url, t.running, t.checked = "", false, time.Time{}
	}
}

func (t *tunnel) start(ctx, base context.Context, target string) (string, error) {
	t.mu.Lock()
	if t.running && t.url != "" {
		u := t.url
		t.mu.Unlock()
		return u, nil
	}
	if !t.running {
		t.running = true
		t.url, t.err, t.checked = "", nil, time.Time{}
		t.ready = make(chan struct{})
		pctx, stop := context.WithCancel(base)
		t.stop = stop
		go t.run(pctx, target, t.ready)
	}
	ready := t.ready
	t.mu.Unlock()

	select {
	case <-ready:
	case <-time.After(tunnelStartWait):
		return "", errors.New("the secure link is taking too long to start, try again in a minute")
	case <-ctx.Done():
		return "", ctx.Err()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.url == "" {
		if t.err != nil {
			return "", t.err
		}
		return "", errors.New("the secure link stopped, try again")
	}
	return t.url, nil
}

// publicDNS resolves through Cloudflare's own resolver, so the check sees
// the address as soon as it exists and the host's resolver never caches a
// "not found" for it.
var publicDNS = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, system string) (net.Conn, error) {
	d := net.Dialer{Timeout: 2 * time.Second}
	if c, err := d.DialContext(ctx, network, "1.1.1.1:53"); err == nil {
		return c, nil
	}
	return d.DialContext(ctx, network, system) // DNS port blocked: the host's own
}}

var tunnelClient = &http.Client{Transport: &http.Transport{
	Proxy:       http.ProxyFromEnvironment,
	DialContext: (&net.Dialer{Timeout: 5 * time.Second, Resolver: publicDNS}).DialContext,
}}

// tunnelAnswers reports whether u reaches this very server (same boot id).
func tunnelAnswers(ctx context.Context, u string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+"/api/status", nil)
	if err != nil {
		return false
	}
	res, err := tunnelClient.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
	return res.StatusCode == http.StatusOK && strings.Contains(string(b), bootID)
}

func (t *tunnel) run(ctx context.Context, target string, ready chan struct{}) {
	var once sync.Once
	signal := func() { once.Do(func() { close(ready) }) }
	fail := func(err error) {
		t.mu.Lock()
		if t.ready == ready { // not already replaced by a newer tunnel
			t.err, t.url, t.running, t.checked = err, "", false, time.Time{}
		}
		t.mu.Unlock()
		signal()
	}
	bin, err := t.bin(ctx)
	if err != nil {
		log.Printf("secure link: %v", err)
		fail(fmt.Errorf("couldn't get the secure link helper: %w", err))
		return
	}
	// http2 runs over TCP 7844, which more hosts allow out than QUIC.
	cmd := exec.CommandContext(ctx, bin, "tunnel", "--no-autoupdate", "--protocol", "http2", "--url", target)
	out, err := cmd.StderrPipe()
	if err == nil {
		cmd.Stdout = cmd.Stderr
		err = cmd.Start()
	}
	if err != nil {
		log.Printf("secure link: start: %v", err)
		fail(fmt.Errorf("couldn't start the secure link: %w", err))
		return
	}
	var tail []string
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		line := sc.Text()
		if u := tunnelURLRe.FindString(line); u != "" {
			t.mu.Lock()
			if t.url == "" && t.ready == ready {
				t.url = u
				log.Printf("secure link: %s", u)
			}
			t.mu.Unlock()
			signal()
		}
		if tail = append(tail, line); len(tail) > 5 {
			tail = tail[1:]
		}
	}
	err = cmd.Wait()
	log.Printf("secure link stopped: %v %s", err, strings.Join(tail, " | "))
	fail(errors.New("the secure link stopped, try again"))
}

// newCode makes a one-time sign-in code for the viewer.
func (t *tunnel) newCode(u auth.User) string {
	code := randomHex(24)
	h := handoff{account: u.AccountID, exp: time.Now().Add(handoffTTL)}
	if u.IsProfile {
		h.profile = u.ID
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for k, v := range t.codes {
		if time.Now().After(v.exp) {
			delete(t.codes, k)
		}
	}
	t.codes[code] = h
	return code
}

func (t *tunnel) take(code string) (handoff, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	h, ok := t.codes[code]
	delete(t.codes, code)
	return h, ok && time.Now().Before(h.exp)
}

// cloudflaredPath finds cloudflared, downloading it into the data
// directory the first time.
func cloudflaredPath(ctx context.Context, dataDir string) (string, error) {
	if p := os.Getenv("PLEXODUS_CLOUDFLARED"); p != "" {
		return p, nil
	}
	if p, err := exec.LookPath("cloudflared"); err == nil {
		return p, nil
	}
	dst := filepath.Join(dataDir, "bin", "cloudflared")
	if st, err := os.Stat(dst); err == nil && st.Size() > 1<<20 {
		return dst, nil
	}
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("install cloudflared on this %s server first", runtime.GOOS)
	}
	arch := runtime.GOARCH
	switch arch {
	case "amd64", "arm64", "arm", "386":
	default:
		return "", fmt.Errorf("no cloudflared build for %s", arch)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	dctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(dctx, http.MethodGet,
		"https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-"+arch, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: %s", res.Status)
	}
	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(f, io.LimitReader(res.Body, 200<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	log.Printf("secure link: downloaded cloudflared to %s", dst)
	return dst, os.Rename(tmp, dst)
}

// localTarget is where the tunnel sends requests: this server on loopback.
func localTarget(addr string) string {
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		port = "8484"
	}
	return "http://127.0.0.1:" + port
}

// POST /api/companion/secure → {url}: a one-time link to Scene notes on the
// secure address, already signed in as the caller.
func (s *Server) handleSecureLink(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), tunnelStartWait+2*tunnelCheckWait+10*time.Second)
	defer cancel()
	base, err := s.tunnel.URL(ctx, s.baseCtx, localTarget(s.cfg.Addr))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	code := s.tunnel.newCode(currentUser(r))
	writeJSON(w, map[string]string{"url": base + "/api/handoff/" + code})
}

// GET /api/handoff/{code} (public): swaps a one-time code for a session on
// this address and opens Scene notes.
func (s *Server) handleHandoff(w http.ResponseWriter, r *http.Request) {
	h, ok := s.tunnel.take(r.PathValue("code"))
	if !ok {
		http.Redirect(w, r, "/scene", http.StatusSeeOther)
		return
	}
	tok, err := s.auth.SignInAs(r.Context(), h.account, h.profile)
	if err != nil {
		http.Error(w, "couldn't sign in", http.StatusInternalServerError)
		return
	}
	s.setCookie(w, r, tok, 90*24*3600)
	http.Redirect(w, r, "/scene", http.StatusSeeOther)
}
