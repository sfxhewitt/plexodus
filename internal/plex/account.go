package plex

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// plex.tv account API: PIN sign-in, who a token belongs to, which servers it
// can reach, and who the owner shares a server with.

// TVBase is plex.tv (tests point it at a fake).
var TVBase = "https://plex.tv"

var accountHTTP = &http.Client{Timeout: 15 * time.Second}

// Account talks to plex.tv as one Plexodus server (ClientID is stable per server).
type Account struct {
	ClientID string
}

func (a Account) do(ctx context.Context, method, path, token string, q url.Values, out any) error {
	u := TVBase + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Product", "Plexodus")
	req.Header.Set("X-Plex-Client-Identifier", a.ClientID)
	if token != "" {
		req.Header.Set("X-Plex-Token", token)
	}
	resp, err := accountHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return ErrBadToken
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("plex.tv %s: %s %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	if out == nil {
		return nil
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "xml") {
		return xml.NewDecoder(resp.Body).Decode(out)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

var ErrBadToken = fmt.Errorf("plex.tv rejected the sign-in")

type Pin struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	AuthToken string `json:"authToken"`
	ExpiresIn int    `json:"expiresIn"`
}

// NewPin starts a PIN sign-in. Short (4 character) PINs are typed at
// plex.tv/link; strong ones go in the app.plex.tv/auth URL.
func (a Account) NewPin(ctx context.Context, strong bool) (Pin, error) {
	var p Pin
	err := a.do(ctx, "POST", "/api/v2/pins", "", url.Values{"strong": {fmt.Sprint(strong)}}, &p)
	return p, err
}

// CheckPin returns the PIN; AuthToken is set once the user has approved it.
func (a Account) CheckPin(ctx context.Context, id int64) (Pin, error) {
	var p Pin
	err := a.do(ctx, "GET", fmt.Sprintf("/api/v2/pins/%d", id), "", nil, &p)
	return p, err
}

// AuthURL is the plex.tv page that approves a strong PIN in a browser.
func (a Account) AuthURL(code string) string {
	q := url.Values{"clientID": {a.ClientID}, "code": {code}, "context[device][product]": {"Plexodus"}}
	return "https://app.plex.tv/auth#?" + q.Encode()
}

type User struct {
	ID       int64  `json:"id"`
	UUID     string `json:"uuid"`
	Username string `json:"username"`
	Title    string `json:"title"`
	Email    string `json:"email"`
	Thumb    string `json:"thumb"`
}

// Name is the best display name for the account.
func (u User) Name() string {
	for _, s := range []string{u.Username, u.Title, strings.Split(u.Email, "@")[0]} {
		if s = strings.TrimSpace(s); s != "" {
			return s
		}
	}
	return fmt.Sprintf("plex%d", u.ID)
}

// Me returns the account a token belongs to.
func (a Account) Me(ctx context.Context, token string) (User, error) {
	var u User
	err := a.do(ctx, "GET", "/api/v2/user", token, nil, &u)
	if err == nil && u.ID == 0 {
		err = ErrBadToken
	}
	return u, err
}

type Connection struct {
	URI      string `json:"uri"`
	Local    bool   `json:"local"`
	Relay    bool   `json:"relay"`
	Protocol string `json:"protocol"`
}

type Resource struct {
	Name             string       `json:"name"`
	ClientIdentifier string       `json:"clientIdentifier"`
	Provides         string       `json:"provides"`
	Owned            bool         `json:"owned"`
	Connections      []Connection `json:"connections"`
}

// Resources lists the servers and devices the token's account can reach,
// its own and those shared with it.
func (a Account) Resources(ctx context.Context, token string) ([]Resource, error) {
	var out []Resource
	err := a.do(ctx, "GET", "/api/v2/resources", token, url.Values{"includeHttps": {"1"}, "includeRelay": {"1"}}, &out)
	return out, err
}

// Share is one friend's access to the owner's server.
type Share struct {
	UserID       int64    `xml:"userID,attr"`
	Username     string   `xml:"username,attr"`
	AllLibraries bool     `xml:"allLibraries,attr"`
	Sections     []string // Plex section keys shared with them
}

// Shares lists who the owner (token) shares the server with, keyed by
// plex.tv user id.
func (a Account) Shares(ctx context.Context, ownerToken, machineID string) (map[int64]Share, error) {
	var out struct {
		Servers []struct {
			UserID       int64  `xml:"userID,attr"`
			Username     string `xml:"username,attr"`
			AllLibraries string `xml:"allLibraries,attr"`
			Sections     []struct {
				Key    string `xml:"key,attr"`
				Shared string `xml:"shared,attr"`
			} `xml:"Section"`
		} `xml:"SharedServer"`
	}
	if err := a.doXML(ctx, "/api/servers/"+url.PathEscape(machineID)+"/shared_servers", ownerToken, &out); err != nil {
		return nil, err
	}
	m := map[int64]Share{}
	for _, s := range out.Servers {
		if s.UserID == 0 {
			continue
		}
		sh := Share{UserID: s.UserID, Username: s.Username, AllLibraries: s.AllLibraries == "1"}
		for _, sec := range s.Sections {
			if sec.Shared == "1" {
				sh.Sections = append(sh.Sections, sec.Key)
			}
		}
		m[s.UserID] = sh
	}
	if len(out.Servers) > 0 && len(m) == 0 {
		// Never treat a reply we can't read as "shared with nobody".
		return nil, fmt.Errorf("plex.tv shared_servers: unexpected reply format")
	}
	return m, nil
}

// doXML is for the older plex.tv endpoints that only speak XML.
func (a Account) doXML(ctx context.Context, path, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", TVBase+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Plex-Product", "Plexodus")
	req.Header.Set("X-Plex-Client-Identifier", a.ClientID)
	req.Header.Set("X-Plex-Token", token)
	resp, err := accountHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("plex.tv %s: %s", path, resp.Status)
	}
	return xml.NewDecoder(resp.Body).Decode(out)
}

// ---- the local Plex Media Server ----

// Identity returns the server's machine identifier (its plex.tv clientIdentifier).
func (c *Client) Identity(ctx context.Context) (string, error) {
	var out struct {
		MediaContainer struct {
			MachineIdentifier string `json:"machineIdentifier"`
		} `json:"MediaContainer"`
	}
	if err := c.get(ctx, "/identity", nil, &out); err != nil {
		return "", err
	}
	if out.MediaContainer.MachineIdentifier == "" {
		return "", fmt.Errorf("plex didn't say which server it is")
	}
	return out.MediaContainer.MachineIdentifier, nil
}

// Pref reads one Plex server setting.
func (c *Client) Pref(ctx context.Context, id string) (string, error) {
	var out struct {
		MediaContainer struct {
			Setting []struct {
				ID    string `json:"id"`
				Value any    `json:"value"`
			} `json:"Setting"`
		} `json:"MediaContainer"`
	}
	if err := c.get(ctx, "/:/prefs", nil, &out); err != nil {
		return "", err
	}
	for _, s := range out.MediaContainer.Setting {
		if s.ID == id {
			return fmt.Sprint(s.Value), nil
		}
	}
	return "", nil
}

// SetPref changes one Plex server setting (owner token needed).
func (c *Client) SetPref(ctx context.Context, id, value string) error {
	q := url.Values{id: {value}}
	req, err := http.NewRequestWithContext(ctx, "PUT", c.base+"/:/prefs?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Plex-Token", c.token)
	req.Header.Set("X-Plex-Client-Identifier", "plexodus")
	req.Header.Set("X-Plex-Product", "Plexodus")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("plex prefs: %s", resp.Status)
	}
	return nil
}

// WithConnection returns Plex's comma-separated customConnections with url
// added (add=true) or removed, leaving every other entry as it was.
func WithConnection(current, u string, add bool) string {
	var out []string
	found := false
	for _, p := range strings.Split(current, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.EqualFold(strings.TrimRight(p, "/"), strings.TrimRight(u, "/")) {
			if !add || found {
				continue
			}
			found = true
		}
		out = append(out, p)
	}
	if add && !found && u != "" {
		out = append(out, u)
	}
	return strings.Join(out, ",")
}
