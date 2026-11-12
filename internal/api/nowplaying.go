package api

import (
	"context"
	"database/sql"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Now watching: an in-memory view of who is playing what, for the admin
// dashboard. Nothing here touches the disk or the media mount; it's fed by
// the requests players already make (progress reports, direct-play byte
// ranges, remux sessions) and forgotten on restart.

const (
	// npPlayingFor is how long after its last sign of life a stream still
	// counts as playing (players report every 10 s while playing).
	npPlayingFor = 45 * time.Second
	// npPausedFor keeps a stream the player said was paused; npIdleFor drops
	// one that just went quiet (an older app that never says "stopped").
	npPausedFor = 30 * time.Minute
	npIdleFor   = 3 * time.Minute
	// npQuietAfterStop ignores stray byte ranges a player fetches as it
	// closes, so a stopped stream doesn't come back as "paused".
	npQuietAfterStop = 30 * time.Second
)

type npKey struct{ viewer, item int64 }

type npSession struct {
	viewer, item, file int64
	ip, ua             string
	uaRank             int
	started, lastSeen  time.Time
	lastProgress       time.Time
	position, duration float64
	state              string // what the player last said: playing, paused or ""
	remux              bool
	inflight           int
	bytes              int64
}

type nowPlaying struct {
	mu       sync.Mutex
	m        map[npKey]*npSession
	stopped  map[npKey]time.Time
	fileItem map[int64]int64 // remux sessions name files, the dashboard wants items
}

func newNowPlaying() *nowPlaying {
	return &nowPlaying{m: map[npKey]*npSession{}, stopped: map[npKey]time.Time{}, fileItem: map[int64]int64{}}
}

// session returns the stream for a key, creating it unless create is false
// or the player stopped it a moment ago. Callers hold mu.
func (n *nowPlaying) session(k npKey, r *http.Request, create bool, now time.Time) *npSession {
	s := n.m[k]
	if s == nil {
		if !create {
			return nil
		}
		if at, ok := n.stopped[k]; ok && now.Sub(at) < npQuietAfterStop {
			return nil
		}
		s = &npSession{viewer: k.viewer, item: k.item, started: now}
		n.m[k] = s
	}
	s.lastSeen = now
	if r != nil {
		if ip := clientIP(r); ip != "" {
			s.ip = ip
		}
		ua := r.UserAgent()
		if rank := uaRank(ua); rank >= s.uaRank && ua != "" {
			s.ua, s.uaRank = ua, rank
		}
	}
	return s
}

// progress records a player's progress report. state is "playing",
// "paused", "stopped" or "" (older players don't say).
func (n *nowPlaying) progress(r *http.Request, viewer, item int64, pos, dur float64, state string) {
	if n == nil {
		return
	}
	now := time.Now()
	k := npKey{viewer, item}
	n.mu.Lock()
	defer n.mu.Unlock()
	if state == "stopped" {
		delete(n.m, k)
		n.stopped[k] = now
		return
	}
	delete(n.stopped, k) // playing again
	s := n.session(k, r, true, now)
	s.position, s.duration, s.lastProgress = pos, dur, now
	s.state = state
}

// direct notes a direct-play byte range starting; call the returned func
// when it ends, with the bytes sent.
func (n *nowPlaying) direct(r *http.Request, viewer, item, file int64) func(int64) {
	if n == nil {
		return func(int64) {}
	}
	k := npKey{viewer, item}
	n.mu.Lock()
	s := n.session(k, r, true, time.Now())
	if s != nil {
		s.file = file
		s.remux = false
		s.inflight++
	}
	n.mu.Unlock()
	return func(sent int64) {
		if s == nil {
			return
		}
		n.mu.Lock()
		s.inflight--
		s.bytes += sent
		s.lastSeen = time.Now()
		n.mu.Unlock()
	}
}

// remuxStart notes a browser remux (HLS) session for a file.
func (n *nowPlaying) remuxStart(r *http.Request, viewer, item, file int64) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	n.fileItem[file] = item
	if s := n.session(npKey{viewer, item}, r, true, time.Now()); s != nil {
		s.file, s.remux = file, true
	}
}

// remuxTouch keeps a remux stream alive as the browser fetches segments.
func (n *nowPlaying) remuxTouch(r *http.Request, viewer, file int64, bytes int64) {
	if n == nil {
		return
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	item, ok := n.fileItem[file]
	if !ok {
		return
	}
	if s := n.session(npKey{viewer, item}, r, false, time.Now()); s != nil {
		s.remux = true
		s.bytes += bytes
	}
}

// npRaw is a copy of one stream, taken under the lock.
type npRaw struct {
	npSession
	playing bool
}

func (n *nowPlaying) snapshot(now time.Time) []npRaw {
	n.mu.Lock()
	defer n.mu.Unlock()
	for k, at := range n.stopped {
		if now.Sub(at) > npQuietAfterStop {
			delete(n.stopped, k)
		}
	}
	out := []npRaw{}
	for k, s := range n.m {
		idle := now.Sub(s.lastSeen)
		keep := npIdleFor
		if s.state == "paused" {
			keep = npPausedFor
		}
		if s.inflight == 0 && idle > keep {
			delete(n.m, k)
			continue
		}
		playing := s.state != "paused" && (s.inflight > 0 || idle < npPlayingFor)
		out = append(out, npRaw{npSession: *s, playing: playing})
	}
	return out
}

// NowPlaying is one stream on the dashboard.
type NowPlaying struct {
	Key         string  `json:"key"`
	UserID      int64   `json:"userId"`
	Username    string  `json:"username"`
	Profile     string  `json:"profile,omitempty"`
	ItemID      int64   `json:"itemId"`
	Kind        string  `json:"kind"`
	Title       string  `json:"title"`
	Year        int     `json:"year,omitempty"`
	ShowTitle   string  `json:"showTitle,omitempty"`
	Season      *int    `json:"seasonNumber,omitempty"`
	Episode     *int    `json:"episodeNumber,omitempty"`
	Poster      string  `json:"poster,omitempty"`
	Backdrop    string  `json:"backdrop,omitempty"`
	Library     string  `json:"library,omitempty"`
	State       string  `json:"state"` // playing or paused
	Position    float64 `json:"position"`
	Duration    float64 `json:"duration"`
	Method      string  `json:"method"` // "direct" or "remux"
	Device      string  `json:"device"`
	App         string  `json:"app"`
	IP          string  `json:"ip"`
	Local       bool    `json:"local"`
	StartedAt   int64   `json:"startedAt"`
	Container   string  `json:"container,omitempty"`
	VideoCodec  string  `json:"videoCodec,omitempty"`
	AudioCodec  string  `json:"audioCodec,omitempty"`
	Width       int     `json:"width,omitempty"`
	Height      int     `json:"height,omitempty"`
	Bitrate     int64   `json:"bitrate,omitempty"`
	BytesServed int64   `json:"bytesServed"`
}

// RecentPlay is a finished play, newest first.
type RecentPlay struct {
	Username  string `json:"username"`
	Profile   string `json:"profile,omitempty"`
	ItemID    int64  `json:"itemId"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	ShowTitle string `json:"showTitle,omitempty"`
	Season    *int   `json:"seasonNumber,omitempty"`
	Episode   *int   `json:"episodeNumber,omitempty"`
	Poster    string `json:"poster,omitempty"`
	At        int64  `json:"at"`
	Source    string `json:"source"`
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	streams := []NowPlaying{}
	for _, raw := range s.nowPlaying.snapshot(now) {
		np, ok := s.describeStream(r.Context(), raw, now)
		if ok {
			streams = append(streams, np)
		}
	}
	sort.Slice(streams, func(i, j int) bool {
		if streams[i].StartedAt != streams[j].StartedAt {
			return streams[i].StartedAt > streams[j].StartedAt
		}
		return streams[i].Key < streams[j].Key
	})
	out := map[string]any{"streams": streams}
	if r.URL.Query().Get("recent") != "0" {
		recent, err := s.recentPlays(r.Context(), 30)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		out["recent"] = recent
	}
	writeJSON(w, out)
}

func (s *Server) describeStream(ctx context.Context, raw npRaw, now time.Time) (NowPlaying, bool) {
	np := NowPlaying{
		Key: strconv.FormatInt(raw.viewer, 10) + "-" + strconv.FormatInt(raw.item, 10), UserID: raw.viewer, ItemID: raw.item,
		IP: raw.ip, Local: isLocalIP(raw.ip), StartedAt: raw.started.Unix(), BytesServed: raw.bytes,
		Duration: raw.duration, Method: "direct",
	}
	if raw.remux {
		np.Method = "remux"
	}
	np.State = "paused"
	np.Position = raw.position
	if raw.playing {
		np.State = "playing"
		if !raw.lastProgress.IsZero() {
			np.Position += now.Sub(raw.lastProgress).Seconds()
		}
	}
	np.Device, np.App = describeClient(raw.ua)

	u, err := s.auth.ByID(ctx, raw.viewer)
	if err != nil {
		return np, false
	}
	np.Username = u.Username
	if u.IsProfile && u.Name != "" && !strings.EqualFold(u.Name, u.Username) {
		np.Profile = u.Name
	}

	var sn, en sql.NullInt64
	err = s.db.R.QueryRowContext(ctx, `SELECT i.kind, i.title, COALESCE(i.year, 0), i.season_number, i.episode_number,
		COALESCE(sh.title, ''), COALESCE(NULLIF(i.poster, ''), sh.poster, ''), COALESCE(NULLIF(i.backdrop, ''), sh.backdrop, ''),
		COALESCE(l.name, '')
		FROM items i
		LEFT JOIN items se ON i.kind = 'episode' AND se.id = i.parent_id
		LEFT JOIN items sh ON sh.id = se.parent_id
		LEFT JOIN libraries l ON l.id = i.library_id
		WHERE i.id = ?`, raw.item).
		Scan(&np.Kind, &np.Title, &np.Year, &sn, &en, &np.ShowTitle, &np.Poster, &np.Backdrop, &np.Library)
	if err != nil {
		return np, false
	}
	np.Season, np.Episode = nullInt(sn), nullInt(en)

	// The file being played, else the item's first file.
	q := `SELECT COALESCE(container, ''), COALESCE(video_codec, ''), COALESCE(audio_codec, ''), COALESCE(width, 0),
		COALESCE(height, 0), COALESCE(bitrate, 0), COALESCE(duration, 0) FROM media_files WHERE `
	var fileDur float64
	if raw.file != 0 {
		err = s.db.R.QueryRowContext(ctx, q+`id = ?`, raw.file).
			Scan(&np.Container, &np.VideoCodec, &np.AudioCodec, &np.Width, &np.Height, &np.Bitrate, &fileDur)
	} else {
		err = s.db.R.QueryRowContext(ctx, q+`item_id = ? ORDER BY id LIMIT 1`, raw.item).
			Scan(&np.Container, &np.VideoCodec, &np.AudioCodec, &np.Width, &np.Height, &np.Bitrate, &fileDur)
	}
	if np.Duration <= 0 {
		np.Duration = fileDur
	}
	if np.Position <= 0 && raw.lastProgress.IsZero() {
		// Only byte ranges so far: show where they resumed from.
		s.db.R.QueryRowContext(ctx, `SELECT position FROM watch_state WHERE user_id = ? AND item_id = ?`, raw.viewer, raw.item).Scan(&np.Position)
	}
	if np.Duration > 0 && np.Position > np.Duration {
		np.Position = np.Duration
	}
	return np, true
}

func (s *Server) recentPlays(ctx context.Context, limit int) ([]RecentPlay, error) {
	rows, err := s.db.R.QueryContext(ctx, `SELECT COALESCE(acct.username, u.username), CASE WHEN u.parent_id IS NOT NULL THEN COALESCE(u.display_name, u.username) ELSE '' END,
		i.id, i.kind, i.title, i.season_number, i.episode_number, COALESCE(sh.title, ''),
		COALESCE(NULLIF(i.poster, ''), sh.poster, ''), h.watched_at, h.source
		FROM watch_history h
		JOIN users u ON u.id = h.user_id
		LEFT JOIN users acct ON acct.id = u.parent_id
		JOIN items i ON i.id = h.item_id
		LEFT JOIN items se ON i.kind = 'episode' AND se.id = i.parent_id
		LEFT JOIN items sh ON sh.id = se.parent_id
		ORDER BY h.watched_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RecentPlay{}
	for rows.Next() {
		var p RecentPlay
		var sn, en sql.NullInt64
		if err := rows.Scan(&p.Username, &p.Profile, &p.ItemID, &p.Kind, &p.Title, &sn, &en, &p.ShowTitle, &p.Poster, &p.At, &p.Source); err != nil {
			return nil, err
		}
		p.Season, p.Episode = nullInt(sn), nullInt(en)
		out = append(out, p)
	}
	return out, rows.Err()
}

func nullInt(n sql.NullInt64) *int {
	if !n.Valid {
		return nil
	}
	v := int(n.Int64)
	return &v
}

// ---- telling devices apart from their User-Agent ----

// uaRank prefers the most telling User-Agent seen for a stream: the
// Android app's own HTTP client names the device model, libVLC doesn't.
func uaRank(ua string) int {
	switch {
	case ua == "":
		return 0
	case strings.Contains(ua, "LibVLC") || strings.HasPrefix(ua, "VLC/"):
		return 1
	case strings.HasPrefix(ua, "Dalvik/") || strings.Contains(ua, "Plexodus/"):
		return 3
	default:
		return 2
	}
}

var androidModel = regexp.MustCompile(`Android [\d.]+; (?:[a-z]{2}[-_][A-Za-z]{2}; )?([^;)]+?)(?: Build/|\)|;)`)

// describeClient turns a User-Agent into a device and an app name.
func describeClient(ua string) (device, app string) {
	switch {
	case ua == "":
		return "Unknown device", "Unknown player"
	case strings.Contains(ua, "LibVLC") || strings.HasPrefix(ua, "VLC/"):
		return "Android device", "Plexodus app"
	}
	if strings.HasPrefix(ua, "Dalvik/") || strings.Contains(ua, "Plexodus/") || (strings.Contains(ua, "Android") && strings.Contains(ua, "; wv)")) {
		app = "Plexodus app"
		device = "Android device"
		if strings.Contains(ua, "Android TV") || strings.Contains(ua, "AFT") {
			device = "Android TV"
		}
		if m := androidModel.FindStringSubmatch(ua); m != nil {
			device = friendlyModel(strings.TrimSpace(m[1]), device)
		}
		return device, app
	}
	switch {
	case strings.Contains(ua, "iPhone"):
		device = "iPhone"
	case strings.Contains(ua, "iPad"):
		device = "iPad"
	case strings.Contains(ua, "Android"):
		device = "Android phone"
		if m := androidModel.FindStringSubmatch(ua); m != nil && m[1] != "K" {
			device = friendlyModel(strings.TrimSpace(m[1]), device)
		}
	case strings.Contains(ua, "CrOS"):
		device = "Chromebook"
	case strings.Contains(ua, "Macintosh"):
		device = "Mac"
	case strings.Contains(ua, "Windows"):
		device = "Windows PC"
	case strings.Contains(ua, "Linux"):
		device = "Linux PC"
	default:
		device = "Web browser"
	}
	switch {
	case strings.Contains(ua, "Edg/"):
		app = "Edge"
	case strings.Contains(ua, "OPR/"):
		app = "Opera"
	case strings.Contains(ua, "Firefox/") || strings.Contains(ua, "FxiOS/"):
		app = "Firefox"
	case strings.Contains(ua, "CriOS/") || strings.Contains(ua, "Chrome/"):
		app = "Chrome"
	case strings.Contains(ua, "Safari/"):
		app = "Safari"
	default:
		app = "Browser"
	}
	return device, app
}

// friendlyModel names the TV boxes people actually have; anything else
// keeps its model string.
func friendlyModel(model, fallback string) string {
	switch {
	case model == "":
		return fallback
	case strings.HasPrefix(model, "SHIELD"):
		return "Nvidia Shield"
	case model == "AFTGAZL" || model == "AFTR" || model == "AFTA":
		return "Fire TV Cube"
	case strings.HasPrefix(model, "AFT"):
		return "Fire TV"
	}
	return model
}

func isLocalIP(ip string) bool {
	if ip == "" {
		return false
	}
	p := net.ParseIP(ip)
	return p != nil && (p.IsLoopback() || p.IsPrivate() || p.IsLinkLocalUnicast())
}
