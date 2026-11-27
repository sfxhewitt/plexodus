package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/sfxhewitt/plexodus/internal/ask"
	"github.com/sfxhewitt/plexodus/internal/metadata"
	"github.com/sfxhewitt/plexodus/internal/probe"
	"github.com/sfxhewitt/plexodus/internal/subfetch"
	"github.com/sfxhewitt/plexodus/internal/subs"
)

// Scene companion: a phone page that follows whatever this account is
// playing (from the Now watching tracker) and shows notes about the scene on
// screen: why a character acts the way they do, context, dated language,
// trivia, details you'd miss. Claude writes the notes for ten-minute stretches
// of a title from its subtitles and stills of the picture, ahead of time, and they're kept in SQLite so
// a stretch is only ever written once. The page shows a note only once
// playback reaches its moment, so nothing ahead is revealed.

const (
	// compVersion is stored with each stretch. Stretches written by an
	// older version (1 = subtitles only, 2 = with stills, 3 = with the actors a note mentions) are
	// written again when someone next watches them.
	compVersion = 3
	compChunk   = 600 // seconds per stretch of notes
	// compLead starts writing the next stretch this long before it begins.
	compLead = 240
)

type compNote struct {
	At    int    `json:"at"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Text  string `json:"text"`
	// People: the actors behind the characters this note mentions, so a
	// viewer who can't place a name sees the face.
	People []metadata.Person `json:"people,omitempty"`
}

// compCastMax is how many faces one note may carry.
const compCastMax = 3

// companionCast is the title's top-billed cast (a show's, for an episode);
// nil when TMDB doesn't know it.
func (s *Server) companionCast(ctx context.Context, item int64) []metadata.Person {
	var id, tmdb int64
	var kind string
	err := s.db.R.QueryRowContext(ctx, `SELECT COALESCE(sh.id, i.id), COALESCE(sh.kind, i.kind), COALESCE(sh.tmdb_id, i.tmdb_id, 0)
		FROM items i
		LEFT JOIN items se ON i.kind = 'episode' AND se.id = i.parent_id
		LEFT JOIN items sh ON sh.id = se.parent_id
		WHERE i.id = ?`, item).Scan(&id, &kind, &tmdb)
	if err != nil || tmdb <= 0 || (kind != "movie" && kind != "show") {
		return nil
	}
	ex := s.extras(ctx, id, kind, tmdb)
	if ex == nil {
		return nil
	}
	var out []metadata.Person
	for _, p := range ex.Cast {
		if strings.TrimSpace(p.Character) != "" && p.Name != "" {
			out = append(out, p)
		}
	}
	return out
}

// castPrompt lists the cast for Claude, numbered from 1.
func castPrompt(cast []metadata.Person) string {
	if len(cast) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nCast of this title (number, character, actor):\n")
	for i, p := range cast {
		fmt.Fprintf(&b, "%d. %s: played by %s\n", i+1, p.Character, p.Name)
	}
	return b.String()
}

// castPeople turns the numbers Claude gave into cast members, dropping
// anything that isn't on the list.
func castPeople(cast []metadata.Person, nums []int) []metadata.Person {
	var out []metadata.Person
	seen := map[int]bool{}
	for _, n := range nums {
		if n < 1 || n > len(cast) || seen[n] || len(out) == compCastMax {
			continue
		}
		seen[n] = true
		out = append(out, cast[n-1])
	}
	return out
}

type subLine struct {
	start, end float64
	text       string
}

type subEntry struct {
	lines []subLine
	track string
	at    time.Time
}

type companion struct {
	mu       sync.Mutex
	inflight map[[2]int64]bool
	errs     map[int64]string // item -> last generation error
	subs     map[int64]subEntry
	subsBusy map[int64]bool
	// stills grabs frames for Claude to look at; nil = notes from text only.
	stills *stillMaker
}

func newCompanion() *companion {
	return &companion{inflight: map[[2]int64]bool{}, errs: map[int64]string{}, subs: map[int64]subEntry{}, subsBusy: map[int64]bool{}}
}

// accountOf is the account a viewer (account or profile) belongs to.
func (s *Server) accountOf(ctx context.Context, viewer int64) int64 {
	var acct int64
	if s.db.R.QueryRowContext(ctx, `SELECT COALESCE(parent_id, id) FROM users WHERE id = ?`, viewer).Scan(&acct) != nil {
		return 0
	}
	return acct
}

// myStream is what this account is watching: something playing beats
// something paused, then the most recently active.
func (s *Server) myStream(r *http.Request, now time.Time) (npRaw, bool) {
	u := currentUser(r)
	acct := u.AccountID
	if acct == 0 {
		acct = u.ID
	}
	var best npRaw
	found := false
	for _, raw := range s.nowPlaying.snapshot(now) {
		if raw.viewer != u.ID && s.accountOf(r.Context(), raw.viewer) != acct {
			continue
		}
		if !found || (raw.playing && !best.playing) || (raw.playing == best.playing && raw.lastSeen.After(best.lastSeen)) {
			best, found = raw, true
		}
	}
	return best, found
}

type companionResp struct {
	Enabled  bool        `json:"enabled"`
	Stream   *NowPlaying `json:"stream"`
	Notes    []compNote  `json:"notes"`
	Pending  bool        `json:"pending"`
	Grounded bool        `json:"grounded"`
	Notice   string      `json:"notice,omitempty"`
}

func (s *Server) handleCompanion(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	out := companionResp{Enabled: s.claude.Enabled(), Notes: []compNote{}}
	raw, ok := s.myStream(r, now)
	if !ok {
		writeJSON(w, out)
		return
	}
	np, ok := s.describeStream(r.Context(), raw, now)
	if !ok {
		writeJSON(w, out)
		return
	}
	np.IP, np.Local = "", false
	out.Stream = &np
	pos := np.Position
	cur := int64(pos) / compChunk

	rows, err := s.db.R.QueryContext(r.Context(), `SELECT chunk, grounded, notes, v FROM companion_notes WHERE item_id = ? AND chunk <= ? ORDER BY chunk`, np.ItemID, cur+1)
	have := map[int64]bool{}
	grounded := map[int64]bool{}
	old := map[int64]bool{} // written by an earlier version
	if err == nil {
		for rows.Next() {
			var c int64
			var g, v int
			var js string
			if rows.Scan(&c, &g, &js, &v) != nil {
				continue
			}
			have[c], grounded[c], old[c] = true, g == 1, v < compVersion
			var ns []compNote
			json.Unmarshal([]byte(js), &ns)
			for _, n := range ns {
				if float64(n.At) <= pos+2 {
					out.Notes = append(out.Notes, n)
				}
			}
		}
		rows.Close()
	}
	sort.SliceStable(out.Notes, func(i, j int) bool { return out.Notes[i].At > out.Notes[j].At })
	out.Grounded = grounded[cur]

	if out.Enabled {
		file := s.companionFile(r.Context(), raw)
		if !have[cur] {
			out.Pending = s.companionGenerate(np, file, cur)
		} else if old[cur] {
			// Keep showing the old notes while better ones are written.
			s.companionGenerate(np, file, cur)
		}
		next := cur + 1
		nextStart := float64(next * compChunk)
		if (np.Duration <= 0 || nextStart < np.Duration) && nextStart-pos < compLead {
			// Write the next stretch ahead of time, and rewrite one written
			// before the subtitles were ready, as long as it hasn't started.
			if !have[next] || old[next] || (!grounded[next] && s.companionHasSubs(file)) {
				s.companionGenerate(np, file, next)
			}
		}
		s.comp.mu.Lock()
		if !out.Pending && !have[cur] {
			out.Pending = s.comp.inflight[[2]int64{np.ItemID, cur}]
		}
		if !have[cur] && !out.Pending {
			out.Notice = s.comp.errs[np.ItemID]
		}
		s.comp.mu.Unlock()
	}
	writeJSON(w, out)
}

// companionFile is the media file being played, else the item's first.
func (s *Server) companionFile(ctx context.Context, raw npRaw) int64 {
	if raw.file != 0 {
		return raw.file
	}
	var id int64
	s.db.R.QueryRowContext(ctx, `SELECT id FROM media_files WHERE item_id = ? ORDER BY id LIMIT 1`, raw.item).Scan(&id)
	return id
}

// filePath is where a media file lives, "" if unknown.
func (s *Server) filePath(ctx context.Context, file int64) string {
	var p string
	if file != 0 {
		s.db.R.QueryRowContext(ctx, `SELECT path FROM media_files WHERE id = ?`, file).Scan(&p)
	}
	return p
}

func (s *Server) companionHasSubs(file int64) bool {
	s.comp.mu.Lock()
	defer s.comp.mu.Unlock()
	e, ok := s.comp.subs[file]
	return ok && len(e.lines) > 0
}

// companionGenerate starts writing a stretch in the background; it reports
// whether one is now being written.
func (s *Server) companionGenerate(np NowPlaying, file, chunk int64) bool {
	k := [2]int64{np.ItemID, chunk}
	s.comp.mu.Lock()
	if s.comp.inflight[k] {
		s.comp.mu.Unlock()
		return true
	}
	s.comp.inflight[k] = true
	s.comp.mu.Unlock()
	go func() {
		defer func() {
			s.comp.mu.Lock()
			delete(s.comp.inflight, k)
			s.comp.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(s.baseCtx, 4*time.Minute)
		defer cancel()
		err := s.writeNotes(ctx, np, file, chunk)
		s.comp.mu.Lock()
		if err != nil {
			s.comp.errs[np.ItemID] = err.Error()
		} else {
			delete(s.comp.errs, np.ItemID)
		}
		s.comp.mu.Unlock()
		if err != nil {
			log.Printf("companion: item %d stretch %d: %v", np.ItemID, chunk, err)
		}
	}()
	return true
}

type compItem struct {
	kind, title, show, overview, tagline string
	year                                 int
	season, episode                      sql.NullInt64
	genres                               string
}

func (s *Server) companionItem(ctx context.Context, item int64) (compItem, error) {
	var c compItem
	err := s.db.R.QueryRowContext(ctx, `SELECT i.kind, i.title, COALESCE(i.year, sh.year, 0), COALESCE(sh.title, ''),
		COALESCE(NULLIF(i.overview, ''), ''), COALESCE(NULLIF(i.tagline, ''), sh.tagline, ''), i.season_number, i.episode_number,
		COALESCE(i.genres, sh.genres, '')
		FROM items i
		LEFT JOIN items se ON i.kind = 'episode' AND se.id = i.parent_id
		LEFT JOIN items sh ON sh.id = se.parent_id
		WHERE i.id = ?`, item).Scan(&c.kind, &c.title, &c.year, &c.show, &c.overview, &c.tagline, &c.season, &c.episode, &c.genres)
	return c, err
}

func (c compItem) describe() string {
	var b strings.Builder
	if c.kind == "episode" {
		fmt.Fprintf(&b, "TV episode: %q", c.show)
		if c.season.Valid && c.episode.Valid {
			fmt.Fprintf(&b, " season %d episode %d", c.season.Int64, c.episode.Int64)
		}
		fmt.Fprintf(&b, ", titled %q", c.title)
	} else {
		fmt.Fprintf(&b, "Film: %q", c.title)
	}
	if c.year > 0 {
		fmt.Fprintf(&b, " (%d)", c.year)
	}
	b.WriteString("\n")
	if c.genres != "" && c.genres != "null" {
		var g []string
		if json.Unmarshal([]byte(c.genres), &g) == nil && len(g) > 0 {
			fmt.Fprintf(&b, "Genres: %s\n", strings.Join(g, ", "))
		}
	}
	if c.overview != "" {
		fmt.Fprintf(&b, "Summary: %s\n", c.overview)
	}
	return b.String()
}

func clock(sec float64) string {
	t := int(sec)
	if t < 0 {
		t = 0
	}
	return fmt.Sprintf("%d:%02d:%02d", t/3600, t/60%60, t%60)
}

func (s *Server) writeNotes(ctx context.Context, np NowPlaying, file, chunk int64) error {
	it, err := s.companionItem(ctx, np.ItemID)
	if err != nil {
		return err
	}
	start := float64(chunk * compChunk)
	end := start + compChunk
	if np.Duration > 0 && end > np.Duration {
		end = np.Duration
	}
	// The first stretch of a title waits a little for subtitles that are
	// still being extracted; later ones are usually ready by then. Stills
	// are grabbed meanwhile.
	wait := 25 * time.Second
	var shots []still
	done := make(chan struct{})
	go func() {
		defer close(done)
		shots = s.comp.stills.stills(ctx, s.filePath(ctx, file), stillTimes(start, end), stillBudget)
	}()
	lines, _ := s.subtitleLines(ctx, file, wait)
	<-done

	var b strings.Builder
	var parts []ask.Part
	flush := func() {
		if b.Len() > 0 {
			parts = append(parts, ask.Part{Text: b.String()})
			b.Reset()
		}
	}
	b.WriteString(it.describe())
	cast := s.companionCast(ctx, np.ItemID)
	b.WriteString(castPrompt(cast))
	if np.Duration > 0 {
		fmt.Fprintf(&b, "Running time of this copy: %s\n", clock(np.Duration))
	}
	fmt.Fprintf(&b, "\nWrite notes for the stretch from %s to %s (that is %d to %d seconds into this copy).\n", clock(start), clock(end), int(start), int(end))
	grounded := false
	var said []subLine
	for _, l := range lines {
		if l.end < start-120 || l.start > end {
			continue
		}
		said = append(said, l)
	}
	switch {
	case len(said) > 0:
		grounded = true
	case len(lines) > 0:
		b.WriteString("\nThe subtitles have no dialogue in this stretch (it may be a quiet or musical passage, or the end credits).\n")
		grounded = true
	case len(shots) > 0:
		b.WriteString("\nNo subtitles are available for this copy, so rely on the stills and their timestamps to place notes.\n")
		grounded = true
	default:
		b.WriteString("\nNo subtitles are available for this copy, so place notes at your best estimate of when each moment happens, based on your knowledge of the work and typical pacing. Prefer notes that don't depend on split-second timing.\n")
	}
	if len(shots) > 0 {
		fmt.Fprintf(&b, "\nWhat follows is this stretch in order: a still from the picture about every %d seconds, each preceded by its timestamp in this copy", stillEvery)
		if len(said) > 0 {
			b.WriteString(", with the subtitle lines spoken in between")
		}
		b.WriteString(".\n\n")
		i := 0
		for _, sh := range shots {
			for ; i < len(said) && said[i].start < sh.at; i++ {
				fmt.Fprintf(&b, "[%s] %s\n", clock(said[i].start), said[i].text)
			}
			fmt.Fprintf(&b, "Still at [%s] (%d seconds):\n", clock(sh.at), int(sh.at))
			flush()
			parts = append(parts, ask.Part{JPEG: sh.jpeg})
		}
		for ; i < len(said); i++ {
			fmt.Fprintf(&b, "[%s] %s\n", clock(said[i].start), said[i].text)
		}
	} else if len(said) > 0 {
		b.WriteString("\nSubtitles from just before and during this stretch, with timestamps in this copy:\n")
		for _, l := range said {
			fmt.Fprintf(&b, "[%s] %s\n", clock(l.start), l.text)
		}
	}
	// Earlier notes, so the same point isn't made twice.
	var prev string
	if s.db.R.QueryRowContext(ctx, `SELECT notes FROM companion_notes WHERE item_id = ? AND chunk = ?`, np.ItemID, chunk-1).Scan(&prev) == nil {
		var ns []compNote
		if json.Unmarshal([]byte(prev), &ns) == nil && len(ns) > 0 {
			b.WriteString("\nNotes already shown for the previous stretch (don't repeat them):\n")
			for _, n := range ns {
				fmt.Fprintf(&b, "- %s\n", n.Title)
			}
		}
	}

	flush()
	raw, err := s.claude.CompleteParts(ctx, companionSystem, parts, notesSchema, anthropic.BetaOutputConfigEffortMedium, 8000, 3*time.Minute)
	if err != nil {
		return err
	}
	var res struct {
		Notes []struct {
			compNote
			Cast []int `json:"cast"`
		} `json:"notes"`
	}
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		return fmt.Errorf("unreadable answer from Claude: %w", err)
	}
	notes := make([]compNote, 0, len(res.Notes))
	for _, rn := range res.Notes {
		n := rn.compNote
		n.People = castPeople(cast, rn.Cast)
		n.Title, n.Text = strings.TrimSpace(n.Title), strings.TrimSpace(n.Text)
		if n.Text == "" {
			continue
		}
		if float64(n.At) < start {
			n.At = int(start)
		}
		if float64(n.At) > end {
			n.At = int(end)
		}
		notes = append(notes, n)
	}
	sort.SliceStable(notes, func(i, j int) bool { return notes[i].At < notes[j].At })
	js, _ := json.Marshal(notes)
	g := 0
	if grounded {
		g = 1
	}
	_, err = s.db.W.ExecContext(ctx, `INSERT INTO companion_notes (item_id, chunk, grounded, notes, created_at, v) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (item_id, chunk) DO UPDATE SET grounded = excluded.grounded, notes = excluded.notes, created_at = excluded.created_at, v = excluded.v`,
		np.ItemID, chunk, g, string(js), time.Now().Unix(), compVersion)
	if err == nil {
		log.Printf("companion: wrote %d notes for item %d at %s (subtitle lines: %d, stills: %d)", len(notes), np.ItemID, clock(start), len(said), len(shots))
	}
	return err
}

const companionSystem = `You are a second-screen companion for someone watching a film or TV episode at home. Their phone shows your notes as playback reaches each moment. The goal is to deepen the experience the way a brilliant, warm film-buff friend sitting beside them would.

Write notes that add what the viewer might not get straight away:
- character: why someone is acting this way right now (motivation, what they want, what they're hiding, how it connects to their past or to earlier scenes)
- context: history, culture, places, social norms and events of the time that explain what's happening
- language: dated slang, idioms, references and phrases whose meaning has shifted (for example, in 1940s films "making love" meant romantic wooing, not sex)
- detail: small things on screen or in the dialogue that are easy to miss: what a newspaper, letter, sign or screen in shot actually says and why it matters, a prop or a face in the background, a look between two people, what a wordless scene is telling us, setups that matter later (without saying how they pay off), symbolism, recurring motifs
- trivia: genuinely interesting production facts, casting, behind-the-scenes stories, real-world connections

You are usually given stills from the picture as well as the subtitles. Look at them closely: read any text in shot, notice who is there, what they hold, where they are and how the shot is staged, and combine that with what you know about the work. Much of what matters in a film is never said aloud, so stretches with little or no dialogue deserve notes too. The stills are only a sample (one every several seconds), so things happen between them; use your knowledge of the work to fill gaps, and place a note about something visual at the timestamp of the still that shows it.

Rules:
- Never describe what is plainly on screen ("they sit down and talk"). No play-by-play, no recapping the dialogue.
- No spoilers. Say nothing about anything after the end of the stretch you are given: no later plot turns, deaths, twists, reveals or endings, and no hints that something is foreshadowing a specific event. Character backstory that the story has already shown or that a viewer at this point would know is fine.
- Only state facts you are confident are true. Trivia must be real; if unsure, leave it out.
- Tie every note to a specific moment in the stretch, using the timestamps of the stills and subtitles when given. "at" is the number of seconds into this copy when the note should appear: just after the line or moment it is about.
- 4 to 8 notes per ten-minute stretch, spread across it. Fewer is fine for quiet stretches or credits; quality over quantity.
- title: a short hook of at most 8 words. text: 1 to 3 conversational sentences, plain English, no markdown.
- cast: when a cast list is given, the numbers of the listed characters this note names or is plainly about (at most 3, most central first), so the viewer can be shown the actor's face. Match by who the character is, not by spelling: a note may use a first name, surname or nickname. Only include a number when you are sure it is that character; a wrong face is worse than none. Use an empty list when the note names nobody on the list or no list is given.`

var notesSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required":             []string{"notes"},
	"properties": map[string]any{
		"notes": map[string]any{"type": "array", "items": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"at", "kind", "title", "text", "cast"},
			"properties": map[string]any{
				"at":    map[string]any{"type": "integer"},
				"kind":  map[string]any{"type": "string", "enum": []string{"character", "context", "language", "detail", "trivia"}},
				"title": map[string]any{"type": "string"},
				"text":  map[string]any{"type": "string"},
				"cast":  map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
			},
		}},
	},
}

// ---- asking about the moment ----

func (s *Server) handleCompanionAsk(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Question string `json:"question"`
	}
	if readJSON(r, &body) != nil || strings.TrimSpace(body.Question) == "" {
		writeErr(w, 400, "ask a question")
		return
	}
	if len(body.Question) > 500 {
		body.Question = body.Question[:500]
	}
	if !s.claude.Enabled() {
		writeErr(w, 409, "Add an Anthropic API key in Settings → Plain-English search first")
		return
	}
	now := time.Now()
	raw, ok := s.myStream(r, now)
	if !ok {
		writeErr(w, 404, "Nothing is playing right now")
		return
	}
	np, ok := s.describeStream(r.Context(), raw, now)
	if !ok {
		writeErr(w, 404, "Nothing is playing right now")
		return
	}
	it, err := s.companionItem(r.Context(), np.ItemID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	file := s.companionFile(r.Context(), raw)
	lines, _ := s.subtitleLines(r.Context(), file, 3*time.Second)
	var b strings.Builder
	b.WriteString(it.describe())
	cast := s.companionCast(r.Context(), np.ItemID)
	b.WriteString(castPrompt(cast))
	fmt.Fprintf(&b, "\nThe viewer is at %s.\n", clock(np.Position))
	var dl strings.Builder
	for _, l := range lines {
		if l.start >= np.Position-300 && l.start <= np.Position+5 {
			fmt.Fprintf(&dl, "[%s] %s\n", clock(l.start), l.text)
		}
	}
	if dl.Len() > 0 {
		b.WriteString("Subtitles from the last five minutes:\n")
		b.WriteString(dl.String())
	}
	// What they're looking at: the last minute or so of the picture.
	parts := []ask.Part{{Text: b.String()}}
	var at []float64
	for _, back := range []float64{60, 40, 25, 12, 2} {
		if t := np.Position - back; t >= 0 {
			at = append(at, t)
		}
	}
	if shots := s.comp.stills.stills(r.Context(), s.filePath(r.Context(), file), at, 12*time.Second); len(shots) > 0 {
		parts = append(parts, ask.Part{Text: "\nStills from the last minute of the picture, oldest first (the last is what's on screen now):\n"})
		for _, sh := range shots {
			parts = append(parts, ask.Part{Text: fmt.Sprintf("[%s]", clock(sh.at))}, ask.Part{JPEG: sh.jpeg})
		}
	}
	parts = append(parts, ask.Part{Text: fmt.Sprintf("\nTheir question: %s", body.Question)})
	ans, err := s.claude.CompleteParts(r.Context(), companionAskSystem, parts, answerSchema, anthropic.BetaOutputConfigEffortLow, 3000, 90*time.Second)
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	var res struct {
		Answer string `json:"answer"`
		Cast   []int  `json:"cast"`
	}
	if json.Unmarshal([]byte(ans), &res) != nil {
		writeErr(w, 502, "unreadable answer from Claude")
		return
	}
	people := castPeople(cast, res.Cast)
	if people == nil {
		people = []metadata.Person{}
	}
	writeJSON(w, map[string]any{"answer": strings.TrimSpace(res.Answer), "at": int(np.Position), "people": people})
}

const companionAskSystem = `You are a second-screen companion for someone watching a film or TV episode. They paused to ask about what's happening. You may be shown stills of what they've just seen: use them (read text in shot, notice who and what is there). Answer like a knowledgeable, warm friend: directly, in 1 to 4 conversational sentences, plain English, no markdown. Explain motivations, context, dated language or references as needed. Never spoil anything after the point they've reached, even if asked what happens next: say you'd rather not spoil it. Only state facts you're confident about. cast: when a cast list is given, the numbers of the listed characters your answer names (at most 3), only when you are sure who they are; otherwise an empty list.`

var answerSchema = map[string]any{
	"type": "object", "additionalProperties": false, "required": []string{"answer", "cast"},
	"properties": map[string]any{"answer": map[string]any{"type": "string"},
		"cast": map[string]any{"type": "array", "items": map[string]any{"type": "integer"}}},
}

// ---- subtitles ----

// subtitleLines returns the file's best English text subtitles, parsed. A
// sidecar file is read at once; an embedded track is extracted in the
// background (ffmpeg reads the whole file), waiting up to wait for it.
func (s *Server) subtitleLines(ctx context.Context, file int64, wait time.Duration) ([]subLine, error) {
	if file == 0 {
		return nil, nil
	}
	s.comp.mu.Lock()
	if e, ok := s.comp.subs[file]; ok && (len(e.lines) > 0 || time.Since(e.at) < 10*time.Minute) {
		s.comp.mu.Unlock()
		return e.lines, nil
	}
	s.comp.mu.Unlock()

	var path, streams string
	if err := s.db.R.QueryRowContext(ctx, `SELECT path, COALESCE(streams, 'null') FROM media_files WHERE id = ?`, file).Scan(&path, &streams); err != nil {
		return nil, err
	}
	if streams == "null" {
		pctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		s.prober.ProbeNow(pctx, file, path)
		cancel()
		s.db.R.QueryRowContext(ctx, `SELECT COALESCE(streams, 'null') FROM media_files WHERE id = ?`, file).Scan(&streams)
	}
	var ss []probe.Stream
	json.Unmarshal([]byte(streams), &ss)
	tracks, es := embeddedTracks(ss)
	tracks = append(subs.Sidecars(path), tracks...)
	t := pickTrack(tracks)
	if t == nil || !hasText(tracks, s.onlineSubsLang(ctx)) {
		// Nothing readable in the file: use subtitles found online (the
		// search starts when playback does).
		if body, err := s.onlineSRT(ctx, file); err == nil {
			var lines []subLine
			for _, c := range subfetch.ParseSRT(string(body)) {
				lines = append(lines, subLine{start: c.Start, end: c.End, text: strings.Join(strings.Fields(htmlTags.ReplaceAllString(c.Text, "")), " ")})
			}
			if len(lines) > 0 {
				s.comp.mu.Lock()
				s.comp.subs[file] = subEntry{lines: lines, track: onlineTrackID, at: time.Now()}
				s.comp.mu.Unlock()
				return lines, nil
			}
		}
	}
	if t == nil {
		s.comp.mu.Lock()
		s.comp.subs[file] = subEntry{at: time.Now()}
		s.comp.mu.Unlock()
		return nil, nil
	}
	var body []byte
	var err error
	if t.External {
		body, err = s.subs.Sidecar(ctx, path, *t)
	} else {
		if !s.subs.Available() {
			return nil, nil
		}
		idx, _ := strconv.Atoi(t.ID[1:])
		wctx, cancel := context.WithTimeout(ctx, wait)
		body, err = s.subs.Embedded(wctx, file, path, es, idx)
		cancel()
		if err != nil && wctx.Err() != nil {
			return nil, nil // still extracting; try again next time
		}
	}
	if err != nil {
		s.comp.mu.Lock()
		s.comp.subs[file] = subEntry{at: time.Now()}
		s.comp.mu.Unlock()
		return nil, err
	}
	lines := parseASS(string(body))
	s.comp.mu.Lock()
	if len(s.comp.subs) > 50 {
		s.comp.subs = map[int64]subEntry{}
	}
	s.comp.subs[file] = subEntry{lines: lines, track: t.ID, at: time.Now()}
	s.comp.mu.Unlock()
	return lines, nil
}

func isEnglish(lang string) bool {
	switch strings.ToLower(lang) {
	case "", "en", "eng", "english", "und":
		return true
	}
	return false
}

// pickTrack prefers full English text tracks, sidecars first (no file read).
func pickTrack(tracks []subs.Track) *subs.Track {
	var best *subs.Track
	bestScore := -1
	for i := range tracks {
		t := &tracks[i]
		if t.Format == "image" || !isEnglish(t.Language) {
			continue
		}
		score := 1
		if strings.ToLower(t.Language) != "" && strings.ToLower(t.Language) != "und" {
			score += 4
		}
		if !t.Forced && !strings.Contains(strings.ToLower(t.Title), "forced") {
			score += 8
		}
		if t.External {
			score += 2
		}
		if strings.Contains(strings.ToLower(t.Title), "sdh") {
			score++ // fuller: includes sounds and speakers
		}
		if score > bestScore {
			best, bestScore = t, score
		}
	}
	return best
}

var assTags = regexp.MustCompile(`\{[^}]*\}`)
var htmlTags = regexp.MustCompile(`</?[a-zA-Z][^>]*>|\{[^}]*\}`)

// forgetSubs drops what is remembered about a file's subtitles, so newly
// downloaded ones are picked up.
func (c *companion) forgetSubs(file int64) {
	c.mu.Lock()
	delete(c.subs, file)
	c.mu.Unlock()
}

// parseASS pulls timed dialogue out of an ASS script.
func parseASS(s string) []subLine {
	var out []subLine
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "Dialogue:") {
			continue
		}
		f := strings.SplitN(strings.TrimPrefix(ln, "Dialogue:"), ",", 10)
		if len(f) < 10 {
			continue
		}
		st, ok1 := assTime(f[1])
		en, ok2 := assTime(f[2])
		if !ok1 || !ok2 {
			continue
		}
		txt := assTags.ReplaceAllString(f[9], "")
		txt = strings.NewReplacer(`\N`, " ", `\n`, " ", `\h`, " ").Replace(txt)
		txt = strings.Join(strings.Fields(txt), " ")
		if txt == "" {
			continue
		}
		if n := len(out); n > 0 && out[n-1].text == txt && st-out[n-1].end < 1 {
			out[n-1].end = en
			continue
		}
		out = append(out, subLine{start: st, end: en, text: txt})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].start < out[j].start })
	return out
}

// assTime parses H:MM:SS.cc.
func assTime(v string) (float64, bool) {
	p := strings.Split(strings.TrimSpace(v), ":")
	if len(p) != 3 {
		return 0, false
	}
	h, e1 := strconv.Atoi(p[0])
	m, e2 := strconv.Atoi(p[1])
	sec, e3 := strconv.ParseFloat(p[2], 64)
	if e1 != nil || e2 != nil || e3 != nil {
		return 0, false
	}
	return float64(h*3600+m*60) + sec, true
}
