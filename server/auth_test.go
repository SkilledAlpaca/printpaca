package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ---------------------------------------------------------------------------
// isAllowedHost / parseAllowedHosts
// ---------------------------------------------------------------------------

func TestParseAllowedHosts(t *testing.T) {
	got := parseAllowedHosts(" printpaca.local ,, My-NAS ,")
	want := []string{"printpaca.local", "My-NAS"}
	if len(got) != len(want) {
		t.Fatalf("parseAllowedHosts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("parseAllowedHosts = %v, want %v", got, want)
		}
	}
}

func TestIsAllowedHost(t *testing.T) {
	allowed := parseAllowedHosts("printpaca.local, My-NAS")
	// newAuthState lowercases allowedHosts and isAllowedHost expects that shape, so lower it the same way.
	for i, h := range allowed {
		allowed[i] = strings.ToLower(h)
	}

	cases := []struct {
		name string
		host string
		want bool
	}{
		{"ipv4 literal with port", "192.168.1.5:8080", true},
		{"ipv4 literal no port", "192.168.1.5", true},
		{"ipv6 literal with port", "[::1]:8080", true},
		{"localhost", "localhost:8080", true},
		{"localhost no port", "localhost", true},
		{"allowlisted name", "printpaca.local:8080", true},
		{"allowlisted name case-insensitive", "PRINTPACA.LOCAL:8080", true},
		{"allowlisted name, second entry", "my-nas", true},
		{"unlisted dns name", "evil.example.com:8080", false},
		{"unlisted dns name no port", "printpaca.evil.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isAllowedHost(tc.host, allowed); got != tc.want {
				t.Errorf("isAllowedHost(%q) = %v, want %v", tc.host, got, tc.want)
			}
		})
	}
}

func TestIsAllowedHostEmptyAllowlistStillAllowsIPAndLocalhost(t *testing.T) {
	if !isAllowedHost("127.0.0.1:8080", nil) {
		t.Error("IP literal should be allowed even with no ALLOWED_HOSTS configured (e.g. Docker healthchecks)")
	}
	if !isAllowedHost("localhost:8080", nil) {
		t.Error("localhost should be allowed even with no ALLOWED_HOSTS configured")
	}
	if isAllowedHost("attacker.example.com:8080", nil) {
		t.Error("an arbitrary DNS name must not be allowed with no ALLOWED_HOSTS configured")
	}
}

// TestIsAllowedHostEntryWithPortMatchesHostWithoutPort covers the reverse case: parseAllowedHosts strips a port
// from a configured entry too, so an entry copied with a port (e.g. from the rejection log) isn't dead.
func TestIsAllowedHostEntryWithPortMatchesHostWithoutPort(t *testing.T) {
	allowed := parseAllowedHosts("printpaca.local:8080")
	if !isAllowedHost("printpaca.local", allowed) {
		t.Error("an ALLOWED_HOSTS entry carrying a port should match a Host header with no port")
	}
	if !isAllowedHost("printpaca.local:9090", allowed) {
		t.Error("an ALLOWED_HOSTS entry carrying a port should match a Host header with a DIFFERENT port (only the hostname is compared)")
	}
}

// TestHostOnly covers stripPort/hostOnly directly: port stripping, IPv6
// bracket stripping, and lowercasing.
func TestHostOnly(t *testing.T) {
	cases := []struct{ in, want string }{
		{"printpaca.local:8080", "printpaca.local"},
		{"printpaca.local", "printpaca.local"},
		{"PRINTPACA.LOCAL:8080", "printpaca.local"},
		{"[::1]:8080", "::1"},
		{"192.168.1.5:8080", "192.168.1.5"},
	}
	for _, tc := range cases {
		if got := hostOnly(tc.in); got != tc.want {
			t.Errorf("hostOnly(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// POST /api/login and /api/logout
// ---------------------------------------------------------------------------

// loginMux exposes only login/logout behind the auth middleware, wrapped as main.go wraps the admin mux.
func loginMux(auth *authState) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", auth.handleLogin)
	mux.HandleFunc("POST /api/logout", auth.handleLogout)
	mux.HandleFunc("GET /api/probe", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	return mux
}

func doLogin(t *testing.T, mux http.Handler, password string) *httptest.ResponseRecorder {
	t.Helper()
	return doLoginFrom(t, mux, password, "")
}

// doLoginFrom is doLogin with an explicit RemoteAddr to drive the per-IP login limiter from several sources.
func doLoginFrom(t *testing.T, mux http.Handler, password, remoteAddr string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":`+jsonQuote(password)+`}`))
	req.Host = "localhost"
	// csrfGuard requires application/json on POST/PUT/PATCH to /api/*; the real login form sets it.
	req.Header.Set("Content-Type", "application/json")
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	mux.ServeHTTP(rec, req)
	return rec
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestLoginSuccessSetsSessionCookie(t *testing.T) {
	auth := newAuthState("secret", nil)
	wrapped := auth.middleware(loginMux(auth))

	rec := doLogin(t, wrapped, "secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != authCookieName {
		t.Fatalf("cookies = %v, want one %q cookie", cookies, authCookieName)
	}
	c := cookies[0]
	if !c.HttpOnly {
		t.Error("session cookie must be HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", c.SameSite)
	}
	if c.Secure {
		t.Error("session cookie must not set Secure over plain HTTP (LAN deployment default)")
	}
	if c.Value == "" {
		t.Error("session cookie value must not be empty")
	}
	if c.Path != "/admin/" {
		t.Errorf("Path = %q, want /admin/ (the browser must not send the session to customer routes)", c.Path)
	}
}

// The session cookie's Secure flag follows requestIsTLS: r.TLS always counts (never attacker-controlled), X-Forwarded-Proto only under trustProxy.
func TestLoginSecureCookieDecision(t *testing.T) {
	cases := []struct {
		name       string
		trustProxy bool
		tls        bool
		header     func(h http.Header)
		wantSecure bool
	}{
		{"r.TLS set, trustProxy off", false, true, nil, true},
		{"trusted X-Forwarded-Proto https", true, false, func(h http.Header) { h.Set("X-Forwarded-Proto", "https") }, true},
		// Without ADMIN_TRUST_PROXY the header is attacker-controlled on a directly-exposed listener and must never flip Secure.
		{"untrusted X-Forwarded-Proto https", false, false, func(h http.Header) { h.Set("X-Forwarded-Proto", "https") }, false},
		// A client can send its own header while a trusted proxy appends "http" as a separate line: Get would see the spoofed
		// first line, so requestIsTLS must use Values and trust only the last.
		{"spoofed first line, proxy's last line http", true, false, func(h http.Header) {
			h.Add("X-Forwarded-Proto", "https")
			h.Add("X-Forwarded-Proto", "http")
		}, false},
		// The proxy's own "https" is the last element of a comma-joined line, like clientIP's rule for X-Forwarded-For.
		{"last element of a comma-joined line", true, false, func(h http.Header) { h.Set("X-Forwarded-Proto", "http, https") }, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth := newAuthState("secret", nil)
			auth.trustProxy = tc.trustProxy
			wrapped := auth.middleware(loginMux(auth))

			req := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"secret"}`))
			req.Host = "localhost"
			req.Header.Set("Content-Type", "application/json")
			if tc.tls {
				req.TLS = &tls.ConnectionState{} // non-nil is all requestIsTLS checks
			}
			if tc.header != nil {
				tc.header(req.Header)
			}
			rec := serveReq(wrapped, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
			}
			cookies := rec.Result().Cookies()
			if len(cookies) != 1 || cookies[0].Secure != tc.wantSecure {
				t.Errorf("cookies = %v, want one %q cookie with Secure = %v", cookies, authCookieName, tc.wantSecure)
			}
		})
	}
}

func TestLoginWrongPasswordIs401(t *testing.T) {
	auth := newAuthState("secret", nil)
	wrapped := auth.middleware(loginMux(auth))

	rec := doLogin(t, wrapped, "wrong")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body: %s", rec.Code, rec.Body.String())
	}
	var body authErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error == "" {
		t.Error("error message should not be empty")
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Error("a failed login must not set a session cookie")
	}
}

func TestLoginDisabledIs404(t *testing.T) {
	auth := newAuthState("", nil) // ADMIN_PASSWORD unset
	mux := loginMux(auth)         // unwrapped: middleware is a no-op when disabled anyway

	rec := doLogin(t, mux, "anything")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body: %s", rec.Code, rec.Body.String())
	}
}

// A flood from ONE address exhausts its tighter per-IP budget (authIPLoginRateLimit) well before the shared
// global budget (authLoginRateLimit) trips, so the delay starts earlier. Failures stay 401; only the delay changes.
func TestLoginRateLimitPerIPThrottlesBeforeGlobalBudget(t *testing.T) {
	auth := newAuthState("secret", nil)
	wrapped := auth.middleware(loginMux(auth))

	for i := range authIPLoginRateLimit {
		start := time.Now()
		rec := doLogin(t, wrapped, "wrong")
		elapsed := time.Since(start)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401; body: %s", i+1, rec.Code, rec.Body.String())
		}
		if elapsed >= authLoginThrottleMin {
			t.Fatalf("attempt %d: elapsed = %v, want < %v (per-IP budget not yet exhausted)", i+1, elapsed, authLoginThrottleMin)
		}
	}

	start := time.Now()
	rec := doLogin(t, wrapped, "wrong")
	elapsed := time.Since(start)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("attempt %d: status = %d, want 401; body: %s", authIPLoginRateLimit+1, rec.Code, rec.Body.String())
	}
	if elapsed < authLoginThrottleMin {
		t.Fatalf("attempt %d: elapsed = %v, want >= %v (per-IP budget exhausted: delay, not rejection)", authIPLoginRateLimit+1, elapsed, authLoginThrottleMin)
	}

	// The correct password still succeeds once the per-IP budget is exhausted, but pays the same delay first.
	start = time.Now()
	rec = doLogin(t, wrapped, "secret")
	elapsed = time.Since(start)
	if rec.Code != http.StatusOK {
		t.Fatalf("correct password after rate limit: status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	if elapsed < authLoginThrottleMin {
		t.Fatalf("correct password after rate limit: elapsed = %v, want >= %v (still throttled, not rejected)", elapsed, authLoginThrottleMin)
	}
}

// Rotating source addresses below the per-IP threshold cannot evade the shared global budget: many addresses each
// stay under their own per-IP budget, yet the next attempt from a fresh address still pays the throttle.
func TestLoginRateLimitGlobalBudgetStillAppliesAcrossIPs(t *testing.T) {
	auth := newAuthState("secret", nil)
	wrapped := auth.middleware(loginMux(auth))

	perIPAttempts := authIPLoginRateLimit - 1 // stay under each IP's own budget
	sources := authLoginRateLimit/perIPAttempts + 1
	total := 0
	for s := range sources {
		addr := fmt.Sprintf("203.0.113.%d:1234", s+1)
		for i := range perIPAttempts {
			start := time.Now()
			rec := doLoginFrom(t, wrapped, "wrong", addr)
			elapsed := time.Since(start)
			total++
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("source %d attempt %d: status = %d, want 401; body: %s", s, i+1, rec.Code, rec.Body.String())
			}
			if total <= authLoginRateLimit && elapsed >= authLoginThrottleMin {
				t.Fatalf("source %d attempt %d (global attempt %d): elapsed = %v, want < %v (global budget not yet exhausted)", s, i+1, total, elapsed, authLoginThrottleMin)
			}
		}
	}
	if total <= authLoginRateLimit {
		t.Fatalf("test setup: total attempts = %d, want > %d (authLoginRateLimit)", total, authLoginRateLimit)
	}

	start := time.Now()
	rec := doLoginFrom(t, wrapped, "wrong", "203.0.113.250:1234") // yet another fresh source
	elapsed := time.Since(start)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body: %s", rec.Code, rec.Body.String())
	}
	if elapsed < authLoginThrottleMin {
		t.Fatalf("elapsed = %v, want >= %v (global budget exhausted despite no single IP exceeding its own budget)", elapsed, authLoginThrottleMin)
	}
}

// ---------------------------------------------------------------------------
// Bounded throttle queue under real concurrency
// ---------------------------------------------------------------------------

// loginThrottleMu must not be held across the throttle sleep: concurrent wrong attempts would serialize and push a legitimate login past httpWriteTimeout.
// The wait queue is bounded (loginThrottleSem/loginThrottleMaxInFlight): a full queue gets an immediate logged 429, so any admitted attempt finishes within the bound however large the flood.
// The legitimate login retries after a 429, as a real client would, proving the bound holds regardless of N.
func TestLoginThrottleConcurrentFloodDoesNotBlockCorrectLogin(t *testing.T) {
	auth := newAuthState("secret", nil)
	wrapped := auth.middleware(loginMux(auth))

	// Exhaust the global budget first so every attempt below takes the throttled path.
	for i := range authLoginRateLimit {
		addr := fmt.Sprintf("198.51.100.%d:1234", i+1)
		rec := doLoginFrom(t, wrapped, "wrong", addr)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("setup attempt %d: status = %d, want 401", i, rec.Code)
		}
	}

	// Far more concurrent wrong attempts than loginThrottleMaxInFlight; a serial queue would exceed httpWriteTimeout.
	const flood = 100
	var wg sync.WaitGroup
	var floodAdmitted, floodRejected int32
	wg.Add(flood)
	for i := range flood {
		go func(i int) {
			defer wg.Done()
			addr := fmt.Sprintf("203.0.113.%d:1234", i%254+1)
			start := time.Now()
			rec := doLoginFrom(t, wrapped, "wrong", addr)
			elapsed := time.Since(start)
			switch rec.Code {
			case http.StatusUnauthorized:
				atomic.AddInt32(&floodAdmitted, 1)
				// The per-attempt delay floor is still enforced for every admitted attempt under concurrency.
				if elapsed < authLoginThrottleMin {
					t.Errorf("flood attempt %d: elapsed = %v, want >= %v (throttle floor must still hold)", i, elapsed, authLoginThrottleMin)
				}
			case http.StatusTooManyRequests:
				atomic.AddInt32(&floodRejected, 1)
			default:
				t.Errorf("flood attempt %d: status = %d, want 401 or 429; body: %s", i, rec.Code, rec.Body.String())
			}
		}(i)
	}

	legitDone := make(chan struct{})
	go func() {
		defer close(legitDone)
		deadline := time.Now().Add(30 * time.Second)
		for {
			rec := doLoginFrom(t, wrapped, "secret", "192.0.2.99:1234")
			if rec.Code == http.StatusOK {
				return
			}
			if rec.Code != http.StatusTooManyRequests {
				t.Errorf("correct-password login: status = %d, want 200 or 429; body: %s", rec.Code, rec.Body.String())
				return
			}
			if time.Now().After(deadline) {
				t.Error("correct-password login never succeeded within 30s of retrying despite the flood")
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
	}()

	select {
	case <-legitDone:
	case <-time.After(35 * time.Second):
		t.Fatal("legitimate login did not resolve within 35s -- want well under the 60s httpWriteTimeout")
	}

	wg.Wait()
	t.Logf("flood=%d: %d admitted+throttled, %d rejected with 429 (loginThrottleMaxInFlight=%d)", flood, floodAdmitted, floodRejected, loginThrottleMaxInFlight)
	if floodRejected == 0 {
		t.Errorf("expected at least some of %d concurrent flood attempts to be rejected with 429 (bound=%d)", flood, loginThrottleMaxInFlight)
	}
}

// Once loginThrottleMaxInFlight attempts are queued, the next gets an immediate 429 (not a queued wait),
// and the rejection is logged with its source.
func TestLoginThrottleSemaphoreRejectsExcessAttemptsWith429(t *testing.T) {
	auth := newAuthState("secret", nil)
	wrapped := auth.middleware(loginMux(auth))

	for i := range authLoginRateLimit {
		addr := fmt.Sprintf("198.51.100.%d:1234", i+1)
		doLoginFrom(t, wrapped, "wrong", addr)
	}

	// Drain leftover slots from a prior test deterministically rather than assume none.
	waitForSemLevel(t, 0)

	var wg sync.WaitGroup
	wg.Add(loginThrottleMaxInFlight)
	for i := range loginThrottleMaxInFlight {
		go func(i int) {
			defer wg.Done()
			addr := fmt.Sprintf("203.0.113.%d:1234", i+1)
			doLoginFrom(t, wrapped, "wrong", addr)
		}(i)
	}

	// Wait until every slot is held (whitebox on loginThrottleSem) rather than sleeping a guessed duration.
	waitForSemLevel(t, loginThrottleMaxInFlight)

	var buf bytes.Buffer
	restore := redirectLog(t, &buf)

	start := time.Now()
	// 198.51.100.1 has a recorded failure, so it is not a clean source and gets no reserved lane.
	rec := doLoginFrom(t, wrapped, "wrong", "198.51.100.1:1234")
	elapsed := time.Since(start)
	restore()

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body: %s", rec.Code, rec.Body.String())
	}
	if elapsed >= authLoginThrottleMin {
		t.Fatalf("elapsed = %v, want well under %v (429 must be immediate, never a queued wait)", elapsed, authLoginThrottleMin)
	}
	if !strings.Contains(buf.String(), "198.51.100.1") {
		t.Errorf("429 rejection was not logged with its source: %q", buf.String())
	}

	wg.Wait()
}

// waitForSemLevel polls loginThrottleSem until it holds exactly n slots, or fails after a generous deadline.
func waitForSemLevel(t *testing.T, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for len(loginThrottleSem) != n {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for loginThrottleSem to reach %d (currently %d)", n, len(loginThrottleSem))
		}
		time.Sleep(time.Millisecond)
	}
}

func TestLogoutClearsSessionAndCookie(t *testing.T) {
	auth := newAuthState("secret", nil)
	wrapped := auth.middleware(loginMux(auth))

	loginRec := doLogin(t, wrapped, "secret")
	cookie := loginRec.Result().Cookies()[0]

	// The session works before logout.
	probe := httptest.NewRequest("GET", "/api/probe", nil)
	probe.Host = "localhost"
	probe.AddCookie(cookie)
	probeRec := serveReq(wrapped, probe)
	if probeRec.Code != http.StatusOK {
		t.Fatalf("probe before logout: status = %d, want 200", probeRec.Code)
	}

	logoutReq := httptest.NewRequest("POST", "/api/logout", nil)
	logoutReq.Host = "localhost"
	logoutReq.AddCookie(cookie)
	logoutRec := serveReq(wrapped, logoutReq)
	if logoutRec.Code != http.StatusOK {
		t.Fatalf("logout: status = %d, want 200; body: %s", logoutRec.Code, logoutRec.Body.String())
	}
	cleared := logoutRec.Result().Cookies()[0]
	if cleared.MaxAge >= 0 {
		t.Errorf("logout cookie MaxAge = %d, want negative (cleared)", cleared.MaxAge)
	}
	if cleared.Path != "/admin/" {
		t.Errorf("logout cookie Path = %q, want /admin/ (a clear only matches the cookie it was set with)", cleared.Path)
	}

	// The old session no longer works after logout.
	probe2 := httptest.NewRequest("GET", "/api/probe", nil)
	probe2.Host = "localhost"
	probe2.AddCookie(cookie)
	probeRec2 := serveReq(wrapped, probe2)
	if probeRec2.Code != http.StatusUnauthorized {
		t.Fatalf("probe after logout: status = %d, want 401", probeRec2.Code)
	}
}

// ---------------------------------------------------------------------------
// middleware: session enforcement + exemptions
// ---------------------------------------------------------------------------

// With auth disabled no session is required, but the Host allowlist still applies: it defends the listener
// against DNS rebinding and isn't conditioned on a.enabled.
func TestMiddlewareDisabledSkipsSessionButNotHostCheck(t *testing.T) {
	auth := newAuthState("", nil)
	mux := loginMux(auth)
	wrapped := auth.middleware(mux)

	unlisted := httptest.NewRequest("GET", "/api/probe", nil)
	unlisted.Host = "totally-unlisted-host.example.com"
	rec := serveReq(wrapped, unlisted)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unlisted host, auth disabled: status = %d, want 403 (host allowlist applies regardless of auth mode)", rec.Code)
	}

	allowed := httptest.NewRequest("GET", "/api/probe", nil)
	allowed.Host = "localhost"
	rec2 := serveReq(wrapped, allowed)
	if rec2.Code != http.StatusOK {
		t.Fatalf("localhost, auth disabled: status = %d, want 200 (no session required)", rec2.Code)
	}
}

func TestMiddlewareRequiresSessionForApi(t *testing.T) {
	auth := newAuthState("secret", nil)
	wrapped := auth.middleware(loginMux(auth))

	req := httptest.NewRequest("GET", "/api/probe", nil)
	req.Host = "localhost"
	rec := serveReq(wrapped, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body: %s", rec.Code, rec.Body.String())
	}
	var body authErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error != "unauthorized" {
		t.Errorf("error = %q, want %q", body.Error, "unauthorized")
	}
}

func TestMiddlewareAllowsValidSession(t *testing.T) {
	auth := newAuthState("secret", nil)
	wrapped := auth.middleware(loginMux(auth))

	cookie := doLogin(t, wrapped, "secret").Result().Cookies()[0]

	req := httptest.NewRequest("GET", "/api/probe", nil)
	req.Host = "localhost"
	req.AddCookie(cookie)
	rec := serveReq(wrapped, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
}

func TestMiddlewareExemptsLoginAndHealthzWithoutSession(t *testing.T) {
	auth := newAuthState("secret", nil)
	mux := loginMux(auth)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	wrapped := auth.middleware(mux)

	for _, path := range []string{"/healthz"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = "localhost"
		rec := serveReq(wrapped, req)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200 (exempt from session requirement)", path, rec.Code)
		}
	}

	// /api/login must be reachable with no session: a wrong password yields handleLogin's 401, never a host/session rejection.
	rec := doLogin(t, wrapped, "wrong")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("login without a session: status = %d, want 401 from handleLogin itself", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// csrfGuard
// ---------------------------------------------------------------------------

func TestCSRFGuard(t *testing.T) {
	const loginBody = `{"password":"secret"}`
	cases := []struct {
		name        string
		password    string // "" disables auth
		method      string
		path        string
		body        string
		contentType string
		origin      string
		chunked     bool // ContentLength -1, as for Transfer-Encoding: chunked
		session     bool // send a valid session cookie
		wantStatus  int  // exact status; 0 skips
		notStatus   int  // status that must not occur; 0 skips
	}{
		// A non-JSON Content-Type is rejected before the handler runs: the CORS "simple request" shape a cross-origin page could use to blind-write.
		{"non-JSON content type", "secret", "POST", "/api/login", loginBody, "text/plain", "", false, false, http.StatusUnsupportedMediaType, 0},
		// A chunked body reports ContentLength == -1; the guard must still require application/json, not skip as for an empty body.
		{"chunked non-JSON body", "secret", "POST", "/api/login", loginBody, "text/plain", "", true, false, http.StatusUnsupportedMediaType, 0},
		// A genuinely empty body (ContentLength == 0) skips the Content-Type check, so a bodyless POST such as logout isn't 415'd.
		{"bodyless request", "secret", "POST", "/api/logout", "", "", "", false, false, 0, http.StatusUnsupportedMediaType},
		// The guard runs even with auth disabled: that opted-out mode has no session/SameSite defense at all.
		{"applies when auth disabled", "", "POST", "/api/probe", `{}`, "text/plain", "", false, false, http.StatusUnsupportedMediaType, 0},
		// An Origin naming a different host than the request arrived on is rejected even with Content-Type application/json.
		{"cross-origin write", "secret", "POST", "/api/login", loginBody, "application/json", "http://evil.example.com", false, false, http.StatusForbidden, 0},
		// originAllowed must NOT reuse isAllowedHost's "any IP literal is safe" rule (a Host-header rebinding defense):
		// trusting it here would let a page served from a raw IP pass.
		{"cross-origin IP literal", "secret", "POST", "/api/login", loginBody, "application/json", "http://203.0.113.9", false, false, http.StatusForbidden, 0},
		{"same-origin JSON", "secret", "POST", "/api/login", loginBody, "application/json", "http://localhost", false, false, http.StatusOK, 0},
		// The Content-Type check never applies to DELETE (the SPA's DELETE calls send no body or Content-Type), but the Origin check does.
		{"bodyless DELETE", "secret", "DELETE", "/api/probe", "", "", "", false, true, http.StatusOK, 0},
		{"cross-origin DELETE", "secret", "DELETE", "/api/probe", "", "", "http://evil.example.com", false, true, http.StatusForbidden, 0},
		// POST /api/logout, the one bodyless POST endpoint, is unaffected by the Content-Type check.
		{"bodyless logout", "secret", "POST", "/api/logout", "", "", "", false, true, http.StatusOK, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth := newAuthState(tc.password, nil)
			mux := loginMux(auth)
			mux.HandleFunc("POST /api/probe", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
			mux.HandleFunc("DELETE /api/probe", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
			wrapped := auth.middleware(mux)

			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, body)
			req.Host = "localhost"
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.chunked {
				req.ContentLength = -1
			}
			if tc.session {
				req.AddCookie(doLogin(t, wrapped, "secret").Result().Cookies()[0])
			}
			rec := serveReq(wrapped, req)
			if tc.wantStatus != 0 && rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.notStatus != 0 && rec.Code == tc.notStatus {
				t.Fatalf("status = %d, which this request must be exempt from; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMiddlewareLeavesStaticAssetsOpen(t *testing.T) {
	auth := newAuthState("secret", nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	wrapped := auth.middleware(mux)

	req := httptest.NewRequest("GET", "/assets/app.js", nil)
	req.Host = "localhost"
	rec := serveReq(wrapped, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (static assets stay open so the login screen can load)", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// middleware: Host allowlist
// ---------------------------------------------------------------------------

func TestMiddlewareHostAllowlistBlocksUnlistedHost(t *testing.T) {
	auth := newAuthState("secret", []string{"printpaca.local"})
	wrapped := auth.middleware(loginMux(auth))

	req := httptest.NewRequest("GET", "/api/probe", nil)
	req.Host = "evil.example.com"
	rec := serveReq(wrapped, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ALLOWED_HOSTS") {
		t.Errorf("403 body should mention ALLOWED_HOSTS: %q", rec.Body.String())
	}
}

func TestMiddlewareHostAllowlistAllowsListedHost(t *testing.T) {
	auth := newAuthState("secret", []string{"printpaca.local"})
	wrapped := auth.middleware(loginMux(auth))

	// A listed host reaches handleLogin normally (401 for a wrong password,
	// never the middleware's 403 host rejection).
	loginReq := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"wrong"}`))
	loginReq.Host = "printpaca.local:8080"
	loginReq.Header.Set("Content-Type", "application/json")
	loginRec := serveReq(wrapped, loginReq)
	if loginRec.Code != http.StatusUnauthorized {
		t.Fatalf("listed host login: status = %d, want 401 (not a host rejection); body: %s", loginRec.Code, loginRec.Body.String())
	}

	// An IP literal is always allowed regardless of ALLOWED_HOSTS (Docker
	// healthchecks rely on this).
	req := httptest.NewRequest("GET", "/api/probe", nil)
	req.Host = "127.0.0.1:8080"
	probeRec := serveReq(wrapped, req)
	if probeRec.Code == http.StatusForbidden {
		t.Fatalf("IP literal host: status = %d, should never be 403", probeRec.Code)
	}
}

func TestMiddlewareHostAllowlistAppliesToStaticAssets(t *testing.T) {
	auth := newAuthState("secret", nil) // no ALLOWED_HOSTS: only IP literal/localhost pass
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	wrapped := auth.middleware(mux)

	req := httptest.NewRequest("GET", "/assets/app.js", nil)
	req.Host = "evil.example.com"
	rec := serveReq(wrapped, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (host allowlist applies to static assets too)", rec.Code)
	}
}

// A rejected Host must log the offending value and name the fix, so a misconfigured ALLOWED_HOSTS is diagnosable.
func TestMiddlewareHostAllowlistRejectionIsLogged(t *testing.T) {
	auth := newAuthState("secret", []string{"printpaca.local"})
	wrapped := auth.middleware(loginMux(auth))

	var buf bytes.Buffer
	restore := redirectLog(t, &buf)
	defer restore()

	req := httptest.NewRequest("GET", "/api/probe", nil)
	req.Host = "evil.example.com"
	rec := serveReq(wrapped, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}

	logged := buf.String()
	if !strings.Contains(logged, "evil.example.com") {
		t.Errorf("log output missing the rejected Host value %q: %q", "evil.example.com", logged)
	}
	if !strings.Contains(logged, "ALLOWED_HOSTS") {
		t.Errorf("log output missing the ALLOWED_HOSTS remedy: %q", logged)
	}
}

// A burst of requests for the SAME rejected Host produces at most one log line (hostRejectLogLimiter), so probing cannot flood the log.
func TestMiddlewareHostAllowlistRejectionLogIsRateLimited(t *testing.T) {
	auth := newAuthState("secret", []string{"printpaca.local"})
	wrapped := auth.middleware(loginMux(auth))

	var buf bytes.Buffer
	restore := redirectLog(t, &buf)
	defer restore()

	for i := range 10 {
		req := httptest.NewRequest("GET", "/api/probe", nil)
		req.Host = "evil.example.com"
		rec := serveReq(wrapped, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("request %d: status = %d, want 403", i, rec.Code)
		}
	}

	if n := strings.Count(buf.String(), "evil.example.com"); n != 1 {
		t.Errorf("log line for evil.example.com appeared %d times across 10 rejections, want exactly 1: %q", n, buf.String())
	}
}

// The rejection log must print the port-stripped host the comparison used, and pasting that value into
// ALLOWED_HOSTS must fix the rejection (parseAllowedHosts never stripped a port from a configured entry).
func TestMiddlewareHostAllowlistRejectionLogsPortStrippedHostAndFixWorks(t *testing.T) {
	auth := newAuthState("secret", nil)
	wrapped := auth.middleware(loginMux(auth))

	var buf bytes.Buffer
	restore := redirectLog(t, &buf)

	req := httptest.NewRequest("GET", "/api/probe", nil)
	req.Host = "printpaca.local:8080"
	rec := serveReq(wrapped, req)
	restore()

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	logged := buf.String()
	if strings.Contains(logged, ":8080") {
		t.Errorf("log line must not include the port: %q", logged)
	}
	if !strings.Contains(logged, `"printpaca.local"`) {
		t.Errorf("log line missing the port-stripped host %q: %q", "printpaca.local", logged)
	}

	// Pasting the logged value into ALLOWED_HOSTS must make the next request succeed; auth stays disabled so only the Host allowlist is tested.
	auth2 := newAuthState("", parseAllowedHosts("printpaca.local"))
	wrapped2 := auth2.middleware(loginMux(auth2))
	req2 := httptest.NewRequest("GET", "/api/probe", nil)
	req2.Host = "printpaca.local:8080"
	rec2 := serveReq(wrapped2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("after adding the logged host to ALLOWED_HOSTS: status = %d, want 200; body: %s", rec2.Code, rec2.Body.String())
	}
}

// hostRejectLogLimiter is keyed on the attacker-controlled r.Host, so rotating Hosts would flood appLogRing.
// The aggregate budget (hostRejectLogAggregateKey/Limit) caps a rotating-Host flood at Limit lines per minute.
func TestMiddlewareHostRejectionLogAggregateBoundCapsRotatingHostFlood(t *testing.T) {
	auth := newAuthState("secret", nil)
	wrapped := auth.middleware(loginMux(auth))

	var buf bytes.Buffer
	restore := redirectLog(t, &buf)
	defer restore()

	const attempts = hostRejectLogAggregateLimit * 3
	for i := range attempts {
		req := httptest.NewRequest("GET", "/api/probe", nil)
		req.Host = fmt.Sprintf("evil-%d.example.com", i) // a fresh Host value every time
		rec := serveReq(wrapped, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("attempt %d: status = %d, want 403", i, rec.Code)
		}
	}

	lines := strings.Count(buf.String(), "admin listener: rejected request for disallowed Host")
	if lines > hostRejectLogAggregateLimit {
		t.Errorf("logged %d lines for %d distinct rejected Host values within a minute, want at most %d (hostRejectLogAggregateLimit)", lines, attempts, hostRejectLogAggregateLimit)
	}
	if lines == 0 {
		t.Error("expected at least one logged line before the aggregate budget was exhausted")
	}
}

// A single repeated hostname still gets its own one-per-minute named line alongside the aggregate budget.
func TestMiddlewareHostRejectionLogAggregateBoundStillNamesARepeatedHostname(t *testing.T) {
	auth := newAuthState("secret", nil)
	wrapped := auth.middleware(loginMux(auth))

	var buf bytes.Buffer
	restore := redirectLog(t, &buf)
	defer restore()

	for i := range 5 {
		req := httptest.NewRequest("GET", "/api/probe", nil)
		req.Host = "printpaca.local"
		rec := serveReq(wrapped, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("request %d: status = %d, want 403", i, rec.Code)
		}
	}

	if n := strings.Count(buf.String(), "printpaca.local"); n != 1 {
		t.Errorf("log line for the repeated hostname appeared %d times across 5 rejections, want exactly 1: %q", n, buf.String())
	}
}

// ---------------------------------------------------------------------------
// Cloudflare tunnel guard
// ---------------------------------------------------------------------------

// The tunnel guard has three outcomes for a request carrying a Cloudflare-only header (either header is
// sufficient evidence) when Cloudflare Access is not configured: with a password set the request continues
// to the normal chain (here a 401 without a session) and a log line notes that the password is the only
// lock; with authentication off it is a 403 that never reaches the wrapped handler; and demo mode, which is
// served through the tunnel, passes either header. Without the headers the request is untouched.
func TestMiddlewareTunnelGuard(t *testing.T) {
	cases := []struct {
		name         string
		password     string // "" = authentication off
		demo         bool
		method, path string
		body         string
		header       [2]string // name, value; zero for none
		wantStatus   int
		wantBody     string // substring of the response body
		wantLog      string // substring of the log output
	}{
		{"password set: CF-Connecting-IP continues to auth", "secret", false, "GET", "/api/probe", "", [2]string{"CF-Connecting-IP", "203.0.113.7"}, http.StatusUnauthorized, "", "the printpaca password is the only lock"},
		{"password set: Cf-Ray alone continues to auth", "secret", false, "GET", "/api/probe", "", [2]string{"Cf-Ray", "8c1234567890abcd-SJC"}, http.StatusUnauthorized, "", "the printpaca password is the only lock"},
		{"password set: tunneled login works", "secret", false, "POST", "/api/login", `{"password":"secret"}`, [2]string{"CF-Connecting-IP", "203.0.113.7"}, http.StatusOK, "", "the printpaca password is the only lock"},
		{"auth off: CF-Connecting-IP rejected", "", false, "GET", "/api/probe", "", [2]string{"CF-Connecting-IP", "203.0.113.7"}, http.StatusForbidden, "authentication is off", "authentication is off"},
		{"auth off: Cf-Ray alone rejected", "", false, "GET", "/api/probe", "", [2]string{"Cf-Ray", "8c1234567890abcd-SJC"}, http.StatusForbidden, "ADMIN_PASSWORD", ""},
		{"no Cloudflare headers passes", "secret", false, "POST", "/api/login", `{"password":"wrong"}`, [2]string{}, http.StatusUnauthorized, "", ""},
		{"no Cloudflare headers, auth off passes", "", false, "GET", "/api/probe", "", [2]string{}, http.StatusOK, "", ""},
		{"demo mode skips for CF-Connecting-IP", "secret", true, "POST", "/api/login", `{"password":"wrong"}`, [2]string{"CF-Connecting-IP", "203.0.113.7"}, http.StatusUnauthorized, "", ""},
		{"demo mode skips for Cf-Ray", "secret", true, "POST", "/api/login", `{"password":"wrong"}`, [2]string{"Cf-Ray", "8c1234567890abcd-SJC"}, http.StatusUnauthorized, "", ""},
		{"demo mode, auth off, skips the guard", "", true, "GET", "/api/probe", "", [2]string{"CF-Connecting-IP", "203.0.113.7"}, http.StatusOK, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth := newAuthState(tc.password, nil)
			auth.demoMode = tc.demo
			wrapped := auth.middleware(loginMux(auth))

			var buf bytes.Buffer
			restore := redirectLog(t, &buf)
			defer restore()

			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req := httptest.NewRequest(tc.method, tc.path, body)
			req.Host = "127.0.0.1:8080" // an allowed Host, so only the tunnel guard is under test
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if tc.header[0] != "" {
				req.Header.Set(tc.header[0], tc.header[1])
			}
			rec := serveReq(wrapped, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Errorf("response body should contain %q: %q", tc.wantBody, rec.Body.String())
			}
			if !strings.Contains(buf.String(), tc.wantLog) {
				t.Errorf("log output should contain %q: %q", tc.wantLog, buf.String())
			}
		})
	}
}

// A burst of tunneled requests produces at most one tunnel-guard log line per window, whichever way the guard goes.
func TestMiddlewareTunnelGuardLogIsRateLimited(t *testing.T) {
	for _, tc := range []struct {
		name     string
		password string
		want     int
	}{
		{"password set", "secret", http.StatusUnauthorized},
		{"auth off", "", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := newAuthState(tc.password, nil)
			wrapped := auth.middleware(loginMux(auth))

			var buf bytes.Buffer
			restore := redirectLog(t, &buf)
			defer restore()

			for i := range 10 {
				req := httptest.NewRequest("GET", "/api/probe", nil)
				req.Host = "127.0.0.1:8080"
				req.Header.Set("CF-Connecting-IP", "203.0.113.7")
				rec := serveReq(wrapped, req)
				if rec.Code != tc.want {
					t.Fatalf("request %d: status = %d, want %d", i, rec.Code, tc.want)
				}
			}

			if n := strings.Count(buf.String(), "\n"); n != 1 {
				t.Errorf("tunnel-guard log line appeared %d times across 10 requests, want exactly 1: %q", n, buf.String())
			}
		})
	}
}

// The guard lives only in the admin chain: the public routes, which are tunneled, must be unaffected by the same headers.
func TestPublicListenerUnaffectedByCloudflareHeaders(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /probe", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := publicListenerHandler(mux, false)

	req := httptest.NewRequest("GET", "/probe", nil)
	req.Header.Set("CF-Connecting-IP", "203.0.113.7")
	req.Header.Set("Cf-Ray", "8c1234567890abcd-SJC")
	rec := serveReq(handler, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the public listener must never apply the admin-only tunnel guard); body: %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Session TTL / expiry
// ---------------------------------------------------------------------------

func TestValidSessionRejectsExpiredToken(t *testing.T) {
	auth := newAuthState("secret", nil)
	token := "deadbeef"
	auth.sessions[token] = authSession{expires: time.Now().Add(-time.Minute), username: "admin", isAdmin: true} // already expired

	if _, ok := auth.resolveSession(token); ok {
		t.Error("an expired session must not validate")
	}
	auth.mu.Lock()
	_, stillPresent := auth.sessions[token]
	auth.mu.Unlock()
	if stillPresent {
		t.Error("an expired session should be pruned on lookup")
	}
}

func TestValidSessionDoesNotSlideExpiry(t *testing.T) {
	auth := newAuthState("secret", nil)
	token := "deadbeef"
	expires := time.Now().Add(time.Hour)
	auth.sessions[token] = authSession{expires: expires, username: "admin", isAdmin: true}

	id, ok := auth.resolveSession(token)
	if !ok {
		t.Fatal("a not-yet-expired session should validate")
	}
	if !id.isAdmin || id.username != "admin" {
		t.Errorf("resolveSession identity = %+v, want admin", id)
	}
	auth.mu.Lock()
	after := auth.sessions[token]
	auth.mu.Unlock()
	if !after.expires.Equal(expires) {
		t.Error("resolveSession must not extend the absolute session lifetime")
	}
}

func TestSweepSessionsDropsOnlyExpired(t *testing.T) {
	auth := newAuthState("secret", nil)
	auth.sessions["old"] = authSession{expires: time.Now().Add(-time.Minute)}
	auth.sessions["live"] = authSession{expires: time.Now().Add(time.Hour)}
	if n := auth.sweepSessions(); n != 1 {
		t.Errorf("sweepSessions removed %d, want 1", n)
	}
	if _, ok := auth.sessions["old"]; ok {
		t.Error("expired session survived the sweep")
	}
	if _, ok := auth.sessions["live"]; !ok {
		t.Error("live session was swept")
	}
}

// checkUserLogin is the employee half of handleLogin; newCheckUserLoginState wires just db/configStore with multiUser on.

func newCheckUserLoginState(t *testing.T) *authState {
	t.Helper()
	dir := t.TempDir()
	db := openTestDB(t, dir)
	cfg := loadTestConfig(t, dir)
	cfg.Users.MultiUser = true
	auth := newAuthState("adminpw", nil)
	auth.db = db
	auth.configStore = NewConfigStore(cfg)
	return auth
}

func TestCheckUserLogin(t *testing.T) {
	cases := []struct {
		name     string
		password string
		prepare  func(t *testing.T, auth *authState, u User) // nil for none
		wantOK   bool
	}{
		{"enabled user", "password123", nil, true},
		{"wrong password", "wrong", nil, false},
		{"disabled user fails like a wrong password", "password123", func(t *testing.T, auth *authState, u User) {
			disabled := true
			if _, _, err := updateUserFields(auth.db, u.ID, nil, &disabled, nil); err != nil {
				t.Fatalf("disable user: %v", err)
			}
		}, false},
		// An employee login must fail while users.multiUser is off, even with valid credentials.
		{"multiUser off", "password123", func(t *testing.T, auth *authState, u User) {
			cfg := auth.configStore.Get()
			cfg.Users.MultiUser = false
			auth.configStore.Set(cfg)
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			auth := newCheckUserLoginState(t)
			u, _, err := createUser(auth.db, "alice", "password123", []string{"quotes"}, "")
			if err != nil {
				t.Fatalf("createUser: %v", err)
			}
			if tc.prepare != nil {
				tc.prepare(t, auth, u)
			}
			correct, username, _ := auth.checkUserLogin("alice", tc.password)
			if correct != tc.wantOK {
				t.Errorf("checkUserLogin correct = %v, want %v", correct, tc.wantOK)
			}
			if tc.wantOK && username != "alice" {
				t.Errorf("checkUserLogin username = %q, want \"alice\"", username)
			}
		})
	}
}

// An unknown username burns a bcrypt compare against a fixed dummy hash so timing does not enumerate users.
// Timing can't be asserted reliably in CI, so assert it fails exactly like a wrong password and that
// dummyBcryptHash is bcrypt-shaped and distinct from any real password.
func TestCheckUserLoginUnknownUserExecutesDummyCompare(t *testing.T) {
	auth := newCheckUserLoginState(t)
	correct, username, _ := auth.checkUserLogin("nobody", "whatever")
	if correct || username != "" {
		t.Errorf("checkUserLogin for unknown user = (%v, %q), want (false, \"\")", correct, username)
	}
	if err := bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte("printpaca-timing-safety-dummy")); err != nil {
		t.Errorf("dummyBcryptHash does not verify against its own known input: %v", err)
	}
}

// POST /api/login with a username creates a session with that identity, distinct from the env-admin's.
func TestLoginHTTPWithUsernameCreatesEmployeeSession(t *testing.T) {
	handler, auth, _ := newMultiUserTestMux(t, false)
	if _, _, err := createUser(auth.db, "alice", "password123", []string{"quotes"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	cookie := employeeSessionCookie(t, handler, "alice")

	id, ok := auth.resolveSession(cookie.Value)
	if !ok {
		t.Fatal("resolveSession should find the just-created employee session")
	}
	if id.isAdmin || id.username != "alice" {
		t.Errorf("session identity = %+v, want isAdmin=false username=alice", id)
	}
}

// An unknown username and a wrong password return the exact same 401 body as a wrong admin password.
func TestLoginUnknownUsernameAndWrongPasswordReturnSameMessage(t *testing.T) {
	handler, _, _ := newMultiUserTestMux(t, false)

	rec := doJSON(t, handler, "POST", "/api/login", nil, map[string]string{"username": "nobody", "password": "whatever"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown username: status = %d, want 401", rec.Code)
	}
	var unknownBody authErrorBody
	json.Unmarshal(rec.Body.Bytes(), &unknownBody)

	rec2 := doJSON(t, handler, "POST", "/api/login", nil, map[string]string{"password": "wrong-admin-password"})
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("wrong admin password: status = %d, want 401", rec2.Code)
	}
	var wrongBody authErrorBody
	json.Unmarshal(rec2.Body.Bytes(), &wrongBody)

	if unknownBody.Error != wrongBody.Error {
		t.Errorf("unknown-user error %q != wrong-password error %q -- must be identical to avoid enumeration", unknownBody.Error, wrongBody.Error)
	}
}

// ---------------------------------------------------------------------------
// Concurrency: sessions map under -race.
// ---------------------------------------------------------------------------

// Hammers resolveSession, handleLogin, killSessionsForUsername and killAllEmployeeSessions concurrently; meaningful under -race.
func TestSessionMapConcurrentAccessRace(t *testing.T) {
	handler, auth, _ := newMultiUserTestMux(t, false)
	if _, _, err := createUser(auth.db, "alice", "password123", []string{"quotes"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	cookie := employeeSessionCookie(t, handler, "alice")

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			auth.resolveSession(cookie.Value)
		})
		wg.Go(func() {
			doJSON(t, handler, "GET", "/api/me", cookie, nil)
		})
		wg.Go(func() {
			auth.killSessionsForUsername("nobody-else", "")
		})
		wg.Go(func() {
			auth.killAllEmployeeSessions()
		})
	}
	wg.Wait()
}

// registerRoutes wiring: an unauthenticated call (newAuthState("", nil)) behaves as every other test file assumes.

func TestRegisterRoutesAuthDisabledLoginIs404(t *testing.T) {
	mux := newTestMux(t, t.TempDir()) // wires a disabled authState (store_test.go)
	rec := serveReq(mux, httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"x"}`)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (auth disabled: nothing to log in to)", rec.Code)
	}
}

// ---------------------------------------------------------------------------
// resolveAdminPassword / generateAdminPassword (secure-by-default admin auth)
// ---------------------------------------------------------------------------

// unsetAdminPasswordEnv unsets ADMIN_PASSWORD for the test and restores it on cleanup; t.Setenv cannot express
// "unset", which resolveAdminPassword distinguishes from "set to empty" via os.LookupEnv.
func unsetAdminPasswordEnv(t *testing.T) {
	t.Helper()
	old, had := os.LookupEnv("ADMIN_PASSWORD")
	if err := os.Unsetenv("ADMIN_PASSWORD"); err != nil {
		t.Fatalf("unsetenv ADMIN_PASSWORD: %v", err)
	}
	t.Cleanup(func() {
		if had {
			os.Setenv("ADMIN_PASSWORD", old)
		} else {
			os.Unsetenv("ADMIN_PASSWORD")
		}
	})
}

func TestResolveAdminPasswordEnvIsUsedVerbatim(t *testing.T) {
	cases := []struct {
		name string
		env  string
	}{
		{"non-empty wins", "my-own-password"},
		// The floor is inclusive: exactly minAdminPasswordLength characters is accepted.
		{"exactly at the floor", "exactly8"},
		// Present but empty is the only opt-out: auth stays disabled.
		{"explicit empty disables auth", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "exactly at the floor" && len(tc.env) != minAdminPasswordLength {
				t.Fatalf("test fixture %q is %d characters, want exactly %d", tc.env, len(tc.env), minAdminPasswordLength)
			}
			t.Setenv("ADMIN_PASSWORD", tc.env)
			dir := t.TempDir()

			pw, err := resolveAdminPassword(dir)
			if err != nil {
				t.Fatalf("resolveAdminPassword: %v", err)
			}
			if pw != tc.env {
				t.Errorf("password = %q, want %q", pw, tc.env)
			}
			if _, err := os.Stat(filepath.Join(dir, adminPasswordFileName)); !os.IsNotExist(err) {
				t.Error("an explicit ADMIN_PASSWORD must not write CONFIG_DIR/admin-password")
			}
			// newAuthState derives enabled from password != "".
			if auth := newAuthState(pw, nil); auth.enabled != (tc.env != "") {
				t.Errorf("newAuthState(pw, nil).enabled = %v, want %v", auth.enabled, tc.env != "")
			}
		})
	}
}

// An explicit ADMIN_PASSWORD below minAdminPasswordLength is startup-fatal; the error names the actual and required length.
func TestResolveAdminPasswordEnvTooShortErrors(t *testing.T) {
	t.Setenv("ADMIN_PASSWORD", "short1")
	dir := t.TempDir()

	_, err := resolveAdminPassword(dir)
	if err == nil {
		t.Fatal("resolveAdminPassword: want error for a too-short ADMIN_PASSWORD, got nil")
	}
	if !strings.Contains(err.Error(), "ADMIN_PASSWORD") || !strings.Contains(err.Error(), "8") {
		t.Errorf("error = %q, want it to name ADMIN_PASSWORD and the %d-character floor", err.Error(), minAdminPasswordLength)
	}
	if _, statErr := os.Stat(filepath.Join(dir, adminPasswordFileName)); !os.IsNotExist(statErr) {
		t.Error("a rejected ADMIN_PASSWORD must not write CONFIG_DIR/admin-password")
	}
}

func TestResolveAdminPasswordUnsetGeneratesAndPersists(t *testing.T) {
	unsetAdminPasswordEnv(t)
	dir := t.TempDir()

	pw1, err := resolveAdminPassword(dir)
	if err != nil {
		t.Fatalf("first resolveAdminPassword: %v", err)
	}
	if len(pw1) != adminPasswordLength {
		t.Fatalf("generated password length = %d, want %d", len(pw1), adminPasswordLength)
	}
	if auth := newAuthState(pw1, nil); !auth.enabled {
		t.Error("a generated password must enable auth")
	}

	path := filepath.Join(dir, adminPasswordFileName)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("%s mode = %o, want 0600", path, perm)
	}

	// A second startup against the same CONFIG_DIR must reuse the persisted password, or every restart locks the operator out.
	pw2, err := resolveAdminPassword(dir)
	if err != nil {
		t.Fatalf("second resolveAdminPassword: %v", err)
	}
	if pw2 != pw1 {
		t.Fatalf("second startup password = %q, want reused %q", pw2, pw1)
	}
}

func TestResolveAdminPasswordTrimsTrailingNewline(t *testing.T) {
	unsetAdminPasswordEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, adminPasswordFileName)
	if err := os.WriteFile(path, []byte("hand-written-password\n"), 0o600); err != nil {
		t.Fatalf("seed admin-password file: %v", err)
	}

	pw, err := resolveAdminPassword(dir)
	if err != nil {
		t.Fatalf("resolveAdminPassword: %v", err)
	}
	if pw != "hand-written-password" {
		t.Errorf("password = %q, want %q (trailing newline trimmed)", pw, "hand-written-password")
	}
}

func TestResolveAdminPasswordEmptyFileErrors(t *testing.T) {
	unsetAdminPasswordEnv(t)
	dir := t.TempDir()
	path := filepath.Join(dir, adminPasswordFileName)
	if err := os.WriteFile(path, []byte("\n  \n"), 0o600); err != nil {
		t.Fatalf("seed empty admin-password file: %v", err)
	}

	if _, err := resolveAdminPassword(dir); err == nil {
		t.Error("resolveAdminPassword should fail on a present-but-empty admin-password file rather than silently regenerating")
	}
}

func TestResolveAdminPasswordUnreadableConfigDirErrors(t *testing.T) {
	unsetAdminPasswordEnv(t)
	dir := t.TempDir()
	// A CONFIG_DIR that is a file makes ReadFile fail with ENOTDIR, exercising the error path without relying on
	// permission bits, which don't bind root.
	blocked := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed blocker file: %v", err)
	}

	_, err := resolveAdminPassword(blocked)
	if err == nil {
		t.Fatal("resolveAdminPassword should fail when CONFIG_DIR isn't usable")
	}
	if !strings.Contains(err.Error(), blocked) {
		t.Errorf("error %q should name the offending path %q", err.Error(), blocked)
	}
}

func TestGenerateAdminPasswordLengthAndAlphabet(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		pw := generateAdminPassword()
		if len(pw) != adminPasswordLength {
			t.Fatalf("length = %d, want %d", len(pw), adminPasswordLength)
		}
		for _, c := range pw {
			if !strings.ContainsRune(adminPasswordAlphabet, c) {
				t.Fatalf("password %q contains char %q outside adminPasswordAlphabet", pw, c)
			}
		}
		seen[pw] = true
	}
	if len(seen) != 50 {
		t.Errorf("generated %d distinct passwords out of 50 calls, want 50 (collision suggests a broken RNG path)", len(seen))
	}
}

// Two sources that spent their failure budgets and keep flooding must not lock out a clean third source:
// each flooder holds at most loginThrottlePerSource queue slots, so the real admin still queues and logs in.
func TestLoginTwoFloodingSourcesDoNotLockOutCleanSource(t *testing.T) {
	waitForSemLevel(t, 0)
	auth := newAuthState("secret", nil)
	wrapped := auth.middleware(loginMux(auth))

	flooders := []string{"198.51.100.1:1", "198.51.100.2:1"}
	for _, addr := range flooders {
		for range authIPLoginRateLimit {
			doLoginFrom(t, wrapped, "wrong", addr)
		}
	}
	if !auth.loginLimiter.exhausted("login", authLoginRateLimit, authLoginRateWindow) {
		t.Fatal("global budget not exhausted by the two flooders")
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for _, addr := range flooders {
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
					}
					doLoginFrom(t, wrapped, "wrong", addr)
				}
			}()
		}
	}
	time.Sleep(300 * time.Millisecond) // let the flood fill what it can

	rec := doLoginFrom(t, wrapped, "secret", "203.0.113.50:1")
	close(stop)
	wg.Wait()
	if rec.Code != http.StatusOK {
		t.Fatalf("clean source's correct login = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
}

// A source holds at most loginThrottlePerSource queue slots, and a clean source falls back to the reserved
// lane when the shared queue is full while a source with failures does not.
func TestAcquireLoginThrottlePerSourceCapAndReservedLane(t *testing.T) {
	waitForSemLevel(t, 0)
	var rels []func()
	t.Cleanup(func() {
		for _, r := range rels {
			r()
		}
	})
	for i := range loginThrottlePerSource {
		rel, ok := acquireLoginThrottle("192.0.2.1", false)
		if !ok {
			t.Fatalf("slot %d refused", i)
		}
		rels = append(rels, rel)
	}
	if _, ok := acquireLoginThrottle("192.0.2.1", true); ok {
		t.Fatal("source got more than its per-source cap")
	}
	// Fill the shared queue with other sources.
	for i := 0; len(loginThrottleSem) < loginThrottleMaxInFlight; i++ {
		rel, ok := acquireLoginThrottle(fmt.Sprintf("192.0.2.%d", 10+i), false)
		if !ok {
			t.Fatalf("filler %d refused", i)
		}
		rels = append(rels, rel)
	}
	if _, ok := acquireLoginThrottle("192.0.2.100", false); ok {
		t.Fatal("source with failures got a slot in a full queue")
	}
	rel, ok := acquireLoginThrottle("192.0.2.100", true)
	if !ok {
		t.Fatal("clean source was refused despite the reserved lane")
	}
	rels = append(rels, rel)
}
