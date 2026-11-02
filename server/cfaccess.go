package main

// Cloudflare Access verification for the admin listener (opt-in).
//
// The admin listener rejects requests carrying Cloudflare proxy headers (isCloudflareTunneled) unless
// CF_ACCESS_TEAM and CF_ACCESS_AUD are set and the request carries a valid Access token
// (Cf-Access-Jwt-Assertion). That token is also the login (email claim, see accessIdentity) unless
// CF_ACCESS_REQUIRE_PASSWORD=1 additionally requires a printpaca session.
//
// The token is an RS256 JWT verified with the standard library only. The issuer and key-set URL come
// from the configured team, never the token, and the algorithm is pinned, so a forged header can't
// steer the key or algorithm. Failures fail closed with a reason code selecting the 403 body and log line.

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"math/big"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	// cfAccessJWTHeader is the header Access adds to requests it lets through.
	cfAccessJWTHeader = "Cf-Access-Jwt-Assertion"

	// cfAccessMaxTokenBytes caps the header value before any parsing.
	cfAccessMaxTokenBytes = 8 << 10

	// cfAccessMaxJWKSBytes caps the key-set response body.
	cfAccessMaxJWKSBytes = 64 << 10

	// cfAccessFetchTimeout bounds one key-set fetch.
	cfAccessFetchTimeout = 10 * time.Second

	// cfAccessRefreshInterval is how often the key set is refreshed in the
	// background.
	cfAccessRefreshInterval = time.Hour

	// cfAccessUnknownKidRefetchGap spaces unknown-kid refetches so random kids can't hammer Cloudflare.
	cfAccessUnknownKidRefetchGap = 30 * time.Second

	// cfAccessLeeway is the clock-skew tolerance for exp, nbf, and iat.
	cfAccessLeeway = 60 * time.Second

	// cfAccessMinRSABits rejects weak keys from the key set.
	cfAccessMinRSABits = 2048
)

var (
	cfAccessTeamRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	cfAccessAudRe  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Reason codes for a rejected token. Each selects a 403 body
// (cfAccessErrorBodies) and appears in the rate-limited log line.
const (
	cfReasonMissing         = "missing"
	cfReasonMalformed       = "malformed"
	cfReasonAlgorithm       = "algorithm"
	cfReasonUnknownKey      = "unknown_key"
	cfReasonSignature       = "signature"
	cfReasonIssuer          = "issuer"
	cfReasonAudience        = "audience"
	cfReasonExpired         = "expired"
	cfReasonNotYetValid     = "not_yet_valid"
	cfReasonKeysUnavailable = "keys_unavailable"
)

// cfAccessErrorBodies maps each reason code to a one-line 403 body naming the likely fix.
var cfAccessErrorBodies = map[string]string{
	cfReasonMissing:         "Cloudflare Access: this request has no Access token. No Access application protects this hostname; create one in Cloudflare Zero Trust before routing the hostname here (see docs/cloudflare-tunnel.md).\n",
	cfReasonMalformed:       "Cloudflare Access: the Access token is malformed. Sign in again through the Access login page.\n",
	cfReasonAlgorithm:       "Cloudflare Access: the Access token uses an unsupported signing algorithm. Only RS256 is accepted.\n",
	cfReasonUnknownKey:      "Cloudflare Access: the Access token is signed with a key printpaca does not know. Check that CF_ACCESS_TEAM matches your Zero Trust team name.\n",
	cfReasonSignature:       "Cloudflare Access: the Access token signature is invalid. Check that CF_ACCESS_TEAM matches your Zero Trust team name.\n",
	cfReasonIssuer:          "Cloudflare Access: the Access token was issued by a different team. Check that CF_ACCESS_TEAM matches your Zero Trust team name.\n",
	cfReasonAudience:        "Cloudflare Access: CF_ACCESS_AUD doesn't match the Access application on this hostname. Copy the Application Audience (AUD) Tag from that application.\n",
	cfReasonExpired:         "Cloudflare Access: the Access token has expired. Reload the page to sign in again; also check that the server clock is correct.\n",
	cfReasonNotYetValid:     "Cloudflare Access: the Access token is not valid yet. Check that the server clock is correct.\n",
	cfReasonKeysUnavailable: "Cloudflare Access: printpaca could not load the Access signing keys. Check that the server can reach https://<team>.cloudflareaccess.com and that CF_ACCESS_TEAM is correct.\n",
}

// normalizeCFAccessTeam reduces a team name, "<team>.cloudflareaccess.com" or its https URL to the team name.
func normalizeCFAccessTeam(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	v = strings.TrimPrefix(v, "https://")
	v = strings.TrimSuffix(v, "/")
	v = strings.TrimSuffix(v, ".cloudflareaccess.com")
	if !cfAccessTeamRe.MatchString(v) {
		return "", fmt.Errorf("CF_ACCESS_TEAM %q is not a valid team name; use the name from your Zero Trust dashboard, for example myteam or myteam.cloudflareaccess.com", raw)
	}
	return v, nil
}

// cfAccess verifies Cloudflare Access tokens against the team's key set. The
// zero value is not usable; build one with newCFAccess.
type cfAccess struct {
	team    string
	issuer  string
	keysURL string
	aud     string

	// requirePassword is CF_ACCESS_REQUIRE_PASSWORD=1: a verified request still needs a printpaca session.
	requirePassword bool

	// client and now are injectable for tests only.
	client *http.Client
	now    func() time.Time

	mu   sync.RWMutex
	keys map[string]*rsa.PublicKey

	// fetchMu serializes fetches: a burst of misses on one kid makes one fetch (waiters re-check once locked).
	fetchMu          sync.Mutex
	lastUnknownFetch time.Time
}

// newCFAccess builds a verifier for the already-normalized team and aud.
func newCFAccess(team, aud string) *cfAccess {
	return &cfAccess{
		team:    team,
		issuer:  "https://" + team + ".cloudflareaccess.com",
		keysURL: "https://" + team + ".cloudflareaccess.com/cdn-cgi/access/certs",
		aud:     aud,
		client: &http.Client{
			Timeout: cfAccessFetchTimeout,
			// Don't follow redirects: fetchKeys' non-200 check rejects them, so keys come only from the derived URL.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		now:  time.Now,
		keys: map[string]*rsa.PublicKey{},
	}
}

// parseCFAccessConfig validates CF_ACCESS_TEAM and CF_ACCESS_AUD. It returns (nil, nil) when neither
// is set; one set, an invalid value, or Access with DEMO_MODE is an error naming the variable and fix.
func parseCFAccessConfig(teamRaw, audRaw string, demoMode bool) (*cfAccess, error) {
	teamRaw = strings.TrimSpace(teamRaw)
	audRaw = strings.TrimSpace(audRaw)
	if teamRaw == "" && audRaw == "" {
		return nil, nil
	}
	if teamRaw == "" {
		return nil, fmt.Errorf("CF_ACCESS_AUD is set but CF_ACCESS_TEAM is not; set both to enable Cloudflare Access, or unset both (see docs/cloudflare-tunnel.md)")
	}
	if audRaw == "" {
		return nil, fmt.Errorf("CF_ACCESS_TEAM is set but CF_ACCESS_AUD is not; set both to enable Cloudflare Access, or unset both (see docs/cloudflare-tunnel.md)")
	}
	team, err := normalizeCFAccessTeam(teamRaw)
	if err != nil {
		return nil, err
	}
	aud := strings.ToLower(audRaw)
	if !cfAccessAudRe.MatchString(aud) {
		return nil, fmt.Errorf("CF_ACCESS_AUD must be the 64-character hex Application Audience (AUD) Tag copied from your Access application")
	}
	if demoMode {
		return nil, fmt.Errorf("CF_ACCESS_TEAM/CF_ACCESS_AUD can't be combined with DEMO_MODE=1; the demo already skips the Cloudflare guard, so unset the CF_ACCESS_* variables")
	}
	return newCFAccess(team, aud), nil
}

// parseCFAccessRequirePassword reads CF_ACCESS_REQUIRE_PASSWORD: unset or
// "0" is false, "1" is true, anything else is an error naming the variable.
func parseCFAccessRequirePassword(raw string) (bool, error) {
	switch strings.TrimSpace(raw) {
	case "", "0":
		return false, nil
	case "1":
		return true, nil
	}
	return false, fmt.Errorf("CF_ACCESS_REQUIRE_PASSWORD must be unset, 0, or 1, not %q", raw)
}

// cfAccessRequiresAuthProblem returns a fatal message when CF_ACCESS_REQUIRE_PASSWORD=1 is combined
// with disabled admin auth, since no session could exist. A pure helper so it's testable without main().
func cfAccessRequiresAuthProblem(acc *cfAccess, requirePassword, authEnabled bool) string {
	if acc != nil && requirePassword && !authEnabled {
		return "CF_ACCESS_REQUIRE_PASSWORD=1 requires admin authentication: ADMIN_PASSWORD is set to an empty string; unset ADMIN_PASSWORD (to have one generated) or set a password, or unset CF_ACCESS_REQUIRE_PASSWORD"
	}
	return ""
}

// jwk is one entry of the Access key set.
type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// fetchKeys downloads and parses the key set, returning only usable RSA
// keys of at least cfAccessMinRSABits bits. It does not touch c.keys.
func (c *cfAccess) fetchKeys() (map[string]*rsa.PublicKey, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cfAccessFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.keysURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("key set responded with HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, cfAccessMaxJWKSBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > cfAccessMaxJWKSBytes {
		return nil, fmt.Errorf("key set larger than %d bytes", cfAccessMaxJWKSBytes)
	}
	var doc struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("key set is not valid JSON: %w", err)
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		nb, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			continue
		}
		eb, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil || len(eb) == 0 || len(eb) > 4 {
			continue
		}
		n := new(big.Int).SetBytes(nb)
		if n.BitLen() < cfAccessMinRSABits {
			continue
		}
		e := int(new(big.Int).SetBytes(eb).Int64())
		if e < 3 || e%2 == 0 {
			continue
		}
		out[k.Kid] = &rsa.PublicKey{N: n, E: e}
	}
	// An empty set is a failed fetch: accepting it would replace the last good keys and lock every request out.
	if len(out) == 0 {
		return nil, fmt.Errorf("key set contains no usable RSA keys of at least %d bits", cfAccessMinRSABits)
	}
	return out, nil
}

// refresh fetches the key set and, on success, replaces the held keys.
// A failed fetch keeps the last good set. Returns the number of keys loaded.
func (c *cfAccess) refresh() (int, error) {
	keys, err := c.fetchKeys()
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	c.keys = keys
	c.mu.Unlock()
	return len(keys), nil
}

// start loads keys in the background (an unreachable Cloudflare never blocks startup, so LAN access
// keeps working) and refreshes hourly until ctx ends.
func (c *cfAccess) start(ctx context.Context) {
	go func() {
		load := func() {
			// Same lock as lookupKey's refetch, so fetches never overlap.
			c.fetchMu.Lock()
			n, err := c.refresh()
			c.fetchMu.Unlock()
			if err != nil {
				log.Printf("Cloudflare Access: could not load keys from %s: %v (requests through Cloudflare are rejected until it succeeds)", c.keysURL, err)
			} else {
				log.Printf("Cloudflare Access: loaded %d keys for team %s", n, c.team)
			}
		}
		load()
		t := time.NewTicker(cfAccessRefreshInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				load()
			}
		}
	}()
}

func (c *cfAccess) key(kid string) (*rsa.PublicKey, int) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.keys[kid], len(c.keys)
}

// lookupKey returns the key for kid, refetching the set when kid is unknown (single-flight, at most
// once per cfAccessUnknownKidRefetchGap). The second return is a reason code when no key is available.
// The refetch holds fetchMu, so concurrent misses wait at most cfAccessFetchTimeout, once per window,
// and only for requests arriving through Cloudflare.
func (c *cfAccess) lookupKey(kid string) (*rsa.PublicKey, string) {
	if k, _ := c.key(kid); k != nil {
		return k, ""
	}
	c.fetchMu.Lock()
	// A concurrent caller may have fetched while we waited.
	if k, _ := c.key(kid); k != nil {
		c.fetchMu.Unlock()
		return k, ""
	}
	if now := c.now(); now.Sub(c.lastUnknownFetch) >= cfAccessUnknownKidRefetchGap {
		c.lastUnknownFetch = now
		c.refresh() // error ignored: the last good set is kept, and the periodic loader logs failures
	}
	c.fetchMu.Unlock()
	k, total := c.key(kid)
	if k != nil {
		return k, ""
	}
	if total == 0 {
		return nil, cfReasonKeysUnavailable
	}
	return nil, cfReasonUnknownKey
}

// verifyClaims checks a Cf-Access-Jwt-Assertion value. reason is "" when valid, else a reason code; email
// is the email claim (trimmed, ASCII-lowercased; "" if missing or not a string).
func (c *cfAccess) verifyClaims(token string) (email, reason string) {
	if token == "" {
		return "", cfReasonMissing
	}
	if len(token) > cfAccessMaxTokenBytes {
		return "", cfReasonMalformed
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", cfReasonMalformed
	}
	hdrJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", cfReasonMalformed
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", cfReasonMalformed
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", cfReasonMalformed
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(hdrJSON, &hdr); err != nil {
		return "", cfReasonMalformed
	}
	if hdr.Alg != "RS256" {
		return "", cfReasonAlgorithm
	}
	if hdr.Kid == "" {
		return "", cfReasonMalformed
	}

	pub, reason := c.lookupKey(hdr.Kid)
	if reason != "" {
		return "", reason
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		return "", cfReasonSignature
	}

	var claims struct {
		Iss string          `json:"iss"`
		Aud json.RawMessage `json:"aud"`
		Exp *float64        `json:"exp"`
		Nbf *float64        `json:"nbf"`
		Iat *float64        `json:"iat"`
		// Email is decoded as raw JSON so a non-string claim yields no
		// identity instead of failing verification.
		Email json.RawMessage `json:"email"`
	}
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return "", cfReasonMalformed
	}
	if claims.Iss != c.issuer {
		return "", cfReasonIssuer
	}
	if !cfAudContains(claims.Aud, c.aud) {
		return "", cfReasonAudience
	}
	if claims.Exp == nil {
		return "", cfReasonMalformed
	}
	// Bounds-check before converting: int64 of NaN/Inf/huge floats is implementation-defined.
	for _, v := range []*float64{claims.Exp, claims.Nbf, claims.Iat} {
		if v != nil && !cfValidUnix(*v) {
			return "", cfReasonMalformed
		}
	}
	now := c.now()
	if !unixTime(*claims.Exp).Add(cfAccessLeeway).After(now) {
		return "", cfReasonExpired
	}
	if claims.Nbf != nil && unixTime(*claims.Nbf).After(now.Add(cfAccessLeeway)) {
		return "", cfReasonNotYetValid
	}
	if claims.Iat != nil && unixTime(*claims.Iat).After(now.Add(cfAccessLeeway)) {
		return "", cfReasonNotYetValid
	}
	var em string
	if json.Unmarshal(claims.Email, &em) == nil {
		email = foldEmail(em)
	}
	return email, ""
}

// cfMaxUnix is 9999-12-31T23:59:59Z, the latest time a claim may name.
const cfMaxUnix = 253402300799

// cfValidUnix reports whether sec is a finite time between 1970 and year 9999.
func cfValidUnix(sec float64) bool {
	return !math.IsNaN(sec) && sec >= 0 && sec <= cfMaxUnix
}

func unixTime(sec float64) time.Time {
	return time.Unix(int64(sec), 0)
}

// cfAudContains reports whether the aud claim (a string or an array of
// strings) contains want.
func cfAudContains(raw json.RawMessage, want string) bool {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return one == want
	}
	var many []string
	if json.Unmarshal(raw, &many) == nil {
		if slices.Contains(many, want) {
			return true
		}
	}
	return false
}

type cfVerifiedKey struct{}

// cfEmailKey carries the verified token's email claim in the request context.
type cfEmailKey struct{}

// cfVerifiedHolder is installed by accessLogMiddleware so the access log, which runs outside the
// auth middleware, can learn the request was Access-verified.
type cfVerifiedHolder struct {
	mu sync.Mutex
	ok bool
}

type cfHolderKey struct{}

// markCFVerified returns r marked as Access-verified, and flags the access
// log's holder when present.
func markCFVerified(r *http.Request, email string) *http.Request {
	if h, ok := r.Context().Value(cfHolderKey{}).(*cfVerifiedHolder); ok {
		h.mu.Lock()
		h.ok = true
		h.mu.Unlock()
	}
	ctx := context.WithValue(r.Context(), cfVerifiedKey{}, true)
	return r.WithContext(context.WithValue(ctx, cfEmailKey{}, email))
}

// cfAccessVerified reports whether r passed Access verification in the admin
// middleware.
func cfAccessVerified(r *http.Request) bool {
	v, _ := r.Context().Value(cfVerifiedKey{}).(bool)
	return v
}

// cfConnectingIP returns CF-Connecting-IP for an Access-verified request, or "" if unverified or not an IP.
// The token proves only that the caller holds a valid Access token: one who reaches the admin port
// directly (LAN) can forge this header. Accepted residual risk: they can dodge the per-IP login
// budget; the global budget still applies and they still need the password.
func cfConnectingIP(r *http.Request) string {
	if !cfAccessVerified(r) {
		return ""
	}
	v := strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))
	if net.ParseIP(v) == nil {
		return ""
	}
	return v
}
