package main

import (
	"net/http"
	"net/netip"
	"strings"
)

// Response-header middlewares for both listeners: security headers, and no-store on /api/*.
// Both wrap outermost so they apply to 401/403 responses too.

// publicContentSecurityPolicy is the CSP sent with every response, checked against the built output
// (no inline script/style, no CDN, no eval). img-src allows data: and https: because the owner-supplied
// logo URL may be either. frame-ancestors 'none' backs X-Frame-Options for older browsers.
const publicContentSecurityPolicy = "default-src 'self'; img-src 'self' data: https:; object-src 'none'; base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

// securityHeadersMiddleware sets nosniff (gcode downloads serve uploaded bytes as text/plain),
// X-Frame-Options, Referrer-Policy and the CSP on every response, including 401/403.
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return securityHeadersTLSMiddleware(next, false)
}

// hstsValue is the Strict-Transport-Security value: one year, no includeSubDomains or preload, since
// printpaca can't know what else lives on the owner's domain.
const hstsValue = "max-age=31536000"

// hstsApplies reports whether r is known to be HTTPS (requestIsTLS, so X-Forwarded-Proto counts only with
// trustProxy) and addressed by a DNS name. HSTS is ignored by browsers on IP hosts and must never be sent
// over plain HTTP.
func hstsApplies(r *http.Request, trustProxy bool) bool {
	if !requestIsTLS(r, trustProxy) {
		return false
	}
	if _, err := netip.ParseAddr(hostOnly(r.Host)); err == nil {
		return false
	}
	return true
}

// securityHeadersTLSMiddleware is securityHeadersMiddleware plus HSTS on requests hstsApplies accepts;
// trustProxy is the listener's trusted-proxy setting (ADMIN_TRUST_PROXY or PUBLIC_TRUST_PROXY).
func securityHeadersTLSMiddleware(next http.Handler, trustProxy bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if hstsApplies(r, trustProxy) {
			h.Set("Strict-Transport-Security", hstsValue)
		}
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", publicContentSecurityPolicy)
		next.ServeHTTP(w, r)
	})
}

// noStoreAPIMiddleware sets Cache-Control: no-store on /api/* so PII and financial responses never
// land in a browser or proxy cache. /api/spoolman/ and /api/moonraker/ are excluded: the proxied
// service's own headers pass through.
func noStoreAPIMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") &&
			!strings.HasPrefix(r.URL.Path, "/api/spoolman/") &&
			!strings.HasPrefix(r.URL.Path, "/api/moonraker/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
