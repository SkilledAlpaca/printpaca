package main

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log"
	"math"
	"mime/multipart"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// The public listener: a second HTTP server, enabled only when PUBLIC_PORT is set, reachable only
// through its own ServeMux (buildPublicMux). The server is the sole price authority: the preview and
// submission paths both compute price via ComputeQuote and never read a client-sent price.

const (
	// publicMaxUploadBytes caps the whole multipart request body.
	publicMaxUploadBytes = 32 << 20 // 32 MiB
	// publicUploadsCapBytes is the total size CONFIG_DIR/uploads may reach; past it, submissions are stored without their file.
	publicUploadsCapBytes = 2 << 30 // 2 GiB
	// publicRateLimit/publicRateWindow are the per-IP abuse guard. Item orders and custom estimates each get
	// their own budget of this size (publicShared.orderRl, quoteRl), so a visitor who sends estimates can
	// still place an order.
	publicRateLimit  = 10
	publicRateWindow = time.Hour
	// publicMaxConcurrentUploads bounds submissions mid-upload. Streaming to disk avoids heap buffering, but
	// gzip and multipart buffers and disk writes still cost; the per-IP rate limit doesn't bound concurrency.
	publicMaxConcurrentUploads = 4
	// publicUploadReadDeadline is the per-request body read deadline, set via ResponseController once a
	// submission is accepted. It replaces a 15s server ReadTimeout that made publicMaxUploadBytes
	// unreachable for a slow but real client.
	publicUploadReadDeadline = 5 * time.Minute
	// publicUploadIdleTimeout is the rolling read deadline: each body Read must make progress within it, so a
	// client that stalls entirely frees its slot in seconds instead of holding it for publicUploadReadDeadline.
	publicUploadIdleTimeout = 30 * time.Second
	// publicDeadlineMargin backs two defensive, independent backstops over publicUploadReadDeadline:
	// newPublicServer's ReadTimeout and the per-request write-deadline extension. If a middleware wrapper
	// ever breaks Unwrap() again (as a status recorder could) the primary per-request deadlines
	// fail closed with a logged error instead of leaving no deadline. 30s covers the final response write.
	publicDeadlineMargin = 30 * time.Second
	// publicMaxFieldBytes bounds any single non-file form field read into memory (note tops out at 2000 chars).
	publicMaxFieldBytes = 4 << 10 // 4 KiB
	// publicMaxOpenRequests caps quote_requests rows in a non-finalized state ("new" or "viewed"). Unlike
	// publicUploadsCapBytes it bounds row count for every submission, so omitting the file can't grow it.
	publicMaxOpenRequests = 500
)

// publicUploadMinRate and publicUploadRateGrace set the minimum average body transfer rate (bytes/second),
// enforced once publicUploadRateGrace has elapsed. A trickle client (slowloris style) that sends a byte
// every few seconds would otherwise hold an upload slot for the whole publicUploadReadDeadline. Variables
// so tests can shrink them; 8 KiB/s is far below any real connection that can finish a 32 MiB upload.
var (
	publicUploadMinRate   int64 = 8 << 10
	publicUploadRateGrace       = 20 * time.Second
)

// errUploadTooSlow is returned by minRateBody when the average transfer rate falls below publicUploadMinRate.
var errUploadTooSlow = errors.New("upload transfer rate below minimum")

// minRateBody wraps a request body to enforce a rolling per-Read idle deadline and the minimum average rate.
type minRateBody struct {
	rc       *http.ResponseController
	body     io.ReadCloser
	start    time.Time
	absolute time.Time // hard deadline; the rolling deadline never extends past it
	n        int64
}

func (m *minRateBody) Read(p []byte) (int, error) {
	now := time.Now()
	if el := now.Sub(m.start); el > publicUploadRateGrace && m.n*int64(time.Second) < publicUploadMinRate*int64(el) {
		return 0, errUploadTooSlow
	}
	next := now.Add(publicUploadIdleTimeout)
	if next.After(m.absolute) {
		next = m.absolute
	}
	_ = m.rc.SetReadDeadline(next) // ErrNotSupported is already logged once by the initial deadline call
	n, err := m.body.Read(p)
	m.n += int64(n)
	return n, err
}

func (m *minRateBody) Close() error { return m.body.Close() }

// plainEmailForMailto reports whether email is safe to place in a mailto: link: no whitespace or control
// characters and none of ? & % # < > " , ; \ that could add mailto headers (cc, bcc, body) or break out of
// the address. Real addresses practically never contain them.
func plainEmailForMailto(email string) bool {
	for _, r := range email {
		if r <= ' ' || r == 0x7f || strings.ContainsRune("?&%#<>\",;\\", r) {
			return false
		}
	}
	return true
}

// plausiblePublicEmail reports whether email looks like name@example.com: exactly one @, a non-empty local
// part, and a domain that contains a dot which is neither first nor last. Whitespace is rejected too. It is a
// shape check (the visitor still has to type a reachable address), applied to the public estimate and item
// order forms alongside plainEmailForMailto; web/src/lib/publicFormLimits.ts mirrors it.
func plausiblePublicEmail(email string) bool {
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || strings.Contains(domain, "@") {
		return false
	}
	if strings.ContainsFunc(email, unicode.IsSpace) {
		return false
	}
	dot := strings.Index(domain, ".")
	return dot > 0 && !strings.HasSuffix(domain, ".")
}

// publicEmailFormatProblem is the 422 message for an email that fails plausiblePublicEmail; the web forms show
// the same sentence.
const publicEmailFormatProblem = "email: enter an email address like name@example.com"

// handlePublicHealthz is a bare liveness check, deliberately cheaper than the admin handleHealthz: it
// touches neither the database nor CONFIG_DIR. The full readiness probe is not exposed here: it
// would let an anonymous caller contend the single SQLite connection, churn CONFIG_DIR, and read raw
// error text (paths, driver errors). The Docker HEALTHCHECK probes the admin port, not this one.
func handlePublicHealthz(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "ok")
}

// countOpenRequests counts non-finalized quote_requests rows, the population publicMaxOpenRequests
// bounds. Finalized rows are ordinary retained records. The quote_requests_status index (db.go) backs
// this and pruneFinalizedGcode.
func countOpenRequests(db *sql.DB) (int, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM quote_requests WHERE status != 'finalized'`).Scan(&n)
	return n, err
}

// errPublicStorageFull is returned by writeGcodePart when CONFIG_DIR/uploads is over
// publicUploadsCapBytes, so the caller can tell it from other write failures (500). The handler
// degrades instead of failing: it finishes the submission without a stored file (fileStored: false)
// rather than discarding the visitor's estimate.
var errPublicStorageFull = errors.New("public quote-request: uploads storage full")

// publicQuoteFormFields are the non-file field names handlePublicQuoteRequest reads. Others are drained
// and ignored. priceCents is deliberately absent: the server computes its own price at insert time.
var publicQuoteFormFields = map[string]struct{}{
	"name": {}, "email": {}, "note": {}, "material": {},
	"quantity": {}, "weightG": {}, "timeSeconds": {},
}

// lastForwardedValue extracts the single trusted value from every header line named name (used by
// clientIP and requestIsTLS). Lines are read via h.Values and joined with commas, and the LAST element
// is returned, so an intermediary appending a separate header line is still caught. The one trusted
// proxy appends to what it received, so everything left of the final hop is client-controlled; this
// breaks down with two or more trusted appending hops. Returns "" for an absent header or empty last
// element, so callers fall back to their default. Whether to consult it (trustProxy) stays with callers.
func lastForwardedValue(h http.Header, name string) string {
	joined := strings.Join(h.Values(name), ",")
	if joined == "" {
		return ""
	}
	parts := strings.Split(joined, ",")
	return strings.TrimSpace(parts[len(parts)-1])
}

// clientIP resolves the rate-limit key for r. X-Forwarded-For is consulted ONLY when trustProxy is set
// (PUBLIC_TRUST_PROXY=1: exactly one trusted proxy appends the client address as the last element);
// on a directly-exposed listener it is attacker-controlled and would let a client rotate fabricated
// values to bypass the limit and bloat the limiter map. The LAST element is read, not the first,
// since everything left of the final hop is client-controlled. With two or more trusted appending
// hops every visitor collapses to the nearest hop's address, so set trust only behind a single proxy.
// The default is the socket's host. The result goes through rateLimitKey (IPv6 keys by network).
func clientIP(r *http.Request, trustProxy bool) string {
	// An Access-verified admin request is keyed on CF-Connecting-IP. The token proves only Access
	// authorization, not that it came through Cloudflare (see cfConnectingIP). Never true on the public one.
	if ip := cfConnectingIP(r); ip != "" {
		return rateLimitKey(ip)
	}
	if trustProxy {
		if last := lastForwardedValue(r.Header, "X-Forwarded-For"); last != "" {
			return rateLimitKey(last)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return rateLimitKey(r.RemoteAddr)
	}
	return rateLimitKey(host)
}

// rateLimitKey normalizes a host into a rate-limit key by network: an ordinary IPv6 allocation is a /64,
// so exact-address keying let one machine mint unlimited budgets by rotating within its /64 and evict
// real entries from the LRU. IPv4 keys by exact address. An unparsable value is keyed verbatim.
//
// This raises the evasion cost only: a holder of several /64s can still mint budgets faster than the
// LRU retains old ones, and no smaller key space would avoid punishing legitimate visitors sharing a
// /64 or CGNAT. It is best-effort damping; publicMaxOpenRequests and DELETE /api/requests/new are the
// backstops that bound and recover from that case.
func rateLimitKey(host string) string {
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	addr = addr.Unmap() // IPv4-mapped IPv6 (::ffff:a.b.c.d) counts as IPv4
	if addr.Is4() {
		return addr.String()
	}
	prefix, err := addr.Prefix(64)
	if err != nil {
		return addr.String()
	}
	return prefix.String()
}

// publicMaterial is the narrow public shape: name and density only. Cost-model inputs are server-side
// now. Density is sent so the page can turn a filament length or model volume into weight.
type publicMaterial struct {
	Name    string  `json:"name"`
	Density float64 `json:"density"`
}

// publicConfigResponse is assembled field by field in handlePublicConfig, never by serializing Config
// wholesale, so a new config field can't leak by default. It carries materials/printer (narrowed to
// client-side file-parsing fields), currency, and display-only/operational fields; cost-model fields
// are computed by POST /api/public/quote/preview.
type publicConfigResponse struct {
	Branding struct {
		BusinessName string `json:"businessName"`
		LogoURL      string `json:"logoUrl"`
		// Email/Phone/Address feed the public "Questions?" footer. The owner already publishes them on every
		// printed quote and invoice, so serving them discloses nothing new.
		Email   string `json:"email"`
		Phone   string `json:"phone"`
		Address string `json:"address"`
	} `json:"branding"`
	// StaffUrl is the Staff sign-in target in the shop footer, "" to hide the link. It depends on the
	// listener answering (staffURLFunc): /admin/ on the main port, site.adminUrl on PUBLIC_PORT.
	StaffUrl  string           `json:"staffUrl"`
	IntroText string           `json:"introText"`
	Currency  Currency         `json:"currency"`
	Materials []publicMaterial `json:"materials"`
	// FilamentDiameterMm is the one printer field the client still needs, for weight/time estimation from
	// a parsed model. It is top-level because a wrapper object would invite cost-model fields back in.
	FilamentDiameterMm float64 `json:"filamentDiameterMm"`
	// MaxUploadBytes is publicMaxUploadBytes verbatim, so the page warns before a doomed upload (a late 413).
	MaxUploadBytes int `json:"maxUploadBytes"`
	// DemoMode lets the page show a "nothing is saved" banner up front (demo submissions insert no row).
	DemoMode bool `json:"demoMode"`
	// MaxOrderQty is shop.maxOrderQty: the largest quantity one estimate request or gallery order may carry.
	MaxOrderQty int `json:"maxOrderQty"`
	// Shop is the storefront settings the page needs, built field by field by publicShopFromConfig.
	Shop publicShop `json:"shop"`
}

// publicShop is the public-safe projection of Config.Shop. Plain text only: the page renders every string as
// text. Nothing here is a secret; the hero image is served from /item-images/ like item photos.
type publicShop struct {
	CustomEstimates bool `json:"customEstimates"`
	Gallery         bool `json:"gallery"`
	AcceptingOrders bool `json:"acceptingOrders"`
	// PausedMessage is the owner's message, or a default when it is blank. Meaningful when AcceptingOrders is false.
	PausedMessage string `json:"pausedMessage"`
	// DefaultView is the view the shop opens on: "estimate" only when estimates are on and either the owner
	// chose it or the gallery is off; otherwise "gallery".
	DefaultView string `json:"defaultView"`
	// PageTitle is the resolved shop title (Config.shopTitle): shop.pageTitle, else the business name, else
	// "Shop". The page composes its tab title from it, and the server puts it in the <title> of the shop HTML.
	PageTitle    string           `json:"pageTitle"`
	Heading      string           `json:"heading"`
	Announcement string           `json:"announcement"`
	HeroImage    *publicHeroImage `json:"heroImage"`
	// Accent is null for the theme accent. Both schemes carry the button label color that passes contrast.
	Accent         *publicAccent    `json:"accent"`
	ShowStock      bool             `json:"showStock"`
	ShowFromPrice  bool             `json:"showFromPrice"`
	ItemSort       string           `json:"itemSort"`
	FooterLinks    []ShopFooterLink `json:"footerLinks"`
	ShowContact    bool             `json:"showContact"`
	ShippingNote   string           `json:"shippingNote"`
	TurnaroundNote string           `json:"turnaroundNote"`
}

type publicHeroImage struct {
	URL string `json:"url"`
	Alt string `json:"alt"`
}

type publicAccentScheme struct {
	Accent     string `json:"accent"`
	ButtonText string `json:"buttonText"`
}

type publicAccent struct {
	Light publicAccentScheme `json:"light"`
	Dark  publicAccentScheme `json:"dark"`
}

// publicShopFromConfig projects cfg.Shop. The accent is re-checked here, so a hand-edited config.yaml that
// slipped a failing color past an older validate never reaches a page: it falls back to the theme accent.
func publicShopFromConfig(cfg *Config) publicShop {
	s := cfg.Shop
	paused := s.PausedMessage
	if paused == "" {
		paused = defaultShopPausedMessage
	}
	view := "gallery"
	if s.CustomEstimates && (s.DefaultView == "estimate" || !s.Gallery) {
		view = "estimate"
	}
	out := publicShop{
		CustomEstimates: s.CustomEstimates,
		Gallery:         s.Gallery,
		AcceptingOrders: s.AcceptingOrders,
		PausedMessage:   paused,
		DefaultView:     view,
		PageTitle:       cfg.shopTitle(),
		Heading:         s.Heading,
		Announcement:    s.Announcement,
		ShowStock:       s.ShowStock,
		ShowFromPrice:   s.ShowFromPrice,
		ItemSort:        s.ItemSort,
		FooterLinks:     append([]ShopFooterLink{}, s.FooterLinks...),
		ShowContact:     s.ShowContact,
		ShippingNote:    s.ShippingNote,
		TurnaroundNote:  s.TurnaroundNote,
	}
	if s.HeroImage.File != "" && itemImageName.MatchString(s.HeroImage.File) {
		out.HeroImage = &publicHeroImage{URL: "/item-images/" + s.HeroImage.File, Alt: s.HeroImage.Alt}
	}
	if s.AccentColor != "" {
		if res := checkAccentColor(s.AccentColor); res.OK {
			out.Accent = &publicAccent{
				Light: publicAccentScheme{Accent: res.Light.Accent, ButtonText: res.Light.ButtonText},
				Dark:  publicAccentScheme{Accent: res.Dark.Accent, ButtonText: res.Dark.ButtonText},
			}
		}
	}
	return out
}

// shopKind is which customer submission a request is, for shopRejects.
type shopKind int

const (
	shopKindEstimate shopKind = iota // preview or submit of a custom estimate
	shopKindItemOrder
)

// shopRejects reports whether cfg's storefront settings forbid this kind of submission, and writes the
// response when they do. A feature that is switched off answers 404 with a generic message, revealing
// nothing about the setting. Vacation mode answers 503 with the owner's message and "paused": true so the
// page can show it; requests already stored are unaffected. The checks run before rate limiting and body
// parsing, so a rejected request costs almost nothing.
func shopRejects(w http.ResponseWriter, cfg *Config, kind shopKind) bool {
	if (kind == shopKindEstimate && !cfg.Shop.CustomEstimates) || (kind == shopKindItemOrder && !cfg.Shop.Gallery) {
		writeAPIError(w, http.StatusNotFound, "not available")
		return true
	}
	if !cfg.Shop.AcceptingOrders {
		msg := cfg.Shop.PausedMessage
		if msg == "" {
			msg = defaultShopPausedMessage
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(struct {
			Error   string   `json:"error"`
			Errors  []string `json:"errors"`
			Paused  bool     `json:"paused"`
			Message string   `json:"pausedMessage"`
		}{Error: msg, Errors: []string{msg}, Paused: true, Message: msg})
		return true
	}
	return false
}

// staffURLFunc computes the shop footer's Staff sign-in target for one listener from the live config.
type staffURLFunc func(cfg *Config) string

// mainStaffURL is the main port's target: the admin app under the same host, when site.staffLink is on.
func mainStaffURL(cfg *Config) string {
	if cfg.Site.StaffLink {
		return "/admin/"
	}
	return ""
}

// publicPortStaffURL is PUBLIC_PORT's target: site.adminUrl, since the admin app isn't served there.
func publicPortStaffURL(cfg *Config) string {
	return cfg.Site.AdminURL
}

// buildPublicConfig assembles the public config for one Config snapshot. GET /api/public/config serves it and
// the shop HTML embeds it (renderShopHTML), so the two can never disagree about what is public.
func buildPublicConfig(cfg *Config, demoMode bool, staffURL staffURLFunc) publicConfigResponse {
	materials := []publicMaterial{}
	for _, m := range cfg.Materials {
		if !m.Public {
			continue
		}
		materials = append(materials, publicMaterial{
			Name:    m.Name,
			Density: m.Density,
		})
	}

	var printer Printer
	for _, p := range cfg.Printers {
		if p.ID == cfg.PublicQuotes.Printer {
			printer = p
			break
		}
	}

	resp := publicConfigResponse{
		StaffUrl:           staffURL(cfg),
		IntroText:          cfg.PublicQuotes.IntroText,
		Currency:           cfg.Currency,
		Materials:          materials,
		FilamentDiameterMm: printer.FilamentDiameterMm,
		MaxUploadBytes:     publicMaxUploadBytes,
		DemoMode:           demoMode,
		MaxOrderQty:        cfg.maxOrderQty(),
		Shop:               publicShopFromConfig(cfg),
	}
	resp.Branding.BusinessName = cfg.Quote.BusinessName
	resp.Branding.LogoURL = cfg.Quote.LogoURL
	// shop.showContact off means the details are not served at all, not merely hidden in the footer.
	if cfg.Shop.ShowContact {
		resp.Branding.Email = cfg.Quote.Email
		resp.Branding.Phone = cfg.Quote.Phone
		resp.Branding.Address = cfg.Quote.Address
	}
	return resp
}

// handlePublicConfig serves the Config subset the public page needs; prices: POST /api/public/quote/preview.
func handlePublicConfig(store *ConfigStore, demoMode bool, staffURL staffURLFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")

		resp := buildPublicConfig(store.Get(), demoMode, staffURL) // one snapshot for this request
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			log.Printf("public config encode: %v", err)
		}
	}
}

// publicConfigFunc returns the public config for the current settings snapshot.
type publicConfigFunc func() publicConfigResponse

// renderShopHTML fills the shop's head from the live settings (D30), so a hard refresh paints the owner's
// own title, description and config, never built-in defaults: <title> is the shop title, the description meta
// is the intro as plain text, markup stripped (when there is one), and the config is embedded as a JSON data block the page reads
// synchronously. The block is type="application/json", which the browser never executes, so the CSP needs no
// unsafe-inline. json.Marshal escapes <, > and & (and U+2028/2029), so no value can close the script element
// or open a comment. It returns false when the template has no <title>..</title> to fill.
func renderShopHTML(tmpl string, cfg publicConfigResponse) ([]byte, bool) {
	start := strings.Index(tmpl, "<title>")
	if start < 0 {
		return nil, false
	}
	end := strings.Index(tmpl[start:], "</title>")
	if end < 0 {
		return nil, false
	}
	end += start + len("</title>")
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, false
	}
	var b strings.Builder
	b.Grow(len(tmpl) + len(data) + 256)
	b.WriteString(tmpl[:start])
	b.WriteString("<title>" + html.EscapeString(cfg.Shop.PageTitle) + "</title>")
	if intro := lightMarkdownToPlain(cfg.IntroText); intro != "" {
		b.WriteString(`<meta name="description" content="` + html.EscapeString(intro) + `">`)
	}
	b.WriteString(`<script type="application/json" id="shop-config">`)
	b.Write(data)
	b.WriteString(`</script>`)
	b.WriteString(tmpl[end:])
	return []byte(b.String()), true
}

// handlePublicCustomCss serves the top-level customCss config key verbatim as a stylesheet, registered
// on BOTH listeners: public at GET /custom.css (web/public.html) and admin at the same path
// (web/index.html with media="print", styling printed quotes and invoices). It reads a fresh
// store.Get() per request, so a settings save applies on the next load.
//
// The value is served unescaped and unsanitized on purpose: see Config.CustomCss for why there is no
// injection boundary, only the validate() byte cap. An empty value still gets a 200 with an empty
// body, since both pages link it unconditionally and a 404 is console noise.
//
// Cache-Control is "no-cache" (always revalidate), like publicIndexHandler for "/", not no-store: no
// per-visitor state or PII. nosniff is not set here; securityHeadersMiddleware already sets it.
func handlePublicCustomCss(store *ConfigStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := store.Get() // one snapshot for this request
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		io.WriteString(w, cfg.CustomCss)
	}
}

// publicItemStock is the narrow stock projection of Item's StockMode/StockQty/AllowBackorder. Qty and
// AllowBackorder have no omitempty: the frontend must tell "0 left" and backorder=false from "doesn't apply".
type publicItemStock struct {
	Mode           string `json:"mode"` // "ready_to_print" | "in_stock"
	Qty            int    `json:"qty"`  // only meaningful for in_stock
	AllowBackorder bool   `json:"allowBackorder"`
}

// publicItem is the narrow shape served per public item: no imageFile (ImageURL is derived) and none
// of the admin-only bookkeeping fields (public, createdAt, updatedAt, sortOrder).
type publicItem struct {
	ID           string              `json:"id"`
	Name         string              `json:"name"`
	Description  string              `json:"description"`
	PriceCents   int                 `json:"priceCents"`
	OptionGroups []publicOptionGroup `json:"optionGroups"`
	ImageURL     string              `json:"imageUrl"` // "/item-images/{file}" or ""
	Stock        publicItemStock     `json:"stock"`
	// Category is null for an item with no category or one in a hidden category (it still shows under All).
	Category *publicCategoryRef `json:"category"`
	Featured bool               `json:"featured"`
}

// publicCategoryRef is the category an item belongs to, as the shop's chips and #/category/<slug> use it.
type publicCategoryRef struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

// publicOptionGroup and publicOption are ItemOptionGroup and ItemOption without imageFile: the photo is
// served as ImageURL ("/item-images/{file}" or "").
type publicOptionGroup struct {
	ID       string         `json:"id"`
	Label    string         `json:"label"`
	Required bool           `json:"required"`
	Options  []publicOption `json:"options"`
}

type publicOption struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	DeltaCents int    `json:"deltaCents"`
	ImageURL   string `json:"imageUrl"`
}

func toPublicOptionGroups(groups []ItemOptionGroup) []publicOptionGroup {
	out := make([]publicOptionGroup, 0, len(groups))
	for _, g := range groups {
		pg := publicOptionGroup{ID: g.ID, Label: g.Label, Required: g.Required, Options: make([]publicOption, 0, len(g.Options))}
		for _, o := range g.Options {
			url := ""
			if o.ImageFile != "" {
				url = "/item-images/" + o.ImageFile
			}
			pg.Options = append(pg.Options, publicOption{ID: o.ID, Name: o.Name, DeltaCents: o.DeltaCents, ImageURL: url})
		}
		out = append(out, pg)
	}
	return out
}

// publicItemsCacheTTL bounds how long handlePublicItems serves its cached response. This is the only
// anonymous, unbounded, DB-touching public route, and the single SQLite connection is scarce (see
// handlePublicHealthz). A var so tests can shrink it.
var publicItemsCacheTTL = 5 * time.Second

// publicItemsNow is the clock publicItemsCache reads; a var so tests fake expiry instead of sleeping.
var publicItemsNow = time.Now

// publicItemsCache holds the last serialized response for publicItemsCacheTTL. One per handlePublicItems
// closure (not shared) so concurrent requests share it and tests get isolation. The body is only valid for
// the item sort it was built with, so a settings change shows on the next request.
type publicItemsCache struct {
	mu      sync.Mutex
	body    []byte
	sort    string
	expires time.Time
}

// get returns the cached body and true when it is still within TTL and was built for sort.
func (c *publicItemsCache) get(sort string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.body != nil && c.sort == sort && publicItemsNow().Before(c.expires) {
		return c.body, true
	}
	return nil, false
}

// set stores body for sort, valid for publicItemsCacheTTL.
func (c *publicItemsCache) set(sort string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.body = body
	c.sort = sort
	c.expires = publicItemsNow().Add(publicItemsCacheTTL)
}

// publicItemsOrderBy is the ORDER BY for shop.itemSort. Featured items come first in every mode, so they
// lead whichever category chip is selected.
func publicItemsOrderBy(sort string) string {
	switch sort {
	case "name":
		return "featured DESC, name COLLATE NOCASE, sort_order, id"
	case "price":
		return "featured DESC, price_cents, name COLLATE NOCASE, id"
	default:
		return "featured DESC, sort_order, name, id"
	}
}

// publicItemsResponse is the GET /api/public/items body. Categories lists the visible categories that have
// at least one public item, in shop order.
type publicItemsResponse struct {
	Items      []publicItem        `json:"items"`
	Categories []publicCategoryRef `json:"categories"`
}

// handlePublicItems serves every public item, featured first and then in shop.itemSort order, narrowed to
// publicItem, cached for publicItemsCacheTTL so a burst costs at most one query per window. With the gallery
// off it serves no items.
func handlePublicItems(db *sql.DB, store *ConfigStore) http.HandlerFunc {
	cache := &publicItemsCache{}
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := store.Get()
		w.Header().Set("Content-Type", "application/json")
		if !cfg.Shop.Gallery {
			w.Write([]byte(`{"items":[],"categories":[]}`))
			return
		}
		sortMode := cfg.Shop.ItemSort
		if body, ok := cache.get(sortMode); ok {
			w.Write(body)
			return
		}

		w.Header().Del("Content-Type")
		rows, err := db.Query(`SELECT ` + itemColumns + ` FROM items WHERE public = 1 ORDER BY ` + publicItemsOrderBy(sortMode))
		if err != nil {
			log.Printf("public items query: %v", err)
			writeAPIError(w, http.StatusInternalServerError, "items unavailable")
			return
		}
		defer rows.Close()

		var raw []Item
		for rows.Next() {
			it, err := scanItem(rows)
			if err != nil {
				log.Printf("public items scan: %v", err)
				writeAPIError(w, http.StatusInternalServerError, "items unavailable")
				return
			}
			raw = append(raw, it)
		}
		if err := rows.Err(); err != nil {
			log.Printf("public items read: %v", err)
			writeAPIError(w, http.StatusInternalServerError, "items unavailable")
			return
		}
		rows.Close() // the pool is one connection: finish this result before the next query

		cats, err := listCategories(db)
		if err != nil {
			log.Printf("public items categories: %v", err)
			writeAPIError(w, http.StatusInternalServerError, "items unavailable")
			return
		}
		visible := map[string]publicCategoryRef{}
		for _, c := range cats {
			if !c.Hidden {
				visible[c.ID] = publicCategoryRef{Slug: c.Slug, Name: c.Name}
			}
		}

		// Empty, not nil, so no public items serializes "items":[] not null.
		items := []publicItem{}
		used := map[string]bool{}
		for _, it := range raw {
			imageURL := ""
			if it.ImageFile != "" {
				imageURL = "/item-images/" + it.ImageFile
			}
			var cat *publicCategoryRef
			if ref, ok := visible[it.CategoryID]; ok {
				c := ref
				cat = &c
				used[it.CategoryID] = true
			}
			items = append(items, publicItem{
				ID:           it.ID,
				Name:         it.Name,
				Description:  it.Description,
				PriceCents:   it.PriceCents,
				OptionGroups: toPublicOptionGroups(it.OptionGroups),
				ImageURL:     imageURL,
				Stock: publicItemStock{
					Mode:           it.StockMode,
					Qty:            it.StockQty,
					AllowBackorder: it.AllowBackorder,
				},
				Category: cat,
				Featured: it.Featured,
			})
		}
		chips := []publicCategoryRef{}
		for _, c := range cats {
			if used[c.ID] {
				chips = append(chips, visible[c.ID])
			}
		}

		body, err := json.Marshal(publicItemsResponse{Items: items, Categories: chips})
		if err != nil {
			log.Printf("public items encode: %v", err)
			writeAPIError(w, http.StatusInternalServerError, "items unavailable")
			return
		}
		cache.set(sortMode, body)

		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}
}

// publicItemOrderMaxBytes caps the JSON body (no file part here); 16 KiB is far more than needed.
const publicItemOrderMaxBytes = 16 * 1024

// quoteRequestsEmailMaxLen bounds quote_requests.email. Both writers (handlePublicItemOrder and
// handlePublicQuoteRequest) must enforce the same length, or one endpoint accepts what the other
// rejects. 320 is the RFC 5321/5322 maximum address length; the column is unbounded TEXT.
const quoteRequestsEmailMaxLen = 320

// publicItemOrderRequest is the POST /api/public/item-order body. DisallowUnknownFields rejects any
// other field, a client-supplied price above all: the server is the only price authority.
type publicItemOrderRequest struct {
	ItemID     string            `json:"itemId"`
	Selections []optionSelection `json:"selections"`
	Quantity   int               `json:"quantity"`
	Name       string            `json:"name"`
	Email      string            `json:"email"`
	Note       string            `json:"note"`
}

// writePublicOrderAccepted writes an order's 201, kept apart from writePublicQuoteAccepted's shape.
func writePublicOrderAccepted(w http.ResponseWriter, id, reference string, priceCents int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(struct {
		ID         string `json:"id"`
		Reference  string `json:"reference"`
		PriceCents int    `json:"priceCents"`
		Message    string `json:"message"`
	}{ID: id, Reference: reference, PriceCents: priceCents, Message: "Order request received, subject to review"})
}

// handlePublicItemOrder stores a visitor's order against one public item. Pricing is server-authoritative:
// the item is reloaded (getItem) and total = (item.PriceCents + Σ selected option deltas) * quantity
// is computed from that row; DisallowUnknownFields rejects a client priceCents.
//
// Order mirrors handlePublicQuoteRequest: rate limit, decode/validate, load the item (not found and
// not public give the same "item: no longer available", so a scan can't tell them apart), options,
// stock, price, persist, notify.
//
// The stock check is skipped for in_stock items with AllowBackorder: quantity is already bounded by
// maxItemOrderQty, as for ready_to_print items.
//
// demoMode runs every validation and the price computation, but inserts nothing and queues no notification.
func handlePublicItemOrder(db *sql.DB, store *ConfigStore, rl *rateLimiter, trustProxy bool, nq *notifyQueue, demoMode bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := store.Get() // one snapshot for this request
		if shopRejects(w, cfg, shopKindItemOrder) {
			return
		}
		if !rl.allow(clientIP(r, trustProxy), publicRateLimit, publicRateWindow) {
			writeAPIError(w, http.StatusTooManyRequests, "too many requests, try again later")
			return
		}

		// Same guard as handlePublicQuoteRequest: orders are quote_requests rows counted by publicMaxOpenRequests.
		if open, err := countOpenRequests(db); err != nil {
			log.Printf("public item-order: count open requests: %v", err)
			writeAPIError(w, http.StatusInternalServerError, "requests not writable")
			return
		} else if open >= publicMaxOpenRequests {
			writeAPIError(w, http.StatusServiceUnavailable, "too many pending requests, try again later")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, publicItemOrderMaxBytes)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var in publicItemOrderRequest
		if err := dec.Decode(&in); err != nil {
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				writeAPIError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			// Never echo err.Error() to a visitor (it can include the bind address); log it, return a fixed message.
			log.Printf("public item-order: decode: %v", err)
			writeAPIError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		name := strings.TrimSpace(in.Name)
		email := strings.TrimSpace(in.Email)
		note := strings.TrimSpace(in.Note)

		var problems []string
		if name == "" {
			problems = append(problems, "name: required")
		} else if len(name) > 200 {
			problems = append(problems, "name: max 200 characters")
		}
		if email == "" {
			problems = append(problems, "email: required")
		} else if len(email) > quoteRequestsEmailMaxLen {
			problems = append(problems, fmt.Sprintf("email: max %d characters", quoteRequestsEmailMaxLen))
		} else if !plausiblePublicEmail(email) {
			problems = append(problems, publicEmailFormatProblem)
		} else if !plainEmailForMailto(email) {
			problems = append(problems, "email: contains characters that are not allowed")
		}
		if len(note) > 2000 {
			problems = append(problems, "note: max 2000 characters")
		}
		if in.Quantity < 1 || in.Quantity > cfg.maxOrderQty() {
			problems = append(problems, fmt.Sprintf("quantity: must be an integer 1-%d", cfg.maxOrderQty()))
		}

		item, found, err := getItem(db, in.ItemID)
		if err != nil {
			log.Printf("public item-order: load item: %v", err)
			writeAPIError(w, http.StatusInternalServerError, "items unavailable")
			return
		}

		// Not found and "not public" give the same message so an itemId scan can't detect unlisted items. Return
		// early with the problems accumulated so far, since there is no item to check options or stock against.
		if !found || !item.Public {
			problems = append(problems, "item: no longer available")
			writeAPIError(w, http.StatusUnprocessableEntity, problems...)
			return
		}

		delta, optionName, selProblems := resolveSelections(item.OptionGroups, in.Selections)
		problems = append(problems, selProblems...)

		if item.StockMode == stockModeInStock && !item.AllowBackorder {
			// Check-then-insert race accepted by design: concurrent orders can both pass against the same on-hand
			// quantity. Nothing decrements stock_qty (an order is a review request, not a sale), so an oversell
			// just shows the owner more requests than stock when finalizing.
			if item.StockQty <= 0 {
				problems = append(problems, "item: no longer available")
			} else if in.Quantity > item.StockQty {
				problems = append(problems, fmt.Sprintf("quantity: only %d in stock", item.StockQty))
			}
		}
		// in_stock with AllowBackorder: no stock check; the cap is maxItemOrderQty, already validated.

		if len(problems) > 0 {
			writeAPIError(w, http.StatusUnprocessableEntity, problems...)
			return
		}

		total := (item.PriceCents + delta) * in.Quantity
		id := newID()

		if demoMode {
			// Simulate the rest: same 201 and synthesized id, but no row and no notification.
			writePublicOrderAccepted(w, id, peekRequestReference(db, referencePrefix(cfg)), total)
			return
		}

		now := time.Now().UTC().Format(time.RFC3339)
		reference, err := insertRequestWithReference(db, cfg, `INSERT INTO quote_requests
		  (id, status, kind, name, email, note, material, quantity, weight_g, time_seconds,
		   price_cents, gcode_filename, gcode_path, gcode_bytes, created_at,
		   item_id, item_name, option_name, tax_inclusive, reference)
		  VALUES (?, 'new', 'item', ?, ?, ?, '', ?, 0, 0, ?, '', '', 0, ?, ?, ?, ?, 0, ?)`,
			id, name, email, note, in.Quantity, total, now, item.ID, item.Name, optionName)
		if err != nil {
			log.Printf("public item-order: insert: %v", err)
			writeAPIError(w, http.StatusInternalServerError, "requests not writable")
			return
		}

		// Owner notification, queued like handlePublicQuoteRequest's (see notifyQueue): fire-and-forget.
		nq.enqueue(cfg, QuoteRequest{
			ID: id, Status: "new", Kind: "item", Name: name, Email: email, Note: note,
			Quantity: in.Quantity, PriceCents: total, CreatedAt: now,
			ItemID: item.ID, ItemName: item.Name, OptionName: optionName, Reference: reference,
		})

		writePublicOrderAccepted(w, id, reference, total)
	}
}

// splitPublicSeconds ports web/src/lib/gcodeTime.ts's splitSeconds: round at the minute level first, then
// carry into hours, so [3570, 3600) seconds becomes 1h 0m, not 0h 60m (failing timeMinutes <= 59).
func splitPublicSeconds(timeSeconds int) (hours, minutes float64) {
	mins := int(math.Round(float64(timeSeconds) / 60))
	return float64(mins / 60), float64(mins % 60)
}

// buildPublicQuoteInputs mirrors web/src/lib/quoteInputs.ts's buildQuoteInputs plus PublicQuotePage's
// field mapping, so the server recomputes the exact price a well-behaved browser did. It always uses
// the owner's cfg.PublicQuotes.Printer/Preset, never a visitor-chosen one.
//   - energy uses cfg.Energy's default rates; visitors can't override them.
//   - pricing.shippingCharge/shippingCost are forced to 0: the page shows shipping as a separate
//     line, never folded into engine revenue.
//   - laborPerUnit comes from cfg.Pricing.LaborPerUnit; the failure base is always the consumables one
//     (DefaultEngineOptions, like the admin Quote tab).
func buildPublicQuoteInputs(cfg *Config, material Material, quantity int, weightG float64, timeSeconds int) (QuoteInputs, EngineOptions) {
	var printer Printer
	for _, p := range cfg.Printers {
		if p.ID == cfg.PublicQuotes.Printer {
			printer = p
			break
		}
	}
	preset := cfg.Presets[cfg.PublicQuotes.Preset]

	hours, minutes := splitPublicSeconds(timeSeconds)

	var energy EnergyInputs
	if cfg.Energy.Mode == "tou" {
		energy = EnergyInputs{
			Mode:        "tou",
			PeakRate:    OptF{Value: cfg.Energy.Tou.PeakRate, Present: true},
			OffPeakRate: OptF{Value: cfg.Energy.Tou.OffPeakRate, Present: true},
			OffPeakFrac: OptF{Value: cfg.Energy.Tou.DefaultOffPeakFrac, Present: true},
		}
	} else if cfg.Energy.Mode == "schedule" {
		// A visitor can't choose a start, so the quote prices from the next occurrence of the shop's default
		// start time at request time.
		sched := cfg.Energy.Schedule
		energy = EnergyInputs{
			Mode:     "schedule",
			Schedule: &sched,
			Timezone: cfg.Timezone,
			Start:    nextStartWall(sched.DefaultStartTime, cfg.Location(), time.Now()),
		}
	} else {
		energy = EnergyInputs{Mode: "flat", FlatRate: OptF{Value: cfg.Energy.FlatRate, Present: true}}
	}

	qi := QuoteInputs{
		WeightG:          F(weightG),
		PricePerKg:       F(material.PricePerKg),
		WastePct:         F(material.WastePct),
		TimeHours:        F(hours),
		TimeMinutes:      F(minutes),
		Watts:            F(printer.Watts),
		Energy:           energy,
		FailureRatePct:   F(preset.FailureRatePct),
		PrepMinutes:      F(preset.PrepMinutes),
		LaborRate:        F(preset.LaborRate),
		MachineCost:      F(printer.PurchaseCost),
		MachineLifeHours: F(printer.LifespanHours),
		MonthlyFixed:     F(preset.MonthlyFixed),
		MonthlyHours:     F(preset.MonthlyHours),
		Quantity:         F(float64(quantity)),
		Pricing: &PricingInputs{
			Strategy:         cfg.Pricing.Strategy,
			MarginPct:        OptF{Value: cfg.Pricing.MarginPct, Present: true},
			MarkupMultiplier: OptF{Value: cfg.Pricing.MarkupMultiplier, Present: true},
			TaxRatePct:       F(cfg.Pricing.TaxRatePct),
			ShippingCharge:   F(0),
			ShippingCost:     F(0),
		},
	}
	opt := EngineOptions{LaborPerUnit: cfg.Pricing.LaborPerUnit}
	return qi, opt
}

// publicQuotePreviewMaxBytes caps the JSON body: four small fields, like publicItemOrderMaxBytes.
const publicQuotePreviewMaxBytes = 2 * 1024

// publicPreviewRateLimit/publicPreviewRateWindow bound POST /api/public/quote/preview on its OWN per-IP
// budget (handlePublicQuotePreview gets a dedicated rateLimiter): the page calls it on every debounced
// keystroke, so sharing the per-hour submission budget would starve real submissions. 60/min is generous
// for a ~350ms debounce yet bounds direct hammering.
const (
	publicPreviewRateLimit  = 60
	publicPreviewRateWindow = time.Minute
)

// publicQuotePreviewRequest is the POST /api/public/quote/preview body, a frozen shape the web frontend
// codes against. DisallowUnknownFields rejects other fields; there is no price field, since this
// endpoint is the price authority.
type publicQuotePreviewRequest struct {
	Material    string  `json:"material"`
	Quantity    int     `json:"quantity"`
	WeightG     float64 `json:"weightG"`
	TimeSeconds int     `json:"timeSeconds"`
}

// publicQuotePreviewResponse is the 200 body (frozen): priceCents is the batch total (resolvedPrice x
// quantity), shippingCents the owner's flat customer shipping charge, pricing.defaultShippingCharge (never folded into priceCents), and currency
// lets the page format both without another round trip.
type publicQuotePreviewResponse struct {
	PriceCents    int      `json:"priceCents"`
	ShippingCents int      `json:"shippingCents"`
	Currency      Currency `json:"currency"`
}

// handlePublicQuotePreview computes a price estimate for an in-progress form without persisting
// anything: the same validation and engine call (buildPublicQuoteInputs + ComputeQuote) as submission,
// so a preview and the stored price can't disagree. It behaves the same in DEMO_MODE since it writes nothing.
func handlePublicQuotePreview(store *ConfigStore, rl *rateLimiter, trustProxy bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := store.Get() // one snapshot for this request
		if shopRejects(w, cfg, shopKindEstimate) {
			return
		}
		if !rl.allow(clientIP(r, trustProxy), publicPreviewRateLimit, publicPreviewRateWindow) {
			writeAPIError(w, http.StatusTooManyRequests, "too many requests, try again later")
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, publicQuotePreviewMaxBytes)
		dec := json.NewDecoder(r.Body)
		dec.DisallowUnknownFields()
		var in publicQuotePreviewRequest
		if err := dec.Decode(&in); err != nil {
			if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
				writeAPIError(w, http.StatusRequestEntityTooLarge, "request body too large")
				return
			}
			// Never echo err.Error() to an anonymous visitor (see handlePublicItemOrder).
			log.Printf("public quote-preview: decode: %v", err)
			writeAPIError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		material := strings.TrimSpace(in.Material)

		// Same material lookup and numeric caps as handlePublicQuoteRequest, so preview and submission agree.
		// encoding/json rejects bare NaN/Inf at decode time, so in.WeightG is finite here; the multipart path
		// uses strconv.ParseFloat, which accepts "NaN"/"Inf", and must reject them explicitly.
		var problems []string
		var matchedMaterial Material
		materialIsPublic := false
		for _, m := range cfg.Materials {
			if m.Public && m.Name == material {
				matchedMaterial = m
				materialIsPublic = true
				break
			}
		}
		if material == "" {
			problems = append(problems, "material: required")
		} else if !materialIsPublic {
			problems = append(problems, "material: no longer offered; choose another")
		}
		if in.Quantity < 1 || in.Quantity > cfg.maxOrderQty() {
			problems = append(problems, fmt.Sprintf("quantity: must be an integer 1-%d", cfg.maxOrderQty()))
		}
		if in.WeightG < 0 || in.WeightG > 100000 {
			problems = append(problems, "weightG: must be 0-100000")
		}
		if in.TimeSeconds < 0 || in.TimeSeconds > maxTimeSeconds {
			problems = append(problems, fmt.Sprintf("timeSeconds: must be 0-%d", maxTimeSeconds))
		}
		if len(problems) > 0 {
			writeAPIError(w, http.StatusUnprocessableEntity, problems...)
			return
		}

		qi, opt := buildPublicQuoteInputs(cfg, matchedMaterial, in.Quantity, in.WeightG, in.TimeSeconds)
		bd := ComputeQuote(qi, opt)
		if !bd.Valid || bd.Pricing == nil || !bd.Pricing.Valid {
			// A config-shaped failure, not a visitor input problem: log the detail, tell the visitor only "try
			// again" since the cause is in the owner's pricing configuration.
			log.Printf("public quote-preview: recompute produced an invalid breakdown for material %q", material)
			writeAPIError(w, http.StatusUnprocessableEntity, "price: we can't price this request right now; try again later or contact the shop")
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(publicQuotePreviewResponse{
			PriceCents:    int(bd.Pricing.Revenue),
			ShippingCents: int(toCents(cfg.Pricing.DefaultShippingCharge)),
			Currency:      cfg.Currency,
		}); err != nil {
			log.Printf("public quote-preview: encode: %v", err)
		}
	}
}

// dirSize sums regular-file sizes under dir; a missing dir counts as 0. dir is configDir/uploads, whose
// "items" subdirectory holds admin item-gallery images, a separate owner-controlled budget. The SkipDir
// below keeps those from eating publicUploadsCapBytes, the budget for anonymous public uploads.
func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if d.Name() == "items" && filepath.Dir(path) == dir {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

// sanitizeGcodeFilename reduces an untrusted upload filename to a safe basename (no separators,
// conservative charset, capped length). The "upload.bin" fallback is extension-neutral because the
// field carries STL, 3MF or g-code uploads.
func sanitizeGcodeFilename(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, `\`, "/"))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.TrimLeft(b.String(), ".")
	if len(out) > 200 {
		out = out[len(out)-200:] // keep the tail so an extension survives truncation
	}
	if out == "" {
		return "upload.bin"
	}
	return out
}

const maxTimeSeconds = 30 * 24 * 3600 // 30 days

// readBoundedField reads at most publicMaxFieldBytes from a non-file part, so no text field grows the
// heap. A value truncated at the cap still fails its length check with the right error.
func readBoundedField(part *multipart.Part) (string, error) {
	data, err := io.ReadAll(io.LimitReader(part, publicMaxFieldBytes))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// gcodeUploadSuffix is the extension of every stored upload; retention.go's sweepOrphanedUploads matches it.
const gcodeUploadSuffix = ".gcode.gz"

// writeGcodePart streams the "gcode" multipart part through gzip straight to configDir/uploads/<id>.gcode.gz,
// never holding the file in the heap. The uploads size cap is enforced before any byte is written. On
// error the returned gzPath (if non-empty) may name a partial file; the caller removes it. Only
// errPublicStorageFull is distinguished from other errors (plain 500).
func writeGcodePart(configDir, id string, part *multipart.Part) (filename, gzPath string, written int64, err error) {
	uploadsDir := filepath.Join(configDir, "uploads")
	total, err := dirSize(uploadsDir)
	if err != nil {
		return "", "", 0, fmt.Errorf("uploads dir size: %w", err)
	}
	if total > publicUploadsCapBytes {
		// Logged here because only this function has dirSize and the cap; the caller returns before logging.
		log.Printf("public quote-request: uploads storage full (dirSize=%d cap=%d)", total, publicUploadsCapBytes)
		return "", "", 0, errPublicStorageFull
	}
	if err := os.MkdirAll(uploadsDir, 0o755); err != nil {
		return "", "", 0, fmt.Errorf("mkdir uploads: %w", err)
	}

	gzPath = filepath.Join(uploadsDir, id+gcodeUploadSuffix)
	outFile, err := os.Create(gzPath)
	if err != nil {
		return "", "", 0, fmt.Errorf("create %s: %w", gzPath, err)
	}
	gz := gzip.NewWriter(outFile)
	copiedBytes, copyErr := io.Copy(gz, part)
	closeErr := gz.Close()
	// fsync before Close: otherwise a power loss after this returns can leave the row's INSERT durably
	// committed while the bytes sit only in the page cache. This syncs the file's data, not the uploads/
	// directory entry, so a crash can still leave a committed row pointing at a missing path; the 404
	// path in handleGcodeDownload covers that. Skipped after an earlier failure; Close still runs.
	var syncErr error
	if copyErr == nil && closeErr == nil {
		syncErr = outFile.Sync()
	}
	fileCloseErr := outFile.Close()
	if copyErr != nil || closeErr != nil || syncErr != nil || fileCloseErr != nil {
		return "", gzPath, 0,
			fmt.Errorf("write gzip: copy=%w close=%v sync=%v closeFile=%v", copyErr, closeErr, syncErr, fileCloseErr)
	}
	return sanitizeGcodeFilename(part.FileName()), gzPath, copiedBytes, nil
}

// publicNotifyQueueCap bounds queued owner notifications. A send can take up to smtpSessionDeadline
// (30s), so the cap stops a burst growing the backlog; excess notifications are dropped and logged.
const publicNotifyQueueCap = 32

// notifyQueue serializes owner notifications through one worker goroutine over a bounded channel. A
// bare `go notifyNewRequest` per submission let anonymous visitors open unbounded concurrent SMTP
// conversations. One worker suffices: notification is best-effort and off the request path.
type notifyQueue struct {
	ch chan notifyJob
	// done is closed once run() returns (ch closed and drained). nil for a test queue that never calls stop.
	done chan struct{}
	// worker is what run() calls per job. Set once at construction, so the goroutine reads it unsynchronized.
	worker func(cfg *Config, req QuoteRequest)
	// mu guards closed. stop can run when Shutdown didn't return nil (a slow upload can outlive the shared
	// budget), so enqueue and stop can race on ch; mu makes "is ch closed" and "send on ch" atomic so
	// enqueue never panics sending on a closed channel.
	mu     sync.Mutex
	closed bool
}

type notifyJob struct {
	cfg *Config
	req QuoteRequest
}

// newNotifyQueue starts the worker and returns a queue ready for enqueue. It runs until ch is closed
// via stop, which main's shutdown calls with the shared 5s budget so queued notifications drain.
func newNotifyQueue() *notifyQueue {
	return newNotifyQueueWithWorker(notifyNewRequest)
}

// newNotifyQueueWithWorker lets tests inject a worker to observe stop's drain-vs-budget behavior without
// SMTP. It is a field, not a package var, because many tests build their own queue whose worker can
// outlive the test, and a global stub would race with other queues.
func newNotifyQueueWithWorker(worker func(cfg *Config, req QuoteRequest)) *notifyQueue {
	q := &notifyQueue{ch: make(chan notifyJob, publicNotifyQueueCap), done: make(chan struct{}), worker: worker}
	go q.run()
	return q
}

func (q *notifyQueue) run() {
	defer close(q.done)
	for job := range q.ch {
		q.worker(job.cfg, job.req)
	}
}

// enqueue submits a job, dropping and logging it instead of blocking when the queue is full. A successful
// enqueue logs the request id so an operator can reconcile if the process dies before stop drains.
// If stop already closed ch (possible: Shutdown waits only the budget, not every in-flight handler),
// it logs the drop and returns rather than panic.
func (q *notifyQueue) enqueue(cfg *Config, req QuoteRequest) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		log.Printf("public quote-request: notify queue closed, dropping notification for request %s", req.ID)
		return
	}
	select {
	case q.ch <- notifyJob{cfg: cfg, req: req}:
		log.Printf("public quote-request: queued notification for request %s", req.ID)
	default:
		log.Printf("public quote-request: notification queue full, dropping notification for request %s", req.ID)
	}
}

// stop closes ch, refusing further enqueues, and waits up to ctx's deadline for the in-flight send and
// the queue to drain. If the budget expires it logs how many jobs were still queued, so loss is named.
// The drain guarantee holds only when publicSrv.Shutdown returns without error; after
// DeadlineExceeded a straggler's enqueue drops and logs (see enqueue), and that notification is lost.
func (q *notifyQueue) stop(ctx context.Context) {
	q.mu.Lock()
	q.closed = true
	close(q.ch)
	q.mu.Unlock()
	select {
	case <-q.done:
	case <-ctx.Done():
		log.Printf("notify queue: shutdown budget expired with %d notification(s) still queued", len(q.ch))
	}
}

// writePublicQuoteAccepted writes the 201 for an accepted submission, shared by the real and demoMode
// paths so a demo browser sees the real success shape.
//
// fileStored says whether an uploaded file actually persisted: true when no file was sent (a manual
// estimate isn't a "dropped file") or the part was written; false only when a part arrived but was
// dropped at the uploads cap (the errPublicStorageFull branch). Always true in demoMode.
//
// needsPrintTime is true when the visitor sent no print time: priceCents is then 0 and means "to be confirmed".
func writePublicQuoteAccepted(w http.ResponseWriter, id, reference string, priceCents int, needsPrintTime, fileStored bool) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(struct {
		ID             string `json:"id"`
		Reference      string `json:"reference"`
		PriceCents     int    `json:"priceCents"`
		NeedsPrintTime bool   `json:"needsPrintTime"`
		Message        string `json:"message"`
		FileStored     bool   `json:"fileStored"`
	}{ID: id, Reference: reference, PriceCents: priceCents, NeedsPrintTime: needsPrintTime, Message: "Estimate, subject to review", FileStored: fileStored})
}

// uploadSlots bounds concurrent gcode file copies: at most n in total and one per source key (IP, or /64
// for IPv6), so a few trickling sources can't hold every slot. Text-only requests never take one.
type uploadSlots struct {
	sem   chan struct{}
	mu    sync.Mutex
	bySrc map[string]int
}

func newUploadSlots(n int) *uploadSlots {
	return &uploadSlots{sem: make(chan struct{}, n), bySrc: map[string]int{}}
}

// acquire takes a slot for source without blocking. The returned release is idempotent.
func (u *uploadSlots) acquire(source string) (release func(), ok bool) {
	u.mu.Lock()
	if u.bySrc[source] >= 1 {
		u.mu.Unlock()
		return nil, false
	}
	select {
	case u.sem <- struct{}{}:
	default:
		u.mu.Unlock()
		return nil, false
	}
	u.bySrc[source]++
	u.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			u.mu.Lock()
			if u.bySrc[source]--; u.bySrc[source] <= 0 {
				delete(u.bySrc, source)
			}
			u.mu.Unlock()
			<-u.sem
		})
	}, true
}

// inUse reports the slots currently held.
func (u *uploadSlots) inUse() int { return len(u.sem) }

// handlePublicQuoteRequest stores a visitor's quote request in a fixed order: rate limit, then a
// bounded-time streaming read of the multipart body (the gcode part takes an upload slot for its copy).
//
// The body is never buffered whole: r.MultipartReader() is read part by part, text fields are capped
// at publicMaxFieldBytes, and the gcode part (in whatever position) streams through gzip to disk via
// writeGcodePart. Only streaming makes the upload-slot ceiling meaningful; otherwise each in-flight
// request would hold its whole upload on the heap.
//
// demoMode runs every validation and the price recompute, but drains the gcode part instead of writing
// it and synthesizes the row instead of inserting, so no email is queued (see the gcode-part switch and
// the demoMode branch near the end).
func handlePublicQuoteRequest(db *sql.DB, store *ConfigStore, configDir string, rl *rateLimiter, trustProxy bool, uploads *uploadSlots, nq *notifyQueue, demoMode bool) http.HandlerFunc {
	// deadlineWarnOnce limits the "deadline could not be set" warning to one line per handler lifetime. It
	// should never fire (accessLogStatusRecorder implements Unwrap); it is a tripwire for a regression.
	var deadlineWarnOnce sync.Once

	return func(w http.ResponseWriter, r *http.Request) {
		cfg := store.Get() // one snapshot for this request
		if shopRejects(w, cfg, shopKindEstimate) {
			return
		}
		if !rl.allow(clientIP(r, trustProxy), publicRateLimit, publicRateWindow) {
			writeAPIError(w, http.StatusTooManyRequests, "too many requests, try again later")
			return
		}

		// Bounds total outstanding work even without a gcode file (publicUploadsCapBytes covers only the file
		// case): reject before multipart parsing once too many requests sit unfinalized.
		if open, err := countOpenRequests(db); err != nil {
			log.Printf("public quote-request: count open requests: %v", err)
			writeAPIError(w, http.StatusInternalServerError, "requests not writable")
			return
		} else if open >= publicMaxOpenRequests {
			writeAPIError(w, http.StatusServiceUnavailable, "too many pending requests, try again later")
			return
		}

		// The upload slot is not taken here. Text-only requests never need one; the gcode part takes one (per
		// source) just for its disk copy, below.
		source := clientIP(r, trustProxy)
		releaseUpload := func() {}
		defer func() { releaseUpload() }()

		// Per-request deadline for a slow but real client uploading up to publicMaxUploadBytes, replacing the
		// blanket 15s ReadTimeout (see newPublicServer).
		// The write deadline is extended to the same budget plus publicDeadlineMargin: net/http arms the
		// server WriteTimeout after headers, before this handler runs, so a long body read would otherwise
		// finish the write, insert and enqueue and then fail to write the 201, a stored-but-reported-failed
		// submission that invites a duplicate retry.
		rc := http.NewResponseController(w)
		deadline := time.Now().Add(publicUploadReadDeadline)
		readErr := rc.SetReadDeadline(deadline)
		writeErr := rc.SetWriteDeadline(deadline.Add(publicDeadlineMargin))
		for _, derr := range []error{readErr, writeErr} {
			if derr == nil {
				continue
			}
			if errors.Is(derr, http.ErrNotSupported) {
				// Unreachable while accessLogStatusRecorder implements Unwrap; logged once per listener as a
				// tripwire in case a wrapper breaks the chain (newPublicServer's ReadTimeout is the other half).
				deadlineWarnOnce.Do(func() {
					log.Printf("public quote-request: response deadlines not supported by this ResponseWriter chain; falling back to the server's own timeouts")
				})
				continue
			}
			log.Printf("public quote-request: set response deadline: %v", derr)
		}

		r.Body = http.MaxBytesReader(w, &minRateBody{rc: rc, body: r.Body, start: time.Now(), absolute: deadline}, publicMaxUploadBytes)
		mr, err := r.MultipartReader()
		if err != nil {
			// Never echo err.Error() to a visitor (it can include the bind address); log it, return a fixed message.
			log.Printf("public quote-request: multipart reader: %v", err)
			writeAPIError(w, http.StatusBadRequest, "invalid upload")
			return
		}

		id := newID()
		fields := map[string]string{}
		var hasFile bool
		var filename, gzPath string
		var written int64
		// fileStored answers "did the file land on disk", separate from hasFile ("did a file part arrive"). The
		// two diverge only when writeGcodePart reports errPublicStorageFull. It starts true (no file part is
		// not a "dropped file", see TestPublicQuoteRequestManualEntryNoFile) and flips only if a sent file drops.
		fileStored := true

		cleanupFile := func() {
			if gzPath != "" {
				os.Remove(gzPath)
			}
		}

		for {
			part, perr := mr.NextPart()
			if perr == io.EOF {
				break
			}
			if perr != nil {
				cleanupFile()
				if _, ok := errors.AsType[*http.MaxBytesError](perr); ok {
					writeAPIError(w, http.StatusRequestEntityTooLarge, "request body too large")
					return
				}
				log.Printf("public quote-request: read multipart part: %v", perr)
				writeAPIError(w, http.StatusBadRequest, "invalid upload")
				return
			}

			formName := part.FormName()
			switch {
			case formName == "gcode" && part.FileName() != "" && !hasFile:
				rel, ok := uploads.acquire(source)
				if !ok {
					part.Close()
					cleanupFile()
					writeAPIError(w, http.StatusServiceUnavailable, "the server is busy with other uploads; try again in a minute")
					return
				}
				releaseUpload = rel
				if demoMode {
					// Drain instead of writing via writeGcodePart; the r.Body size limit still applies, nothing hits disk.
					filename = sanitizeGcodeFilename(part.FileName())
					_, drainErr := io.Copy(io.Discard, part)
					part.Close()
					if drainErr != nil {
						if _, ok := errors.AsType[*http.MaxBytesError](drainErr); ok {
							writeAPIError(w, http.StatusRequestEntityTooLarge, "request body too large")
							return
						}
						log.Printf("public quote-request: read gcode part (demo mode): %v", drainErr)
						writeAPIError(w, http.StatusBadRequest, "invalid upload")
						return
					}
					hasFile = true
					break
				}
				var writeErr error
				filename, gzPath, written, writeErr = writeGcodePart(configDir, id, part)
				if errors.Is(writeErr, errPublicStorageFull) {
					// Degrade, don't hard-fail: a full uploads volume must not discard the visitor's name, email, note and
					// pricing. writeGcodePart already logged dirSize/cap; log here too, scoped to this request id.
					//
					// writeGcodePart returns before reading the part here, so filename and written are zero. An empty
					// gcode_filename/gcode_bytes would make the admin UI claim no file was uploaded, so recover the
					// sanitized filename from the open part and drain it (bounded by the MaxBytesReader on r.Body) to
					// learn the byte count. gzPath/gcode_path stay "": the encoding QuoteRequest and retention.go use for
					// "a file arrived but was dropped", distinct from a retention prune (gcode_pruned_at set) or no file.
					filename = sanitizeGcodeFilename(part.FileName())
					drained, drainErr := io.Copy(io.Discard, part)
					written = drained
					part.Close()
					if drainErr != nil {
						if _, ok := errors.AsType[*http.MaxBytesError](drainErr); ok {
							writeAPIError(w, http.StatusRequestEntityTooLarge, "request body too large")
							return
						}
						log.Printf("public quote-request: drain gcode part after storage-full: %v", drainErr)
						writeAPIError(w, http.StatusBadRequest, "invalid upload")
						return
					}
					log.Printf("public quote-request: uploads storage full, continuing request %s without its file (kept filename=%q bytes=%d)", id, filename, written)
					fileStored = false
					// hasFile still becomes true: a file part did arrive, it just didn't persist (see fileStored).
				} else {
					part.Close()
					if writeErr != nil {
						cleanupFile()
						if _, ok := errors.AsType[*http.MaxBytesError](writeErr); ok {
							writeAPIError(w, http.StatusRequestEntityTooLarge, "request body too large")
							return
						}
						log.Printf("public quote-request: %v", writeErr)
						writeAPIError(w, http.StatusInternalServerError, "requests not writable")
						return
					}
				}
				hasFile = true
			default:
				if _, known := publicQuoteFormFields[formName]; known {
					if _, exists := fields[formName]; !exists {
						val, rerr := readBoundedField(part)
						if rerr != nil {
							part.Close()
							cleanupFile()
							if _, ok := errors.AsType[*http.MaxBytesError](rerr); ok {
								writeAPIError(w, http.StatusRequestEntityTooLarge, "request body too large")
								return
							}
							log.Printf("public quote-request: read field %s: %v", formName, rerr)
							writeAPIError(w, http.StatusBadRequest, "invalid upload")
							return
						}
						fields[formName] = val
					}
				}
				part.Close()
			}
			// The file copy (the only slot-holding work) is over; later text parts don't need the slot.
			releaseUpload()
		}

		name := strings.TrimSpace(fields["name"])
		email := strings.TrimSpace(fields["email"])
		note := strings.TrimSpace(fields["note"])
		material := strings.TrimSpace(fields["material"])

		var problems []string
		if name == "" {
			problems = append(problems, "name: required")
		} else if len(name) > 200 {
			problems = append(problems, "name: max 200 characters")
		}
		if email == "" {
			problems = append(problems, "email: required")
		} else if len(email) > quoteRequestsEmailMaxLen {
			problems = append(problems, fmt.Sprintf("email: max %d characters", quoteRequestsEmailMaxLen))
		} else if !plausiblePublicEmail(email) {
			problems = append(problems, publicEmailFormatProblem)
		} else if !plainEmailForMailto(email) {
			problems = append(problems, "email: contains characters that are not allowed")
		}
		if len(note) > 2000 {
			problems = append(problems, "note: max 2000 characters")
		}

		var matchedMaterial Material
		materialIsPublic := false
		for _, m := range cfg.Materials {
			if m.Public && m.Name == material {
				matchedMaterial = m
				materialIsPublic = true
				break
			}
		}
		if material == "" {
			problems = append(problems, "material: required")
		} else if !materialIsPublic {
			problems = append(problems, "material: no longer offered; choose another")
		}

		quantity, err := strconv.Atoi(strings.TrimSpace(fields["quantity"]))
		if err != nil || quantity < 1 || quantity > cfg.maxOrderQty() {
			problems = append(problems, fmt.Sprintf("quantity: must be an integer 1-%d", cfg.maxOrderQty()))
		}

		// strconv.ParseFloat accepts "NaN"/"Inf"/"-Inf" with a nil err, and every comparison against NaN is
		// false, so a range check alone lets NaN and the infinities through. Reject non-finite explicitly.
		weightG, err := strconv.ParseFloat(strings.TrimSpace(fields["weightG"]), 64)
		if err != nil || math.IsNaN(weightG) || math.IsInf(weightG, 0) || weightG < 0 || weightG > 100000 {
			problems = append(problems, "weightG: must be 0-100000")
		}

		// A blank or absent timeSeconds is allowed: an unsliced STL or 3MF has no print time the visitor can
		// know. The request is stored with needs_print_time set and no price; the owner confirms it. The time
		// is never estimated from volume.
		var timeSeconds int
		needsPrintTime := strings.TrimSpace(fields["timeSeconds"]) == ""
		if !needsPrintTime {
			timeSeconds, err = strconv.Atoi(strings.TrimSpace(fields["timeSeconds"]))
			if err != nil || timeSeconds < 0 || timeSeconds > maxTimeSeconds {
				problems = append(problems, fmt.Sprintf("timeSeconds: must be 0-%d", maxTimeSeconds))
			}
		}

		if len(problems) > 0 {
			cleanupFile()
			writeAPIError(w, http.StatusUnprocessableEntity, problems...)
			return
		}

		now := time.Now().UTC().Format(time.RFC3339)

		// Server-authoritative price: the client sends none. POST /api/public/quote/preview is the only place a
		// visitor sees a number before submitting, and it runs this same engine call, so preview and stored
		// price can't disagree. price_cents is the sole price column.
		//
		// Without a print time there is nothing to price: price_cents stays 0, which with needs_print_time means
		// "to be confirmed", never "free".
		priceCents := 0
		if !needsPrintTime {
			qi, opt := buildPublicQuoteInputs(cfg, matchedMaterial, quantity, weightG, timeSeconds)
			bd := ComputeQuote(qi, opt)
			if !bd.Valid || bd.Pricing == nil || !bd.Pricing.Valid {
				// Same fail-closed posture as handlePublicQuotePreview: a form that passed every bound shouldn't hit
				// an invalid breakdown, so this is a config-shaped failure; refuse to store a fabricated price, not 0.
				cleanupFile()
				log.Printf("public quote-request: recompute produced an invalid breakdown for material %q", material)
				writeAPIError(w, http.StatusUnprocessableEntity, "price: we can't price this request right now; try again later or contact the shop")
				return
			}
			// ShippingCharge is forced to 0 in buildPublicQuoteInputs, so Revenue is ResolvedPrice * quantity.
			priceCents = int(bd.Pricing.Revenue)
		}
		// The shipping charge the preview showed, stored so converting the request to an invoice adds the
		// same Shipping line the visitor saw even if the setting changes later.
		shippingCents := int(toCents(cfg.Pricing.DefaultShippingCharge))
		// The tax basis at submit, stored for the same reason: the price includes tax while taxRatePct > 0.
		taxInclusive := cfg.Pricing.TaxRatePct > 0

		if demoMode {
			// Simulate the rest: same 201 and synthesized id, but no row, gcode file or notification.
			writePublicQuoteAccepted(w, id, peekRequestReference(db, referencePrefix(cfg)), priceCents, needsPrintTime, fileStored)
			return
		}

		reference, err := insertRequestWithReference(db, cfg, `
			INSERT INTO quote_requests (id, status, name, email, note, material, quantity, weight_g, time_seconds, price_cents, gcode_filename, gcode_path, gcode_bytes, created_at, shipping_cents, tax_inclusive, needs_print_time, reference)
			VALUES (?, 'new', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, name, email, note, material, quantity, weightG, timeSeconds, priceCents, filename, gzPath, written, now, shippingCents, taxInclusive, needsPrintTime)
		if err != nil {
			cleanupFile()
			log.Printf("public quote-request: insert: %v", err)
			writeAPIError(w, http.StatusInternalServerError, "requests not writable")
			return
		}

		// Owner notification, queued onto nq instead of a bare `go notifyNewRequest(...)` so anonymous
		// submissions can't spawn unbounded SMTP conversations (see notifyQueue). Fire-and-forget, never awaited.
		nq.enqueue(cfg, QuoteRequest{
			ID: id, Status: "new", Name: name, Email: email, Note: note, Material: material,
			Quantity: quantity, WeightG: weightG, TimeSeconds: timeSeconds, PriceCents: priceCents, ShippingCents: shippingCents, TaxInclusive: taxInclusive,
			GcodeFilename: filename, GcodePath: gzPath, GcodeBytes: int(written), CreatedAt: now,
			NeedsPrintTime: needsPrintTime, Reference: reference,
		})

		writePublicQuoteAccepted(w, id, reference, priceCents, needsPrintTime, fileStored)
	}
}

// insertRequestWithReference runs the quote_requests INSERT with a freshly issued reference appended as its
// last bound argument, in one transaction with the counter bump, so a failed insert leaves no gap in the
// sequence. It returns the reference.
func insertRequestWithReference(db *sql.DB, cfg *Config, query string, args ...any) (string, error) {
	tx, err := db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	reference, err := nextRequestReference(tx, referencePrefix(cfg))
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(query, append(args, reference)...); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return reference, nil
}

// loadPublicManifest reads public-manifest.json from sub and returns the set of files it lists. The
// file is generated at build time by web/vite.config.ts's printpaca-public-manifest plugin from the
// Rollup bundle graph (the public entry's JS/CSS, shared chunks like "theme-*", and files public.html
// references). publicIndexHandler uses it as an allowlist so admin-only files in the same embedded
// dist (index.html, assets/main-*.js/css) are never reachable, without a hand-kept filename list.
//
// It returns an empty non-nil set, never an error, when the file is missing or malformed. That is right
// for the dev placeholder dist (no public.html; "/" 503s), but not when public.html IS present: every
// asset would 404 behind a blank page, so publicIndexHandler checks that combination (buildBroken) and 503s.
func loadPublicManifest(sub fs.FS) map[string]struct{} {
	allowed := map[string]struct{}{}
	data, err := fs.ReadFile(sub, "public-manifest.json")
	if err != nil {
		return allowed
	}
	var names []string
	if err := json.Unmarshal(data, &names); err != nil {
		return allowed
	}
	for _, name := range names {
		allowed[name] = struct{}{}
	}
	return allowed
}

// publicIndexHandler serves the public listener's static assets from sub (the admin's embedded dist),
// but not all of it. "/" (the shop) and "/order" (the same shop, kept for old links) serve public.html, the public Vite entry,
// and 503 when it is absent (the
// placeholder dist). Every other path must be in loadPublicManifest's allowlist, so the admin bundle
// (index.html, assets/main-*.js/css) still 404s here.
// .gz siblings are negotiated via serveGzipVariant. The allowlist checks the requested plain path
// (".../app.js"), so no manifest change is needed.
func publicIndexHandler(sub fs.FS, config publicConfigFunc) http.Handler {
	assets := http.FileServerFS(sub)
	_, statErr := fs.Stat(sub, "public.html")
	hasPublicHTML := statErr == nil
	// The shop HTML is read once; each request fills its head from the live settings (renderShopHTML).
	var shopTemplate string
	if hasPublicHTML {
		if data, err := fs.ReadFile(sub, "public.html"); err == nil {
			shopTemplate = string(data)
		}
	}
	allowed := loadPublicManifest(sub)

	// buildBroken is the fail-closed check: public.html with no (or an empty) manifest guarantees every
	// asset 404s. Computed once at construction so the broken build is named in the startup log.
	buildBroken := hasPublicHTML && len(allowed) == 0
	if buildBroken {
		log.Printf("public listener: public.html is present but public-manifest.json is missing or empty; \"/\" will serve 503 until the web build is corrected")
	}

	serveUnavailable := func(w http.ResponseWriter) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, "public page not built")
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/order" {
			if _, ok := allowed[strings.TrimPrefix(r.URL.Path, "/")]; !ok {
				http.NotFound(w, r)
				return
			}
			// Set Cache-Control only after the allowlist check, on a path that serves an asset; otherwise a
			// missing /assets/<name> 404 inherits the year-long immutable header and a client or CDN caches the
			// 404 for a year (same bug class as items.go).
			if strings.HasPrefix(r.URL.Path, "/assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			if serveGzipVariant(w, r, sub) {
				return
			}
			assets.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		if !hasPublicHTML || buildBroken {
			serveUnavailable(w)
			return
		}
		page, ok := renderShopHTML(shopTemplate, config())
		if !ok {
			log.Printf("public listener: public.html has no <title> to fill; serving it unrendered")
			page = []byte(shopTemplate)
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			w.Write(page)
		}
	})
}

// publicShared is the state every listener serving the public routes shares, so a visitor gets one per-IP
// budget, one upload-concurrency bound and one notification worker whichever port they arrive on. main
// builds it once and mounts the public mux on the main port and on PUBLIC_PORT (when set).
type publicShared struct {
	// quoteRl and orderRl are the per-IP publicRateLimit budgets of handlePublicQuoteRequest and
	// handlePublicItemOrder. They are separate so sending estimates doesn't use up a visitor's orders. Both
	// kinds still count against publicMaxOpenRequests, the global bound.
	quoteRl *rateLimiter
	orderRl *rateLimiter
	// previewRl is SEPARATE from quoteRl and orderRl: preview must not use the per-hour submission budgets (publicPreviewRateLimit).
	previewRl *rateLimiter
	// uploads bounds in-flight gcode file copies (publicMaxConcurrentUploads, one per source).
	uploads *uploadSlots
	// nq serializes owner notifications through one worker (see notifyQueue); main drains it on shutdown.
	nq *notifyQueue
}

func newPublicShared() *publicShared {
	return &publicShared{
		quoteRl:   newRateLimiter(),
		orderRl:   newRateLimiter(),
		previewRl: newRateLimiter(),
		uploads:   newUploadSlots(publicMaxConcurrentUploads),
		nq:        newNotifyQueue(),
	}
}

// buildPublicMux is buildPublicMuxShared over fresh shared state, for tests and single-mux callers. It also
// returns the notifyQueue so the caller can drain it.
func buildPublicMux(store *ConfigStore, db *sql.DB, configDir string, trustProxy, demoMode bool) (*http.ServeMux, *notifyQueue) {
	shared := newPublicShared()
	return buildPublicMuxShared(store, db, configDir, shared, trustProxy, demoMode, publicPortStaffURL), shared.nq
}

// buildPublicMuxShared constructs the public routes' ENTIRE route table: GET / and /order with the static
// assets, the public API, /item-images, /custom.css and /healthz. Factored out so tests can use httptest.
// Admin routes are absent, and publicIndexHandler allowlists only the public entry's own assets (see
// loadPublicManifest), so the admin bundle in the same embedded dist 404s. That is the isolation the public
// routes require. trustProxy is per listener (PUBLIC_TRUST_PROXY, or ADMIN_TRUST_PROXY on the main port).
// demoMode is threaded to handlePublicQuoteRequest to simulate submissions without persisting. staffURL is
// per listener (mainStaffURL, publicPortStaffURL).
func buildPublicMuxShared(store *ConfigStore, db *sql.DB, configDir string, shared *publicShared, trustProxy, demoMode bool, staffURL staffURLFunc) *http.ServeMux {
	mux := http.NewServeMux()

	// INVARIANT: no public route may serve customer-controlled content (an uploaded file, a name, a note) as
	// HTML or JavaScript on this origin. On the main port the public routes share an origin with /admin/, so
	// script injected through any of them would run next to the admin app. Item images are sniffed and served
	// as images only; G-code is never served back; JSON responses carry application/json. Keep it that way
	// when adding a route.

	mux.HandleFunc("GET /healthz", handlePublicHealthz)
	mux.HandleFunc("GET /api/public/config", handlePublicConfig(store, demoMode, staffURL))
	mux.HandleFunc("GET /custom.css", handlePublicCustomCss(store))
	mux.HandleFunc("GET /api/public/items", handlePublicItems(db, store))
	mux.HandleFunc("GET /item-images/{file}", itemImageHandler(configDir))
	mux.HandleFunc("POST /api/public/quote/preview", handlePublicQuotePreview(store, shared.previewRl, trustProxy))
	mux.HandleFunc("POST /api/public/quote-request", handlePublicQuoteRequest(db, store, configDir, shared.quoteRl, trustProxy, shared.uploads, shared.nq, demoMode))
	mux.HandleFunc("POST /api/public/item-order", handlePublicItemOrder(db, store, shared.orderRl, trustProxy, shared.nq, demoMode))

	sub, err := fs.Sub(distFS, "embedded/dist")
	if err != nil {
		panic(err) // embedded FS shape is a build-time invariant
	}
	mux.Handle("GET /", publicIndexHandler(sub, func() publicConfigResponse {
		return buildPublicConfig(store.Get(), demoMode, staffURL)
	}))

	return mux
}

// publicListenerHandler wraps mux with security headers, no-store on /api/*, gzip (just inside the
// access log, as in adminListenerHandler), and an access log line per request, always (accessLogAlways).
// Separate from buildPublicMux so tests can exercise the routes without asserting on headers or logging.
func publicListenerHandler(mux http.Handler, trustProxy bool) http.Handler {
	return accessLogMiddleware("public", accessLogAlways, trustProxy,
		compressMiddleware(securityHeadersTLSMiddleware(noStoreAPIMiddleware(mux), trustProxy)))
}

// newPublicServer builds the second HTTP server (when PUBLIC_PORT is set) with the admin server's
// httpWriteTimeout/httpIdleTimeout, except for ReadTimeout. ReadHeaderTimeout bounds the header read;
// handlePublicQuoteRequest sets its own per-request read/write deadlines via ResponseController, the
// PRIMARY mechanism, normally tighter than ReadTimeout.
//
// ReadTimeout is also set, to publicUploadReadDeadline + publicDeadlineMargin, as a DEFENSIVE
// backstop (see publicDeadlineMargin). Without it, a middleware that loses Unwrap would leave the
// per-request deadline silently inapplicable and no read timeout after headers, so two slow-trickle
// connections could hold both upload semaphore slots forever. Keeping both means a regression in one (a
// middleware losing Unwrap, a handler bug) fails closed.
//
// It returns the *http.Server unstarted so main can Shutdown it gracefully with the main server before
// db.Close(). shared is the state main also mounts on the main port; main drains shared.nq once.
func newPublicServer(publicPort string, store *ConfigStore, db *sql.DB, configDir string, shared *publicShared, trustProxy, demoMode bool) *http.Server {
	mux := buildPublicMuxShared(store, db, configDir, shared, trustProxy, demoMode, publicPortStaffURL)
	return &http.Server{
		Addr:              ":" + publicPort,
		Handler:           publicListenerHandler(mux, trustProxy),
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       publicUploadReadDeadline + publicDeadlineMargin,
		WriteTimeout:      httpWriteTimeout,
		IdleTimeout:       httpIdleTimeout,
	}
}
