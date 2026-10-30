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
	r, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(8)
	r.SetConnMaxIdleTime(5 * time.Minute)
	return &DB{W: w, R: r}, nil
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
