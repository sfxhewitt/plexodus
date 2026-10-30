// Package auth handles users, password hashing and login sessions.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/sfxhewitt/plexodus/internal/db"
)

const (
	CookieName  = "plexodus_session"
	sessionTTL  = 90 * 24 * time.Hour
	minPassword = 6
)

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	IsAdmin  bool   `json:"isAdmin"`
}

type Auth struct {
	db *db.DB
	// Small cache so every request doesn't hit SQLite for its session.
	mu    sync.RWMutex
	cache map[string]cached
}

type cached struct {
	user    User
	expires time.Time
}

func New(d *db.DB) *Auth { return &Auth{db: d, cache: map[string]cached{}} }

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("plexodus"), bcrypt.DefaultCost)

var (
	ErrInvalid  = errors.New("invalid username or password")
	ErrWeakPass = errors.New("password must be at least 6 characters")
)

func (a *Auth) HasUsers(ctx context.Context) bool {
	var n int
	a.db.R.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n > 0
}

func (a *Auth) CreateUser(ctx context.Context, username, password string, admin bool) (User, error) {
	username = strings.TrimSpace(username)
	if username == "" {
		return User{}, errors.New("username is required")
	}
	if len(password) < minPassword {
		return User{}, ErrWeakPass
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return User{}, err
	}
	res, err := a.db.W.ExecContext(ctx, `INSERT INTO users(username, password_hash, is_admin, created_at) VALUES(?, ?, ?, ?)`,
		username, string(hash), admin, db.Now())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return User{}, errors.New("that username is taken")
		}
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return User{ID: id, Username: username, IsAdmin: admin}, nil
}

func (a *Auth) SetPassword(ctx context.Context, userID int64, password string) error {
	if len(password) < minPassword {
		return ErrWeakPass
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = a.db.W.ExecContext(ctx, `UPDATE users SET password_hash = ? WHERE id = ?`, string(hash), userID)
	return err
}

func (a *Auth) Login(ctx context.Context, username, password string) (string, User, error) {
	var u User
	var hash string
	err := a.db.R.QueryRowContext(ctx, `SELECT id, username, is_admin, password_hash FROM users WHERE username = ?`,
		strings.TrimSpace(username)).Scan(&u.ID, &u.Username, &u.IsAdmin, &hash)
	if err != nil {
		// Spend similar time as a real check to avoid user enumeration.
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return "", User{}, ErrInvalid
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return "", User{}, ErrInvalid
	}
	tok, err := a.newSession(ctx, u.ID)
	return tok, u, err
}

func (a *Auth) newSession(ctx context.Context, userID int64) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	now := time.Now()
	_, err := a.db.W.ExecContext(ctx, `INSERT INTO sessions(token, user_id, created_at, expires_at) VALUES(?, ?, ?, ?)`,
		tok, userID, now.Unix(), now.Add(sessionTTL).Unix())
	return tok, err
}

func (a *Auth) Logout(ctx context.Context, token string) {
	a.mu.Lock()
	delete(a.cache, token)
	a.mu.Unlock()
	a.db.W.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
}

// Forget drops cached sessions (after a user is deleted or changed).
func (a *Auth) Forget() {
	a.mu.Lock()
	a.cache = map[string]cached{}
	a.mu.Unlock()
}

func (a *Auth) Lookup(ctx context.Context, token string) (User, bool) {
	if token == "" {
		return User{}, false
	}
	a.mu.RLock()
	c, ok := a.cache[token]
	a.mu.RUnlock()
	if ok && time.Now().Before(c.expires) {
		return c.user, true
	}
	var u User
	var exp int64
	err := a.db.R.QueryRowContext(ctx, `SELECT u.id, u.username, u.is_admin, s.expires_at FROM sessions s
		JOIN users u ON u.id = s.user_id WHERE s.token = ?`, token).Scan(&u.ID, &u.Username, &u.IsAdmin, &exp)
	if err != nil || time.Now().Unix() > exp {
		return User{}, false
	}
	a.mu.Lock()
	a.cache[token] = cached{user: u, expires: time.Now().Add(time.Minute)}
	a.mu.Unlock()
	return u, true
}

func TokenFrom(r *http.Request) string {
	if c, err := r.Cookie(CookieName); err == nil {
		return c.Value
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

type ctxKey struct{}

func WithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, ctxKey{}, u)
}

func UserFrom(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(ctxKey{}).(User)
	return u, ok
}

func (a *Auth) PruneSessions(ctx context.Context) {
	a.db.W.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, time.Now().Unix())
}
