package mood

import (
	"strings"
	"testing"
)

func TestCatalogueAndHeuristic(t *testing.T) {
	if n := CatalogSize(); n < 9000 {
		t.Fatalf("catalogue has %d films", n)
	}
	// Interstellar: demanding, not funny.
	d, scored := For(157336, nil)
	if !scored || d[Complex] < .7 || d[Humour] > .2 {
		t.Fatalf("Interstellar = %v scored=%v", d, scored)
	}
	h, scored := For(0, []string{"Horror", "Science Fiction"})
	if scored || h[Scary] < .9 || h[Complex] != .65 {
		t.Fatalf("heuristic = %v", h)
	}
	if got := Heuristic(nil); got != basePrior {
		t.Fatalf("no genres = %v", got)
	}
}

func TestMoodsAndFlags(t *testing.T) {
	comedy := Heuristic([]string{"Comedy", "Family"})
	horror := Heuristic([]string{"Horror"})
	la, _ := Mood("la")
	if !la.Fits(comedy) || la.Fits(horror) {
		t.Fatal("Laugh should fit a family comedy and not a horror")
	}
	ns, _ := FlagByKey("noscares")
	if !ns.Test(comedy) || ns.Test(horror) {
		t.Fatal("No scares flag")
	}
	fg, _ := Mood("fg")
	if tg := fg.Target(); tg[V] != .81 || tg[Dark] != .2 {
		t.Fatalf("feel-good target = %v", tg)
	}
}

func films() []*Film {
	mk := func(id int64, title string, rating float64, votes int, genres ...string) *Film {
		return &Film{ID: id, Title: title, Rating: rating, Votes: votes, Genres: genres, D: Heuristic(genres)}
	}
	return []*Film{
		mk(1, "Laugh Riot", 7.5, 5000, "Comedy"),
		mk(2, "Laugh Riot 2", 7.4, 4000, "Comedy"),
		mk(3, "Night Terror", 7.6, 6000, "Horror"),
		mk(4, "Space Puzzle", 8.2, 9000, "Science Fiction", "Mystery"),
		mk(5, "Unknown Comedy", 9.8, 0, "Comedy", "Romance"),
	}
}

func TestRankMMRAndWhy(t *testing.T) {
	ix := NewIndex(films())
	la, _ := Mood("la")
	ctx := &Ctx{Target: la.Target(), MoodLabel: la.Label}
	r := ctx.Rank(ix.Films)
	if r[0].F.Genres[0] != "Comedy" || r[len(r)-1].F.ID != 3 {
		t.Fatalf("rank order: first %s, last %s", r[0].F.Title, r[len(r)-1].F.Title)
	}
	why := ctx.Why(r[0])
	if !strings.HasPrefix(why, "Fits laugh: ") || !strings.Contains(why, "funny (90%)") {
		t.Fatalf("why = %q", why)
	}
	// A 9.8 with no known votes must not outrank everything on quality alone.
	if f := ix.Get(5); f.WR > 9 {
		t.Fatalf("unknown-votes film weighted rating = %.2f", f.WR)
	}
	// Variety: with no target the two near-identical comedies don't both lead.
	top := MMR((&Ctx{}).Rank(ix.Films), 2, 0.5, 10)
	if len(top) != 2 || (top[0].F.Genres[0] == "Comedy" && top[1].F.Genres[0] == "Comedy" && len(top[1].F.Genres) == 1 && len(top[0].F.Genres) == 1) {
		t.Fatalf("MMR picked %s and %s", top[0].F.Title, top[1].F.Title)
	}
}

func TestRocchioLeansTowardsLikes(t *testing.T) {
	ix := NewIndex(films())
	p := ix.Rocchio([]Event{{ID: 3, W: 1}, {ID: 1, W: -1}})
	if p == nil {
		t.Fatal("no profile")
	}
	ctx := &Ctx{Profile: p}
	horror, comedy := ctx.relevance(ix.Get(3)), ctx.relevance(ix.Get(2))
	if horror.ps <= comedy.ps {
		t.Fatalf("liked horror %.2f should beat disliked comedy's twin %.2f", horror.ps, comedy.ps)
	}
	if ix.Rocchio(nil) != nil {
		t.Fatal("no feedback should mean no profile")
	}
}
