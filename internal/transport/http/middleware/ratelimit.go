package middleware

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/izzam/mini-exchange/pkg/netutil"
	"github.com/izzam/mini-exchange/pkg/response"
)

// RateLimiter enforces a per-IP token-bucket rate limit.
//
// Design (Bottleneck #2 implementation):
//   - Each unique client IP gets its own *rate.Limiter (token bucket).
//   - A background goroutine sweeps the map every cleanupInterval to evict
//     entries that have been idle longer than idleTTL, preventing unbounded
//     memory growth.
//   - trustedCIDRs controls which upstream proxies are trusted for
//     X-Forwarded-For, preventing IP spoofing from untrusted clients.
type RateLimiter struct {
	mu           sync.Mutex
	clients      map[string]*clientEntry
	rps          rate.Limit
	burst        int
	trustedCIDRs []*net.IPNet
	done         chan struct{} // closed by Stop() to terminate the cleanup goroutine
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
// trustedCIDRs: upstream proxy CIDRs whose X-Forwarded-For header is trusted.
func NewRateLimiter(rps float64, burst int, trustedCIDRs []*net.IPNet) *RateLimiter {
	rl := &RateLimiter{
		clients:      make(map[string]*clientEntry),
		rps:          rate.Limit(rps),
		burst:        burst,
		trustedCIDRs: trustedCIDRs,
		done:         make(chan struct{}),
	}
	go rl.cleanup()
	return rl
}

// Stop shuts down the background cleanup goroutine.
// It is safe to call multiple times.
func (rl *RateLimiter) Stop() {
	select {
	case <-rl.done:
		// already closed
	default:
		close(rl.done)
	}
}

// Middleware returns an http.Handler middleware that enforces the rate limit.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := netutil.ExtractClientIP(r, rl.trustedCIDRs)
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
	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			for ip, entry := range rl.clients {
				if time.Since(entry.lastSeen) > idleTTL {
					delete(rl.clients, ip)
				}
			}
			rl.mu.Unlock()
		case <-rl.done:
			return
		}
	}
}
