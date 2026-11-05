package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// decodeGzip decompresses a gzip member and fails the test on error so a corrupt stream fails loudly.
func decodeGzip(t *testing.T, data []byte) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gz.Close()
	out, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read gzip stream: %v", err)
	}
	return out
}

// Table of single-request scenarios run directly against compressMiddleware, each wrapping a small handler
// shaped like a real one; check asserts status, Content-Encoding, Vary, Content-Length, and body.
func TestCompressMiddleware(t *testing.T) {
	// bigJSON is over compressMinSize and repetitive, so it also proves compression actually shrinks it.
	bigJSON := []byte(`{"items":[` + strings.Repeat(`{"name":"Benchy","priceCents":1500},`, 80) + `{"name":"last"}]}`)
	if len(bigJSON) < compressMinSize {
		t.Fatalf("test setup bug: bigJSON is %d bytes, want > compressMinSize (%d)", len(bigJSON), compressMinSize)
	}
	smallJSON := []byte(`{"ok":true}`)
	bigImage := bytes.Repeat([]byte{0x89, 'P', 'N', 'G', 0, 0, 0, 0}, 200) // 1600 bytes, not text/JSON

	cases := []struct {
		name           string
		method         string
		acceptEncoding string
		handler        http.HandlerFunc
		check          func(t *testing.T, rec *httptest.ResponseRecorder)
	}{
		{
			name:           "JSON over threshold with Accept-Encoding gzip is compressed",
			method:         "GET",
			acceptEncoding: "gzip",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				// A handler that pre-sets Content-Length (as http.ServeContent might) proves startGzip deletes it.
				w.Header().Set("Content-Length", strconv.Itoa(len(bigJSON)))
				w.Write(bigJSON)
			},
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200", rec.Code)
				}
				if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
					t.Fatalf("Content-Encoding = %q, want gzip", enc)
				}
				if vary := rec.Header().Get("Vary"); !headerListContains(vary, "Accept-Encoding") {
					t.Errorf("Vary = %q, want it to include Accept-Encoding", vary)
				}
				if cl := rec.Header().Get("Content-Length"); cl != "" {
					t.Errorf("Content-Length = %q, want empty (compressed length is unknown at header time, and must never mismatch the actual gzip body)", cl)
				}
				got := decodeGzip(t, rec.Body.Bytes())
				if !bytes.Equal(got, bigJSON) {
					t.Errorf("decompressed body does not match original\n got: %s\nwant: %s", got, bigJSON)
				}
				if rec.Body.Len() >= len(bigJSON) {
					t.Errorf("compressed body (%d bytes) is not smaller than the original (%d bytes)", rec.Body.Len(), len(bigJSON))
				}
			},
		},
		{
			name:           "no Accept-Encoding stays identity",
			method:         "GET",
			acceptEncoding: "",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write(bigJSON)
			},
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				if enc := rec.Header().Get("Content-Encoding"); enc != "" {
					t.Errorf("Content-Encoding = %q, want empty: client sent no Accept-Encoding", enc)
				}
				if !bytes.Equal(rec.Body.Bytes(), bigJSON) {
					t.Errorf("identity body does not match original")
				}
				// A client with no Accept-Encoding is ineligible from the first Write, so the response passes through
				// unbuffered and whatever Content-Length the handler set (here, none) is left untouched.
				if cl := rec.Header().Get("Content-Length"); cl != "" {
					t.Errorf("Content-Length = %q, want empty (passthrough never sets it)", cl)
				}
			},
		},
		{
			name:           "small response stays identity even with Accept-Encoding gzip",
			method:         "GET",
			acceptEncoding: "gzip",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write(smallJSON)
			},
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				if enc := rec.Header().Get("Content-Encoding"); enc != "" {
					t.Errorf("Content-Encoding = %q, want empty: body is under compressMinSize", enc)
				}
				if !bytes.Equal(rec.Body.Bytes(), smallJSON) {
					t.Errorf("identity body = %q, want %q", rec.Body.Bytes(), smallJSON)
				}
				if cl := rec.Header().Get("Content-Length"); cl != strconv.Itoa(len(smallJSON)) {
					t.Errorf("Content-Length = %q, want %d", cl, len(smallJSON))
				}
			},
		},
		{
			name:           "non-compressible Content-Type stays identity",
			method:         "GET",
			acceptEncoding: "gzip",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "image/png")
				w.Write(bigImage)
			},
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				if enc := rec.Header().Get("Content-Encoding"); enc != "" {
					t.Errorf("Content-Encoding = %q, want empty: image/png is not in compressibleTypes", enc)
				}
				if !bytes.Equal(rec.Body.Bytes(), bigImage) {
					t.Errorf("identity body does not match original image bytes")
				}
			},
		},
		{
			name:           "handler-set Content-Encoding is left untouched",
			method:         "GET",
			acceptEncoding: "gzip",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Content-Encoding", "br") // pretend a handler already encoded its own body
				w.Write(bigJSON)
			},
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				if enc := rec.Header().Get("Content-Encoding"); enc != "br" {
					t.Errorf("Content-Encoding = %q, want br (untouched, never double-encoded)", enc)
				}
				if !bytes.Equal(rec.Body.Bytes(), bigJSON) {
					t.Errorf("body was modified even though Content-Encoding was already set")
				}
			},
		},
		{
			name:           "404 status is preserved",
			method:         "GET",
			acceptEncoding: "gzip",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				w.Write(smallJSON)
			},
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				if rec.Code != http.StatusNotFound {
					t.Errorf("status = %d, want 404", rec.Code)
				}
				if !bytes.Equal(rec.Body.Bytes(), smallJSON) {
					t.Errorf("body = %q, want %q", rec.Body.Bytes(), smallJSON)
				}
			},
		},
		{
			name:           "422 status is preserved",
			method:         "GET",
			acceptEncoding: "gzip",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnprocessableEntity)
				w.Write(smallJSON)
			},
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				if rec.Code != http.StatusUnprocessableEntity {
					t.Errorf("status = %d, want 422", rec.Code)
				}
			},
		},
		{
			name:           "HEAD request gets no body",
			method:         "HEAD",
			acceptEncoding: "gzip",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write(bigJSON) // a real handler wouldn't normally do this for HEAD, but nothing here should leak it either way
			},
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				if rec.Code != http.StatusOK {
					t.Errorf("status = %d, want 200", rec.Code)
				}
				if rec.Body.Len() != 0 {
					t.Errorf("HEAD response body = %d bytes, want 0", rec.Body.Len())
				}
				if enc := rec.Header().Get("Content-Encoding"); enc != "" {
					t.Errorf("Content-Encoding = %q, want empty for a HEAD response", enc)
				}
			},
		},
		{
			name:           "204 No Content is not wrapped",
			method:         "GET",
			acceptEncoding: "gzip",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			},
			check: func(t *testing.T, rec *httptest.ResponseRecorder) {
				if rec.Code != http.StatusNoContent {
					t.Errorf("status = %d, want 204", rec.Code)
				}
				if rec.Body.Len() != 0 {
					t.Errorf("204 response body = %d bytes, want 0", rec.Body.Len())
				}
				if enc := rec.Header().Get("Content-Encoding"); enc != "" {
					t.Errorf("Content-Encoding = %q, want empty for 204", enc)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := compressMiddleware(tc.handler)
			req := httptest.NewRequest(tc.method, "/probe", nil)
			if tc.acceptEncoding != "" {
				req.Header.Set("Accept-Encoding", tc.acceptEncoding)
			}
			rec := serveReq(handler, req)
			tc.check(t, rec)
		})
	}
}

// /api/spoolman/ and /api/moonraker/ bypass compressMiddleware entirely (the writer is never wrapped):
// reverse-proxied responses stream, and buffering them would defeat that.
func TestCompressMiddlewareSkipsProxyPrefixes(t *testing.T) {
	paths := []string{"/api/spoolman/api/v1/spool", "/api/moonraker/server/info"}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			body := strings.Repeat("x", compressMinSize*2)
			handler := compressMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(body))
			}))
			req := httptest.NewRequest("GET", path, nil)
			req.Header.Set("Accept-Encoding", "gzip")
			rec := serveReq(handler, req)

			if enc := rec.Header().Get("Content-Encoding"); enc != "" {
				t.Errorf("Content-Encoding = %q, want empty: excluded prefixes must never be compressed", enc)
			}
			if vary := rec.Header().Get("Vary"); vary != "" {
				t.Errorf("Vary = %q, want empty: compressMiddleware must not touch excluded-prefix responses at all", vary)
			}
			if rec.Body.String() != body {
				t.Errorf("body was altered for an excluded-prefix path")
			}
		})
	}
}

// Drives GET /api/public/items through publicListenerHandler, the production chain, proving compressMiddleware
// works end-to-end with accessLogMiddleware, securityHeadersTLSMiddleware, and noStoreAPIMiddleware.
func TestPublicListenerCompressesRealEndpoint(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, _, _ := newRequestsTestSetup(t, dir)

	// A handful of items with a repeated long description, comfortably
	// over compressMinSize once serialized together and public so they
	// surface on GET /api/public/items.
	longDescription := strings.Repeat("Layer-adhesion tuned PLA benchy print. ", 30)
	for i := range 3 {
		body := fmt.Sprintf(`{"name":"Item %d","description":%q,"priceCents":1500,"public":true,"sortOrder":%d}`, i, longDescription, i)
		rec := postItem(t, adminMux, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("seed item %d: want 201, got %d %s", i, rec.Code, rec.Body.String())
		}
	}

	handler := publicListenerHandler(publicMux, false)

	gzReq := httptest.NewRequest("GET", "/api/public/items", nil)
	gzReq.Header.Set("Accept-Encoding", "gzip")
	gzRec := serveReq(handler, gzReq)
	if gzRec.Code != http.StatusOK {
		t.Fatalf("gzip request: want 200, got %d %s", gzRec.Code, gzRec.Body.String())
	}
	if enc := gzRec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip (response was %d bytes on the wire)", enc, gzRec.Body.Len())
	}
	if vary := gzRec.Header().Get("Vary"); !headerListContains(vary, "Accept-Encoding") {
		t.Errorf("Vary = %q, want it to include Accept-Encoding", vary)
	}
	decompressed := decodeGzip(t, gzRec.Body.Bytes())

	idReq := httptest.NewRequest("GET", "/api/public/items", nil) // no Accept-Encoding: identity baseline
	idRec := serveReq(handler, idReq)
	if idRec.Code != http.StatusOK {
		t.Fatalf("identity request: want 200, got %d %s", idRec.Code, idRec.Body.String())
	}
	if enc := idRec.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("identity request Content-Encoding = %q, want empty", enc)
	}

	var gzResp, idResp publicItemsResponse
	if err := json.Unmarshal(decompressed, &gzResp); err != nil {
		t.Fatalf("unmarshal decompressed gzip body: %v\nbody: %s", err, decompressed)
	}
	if err := json.Unmarshal(idRec.Body.Bytes(), &idResp); err != nil {
		t.Fatalf("unmarshal identity body: %v", err)
	}
	if len(gzResp.Items) != 3 || len(idResp.Items) != 3 {
		t.Fatalf("want 3 items in both responses, got gzip=%d identity=%d", len(gzResp.Items), len(idResp.Items))
	}
	if !reflect.DeepEqual(gzResp, idResp) {
		t.Errorf("gzip-decompressed response differs from the identity response:\ngzip:     %+v\nidentity: %+v", gzResp, idResp)
	}
}

// A Flush while the writer is still undecided (eligible body under compressMinSize) forces the compression decision:
// the first chunk must already be on the wire, gzip-framed, before the second write, and the stream must decompress to the full body.
func TestCompressMiddlewareFlushDrainsUndecidedBuffer(t *testing.T) {
	first := strings.Repeat("a", 100) // well under compressMinSize
	second := strings.Repeat("b", 100)
	var bytesOnWireAtFlush int

	handler := compressMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, first)
		w.(http.Flusher).Flush()
		if rec, ok := w.(*compressResponseWriter).ResponseWriter.(*httptest.ResponseRecorder); ok {
			bytesOnWireAtFlush = rec.Body.Len()
		}
		io.WriteString(w, second)
	}))

	req := httptest.NewRequest("GET", "/api/anything", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := serveReq(handler, req)

	if !rec.Flushed {
		t.Fatal("underlying writer was never flushed")
	}
	if bytesOnWireAtFlush == 0 {
		t.Fatal("first chunk was still buffered after Flush; want it drained to the client")
	}
	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip (flush on an eligible response starts compression)", enc)
	}
	if got := string(decodeGzip(t, rec.Body.Bytes())); got != first+second {
		t.Fatalf("decompressed body mismatch: got %d bytes, want %d", len(got), len(first+second))
	}
}

// TestCompressMiddlewareFlushIneligiblePassesThrough is the identity twin:
// flushing an ineligible response (client without Accept-Encoding) drains
// the buffered prefix uncompressed.
func TestCompressMiddlewareFlushIneligiblePassesThrough(t *testing.T) {
	handler := compressMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, "hello")
		w.(http.Flusher).Flush()
		io.WriteString(w, " world")
	}))

	req := httptest.NewRequest("GET", "/api/anything", nil) // no Accept-Encoding
	rec := serveReq(handler, req)

	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Fatalf("Content-Encoding = %q, want empty", enc)
	}
	if rec.Body.String() != "hello world" {
		t.Fatalf("body = %q, want %q", rec.Body.String(), "hello world")
	}
}
