// Package netutil provides network utility helpers.
package netutil

import (
	"net"
	"net/http"
	"strings"
)

// ExtractClientIP returns the real client IP address from an HTTP request.
//
// If the direct connection (r.RemoteAddr) originates from one of the
// trustedCIDRs (e.g. an internal reverse proxy), the first value in
// X-Forwarded-For is used as the client IP.  Otherwise the direct
// connection address is used, preventing IP spoofing from untrusted clients
// who can freely set the X-Forwarded-For header.
//
// trustedCIDRs may be nil or empty, in which case X-Forwarded-For is never
// trusted and the direct connection IP is always returned.
func ExtractClientIP(r *http.Request, trustedCIDRs []*net.IPNet) string {
	remoteIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr may not have a port (rare, e.g. unit tests).
		remoteIP = r.RemoteAddr
	}

	if len(trustedCIDRs) > 0 && isTrusted(remoteIP, trustedCIDRs) {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			// The header may contain a comma-separated list; take the first.
			first := strings.TrimSpace(strings.SplitN(fwd, ",", 2)[0])
			if first != "" {
				return first
			}
		}
	}

	return remoteIP
}

// ParseCIDRs parses a slice of CIDR strings and returns the corresponding
// *net.IPNet values.  Invalid entries are silently skipped.
func ParseCIDRs(cidrs []string) []*net.IPNet {
	var result []*net.IPNet
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(strings.TrimSpace(cidr))
		if err == nil {
			result = append(result, ipNet)
		}
	}
	return result
}

func isTrusted(ip string, cidrs []*net.IPNet) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, cidr := range cidrs {
		if cidr.Contains(parsed) {
			return true
		}
	}
	return false
}
