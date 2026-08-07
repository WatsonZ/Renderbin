package handlers

import (
	"context"
	"net/http"

	"github.com/shawn-bluce/renderbin/backend/internal/auth"
	"github.com/shawn-bluce/renderbin/backend/internal/db/sqlcgen"
)

// SuperAdminID is the fixed id of the super admin: the first user ever
// created (through the welcome/setup flow) gets id=1.
const SuperAdminID = 1

// IsSuperAdmin reports whether the user is the super admin (id=1).
// Note this gates *capabilities* (global settings, backups), never access to
// files: file endpoints are owner-scoped for everyone, super admin included,
// matching what MCP has always done.
func IsSuperAdmin(u sqlcgen.User) bool {
	return u.ID == SuperAdminID
}

// Two ways to identify a caller, for the two kinds of route:
//
//   - Behind the requireAuth middleware, the user is already resolved and
//     stashed in the request context: use requireUser.
//   - Outside it (/res/{slug}, /api/auth/me), nothing has done that, so the
//     cookie must be resolved on the spot: use CurrentUser.
//
// Both live in handlers rather than server to avoid an import cycle:
// internal/server already imports internal/handlers to build routes.

// CurrentUser resolves the request's session cookie to its user row. Called by
// the requireAuth middleware and by the handlers registered outside it;
// handlers behind the middleware should call requireUser instead of paying
// for a second lookup.
func CurrentUser(r *http.Request, queries *sqlcgen.Queries) (sqlcgen.User, bool) {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil {
		return sqlcgen.User{}, false
	}
	sess, err := queries.GetValidSession(r.Context(), cookie.Value)
	if err != nil {
		return sqlcgen.User{}, false
	}
	user, err := queries.GetUserByID(r.Context(), sess.UserID)
	if err != nil {
		return sqlcgen.User{}, false
	}
	return user, true
}

// userContextKey is unexported so nothing outside this package can plant a
// user in the context; only WithUser can.
type userContextKey struct{}

// WithUser returns r carrying user in its context. The requireAuth middleware
// in internal/server is the only intended caller.
func WithUser(r *http.Request, user sqlcgen.User) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), userContextKey{}, user))
}

// requireUser is the standard preamble for every handler behind requireAuth:
//
//	user, ok := requireUser(w, r)
//	if !ok {
//		return
//	}
//
// Owner-scoped queries need the returned user.ID, so a handler that skips this
// won't compile. The 401 is unreachable in practice — requireAuth would have
// rejected the request first — but it fails closed if a route is ever mounted
// outside the middleware group by mistake.
func requireUser(w http.ResponseWriter, r *http.Request) (sqlcgen.User, bool) {
	user, ok := r.Context().Value(userContextKey{}).(sqlcgen.User)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return sqlcgen.User{}, false
	}
	return user, true
}
