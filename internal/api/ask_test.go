package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/v2/option"
	"github.com/sfxhewitt/plexodus/internal/ask"
	"github.com/sfxhewitt/plexodus/internal/config"
	"github.com/sfxhewitt/plexodus/internal/db"
)

// askServer adds a small film/show catalogue to the test server.
func askServer(t *testing.T) (*Server, func(q string) askResult) {
	s, u := testServer(t)
	s.cfg = &config.Config{}
	now := db.Now()
	for _, q := range []string{
		`INSERT INTO libraries(id, name, kind, paths, created_at) VALUES (3, 'Movies 4K', 'movies', '[]', 0)`,
		`UPDATE items SET year = 1995, runtime = 170, genres = '["Crime","Thriller"]', overview = 'A heist crew and the detective chasing them.' WHERE id = 10`,
		`UPDATE items SET year = 1988, runtime = 124, genres = '["Animation","Science Fiction"]' WHERE id = 20`,
		`INSERT INTO items(id, library_id, kind, guid, title, sort_title, year, runtime, rating, genres, overview, added_at, updated_at) VALUES
			(11, 1, 'movie', 'm11', 'Reservoir Dogs', 'reservoir dogs', 1992, 99, 8.0, '["Crime","Thriller"]', 'A jewel heist goes wrong.', ?1, ?1),
			(12, 1, 'movie', 'm12', 'The Usual Suspects', 'usual suspects', 1995, 106, 8.2, '["Crime","Mystery"]', 'Survivors of a heist are questioned.', ?1, ?1),
			(13, 1, 'movie', 'm13', 'Ocean''s Eleven', 'oceans eleven', 2001, 116, 7.5, '["Crime","Comedy"]', 'A casino heist.', ?1, ?1),
			(14, 1, 'movie', 'm14', 'Point Break', 'point break', 1991, 122, 7.0, '["Action","Crime"]', 'Surfing bank robbers.', ?1, ?1),
			(15, 3, 'movie', 'm15', 'Reservoir Dogs', 'reservoir dogs', 1992, 99, 8.0, '["Crime","Thriller"]', 'A jewel heist goes wrong.', ?1, ?1),
			(30, 1, 'show', 's30', 'Severance', 'severance', 2022, 50, 8.7, '["Drama","Mystery","Sci-Fi & Fantasy"]', 'Office workers split memories.', ?1, ?1),
			(31, 1, 'show', 's31', 'Dark', 'dark', 2017, 55, 8.4, '["Drama","Mystery","Sci-Fi & Fantasy"]', 'Time travel in a small town.', ?1, ?1),
			(32, 1, 'show', 's32', 'Friends', 'friends', 1994, 22, 8.0, '["Comedy"]', 'Six friends in New York.', ?1, ?1)`,
		// Reservoir Dogs has been watched.
		`INSERT INTO watch_state(user_id, item_id, watched, updated_at) VALUES (1, 11, 1, ?1), (1, 15, 1, ?1)`,
	} {
		if _, err := s.db.W.Exec(q, now); err != nil {
			t.Fatal(q, err)
		}
	}
	return s, func(q string) askResult {
		var res askResult
		if code := call(t, s, u, s.handleAsk, "GET", "/api/ask?q="+strings.ReplaceAll(q, " ", "+"), "", nil, &res); code != 200 {
			t.Fatalf("ask %q = %d", q, code)
		}
		return res
	}
}

func titles(cs []Card) []string {
	out := []string{}
	for _, c := range cs {
		out = append(out, c.Title)
	}
	return out
}

func TestAskBasic(t *testing.T) {
	s, askq := askServer(t)
	s.claude = newTestClaude("", "")

	res := askq("90s heist films under two hours I haven't seen")
	if res.Engine != "basic" {
		t.Fatalf("engine = %s", res.Engine)
	}
	// Heat is too long, Reservoir Dogs is watched, Ocean's Eleven is 2001.
	if got := strings.Join(titles(res.Matches), ","); got != "The Usual Suspects" {
		t.Errorf("matches = %s", got)
	}

	// Like Severance: shares two genres, not Severance itself, shows only.
	res = askq("something like severance")
	if got := strings.Join(titles(res.Matches), ","); got != "Dark" {
		t.Errorf("like severance = %s", got)
	}

	// Gibberish shouldn't dump the whole library.
	res = askq("zzqx")
	if len(res.Matches) != 0 {
		t.Errorf("gibberish matched %v", titles(res.Matches))
	}
}

func TestAskClaude(t *testing.T) {
	s, askq := askServer(t)
	var body map[string]any
	answer := `{"kinds":["movie"],"genres":["Crime"],"excludeGenres":[],"yearFrom":1990,"yearTo":1999,"minRuntime":0,"maxRuntime":120,
		"minRating":0,"watched":"unwatched","keywords":["heist","robbery"],"similarTo":"",
		"titles":[{"title":"Reservoir Dogs","year":1992},{"title":"Point Break","year":1991},{"title":"Heat","year":1995},{"title":"Sneakers","year":1992}],
		"sort":"relevance","summary":"90s heist films under two hours"}`
	s.claude = newTestClaude("sk-test", answer, &body)

	res := askq("90s heist films under two hours I haven't seen")
	if res.Engine != "claude" || res.Summary != "90s heist films under two hours" {
		t.Fatalf("engine = %s, summary = %q, notice = %q", res.Engine, res.Summary, res.Notice)
	}
	// Picks keep Claude's order, skip the watched one and the ones not in the
	// library; Heat is a pick even though it runs long (Claude's call).
	if got := strings.Join(titles(res.Picks), ","); got != "Point Break,Heat" {
		t.Errorf("picks = %s", got)
	}
	if got := strings.Join(titles(res.Matches), ","); got != "The Usual Suspects" {
		t.Errorf("matches = %s", got)
	}
	if body["model"] != ask.DefaultModel {
		t.Errorf("model = %v", body["model"])
	}
	if body["fallbacks"] != "default" {
		t.Errorf("fallbacks = %v", body["fallbacks"])
	}
	oc, _ := body["output_config"].(map[string]any)
	if f, _ := oc["format"].(map[string]any); f["type"] != "json_schema" {
		t.Errorf("output_config = %v", oc)
	}
	sys, _ := json.Marshal(body["system"])
	if !strings.Contains(string(sys), "Sci-Fi \\u0026 Fantasy") && !strings.Contains(string(sys), "Sci-Fi & Fantasy") {
		t.Errorf("system prompt lacks the library genres: %s", sys)
	}
}

func TestAskClaudeErrorFallsBack(t *testing.T) {
	s, askq := askServer(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`)
	}))
	t.Cleanup(srv.Close)
	s.claude = askNewClaude("sk-bad", srv.URL)
	res := askq("90s heist films under two hours I haven't seen")
	if res.Engine != "basic" || !strings.Contains(res.Notice, "invalid x-api-key") {
		t.Fatalf("engine = %s notice = %q", res.Engine, res.Notice)
	}
	if len(res.Matches) != 1 {
		t.Errorf("fallback matches = %v", titles(res.Matches))
	}
}

func askNewClaude(key, url string) *ask.Claude {
	return ask.NewClaude(key, "", option.WithBaseURL(url))
}

// newTestClaude returns a translator backed by a fake Messages API that
// answers with answer, recording the request body into rec.
func newTestClaude(key, answer string, rec ...*map[string]any) *ask.Claude {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if len(rec) > 0 {
			json.Unmarshal(b, rec[0])
		}
		msg, _ := json.Marshal(map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-opus-5-5",
			"content":     []map[string]any{{"type": "text", "text": answer}},
			"stop_reason": "end_turn", "usage": map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
		w.Header().Set("Content-Type", "application/json")
		w.Write(msg)
	}))
	return askNewClaude(key, srv.URL)
}
