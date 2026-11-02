package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

// Minimal structured access logging via log.Printf: method, path, status, duration, remote address.
// Never a body, query string, cookie, Authorization value, or login password.
//
// Method, path and client address are logged with %q to prevent log injection: net/http decodes
// %0d%0a in the path, and X-Forwarded-For is client-controlled with trustProxy on, so plain %s
// would let a request forge log lines.

// accessLogStatusRecorder captures the written status, defaulting to 200.
type accessLogStatusRecorder struct {
	http.ResponseWriter
	status int
}

func (rec *accessLogStatusRecorder) WriteHeader(code int) {
	rec.status = code
	rec.ResponseWriter.WriteHeader(code)
}

// Unwrap exposes the wrapped ResponseWriter to http.ResponseController. Without it
// SetReadDeadline/SetWriteDeadline silently return ErrNotSupported, which would disable the public
// upload read deadline and break flushing in the reverse proxy.
func (rec *accessLogStatusRecorder) Unwrap() http.ResponseWriter {
	return rec.ResponseWriter
}

// origPathKey carries the request path as it arrived, before the main router strips "/admin", so the
// access log records what the client asked for.
type origPathKey struct{}

// accessLogAlways logs every request; the public listener has no other record of abuse.
func accessLogAlways(status int) bool { return true }

// accessLogAdminFilter logs only auth failures (401/403) and 5xx; the LAN-scoped admin listener
// would otherwise log noise.
func accessLogAdminFilter(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden || status >= 500
}

// accessLogMiddleware logs one "access <listener> <method> <path> <status> <duration> <remoteAddr>"
// line per request when shouldLog(status) is true. remoteAddr goes through clientIP, so
// X-Forwarded-For is trusted only with trustProxy. See the file header for why fields use %q.
func accessLogMiddleware(listener string, shouldLog func(status int) bool, trustProxy bool, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &accessLogStatusRecorder{ResponseWriter: w, status: http.StatusOK}
		// The auth middleware can't hand back a modified request, so it flags this holder on Access
		// verification and the log then uses CF-Connecting-IP.
		holder := &cfVerifiedHolder{}
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), cfHolderKey{}, holder)))
		if shouldLog(rec.status) {
			addr := clientIP(r, trustProxy)
			holder.mu.Lock()
			verified := holder.ok
			holder.mu.Unlock()
			if verified {
				if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); net.ParseIP(ip) != nil {
					addr = rateLimitKey(ip)
				}
			}
			path := r.URL.Path
			if orig, ok := r.Context().Value(origPathKey{}).(string); ok {
				path = orig
			}
			log.Printf("access %s %q %q %d %s %q",
				listener, r.Method, path, rec.status,
				time.Since(start).Round(time.Millisecond), addr)
		}
	})
}
