package api

import (
	"context"
	"testing"

	"github.com/sfxhewitt/plexodus/internal/metadata"
)

func TestGuide(t *testing.T) {
	s, admin := testServer(t)
	if _, err := s.db.W.Exec(`INSERT INTO items(id, library_id, kind, guid, title, sort_title, tmdb_id, parent_id, season_number, episode_number, added_at, updated_at) VALUES
		(500, 1, 'show', 'g500', 'A Show', 'a show', 77, NULL, NULL, NULL, 0, 0),
		(501, 1, 'season', 'g501', 'Season 1', 'season 1', NULL, 500, 1, NULL, 0, 0),
		(511, 1, 'episode', 'g511', 'One', 'one', NULL, 501, 1, 1, 0, 0),
		(512, 1, 'episode', 'g512', 'Two', 'two', NULL, 501, 1, 2, 0, 0)`); err != nil {
		t.Fatal(err)
	}
	calls := 0
	old := fetchGuide
	t.Cleanup(func() { fetchGuide = old })
	fetchGuide = func(_ *Server, _ context.Context, id int64) (*metadata.Guide, error) {
		calls++
		ep := func(s, e int, d string) metadata.GuideEpisode { return metadata.GuideEpisode{Season: s, Episode: e, AirDate: d} }
		return &metadata.Guide{TMDB: id, Status: "Returning Series", Seasons: []metadata.GuideSeason{
			{Season: 0, Name: "Specials", Episodes: []metadata.GuideEpisode{ep(0, 1, "2020-01-01")}},
			{Season: 1, Name: "Season 1", Episodes: []metadata.GuideEpisode{ep(1, 1, "2020-01-01"), ep(1, 2, "2020-01-08"), ep(1, 3, "2020-01-15")}},
			{Season: 2, Name: "Season 2", Episodes: []metadata.GuideEpisode{ep(2, 1, "2099-03-01"), ep(2, 2, "")}},
		}}, nil
	}

	var out GuideResponse
	if code := call(t, s, admin, s.handleGuide, "GET", "/api/items/500/guide", "", map[string]string{"id": "500"}, &out); code != 200 {
		t.Fatal(code)
	}
	if !out.Available || out.Total != 5 || out.Aired != 3 || out.Have != 2 {
		t.Fatalf("totals: %+v", out)
	}
	if len(out.Seasons) != 2 || out.Seasons[0].State != "partial" || out.Seasons[0].ItemID != 501 || out.Seasons[1].State != "upcoming" || out.Seasons[1].ItemID != 0 {
		t.Fatalf("seasons (no specials when you have none): %+v", out.Seasons)
	}
	if out.Next == nil || out.Next.Season != 2 || out.Next.Episode != 1 || out.Seasons[1].NextAirDate != "2099-03-01" {
		t.Fatalf("next: %+v", out.Next)
	}
	if len(out.Episodes) != 0 {
		t.Fatal("episodes only when a season is asked for")
	}

	// A season's own page: its episodes, two in the library and one missing.
	out = GuideResponse{}
	call(t, s, admin, s.handleGuide, "GET", "/api/items/501/guide", "", map[string]string{"id": "501"}, &out)
	if len(out.Episodes) != 3 || out.Episodes[0].ItemID != 511 || out.Episodes[2].State != "missing" || out.Episodes[2].ItemID != 0 {
		t.Fatalf("season episodes: %+v", out.Episodes)
	}
	// A season you have none of, from the show page.
	out = GuideResponse{}
	call(t, s, admin, s.handleGuide, "GET", "/api/items/500/guide?season=2", "", map[string]string{"id": "500"}, &out)
	if len(out.Episodes) != 2 || out.Episodes[1].State != "upcoming" {
		t.Fatalf("missing season: %+v", out.Episodes)
	}
	if calls != 1 {
		t.Fatalf("TMDB asked %d times, want once (cached)", calls)
	}
	// Cached in SQLite across restarts.
	guides.mu.Lock()
	guides.mem = map[int64]guideEntry{}
	guides.mu.Unlock()
	if _, err := s.guide(context.Background(), 77); err != nil || calls != 1 {
		t.Fatalf("from sqlite: %v, calls %d", err, calls)
	}
}

func TestGuideNumberedDifferently(t *testing.T) {
	ls := &localSeason{id: 1, episodes: map[int]int64{}}
	for n := 100; n < 120; n++ {
		ls.episodes[n] = int64(n)
	}
	g := &metadata.Guide{Seasons: []metadata.GuideSeason{{Season: 1, Episodes: []metadata.GuideEpisode{{Season: 1, Episode: 1, AirDate: "2000-01-01"}}}}}
	var out GuideResponse
	buildGuide(&out, g, map[int]*localSeason{1: ls}, 1, "2026-01-01")
	if out.Seasons[0].Total != 0 || out.Seasons[0].Have != 20 || out.Seasons[0].State != "have" || len(out.Episodes) != 0 {
		t.Fatalf("absolute numbering shouldn't call anything missing: %+v", out)
	}
}
