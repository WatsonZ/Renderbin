package server_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/shawn-bluce/renderbin/backend/internal/auth"
	"github.com/shawn-bluce/renderbin/backend/internal/db"
	"github.com/shawn-bluce/renderbin/backend/internal/db/sqlcgen"
	"github.com/shawn-bluce/renderbin/backend/internal/server"
)

const (
	testUser = "admin"
	testPass = "s3cret"
)

type testEnv struct {
	srv     *httptest.Server
	queries *sqlcgen.Queries
	admin   sqlcgen.User
}

// newBareEnv starts a server against an empty database — no users yet, the
// state the first-run /api/setup flow expects.
func newBareEnv(t *testing.T) *testEnv {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	queries := sqlcgen.New(conn)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(server.New(queries, conn, logger))
	t.Cleanup(srv.Close)

	return &testEnv{srv: srv, queries: queries}
}

// newEnv additionally seeds the super admin (id=1, testUser/testPass), the
// state every post-setup test wants.
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	e := newBareEnv(t)
	hash, err := auth.HashPassword(testPass)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	admin, err := e.queries.CreateUser(context.Background(), sqlcgen.CreateUserParams{
		Username:     testUser,
		Nickname:     "Admin",
		PasswordHash: hash,
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	e.admin = admin
	return e
}

// cookieFor inserts a valid session for userID and returns the cookie to send
// with it. The real cookie is Secure, so a plain-HTTP test client's jar won't
// resend it — we attach it explicitly instead.
func (e *testEnv) cookieFor(t *testing.T, userID int64) *http.Cookie {
	t.Helper()
	tok, err := auth.NewSessionToken()
	if err != nil {
		t.Fatalf("NewSessionToken: %v", err)
	}
	if err := e.queries.CreateSession(context.Background(), sqlcgen.CreateSessionParams{
		Token:     tok,
		UserID:    userID,
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return &http.Cookie{Name: auth.SessionCookieName, Value: tok}
}

// authCookie is a session for the seeded super admin.
func (e *testEnv) authCookie(t *testing.T) *http.Cookie {
	t.Helper()
	return e.cookieFor(t, e.admin.ID)
}

// newUser creates an ordinary (non-super-admin) account and a session for it.
// Owner-isolation tests need a second identity; keeping this in one place
// stops a copy from quietly reusing the admin's cookie and passing vacuously.
func (e *testEnv) newUser(t *testing.T, username string) (sqlcgen.User, *http.Cookie) {
	t.Helper()
	hash, err := auth.HashPassword("secret2")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	user, err := e.queries.CreateUser(context.Background(), sqlcgen.CreateUserParams{
		Username:     username,
		Nickname:     username,
		PasswordHash: hash,
	})
	if err != nil {
		t.Fatalf("CreateUser(%s): %v", username, err)
	}
	return user, e.cookieFor(t, user.ID)
}

func (e *testEnv) do(t *testing.T, method, path, body string, cookie *http.Cookie) *http.Response {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, e.srv.URL+path, r)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

type fileResp struct {
	Slug             string  `json:"slug"`
	Name             string  `json:"name"`
	Kind             string  `json:"kind"`
	HTMLContent      string  `json:"html_content"`
	IsPublic         bool    `json:"is_public"`
	AccessCode       string  `json:"access_code"`
	Tags             string  `json:"tags"`
	SuccessCount     int64   `json:"success_count"`
	CodeSuccessCount int64   `json:"code_success_count"`
	FailureCount     int64   `json:"failure_count"`
	ExpiresAt        *string `json:"expires_at"`
	MaxViews         *int64  `json:"max_views"`
	ViewCount        int64   `json:"view_count"`
}

func decodeFile(t *testing.T, resp *http.Response) fileResp {
	t.Helper()
	defer resp.Body.Close()
	var f fileResp
	if err := json.NewDecoder(resp.Body).Decode(&f); err != nil {
		t.Fatalf("decode file response: %v", err)
	}
	return f
}

func bodyString(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func assertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Errorf("status = %d, want %d", resp.StatusCode, want)
	}
}

// createViaAPI creates a private file through the authenticated endpoint and
// returns the decoded response.
func (e *testEnv) createViaAPI(t *testing.T, cookie *http.Cookie, name, html string) fileResp {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"name": name, "html_content": html})
	resp := e.do(t, http.MethodPost, "/api/files", string(body), cookie)
	assertStatus(t, resp, http.StatusCreated)
	return decodeFile(t, resp)
}

func TestHealth(t *testing.T) {
	e := newEnv(t)
	resp := e.do(t, http.MethodGet, "/api/health", "", nil)
	assertStatus(t, resp, http.StatusOK)
	if b := bodyString(t, resp); !strings.Contains(b, `"status":"ok"`) {
		t.Errorf("health body = %q", b)
	}
}

func TestLogin(t *testing.T) {
	e := newEnv(t)

	// Bad JSON.
	resp := e.do(t, http.MethodPost, "/api/auth/login", "{not json", nil)
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Wrong credentials.
	resp = e.do(t, http.MethodPost, "/api/auth/login", `{"username":"admin","password":"nope"}`, nil)
	assertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()

	// Correct credentials -> 204 + Set-Cookie.
	resp = e.do(t, http.MethodPost, "/api/auth/login", `{"username":"admin","password":"s3cret"}`, nil)
	assertStatus(t, resp, http.StatusNoContent)
	var found bool
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookieName && c.Value != "" {
			found = true
		}
	}
	resp.Body.Close()
	if !found {
		t.Error("successful login did not set a session cookie")
	}
}

func TestMeAndLogout(t *testing.T) {
	e := newEnv(t)

	// Unauthenticated.
	resp := e.do(t, http.MethodGet, "/api/auth/me", "", nil)
	assertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()

	// Authenticated.
	cookie := e.authCookie(t)
	resp = e.do(t, http.MethodGet, "/api/auth/me", "", cookie)
	assertStatus(t, resp, http.StatusOK)
	var me struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	resp.Body.Close()
	if me.Username != testUser {
		t.Errorf("me username = %q, want %q", me.Username, testUser)
	}

	// Logout always 204.
	resp = e.do(t, http.MethodPost, "/api/auth/logout", "", cookie)
	assertStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	// The session should no longer be valid.
	resp = e.do(t, http.MethodGet, "/api/auth/me", "", cookie)
	assertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

func TestRequireAuthGuardsFiles(t *testing.T) {
	e := newEnv(t)
	resp := e.do(t, http.MethodGet, "/api/files", "", nil)
	assertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

func TestFilesCRUD(t *testing.T) {
	e := newEnv(t)
	cookie := e.authCookie(t)

	created := e.createViaAPI(t, cookie, "doc", "<h1>v1</h1>")
	if created.IsPublic {
		t.Error("new file should be private")
	}
	if created.HTMLContent != "" {
		t.Error("create response should omit html_content")
	}
	// Short generation scheme: base64(first 6 chars) -> exactly 8 chars.
	if len(created.Slug) != 8 {
		t.Errorf("slug length = %d, want 8", len(created.Slug))
	}
	if len(created.AccessCode) != 8 {
		t.Errorf("access code length = %d, want 8", len(created.AccessCode))
	}

	// Get returns full content.
	resp := e.do(t, http.MethodGet, "/api/files/"+created.Slug, "", cookie)
	assertStatus(t, resp, http.StatusOK)
	got := decodeFile(t, resp)
	if got.HTMLContent != "<h1>v1</h1>" {
		t.Errorf("Get html_content = %q", got.HTMLContent)
	}

	// List includes it.
	resp = e.do(t, http.MethodGet, "/api/files", "", cookie)
	assertStatus(t, resp, http.StatusOK)
	var list []fileResp
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	resp.Body.Close()
	if len(list) != 1 || list[0].Slug != created.Slug {
		t.Errorf("list = %d items, want the created file", len(list))
	}

	// Update name, slug, content, access code.
	upd := `{"name":"renamed","slug":"custom-slug","html_content":"<h1>v2</h1>","access_code":"mycode12"}`
	resp = e.do(t, http.MethodPatch, "/api/files/"+created.Slug, upd, cookie)
	assertStatus(t, resp, http.StatusOK)
	updated := decodeFile(t, resp)
	if updated.Slug != "custom-slug" || updated.Name != "renamed" {
		t.Errorf("update result = %+v", updated)
	}
	if updated.AccessCode != "mycode12" {
		t.Errorf("access code = %q, want custom code", updated.AccessCode)
	}

	// Old slug is gone.
	resp = e.do(t, http.MethodGet, "/api/files/"+created.Slug, "", cookie)
	assertStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestUpdateValidation(t *testing.T) {
	e := newEnv(t)
	cookie := e.authCookie(t)
	a := e.createViaAPI(t, cookie, "a", "<p>a</p>")
	b := e.createViaAPI(t, cookie, "b", "<p>b</p>")

	// Invalid slug.
	resp := e.do(t, http.MethodPatch, "/api/files/"+a.Slug,
		`{"name":"x","slug":"bad slug!","html_content":"<p>x</p>","access_code":"ok123456"}`, cookie)
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Empty content.
	resp = e.do(t, http.MethodPatch, "/api/files/"+a.Slug,
		`{"name":"x","slug":"ok","html_content":"","access_code":"ok123456"}`, cookie)
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Empty access code (would open the file to code-less requests).
	resp = e.do(t, http.MethodPatch, "/api/files/"+a.Slug,
		`{"name":"x","slug":"ok","html_content":"<p>x</p>","access_code":""}`, cookie)
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Access code with invalid characters.
	resp = e.do(t, http.MethodPatch, "/api/files/"+a.Slug,
		`{"name":"x","slug":"ok","html_content":"<p>x</p>","access_code":"has space"}`, cookie)
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Slug collision -> 409.
	resp = e.do(t, http.MethodPatch, "/api/files/"+a.Slug,
		`{"name":"x","slug":"`+b.Slug+`","html_content":"<p>x</p>","access_code":"ok123456"}`, cookie)
	assertStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
}

func TestCreateValidation(t *testing.T) {
	e := newEnv(t)
	cookie := e.authCookie(t)

	// Missing html_content.
	resp := e.do(t, http.MethodPost, "/api/files", `{"name":"x","html_content":""}`, cookie)
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Over 5MB.
	big := strings.Repeat("a", (5<<20)+1)
	body, _ := json.Marshal(map[string]string{"name": "big", "html_content": big})
	resp = e.do(t, http.MethodPost, "/api/files", string(body), cookie)
	assertStatus(t, resp, http.StatusRequestEntityTooLarge)
	resp.Body.Close()

	// Unknown kind -> 400.
	resp = e.do(t, http.MethodPost, "/api/files", `{"name":"x","kind":"pdf","html_content":"x"}`, cookie)
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()
}

func TestCreateKind(t *testing.T) {
	e := newEnv(t)
	cookie := e.authCookie(t)

	// An explicit kind round-trips in the response.
	resp := e.do(t, http.MethodPost, "/api/files", `{"name":"notes","kind":"markdown","html_content":"# hi"}`, cookie)
	assertStatus(t, resp, http.StatusCreated)
	if got := decodeFile(t, resp); got.Kind != "markdown" {
		t.Errorf("kind = %q, want markdown", got.Kind)
	}

	// Omitting kind defaults to html (back-compat).
	resp = e.do(t, http.MethodPost, "/api/files", `{"name":"page","html_content":"<p>x</p>"}`, cookie)
	assertStatus(t, resp, http.StatusCreated)
	if got := decodeFile(t, resp); got.Kind != "html" {
		t.Errorf("default kind = %q, want html", got.Kind)
	}
}

func TestTrashAndRestore(t *testing.T) {
	e := newEnv(t)
	cookie := e.authCookie(t)
	f := e.createViaAPI(t, cookie, "doc", "<p>x</p>")

	// Delete -> 204.
	resp := e.do(t, http.MethodDelete, "/api/files/"+f.Slug, "", cookie)
	assertStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	// Not in active list.
	resp = e.do(t, http.MethodGet, "/api/files", "", cookie)
	if b := bodyString(t, resp); strings.Contains(b, f.Slug) {
		t.Error("deleted file still in active list")
	}

	// In trashed list.
	resp = e.do(t, http.MethodGet, "/api/files?deleted=true", "", cookie)
	if b := bodyString(t, resp); !strings.Contains(b, f.Slug) {
		t.Error("deleted file not in trashed list")
	}

	// Restore -> 200.
	resp = e.do(t, http.MethodPost, "/api/files/"+f.Slug+"/restore", "", cookie)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Restoring again -> 404 (no longer deleted).
	resp = e.do(t, http.MethodPost, "/api/files/"+f.Slug+"/restore", "", cookie)
	assertStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestHardDelete(t *testing.T) {
	e := newEnv(t)
	cookie := e.authCookie(t)
	f := e.createViaAPI(t, cookie, "doc", "<p>x</p>")

	// Active file -> 404 (only trashed files can be purged).
	resp := e.do(t, http.MethodDelete, "/api/files/"+f.Slug+"/permanent", "", cookie)
	assertStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()

	// Soft delete, then purge -> 204.
	resp = e.do(t, http.MethodDelete, "/api/files/"+f.Slug, "", cookie)
	assertStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	resp = e.do(t, http.MethodDelete, "/api/files/"+f.Slug+"/permanent", "", cookie)
	assertStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	// Gone from the trashed list.
	resp = e.do(t, http.MethodGet, "/api/files?deleted=true", "", cookie)
	if b := bodyString(t, resp); strings.Contains(b, f.Slug) {
		t.Error("purged file still in trashed list")
	}

	// Purging again -> 404; restore is impossible too.
	resp = e.do(t, http.MethodDelete, "/api/files/"+f.Slug+"/permanent", "", cookie)
	assertStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
	resp = e.do(t, http.MethodPost, "/api/files/"+f.Slug+"/restore", "", cookie)
	assertStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()

	// The freed slug can be taken by another file.
	other := e.createViaAPI(t, cookie, "other", "<p>y</p>")
	upd := `{"name":"other","slug":"` + f.Slug + `","html_content":"<p>y</p>","access_code":"ok123456"}`
	resp = e.do(t, http.MethodPatch, "/api/files/"+other.Slug, upd, cookie)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

func TestSetExpiry(t *testing.T) {
	e := newEnv(t)
	cookie := e.authCookie(t)
	f := e.createViaAPI(t, cookie, "doc", "<p>x</p>")

	// max_views forces public.
	resp := e.do(t, http.MethodPatch, "/api/files/"+f.Slug+"/expiry", `{"max_views":3}`, cookie)
	assertStatus(t, resp, http.StatusOK)
	got := decodeFile(t, resp)
	if !got.IsPublic {
		t.Error("setting a view limit must force public")
	}
	if got.MaxViews == nil || *got.MaxViews != 3 {
		t.Errorf("max_views = %v, want 3", got.MaxViews)
	}

	// ttl sets an expiry timestamp.
	resp = e.do(t, http.MethodPatch, "/api/files/"+f.Slug+"/expiry", `{"ttl":"24h"}`, cookie)
	assertStatus(t, resp, http.StatusOK)
	got = decodeFile(t, resp)
	if got.ExpiresAt == nil {
		t.Error("ttl should set expires_at")
	}
	if got.MaxViews != nil {
		t.Error("ttl should clear max_views")
	}

	// Mutually exclusive -> 400.
	resp = e.do(t, http.MethodPatch, "/api/files/"+f.Slug+"/expiry", `{"ttl":"24h","max_views":5}`, cookie)
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Invalid ttl -> 400.
	resp = e.do(t, http.MethodPatch, "/api/files/"+f.Slug+"/expiry", `{"ttl":"99h"}`, cookie)
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Non-positive max_views -> 400.
	resp = e.do(t, http.MethodPatch, "/api/files/"+f.Slug+"/expiry", `{"max_views":0}`, cookie)
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Clearing keeps visibility (currently public) and nulls the limits.
	resp = e.do(t, http.MethodPatch, "/api/files/"+f.Slug+"/expiry", `{}`, cookie)
	assertStatus(t, resp, http.StatusOK)
	got = decodeFile(t, resp)
	if got.ExpiresAt != nil || got.MaxViews != nil {
		t.Error("clearing should null both limits")
	}
	if !got.IsPublic {
		t.Error("clearing must not change visibility")
	}
}

func TestRename(t *testing.T) {
	e := newEnv(t)
	cookie := e.authCookie(t)
	f := e.createViaAPI(t, cookie, "old name", "<p>x</p>")

	// Success.
	resp := e.do(t, http.MethodPatch, "/api/files/"+f.Slug+"/name", `{"name":"new name"}`, cookie)
	assertStatus(t, resp, http.StatusOK)
	if got := decodeFile(t, resp); got.Name != "new name" {
		t.Errorf("name = %q, want %q", got.Name, "new name")
	}

	// Empty/whitespace name -> 400.
	resp = e.do(t, http.MethodPatch, "/api/files/"+f.Slug+"/name", `{"name":"  "}`, cookie)
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Unknown slug -> 404.
	resp = e.do(t, http.MethodPatch, "/api/files/nope/name", `{"name":"x"}`, cookie)
	assertStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestVisibilityTagsRefresh(t *testing.T) {
	e := newEnv(t)
	cookie := e.authCookie(t)
	f := e.createViaAPI(t, cookie, "doc", "<p>x</p>")

	resp := e.do(t, http.MethodPatch, "/api/files/"+f.Slug+"/visibility", `{"is_public":true}`, cookie)
	assertStatus(t, resp, http.StatusOK)
	if got := decodeFile(t, resp); !got.IsPublic {
		t.Error("visibility not set to public")
	}

	resp = e.do(t, http.MethodPatch, "/api/files/"+f.Slug+"/tags", `{"tags":" a , b ,a "}`, cookie)
	assertStatus(t, resp, http.StatusOK)
	if got := decodeFile(t, resp); got.Tags != "a,b" {
		t.Errorf("tags = %q, want normalized %q", got.Tags, "a,b")
	}

	resp = e.do(t, http.MethodPost, "/api/files/"+f.Slug+"/refresh-code", "", cookie)
	assertStatus(t, resp, http.StatusOK)
	if got := decodeFile(t, resp); got.AccessCode == f.AccessCode || got.AccessCode == "" {
		t.Errorf("refresh-code did not produce a new code: %q", got.AccessCode)
	}
}

// createRaw inserts a file directly with a controlled slug/access_code/visibility,
// which the API's Create (random slug, private) can't express.
func (e *testEnv) createRaw(t *testing.T, slug, code, html string, public bool) {
	t.Helper()
	if _, err := e.queries.CreateFile(context.Background(), sqlcgen.CreateFileParams{
		Slug:        slug,
		Name:        slug,
		HtmlContent: html,
		Kind:        "html",
		IsPublic:    public,
		AccessCode:  code,
		UserID:      e.admin.ID,
	}); err != nil {
		t.Fatalf("CreateFile(raw): %v", err)
	}
}

// createRawKind is createRaw with an explicit kind, for exercising the
// non-html render paths.
func (e *testEnv) createRawKind(t *testing.T, slug, kind, source string) {
	t.Helper()
	if _, err := e.queries.CreateFile(context.Background(), sqlcgen.CreateFileParams{
		Slug:        slug,
		Name:        slug,
		HtmlContent: source,
		Kind:        kind,
		IsPublic:    true,
		AccessCode:  "c",
		UserID:      e.admin.ID,
	}); err != nil {
		t.Fatalf("CreateFile(raw %s): %v", kind, err)
	}
}

func TestRenderByKind(t *testing.T) {
	e := newEnv(t)

	// Markdown is rendered to HTML.
	e.createRawKind(t, "md", "markdown", "# Title\n\nsome **bold** text\n")
	resp := e.do(t, http.MethodGet, "/res/md?code=c", "", nil)
	assertStatus(t, resp, http.StatusOK)
	if b := bodyString(t, resp); !strings.Contains(b, "Title</h1>") || !strings.Contains(b, "<strong>bold</strong>") {
		t.Errorf("markdown not rendered to HTML: %q", b)
	}

	// Text is HTML-escaped and its newlines preserved (never interpreted).
	e.createRawKind(t, "note", "txt", "a <b> tag\nsecond line")
	resp = e.do(t, http.MethodGet, "/res/note?code=c", "", nil)
	assertStatus(t, resp, http.StatusOK)
	b := bodyString(t, resp)
	if strings.Contains(b, "<b> tag") {
		t.Errorf("txt content should be escaped, not interpreted: %q", b)
	}
	if !strings.Contains(b, "a &lt;b&gt; tag\nsecond line") {
		t.Errorf("txt content not preserved/escaped: %q", b)
	}
}

func TestRenderAccessControl(t *testing.T) {
	e := newEnv(t)

	// Missing slug -> 404.
	resp := e.do(t, http.MethodGet, "/res/nope", "", nil)
	assertStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()

	// Private file, no session -> 403.
	e.createRaw(t, "priv", "code1", "<h1>secret</h1>", false)
	resp = e.do(t, http.MethodGet, "/res/priv", "", nil)
	assertStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// Public file, wrong code -> 403.
	e.createRaw(t, "pub", "rightcode", "<h1>hello</h1>", true)
	resp = e.do(t, http.MethodGet, "/res/pub?code=wrong", "", nil)
	assertStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// Public file, correct code -> 200 + content, and code_success_count
	// bumps (anonymous access), not success_count (session access).
	resp = e.do(t, http.MethodGet, "/res/pub?code=rightcode", "", nil)
	assertStatus(t, resp, http.StatusOK)
	if b := bodyString(t, resp); b != "<h1>hello</h1>" {
		t.Errorf("render body = %q", b)
	}
	f, err := e.queries.GetFileBySlugAnyOwner(context.Background(), "pub")
	if err != nil {
		t.Fatalf("GetFileBySlugAnyOwner: %v", err)
	}
	if f.CodeSuccessCount != 1 {
		t.Errorf("code_success_count = %d, want 1", f.CodeSuccessCount)
	}
	if f.SuccessCount != 0 {
		t.Errorf("success_count = %d, want 0 for code-based access", f.SuccessCount)
	}
	if f.FailureCount != 1 { // from the wrong-code attempt above
		t.Errorf("failure_count = %d, want 1", f.FailureCount)
	}

	// Admin session bypasses the code on a private file and bumps
	// success_count, not code_success_count.
	cookie := e.authCookie(t)
	resp = e.do(t, http.MethodGet, "/res/priv", "", cookie)
	assertStatus(t, resp, http.StatusOK)
	if b := bodyString(t, resp); b != "<h1>secret</h1>" {
		t.Errorf("admin render body = %q", b)
	}
	f, err = e.queries.GetFileBySlugAnyOwner(context.Background(), "priv")
	if err != nil {
		t.Fatalf("GetFileBySlugAnyOwner: %v", err)
	}
	if f.SuccessCount != 1 {
		t.Errorf("success_count = %d, want 1 for session access", f.SuccessCount)
	}
	if f.CodeSuccessCount != 0 {
		t.Errorf("code_success_count = %d, want 0 for session access", f.CodeSuccessCount)
	}
}

func TestRenderDeletedIsNotFound(t *testing.T) {
	e := newEnv(t)
	cookie := e.authCookie(t)
	e.createRaw(t, "gone", "code", "<p>x</p>", true)
	if _, err := e.queries.SoftDeleteFile(context.Background(), sqlcgen.SoftDeleteFileParams{Slug: "gone", UserID: e.admin.ID}); err != nil {
		t.Fatalf("SoftDeleteFile: %v", err)
	}
	// Even an admin can't resurrect a deleted file via /res.
	resp := e.do(t, http.MethodGet, "/res/gone", "", cookie)
	assertStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestRenderViewLimitExpiry(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.createRaw(t, "limited", "c", "<h1>x</h1>", false)
	if _, err := e.queries.SetFileExpiry(ctx, sqlcgen.SetFileExpiryParams{
		MaxViews: sqlNullInt64(1),
		Slug:     "limited",
		UserID:   e.admin.ID,
	}); err != nil {
		t.Fatalf("SetFileExpiry: %v", err)
	}

	// First anonymous access consumes the single allowed view.
	resp := e.do(t, http.MethodGet, "/res/limited?code=c", "", nil)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Second anonymous access is over quota -> 403, and the file flips private.
	resp = e.do(t, http.MethodGet, "/res/limited?code=c", "", nil)
	assertStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	f, err := e.queries.GetFileBySlugAnyOwner(ctx, "limited")
	if err != nil {
		t.Fatalf("GetFileBySlugAnyOwner: %v", err)
	}
	if f.IsPublic {
		t.Error("file should be private after exhausting its view quota")
	}
	if f.MaxViews.Valid {
		t.Error("limits should be cleared after expiry")
	}
}

func TestRenderAdminDoesNotConsumeQuota(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	cookie := e.authCookie(t)
	e.createRaw(t, "quota", "c", "<h1>x</h1>", false)
	if _, err := e.queries.SetFileExpiry(ctx, sqlcgen.SetFileExpiryParams{
		MaxViews: sqlNullInt64(2),
		Slug:     "quota",
		UserID:   e.admin.ID,
	}); err != nil {
		t.Fatalf("SetFileExpiry: %v", err)
	}

	for i := 0; i < 3; i++ {
		resp := e.do(t, http.MethodGet, "/res/quota", "", cookie)
		assertStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}
	f, err := e.queries.GetFileBySlugAnyOwner(ctx, "quota")
	if err != nil {
		t.Fatalf("GetFileBySlugAnyOwner: %v", err)
	}
	if f.ViewCount != 0 {
		t.Errorf("admin views consumed quota: view_count = %d, want 0", f.ViewCount)
	}
	if !f.IsPublic {
		t.Error("file should remain public — admin views don't expire it")
	}
}

func TestDownload(t *testing.T) {
	e := newEnv(t)
	cookie := e.authCookie(t)
	f := e.createViaAPI(t, cookie, "report", "<h1>dl</h1>")

	// Unauthenticated -> 401 (requireAuth middleware).
	resp := e.do(t, http.MethodGet, "/api/files/"+f.Slug+"/download", "", nil)
	assertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()

	// Authenticated -> 200 with attachment disposition + raw content.
	resp = e.do(t, http.MethodGet, "/api/files/"+f.Slug+"/download", "", cookie)
	assertStatus(t, resp, http.StatusOK)
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if b := bodyString(t, resp); b != "<h1>dl</h1>" {
		t.Errorf("download body = %q", b)
	}

	// Missing slug -> 404.
	resp = e.do(t, http.MethodGet, "/api/files/nope/download", "", cookie)
	assertStatus(t, resp, http.StatusNotFound)
	resp.Body.Close()
}

func TestBackupDownload(t *testing.T) {
	e := newEnv(t)
	cookie := e.authCookie(t)
	// Put some data in the DB so the snapshot isn't trivially empty.
	e.createViaAPI(t, cookie, "doc", "<h1>backed up</h1>")

	// Unauthenticated -> 401 (requireAuth middleware).
	resp := e.do(t, http.MethodGet, "/api/backup", "", nil)
	assertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()

	// Authenticated -> 200, attachment, and a valid SQLite file.
	resp = e.do(t, http.MethodGet, "/api/backup", "", cookie)
	assertStatus(t, resp, http.StatusOK)
	if cd := resp.Header.Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	// A signed-in non-admin -> 403. The snapshot is the whole database, so
	// it carries every user's files, password hash and API key.
	_, otherCookie := e.newUser(t, "other")
	other := e.do(t, http.MethodGet, "/api/backup", "", otherCookie)
	assertStatus(t, other, http.StatusForbidden)
	other.Body.Close()

	body := bodyString(t, resp)
	// Every SQLite database file begins with this 16-byte magic header.
	if !strings.HasPrefix(body, "SQLite format 3\x00") {
		t.Errorf("backup body does not look like a SQLite database (first bytes: %q)", body[:min(16, len(body))])
	}
}

func TestSPAFallback(t *testing.T) {
	e := newEnv(t)
	resp := e.do(t, http.MethodGet, "/", "", nil)
	assertStatus(t, resp, http.StatusOK)
	if b := bodyString(t, resp); !strings.Contains(strings.ToLower(b), "<!doctype html") {
		t.Errorf("SPA fallback did not serve index.html: %q", b)
	}
}

func sqlNullInt64(v int64) sql.NullInt64 {
	return sql.NullInt64{Int64: v, Valid: true}
}

// cookieFrom extracts the session cookie set by a login/register/setup response.
func cookieFrom(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range resp.Cookies() {
		if c.Name == auth.SessionCookieName && c.Value != "" {
			return &http.Cookie{Name: c.Name, Value: c.Value}
		}
	}
	t.Fatal("response did not set a session cookie")
	return nil
}

func TestSetupFlow(t *testing.T) {
	e := newBareEnv(t)

	// Empty database → needs setup.
	resp := e.do(t, http.MethodGet, "/api/setup/status", "", nil)
	assertStatus(t, resp, http.StatusOK)
	if b := bodyString(t, resp); !strings.Contains(b, `"needs_setup":true`) {
		t.Errorf("status body = %q, want needs_setup true", b)
	}

	// Registration is blocked before setup.
	resp = e.do(t, http.MethodPost, "/api/auth/register",
		`{"username":"u","nickname":"n","password":"secret1"}`, nil)
	assertStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	// Setup creates the super admin, writes configs, and logs in.
	resp = e.do(t, http.MethodPost, "/api/setup",
		`{"username":"boss","nickname":"Boss","password":"secret1","allow_registration":true,"mcp_enabled":false}`, nil)
	assertStatus(t, resp, http.StatusCreated)
	cookie := cookieFrom(t, resp)
	resp.Body.Close()

	resp = e.do(t, http.MethodGet, "/api/auth/me", "", cookie)
	assertStatus(t, resp, http.StatusOK)
	if b := bodyString(t, resp); !strings.Contains(b, `"is_admin":true`) {
		t.Errorf("me body = %q, want is_admin true", b)
	}

	// Second setup attempt is rejected.
	resp = e.do(t, http.MethodPost, "/api/setup",
		`{"username":"evil","nickname":"E","password":"secret1"}`, nil)
	assertStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	resp = e.do(t, http.MethodGet, "/api/setup/status", "", nil)
	if b := bodyString(t, resp); !strings.Contains(b, `"needs_setup":false`) || !strings.Contains(b, `"allow_registration":true`) {
		t.Errorf("status body after setup = %q", b)
	}
}

func TestRegisterRespectsConfig(t *testing.T) {
	e := newEnv(t) // super admin exists, no configs → registration disabled
	resp := e.do(t, http.MethodPost, "/api/auth/register",
		`{"username":"u2","nickname":"N","password":"secret1"}`, nil)
	assertStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// Admin enables registration.
	cookie := e.authCookie(t)
	resp = e.do(t, http.MethodPut, "/api/settings", `{"allow_registration":true}`, cookie)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Now registration works and logs the new user in as a non-admin.
	resp = e.do(t, http.MethodPost, "/api/auth/register",
		`{"username":"u2","nickname":"N","password":"secret1"}`, nil)
	assertStatus(t, resp, http.StatusCreated)
	userCookie := cookieFrom(t, resp)
	resp.Body.Close()

	resp = e.do(t, http.MethodGet, "/api/auth/me", "", userCookie)
	assertStatus(t, resp, http.StatusOK)
	if b := bodyString(t, resp); !strings.Contains(b, `"is_admin":false`) {
		t.Errorf("me body = %q, want is_admin false", b)
	}

	// Duplicate username → 409; weak password → 400.
	resp = e.do(t, http.MethodPost, "/api/auth/register",
		`{"username":"u2","nickname":"N","password":"secret1"}`, nil)
	assertStatus(t, resp, http.StatusConflict)
	resp.Body.Close()
	resp = e.do(t, http.MethodPost, "/api/auth/register",
		`{"username":"u3","nickname":"N","password":"short"}`, nil)
	assertStatus(t, resp, http.StatusBadRequest)
	resp.Body.Close()

	// Non-admin users cannot change settings.
	resp = e.do(t, http.MethodPut, "/api/settings", `{"allow_registration":false}`, userCookie)
	assertStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
}

func TestUpdateProfile(t *testing.T) {
	e := newEnv(t)
	cookie := e.authCookie(t)

	// Nickname change.
	resp := e.do(t, http.MethodPatch, "/api/user", `{"nickname":"New Name"}`, cookie)
	assertStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	resp = e.do(t, http.MethodGet, "/api/auth/me", "", cookie)
	if b := bodyString(t, resp); !strings.Contains(b, `"nickname":"New Name"`) {
		t.Errorf("me body = %q, want updated nickname", b)
	}

	// Password change requires the current password.
	resp = e.do(t, http.MethodPatch, "/api/user",
		`{"current_password":"wrong","new_password":"secret2"}`, cookie)
	assertStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()
	resp = e.do(t, http.MethodPatch, "/api/user",
		`{"current_password":"`+testPass+`","new_password":"secret2"}`, cookie)
	assertStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	// Old password no longer logs in; the new one does.
	resp = e.do(t, http.MethodPost, "/api/auth/login",
		`{"username":"`+testUser+`","password":"`+testPass+`"}`, nil)
	assertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
	resp = e.do(t, http.MethodPost, "/api/auth/login",
		`{"username":"`+testUser+`","password":"secret2"}`, nil)
	assertStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
}

func TestAPIKeyLifecycle(t *testing.T) {
	e := newEnv(t)
	cookie := e.authCookie(t)

	// MCP disabled → no key issued.
	resp := e.do(t, http.MethodPost, "/api/user/api-key", "", cookie)
	assertStatus(t, resp, http.StatusConflict)
	resp.Body.Close()

	resp = e.do(t, http.MethodPut, "/api/settings", `{"mcp_enabled":true}`, cookie)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	keyOf := func(resp *http.Response) string {
		t.Helper()
		defer resp.Body.Close()
		var body struct {
			APIKey string `json:"api_key"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode api key: %v", err)
		}
		return body.APIKey
	}

	// First ensure creates a key; a second ensure returns the same key.
	resp = e.do(t, http.MethodPost, "/api/user/api-key", "", cookie)
	assertStatus(t, resp, http.StatusOK)
	first := keyOf(resp)
	if !strings.HasPrefix(first, "rb_") {
		t.Errorf("api key = %q, want rb_ prefix", first)
	}
	resp = e.do(t, http.MethodPost, "/api/user/api-key", "", cookie)
	if again := keyOf(resp); again != first {
		t.Errorf("ensure returned a different key: %q vs %q", again, first)
	}

	// Reset issues a fresh key.
	resp = e.do(t, http.MethodPost, "/api/user/api-key/reset", "", cookie)
	if fresh := keyOf(resp); fresh == first {
		t.Error("reset should replace the key")
	}
}

type searchResp struct {
	Slug           string `json:"slug"`
	Name           string `json:"name"`
	MatchedName    bool   `json:"matched_name"`
	MatchedContent bool   `json:"matched_content"`
	Snippet        string `json:"snippet"`
}

func (e *testEnv) search(t *testing.T, cookie *http.Cookie, query string) []searchResp {
	t.Helper()
	resp := e.do(t, http.MethodGet, "/api/files/search?"+query, "", cookie)
	assertStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()
	var out []searchResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	return out
}

func TestSearchFiles(t *testing.T) {
	e := newEnv(t)
	cookie := e.authCookie(t)

	e.createViaAPI(t, cookie, "hello world", "<p>nothing interesting</p>")
	e.createViaAPI(t, cookie, "report", "<p>padding "+strings.Repeat("x", 300)+" hello inside the content "+strings.Repeat("y", 300)+"</p>")
	e.createViaAPI(t, cookie, "unrelated", "<p>zzz</p>")

	// Name-only search matches just the title hit, case-insensitively.
	results := e.search(t, cookie, "q=HELLO")
	if len(results) != 1 || results[0].Name != "hello world" {
		t.Fatalf("name search = %+v, want only 'hello world'", results)
	}
	if !results[0].MatchedName || results[0].Snippet != "" {
		t.Errorf("name hit = %+v, want matched_name and no snippet", results[0])
	}

	// Content search adds the content-only hit with a windowed snippet.
	results = e.search(t, cookie, "q=hello&content=true")
	if len(results) != 2 {
		t.Fatalf("content search returned %d results, want 2", len(results))
	}
	var contentHit *searchResp
	for i := range results {
		if results[i].Name == "report" {
			contentHit = &results[i]
		}
	}
	if contentHit == nil {
		t.Fatal("content search missing the content-only hit")
	}
	if contentHit.MatchedName || !contentHit.MatchedContent {
		t.Errorf("content hit flags = %+v", contentHit)
	}
	if !strings.Contains(contentHit.Snippet, "hello inside the content") {
		t.Errorf("snippet %q missing the match", contentHit.Snippet)
	}
	if !strings.HasPrefix(contentHit.Snippet, "…") || !strings.HasSuffix(contentHit.Snippet, "…") {
		t.Errorf("snippet %q should be truncated on both sides", contentHit.Snippet)
	}
	if n := len([]rune(contentHit.Snippet)); n > 2*100+len("hello inside the content")+2 {
		t.Errorf("snippet is %d runes, want ≤ match+200+ellipses", n)
	}

	// Empty query → empty result set, not an error.
	if results := e.search(t, cookie, "q="); len(results) != 0 {
		t.Errorf("empty query returned %d results, want 0", len(results))
	}

	// Search requires auth.
	resp := e.do(t, http.MethodGet, "/api/files/search?q=hello", "", nil)
	assertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
}

func TestSearchScopedToOwnFiles(t *testing.T) {
	e := newEnv(t)
	adminCookie := e.authCookie(t)
	e.createViaAPI(t, adminCookie, "hello admin file", "<p>x</p>")

	// A second user with their own session sees only their own files.
	_, otherCookie := e.newUser(t, "other")

	e.createViaAPI(t, otherCookie, "hello other file", "<p>y</p>")

	adminResults := e.search(t, adminCookie, "q=hello&content=true")
	if len(adminResults) != 1 || adminResults[0].Name != "hello admin file" {
		t.Errorf("admin search = %+v, want only their own file", adminResults)
	}
	otherResults := e.search(t, otherCookie, "q=hello&content=true")
	if len(otherResults) != 1 || otherResults[0].Name != "hello other file" {
		t.Errorf("other search = %+v, want only their own file", otherResults)
	}
}

// --- MCP endpoint ---

// enableMCP turns the mcp_enabled config on and issues the admin an API key.
func (e *testEnv) enableMCP(t *testing.T) string {
	t.Helper()
	if err := e.queries.SetConfig(context.Background(), sqlcgen.SetConfigParams{Key: "mcp_enabled", Value: "true"}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	key, err := auth.NewAPIKey()
	if err != nil {
		t.Fatalf("NewAPIKey: %v", err)
	}
	if err := e.queries.SetUserAPIKey(context.Background(), sqlcgen.SetUserAPIKeyParams{
		ApiKey: sql.NullString{String: key, Valid: true}, ID: e.admin.ID,
	}); err != nil {
		t.Fatalf("SetUserAPIKey: %v", err)
	}
	return key
}

// mcpCall POSTs a raw JSON-RPC tools/call to /mcp. Stateless mode means each
// call is self-contained — no initialize handshake needed.
func (e *testEnv) mcpCall(t *testing.T, apiKey, tool string, args map[string]any) *http.Response {
	t.Helper()
	argJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	body := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, tool, argJSON)
	req, err := http.NewRequest(http.MethodPost, e.srv.URL+"/mcp", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	return resp
}

type mcpToolResult struct {
	IsError bool `json:"isError"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StructuredContent map[string]any `json:"structuredContent"`
}

func (r mcpToolResult) text() string {
	var parts []string
	for _, c := range r.Content {
		parts = append(parts, c.Text)
	}
	return strings.Join(parts, "\n")
}

func decodeMCPResult(t *testing.T, resp *http.Response) mcpToolResult {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("mcp status = %d, body %q", resp.StatusCode, b)
	}
	var envelope struct {
		Result mcpToolResult `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode mcp response: %v", err)
	}
	if envelope.Error != nil {
		t.Fatalf("mcp protocol error %d: %s", envelope.Error.Code, envelope.Error.Message)
	}
	return envelope.Result
}

// mcpUpload uploads one file over MCP and returns its structured output.
func (e *testEnv) mcpUpload(t *testing.T, key, name, kind, content string) map[string]any {
	t.Helper()
	res := decodeMCPResult(t, e.mcpCall(t, key, "upload_file", map[string]any{
		"name": name, "kind": kind, "content": content,
	}))
	if res.IsError {
		t.Fatalf("upload_file error: %s", res.text())
	}
	return res.StructuredContent
}

// pathOf strips the test server origin so the URL can be fetched via e.do.
func (e *testEnv) pathOf(t *testing.T, url string) string {
	t.Helper()
	if !strings.HasPrefix(url, e.srv.URL) {
		t.Fatalf("url %q does not start with server origin %q", url, e.srv.URL)
	}
	return strings.TrimPrefix(url, e.srv.URL)
}

func TestMCPGateAndAuth(t *testing.T) {
	e := newEnv(t)

	// mcp_enabled off (default) → 403 for everyone.
	resp := e.mcpCall(t, "some-key", "search_files", map[string]any{"query": "x"})
	assertStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	key := e.enableMCP(t)

	// Missing and invalid bearer tokens → 401.
	resp = e.mcpCall(t, "", "search_files", map[string]any{"query": "x"})
	assertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()
	resp = e.mcpCall(t, "rb_wrong", "search_files", map[string]any{"query": "x"})
	assertStatus(t, resp, http.StatusUnauthorized)
	resp.Body.Close()

	// Valid key works.
	res := decodeMCPResult(t, e.mcpCall(t, key, "search_files", map[string]any{"query": "x"}))
	if res.IsError {
		t.Errorf("valid key got tool error: %s", res.text())
	}
}

func TestMCPUploadPublishFlow(t *testing.T) {
	e := newEnv(t)
	key := e.enableMCP(t)

	out := e.mcpUpload(t, key, "发布测试", "markdown", "# Hello MCP")
	url, _ := out["url"].(string)
	slug, _ := out["slug"].(string)
	if url == "" || slug == "" {
		t.Fatalf("upload output missing url/slug: %v", out)
	}
	if isPublic, _ := out["is_public"].(bool); isPublic {
		t.Error("MCP uploads must start private")
	}

	// Private → the returned URL is not anonymously viewable yet.
	resp := e.do(t, http.MethodGet, e.pathOf(t, url), "", nil)
	assertStatus(t, resp, http.StatusForbidden)
	resp.Body.Close()

	// publish_file → same URL becomes anonymously viewable, rendered as HTML.
	res := decodeMCPResult(t, e.mcpCall(t, key, "publish_file", map[string]any{"slug": slug}))
	if res.IsError {
		t.Fatalf("publish_file error: %s", res.text())
	}
	resp = e.do(t, http.MethodGet, e.pathOf(t, url), "", nil)
	assertStatus(t, resp, http.StatusOK)
	if b := bodyString(t, resp); !strings.Contains(b, "<h1") || !strings.Contains(b, "Hello MCP") {
		t.Errorf("published markdown did not render: %q", b)
	}
}

func TestMCPUploadValidation(t *testing.T) {
	e := newEnv(t)
	key := e.enableMCP(t)

	// txt is a web-UI kind but not an MCP upload kind.
	res := decodeMCPResult(t, e.mcpCall(t, key, "upload_file", map[string]any{
		"name": "x", "kind": "txt", "content": "plain",
	}))
	if !res.IsError || !strings.Contains(res.text(), "markdown or html") {
		t.Errorf("txt upload = %v %q, want kind error", res.IsError, res.text())
	}

	res = decodeMCPResult(t, e.mcpCall(t, key, "upload_file", map[string]any{
		"name": "x", "kind": "html", "content": "",
	}))
	if !res.IsError || !strings.Contains(res.text(), "content is required") {
		t.Errorf("empty content = %v %q, want content error", res.IsError, res.text())
	}
}

func TestMCPBatchUpload(t *testing.T) {
	e := newEnv(t)
	key := e.enableMCP(t)

	res := decodeMCPResult(t, e.mcpCall(t, key, "upload_files", map[string]any{
		"files": []map[string]any{
			{"name": "a", "kind": "markdown", "content": "# a"},
			{"name": "b", "kind": "html", "content": "<p>b</p>"},
			{"name": "bad", "kind": "pdf", "content": "x"},
		},
	}))
	if res.IsError {
		t.Fatalf("upload_files error: %s", res.text())
	}
	if got := res.StructuredContent["uploaded"].(float64); got != 2 {
		t.Errorf("uploaded = %v, want 2", got)
	}
	if got := res.StructuredContent["failed"].(float64); got != 1 {
		t.Errorf("failed = %v, want 1", got)
	}

	// Both successful files landed in the DB, attributed to the admin.
	files, err := e.queries.ListUserFiles(context.Background(), e.admin.ID)
	if err != nil {
		t.Fatalf("ListUserFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("db has %d files, want 2", len(files))
	}
	for _, f := range files {
		if f.UserID != e.admin.ID {
			t.Errorf("file %q user_id = %d, want %d", f.Slug, f.UserID, e.admin.ID)
		}
	}
}

func TestMCPSearch(t *testing.T) {
	e := newEnv(t)
	key := e.enableMCP(t)

	e.mcpUpload(t, key, "部署文档", "markdown", "# 部署\n\n如何部署本项目。")
	e.mcpUpload(t, key, "另一篇", "html", "<p>这里提到了部署流程的细节。</p>")
	e.mcpUpload(t, key, "无关", "html", "<p>nothing</p>")

	res := decodeMCPResult(t, e.mcpCall(t, key, "search_files", map[string]any{"query": "部署"}))
	if res.IsError {
		t.Fatalf("search_files error: %s", res.text())
	}
	results := res.StructuredContent["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("search returned %d results, want 2", len(results))
	}
	// The content-only hit carries a snippet; the URL is present on both.
	var contentHit map[string]any
	for _, r := range results {
		m := r.(map[string]any)
		if m["url"] == nil || m["url"] == "" {
			t.Errorf("result missing url: %v", m)
		}
		if m["name"] == "另一篇" {
			contentHit = m
		}
	}
	if contentHit == nil || contentHit["snippet"] == nil || !strings.Contains(contentHit["snippet"].(string), "部署") {
		t.Errorf("content-only hit missing snippet: %v", contentHit)
	}

	// Miss → found=false with a human-readable message.
	res = decodeMCPResult(t, e.mcpCall(t, key, "search_files", map[string]any{"query": "不存在的词"}))
	if res.IsError {
		t.Fatalf("search miss errored: %s", res.text())
	}
	if found := res.StructuredContent["found"].(bool); found {
		t.Error("miss should report found=false")
	}
	if !strings.Contains(res.text(), "No documents matching") {
		t.Errorf("miss text = %q", res.text())
	}
}

func TestMCPUpdate(t *testing.T) {
	e := newEnv(t)
	key := e.enableMCP(t)

	out := e.mcpUpload(t, key, "旧标题", "markdown", "# v1")
	slug := out["slug"].(string)
	before, err := e.queries.GetFileBySlugAnyOwner(context.Background(), slug)
	if err != nil {
		t.Fatalf("GetFileBySlugAnyOwner: %v", err)
	}

	// Neither field → tool error.
	res := decodeMCPResult(t, e.mcpCall(t, key, "update_file", map[string]any{"slug": slug}))
	if !res.IsError {
		t.Error("update with nothing to change should error")
	}

	res = decodeMCPResult(t, e.mcpCall(t, key, "update_file", map[string]any{
		"slug": slug, "name": "新标题", "content": "# v2",
	}))
	if res.IsError {
		t.Fatalf("update_file error: %s", res.text())
	}

	after, err := e.queries.GetFileBySlugAnyOwner(context.Background(), slug)
	if err != nil {
		t.Fatalf("GetFileBySlugAnyOwner after update: %v", err)
	}
	if after.Name != "新标题" || after.HtmlContent != "# v2" {
		t.Errorf("update result = %q %q", after.Name, after.HtmlContent)
	}
	if after.Slug != before.Slug || after.AccessCode != before.AccessCode || after.Kind != before.Kind {
		t.Error("update must not change slug, access code, or kind")
	}
}

func TestMCPDeleteTwoPhase(t *testing.T) {
	e := newEnv(t)
	key := e.enableMCP(t)

	out := e.mcpUpload(t, key, "待删除", "html", "<p>bye</p>")
	slug := out["slug"].(string)
	url := out["url"].(string)

	// Phase 1: no confirm → nothing deleted, name and URL surfaced for the user.
	res := decodeMCPResult(t, e.mcpCall(t, key, "delete_file", map[string]any{"slug": slug}))
	if res.IsError {
		t.Fatalf("delete phase 1 errored: %s", res.text())
	}
	if deleted := res.StructuredContent["deleted"].(bool); deleted {
		t.Error("phase 1 must not delete")
	}
	if !strings.Contains(res.text(), "待删除") || !strings.Contains(res.text(), url) {
		t.Errorf("phase 1 text %q must carry name and URL", res.text())
	}
	if _, err := e.queries.GetFileBySlugAnyOwner(context.Background(), slug); err != nil {
		t.Fatalf("file should still exist after phase 1: %v", err)
	}

	// Phase 2: confirm=true → soft-deleted (in trash, not gone permanently).
	res = decodeMCPResult(t, e.mcpCall(t, key, "delete_file", map[string]any{"slug": slug, "confirm": true}))
	if res.IsError {
		t.Fatalf("delete phase 2 errored: %s", res.text())
	}
	if deleted := res.StructuredContent["deleted"].(bool); !deleted {
		t.Error("phase 2 should report deleted=true")
	}
	if _, err := e.queries.GetFileBySlugAnyOwner(context.Background(), slug); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("file should be soft-deleted, got err=%v", err)
	}
	trashed, err := e.queries.ListUserDeletedFiles(context.Background(), e.admin.ID)
	if err != nil {
		t.Fatalf("ListUserDeletedFiles: %v", err)
	}
	if len(trashed) != 1 || trashed[0].Slug != slug {
		t.Errorf("trash = %v, want the deleted file", trashed)
	}
}

func TestMCPOwnershipIsolation(t *testing.T) {
	e := newEnv(t)
	key := e.enableMCP(t)

	// A second user owns a file; the admin's key must not see or touch it.
	other, _ := e.newUser(t, "other")
	theirs, err := e.queries.CreateFile(context.Background(), sqlcgen.CreateFileParams{
		Slug: "their-doc", Name: "机密文档", HtmlContent: "<p>secret</p>", Kind: "html",
		AccessCode: "code1234", UserID: other.ID,
	})
	if err != nil {
		t.Fatalf("CreateFile: %v", err)
	}

	// search does not surface it…
	res := decodeMCPResult(t, e.mcpCall(t, key, "search_files", map[string]any{"query": "机密"}))
	if found := res.StructuredContent["found"].(bool); found {
		t.Error("search must not return another user's files")
	}
	// …and update/publish/delete all answer "not found".
	for _, call := range []struct {
		tool string
		args map[string]any
	}{
		{"update_file", map[string]any{"slug": theirs.Slug, "name": "hacked"}},
		{"publish_file", map[string]any{"slug": theirs.Slug}},
		{"delete_file", map[string]any{"slug": theirs.Slug, "confirm": true}},
	} {
		res := decodeMCPResult(t, e.mcpCall(t, key, call.tool, call.args))
		if !res.IsError || !strings.Contains(res.text(), "not found") {
			t.Errorf("%s on foreign file = %v %q, want not-found error", call.tool, res.IsError, res.text())
		}
	}
	if _, err := e.queries.GetFileBySlugAnyOwner(context.Background(), theirs.Slug); err != nil {
		t.Errorf("foreign file should be untouched: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Owner isolation.
//
// Every /api/files endpoint is owner-scoped, so another user's slug must be
// indistinguishable from one that never existed: 404, identical body, and no
// mutation. These tests are the executable form of that rule.
// ---------------------------------------------------------------------------

// foreignCase is one endpoint exercised as a non-owner. body must be valid
// enough to pass the handler's own validation, or the request would 400
// before it ever reaches the ownership check and the case would prove nothing.
type foreignCase struct {
	method string
	path   string // %s is replaced by the victim's slug
	body   string
	// trashed marks cases that only make sense against a soft-deleted file.
	trashed bool
}

// foreignCases must cover every slug-keyed route under /api/files.
// TestFileEndpointsCoverAllRoutes fails if a new route appears without one.
var foreignCases = []foreignCase{
	{method: http.MethodGet, path: "/api/files/%s"},
	{method: http.MethodGet, path: "/api/files/%s/download"},
	{method: http.MethodPatch, path: "/api/files/%s",
		body: `{"name":"hacked","slug":"hacked-slug","html_content":"<p>hacked</p>","access_code":"hackedcode"}`},
	{method: http.MethodPatch, path: "/api/files/%s/name", body: `{"name":"hacked"}`},
	{method: http.MethodPatch, path: "/api/files/%s/visibility", body: `{"is_public":true}`},
	{method: http.MethodPatch, path: "/api/files/%s/tags", body: `{"tags":"hacked"}`},
	{method: http.MethodPatch, path: "/api/files/%s/expiry", body: `{"ttl":"24h"}`},
	{method: http.MethodPost, path: "/api/files/%s/refresh-code"},
	{method: http.MethodDelete, path: "/api/files/%s"},
	{method: http.MethodPost, path: "/api/files/%s/restore", trashed: true},
	{method: http.MethodDelete, path: "/api/files/%s/permanent", trashed: true},
}

// fingerprint is every column a foreignCase could plausibly mutate, so the
// "nothing changed" assertion catches an endpoint that 404s and writes anyway.
func (e *testEnv) fingerprint(t *testing.T, slug string) string {
	t.Helper()
	f, err := e.queries.GetFileBySlugAnyOwner(context.Background(), slug)
	if err != nil {
		t.Fatalf("fingerprint %q: %v", slug, err)
	}
	return fmt.Sprintf("name=%q content=%q public=%v code=%q tags=%q expires=%v maxviews=%v",
		f.Name, f.HtmlContent, f.IsPublic, f.AccessCode, f.Tags, f.ExpiresAt.Valid, f.MaxViews.Valid)
}

// trashFingerprint is fingerprint for a soft-deleted file, which
// GetFileBySlugAnyOwner filters out by design.
func (e *testEnv) trashFingerprint(t *testing.T, ownerID int64, slug string) string {
	t.Helper()
	rows, err := e.queries.ListUserDeletedFiles(context.Background(), ownerID)
	if err != nil {
		t.Fatalf("ListUserDeletedFiles: %v", err)
	}
	for _, f := range rows {
		if f.Slug == slug {
			return fmt.Sprintf("name=%q content=%q public=%v code=%q tags=%q",
				f.Name, f.HtmlContent, f.IsPublic, f.AccessCode, f.Tags)
		}
	}
	t.Fatalf("file %q is no longer in user %d's trash", slug, ownerID)
	return ""
}

func TestFileEndpointsRejectForeignSlug(t *testing.T) {
	// Run both directions: the super admin gets no exception on file
	// endpoints, which is otherwise an invisible policy someone could
	// "fix" as a bug.
	for _, dir := range []struct {
		name              string
		ownerIsSuperAdmin bool
	}{
		{"owner=admin attacker=user", true},
		{"owner=user attacker=admin", false},
	} {
		t.Run(dir.name, func(t *testing.T) {
			e := newEnv(t)
			adminCookie := e.authCookie(t)
			user, userCookie := e.newUser(t, "other")

			ownerCookie, attackerCookie := adminCookie, userCookie
			ownerID := e.admin.ID
			if !dir.ownerIsSuperAdmin {
				ownerCookie, attackerCookie = userCookie, adminCookie
				ownerID = user.ID
			}

			active := e.createViaAPI(t, ownerCookie, "victim active", "<p>secret</p>")
			trashed := e.createViaAPI(t, ownerCookie, "victim trashed", "<p>secret2</p>")
			resp := e.do(t, http.MethodDelete, "/api/files/"+trashed.Slug, "", ownerCookie)
			assertStatus(t, resp, http.StatusNoContent)
			resp.Body.Close()

			activeBefore := e.fingerprint(t, active.Slug)
			trashedBefore := e.trashFingerprint(t, ownerID, trashed.Slug)

			for _, c := range foreignCases {
				victim := active.Slug
				if c.trashed {
					victim = trashed.Slug
				}
				path := fmt.Sprintf(c.path, victim)
				resp := e.do(t, c.method, path, c.body, attackerCookie)
				if resp.StatusCode != http.StatusNotFound {
					t.Errorf("%s %s as non-owner = %d, want 404", c.method, path, resp.StatusCode)
				}
				resp.Body.Close()
			}

			if got := e.fingerprint(t, active.Slug); got != activeBefore {
				t.Errorf("active file was mutated by a rejected request:\n got %s\nwant %s", got, activeBefore)
			}
			// Still in the owner's trash: neither restored nor purged.
			if got := e.trashFingerprint(t, ownerID, trashed.Slug); got != trashedBefore {
				t.Errorf("trashed file was mutated by a rejected request:\n got %s\nwant %s", got, trashedBefore)
			}
		})
	}
}

// TestFileEndpointsCoverAllRoutes walks the real router so that adding a
// /api/files endpoint without an ownership case fails a test that already
// exists, rather than silently going untested.
func TestFileEndpointsCoverAllRoutes(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range foreignCases {
		// Normalise "/api/files/%s/name" to chi's "/api/files/{slug}/name".
		covered[c.method+" "+strings.ReplaceAll(c.path, "%s", "{slug}")] = true
	}
	// Routes that are not slug-keyed and so cannot leak another user's file.
	notSlugKeyed := map[string]bool{
		http.MethodGet + " /api/files":        true, // covered by TestListScopedToOwner
		http.MethodPost + " /api/files":       true, // creates, attributed to caller
		http.MethodGet + " /api/files/search": true, // covered by TestSearchScopedToOwnFiles
	}

	seen := 0
	err := chi.Walk(server.New(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil))).(*chi.Mux),
		func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			route = strings.TrimSuffix(route, "/")
			if !strings.HasPrefix(route, "/api/files") {
				return nil
			}
			seen++
			key := method + " " + route
			if !covered[key] && !notSlugKeyed[key] {
				t.Errorf("route %q has no ownership test. Every /api/files endpoint must be "+
					"owner-scoped: add a foreignCase (or list it in notSlugKeyed with a reason).", key)
			}
			return nil
		})
	if err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	// A prefix filter that matches nothing would make this test vacuous.
	if want := len(foreignCases) + len(notSlugKeyed); seen != want {
		t.Errorf("walked %d /api/files routes, expected %d — the route table and the "+
			"ownership table have drifted apart", seen, want)
	}
}

func TestListScopedToOwner(t *testing.T) {
	e := newEnv(t)
	adminCookie := e.authCookie(t)
	_, otherCookie := e.newUser(t, "other")

	e.createViaAPI(t, adminCookie, "admin one", "<p>1</p>")
	adminTrash := e.createViaAPI(t, adminCookie, "admin two", "<p>2</p>")
	otherFile := e.createViaAPI(t, otherCookie, "other one", "<p>3</p>")

	list := func(cookie *http.Cookie, path string) []fileResp {
		t.Helper()
		resp := e.do(t, http.MethodGet, path, "", cookie)
		assertStatus(t, resp, http.StatusOK)
		defer resp.Body.Close()
		var out []fileResp
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode list: %v", err)
		}
		return out
	}

	if got := list(adminCookie, "/api/files"); len(got) != 2 {
		t.Errorf("admin sees %d files, want only their own 2", len(got))
	}
	other := list(otherCookie, "/api/files")
	if len(other) != 1 || other[0].Slug != otherFile.Slug {
		t.Errorf("other user sees %+v, want only their own file", other)
	}

	// Trash is scoped too.
	resp := e.do(t, http.MethodDelete, "/api/files/"+adminTrash.Slug, "", adminCookie)
	assertStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()

	if got := list(adminCookie, "/api/files?deleted=true"); len(got) != 1 {
		t.Errorf("admin trash has %d files, want 1", len(got))
	}
	if got := list(otherCookie, "/api/files?deleted=true"); len(got) != 0 {
		t.Errorf("other user's trash = %+v, want empty", got)
	}
}

// TestRenderIgnoresForeignSession pins the /res/{slug} half of the same bug:
// a session used to bypass the access code for *any* file, not just the
// viewer's own.
func TestRenderIgnoresForeignSession(t *testing.T) {
	e := newEnv(t)
	adminCookie := e.authCookie(t)
	_, otherCookie := e.newUser(t, "other")

	file := e.createViaAPI(t, adminCookie, "private", "<h1>secret</h1>")

	// A signed-in stranger gets no more than an anonymous visitor: the file
	// is private, so no code can help and the attempt is counted as failed.
	resp := e.do(t, http.MethodGet, "/res/"+file.Slug, "", otherCookie)
	assertStatus(t, resp, http.StatusForbidden)
	if b := bodyString(t, resp); strings.Contains(b, "secret") {
		t.Error("a foreign session must not receive the file's content")
	}
	after, err := e.queries.GetFileBySlugAnyOwner(context.Background(), file.Slug)
	if err != nil {
		t.Fatalf("GetFileBySlugAnyOwner: %v", err)
	}
	if after.FailureCount != 1 || after.SuccessCount != 0 {
		t.Errorf("counters = success %d / failure %d, want 0 / 1",
			after.SuccessCount, after.FailureCount)
	}

	// The owner still gets in without a code.
	resp = e.do(t, http.MethodGet, "/res/"+file.Slug, "", adminCookie)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	// Once public, the stranger's correct code works and counts as a
	// code-based view rather than an owner view.
	resp = e.do(t, http.MethodPatch, "/api/files/"+file.Slug+"/visibility", `{"is_public":true}`, adminCookie)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	resp = e.do(t, http.MethodGet, "/res/"+file.Slug+"?code="+file.AccessCode, "", otherCookie)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	after, err = e.queries.GetFileBySlugAnyOwner(context.Background(), file.Slug)
	if err != nil {
		t.Fatalf("GetFileBySlugAnyOwner: %v", err)
	}
	if after.CodeSuccessCount != 1 || after.SuccessCount != 1 {
		t.Errorf("counters = success %d / code %d, want 1 / 1",
			after.SuccessCount, after.CodeSuccessCount)
	}
}
