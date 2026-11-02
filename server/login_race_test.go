package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countCompares installs loginCompareHook for the test and returns the live counter.
func countCompares(t *testing.T) *atomic.Int64 {
	t.Helper()
	var n atomic.Int64
	loginCompareHook = func() { n.Add(1) }
	t.Cleanup(func() { loginCompareHook = nil })
	return &n
}

// N parallel bad logins from one IP must start at most authIPLoginRateLimit compares immediately; the rest
// are delayed by the throttle or, once its slots are full, answered 429. Before the atomic reservation every
// parallel attempt saw an unexhausted budget and compared at once.
func TestLoginParallelFloodIsBoundedByBudget(t *testing.T) {
	waitForSemLevel(t, 0)
	auth := newAuthState("secret", nil)
	wrapped := auth.middleware(loginMux(auth))
	compares := countCompares(t)

	const n = 60
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var wg sync.WaitGroup
	var tooMany atomic.Int64
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"password":"wrong"}`)).WithContext(ctx)
			req.Host = "localhost"
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = "198.51.100.77:1234"
			wrapped.ServeHTTP(rec, req)
			if rec.Code == http.StatusTooManyRequests {
				tooMany.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := compares.Load(); got > authIPLoginRateLimit {
		t.Errorf("compares started within the throttle delay = %d, want <= %d", got, authIPLoginRateLimit)
	}
	if want := int64(n - authIPLoginRateLimit - loginThrottleMaxInFlight); tooMany.Load() < want {
		t.Errorf("429 responses = %d, want >= %d", tooMany.Load(), want)
	}
}

// Correct logins refund their reserved slot, so any number of them from one IP stay undelayed.
func TestLoginSuccessRefundsReservation(t *testing.T) {
	auth := newAuthState("secret", nil)
	wrapped := auth.middleware(loginMux(auth))
	for i := range authIPLoginRateLimit * 3 {
		start := time.Now()
		rec := doLoginFrom(t, wrapped, "secret", "198.51.100.88:1234")
		if rec.Code != http.StatusOK || time.Since(start) >= authLoginThrottleMin {
			t.Fatalf("login %d: status %d after %v, want fast 200", i, rec.Code, time.Since(start))
		}
	}
}

// With multi-user mode off a username login is rejected without running bcrypt.
func TestLoginUsernameModeOffSkipsBcrypt(t *testing.T) {
	auth := newAuthState("secret", nil)
	wrapped := auth.middleware(loginMux(auth))
	compares := countCompares(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/login", strings.NewReader(`{"username":"bob","password":"whatever"}`))
	req.Host = "localhost"
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "198.51.100.99:1234"
	wrapped.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if compares.Load() != 0 {
		t.Errorf("compares = %d, want 0", compares.Load())
	}
}

// A full bcrypt semaphore answers an employee login with an immediate 429.
func TestLoginBcryptSemaphoreFullIs429(t *testing.T) {
	handler, auth, _ := newMultiUserTestMux(t, false)
	if _, _, err := createUser(auth.db, "alice", "password123", []string{"quotes"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}
	for range bcryptMaxInFlight {
		bcryptSem <- struct{}{}
	}
	defer func() {
		for range bcryptMaxInFlight {
			<-bcryptSem
		}
	}()
	start := time.Now()
	rec := doJSON(t, handler, "POST", "/api/login", nil, map[string]string{"username": "alice", "password": "password123"})
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429; body %s", rec.Code, rec.Body.String())
	}
	if time.Since(start) > time.Second {
		t.Errorf("429 took %v, want immediate", time.Since(start))
	}
}
