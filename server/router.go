package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// apiNotFoundMessage is the 404 body for /api/* (other than /api/public/*) on the main port.
const apiNotFoundMessage = "not found"

// newMainRouter is the top-level handler of the main port (PORT), in front of the existing admin and
// public chains. It dispatches by path prefix:
//
//	/admin          301 to /admin/ (query string kept)
//	/admin/*        the admin chain with "/admin" stripped, so auth, CSRF, the demo write guard, Access
//	                verification and the Host allowlist see the chain's own paths ("/api/me", "/")
//	GET /healthz    the readiness probe, outside the admin auth and tunnel chain
//	/api/public/*   the public chain
//	/api/*          404, never a redirect and never the shop: the admin API lives under /admin/api/
//	everything else the public chain ("/", "/order", item images, custom.css, public assets)
//
// admin and public are complete chains (adminListenerHandler, and publicListenerHandler around hostGuard),
// so each branch keeps its own security headers, access log and Host allowlist. Every /admin/* request
// passes through admin, so none can skip the allowlist, tunnel guard, CSRF or session checks.
//
// wrap puts the public chain (security headers, access log, no-store, Host allowlist) around the handlers
// the router answers itself: the /admin redirect, the /api 404, the readiness probe and the redirects
// that clean a non-canonical path. Without it those responses would skip the Host allowlist, the headers
// and the access log.
func newMainRouter(admin, public, health http.Handler, wrap func(http.Handler) http.Handler) http.Handler {
	adminStripped := http.StripPrefix("/admin", admin)
	adminBranch := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The access log sits inside admin and sees the stripped path; hand it the original.
		ctx := context.WithValue(r.Context(), origPathKey{}, r.URL.Path)
		adminStripped.ServeHTTP(w, r.WithContext(ctx))
	})
	apiNotFound := wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeAPIError(w, http.StatusNotFound, apiNotFoundMessage)
	}))

	mux := http.NewServeMux()
	mux.Handle("/admin", wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := "/admin/"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})))
	mux.Handle("/admin/", adminBranch)
	mux.Handle("GET /healthz", wrap(health))
	mux.Handle("/api/public/", public)
	mux.Handle("/api/", apiNotFound)
	mux.Handle("/api", apiNotFound)
	mux.Handle("/", public)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ServeMux would answer a non-canonical path with its own redirect, outside every chain. Answer it
		// here, inside the public chain, with the same target the mux would have used.
		if r.Method != http.MethodConnect {
			if p := cleanRequestPath(r.URL.EscapedPath()); p != r.URL.EscapedPath() {
				u := &url.URL{Path: p, RawQuery: r.URL.RawQuery}
				wrap(http.RedirectHandler(u.String(), http.StatusMovedPermanently)).ServeHTTP(w, r)
				return
			}
		}
		mux.ServeHTTP(w, r)
	})
}

// cleanRequestPath is net/http's unexported cleanPath: path.Clean that keeps a trailing slash.
func cleanRequestPath(p string) string {
	if p == "" {
		return "/"
	}
	if p[0] != '/' {
		p = "/" + p
	}
	np := path.Clean(p)
	if p[len(p)-1] == '/' && np != "/" {
		if len(p) == len(np)+1 && strings.HasPrefix(p, np) {
			np = p
		} else {
			np += "/"
		}
	}
	return np
}

// mainPublicHandler is the public chain for the main port: publicListenerHandler with the Host allowlist
// applied first, because the allowlist covers every request on this port, then the site.shopOnMainPort gate.
func mainPublicHandler(mux http.Handler, auth *authState, store *ConfigStore, trustProxy bool) http.Handler {
	return publicListenerHandler(auth.hostGuard(shopOnMainPortGate(store, mux)), trustProxy)
}

// shopOnMainPortGate serves next only while site.shopOnMainPort is on, read per request so a Settings save
// applies without a restart. When off, / redirects to /admin/ and every other public route is a 404.
func shopOnMainPortGate(store *ConfigStore, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if store.Get().Site.ShopOnMainPort {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/admin/", http.StatusFound)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/public/") {
			writeAPIError(w, http.StatusNotFound, "not found")
			return
		}
		http.NotFound(w, r)
	})
}

// buildMainHandler is the main port's whole handler, factored out of main() so tests run the production
// wiring: the admin chain around adminMux, the public routes on a mux of their own (trustProxy follows
// ADMIN_TRUST_PROXY here), the readiness probe outside the admin chain, and newMainRouter in front of all.
func buildMainHandler(adminMux *http.ServeMux, auth *authState, store *ConfigStore, db *sql.DB, configDir string, shared *publicShared, demoMode bool) http.Handler {
	admin := adminListenerHandler(adminMux, auth, auth.trustProxy)
	publicMux := buildPublicMuxShared(store, db, configDir, shared, auth.trustProxy, demoMode, mainStaffURL)
	wrap := func(h http.Handler) http.Handler {
		return publicListenerHandler(auth.hostGuard(h), auth.trustProxy)
	}
	return newMainRouter(admin, mainPublicHandler(publicMux, auth, store, auth.trustProxy), handleHealthz(db, configDir), wrap)
}

// newMainServer builds the main port's server with the timeouts the admin listener always had. G-code
// uploads outlast ReadTimeout, so handlePublicQuoteRequest extends its own read and write deadlines
// through http.ResponseController, exactly as it does on PUBLIC_PORT.
func newMainServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
	}
}
