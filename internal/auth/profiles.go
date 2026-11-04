package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"

	"github.com/sfxhewitt/plexodus/internal/db"
)

// Profiles are extra viewers under one account (a household): each has its
// own watch history, watchlist and library choices, but signs in through
// the account. A profile is a row in users with parent_id set to the
// account; it has no password and can't sign in on its own. A kids profile
// (max_age set) only sees titles rated for its age.

const MaxProfiles = 8

type Profile struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Avatar    string `json:"avatar"`
	Color     string `json:"color"`
	MaxAge    *int   `json:"maxAge,omitempty"`
	Kids      bool   `json:"kids"`
	HasPIN    bool   `json:"hasPin"`
	IsAccount bool   `json:"isAccount"`
}

// ProfileInput carries changes; nil fields are left as they are.
type ProfileInput struct {
	Name   *string `json:"name"`
	Avatar *string `json:"avatar"`
	Color  *string `json:"color"`
	// Kids turns kids mode on (with MaxAge) or off.
	Kids   *bool `json:"kids"`
	MaxAge *int  `json:"maxAge"`
	// PIN sets a 4-digit PIN; "" removes it.
	PIN *string `json:"pin"`
}

var (
	ErrPIN         = errors.New("wrong PIN")
	ErrNoProfile   = errors.New("profile not found")
	errPINFormat   = errors.New("the PIN must be 4 digits")
	errName        = errors.New("give the profile a name (up to 24 characters)")
	errTooMany     = fmt.Errorf("an account can have up to %d profiles", MaxProfiles)
	errAccountKids = errors.New("the main profile can't be a kids profile")
)

// Profiles lists the account (first) and its profiles.
func (a *Auth) Profiles(ctx context.Context, accountID int64) ([]Profile, error) {
	rows, err := a.db.R.QueryContext(ctx, `SELECT id, COALESCE(NULLIF(display_name, ''), username), COALESCE(avatar, ''),
		COALESCE(color, ''), max_age, COALESCE(pin_hash, '') != '', parent_id IS NULL
		FROM users WHERE id = ?1 OR parent_id = ?1 ORDER BY parent_id IS NOT NULL, id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Profile{}
	for rows.Next() {
		var p Profile
		var maxAge sql.NullInt64
		if err := rows.Scan(&p.ID, &p.Name, &p.Avatar, &p.Color, &maxAge, &p.HasPIN, &p.IsAccount); err != nil {
			return nil, err
		}
		if maxAge.Valid {
			n := int(maxAge.Int64)
			p.MaxAge, p.Kids = &n, true
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func validPIN(pin string) bool {
	if len(pin) != 4 {
		return false
	}
	for _, c := range pin {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func cleanName(n string) (string, error) {
	n = strings.TrimSpace(n)
	if n == "" || utf8.RuneCountInString(n) > 24 {
		return "", errName
	}
	return n, nil
}

// CreateProfile adds a profile to an account.
func (a *Auth) CreateProfile(ctx context.Context, accountID int64, in ProfileInput) (Profile, error) {
	if in.Name == nil {
		return Profile{}, errName
	}
	name, err := cleanName(*in.Name)
	if err != nil {
		return Profile{}, err
	}
	var n int
	a.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE parent_id = ?`, accountID).Scan(&n)
	if n >= MaxProfiles {
		return Profile{}, errTooMany
	}
	// Profiles can't sign in, but username is unique and required, so give
	// them one nobody can type at the login screen.
	res, err := a.db.W.ExecContext(ctx, `INSERT INTO users(username, password_hash, is_admin, created_at, parent_id, display_name, all_libraries)
		VALUES(?, '', 0, ?, ?, ?, 1)`, "profile:"+randomToken(8), db.Now(), accountID, name)
	if err != nil {
		return Profile{}, err
	}
	id, _ := res.LastInsertId()
	in.Name = nil
	if err := a.UpdateProfile(ctx, accountID, id, in); err != nil {
		a.db.W.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
		return Profile{}, err
	}
	return a.profile(ctx, accountID, id)
}

func (a *Auth) profile(ctx context.Context, accountID, id int64) (Profile, error) {
	ps, err := a.Profiles(ctx, accountID)
	if err != nil {
		return Profile{}, err
	}
	for _, p := range ps {
		if p.ID == id {
			return p, nil
		}
	}
	return Profile{}, ErrNoProfile
}

// UpdateProfile changes a profile, or the account's own name, look and PIN.
func (a *Auth) UpdateProfile(ctx context.Context, accountID, id int64, in ProfileInput) error {
	p, err := a.profile(ctx, accountID, id)
	if err != nil {
		return err
	}
	sets := []string{}
	args := []any{}
	if in.Name != nil {
		name, err := cleanName(*in.Name)
		if err != nil {
			return err
		}
		sets, args = append(sets, "display_name = ?"), append(args, name)
	}
	if in.Avatar != nil {
		sets, args = append(sets, "avatar = ?"), append(args, limit(*in.Avatar, 16))
	}
	if in.Color != nil {
		sets, args = append(sets, "color = ?"), append(args, limit(*in.Color, 16))
	}
	if in.Kids != nil || in.MaxAge != nil {
		kids := p.Kids
		if in.Kids != nil {
			kids = *in.Kids
		}
		if kids && p.IsAccount {
			return errAccountKids
		}
		var age any
		if kids {
			n := 8
			if p.MaxAge != nil {
				n = *p.MaxAge
			}
			if in.MaxAge != nil {
				n = *in.MaxAge
			}
			if n < 0 || n > 18 {
				return errors.New("age limit must be between 0 and 18")
			}
			age = n
		}
		sets, args = append(sets, "max_age = ?"), append(args, age)
	}
	if in.PIN != nil {
		var hash any
		if *in.PIN != "" {
			if !validPIN(*in.PIN) {
				return errPINFormat
			}
			h, err := bcrypt.GenerateFromPassword([]byte(*in.PIN), bcrypt.DefaultCost)
			if err != nil {
				return err
			}
			hash = string(h)
		}
		sets, args = append(sets, "pin_hash = ?"), append(args, hash)
	}
	if len(sets) == 0 {
		return nil
	}
	args = append(args, id)
	_, err = a.db.W.ExecContext(ctx, `UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	a.Forget()
	return err
}

// DeleteProfile removes a profile and its watch history.
func (a *Auth) DeleteProfile(ctx context.Context, accountID, id int64) error {
	res, err := a.db.W.ExecContext(ctx, `DELETE FROM users WHERE id = ? AND parent_id = ?`, id, accountID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoProfile
	}
	a.Forget()
	return nil
}

// SwitchProfile makes a profile (or the account itself) active on this
// session. The target's PIN is needed if it has one, except when the
// account itself is switching to one of its own profiles.
func (a *Auth) SwitchProfile(ctx context.Context, token string, current User, target int64, pin string) (User, error) {
	var hash string
	err := a.db.R.QueryRowContext(ctx, `SELECT COALESCE(pin_hash, '') FROM users WHERE id = ? AND (id = ?2 OR parent_id = ?2)`,
		target, current.AccountID).Scan(&hash)
	if err != nil {
		return User{}, ErrNoProfile
	}
	fromAccount := !current.IsProfile && target != current.AccountID
	if hash != "" && !fromAccount && target != current.ID {
		if bcrypt.CompareHashAndPassword([]byte(hash), []byte(pin)) != nil {
			return User{}, ErrPIN
		}
	}
	var pid any
	if target != current.AccountID {
		pid = target
	}
	if _, err := a.db.W.ExecContext(ctx, `UPDATE sessions SET profile_id = ? WHERE token = ?`, pid, token); err != nil {
		return User{}, err
	}
	a.mu.Lock()
	delete(a.cache, token)
	a.mu.Unlock()
	u, ok := a.Lookup(ctx, token)
	if !ok {
		return User{}, errors.New("session expired")
	}
	return u, nil
}

func limit(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > n {
		return string([]rune(s)[:n])
	}
	return s
}

func randomToken(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}
