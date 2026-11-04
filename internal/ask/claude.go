package ask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

// DefaultModel is used unless PLEXODUS_ASK_MODEL says otherwise.
const DefaultModel = "claude-opus-5-5"

// Claude translates requests with the Claude API. It is safe for concurrent
// use; translations are cached so repeating a search costs nothing.
type Claude struct {
	mu     sync.Mutex
	key    string
	model  string
	client anthropic.Client
	cache  map[string]cached
	sem    chan struct{}
	opts   []option.RequestOption
}

type cached struct {
	q  Query
	at time.Time
}

const cacheTTL = 6 * time.Hour

// NewClaude makes a translator; opts are extra client options (tests point
// the client at a fake server with option.WithBaseURL).
func NewClaude(key, model string, opts ...option.RequestOption) *Claude {
	if model == "" {
		model = DefaultModel
	}
	c := &Claude{model: model, cache: map[string]cached{}, sem: make(chan struct{}, 4), opts: opts}
	c.SetKey(key)
	return c
}

// SetKey swaps the API key; an empty key turns Claude off.
func (c *Claude) SetKey(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.key = strings.TrimSpace(key)
	c.client = anthropic.NewClient(append([]option.RequestOption{option.WithAPIKey(c.key), option.WithMaxRetries(1)}, c.opts...)...)
	c.cache = map[string]cached{}
}

func (c *Claude) Enabled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.key != ""
}

func (c *Claude) Model() string { return c.model }

var ErrRefused = errors.New("Claude declined this request")

// Translate turns text into a Query. genres are the library's genre names.
func (c *Claude) Translate(ctx context.Context, text string, genres []string) (Query, error) {
	cacheKey := strings.ToLower(strings.Join(strings.Fields(text), " "))
	c.mu.Lock()
	if e, ok := c.cache[cacheKey]; ok && time.Since(e.at) < cacheTTL {
		c.mu.Unlock()
		return e.q, nil
	}
	client := c.client
	c.mu.Unlock()

	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return Query{}, ctx.Err()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	resp, err := client.Beta.Messages.New(ctx, anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(c.model),
		MaxTokens: 4000,
		System:    []anthropic.BetaTextBlockParam{{Text: systemPrompt(genres)}},
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(text))},
		OutputConfig: anthropic.BetaOutputConfigParam{
			// Low effort keeps searches quick; this is a small translation job.
			Effort: anthropic.BetaOutputConfigEffortLow,
			Format: anthropic.BetaJSONOutputFormatParam{Schema: querySchema},
		},
		// If a request is ever declined by a safety classifier, let the API
		// re-serve it on a fallback model inside the same call.
		Fallbacks: anthropic.BetaFallbacksParamUnion{OfDefault: "default"},
		Betas:     []anthropic.AnthropicBeta{anthropic.AnthropicBetaServerSideFallback2026_07_01},
	})
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			return Query{}, fmt.Errorf("Claude API error %d: %s", apiErr.StatusCode, apiMessage(apiErr))
		}
		return Query{}, err
	}
	if resp.StopReason == anthropic.BetaStopReasonRefusal {
		return Query{}, ErrRefused
	}
	var out string
	for _, b := range resp.Content {
		if t, ok := b.AsAny().(anthropic.BetaTextBlock); ok {
			out += t.Text
		}
	}
	var q Query
	if err := json.Unmarshal([]byte(out), &q); err != nil {
		return Query{}, fmt.Errorf("unreadable answer from Claude: %w", err)
	}
	q.Normalize()

	c.mu.Lock()
	if len(c.cache) > 1000 {
		c.cache = map[string]cached{}
	}
	c.cache[cacheKey] = cached{q: q, at: time.Now()}
	c.mu.Unlock()
	return q, nil
}

// apiMessage pulls the human-readable message out of an API error body.
func apiMessage(e *anthropic.Error) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(e.RawJSON()), &body) == nil && body.Error.Message != "" {
		return body.Error.Message
	}
	return strings.TrimSpace(e.Error())
}

func systemPrompt(genres []string) string {
	g := "(none recorded)"
	if len(genres) > 0 {
		g = strings.Join(genres, ", ")
	}
	return fmt.Sprintf(`You turn a viewer's plain-English request into a search of their home media library (films and TV shows). The current year is %d.

Fill in every field of the JSON schema. Use zero, empty lists, "any" or "relevance" for anything the request doesn't constrain; don't invent constraints the viewer didn't ask for.

- kinds: ["movie"] for films, ["show"] for TV series, [] when either is fine.
- genres: titles must match at least one. Use ONLY names from this library's genre list, spelled exactly as listed: %s. Pick every listed genre that fits (for example both "Sci-Fi & Fantasy" and "Science Fiction" if both exist). Leave empty when no genre is implied.
- excludeGenres: genres the viewer wants to avoid ("nothing scary" excludes Horror), same exact names.
- yearFrom / yearTo: release-year range, inclusive ("90s" is 1990-1999, "recent" is roughly the last 3 years).
- minRuntime / maxRuntime: film length in minutes ("under two hours" is maxRuntime 120). Don't set these for TV shows.
- minRating: 0-10 audience rating floor, only when the viewer asks for good, acclaimed or highly rated titles (use about 7).
- watched: "unwatched" for things they haven't seen, "watched" for rewatches, "inprogress" for things they started, otherwise "any".
- keywords: up to 4 single words likely to appear in the plot summary or title of matching titles, for themes and subjects that genres can't express (a heist request might use "heist", "robbery", "thief"). Leave empty for requests fully covered by other fields.
- similarTo: the title the viewer names when asking for something like it, otherwise "".
- titles: when the request asks for something like a named title, or describes a theme, mood or subject, list 15-30 well-known real titles (with release year) that genuinely fit every constraint in the request, best fits first. These are recommendations; the server checks which ones the library has. Don't include the title the viewer named. Leave empty for pure filter requests such as "unwatched comedies from 2019".
- sort: "relevance" when keywords or titles drive the results, "rating" for best or acclaimed, "newest" / "oldest" for release order, "added" for recently added, "random" for "surprise me", otherwise "rating".
- summary: the request restated as a short, friendly results heading of at most 8 words, for example "90s heist films under two hours".`, time.Now().Year(), g)
}

var querySchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"required": []string{"kinds", "genres", "excludeGenres", "yearFrom", "yearTo", "minRuntime", "maxRuntime", "minRating",
		"watched", "keywords", "similarTo", "titles", "sort", "summary"},
	"properties": map[string]any{
		"kinds":         map[string]any{"type": "array", "items": map[string]any{"type": "string", "enum": []string{"movie", "show"}}},
		"genres":        map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"excludeGenres": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"yearFrom":      map[string]any{"type": "integer"},
		"yearTo":        map[string]any{"type": "integer"},
		"minRuntime":    map[string]any{"type": "integer"},
		"maxRuntime":    map[string]any{"type": "integer"},
		"minRating":     map[string]any{"type": "number"},
		"watched":       map[string]any{"type": "string", "enum": []string{"any", "unwatched", "watched", "inprogress"}},
		"keywords":      map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"similarTo":     map[string]any{"type": "string"},
		"titles": map[string]any{"type": "array", "items": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"title", "year"},
			"properties": map[string]any{"title": map[string]any{"type": "string"}, "year": map[string]any{"type": "integer"}},
		}},
		"sort":    map[string]any{"type": "string", "enum": []string{"relevance", "rating", "newest", "oldest", "added", "random"}},
		"summary": map[string]any{"type": "string"},
	},
}
