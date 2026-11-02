package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testCFTeam = "myteam"
	testCFAud  = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	testCFKid  = "kid-1"
)

var testCFNow = time.Unix(1_800_000_000, 0)

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func genRSA(t *testing.T, bits int) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func jwkFor(kid string, pub *rsa.PublicKey) map[string]string {
	e := []byte{byte(pub.E >> 16), byte(pub.E >> 8), byte(pub.E)}
	return map[string]string{"kid": kid, "kty": "RSA", "alg": "RS256", "use": "sig", "n": b64(pub.N.Bytes()), "e": b64(e)}
}

// cfFixture is a verifier wired to an httptest JWKS server.
type cfFixture struct {
	t     *testing.T
	key   *rsa.PrivateKey
	srv   *httptest.Server
	cf    *cfAccess
	hits  atomic.Int32
	body  atomic.Value // []byte served by the JWKS endpoint
	code  atomic.Int32
	clock atomic.Int64 // unix seconds
}

func newCFFixture(t *testing.T) *cfFixture {
	t.Helper()
	f := &cfFixture{t: t, key: genRSA(t, 2048)}
	f.setKeys(map[string]*rsa.PublicKey{testCFKid: &f.key.PublicKey})
	f.code.Store(200)
	f.clock.Store(testCFNow.Unix())
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		w.WriteHeader(int(f.code.Load()))
		w.Write(f.body.Load().([]byte))
	}))
	t.Cleanup(f.srv.Close)
	f.cf = newCFAccess(testCFTeam, testCFAud)
	f.cf.client = f.srv.Client()
	f.cf.keysURL = f.srv.URL
	f.cf.now = func() time.Time { return time.Unix(f.clock.Load(), 0) }
	return f
}

func (f *cfFixture) setKeys(keys map[string]*rsa.PublicKey) {
	var list []map[string]string
	for kid, k := range keys {
		list = append(list, jwkFor(kid, k))
	}
	b, _ := json.Marshal(map[string]any{"keys": list})
	f.body.Store(b)
}

func (f *cfFixture) load() {
	f.t.Helper()
	if _, err := f.cf.refresh(); err != nil {
		f.t.Fatal(err)
	}
}

type tokOpt struct {
	hdr    map[string]any
	claims map[string]any
	key    *rsa.PrivateKey
}

func (f *cfFixture) token(mut func(*tokOpt)) string {
	o := &tokOpt{
		hdr: map[string]any{"alg": "RS256", "kid": testCFKid, "typ": "JWT"},
		claims: map[string]any{
			"iss": "https://" + testCFTeam + ".cloudflareaccess.com",
			"aud": []string{testCFAud},
			"exp": testCFNow.Unix() + 3600,
			"iat": testCFNow.Unix() - 10,
			"nbf": testCFNow.Unix() - 10,
		},
		key: f.key,
	}
	if mut != nil {
		mut(o)
	}
	h, _ := json.Marshal(o.hdr)
	p, _ := json.Marshal(o.claims)
	signing := b64(h) + "." + b64(p)
	d := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, o.key, crypto.SHA256, d[:])
	if err != nil {
		f.t.Fatal(err)
	}
	return signing + "." + b64(sig)
}

func TestCFAccessVerifyValid(t *testing.T) {
	f := newCFFixture(t)
	f.load()
	if _, r := f.cf.verifyClaims(f.token(nil)); r != "" {
		t.Fatalf("valid token rejected: %s", r)
	}
	// aud as a plain string also passes.
	tok := f.token(func(o *tokOpt) { o.claims["aud"] = testCFAud })
	if _, r := f.cf.verifyClaims(tok); r != "" {
		t.Fatalf("string aud rejected: %s", r)
	}
	// aud array with several entries containing ours passes.
	tok = f.token(func(o *tokOpt) { o.claims["aud"] = []string{"other", testCFAud} })
	if _, r := f.cf.verifyClaims(tok); r != "" {
		t.Fatalf("aud array rejected: %s", r)
	}
}

func TestCFAccessVerifyReasons(t *testing.T) {
	f := newCFFixture(t)
	f.load()
	valid := f.token(nil)
	parts := strings.Split(valid, ".")

	// HS256 token signed with the DER public key bytes as the HMAC secret.
	der, _ := x509.MarshalPKIXPublicKey(&f.key.PublicKey)
	hsHdr, _ := json.Marshal(map[string]any{"alg": "HS256", "kid": testCFKid})
	hsSigning := b64(hsHdr) + "." + parts[1]
	mac := hmac.New(sha256.New, der)
	mac.Write([]byte(hsSigning))
	hs256 := hsSigning + "." + b64(mac.Sum(nil))

	noneHdr, _ := json.Marshal(map[string]any{"alg": "none", "kid": testCFKid})
	noneTok := b64(noneHdr) + "." + parts[1] + "."

	tamperedPayload, _ := json.Marshal(map[string]any{"iss": "https://" + testCFTeam + ".cloudflareaccess.com", "aud": []string{testCFAud}, "exp": testCFNow.Unix() + 99999})
	tampered := parts[0] + "." + b64(tamperedPayload) + "." + parts[2]

	otherKey := genRSA(t, 2048)

	cases := []struct {
		name  string
		token string
		want  string
	}{
		{"missing", "", cfReasonMissing},
		{"two segments", "a.b", cfReasonMalformed},
		{"four segments", "a.b.c.d", cfReasonMalformed},
		{"bad base64", "!!!." + parts[1] + "." + parts[2], cfReasonMalformed},
		{"padded base64", parts[0] + "=." + parts[1] + "." + parts[2], cfReasonMalformed},
		{"oversize", strings.Repeat("a", cfAccessMaxTokenBytes+1), cfReasonMalformed},
		{"no kid", f.token(func(o *tokOpt) { delete(o.hdr, "kid") }), cfReasonMalformed},
		{"no exp", f.token(func(o *tokOpt) { delete(o.claims, "exp") }), cfReasonMalformed},
		{"alg none", noneTok, cfReasonAlgorithm},
		{"alg HS256", hs256, cfReasonAlgorithm},
		{"alg ES256", f.token(func(o *tokOpt) { o.hdr["alg"] = "ES256" }), cfReasonAlgorithm},
		{"alg PS256", f.token(func(o *tokOpt) { o.hdr["alg"] = "PS256" }), cfReasonAlgorithm},
		{"alg missing", f.token(func(o *tokOpt) { delete(o.hdr, "alg") }), cfReasonAlgorithm},
		{"tampered payload", tampered, cfReasonSignature},
		{"signed by other key", f.token(func(o *tokOpt) { o.key = otherKey }), cfReasonSignature},
		{"wrong issuer", f.token(func(o *tokOpt) { o.claims["iss"] = "https://evil.cloudflareaccess.com" }), cfReasonIssuer},
		{"wrong audience", f.token(func(o *tokOpt) { o.claims["aud"] = []string{strings.Repeat("a", 64)} }), cfReasonAudience},
		{"no audience", f.token(func(o *tokOpt) { delete(o.claims, "aud") }), cfReasonAudience},
		{"expired", f.token(func(o *tokOpt) { o.claims["exp"] = testCFNow.Unix() - 120 }), cfReasonExpired},
		{"expired within leeway passes", f.token(func(o *tokOpt) { o.claims["exp"] = testCFNow.Unix() - 30 }), ""},
		{"nbf in future", f.token(func(o *tokOpt) { o.claims["nbf"] = testCFNow.Unix() + 600 }), cfReasonNotYetValid},
		{"iat in future", f.token(func(o *tokOpt) { o.claims["iat"] = testCFNow.Unix() + 600 }), cfReasonNotYetValid},
		{"nbf within leeway passes", f.token(func(o *tokOpt) { o.claims["nbf"] = testCFNow.Unix() + 30 }), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, got := f.cf.verifyClaims(c.token); got != c.want {
				t.Fatalf("verify = %q, want %q", got, c.want)
			}
		})
	}
}

func TestCFAccessEveryReasonHasABody(t *testing.T) {
	for _, r := range []string{cfReasonMissing, cfReasonMalformed, cfReasonAlgorithm, cfReasonUnknownKey, cfReasonSignature, cfReasonIssuer, cfReasonAudience, cfReasonExpired, cfReasonNotYetValid, cfReasonKeysUnavailable} {
		b := cfAccessErrorBodies[r]
		if b == "" || strings.Count(b, "\n") != 1 || !strings.HasSuffix(b, "\n") {
			t.Errorf("reason %s: body must be one non-empty line, got %q", r, b)
		}
	}
	if !strings.Contains(cfAccessErrorBodies[cfReasonAudience], "CF_ACCESS_AUD") {
		t.Error("audience body must name CF_ACCESS_AUD")
	}
}

func TestCFAccessUnknownKidRefetchesOnceThenRateLimits(t *testing.T) {
	f := newCFFixture(t)
	f.load()
	hits := f.hits.Load()

	unknown := f.token(func(o *tokOpt) { o.hdr["kid"] = "rotated" })
	if _, r := f.cf.verifyClaims(unknown); r != cfReasonUnknownKey {
		t.Fatalf("first unknown kid = %q, want unknown_key", r)
	}
	if got := f.hits.Load() - hits; got != 1 {
		t.Fatalf("refetches after first unknown kid = %d, want 1", got)
	}
	for range 5 {
		f.cf.verifyClaims(unknown)
	}
	if got := f.hits.Load() - hits; got != 1 {
		t.Fatalf("refetches within the rate-limit gap = %d, want still 1", got)
	}

	// After the gap, and once Cloudflare has published the new key, the same
	// kid resolves through exactly one more refetch.
	f.clock.Add(int64(cfAccessUnknownKidRefetchGap/time.Second) + 1)
	f.setKeys(map[string]*rsa.PublicKey{testCFKid: &f.key.PublicKey, "rotated": &f.key.PublicKey})
	if _, r := f.cf.verifyClaims(unknown); r != "" {
		t.Fatalf("rotated key after refetch = %q, want valid", r)
	}
	if got := f.hits.Load() - hits; got != 2 {
		t.Fatalf("total refetches = %d, want 2", got)
	}
}

func TestCFAccessKeysUnavailable(t *testing.T) {
	f := newCFFixture(t)
	f.srv.Close() // unreachable
	if _, r := f.cf.verifyClaims(f.token(nil)); r != cfReasonKeysUnavailable {
		t.Fatalf("unreachable JWKS = %q, want keys_unavailable", r)
	}
}

func TestCFAccessJWKSFailures(t *testing.T) {
	t.Run("non-200", func(t *testing.T) {
		f := newCFFixture(t)
		f.code.Store(500)
		if _, err := f.cf.refresh(); err == nil {
			t.Fatal("expected error on HTTP 500")
		}
		if _, r := f.cf.verifyClaims(f.token(nil)); r != cfReasonKeysUnavailable {
			t.Fatalf("verify = %q, want keys_unavailable", r)
		}
	})
	t.Run("oversized", func(t *testing.T) {
		f := newCFFixture(t)
		f.body.Store([]byte(`{"keys":[],"pad":"` + strings.Repeat("x", cfAccessMaxJWKSBytes) + `"}`))
		if _, err := f.cf.refresh(); err == nil {
			t.Fatal("expected error on oversized key set")
		}
		if _, r := f.cf.verifyClaims(f.token(nil)); r != cfReasonKeysUnavailable {
			t.Fatalf("verify = %q, want keys_unavailable", r)
		}
	})
	t.Run("not json", func(t *testing.T) {
		f := newCFFixture(t)
		f.body.Store([]byte(`nope`))
		if _, err := f.cf.refresh(); err == nil {
			t.Fatal("expected error on invalid JSON")
		}
	})
	t.Run("small key ignored", func(t *testing.T) {
		f := newCFFixture(t)
		small := genRSA(t, 1024)
		f.setKeys(map[string]*rsa.PublicKey{testCFKid: &small.PublicKey})
		if n, err := f.cf.refresh(); err == nil {
			t.Fatalf("refresh = %d, nil; want an error for a set with no usable keys", n)
		}
		tok := f.token(func(o *tokOpt) { o.key = small })
		if _, r := f.cf.verifyClaims(tok); r != cfReasonKeysUnavailable {
			t.Fatalf("token from a 1024-bit key = %q, want keys_unavailable", r)
		}
	})
	t.Run("non-RSA ignored", func(t *testing.T) {
		f := newCFFixture(t)
		f.body.Store([]byte(`{"keys":[{"kid":"e","kty":"EC","crv":"P-256","x":"AA","y":"AA"}]}`))
		if n, err := f.cf.refresh(); err == nil {
			t.Fatalf("refresh = %d, nil; want an error for a set with no usable keys", n)
		}
	})
	t.Run("failed refresh keeps last good keys", func(t *testing.T) {
		f := newCFFixture(t)
		f.load()
		f.code.Store(503)
		if _, err := f.cf.refresh(); err == nil {
			t.Fatal("expected error")
		}
		if _, r := f.cf.verifyClaims(f.token(nil)); r != "" {
			t.Fatalf("verify after failed refresh = %q, want valid", r)
		}
	})
}

func TestCFAccessEmptyKeySetKeepsLastGoodKeys(t *testing.T) {
	f := newCFFixture(t)
	f.load()
	f.body.Store([]byte(`{"keys":[]}`))
	if _, err := f.cf.refresh(); err == nil {
		t.Fatal("refresh of an empty key set must error")
	}
	if _, r := f.cf.verifyClaims(f.token(nil)); r != "" {
		t.Fatalf("verify after empty refresh = %q, want valid (last good set kept)", r)
	}
}

func TestCFAccessJWKSRedirectNotFollowed(t *testing.T) {
	f := newCFFixture(t)
	var targetHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		w.Write(f.body.Load().([]byte))
	}))
	t.Cleanup(target.Close)
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	t.Cleanup(redir.Close)
	f.cf.client = newCFAccess(testCFTeam, testCFAud).client // production client policy
	f.cf.keysURL = redir.URL
	if _, err := f.cf.refresh(); err == nil {
		t.Fatal("a 302 from the key-set URL must be an error")
	}
	if targetHits.Load() != 0 {
		t.Fatal("redirect target was fetched; redirects must not be followed")
	}
}

func TestCFAccessKeysUnavailableRecoversOnFirstRequest(t *testing.T) {
	// Startup fetch failed (nothing loaded); the first request retries.
	f := newCFFixture(t)
	if _, r := f.cf.verifyClaims(f.token(nil)); r != "" {
		t.Fatalf("lazy load = %q, want valid", r)
	}
}

func TestNormalizeCFAccessTeam(t *testing.T) {
	good := []string{"myteam", "MyTeam", " myteam ", "myteam.cloudflareaccess.com", "https://myteam.cloudflareaccess.com", "https://myteam.cloudflareaccess.com/", "HTTPS://MyTeam.CloudflareAccess.com/", "my-team"}
	for _, in := range good {
		got, err := normalizeCFAccessTeam(in)
		want := "myteam"
		if in == "my-team" {
			want = "my-team"
		}
		if err != nil || got != want {
			t.Errorf("normalize(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{"", "-bad", "bad-", "a b", "my_team", "http://myteam.cloudflareaccess.com", "myteam.example.com", "myteam/path", "https://myteam.cloudflareaccess.com/x", strings.Repeat("a", 64)}
	for _, in := range bad {
		if got, err := normalizeCFAccessTeam(in); err == nil {
			t.Errorf("normalize(%q) = %q, want error", in, got)
		}
	}
}

func TestParseCFAccessConfig(t *testing.T) {
	cf, err := parseCFAccessConfig("", "", false)
	if cf != nil || err != nil {
		t.Fatalf("both unset = %v, %v; want nil, nil", cf, err)
	}
	cf, err = parseCFAccessConfig("https://MyTeam.cloudflareaccess.com/", " "+strings.ToUpper(testCFAud)+" ", false)
	if err != nil || cf == nil {
		t.Fatalf("valid = %v, %v", cf, err)
	}
	if cf.issuer != "https://myteam.cloudflareaccess.com" || cf.keysURL != "https://myteam.cloudflareaccess.com/cdn-cgi/access/certs" || cf.aud != testCFAud {
		t.Fatalf("derived values wrong: %+v", cf)
	}
	errs := []struct{ team, aud, demo, wantVar string }{
		{"myteam", "", "", "CF_ACCESS_AUD"},
		{"", testCFAud, "", "CF_ACCESS_TEAM"},
		{"bad team!", testCFAud, "", "CF_ACCESS_TEAM"},
		{"myteam", "xyz", "", "CF_ACCESS_AUD"},
		{"myteam", testCFAud[:63], "", "CF_ACCESS_AUD"},
		{"myteam", testCFAud, "1", "DEMO_MODE"},
	}
	for _, e := range errs {
		_, err := parseCFAccessConfig(e.team, e.aud, e.demo == "1")
		if err == nil || !strings.Contains(err.Error(), e.wantVar) {
			t.Errorf("parse(%q,%q,demo=%q) err = %v; want one naming %s", e.team, e.aud, e.demo, err, e.wantVar)
		}
	}
}

func TestParseCFAccessRequirePassword(t *testing.T) {
	for raw, want := range map[string]bool{"": false, "0": false, "1": true, " 1 ": true} {
		got, err := parseCFAccessRequirePassword(raw)
		if err != nil || got != want {
			t.Errorf("parse(%q) = %v, %v; want %v, nil", raw, got, err, want)
		}
	}
	for _, raw := range []string{"true", "yes", "2", "on"} {
		if _, err := parseCFAccessRequirePassword(raw); err == nil || !strings.Contains(err.Error(), "CF_ACCESS_REQUIRE_PASSWORD") {
			t.Errorf("parse(%q) err = %v, want one naming CF_ACCESS_REQUIRE_PASSWORD", raw, err)
		}
	}
}

func TestCFAccessEmailClaim(t *testing.T) {
	f := newCFFixture(t)
	f.load()
	cases := []struct {
		name  string
		claim any
		set   bool
		want  string
	}{
		{"plain", "a@example.com", true, "a@example.com"},
		{"trimmed and lowercased", "  A@Example.COM ", true, "a@example.com"},
		{"missing", nil, false, ""},
		{"empty", "", true, ""},
		{"not a string", 42, true, ""},
	}
	for _, tc := range cases {
		email, reason := f.cf.verifyClaims(f.token(func(o *tokOpt) {
			if tc.set {
				o.claims["email"] = tc.claim
			}
		}))
		if reason != "" || email != tc.want {
			t.Errorf("%s: email = %q reason = %q, want %q", tc.name, email, reason, tc.want)
		}
	}
	if email, reason := f.cf.verifyClaims("bad"); email != "" || reason != cfReasonMalformed {
		t.Errorf("malformed token returned email %q reason %q", email, reason)
	}
}

// ---------------------------------------------------------------------------
// Middleware integration
// ---------------------------------------------------------------------------

func cfAuthWithAccess(t *testing.T, f *cfFixture, allowedHosts []string) (*authState, http.Handler) {
	t.Helper()
	f.load()
	auth := newAuthState("secret", allowedHosts)
	auth.cfAccess = f.cf
	return auth, auth.middleware(loginMux(auth))
}

func cfReq(method, path, host, body string, hdr map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = host
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "https://"+host)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return req
}

func TestCFAccessMiddlewareVerifiedLogin(t *testing.T) {
	f := newCFFixture(t)
	auth, h := cfAuthWithAccess(t, f, nil)
	tok := f.token(nil)
	hdr := map[string]string{"Cf-Access-Jwt-Assertion": tok, "CF-Connecting-IP": "203.0.113.9", "Cf-Ray": "abc"}

	// Host admin.example.com is not in the allowlist and not an IP.
	rec := serveReq(h, cfReq("POST", "/api/login", "admin.example.com", `{"password":"secret"}`, hdr))
	if rec.Code != http.StatusOK {
		t.Fatalf("verified login = %d (%s), want 200", rec.Code, rec.Body.String())
	}
	var secure bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == authCookieName {
			secure = c.Secure
		}
	}
	if !secure {
		t.Fatal("session cookie must be Secure for a verified request without ADMIN_TRUST_PROXY")
	}

	// The failure budget is keyed on CF-Connecting-IP, not the socket address.
	rec = serveReq(h, cfReq("POST", "/api/login", "admin.example.com", `{"password":"wrong"}`, hdr))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, want 401", rec.Code)
	}
	if !auth.ipLoginLimiter.exhausted("203.0.113.9", 1, time.Hour) {
		t.Fatal("login failure should be keyed on CF-Connecting-IP")
	}
}

func TestCFAccessMiddlewareRejections(t *testing.T) {
	f := newCFFixture(t)
	_, h := cfAuthWithAccess(t, f, nil)
	cfHdr := map[string]string{"CF-Connecting-IP": "203.0.113.9"}

	// No token: 403 missing.
	rec := serveReq(h, cfReq("GET", "/api/probe", "admin.example.com", "", cfHdr))
	if rec.Code != http.StatusForbidden || rec.Body.String() != cfAccessErrorBodies[cfReasonMissing] {
		t.Fatalf("no token = %d %q, want 403 missing body", rec.Code, rec.Body.String())
	}

	// Bad token never falls through, even for an allowed Host.
	rec = serveReq(h, cfReq("GET", "/api/probe", "127.0.0.1:8080", "", map[string]string{"CF-Connecting-IP": "203.0.113.9", "Cf-Access-Jwt-Assertion": f.token(func(o *tokOpt) { o.claims["aud"] = "nope" })}))
	if rec.Code != http.StatusForbidden || rec.Body.String() != cfAccessErrorBodies[cfReasonAudience] {
		t.Fatalf("bad audience = %d %q", rec.Code, rec.Body.String())
	}

	// A token without any Cloudflare header is not treated as Access traffic:
	// the unchanged Host allowlist applies.
	rec = serveReq(h, cfReq("GET", "/api/probe", "admin.example.com", "", map[string]string{"Cf-Access-Jwt-Assertion": f.token(nil)}))
	if rec.Code != http.StatusForbidden || rec.Body.String() != allowedHostsErrorBody {
		t.Fatalf("token without CF header, unlisted host = %d %q, want Host allowlist 403", rec.Code, rec.Body.String())
	}
}

func TestCFAccessMiddlewareUnchangedWithoutCloudflareHeader(t *testing.T) {
	f := newCFFixture(t)
	_, h := cfAuthWithAccess(t, f, []string{"printpaca.local"})

	// LAN request to an IP: reaches the session check as today.
	rec := serveReq(h, cfReq("GET", "/api/probe", "192.168.1.5:8080", "", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("LAN request = %d, want 401", rec.Code)
	}
	// Unlisted name: Host allowlist rejection, identical body.
	rec = serveReq(h, cfReq("GET", "/api/probe", "admin.example.com", "", nil))
	if rec.Code != http.StatusForbidden || rec.Body.String() != allowedHostsErrorBody {
		t.Fatalf("unlisted host = %d %q", rec.Code, rec.Body.String())
	}
	// Plain-HTTP LAN login keeps a non-Secure cookie.
	rec = serveReq(h, cfReq("POST", "/api/login", "192.168.1.5:8080", `{"password":"secret"}`, map[string]string{"Origin": "http://192.168.1.5:8080"}))
	if rec.Code != http.StatusOK {
		t.Fatalf("LAN login = %d (%s)", rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == authCookieName && c.Secure {
			t.Fatal("LAN cookie must not be Secure")
		}
	}
}

// Without Access configured, a stray Access header changes nothing: with a password the tunneled request
// goes on to the session check, with authentication off it keeps the tunnel guard's 403.
func TestCFAccessOffTunnelGuardRules(t *testing.T) {
	hdr := map[string]string{"CF-Connecting-IP": "203.0.113.9", "Cf-Access-Jwt-Assertion": "anything"}

	auth := newAuthState("secret", nil)
	h := auth.middleware(loginMux(auth))
	rec := serveReq(h, cfReq("GET", "/api/probe", "127.0.0.1:8080", "", hdr))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("Access off, password set = %d %q, want 401 (password is the lock)", rec.Code, rec.Body.String())
	}

	auth = newAuthState("", nil)
	h = auth.middleware(loginMux(auth))
	rec = serveReq(h, cfReq("GET", "/api/probe", "127.0.0.1:8080", "", hdr))
	if rec.Code != http.StatusForbidden || rec.Body.String() != cloudflareTunnelGuardErrorBody {
		t.Fatalf("Access off, auth off = %d %q, want the tunnel guard 403", rec.Code, rec.Body.String())
	}
}

func TestClientIPUsesCFConnectingIPOnlyWhenVerified(t *testing.T) {
	req := cfReq("GET", "/", "x", "", map[string]string{"CF-Connecting-IP": "203.0.113.9"})
	req.RemoteAddr = "10.0.0.2:5555"
	if got := clientIP(req, false); got != "10.0.0.2" {
		t.Fatalf("unverified clientIP = %q, want socket address", got)
	}
	v := markCFVerified(req, "")
	if got := clientIP(v, false); got != "203.0.113.9" {
		t.Fatalf("verified clientIP = %q, want CF-Connecting-IP", got)
	}
	bad := cfReq("GET", "/", "x", "", map[string]string{"CF-Connecting-IP": "not-an-ip"})
	bad.RemoteAddr = "10.0.0.2:5555"
	if got := clientIP(markCFVerified(bad, ""), false); got != "10.0.0.2" {
		t.Fatalf("verified but unparsable = %q, want socket fallback", got)
	}
}

func TestCFAccessAccessLogUsesCFConnectingIP(t *testing.T) {
	f := newCFFixture(t)
	auth, _ := cfAuthWithAccess(t, f, nil)
	mux := loginMux(auth)
	var buf bytes.Buffer
	restore := redirectLog(t, &buf)
	defer restore()

	h := adminListenerHandler(mux, auth, false)
	// A wrong-password login is a 401 in either mode, so the admin access log
	// filter (401/403/5xx only) logs it. A bare verified GET would now be a 200
	// (Access is the login) and wouldn't be logged at all.
	req := cfReq("POST", "/api/login", "admin.example.com", `{"password":"wrong"}`, map[string]string{"CF-Connecting-IP": "203.0.113.9", "Cf-Access-Jwt-Assertion": f.token(nil)})
	req.RemoteAddr = "172.18.0.5:4444"
	h.ServeHTTP(httptest.NewRecorder(), req)
	if !strings.Contains(buf.String(), "203.0.113.9") || strings.Contains(buf.String(), "172.18.0.5") {
		t.Fatalf("access log should show the CF client address only: %s", buf.String())
	}
}

// lockedBuffer is a bytes.Buffer safe for the start goroutine's log writes
// to race the test's polling reads.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func TestCFAccessStartLogsLoadedKeys(t *testing.T) {
	f := newCFFixture(t)
	var buf lockedBuffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)
	ctx, cancel := contextWithCancel()
	defer cancel()
	f.cf.start(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(buf.String(), fmt.Sprintf("Cloudflare Access: loaded 1 keys for team %s", testCFTeam)) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("startup log line not seen: %s", buf.String())
}

func contextWithCancel() (context.Context, context.CancelFunc) {
	return context.WithCancel(context.Background())
}

func TestCFAccessTimeClaimBounds(t *testing.T) {
	f := newCFFixture(t)
	f.load()
	// JSON can't carry NaN/Inf, but it can carry huge or negative numbers
	// whose int64 conversion is undefined or wraps.
	for _, claim := range []string{"exp", "nbf", "iat"} {
		for _, bad := range []any{-1, 1e30, 253402300800.0} {
			tok := f.token(func(o *tokOpt) { o.claims[claim] = bad })
			if _, r := f.cf.verifyClaims(tok); r != cfReasonMalformed {
				t.Errorf("%s=%v = %q, want malformed", claim, bad, r)
			}
		}
	}
	if cfValidUnix(math.NaN()) || cfValidUnix(math.Inf(1)) || cfValidUnix(math.Inf(-1)) {
		t.Error("cfValidUnix must reject NaN and infinities")
	}
}

func TestCFAccessUnknownKidParallelSingleFetch(t *testing.T) {
	f := newCFFixture(t)
	f.load()
	hits := f.hits.Load()
	unknown := f.token(func(o *tokOpt) { o.hdr["kid"] = "rotated" })
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			f.cf.verifyClaims(unknown)
		})
	}
	wg.Wait()
	if got := f.hits.Load() - hits; got != 1 {
		t.Fatalf("20 parallel unknown-kid requests caused %d JWKS fetches, want 1", got)
	}
}

func TestCFAccessStaticPathNeedsToken(t *testing.T) {
	f := newCFFixture(t)
	_, h := cfAuthWithAccess(t, f, nil)
	rec := serveReq(h, cfReq("GET", "/", "admin.example.com", "", map[string]string{"CF-Connecting-IP": "203.0.113.9"}))
	if rec.Code != http.StatusForbidden || rec.Body.String() != cfAccessErrorBodies[cfReasonMissing] {
		t.Fatalf("static path without token = %d %q, want 403 missing body", rec.Code, rec.Body.String())
	}
}

func TestCFAccessVerifiedStillSubjectToCSRF(t *testing.T) {
	f := newCFFixture(t)
	_, h := cfAuthWithAccess(t, f, nil)
	req := cfReq("POST", "/api/login", "admin.example.com", `{"password":"secret"}`, map[string]string{
		"Cf-Access-Jwt-Assertion": f.token(nil),
		"CF-Connecting-IP":        "203.0.113.9",
		"Origin":                  "https://evil.example.net",
	})
	rec := serveReq(h, req)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "cross-origin request not allowed") {
		t.Fatalf("verified cross-site POST = %d %q, want the CSRF 403", rec.Code, rec.Body.String())
	}
}

func TestCFAccessRequiresAuthProblem(t *testing.T) {
	acc := newCFAccess(testCFTeam, testCFAud)
	cases := []struct {
		name            string
		acc             *cfAccess
		requirePassword bool
		authEnabled     bool
		wantProblem     bool
	}{
		{"access on, auth on", acc, false, true, false},
		// Access is the login, so auth off is not refused.
		{"access on, auth off", acc, false, false, false},
		{"access on, require password, auth on", acc, true, true, false},
		{"access on, require password, auth off", acc, true, false, true},
		{"access off, auth off", nil, false, false, false},
		{"access off, auth on", nil, false, true, false},
		{"access off, require password, auth off", nil, true, false, false},
	}
	for _, tc := range cases {
		got := cfAccessRequiresAuthProblem(tc.acc, tc.requirePassword, tc.authEnabled)
		if (got != "") != tc.wantProblem {
			t.Errorf("%s: problem = %q, wantProblem = %v", tc.name, got, tc.wantProblem)
		}
		if got != "" && (!strings.Contains(got, "ADMIN_PASSWORD") || !strings.Contains(got, "CF_ACCESS_REQUIRE_PASSWORD")) {
			t.Errorf("%s: message %q must name ADMIN_PASSWORD and CF_ACCESS_REQUIRE_PASSWORD", tc.name, got)
		}
	}
}
