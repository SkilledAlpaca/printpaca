package main

import (
	"bytes"
	"encoding/json"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shopEnv is one database, config store, admin mux (with PUT /api/settings) and public mux.
type shopEnv struct {
	dir           string
	admin, public *http.ServeMux
	store         *ConfigStore
	cfg           *Config
}

func newShopEnv(t *testing.T) *shopEnv {
	t.Helper()
	dir := t.TempDir()
	adminMux, publicMux, db, cfg := newRequestsTestSetup(t, dir)
	_ = db
	// newRequestsTestSetup keeps the store private; rebuild around the same config pointer so tests can
	// flip cfg.Shop in place, as the other public tests do.
	store := NewConfigStore(cfg)
	adminMux = http.NewServeMux()
	registerRoutes(adminMux, db, store, newAuthState("", nil), dir, false)
	adminMux.HandleFunc("PUT /api/settings", handleSettingsPut(dir, store, newAuthState("", nil)))
	publicMux, _ = buildPublicMux(store, db, dir, false, false)
	return &shopEnv{dir: dir, admin: adminMux, public: publicMux, store: store, cfg: cfg}
}

func (e *shopEnv) putSettings(t *testing.T, cfg *Config) *httptest.ResponseRecorder {
	t.Helper()
	return putSettings(t, e.admin, cfg)
}

func (e *shopEnv) publicConfig(t *testing.T) publicConfigResponse {
	t.Helper()
	rec := serveReq(e.public, httptest.NewRequest("GET", "/api/public/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/public/config = %d %s", rec.Code, rec.Body.String())
	}
	var pc publicConfigResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &pc); err != nil {
		t.Fatal(err)
	}
	return pc
}

func (e *shopEnv) postHero(t *testing.T, alt string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	target := "/api/shop/hero-image"
	if alt != "" {
		target += "?alt=" + strings.ReplaceAll(alt, " ", "%20")
	}
	req := httptest.NewRequest("POST", target, bytes.NewReader(body))
	req.Header.Set("Content-Type", "image/png")
	return serveReq(e.admin, req)
}

// ---------------------------------------------------------------------------
// Config defaults and validation
// ---------------------------------------------------------------------------

// Defaults keep v1.1 behavior exactly: everything on, nothing restyled.
func TestShopDefaultsPreserveV11Behavior(t *testing.T) {
	cfg := loadTestConfig(t, t.TempDir())
	want := Shop{
		MaxOrderQty: 100, CustomEstimates: true, Gallery: true, AcceptingOrders: true, DefaultView: "gallery",
		ShowStock: true, ShowFromPrice: true, ItemSort: "manual", FooterLinks: []ShopFooterLink{}, ShowContact: true,
	}
	got := cfg.Shop
	if got.MaxOrderQty != want.MaxOrderQty || got.CustomEstimates != want.CustomEstimates || got.Gallery != want.Gallery ||
		got.AcceptingOrders != want.AcceptingOrders || got.DefaultView != want.DefaultView || got.ShowStock != want.ShowStock ||
		got.ShowFromPrice != want.ShowFromPrice || got.ItemSort != want.ItemSort || got.ShowContact != want.ShowContact ||
		got.PausedMessage != "" || got.Heading != "" || got.Announcement != "" || got.AccentColor != "" ||
		got.HeroImage != (ShopHeroImage{}) || got.ShippingNote != "" || got.TurnaroundNote != "" || got.FooterLinks == nil || len(got.FooterLinks) != 0 {
		t.Fatalf("shop defaults = %+v, want %+v", got, want)
	}
}

func TestShopValidation(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	cases := []struct {
		name   string
		mutate func(*Shop)
		want   string // substring of a problem; "" means valid
	}{
		{"defaults are valid", func(s *Shop) {}, ""},
		{"both gallery and estimates off", func(s *Shop) { s.Gallery, s.CustomEstimates = false, false }, "can't both be off"},
		{"estimates off alone", func(s *Shop) { s.CustomEstimates = false }, ""},
		{"gallery off alone", func(s *Shop) { s.Gallery = false }, ""},
		{"bad default view", func(s *Shop) { s.DefaultView = "grid" }, "shop.defaultView"},
		{"estimate default view", func(s *Shop) { s.DefaultView = "estimate" }, ""},
		{"bad sort", func(s *Shop) { s.ItemSort = "newest" }, "shop.itemSort"},
		{"sort by price", func(s *Shop) { s.ItemSort = "price" }, ""},
		{"page title at limit", func(s *Shop) { s.PageTitle = long(70) }, ""},
		{"page title too long", func(s *Shop) { s.PageTitle = long(71) }, "shop.pageTitle: at most 70"},
		{"page title with newline", func(s *Shop) { s.PageTitle = "a\nb" }, "shop.pageTitle: must be plain text"},
		{"heading at limit", func(s *Shop) { s.Heading = long(120) }, ""},
		{"heading too long", func(s *Shop) { s.Heading = long(121) }, "shop.heading: at most 120"},
		{"heading with newline", func(s *Shop) { s.Heading = "a\nb" }, "shop.heading: must be plain text"},
		{"announcement too long", func(s *Shop) { s.Announcement = long(301) }, "shop.announcement"},
		{"paused message too long", func(s *Shop) { s.PausedMessage = long(301) }, "shop.pausedMessage"},
		{"shipping note too long", func(s *Shop) { s.ShippingNote = long(301) }, "shop.shippingNote"},
		{"turnaround note too long", func(s *Shop) { s.TurnaroundNote = long(301) }, "shop.turnaroundNote"},
		{"markup stays as text", func(s *Shop) { s.Announcement = "<b>Closed</b> until 5 Jan" }, ""},
		{"hero file needs alt", func(s *Shop) { s.HeroImage = ShopHeroImage{File: "0123456789abcdef.png"} }, "shop.heroImage.alt: required"},
		{"hero file and alt", func(s *Shop) { s.HeroImage = ShopHeroImage{File: "0123456789abcdef.png", Alt: "Shelves of prints"} }, ""},
		{"hero file must be a stored image name", func(s *Shop) { s.HeroImage = ShopHeroImage{File: "../x.png", Alt: "a"} }, "shop.heroImage.file"},
		{"hero alt too long", func(s *Shop) { s.HeroImage = ShopHeroImage{File: "0123456789abcdef.png", Alt: long(201)} }, "shop.heroImage.alt: at most 200"},
		{"accent passes", func(s *Shop) { s.AccentColor = "#9c4212" }, ""},
		{"accent fails contrast", func(s *Shop) { s.AccentColor = "#ffd700" }, "Accent color"},
		{"accent bad format", func(s *Shop) { s.AccentColor = "red" }, "six hex digits"},
		{"six footer links", func(s *Shop) {
			for i := 0; i < 6; i++ {
				s.FooterLinks = append(s.FooterLinks, ShopFooterLink{Label: "L", URL: "https://example.com/x"})
			}
		}, ""},
		{"seven footer links", func(s *Shop) {
			for i := 0; i < 7; i++ {
				s.FooterLinks = append(s.FooterLinks, ShopFooterLink{Label: "L", URL: "https://example.com/x"})
			}
		}, "shop.footerLinks: at most 6"},
		{"footer link http", func(s *Shop) { s.FooterLinks = []ShopFooterLink{{Label: "Etsy", URL: "http://example.com"}} }, "must be an https://"},
		{"footer link javascript", func(s *Shop) { s.FooterLinks = []ShopFooterLink{{Label: "x", URL: "javascript:alert(1)"}} }, "must be an https://"},
		{"footer link credentials", func(s *Shop) { s.FooterLinks = []ShopFooterLink{{Label: "x", URL: "https://user:pw@example.com"}} }, "must be an https://"},
		{"footer link without host", func(s *Shop) { s.FooterLinks = []ShopFooterLink{{Label: "x", URL: "https://"}} }, "must be an https://"},
		{"footer link with space", func(s *Shop) { s.FooterLinks = []ShopFooterLink{{Label: "x", URL: "https://example.com/a b"}} }, "must not contain spaces"},
		{"footer link label required", func(s *Shop) { s.FooterLinks = []ShopFooterLink{{Label: " ", URL: "https://example.com"}} }, "label: required"},
		{"footer link label too long", func(s *Shop) { s.FooterLinks = []ShopFooterLink{{Label: long(41), URL: "https://example.com"}} }, "label: at most 40"},
		{"footer link url required", func(s *Shop) { s.FooterLinks = []ShopFooterLink{{Label: "x"}} }, "url: required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := loadTestConfig(t, t.TempDir())
			c.mutate(&cfg.Shop)
			normalizeConfig(cfg)
			var matched bool
			for _, p := range validateShop(cfg.Shop) {
				if c.want != "" && strings.Contains(p, c.want) {
					matched = true
				} else if c.want == "" {
					t.Errorf("unexpected problem %q", p)
				}
			}
			if c.want != "" && !matched {
				t.Errorf("want a problem containing %q, got %v", c.want, validateShop(cfg.Shop))
			}
		})
	}
}

// A hand-edited config.yaml goes through the same validation at load.
func TestShopConfigYAMLLoadsAndRejects(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("shop:\n  gallery: false\n  customEstimates: false\n"), 0o644)
	if _, err := LoadConfig(dir); err == nil || !strings.Contains(err.Error(), "can't both be off") {
		t.Errorf("both off: err = %v, want the both-off message", err)
	}
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("shop:\n  accentColor: \"#9C4212\"\n  footerLinks:\n    - label: Etsy\n      url: https://example.com/shop\n  itemSort: price\n"), 0o644)
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Shop.AccentColor != "#9c4212" || len(cfg.Shop.FooterLinks) != 1 || cfg.Shop.ItemSort != "price" || !cfg.Shop.Gallery {
		t.Errorf("shop = %+v", cfg.Shop)
	}
}

func TestShopSettingsPutValidatesAndRoundTrips(t *testing.T) {
	e := newShopEnv(t)

	bad := cloneConfig(t, e.cfg)
	bad.Shop.Gallery, bad.Shop.CustomEstimates = false, false
	if rec := e.putSettings(t, bad); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "can't both be off") {
		t.Fatalf("both off PUT = %d %s", rec.Code, rec.Body.String())
	}
	low := cloneConfig(t, e.cfg)
	low.Shop.AccentColor = "#ffd700"
	rec := e.putSettings(t, low)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "links measure") || !strings.Contains(rec.Body.String(), "try #") {
		t.Fatalf("low-contrast accent PUT = %d %s", rec.Code, rec.Body.String())
	}

	ok := cloneConfig(t, e.cfg)
	ok.Shop.CustomEstimates = false
	ok.Shop.Heading = "Alpaca Prints"
	ok.Shop.AccentColor = "#9C4212"
	ok.Shop.FooterLinks = []ShopFooterLink{{Label: "Etsy", URL: "https://example.com/shop"}}
	if rec := e.putSettings(t, ok); rec.Code != http.StatusOK {
		t.Fatalf("valid PUT = %d %s", rec.Code, rec.Body.String())
	}
	got := e.store.Get().Shop
	if got.CustomEstimates || got.Heading != "Alpaca Prints" || got.AccentColor != "#9c4212" || len(got.FooterLinks) != 1 {
		t.Errorf("stored shop = %+v", got)
	}
	// Reload from disk: the sparse overrides reproduce it.
	reloaded := loadTestConfig(t, e.dir)
	if reloaded.Shop.CustomEstimates || reloaded.Shop.Heading != "Alpaca Prints" || !reloaded.Shop.Gallery {
		t.Errorf("reloaded shop = %+v", reloaded.Shop)
	}
}

// ---------------------------------------------------------------------------
// Public config
// ---------------------------------------------------------------------------

func TestPublicConfigShopFields(t *testing.T) {
	e := newShopEnv(t)
	pc := e.publicConfig(t)
	s := pc.Shop
	if !s.CustomEstimates || !s.Gallery || !s.AcceptingOrders || s.DefaultView != "gallery" || s.HeroImage != nil || s.Accent != nil ||
		!s.ShowStock || !s.ShowFromPrice || s.ItemSort != "manual" || s.FooterLinks == nil || len(s.FooterLinks) != 0 || !s.ShowContact {
		t.Fatalf("default public shop = %+v", s)
	}
	if s.PausedMessage != defaultShopPausedMessage {
		t.Errorf("pausedMessage = %q, want the default", s.PausedMessage)
	}

	e.cfg.Quote.Email, e.cfg.Quote.Phone, e.cfg.Quote.Address = "hi@example.com", "555-0100", "1 Main St"
	e.cfg.Shop.CustomEstimates = false
	e.cfg.Shop.DefaultView = "estimate"
	e.cfg.Shop.AcceptingOrders = false
	e.cfg.Shop.PausedMessage = "Closed until January 5"
	e.cfg.Shop.Heading = "Alpaca Prints"
	e.cfg.Shop.AccentColor = "#9c4212"
	e.cfg.Shop.HeroImage = ShopHeroImage{File: "0123456789abcdef.png", Alt: "Shelves of prints"}
	pc = e.publicConfig(t)
	s = pc.Shop
	if s.CustomEstimates || s.DefaultView != "gallery" {
		t.Errorf("estimates off must force the gallery view: %+v", s)
	}
	if s.AcceptingOrders || s.PausedMessage != "Closed until January 5" || s.Heading != "Alpaca Prints" {
		t.Errorf("shop = %+v", s)
	}
	if s.HeroImage == nil || s.HeroImage.URL != "/item-images/0123456789abcdef.png" || s.HeroImage.Alt != "Shelves of prints" {
		t.Errorf("hero = %+v", s.HeroImage)
	}
	if s.Accent == nil || s.Accent.Light.Accent != "#9c4212" || s.Accent.Light.ButtonText != "#ffffff" || s.Accent.Dark.Accent == "" || s.Accent.Dark.ButtonText == "" {
		t.Errorf("accent = %+v", s.Accent)
	}
	if pc.Branding.Email != "hi@example.com" {
		t.Errorf("contact should show by default: %+v", pc.Branding)
	}

	// Gallery off leaves the estimate as the only view.
	e.cfg.Shop.CustomEstimates, e.cfg.Shop.Gallery, e.cfg.Shop.DefaultView = true, false, "gallery"
	if got := e.publicConfig(t).Shop.DefaultView; got != "estimate" {
		t.Errorf("gallery off: defaultView = %q, want estimate", got)
	}

	// Hiding contact removes the details from the response itself.
	e.cfg.Shop.ShowContact = false
	pc = e.publicConfig(t)
	if pc.Branding.Email != "" || pc.Branding.Phone != "" || pc.Branding.Address != "" || pc.Shop.ShowContact {
		t.Errorf("showContact off still serves contact details: %+v", pc.Branding)
	}
}

// A failing accent that reached config some other way never reaches a page.
func TestPublicConfigDropsFailingAccent(t *testing.T) {
	e := newShopEnv(t)
	e.cfg.Shop.AccentColor = "#ffd700"
	if got := e.publicConfig(t).Shop.Accent; got != nil {
		t.Errorf("accent = %+v, want nil", got)
	}
	// The response is assembled field by field: no secret-ish config key rides along.
	rec := serveReq(e.public, httptest.NewRequest("GET", "/api/public/config", nil))
	for _, banned := range []string{"maxOrderQty\":100,\"shop\":{\"maxOrderQty", "smtp", "password", "heroImage\":{\"file"} {
		if strings.Contains(rec.Body.String(), banned) {
			t.Errorf("public config contains %q", banned)
		}
	}
}

// ---------------------------------------------------------------------------
// Server enforcement
// ---------------------------------------------------------------------------

func (e *shopEnv) publicItem(t *testing.T) Item {
	t.Helper()
	rec := postItem(t, e.admin, `{"name":"Widget","priceCents":500,"public":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed item = %d %s", rec.Code, rec.Body.String())
	}
	var it Item
	json.Unmarshal(rec.Body.Bytes(), &it)
	return it
}

func (e *shopEnv) estimateStatuses(t *testing.T) (preview, submit int, previewBody, submitBody string) {
	t.Helper()
	p := postPublicQuotePreview(t, e.public, validQuotePreviewBody("PLA"))
	s := postPublicQuoteRequest(t, e.public, publicQuoteRequestFields("PLA"), "model.gcode", []byte("G1 X1\n"))
	return p.Code, s.Code, p.Body.String(), s.Body.String()
}

func TestShopCustomEstimatesOffRejectsEstimatesOnly(t *testing.T) {
	e := newShopEnv(t)
	item := e.publicItem(t)
	e.cfg.Shop.CustomEstimates = false

	preview, submit, pb, sb := e.estimateStatuses(t)
	if preview != http.StatusNotFound || submit != http.StatusNotFound {
		t.Fatalf("estimates off: preview %d, submit %d, want 404 for both", preview, submit)
	}
	for _, body := range []string{pb, sb} {
		if strings.Contains(strings.ToLower(body), "estimate") || strings.Contains(body, "shop.") || strings.Contains(body, "customEstimates") {
			t.Errorf("rejection leaks the setting: %s", body)
		}
	}
	if rec := postPublicItemOrder(t, e.public, validItemOrderBody(item.ID, nil, 1)); rec.Code != http.StatusCreated {
		t.Errorf("item order with estimates off = %d %s, want 201", rec.Code, rec.Body.String())
	}
	// Turning it back on restores the flow, and the material's Public flag was never touched.
	e.cfg.Shop.CustomEstimates = true
	if preview, submit, _, _ := e.estimateStatuses(t); preview != http.StatusOK || submit != http.StatusCreated {
		t.Errorf("estimates back on: preview %d, submit %d", preview, submit)
	}
}

func TestShopGalleryOffRejectsItemOrders(t *testing.T) {
	e := newShopEnv(t)
	item := e.publicItem(t)
	e.cfg.Shop.Gallery = false

	rec := postPublicItemOrder(t, e.public, validItemOrderBody(item.ID, nil, 1))
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "gallery") {
		t.Errorf("item order with gallery off = %d %s, want a bare 404", rec.Code, rec.Body.String())
	}
	if preview, submit, _, _ := e.estimateStatuses(t); preview != http.StatusOK || submit != http.StatusCreated {
		t.Errorf("estimates with gallery off: preview %d, submit %d, want 200 and 201", preview, submit)
	}
	list := serveReq(e.public, httptest.NewRequest("GET", "/api/public/items", nil))
	if list.Code != http.StatusOK || strings.TrimSpace(list.Body.String()) != `{"items":[],"categories":[]}` {
		t.Errorf("items with gallery off = %d %s, want an empty list", list.Code, list.Body.String())
	}
	e.cfg.Shop.Gallery = true
	if rec := postPublicItemOrder(t, e.public, validItemOrderBody(item.ID, nil, 1)); rec.Code != http.StatusCreated {
		t.Errorf("item order with gallery back on = %d %s", rec.Code, rec.Body.String())
	}
}

func TestShopPausedRejectsBothKindsAndKeepsExistingRequests(t *testing.T) {
	e := newShopEnv(t)
	item := e.publicItem(t)

	// A request submitted before the pause.
	existing := postPublicItemOrder(t, e.public, validItemOrderBody(item.ID, nil, 1))
	if existing.Code != http.StatusCreated {
		t.Fatalf("seed order = %d %s", existing.Code, existing.Body.String())
	}
	var accepted struct {
		ID string `json:"id"`
	}
	json.Unmarshal(existing.Body.Bytes(), &accepted)

	e.cfg.Shop.AcceptingOrders = false
	e.cfg.Shop.PausedMessage = "Closed until January 5"

	check := func(name string, code int, body string) {
		t.Helper()
		if code != http.StatusServiceUnavailable {
			t.Errorf("%s while paused = %d %s, want 503", name, code, body)
		}
		var resp struct {
			Error   string   `json:"error"`
			Errors  []string `json:"errors"`
			Paused  bool     `json:"paused"`
			Message string   `json:"pausedMessage"`
		}
		if err := json.Unmarshal([]byte(body), &resp); err != nil || !resp.Paused || resp.Error != "Closed until January 5" || resp.Message != "Closed until January 5" ||
			len(resp.Errors) != 1 || resp.Errors[0] != "Closed until January 5" {
			t.Errorf("%s paused body = %s (err %v)", name, body, err)
		}
	}
	rec := postPublicItemOrder(t, e.public, validItemOrderBody(item.ID, nil, 1))
	check("item order", rec.Code, rec.Body.String())
	preview, submit, pb, sb := e.estimateStatuses(t)
	check("preview", preview, pb)
	check("submit", submit, sb)

	// Existing requests are unaffected, and the owner can still work them.
	get := serveReq(e.admin, httptest.NewRequest("GET", "/api/requests/"+accepted.ID, nil))
	if get.Code != http.StatusOK {
		t.Errorf("existing request while paused = %d %s, want 200", get.Code, get.Body.String())
	}
	if rec := serveReq(e.admin, httptest.NewRequest("POST", "/api/requests/"+accepted.ID+"/status", strings.NewReader(`{"status":"finalized"}`))); rec.Code != http.StatusOK {
		t.Errorf("finalize while paused = %d %s, want 200", rec.Code, rec.Body.String())
	}

	// A blank message falls back to the default wording.
	e.cfg.Shop.PausedMessage = ""
	rec = postPublicItemOrder(t, e.public, validItemOrderBody(item.ID, nil, 1))
	if !strings.Contains(rec.Body.String(), defaultShopPausedMessage) {
		t.Errorf("blank pausedMessage body = %s", rec.Body.String())
	}

	e.cfg.Shop.AcceptingOrders = true
	if rec := postPublicItemOrder(t, e.public, validItemOrderBody(item.ID, nil, 1)); rec.Code != http.StatusCreated {
		t.Errorf("order after reopening = %d %s", rec.Code, rec.Body.String())
	}
}

// A feature that is switched off wins over vacation mode, so the response never hints at a closed feature.
func TestShopOffFeatureAnswers404EvenWhenPaused(t *testing.T) {
	e := newShopEnv(t)
	e.cfg.Shop.CustomEstimates = false
	e.cfg.Shop.AcceptingOrders = false
	if preview, submit, _, _ := e.estimateStatuses(t); preview != http.StatusNotFound || submit != http.StatusNotFound {
		t.Errorf("preview %d, submit %d, want 404", preview, submit)
	}
}

// ---------------------------------------------------------------------------
// Hero image
// ---------------------------------------------------------------------------

func TestHeroImageUploadRequiresAltAndImage(t *testing.T) {
	e := newShopEnv(t)
	png := testPNG(t, color.RGBA{1, 2, 3, 255})
	if rec := e.postHero(t, "", png); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "alt") {
		t.Errorf("no alt = %d %s, want 422 naming alt", rec.Code, rec.Body.String())
	}
	if rec := e.postHero(t, "   ", png); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("blank alt = %d", rec.Code)
	}
	if rec := e.postHero(t, "Prints", []byte("not an image")); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("non-image = %d %s, want 422", rec.Code, rec.Body.String())
	}
	if rec := e.postHero(t, "Prints", bytes.Repeat([]byte{0}, itemImageMaxBytes+1)); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize = %d, want 413", rec.Code)
	}
	if e.store.Get().Shop.HeroImage.File != "" {
		t.Errorf("a rejected upload changed the config: %+v", e.store.Get().Shop.HeroImage)
	}
}

func TestHeroImageUploadReplaceDelete(t *testing.T) {
	e := newShopEnv(t)
	rec := e.postHero(t, "Shelves of prints", testPNG(t, color.RGBA{1, 2, 3, 255}))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d %s", rec.Code, rec.Body.String())
	}
	var first heroImageResponse
	json.Unmarshal(rec.Body.Bytes(), &first)
	if !itemImageName.MatchString(first.File) || first.URL != "/item-images/"+first.File || first.Alt != "Shelves of prints" {
		t.Fatalf("response = %+v", first)
	}
	if _, err := os.Stat(itemImagePath(e.dir, first.File)); err != nil {
		t.Fatalf("stored file: %v", err)
	}
	// Served like an item photo, on the public mux.
	img := serveReq(e.public, httptest.NewRequest("GET", first.URL, nil))
	if img.Code != http.StatusOK || !strings.HasPrefix(img.Header().Get("Content-Type"), "image/png") {
		t.Errorf("GET hero = %d %s", img.Code, img.Header().Get("Content-Type"))
	}
	// The public config carries it, and a restart (reload from disk) keeps it.
	if h := e.publicConfig(t).Shop.HeroImage; h == nil || h.URL != first.URL || h.Alt != "Shelves of prints" {
		t.Errorf("public hero = %+v", h)
	}
	if loadTestConfig(t, e.dir).Shop.HeroImage.File != first.File {
		t.Errorf("hero file not persisted to config.yaml")
	}

	// Replacing removes the old file.
	rec = e.postHero(t, "Printer at work", testPNG(t, color.RGBA{9, 9, 9, 255}))
	var second heroImageResponse
	json.Unmarshal(rec.Body.Bytes(), &second)
	if rec.Code != http.StatusOK || second.File == first.File {
		t.Fatalf("replace = %d %+v", rec.Code, second)
	}
	if _, err := os.Stat(itemImagePath(e.dir, first.File)); !os.IsNotExist(err) {
		t.Errorf("old hero file still on disk (err %v)", err)
	}

	// Delete clears the config and the file; a second delete is a no-op success.
	del := serveReq(e.admin, httptest.NewRequest("DELETE", "/api/shop/hero-image", nil))
	if del.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", del.Code, del.Body.String())
	}
	if _, err := os.Stat(itemImagePath(e.dir, second.File)); !os.IsNotExist(err) {
		t.Errorf("deleted hero file still on disk (err %v)", err)
	}
	if got := e.store.Get().Shop.HeroImage; got != (ShopHeroImage{}) {
		t.Errorf("hero after delete = %+v", got)
	}
	if again := serveReq(e.admin, httptest.NewRequest("DELETE", "/api/shop/hero-image", nil)); again.Code != http.StatusOK {
		t.Errorf("second delete = %d", again.Code)
	}
}

// Orphan cleanup counts the hero as a reference: an item photo with the same bytes keeps the file when the
// hero goes, and a hero keeps the file when the item photo goes.
func TestHeroImageSharedWithItemPhoto(t *testing.T) {
	e := newShopEnv(t)
	body := testPNG(t, color.RGBA{50, 60, 70, 255})
	item := e.publicItem(t)
	up := uploadItemImage(t, e.admin, item.ID, body)
	var withPhoto Item
	json.Unmarshal(up.Body.Bytes(), &withPhoto)

	rec := e.postHero(t, "Same picture", body)
	var hero heroImageResponse
	json.Unmarshal(rec.Body.Bytes(), &hero)
	if hero.File != withPhoto.ImageFile {
		t.Fatalf("content addressing: hero %s, item %s", hero.File, withPhoto.ImageFile)
	}
	serveReq(e.admin, httptest.NewRequest("DELETE", "/api/shop/hero-image", nil))
	if _, err := os.Stat(itemImagePath(e.dir, hero.File)); err != nil {
		t.Errorf("deleting the hero removed a file an item still uses: %v", err)
	}

	// Now the other way round: hero set, item deleted.
	e.postHero(t, "Same picture", body)
	if rec := deleteItem(t, e.admin, item.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("delete item = %d", rec.Code)
	}
	if _, err := os.Stat(itemImagePath(e.dir, hero.File)); err != nil {
		t.Errorf("deleting the item removed the hero's file: %v", err)
	}
}

func TestHeroImageSettingsSaveKeepsFileAndRequiresAlt(t *testing.T) {
	e := newShopEnv(t)
	rec := e.postHero(t, "Shelves of prints", testPNG(t, color.RGBA{1, 2, 3, 255}))
	var hero heroImageResponse
	json.Unmarshal(rec.Body.Bytes(), &hero)

	// A settings save that names a different file, or none, can't change the stored file.
	cfg := cloneConfig(t, e.store.Get())
	cfg.Shop.HeroImage = ShopHeroImage{File: "ffffffffffffffff.png", Alt: "New alt text"}
	if rec := e.putSettings(t, cfg); rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", rec.Code, rec.Body.String())
	}
	if got := e.store.Get().Shop.HeroImage; got.File != hero.File || got.Alt != "New alt text" {
		t.Errorf("hero after PUT = %+v, want file %s with the new alt", got, hero.File)
	}
	cfg.Shop.HeroImage = ShopHeroImage{}
	if rec := e.putSettings(t, cfg); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "shop.heroImage.alt: required") {
		t.Errorf("PUT with the alt cleared = %d %s, want 422", rec.Code, rec.Body.String())
	}
	if got := e.store.Get().Shop.HeroImage; got.File != hero.File || got.Alt != "New alt text" {
		t.Errorf("a rejected PUT changed the hero: %+v", got)
	}
}

func TestHeroImageEndpointsAreAdminOnly(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	store := NewConfigStore(loadTestConfig(t, dir))
	auth := newAuthState("secret", nil)
	mux := http.NewServeMux()
	registerRoutes(mux, db, store, auth, dir, false)
	handler := auth.middleware(mux)
	for _, method := range []string{"POST", "DELETE"} {
		req := httptest.NewRequest(method, "/api/shop/hero-image?alt=x", bytes.NewReader(testPNG(t, color.RGBA{1, 1, 1, 255})))
		req.Header.Set("Content-Type", "image/png")
		rec := serveReq(handler, req)
		if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
			t.Errorf("%s without a session = %d, want 401 or 403", method, rec.Code)
		}
	}
}

// A client that predates the storefront settings sends only shop.maxOrderQty: the rest keeps its default
// instead of reading as "everything off". An absent shop key is still refused, like an absent site key.
func TestSettingsPutPartialShopKeepsDefaults(t *testing.T) {
	e := newShopEnv(t)
	cfg := cloneConfig(t, e.store.Get())
	raw, err := json.Marshal(struct {
		Config map[string]any `json:"config"`
	}{Config: func() map[string]any {
		var m map[string]any
		b, _ := json.Marshal(cfg)
		json.Unmarshal(b, &m)
		m["shop"] = map[string]any{"maxOrderQty": 7}
		return m
	}()})
	if err != nil {
		t.Fatal(err)
	}
	rec := serveReq(e.admin, httptest.NewRequest("PUT", "/api/settings", bytes.NewReader(raw)))
	if rec.Code != http.StatusOK {
		t.Fatalf("partial shop PUT = %d %s", rec.Code, rec.Body.String())
	}
	got := e.store.Get().Shop
	if got.MaxOrderQty != 7 || !got.Gallery || !got.CustomEstimates || !got.AcceptingOrders || got.ItemSort != "manual" || got.DefaultView != "gallery" || !got.ShowContact {
		t.Errorf("shop after a partial PUT = %+v, want maxOrderQty 7 and the defaults elsewhere", got)
	}

	var m map[string]any
	b, _ := json.Marshal(cfg)
	json.Unmarshal(b, &m)
	delete(m, "shop")
	raw, _ = json.Marshal(map[string]any{"config": m})
	rec = serveReq(e.admin, httptest.NewRequest("PUT", "/api/settings", bytes.NewReader(raw)))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "shop: required") {
		t.Errorf("PUT without shop = %d %s, want 422 shop: required", rec.Code, rec.Body.String())
	}
}
