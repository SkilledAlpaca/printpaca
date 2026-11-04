package main

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // embed the IANA zone database so the config timezone works in the scratch image
)

var version = "dev" // overridden via -ldflags "-X main.version=..."

type Integrations struct {
	Spoolman bool `json:"spoolman"`
}

// deriveIntegrations reports whether Spoolman is configured (SPOOLMAN_URL or its Settings fallback; env
// wins). demoMode forces it on so the SPA's picker renders against the fake data.
func deriveIntegrations(demoMode bool, cfg *Config) Integrations {
	raw, _ := resolveIntegrationURL(os.Getenv("SPOOLMAN_URL"), cfg.IntegrationsUrls.SpoolmanURL)
	return Integrations{
		Spoolman: raw != "" || demoMode,
	}
}

// deriveMoonraker mirrors deriveIntegrations; Moonraker is top-level in /api/config, not under integrations.
func deriveMoonraker(demoMode bool, cfg *Config) bool {
	raw, _ := resolveIntegrationURL(os.Getenv("MOONRAKER_URL"), cfg.IntegrationsUrls.MoonrakerURL)
	return raw != "" || demoMode
}

// deriveFluiddURL returns the resolved FLUIDD_URL (env, else config), or "". It is link-only. A bad env value
// fails fast at startup (checkIntegrationEnv); config values were validated at PUT/LoadConfig time, so a value
// that still fails here (never expected) yields "" instead of a link the browser would follow blindly.
func deriveFluiddURL(cfg *Config) string {
	raw, _ := resolveIntegrationURL(os.Getenv("FLUIDD_URL"), cfg.IntegrationsUrls.FluiddURL)
	if raw == "" {
		return ""
	}
	if _, err := validProxyURL("FLUIDD_URL", raw); err != nil {
		return ""
	}
	return raw
}

// checkIntegrationEnv fails fast at startup on an invalid SPOOLMAN_URL, MOONRAKER_URL or FLUIDD_URL, so a
// typo shows in the log at boot instead of as a 502 later. Config-sourced values are validated on save.
func checkIntegrationEnv() {
	for _, name := range []string{"SPOOLMAN_URL", "MOONRAKER_URL", "FLUIDD_URL"} {
		if raw := os.Getenv(name); raw != "" {
			mustValidLinkURL(name, raw)
		}
	}
}

// deriveDemoPublicURL returns the validated DEMO_PUBLIC_URL, or "" when demoMode is off. It is read only in
// demo mode so a stray value never blocks a normal boot or leaks through GET /api/demo. Link-only.
func deriveDemoPublicURL(demoMode bool) string {
	if !demoMode {
		return ""
	}
	raw := os.Getenv("DEMO_PUBLIC_URL")
	if raw == "" {
		return ""
	}
	return mustValidLinkURL("DEMO_PUBLIC_URL", raw)
}

// IntegrationSources reports whether each integration came from "env", "config" or "unset", for the Settings
// page to label rows and lock env-sourced fields. Computed per request, like the proxy targets.
type IntegrationSources struct {
	Spoolman  string `json:"spoolman"`
	Moonraker string `json:"moonraker"`
	Fluidd    string `json:"fluidd"`
}

// deriveIntegrationSources applies the same resolveIntegrationURL precedence as the other derive helpers.
func deriveIntegrationSources(cfg *Config) IntegrationSources {
	_, spoolman := resolveIntegrationURL(os.Getenv("SPOOLMAN_URL"), cfg.IntegrationsUrls.SpoolmanURL)
	_, moonraker := resolveIntegrationURL(os.Getenv("MOONRAKER_URL"), cfg.IntegrationsUrls.MoonrakerURL)
	_, fluidd := resolveIntegrationURL(os.Getenv("FLUIDD_URL"), cfg.IntegrationsUrls.FluiddURL)
	return IntegrationSources{Spoolman: spoolman, Moonraker: moonraker, Fluidd: fluidd}
}

// handleHealthz is the main port's readiness probe (the PUBLIC_PORT listener has a cheaper
// handlePublicHealthz). It reads a real table and fsyncs a temp file in CONFIG_DIR, and reports 503
// when either fails, instead of a misleading "ok". The Docker HEALTHCHECK probes the main port. The main
// router serves it outside the admin auth and tunnel chain, behind the public chain and Host allowlist, so
// tunneled monitors and an Access application scoped to /admin both get an answer.
//
// The success body is a bare "ok", not "ok <version>": /healthz needs no session, and the build
// version helps match known-vulnerable releases. The version is in the authenticated GET /api/config.
//
// Because /healthz is unauthenticated:
//  1. A failed probe returns the fixed string "unhealthy", never the error text (sqlite driver errors,
//     absolute CONFIG_DIR paths). The real reason goes to the server log.
//  2. The real probe runs at most once per healthzCacheTTL and other requests replay the result, so a
//     fast poller can't contend the single SQLite connection or churn CONFIG_DIR. See healthzCache.
func handleHealthz(db *sql.DB, configDir string) http.HandlerFunc {
	return newHealthzHandler(db, configDir, &healthzCache{now: time.Now, ttl: healthzCacheTTL})
}

// httpWriteTimeout bounds response writes on both listeners; generous for slow proxied Spoolman responses.
const httpWriteTimeout = 60 * time.Second

// httpIdleTimeout bounds idle keep-alive connections on both listeners.
const httpIdleTimeout = 120 * time.Second

// healthzCacheTTL bounds how long a probe result is reused: enough to blunt a fast poll loop, far below
// the 30s HEALTHCHECK interval so real state changes still show within one cycle.
const healthzCacheTTL = 5 * time.Second

// healthzResult is one probe pass. reason may be sensitive: it is logged, never sent to the client.
type healthzResult struct {
	ok     bool
	reason string
}

// healthzCache holds the latest result and its deadline, shared by every request through one handleHealthz
// closure. now is injectable so tests control time.
type healthzCache struct {
	mu      sync.Mutex
	now     func() time.Time
	ttl     time.Duration
	expires time.Time
	result  healthzResult
}

// get returns the cached result within its TTL, else runs probe and caches it. One caller per window probes.
func (c *healthzCache) get(probe func() healthzResult) healthzResult {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Before(c.expires) {
		return c.result
	}
	c.result = probe()
	c.expires = now.Add(c.ttl)
	return c.result
}

// newHealthzHandler builds the handler around a cache, so tests can inject a controllable clock.
func newHealthzHandler(db *sql.DB, configDir string, cache *healthzCache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		result := cache.get(func() healthzResult {
			if err := probeDBReadable(db); err != nil {
				return healthzResult{reason: fmt.Sprintf("db probe failed: %v", err)}
			}
			if err := probeConfigDirWritable(configDir); err != nil {
				return healthzResult{reason: fmt.Sprintf("config dir probe failed: %v", err)}
			}
			return healthzResult{ok: true}
		})
		if !result.ok {
			log.Printf("healthz: %s", result.reason)
			writeHealthzUnavailable(w)
			return
		}
		fmt.Fprint(w, "ok")
	}
}

// probeDBReadable reads a real table instead of SELECT 1, which never touches a b-tree page and so passes
// on a corrupt database. COUNT(*) over the tiny app_state forces a page read and always returns one row
// (a LIMIT 1 select would report ErrNoRows on an empty table).
func probeDBReadable(db *sql.DB) error {
	var n int
	return db.QueryRow("SELECT COUNT(*) FROM app_state").Scan(&n)
}

// probeWritePayload is written and fsynced before removal: os.CreateTemp alone only allocates an inode,
// which succeeds on a full disk (ENOSPC), so a create-close-remove probe reports healthy there.
var probeWritePayload = []byte("printpaca healthz probe\n")

// probeConfigDirWritable creates, writes, fsyncs and removes a temp file in configDir, proving the
// directory is writable and has room. Used at startup (bad mounts fail fast, named fix) and by handleHealthz.
func probeConfigDirWritable(configDir string) error {
	f, err := os.CreateTemp(configDir, ".healthz-probe-*")
	if err != nil {
		return err
	}
	name := f.Name()
	if err := writeAndSyncProbe(f); err != nil {
		f.Close()
		os.Remove(name)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Remove(name)
}

// writeAndSyncProbe is split out so a test can run the write+sync against a readable file.
func writeAndSyncProbe(f *os.File) error {
	n, err := f.Write(probeWritePayload)
	if err != nil {
		return err
	}
	if n != len(probeWritePayload) {
		return fmt.Errorf("short write: wrote %d of %d bytes", n, len(probeWritePayload))
	}
	return f.Sync()
}

// adminListenerHandler wraps mux in this fixed order, outermost first: access log (sees every final status);
// gzip (compress.go), just inside it so the recorder sees the real handler's status; security headers,
// applied before auth decides 401/403/200; then auth.middleware (Host allowlist plus session check).
// Factored out of main() so tests run this exact production chain rather than a hand-built copy.
func adminListenerHandler(mux *http.ServeMux, auth *authState, trustProxy bool) http.Handler {
	return accessLogMiddleware("admin", accessLogAdminFilter, trustProxy,
		compressMiddleware(securityHeadersTLSMiddleware(noStoreAPIMiddleware(auth.middleware(mux)), trustProxy)))
}

// writeHealthzUnavailable writes the fixed, content-free "unhealthy" 503 body; see handleHealthz.
func writeHealthzUnavailable(w http.ResponseWriter) {
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprintln(w, "unhealthy")
}

// configExtras groups handleConfig's startup-fixed extras by name. The integration URLs are not here: they
// resolve per request from the live config so a Settings save shows at once.
type configExtras struct {
	authEnabled bool
	// demoMode surfaces DEMO_MODE to the SPA.
	demoMode bool
}

// auth backs the "user" extra, resolved per request, unlike the startup-fixed extras.
func handleConfig(store *ConfigStore, extras configExtras, auth *authState) http.HandlerFunc {
	type payload struct {
		*Config
		Integrations Integrations `json:"integrations"`
		Version      string       `json:"version"`
		// AuthEnabled tells the web app whether ADMIN_PASSWORD is set, so it can show "Log out". A session
		// cookie is HttpOnly and absent when auth is off, so it isn't a client-side signal.
		AuthEnabled bool `json:"authEnabled"`
		// Moonraker and FluiddURL are top-level (not under Integrations) because FluiddURL carries a value and
		// web/src reads config.moonraker and config.fluiddUrl as a frozen interface.
		Moonraker bool   `json:"moonraker"`
		FluiddURL string `json:"fluiddUrl"`
		// DemoMode lets the SPA degrade write UI gracefully instead of discovering demoWriteGuard's 403.
		DemoMode bool `json:"demoMode"`
		// IntegrationSources lets the Settings Integrations card label each row and lock env-sourced fields.
		IntegrationSources IntegrationSources `json:"integrationSources"`
		// User gives the SPA the username, isAdmin and tab grants in one trip, resolved per request, never cached.
		User meResponse `json:"user"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		cfg := store.Get() // one snapshot for this request
		user, err := currentUser(auth.db, identityFor(r))
		if err != nil {
			log.Printf("config: resolving current user: %v", err)
			http.Error(w, "config unavailable", http.StatusInternalServerError)
			return
		}
		user.SignedInVia, user.AccessEmail = auth.signedInVia(r)
		if !identityFor(r).isAdmin && cfg.Users.AccessAdminEmail != "" {
			// The Access admin email names an admin credential; employees get a copy without it.
			redacted := *cfg
			redacted.Users.AccessAdminEmail = ""
			cfg = &redacted
		}
		if err := json.NewEncoder(w).Encode(payload{
			Config:             cfg,
			Integrations:       deriveIntegrations(extras.demoMode, cfg),
			Version:            version,
			AuthEnabled:        extras.authEnabled,
			Moonraker:          deriveMoonraker(extras.demoMode, cfg),
			FluiddURL:          deriveFluiddURL(cfg),
			DemoMode:           extras.demoMode,
			IntegrationSources: deriveIntegrationSources(cfg),
			User:               user,
		}); err != nil {
			log.Printf("encode config: %v", err)
		}
	}
}

// demoModeWarning is logged whenever DEMO_MODE=1: a bordered block (like adminAuthDisabledWarning) so
// operators see this is a public read-only demo.
const demoModeWarning = "" +
	"printpaca: ================================================================\n" +
	"printpaca: DEMO_MODE is enabled. This instance is a READ-ONLY DEMO.\n" +
	"printpaca: Every admin write is blocked (except login/logout), the public\n" +
	"printpaca: quote submission is simulated -- nothing is persisted -- and the\n" +
	"printpaca: Spoolman/Moonraker integrations serve fake sample data.\n" +
	"printpaca: ================================================================"

// demoModeStartupGate is DEMO_MODE's fail-closed startup check, called from main() after openDB:
//   - Empty database: seed it (seedDemoData records demoSeedMarkerKey in the same transaction). A seed
//     failure is logged, not fatal; it rolled back, so the next restart retries.
//   - Non-empty with the marker: the demo's own seeded data (or a restart); proceed.
//   - Non-empty without the marker: not demo data. DEMO_MODE=1 waives the admin-password length floor
//     and the Cloudflare-tunnel guard because a demo is public with a published password, which is
//     exactly wrong in front of real clients and invoices. Refuse to start.
func demoModeStartupGate(db *sql.DB, store *ConfigStore, configDir string) error {
	empty, err := demoDBIsEmpty(db)
	if err != nil {
		return fmt.Errorf("demo mode: check database emptiness: %w", err)
	}
	if empty {
		if err := seedDemoData(db, store, configDir); err != nil {
			log.Printf("demo seed: %v", err)
		}
		return nil
	}

	marked, err := demoSeedMarkerPresent(db)
	if err != nil {
		return fmt.Errorf("demo mode: check seed marker: %w", err)
	}
	if !marked {
		return fmt.Errorf(
			"DEMO_MODE=1 refused to start: this database already holds data that this demo did not seed (no demo-seed marker found), and DEMO_MODE disables the Cloudflare-tunnel guard and waives the admin-password length floor -- starting would expose that data behind the demo's short, published password. Unset DEMO_MODE to run this database normally, or set CONFIG_DIR to a fresh, empty directory to run a demo")
	}
	return nil
}

func main() {
	// Installed first so every log line, including banners, reaches the ring behind GET /api/logs/errors.
	// os.Stderr stays the primary destination.
	installLogTee(appLogRing, os.Stderr)

	healthcheck := flag.Bool("healthcheck", false, "probe /healthz and exit")
	flag.Parse()
	port := cmp.Or(os.Getenv("PORT"), "8080")
	// DEMO_MODE makes this a read-only public demo. Read once, passed explicitly to the functions that need it.
	demoMode := os.Getenv("DEMO_MODE") == "1"

	if *healthcheck {
		client := &http.Client{Timeout: 3 * time.Second}
		resp, err := client.Get("http://127.0.0.1:" + port + "/healthz")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Logged after the -healthcheck early exit, which runs every HEALTHCHECK interval and would spam the log.
	if demoMode {
		log.Print(demoModeWarning)
	}

	configDir := cmp.Or(os.Getenv("CONFIG_DIR"), "/config")
	cfg, err := LoadConfig(configDir)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	store := NewConfigStore(cfg)
	warnIfSmtpAuthMisconfigured(cfg)

	// Integration URLs resolve per request from the live config (handleConfig, registerProxies); an invalid
	// environment value is the one startup failure.
	checkIntegrationEnv()
	demoPublicURL := deriveDemoPublicURL(demoMode)

	// Fail fast if CONFIG_DIR isn't writable: otherwise openDB's "unable to open database file" doesn't name the
	// fix. Same check as handleHealthz; catches the common wrong-uid bind mount.
	if err := probeConfigDirWritable(configDir); err != nil {
		log.Fatalf("config dir %s is not writable by this process (uid 65532): %v\nFix ownership on the host with: sudo chown -R 65532:65532 %s -- then, if that directory is your cloned repo's config/ folder, restore your own ownership of the two git-tracked files it also fixed: sudo chown $(id -u):$(id -g) %s/config.example.yaml %s/config.schema.json", configDir, err, configDir, configDir, configDir)
	}

	db, err := openDB(configDir)
	if err != nil {
		log.Fatalf("db: %v", err)
	}

	// seedDemoData seeds ~3 months of backdated sample data into a fresh demo container. demoModeStartupGate
	// refuses a database neither fresh nor demo-seeded: DEMO_MODE waives the password floor and tunnel guard.
	if demoMode {
		if err := demoModeStartupGate(db, store, configDir); err != nil {
			log.Fatalf("%v", err)
		}
	}

	// G-code retention (retention.go) prunes stored uploads for finalized requests past their grace period,
	// at startup and daily. Skipped in demo mode so seeded data is never pruned; retentionDone then stays
	// nil, which the shutdown sequence checks.
	var retentionCancel context.CancelFunc
	var retentionDone <-chan struct{}
	if !demoMode {
		var retentionCtx context.Context
		retentionCtx, retentionCancel = context.WithCancel(context.Background())
		retentionDone = runRetentionLoop(retentionCtx, db, configDir)
	}

	// Auth is secure by default: resolveAdminPassword generates and persists a random password when
	// ADMIN_PASSWORD is unset, and disables auth only when it is explicitly "". Enabled auth requires a session
	// for every admin /api/* route (served at /admin/api/*) except authExemptPaths; other paths, including
	// /healthz, skip it. The ALLOWED_HOSTS allowlist applies in every auth mode to every request on the main port,
	// never to PUBLIC_PORT.
	adminPassword, err := resolveAdminPassword(configDir)
	if err != nil {
		log.Fatalf("admin password: %v", err)
	}
	authSt := newAuthState(adminPassword, parseAllowedHosts(os.Getenv("ALLOWED_HOSTS")))
	// ADMIN_TRUST_PROXY=1 means a TLS-terminating proxy that overwrites X-Forwarded-Proto/For fronts the
	// admin listener; only then are they trusted (otherwise attacker-controlled). Decides the session
	// cookie's Secure flag and the address the login limiter and access log see.
	authSt.trustProxy = os.Getenv("ADMIN_TRUST_PROXY") == "1"
	// Set post-construction like trustProxy; demoWriteGuard reads it off authSt.
	authSt.demoMode = demoMode
	sweepCtx, stopSweep := context.WithCancel(context.Background())
	defer stopSweep()
	go authSt.sweepSessionsLoop(sweepCtx)
	// Cloudflare Access (opt-in, cfaccess.go): CF_ACCESS_TEAM and CF_ACCESS_AUD both set enable token
	// verification for requests through Cloudflare. Validated here so a typo fails fast; keys load in the
	// background so an unreachable Cloudflare never blocks startup or LAN access.
	cfAcc, err := parseCFAccessConfig(os.Getenv("CF_ACCESS_TEAM"), os.Getenv("CF_ACCESS_AUD"), demoMode)
	if err != nil {
		log.Fatalf("cloudflare access: %v", err)
	}
	cfRequirePassword, err := parseCFAccessRequirePassword(os.Getenv("CF_ACCESS_REQUIRE_PASSWORD"))
	if err != nil {
		log.Fatalf("cloudflare access: %v", err)
	}
	if problem := cfAccessRequiresAuthProblem(cfAcc, cfRequirePassword, authSt.enabled); problem != "" {
		log.Fatalf("cloudflare access: %s", problem)
	}
	if cfAcc != nil {
		cfAcc.requirePassword = cfRequirePassword
		authSt.cfAccess = cfAcc
		cfAcc.start(context.Background())
	}
	// Set after construction; deriveDemoPublicURL is "" when demoMode is off, so handleDemo has no publicUrl.
	authSt.publicURL = demoPublicURL

	// users.multiUser requires ADMIN_PASSWORD (no auth means single-user). Checked here, not in validate(),
	// because it needs the env-derived authSt.enabled; log.Fatalf on violation.
	if problem := usersMultiUserRequiresAuthProblem(cfg.Users.MultiUser, authSt.enabled); problem != "" {
		log.Fatalf("config: %s", problem)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", handleConfig(store, configExtras{
		authEnabled: authSt.enabled,
		demoMode:    demoMode,
	}, authSt))
	// GET /api/settings: admin or any identity granted filament/electricity/printers (their embedded
	// SettingsEditor can't load without it; GET /api/config already exposes the config). PUT's scoped case
	// lives in handleSettingsPut.
	mux.HandleFunc("GET /api/settings", requireAnyPermission(db, []string{"filament", "electricity", "printers"}, handleSettingsGet(configDir, store)))
	mux.HandleFunc("PUT /api/settings", handleSettingsPut(configDir, store, authSt))
	registerRoutes(mux, db, store, authSt, configDir, demoMode) // static + quotes + clients + invoices + items + ... + proxies

	// The admin chain serves /admin/* on the main port; the public routes (the shop, /api/public/*)
	// sit beside it behind newMainRouter. PUBLIC_PORT, when set, mounts the same public routes on their own
	// listener. Both share one publicShared, so rate limits, the upload bound and the notify queue are
	// per process, not per listener.
	shared := newPublicShared()

	srv := newMainServer(":"+port, buildMainHandler(mux, authSt, store, db, configDir, shared, demoMode))

	// The optional customer-only listener is a second isolated server, started only when PUBLIC_PORT is set.
	var publicSrv *http.Server
	if publicPort := os.Getenv("PUBLIC_PORT"); publicPort != "" {
		// PUBLIC_TRUST_PROXY=1: a proxy overwriting X-Forwarded-For fronts the listener; trusted for rate limiting.
		trustProxy := os.Getenv("PUBLIC_TRUST_PROXY") == "1"
		publicSrv = newPublicServer(publicPort, store, db, configDir, shared, trustProxy, demoMode)
		go func() {
			log.Printf("printpaca public listener on :%s", publicPort)
			if err := publicSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatalf("public listen: %v", err)
			}
		}()
	}

	go func() {
		log.Printf("printpaca %s listening on :%s (config: %s)", version, port, configDir)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	// Graceful shutdown: SIGTERM/SIGINT stop both listeners concurrently within a shared 5s budget, before the
	// database closes. The notify queue drains after both Shutdowns return: on success no submission
	// is in flight, so none can still enqueue. On context.DeadlineExceeded a straggler may call enqueue
	// after stop closed the channel; enqueue/stop drop and log rather than panic, losing that notification.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh
	log.Print("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var shutdownWG sync.WaitGroup
	shutdownWG.Go(func() {
		// The notify queue drains after both listeners return from Shutdown: on success no submission is in
		// flight, so none can still enqueue.
		var serversWG sync.WaitGroup
		serversWG.Go(func() {
			if err := srv.Shutdown(ctx); err != nil {
				log.Printf("shutdown main listener: %v", err)
			}
		})
		if publicSrv != nil {
			serversWG.Go(func() {
				if err := publicSrv.Shutdown(ctx); err != nil {
					log.Printf("shutdown public listener: %v", err)
				}
			})
		}
		serversWG.Wait()
		shared.nq.stop(ctx)
	})
	if retentionDone != nil {
		shutdownWG.Go(func() {
			retentionCancel()
			select {
			case <-retentionDone:
			case <-ctx.Done():
				log.Print("shutdown: retention loop did not stop within the shutdown budget")
			}
		})
	}
	shutdownWG.Wait()

	if err := db.Close(); err != nil {
		log.Printf("close db: %v", err)
	}
}
