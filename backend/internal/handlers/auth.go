package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/shawn-bluce/renderbin/backend/internal/auth"
	"github.com/shawn-bluce/renderbin/backend/internal/db/sqlcgen"
)

// Config keys stored in the configs table. Boolean values are the strings
// "true"/"false"; a missing key reads as false.
const (
	ConfigAllowRegistration = "allow_registration"
	ConfigMCPEnabled        = "mcp_enabled"
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

const minPasswordLen = 6

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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	user, errMsg, err := createUser(r, h.queries, req)
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

// createUser validates a registration payload and inserts the user. It
// returns a client-facing message for validation failures (empty when the
// user was created) and err for unexpected database errors.
func createUser(r *http.Request, queries *sqlcgen.Queries, req registerRequest) (sqlcgen.User, string, error) {
	req.Username = strings.TrimSpace(req.Username)
	req.Nickname = strings.TrimSpace(req.Nickname)
	if !usernamePattern.MatchString(req.Username) {
		return sqlcgen.User{}, "username must be 1-64 chars of letters, digits, '.', '_' or '-'", nil
	}
	if req.Nickname == "" || len(req.Nickname) > 64 {
		return sqlcgen.User{}, "nickname must be 1-64 characters", nil
	}
	if len(req.Password) < minPasswordLen {
		return sqlcgen.User{}, "password must be at least 6 characters", nil
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return sqlcgen.User{}, "", err
	}
	user, err := queries.CreateUser(r.Context(), sqlcgen.CreateUserParams{
		Username:     req.Username,
		Nickname:     req.Nickname,
		PasswordHash: hash,
	})
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

	auth.SetSessionCookie(w, token, expiresAt)

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
	auth.ClearSessionCookie(w)
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(meResponse{
		ID:       user.ID,
		Username: user.Username,
		Nickname: user.Nickname,
		IsAdmin:  IsSuperAdmin(user),
	})
}
