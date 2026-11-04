package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShopMaxOrderQtyDefaultsTo100(t *testing.T) {
	cfg := loadTestConfig(t, t.TempDir())
	if cfg.Shop.MaxOrderQty != 100 {
		t.Fatalf("shop.maxOrderQty = %d, want 100", cfg.Shop.MaxOrderQty)
	}
}

func TestShopMaxOrderQtyValidation(t *testing.T) {
	for _, bad := range []string{"0", "-5", "10001"} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("shop:\n  maxOrderQty: "+bad+"\n"), 0o644)
		if _, err := LoadConfig(dir); err == nil || !strings.Contains(err.Error(), "shop.maxOrderQty") {
			t.Errorf("maxOrderQty %s: want error naming shop.maxOrderQty, got %v", bad, err)
		}
	}
	for _, ok := range []string{"1", "10000"} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("shop:\n  maxOrderQty: "+ok+"\n"), 0o644)
		if _, err := LoadConfig(dir); err != nil {
			t.Errorf("maxOrderQty %s: %v", ok, err)
		}
	}
}

// The public config, the preview and the request all read shop.maxOrderQty, so raising it admits a quantity
// the default would reject, and lowering it rejects one the default admits.
func TestShopMaxOrderQtyDrivesPublicValidation(t *testing.T) {
	dir := t.TempDir()
	_, publicMux, _, cfg := newRequestsTestSetup(t, dir)
	cfg.Shop.MaxOrderQty = 5

	rec := httptest.NewRecorder()
	publicMux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/public/config", nil))
	var pc struct {
		MaxOrderQty int `json:"maxOrderQty"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &pc); err != nil || pc.MaxOrderQty != 5 {
		t.Fatalf("public config maxOrderQty = %d (err %v), want 5", pc.MaxOrderQty, err)
	}

	body := validQuotePreviewBody("PLA")
	body["quantity"] = 6
	if rec := postPublicQuotePreview(t, publicMux, body); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "1-5") {
		t.Errorf("preview qty 6 = %d %s, want 422 naming 1-5", rec.Code, rec.Body.String())
	}
	body["quantity"] = 5
	if rec := postPublicQuotePreview(t, publicMux, body); rec.Code != http.StatusOK {
		t.Errorf("preview qty 5 = %d %s, want 200", rec.Code, rec.Body.String())
	}

	fields := publicQuoteRequestFields("PLA")
	fields["quantity"] = "6"
	rec = postPublicQuoteRequest(t, publicMux, fields, "model.gcode", []byte("G1 X1\n"))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "1-5") {
		t.Errorf("request qty 6 = %d %s, want 422 naming 1-5", rec.Code, rec.Body.String())
	}

	cfg.Shop.MaxOrderQty = 500
	body["quantity"] = 400
	if rec := postPublicQuotePreview(t, publicMux, body); rec.Code != http.StatusOK {
		t.Errorf("preview qty 400 with cap 500 = %d %s, want 200", rec.Code, rec.Body.String())
	}
}
