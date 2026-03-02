package ws

import (
	"net/http"
	"strings"

	"github.com/coder/websocket"
)

// Handler upgrades HTTP connections to WebSocket and hands them to the Hub.
type Handler struct {
	hub *Hub
}

// NewHandler creates a Handler backed by the given Hub.
func NewHandler(hub *Hub) *Handler {
	return &Handler{hub: hub}
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // allow all origins; restrict in production via OriginPatterns
	})
	if err != nil {
		// websocket.Accept has already written an error response.
		return
	}

	// Extract authenticated user from request context (populated by JWT middleware in Plan 8).
	// Fall back to "anonymous" when auth middleware is absent.
	userID := "anonymous"
	if id, ok := r.Context().Value(userIDContextKey{}).(string); ok && id != "" {
		userID = id
	}

	// Use the forwarded IP if available (behind a proxy), otherwise use RemoteAddr.
	ip := r.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = r.RemoteAddr
	}
	// Strip port from RemoteAddr ("host:port" → "host").
	if idx := strings.LastIndex(ip, ":"); idx != -1 && !strings.Contains(ip, "[") {
		ip = ip[:idx]
	}

	client := NewClient(conn, h.hub, userID, ip)
	h.hub.register <- client

	// Each pump runs in its own goroutine; the handler returns immediately.
	go client.writePump()
	go client.readPump()
}

// userIDContextKey is the unexported context key used by the JWT middleware
// (Plan 8) to propagate the authenticated user ID.
type userIDContextKey struct{}
