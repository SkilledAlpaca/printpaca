package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// Per-request identity and the route-wrapping helpers (adminOnly, requirePermission) that enforce it,
// applied explicitly at every registerRoutes call site so each route's permission is greppable.

// identity is who a request acts as: the env admin (username always "admin") or a DB-backed employee
// (lowercased username). reservedUsernames bars an employee named "admin", which would make
// attribution ambiguous and let killSessionsForUsername kill the env-admin's session.
type identity struct {
	username string
	isAdmin  bool
}

// adminIdentity is the fallback when no session identity exists: auth disabled (single-user LAN mode)
// or a handler called directly in a test. Centralizing it in identityFor keeps adminOnly and
// requirePermission free of an "auth disabled" branch, since admin satisfies both.
func adminIdentity() identity {
	return identity{username: "admin", isAdmin: true}
}

// identityCtxKey is the context key auth.middleware stores the session identity under.
type identityCtxKey struct{}

// withIdentity returns ctx carrying id.
func withIdentity(ctx context.Context, id identity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// identityFor returns the identity auth.middleware attached, or adminIdentity() if none. Handlers use
// it instead of reading the context key, so "no identity" is decided in one place.
func identityFor(r *http.Request) identity {
	if id, ok := r.Context().Value(identityCtxKey{}).(identity); ok {
		return id
	}
	return adminIdentity()
}

// accessSignInCtxKey marks a Cloudflare Access-authenticated request; the value is its email ("" if none).
type accessSignInCtxKey struct{}

// withAccessSignIn returns ctx marked as signed in through Access as email.
func withAccessSignIn(ctx context.Context, email string) context.Context {
	return context.WithValue(ctx, accessSignInCtxKey{}, email)
}

// accessSignIn reports whether r was authenticated by its Access token (not a session) and its email.
func accessSignIn(r *http.Request) (email string, ok bool) {
	email, ok = r.Context().Value(accessSignInCtxKey{}).(string)
	return email, ok
}

// validPermissionKeys are the grantable tab keys. An unrecognized key is always a validation error,
// never a silently ignored grant.
var validPermissionKeys = map[string]bool{
	"quote":       true,
	"quotes":      true,
	"requests":    true,
	"invoices":    true,
	"items":       true,
	"clients":     true,
	"dashboard":   true,
	"filament":    true,
	"electricity": true,
	"printers":    true,
}

// hasPermission reports whether perms contains key.
func hasPermission(perms []string, key string) bool {
	return slices.Contains(perms, key)
}

// adminOnly wraps next so only the admin identity may call it; anyone else gets 403. It is the
// registerRoutes default for any route the permission table doesn't name (default-deny), and covers
// the hard admin-only families: Settings PUT, user management, integrations, log and data export.
func adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !identityFor(r).isAdmin {
			writeAuthError(w, http.StatusForbidden, "admin access required")
			return
		}
		next(w, r)
	}
}

// requirePermission wraps next so only admin or an employee granted key may call it. The grant is
// read from db on every request so an admin edit applies immediately. A lookup error fails closed
// with 500; a missing or disabled user means "no permissions" (403).
func requirePermission(db *sql.DB, key string, next http.HandlerFunc) http.HandlerFunc {
	if !validPermissionKeys[key] {
		// Keys are compile-time literals; a bad one is a bug, so panic at registration, not silently deny or allow.
		panic(fmt.Sprintf("requirePermission: %q is not a valid permission key", key))
	}
	return func(w http.ResponseWriter, r *http.Request) {
		id := identityFor(r)
		if id.isAdmin {
			next(w, r)
			return
		}
		perms, err := userPermissions(db, id.username)
		if err != nil {
			http.Error(w, "permission check failed", http.StatusInternalServerError)
			return
		}
		if !hasPermission(perms, key) {
			writeAuthError(w, http.StatusForbidden, fmt.Sprintf("missing permission: %s", key))
			return
		}
		next(w, r)
	}
}

// requireAnyPermission is the OR counterpart to requirePermission: admin or an employee holding at
// least one of keys. Its one use is GET /api/settings for filament/electricity/printers, whose
// embedded SettingsEditor can't load without it; the read exposes nothing GET /api/config doesn't.
// PUT scoping is unchanged (settingsScopedProblem). Same fail-closed and panic posture as requirePermission.
func requireAnyPermission(db *sql.DB, keys []string, next http.HandlerFunc) http.HandlerFunc {
	for _, key := range keys {
		if !validPermissionKeys[key] {
			panic(fmt.Sprintf("requireAnyPermission: %q is not a valid permission key", key))
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		id := identityFor(r)
		if id.isAdmin {
			next(w, r)
			return
		}
		perms, err := userPermissions(db, id.username)
		if err != nil {
			http.Error(w, "permission check failed", http.StatusInternalServerError)
			return
		}
		for _, key := range keys {
			if hasPermission(perms, key) {
				next(w, r)
				return
			}
		}
		writeAuthError(w, http.StatusForbidden, fmt.Sprintf("missing permission: one of %s", strings.Join(keys, ", ")))
	}
}
