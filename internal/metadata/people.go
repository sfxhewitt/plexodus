package metadata

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

// PersonDetails is a cast member's page: who they are and everything TMDB
// lists them in, as an actor, most notable first.
type PersonDetails struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Biography  string   `json:"biography,omitempty"`
	Profile    string   `json:"profile,omitempty"`
	Birthday   string   `json:"birthday,omitempty"`
	Deathday   string   `json:"deathday,omitempty"`
	Birthplace string   `json:"birthplace,omitempty"`
	KnownFor   string   `json:"knownFor,omitempty"`
	Credits    []Credit `json:"credits"`
}

// Credit is one film or show a person appeared in. Kind is "movie" or
// "show", like library items.
type Credit struct {
	TMDB       int64   `json:"tmdbId"`
	Kind       string  `json:"kind"`
	Title      string  `json:"title"`
	Year       int     `json:"year,omitempty"`
	Character  string  `json:"character,omitempty"`
	Poster     string  `json:"poster,omitempty"`
	Episodes   int     `json:"episodes,omitempty"`
	Popularity float64 `json:"-"`
	Votes      int     `json:"-"`
}

// Person fetches a person's details and acting credits in one request.
func (t *TMDB) Person(ctx context.Context, id int64) (*PersonDetails, error) {
	var m struct {
		ID                 int64  `json:"id"`
		Name               string `json:"name"`
		Biography          string `json:"biography"`
		ProfilePath        string `json:"profile_path"`
		Birthday           string `json:"birthday"`
		Deathday           string `json:"deathday"`
		PlaceOfBirth       string `json:"place_of_birth"`
		KnownForDepartment string `json:"known_for_department"`
		Credits            struct {
			Cast []struct {
				ID           int64   `json:"id"`
				MediaType    string  `json:"media_type"`
				Title        string  `json:"title"`
				Name         string  `json:"name"`
				ReleaseDate  string  `json:"release_date"`
				FirstAirDate string  `json:"first_air_date"`
				Character    string  `json:"character"`
				PosterPath   string  `json:"poster_path"`
				EpisodeCount int     `json:"episode_count"`
				Popularity   float64 `json:"popularity"`
				VoteCount    int     `json:"vote_count"`
				GenreIDs     []int   `json:"genre_ids"`
			} `json:"cast"`
		} `json:"combined_credits"`
	}
	q := url.Values{"append_to_response": {"combined_credits"}}
	if err := t.get(ctx, fmt.Sprintf("/person/%d", id), q, &m); err != nil {
		return nil, err
	}
	p := &PersonDetails{
		ID: m.ID, Name: m.Name, Biography: strings.TrimSpace(m.Biography), Profile: m.ProfilePath,
		Birthday: m.Birthday, Deathday: m.Deathday, Birthplace: m.PlaceOfBirth, KnownFor: m.KnownForDepartment,
		Credits: []Credit{},
	}
	seen := map[string]int{}
	for _, c := range m.Credits.Cast {
		kind := "movie"
		title, date := c.Title, c.ReleaseDate
		if c.MediaType == "tv" {
			kind, title, date = "show", c.Name, c.FirstAirDate
			// Talk shows, news and award ceremonies, where they appear as themselves.
			if isAppearance(c.GenreIDs) {
				continue
			}
		} else if c.MediaType != "movie" {
			continue
		}
		key := fmt.Sprint(kind, c.ID)
		if i, dup := seen[key]; dup {
			// The same film twice is two roles in it.
			if c.Character != "" && !strings.Contains(p.Credits[i].Character, c.Character) {
				p.Credits[i].Character = strings.TrimPrefix(p.Credits[i].Character+" / "+c.Character, " / ")
			}
			continue
		}
		cr := Credit{TMDB: c.ID, Kind: kind, Title: title, Character: strings.TrimSpace(c.Character), Poster: c.PosterPath,
			Episodes: c.EpisodeCount, Popularity: c.Popularity, Votes: c.VoteCount}
		if len(date) >= 4 {
			fmt.Sscan(date[:4], &cr.Year)
		}
		seen[key] = len(p.Credits)
		p.Credits = append(p.Credits, cr)
	}
	SortCredits(p.Credits)
	return p, nil
}

// TMDB's TV genres for talk, news and reality.
var appearanceGenres = map[int]bool{10767: true, 10763: true, 10764: true}

func isAppearance(ids []int) bool {
	for _, g := range ids {
		if appearanceGenres[g] {
			return true
		}
	}
	return false
}

// SortCredits puts the best-known work first: titles many people have rated,
// then popularity. Bit parts in obscure titles sink to the end.
func SortCredits(cs []Credit) {
	score := func(c Credit) float64 {
		s := float64(c.Votes) + c.Popularity*20
		if c.Poster == "" {
			s /= 10
		}
		return s
	}
	sort.SliceStable(cs, func(i, j int) bool { return score(cs[i]) > score(cs[j]) })
}
