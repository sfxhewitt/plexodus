package ask

import (
	"reflect"
	"testing"
)

var libGenres = []string{"Action", "Comedy", "Crime", "Drama", "Horror", "Science Fiction", "Sci-Fi & Fantasy", "Thriller", "Animation", "Documentary"}

func TestBasic(t *testing.T) {
	cases := []struct {
		in   string
		want Query
	}{
		{"90s heist films under two hours I haven't seen", Query{
			Kinds: []string{"movie"}, YearFrom: 1990, YearTo: 1999, MaxRuntime: 120, Watched: "unwatched",
			Keywords: []string{"heist"}, Sort: "relevance"}},
		{"scary movies from the 1980s", Query{Kinds: []string{"movie"}, Genres: []string{"Horror"}, YearFrom: 1980, YearTo: 1989, Watched: "any", Sort: "rating"}},
		{"something like Severance", Query{SimilarTo: "severance", Watched: "any", Sort: "rating"}},
		{"funny shows after 2015", Query{Kinds: []string{"show"}, Genres: []string{"Comedy"}, YearFrom: 2015, Watched: "any", Sort: "rating"}},
		{"show me sci-fi under 90 minutes", Query{Genres: []string{"Science Fiction", "Sci-Fi & Fantasy"}, MaxRuntime: 90, Watched: "any", Sort: "rating"}},
		{"best science fiction films", Query{Kinds: []string{"movie"}, Genres: []string{"Science Fiction"}, MinRating: 7, Watched: "any", Sort: "rating"}},
		{"crime dramas from 2001 to 2005 i haven't watched", Query{Genres: []string{"Crime", "Drama"}, YearFrom: 2001, YearTo: 2005, Watched: "unwatched", Sort: "rating"}},
	}
	for _, c := range cases {
		got := Basic(c.in, libGenres)
		got.Normalize()
		c.want.Normalize()
		if !reflect.DeepEqual(norm(got), norm(c.want)) {
			t.Errorf("Basic(%q)\n got %+v\nwant %+v", c.in, got, c.want)
		}
	}
}

// norm makes nil and empty slices compare equal.
func norm(q Query) Query {
	if len(q.Kinds) == 0 {
		q.Kinds = nil
	}
	if len(q.Genres) == 0 {
		q.Genres = nil
	}
	if len(q.Keywords) == 0 {
		q.Keywords = nil
	}
	return q
}
