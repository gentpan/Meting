package auth

import (
	"net"
	"net/http"
	"strconv"
	"strings"
)

// ExtractToken returns the bearer token from Authorization header or `token` query param.
func ExtractToken(r *http.Request) string {
	if header := r.Header.Get("Authorization"); header != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(header, prefix) {
			return strings.TrimSpace(header[len(prefix):])
		}
	}
	if q := r.URL.Query().Get("token"); q != "" {
		return strings.TrimSpace(q)
	}
	return ""
}

// ClientIP extracts a best-effort client IP. Prefers a custom X-Original-Client-IP
// header that trusted edge proxies set (and intermediate hops won't strip), then
// X-Forwarded-For, X-Real-IP, and finally the direct connection.
func ClientIP(r *http.Request) string {
	if v := strings.TrimSpace(r.Header.Get("X-Original-Client-IP")); v != "" {
		return v
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		first := strings.TrimSpace(strings.Split(xff, ",")[0])
		if first != "" {
			return first
		}
	}
	if xr := r.Header.Get("X-Real-IP"); xr != "" {
		return strings.TrimSpace(xr)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// WriteRateHeaders adds X-RateLimit-* headers to a response.
// limit == -1 indicates an unlimited (special) token; headers are omitted.
func WriteRateHeaders(w http.ResponseWriter, limit, remaining int) {
	if limit < 0 {
		return
	}
	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(remaining))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(NextResetUnix(), 10))
}
