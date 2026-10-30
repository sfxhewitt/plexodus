// Package plex imports libraries, metadata, watch history and intro/credit
// markers from an existing Plex Media Server, so a large library can be
// brought over without rescanning cloud storage or re-matching anything.
package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	base  string
	token string
	http  *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		base:  strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token: strings.TrimSpace(token),
		http:  &http.Client{Timeout: 5 * time.Minute},
	}
}

func (c *Client) Configured() bool { return c != nil && c.base != "" && c.token != "" }

func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	if q == nil {
		q = url.Values{}
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Token", c.token)
	req.Header.Set("X-Plex-Client-Identifier", "plexodus")
	req.Header.Set("X-Plex-Product", "Plexodus")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("plex %s: %s %s", path, resp.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Image fetches an image through Plex's photo transcoder at the given width.
func (c *Client) Image(ctx context.Context, path string, width, height int) (*http.Response, error) {
	q := url.Values{
		"url": {path}, "width": {fmt.Sprint(width)}, "height": {fmt.Sprint(height)},
		"minSize": {"1"}, "upscale": {"0"}, "format": {"jpeg"}, "quality": {"88"},
	}
	req, err := http.NewRequestWithContext(ctx, "GET", c.base+"/photo/:/transcode?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Plex-Token", c.token)
	return c.http.Do(req)
}

type Section struct {
	Key      string `json:"key"`
	Title    string `json:"title"`
	Type     string `json:"type"` // movie | show | artist | photo
	Location []struct {
		Path string `json:"path"`
	} `json:"Location"`
}

func (c *Client) Sections(ctx context.Context) ([]Section, error) {
	var out struct {
		MediaContainer struct {
			Directory []Section `json:"Directory"`
		} `json:"MediaContainer"`
	}
	err := c.get(ctx, "/library/sections", nil, &out)
	return out.MediaContainer.Directory, err
}

type Tag struct {
	Tag string `json:"tag"`
}

type Image struct {
	Type string `json:"type"` // coverPoster | background | clearLogo | snapshot
	URL  string `json:"url"`
}

type Part struct {
	File      string `json:"file"`
	Size      int64  `json:"size"`
	Container string `json:"container"`
	Duration  int64  `json:"duration"`
}

type Media struct {
	Duration     int64  `json:"duration"`
	Bitrate      int64  `json:"bitrate"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	VideoCodec   string `json:"videoCodec"`
	AudioCodec   string `json:"audioCodec"`
	Container    string `json:"container"`
	AudioChannel int    `json:"audioChannels"`
	Part         []Part `json:"Part"`
}

type Marker struct {
	Type      string `json:"type"` // intro | credits
	StartTime int64  `json:"startTimeOffset"`
	EndTime   int64  `json:"endTimeOffset"`
}

type Metadata struct {
	RatingKey             string  `json:"ratingKey"`
	ParentRatingKey       string  `json:"parentRatingKey"`
	GrandparentRatingKey  string  `json:"grandparentRatingKey"`
	Type                  string  `json:"type"`
	Title                 string  `json:"title"`
	OriginalTitle         string  `json:"originalTitle"`
	TitleSort             string  `json:"titleSort"`
	Summary               string  `json:"summary"`
	Tagline               string  `json:"tagline"`
	Year                  int     `json:"year"`
	Index                 int     `json:"index"`
	ParentIndex           int     `json:"parentIndex"`
	Rating                float64 `json:"rating"`
	AudienceRating        float64 `json:"audienceRating"`
	Duration              int64   `json:"duration"`
	OriginallyAvailableAt string  `json:"originallyAvailableAt"`
	AddedAt               int64   `json:"addedAt"`
	Thumb                 string  `json:"thumb"`
	Art                   string  `json:"art"`
	ViewCount             int     `json:"viewCount"`
	ViewOffset            int64   `json:"viewOffset"`
	LastViewedAt          int64   `json:"lastViewedAt"`
	Guid                  []struct {
		ID string `json:"id"`
	} `json:"Guid"`
	Genre  []Tag    `json:"Genre"`
	Image  []Image  `json:"Image"`
	Media  []Media  `json:"Media"`
	Marker []Marker `json:"Marker"`
}

func (m *Metadata) ExternalID(prefix string) string {
	for _, g := range m.Guid {
		if strings.HasPrefix(g.ID, prefix+"://") {
			return strings.TrimPrefix(g.ID, prefix+"://")
		}
	}
	return ""
}

func (m *Metadata) ImageOf(kind string) string {
	for _, im := range m.Image {
		if im.Type == kind {
			return im.URL
		}
	}
	return ""
}

// Plex metadata type numbers for /library/sections/{key}/all?type=N.
const (
	TypeMovie   = 1
	TypeShow    = 2
	TypeSeason  = 3
	TypeEpisode = 4
)

// Each pages through every item of one type in a section.
func (c *Client) Each(ctx context.Context, section string, typ int, pageSize int, fn func([]Metadata) error) error {
	for start := 0; ; start += pageSize {
		var out struct {
			MediaContainer struct {
				TotalSize int        `json:"totalSize"`
				Metadata  []Metadata `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		q := url.Values{
			"type": {fmt.Sprint(typ)}, "includeGuids": {"1"},
			"X-Plex-Container-Start": {fmt.Sprint(start)}, "X-Plex-Container-Size": {fmt.Sprint(pageSize)},
		}
		if typ == TypeEpisode || typ == TypeMovie {
			q.Set("includeMarkers", "1")
		}
		if err := c.get(ctx, "/library/sections/"+section+"/all", q, &out); err != nil {
			return err
		}
		if len(out.MediaContainer.Metadata) == 0 {
			return nil
		}
		if err := fn(out.MediaContainer.Metadata); err != nil {
			return err
		}
		if len(out.MediaContainer.Metadata) < pageSize {
			return nil
		}
	}
}

// Item fetches one item with its markers.
func (c *Client) Item(ctx context.Context, ratingKey string) (*Metadata, error) {
	var out struct {
		MediaContainer struct {
			Metadata []Metadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := c.get(ctx, "/library/metadata/"+ratingKey, url.Values{"includeMarkers": {"1"}}, &out); err != nil {
		return nil, err
	}
	if len(out.MediaContainer.Metadata) == 0 {
		return nil, fmt.Errorf("plex item %s not found", ratingKey)
	}
	return &out.MediaContainer.Metadata[0], nil
}
