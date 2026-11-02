package main

import (
	"encoding/json"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// GET /item-images/{file}: a successful fetch (right Content-Type, long-lived Cache-Control), the name-regexp gate rejecting
// traversal and unknown filenames, and the file disappearing once its item is deleted.
func TestItemImageServe(t *testing.T) {
	dir := t.TempDir()
	mux := newTestMux(t, dir)

	rec := postItem(t, mux, `{"name":"Benchy","priceCents":1500}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d %s", rec.Code, rec.Body.String())
	}
	var created Item
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created: %v", err)
	}

	rec = uploadItemImage(t, mux, created.ID, testPNG(t, color.RGBA{10, 20, 30, 255}))
	if rec.Code != http.StatusOK {
		t.Fatalf("image upload want 200, got %d %s", rec.Code, rec.Body.String())
	}
	var uploaded Item
	if err := json.Unmarshal(rec.Body.Bytes(), &uploaded); err != nil {
		t.Fatalf("unmarshal uploaded: %v", err)
	}

	rec = serveReq(mux, httptest.NewRequest("GET", "/item-images/"+uploaded.ImageFile, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /item-images/%s want 200, got %d %s", uploaded.ImageFile, rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=31536000, immutable" {
		t.Fatalf("Cache-Control = %q, want public, max-age=31536000, immutable", cc)
	}

	// A well-formed but nonexistent filename 404s; the handler must os.Stat before setting the immutable Cache-Control, or the 404 would be cached for a year.
	rec = serveReq(mux, httptest.NewRequest("GET", "/item-images/nope.png", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /item-images/nope.png want 404, got %d", rec.Code)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "" {
		t.Fatalf("Cache-Control on a missing-file 404 = %q, want unset", cc)
	}

	// Traversal is rejected by the name regexp before http.ServeFile. Call the handler directly with an injected PathValue:
	// http.ServeMux would redirect an unclean "../.." path first and test routing instead of the gate.
	req := httptest.NewRequest("GET", "/item-images/x", nil)
	req.SetPathValue("file", "../../etc/passwd")
	rec = httptest.NewRecorder()
	itemImageHandler(dir)(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("traversal attempt want 404, got %d", rec.Code)
	}

	path := filepath.Join(dir, "uploads", "items", uploaded.ImageFile)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("uploaded file missing before delete: %v", err)
	}
	rec = deleteItem(t, mux, created.ID)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete want 204, got %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("image file still present after item delete (err = %v)", err)
	}
}
