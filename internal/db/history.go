package db

import (
	"context"
	"database/sql"
)

// Execer is a *sql.DB or *sql.Tx.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// PlayGap is how close two plays of one item must be to count as the same
// view: progress keeps reporting "watched" through the end credits, and Plex
// and Plexodus can both report a view.
const PlayGap = 6 * 3600

// RecordPlay adds a completed play to watch_history unless one for the same
// item is already within PlayGap of it. seconds may be 0 (unknown).
func RecordPlay(ctx context.Context, x Execer, userID, itemID, at int64, seconds float64, source string) error {
	_, err := x.ExecContext(ctx, `INSERT INTO watch_history(user_id, item_id, watched_at, seconds, source)
		SELECT ?1, ?2, ?3, ?4, ?5 WHERE EXISTS (SELECT 1 FROM items WHERE id = ?2)
		AND NOT EXISTS (SELECT 1 FROM watch_history WHERE user_id = ?1 AND item_id = ?2
			AND watched_at BETWEEN ?3 - ?6 AND ?3 + ?6)`,
		userID, itemID, at, seconds, source, PlayGap)
	return err
}

// seedHistory fills watch_history once from watch_state, so plays made
// before the table existed still count. Rows with no duration were marked
// watched by hand (or by a bulk "mark season watched"), not played, so they
// are left out.
func seedHistory(w *sql.DB) error {
	var done string
	w.QueryRow(`SELECT value FROM settings WHERE key = 'history_seeded'`).Scan(&done)
	if done != "" {
		return nil
	}
	if _, err := w.Exec(`INSERT INTO watch_history(user_id, item_id, watched_at, seconds, source)
		SELECT ws.user_id, ws.item_id, ws.updated_at, ws.duration, 'state' FROM watch_state ws
		WHERE ws.watched = 1 AND ws.duration > 0
		AND NOT EXISTS (SELECT 1 FROM watch_history h WHERE h.user_id = ws.user_id AND h.item_id = ws.item_id)`); err != nil {
		return err
	}
	_, err := w.Exec(`INSERT INTO settings(key, value) VALUES('history_seeded', '1')
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
	return err
}
