package metadata

import (
	"encoding/json"
	"testing"
)

// TMDB puts appended seasons beside the show's own fields.
func TestSeasonAppendDecode(t *testing.T) {
	body := `{"id": 1, "name": "Show", "seasons": [{"season_number": 1}], "next_episode_to_air": {"id": 9}, "genres": [],
		"season/1": {"episodes": [{"episode_number": 1, "name": "Pilot", "air_date": "2020-01-01", "still_path": "/s.jpg"}]}}`
	var m map[string]seasonPayload
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	if e := m["season/1"].Episodes; len(e) != 1 || e[0].Name != "Pilot" || e[0].StillPath != "/s.jpg" {
		t.Fatalf("%+v", m["season/1"])
	}
}
