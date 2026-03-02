package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/izzam/mini-exchange/pkg/response"
)

// RateLimiter enforces a per-IP token-bucket rate limit.
//
// Design (Bottleneck #2 implementation):
//   - Each unique client IP gets its own *rate.Limiter (token bucket).
//   - A background goroutine sweeps the map every cleanupInterval to evict
//     entries that have been idle longer than idleTTL, preventing unbounded
//     memory growth.
type RateLimiter struct {
	mu      sync.Mutex
	clients map[string]*clientEntry
	rps     rate.Limit
	burst   int
}

type clientEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

const (
	cleanupInterval = 5 * time.Minute
	idleTTL         = 10 * time.Minute
)

// NewRateLimiter creates a RateLimiter and starts the background cleanup goroutine.
// rps: requests per second allowed per IP.
// burst: maximum burst size (token bucket capacity).
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	rl := &RateLimiter{
		clients: make(map[string]*clientEntry),
		rps:     rate.Limit(rps),
		burst:   burst,
	}
	go rl.cleanup()
	return rl
}

// Middleware returns an http.Handler middleware that enforces the rate limit.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)
		limiter := rl.getLimiter(ip)
		if !limiter.Allow() {
			response.Error(w, http.StatusTooManyRequests, "RATE_LIMITED",
				"too many requests — please slow down")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// getLimiter returns the rate.Limiter for the given IP, creating one if needed.
func (rl *RateLimiter) getLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, ok := rl.clients[ip]
	if !ok {
		entry = &clientEntry{
			limiter: rate.NewLimiter(rl.rps, rl.burst),
		}
		rl.clients[ip] = entry
	}
	entry.lastSeen = time.Now()
	return entry.limiter
}

// cleanup periodically removes idle entries from the map.
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		for ip, entry := range rl.clients {
			if time.Since(entry.lastSeen) > idleTTL {
				delete(rl.clients, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// extractIP returns the client IP, stripping any port.
func extractIP(r *http.Request) string {
	// Honour X-Forwarded-For when behind a reverse proxy.
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return fwd
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}
