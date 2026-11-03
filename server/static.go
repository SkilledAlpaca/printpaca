package main

import (
	"database/sql"
	"embed"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed all:embedded/dist
var distFS embed.FS

// mime has no built-in .webmanifest mapping; without it serveGzipVariant falls back to octet-stream.
func init() {
	if err := mime.AddExtensionType(".webmanifest", "application/manifest+json"); err != nil {
		panic(err) // a bad hard-coded extension/type pair is a build-time bug
	}
}

// registerRoutes wires the admin mux's route table except /api/config and /healthz.
// It never enforces session auth (the caller wraps the mux in auth.middleware) but wires the per-route
// permission check at every registration site: adminOnly is the default, so no route is reachable by
// an arbitrary authenticated session unchecked. Both wrappers are no-ops for the admin identity and
// when auth is disabled.
// demoMode makes registerProxies register fake Spoolman/Moonraker handlers when the *_URL is unset.
func registerRoutes(mux *http.ServeMux, db *sql.DB, cfgStore *ConfigStore, auth *authState, configDir string, demoMode bool) {
	// db/configStore back handleLogin's employee path and handleDemo's multiUser flag; see authState.
	auth.db = db
	auth.configStore = cfgStore

	mux.HandleFunc("POST /api/login", auth.handleLogin)
	mux.HandleFunc("POST /api/logout", auth.handleLogout)
	mux.HandleFunc("GET /api/demo", auth.handleDemo)

	// GET /api/me and POST /api/me/password: any authenticated user, no permission wrapper.
	me := newMeAPI(db, auth)
	mux.HandleFunc("GET /api/me", me.handleGet)
	mux.HandleFunc("POST /api/me/password", me.handlePassword)

	// /api/users: hard admin-only. Handlers also 409 while users.multiUser is off.
	ua := newUsersAPI(db, auth)
	mux.HandleFunc("GET /api/users", adminOnly(func(w http.ResponseWriter, r *http.Request) {
		ua.handleList(w, r, cfgStore.Get())
	}))
	mux.HandleFunc("POST /api/users", adminOnly(func(w http.ResponseWriter, r *http.Request) {
		ua.handlePost(w, r, cfgStore.Get())
	}))
	mux.HandleFunc("PUT /api/users/{id}", adminOnly(func(w http.ResponseWriter, r *http.Request) {
		ua.handlePut(w, r, cfgStore.Get())
	}))
	mux.HandleFunc("POST /api/users/{id}/password", adminOnly(func(w http.ResponseWriter, r *http.Request) {
		ua.handlePassword(w, r, cfgStore.Get())
	}))
	mux.HandleFunc("DELETE /api/users/{id}", adminOnly(func(w http.ResponseWriter, r *http.Request) {
		ua.handleDelete(w, r, cfgStore.Get())
	}))

	q := newQuotes(db)
	mux.HandleFunc("GET /api/quotes", requirePermission(db, "quotes", q.handleList))
	mux.HandleFunc("GET /api/quotes/{id}", requirePermission(db, "quotes", q.handleGet))
	mux.HandleFunc("POST /api/quotes", requirePermission(db, "quotes", q.handlePost))
	mux.HandleFunc("PUT /api/quotes/{id}", requirePermission(db, "quotes", q.handlePut))
	mux.HandleFunc("DELETE /api/quotes/{id}", requirePermission(db, "quotes", q.handleDelete))
	mux.HandleFunc("POST /api/quote/compute", requirePermission(db, "quote", handleQuoteCompute))

	cl := newClients(db)
	mux.HandleFunc("GET /api/clients", requirePermission(db, "clients", cl.handleList))
	mux.HandleFunc("POST /api/clients", requirePermission(db, "clients", cl.handlePost))
	mux.HandleFunc("PUT /api/clients/{id}", requirePermission(db, "clients", cl.handlePut))
	mux.HandleFunc("DELETE /api/clients/{id}", requirePermission(db, "clients", cl.handleDelete))

	inv := newInvoices(db, cfgStore)
	mux.HandleFunc("GET /api/invoices", requirePermission(db, "invoices", inv.handleList))
	mux.HandleFunc("GET /api/invoices/{id}", requirePermission(db, "invoices", inv.handleGet))
	mux.HandleFunc("POST /api/invoices", requirePermission(db, "invoices", inv.handlePost))
	mux.HandleFunc("PUT /api/invoices/{id}", requirePermission(db, "invoices", inv.handlePut))
	mux.HandleFunc("POST /api/invoices/{id}/status", requirePermission(db, "invoices", inv.handleStatus))
	mux.HandleFunc("DELETE /api/invoices/{id}", requirePermission(db, "invoices", inv.handleDelete))
	mux.HandleFunc("POST /api/invoices/{id}/payments", requirePermission(db, "invoices", inv.handlePaymentPost))
	mux.HandleFunc("DELETE /api/invoices/{id}/payments/{paymentId}", requirePermission(db, "invoices", inv.handlePaymentDelete))
	mux.HandleFunc("POST /api/invoices/{id}/send", requirePermission(db, "invoices", inv.handleSend))

	// POST /api/quotes/send is the "quotes" tab; POST /api/smtp/test maps to no tab, so it's admin-only.
	mux.HandleFunc("POST /api/quotes/send", requirePermission(db, "quotes", handleQuoteSend(db, cfgStore, dialSMTP)))
	mux.HandleFunc("POST /api/smtp/test", adminOnly(handleSmtpTest(cfgStore, dialSMTP)))

	dash := newDashboard(db, cfgStore)
	mux.HandleFunc("GET /api/dashboard", requirePermission(db, "dashboard", dash.handleGet))
	mux.HandleFunc("GET /api/dashboard/top-items", requirePermission(db, "dashboard", dash.handleTopItems))

	qr := newQuoteRequests(db, cfgStore, configDir, demoMode)
	mux.HandleFunc("GET /api/requests", requirePermission(db, "requests", qr.handleList))
	// Literal segment; ServeMux resolves it by specificity, independent of order.
	mux.HandleFunc("GET /api/requests/storage", requirePermission(db, "requests", qr.handleStorage))
	mux.HandleFunc("GET /api/requests/{id}", requirePermission(db, "requests", qr.handleGet))
	mux.HandleFunc("GET /api/requests/{id}/gcode", requirePermission(db, "requests", qr.handleGcodeDownload))
	mux.HandleFunc("POST /api/requests/{id}/status", requirePermission(db, "requests", qr.handleStatus))
	mux.HandleFunc("POST /api/requests/{id}/print-time", requirePermission(db, "requests", qr.handlePrintTime))
	// Bulk-delete of new requests (flood recovery). Literal segment like /storage; request ids are 32 hex chars.
	mux.HandleFunc("DELETE /api/requests/new", requirePermission(db, "requests", qr.handleDeleteNew))
	mux.HandleFunc("DELETE /api/requests/{id}", requirePermission(db, "requests", qr.handleDelete))

	it := newItems(db, configDir)
	mux.HandleFunc("GET /api/items", requirePermission(db, "items", it.handleList))
	mux.HandleFunc("POST /api/items", requirePermission(db, "items", it.handlePost))
	mux.HandleFunc("PUT /api/items/{id}", requirePermission(db, "items", it.handlePut))
	mux.HandleFunc("DELETE /api/items/{id}", requirePermission(db, "items", it.handleDelete))
	mux.HandleFunc("POST /api/items/{id}/image", requirePermission(db, "items", it.handleImageUpload))
	mux.HandleFunc("POST /api/items/{id}/options/{optionId}/image", requirePermission(db, "items", it.handleOptionImageUpload))
	mux.HandleFunc("DELETE /api/items/{id}/options/{optionId}/image", requirePermission(db, "items", it.handleOptionImageDelete))
	// Outside /api/ on purpose: keeps the long-lived Cache-Control past noStoreAPIMiddleware, and item
	// images are public content. No permission wrapper applies.
	mux.HandleFunc("GET /item-images/{file}", itemImageHandler(configDir))

	// Manual-backup export bundle. Hard admin-only.
	mux.HandleFunc("GET /api/export", adminOnly(handleExport(cfgStore, db)))

	// Diagnostics export of PII-redacted error lines from the in-memory log ring. Hard admin-only.
	// The handler has its own demo-mode block (GET is never seen by demoWriteGuard).
	mux.HandleFunc("GET /api/logs/errors", adminOnly(handleLogsErrors(appLogRing, demoMode)))

	registerProxies(mux, demoMode, cfgStore, db) // proxy.go; permission-gated internally

	// Probes a Spoolman/Moonraker URL before it is saved. Hard admin-only; demoWriteGuard blocks it in demo.
	mux.HandleFunc("POST /api/integrations/test", adminOnly(handleIntegrationsTest))

	// Same handler as the public listener, so web/index.html's print-only stylesheet link works here.
	// Safe GET outside /api/*, so no demo block or auth exemption is needed.
	mux.HandleFunc("GET /custom.css", handlePublicCustomCss(cfgStore))

	sub, err := fs.Sub(distFS, "embedded/dist")
	if err != nil {
		panic(err) // embedded FS shape is a build-time invariant
	}
	mux.Handle("GET /", newStaticHandler(sub))
}

// newStaticHandler serves the embedded SPA, substituting the precompressed .gz sibling (emitted by
// web/vite.config.ts) when the client accepts gzip. Takes an fs.FS so tests can use an in-memory one.
func newStaticHandler(fsys fs.FS) http.Handler {
	fileServer := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			// Content-hashed asset names cache forever, but only on a successful response: assetCacheWriter
			// decides at WriteHeader so a 404 (e.g. a stale index.html after rollback) is never cached immutable.
			w = &assetCacheWriter{ResponseWriter: w}
		} else {
			// embed.FS gives no ETag/Last-Modified, so force revalidation.
			w.Header().Set("Cache-Control", "no-cache")
		}
		if serveGzipVariant(w, r, fsys) {
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}

// assetCacheWriter sets Cache-Control for /assets/ at WriteHeader time: under 400 gets the immutable
// year-long directive, anything else (a 404 above all) gets no-store. Setting it here also survives
// net/http's serveError, which deletes any Cache-Control set earlier.
type assetCacheWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *assetCacheWriter) WriteHeader(status int) {
	if !w.wroteHeader {
		w.wroteHeader = true
		if status < http.StatusBadRequest {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-store")
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

// Write implies a 200 when WriteHeader was never called, so Cache-Control still gets set.
func (w *assetCacheWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// serveGzipVariant serves name+".gz" when the client accepts gzip and the sibling exists, reporting
// whether it did. Content-Type comes from the original name's extension, not ".gz".
func serveGzipVariant(w http.ResponseWriter, r *http.Request, fsys fs.FS) bool {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return false
	}
	if !acceptsGzip(r.Header.Get("Accept-Encoding")) {
		return false
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "." {
		name = "index.html"
	}
	f, err := fsys.Open(name + ".gz")
	if err != nil {
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		return false
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		return false // embed.FS and os.DirFS files both satisfy this; a defensive fallback only
	}
	// Vary so a shared cache never serves the gzip variant to a client that didn't ask for it.
	w.Header().Set("Vary", "Accept-Encoding")
	w.Header().Set("Content-Encoding", "gzip")
	if ctype := mime.TypeByExtension(path.Ext(name)); ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	http.ServeContent(w, r, name, info.ModTime(), rs)
	return true
}

// acceptsGzip: does Accept-Encoding permit gzip? "gzip" or "*" accepts, "gzip;q=0" rejects, no other q.
func acceptsGzip(header string) bool {
	for part := range strings.SplitSeq(header, ",") {
		token, param, _ := strings.Cut(strings.TrimSpace(part), ";")
		if token != "gzip" && token != "*" {
			continue
		}
		if strings.TrimSpace(param) == "q=0" {
			continue
		}
		return true
	}
	return false
}
