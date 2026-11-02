package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
)

// openTestDB opens a fresh database in dir; it is closed at test cleanup.
func openTestDB(t *testing.T, dir string) *sql.DB {
	t.Helper()
	db, err := openDB(dir)
	if err != nil {
		t.Fatalf("openDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// loadTestConfig loads the config in dir (the defaults when there is no config.yaml).
func loadTestConfig(t *testing.T, dir string) *Config {
	t.Helper()
	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	return cfg
}

// openTestEnv opens a database and loads the config in dir, wrapped in a ConfigStore.
func openTestEnv(t *testing.T, dir string) (*sql.DB, *Config, *ConfigStore) {
	t.Helper()
	db := openTestDB(t, dir)
	cfg := loadTestConfig(t, dir)
	return db, cfg, NewConfigStore(cfg)
}

// routesMux wires the full admin route table as main.go does.
func routesMux(db *sql.DB, store *ConfigStore, auth *authState, dir string, demoMode bool) *http.ServeMux {
	mux := http.NewServeMux()
	registerRoutes(mux, db, store, auth, dir, demoMode)
	return mux
}

// newTestMux wires the route table (auth disabled) over a fresh database in dir.
func newTestMux(t *testing.T, dir string) *http.ServeMux {
	t.Helper()
	mux, _ := newTestMuxWithDB(t, dir)
	return mux
}

// newTestMuxWithDB is newTestMux plus the *sql.DB, for tests that seed rows directly.
func newTestMuxWithDB(t *testing.T, dir string) (*http.ServeMux, *sql.DB) {
	t.Helper()
	db, _, store := openTestEnv(t, dir)
	return routesMux(db, store, newAuthState("", nil), dir, false), db
}

// serveReq runs req through h and returns the recorded response.
func serveReq(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// openRawSQLite opens path with the sqlite driver and no printpaca schema or pragmas, for building databases
// openDB must refuse.
func openRawSQLite(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+path)
}
