package plex

import (
	"encoding/json"
	"testing"
)

// Real servers send both "guid" (string) and "Guid" (array), and both
// "rating" (number) and "Rating" (array) on the same item.
func TestMetadataDecodesMixedCaseKeys(t *testing.T) {
	body := `{"MediaContainer":{"Metadata":[{
		"ratingKey":"1","type":"movie","title":"Heat","guid":"plex://movie/5d776",
		"rating":8.1,"Rating":[{"image":"imdb://image.rating","value":8.3,"type":"audience"}],
		"Guid":[{"id":"imdb://tt0113277"},{"id":"tmdb://949"}]
	},{
		"ratingKey":"2","type":"movie","title":"Old","guid":"com.plexapp.agents.themoviedb://278?lang=en"
	},{
		"ratingKey":"3","type":"episode","title":"Ep","guid":"com.plexapp.agents.themoviedb://1399/1/1?lang=en"
	}]}}`
	var r struct {
		MediaContainer struct {
			Metadata []Metadata `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatal(err)
	}
	m := r.MediaContainer.Metadata
	if m[0].ExternalID("tmdb") != "949" || m[0].ExternalID("imdb") != "tt0113277" || m[0].Rating != 8.1 {
		t.Errorf("modern item: %+v", m[0])
	}
	if got := m[1].ExternalID("tmdb"); got != "278" {
		t.Errorf("legacy movie tmdb = %q", got)
	}
	if got := m[2].ExternalID("tmdb"); got != "" {
		t.Errorf("legacy episode tmdb = %q, want empty", got)
	}
}
