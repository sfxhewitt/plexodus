// Package metadata matches library items against TMDB and caches artwork.
package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const apiBase = "https://api.themoviedb.org/3"

type TMDB struct {
	key    string
	client *http.Client
	limit  <-chan time.Time
}

func NewTMDB(key string) *TMDB {
	return &TMDB{
		key:    strings.TrimSpace(key),
		client: &http.Client{Timeout: 20 * time.Second},
		// TMDB allows roughly 50 requests/second; stay well under.
		limit: time.Tick(time.Second / 25),
	}
}

func (t *TMDB) Enabled() bool { return t != nil && t.key != "" }

func (t *TMDB) get(ctx context.Context, path string, q url.Values, out any) error {
	if q == nil {
		q = url.Values{}
	}
	req, err := http.NewRequestWithContext(ctx, "GET", apiBase+path, nil)
	if err != nil {
		return err
	}
	// v4 read access tokens are JWTs; v3 keys are 32 hex characters.
	if strings.HasPrefix(t.key, "eyJ") {
		req.Header.Set("Authorization", "Bearer "+t.key)
	} else {
		q.Set("api_key", t.key)
	}
	req.URL.RawQuery = q.Encode()
	req.Header.Set("Accept", "application/json")

	for attempt := 0; ; attempt++ {
		select {
		case <-t.limit:
		case <-ctx.Done():
			return ctx.Err()
		}
		resp, err := t.client.Do(req)
		if err != nil {
			if attempt < 3 {
				time.Sleep(time.Duration(attempt+1) * time.Second)
				continue
			}
			return err
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode == 429 && attempt < 5 {
			time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
			continue
		}
		if resp.StatusCode != 200 {
			return fmt.Errorf("tmdb %s: %s: %s", path, resp.Status, truncate(string(body), 200))
		}
		if err != nil {
			return err
		}
		return json.Unmarshal(body, out)
	}
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

type SearchResult struct {
	ID            int64   `json:"id"`
	Title         string  `json:"title"`
	Name          string  `json:"name"`
	OriginalTitle string  `json:"original_title"`
	OriginalName  string  `json:"original_name"`
	ReleaseDate   string  `json:"release_date"`
	FirstAirDate  string  `json:"first_air_date"`
	Overview      string  `json:"overview"`
	PosterPath    string  `json:"poster_path"`
	Popularity    float64 `json:"popularity"`
}

func (r SearchResult) DisplayTitle() string {
	if r.Title != "" {
		return r.Title
	}
	return r.Name
}

func (r SearchResult) Year() string {
	d := r.ReleaseDate
	if d == "" {
		d = r.FirstAirDate
	}
	if len(d) >= 4 {
		return d[:4]
	}
	return ""
}

// Search searches movies ("movie") or shows ("tv").
func (t *TMDB) Search(ctx context.Context, kind, query string, year int) ([]SearchResult, error) {
	q := url.Values{"query": {query}, "include_adult": {"false"}}
	if year > 0 {
		if kind == "movie" {
			q.Set("year", fmt.Sprint(year))
		} else {
			q.Set("first_air_date_year", fmt.Sprint(year))
		}
	}
	var out struct {
		Results []SearchResult `json:"results"`
	}
	err := t.get(ctx, "/search/"+kind, q, &out)
	return out.Results, err
}

type Genre struct {
	Name string `json:"name"`
}

type Images struct {
	Logos []struct {
		FilePath    string  `json:"file_path"`
		Language    string  `json:"iso_639_1"`
		VoteAverage float64 `json:"vote_average"`
	} `json:"logos"`
}

func (im Images) BestLogo() string {
	best, score := "", -1.0
	for _, l := range im.Logos {
		s := l.VoteAverage
		if l.Language == "en" {
			s += 100
		}
		if strings.HasSuffix(l.FilePath, ".svg") {
			continue
		}
		if s > score {
			best, score = l.FilePath, s
		}
	}
	return best
}

type MovieDetails struct {
	ID            int64   `json:"id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Overview      string  `json:"overview"`
	Tagline       string  `json:"tagline"`
	Runtime       int     `json:"runtime"`
	ReleaseDate   string  `json:"release_date"`
	VoteAverage   float64 `json:"vote_average"`
	ImdbID        string  `json:"imdb_id"`
	PosterPath    string  `json:"poster_path"`
	BackdropPath  string  `json:"backdrop_path"`
	Genres        []Genre `json:"genres"`
	Images        Images  `json:"images"`
}

func (t *TMDB) Movie(ctx context.Context, id int64) (*MovieDetails, error) {
	var m MovieDetails
	err := t.get(ctx, fmt.Sprintf("/movie/%d", id), url.Values{
		"append_to_response": {"images"}, "include_image_language": {"en,null"},
	}, &m)
	return &m, err
}

type ShowDetails struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	OriginalName   string  `json:"original_name"`
	Overview       string  `json:"overview"`
	Tagline        string  `json:"tagline"`
	FirstAirDate   string  `json:"first_air_date"`
	VoteAverage    float64 `json:"vote_average"`
	EpisodeRunTime []int   `json:"episode_run_time"`
	PosterPath     string  `json:"poster_path"`
	BackdropPath   string  `json:"backdrop_path"`
	Genres         []Genre `json:"genres"`
	Images         Images  `json:"images"`
	ExternalIDs    struct {
		ImdbID string `json:"imdb_id"`
	} `json:"external_ids"`
}

func (t *TMDB) Show(ctx context.Context, id int64) (*ShowDetails, error) {
	var s ShowDetails
	err := t.get(ctx, fmt.Sprintf("/tv/%d", id), url.Values{
		"append_to_response": {"images,external_ids"}, "include_image_language": {"en,null"},
	}, &s)
	return &s, err
}

type SeasonDetails struct {
	Name       string `json:"name"`
	Overview   string `json:"overview"`
	AirDate    string `json:"air_date"`
	PosterPath string `json:"poster_path"`
	Episodes   []struct {
		EpisodeNumber int     `json:"episode_number"`
		Name          string  `json:"name"`
		Overview      string  `json:"overview"`
		AirDate       string  `json:"air_date"`
		Runtime       int     `json:"runtime"`
		StillPath     string  `json:"still_path"`
		VoteAverage   float64 `json:"vote_average"`
	} `json:"episodes"`
}

func (t *TMDB) Season(ctx context.Context, showID int64, n int) (*SeasonDetails, error) {
	var s SeasonDetails
	err := t.get(ctx, fmt.Sprintf("/tv/%d/season/%d", showID, n), nil, &s)
	return &s, err
}

// keyHolder lets the API swap the TMDB key at runtime (set from settings).
type keyHolder struct {
	mu   sync.RWMutex
	tmdb *TMDB
}

func (k *keyHolder) get() *TMDB {
	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.tmdb
}

func (k *keyHolder) set(key string) {
	k.mu.Lock()
	k.tmdb = NewTMDB(key)
	k.mu.Unlock()
}
