package plex

import (
	"context"
	"log"
)

// ratingsDoneKey marks that content ratings were copied from Plex for
// libraries imported before Plexodus stored them (needed by kids profiles).
const ratingsDoneKey = "plex_ratings_v1"

// BackfillRatings copies Plex's content ratings onto imported films and
// shows, once. It reads one listing per library section (no file access).
func (s *Syncer) BackfillRatings(ctx context.Context, c *Client) error {
	if s.db.Setting(ctx, ratingsDoneKey) == "1" {
		return nil
	}
	var linked int
	s.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM plex_links`).Scan(&linked)
	if linked == 0 {
		return s.db.SetSetting(ctx, ratingsDoneKey, "1")
	}
	sections, err := c.Sections(ctx)
	if err != nil {
		return err
	}
	n := 0
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
			tx, err := s.db.W.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer tx.Rollback()
			for _, m := range ms {
				if m.ContentRating == "" {
					continue
				}
				rows, err := tx.Query(`SELECT item_id FROM plex_links WHERE rating_key = ?`, m.RatingKey)
				if err != nil {
					return err
				}
				var ids []int64
				for rows.Next() {
					var id int64
					rows.Scan(&id)
					ids = append(ids, id)
				}
				rows.Close()
				for _, id := range ids {
					if err := SetContentRating(tx, id, m.ContentRating); err != nil {
						return err
					}
					n++
				}
			}
			return tx.Commit()
		})
		if err != nil {
			return err
		}
	}
	log.Printf("plex: copied content ratings for %d titles", n)
	return s.db.SetSetting(ctx, ratingsDoneKey, "1")
}
