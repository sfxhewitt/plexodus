// Package db opens the SQLite database and applies the schema.
//
// Writes go through a single connection (SQLite allows one writer at a time),
// while reads use a separate pool so browsing never queues behind a scan.
package db

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

type DB struct {
	W *sql.DB // single writer
	R *sql.DB // reader pool

	titlesReady atomic.Bool // titles_fts is filled (see TitleFTS)
}

func dsn(path string, readOnly bool) string {
	d := "file:" + path + "?_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)&_pragma=mmap_size(268435456)&_pragma=cache_size(-65536)"
	if readOnly {
		d += "&mode=ro"
	}
	return d
}

func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	w, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1)
	if _, err := w.Exec(schema); err != nil {
		w.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(w); err != nil {
		w.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	r, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(8)
	r.SetConnMaxIdleTime(5 * time.Minute)
	d := &DB{W: w, R: r}
	if d.Setting(context.Background(), titleIndexSetting) != "" {
		d.titlesReady.Store(true)
	} else {
		// A new library: the triggers keep the index complete from the start.
		var n int
		if err := w.QueryRow(`SELECT COUNT(*) FROM (SELECT 1 FROM items WHERE kind IN ('movie', 'show') LIMIT 1)`).Scan(&n); err == nil && n == 0 {
			if d.SetSetting(context.Background(), titleIndexSetting, "1") == nil {
				d.titlesReady.Store(true)
			}
		}
	}
	return d, nil
}

const titleIndexSetting = "titles_fts_v1"

// TitleFTS names the full-text index to search films and shows with:
// titles_fts, or items_fts (which has every episode too) until
// BuildTitleIndex has filled titles_fts. Both have the same columns.
func (d *DB) TitleFTS() string {
	if d.titlesReady.Load() {
		return "titles_fts"
	}
	return "items_fts"
}

// BuildTitleIndex fills titles_fts on a library that predates it. It does
// nothing once that is done.
func (d *DB) BuildTitleIndex(ctx context.Context) error {
	if d.titlesReady.Load() {
		return nil
	}
	// Read the rows first, on the read side: that is the slow part on a
	// busy disk, and it leaves them cached for the write below, which then
	// holds the write lock only briefly.
	var n, size int64
	if err := d.R.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(length(title) + length(COALESCE(original_title, '')) + length(COALESCE(overview, ''))), 0)
		FROM items WHERE kind IN ('movie', 'show')`).Scan(&n, &size); err != nil {
		return err
	}
	err := d.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO titles_fts(titles_fts) VALUES ('delete-all')`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO titles_fts(rowid, title, original_title, overview)
			SELECT id, title, original_title, overview FROM items WHERE kind IN ('movie', 'show')`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES (?, '1') ON CONFLICT(key) DO UPDATE SET value = excluded.value`, titleIndexSetting)
		return err
	})
	if err != nil {
		return err
	}
	d.titlesReady.Store(true)
	return nil
}

// migrate applies column additions that CREATE TABLE IF NOT EXISTS can't.
func migrate(w *sql.DB) error {
	columns := []struct{ table, column, def string }{
		{"users", "all_libraries", "INTEGER NOT NULL DEFAULT 1"},
		// Profiles: extra viewers under one account (see auth.Profile).
		{"users", "parent_id", "INTEGER REFERENCES users(id) ON DELETE CASCADE"},
		{"users", "display_name", "TEXT"},
		{"users", "avatar", "TEXT"},
		{"users", "color", "TEXT"},
		{"users", "max_age", "INTEGER"},
		{"users", "pin_hash", "TEXT"},
		{"sessions", "profile_id", "INTEGER REFERENCES users(id) ON DELETE SET NULL"},
		// Content rating ("PG-13") and the minimum age it maps to, for kids profiles.
		{"items", "content_rating", "TEXT"},
		{"items", "age", "INTEGER"},
		{"markers", "source", "TEXT NOT NULL DEFAULT 'plex'"}, // plex | chapter | audio
		// Sign in with Plex: the plex.tv account linked to a Plexodus account.
		{"users", "plex_id", "INTEGER"},
		{"users", "plex_username", "TEXT"},
		// 1 when an admin chose a Plex friend's libraries in Plexodus, so
		// the hourly Plex sharing check leaves them alone.
		{"users", "access_custom", "INTEGER NOT NULL DEFAULT 0"},
		// Invite codes typed into the app, and invites usable more than once
		// (max_uses 0 = any number, e.g. an app store reviewer's code).
		{"invites", "code", "TEXT"},
		{"invites", "max_uses", "INTEGER NOT NULL DEFAULT 1"},
		{"invites", "uses", "INTEGER NOT NULL DEFAULT 0"},
		// TMDB collection (franchise) a film belongs to; 0 = none, NULL = not asked yet.
		{"items", "collection_id", "INTEGER"},
		// Collections from Plex as well as TMDB (see collection_items).
		{"collections", "source", "TEXT NOT NULL DEFAULT 'tmdb'"}, // tmdb | plex
		{"collections", "plex_key", "TEXT"},                       // Plex ratingKey
		{"collections", "kind", "TEXT NOT NULL DEFAULT 'movie'"},  // movie | show
		{"collections", "summary", "TEXT"},
		{"collections", "smart", "INTEGER NOT NULL DEFAULT 0"},
		// When TMDB was last asked for artwork this collection lacks.
		{"collections", "art_checked_at", "INTEGER"},
		// When the item was last actually played (here or in Plex), however
		// briefly, for History. Marking watched by hand leaves it alone; 0 =
		// never played or removed from History.
		{"watch_state", "played_at", "INTEGER NOT NULL DEFAULT 0"},
		// Plex's guid for a linked title when last seen, so a re-match in
		// Plex ("Fix match") can be noticed and copied over.
		{"plex_links", "plex_guid", "TEXT"},
		// 1 when an admin fixed the match in Plexodus.
		{"items", "match_manual", "INTEGER NOT NULL DEFAULT 0"},
		// Rotten Tomatoes scores (0–100) copied from Plex: the critics'
		// Tomatometer and the audience score. NULL = not known.
		{"items", "rt_critic", "INTEGER"},
		{"items", "rt_audience", "INTEGER"},
		// Which version of the scene companion wrote a stretch of notes
		// (1 = from subtitles only, 2 = also shown stills of the picture).
		{"companion_notes", "v", "INTEGER NOT NULL DEFAULT 1"},
	}
	for _, c := range columns {
		var n int
		if err := w.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, c.table, c.column).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := w.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", c.table, c.column, c.def)); err != nil {
				return err
			}
		}
	}
	if _, err := w.Exec(`CREATE INDEX IF NOT EXISTS users_parent ON users(parent_id)`); err != nil {
		return err
	}
	if _, err := w.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS users_plex ON users(plex_id) WHERE plex_id IS NOT NULL`); err != nil {
		return err
	}
	// Collections: the films in each, with what the access and kids checks
	// read, so the Collections tab never touches the items table itself.
	// Replaces items_collection, which had collection_id alone.
	// v2 adds what tells copies of one film apart from different films.
	if _, err := w.Exec(`CREATE INDEX IF NOT EXISTS items_collection_films_v2 ON items(collection_id, kind, library_id, age, parent_id, tmdb_id, title, year) WHERE collection_id > 0`); err != nil {
		return err
	}
	for _, old := range []string{"items_collection", "items_collection_films"} {
		if _, err := w.Exec(`DROP INDEX IF EXISTS ` + old); err != nil {
			return err
		}
	}
	// Films still waiting for their collection to be looked up.
	if _, err := w.Exec(`CREATE INDEX IF NOT EXISTS items_collection_pending ON items(kind) WHERE collection_id IS NULL AND tmdb_id > 0`); err != nil {
		return err
	}
	// "More like this": TMDB recommendations mapped to local titles.
	if _, err := w.Exec(`CREATE INDEX IF NOT EXISTS items_tmdb ON items(tmdb_id) WHERE tmdb_id IS NOT NULL`); err != nil {
		return err
	}
	if _, err := w.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS invites_code ON invites(code) WHERE code IS NOT NULL`); err != nil {
		return err
	}
	// Invites used before uses was counted.
	if _, err := w.Exec(`UPDATE invites SET uses = 1 WHERE used_by IS NOT NULL AND uses = 0`); err != nil {
		return err
	}
	if _, err := w.Exec(`CREATE INDEX IF NOT EXISTS watch_state_played ON watch_state(user_id, played_at DESC) WHERE played_at > 0`); err != nil {
		return err
	}
	if err := seedHistory(w); err != nil {
		return err
	}
	return seedPlayedAt(w)
}

func (d *DB) Close() error {
	d.R.Close()
	return d.W.Close()
}

// Tx runs fn inside a write transaction.
func (d *DB) Tx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.W.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (d *DB) Setting(ctx context.Context, key string) string {
	var v string
	d.R.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	return v
}

func (d *DB) SetSetting(ctx context.Context, key, value string) error {
	_, err := d.W.ExecContext(ctx, `INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func Now() int64 { return time.Now().Unix() }
