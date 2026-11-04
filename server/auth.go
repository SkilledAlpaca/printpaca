package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	mathrand "math/rand/v2"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Admin listener authentication and Host binding: session-cookie auth gated by ADMIN_PASSWORD, plus a
// Host allowlist against DNS rebinding that applies on every request regardless of auth. Secure by
// default: an unset ADMIN_PASSWORD generates a random one; only an explicit "" opts out. The public
// listener is never wrapped by any of this.

// authSessionTTL is the absolute session lifetime: a session expires this long after login, whatever the
// activity. resolveSession never extends it, and sweepSessions drops expired entries periodically.
const authSessionTTL = 30 * 24 * time.Hour

// authCookieName is the session cookie name.
const authCookieName = "printpaca_session"

// authLoginMaxBytes bounds the POST /api/login body.
const authLoginMaxBytes = 4 * 1024

// authLoginRateLimit/authLoginRateWindow bound login failures with a global (not per-IP) budget, since
// the admin listener serves a single owner. A correct password never counts against it, but once the
// budget is exhausted every attempt pays authLoginThrottleMin-authLoginThrottleMax before the compare
// (see handleLogin). Serialized by loginThrottleMu.
const (
	authLoginRateLimit  = 10
	authLoginRateWindow = time.Minute
)

// authIPLoginRateLimit is the per-source-IP failure budget, checked with the global one and tighter, so one
// source is throttled before it consumes the shared budget.
const authIPLoginRateLimit = 5

// authLoginThrottleMin/Max bound the randomized delay applied to every attempt once the failure budget is
// exhausted. Throttling instead of rejecting never blocks a legitimate login, and caps guessing at about
// one attempt per second globally.
const (
	authLoginThrottleMin = 1 * time.Second
	authLoginThrottleMax = 2 * time.Second
)

// loginThrottleMu guards loginThrottleNextAt across all handleLogin calls (one process-wide admin
// password, so one process-wide mutex) and brackets the final password compare. It is held only for
// constant-time bookkeeping, never across the throttle sleep. Holding it across the sleep serialized
// every attempt behind a queue of unbounded depth, so a flood could push a legitimate login past
// httpWriteTimeout with no response and no log. The wait is bounded instead by loginThrottleSem
// (loginThrottleMaxInFlight * authLoginThrottleMax); attempts beyond it get an immediate logged 429.
var loginThrottleMu sync.Mutex

// loginThrottleNextAt is the shared "next attempt permitted at" instant. Each throttled attempt advances
// it under loginThrottleMu by its own delay past the later of now and the previous value, then waits
// outside the lock. Concurrent throttled attempts therefore can't all finish within one delay window,
// keeping the compare rate at about one per delay without blocking other requests. A zero or past
// value means no queue.
var loginThrottleNextAt time.Time

// loginThrottleMaxInFlight bounds attempts holding a slot in loginThrottleSem. Without a bound the
// chained delays grow without limit. It times authLoginThrottleMax must stay well under
// httpWriteTimeout so every admitted attempt, a legitimate login included, gets a response (10 * 2s =
// 20s vs 60s). 10 far exceeds this single-admin app's real concurrency; more get an immediate logged 429.
const loginThrottleMaxInFlight = 10

// loginThrottleSem is a process-wide non-blocking semaphore of loginThrottleMaxInFlight slots for
// attempts waiting out the throttle (full means 429; release via defer). Unthrottled attempts never touch it.
var loginThrottleSem = make(chan struct{}, loginThrottleMaxInFlight)

// loginThrottlePerSource caps the queue slots one source (IP, or IPv6 /64 via clientIP) may hold, so
// filling the queue takes several sources instead of one. loginThrottleReservedSlots is an extra lane only
// for sources with no failures in the window: when the shared queue is full, a clean source's first attempt
// still queues (and still waits its turn on the shared timeline, so the global guess rate is unchanged).
const (
	loginThrottlePerSource     = 2
	loginThrottleReservedSlots = 2
)

var (
	loginThrottleReserved = make(chan struct{}, loginThrottleReservedSlots)
	loginThrottleSrcMu    sync.Mutex
	loginThrottleBySrc    = map[string]int{}
)

// acquireLoginThrottle takes a queue slot for source without blocking and returns its release. clean
// marks a source with no recent failures, which may fall back to the reserved lane.
func acquireLoginThrottle(source string, clean bool) (release func(), ok bool) {
	loginThrottleSrcMu.Lock()
	defer loginThrottleSrcMu.Unlock()
	if loginThrottleBySrc[source] >= loginThrottlePerSource {
		return nil, false
	}
	lane := loginThrottleSem
	select {
	case lane <- struct{}{}:
	default:
		if !clean {
			return nil, false
		}
		lane = loginThrottleReserved
		select {
		case lane <- struct{}{}:
		default:
			return nil, false
		}
	}
	loginThrottleBySrc[source]++
	return func() {
		loginThrottleSrcMu.Lock()
		if loginThrottleBySrc[source]--; loginThrottleBySrc[source] <= 0 {
			delete(loginThrottleBySrc, source)
		}
		loginThrottleSrcMu.Unlock()
		<-lane
	}, true
}

// loginThrottleBusyMessage is the 429 body when loginThrottleSem is full. It says nothing about the
// password; the server is just declining to queue another attempt.
const loginThrottleBusyMessage = "too many login attempts are already being throttled; try again shortly"

// authLoginThrottleDelay returns a random duration in [authLoginThrottleMin, authLoginThrottleMax]. The
// jitter is not a secret, so math/rand/v2 suffices.
func authLoginThrottleDelay() time.Duration {
	span := authLoginThrottleMax - authLoginThrottleMin
	return authLoginThrottleMin + mathrand.N(span+1)
}

// hostRejectLogAggregateKey is hostRejectLogLimiter's aggregate-budget key; a Host can't hold whitespace.
const hostRejectLogAggregateKey = "host reject aggregate"

// hostRejectLogAggregateLimit caps Host-rejection log lines per minute across all values: enough for
// several distinct misconfigurations, too few for an attacker rotating Host values to flood the log.
const hostRejectLogAggregateLimit = 20

// authState is the admin listener's auth configuration and live session table. Sessions are
// process-local, so a restart invalidates them; re-login is one password away.
type authState struct {
	enabled      bool
	password     string
	allowedHosts []string // lowercase, from ALLOWED_HOSTS; empty when unset

	// trustProxy mirrors PUBLIC_TRUST_PROXY: ADMIN_TRUST_PROXY=1 when a single TLS-terminating proxy sets
	// or appends the last X-Forwarded-Proto/For element. It decides the session cookie's Secure flag
	// (via requestIsTLS) and the per-IP login-limiter key (via clientIP). False on a directly-exposed
	// listener, where those headers are attacker-controlled.
	trustProxy bool

	// demoMode mirrors DEMO_MODE: demoWriteGuard rejects every unsafe /api/* request except login/logout with
	// a 403. Set post-construction by main(), so newAuthState callers are unaffected.
	demoMode bool

	// publicURL is set after construction from DEMO_PUBLIC_URL when demoMode is true; handleDemo surfaces it.
	publicURL string

	mu       sync.Mutex
	sessions map[string]authSession // token -> session record

	// db and configStore back handleLogin's employee path and handleDemo's multiUser flag. They live on the
	// receiver because bound-method handlers can't take extra arguments. Set by registerRoutes; nil in
	// tests that build an authState directly, where checkUserLogin and handleDemo guard for it.
	db          *sql.DB
	configStore *ConfigStore

	// loginLimiter is a rateLimiter with a single constant key: the budget is global (see authLoginRateLimit).
	loginLimiter *rateLimiter

	// ipLoginLimiter buckets the same failure budget per source address (see authIPLoginRateLimit), LRU-bounded.
	ipLoginLimiter *rateLimiter

	// hostRejectLogLimiter rate-limits the isAllowedHost rejection log with two bounds that must both allow:
	// one line per rejected Host value per minute, and hostRejectLogAggregateLimit in total. Without the
	// per-value line a missing ALLOWED_HOSTS is a silent wall of 403s. r.Host is attacker-controlled, so
	// the aggregate key stops rotating values from buying unbounded log volume.
	hostRejectLogLimiter *rateLimiter

	// tunnelGuardLogLimiter rate-limits the Cloudflare-tunnel-guard rejection log like hostRejectLogLimiter.
	tunnelGuardLogLimiter *rateLimiter

	// cfAccess is the Cloudflare Access verifier, set by main() when CF_ACCESS_TEAM/AUD are set; nil means off.
	cfAccess *cfAccess
}

// authSession is one live session: expires (absolute, fixed at login) plus its identity.
// username is "admin" for the env-admin login, else a DB user's lowercased name; isAdmin only for the former.
type authSession struct {
	expires  time.Time
	username string
	isAdmin  bool
}

// newAuthState builds the auth configuration from explicit values so tests don't touch the environment.
// password == "" means auth is disabled and every method is a no-op.
func newAuthState(password string, allowedHosts []string) *authState {
	lower := make([]string, len(allowedHosts))
	for i, h := range allowedHosts {
		lower[i] = strings.ToLower(strings.TrimSpace(h))
	}
	return &authState{
		enabled:               password != "",
		password:              password,
		allowedHosts:          lower,
		sessions:              map[string]authSession{},
		loginLimiter:          newRateLimiter(),
		ipLoginLimiter:        newRateLimiter(),
		hostRejectLogLimiter:  newRateLimiter(),
		tunnelGuardLogLimiter: newRateLimiter(),
	}
}

// parseAllowedHosts splits ALLOWED_HOSTS on commas, trims, drops empties, and strips a ":port" suffix,
// because comparisons use port-stripped Host headers and a kept port would never match. Case is left
// to newAuthState.
func parseAllowedHosts(v string) []string {
	var out []string
	for h := range strings.SplitSeq(v, ",") {
		h = strings.TrimSpace(h)
		if h != "" {
			out = append(out, stripPort(h))
		}
	}
	return out
}

// stripPort removes a ":port" suffix and IPv6 brackets ("[::1]:8080" -> "::1"); case is untouched.
func stripPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return strings.Trim(host, "[]")
}

// hostOnly is stripPort plus lowercasing: the exact value isAllowedHost compares and the rejection log
// prints, so the string an operator pastes into ALLOWED_HOSTS is the one that matches.
func hostOnly(hostHeader string) string {
	return strings.ToLower(stripPort(hostHeader))
}

// newSessionToken returns a 32-byte crypto/rand hex token; wider than newID to resist guessing.
func newSessionToken() string {
	b := make([]byte, 32)
	rand.Read(b) // never fails since Go 1.24: an entropy failure crashes the process instead
	return hex.EncodeToString(b)
}

// authErrorBody is the JSON shape for auth responses: a single "error" string, unlike apiErrorBody.
type authErrorBody struct {
	Error string `json:"error"`
}

func writeAuthError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(authErrorBody{Error: msg})
}

// requestIsTLS reports whether r arrived over TLS: directly, or, only with trustProxy (ADMIN_TRUST_PROXY=1,
// exactly one trusted proxy that sets or appends "https" as the last X-Forwarded-Proto element), via
// that header. The header is attacker-controlled on a directly-exposed listener and must not be trusted
// there. With two or more trusted appending hops this breaks down (as for clientIP), so set it only
// behind a single proxy.
// The header is parsed by lastForwardedValue: every header line is joined with commas and the last
// element wins, so a client-spoofed first line can't be trusted over an intermediary's appended one.
func requestIsTLS(r *http.Request, trustProxy bool) bool {
	if r.TLS != nil {
		return true
	}
	// An Access-verified request came through Cloudflare, which serves Access hostnames over HTTPS only,
	// so it counts as TLS. A token holder connecting directly just gets an unused Secure cookie.
	if cfAccessVerified(r) {
		return true
	}
	if !trustProxy {
		return false
	}
	last := lastForwardedValue(r.Header, "X-Forwarded-Proto")
	if last == "" {
		return false
	}
	return strings.EqualFold(last, "https")
}

// sessionCookiePath scopes the session cookie to the admin app, so the browser never sends it to the
// customer-facing routes that share the origin. Login, logout and every clear must use the same Path.
const sessionCookiePath = "/admin/"

// setSessionCookie writes the admin session cookie. Secure is set only when the request arrived over
// TLS (requestIsTLS), since the primary deployment is a LAN container on plain HTTP where an
// unconditional Secure would break the cookie. HttpOnly and SameSite=Strict always apply.
func setSessionCookie(w http.ResponseWriter, r *http.Request, trustProxy bool, token string, maxAge time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    token,
		Path:     sessionCookiePath,
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		Secure:   requestIsTLS(r, trustProxy),
		SameSite: http.SameSiteStrictMode,
	})
}

// clearSessionCookie mirrors setSessionCookie's Secure choice, since a Secure cookie needs a Secure clear.
func clearSessionCookie(w http.ResponseWriter, r *http.Request, trustProxy bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    "",
		Path:     sessionCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   requestIsTLS(r, trustProxy),
		SameSite: http.SameSiteStrictMode,
	})
}

// handleLogin implements POST /api/login. 404 when auth is disabled. Once the global failure budget is
// exhausted (see authLoginRateLimit) every further attempt, right or wrong, pays a randomized delay
// before the compare. It never rejects a legitimate login on the password itself, but can 429 when
// loginThrottleSem is full (bounded, logged, immediate). 401 on a wrong password; 200 plus cookie on success.
//
// An optional username in the body selects the DB employee path (checkUserLogin) instead of the
// env-admin subtle.ConstantTimeCompare path. Both share this throttle gate; checkUserLogin does not take
// loginThrottleMu (see its doc). A deployment without users.multiUser behaves as if the field didn't exist.
func (a *authState) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !a.enabled {
		writeAuthError(w, http.StatusNotFound, "authentication is not enabled")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, authLoginMaxBytes)
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	username := strings.TrimSpace(in.Username)

	// Reserve this attempt's failure slot atomically before comparing: allow() checks and records in one
	// step, so N parallel attempts can't all observe a not-yet-exhausted budget (a check-then-act race). A
	// successful login refunds the reservation below. When either the global or this source's tighter per-IP
	// budget refuses, the attempt is throttled (delayed) rather than rejected. Both calls always run so each
	// limiter records the attempt. rateLimiter is thread-safe, so neither needs loginThrottleMu.
	ip := clientIP(r, a.trustProxy)
	cleanSource := !a.ipLoginLimiter.exhausted(ip, 1, authLoginRateWindow)
	globalOK := a.loginLimiter.allow("login", authLoginRateLimit, authLoginRateWindow)
	ipOK := a.ipLoginLimiter.allow(ip, authIPLoginRateLimit, authLoginRateWindow)
	reserved := globalOK && ipOK
	if globalOK && !ipOK {
		a.loginLimiter.refund("login")
	} else if ipOK && !globalOK {
		a.ipLoginLimiter.refund(ip)
	}
	exhausted := !reserved

	if exhausted {
		// Acquire a slot before touching loginThrottleNextAt, so a rejected attempt never advances the shared
		// timeline. Full means an immediate logged 429.
		releaseThrottle, got := acquireLoginThrottle(ip, cleanSource)
		if !got {
			log.Printf("admin listener: rejected a login attempt from %s -- the throttle queue is full or this source already holds its %d slots", ip, loginThrottlePerSource)
			writeAuthError(w, http.StatusTooManyRequests, loginThrottleBusyMessage)
			return
		}
		defer releaseThrottle()

		// Chain this wait onto the later of the queue tail and now, advance the tail (see loginThrottleNextAt).
		loginThrottleMu.Lock()
		now := time.Now()
		if loginThrottleNextAt.Before(now) {
			loginThrottleNextAt = now
		}
		loginThrottleNextAt = loginThrottleNextAt.Add(authLoginThrottleDelay())
		waitUntil := loginThrottleNextAt
		loginThrottleMu.Unlock()

		timer := time.NewTimer(time.Until(waitUntil))
		select {
		case <-timer.C:
		case <-r.Context().Done():
			// Client gave up before its turn: nothing to write and no failed attempt to record.
			timer.Stop()
			return
		}
	}

	var correct bool
	var sessUsername string
	var sessIsAdmin bool
	if username == "" {
		if loginCompareHook != nil {
			loginCompareHook()
		}
		loginThrottleMu.Lock()
		correct = subtle.ConstantTimeCompare([]byte(in.Password), []byte(a.password)) == 1
		loginThrottleMu.Unlock()
		sessUsername = "admin"
		sessIsAdmin = true
	} else {
		var busy bool
		correct, sessUsername, busy = a.checkUserLogin(username, in.Password)
		sessIsAdmin = false
		if busy {
			if reserved {
				a.loginLimiter.refund("login")
				a.ipLoginLimiter.refund(ip)
			}
			log.Printf("admin listener: rejected a username login from %s -- all %d password-hash slots are busy", ip, bcryptMaxInFlight)
			writeAuthError(w, http.StatusTooManyRequests, loginBusyMessage)
			return
		}
	}

	if correct {
		// A correct password never counts against the budget: give back the slot reserved above.
		if reserved {
			a.loginLimiter.refund("login")
			a.ipLoginLimiter.refund(ip)
		}
		token := newSessionToken()
		a.mu.Lock()
		a.sessions[token] = authSession{expires: time.Now().Add(authSessionTTL), username: sessUsername, isAdmin: sessIsAdmin}
		a.mu.Unlock()
		setSessionCookie(w, r, a.trustProxy, token, authSessionTTL)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		json.NewEncoder(w).Encode(struct {
			Ok bool `json:"ok"`
		}{Ok: true})
		return
	}

	// A wrong password keeps the reservation made above as its recorded failure.
	writeAuthError(w, http.StatusUnauthorized, "incorrect password")
}

// bcryptMaxInFlight bounds concurrent employee-path bcrypt compares (cost 12 is ~100-250ms of CPU each).
// An unauthenticated flood otherwise burns unbounded CPU; excess attempts get an immediate 429 instead of
// queueing. Small on purpose: a handful of employees never logs in simultaneously.
const bcryptMaxInFlight = 4

// bcryptSem is the non-blocking semaphore for bcryptMaxInFlight.
var bcryptSem = make(chan struct{}, bcryptMaxInFlight)

// loginBusyMessage is the 429 body when bcryptSem is full.
const loginBusyMessage = "the server is busy verifying other logins; try again shortly"

// loginCompareHook, when non-nil, is called once per started password comparison (admin or employee).
// Tests use it to count compares; production leaves it nil.
var loginCompareHook func()

// boundedBcryptCompare runs a bcrypt compare under bcryptSem. busy is true, with no compare done, when the
// semaphore is full.
func boundedBcryptCompare(hash []byte, password string) (match, busy bool) {
	select {
	case bcryptSem <- struct{}{}:
	default:
		return false, true
	}
	defer func() { <-bcryptSem }()
	if loginCompareHook != nil {
		loginCompareHook()
	}
	return bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil, false
}

// checkUserLogin is the employee half of handleLogin (username already trimmed, non-empty). When
// users.multiUser is off it fails immediately with no bcrypt, so an unauthenticated caller can't burn CPU
// on a path that can never succeed (the admin password path is a microsecond compare and is unaffected).
// With the mode on, every failing path (unknown or disabled user, lookup error) burns one bcrypt compare
// against dummyBcryptHash, so timing can't enumerate usernames or tell "disabled" from "unknown". All
// compares run under bcryptSem; busy reports a full semaphore (the caller answers 429).
//
// It deliberately does NOT take loginThrottleMu: bcrypt at bcryptCost is ~100ms+, and holding a
// process-wide mutex across it would serialize every login behind one compare.
//
// a.db and a.configStore are nil in tests that skip registerRoutes; that is treated as multi-user mode off.
func (a *authState) checkUserLogin(username, password string) (correct bool, sessUsername string, busy bool) {
	multiUser := a.configStore != nil && a.db != nil && a.configStore.Get().Users.MultiUser
	if !multiUser {
		return false, "", false
	}
	hash := dummyBcryptHash
	var user userRow
	u, ok, err := getUserByUsername(a.db, username)
	if err != nil {
		log.Printf("login: looking up user %q: %v", username, err)
	} else if ok && !u.Disabled {
		user = u
		hash = []byte(u.PasswordHash)
	} else {
		ok = false
	}
	match, busy := boundedBcryptCompare(hash, password)
	if busy {
		return false, "", true
	}
	if err != nil || !ok || !match {
		return false, "", false
	}
	return true, strings.ToLower(user.Username), false
}

// killSessionsForUsername deletes every live session for username (case-insensitive, matching the users
// table's NOCASE uniqueness) except exceptToken when non-empty. Called when a user is disabled, deleted
// or has a password reset (self-service change passes its own token to keep that session; others pass "").
func (a *authState) killSessionsForUsername(username, exceptToken string) {
	lower := strings.ToLower(username)
	a.mu.Lock()
	defer a.mu.Unlock()
	for token, sess := range a.sessions {
		if token == exceptToken {
			continue
		}
		if strings.ToLower(sess.username) == lower {
			delete(a.sessions, token)
		}
	}
}

// killAllEmployeeSessions deletes every non-admin session when users.multiUser is toggled off. Employee
// rows survive, but their access must stop immediately.
func (a *authState) killAllEmployeeSessions() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for token, sess := range a.sessions {
		if !sess.isAdmin {
			delete(a.sessions, token)
		}
	}
}

// handleLogout implements POST /api/logout: clears the session and cookie; always 200.
func (a *authState) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(authCookieName); err == nil {
		a.mu.Lock()
		delete(a.sessions, c.Value)
		a.mu.Unlock()
	}
	clearSessionCookie(w, r, a.trustProxy)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(struct {
		Ok bool `json:"ok"`
	}{Ok: true})
}

// handleDemo implements GET /api/demo: a tiny unauthenticated endpoint (see authExemptPaths) returning
// {"demoMode": bool}. LoginScreen.svelte needs it before a session exists, and GET /api/config 401s
// pre-login. A safe GET, so demoWriteGuard never touches it.
//
// publicUrl rides along, omitempty: a.publicURL is "" unless demoMode is on, so non-demo deployments
// see an unchanged response whatever DEMO_PUBLIC_URL holds.
//
// multiUser is the login screen's pre-auth mode signal, carried here instead of a new endpoint. It is
// false when a.configStore is nil (tests that skip registerRoutes).
//
// shopUrl is "/" while site.shopOnMainPort is on, so the sign-in page can offer a Back to shop link, and is
// omitted otherwise (the shop isn't on this port).
func (a *authState) handleDemo(w http.ResponseWriter, r *http.Request) {
	var multiUser bool
	var shopURL string
	if a.configStore != nil {
		cfg := a.configStore.Get()
		multiUser = cfg.Users.MultiUser
		if cfg.Site.ShopOnMainPort {
			shopURL = "/"
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(struct {
		DemoMode  bool   `json:"demoMode"`
		PublicURL string `json:"publicUrl,omitempty"`
		MultiUser bool   `json:"multiUser"`
		ShopURL   string `json:"shopUrl,omitempty"`
	}{DemoMode: a.demoMode, PublicURL: a.publicURL, MultiUser: multiUser, ShopURL: shopURL})
}

// resolveSession reports whether token names a live session and returns the identity. Sessions have a
// fixed expiry (authSessionTTL after login) that is never extended. auth.middleware attaches it to the context for identityFor.
func (a *authState) resolveSession(token string) (identity, bool) {
	if token == "" {
		return identity{}, false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	sess, ok := a.sessions[token]
	if !ok || time.Now().After(sess.expires) {
		if ok {
			delete(a.sessions, token) // expired: prune rather than leave it for the next lookup
		}
		return identity{}, false
	}
	return identity{username: sess.username, isAdmin: sess.isAdmin}, true
}

// authSessionSweepInterval is how often sweepSessionsLoop drops expired sessions that nobody looked up again.
const authSessionSweepInterval = time.Hour

// sweepSessions deletes every expired session and returns how many it removed.
func (a *authState) sweepSessions() int {
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for token, sess := range a.sessions {
		if now.After(sess.expires) {
			delete(a.sessions, token)
			n++
		}
	}
	return n
}

// sweepSessionsLoop calls sweepSessions every authSessionSweepInterval until ctx is done.
func (a *authState) sweepSessionsLoop(ctx context.Context) {
	t := time.NewTicker(authSessionSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.sweepSessions()
		}
	}
}

// multiUserEnabled reports whether users.multiUser is on; false when db or configStore are unset (tests).
func (a *authState) multiUserEnabled() bool {
	return a.configStore != nil && a.db != nil && a.configStore.Get().Users.MultiUser
}

// accessIdentity maps a verified Access token's email to an identity, or found=false so the caller
// falls back to the normal login flow.
//   - Single-user mode (users.multiUser off): every verified request is the admin, because the Access
//     policy is the list of people. Exception: an email matching ANY users row (enabled or disabled) is
//     found=false, so a current or former employee never becomes admin because multi-user was switched
//     off. A lookup error fails closed.
//   - Multi-user mode: users.accessAdminEmail is the admin; an enabled user whose email matches gets
//     that user's identity and permissions. No email, no match, a disabled match, or a lookup error is
//     found=false (lookup errors are logged and fail closed).
func (a *authState) accessIdentity(email string) (identity, bool) {
	if email != "" && !plainEmailBytes(email) {
		// Stored emails are plain ASCII, so this cannot name anyone.
		return identity{}, false
	}
	multiUser := a.multiUserEnabled()
	if !multiUser {
		if email == "" {
			return identity{}, false
		}
		if a.db != nil {
			_, isEmployee, err := getUserByEmail(a.db, email)
			if err != nil {
				log.Printf("cloudflare access: looking up user by email: %v", err)
				return identity{}, false
			}
			if isEmployee {
				return identity{}, false
			}
		}
		return adminIdentity(), true
	}
	if email == "" {
		return identity{}, false
	}
	if admin := a.configStore.Get().Users.AccessAdminEmail; admin != "" && email == admin {
		return adminIdentity(), true
	}
	user, ok, err := getUserByEmail(a.db, email)
	if err != nil {
		log.Printf("cloudflare access: looking up user by email: %v", err)
		return identity{}, false
	}
	if !ok || user.Disabled {
		return identity{}, false
	}
	return identity{username: strings.ToLower(user.Username)}, true
}

// authExemptPaths are the only /api/* routes reachable without a session: login, plus GET /api/demo
// (the login screen's pre-login signal; GET /api/config 401s). /healthz does NOT belong here:
// middleware skips every non-/api/* path before consulting this map, so an entry would be dead.
var authExemptPaths = map[string]bool{
	"/api/login": true,
	"/api/demo":  true,
}

// allowedHostsErrorBody is isAllowedHost's 403 body: one line naming the fix.
const allowedHostsErrorBody = "Host not allowed. Set ALLOWED_HOSTS to a comma-separated list including this host (see README's Authentication section).\n"

// cloudflareTunnelGuardErrorBody is isCloudflareTunneled's 403 body, shaped like allowedHostsErrorBody.
const cloudflareTunnelGuardErrorBody = "This request arrived through Cloudflare, but the admin app can't be reached through Cloudflare while authentication is off. Set ADMIN_PASSWORD, or configure Cloudflare Access (CF_ACCESS_TEAM and CF_ACCESS_AUD); see docs/cloudflare-tunnel.md.\n"

// cloudflarePasswordOnlyLogMessage is logged (at most once a minute) when /admin/ is reached through Cloudflare
// without Cloudflare Access: allowed, because the printpaca password is then the only lock.
const cloudflarePasswordOnlyLogMessage = "admin reached through Cloudflare without Cloudflare Access; the printpaca password is the only lock (see docs/cloudflare-tunnel.md)"

// cloudflareTrustProxyUnsetLogSuffix is appended to that line while ADMIN_TRUST_PROXY is unset: every
// tunneled request then shares cloudflared's address, so the login rate limiter has one bucket for all.
const cloudflareTrustProxyUnsetLogSuffix = "; ADMIN_TRUST_PROXY is unset, so all tunneled clients share one rate-limit bucket"

// isCloudflareTunneled reports whether r carries a header only Cloudflare's edge adds: CF-Connecting-IP
// or Cf-Ray. See the tunnel guard in middleware.
func isCloudflareTunneled(r *http.Request) bool {
	return r.Header.Get("CF-Connecting-IP") != "" || r.Header.Get("Cf-Ray") != ""
}

// isAllowedHost reports whether hostHeader is safe to serve the admin listener to: an IP literal,
// "localhost", or a name in allowedHosts (lowercased by newAuthState). This is the DNS rebinding defense:
// otherwise a page could point a DNS name it controls at the visitor's LAN address and send same-origin
// requests past SameSite=Strict to the admin API.
func isAllowedHost(hostHeader string, allowedHosts []string) bool {
	host := hostOnly(hostHeader)

	if net.ParseIP(host) != nil {
		return true
	}
	if host == "localhost" {
		return true
	}
	return slices.Contains(allowedHosts, host)
}

// isUnsafeAPIMethod: method changes state (not GET/HEAD/OPTIONS), so csrfGuard's Origin check covers it.
func isUnsafeAPIMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// requiresJSONContentType is isUnsafeAPIMethod minus DELETE: POST/PUT/PATCH bodies are always JSON, so
// csrfGuard's Content-Type check applies there, only when a body is present (POST /api/logout is bodyless).
// DELETE is excluded because every DELETE endpoint takes no body and the SPA sends no Content-Type
// (requiring one would 415 the real app). That isn't a gap: DELETE isn't a CORS simple method, and
// this server never grants the preflight (no Access-Control-Allow-Origin).
func requiresJSONContentType(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch
}

// hasJSONContentType reports whether ct is application/json, ignoring parameters.
func hasJSONContentType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	return mt == "application/json"
}

// originAllowed reports whether an Origin names this request's own Host or an ALLOWED_HOSTS entry. It is
// deliberately NOT isAllowedHost, which trusts every IP literal Host: right for DNS rebinding, wrong here,
// since an attacker page served from its own IP would pass an Origin check that trusted any IP literal.
func originAllowed(origin string, r *http.Request, allowedHosts []string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	originHost := strings.ToLower(u.Hostname())

	reqHost := r.Host
	if h, _, err := net.SplitHostPort(reqHost); err == nil {
		reqHost = h
	}
	reqHost = strings.ToLower(strings.Trim(reqHost, "[]"))

	if originHost == reqHost {
		return true
	}
	return slices.Contains(allowedHosts, originHost)
}

// csrfGuard closes a CSRF gap: with ADMIN_PASSWORD set to "", a POST with Content-Type: text/plain is a
// CORS simple request sent without a preflight, so any website the owner visits could blind-write to
// every /api/* JSON endpoint (create clients, send email through the owner's SMTP relay, flip invoice
// status). Two independent checks, both applied regardless of a.enabled: a JSON-only Content-Type on
// methods that carry a JSON body (requiresJSONContentType), and an Origin match when the header is
// present, for every unsafe method including DELETE.
// POST /api/items/{id}/image (raw image body) is the one route exempt from the JSON rule;
// isItemImageUploadRoute narrows the exemption to it, with its own Content-Type allowlist.
func (a *authState) csrfGuard(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/") || !isUnsafeAPIMethod(r.Method) {
		return true
	}
	// Skip the Content-Type check only for an explicitly empty body (ContentLength == 0, e.g. POST
	// /api/logout). A chunked body reports -1, so anything not explicitly empty must be JSON.
	if isItemImageUploadRoute(r.Method, r.URL.Path) {
		// image/jpeg, png and webp aren't CORS simple-request types, so a cross-origin POST claiming one is
		// always preflighted, and this server grants no Access-Control-Allow-Origin. The no-preflight
		// blind-write gap therefore stays closed: every type a browser sends without a preflight is rejected too.
		if r.ContentLength != 0 && !hasImageContentType(r.Header.Get("Content-Type")) {
			writeAuthError(w, http.StatusUnsupportedMediaType, "Content-Type must be image/jpeg, image/png, or image/webp")
			return false
		}
	} else if requiresJSONContentType(r.Method) && r.ContentLength != 0 && !hasJSONContentType(r.Header.Get("Content-Type")) {
		writeAuthError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" && !originAllowed(origin, r, a.allowedHosts) {
		writeAuthError(w, http.StatusForbidden, "cross-origin request not allowed")
		return false
	}
	return true
}

// tokenWriteSameOrigin is the extra CSRF check for an unsafe /api/* request authenticated only by the
// Access token. That cookie is attached to any request and lacks printpaca's SameSite=Strict, so the
// request must carry an Origin passing originAllowed or Sec-Fetch-Site: same-origin. Safe methods and
// non-/api/ paths pass.
func tokenWriteSameOrigin(r *http.Request, allowedHosts []string) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/") || !isUnsafeAPIMethod(r.Method) {
		return true
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		return originAllowed(origin, r, allowedHosts)
	}
	return r.Header.Get("Sec-Fetch-Site") == "same-origin"
}

// isItemImageUploadRoute matches "POST /api/items/{id}/image", "POST /api/items/{id}/options/{optionId}/image", and
// "POST /api/shop/hero-image".
// The ids are the only variable parts, so a prefix/suffix match is unambiguous.
func isItemImageUploadRoute(method, path string) bool {
	if method == http.MethodPost && path == "/api/shop/hero-image" {
		return true
	}
	return method == http.MethodPost && strings.HasPrefix(path, "/api/items/") && strings.HasSuffix(path, "/image")
}

// hasImageContentType is a cheap first rejection for the upload route, not the source of truth:
// handleImageUpload sniffs the body with http.DetectContentType regardless of this header.
func hasImageContentType(ct string) bool {
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	switch mt {
	case "image/jpeg", "image/png", "image/webp":
		return true
	}
	return false
}

// demoReadOnlyMessage is the plain-English 403 error for writes rejected in demo mode.
const demoReadOnlyMessage = "This is a read-only demo. Changes are disabled."

// demoWriteGuardExemptPaths are the /api/* routes demoWriteGuard still allows. It is distinct from
// authExemptPaths, which must stay unaffected by demo mode.
//   - /api/login, /api/logout: a demo visitor still needs to get in and out.
//   - /api/quote/compute: a POST only because it carries a body. handleQuoteCompute is a pure function
//     of that body and the config, and the admin quote editor's only pricing path; blocking it left
//     the New quote page unusable in the demo. It is read-only, which is what the guard protects.
var demoWriteGuardExemptPaths = map[string]bool{
	"/api/login":         true,
	"/api/logout":        true,
	"/api/quote/compute": true,
}

// demoWriteGuard implements the read-only demo: with a.demoMode on, every unsafe /api/* request except
// demoWriteGuardExemptPaths gets a friendly 403. GET/HEAD and non-/api/ paths pass; demoMode off is a no-op.
// It runs first in middleware so a logged-out visitor sees this message, not a CSRF 415 or session 401.
func (a *authState) demoWriteGuard(w http.ResponseWriter, r *http.Request) bool {
	if !a.demoMode || !strings.HasPrefix(r.URL.Path, "/api/") || !isUnsafeAPIMethod(r.Method) || demoWriteGuardExemptPaths[r.URL.Path] {
		return true
	}
	writeAuthError(w, http.StatusForbidden, demoReadOnlyMessage)
	return false
}

// allowHost enforces the Host allowlist (isAllowedHost): it writes the 403 and reports false when r.Host
// is not allowed. Shared by middleware (admin) and the main port's public branch, so both reject with
// the same body and the same rate-limited log naming the Host.
func (a *authState) allowHost(w http.ResponseWriter, r *http.Request, branch string) bool {
	if isAllowedHost(r.Host, a.allowedHosts) {
		return true
	}
	// Rate-limited log naming the rejected Host (see hostRejectLogLimiter), so missing ALLOWED_HOSTS isn't an
	// unexplained wall of 403s. Both the per-value and aggregate budgets must allow. The logged value is
	// hostOnly(r.Host), the port-stripped lowercase string isAllowedHost compared, so pasting it into
	// ALLOWED_HOSTS fixes the rejection.
	if a.hostRejectLogLimiter.allow(r.Host, 1, time.Minute) &&
		a.hostRejectLogLimiter.allow(hostRejectLogAggregateKey, hostRejectLogAggregateLimit, time.Minute) {
		log.Printf("%s: rejected request for disallowed Host %q; set ALLOWED_HOSTS to include it if this host is legitimate (see README's Authentication section)", branch, hostOnly(r.Host))
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	w.Write([]byte(allowedHostsErrorBody))
	return false
}

// hostGuard applies allowHost to every request before next. The main port's public branch uses it: the
// allowlist covers public paths too, because the DNS rebinding defense is a property of the port.
func (a *authState) hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.allowHost(w, r, "main port") {
			return
		}
		next.ServeHTTP(w, r)
	})
}

// middleware wraps the admin mux (the /admin/* branch of the main port; never PUBLIC_PORT) with, in order:
// Host allowlist, Cloudflare tunnel guard, demo write guard, CSRF guard, session auth. The allowlist and
// tunnel guard run on every request regardless of a.enabled, because they defend the listener itself: with
// auth opted out a rebound request must still not reach the admin API. The tunnel guard has three outcomes
// for a request carrying a Cloudflare-only header (outside demo mode): with Cloudflare Access configured
// it needs a valid token (handled first, reason-specific 403); without Access and with authentication on
// it is allowed and logged once a minute, the password being the only lock; without Access and with
// authentication off it is a 403. The allowlist alone doesn't stop a cross-origin write to an IP
// literal or allowlisted name (isAllowedHost trusts IP literals), so csrfGuard closes that gap, also
// regardless of a.enabled. demoWriteGuard precedes csrfGuard and the session check for the same reason.
// Only the session check is skipped when auth is disabled.
func (a *authState) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Cloudflare Access (cfaccess.go), opt-in: a request with a Cloudflare header is verified first. A
		// valid token marks it verified, skipping the Host allowlist and tunnel guard (rebinding reaches the
		// listener directly and can't produce a token signed for this app) and continuing to CSRF and
		// session checks. An invalid or missing token is a reason-specific 403 and never falls through.
		// Requests without a Cloudflare header (LAN, VPN) take the path below unchanged.
		verified := false
		var accessEmail string
		if a.cfAccess != nil && isCloudflareTunneled(r) {
			email, reason := a.cfAccess.verifyClaims(r.Header.Get(cfAccessJWTHeader))
			if reason != "" {
				if a.tunnelGuardLogLimiter.allow("cfaccess:"+reason, 1, time.Minute) {
					log.Printf("admin listener: rejected request through Cloudflare Access: %s (see docs/cloudflare-tunnel.md's troubleshooting table)", reason)
				}
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(cfAccessErrorBodies[reason]))
				return
			}
			r = markCFVerified(r, email)
			accessEmail = email
			verified = true
		}

		// The Host allowlist applies to every request, static assets included.
		if !verified && !a.allowHost(w, r, "admin listener") {
			return
		}

		// Cloudflare tunnel guard. Without Access (verified is false for every tunneled request here, since an
		// Access-configured tunneled request was verified or rejected above), a tunneled request means /admin/ is
		// published through Cloudflare with no second lock. With authentication on that is allowed (the printpaca
		// password and login rate limits are the lock; docs/cloudflare-tunnel.md recommends Access) and noted in
		// the log. With authentication off nothing would guard the admin API at all, so it stays a 403.
		//
		// a.demoMode is the exception: a demo is read-only, seeded with fake data, and has a deliberately
		// published password, so there is no client data to protect and the demo is intentionally tunneled.
		if !verified && !a.demoMode && isCloudflareTunneled(r) {
			if a.enabled {
				if a.tunnelGuardLogLimiter.allow("cloudflare", 1, time.Minute) {
					msg := cloudflarePasswordOnlyLogMessage
					if !a.trustProxy {
						msg += cloudflareTrustProxyUnsetLogSuffix
					}
					log.Print(msg)
				}
			} else {
				if a.tunnelGuardLogLimiter.allow("cloudflare", 1, time.Minute) {
					log.Print("admin listener: rejected request carrying a Cloudflare proxy header (CF-Connecting-IP/Cf-Ray) while authentication is off; set ADMIN_PASSWORD or configure Cloudflare Access (see docs/cloudflare-tunnel.md)")
				}
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				w.Write([]byte(cloudflareTunnelGuardErrorBody))
				return
			}
		}

		if !a.demoWriteGuard(w, r) {
			return
		}

		if !a.csrfGuard(w, r) {
			return
		}

		// With the password off there is no session to check. A verified request is still marked as signed in
		// through Access so the SPA offers the Cloudflare sign-out; the identity is admin either way.
		if !a.enabled {
			if verified && !a.cfAccess.requirePassword {
				// Same token-write rule as with auth on: the Access cookie carries this request, so writes need proof.
				if !tokenWriteSameOrigin(r, a.allowedHosts) {
					writeAuthError(w, http.StatusForbidden, "cross-origin request not allowed")
					return
				}
				r = r.WithContext(withAccessSignIn(r.Context(), accessEmail))
			}
			next.ServeHTTP(w, r)
			return
		}

		// Static assets and the SPA shell (non-/api/*) stay open so login can load; /api/login is the exception.
		if !strings.HasPrefix(r.URL.Path, "/api/") || authExemptPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		var token string
		if c, err := r.Cookie(authCookieName); err == nil {
			token = c.Value
		}
		id, ok := a.resolveSession(token)
		// No session: an Access-verified request is authenticated by its token unless CF_ACCESS_REQUIRE_PASSWORD=1.
		// A valid session cookie always wins. The token identity is resolved per request, never stored.
		if !ok && verified && !a.cfAccess.requirePassword {
			// Single-user mode trusts the Access policy as the list of people, but a token with no email
			// claim (a service token, or an IdP that omits it) names nobody, so it is never the admin.
			if accessEmail == "" && !a.multiUserEnabled() {
				if a.tunnelGuardLogLimiter.allow("cfaccess:noemail", 1, time.Minute) {
					log.Print("admin listener: rejected a Cloudflare Access token with no email claim in single-user mode (see docs/cloudflare-tunnel.md)")
				}
				// 401, not 403: the web app shows its password sign-in only on 401, and /api/login still works.
				writeAuthError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
			if accessID, found := a.accessIdentity(accessEmail); found {
				if !tokenWriteSameOrigin(r, a.allowedHosts) {
					writeAuthError(w, http.StatusForbidden, "cross-origin request not allowed")
					return
				}
				id, ok = accessID, true
				r = r.WithContext(withAccessSignIn(r.Context(), accessEmail))
			}
		}
		if !ok {
			writeAuthError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		// Attach the identity so adminOnly, requirePermission and identityFor skip a second session lookup.
		next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), id)))
	})
}

// adminPasswordFileName holds a generated admin password in CONFIG_DIR, so a restart reuses it.
const adminPasswordFileName = "admin-password"

// adminPasswordAlphabet excludes look-alike characters (0/O, 1/I/l) so a logged password is easy to retype.
const adminPasswordAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// adminPasswordLength: 20 of 58 symbols is ~117 bits, ample against a login throttled to ~1 attempt/second.
const adminPasswordLength = 20

// minAdminPasswordLength is the floor for an explicit ADMIN_PASSWORD: low enough for any reasonable
// passphrase, high enough to rule out "1234" or "admin", which the throttle would fall to in minutes.
const minAdminPasswordLength = 8

// generateAdminPassword draws uniformly from adminPasswordAlphabet using rejection sampling, not modulo.
func generateAdminPassword() string {
	n := len(adminPasswordAlphabet)
	limit := 256 - (256 % n) // bytes >= limit are rejected to avoid modulo bias
	out := make([]byte, adminPasswordLength)
	buf := make([]byte, 1)
	for i := 0; i < adminPasswordLength; {
		rand.Read(buf) // never fails since Go 1.24
		if int(buf[0]) >= limit {
			continue
		}
		out[i] = adminPasswordAlphabet[int(buf[0])%n]
		i++
	}
	return string(out)
}

// adminAuthDisabledWarning is logged whenever ADMIN_PASSWORD is "": loud on purpose, since this opts an
// app holding client PII, invoices and payments out of the secure default.
const adminAuthDisabledWarning = "" +
	"printpaca: ================================================================\n" +
	"printpaca: WARNING: ADMIN_PASSWORD is set to an empty string.\n" +
	"printpaca: Authentication is DISABLED on the admin listener. Anyone who can\n" +
	"printpaca: reach this port has full, unauthenticated access to client PII,\n" +
	"printpaca: invoices, and payment records.\n" +
	"printpaca: This is the only supported opt-out. Unset ADMIN_PASSWORD entirely\n" +
	"printpaca: to have a random password generated and enforced automatically.\n" +
	"printpaca: ================================================================"

// resolveAdminPassword implements secure-by-default admin auth:
//   - ADMIN_PASSWORD set non-empty: used verbatim; env wins.
//   - Unset: reuse CONFIG_DIR/admin-password if present, else generate, persist (0600) and log it once.
//   - Present but "": the only opt-out; auth is disabled and adminAuthDisabledWarning is logged.
//
// Errors are startup-fatal (main wraps in log.Fatalf) and name the offending path.
func resolveAdminPassword(configDir string) (string, error) {
	envPassword, set := os.LookupEnv("ADMIN_PASSWORD")
	if set && envPassword != "" {
		// DEMO_MODE's password is intentionally the short, published "demo", so the length floor is waived here
		// only. Read from the env directly to avoid threading demoMode through this function's test call sites.
		demoMode := os.Getenv("DEMO_MODE") == "1"
		if !demoMode && len(envPassword) < minAdminPasswordLength {
			return "", fmt.Errorf(
				"ADMIN_PASSWORD is %d characters, must be at least %d; choose a longer password, or unset ADMIN_PASSWORD entirely to have a strong one generated and stored automatically",
				len(envPassword), minAdminPasswordLength)
		}
		log.Print("printpaca: ADMIN_PASSWORD is set; the admin listener requires login")
		return envPassword, nil
	}
	if set { // set && envPassword == "": explicit opt-out
		log.Print(adminAuthDisabledWarning)
		return "", nil
	}

	path := filepath.Join(configDir, adminPasswordFileName)
	existing, readErr := os.ReadFile(path)
	if readErr == nil {
		pw := strings.TrimSpace(string(existing))
		if pw == "" {
			return "", fmt.Errorf("admin password: %s exists but is empty; delete it to generate a new password, or set ADMIN_PASSWORD explicitly", path)
		}
		log.Printf("printpaca: ADMIN_PASSWORD is not set; reusing the generated password stored at %s. The admin listener requires login.", path)
		return pw, nil
	}
	if !errors.Is(readErr, os.ErrNotExist) {
		return "", fmt.Errorf("admin password: reading %s: %w (check CONFIG_DIR ownership/permissions)", path, readErr)
	}

	pw := generateAdminPassword()
	if writeErr := os.WriteFile(path, []byte(pw+"\n"), 0o600); writeErr != nil {
		return "", fmt.Errorf("admin password: writing %s: %w (check CONFIG_DIR ownership/permissions)", path, writeErr)
	}
	log.Printf("printpaca: ADMIN_PASSWORD is not set; generated a new admin password and stored it at %s.", path)
	log.Printf("printpaca: Use this password to sign in: %s", pw)
	return pw, nil
}
