package metadata

import (
	"context"
	"fmt"
	"net/url"
)

// Person is one cast member on a detail page.
type Person struct {
	Name      string `json:"name"`
	Character string `json:"character,omitempty"`
	Profile   string `json:"profile,omitempty"` // TMDB image path
}

// Extras is what a detail page shows below the fold: the top-billed cast
// and TMDB's recommendations (as TMDB ids of the same kind).
type Extras struct {
	Cast            []Person
	Recommendations []int64
}

// Extras fetches cast and recommendations for a film ("movie") or show ("tv")
// in one request.
func (t *TMDB) Extras(ctx context.Context, kind string, id int64) (*Extras, error) {
	var m struct {
		Credits struct {
			Cast []struct {
				Name        string `json:"name"`
				Character   string `json:"character"`
				ProfilePath string `json:"profile_path"`
			} `json:"cast"`
		} `json:"credits"`
		Recommendations struct {
			Results []struct {
				ID int64 `json:"id"`
			} `json:"results"`
		} `json:"recommendations"`
	}
	q := url.Values{"append_to_response": {"credits,recommendations"}}
	if err := t.get(ctx, fmt.Sprintf("/%s/%d", kind, id), q, &m); err != nil {
		return nil, err
	}
	e := &Extras{}
	for _, c := range m.Credits.Cast {
		if len(e.Cast) == 15 {
			break
		}
		e.Cast = append(e.Cast, Person{c.Name, c.Character, c.ProfilePath})
	}
	for _, r := range m.Recommendations.Results {
		e.Recommendations = append(e.Recommendations, r.ID)
	}
	return e, nil
}
