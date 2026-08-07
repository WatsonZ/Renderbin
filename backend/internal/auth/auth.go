// Package auth holds pure credential/token/cookie mechanics with no
// database dependency. User storage, session storage, and HTTP wiring live
// in internal/handlers and internal/server.
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const SessionCookieName = "rb_session"

// APIKeyPrefix marks a string as one of our MCP API keys, making it
// recognizable in configs and logs.
const APIKeyPrefix = "rb_"

// SessionTTL is how long a login session (and its cookie) stays valid.
const SessionTTL = 30 * 24 * time.Hour

// HashPassword bcrypt-hashes a password for storage in users.password_hash.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// VerifyPassword reports whether password matches the stored bcrypt hash.
func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

// dummyHash is a bcrypt hash of an unguessable throwaway value. Login runs a
// bcrypt compare against it when the username doesn't exist, so "unknown
// user" and "wrong password" take the same time and usernames can't be
// enumerated through response timing.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("rb-no-such-user"), bcrypt.DefaultCost)

// BurnPasswordCheck spends one bcrypt verification's worth of CPU and always
// reports false. Used to equalize login timing for nonexistent usernames.
func BurnPasswordCheck(password string) bool {
	return bcrypt.CompareHashAndPassword(dummyHash, []byte(password)) == nil && false
}

// NewSessionToken returns a 64-character hex string (32 random bytes).
func NewSessionToken() (string, error) {
	return randomHex(32)
}

// NewAccessCode returns an 8-character string: the first 6 characters of a
// random hex string, base64-encoded. That is 24 bits of entropy — a deliberate
// choice trading strength for short shareable URLs; the admin can set a longer
// custom code per file when it matters. RawURLEncoding keeps the result
// URL-query-safe and padding-free (StdEncoding's '+', '/', '=' would need
// escaping in ?code= and violate the slug charset).
func NewAccessCode() (string, error) {
	h, err := randomHex(16)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString([]byte(h[:6])), nil
}

// NewAPIKey returns a per-user MCP API key: APIKeyPrefix plus 48 hex
// characters (24 random bytes).
func NewAPIKey() (string, error) {
	h, err := randomHex(24)
	if err != nil {
		return "", err
	}
	return APIKeyPrefix + h, nil
}

func randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func SetSessionCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(time.Until(expiresAt).Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func ClearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}
