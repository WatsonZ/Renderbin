// Package server assembles the chi router: API routes under /api and the
// embedded SvelteKit SPA (with client-side-routing fallback) for everything else.
package server

import (
	"database/sql"
	"io/fs"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/shawn-bluce/renderbin/backend/internal/db/sqlcgen"
	"github.com/shawn-bluce/renderbin/backend/internal/handlers"
	"github.com/shawn-bluce/renderbin/backend/internal/web"
)

func New(queries *sqlcgen.Queries, conn *sql.DB, logger *slog.Logger) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(slogRequestLogger(logger))

	authH := handlers.NewAuthHandler(queries, logger)
	setupH := handlers.NewSetupHandler(queries, authH, logger)
	settingsH := handlers.NewSettingsHandler(queries, logger)
	files := handlers.NewFilesHandler(queries, logger)
	backupH := handlers.NewBackupHandler(conn, logger)

	r.Route("/api", func(r chi.Router) {
		r.Get("/health", handlers.Health)

		r.Get("/setup/status", setupH.Status)
		r.Post("/setup", setupH.Setup)

		r.Post("/auth/login", authH.Login)
		r.Post("/auth/register", authH.Register)
		r.Post("/auth/logout", authH.Logout)
		r.Get("/auth/me", authH.Me)

		r.Group(func(r chi.Router) {
			r.Use(requireAuth(queries))

			r.Get("/settings", settingsH.Get)
			r.Put("/settings", settingsH.Update)
			r.Patch("/user", settingsH.UpdateProfile)
			r.Post("/user/api-key", settingsH.EnsureAPIKey)
			r.Post("/user/api-key/reset", settingsH.ResetAPIKey)

			r.Get("/files", files.List)
			r.Get("/files/search", files.Search)
			r.Get("/files/{slug}", files.Get)
			r.Get("/files/{slug}/download", files.Download)
			r.Post("/files", files.Create)
			r.Patch("/files/{slug}", files.Update)
			r.Patch("/files/{slug}/name", files.Rename)
			r.Patch("/files/{slug}/visibility", files.SetVisibility)
			r.Patch("/files/{slug}/tags", files.SetTags)
			r.Patch("/files/{slug}/expiry", files.SetExpiry)
			r.Post("/files/{slug}/refresh-code", files.RefreshCode)
			r.Post("/files/{slug}/restore", files.Restore)
			r.Delete("/files/{slug}", files.Delete)
			r.Delete("/files/{slug}/permanent", files.HardDelete)

			r.Get("/backup", backupH.Download)
		})
	})

	r.Get("/res/{slug}", files.Render)

	// MCP endpoint: its own Bearer-token (API key) auth + mcp_enabled gate,
	// deliberately outside /api and the session-cookie requireAuth.
	r.Handle("/mcp", handlers.NewMCPHandler(queries, logger))

	r.NotFound(spaHandler(web.FS()))

	return r
}

// requireAuth 401s any request without a valid session cookie, and resolves
// the session to its user once so downstream handlers can read it from the
// request context instead of each repeating the lookup. Every file query is
// owner-scoped, so handlers need that identity anyway — resolving it here
// makes "behind requireAuth implies a known user" a property of the router
// rather than a convention each new handler has to remember.
//
// The lookup and the context accessors live in internal/handlers so this
// middleware and the routes outside it can share them without an import cycle
// (this package already imports internal/handlers).
func requireAuth(queries *sqlcgen.Queries) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := handlers.CurrentUser(r, queries)
			if !ok {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, handlers.WithUser(r, user))
		})
	}
}

func slogRequestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			logger.Info("request", "method", r.Method, "path", r.URL.Path)
			next.ServeHTTP(w, r)
		})
	}
}

// spaHandler serves static assets from distFS, falling back to index.html
// for any path that isn't a real file so client-side routing works.
func spaHandler(distFS fs.FS) http.HandlerFunc {
	fileServer := http.FileServer(http.FS(distFS))
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(distFS, trimLeadingSlash(r.URL.Path)); err != nil {
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	}
}

func trimLeadingSlash(p string) string {
	if len(p) > 0 && p[0] == '/' {
		return p[1:]
	}
	return p
}
