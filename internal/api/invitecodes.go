package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// Invite codes: every invite gets a short code ("K7QM-2XPA") a friend can
// type into the app instead of a server address. The app looks the code up
// in a small public directory (a few Supabase functions, see
// deploy/supabase/invite_codes.sql) that maps it to this server's address;
// this server then checks the code itself like an invite link.

const (
	// The directory's publishable key is Supabase's client-side key, meant
	// to ship in apps; it can only call the plexodus_* functions.
	defaultDirectoryURL = "https://samxshajnvjyarvuqfsf.supabase.co/rest/v1"
	defaultDirectoryKey = "sb_publishable_wJCyACOz0jtPh-KjUd9f5w_r8JJOr4o"
)

// codeAlphabet leaves out 0/O and 1/I/L, which people mix up.
const codeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func newInviteCode() string {
	b := make([]byte, 8)
	rand.Read(b)
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(b)
}

// normalizeCode turns "k7qm-2xpa" into "K7QM2XPA"; "" when it isn't a code.
func normalizeCode(s string) string {
	s = strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(s)))
	if len(s) != 8 {
		return ""
	}
	for _, c := range s {
		if !(c >= 'A' && c <= 'Z' || c >= '2' && c <= '9') {
			return ""
		}
	}
	return s
}

// formatCode shows a code in two halves: "K7QM-2XPA".
func formatCode(c string) string {
	if len(c) != 8 {
		return c
	}
	return c[:4] + "-" + c[4:]
}

type directory struct {
	url, key string
	client   *http.Client
}

func newDirectory() *directory {
	d := &directory{url: defaultDirectoryURL, key: defaultDirectoryKey, client: &http.Client{Timeout: 10 * time.Second}}
	if v := os.Getenv("PLEXODUS_INVITE_DIRECTORY"); v != "" {
		d.url = strings.TrimRight(v, "/")
	}
	if v := os.Getenv("PLEXODUS_INVITE_DIRECTORY_KEY"); v != "" {
		d.key = v
	}
	if os.Getenv("PLEXODUS_INVITE_DIRECTORY") == "off" {
		d.url = ""
	}
	return d
}

func (d *directory) call(ctx context.Context, fn string, args map[string]any) (json.RawMessage, error) {
	if d == nil || d.url == "" {
		return nil, fmt.Errorf("invite codes are turned off on this server")
	}
	body, _ := json.Marshal(args)
	req, err := http.NewRequestWithContext(ctx, "POST", d.url+"/rpc/"+fn, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apikey", d.key)
	res, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var out json.RawMessage
	json.NewDecoder(res.Body).Decode(&out)
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("invite directory answered %d", res.StatusCode)
	}
	return out, nil
}

// directorySecret proves to the directory that this server owns its codes.
func (s *Server) directorySecret(ctx context.Context) string {
	sec := s.db.Setting(ctx, "invite_directory_secret")
	if sec == "" {
		sec = randomHex(24)
		s.db.SetSetting(ctx, "invite_directory_secret", sec)
	}
	return sec
}

// registerCode publishes code -> this server's address until expires.
func (s *Server) registerCode(ctx context.Context, code, serverURL string, expires time.Time) error {
	out, err := s.directory.call(ctx, "plexodus_register", map[string]any{
		"p_code": code, "p_url": serverURL, "p_secret": s.directorySecret(ctx), "p_expires": expires.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	if string(out) != "true" {
		return fmt.Errorf("the invite directory refused the code")
	}
	return nil
}

// revokeCode takes a code out of the directory (best effort).
func (s *Server) revokeCode(code string) {
	if code == "" {
		return
	}
	ctx, cancel := context.WithTimeout(s.baseCtx, 15*time.Second)
	defer cancel()
	s.directory.call(ctx, "plexodus_revoke", map[string]any{"p_code": code, "p_secret": s.directorySecret(ctx)})
}

// inviteAddress is the address friends reach this server at: the public
// address from Settings (or the one an admin last used from outside), else
// the address this request came in on when that isn't a local one.
func (s *Server) inviteAddress(r *http.Request) string {
	if u := s.publicURL(r.Context()); u != "" {
		return strings.TrimRight(u, "/")
	}
	return requestOrigin(r)
}
