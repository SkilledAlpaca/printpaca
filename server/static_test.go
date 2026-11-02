package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func staticTestFS(t *testing.T) fstest.MapFS {
	t.Helper()
	js := []byte(`console.log("printpaca");`)
	html := []byte(`<!doctype html><html><body>printpaca</body></html>`)
	return fstest.MapFS{
		"index.html":            {Data: html, ModTime: time.Unix(0, 0)},
		"assets/main-abc123.js": {Data: js, ModTime: time.Unix(0, 0)},
		"assets/main-abc123.js.gz": {
			Data:    gzipBytes(t, js),
			ModTime: time.Unix(0, 0),
		},
		// No index.html.gz: exercises the "no sibling" fallback path.
	}
}

func TestStaticHandlerServesGzipVariantWhenAccepted(t *testing.T) {
	h := newStaticHandler(staticTestFS(t))

	req := httptest.NewRequest(http.MethodGet, "/assets/main-abc123.js", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate, br")
	rec := serveReq(h, req)

	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Encoding"); got != "gzip" {
		t.Errorf("Content-Encoding = %q, want %q", got, "gzip")
	}
	if got := res.Header.Get("Vary"); got != "Accept-Encoding" {
		t.Errorf("Vary = %q, want %q", got, "Accept-Encoding")
	}
	if got := res.Header.Get("Content-Type"); got != "text/javascript; charset=utf-8" {
		t.Errorf("Content-Type = %q, want the original .js type, not .gz's", got)
	}
	if got := res.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control = %q, want the immutable assets/ policy", got)
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	gr, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("response body is not valid gzip: %v", err)
	}
	decoded, err := io.ReadAll(gr)
	if err != nil {
		t.Fatalf("decode gzip body: %v", err)
	}
	if string(decoded) != `console.log("printpaca");` {
		t.Errorf("decoded body = %q, want the original JS source", decoded)
	}
}

func TestStaticHandlerFallsBackWithoutAcceptEncoding(t *testing.T) {
	h := newStaticHandler(staticTestFS(t))

	req := httptest.NewRequest(http.MethodGet, "/assets/main-abc123.js", nil)
	rec := serveReq(h, req)

	res := rec.Result()
	if got := res.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want unset when the client sent no Accept-Encoding", got)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != `console.log("printpaca");` {
		t.Errorf("body = %q, want the uncompressed JS source verbatim", body)
	}
}

func TestStaticHandlerFallsBackWhenGzipExplicitlyRejected(t *testing.T) {
	h := newStaticHandler(staticTestFS(t))

	req := httptest.NewRequest(http.MethodGet, "/assets/main-abc123.js", nil)
	req.Header.Set("Accept-Encoding", "gzip;q=0, identity")
	rec := serveReq(h, req)

	res := rec.Result()
	if got := res.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want unset when the client sent gzip;q=0", got)
	}
}

func TestStaticHandlerFallsBackWithoutGzipSibling(t *testing.T) {
	h := newStaticHandler(staticTestFS(t))

	// index.html has no .gz sibling in staticTestFS: must still serve the
	// plain file successfully rather than erroring or 404ing.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := serveReq(h, req)

	res := rec.Result()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want unset: no index.html.gz sibling exists", got)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want the no-cache top-level-file policy", got)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != `<!doctype html><html><body>printpaca</body></html>` {
		t.Errorf("body = %q, want the uncompressed index.html verbatim", body)
	}
}

// A 404 under /assets/ must not carry the year-long immutable Cache-Control (newStaticHandler must check the file exists first): a client or shared cache would keep a rollback-removed hash broken for a year.
// A hit keeps the immutable header; a miss gets a no-store (or similarly short) directive.
func TestStaticHandlerMissingAssetHasNoImmutableCacheControl(t *testing.T) {
	h := newStaticHandler(staticTestFS(t))

	rec := serveReq(h, httptest.NewRequest(http.MethodGet, "/assets/does-not-exist.js", nil))

	res := rec.Result()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", res.StatusCode)
	}
	cc := res.Header.Get("Cache-Control")
	if strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control on missing /assets/ file = %q, must not contain immutable", cc)
	}
	if cc != "no-store" {
		t.Errorf("Cache-Control on missing /assets/ file = %q, want no-store (or another short-lived directive)", cc)
	}

	// A real asset must still get the immutable header: this isn't a
	// blanket removal of caching under /assets/, only a fix for the miss
	// case.
	rec2 := serveReq(h, httptest.NewRequest(http.MethodGet, "/assets/main-abc123.js", nil))
	res2 := rec2.Result()
	if res2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res2.StatusCode)
	}
	if got := res2.Header.Get("Cache-Control"); got != "public, max-age=31536000, immutable" {
		t.Errorf("Cache-Control on existing asset = %q, want the immutable assets/ policy", got)
	}
}

// TestStaticServesIndex and TestStaticIndexNoCache exercise newStaticHandler through the real route table (newTestMux) rather than a synthetic fstest.MapFS; they pin static.go's behavior.
func TestStaticServesIndex(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := serveReq(mux, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "printpaca") {
		t.Fatalf("index not served: %d", rec.Code)
	}
}

func TestStaticIndexNoCache(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := serveReq(mux, httptest.NewRequest("GET", "/", nil))
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", cc)
	}
}

func TestAcceptsGzip(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{"", false},
		{"gzip", true},
		{"gzip, deflate, br", true},
		{"deflate, br", false},
		{"*", true},
		{"gzip;q=0", false},
		{"gzip;q=0, identity", false},
		{"gzip;q=0.5", true},
		{" gzip ", true},
	}
	for _, c := range cases {
		if got := acceptsGzip(c.header); got != c.want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", c.header, got, c.want)
		}
	}
}
