// Package auth handles the single-administrator login: password hashing,
// session cookies, CSRF tokens and login throttling (FR-01).
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/wisnu/ylivemasadi/internal/store"
)

// Argon2id parameters. Tuned for a 2 GB ARM box: ~64 MiB, ~50 ms per hash.
const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
	saltLen      = 16
)

// Service manages users and sessions.
type Service struct {
	st         *store.Store
	sessionTTL time.Duration
	secure     bool
	// cooldown tracks failed logins per username to throttle guessing.
	failures map[string]*loginFailures
}

// New builds an auth service.
func New(st *store.Store, sessionTTL time.Duration, secure bool) *Service {
	return &Service{st: st, sessionTTL: sessionTTL, secure: secure, failures: map[string]*loginFailures{}}
}

type loginFailures struct {
	count       int
	lockedUntil time.Time
}

// maxAttempts before a temporary lock. Chosen so a human rarely trips it.
const maxAttempts = 5

// lockoutFor grows with repeated failures to slow automated guessing.
func lockoutFor(attempts int) time.Duration {
	switch {
	case attempts >= 10:
		return 15 * time.Minute
	case attempts >= maxAttempts:
		return 5 * time.Minute
	default:
		return 0
	}
}

// HasAdmin reports whether the first-run setup has created an account.
func (s *Service) HasAdmin() (bool, error) {
	var n int
	err := s.st.DB().QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// CreateAdmin creates the single administrator. It refuses if one exists so
// the setup flow cannot be replayed.
func (s *Service) CreateAdmin(username, password string) error {
	if strings.TrimSpace(username) == "" {
		return errors.New("username is required")
	}
	if len(password) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	exists, err := s.HasAdmin()
	if err != nil {
		return err
	}
	if exists {
		return errors.New("administrator already exists")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err = s.st.DB().Exec(
		`INSERT INTO users (username, password_hash, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		username, hash, now, now,
	)
	if err != nil {
		return fmt.Errorf("create admin: %w", err)
	}
	return nil
}

// HashPassword returns a self-describing argon2id hash string.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword checks a password against a stored argon2id hash.
func VerifyPassword(stored, password string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory uint32
	var time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	// Constant-time compare; length difference is already a mismatch.
	if len(got) != len(want) {
		return false
	}
	var diff byte
	for i := range got {
		diff |= got[i] ^ want[i]
	}
	return diff == 0
}

// ChangePassword replaces the stored hash after verifying the current one.
func (s *Service) ChangePassword(userID int64, current, next string) error {
	if len(next) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	var stored string
	if err := s.st.DB().QueryRow(`SELECT password_hash FROM users WHERE id = ?`, userID).Scan(&stored); err != nil {
		return err
	}
	if !VerifyPassword(stored, current) {
		return errors.New("current password is incorrect")
	}
	hash, err := HashPassword(next)
	if err != nil {
		return err
	}
	_, err = s.st.DB().Exec(`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`,
		hash, time.Now().UTC().Format(time.RFC3339Nano), userID)
	return err
}

// Authenticate verifies credentials and returns the user id, applying the
// attempt throttle.
func (s *Service) Authenticate(username, password string) (int64, error) {
	if f, ok := s.failures[username]; ok {
		if time.Now().Before(f.lockedUntil) {
			return 0, fmt.Errorf("too many failed attempts, retry after %s", time.Until(f.lockedUntil).Round(time.Second))
		}
	}
	var id int64
	var stored string
	err := s.st.DB().QueryRow(`SELECT id, password_hash FROM users WHERE username = ?`, username).Scan(&id, &stored)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Still spend a hash to avoid leaking account existence by timing.
			_, _ = HashPassword(password)
			return 0, errors.New("invalid username or password")
		}
		return 0, err
	}
	if !VerifyPassword(stored, password) {
		s.registerFailure(username)
		return 0, errors.New("invalid username or password")
	}
	delete(s.failures, username)
	return id, nil
}

func (s *Service) registerFailure(username string) {
	f, ok := s.failures[username]
	if !ok {
		f = &loginFailures{}
		s.failures[username] = f
	}
	f.count++
	f.lockedUntil = time.Now().Add(lockoutFor(f.count))
}

// Session is an authenticated browser session.
type Session struct {
	Token string
	// CSRFToken is the raw secret handed to the browser once at login; only its
	// hash is stored, so it is empty on later lookups.
	CSRFToken string
	// CSRFHash is the stored hash used to validate the X-CSRF-Token header.
	CSRFHash  string
	UserID    int64
	Username  string
	ExpiresAt time.Time
}

// ValidCSRF reports whether the submitted token matches the session's secret.
func (s *Session) ValidCSRF(submitted string) bool {
	if s.CSRFHash == "" || submitted == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hashToken(submitted)), []byte(s.CSRFHash)) == 1
}

// CreateSession issues a new session bound to the requesting client.
func (s *Service) CreateSession(userID int64, r *http.Request) (*Session, error) {
	token, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	csrf, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	expires := now.Add(s.sessionTTL)
	if _, err := s.st.DB().Exec(
		`INSERT INTO auth_sessions (token_hash, user_id, csrf_token, created_at, last_seen_at, expires_at, ip, user_agent)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		hashToken(token), userID, hashToken(csrf), now.Format(time.RFC3339Nano),
		now.Format(time.RFC3339Nano), expires.Format(time.RFC3339Nano),
		clientIP(r), truncate(r.UserAgent(), 300),
	); err != nil {
		return nil, err
	}
	var username string
	if err := s.st.DB().QueryRow(`SELECT username FROM users WHERE id = ?`, userID).Scan(&username); err != nil {
		return nil, err
	}
	return &Session{
		Token:     token,
		CSRFToken: csrf,
		CSRFHash:  hashToken(csrf),
		UserID:    userID,
		Username:  username,
		ExpiresAt: expires,
	}, nil
}

// CookieName is the session cookie name.
const CookieName = "ytls_session"

func (s *Service) setCookie(w http.ResponseWriter, sess *Session) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    sess.Token,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  sess.ExpiresAt,
		MaxAge:   int(s.sessionTTL.Seconds()),
	})
}

// AttachSession writes the HttpOnly session cookie for a new session.
func (s *Service) AttachSession(w http.ResponseWriter, sess *Session) { s.setCookie(w, sess) }

// ClearSessionCookie removes the session cookie from the browser.
func (s *Service) ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/", HttpOnly: true,
		Secure: s.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1,
	})
}

// Logout deletes the server-side session record for the request's cookie.
func (s *Service) Logout(r *http.Request) error {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return nil
	}
	_, err = s.st.DB().Exec(`DELETE FROM auth_sessions WHERE token_hash = ?`, hashToken(c.Value))
	return err
}

// Lookup resolves a request's session cookie to an authenticated session.
func (s *Service) Lookup(r *http.Request) (*Session, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil, store.ErrNotFound
	}
	var sess Session
	var expires string
	err = s.st.DB().QueryRow(
		`SELECT s.user_id, u.username, s.csrf_token, s.expires_at
		 FROM auth_sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.token_hash = ?`, hashToken(c.Value),
	).Scan(&sess.UserID, &sess.Username, &sess.CSRFHash, &expires)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, err
	}
	exp, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil {
		return nil, store.ErrNotFound
	}
	if time.Now().After(exp) {
		_, _ = s.st.DB().Exec(`DELETE FROM auth_sessions WHERE token_hash = ?`, hashToken(c.Value))
		return nil, store.ErrNotFound
	}
	sess.Token = c.Value
	sess.ExpiresAt = exp
	return &sess, nil
}

// DestroyAllSessions invalidates every session, used after a password change.
func (s *Service) DestroyAllSessions(keepToken string) error {
	_, err := s.st.DB().Exec(`DELETE FROM auth_sessions WHERE token_hash <> ?`, hashToken(keepToken))
	return err
}

// PurgeExpiredSessions removes session rows past their expiry.
func (s *Service) PurgeExpiredSessions() error {
	_, err := s.st.DB().Exec(`DELETE FROM auth_sessions WHERE expires_at < ?`,
		time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func randomToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}
