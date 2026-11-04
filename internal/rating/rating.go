// Package rating turns content ratings (certifications) such as "PG-13",
// "TV-Y7", "gb/15" or "FSK 16" into a minimum viewer age, which kids
// profiles filter on.
package rating

import (
	"strconv"
	"strings"
)

// Known names. Ages are the youngest viewer each rating suits; PG-style
// "parental guidance" ratings count as 8.
var named = map[string]int{
	// US film
	"G": 0, "PG": 8, "PG-13": 13, "R": 17, "NC-17": 18, "X": 18,
	// US TV
	"TV-Y": 0, "TV-G": 0, "TV-Y7": 7, "TV-Y7-FV": 7, "TV-PG": 8, "TV-14": 14, "TV-MA": 17,
	// UK
	"U": 0, "UC": 0, "12A": 12, "R18": 18,
	// Australia / NZ
	"P": 0, "C": 0, "M": 15, "MA15+": 15, "MA 15+": 15, "R18+": 18, "R 18+": 18, "X18+": 18, "RP13": 13, "RP16": 16,
	// Canada
	"14A": 14, "18A": 18, "E": 0,
	// Misc
	"ALL": 0, "AL": 0, "TP": 0, "L": 0, "A": 0, "BTL": 0, "KT": 0,
	"APPROVED": 0, "PASSED": 0,
}

// Age returns the minimum age for a rating, and false when the rating is
// empty or unknown ("NR", "Unrated", ...), which kids profiles treat as
// not suitable.
func Age(cert string) (int, bool) {
	c := strings.ToUpper(strings.TrimSpace(cert))
	// Plex prefixes non-US ratings with a country: "gb/15", "de/16".
	if i := strings.LastIndexByte(c, '/'); i >= 0 {
		c = c[i+1:]
	}
	c = strings.TrimSpace(c)
	if c == "" {
		return 0, false
	}
	if a, ok := named[c]; ok {
		return a, true
	}
	for _, p := range []string{"FSK", "FSK-", "PEGI", "K-", "K", "+", "-"} {
		c = strings.TrimSpace(strings.TrimPrefix(c, p))
	}
	c = strings.TrimSuffix(strings.TrimSuffix(c, "+"), "A")
	if n, err := strconv.Atoi(c); err == nil && n >= 0 && n <= 21 {
		return n, true
	}
	return 0, false
}

// Levels are the choices offered for a kids profile.
var Levels = []struct {
	MaxAge int    `json:"maxAge"`
	Label  string `json:"label"`
	Detail string `json:"detail"`
}{
	{0, "Little kids", "G, TV-Y, TV-G, U"},
	{8, "Kids", "Up to PG, TV-Y7, TV-PG"},
	{14, "Teens", "Up to PG-13, TV-14, 12A"},
}
