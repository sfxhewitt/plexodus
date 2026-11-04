package metadata

import "testing"

func TestBestTrailer(t *testing.T) {
	vs := []Video{
		{Key: "clip", Site: "YouTube", Type: "Clip", Official: true, Language: "en"},
		{Key: "vimeo", Site: "Vimeo", Type: "Trailer", Official: true, Language: "en"},
		{Key: "teaser", Site: "YouTube", Type: "Teaser", Official: true, Language: "en"},
		{Key: "fan", Site: "YouTube", Type: "Trailer", Language: "fr"},
		{Key: "main", Site: "YouTube", Type: "Trailer", Official: true, Language: "en", Name: "Official Trailer", Size: 1080},
		{Key: "spot", Site: "YouTube", Type: "Trailer", Official: true, Language: "en", Name: "TV Spot", Size: 1080},
	}
	if got := BestTrailer(vs); got != "main" {
		t.Fatalf("got %q, want main", got)
	}
	if got := BestTrailer(vs[:3]); got != "teaser" {
		t.Fatalf("got %q, want teaser", got)
	}
	if got := BestTrailer(vs[:2]); got != "" {
		t.Fatalf("got %q, want none", got)
	}
}
