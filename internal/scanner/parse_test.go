package scanner

import "testing"

func TestParseTitleYear(t *testing.T) {
	cases := []struct {
		in    string
		title string
		year  int
	}{
		{"The Matrix (1999)", "The Matrix", 1999},
		{"The.Matrix.1999.1080p.BluRay.x264-GROUP", "The Matrix", 1999},
		{"Blade Runner 2049 (2017)", "Blade Runner 2049", 2017},
		{"1917 (2019)", "1917", 2019},
		{"2001.A.Space.Odyssey.1968.2160p.UHD", "2001 A Space Odyssey", 1968},
		{"Inception [2010]", "Inception", 2010},
		{"Some Movie 1080p WEB-DL", "Some Movie", 0},
		{"Dune Part Two (2024) {tmdb-693134}", "Dune Part Two", 2024},
	}
	for _, c := range cases {
		title, year := ParseTitleYear(c.in)
		if title != c.title || year != c.year {
			t.Errorf("ParseTitleYear(%q) = %q, %d; want %q, %d", c.in, title, year, c.title, c.year)
		}
	}
}

func TestMovieInfo(t *testing.T) {
	title, year := MovieInfo("/m/Heat (1995)/heat.1995.remux.mkv", "/m")
	if title != "Heat" || year != 1995 {
		t.Errorf("got %q %d", title, year)
	}
	title, year = MovieInfo("/m/Alien.1979.Directors.Cut.1080p.mkv", "/m")
	if title != "Alien" || year != 1979 {
		t.Errorf("got %q %d", title, year)
	}
}

func TestEpisodeFromPath(t *testing.T) {
	cases := []struct {
		path       string
		show       string
		season, ep int
		ok         bool
	}{
		{"/tv/Breaking Bad (2008)/Season 01/Breaking.Bad.S01E02.720p.mkv", "Breaking Bad", 1, 2, true},
		{"/tv/The Office (US)/Season 3/The Office 3x05.mkv", "The Office (US)", 3, 5, true},
		{"/tv/Show/Season 02/05 - Episode Name.mkv", "Show", 2, 5, true},
		{"/tv/Show/Specials/Show - E01.mkv", "Show", 0, 1, true},
		{"/tv/Loose.Show.S04E10E11.mkv", "Loose Show", 4, 10, true},
		{"/tv/Show/random.mkv", "", 0, 0, false},
	}
	for _, c := range cases {
		info, ok := EpisodeFromPath(c.path, "/tv")
		if ok != c.ok {
			t.Errorf("%s: ok=%v want %v", c.path, ok, c.ok)
			continue
		}
		if ok && (info.ShowTitle != c.show || info.Season != c.season || info.Episode != c.ep) {
			t.Errorf("%s: got %q S%dE%d", c.path, info.ShowTitle, info.Season, info.Episode)
		}
	}
}

func TestIsVideo(t *testing.T) {
	for name, want := range map[string]bool{
		"movie.mkv": true, "movie-sample.mkv": false, "sample.mkv": false, "Samples Of Life.mkv": true,
		"movie.srt": false, ".hidden.mkv": false, "movie-trailer.mp4": false,
	} {
		if IsVideo(name) != want {
			t.Errorf("IsVideo(%q) != %v", name, want)
		}
	}
}
