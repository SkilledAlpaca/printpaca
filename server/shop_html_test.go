package main

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

// stubShopConfig is the config source for publicIndexHandler tests that don't care about the head.
func stubShopConfig() publicConfigResponse { return publicConfigResponse{} }

const shopHTMLTemplate = `<!doctype html><html><head><meta charset="utf-8"><title>Neutral</title><script type="module" src="/assets/public-abc.js"></script></head><body><div id="app"></div></body></html>`

func shopHTMLFS() fstest.MapFS {
	return fstest.MapFS{
		"public.html":          {Data: []byte(shopHTMLTemplate)},
		"assets/public-abc.js": {Data: []byte("console.log('public')")},
		"public-manifest.json": {Data: []byte(`["public.html", "assets/public-abc.js"]`)},
	}
}

var shopConfigBlock = regexp.MustCompile(`<script type="application/json" id="shop-config">(.*?)</script>`)

// shopHTMLHandler is the shop HTML behind the real public listener chain (security headers, gzip), reading a
// live ConfigStore.
func shopHTMLHandler(t *testing.T, cfg *Config) http.Handler {
	t.Helper()
	store := NewConfigStore(cfg)
	h := publicIndexHandler(shopHTMLFS(), func() publicConfigResponse {
		return buildPublicConfig(store.Get(), false, publicPortStaffURL)
	})
	return publicListenerHandler(h, false)
}

func getShopHTML(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	return serveReq(h, httptest.NewRequest("GET", path, nil))
}

func TestShopTitleFallbackOrder(t *testing.T) {
	var c Config
	if got := c.shopTitle(); got != "Shop" {
		t.Errorf("empty config title = %q, want Shop", got)
	}
	c.Quote.BusinessName = "Alpaca Prints"
	if got := c.shopTitle(); got != "Alpaca Prints" {
		t.Errorf("business name title = %q", got)
	}
	c.Shop.PageTitle = "Alpaca Prints | 3D prints"
	if got := c.shopTitle(); got != "Alpaca Prints | 3D prints" {
		t.Errorf("page title = %q", got)
	}
	c.Shop.PageTitle = "   "
	if got := c.shopTitle(); got != "Alpaca Prints" {
		t.Errorf("blank page title should fall back, got %q", got)
	}
}

func TestShopPageTitleTrimmedOnNormalize(t *testing.T) {
	cfg := loadTestConfig(t, t.TempDir())
	cfg.Shop.PageTitle = "  Hello  "
	normalizeConfig(cfg)
	if cfg.Shop.PageTitle != "Hello" {
		t.Errorf("PageTitle = %q, want trimmed", cfg.Shop.PageTitle)
	}
}

func TestShopHTMLRendersHeadFromSettings(t *testing.T) {
	cfg := newPublicTestConfig(t, t.TempDir())
	cfg.Quote.BusinessName = "Alpaca Prints"
	cfg.PublicQuotes.IntroText = "Prints made to order."
	cfg.Shop.PageTitle = "Alpaca Prints · Shop"
	h := shopHTMLHandler(t, cfg)

	for _, path := range []string{"/", "/order"} {
		rec := getShopHTML(t, h, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "<title>Alpaca Prints · Shop</title>") {
			t.Errorf("%s: title not rendered: %s", path, body)
		}
		if strings.Contains(body, "Neutral") {
			t.Errorf("%s: template title leaked", path)
		}
		if !strings.Contains(body, `<meta name="description" content="Prints made to order.">`) {
			t.Errorf("%s: description meta missing: %s", path, body)
		}
		if !strings.Contains(body, `src="/assets/public-abc.js"`) {
			t.Errorf("%s: template body lost", path)
		}
		if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Errorf("Content-Type = %q", got)
		}
	}
}

func TestShopHTMLTitleFallsBackToBusinessNameThenShop(t *testing.T) {
	cfg := newPublicTestConfig(t, t.TempDir())
	cfg.Quote.BusinessName = "Alpaca Prints"
	body := getShopHTML(t, shopHTMLHandler(t, cfg), "/").Body.String()
	if !strings.Contains(body, "<title>Alpaca Prints</title>") {
		t.Errorf("want business name title: %s", body)
	}
	if strings.Contains(body, `name="description"`) && cfg.PublicQuotes.IntroText == "" {
		t.Errorf("description meta rendered without an intro")
	}

	cfg2 := newPublicTestConfig(t, t.TempDir())
	cfg2.Quote.BusinessName = ""
	body = getShopHTML(t, shopHTMLHandler(t, cfg2), "/").Body.String()
	if !strings.Contains(body, "<title>Shop</title>") {
		t.Errorf("want Shop title: %s", body)
	}
}

func TestShopHTMLEmbedsExactlyThePublicConfig(t *testing.T) {
	dir := t.TempDir()
	cfg := newPublicTestConfig(t, dir)
	cfg.Quote.BusinessName = "Alpaca Prints"
	cfg.Shop.Heading = "Hand made"
	store := NewConfigStore(cfg)
	h := publicListenerHandler(publicIndexHandler(shopHTMLFS(), func() publicConfigResponse {
		return buildPublicConfig(store.Get(), false, publicPortStaffURL)
	}), false)

	m := shopConfigBlock.FindStringSubmatch(getShopHTML(t, h, "/").Body.String())
	if m == nil {
		t.Fatal("no shop-config block")
	}
	var embedded any
	if err := json.Unmarshal([]byte(m[1]), &embedded); err != nil {
		t.Fatalf("embedded config is not JSON: %v", err)
	}

	api := httptest.NewRecorder()
	handlePublicConfig(store, false, publicPortStaffURL)(api, httptest.NewRequest("GET", "/api/public/config", nil))
	var fromAPI any
	if err := json.Unmarshal(api.Body.Bytes(), &fromAPI); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(embedded, fromAPI) {
		t.Errorf("embedded config differs from /api/public/config\nembedded: %v\napi:      %v", embedded, fromAPI)
	}
	// The embedded config carries the resolved title the page composes its tab title from.
	shop := embedded.(map[string]any)["shop"].(map[string]any)
	if shop["pageTitle"] != "Alpaca Prints" {
		t.Errorf("shop.pageTitle = %v", shop["pageTitle"])
	}
}

func TestShopHTMLEscapesHostileValues(t *testing.T) {
	cfg := newPublicTestConfig(t, t.TempDir())
	cfg.Quote.BusinessName = `Evil </script><script>alert(1)</script> & <!-- Co`
	cfg.PublicQuotes.IntroText = `Say "hi" </title><b>&`
	cfg.Shop.PageTitle = `A </title><script>x</script> "q" & b`
	body := getShopHTML(t, shopHTMLHandler(t, cfg), "/").Body.String()

	if strings.Contains(body, "<script>alert(1)") || strings.Contains(body, "<script>x</script>") {
		t.Errorf("hostile script reached the page: %s", body)
	}
	if strings.Count(body, "</script>") != 2 { // the module script and the config block
		t.Errorf("config block was closed early: %s", body)
	}
	if strings.Contains(body, "<!-- Co") || strings.Contains(body, "</title><b>") {
		t.Errorf("markup not escaped: %s", body)
	}
	wantTitle := "<title>" + html.EscapeString(cfg.Shop.PageTitle) + "</title>"
	if !strings.Contains(body, wantTitle) {
		t.Errorf("title not HTML-escaped, want %s in %s", wantTitle, body)
	}
	if !strings.Contains(body, `content="`+html.EscapeString(cfg.PublicQuotes.IntroText)+`"`) {
		t.Errorf("description not HTML-escaped: %s", body)
	}
	// The JSON still round-trips to the exact hostile name.
	m := shopConfigBlock.FindStringSubmatch(body)
	if m == nil {
		t.Fatal("no shop-config block")
	}
	var got publicConfigResponse
	if err := json.Unmarshal([]byte(m[1]), &got); err != nil {
		t.Fatal(err)
	}
	if got.Branding.BusinessName != cfg.Quote.BusinessName {
		t.Errorf("business name = %q", got.Branding.BusinessName)
	}
	if strings.ContainsAny(m[1], "<>&") {
		t.Errorf("embedded JSON has unescaped HTML characters: %s", m[1])
	}
}

func TestShopHTMLHeadersNoCacheAndCSPUnchanged(t *testing.T) {
	cfg := newPublicTestConfig(t, t.TempDir())
	rec := getShopHTML(t, shopHTMLHandler(t, cfg), "/")
	if got := rec.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", got)
	}
	if got := rec.Header().Get("Content-Security-Policy"); got != publicContentSecurityPolicy {
		t.Errorf("CSP = %q, want the unchanged public policy", got)
	}
	// A JSON data block isn't executed, so the policy needs no inline-script allowance.
	if strings.Contains(publicContentSecurityPolicy, "unsafe-inline") || strings.Contains(publicContentSecurityPolicy, "script-src") {
		t.Errorf("CSP grants inline script: %s", publicContentSecurityPolicy)
	}
	if !strings.Contains(rec.Body.String(), `<script type="application/json" id="shop-config">`) {
		t.Error("embedded block must be a non-executing application/json script")
	}
	for _, h := range []string{"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy"} {
		if rec.Header().Get(h) == "" {
			t.Errorf("security header %s missing", h)
		}
	}
}

func TestShopHTMLPausedStays200AndEmbedsPausedState(t *testing.T) {
	cfg := newPublicTestConfig(t, t.TempDir())
	cfg.Shop.AcceptingOrders = false
	cfg.Shop.PausedMessage = "Back Monday"
	rec := getShopHTML(t, shopHTMLHandler(t, cfg), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("paused shop HTML = %d, want 200", rec.Code)
	}
	m := shopConfigBlock.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatal("no shop-config block")
	}
	var got publicConfigResponse
	if err := json.Unmarshal([]byte(m[1]), &got); err != nil {
		t.Fatal(err)
	}
	if got.Shop.AcceptingOrders || got.Shop.PausedMessage != "Back Monday" {
		t.Errorf("embedded shop = %+v, want paused with message", got.Shop)
	}
}

func TestShopHTMLReflectsSettingsChangeWithoutRestart(t *testing.T) {
	cfg := newPublicTestConfig(t, t.TempDir())
	store := NewConfigStore(cfg)
	h := publicIndexHandler(shopHTMLFS(), func() publicConfigResponse {
		return buildPublicConfig(store.Get(), false, publicPortStaffURL)
	})
	if body := getShopHTML(t, h, "/").Body.String(); strings.Contains(body, "<title>Second</title>") {
		t.Fatal("precondition")
	}
	next := *store.Get()
	next.Shop.PageTitle = "Second"
	store.Set(&next)
	if body := getShopHTML(t, h, "/").Body.String(); !strings.Contains(body, "<title>Second</title>") {
		t.Errorf("settings change not reflected: %s", body)
	}
}

func TestShopHTMLUnbuiltStillFailsClosed(t *testing.T) {
	called := false
	h := publicIndexHandler(fstest.MapFS{}, func() publicConfigResponse { called = true; return publicConfigResponse{} })
	rec := getShopHTML(t, h, "/")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("unbuilt = %d, want 503", rec.Code)
	}
	if called {
		t.Error("config was read for a build that can't serve the page")
	}
}
