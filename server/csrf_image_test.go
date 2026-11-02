package main

import (
	"bytes"
	"encoding/json"
	"image/color"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fullChainTestMux wraps the registerRoutes table in auth.middleware as main.go does. Other resource tests use the bare
// mux, which never runs csrfGuard, so a csrfGuard-only bug is invisible to them. csrfGuard runs even with auth disabled.
func fullChainTestMux(t *testing.T, dir string) http.Handler {
	t.Helper()
	mux := newTestMux(t, dir)
	auth := newAuthState("", nil)
	return auth.middleware(mux)
}

// Through the full chain, an image upload with Content-Type image/png must succeed and set the item's imageFile;
// csrfGuard's blanket JSON-only rule once 415'd it, which newTestMux-based tests could not see.
func TestCSRFGuardAllowsItemImageUploadWithImageContentType(t *testing.T) {
	dir := t.TempDir()
	handler := fullChainTestMux(t, dir)

	createReq := httptest.NewRequest("POST", "/api/items", strings.NewReader(`{"name":"Benchy","priceCents":1500}`))
	createReq.Host = "localhost"
	createReq.Header.Set("Content-Type", "application/json")
	rec := serveReq(handler, createReq)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create item: want 201, got %d %s", rec.Code, rec.Body.String())
	}
	var created Item
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created item: %v", err)
	}

	png := testPNG(t, color.RGBA{255, 0, 0, 255})
	req := httptest.NewRequest("POST", "/api/items/"+created.ID+"/image", bytes.NewReader(png))
	req.Host = "localhost"
	req.Header.Set("Content-Type", "image/png")
	rec = serveReq(handler, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("image upload through full chain: want 200, got %d %s", rec.Code, rec.Body.String())
	}
	var updated Item
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal upload response: %v", err)
	}
	if updated.ImageFile == "" {
		t.Fatalf("uploaded item has no imageFile set: %+v", updated)
	}
}

// text/plain (the non-preflighted CSRF shape) and a missing Content-Type must each 415 with the image-specific message.
func TestCSRFGuardRejectsItemImageUploadWithWrongContentType(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
		setHeader   bool
	}{
		{"text/plain", "text/plain", true},
		{"no Content-Type", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			handler := fullChainTestMux(t, dir)

			createReq := httptest.NewRequest("POST", "/api/items", strings.NewReader(`{"name":"Benchy","priceCents":1500}`))
			createReq.Host = "localhost"
			createReq.Header.Set("Content-Type", "application/json")
			rec := serveReq(handler, createReq)
			if rec.Code != http.StatusCreated {
				t.Fatalf("create item: want 201, got %d %s", rec.Code, rec.Body.String())
			}
			var created Item
			if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
				t.Fatalf("unmarshal created item: %v", err)
			}

			png := testPNG(t, color.RGBA{0, 255, 0, 255})
			req := httptest.NewRequest("POST", "/api/items/"+created.ID+"/image", bytes.NewReader(png))
			req.Host = "localhost"
			if c.setHeader {
				req.Header.Set("Content-Type", c.contentType)
			}
			rec = serveReq(handler, req)
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("want 415, got %d %s", rec.Code, rec.Body.String())
			}
			var body authErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v", err)
			}
			const want = "Content-Type must be image/jpeg, image/png, or image/webp"
			if body.Error != want {
				t.Fatalf("error = %q, want %q", body.Error, want)
			}
		})
	}
}

// The image route's allowlist must not change the JSON rule elsewhere: POST /api/items still succeeds with
// application/json and 415s with text/plain.
func TestCSRFGuardJSONRuleUnaffectedByImageRoute(t *testing.T) {
	dir := t.TempDir()
	handler := fullChainTestMux(t, dir)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/items", strings.NewReader(`{"name":"Benchy","priceCents":1500}`))
	req.Host = "localhost"
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("application/json: want 201, got %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest("POST", "/api/items", strings.NewReader(`{"name":"Benchy","priceCents":1500}`))
	req.Host = "localhost"
	req.Header.Set("Content-Type", "text/plain")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain: want 415, got %d %s", rec.Code, rec.Body.String())
	}
	var body authErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	const want = "Content-Type must be application/json"
	if body.Error != want {
		t.Fatalf("error = %q, want %q", body.Error, want)
	}
}
