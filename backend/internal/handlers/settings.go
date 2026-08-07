package handlers

import (
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/shawn-bluce/renderbin/backend/internal/auth"
	"github.com/shawn-bluce/renderbin/backend/internal/db/sqlcgen"
)

// SettingsHandler serves the settings page: global configs (readable by any
// logged-in user, writable only by the super admin), the current user's
// profile (nickname/password), and the per-user MCP API key.
type SettingsHandler struct {
	queries *sqlcgen.Queries
	logger  *slog.Logger
}

func NewSettingsHandler(queries *sqlcgen.Queries, logger *slog.Logger) *SettingsHandler {
	return &SettingsHandler{queries: queries, logger: logger}
}

type settingsResponse struct {
	AllowRegistration bool `json:"allow_registration"`
	MCPEnabled        bool `json:"mcp_enabled"`
}

func (h *SettingsHandler) Get(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(settingsResponse{
		AllowRegistration: configBool(r, h.queries, ConfigAllowRegistration),
		MCPEnabled:        configBool(r, h.queries, ConfigMCPEnabled),
	})
}

type updateSettingsRequest struct {
	AllowRegistration *bool `json:"allow_registration"`
	MCPEnabled        *bool `json:"mcp_enabled"`
}

// Update writes global configs; super admin only. Fields are optional so the
// frontend can toggle one switch without resending the other.
func (h *SettingsHandler) Update(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !IsSuperAdmin(user) {
		http.Error(w, "super admin only", http.StatusForbidden)
		return
	}

	var req updateSettingsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	updates := map[string]*bool{
		ConfigAllowRegistration: req.AllowRegistration,
		ConfigMCPEnabled:        req.MCPEnabled,
	}
	for key, value := range updates {
		if value == nil {
			continue
		}
		if err := h.queries.SetConfig(r.Context(), sqlcgen.SetConfigParams{Key: key, Value: boolString(*value)}); err != nil {
			h.logger.Error("set config", "key", key, "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	h.Get(w, r)
}

type updateProfileRequest struct {
	Nickname        *string `json:"nickname"`
	CurrentPassword string  `json:"current_password"`
	NewPassword     string  `json:"new_password"`
}

// UpdateProfile lets the logged-in user change their own nickname and/or
// password. A password change requires the current password.
func (h *SettingsHandler) UpdateProfile(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	var req updateProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Nickname != nil {
		nickname := *req.Nickname
		if nickname == "" || len(nickname) > 64 {
			http.Error(w, "nickname must be 1-64 characters", http.StatusBadRequest)
			return
		}
		if _, err := h.queries.UpdateUserNickname(r.Context(), sqlcgen.UpdateUserNicknameParams{
			Nickname: nickname,
			ID:       user.ID,
		}); err != nil {
			h.logger.Error("update nickname", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	if req.NewPassword != "" {
		if !auth.VerifyPassword(user.PasswordHash, req.CurrentPassword) {
			http.Error(w, "current password is incorrect", http.StatusForbidden)
			return
		}
		if len(req.NewPassword) < minPasswordLen {
			http.Error(w, "password must be at least 6 characters", http.StatusBadRequest)
			return
		}
		hash, err := auth.HashPassword(req.NewPassword)
		if err != nil {
			h.logger.Error("hash password", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if err := h.queries.UpdateUserPassword(r.Context(), sqlcgen.UpdateUserPasswordParams{
			PasswordHash: hash,
			ID:           user.ID,
		}); err != nil {
			h.logger.Error("update password", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

type apiKeyResponse struct {
	APIKey string `json:"api_key"`
}

// EnsureAPIKey returns the current user's MCP API key, creating one if the
// user doesn't have one yet. Only available while MCP is enabled.
func (h *SettingsHandler) EnsureAPIKey(w http.ResponseWriter, r *http.Request) {
	h.serveAPIKey(w, r, false)
}

// ResetAPIKey replaces the current user's MCP API key with a fresh one.
func (h *SettingsHandler) ResetAPIKey(w http.ResponseWriter, r *http.Request) {
	h.serveAPIKey(w, r, true)
}

func (h *SettingsHandler) serveAPIKey(w http.ResponseWriter, r *http.Request, reset bool) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	if !configBool(r, h.queries, ConfigMCPEnabled) {
		http.Error(w, "MCP is disabled", http.StatusConflict)
		return
	}

	key := user.ApiKey.String
	if reset || !user.ApiKey.Valid || key == "" {
		newKey, err := auth.NewAPIKey()
		if err != nil {
			h.logger.Error("generate api key", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if err := h.queries.SetUserAPIKey(r.Context(), sqlcgen.SetUserAPIKeyParams{
			ApiKey: sql.NullString{String: newKey, Valid: true},
			ID:     user.ID,
		}); err != nil {
			h.logger.Error("set api key", "error", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		key = newKey
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(apiKeyResponse{APIKey: key})
}
