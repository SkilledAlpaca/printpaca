package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// D53: the shop HTML sets data-theme on <html> from the pp_theme cookie, so the first paint matches the visitor's
// choice without an inline script. Only "light" and "dark" count; anything else is the same as no cookie.
func TestShopHTMLThemeFromCookie(t *testing.T) {
	sub := fstest.MapFS{
		"public.html":          {Data: []byte(`<!doctype html><html lang="en-US"><head><title>x</title></head><body></body></html>`)},
		"public-manifest.json": {Data: []byte(`["public.html"]`)},
	}
	handler := publicIndexHandler(sub, stubShopConfig)
	cases := []struct {
		name   string
		cookie string // empty: no cookie at all
		want   string // expected data-theme value, empty for none
	}{
		{"light", "pp_theme=light", "light"},
		{"dark", "pp_theme=dark", "dark"},
		{"missing", "", ""},
		{"garbage", "pp_theme=solarized", ""},
		{"wrong case", "pp_theme=Dark", ""},
		{"empty value", "pp_theme=", ""},
		{"markup", `pp_theme="><script>alert(1)</script>`, ""},
		{"other cookie only", "theme=dark", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			if tc.cookie != "" {
				req.Header.Set("Cookie", tc.cookie)
			}
			rec := serveReq(handler, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET / = %d", rec.Code)
			}
			body := rec.Body.String()
			if tc.want == "" {
				if strings.Contains(body, "data-theme") {
					t.Errorf("unexpected data-theme in %s", body)
				}
			} else if !strings.Contains(body, `<html data-theme="`+tc.want+`" lang="en-US">`) {
				t.Errorf("want data-theme=%q on <html> in %s", tc.want, body)
			}
			if strings.Contains(body, "alert(1)") {
				t.Error("cookie value leaked into the page")
			}
			if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
				t.Errorf("Cache-Control = %q, want no-cache", cc)
			}
		})
	}
}

// The attribute is written from the two literals, never from the request, and only on <html>.
func TestRenderShopHTMLThemeEscaping(t *testing.T) {
	const tmpl = `<html lang="en"><head><title>x</title></head><body data-x="<html>"></body></html>`
	for _, theme := range []string{`dark" onload="x`, "<script>", " dark", "dark\n"} {
		out, ok := renderShopHTML(tmpl, publicConfigResponse{}, theme)
		if !ok {
			t.Fatal("render failed")
		}
		if strings.Contains(string(out), "data-theme") || strings.Contains(string(out), theme) {
			t.Errorf("theme %q reached the page: %s", theme, out)
		}
	}
	out, _ := renderShopHTML(tmpl, publicConfigResponse{}, "dark")
	if strings.Count(string(out), "data-theme") != 1 {
		t.Errorf("want exactly one data-theme: %s", out)
	}
}
