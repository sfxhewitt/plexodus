package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Plex Discover: the plex.tv catalogue behind every account's Watchlist.
// Films and shows there are keyed by the last part of their Plex GUID
// (plex://movie/5d776b59ad5437001f79c6f8 -> 5d776b59ad5437001f79c6f8).

// DiscoverBase is Plex's Discover service (tests point it at a fake).
var DiscoverBase = "https://discover.provider.plex.tv"

// ErrNotOnPlex means Discover has no match for the title.
var ErrNotOnPlex = errors.New("couldn't find it on Plex")

// DiscoverKey returns the Discover id in a Plex GUID such as
// plex://movie/5d776b59ad5437001f79c6f8, or "" for any other kind of GUID.
func DiscoverKey(guid string) string {
	for _, p := range []string{"plex://movie/", "plex://show/"} {
		if strings.HasPrefix(guid, p) {
			k := strings.TrimPrefix(guid, p)
			if k != "" && !strings.ContainsAny(k, "/?") {
				return k
			}
		}
	}
	return ""
}

func (a Account) discover(ctx context.Context, method, path, token string, q url.Values, out any) (int, error) {
	u := DiscoverBase + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Product", "Plexodus")
	req.Header.Set("X-Plex-Client-Identifier", a.ClientID)
	req.Header.Set("X-Plex-Token", token)
	resp, err := discoverHTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return resp.StatusCode, ErrBadToken
	}
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return resp.StatusCode, fmt.Errorf("plex discover %s: %s %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	if out != nil {
		return resp.StatusCode, json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode, nil
}

var discoverHTTP = &http.Client{Timeout: 15 * time.Second}

// DiscoverResult is one film or show in a Discover search.
type DiscoverResult struct {
	Key   string
	Type  string // movie | show
	Title string
	Year  int
	IDs   []string // external ids (tmdb://949, imdb://tt0113277), when Plex sends them
}

// SearchDiscover looks a film (kind "movie") or show ("show") up by title.
func (a Account) SearchDiscover(ctx context.Context, token, query, kind string) ([]DiscoverResult, error) {
	types := "movies"
	if kind == "show" {
		types = "tv"
	}
	var out struct {
		MediaContainer struct {
			SearchResults []struct {
				ID           string `json:"id"`
				SearchResult []struct {
					Metadata struct {
						GUID  string `json:"guid"`
						Type  string `json:"type"`
						Title string `json:"title"`
						Year  int    `json:"year"`
						Guids []struct {
							ID string `json:"id"`
						} `json:"Guid"`
					} `json:"Metadata"`
				} `json:"SearchResult"`
			} `json:"SearchResults"`
		} `json:"MediaContainer"`
	}
	q := url.Values{
		"query": {query}, "limit": {"20"}, "searchTypes": {types},
		"searchProviders": {"discover"}, "includeMetadata": {"1"}, "includeGuids": {"1"},
	}
	if _, err := a.discover(ctx, "GET", "/library/search", token, q, &out); err != nil {
		return nil, err
	}
	var res []DiscoverResult
	for _, sr := range out.MediaContainer.SearchResults {
		for _, r := range sr.SearchResult {
			m := r.Metadata
			k := DiscoverKey(m.GUID)
			if k == "" || (m.Type != "movie" && m.Type != "show") {
				continue
			}
			d := DiscoverResult{Key: k, Type: m.Type, Title: m.Title, Year: m.Year}
			for _, g := range m.Guids {
				d.IDs = append(d.IDs, g.ID)
			}
			res = append(res, d)
		}
	}
	return res, nil
}

// BestMatch picks the search result for a film or show: the one with the
// same TMDB or IMDb id, else the same title and year (one year either way,
// release dates differ by country), else the same title if it's the only one.
func BestMatch(results []DiscoverResult, kind, title string, year int, tmdb int64, imdb string) (DiscoverResult, bool) {
	want := map[string]bool{}
	if tmdb > 0 {
		want[fmt.Sprintf("tmdb://%d", tmdb)] = true
	}
	if imdb != "" {
		want["imdb://"+imdb] = true
	}
	var sameTitle []DiscoverResult
	for _, r := range results {
		if r.Type != kind {
			continue
		}
		for _, id := range r.IDs {
			if want[id] {
				return r, true
			}
		}
		if normTitle(r.Title) == normTitle(title) {
			sameTitle = append(sameTitle, r)
		}
	}
	if year > 0 {
		for _, d := range []int{0, 1, -1} {
			for _, r := range sameTitle {
				if r.Year == year+d {
					return r, true
				}
			}
		}
	}
	if len(sameTitle) == 1 && (year == 0 || sameTitle[0].Year == 0) {
		return sameTitle[0], true
	}
	return DiscoverResult{}, false
}

func normTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r > 127 {
			b.WriteRune(r)
		}
	}
	out := b.String()
	out = strings.TrimPrefix(out, "the")
	return out
}

// OnWatchlist says whether the film or show is already on the token's watchlist.
func (a Account) OnWatchlist(ctx context.Context, token, key string) (bool, error) {
	var out struct {
		MediaContainer struct {
			UserState []struct {
				WatchlistedAt int64 `json:"watchlistedAt"`
			} `json:"UserState"`
		} `json:"MediaContainer"`
	}
	if _, err := a.discover(ctx, "GET", "/library/metadata/"+url.PathEscape(key)+"/userState", token, nil, &out); err != nil {
		return false, err
	}
	for _, u := range out.MediaContainer.UserState {
		if u.WatchlistedAt > 0 {
			return true, nil
		}
	}
	return false, nil
}

// AddToWatchlist puts a Discover film or show on the token's account
// Watchlist. already is true when it was there before.
func (a Account) AddToWatchlist(ctx context.Context, token, key string) (already bool, err error) {
	if on, err := a.OnWatchlist(ctx, token, key); err == nil && on {
		return true, nil
	} else if errors.Is(err, ErrBadToken) {
		return false, err
	}
	_, err = a.discover(ctx, "PUT", "/actions/addToWatchlist", token, url.Values{"ratingKey": {key}}, nil)
	if err != nil && !errors.Is(err, ErrBadToken) {
		// Plex answers an error for titles already on the list.
		if on, e := a.OnWatchlist(ctx, token, key); e == nil && on {
			return true, nil
		}
	}
	return false, err
}
