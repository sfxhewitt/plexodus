package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/sfxhewitt/plexodus/internal/db"
)

// PlexAccount is a Plexodus account linked to a plex.tv account.
type PlexAccount struct {
	UserID  int64
	IsAdmin bool
}

// ByPlex finds the account linked to a plex.tv user id.
func (a *Auth) ByPlex(ctx context.Context, plexID int64) (PlexAccount, bool) {
	var p PlexAccount
	err := a.db.R.QueryRowContext(ctx, `SELECT id, is_admin FROM users WHERE plex_id = ? AND parent_id IS NULL`, plexID).
		Scan(&p.UserID, &p.IsAdmin)
	return p, err == nil
}

// FirstAdmin is the account that set the server up.
func (a *Auth) FirstAdmin(ctx context.Context) (int64, error) {
	var id int64
	err := a.db.R.QueryRowContext(ctx, `SELECT id FROM users WHERE is_admin = 1 AND parent_id IS NULL ORDER BY id LIMIT 1`).Scan(&id)
	return id, err
}

// LinkPlex ties an account to a plex.tv account (replacing any earlier link).
func (a *Auth) LinkPlex(ctx context.Context, userID, plexID int64, plexName string) error {
	return a.db.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`UPDATE users SET plex_id = NULL WHERE plex_id = ? AND id <> ?`, plexID, userID); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE users SET plex_id = ?, plex_username = ? WHERE id = ?`, plexID, plexName, userID)
		return err
	})
}

// CreatePlexUser makes a non-admin account for a Plex friend. It has a
// random password, so it can only be used through Sign in with Plex until
// an admin sets a password.
func (a *Auth) CreatePlexUser(ctx context.Context, name string, plexID int64) (int64, error) {
	base := strings.TrimSpace(name)
	if base == "" {
		base = fmt.Sprintf("plex%d", plexID)
	}
	base = limit(base, 40)
	hash, err := bcrypt.GenerateFromPassword([]byte(randomToken(24)), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}
	for i := 1; i < 50; i++ {
		username := base
		if i > 1 {
			username = fmt.Sprintf("%s %d", base, i)
		}
		res, err := a.db.W.ExecContext(ctx, `INSERT INTO users(username, password_hash, is_admin, created_at, plex_id, plex_username, all_libraries)
			VALUES(?, ?, 0, ?, ?, ?, 0)`, username, string(hash), db.Now(), plexID, name)
		if err != nil {
			if strings.Contains(err.Error(), "UNIQUE") && strings.Contains(err.Error(), "username") {
				continue
			}
			return 0, err
		}
		return res.LastInsertId()
	}
	return 0, errors.New("couldn't pick a username")
}

// SignIn starts a session for an account that has been verified some other
// way (Sign in with Plex).
func (a *Auth) SignIn(ctx context.Context, userID int64) (string, User, error) {
	u, err := a.viewer(ctx, userID, 0)
	if err != nil {
		return "", User{}, err
	}
	tok, err := a.newSession(ctx, userID)
	return tok, u, err
}

// EndSessions signs an account out everywhere.
func (a *Auth) EndSessions(ctx context.Context, userID int64) {
	a.db.W.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	a.Forget()
}

// PlexFriends lists non-admin accounts linked to Plex: user id -> plex id.
func (a *Auth) PlexFriends(ctx context.Context) (map[int64]PlexFriend, error) {
	rows, err := a.db.R.QueryContext(ctx, `SELECT id, plex_id, access_custom FROM users
		WHERE plex_id IS NOT NULL AND is_admin = 0 AND parent_id IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[int64]PlexFriend{}
	for rows.Next() {
		var id int64
		var f PlexFriend
		rows.Scan(&id, &f.PlexID, &f.Custom)
		m[id] = f
	}
	return m, rows.Err()
}

// PlexFriend is a Plex-linked account: Custom is set once an admin picked
// its libraries in Plexodus instead of following the Plex share.
type PlexFriend struct {
	PlexID int64
	Custom bool
}

// AccessCustom reports whether an admin overrode the account's Plex share.
func (a *Auth) AccessCustom(ctx context.Context, userID int64) bool {
	var c bool
	a.db.R.QueryRowContext(ctx, `SELECT access_custom FROM users WHERE id = ?`, userID).Scan(&c)
	return c
}

// SetAccessCustom marks whether the account's libraries are chosen in
// Plexodus (true) or follow its Plex share (false).
func (a *Auth) SetAccessCustom(ctx context.Context, userID int64, custom bool) error {
	_, err := a.db.W.ExecContext(ctx, `UPDATE users SET access_custom = ? WHERE id = ?`, custom, userID)
	return err
}
