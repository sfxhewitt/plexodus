package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sfxhewitt/plexodus/internal/plex"
)

// Share to someone's Plex Watchlist.
//
// An admin links other people's plex.tv accounts once (Settings > Share to
// Plex: a plex.tv/link code they, or the admin with their login, approve).
// Then Share in the player, or on a film or show, puts that title on their
// Plex Watchlist, which every Plex app shows. Their plex.tv token stays in
// the server's database and never leaves it.

type shareTarget struct {
	ID       string `json:"id"`
	Name     string `json:"name"`     // what the Share button says ("Dad")
	PlexUser string `json:"plexUser"` // their plex.tv username, for Settings
	Token    string `json:"token,omitempty"`
	AddedAt  int64  `json:"addedAt"`
}

const shareTargetsKey = "share_targets"

var shareMu sync.Mutex

func (s *Server) shareTargets(ctx context.Context) []shareTarget {
	var ts []shareTarget
	json.Unmarshal([]byte(s.db.Setting(ctx, shareTargetsKey)), &ts)
	return ts
}

func (s *Server) saveShareTargets(ctx context.Context, ts []shareTarget) error {
	if ts == nil {
		ts = []shareTarget{}
	}
	b, _ := json.Marshal(ts)
	return s.db.SetSetting(ctx, shareTargetsKey, string(b))
}

// publicTargets drops the tokens.
func publicTargets(ts []shareTarget) []shareTarget {
	out := make([]shareTarget, 0, len(ts))
	for _, t := range ts {
		t.Token = ""
		out = append(out, t)
	}
	return out
}

// handleShareTargets lists who titles can be shared to. Only admins share
// (the links are the admin's), so everyone else gets an empty list and the
// apps hide the button.
func (s *Server) handleShareTargets(w http.ResponseWriter, r *http.Request) {
	if !currentUser(r).IsAdmin {
		writeJSON(w, map[string]any{"targets": []shareTarget{}})
		return
	}
	writeJSON(w, map[string]any{"targets": publicTargets(s.shareTargets(r.Context()))})
}

// ---- linking someone's Plex account ----

var (
	sharePinsMu sync.Mutex
	sharePins   = map[int64]sharePin{}
)

type sharePin struct {
	name    string
	created time.Time
}

// handleShareLinkStart makes a plex.tv/link code for the person to approve.
func (s *Server) handleShareLinkStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	readJSON(r, &body)
	body.Name = strings.TrimSpace(body.Name)
	if len(body.Name) > 40 {
		body.Name = body.Name[:40]
	}
	acct := s.plexAccount(r.Context())
	p, err := acct.NewPin(r.Context(), false)
	if err != nil {
		writeErr(w, 502, "couldn't reach plex.tv: "+err.Error())
		return
	}
	sharePinsMu.Lock()
	for id, sp := range sharePins {
		if time.Since(sp.created) > 30*time.Minute {
			delete(sharePins, id)
		}
	}
	sharePins[p.ID] = sharePin{name: body.Name, created: time.Now()}
	sharePinsMu.Unlock()
	writeJSON(w, map[string]any{"id": p.ID, "code": p.Code, "linkUrl": "https://plex.tv/link", "expiresIn": p.ExpiresIn})
}

// handleShareLinkCheck is polled until the code is approved, then saves the person.
func (s *Server) handleShareLinkCheck(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	sharePinsMu.Lock()
	sp, ok := sharePins[id]
	sharePinsMu.Unlock()
	if !ok {
		writeErr(w, 404, "that code has expired, start again")
		return
	}
	acct := s.plexAccount(r.Context())
	p, err := acct.CheckPin(r.Context(), id)
	if err != nil {
		writeErr(w, 502, "couldn't reach plex.tv: "+err.Error())
		return
	}
	if p.AuthToken == "" {
		writeJSON(w, map[string]any{"done": false})
		return
	}
	me, err := acct.Me(r.Context(), p.AuthToken)
	if err != nil {
		writeErr(w, 502, "plex.tv didn't say whose account that is: "+err.Error())
		return
	}
	sharePinsMu.Lock()
	delete(sharePins, id)
	sharePinsMu.Unlock()

	name := sp.name
	if name == "" {
		name = me.Name()
	}
	shareMu.Lock()
	defer shareMu.Unlock()
	ts := s.shareTargets(r.Context())
	t := shareTarget{ID: fmt.Sprint(me.ID), Name: name, PlexUser: me.Name(), Token: p.AuthToken, AddedAt: time.Now().Unix()}
	replaced := false
	for i := range ts {
		if ts[i].ID == t.ID {
			ts[i] = t
			replaced = true
		}
	}
	if !replaced {
		ts = append(ts, t)
	}
	if err := s.saveShareTargets(r.Context(), ts); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	log.Printf("share: linked %s's Plex account (%s)", name, me.Name())
	t.Token = ""
	writeJSON(w, map[string]any{"done": true, "target": t})
}

func (s *Server) handleShareTargetUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Name) == "" {
		writeErr(w, 400, "name required")
		return
	}
	shareMu.Lock()
	defer shareMu.Unlock()
	ts := s.shareTargets(r.Context())
	for i := range ts {
		if ts[i].ID == r.PathValue("id") {
			ts[i].Name = strings.TrimSpace(body.Name)
			if err := s.saveShareTargets(r.Context(), ts); err != nil {
				writeErr(w, 500, err.Error())
				return
			}
			writeJSON(w, map[string]any{"targets": publicTargets(ts)})
			return
		}
	}
	writeErr(w, 404, "not found")
}

func (s *Server) handleShareTargetDelete(w http.ResponseWriter, r *http.Request) {
	shareMu.Lock()
	defer shareMu.Unlock()
	ts := s.shareTargets(r.Context())
	out := ts[:0]
	for _, t := range ts {
		if t.ID != r.PathValue("id") {
			out = append(out, t)
		}
	}
	if err := s.saveShareTargets(r.Context(), out); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"targets": publicTargets(out)})
}

// ---- sharing ----

// shareTitle is the film or show to share.
type shareTitle struct {
	ID     int64
	Kind   string // movie | show
	Title  string
	Year   int
	TMDB   int64
	IMDB   string
	Plex   string // plex_links rating key of the film or show
	EpPlex string // rating key of the episode/season shared from, if any
}

// shareTitleFor finds the film, or the show an episode or season belongs to.
func (s *Server) shareTitleFor(ctx context.Context, itemID int64) (shareTitle, error) {
	var t shareTitle
	var epPlex string
	for i := 0; i < 3; i++ {
		var parent sql.NullInt64
		var year, tmdb sql.NullInt64
		var imdb, plexKey sql.NullString
		err := s.db.R.QueryRowContext(ctx, `
			SELECT i.kind, i.title, i.year, i.tmdb_id, i.imdb_id, i.parent_id, pl.rating_key
			FROM items i LEFT JOIN plex_links pl ON pl.item_id = i.id WHERE i.id = ?`, itemID).
			Scan(&t.Kind, &t.Title, &year, &tmdb, &imdb, &parent, &plexKey)
		if err != nil {
			return t, err
		}
		if t.Kind == "movie" || t.Kind == "show" {
			t.ID, t.Year, t.TMDB, t.IMDB, t.Plex, t.EpPlex = itemID, int(year.Int64), tmdb.Int64, imdb.String, plexKey.String, epPlex
			return t, nil
		}
		if epPlex == "" {
			epPlex = plexKey.String
		}
		if !parent.Valid {
			break
		}
		itemID = parent.Int64
	}
	return t, sql.ErrNoRows
}

// discoverKeyFor finds the title in Plex's catalogue: through the owner's
// own Plex server when the title came from it (exact), else by searching.
func (s *Server) discoverKeyFor(ctx context.Context, t shareTitle, token string) (string, error) {
	if c, _ := s.plexClient(ctx); c.Configured() {
		if t.Plex != "" {
			if m, err := c.Item(ctx, t.Plex); err == nil {
				if k := plex.DiscoverKey(m.PlexGUID); k != "" {
					return k, nil
				}
			}
		} else if t.EpPlex != "" && t.Kind == "show" {
			if ep, err := c.Item(ctx, t.EpPlex); err == nil {
				show := ep.GrandparentRatingKey
				if ep.Type == "season" {
					show = ep.ParentRatingKey
				}
				if show != "" {
					if m, err := c.Item(ctx, show); err == nil {
						if k := plex.DiscoverKey(m.PlexGUID); k != "" {
							return k, nil
						}
					}
				}
			}
		}
	}
	acct := s.plexAccount(ctx)
	res, err := acct.SearchDiscover(ctx, token, t.Title, t.Kind)
	if err != nil {
		return "", err
	}
	if m, ok := plex.BestMatch(res, t.Kind, t.Title, t.Year, t.TMDB, t.IMDB); ok {
		return m.Key, nil
	}
	return "", plex.ErrNotOnPlex
}

// handleShareWatchlist puts the film or show (of an item or a playing
// file) on a linked person's Plex Watchlist.
func (s *Server) handleShareWatchlist(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Target string `json:"target"`
		ItemID int64  `json:"itemId"`
		FileID int64  `json:"fileId"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, "bad request")
		return
	}
	ctx := r.Context()
	if body.ItemID == 0 && body.FileID > 0 {
		s.db.R.QueryRowContext(ctx, `SELECT item_id FROM media_files WHERE id = ?`, body.FileID).Scan(&body.ItemID)
	}
	if body.ItemID == 0 {
		writeErr(w, 404, "nothing to share")
		return
	}
	var target shareTarget
	for _, t := range s.shareTargets(ctx) {
		if t.ID == body.Target || (body.Target == "" && target.ID == "") {
			target = t
		}
	}
	if target.ID == "" {
		writeErr(w, 404, "nobody to share with yet: link their Plex account in Settings")
		return
	}
	t, err := s.shareTitleFor(ctx, body.ItemID)
	if err != nil {
		writeErr(w, 404, "couldn't find that title")
		return
	}
	key, err := s.discoverKeyFor(ctx, t, target.Token)
	if err == nil {
		var already bool
		already, err = s.plexAccount(ctx).AddToWatchlist(ctx, target.Token, key)
		if err == nil {
			log.Printf("share: %s -> %s's Plex watchlist (already=%v)", t.Title, target.Name, already)
			writeJSON(w, map[string]any{"ok": true, "already": already, "title": t.Title, "kind": t.Kind, "name": target.Name})
			return
		}
	}
	log.Printf("share: %s -> %s failed: %v", t.Title, target.Name, err)
	switch {
	case errors.Is(err, plex.ErrBadToken):
		writeErr(w, 409, target.Name+"'s Plex sign-in has expired. Link their account again in Settings.")
	case errors.Is(err, plex.ErrNotOnPlex):
		writeErr(w, 404, "Couldn't find "+t.Title+" on Plex.")
	default:
		writeErr(w, 502, "Plex didn't take it: "+err.Error())
	}
}
