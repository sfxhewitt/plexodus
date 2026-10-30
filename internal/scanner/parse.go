package scanner

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".wmv": true,
	".ts": true, ".m2ts": true, ".webm": true, ".mpg": true, ".mpeg": true, ".flv": true,
	".ogv": true, ".divx": true, ".3gp": true,
}

// IsVideo reports whether a filename looks like a playable video.
func IsVideo(name string) bool {
	if strings.HasPrefix(name, ".") {
		return false
	}
	ext := strings.ToLower(filepath.Ext(name))
	if !videoExts[ext] {
		return false
	}
	// Skip samples and extras that commonly sit next to the real file.
	return !reExtra.MatchString(strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name))))
}

var (
	reExtra      = regexp.MustCompile(`(^|[\.\-_ ])(sample|trailer|featurette|behindthescenes|deleted)([\.\-_ ]|$)`)
	reYearParen  = regexp.MustCompile(`^(.*?)[\s\.\_\-]*[\(\[]((?:19|20)\d{2})[\)\]]`)
	reYearBare   = regexp.MustCompile(`^(.*?)[\s\.\_\-\(\[]+((?:19|20)\d{2})(?:[\s\.\_\-\)\]]|$)`)
	reJunk       = regexp.MustCompile(`(?i)[\s\.\_\-\[\(](2160p|1080p|1080i|720p|576p|480p|4k|uhd|hdr|hdr10|dv|bluray|blu-ray|brrip|bdrip|webrip|web-dl|webdl|web|hdtv|dvdrip|dvd|remux|x264|x265|h264|h\.264|h265|h\.265|hevc|xvid|aac|ac3|dts|atmos|truehd|proper|repack|extended|unrated|remastered|imax|multi|internal)([\s\.\_\-\]\)]|$)`)
	reTags       = regexp.MustCompile(`\{[^}]*\}|\[[^\]]*\]`)
	reSxxEyy     = regexp.MustCompile(`(?i)(?:^|[^a-z0-9])s(\d{1,3})[\s\.\_\-]*e(\d{1,4})(?:[\s\.\_\-]*-?e(\d{1,4}))?`)
	reNxNN       = regexp.MustCompile(`(?i)(?:^|[^0-9])(\d{1,2})x(\d{2,3})(?:[^0-9]|$)`)
	reEpOnly     = regexp.MustCompile(`(?i)(?:^|[^a-z])(?:e|ep|episode)[\s\.\_\-]*(\d{1,4})(?:[^0-9]|$)`)
	reSeasonDir  = regexp.MustCompile(`(?i)^(?:season|series|s)[\s\.\_\-]*(\d{1,3})$`)
	reSpecials   = regexp.MustCompile(`(?i)^specials?$`)
	reSpaces     = regexp.MustCompile(`\s+`)
	reLeadNumber = regexp.MustCompile(`^(\d{1,3})[\s\.\_\-]`)
)

func cleanTitle(s string) string {
	s = reTags.ReplaceAllString(s, " ")
	// Dots and underscores are word separators in scene names, but keep
	// dots in abbreviations like "S.H.I.E.L.D." readable enough.
	if !strings.Contains(s, " ") {
		s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	} else {
		s = strings.ReplaceAll(s, "_", " ")
	}
	s = reSpaces.ReplaceAllString(s, " ")
	return strings.Trim(s, " -.([")
}

// cutJunk truncates a scene-style name at the first quality/source token.
func cutJunk(s string) string {
	if loc := reJunk.FindStringIndex(s); loc != nil && loc[0] > 0 {
		return s[:loc[0]]
	}
	return s
}

// ParseTitleYear extracts "Title" and year from names like
// "The.Matrix.1999.1080p.BluRay" or "The Matrix (1999)".
func ParseTitleYear(name string) (string, int) {
	name = reTags.ReplaceAllStringFunc(name, func(t string) string {
		// keep [1999]-style years, drop other bracketed tags
		if len(t) == 6 && (strings.HasPrefix(t, "[19") || strings.HasPrefix(t, "[20")) {
			return "(" + t[1:5] + ")"
		}
		return " "
	})
	if m := reYearParen.FindStringSubmatch(name); m != nil && strings.TrimSpace(m[1]) != "" {
		y, _ := strconv.Atoi(m[2])
		return cleanTitle(m[1]), y
	}
	// Bare year: take the last plausible year so titles like "2001 A Space
	// Odyssey 1968" or "1917 (2019)" work.
	matches := reYearBare.FindAllStringSubmatchIndex(name, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		pre := strings.TrimSpace(name[m[2]:m[3]])
		if pre == "" {
			continue
		}
		y, _ := strconv.Atoi(name[m[4]:m[5]])
		return cleanTitle(cutJunk(name[:m[4]])), y
	}
	return cleanTitle(cutJunk(name)), 0
}

// MovieInfo derives a movie's title/year from its file path, preferring a
// well-formed parent folder name ("Title (Year)") over the filename.
func MovieInfo(path, root string) (title string, year int) {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	dir := filepath.Base(filepath.Dir(path))
	if filepath.Clean(filepath.Dir(path)) != filepath.Clean(root) {
		if t, y := ParseTitleYear(dir); y != 0 && t != "" {
			return t, y
		}
	}
	t, y := ParseTitleYear(base)
	if t == "" {
		t = base
	}
	return t, y
}

type EpisodeInfo struct {
	ShowDir    string // path of the show's folder (first component under the root)
	ShowTitle  string
	ShowYear   int
	Season     int
	Episode    int
	EpisodeEnd int
}

// EpisodeFromPath parses a TV episode path laid out as
// root/Show Name (Year)/Season 01/Show.S01E02.mkv (season folder optional).
func EpisodeFromPath(path, root string) (EpisodeInfo, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return EpisodeInfo{}, false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	file := parts[len(parts)-1]
	base := strings.TrimSuffix(file, filepath.Ext(file))

	var info EpisodeInfo
	if len(parts) >= 2 {
		info.ShowDir = filepath.Join(root, parts[0])
		info.ShowTitle, info.ShowYear = ParseTitleYear(parts[0])
	}

	season := -1
	for i := 1; i < len(parts)-1; i++ {
		p := parts[i]
		if m := reSeasonDir.FindStringSubmatch(strings.TrimSpace(p)); m != nil {
			season, _ = strconv.Atoi(m[1])
		} else if reSpecials.MatchString(strings.TrimSpace(p)) {
			season = 0
		}
	}

	if m := reSxxEyy.FindStringSubmatch(base); m != nil {
		info.Season, _ = strconv.Atoi(m[1])
		info.Episode, _ = strconv.Atoi(m[2])
		if m[3] != "" {
			info.EpisodeEnd, _ = strconv.Atoi(m[3])
		}
		if info.ShowTitle == "" {
			idx := reSxxEyy.FindStringIndex(base)
			info.ShowTitle, info.ShowYear = ParseTitleYear(base[:idx[0]])
		}
	} else if m := reNxNN.FindStringSubmatch(base); m != nil {
		info.Season, _ = strconv.Atoi(m[1])
		info.Episode, _ = strconv.Atoi(m[2])
	} else if season >= 0 {
		info.Season = season
		if m := reEpOnly.FindStringSubmatch(base); m != nil {
			info.Episode, _ = strconv.Atoi(m[1])
		} else if m := reLeadNumber.FindStringSubmatch(base); m != nil {
			info.Episode, _ = strconv.Atoi(m[1])
		} else {
			return info, false
		}
	} else {
		return info, false
	}
	if info.ShowTitle == "" {
		return info, false
	}
	if info.ShowDir == "" {
		// Loose file directly in the root: group by parsed show title.
		info.ShowDir = filepath.Join(root, "\x00"+strings.ToLower(info.ShowTitle))
	}
	return info, true
}

// SortTitle strips leading articles so "The Matrix" sorts under M.
func SortTitle(t string) string {
	l := strings.ToLower(t)
	for _, a := range []string{"the ", "a ", "an "} {
		if strings.HasPrefix(l, a) && len(l) > len(a) {
			return l[len(a):]
		}
	}
	return l
}
