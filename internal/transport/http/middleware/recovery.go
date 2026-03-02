package middleware

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/izzam/mini-exchange/pkg/response"
)

// Recovery catches panics in downstream handlers, logs the stack trace,
// and returns a 500 Internal Server Error to the client.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("handler panic recovered",
					"panic", rec,
					"stack", string(debug.Stack()),
					"method", r.Method,
					"path", r.URL.Path,
				)
				response.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "an unexpected error occurred")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
