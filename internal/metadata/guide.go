package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Guide is every season and episode TMDB knows for a show, aired or not,
// so show pages can list what's missing from the library and what's coming.
type Guide struct {
	TMDB    int64         `json:"tmdbId"`
	Status  string        `json:"status,omitempty"` // "Returning Series", "Ended", "Canceled", "In Production"…
	Seasons []GuideSeason `json:"seasons"`
}

type GuideSeason struct {
	Season   int            `json:"season"`
	Name     string         `json:"name,omitempty"`
	AirDate  string         `json:"airDate,omitempty"`
	Poster   string         `json:"poster,omitempty"`
	Overview string         `json:"overview,omitempty"`
	Episodes []GuideEpisode `json:"episodes"`
}

type GuideEpisode struct {
	Season   int    `json:"season"`
	Episode  int    `json:"episode"`
	Name     string `json:"name,omitempty"`
	Overview string `json:"overview,omitempty"`
	AirDate  string `json:"airDate,omitempty"` // YYYY-MM-DD; empty = not announced
	Runtime  int    `json:"runtime,omitempty"`
	Still    string `json:"still,omitempty"`
}

// Ended says the show will get no more episodes, so its guide rarely changes.
func (g *Guide) Ended() bool { return g.Status == "Ended" || g.Status == "Canceled" }

// TMDB allows 20 appended responses per request.
const maxAppend = 20

// ShowGuide fetches a show's full season and episode list: the show itself,
// then its seasons appended 20 at a time, so most shows take two requests.
func (t *TMDB) ShowGuide(ctx context.Context, id int64) (*Guide, error) {
	var show struct {
		Status  string `json:"status"`
		Seasons []struct {
			SeasonNumber int    `json:"season_number"`
			EpisodeCount int    `json:"episode_count"`
			Name         string `json:"name"`
			AirDate      string `json:"air_date"`
			PosterPath   string `json:"poster_path"`
			Overview     string `json:"overview"`
		} `json:"seasons"`
	}
	if err := t.get(ctx, fmt.Sprintf("/tv/%d", id), nil, &show); err != nil {
		return nil, err
	}
	g := &Guide{TMDB: id, Status: show.Status, Seasons: []GuideSeason{}}
	var numbers []int
	for _, s := range show.Seasons {
		g.Seasons = append(g.Seasons, GuideSeason{Season: s.SeasonNumber, Name: s.Name, AirDate: s.AirDate,
			Poster: s.PosterPath, Overview: strings.TrimSpace(s.Overview), Episodes: []GuideEpisode{}})
		numbers = append(numbers, s.SeasonNumber)
	}
	for start := 0; start < len(numbers); start += maxAppend {
		end := min(start+maxAppend, len(numbers))
		var parts []string
		for _, n := range numbers[start:end] {
			parts = append(parts, fmt.Sprintf("season/%d", n))
		}
		var m map[string]seasonPayload
		if err := t.get(ctx, fmt.Sprintf("/tv/%d", id), url.Values{"append_to_response": {strings.Join(parts, ",")}}, &m); err != nil {
			return nil, err
		}
		for i := start; i < end; i++ {
			sp, ok := m[fmt.Sprintf("season/%d", numbers[i])]
			if !ok {
				continue
			}
			for _, e := range sp.Episodes {
				g.Seasons[i].Episodes = append(g.Seasons[i].Episodes, GuideEpisode{
					Season: numbers[i], Episode: e.EpisodeNumber, Name: e.Name, Overview: strings.TrimSpace(e.Overview),
					AirDate: e.AirDate, Runtime: e.Runtime, Still: e.StillPath,
				})
			}
		}
	}
	return g, nil
}

// seasonPayload is one appended season; the map it sits in also holds the
// show's own fields, which decode as zero seasons and are ignored.
type seasonPayload struct {
	Episodes []struct {
		EpisodeNumber int    `json:"episode_number"`
		Name          string `json:"name"`
		Overview      string `json:"overview"`
		AirDate       string `json:"air_date"`
		Runtime       int    `json:"runtime"`
		StillPath     string `json:"still_path"`
	} `json:"episodes"`
}

func (p *seasonPayload) UnmarshalJSON(b []byte) error {
	// The show's other fields (strings, numbers, arrays) aren't seasons.
	if len(b) == 0 || b[0] != '{' {
		return nil
	}
	type plain seasonPayload
	var v plain
	if err := json.Unmarshal(b, &v); err != nil {
		return nil
	}
	*p = seasonPayload(v)
	return nil
}
