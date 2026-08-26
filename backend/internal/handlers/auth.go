package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shawn-bluce/renderbin/backend/internal/auth"
	"github.com/shawn-bluce/renderbin/backend/internal/db/sqlcgen"
)

// Config keys stored in the configs table. Boolean values are the strings
// "true"/"false"; a missing key reads as false.
const (
	ConfigAllowRegistration = "allow_registration"
	ConfigMCPEnabled        = "mcp_enabled"
	// ConfigUploadDefaultPublic makes files created over HTTP start public.
	// Missing (the default) means private — the fail-closed direction for a
	// setting that decides whether a link works before its owner ever looked
	// at it. MCP uploads ignore it: those tools promise "starts private" in
	// their descriptions, and publish_file is the agent's explicit consent step.
	ConfigUploadDefaultPublic = "upload_default_public"
)

// configBool reads a boolean config; missing keys and read errors are false.
func configBool(r *http.Request, queries *sqlcgen.Queries, key string) bool {
	v, err := queries.GetConfig(r.Context(), key)
	return err == nil && v == "true"
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// usernamePattern keeps usernames short and unambiguous; nicknames are free-form.
var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// The password policy lives in internal/auth, shared with the reset-password
// CLI subcommand; this alias keeps the call sites in this package short.
const minPasswordLen = auth.MinPasswordLen

type AuthHandler struct {
	queries *sqlcgen.Queries
	logger  *slog.Logger
}

func NewAuthHandler(queries *sqlcgen.Queries, logger *slog.Logger) *AuthHandler {
	return &AuthHandler{queries: queries, logger: logger}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeSmallJSON(w, r, &req) {
		return
	}

	user, err := h.queries.GetUserByUsername(r.Context(), req.Username)
	if errors.Is(err, sql.ErrNoRows) {
		// Spend the same bcrypt cost as a real check so response timing
		// doesn't reveal whether the username exists.
		auth.BurnPasswordCheck(req.Password)
		http.Error(w, "invalid username or password", http.StatusUnauthorized)
		return
	}
	if err != nil {
		h.logger.Error("get user by username", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !auth.VerifyPassword(user.PasswordHash, req.Password) {
		http.Error(w, "invalid username or password", http.StatusUnauthorized)
		return
	}
	// Reported only *after* the password checks out, so this stays a message
	// for the account's owner rather than an oracle telling an attacker which
	// usernames exist -- the same reason BurnPasswordCheck exists above.
	if user.DisabledAt.Valid {
		http.Error(w, "account is disabled", http.StatusForbidden)
		return
	}

	if err := h.startSession(w, r, user.ID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type registerRequest struct {
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	Password string `json:"password"`
}

// Register creates a new account when registration is enabled, then logs the
// new user in. The very first user is created through Setup instead.
func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	count, err := h.queries.CountUsers(r.Context())
	if err != nil {
		h.logger.Error("count users", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if count == 0 {
		http.Error(w, "setup required", http.StatusConflict)
		return
	}
	if !configBool(r, h.queries, ConfigAllowRegistration) {
		http.Error(w, "registration is disabled", http.StatusForbidden)
		return
	}

	var req registerRequest
	if !decodeSmallJSON(w, r, &req) {
		return
	}

	user, errMsg, err := createUser(r, h.queries, req, false)
	if errMsg != "" {
		status := http.StatusBadRequest
		if errMsg == "username already taken" {
			status = http.StatusConflict
		}
		http.Error(w, errMsg, status)
		return
	}
	if err != nil {
		h.logger.Error("create user", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := h.startSession(w, r, user.ID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

// maxNicknameRunes bounds a nickname. Counted in runes, not bytes: the byte
// form of the same rule rejected a 22-character Chinese nickname while telling
// the user the limit was 64 characters.
const maxNicknameRunes = 64

// validateNickname trims and checks a nickname, returning a client-facing
// message when it is unusable.
func validateNickname(nickname string) (string, string) {
	nickname = strings.TrimSpace(nickname)
	if nickname == "" || utf8.RuneCountInString(nickname) > maxNicknameRunes {
		return "", fmt.Sprintf("nickname must be 1-%d characters", maxNicknameRunes)
	}
	return nickname, ""
}

// errFirstUserExists is createUser's signal that the atomic first-run insert
// matched nothing because some other request already created the super admin.
var errFirstUserExists = errors.New("a user already exists")

// createUser validates a registration payload and inserts the user. It
// returns a client-facing message for validation failures (empty when the
// user was created) and err for unexpected database errors.
//
// first selects the atomic first-run insert (CreateFirstUser, which only
// applies while the users table is empty) over the ordinary one. Setup cannot
// use a count-then-insert here: the bcrypt hash above sits inside that window
// and made the check useless under concurrency.
func createUser(r *http.Request, queries *sqlcgen.Queries, req registerRequest, first bool) (sqlcgen.User, string, error) {
	req.Username = strings.TrimSpace(req.Username)
	if !usernamePattern.MatchString(req.Username) {
		return sqlcgen.User{}, "username must be 1-64 chars of letters, digits, '.', '_' or '-'", nil
	}
	nickname, errMsg := validateNickname(req.Nickname)
	if errMsg != "" {
		return sqlcgen.User{}, errMsg, nil
	}
	if errMsg := auth.ValidatePassword(req.Password); errMsg != "" {
		return sqlcgen.User{}, errMsg, nil
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return sqlcgen.User{}, "", err
	}

	var user sqlcgen.User
	if first {
		user, err = queries.CreateFirstUser(r.Context(), sqlcgen.CreateFirstUserParams{
			Username:     req.Username,
			Nickname:     nickname,
			PasswordHash: hash,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return sqlcgen.User{}, "", errFirstUserExists
		}
	} else {
		user, err = queries.CreateUser(r.Context(), sqlcgen.CreateUserParams{
			Username:     req.Username,
			Nickname:     nickname,
			PasswordHash: hash,
		})
	}
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return sqlcgen.User{}, "username already taken", nil
		}
		return sqlcgen.User{}, "", err
	}
	return user, "", nil
}

// startSession issues a session token for the user, persists it, and sets the
// session cookie. Expired sessions are swept opportunistically on each login.
func (h *AuthHandler) startSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	token, err := auth.NewSessionToken()
	if err != nil {
		h.logger.Error("generate session token", "error", err)
		return err
	}

	expiresAt := time.Now().Add(auth.SessionTTL)
	if err := h.queries.CreateSession(r.Context(), sqlcgen.CreateSessionParams{
		Token:     token,
		UserID:    userID,
		ExpiresAt: expiresAt,
	}); err != nil {
		h.logger.Error("create session", "error", err)
		return err
	}

	auth.SetSessionCookie(w, token, expiresAt, requestIsSecure(r))

	if err := h.queries.DeleteExpiredSessions(r.Context()); err != nil {
		h.logger.Warn("delete expired sessions", "error", err)
	}
	return nil
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(auth.SessionCookieName); err == nil {
		if err := h.queries.DeleteSession(r.Context(), cookie.Value); err != nil {
			h.logger.Warn("delete session", "error", err)
		}
	}
	auth.ClearSessionCookie(w, requestIsSecure(r))
	w.WriteHeader(http.StatusNoContent)
}

type meResponse struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Nickname string `json:"nickname"`
	IsAdmin  bool   `json:"is_admin"`
}

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	user, ok := CurrentUser(r, h.queries)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	writeJSON(w, meResponse{
		ID:       user.ID,
		Username: user.Username,
		Nickname: user.Nickname,
		IsAdmin:  IsSuperAdmin(user),
	})
}
