package handlers

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/shawn-bluce/renderbin/backend/internal/auth"
	"github.com/shawn-bluce/renderbin/backend/internal/db/sqlcgen"
)

// maxHTMLBytes caps stored HTML at 5 MB per file; bodyOverhead leaves room for
// the surrounding JSON envelope (name/slug fields) in the request body guard.
const (
	maxHTMLBytes = 5 << 20
	bodyOverhead = 64 << 10
)

// slugPattern restricts custom slugs to URL-safe characters so they can be
// dropped into /res/{slug} without escaping.
var slugPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// newShortSlug returns an 8-character slug: the first 6 characters of a random
// UUID, base64-encoded. 24 bits of entropy — short shareable URLs over
// collision headroom; createFileWithFreshSlug retries on UNIQUE conflicts to
// compensate. RawURLEncoding is required: its output always satisfies
// slugPattern, unlike StdEncoding's '+' and '/'.
func newShortSlug() (string, error) {
	u, err := uuid.NewRandom()
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString([]byte(u.String()[:6])), nil
}

// createFileWithFreshSlug inserts params under a freshly generated short slug
// (params.Slug is overwritten), retrying up to 5 times on UNIQUE collisions —
// 8-char slugs carry only 24 bits of entropy, so a collision is a realistic
// event once enough files (including soft-deleted ones, which keep their slug)
// accumulate. Shared by the web upload handler and the MCP upload tools.
func createFileWithFreshSlug(ctx context.Context, queries *sqlcgen.Queries, params sqlcgen.CreateFileParams) (sqlcgen.File, error) {
	const maxSlugAttempts = 5
	for attempt := 1; ; attempt++ {
		slug, err := newShortSlug()
		if err != nil {
			return sqlcgen.File{}, err
		}
		params.Slug = slug
		file, err := queries.CreateFile(ctx, params)
		if err == nil {
			return file, nil
		}
		if !strings.Contains(err.Error(), "UNIQUE") || attempt >= maxSlugAttempts {
			return sqlcgen.File{}, err
		}
	}
}

type FilesHandler struct {
	queries *sqlcgen.Queries
	logger  *slog.Logger
}

func NewFilesHandler(queries *sqlcgen.Queries, logger *slog.Logger) *FilesHandler {
	return &FilesHandler{queries: queries, logger: logger}
}

type fileResponse struct {
	Slug             string  `json:"slug"`
	Name             string  `json:"name"`
	Kind             string  `json:"kind"`
	HTMLContent      string  `json:"html_content,omitempty"`
	IsPublic         bool    `json:"is_public"`
	AccessCode       string  `json:"access_code"`
	Tags             string  `json:"tags"`
	CreatedAt        string  `json:"created_at"`
	UpdatedAt        string  `json:"updated_at"`
	SuccessCount     int64   `json:"success_count"`
	CodeSuccessCount int64   `json:"code_success_count"`
	FailureCount     int64   `json:"failure_count"`
	ExpiresAt        *string `json:"expires_at"`
	MaxViews         *int64  `json:"max_views"`
	ViewCount        int64   `json:"view_count"`
}

const timeLayout = "2006-01-02T15:04:05Z07:00"

func toFileResponse(f sqlcgen.File) fileResponse {
	resp := fileResponse{
		Slug:             f.Slug,
		Name:             f.Name,
		Kind:             f.Kind,
		HTMLContent:      f.HtmlContent,
		IsPublic:         f.IsPublic,
		AccessCode:       f.AccessCode,
		Tags:             f.Tags,
		CreatedAt:        f.CreatedAt.Format(timeLayout),
		UpdatedAt:        f.UpdatedAt.Format(timeLayout),
		SuccessCount:     f.SuccessCount,
		CodeSuccessCount: f.CodeSuccessCount,
		FailureCount:     f.FailureCount,
		ViewCount:        f.ViewCount,
	}
	if f.ExpiresAt.Valid {
		s := f.ExpiresAt.Time.Format(timeLayout)
		resp.ExpiresAt = &s
	}
	if f.MaxViews.Valid {
		v := f.MaxViews.Int64
		resp.MaxViews = &v
	}
	return resp
}

// normalizeTags trims whitespace around each comma-separated tag, drops
// empty entries, and dedupes while preserving first-seen order, so the
// stored value is always a clean canonical form regardless of what the
// client sent.
func normalizeTags(raw string) string {
	parts := strings.Split(raw, ",")
	seen := make(map[string]bool, len(parts))
	tags := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		tags = append(tags, t)
	}
	return strings.Join(tags, ",")
}

func (h *FilesHandler) List(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	var (
		files []sqlcgen.File
		err   error
	)
	if r.URL.Query().Get("deleted") == "true" {
		files, err = h.queries.ListUserDeletedFiles(r.Context(), user.ID)
	} else {
		files, err = h.queries.ListUserFiles(r.Context(), user.ID)
	}
	if err != nil {
		h.logger.Error("list files", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := make([]fileResponse, 0, len(files))
	for _, f := range files {
		item := toFileResponse(f)
		item.HTMLContent = ""
		resp = append(resp, item)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// searchResultResponse augments the list-row fields with where the query
// matched: a name hit renders as a plain title row, while a content-only hit
// additionally carries the weakened snippet (±100 runes around the match).
type searchResultResponse struct {
	fileResponse
	MatchedName    bool   `json:"matched_name"`
	MatchedContent bool   `json:"matched_content"`
	Snippet        string `json:"snippet,omitempty"`
}

// Search implements GET /api/files/search?q=...&content=true — substring
// search scoped to the current user's own (non-deleted) files. Name-only by
// default; content=true also searches the stored source.
func (h *FilesHandler) Search(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	includeContent := r.URL.Query().Get("content") == "true"

	var (
		files []sqlcgen.File
		err   error
	)
	switch {
	case q == "":
		// Nothing to search; fall through with no rows.
	case includeContent:
		files, err = h.queries.SearchUserFilesWithContent(r.Context(), sqlcgen.SearchUserFilesWithContentParams{
			UserID:       user.ID,
			NameQuery:    q,
			ContentQuery: q,
		})
	default:
		files, err = h.queries.SearchUserFilesByName(r.Context(), sqlcgen.SearchUserFilesByNameParams{
			UserID:    user.ID,
			NameQuery: q,
		})
	}
	if err != nil {
		h.logger.Error("search files", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	resp := make([]searchResultResponse, 0, len(files))
	for _, f := range files {
		item := toFileResponse(f)
		item.HTMLContent = ""
		res := searchResultResponse{fileResponse: item, MatchedName: containsFold(f.Name, q)}
		// A title hit renders as a plain row; only content-only hits carry a snippet.
		if !res.MatchedName && includeContent {
			res.Snippet = contentSnippet(f.HtmlContent, q)
			res.MatchedContent = res.Snippet != ""
		}
		resp = append(resp, res)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// containsFold reports whether s contains substr case-insensitively (full
// Unicode folding — a superset of the SQL queries' ASCII-only lower()).
func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// snippetRadius is how many runes of context to keep on each side of a
// content match in search results.
const snippetRadius = 100

// contentSnippet returns the text around the first case-insensitive
// occurrence of query in content: the match plus up to snippetRadius runes on
// each side, with ellipses marking truncation. Empty when there's no match.
func contentSnippet(content, query string) string {
	idx := strings.Index(strings.ToLower(content), strings.ToLower(query))
	if idx < 0 {
		return ""
	}
	// ToLower can shift byte offsets for a few exotic characters; clamp to a
	// rune boundary in the original string so slicing stays valid UTF-8.
	if idx > len(content) {
		idx = len(content)
	}
	for idx > 0 && !utf8.RuneStart(content[idx]) {
		idx--
	}

	start := idx
	for range snippetRadius {
		if start == 0 {
			break
		}
		_, size := utf8.DecodeLastRuneInString(content[:start])
		start -= size
	}

	end := min(idx+len(query), len(content))
	for end < len(content) && !utf8.RuneStart(content[end]) {
		end++
	}
	for range snippetRadius {
		if end == len(content) {
			break
		}
		_, size := utf8.DecodeRuneInString(content[end:])
		end += size
	}

	snippet := content[start:end]
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(content) {
		snippet += "…"
	}
	return snippet
}

type createFileRequest struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	HTMLContent string `json:"html_content"`
}

func (h *FilesHandler) Create(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxHTMLBytes+bodyOverhead)
	var req createFileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.HTMLContent == "" {
		http.Error(w, "html_content is required", http.StatusBadRequest)
		return
	}
	if len(req.HTMLContent) > maxHTMLBytes {
		http.Error(w, "html_content exceeds 5MB", http.StatusRequestEntityTooLarge)
		return
	}
	kind, ok := normalizeKind(req.Kind)
	if !ok {
		http.Error(w, "kind must be one of html, markdown, txt", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		req.Name = "Untitled"
	}

	// requireAuth resolved the session; the file is attributed to its uploader.
	user, ok := requireUser(w, r)
	if !ok {
		return
	}

	accessCode, err := auth.NewAccessCode()
	if err != nil {
		h.logger.Error("generate access code", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	file, err := createFileWithFreshSlug(r.Context(), h.queries, sqlcgen.CreateFileParams{
		Name:        req.Name,
		HtmlContent: req.HTMLContent,
		Kind:        kind,
		IsPublic:    false,
		AccessCode:  accessCode,
		UserID:      user.ID,
	})
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			http.Error(w, "slug already taken", http.StatusConflict)
			return
		}
		h.logger.Error("create file", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	item := toFileResponse(file)
	item.HTMLContent = ""
	json.NewEncoder(w).Encode(item)
}

// Get returns a single file including its html_content, at GET
// /api/files/{slug}. This is the only endpoint that returns content, and it
// feeds the editor. Scoped to the caller's own files, so another user's slug
// 404s exactly like one that never existed.
func (h *FilesHandler) Get(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	slug := chi.URLParam(r, "slug")

	file, err := h.queries.GetUserFileBySlug(r.Context(), sqlcgen.GetUserFileBySlugParams{
		Slug:   slug,
		UserID: user.ID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.logger.Error("get file", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toFileResponse(file))
}

type updateFileRequest struct {
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	HTMLContent string `json:"html_content"`
	AccessCode  string `json:"access_code"`
}

// Update replaces a file's name, slug, html_content, and access_code at PATCH
// /api/files/{slug}. The slug and access code are user-editable (custom
// values); a slug collision with an existing file returns 409.
func (h *FilesHandler) Update(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	oldSlug := chi.URLParam(r, "slug")

	r.Body = http.MaxBytesReader(w, r.Body, maxHTMLBytes+bodyOverhead)
	var req updateFileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.HTMLContent == "" {
		http.Error(w, "html_content is required", http.StatusBadRequest)
		return
	}
	if len(req.HTMLContent) > maxHTMLBytes {
		http.Error(w, "html_content exceeds 5MB", http.StatusRequestEntityTooLarge)
		return
	}
	req.Slug = strings.TrimSpace(req.Slug)
	if !slugPattern.MatchString(req.Slug) {
		http.Error(w, "slug must be 1-128 chars of letters, digits, '.', '_' or '-'", http.StatusBadRequest)
		return
	}
	req.AccessCode = strings.TrimSpace(req.AccessCode)
	// Reuses slugPattern: same URL-query-safe charset, and {1,128} forbids the
	// empty string — an empty stored access_code would compare equal to a
	// missing ?code= param in accessCodeMatches, opening the file to everyone.
	if !slugPattern.MatchString(req.AccessCode) {
		http.Error(w, "access_code must be 1-128 chars of letters, digits, '.', '_' or '-'", http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		req.Name = "Untitled"
	}

	file, err := h.queries.UpdateFile(r.Context(), sqlcgen.UpdateFileParams{
		Name:        req.Name,
		NewSlug:     req.Slug,
		HtmlContent: req.HTMLContent,
		AccessCode:  req.AccessCode,
		Slug:        oldSlug,
		UserID:      user.ID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			http.Error(w, "slug already taken", http.StatusConflict)
			return
		}
		h.logger.Error("update file", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	item := toFileResponse(file)
	item.HTMLContent = ""
	json.NewEncoder(w).Encode(item)
}

// Restore un-deletes a soft-deleted file at POST /api/files/{slug}/restore.
func (h *FilesHandler) Restore(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	slug := chi.URLParam(r, "slug")

	file, err := h.queries.RestoreFile(r.Context(), sqlcgen.RestoreFileParams{
		Slug:   slug,
		UserID: user.ID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.logger.Error("restore file", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	item := toFileResponse(file)
	item.HTMLContent = ""
	json.NewEncoder(w).Encode(item)
}

// ttlDurations is the allowed set of expiry presets for SetExpiry.
var ttlDurations = map[string]time.Duration{
	"24h": 24 * time.Hour,
	"48h": 48 * time.Hour,
	"72h": 72 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
}

type setExpiryRequest struct {
	TTL      *string `json:"ttl"`
	MaxViews *int64  `json:"max_views"`
}

// SetExpiry configures a file's link expiry at PATCH /api/files/{slug}/expiry.
// A TTL preset and a max-view count are mutually exclusive; setting either
// forces the file Public. Sending neither clears any existing limit without
// changing visibility. Expiry itself is enforced lazily in Render.
func (h *FilesHandler) SetExpiry(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	slug := chi.URLParam(r, "slug")

	var req setExpiryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.TTL != nil && req.MaxViews != nil {
		http.Error(w, "ttl and max_views are mutually exclusive", http.StatusBadRequest)
		return
	}

	var (
		file sqlcgen.File
		err  error
	)
	switch {
	case req.TTL != nil:
		dur, ok := ttlDurations[*req.TTL]
		if !ok {
			http.Error(w, "invalid ttl", http.StatusBadRequest)
			return
		}
		file, err = h.queries.SetFileExpiry(r.Context(), sqlcgen.SetFileExpiryParams{
			ExpiresAt: sql.NullTime{Time: time.Now().Add(dur), Valid: true},
			Slug:      slug,
			UserID:    user.ID,
		})
	case req.MaxViews != nil:
		if *req.MaxViews <= 0 {
			http.Error(w, "max_views must be positive", http.StatusBadRequest)
			return
		}
		file, err = h.queries.SetFileExpiry(r.Context(), sqlcgen.SetFileExpiryParams{
			MaxViews: sql.NullInt64{Int64: *req.MaxViews, Valid: true},
			Slug:     slug,
			UserID:   user.ID,
		})
	default:
		file, err = h.queries.ClearFileExpiry(r.Context(), sqlcgen.ClearFileExpiryParams{
			Slug:   slug,
			UserID: user.ID,
		})
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.logger.Error("set expiry", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	item := toFileResponse(file)
	item.HTMLContent = ""
	json.NewEncoder(w).Encode(item)
}

type renameFileRequest struct {
	Name string `json:"name"`
}

func (h *FilesHandler) Rename(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	slug := chi.URLParam(r, "slug")

	var req renameFileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	file, err := h.queries.RenameFile(r.Context(), sqlcgen.RenameFileParams{
		Name:   name,
		Slug:   slug,
		UserID: user.ID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.logger.Error("rename file", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	item := toFileResponse(file)
	item.HTMLContent = ""
	json.NewEncoder(w).Encode(item)
}

type setVisibilityRequest struct {
	IsPublic bool `json:"is_public"`
}

func (h *FilesHandler) SetVisibility(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	slug := chi.URLParam(r, "slug")

	var req setVisibilityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	file, err := h.queries.SetFileVisibility(r.Context(), sqlcgen.SetFileVisibilityParams{
		IsPublic: req.IsPublic,
		Slug:     slug,
		UserID:   user.ID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.logger.Error("set file visibility", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	item := toFileResponse(file)
	item.HTMLContent = ""
	json.NewEncoder(w).Encode(item)
}

type setTagsRequest struct {
	Tags string `json:"tags"`
}

func (h *FilesHandler) SetTags(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	slug := chi.URLParam(r, "slug")

	var req setTagsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	file, err := h.queries.SetFileTags(r.Context(), sqlcgen.SetFileTagsParams{
		Tags:   normalizeTags(req.Tags),
		Slug:   slug,
		UserID: user.ID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.logger.Error("set file tags", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	item := toFileResponse(file)
	item.HTMLContent = ""
	json.NewEncoder(w).Encode(item)
}

func (h *FilesHandler) RefreshCode(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	slug := chi.URLParam(r, "slug")

	accessCode, err := auth.NewAccessCode()
	if err != nil {
		h.logger.Error("generate access code", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	file, err := h.queries.RefreshFileAccessCode(r.Context(), sqlcgen.RefreshFileAccessCodeParams{
		AccessCode: accessCode,
		Slug:       slug,
		UserID:     user.ID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.logger.Error("refresh access code", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	item := toFileResponse(file)
	item.HTMLContent = ""
	json.NewEncoder(w).Encode(item)
}

// Delete moves a file to the trash at DELETE /api/files/{slug}. A slug that
// is unknown, already trashed, or owned by someone else matches no row and
// 404s -- reporting 204 for those would be a success-shaped no-op.
func (h *FilesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	slug := chi.URLParam(r, "slug")

	rows, err := h.queries.SoftDeleteFile(r.Context(), sqlcgen.SoftDeleteFileParams{
		Slug:   slug,
		UserID: user.ID,
	})
	if err != nil {
		h.logger.Error("delete file", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rows == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HardDelete permanently removes a trashed file at DELETE
// /api/files/{slug}/permanent. Only soft-deleted files qualify — an active
// file or unknown slug is a 404 — so the recycle bin is the only path to
// irreversible deletion.
func (h *FilesHandler) HardDelete(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	slug := chi.URLParam(r, "slug")

	rows, err := h.queries.HardDeleteFile(r.Context(), sqlcgen.HardDeleteFileParams{
		Slug:   slug,
		UserID: user.ID,
	})
	if err != nil {
		h.logger.Error("hard delete file", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if rows == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

const accessDeniedBody = `<!doctype html><html><head><meta charset="utf-8"><title>Access denied</title></head><body><p>Access denied.</p></body></html>`

// Render serves a file's content at GET /res/{slug}?code=<access_code>. The
// served bytes depend on the file's kind (see renderContent): html verbatim,
// markdown rendered to HTML, txt as escaped preformatted text.
// A soft-deleted file always 404s, even for a signed-in owner or a correct
// access code (and isn't counted, since there's no file row to attribute a
// count to). Otherwise: the file's *owner* bypasses the public/code checks;
// everyone else — anonymous, or signed in as a different user — needs
// is_public plus a matching ?code=. Every access to an existing file bumps
// exactly one counter — success_count (owner), code_success_count (correct
// code), or failure_count (blocked) — which is owner-facing analytics, not a
// content change, so it deliberately does not touch updated_at.
//
// This is the one handler that must NOT use an owner-scoped query: it serves
// anonymous visitors holding a share link, who have no user at all.
func (h *FilesHandler) Render(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	file, err := h.queries.GetFileBySlugAnyOwner(r.Context(), slug)
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.logger.Error("get file", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Lazy expiry: a public file whose TTL has passed or whose view quota is
	// used up is flipped to private at access time (no cron). This clears the
	// limit columns and does not touch updated_at.
	if file.IsPublic {
		expired := (file.ExpiresAt.Valid && !time.Now().Before(file.ExpiresAt.Time)) ||
			(file.MaxViews.Valid && file.ViewCount >= file.MaxViews.Int64)
		if expired {
			if err := h.queries.ExpireFile(r.Context(), file.Slug); err != nil {
				h.logger.Warn("expire file", "error", err)
			}
			file.IsPublic = false
		}
	}

	// Only the owner's session bypasses the code check. A signed-in stranger
	// falls through to the public+code path, exactly like an anonymous
	// visitor. Anonymous requests carry no cookie, so this costs them nothing.
	user, signedIn := CurrentUser(r, h.queries)
	owner := signedIn && user.ID == file.UserID
	if owner || (file.IsPublic && accessCodeMatches(file.AccessCode, r.URL.Query().Get("code"))) {
		if owner {
			if err := h.queries.IncrementFileSuccessCount(r.Context(), file.Slug); err != nil {
				h.logger.Warn("increment success count", "error", err)
			}
		} else {
			if err := h.queries.IncrementFileCodeSuccessCount(r.Context(), file.Slug); err != nil {
				h.logger.Warn("increment code success count", "error", err)
			}
		}
		// Only code-based access consumes the view quota; the owner's own
		// views never count against a max-views limit.
		if !owner && file.MaxViews.Valid {
			if err := h.queries.IncrementFileViewCount(r.Context(), file.Slug); err != nil {
				h.logger.Warn("increment view count", "error", err)
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(renderContent(file.Kind, file.Name, file.HtmlContent))
		return
	}

	if err := h.queries.IncrementFileFailureCount(r.Context(), file.Slug); err != nil {
		h.logger.Warn("increment failure count", "error", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	w.Write([]byte(accessDeniedBody))
}

func accessCodeMatches(want, got string) bool {
	if len(want) != len(got) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(want), []byte(got)) == 1
}

// Download serves the raw source as a file attachment at
// GET /api/files/{slug}/download. It returns the stored source untransformed
// (not the rendered form), with a filename extension matching the file's kind.
// Scoped to the caller's own files, so a soft-deleted, unknown, or foreign
// slug all 404 alike. Unlike Render, this is not a public view and
// deliberately does not touch success_count/failure_count.
func (h *FilesHandler) Download(w http.ResponseWriter, r *http.Request) {
	user, ok := requireUser(w, r)
	if !ok {
		return
	}
	slug := chi.URLParam(r, "slug")

	file, err := h.queries.GetUserFileBySlug(r.Context(), sqlcgen.GetUserFileBySlugParams{
		Slug:   slug,
		UserID: user.ID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		h.logger.Error("get file", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", downloadContentType(file.Kind))
	w.Header().Set("Content-Disposition", downloadDisposition(file.Name, file.Slug, extForKind(file.Kind)))
	w.Write([]byte(file.HtmlContent))
}

// downloadDisposition builds a Content-Disposition header for a download.
// file.Name is a free-form display name (may be empty, Unicode, or contain
// characters unsafe for a header), so we emit an ASCII fallback (filename=)
// plus an RFC 5987 filename* carrying the real, possibly-Unicode name. ext is
// the extension for the file's kind (e.g. "html", "md", "txt").
func downloadDisposition(name, slug, ext string) string {
	base := strings.TrimSpace(name)
	if base == "" {
		base = slug
	}
	filename := base + "." + ext

	ascii := sanitizeASCII(filename)
	if ascii == "" {
		ascii = slug + "." + ext
	}

	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`,
		ascii, url.PathEscape(filename))
}

// sanitizeASCII produces a safe ASCII filename: printable ASCII except
// double-quote and backslash is kept, everything else (control chars,
// non-ASCII) becomes '_'. This guarantees no header injection via the
// filename= parameter.
func sanitizeASCII(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7f && r != '"' && r != '\\' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
