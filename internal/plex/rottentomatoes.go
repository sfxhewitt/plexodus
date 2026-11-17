package plex

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strconv"
	"strings"
	"time"
)

// Rotten Tomatoes scores come from Plex. Its movie (and TV) agent fetches
// them when a library's "Ratings source" is Rotten Tomatoes (the default
// for movies), and says so with ratingImage/audienceRatingImage values like
// rottentomatoes://image.rating.ripe. Newer servers also list every source
// in a "Rating" array. Scores are 0–10 there and kept as 0–100 here.

const (
	rtDoneKey  = "plex_rt_at" // when the last full copy finished (unix seconds)
	rtInterval = 24 * time.Hour
)

// RTScores is a title's Tomatometer and audience score; -1 = unknown.
type RTScores struct{ Critic, Audience int }

func rtPct(v float64) int {
	if v <= 0 || v > 10 {
		return -1
	}
	return int(math.Round(v * 10))
}

func isRT(image string) bool { return strings.HasPrefix(image, "rottentomatoes://") }

// RT reads the Rotten Tomatoes scores Plex sent for an item, if any.
func (m *Metadata) RT() RTScores {
	out := RTScores{-1, -1}
	if isRT(m.RatingImage) {
		out.Critic = rtPct(m.Rating)
	}
	if isRT(m.AudienceRatingImage) {
		out.Audience = rtPct(m.AudienceRating)
	}
	for _, raw := range m.Ratings {
		var r struct {
			Image string  `json:"image"`
			Value float64 `json:"value"`
			Type  string  `json:"type"`
		}
		if json.Unmarshal(raw, &r) != nil || !isRT(r.Image) {
			continue
		}
		switch {
		case r.Type == "critic" && out.Critic < 0:
			out.Critic = rtPct(r.Value)
		case r.Type == "audience" && out.Audience < 0:
			out.Audience = rtPct(r.Value)
		}
	}
	return out
}

func (r RTScores) any() bool { return r.Critic >= 0 || r.Audience >= 0 }

func nullPct(v int) sql.NullInt64 { return sql.NullInt64{Int64: int64(v), Valid: v >= 0} }

// SetRT stores a title's scores, writing only when they changed. A score
// Plex no longer sends is kept: a blank listing is no reason to forget it.
func SetRT(ex interface {
	Exec(string, ...any) (sql.Result, error)
}, id int64, r RTScores) (bool, error) {
	if !r.any() {
		return false, nil
	}
	c, a := nullPct(r.Critic), nullPct(r.Audience)
	res, err := ex.Exec(`UPDATE items SET rt_critic = COALESCE(?1, rt_critic), rt_audience = COALESCE(?2, rt_audience)
		WHERE id = ?3 AND (rt_critic IS NOT COALESCE(?1, rt_critic) OR rt_audience IS NOT COALESCE(?2, rt_audience))`, c, a, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Syncer) rtDue(ctx context.Context) bool {
	last, _ := strconv.ParseInt(s.db.Setting(ctx, rtDoneKey), 10, 64)
	return time.Now().Unix()-last >= int64(rtInterval/time.Second)
}

// syncRT copies Rotten Tomatoes scores for every linked film and show, once
// a day. It reads one listing per library section in pages (no file access,
// no per-title requests) and writes only scores that changed, so after the
// first run it costs a few dozen Plex requests and almost no writes.
func (s *Syncer) syncRT(ctx context.Context, c *Client) error {
	sections, err := c.Sections(ctx)
	if err != nil {
		return err
	}
	var seen, scored, changed int
	for _, sec := range sections {
		typ := TypeMovie
		switch sec.Type {
		case "movie":
		case "show":
			typ = TypeShow
		default:
			continue
		}
		err := c.Each(ctx, sec.Key, typ, 500, func(ms []Metadata) error {
			type hit struct {
				key string
				rt  RTScores
			}
			var hits []hit
			for i := range ms {
				seen++
				if rt := ms[i].RT(); rt.any() {
					scored++
					hits = append(hits, hit{ms[i].RatingKey, rt})
				}
			}
			if len(hits) == 0 {
				return nil
			}
			tx, err := s.db.W.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			for _, h := range hits {
				ids, err := queryIDs(tx, `SELECT item_id FROM plex_links WHERE rating_key = ?`, h.key)
				if err != nil {
					return err
				}
				for _, id := range ids {
					ok, err := SetRT(tx, id, h.rt)
					if err != nil {
						return err
					}
					if ok {
						changed++
					}
				}
			}
			return tx.Commit()
		})
		if err != nil {
			return fmt.Errorf("%s: %w", sec.Title, err)
		}
	}
	log.Printf("plex rotten tomatoes: %d of %d titles in Plex have scores, %d updated", scored, seen, changed)
	if seen > 0 && scored == 0 {
		log.Printf("plex rotten tomatoes: none found; set a library's Ratings source to Rotten Tomatoes in Plex (Edit library > Advanced) to get them")
	}
	return s.db.SetSetting(ctx, rtDoneKey, strconv.FormatInt(time.Now().Unix(), 10))
}
