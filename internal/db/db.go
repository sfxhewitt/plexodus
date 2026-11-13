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
	"time"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schema string

type DB struct {
	W *sql.DB // single writer
	R *sql.DB // reader pool
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
	return &DB{W: w, R: r}, nil
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
	return seedHistory(w)
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
