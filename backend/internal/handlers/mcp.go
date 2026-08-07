package handlers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shawn-bluce/renderbin/backend/internal/auth"
	"github.com/shawn-bluce/renderbin/backend/internal/buildinfo"
	"github.com/shawn-bluce/renderbin/backend/internal/db/sqlcgen"
)

const (
	// mcpMaxBatchFiles caps upload_files; each file is separately capped at
	// maxHTMLBytes, but the whole batch must also fit mcpMaxRequestBytes.
	mcpMaxBatchFiles = 20
	// mcpMaxRequestBytes bounds a /mcp request body — the SDK reads the whole
	// JSON-RPC message into memory and (as of v1.2.0) applies no limit itself.
	mcpMaxRequestBytes = 32 << 20
)

// errFileNotFound is returned both for slugs that don't exist and for files
// owned by another user, so MCP callers can't probe other users' slugs.
var errFileNotFound = errors.New("file not found in this project")

// MCPHandler serves the MCP (Model Context Protocol) endpoint at /mcp: a
// stateless streamable-HTTP server whose tools let an AI client manage the
// authenticated user's own files. Authentication is the per-user API key
// (users.api_key, issued on the settings page) as a Bearer token; the whole
// endpoint is gated on the mcp_enabled config.
type MCPHandler struct {
	queries *sqlcgen.Queries
	logger  *slog.Logger
}

// NewMCPHandler builds the /mcp http.Handler: body cap → mcp_enabled gate →
// Bearer-token auth → streamable MCP server with the six file tools.
func NewMCPHandler(queries *sqlcgen.Queries, logger *slog.Logger) http.Handler {
	m := &MCPHandler{queries: queries, logger: logger}

	srv := mcp.NewServer(&mcp.Implementation{Name: "renderbin", Version: buildinfo.Version}, nil)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "upload_file",
		Description: "Upload a Markdown or HTML document. The file starts private; the returned URL (with its access code) becomes publicly viewable once the file is published with publish_file.",
	}, m.uploadFile)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "upload_files",
		Description: "Upload up to 20 Markdown or HTML documents in one call. Files start private; returns each file's URL.",
	}, m.uploadFiles)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "search_files",
		Description: "Search your own documents by name and content. Returns each match's name and access URL.",
	}, m.searchFiles)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "update_file",
		Description: "Update a document's name and/or content, identified by its slug. The slug, kind, and access code stay unchanged.",
	}, m.updateFile)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "publish_file",
		Description: "Make a document publicly accessible and return its shareable URL.",
	}, m.publishFile)
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "delete_file",
		Description: "Move a document to the trash. First call it without confirm: it returns the file's name and URL so you can ask the user to confirm. Only after the user explicitly confirms, call it again with confirm=true. Permanent deletion is not available over MCP.",
	}, m.deleteFile)

	streamable := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)
	withAuth := mcpauth.RequireBearerToken(m.verifyAPIKey, nil)(streamable)
	return m.requireMCPEnabled(capRequestBody(withAuth))
}

// requireMCPEnabled 403s every /mcp request while the mcp_enabled config is off.
func (m *MCPHandler) requireMCPEnabled(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !configBool(r, m.queries, ConfigMCPEnabled) {
			http.Error(w, "MCP is disabled", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func capRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, mcpMaxRequestBytes)
		next.ServeHTTP(w, r)
	})
}

// verifyAPIKey resolves a Bearer token to its user row. The user and the
// request-derived base URL travel to tool handlers via TokenInfo.Extra
// (surfaced as req.Extra.TokenInfo) — the SDK's supported identity path; the
// tool-handler ctx does not carry HTTP middleware values.
func (m *MCPHandler) verifyAPIKey(ctx context.Context, token string, r *http.Request) (*mcpauth.TokenInfo, error) {
	user, err := m.queries.GetUserByAPIKey(ctx, sql.NullString{String: token, Valid: true})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, mcpauth.ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	return &mcpauth.TokenInfo{
		UserID: strconv.FormatInt(user.ID, 10),
		// API keys don't expire; this only satisfies the SDK's per-request
		// zero-expiration check and easily outlives the request it's made for.
		Expiration: time.Now().Add(time.Hour),
		Extra:      map[string]any{"user": user, "baseURL": requestBaseURL(r)},
	}, nil
}

// requestBaseURL derives the externally visible origin per request, so
// returned URLs are correct without a configured base URL. Behind a reverse
// proxy, X-Forwarded-Proto (and optionally X-Forwarded-Host) must be set.
func requestBaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if v := r.Header.Get("X-Forwarded-Proto"); v != "" {
		scheme = v
	}
	host := r.Host
	if v := r.Header.Get("X-Forwarded-Host"); v != "" {
		host = v
	}
	return scheme + "://" + host
}

// mcpIdentity recovers the authenticated user and base URL stashed by
// verifyAPIKey. RequireBearerToken guarantees TokenInfo is present.
func mcpIdentity(req *mcp.CallToolRequest) (sqlcgen.User, string, error) {
	if req.Extra == nil || req.Extra.TokenInfo == nil {
		return sqlcgen.User{}, "", errors.New("unauthenticated")
	}
	extra := req.Extra.TokenInfo.Extra
	user, uok := extra["user"].(sqlcgen.User)
	baseURL, bok := extra["baseURL"].(string)
	if !uok || !bok {
		return sqlcgen.User{}, "", errors.New("unauthenticated")
	}
	return user, baseURL, nil
}

func accessURL(baseURL string, f sqlcgen.File) string {
	return baseURL + "/res/" + f.Slug + "?code=" + f.AccessCode
}

// ownedFile loads the user's own non-deleted file. Ownership is enforced by
// the query itself, so a missing slug and someone else's slug are literally
// the same case: no row, hence the same errFileNotFound.
func (m *MCPHandler) ownedFile(ctx context.Context, user sqlcgen.User, slug string) (sqlcgen.File, error) {
	file, err := m.queries.GetUserFileBySlug(ctx, sqlcgen.GetUserFileBySlugParams{
		Slug:   slug,
		UserID: user.ID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return sqlcgen.File{}, errFileNotFound
	}
	if err != nil {
		m.logger.Error("mcp get file", "error", err)
		return sqlcgen.File{}, errors.New("internal error")
	}
	return file, nil
}

func textResult(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: fmt.Sprintf(format, args...)},
	}}
}

// --- upload ---

type mcpUploadInput struct {
	Name    string `json:"name,omitempty" jsonschema:"display name; defaults to Untitled"`
	Kind    string `json:"kind" jsonschema:"document format: markdown or html"`
	Content string `json:"content" jsonschema:"the raw document source, at most 5MB"`
}

type mcpFileInfo struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	IsPublic bool   `json:"is_public"`
	URL      string `json:"url" jsonschema:"access URL including the access code; anonymously viewable only while the file is public"`
}

func fileInfo(baseURL string, f sqlcgen.File) mcpFileInfo {
	return mcpFileInfo{Slug: f.Slug, Name: f.Name, Kind: f.Kind, IsPublic: f.IsPublic, URL: accessURL(baseURL, f)}
}

// normalizeMCPKind is stricter than the web API's normalizeKind: MCP uploads
// accept only markdown and html documents, and the kind must be explicit.
func normalizeMCPKind(k string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(k)) {
	case KindMarkdown, "md":
		return KindMarkdown, true
	case KindHTML, "htm":
		return KindHTML, true
	default:
		return "", false
	}
}

func (m *MCPHandler) createUpload(ctx context.Context, user sqlcgen.User, in mcpUploadInput) (sqlcgen.File, error) {
	kind, ok := normalizeMCPKind(in.Kind)
	if !ok {
		return sqlcgen.File{}, errors.New("kind must be markdown or html")
	}
	if in.Content == "" {
		return sqlcgen.File{}, errors.New("content is required")
	}
	if len(in.Content) > maxHTMLBytes {
		return sqlcgen.File{}, errors.New("content exceeds 5MB")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = "Untitled"
	}

	accessCode, err := auth.NewAccessCode()
	if err != nil {
		m.logger.Error("mcp generate access code", "error", err)
		return sqlcgen.File{}, errors.New("internal error")
	}
	file, err := createFileWithFreshSlug(ctx, m.queries, sqlcgen.CreateFileParams{
		Name:        name,
		HtmlContent: in.Content,
		Kind:        kind,
		IsPublic:    false, // uploads start private; publish_file makes them public
		AccessCode:  accessCode,
		UserID:      user.ID,
	})
	if err != nil {
		m.logger.Error("mcp create file", "error", err)
		return sqlcgen.File{}, errors.New("internal error")
	}
	return file, nil
}

type uploadFileOutput struct {
	mcpFileInfo
}

func (m *MCPHandler) uploadFile(ctx context.Context, req *mcp.CallToolRequest, in mcpUploadInput) (*mcp.CallToolResult, uploadFileOutput, error) {
	user, baseURL, err := mcpIdentity(req)
	if err != nil {
		return nil, uploadFileOutput{}, err
	}
	file, err := m.createUpload(ctx, user, in)
	if err != nil {
		return nil, uploadFileOutput{}, err
	}
	out := uploadFileOutput{fileInfo(baseURL, file)}
	return textResult("Uploaded %q (private). URL: %s — call publish_file with slug %q to make it publicly viewable.",
		out.Name, out.URL, out.Slug), out, nil
}

type uploadFilesInput struct {
	Files []mcpUploadInput `json:"files" jsonschema:"the documents to upload, at most 20 per call"`
}

type uploadFilesResult struct {
	Name  string `json:"name"`
	Slug  string `json:"slug,omitempty"`
	URL   string `json:"url,omitempty"`
	Error string `json:"error,omitempty" jsonschema:"set when this file failed to upload"`
}

type uploadFilesOutput struct {
	Uploaded int                 `json:"uploaded"`
	Failed   int                 `json:"failed"`
	Results  []uploadFilesResult `json:"results"`
}

func (m *MCPHandler) uploadFiles(ctx context.Context, req *mcp.CallToolRequest, in uploadFilesInput) (*mcp.CallToolResult, uploadFilesOutput, error) {
	user, baseURL, err := mcpIdentity(req)
	if err != nil {
		return nil, uploadFilesOutput{}, err
	}
	if len(in.Files) == 0 {
		return nil, uploadFilesOutput{}, errors.New("files is required")
	}
	if len(in.Files) > mcpMaxBatchFiles {
		return nil, uploadFilesOutput{}, fmt.Errorf("at most %d files per call", mcpMaxBatchFiles)
	}

	out := uploadFilesOutput{Results: make([]uploadFilesResult, 0, len(in.Files))}
	var lines []string
	for _, f := range in.Files {
		file, err := m.createUpload(ctx, user, f)
		if err != nil {
			out.Failed++
			out.Results = append(out.Results, uploadFilesResult{Name: f.Name, Error: err.Error()})
			lines = append(lines, fmt.Sprintf("- %q failed: %s", f.Name, err))
			continue
		}
		out.Uploaded++
		out.Results = append(out.Results, uploadFilesResult{Name: file.Name, Slug: file.Slug, URL: accessURL(baseURL, file)})
		lines = append(lines, fmt.Sprintf("- %q → %s", file.Name, accessURL(baseURL, file)))
	}

	summary := fmt.Sprintf("%d uploaded, %d failed (all uploads start private; use publish_file to share):\n%s",
		out.Uploaded, out.Failed, strings.Join(lines, "\n"))
	return textResult("%s", summary), out, nil
}

// --- search ---

type searchFilesInput struct {
	Query string `json:"query" jsonschema:"substring to search for in document names and content"`
}

type searchFilesMatch struct {
	mcpFileInfo
	Snippet string `json:"snippet,omitempty" jsonschema:"content excerpt around the match, when the match is in the content"`
}

type searchFilesOutput struct {
	Found   bool               `json:"found"`
	Results []searchFilesMatch `json:"results"`
}

func (m *MCPHandler) searchFiles(ctx context.Context, req *mcp.CallToolRequest, in searchFilesInput) (*mcp.CallToolResult, searchFilesOutput, error) {
	user, baseURL, err := mcpIdentity(req)
	if err != nil {
		return nil, searchFilesOutput{}, err
	}
	q := strings.TrimSpace(in.Query)
	if q == "" {
		return nil, searchFilesOutput{}, errors.New("query is required")
	}

	files, err := m.queries.SearchUserFilesWithContent(ctx, sqlcgen.SearchUserFilesWithContentParams{
		UserID:       user.ID,
		NameQuery:    q,
		ContentQuery: q,
	})
	if err != nil {
		m.logger.Error("mcp search files", "error", err)
		return nil, searchFilesOutput{}, errors.New("internal error")
	}

	if len(files) == 0 {
		return textResult("No documents matching %q were found in this project.", q),
			searchFilesOutput{Found: false, Results: []searchFilesMatch{}}, nil
	}

	out := searchFilesOutput{Found: true, Results: make([]searchFilesMatch, 0, len(files))}
	var lines []string
	for _, f := range files {
		match := searchFilesMatch{mcpFileInfo: fileInfo(baseURL, f)}
		if !containsFold(f.Name, q) {
			match.Snippet = contentSnippet(f.HtmlContent, q)
		}
		out.Results = append(out.Results, match)

		visibility := "public"
		if !f.IsPublic {
			visibility = "private — publish_file required before the URL works for others"
		}
		lines = append(lines, fmt.Sprintf("- %q (%s): %s", f.Name, visibility, match.URL))
	}
	return textResult("Found %d document(s) matching %q:\n%s", len(files), q, strings.Join(lines, "\n")), out, nil
}

// --- update ---

type updateFileInput struct {
	Slug    string `json:"slug" jsonschema:"the document's slug (the path segment of its URL)"`
	Name    string `json:"name,omitempty" jsonschema:"new display name; omit to keep the current one"`
	Content string `json:"content,omitempty" jsonschema:"new document source (at most 5MB); omit to keep the current one"`
}

func (m *MCPHandler) updateFile(ctx context.Context, req *mcp.CallToolRequest, in updateFileInput) (*mcp.CallToolResult, mcpFileInfo, error) {
	user, baseURL, err := mcpIdentity(req)
	if err != nil {
		return nil, mcpFileInfo{}, err
	}
	if in.Name == "" && in.Content == "" {
		return nil, mcpFileInfo{}, errors.New("provide a new name, new content, or both")
	}
	if len(in.Content) > maxHTMLBytes {
		return nil, mcpFileInfo{}, errors.New("content exceeds 5MB")
	}

	file, err := m.ownedFile(ctx, user, in.Slug)
	if err != nil {
		return nil, mcpFileInfo{}, err
	}

	name := file.Name
	if in.Name != "" {
		name = strings.TrimSpace(in.Name)
	}
	content := file.HtmlContent
	if in.Content != "" {
		content = in.Content
	}
	updated, err := m.queries.UpdateFile(ctx, sqlcgen.UpdateFileParams{
		Name:        name,
		NewSlug:     file.Slug, // slug and access code deliberately unchanged over MCP
		HtmlContent: content,
		AccessCode:  file.AccessCode,
		Slug:        file.Slug,
		UserID:      user.ID,
	})
	if err != nil {
		m.logger.Error("mcp update file", "error", err)
		return nil, mcpFileInfo{}, errors.New("internal error")
	}

	out := fileInfo(baseURL, updated)
	return textResult("Updated %q. URL: %s", out.Name, out.URL), out, nil
}

// --- publish ---

type publishFileInput struct {
	Slug string `json:"slug" jsonschema:"the document's slug (the path segment of its URL)"`
}

func (m *MCPHandler) publishFile(ctx context.Context, req *mcp.CallToolRequest, in publishFileInput) (*mcp.CallToolResult, mcpFileInfo, error) {
	user, baseURL, err := mcpIdentity(req)
	if err != nil {
		return nil, mcpFileInfo{}, err
	}
	file, err := m.ownedFile(ctx, user, in.Slug)
	if err != nil {
		return nil, mcpFileInfo{}, err
	}

	published, err := m.queries.SetFileVisibility(ctx, sqlcgen.SetFileVisibilityParams{
		IsPublic: true,
		Slug:     file.Slug,
		UserID:   user.ID,
	})
	if err != nil {
		m.logger.Error("mcp publish file", "error", err)
		return nil, mcpFileInfo{}, errors.New("internal error")
	}

	out := fileInfo(baseURL, published)
	return textResult("Published %q. Anyone with this URL can now view it: %s", out.Name, out.URL), out, nil
}

// --- delete ---

type deleteFileInput struct {
	Slug    string `json:"slug" jsonschema:"the document's slug (the path segment of its URL)"`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"must be true to actually delete; call without it first and ask the user to confirm"`
}

type deleteFileOutput struct {
	mcpFileInfo
	Deleted bool `json:"deleted"`
}

func (m *MCPHandler) deleteFile(ctx context.Context, req *mcp.CallToolRequest, in deleteFileInput) (*mcp.CallToolResult, deleteFileOutput, error) {
	user, baseURL, err := mcpIdentity(req)
	if err != nil {
		return nil, deleteFileOutput{}, err
	}
	// Re-verify ownership on both phases: the server is stateless, so the
	// confirm=true call must stand on its own.
	file, err := m.ownedFile(ctx, user, in.Slug)
	if err != nil {
		return nil, deleteFileOutput{}, err
	}
	out := deleteFileOutput{mcpFileInfo: fileInfo(baseURL, file)}

	if !in.Confirm {
		return textResult("About to move %q (%s) to the trash. Ask the user to confirm, then call delete_file again with confirm=true. Nothing has been deleted yet.",
			out.Name, out.URL), out, nil
	}

	rows, err := m.queries.SoftDeleteFile(ctx, sqlcgen.SoftDeleteFileParams{
		Slug:   file.Slug,
		UserID: user.ID,
	})
	if err != nil {
		m.logger.Error("mcp delete file", "error", err)
		return nil, deleteFileOutput{}, errors.New("internal error")
	}
	if rows == 0 {
		// Trashed between the ownedFile lookup and here.
		return nil, deleteFileOutput{}, errFileNotFound
	}
	out.Deleted = true
	return textResult("Moved %q to the trash. It can be restored from the web UI; permanent deletion is not available over MCP.", out.Name), out, nil
}
