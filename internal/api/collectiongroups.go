package api

import (
	"regexp"
	"strings"
	"unicode"
)

// Collections are sorted into groups so a library with hundreds of them
// stays easy to browse on a TV: franchises first (the ones people look for),
// then studios, awards, best-of lists, decades, holidays, TV collections and
// everything else. The group comes from the collection's name, so it works
// for collections made by hand in Plex as well as ones made by tools such as
// Kometa.

const (
	groupContinue   = "Pick up where you left off"
	groupFranchises = "Franchises & series"
	groupStudios    = "Studios & networks"
	groupAwards     = "Award winners"
	groupCharts     = "Best of & charts"
	groupPeople     = "Directors & stars"
	groupDecades    = "By decade"
	groupHolidays   = "Holidays & seasons"
	groupGenres     = "Genres & moods"
	groupShows      = "TV collections"
	groupOther      = "More collections"
)

// shelfOrder is the order of the groups on the Collections page.
var shelfOrder = []string{groupFranchises, groupStudios, groupAwards, groupCharts, groupPeople,
	groupDecades, groupHolidays, groupGenres, groupShows, groupOther}

var (
	decadeRe = regexp.MustCompile(`\b(1[89]|20)\d0s\b|\b\d0s\b|\bdecade\b`)
	keywords = []struct {
		group string
		words []string
	}{
		{groupHolidays, []string{"christmas", "xmas", "halloween", "thanksgiving", "easter", "valentine", "holiday", "holidays",
			"new year", "hanukkah", "st patrick", "summer", "winter", "spooky"}},
		{groupAwards, []string{"oscar", "academy award", "golden globe", "bafta", "cannes", "emmy", "palme", "sundance",
			"razzie", "award", "awards", "nominee", "nominees", "winner", "winners", "berlinale", "venice", "spirit awards", "critics choice", "sag awards"}},
		{groupCharts, []string{"top", "best", "trending", "popular", "imdb", "tmdb",
			"rotten", "letterboxd", "most", "highest", "rated", "chart", "box office", "blockbuster", "acclaimed",
			"essential", "must watch", "must see", "classic", "classics", "afi", "1001", "favorite", "favorites", "favourite", "favourites", "recommended",
			"new release", "recently", "upcoming", "in theaters", "streaming", "seasonal"}},
		{groupPeople, []string{"directed by", "director", "directors", "starring", "actor", "actors", "actress", "actresses", "films of", "movies of",
			"filmography", "written by", "composer", "producer"}},
		{groupStudios, []string{"pixar", "disney", "marvel", "dc comics", "dc extended", "dceu", "dreamworks", "ghibli",
			"lucasfilm", "a24", "netflix", "hbo", "amazon", "apple tv", "hulu", "blumhouse", "illumination", "warner",
			"universal", "paramount", "lionsgate", "sony pictures", "columbia", "20th century", "mgm", "new line",
			"legendary", "laika", "aardman", "studio", "studios", "network", "networks", "bbc", "showtime", "starz", "fx", "amc", "nickelodeon",
			"cartoon network", "adult swim", "peacock", "max original", "criterion", "neon", "focus features", "searchlight"}},
		{groupGenres, []string{"action", "adventure", "animation", "anime", "comedy", "crime", "documentary", "drama",
			"family", "fantasy", "horror", "mystery", "romance", "sci fi", "science fiction", "thriller", "war",
			"western", "westerns", "musical", "musicals", "biography", "biopic", "biopics", "sport", "sports", "superhero", "superheroes", "zombie", "zombies", "vampire", "vampires", "heist", "heists", "noir", "slasher",
			"feel good", "mind bending", "cult", "kids", "teen", "stand up", "standup", "mood", "date night"}},
	}
	franchiseWords = []string{"collection", "saga", "trilogy", "series", "universe", "franchise", "anthology",
		"quadrilogy", "duology", "chronicles", "cinematic"}
)

// collectionGroup is the group for one collection; franchise says whether
// TMDB has a franchise of the same name.
func collectionGroup(c CollectionCard, franchise bool) string {
	if c.Source != "plex" || franchise {
		return groupFranchises // TMDB only knows franchises
	}
	name := " " + normalName(c.Name) + " "
	if decadeRe.MatchString(name) {
		return groupDecades
	}
	has := func(words []string) bool {
		for _, w := range words {
			if strings.Contains(name, " "+w+" ") {
				return true
			}
		}
		return false
	}
	for _, k := range keywords[:4] { // holidays, awards, charts, people
		if has(k.words) {
			return k.group
		}
	}
	if c.Kind == "movie" && has(franchiseWords) {
		return groupFranchises
	}
	for _, k := range keywords[4:] { // studios, genres
		if has(k.words) {
			return k.group
		}
	}
	if c.Kind == "show" {
		return groupShows
	}
	return groupOther
}

// normalName is a name in lower case with punctuation turned into spaces.
func normalName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// groupCollections sets each collection's group and drops TMDB franchises
// that Plex also has: Plex's copy (with Plex's name, poster and summary)
// is kept.
func groupCollections(cs []CollectionCard) []CollectionCard {
	plex, tmdb := map[string]bool{}, map[string]bool{}
	for _, c := range cs {
		if c.Source == "plex" && c.Kind == "movie" {
			plex[collectionWords(c.Name)] = true
		} else if c.Source != "plex" {
			tmdb[collectionWords(c.Name)] = true
		}
	}
	out := cs[:0]
	for _, c := range cs {
		w := collectionWords(c.Name)
		if c.Source != "plex" && plex[w] {
			continue
		}
		c.Group = collectionGroup(c, c.Source == "plex" && c.Kind == "movie" && tmdb[w])
		out = append(out, c)
	}
	return out
}

// CollectionShelf is one group of collections on the Collections page.
type CollectionShelf struct {
	Name        string           `json:"name"`
	Collections []CollectionCard `json:"collections"`
}

// collectionShelves arranges collections into groups in shelfOrder, with a
// first shelf of film collections the viewer is part-way through.
func collectionShelves(cs []CollectionCard) []CollectionShelf {
	by := map[string][]CollectionCard{}
	var started []CollectionCard
	for _, c := range cs {
		by[c.Group] = append(by[c.Group], c)
		if c.Kind == "movie" && c.Watched > 0 && c.Watched < c.Count {
			started = append(started, c)
		}
	}
	out := []CollectionShelf{}
	if len(started) > 0 {
		if len(started) > 30 {
			started = started[:30]
		}
		out = append(out, CollectionShelf{groupContinue, started})
	}
	for _, g := range shelfOrder {
		if len(by[g]) > 0 {
			out = append(out, CollectionShelf{g, by[g]})
		}
	}
	return out
}

// collectionWords is a collection name reduced for comparing: lower case,
// letters and digits only, without "the" or words like "collection".
func collectionWords(name string) string {
	var words []string
	for _, w := range strings.Fields(normalName(name)) {
		switch w {
		case "the", "collection", "collections", "saga", "series", "franchise", "trilogy", "films", "movies":
			continue
		}
		words = append(words, w)
	}
	return strings.Join(words, " ")
}
