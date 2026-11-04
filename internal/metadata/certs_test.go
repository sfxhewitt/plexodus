package metadata

import (
	"encoding/json"
	"testing"
)

func TestCerts(t *testing.T) {
	var rd ReleaseDates
	json.Unmarshal([]byte(`{"results":[{"iso_3166_1":"DE","release_dates":[{"certification":"12"}]},
		{"iso_3166_1":"US","release_dates":[{"certification":""},{"certification":"PG-13"}]}]}`), &rd)
	if c := rd.Cert(); c != "PG-13" {
		t.Errorf("movie cert = %q", c)
	}
	var cr ContentRatings
	json.Unmarshal([]byte(`{"results":[{"iso_3166_1":"GB","rating":"15"},{"iso_3166_1":"US","rating":"NR"}]}`), &cr)
	if c := cr.Cert(); c != "gb/15" {
		t.Errorf("show cert = %q", c)
	}
	if c := (ContentRatings{}).Cert(); c != "" {
		t.Errorf("empty cert = %q", c)
	}
}
