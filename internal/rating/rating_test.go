package rating

import "testing"

func TestAge(t *testing.T) {
	cases := map[string]int{
		"G": 0, "PG": 8, "PG-13": 13, "R": 17, "TV-Y": 0, "TV-Y7": 7, "TV-PG": 8, "TV-14": 14, "TV-MA": 17,
		"gb/U": 0, "gb/PG": 8, "gb/12A": 12, "gb/15": 15, "gb/18": 18, "de/16": 16, "FSK 12": 12, "de/FSK 6": 6,
		"MA15+": 15, "au/M": 15, "14A": 14, "nl/6": 6, "pg-13": 13,
	}
	for in, want := range cases {
		got, ok := Age(in)
		if !ok || got != want {
			t.Errorf("Age(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "NR", "Not Rated", "Unrated", "gb/", "abc"} {
		if _, ok := Age(in); ok {
			t.Errorf("Age(%q) should be unknown", in)
		}
	}
}
