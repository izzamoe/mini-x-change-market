package ws

import (
	"net"
	"net/http"

	"github.com/coder/websocket"
	"github.com/izzam/mini-exchange/internal/transport/http/middleware"
	"github.com/izzam/mini-exchange/pkg/netutil"
)

// Handler upgrades HTTP connections to WebSocket and hands them to the Hub.
type Handler struct {
	hub            *Hub
	trustedCIDRs   []*net.IPNet
	allowedOrigins []string
}

// NewHandler creates a Handler backed by the given Hub.
// trustedCIDRs: upstream proxy CIDRs whose X-Forwarded-For is trusted for IP extraction.
// allowedOrigins: origin patterns passed to websocket.AcceptOptions.OriginPatterns;
// if empty, the connection origin is validated against the Host header (library default).
func NewHandler(hub *Hub, trustedCIDRs []*net.IPNet, allowedOrigins []string) *Handler {
	return &Handler{hub: hub, trustedCIDRs: trustedCIDRs, allowedOrigins: allowedOrigins}
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	opts := &websocket.AcceptOptions{}
	if len(h.allowedOrigins) > 0 {
		opts.OriginPatterns = h.allowedOrigins
	}

	conn, err := websocket.Accept(w, r, opts)
	if err != nil {
		// websocket.Accept has already written an error response.
		return
	}

	// Extract authenticated user from request context (populated by JWT middleware).
	// Fall back to "anonymous" when auth middleware is absent.
	userID := "anonymous"
	if id, ok := middleware.UserIDFromContext(r.Context()); ok && id != "" {
		userID = id
	}

	// Use netutil for safe, proxy-aware IP extraction.
	ip := netutil.ExtractClientIP(r, h.trustedCIDRs)

	client := NewClient(conn, h.hub, userID, ip)
	h.hub.register <- client

	// Each pump runs in its own goroutine; the handler returns immediately.
	go client.writePump()
	go client.readPump()
}
