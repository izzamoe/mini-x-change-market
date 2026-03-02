// Package middleware provides reusable HTTP middleware for the Chi router.
package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/izzam/mini-exchange/internal/infra/auth"
	"github.com/izzam/mini-exchange/pkg/response"
)

// authContextKey is the unexported context key used to propagate user IDs.
type authContextKey struct{}

// UserIDFromContext retrieves the authenticated user ID stored by Auth middleware.
// Returns ("", false) when no authenticated user is present.
func UserIDFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(authContextKey{}).(string)
	return id, ok && id != ""
}

// Auth returns middleware that validates the Bearer JWT from the Authorization
// header. When required=true, unauthenticated requests receive a 401.
// When required=false, the request proceeds and the user is treated as anonymous.
func Auth(svc *auth.Service, required bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token := extractBearerToken(r)

			// Also accept token from query param (used by WS clients).
			if token == "" {
				token = r.URL.Query().Get("token")
			}

			if token == "" {
				if required {
					response.Error(w, http.StatusUnauthorized, "MISSING_TOKEN", "authorization token required")
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			claims, err := svc.ValidateToken(token)
			if err != nil {
				if required {
					response.Error(w, http.StatusUnauthorized, "INVALID_TOKEN", "authorization token is invalid or expired")
					return
				}
				// Not required: proceed as anonymous.
				next.ServeHTTP(w, r)
				return
			}

			ctx := context.WithValue(r.Context(), authContextKey{}, claims.UserID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// extractBearerToken extracts the token from "Authorization: Bearer <token>".
func extractBearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	parts := strings.SplitN(h, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return ""
	}
	return parts[1]
}
