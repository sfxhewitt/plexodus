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
	_, err := w.Exec(`CREATE INDEX IF NOT EXISTS users_parent ON users(parent_id)`)
	return err
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
