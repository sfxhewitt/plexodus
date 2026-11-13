package plex

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sfxhewitt/plexodus/internal/db"
)

// Collection sync brings every Plex collection over, the hand-made ones and
// the smart ones, for films and for shows, so the Collections tab has all of
// them and not just TMDB's franchises. Plexodus keeps its own copy (the
// collections and collection_items tables) so browsing never waits on Plex.

// PlexCollectionBase is added to a Plex collection's ratingKey to make its
// id in the collections table, clear of TMDB's collection ids (under 2M).
const PlexCollectionBase = 1_000_000_000_000

const collectionInterval = 6 * time.Hour

// PlexCollection is one collection as Plex lists it.
type PlexCollection struct {
	RatingKey  string          `json:"ratingKey"`
	Title      string          `json:"title"`
	TitleSort  string          `json:"titleSort"`
	Summary    string          `json:"summary"`
	Subtype    string          `json:"subtype"` // movie | show
	Thumb      string          `json:"thumb"`
	Art        string          `json:"art"`
	ChildCount int             `json:"childCount"`
	Smart      json.RawMessage `json:"smart"` // "1", 1 or true depending on the server
}

func (pc PlexCollection) IsSmart() bool {
	s := strings.Trim(string(pc.Smart), `"`)
	return s == "1" || s == "true"
}

// Collections lists a section's collections.
func (c *Client) Collections(ctx context.Context, section string) ([]PlexCollection, error) {
	var out struct {
		MediaContainer struct {
			Metadata []PlexCollection `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	err := c.get(ctx, "/library/sections/"+section+"/collections", nil, &out)
	return out.MediaContainer.Metadata, err
}

// CollectionItems lists what is in a collection, in the collection's order.
func (c *Client) CollectionItems(ctx context.Context, key string) ([]Metadata, error) {
	const size = 500
	var all []Metadata
	for start := 0; start < 50_000; start += size {
		var out struct {
			MediaContainer struct {
				Metadata []Metadata `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		q := url.Values{"X-Plex-Container-Start": {fmt.Sprint(start)}, "X-Plex-Container-Size": {fmt.Sprint(size)}}
		if err := c.get(ctx, "/library/collections/"+key+"/children", q, &out); err != nil {
			return all, err
		}
		all = append(all, out.MediaContainer.Metadata...)
		if len(out.MediaContainer.Metadata) < size {
			break
		}
	}
	return all, nil
}

// syncCollections runs at most every six hours, from the watch sync.
func (s *Syncer) syncCollections(ctx context.Context, c *Client, pm PathMap, force bool) error {
	now := time.Now()
	last, _ := strconv.ParseInt(s.db.Setting(ctx, "plex_collections_checked"), 10, 64)
	if !force && now.Unix()-last < int64(collectionInterval/time.Second) {
		return nil
	}
	n, err := s.SyncCollections(ctx, c, pm)
	if err != nil {
		return fmt.Errorf("collections: %w", err)
	}
	log.Printf("plex collection sync: %d collections in %s", n, time.Since(now).Round(time.Second))
	return s.db.SetSetting(ctx, "plex_collections_checked", strconv.FormatInt(now.Unix(), 10))
}

type collectionCopy struct {
	pc    PlexCollection
	kind  string
	items []int64
}

// SyncCollections copies every film and show collection from Plex and
// removes ones that are gone from Plex. It returns how many it copied.
func (s *Syncer) SyncCollections(ctx context.Context, c *Client, pm PathMap) (int, error) {
	sections, err := c.Sections(ctx)
	if err != nil {
		return 0, err
	}
	var copies []collectionCopy
	for _, sec := range sections {
		if sec.Type != "movie" && sec.Type != "show" {
			continue
		}
		list, err := c.Collections(ctx, sec.Key)
		if err != nil {
			return 0, err
		}
		for _, pc := range list {
			if _, err := strconv.ParseInt(pc.RatingKey, 10, 64); err != nil || strings.TrimSpace(pc.Title) == "" {
				continue
			}
			children, err := c.CollectionItems(ctx, pc.RatingKey)
			if err != nil {
				return 0, fmt.Errorf("%s: %w", pc.Title, err)
			}
			cc := collectionCopy{pc: pc, kind: sec.Type}
			seen := map[int64]bool{}
			for i := range children {
				ids, err := s.collectionMember(ctx, &children[i], pm)
				if err != nil {
					return 0, err
				}
				for _, id := range ids {
					if !seen[id] {
						seen[id] = true
						cc.items = append(cc.items, id)
					}
				}
			}
			copies = append(copies, cc)
		}
	}
	return len(copies), s.saveCollections(ctx, copies)
}

// collectionMember finds the Plexodus film or show for a collection entry:
// through the import's link, else the file path, else its TMDB id.
func (s *Syncer) collectionMember(ctx context.Context, m *Metadata, pm PathMap) ([]int64, error) {
	ids, _, err := s.lookupItems(ctx, m, pm)
	if err != nil || len(ids) > 0 {
		return ids, err
	}
	kind := m.Type
	if kind != "movie" && kind != "show" {
		return nil, nil
	}
	if t, err := strconv.ParseInt(m.ExternalID("tmdb"), 10, 64); err == nil && t > 0 {
		return readIDs(ctx, s.db.R, `SELECT id FROM items WHERE tmdb_id = ? AND kind = ? LIMIT 3`, t, kind)
	}
	return nil, nil
}

func (s *Syncer) saveCollections(ctx context.Context, copies []collectionCopy) error {
	tx, err := s.db.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := db.Now()
	keep := make([]int64, 0, len(copies))
	for _, cc := range copies {
		key, _ := strconv.ParseInt(cc.pc.RatingKey, 10, 64)
		id := PlexCollectionBase + key
		keep = append(keep, id)
		sortName := cc.pc.TitleSort
		if sortName == "" {
			sortName = cc.pc.Title
		}
		if _, err := tx.Exec(`INSERT INTO collections(id, name, sort_name, poster, backdrop, source, plex_key, kind, summary, smart, updated_at)
			VALUES(?, ?, ?, ?, ?, 'plex', ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET name = excluded.name, sort_name = excluded.sort_name,
				poster = CASE WHEN excluded.poster IS NOT NULL OR collections.poster LIKE 'plex:%' THEN excluded.poster ELSE collections.poster END,
				backdrop = CASE WHEN excluded.backdrop IS NOT NULL OR collections.backdrop LIKE 'plex:%' THEN excluded.backdrop ELSE collections.backdrop END,
				plex_key = excluded.plex_key, kind = excluded.kind, summary = excluded.summary, smart = excluded.smart,
				updated_at = excluded.updated_at`,
			id, strings.TrimSpace(cc.pc.Title), strings.ToLower(strings.TrimSpace(sortName)),
			ownArt(cc.pc.Thumb), ownArt(cc.pc.Art), cc.pc.RatingKey, cc.kind, nullStr(cc.pc.Summary), cc.pc.IsSmart(), now); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM collection_items WHERE collection_id = ?`, id); err != nil {
			return err
		}
		for i, item := range cc.items {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO collection_items(collection_id, item_id, position) VALUES(?, ?, ?)`, id, item, i); err != nil {
				return err
			}
		}
	}
	keepJSON, _ := json.Marshal(keep)
	if _, err := tx.Exec(`DELETE FROM collection_items WHERE collection_id IN
		(SELECT id FROM collections WHERE source = 'plex' AND id NOT IN (SELECT value FROM json_each(?)))`, string(keepJSON)); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM collections WHERE source = 'plex' AND id NOT IN (SELECT value FROM json_each(?))`, string(keepJSON)); err != nil {
		return err
	}
	return tx.Commit()
}

// ownArt is a collection's own poster or background in Plex. Plex makes a
// mosaic of the first few posters ("/composite/") for collections without
// one; those are left empty so Plexodus looks for real artwork on TMDB, and
// falls back to the first title's poster.
func ownArt(p string) any {
	if p == "" || strings.Contains(p, "/composite/") {
		return nil
	}
	return "plex:" + p
}
