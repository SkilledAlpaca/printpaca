package main

import (
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

// newPublicTestConfig loads defaults into dir and marks the "pla" material
// public, matching defaults.yaml's publicQuotes (printer: generic, preset:
// side-hustle) so callers get a working public config out of the box.
func newPublicTestConfig(t *testing.T, dir string) *Config {
	t.Helper()
	cfg := loadTestConfig(t, dir)
	for i := range cfg.Materials {
		if cfg.Materials[i].ID == "pla" {
			cfg.Materials[i].Public = true
		}
	}
	return cfg
}

// newRequestsTestSetup builds the admin table (registerRoutes) and the public mux (buildPublicMux) on one database, so a public submission is visible through adminMux as in production.
func newRequestsTestSetup(t *testing.T, dir string) (adminMux, publicMux *http.ServeMux, db *sql.DB, cfg *Config) {
	t.Helper()
	db = openTestDB(t, dir)
	cfg = newPublicTestConfig(t, dir)
	store := NewConfigStore(cfg)

	adminMux = http.NewServeMux()
	registerRoutes(adminMux, db, store, newAuthState("", nil), dir, false)
	publicMux, _ = buildPublicMux(store, db, dir, false, false)
	return adminMux, publicMux, db, cfg
}

// publicQuoteRequestFields returns a valid field set to mutate per case. priceCents is deliberately included with an implausible value: the server ignores it (drained as an unrecognized field),
// and sending it proves that rather than leaving it unexercised.
func publicQuoteRequestFields(material string) map[string]string {
	return map[string]string{
		"name":        "Visitor",
		"email":       "visitor@example.com",
		"note":        "please rush",
		"material":    material,
		"quantity":    "2",
		"weightG":     "15.5",
		"timeSeconds": "3600",
		"priceCents":  "1", // implausibly low; must be ignored, not stored
	}
}

// expectedPublicPriceCents computes the server-side price for publicQuoteRequestFields' fixed quantity/weightG/timeSeconds (2, 15.5, 3600) so tests assert against the real engine output.
func expectedPublicPriceCents(t *testing.T, cfg *Config, materialName string) int {
	t.Helper()
	var m Material
	found := false
	for _, cand := range cfg.Materials {
		if cand.Name == materialName {
			m = cand
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("test setup: material %q not found in config", materialName)
	}
	qi, opt := buildPublicQuoteInputs(cfg, m, 2, 15.5, 3600)
	bd := ComputeQuote(qi, opt)
	if !bd.Valid || bd.Pricing == nil || !bd.Pricing.Valid {
		t.Fatalf("test setup: expected a validly priced breakdown for material %q, got %+v", materialName, bd)
	}
	return int(bd.Pricing.Revenue)
}

// newPublicQuoteRequest builds a multipart/form-data POST /api/public/quote-request
// request from fields plus an optional gcode file part (skipped when
// gcodeName is empty).
func newPublicQuoteRequest(t *testing.T, fields map[string]string, gcodeName string, gcodeContent []byte) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if gcodeName != "" {
		fw, err := mw.CreateFormFile("gcode", gcodeName)
		if err != nil {
			t.Fatalf("create form file: %v", err)
		}
		if _, err := fw.Write(gcodeContent); err != nil {
			t.Fatalf("write gcode content: %v", err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/public/quote-request", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func postPublicQuoteRequest(t *testing.T, mux *http.ServeMux, fields map[string]string, gcodeName string, gcodeContent []byte) *httptest.ResponseRecorder {
	return serveReq(mux, newPublicQuoteRequest(t, fields, gcodeName, gcodeContent))
}

// ---------------------------------------------------------------------------
// Isolation: admin routes are unreachable on the public mux
// ---------------------------------------------------------------------------

func TestPublicMuxAdminRoutesNotFound(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	mux, _ := buildPublicMux(NewConfigStore(cfg), db, dir, false, false)

	for _, path := range []string{"/api/quotes", "/api/invoices", "/api/settings", "/api/clients", "/api/requests"} {
		rec := serveReq(mux, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s on public mux = %d, want 404", path, rec.Code)
		}
	}
}

func TestPublicMuxHealthz(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	mux, _ := buildPublicMux(NewConfigStore(cfg), db, dir, false, false)

	rec := serveReq(mux, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "ok" {
		t.Errorf("body = %q, want %q", got, "ok")
	}
}

// The public /healthz is a bare liveness check: it once ran the db+config-dir probe, letting an unauthenticated flood contend the single SQLite connection and echo raw errors (paths, driver text).
// This points the public mux at an already-closed *sql.DB and a nonexistent config dir and confirms /healthz still returns 200 "ok"; touching either would fail or hang.
func TestPublicHealthzNeverTouchesDBOrConfigDir(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	cfg := newPublicTestConfig(t, dir)
	missingConfigDir := filepath.Join(dir, "does-not-exist")
	mux, _ := buildPublicMux(NewConfigStore(cfg), db, missingConfigDir, false, false)

	rec := serveReq(mux, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /healthz = %d, want 200 (closed db / missing config dir must not affect public liveness); body: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Body.String(); got != "ok" {
		t.Errorf("body = %q, want %q (no raw error ever, since no probe ever runs)", got, "ok")
	}
}

// ---------------------------------------------------------------------------
// GET /custom.css
// ---------------------------------------------------------------------------

// TestPublicMuxCustomCssServesConfiguredBytes pins the configured-content
// path: whatever bytes the top-level customCss key holds are served
// verbatim, with a text/css content type.
func TestPublicMuxCustomCssServesConfiguredBytes(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	css := ":root { --accent: #2f6fed; }"
	cfg.CustomCss = css
	mux, _ := buildPublicMux(NewConfigStore(cfg), db, dir, false, false)

	rec := serveReq(mux, httptest.NewRequest("GET", "/custom.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /custom.css = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q, want a text/css prefix", ct)
	}
	if got := rec.Body.String(); got != css {
		t.Errorf("body = %q, want %q", got, css)
	}
}

// The handler reads a fresh store.Get() per request, not a value captured at mux-build time: a ConfigStore.Set (a settings save) must show on the next request with no restart.
func TestPublicMuxCustomCssReflectsLiveConfig(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	store := NewConfigStore(cfg)
	mux, _ := buildPublicMux(store, db, dir, false, false)

	// web/public.html links /custom.css unconditionally, so with no customCss configured it must still be a 200 with an empty body, never a 404.
	rec := serveReq(mux, httptest.NewRequest("GET", "/custom.css", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /custom.css = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "" {
		t.Fatalf("body before update = %q, want empty", got)
	}

	updated := *cfg
	updated.CustomCss = "body { color: red; }"
	store.Set(&updated)

	rec2 := serveReq(mux, httptest.NewRequest("GET", "/custom.css", nil))
	if got := rec2.Body.String(); got != "body { color: red; }" {
		t.Errorf("body after update = %q, want the newly configured CSS", got)
	}
}

// GET /custom.css is registered on BOTH muxes (public buildPublicMux, admin registerRoutes), sharing handlePublicCustomCss and serving the SAME value.
// The admin copy lets web/index.html's print-only <link media="print"> style printed quotes/invoices without touching the on-screen admin UI.
func TestCustomCssOnBothListeners(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, _, cfg := newRequestsTestSetup(t, dir)
	css := ":root { --accent: #2f6fed; }"
	cfg.CustomCss = css // same *Config the store already holds; no restart/Set needed

	for _, tc := range []struct {
		name string
		mux  *http.ServeMux
	}{
		{"admin", adminMux},
		{"public", publicMux},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := serveReq(tc.mux, httptest.NewRequest("GET", "/custom.css", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /custom.css on %s mux = %d, want 200", tc.name, rec.Code)
			}
			if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
				t.Errorf("%s mux Content-Type = %q, want a text/css prefix", tc.name, ct)
			}
			if got := rec.Body.String(); got != css {
				t.Errorf("%s mux body = %q, want %q", tc.name, got, css)
			}
		})
	}
}

// adminBundleMarker ("printpaca") is in every admin index.html this repo can serve (placeholder or real build title) and in neither the public 503 body nor public.html's title, so it is a valid negative check in any checkout state.
const adminBundleMarker = "printpaca"

// Routes GET / through the real buildPublicMux (the embedded dist, not an fstest.MapFS) so a mux wiring mutation such as registering "GET /oops/" fails here.
// The response must be 200 (built) or 503 (not built), never 404, and never the admin bundle.
func TestPublicMuxCatchAllServesNeverAdmin(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	mux, _ := buildPublicMux(NewConfigStore(cfg), db, dir, false, false)

	rec := serveReq(mux, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("GET / = %d, want 200 or 503, never 404 (catch-all route missing?)", rec.Code)
	}
	if strings.Contains(rec.Body.String(), adminBundleMarker) {
		t.Errorf("GET / on the public mux served the admin bundle: body contains %q", adminBundleMarker)
	}
}

// Through the real buildPublicMux, GET /index.html and /assets/main-<anything> must 404 whether the dist is the placeholder (no assets/) or a real build; the main-* names are discovered via fs.Glob, not hardcoded.
func TestPublicMuxNeverServesAdminBundleFiles(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	mux, _ := buildPublicMux(NewConfigStore(cfg), db, dir, false, false)

	rec := serveReq(mux, httptest.NewRequest("GET", "/index.html", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /index.html = %d, want 404, body = %s", rec.Code, rec.Body.String())
	}

	realDist, err := fs.Sub(distFS, "embedded/dist")
	if err != nil {
		t.Fatalf("fs.Sub embedded/dist: %v", err)
	}
	matches, err := fs.Glob(realDist, "assets/main-*")
	if err != nil {
		t.Fatalf("fs.Glob assets/main-*: %v", err)
	}
	if len(matches) == 0 {
		t.Skip("no assets/main-* in the embedded dist (placeholder checkout without a real web build)")
	}
	for _, m := range matches {
		path := "/" + m
		rec := serveReq(mux, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s (admin chunk) = %d, want 404, body = %s", path, rec.Code, rec.Body.String())
		}
	}
}

// Completeness counterpart: every file public-manifest.json lists must resolve 200 through the real mux, so the allowlist isn't so strict it breaks the public page. Skips on a placeholder checkout with no manifest.
func TestPublicMuxServesEveryManifestedAsset(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	mux, _ := buildPublicMux(NewConfigStore(cfg), db, dir, false, false)

	realDist, err := fs.Sub(distFS, "embedded/dist")
	if err != nil {
		t.Fatalf("fs.Sub embedded/dist: %v", err)
	}
	data, err := fs.ReadFile(realDist, "public-manifest.json")
	if err != nil {
		t.Skip("public-manifest.json not present (placeholder checkout without a real web build)")
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		t.Fatalf("unmarshal public-manifest.json: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("public-manifest.json is empty")
	}

	for _, name := range names {
		path := "/" + name
		rec := serveReq(mux, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s (manifested) = %d, want 200, body = %s", path, rec.Code, rec.Body.String())
		}
	}

	// "/" itself (public.html's actual serving path) must also 200.
	rec := serveReq(mux, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("GET / = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
}

// publicIndexHandler is tested against fstest.MapFS fixtures, not buildPublicMux: the embedded dist differs by environment (the placeholder has no public.html, the Dockerfile's real build does),
// so a test on the real embed would pass on a host checkout and fail in every image build.

// Anything short of a built public page with a non-empty manifest must fail closed with the 503, never a broken shell
// whose every script/style 404s with no error surfaced, and the asset path must plain 404 (not serve the file, not panic).
func TestPublicIndexHandlerFailsClosed(t *testing.T) {
	const publicHTML = "<html>public quote page</html>"
	cases := []struct {
		name string
		fsys fstest.MapFS
	}{
		{"not built", fstest.MapFS{}},
		{"no manifest", fstest.MapFS{
			"public.html":   {Data: []byte(publicHTML)},
			"assets/app.js": {Data: []byte("console.log('public')")},
		}},
		// loadPublicManifest treats a present-but-empty manifest like a missing one.
		{"empty manifest", fstest.MapFS{
			"public.html":          {Data: []byte(publicHTML)},
			"public-manifest.json": {Data: []byte(`[]`)},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := publicIndexHandler(tc.fsys)

			rec := serveReq(handler, httptest.NewRequest("GET", "/", nil))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("GET / = %d, want 503", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "public page not built") {
				t.Errorf("body = %q, want it to mention the page isn't built", rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "public quote page") {
				t.Error("response served public.html's content instead of the 503 body")
			}

			rec = serveReq(handler, httptest.NewRequest("GET", "/assets/app.js", nil))
			if rec.Code != http.StatusNotFound {
				t.Errorf("GET /assets/app.js = %d, want 404", rec.Code)
			}
		})
	}
}

// With public.html and a matching public-manifest.json, "/" 200s with that file, and a manifested asset path is served from the fs rather than redirected to public.html.
func TestPublicIndexHandlerServesPublicHTML(t *testing.T) {
	sub := fstest.MapFS{
		"public.html":          {Data: []byte("<html>public quote page</html>")},
		"assets/app.js":        {Data: []byte("console.log('public')")},
		"public-manifest.json": {Data: []byte(`["public.html", "assets/app.js"]`)},
	}
	handler := publicIndexHandler(sub)

	rec := serveReq(handler, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "public quote page") {
		t.Errorf("body = %q, want public.html's content", rec.Body.String())
	}

	rec = serveReq(handler, httptest.NewRequest("GET", "/assets/app.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /assets/app.js = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "console.log('public')") {
		t.Errorf("body = %q, want app.js's content", rec.Body.String())
	}
}

// Core isolation assertion: the public listener must never serve the admin bundle. A MapFS shaped like a Vite dist lists the public files in the manifest beside unlisted admin files (index.html, assets/main-*),
// and existence in the fs is not enough: only manifest membership is, with manifested paths read from the manifest and admin names tried with several fake hashes.
func TestPublicIndexHandlerEnforcesManifestAllowlist(t *testing.T) {
	manifest := []string{"public.html", "assets/public-abc123.js", "assets/theme-def456.css", "alpaca-logo.svg"}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	sub := fstest.MapFS{
		"public.html":             {Data: []byte("<html>public quote page</html>")},
		"assets/public-abc123.js": {Data: []byte("console.log('public')")},
		"assets/theme-def456.css": {Data: []byte("body{color:cream}")},
		"alpaca-logo.svg":         {Data: []byte("<svg>alpaca</svg>")},
		"public-manifest.json":    {Data: manifestJSON},
		// Admin-only files: physically present in the same embedded dist
		// (exactly like the real build, where both Vite entries land side by
		// side), but absent from the manifest above.
		"index.html":             {Data: []byte("<html>printpaca admin shell</html>")},
		"assets/main-xyz789.js":  {Data: []byte("the whole admin SPA bundle")},
		"assets/main-xyz789.css": {Data: []byte("the whole admin SPA stylesheet")},
	}
	handler := publicIndexHandler(sub)

	// Every manifested file resolves 200 -- public.html itself served at
	// "/", everything else at its own manifest path.
	for _, name := range manifest {
		path := "/" + name
		if name == "public.html" {
			path = "/"
		}
		rec := serveReq(handler, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s (manifested) = %d, want 200, body = %s", path, rec.Code, rec.Body.String())
		}
	}

	// Files that exist in the fs but are NOT on the manifest (above all the admin bundle) must 404; /assets/main-<hash> is tried with several synthetic hashes so this isn't one coincidental filename.
	notManifested := []string{"/index.html", "/assets/main-xyz789.js", "/assets/main-xyz789.css"}
	for _, hash := range []string{"aaaa0000", "Zz99Yy88", "0"} {
		notManifested = append(notManifested, "/assets/main-"+hash+".js", "/assets/main-"+hash+".css")
	}
	for _, path := range notManifested {
		rec := serveReq(handler, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s (not manifested) = %d, want 404, body = %s", path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "admin") {
			t.Errorf("GET %s leaked admin content in a non-404 body: %s", path, rec.Body.String())
		}
	}
}

// The public listener serves the build's .gz sibling via serveGzipVariant like the admin listener (static.go); the client requests the plain name, which is what the manifest allowlist checks, so only the bytes served change.
func TestPublicIndexHandlerServesGzipVariant(t *testing.T) {
	js := []byte("console.log('public');")
	sub := fstest.MapFS{
		"public.html":             {Data: []byte("<html>public quote page</html>")},
		"assets/public-abc123.js": {Data: js},
		"assets/public-abc123.js.gz": {
			Data: gzipBytes(t, js),
		},
		"public-manifest.json": {Data: []byte(`["public.html", "assets/public-abc123.js"]`)},
	}
	handler := publicIndexHandler(sub)

	req := httptest.NewRequest(http.MethodGet, "/assets/public-abc123.js", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := serveReq(handler, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want gzip", got)
	}
	gr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("response body is not valid gzip: %v", err)
	}
	decoded, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("decode gzip body: %v", err)
	}
	if string(decoded) != string(js) {
		t.Errorf("decoded body = %q, want %q", decoded, js)
	}

	// A path that physically exists in sub but isn't on the manifest must
	// still 404, gzip sibling or not -- the allowlist check runs first.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/assets/public-abc123.js.gz", nil)
	handler.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Errorf("GET .../public-abc123.js.gz directly = %d, want 404 (not on the manifest)", rec2.Code)
	}
}

// The year-long immutable Cache-Control for /assets/ paths must only land on a response that served an allowlisted asset, never on the 404 for a missing /assets/<name>
// (same bug class as itemImageHandler): a client or CDN would cache the 404 for a year.
func TestPublicIndexHandlerNotFoundHasNoImmutableCacheControl(t *testing.T) {
	sub := fstest.MapFS{
		"public.html":             {Data: []byte("<html>public quote page</html>")},
		"assets/public-abc123.js": {Data: []byte("console.log('public')")},
		"public-manifest.json":    {Data: []byte(`["public.html", "assets/public-abc123.js"]`)},
	}
	handler := publicIndexHandler(sub)

	// A well-formed but non-allowlisted /assets/<name> path.
	rec := serveReq(handler, httptest.NewRequest("GET", "/assets/not-on-manifest.js", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /assets/not-on-manifest.js = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); strings.Contains(got, "immutable") {
		t.Errorf("Cache-Control on 404 = %q, must not contain immutable", got)
	}

	// A real allowlisted asset still gets the immutable header.
	rec2 := serveReq(handler, httptest.NewRequest("GET", "/assets/public-abc123.js", nil))
	if rec2.Code != http.StatusOK {
		t.Fatalf("GET /assets/public-abc123.js = %d, want 200, body = %s", rec2.Code, rec2.Body.String())
	}
	if got := rec2.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Errorf("Cache-Control on served asset = %q, want it to contain immutable", got)
	}
}

// ---------------------------------------------------------------------------
// GET /api/public/config
// ---------------------------------------------------------------------------

// Core secrecy assertion: the raw body must contain the public material and its density (client-side file-weight parsing) and must NOT contain the nonpublic material's name,
// printer cost-model fields (watts/purchaseCost) or printer name/id, or anything from the smtp section.
func TestPublicConfigFieldByFieldAssembly(t *testing.T) {
	dir := t.TempDir()
	user := `
materials:
  - { id: pla,  name: PLA,  density: 1.24, pricePerKg: 20.00, wastePct: 0, public: true }
  - { id: petg, name: PETG, density: 1.27, pricePerKg: 22.00, wastePct: 0, public: false }
printers:
  - { id: generic, name: "Generic FDM", watts: 250, purchaseCost: 400,  lifespanHours: 4000, filamentDiameterMm: 1.75 }
  - { id: secret,  name: "Secret Printer", watts: 500, purchaseCost: 9999, lifespanHours: 8000, filamentDiameterMm: 2.85 }
quote:
  businessName: "Alpaca Prints"
  email: "hello@alpaca.example"
  phone: "555-0101"
  address: "1 Pasture Lane"
publicQuotes:
  printer: generic
  preset: hobby
  introText: "Send us your model"
smtp:
  host: smtp.example.com
  username: notifier
  from: quotes@example.com
  to: owner@example.com
`
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := loadTestConfig(t, dir)
	db := openTestDB(t, dir)
	mux, _ := buildPublicMux(NewConfigStore(cfg), db, dir, false, false)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/public/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}

	body := rec.Body.String()
	// Branding contact fields (businessName/email/phone/address) are served
	// for the public page's identity footer -- display-only details the
	// owner already prints on every quote/invoice.
	for _, want := range []string{"PLA", "1.24", "1.75", "Send us your model",
		"Alpaca Prints", "hello@alpaca.example", "555-0101", "1 Pasture Lane"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing expected content %q:\n%s", want, body)
		}
	}
	for _, forbidden := range []string{
		"PETG", "20", "22", // material cost fields (pricePerKg) are gone
		"Generic FDM", "Secret Printer", "250", "400", "9999", // printer cost fields (watts/purchaseCost) are gone
		"smtp", "notifier", "quotes@example.com", "owner@example.com",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("body leaks nonpublic content %q:\n%s", forbidden, body)
		}
	}

	var resp publicConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Materials) != 1 || resp.Materials[0].Name != "PLA" || resp.Materials[0].Density != 1.24 {
		t.Errorf("materials = %+v, want exactly PLA at density 1.24", resp.Materials)
	}
	// The printer object is gone entirely; the one surviving field is top-level and still pins the resolved printer: "generic" is 1.75mm, "secret" is 2.85mm.
	if resp.FilamentDiameterMm != 1.75 {
		t.Errorf("filamentDiameterMm = %v, want 1.75 (the resolved public printer, \"generic\")", resp.FilamentDiameterMm)
	}
}

// Pins server-authoritative public pricing: preset/energy/pricing/defaultShippingCost are gone from the wire response (estimates come from POST /api/public/quote/preview).
// Materials/printer keep only what client-side file parsing needs (density, filamentDiameterMm), not pricePerKg/wastePct/watts/purchaseCost/lifespanHours.
func TestPublicConfigDropsCostModelFields(t *testing.T) {
	dir := t.TempDir()
	cfg := newPublicTestConfig(t, dir)
	db := openTestDB(t, dir)
	mux, _ := buildPublicMux(NewConfigStore(cfg), db, dir, false, false)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/public/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal top level: %v", err)
	}
	for _, forbidden := range []string{"preset", "energy", "pricing", "defaultShippingCost", "printer"} {
		if _, present := raw[forbidden]; present {
			t.Errorf("top-level response carries dropped cost-model field %q: %s", forbidden, rec.Body.String())
		}
	}
	if _, present := raw["filamentDiameterMm"]; !present {
		t.Errorf("response missing top-level filamentDiameterMm, still needed for client-side file parsing: %s", rec.Body.String())
	}

	var materials []map[string]json.RawMessage
	if err := json.Unmarshal(raw["materials"], &materials); err != nil {
		t.Fatalf("unmarshal materials: %v", err)
	}
	if len(materials) == 0 {
		t.Fatalf("materials is empty; test setup should have marked at least one public")
	}
	for _, forbidden := range []string{"pricePerKg", "wastePct"} {
		if _, present := materials[0][forbidden]; present {
			t.Errorf("materials[0] carries dropped field %q: %s", forbidden, raw["materials"])
		}
	}
	if _, present := materials[0]["density"]; !present {
		t.Errorf("materials[0] missing density, still needed for client-side file parsing: %s", raw["materials"])
	}
}

func TestPublicConfigZeroPublicMaterialsIsEmptyList(t *testing.T) {
	dir := t.TempDir()
	cfg, err := LoadConfig(dir) // defaults: no material is public
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	db := openTestDB(t, dir)
	mux, _ := buildPublicMux(NewConfigStore(cfg), db, dir, false, false)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/public/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp publicConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Materials == nil || len(resp.Materials) != 0 {
		t.Errorf("materials = %#v, want an empty (non-nil) list", resp.Materials)
	}
}

// The client's config type declares an optional maxUploadBytes; it must always equal publicMaxUploadBytes verbatim (no per-deployment override) so the browser warns before an upload instead of after a 413.
func TestPublicConfigServesMaxUploadBytes(t *testing.T) {
	dir := t.TempDir()
	cfg := newPublicTestConfig(t, dir)
	db := openTestDB(t, dir)
	mux, _ := buildPublicMux(NewConfigStore(cfg), db, dir, false, false)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/public/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"maxUploadBytes"`) {
		t.Fatalf("body missing maxUploadBytes field:\n%s", rec.Body.String())
	}
	var resp publicConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.MaxUploadBytes != publicMaxUploadBytes {
		t.Errorf("maxUploadBytes = %d, want %d", resp.MaxUploadBytes, publicMaxUploadBytes)
	}
}

// ---------------------------------------------------------------------------
// POST /api/public/quote/preview
// ---------------------------------------------------------------------------

// postPublicQuotePreview POSTs a JSON body (typically a map[string]any so
// individual cases can add/omit/override fields) to
// /api/public/quote/preview on mux.
func postPublicQuotePreview(t *testing.T, mux *http.ServeMux, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal preview body: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/public/quote/preview", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	rec := serveReq(mux, req)
	return rec
}

// validQuotePreviewBody uses the same fixed quantity/weightG/timeSeconds (2, 15.5, 3600) as publicQuoteRequestFields, so expectedPublicPriceCents applies to both.
func validQuotePreviewBody(material string) map[string]any {
	return map[string]any{
		"material":    material,
		"quantity":    2,
		"weightG":     15.5,
		"timeSeconds": 3600,
	}
}

// Pins the frozen response shape: priceCents is the server's recompute (what submission would store), shippingCents is cfg.Pricing.DefaultShippingCharge, and currency lets the page format both without another round trip.
func TestPublicQuotePreviewHappyPath(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, _, cfg := newRequestsTestSetup(t, dir)
	wantPrice := expectedPublicPriceCents(t, cfg, "PLA")
	wantShipping := int(toCents(cfg.Pricing.DefaultShippingCharge))

	rec := postPublicQuotePreview(t, publicMux, validQuotePreviewBody("PLA"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp publicQuotePreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.PriceCents != wantPrice {
		t.Errorf("priceCents = %d, want %d", resp.PriceCents, wantPrice)
	}
	if resp.ShippingCents != wantShipping {
		t.Errorf("shippingCents = %d, want %d", resp.ShippingCents, wantShipping)
	}
	if resp.Currency != cfg.Currency {
		t.Errorf("currency = %+v, want %+v", resp.Currency, cfg.Currency)
	}
}

// A resolved preview and the price a submission later stores must never disagree: both go through the identical server-side engine call.
func TestPublicQuotePreviewMatchesSubmissionPrice(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, db, _ := newRequestsTestSetup(t, dir)

	previewRec := postPublicQuotePreview(t, publicMux, validQuotePreviewBody("PLA"))
	if previewRec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want 200, body = %s", previewRec.Code, previewRec.Body.String())
	}
	var preview publicQuotePreviewResponse
	if err := json.Unmarshal(previewRec.Body.Bytes(), &preview); err != nil {
		t.Fatalf("unmarshal preview: %v", err)
	}

	submitRec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "", nil)
	if submitRec.Code != http.StatusCreated {
		t.Fatalf("submit status = %d, want 201, body = %s", submitRec.Code, submitRec.Body.String())
	}
	var submitted struct {
		ID         string `json:"id"`
		PriceCents int    `json:"priceCents"`
	}
	if err := json.Unmarshal(submitRec.Body.Bytes(), &submitted); err != nil {
		t.Fatalf("unmarshal submit: %v", err)
	}
	if submitted.PriceCents != preview.PriceCents {
		t.Errorf("submission priceCents = %d, want %d (must match the preview)", submitted.PriceCents, preview.PriceCents)
	}

	var stored int
	if err := db.QueryRow(`SELECT price_cents FROM quote_requests WHERE id = ?`, submitted.ID).Scan(&stored); err != nil {
		t.Fatalf("query: %v", err)
	}
	if stored != preview.PriceCents {
		t.Errorf("stored price_cents = %d, want %d", stored, preview.PriceCents)
	}
}

func TestPublicQuotePreviewValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string]any)
		wantSub string
	}{
		{"missing material", func(b map[string]any) { b["material"] = "" }, "material: required"},
		{"nonpublic material", func(b map[string]any) { b["material"] = "PETG" }, "material: no longer offered; choose another"},
		{"quantity zero", func(b map[string]any) { b["quantity"] = 0 }, "quantity:"},
		{"quantity too big", func(b map[string]any) { b["quantity"] = 1001 }, "quantity:"},
		{"weightG negative", func(b map[string]any) { b["weightG"] = -1 }, "weightG:"},
		{"weightG too big", func(b map[string]any) { b["weightG"] = 100001 }, "weightG:"},
		{"timeSeconds negative", func(b map[string]any) { b["timeSeconds"] = -1 }, "timeSeconds:"},
		{"timeSeconds too big", func(b map[string]any) { b["timeSeconds"] = maxTimeSeconds + 1 }, "timeSeconds:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			_, publicMux, _, _ := newRequestsTestSetup(t, dir)
			body := validQuotePreviewBody("PLA")
			tc.mutate(body)
			rec := postPublicQuotePreview(t, publicMux, body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422, body = %s", rec.Code, rec.Body.String())
			}
			var errResp apiErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if !containsSubstr(errResp.Errors, tc.wantSub) {
				t.Fatalf("errors = %v, want one containing %q", errResp.Errors, tc.wantSub)
			}
		})
	}
}

// There is no price field in the preview request shape (see publicQuotePreviewRequest), so a client still sending one (an unmigrated cached page) is rejected by DisallowUnknownFields, not ignored.
func TestPublicQuotePreviewUnknownFieldRejected(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, _, _ := newRequestsTestSetup(t, dir)
	body := validQuotePreviewBody("PLA")
	body["priceCents"] = 1234
	rec := postPublicQuotePreview(t, publicMux, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	var errResp apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if len(errResp.Errors) != 1 || errResp.Errors[0] != "invalid request body" {
		t.Errorf("errors = %v, want exactly [\"invalid request body\"] (no echoed decode error)", errResp.Errors)
	}
}

// TestPublicQuotePreviewWorksInDemoMode pins that preview computes and
// returns normally in DEMO_MODE: unlike the submission path, it never writes
// anything regardless of demo mode, so there is nothing to simulate.
func TestPublicQuotePreviewWorksInDemoMode(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	wantPrice := expectedPublicPriceCents(t, cfg, "PLA")
	mux, _ := buildPublicMux(NewConfigStore(cfg), db, dir, false, true)

	rec := postPublicQuotePreview(t, mux, validQuotePreviewBody("PLA"))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	var resp publicQuotePreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.PriceCents != wantPrice {
		t.Errorf("priceCents = %d, want %d", resp.PriceCents, wantPrice)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM quote_requests`).Scan(&count); err != nil {
		t.Fatalf("count quote_requests rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("quote_requests row count = %d, want 0 (preview must never persist anything)", count)
	}
}

// Preview has its OWN per-IP bucket (publicPreviewRateLimit = 60/min), not the 5/hour submission limiter, or a few debounced keystrokes would starve a visitor's real submission budget.
func TestPublicQuotePreviewRateLimitSeparateFromSubmissions(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, _, _ := newRequestsTestSetup(t, dir)

	// Exhaust the 5/hour submission budget first.
	for i := range 5 {
		rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "", nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("submission %d: status = %d, want 201, body = %s", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "", nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th submission: status = %d, want 429", rec.Code)
	}

	// Preview must still work: it has its own bucket, untouched by the
	// exhausted submission budget above.
	previewRec := postPublicQuotePreview(t, publicMux, validQuotePreviewBody("PLA"))
	if previewRec.Code != http.StatusOK {
		t.Fatalf("preview after exhausting submissions: status = %d, want 200, body = %s", previewRec.Code, previewRec.Body.String())
	}
}

// TestPublicQuotePreviewRateLimit pins the preview endpoint's own 60/min
// budget: the 61st call from the same IP within the window is rejected.
func TestPublicQuotePreviewRateLimit(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, _, _ := newRequestsTestSetup(t, dir)

	for i := range publicPreviewRateLimit {
		rec := postPublicQuotePreview(t, publicMux, validQuotePreviewBody("PLA"))
		if rec.Code != http.StatusOK {
			t.Fatalf("preview %d: status = %d, want 200, body = %s", i+1, rec.Code, rec.Body.String())
		}
	}
	rec := postPublicQuotePreview(t, publicMux, validQuotePreviewBody("PLA"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("preview past budget: status = %d, want 429", rec.Code)
	}

	// A different IP is not affected by the first IP's limit.
	body, err := json.Marshal(validQuotePreviewBody("PLA"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/public/quote/preview", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "10.0.0.9:54321"
	rec2 := serveReq(publicMux, req)
	if rec2.Code != http.StatusOK {
		t.Fatalf("different IP: status = %d, want 200, body = %s", rec2.Code, rec2.Body.String())
	}
}

// ---------------------------------------------------------------------------
// POST /api/public/quote-request
// ---------------------------------------------------------------------------

func TestPublicQuoteRequestHappyPath(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, db, cfg := newRequestsTestSetup(t, dir)
	want := expectedPublicPriceCents(t, cfg, "PLA")

	content := []byte("G1 X10 Y10\nG1 X20 Y20\n; a fake gcode file\n")
	rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "model.gcode", content)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		ID         string `json:"id"`
		PriceCents int    `json:"priceCents"`
		Message    string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ID == "" || resp.PriceCents != want || resp.Message != "Estimate, subject to review" {
		t.Fatalf("response = %+v, want priceCents %d (the server's own recompute)", resp, want)
	}

	var status, gcodePath string
	var gcodeBytes int
	err := db.QueryRow(`SELECT status, gcode_path, gcode_bytes FROM quote_requests WHERE id = ?`, resp.ID).
		Scan(&status, &gcodePath, &gcodeBytes)
	if err != nil {
		t.Fatalf("query row: %v", err)
	}
	if status != "new" {
		t.Errorf("status = %q, want new", status)
	}
	if gcodeBytes != len(content) {
		t.Errorf("gcode_bytes = %d, want %d", gcodeBytes, len(content))
	}

	f, err := os.Open(gcodePath)
	if err != nil {
		t.Fatalf("open stored file: %v", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gz.Close()
	got, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read gunzipped content: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("gunzipped content = %q, want %q", got, content)
	}
}

// A DECIDED scope: the hardware catalog is ADMIN-QUOTE-ONLY. Public visitors cannot select hardware and cannot know which add-ons a part needs, so applying the whole catalog would charge them for parts their model may not use.
// This test keeps the omission intentional: a change in either direction would otherwise be silent.
// Covered paths: buildPublicQuoteInputs (shared by preview and submission) and an end-to-end submission whose stored price must not move when the catalog is populated.
func TestPublicQuotesNeverPriceHardware(t *testing.T) {
	dir := t.TempDir()
	cfg := newPublicTestConfig(t, dir)

	var material Material
	for _, m := range cfg.Materials {
		if m.ID == "pla" {
			material = m
		}
	}
	if material.ID == "" {
		t.Fatal("test setup: no pla material")
	}

	// Baseline: the public price with an empty catalog (the shipped default).
	if len(cfg.Hardware) != 0 {
		t.Fatalf("test setup: expected an empty default hardware catalog, got %d entries", len(cfg.Hardware))
	}
	baseInputs, baseOpt := buildPublicQuoteInputs(cfg, material, 2, 15.5, 3600)
	basePrice := ComputeQuote(baseInputs, baseOpt).Pricing.Revenue

	// Now populate the catalog with an entry expensive enough that pricing
	// it would be unmistakable, including a lostOnFailure one (which would
	// also inflate the failure buffer).
	cfg.Hardware = []Hardware{
		{Name: "Magnet", UnitCost: 25.00, LostOnFailure: true},
		{Name: "Hook", UnitCost: 40.00, LostOnFailure: false},
	}

	qi, opt := buildPublicQuoteInputs(cfg, material, 2, 15.5, 3600)
	if qi.Hardware != nil {
		t.Errorf("buildPublicQuoteInputs returned Hardware = %+v, want nil (hardware is admin-quote-only)", qi.Hardware)
	}
	if got := ComputeQuote(qi, opt).Pricing.Revenue; got != basePrice {
		t.Errorf("public price = %d with a populated hardware catalog, want %d (unchanged)", got, basePrice)
	}

	// End to end through the submission path, whose stored price_cents is
	// what RequestsTab's verification replay compares against.
	dbDir := t.TempDir()
	_, publicMux, db, liveCfg := newRequestsTestSetup(t, dbDir)
	wantStored := expectedPublicPriceCents(t, liveCfg, "PLA")
	liveCfg.Hardware = []Hardware{{Name: "Magnet", UnitCost: 25.00, LostOnFailure: true}}

	rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "", nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID         string `json:"id"`
		PriceCents int    `json:"priceCents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.PriceCents != wantStored {
		t.Errorf("submitted priceCents = %d, want %d (a populated hardware catalog must not move a public price)", resp.PriceCents, wantStored)
	}
	var stored int
	if err := db.QueryRow(`SELECT price_cents FROM quote_requests WHERE id = ?`, resp.ID).Scan(&stored); err != nil {
		t.Fatalf("query: %v", err)
	}
	if stored != wantStored {
		t.Errorf("stored price_cents = %d, want %d", stored, wantStored)
	}
}

// The stored price_cents is always the server's recompute (buildPublicQuoteInputs + ComputeQuote), whatever a client sends for a priceCents field (drained as unrecognized).
// price_cents is the sole price column, so there is no server-vs-client price mismatch to flag.
func TestPublicQuoteRequestPriceIsAlwaysServerComputed(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, db, cfg := newRequestsTestSetup(t, dir)
	want := expectedPublicPriceCents(t, cfg, "PLA")

	fields := publicQuoteRequestFields("PLA")
	fields["priceCents"] = "999999999" // wildly different from the server's own computation; must be ignored
	rec := postPublicQuoteRequest(t, publicMux, fields, "", nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID         string `json:"id"`
		PriceCents int    `json:"priceCents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.PriceCents != want {
		t.Errorf("response priceCents = %d, want %d (the server's own recompute, ignoring the client's priceCents field)", resp.PriceCents, want)
	}

	var priceCents int
	if err := db.QueryRow(`SELECT price_cents FROM quote_requests WHERE id = ?`, resp.ID).Scan(&priceCents); err != nil {
		t.Fatalf("query: %v", err)
	}
	if priceCents != want {
		t.Errorf("stored price_cents = %d, want %d", priceCents, want)
	}
}

// Admin GET /api/requests/{id}, where the owner reviews requests, serves the same server-computed price_cents and no serverPriceCents or priceMismatch fields.
func TestPublicQuoteRequestPriceExposedToOwnerIsServerComputed(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, _, cfg := newRequestsTestSetup(t, dir)
	want := expectedPublicPriceCents(t, cfg, "PLA")

	rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "", nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	rec = serveReq(adminMux, httptest.NewRequest("GET", "/api/requests/"+created.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/requests/%s: %d %s", created.ID, rec.Code, rec.Body.String())
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal raw: %v", err)
	}
	for _, gone := range []string{"serverPriceCents", "priceMismatch"} {
		if _, present := raw[gone]; present {
			t.Errorf("response still carries dropped field %q: %s", gone, rec.Body.String())
		}
	}

	var req QuoteRequest
	if err := json.Unmarshal(rec.Body.Bytes(), &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if req.PriceCents != want {
		t.Errorf("priceCents = %d, want %d (the server's own recompute)", req.PriceCents, want)
	}
}

// A visitor without a sliced file supplies weightG/timeSeconds as manual fields and the "gcode" part is absent; gcode is optional server-side, and the row must land with empty/zero gcode_filename, gcode_path, gcode_bytes.
func TestPublicQuoteRequestManualEntryNoFile(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, db, cfg := newRequestsTestSetup(t, dir)
	want := expectedPublicPriceCents(t, cfg, "PLA")

	rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "", nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		ID         string `json:"id"`
		PriceCents int    `json:"priceCents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ID == "" || resp.PriceCents != want {
		t.Fatalf("response = %+v, want priceCents %d (the server's own recompute)", resp, want)
	}

	var gcodeFilename, gcodePath string
	var gcodeBytes int
	err := db.QueryRow(`SELECT gcode_filename, gcode_path, gcode_bytes FROM quote_requests WHERE id = ?`, resp.ID).
		Scan(&gcodeFilename, &gcodePath, &gcodeBytes)
	if err != nil {
		t.Fatalf("query row: %v", err)
	}
	if gcodeFilename != "" || gcodePath != "" || gcodeBytes != 0 {
		t.Errorf("gcode fields = (%q, %q, %d), want all empty/zero", gcodeFilename, gcodePath, gcodeBytes)
	}
}

func TestPublicQuoteRequestValidation(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string]string)
		noFile  bool
		wantSub string
	}{
		{"missing name", func(f map[string]string) { f["name"] = "" }, false, "name: required"},
		{"overlong name", func(f map[string]string) { f["name"] = strings.Repeat("x", 201) }, false, "name: max"},
		{"missing email", func(f map[string]string) { f["email"] = "" }, false, "email: required"},
		{"email without at", func(f map[string]string) { f["email"] = "visitor" }, false, publicEmailFormatProblem},
		{"overlong note", func(f map[string]string) { f["note"] = strings.Repeat("x", 2001) }, false, "note: max"},
		{"quantity zero", func(f map[string]string) { f["quantity"] = "0" }, false, "quantity:"},
		{"quantity too big", func(f map[string]string) { f["quantity"] = "1001" }, false, "quantity:"},
		{"quantity not a number", func(f map[string]string) { f["quantity"] = "abc" }, false, "quantity:"},
		{"weightG negative", func(f map[string]string) { f["weightG"] = "-1" }, false, "weightG:"},
		{"weightG too big", func(f map[string]string) { f["weightG"] = "100001" }, false, "weightG:"},
		// strconv.ParseFloat accepts these tokens and every comparison against NaN is false, so a bare range check let them through to the config-unavailable 422 instead of this field-scoped one.
		{"weightG NaN", func(f map[string]string) { f["weightG"] = "NaN" }, false, "weightG:"},
		{"weightG Inf", func(f map[string]string) { f["weightG"] = "Inf" }, false, "weightG:"},
		{"weightG -Inf", func(f map[string]string) { f["weightG"] = "-Inf" }, false, "weightG:"},
		{"weightG +Inf", func(f map[string]string) { f["weightG"] = "+Inf" }, false, "weightG:"},
		{"timeSeconds negative", func(f map[string]string) { f["timeSeconds"] = "-1" }, false, "timeSeconds:"},
		{"timeSeconds too big", func(f map[string]string) { f["timeSeconds"] = "2592001" }, false, "timeSeconds:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			_, publicMux, _, _ := newRequestsTestSetup(t, dir)
			fields := publicQuoteRequestFields("PLA")
			if tc.mutate != nil {
				tc.mutate(fields)
			}
			gcodeName, content := "model.gcode", []byte("G1 X1\n")
			if tc.noFile {
				gcodeName = ""
			}
			rec := postPublicQuoteRequest(t, publicMux, fields, gcodeName, content)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422, body = %s", rec.Code, rec.Body.String())
			}
			var errResp apiErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if !containsSubstr(errResp.Errors, tc.wantSub) {
				t.Fatalf("errors = %v, want one containing %q", errResp.Errors, tc.wantSub)
			}
		})
	}
}

// A non-finite weightG is blamed on the visitor's input: the field-scoped 422 naming weightG, not the generic "price: unavailable for the current configuration" 422, and it must never reach the recompute (no "invalid breakdown" log line).
func TestPublicQuoteRequestNonFiniteWeightGIsFieldScopedNot422Config(t *testing.T) {
	for _, weightG := range []string{"NaN", "Inf", "+Inf", "-Inf"} {
		t.Run(weightG, func(t *testing.T) {
			dir := t.TempDir()
			_, publicMux, _, _ := newRequestsTestSetup(t, dir)
			fields := publicQuoteRequestFields("PLA")
			fields["weightG"] = weightG

			var buf bytes.Buffer
			restore := redirectLog(t, &buf)
			defer restore()

			rec := postPublicQuoteRequest(t, publicMux, fields, "model.gcode", []byte("G1\n"))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422, body = %s", rec.Code, rec.Body.String())
			}
			var errResp apiErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			if !containsSubstr(errResp.Errors, "weightG:") {
				t.Fatalf("errors = %v, want a weightG: message", errResp.Errors)
			}
			if containsSubstr(errResp.Errors, "unavailable for the current configuration") {
				t.Fatalf("errors = %v, blamed the owner's configuration instead of the field", errResp.Errors)
			}
			if strings.Contains(buf.String(), "invalid breakdown") {
				t.Fatalf("log output = %q, must not log an invalid-breakdown line for attacker-controlled input", buf.String())
			}
		})
	}
}

// emailOfLen returns a syntactically valid (contains "@") email address
// exactly n characters long, for boundary-testing quoteRequestsEmailMaxLen.
func emailOfLen(n int) string {
	const domain = "@example.com"
	if n < len(domain) {
		panic("emailOfLen: n too small for a valid address")
	}
	return strings.Repeat("x", n-len(domain)) + domain
}

// handlePublicItemOrder and handlePublicQuoteRequest both write quote_requests.email and must share the exact length boundary (quoteRequestsEmailMaxLen), not two drifting literals.
func TestQuoteRequestsEmailMaxLenSharedBoundary(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, _, _ := newRequestsTestSetup(t, dir)

	item := func() Item {
		rec := postItem(t, adminMux, `{"name":"Widget","priceCents":500,"public":true}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("POST item want 201, got %d %s", rec.Code, rec.Body.String())
		}
		var it Item
		if err := json.Unmarshal(rec.Body.Bytes(), &it); err != nil {
			t.Fatalf("unmarshal item: %v", err)
		}
		return it
	}()

	atMax := emailOfLen(quoteRequestsEmailMaxLen)
	overMax := emailOfLen(quoteRequestsEmailMaxLen + 1)

	t.Run("item order accepts at max", func(t *testing.T) {
		b := validItemOrderBody(item.ID, "", 1)
		b["email"] = atMax
		rec := postPublicItemOrder(t, publicMux, b)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201, body = %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("item order rejects over max", func(t *testing.T) {
		b := validItemOrderBody(item.ID, "", 1)
		b["email"] = overMax
		rec := postPublicItemOrder(t, publicMux, b)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422, body = %s", rec.Code, rec.Body.String())
		}
		var errResp apiErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("decode error body: %v", err)
		}
		if !containsSubstr(errResp.Errors, "email: max") {
			t.Fatalf("errors = %v, want an email:max message", errResp.Errors)
		}
	})
	t.Run("quote request accepts at max", func(t *testing.T) {
		fields := publicQuoteRequestFields("PLA")
		fields["email"] = atMax
		rec := postPublicQuoteRequest(t, publicMux, fields, "model.gcode", []byte("G1\n"))
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201, body = %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("quote request rejects over max", func(t *testing.T) {
		fields := publicQuoteRequestFields("PLA")
		fields["email"] = overMax
		rec := postPublicQuoteRequest(t, publicMux, fields, "model.gcode", []byte("G1\n"))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422, body = %s", rec.Code, rec.Body.String())
		}
		var errResp apiErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
			t.Fatalf("decode error body: %v", err)
		}
		if !containsSubstr(errResp.Errors, "email: max") {
			t.Fatalf("errors = %v, want an email:max message", errResp.Errors)
		}
	})
}

func TestPublicQuoteRequestNonpublicMaterialRejected(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, _, _ := newRequestsTestSetup(t, dir)
	fields := publicQuoteRequestFields("PETG") // not marked public in newPublicTestConfig
	rec := postPublicQuoteRequest(t, publicMux, fields, "model.gcode", []byte("G1\n"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422, body = %s", rec.Code, rec.Body.String())
	}
	var errResp apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if !containsSubstr(errResp.Errors, "material: no longer offered; choose another") {
		t.Fatalf("errors = %v", errResp.Errors)
	}
}

func TestPublicQuoteRequestOversizedFileIs413(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, _, _ := newRequestsTestSetup(t, dir)
	big := make([]byte, publicMaxUploadBytes+1024)
	rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "huge.gcode", big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

// dirSize backs publicUploadsCapBytes, which bounds anonymous visitor uploads only. Admin item-gallery images live at uploads/items and must not count against it,
// or a growing gallery would eat the budget real visitor submissions need.
func TestDirSizeSkipsItemsSubdirectory(t *testing.T) {
	dir := t.TempDir()
	uploadsDir := filepath.Join(dir, "uploads")
	if err := os.MkdirAll(filepath.Join(uploadsDir, "items"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(uploadsDir, "request.gcode.gz"), []byte("abcde"), 0o644); err != nil {
		t.Fatalf("write visitor upload: %v", err)
	}
	if err := os.WriteFile(filepath.Join(uploadsDir, "items", "item.jpg"), []byte("a much longer item-gallery image payload"), 0o644); err != nil {
		t.Fatalf("write item image: %v", err)
	}

	total, err := dirSize(uploadsDir)
	if err != nil {
		t.Fatalf("dirSize: %v", err)
	}
	if total != 5 {
		t.Errorf("dirSize = %d, want 5 (only the visitor upload; uploads/items must be skipped)", total)
	}
}

// A full CONFIG_DIR/uploads volume must degrade, not hard-fail with a 507: 201, row persisted with no stored file, fileStored: false. Unlike a manual submission, the dropped upload's filename and byte count must SURVIVE
// (erasing them made the admin UI claim no file was uploaded). Encoding (QuoteRequest doc): gcode_filename != "" AND gcode_path == "" AND gcode_pruned_at == "".
func TestPublicQuoteRequestStorageFullDegradesInsteadOfFailing(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, db, _ := newRequestsTestSetup(t, dir)

	// Push CONFIG_DIR/uploads past publicUploadsCapBytes with one sparse filler: Truncate extends the logical size dirSize reads without writing data, so it stays fast for any cap.
	uploadsDir := filepath.Join(dir, "uploads")
	if err := os.MkdirAll(uploadsDir, 0o755); err != nil {
		t.Fatalf("mkdir uploads: %v", err)
	}
	filler, err := os.Create(filepath.Join(uploadsDir, "filler.bin"))
	if err != nil {
		t.Fatalf("create filler: %v", err)
	}
	if err := filler.Truncate(publicUploadsCapBytes + 1); err != nil {
		t.Fatalf("truncate filler: %v", err)
	}
	if err := filler.Close(); err != nil {
		t.Fatalf("close filler: %v", err)
	}

	rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "model.gcode", []byte("G1 X1 Y1\n"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (storage-full must degrade, not fail, the submission), body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		ID         string `json:"id"`
		PriceCents int    `json:"priceCents"`
		FileStored bool   `json:"fileStored"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ID == "" {
		t.Fatal("response id is empty")
	}
	if resp.FileStored {
		t.Errorf("fileStored = true, want false (the file arrived but was dropped for storage-full)")
	}

	var gcodeFilename, gcodePath, gcodePrunedAt string
	var gcodeBytes int
	if err := db.QueryRow(`SELECT gcode_filename, gcode_path, gcode_bytes, gcode_pruned_at FROM quote_requests WHERE id = ?`, resp.ID).
		Scan(&gcodeFilename, &gcodePath, &gcodeBytes, &gcodePrunedAt); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if gcodePath != "" {
		t.Errorf("gcode_path = %q, want empty (the file was never written)", gcodePath)
	}
	if gcodePrunedAt != "" {
		t.Errorf("gcode_pruned_at = %q, want empty (this row was never stored, so it was never pruned either -- see QuoteRequest's doc)", gcodePrunedAt)
	}
	const content = "G1 X1 Y1\n"
	if gcodeFilename != "model.gcode" {
		t.Errorf("gcode_filename = %q, want %q (the filename must survive the storage-full degrade)", gcodeFilename, "model.gcode")
	}
	if gcodeBytes != len(content) {
		t.Errorf("gcode_bytes = %d, want %d (the byte count must survive the storage-full degrade)", gcodeBytes, len(content))
	}
}

func TestPublicQuoteRequestRateLimit(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, _, _ := newRequestsTestSetup(t, dir)

	for i := range 5 {
		rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "model.gcode", []byte("G1\n"))
		if rec.Code != http.StatusCreated {
			t.Fatalf("submission %d: status = %d, want 201, body = %s", i+1, rec.Code, rec.Body.String())
		}
	}

	rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "model.gcode", []byte("G1\n"))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th submission: status = %d, want 429, body = %s", rec.Code, rec.Body.String())
	}

	// A different IP is not affected by the first IP's limit.
	req := newPublicQuoteRequest(t, publicQuoteRequestFields("PLA"), "model.gcode", []byte("G1\n"))
	req.RemoteAddr = "10.0.0.9:54321"
	rec2 := serveReq(publicMux, req)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("different IP: status = %d, want 201, body = %s", rec2.Code, rec2.Body.String())
	}
}

func TestPublicQuoteRequestSpoofedForwardedForCannotEvadeLimit(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, _, _ := newRequestsTestSetup(t, dir) // trustProxy=false

	// Without PUBLIC_TRUST_PROXY, X-Forwarded-For is attacker-controlled
	// noise: rotating it must not mint fresh rate-limit keys.
	for i := range 5 {
		req := newPublicQuoteRequest(t, publicQuoteRequestFields("PLA"), "model.gcode", []byte("G1\n"))
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
		rec := serveReq(publicMux, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("submission %d: status = %d, want 201, body = %s", i+1, rec.Code, rec.Body.String())
		}
	}
	req := newPublicQuoteRequest(t, publicQuoteRequestFields("PLA"), "model.gcode", []byte("G1\n"))
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	rec := serveReq(publicMux, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th spoofed submission: status = %d, want 429", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// rateLimitKey / clientIP IPv6 normalization
// ---------------------------------------------------------------------------

// Every address within one /64 collapses to the same key, IPv4 keeps its full /32, and a value that doesn't parse as an IP is keyed verbatim.
func TestRateLimitKeyNormalizesIPv6ToSlash64(t *testing.T) {
	a := rateLimitKey("2001:db8:1234:5678::1")
	b := rateLimitKey("2001:db8:1234:5678:ffff:ffff:ffff:ffff")
	if a != b {
		t.Errorf("two addresses in the same /64 produced different keys: %q vs %q", a, b)
	}
	c := rateLimitKey("2001:db8:1234:5679::1") // one bit past the /64 boundary
	if a == c {
		t.Errorf("addresses in different /64s produced the same key: %q", a)
	}

	if got, want := rateLimitKey("203.0.113.5"), "203.0.113.5"; got != want {
		t.Errorf("rateLimitKey(IPv4) = %q, want %q (IPv4 keeps its full /32)", got, want)
	}
	if rateLimitKey("203.0.113.6") == rateLimitKey("203.0.113.5") {
		t.Error("two distinct IPv4 addresses produced the same key")
	}

	if got, want := rateLimitKey("not-an-ip"), "not-an-ip"; got != want {
		t.Errorf("rateLimitKey(garbage) = %q, want it keyed verbatim %q", got, want)
	}
}

// End to end: two IPv6 addresses in the same /64 share the publicRateLimit budget, so rotating within one allocation (2^64 addresses) can't mint unbounded budgets.
func TestPublicQuoteRequestIPv6Slash64SharesRateLimitBudget(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, _, _ := newRequestsTestSetup(t, dir)

	addrs := []string{
		"[2001:db8:1234:5678::1]:1111",
		"[2001:db8:1234:5678::2]:2222",
		"[2001:db8:1234:5678::3]:3333",
		"[2001:db8:1234:5678::4]:4444",
		"[2001:db8:1234:5678::5]:5555",
	}
	for i, addr := range addrs {
		req := newPublicQuoteRequest(t, publicQuoteRequestFields("PLA"), "model.gcode", []byte("G1\n"))
		req.RemoteAddr = addr
		rec := serveReq(publicMux, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("submission %d (%s): status = %d, want 201, body = %s", i+1, addr, rec.Code, rec.Body.String())
		}
	}

	// A 6th distinct address still within the same /64: the shared budget is
	// exhausted.
	req := newPublicQuoteRequest(t, publicQuoteRequestFields("PLA"), "model.gcode", []byte("G1\n"))
	req.RemoteAddr = "[2001:db8:1234:5678::6]:6666"
	rec := serveReq(publicMux, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th address in the same /64: status = %d, want 429, body = %s", rec.Code, rec.Body.String())
	}

	// An address in a DIFFERENT /64 is unaffected.
	req2 := newPublicQuoteRequest(t, publicQuoteRequestFields("PLA"), "model.gcode", []byte("G1\n"))
	req2.RemoteAddr = "[2001:db8:1234:5679::1]:7777"
	rec2 := serveReq(publicMux, req2)
	if rec2.Code != http.StatusCreated {
		t.Fatalf("address in a different /64: status = %d, want 201, body = %s", rec2.Code, rec2.Body.String())
	}
}

// TestPublicQuoteRequestXFFIPv6Slash64Aggregates mirrors the RemoteAddr
// version above through the trusted X-Forwarded-For path (clientIP's other
// branch), confirming the same normalization applies there too.
func TestPublicQuoteRequestXFFIPv6Slash64Aggregates(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	// trustProxy=true so X-Forwarded-For is honored (see clientIP's doc).
	publicMux, _ := buildPublicMux(NewConfigStore(cfg), db, dir, true, false)

	for i := range 5 {
		req := newPublicQuoteRequest(t, publicQuoteRequestFields("PLA"), "model.gcode", []byte("G1\n"))
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("2001:db8:abcd:ef01::%x", i+1))
		rec := serveReq(publicMux, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("submission %d: status = %d, want 201, body = %s", i+1, rec.Code, rec.Body.String())
		}
	}
	req := newPublicQuoteRequest(t, publicQuoteRequestFields("PLA"), "model.gcode", []byte("G1\n"))
	req.Header.Set("X-Forwarded-For", "2001:db8:abcd:ef01::99")
	rec := serveReq(publicMux, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th XFF address in the same /64: status = %d, want 429, body = %s", rec.Code, rec.Body.String())
	}
}

// The multipart body must parse regardless of part order: forms send fields in DOM order, but the streaming reader (MultipartReader + writeGcodePart) must not rely on it. This writes the gcode part BEFORE any text field.
func TestPublicQuoteRequestGcodePartFirst(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, db, _ := newRequestsTestSetup(t, dir)

	content := []byte("G1 X10 Y10\n; gcode-first ordering\n")
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fw, err := mw.CreateFormFile("gcode", "model.gcode")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatalf("write gcode content: %v", err)
	}
	fields := publicQuoteRequestFields("PLA")
	for _, k := range []string{"name", "email", "note", "material", "quantity", "weightG", "timeSeconds", "priceCents"} {
		if err := mw.WriteField(k, fields[k]); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest("POST", "/api/public/quote-request", body)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	rec := serveReq(publicMux, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		ID         string `json:"id"`
		PriceCents int    `json:"priceCents"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var gcodePath string
	var gcodeBytes int
	if err := db.QueryRow(`SELECT gcode_path, gcode_bytes FROM quote_requests WHERE id = ?`, resp.ID).
		Scan(&gcodePath, &gcodeBytes); err != nil {
		t.Fatalf("query row: %v", err)
	}
	if gcodeBytes != len(content) {
		t.Errorf("gcode_bytes = %d, want %d", gcodeBytes, len(content))
	}
	f, err := os.Open(gcodePath)
	if err != nil {
		t.Fatalf("open stored file: %v", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer gz.Close()
	got, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read gunzipped content: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("gunzipped content = %q, want %q", got, content)
	}
}

// Pre-fills the concurrency semaphore to simulate publicMaxConcurrentUploads mid-upload submissions: the next gets 503 with a retry-later message (bounding memory/disk pressure), and draining it confirms it isn't stuck.
func TestPublicQuoteRequestUploadSemaphoreReturns503(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	store := NewConfigStore(cfg)
	rl := newRateLimiter()
	sem := newUploadSlots(publicMaxConcurrentUploads)
	var rels []func()
	for i := range publicMaxConcurrentUploads {
		rel, ok := sem.acquire(fmt.Sprintf("198.51.100.%d", i+1))
		if !ok {
			t.Fatalf("pre-fill slot %d refused", i)
		}
		rels = append(rels, rel)
	}
	handler := handlePublicQuoteRequest(db, store, dir, rl, false, sem, newNotifyQueue(), false)

	rec := serveReq(handler, newPublicQuoteRequest(t, publicQuoteRequestFields("PLA"), "model.gcode", []byte("G1\n")))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", rec.Code, rec.Body.String())
	}
	var errResp apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if !containsSubstr(errResp.Errors, "try again") {
		t.Fatalf("errors = %v, want a retry-later message", errResp.Errors)
	}

	// Draining the semaphore (as a completed in-flight request would) lets
	// the next submission through.
	rels[0]()
	rels[1]()
	rec2 := serveReq(handler, newPublicQuoteRequest(t, publicQuoteRequestFields("PLA"), "model.gcode", []byte("G1\n")))
	if rec2.Code != http.StatusCreated {
		t.Fatalf("after draining semaphore: status = %d, want 201, body = %s", rec2.Code, rec2.Body.String())
	}
}

// Once publicMaxOpenRequests unfinalized quote_requests rows exist, the next submission gets 503 before any multipart parsing; rows are seeded via SQL to stay fast.
func TestPublicQuoteRequestOpenRequestsCapReturns503(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)

	for i := range publicMaxOpenRequests {
		if _, err := db.Exec(
			`INSERT INTO quote_requests (id, status, name, email, material, quantity, created_at)
			 VALUES (?, 'new', 'x', 'x@example.com', 'pla', 1, '2026-01-01T00:00:00Z')`,
			fmt.Sprintf("seed-%d", i)); err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}

	cfg := newPublicTestConfig(t, dir)
	mux, _ := buildPublicMux(NewConfigStore(cfg), db, dir, false, false)

	rec := serveReq(mux, newPublicQuoteRequest(t, publicQuoteRequestFields("PLA"), "model.gcode", []byte("G1\n")))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503, body = %s", rec.Code, rec.Body.String())
	}

	// Finalizing one open request drops the count back under the cap, so
	// the next submission succeeds -- proving the cap counts open requests
	// dynamically, not just "has this process ever hit the ceiling".
	if _, err := db.Exec(`UPDATE quote_requests SET status = 'finalized' WHERE id = 'seed-0'`); err != nil {
		t.Fatalf("finalize seed-0: %v", err)
	}
	rec2 := serveReq(mux, newPublicQuoteRequest(t, publicQuoteRequestFields("PLA"), "model.gcode", []byte("G1\n")))
	if rec2.Code != http.StatusCreated {
		t.Fatalf("after finalizing one seed row: status = %d, want 201, body = %s", rec2.Code, rec2.Body.String())
	}
}

// Once the queue (publicNotifyQueueCap) is full, enqueue must not block the submitting request: it drops the job and logs. The worker is never started, so every slot stays occupied.
func TestNotifyQueueDropsWhenFull(t *testing.T) {
	nq := &notifyQueue{ch: make(chan notifyJob, publicNotifyQueueCap)}
	cfg := &Config{}

	for i := range publicNotifyQueueCap {
		nq.enqueue(cfg, QuoteRequest{ID: fmt.Sprintf("fill-%d", i)})
	}
	if len(nq.ch) != publicNotifyQueueCap {
		t.Fatalf("queue len = %d, want %d (full)", len(nq.ch), publicNotifyQueueCap)
	}

	done := make(chan struct{})
	go func() {
		nq.enqueue(cfg, QuoteRequest{ID: "overflow"}) // must not block
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("enqueue on a full queue blocked instead of dropping")
	}
	if len(nq.ch) != publicNotifyQueueCap {
		t.Fatalf("queue len after overflow = %d, want unchanged %d", len(nq.ch), publicNotifyQueueCap)
	}
}

// A rateLimiter capped at a small size evicts the least-recently-touched key past the cap, so the map stays bounded; an expiry-only sweep couldn't reclaim keys with live timestamps under a flood of rotating keys.
func TestRateLimiterEvictsLeastRecentlyUsed(t *testing.T) {
	rl := newBoundedRateLimiter(2)

	if !rl.allow("a", 10, time.Hour) {
		t.Fatal("allow(a) unexpectedly denied on an empty limiter")
	}
	if !rl.allow("b", 10, time.Hour) {
		t.Fatal("allow(b) unexpectedly denied")
	}
	if len(rl.hits) != 2 {
		t.Fatalf("key count = %d, want 2", len(rl.hits))
	}

	// A third key evicts the least-recently-touched one ("a": touched
	// before "b" and never touched again).
	if !rl.allow("c", 10, time.Hour) {
		t.Fatal("allow(c) unexpectedly denied")
	}
	if len(rl.hits) != 2 {
		t.Fatalf("key count after eviction = %d, want 2 (capped)", len(rl.hits))
	}
	if _, present := rl.hits["a"]; present {
		t.Error(`"a" should have been evicted as least-recently-used`)
	}
	if _, present := rl.hits["b"]; !present {
		t.Error(`"b" should still be present`)
	}
	if _, present := rl.hits["c"]; !present {
		t.Error(`"c" should be present`)
	}

	// A fourth key evicts "b" next (least-recently-touched after "a"'s
	// eviction above: "c" was touched most recently, "b" before that).
	if !rl.allow("d", 10, time.Hour) {
		t.Fatal("allow(d) unexpectedly denied")
	}
	if _, present := rl.hits["b"]; present {
		t.Error(`"b" should have been evicted next as least-recently-used`)
	}

	// "b" was evicted entirely, not merely aged out: a fresh touch must
	// start with a clean budget rather than remembering the one hit it
	// recorded before eviction.
	if !rl.allow("b", 1, time.Hour) {
		t.Fatal("allow(b) denied after eviction/re-creation; a re-created entry must start with a fresh budget")
	}
}

// A body claiming multipart/form-data that doesn't parse must get a fixed, generic message, not the raw parse error, which could reveal internals (e.g. that this is a Go net/http server) to an anonymous visitor.
func TestPublicQuoteRequestMalformedMultipartNotEchoed(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, _, _ := newRequestsTestSetup(t, dir)

	req := httptest.NewRequest("POST", "/api/public/quote-request", strings.NewReader("this is not a multipart body"))
	req.Header.Set("Content-Type", "multipart/form-data; boundary=X")
	rec := serveReq(publicMux, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	var errResp apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if !containsSubstr(errResp.Errors, "invalid upload") {
		t.Fatalf("errors = %v, want a fixed 'invalid upload' message", errResp.Errors)
	}
	for _, leaked := range []string{"multipart:", "NextPart", "EOF", "unexpected"} {
		if containsSubstr(errResp.Errors, leaked) {
			t.Errorf("errors = %v, looks like it echoes the raw parse error (contains %q)", errResp.Errors, leaked)
		}
	}
}

// Narrower than TestPublicQuoteRequestOversizedFileIs413: the 413 body must be the fixed message, not a raw *http.MaxBytesError string.
func TestPublicQuoteRequestOversizedFileErrorNotEchoed(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, _, _ := newRequestsTestSetup(t, dir)
	big := make([]byte, publicMaxUploadBytes+1024)
	rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "huge.gcode", big)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413, body = %s", rec.Code, rec.Body.String())
	}
	var errResp apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if !containsSubstr(errResp.Errors, "too large") {
		t.Fatalf("errors = %v, want a fixed too-large message", errResp.Errors)
	}
}

// slowBodyReader yields data in small chunks with a sleep before each,
// simulating a slow-but-real client upload for
// TestPublicQuoteRequestWriteDeadlineSurvivesServerWriteTimeout below.
type slowBodyReader struct {
	data      []byte
	chunkSize int
	delay     time.Duration
}

func (r *slowBodyReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	time.Sleep(r.delay)
	n := min(min(r.chunkSize, len(p)), len(r.data))
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

// newPublicServer's WriteTimeout is armed right after headers are parsed, BEFORE the handler runs, so the handler must extend the write deadline with the read deadline (handlePublicQuoteRequest);
// otherwise a slow body would finish the file write, insert and enqueue and then FAIL to write the 201, inviting a duplicate-creating retry. Uses a real connection (a Recorder can't support deadlines),
// a tiny server WriteTimeout, and a dribbled body; the 201 must arrive through the full chain (publicListenerHandler's accessLogStatusRecorder supports http.ResponseController).
func TestPublicQuoteRequestWriteDeadlineSurvivesServerWriteTimeout(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := newPublicTestConfig(t, dir)
	mux, _ := buildPublicMux(NewConfigStore(cfg), db, dir, false, false)
	handler := publicListenerHandler(mux, false)

	srv := httptest.NewUnstartedServer(handler)
	srv.Config.WriteTimeout = 200 * time.Millisecond // far below the dribbled body's total duration below
	srv.Start()
	defer srv.Close()

	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	fields := publicQuoteRequestFields("PLA")
	for _, k := range []string{"name", "email", "note", "material", "quantity", "weightG", "timeSeconds", "priceCents"} {
		if err := mw.WriteField(k, fields[k]); err != nil {
			t.Fatalf("write field %s: %v", k, err)
		}
	}
	fw, err := mw.CreateFormFile("gcode", "model.gcode")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte("G1 X1 Y1\nG1 X2 Y2\nG1 X3 Y3\n")); err != nil {
		t.Fatalf("write gcode content: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	// ~20-byte chunks at 50ms apart: for a body of a few hundred bytes this
	// dribbles for roughly a second, well past the 200ms server WriteTimeout
	// above but comfortably inside the client's own 10s timeout below.
	slow := &slowBodyReader{data: body.Bytes(), chunkSize: 20, delay: 50 * time.Millisecond}
	req, err := http.NewRequest("POST", srv.URL+"/api/public/quote-request", slow)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST with dribbled body: %v (a write-deadline regression surfaces here as a broken connection)", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body = %s", resp.StatusCode, respBody)
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name       string
		trustProxy bool
		xff        []string // one X-Forwarded-For header line each
		want       string
	}{
		{"trustProxy off ignores X-Forwarded-For", false, []string{"10.0.0.1, 203.0.113.7"}, "192.0.2.1"},
		{"trustProxy on takes the last hop", true, []string{"10.0.0.1, 203.0.113.7"}, "203.0.113.7"},
		// The common single-hop case (one trusted proxy, no other hops) is unchanged: first == last.
		{"single element", true, []string{"203.0.113.7"}, "203.0.113.7"},
		// A client's own X-Forwarded-For (the spoofed first element) must not control its rate-limit key when a trusted proxy
		// such as Cloudflare Tunnel appends the real address as the last element.
		{"multi element uses last not first", true, []string{"spoofed, real"}, "real"},
		{"whitespace around the last element is trimmed", true, []string{" 203.0.113.7 ,  10.0.0.1  "}, "10.0.0.1"},
		{"no header falls back to RemoteAddr", true, nil, "192.0.2.1"},
		// An intermediary can append the real address as a SEPARATE header line; r.Header.Get returns only the first
		// (client-controlled) line, so clientIP must consider every line (r.Header.Values).
		{"last header line wins over the first", true, []string{"spoofed-first-line", "trusted-second-line"}, "trusted-second-line"},
		// A trailing comma with nothing after it (a malformed header) is no usable value: fall back to the socket, not "".
		{"trailing separator falls back to RemoteAddr", true, []string{"1.2.3.4, "}, "192.0.2.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/", nil)
			req.RemoteAddr = "192.0.2.1:1234"
			for _, line := range tc.xff {
				req.Header.Add("X-Forwarded-For", line)
			}
			if got := clientIP(req, tc.trustProxy); got != tc.want {
				t.Errorf("clientIP(trustProxy=%v) = %q, want %q", tc.trustProxy, got, tc.want)
			}
		})
	}
}

func containsSubstr(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// The quote_requests INSERT failure branch in handlePublicQuoteRequest must respond through writeAPIError, not plain-text http.Error. A BEFORE INSERT trigger fails only the INSERT,
// and the submission carries no gcode part, so the request reaches the final-insert branch rather than writeGcodePart's.
func TestPublicQuoteRequestInsertFailureUsesWriteAPIError(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, db, cfg := newRequestsTestSetup(t, dir)

	if _, err := db.Exec(`CREATE TRIGGER block_quote_requests_insert
		BEFORE INSERT ON quote_requests
		BEGIN SELECT RAISE(ABORT, 'insert blocked for test'); END`); err != nil {
		t.Fatalf("create blocking trigger: %v", err)
	}

	material := "pla"
	for _, m := range cfg.Materials {
		if m.Public {
			material = m.Name
			break
		}
	}
	rec := serveReq(publicMux, newPublicQuoteRequest(t, publicQuoteRequestFields(material), "", nil))
	assertWriteAPIErrorShape(t, rec, http.StatusInternalServerError)
}

// writeGcodePart's generic-failure branch (distinct from storage-full and body-too-large) must also respond through writeAPIError. Pre-creating configDir/uploads as a plain file makes os.MkdirAll fail inside writeGcodePart.
func TestPublicQuoteRequestGcodeWriteFailureUsesWriteAPIError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "uploads"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("pre-create uploads as a file: %v", err)
	}
	_, publicMux, _, cfg := newRequestsTestSetup(t, dir)

	material := "pla"
	for _, m := range cfg.Materials {
		if m.Public {
			material = m.Name
			break
		}
	}
	rec := serveReq(publicMux, newPublicQuoteRequest(t, publicQuoteRequestFields(material), "model.gcode", []byte("G1\n")))
	assertWriteAPIErrorShape(t, rec, http.StatusInternalServerError)
}

// The public page charges pricing.defaultShippingCharge, never the owner's shipping cost, and the stored request
// keeps the charge the visitor saw so the invoice conversion can add the Shipping line.
func TestPublicShippingUsesChargeNotCostAndIsStored(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, db, cfg := newRequestsTestSetup(t, dir)
	cfg.Pricing.DefaultShippingCost = 4
	cfg.Pricing.DefaultShippingCharge = 6

	rec := postPublicQuotePreview(t, publicMux, validQuotePreviewBody("PLA"))
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var preview publicQuotePreviewResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.ShippingCents != 600 {
		t.Errorf("preview shippingCents = %d, want 600 (the charge, not the 400 cost)", preview.ShippingCents)
	}

	submitRec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "", nil)
	if submitRec.Code != http.StatusCreated {
		t.Fatalf("submit status = %d, body = %s", submitRec.Code, submitRec.Body.String())
	}
	var submitted struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(submitRec.Body.Bytes(), &submitted); err != nil {
		t.Fatal(err)
	}
	var stored int
	if err := db.QueryRow(`SELECT shipping_cents FROM quote_requests WHERE id = ?`, submitted.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != 600 {
		t.Errorf("stored shipping_cents = %d, want 600", stored)
	}

	// The default charge is 0, so a stock install shows and stores no shipping.
	cfg.Pricing.DefaultShippingCharge = 0
	rec = postPublicQuotePreview(t, publicMux, validQuotePreviewBody("PLA"))
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil || preview.ShippingCents != 0 {
		t.Errorf("preview shippingCents = %d (%v), want 0 when the charge is 0", preview.ShippingCents, err)
	}
}

// slowBody yields one chunk of a prepared body per Read, sleeping between reads to simulate a trickle client.
type slowBody struct {
	data  []byte
	delay time.Duration
}

func (s *slowBody) Read(p []byte) (int, error) {
	if len(s.data) == 0 {
		return 0, io.EOF
	}
	time.Sleep(s.delay)
	n := copy(p[:min(len(p), 16)], s.data)
	s.data = s.data[n:]
	return n, nil
}

func (s *slowBody) Close() error { return nil }

// A client trickling its body below the minimum rate is cut off after the grace period and releases its
// upload slot, instead of holding it for publicUploadReadDeadline.
func TestPublicQuoteRequestSlowUploadReleasesSlot(t *testing.T) {
	oldRate, oldGrace := publicUploadMinRate, publicUploadRateGrace
	publicUploadMinRate, publicUploadRateGrace = 4<<10, 100*time.Millisecond
	t.Cleanup(func() { publicUploadMinRate, publicUploadRateGrace = oldRate, oldGrace })

	dir := t.TempDir()
	db := openTestDB(t, dir)
	store := NewConfigStore(newPublicTestConfig(t, dir))
	sem := newUploadSlots(publicMaxConcurrentUploads)
	handler := handlePublicQuoteRequest(db, store, dir, newRateLimiter(), false, sem, newNotifyQueue(), false)

	full := newPublicQuoteRequest(t, publicQuoteRequestFields("PLA"), "model.gcode", bytes.Repeat([]byte("G1 X1\n"), 2000))
	raw, _ := io.ReadAll(full.Body)
	req := httptest.NewRequest("POST", "/api/public/quote-request", nil)
	req.Header.Set("Content-Type", full.Header.Get("Content-Type"))
	req.Body = &slowBody{data: raw, delay: 20 * time.Millisecond}

	start := time.Now()
	rec := serveReq(handler, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body = %s", rec.Code, rec.Body.String())
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Fatalf("slow upload held the handler for %v, want a prompt cut-off", el)
	}
	if sem.inUse() != 0 {
		t.Fatalf("upload slot still held after slow client was cut off (%d in use)", sem.inUse())
	}
}

func TestPlainEmailForMailto(t *testing.T) {
	for _, ok := range []string{"a@example.com", "first.last+tag@sub.example.co"} {
		if !plainEmailForMailto(ok) {
			t.Errorf("%q rejected, want accepted", ok)
		}
	}
	for _, bad := range []string{"a@x.com?cc=evil@x.com", "a@x.com&bcc=e@x.com", "a%0d@x.com", "a@x.com\r\nBcc: e@x.com", "a b@x.com", "a@x.com,e@x.com", "<a@x.com>"} {
		if plainEmailForMailto(bad) {
			t.Errorf("%q accepted, want rejected", bad)
		}
	}
}

func TestPlausiblePublicEmail(t *testing.T) {
	for _, ok := range []string{"a@b.c", "a@gmail.com", "first.last+tag@sub.example.co", "a@b.c.d"} {
		if !plausiblePublicEmail(ok) {
			t.Errorf("%q rejected, want accepted", ok)
		}
	}
	for _, bad := range []string{"", "jo@", "@gmail.com", "a@b", "a@.com", "a@b.", "a@.", "a b@c.d", "a@b .c", " a@b.c", "a@b.c\n", "a@@b.c", "a@b@c.d", "visitor"} {
		if plausiblePublicEmail(bad) {
			t.Errorf("%q accepted, want rejected", bad)
		}
	}
}

func TestPublicQuoteRequestRejectsMailtoInjectionEmail(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	store := NewConfigStore(newPublicTestConfig(t, dir))
	handler := handlePublicQuoteRequest(db, store, dir, newRateLimiter(), false, newUploadSlots(publicMaxConcurrentUploads), newNotifyQueue(), false)
	fields := publicQuoteRequestFields("PLA")
	fields["email"] = "a@example.com?cc=evil@example.com"
	rec := serveReq(handler, newPublicQuoteRequest(t, fields, "model.gcode", []byte("G1\n")))
	if rec.Code != http.StatusBadRequest && rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 4xx validation error, body = %s", rec.Code, rec.Body.String())
	}
}

// The stored request keeps the tax basis at submit, so a later taxRatePct change can't flip the invoice conversion.
func TestPublicQuoteRequestStoresTaxBasis(t *testing.T) {
	for _, tc := range []struct {
		taxPct float64
		want   int
	}{{0, 0}, {8, 1}} {
		dir := t.TempDir()
		_, publicMux, db, cfg := newRequestsTestSetup(t, dir)
		cfg.Pricing.TaxRatePct = tc.taxPct
		rec := postPublicQuoteRequest(t, publicMux, publicQuoteRequestFields("PLA"), "", nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("tax %v: submit status = %d, body = %s", tc.taxPct, rec.Code, rec.Body.String())
		}
		var submitted struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &submitted); err != nil {
			t.Fatal(err)
		}
		var got int
		if err := db.QueryRow(`SELECT tax_inclusive FROM quote_requests WHERE id = ?`, submitted.ID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Errorf("tax %v: tax_inclusive = %d, want %d", tc.taxPct, got, tc.want)
		}
	}
}

// Text-only submissions never take an upload slot, so they succeed while every slot is held.
func TestPublicQuoteRequestTextOnlySucceedsWhenSlotsFull(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	store := NewConfigStore(newPublicTestConfig(t, dir))
	sem := newUploadSlots(publicMaxConcurrentUploads)
	for i := range publicMaxConcurrentUploads {
		if _, ok := sem.acquire(fmt.Sprintf("198.51.100.%d", i+1)); !ok {
			t.Fatalf("pre-fill slot %d refused", i)
		}
	}
	handler := handlePublicQuoteRequest(db, store, dir, newRateLimiter(), false, sem, newNotifyQueue(), false)
	rec := serveReq(handler, newPublicQuoteRequest(t, publicQuoteRequestFields("PLA"), "", nil))
	if rec.Code != http.StatusCreated {
		t.Fatalf("text-only status = %d, want 201, body = %s", rec.Code, rec.Body.String())
	}
}

// One source never holds more than one upload slot, however many uploads it starts.
func TestUploadSlotsOnePerSource(t *testing.T) {
	sem := newUploadSlots(publicMaxConcurrentUploads)
	var rels []func()
	granted := 0
	for range 4 {
		if rel, ok := sem.acquire("203.0.113.7"); ok {
			granted++
			rels = append(rels, rel)
		}
	}
	if granted != 1 || sem.inUse() != 1 {
		t.Fatalf("granted = %d, inUse = %d, want 1 and 1", granted, sem.inUse())
	}
	if _, ok := sem.acquire("203.0.113.8"); !ok {
		t.Fatal("a different source was refused while slots were free")
	}
	rels[0]()
	rels[0]() // idempotent
	if _, ok := sem.acquire("203.0.113.7"); !ok {
		t.Fatal("source refused after releasing its slot")
	}
	if sem.inUse() != 2 {
		t.Fatalf("inUse = %d, want 2", sem.inUse())
	}
}
