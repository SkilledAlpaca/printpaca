package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// This file implements the users table: employee accounts, hashes, and per-tab grants.
// The admin identity is never a row here; it is the env ADMIN_PASSWORD, resolved in auth.go.

// bcryptCost applies to every password printpaca stores (ADMIN_PASSWORD is never hashed). 12 makes offline
// brute force costly yet stays well under 1s. Don't lower it to cut login latency: throttling
// (authLoginRateLimit/loginThrottleMu), not hash speed, defends against online guessing.
// It is a var only so tests can lower it (see testmain_test.go).
var bcryptCost = 12

// dummyBcryptHash is compared on every login for a username that doesn't exist or is disabled
// (checkUserLogin), so timing can't enumerate usernames.
// Computed once from a fixed non-secret input; it must never become a real password's hash.
var dummyBcryptHash = mustDummyBcryptHash()

func mustDummyBcryptHash() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("printpaca-timing-safety-dummy"), bcryptCost)
	if err != nil {
		// crypto/rand failing at start is unrecoverable; same posture as newSessionToken.
		panic(fmt.Sprintf("mustDummyBcryptHash: %v", err))
	}
	return h
}

// usernamePattern is 1-64 lowercase ASCII letters, digits, dot, underscore, or hyphen.
// users.username is COLLATE NOCASE, so requiring lowercase leaves one canonical spelling on disk and no
// normalization step that could disagree with the collation.
var usernamePattern = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)

// reservedUsernames lists usernames createUser must refuse. "admin" is the env admin's stamped username; an
// employee named "admin" would let killSessionsForUsername (no isAdmin check) kill the env admin's session
// and make created_by/updated_by ambiguous. Only new writes are checked; an existing row is left alone.
var reservedUsernames = map[string]bool{"admin": true}

// minPasswordLength matches minAdminPasswordLength: employee passwords get the same floor as ADMIN_PASSWORD.
const minPasswordLength = minAdminPasswordLength

// usersMaxBytes bounds every users/me body: small JSON only, so tighter than clients.go's 64 KiB.
const usersMaxBytes = 8 * 1024

// User is the JSON shape of every /api/users and /api/me response; it never includes password_hash.
type User struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	Permissions []string `json:"permissions"`
	// Email is the lowercased address the user signs in to Cloudflare Access with; empty when unset.
	Email     string `json:"email"`
	Disabled  bool   `json:"disabled"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// userRow is User plus the password hash, for callers that verify a password (login, self-service change).
type userRow struct {
	User
	PasswordHash string
}

// normalizePermissions trims, validates, deduplicates, and sorts perms.
// Returns the cleaned list (never nil, so it round-trips as []) and one message per unrecognized key.
func normalizePermissions(perms []string) ([]string, []string) {
	seen := map[string]bool{}
	out := []string{}
	var problems []string
	for _, p := range perms {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !validPermissionKeys[p] {
			problems = append(problems, fmt.Sprintf("permissions: %q is not a valid tab key", p))
			continue
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	sort.Strings(out)
	return out, problems
}

// encodePermissions/decodePermissions round-trip the permissions column's JSON array of strings.
func encodePermissions(perms []string) (string, error) {
	if perms == nil {
		perms = []string{}
	}
	b, err := json.Marshal(perms)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodePermissions(raw string) ([]string, error) {
	var perms []string
	if err := json.Unmarshal([]byte(raw), &perms); err != nil {
		return nil, err
	}
	if perms == nil {
		perms = []string{}
	}
	return perms, nil
}

// scanUserRow scans one users row (the column order every query below uses) into a userRow.
func scanUserRow(scan func(dest ...any) error) (userRow, error) {
	var u userRow
	var disabled int
	var permsRaw string
	if err := scan(&u.ID, &u.Username, &u.PasswordHash, &permsRaw, &disabled, &u.CreatedAt, &u.UpdatedAt, &u.Email); err != nil {
		return userRow{}, err
	}
	perms, err := decodePermissions(permsRaw)
	if err != nil {
		return userRow{}, fmt.Errorf("decode permissions for user %s: %w", u.ID, err)
	}
	u.Permissions = perms
	u.Disabled = disabled != 0
	return u, nil
}

const userSelectColumns = `id, username, password_hash, permissions, disabled, created_at, updated_at, email`

// getUserByUsername looks up a user case-insensitively (COLLATE NOCASE); ok is false, nil error, if none.
func getUserByUsername(db *sql.DB, username string) (userRow, bool, error) {
	row := db.QueryRow(`SELECT `+userSelectColumns+` FROM users WHERE username = ?`, username)
	u, err := scanUserRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return userRow{}, false, nil
	}
	if err != nil {
		return userRow{}, false, err
	}
	return u, true, nil
}

// getUserByEmail looks up a user by Access email, case-insensitively against the stored lowercased value.
// ok is false, with a nil error, for an empty email or no match.
func getUserByEmail(db *sql.DB, email string) (userRow, bool, error) {
	email = foldEmail(email)
	if email == "" {
		return userRow{}, false, nil
	}
	row := db.QueryRow(`SELECT `+userSelectColumns+` FROM users WHERE lower(email) = ?`, email)
	u, err := scanUserRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return userRow{}, false, nil
	}
	if err != nil {
		return userRow{}, false, err
	}
	return u, true, nil
}

// getUserByID is getUserByUsername keyed on the primary key (every /api/users/{id} route).
func getUserByID(db *sql.DB, id string) (userRow, bool, error) {
	row := db.QueryRow(`SELECT `+userSelectColumns+` FROM users WHERE id = ?`, id)
	u, err := scanUserRow(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return userRow{}, false, nil
	}
	if err != nil {
		return userRow{}, false, err
	}
	return u, true, nil
}

// userPermissions returns username's granted tab keys.
// An empty, unknown, or disabled user resolves to an empty slice, not an error: every caller denies alike.
// A real lookup or decode failure is still an error.
func userPermissions(db *sql.DB, username string) ([]string, error) {
	u, ok, err := getUserByUsername(db, username)
	if err != nil {
		return nil, err
	}
	if !ok || u.Disabled {
		return []string{}, nil
	}
	return u.Permissions, nil
}

// listUsers returns every employee ordered by username, as a non-nil slice.
func listUsers(db *sql.DB) ([]User, error) {
	rows, err := db.Query(`SELECT ` + userSelectColumns + ` FROM users ORDER BY username COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []User{}
	for rows.Next() {
		u, err := scanUserRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, u.User)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// errEmailTaken is returned when the normalized email belongs to another user.
var errEmailTaken = errors.New("email already in use")

// accessEmailPattern is a plausibility check (x@y, no whitespace); real validation is Cloudflare's.
var accessEmailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+$`)

// maxEmailLength is the RFC 5321 limit on a mailbox address.
const maxEmailLength = 254

// foldEmail trims whitespace and lowercases ASCII letters only; the token claim, stored emails, and
// users.accessAdminEmail all use it, so they compare identically. Full Unicode lowering is avoided: it maps
// characters such as the Kelvin sign (U+212A) onto ASCII letters, letting another address collide.
func foldEmail(s string) string {
	s = strings.TrimSpace(s)
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// plainEmailBytes reports whether s is printable ASCII only: no non-ASCII, control character, or whitespace.
func plainEmailBytes(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c <= 0x20 || c >= 0x7f {
			return false
		}
	}
	return true
}

// normalizeEmail folds raw and returns a problem message ("" when valid). Empty is valid and means no email.
func normalizeEmail(field, raw string) (string, string) {
	e := foldEmail(raw)
	if e == "" {
		return "", ""
	}
	if len(e) > maxEmailLength || !plainEmailBytes(e) || !accessEmailPattern.MatchString(e) {
		return "", fmt.Sprintf("%s: must be an ASCII email address (name@example.com) without spaces, at most %d characters", field, maxEmailLength)
	}
	return e, ""
}

// accessAdminEmailCollisionMessage is the error for a user email equal to users.accessAdminEmail (and the
// reverse in settings validation): the admin mapping would silently win.
func accessAdminEmailCollisionMessage(email string) string {
	return fmt.Sprintf("email %q is the Access admin email (users.accessAdminEmail); an employee cannot share it", email)
}

// createUser inserts a new employee row; the caller has validated the username and password length.
// usernameTaken is true when the case-insensitive username already exists (reported as 409). email is the
// optional, normalized Access email ("" for none); a duplicate is errEmailTaken.
func createUser(db *sql.DB, username, password string, permissions []string, email string) (u User, usernameTaken bool, err error) {
	if _, exists, err := getUserByUsername(db, username); err != nil {
		return User{}, false, err
	} else if exists {
		return User{}, true, nil
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return User{}, false, err
	}
	permsJSON, err := encodePermissions(permissions)
	if err != nil {
		return User{}, false, err
	}
	if email != "" {
		if _, taken, err := getUserByEmail(db, email); err != nil {
			return User{}, false, err
		} else if taken {
			return User{}, false, errEmailTaken
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	u = User{
		ID:          newID(),
		Username:    username,
		Permissions: permissions,
		Email:       email,
		Disabled:    false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	_, err = db.Exec(`
		INSERT INTO users (id, username, password_hash, permissions, disabled, created_at, updated_at, email)
		VALUES (?, ?, ?, ?, 0, ?, ?, ?)`,
		u.ID, u.Username, string(hash), permsJSON, u.CreatedAt, u.UpdatedAt, email)
	if err != nil {
		// A UNIQUE race between concurrent creates is the one failure the pre-check can miss; report it the
		// same way, not as a 500.
		if msg := strings.ToLower(err.Error()); strings.Contains(msg, "unique") {
			if strings.Contains(msg, "email") {
				return User{}, false, errEmailTaken
			}
			return User{}, true, nil
		}
		return User{}, false, err
	}
	return u, false, nil
}

// updateUserFields applies PUT /api/users/{id}: permissions, disabled, and email only when non-nil.
// A non-nil email ("" clears it) replaces the stored one; a duplicate returns errEmailTaken. found is
// false when id doesn't exist.
func updateUserFields(db *sql.DB, id string, permissions *[]string, disabled *bool, email *string) (u User, found bool, err error) {
	existing, ok, err := getUserByID(db, id)
	if err != nil || !ok {
		return User{}, ok, err
	}

	newPerms := existing.Permissions
	if permissions != nil {
		newPerms = *permissions
	}
	newDisabled := existing.Disabled
	if disabled != nil {
		newDisabled = *disabled
	}
	newEmail := existing.Email
	if email != nil {
		newEmail = *email
		if newEmail != "" {
			if other, taken, err := getUserByEmail(db, newEmail); err != nil {
				return User{}, true, err
			} else if taken && other.ID != id {
				return User{}, true, errEmailTaken
			}
		}
	}
	permsJSON, err := encodePermissions(newPerms)
	if err != nil {
		return User{}, true, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE users SET permissions = ?, disabled = ?, email = ?, updated_at = ? WHERE id = ?`,
		permsJSON, boolToInt(newDisabled), newEmail, now, id); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return User{}, true, errEmailTaken
		}
		return User{}, true, err
	}

	updated, ok, err := getUserByID(db, id)
	if err != nil || !ok {
		return User{}, ok, err
	}
	return updated.User, true, nil
}

// setUserPasswordByID bcrypt-hashes newPassword (length-checked by the caller) and stores it.
// found is false when id doesn't exist; username lets the caller kill that user's sessions.
func setUserPasswordByID(db *sql.DB, id, newPassword string) (username string, found bool, err error) {
	existing, ok, err := getUserByID(db, id)
	if err != nil || !ok {
		return "", ok, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcryptCost)
	if err != nil {
		return "", true, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`, string(hash), now, id); err != nil {
		return "", true, err
	}
	return existing.Username, true, nil
}

// deleteUser hard-deletes id, returning the username (for session-kill) and whether it existed.
func deleteUser(db *sql.DB, id string) (username string, found bool, err error) {
	existing, ok, err := getUserByID(db, id)
	if err != nil || !ok {
		return "", ok, err
	}
	if _, err := db.Exec(`DELETE FROM users WHERE id = ?`, id); err != nil {
		return "", true, err
	}
	return existing.Username, true, nil
}

// ---------------------------------------------------------------------------
// HTTP handlers: /api/users (admin-only via adminOnly) and /api/me (any authenticated user).
// ---------------------------------------------------------------------------

// usersAPI groups the database and session-killing dependencies the handlers need.
type usersAPI struct {
	db   *sql.DB
	auth *authState
}

func newUsersAPI(db *sql.DB, auth *authState) *usersAPI {
	return &usersAPI{db: db, auth: auth}
}

// multiUserModeOffMessage is the 409 body of /api/users* while users.multiUser is off.
const multiUserModeOffMessage = "multi-user mode is off"

// requireMultiUser reports whether users.multiUser is on, writing the 409 itself when it isn't.
func (ua *usersAPI) requireMultiUser(w http.ResponseWriter, cfg *Config) bool {
	if !cfg.Users.MultiUser {
		writeAPIError(w, http.StatusConflict, multiUserModeOffMessage)
		return false
	}
	return true
}

func (ua *usersAPI) handleList(w http.ResponseWriter, r *http.Request, cfg *Config) {
	if !ua.requireMultiUser(w, cfg) {
		return
	}
	users, err := listUsers(ua.db)
	if err != nil {
		log.Printf("users list: %v", err)
		http.Error(w, "users unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Users []User `json:"users"`
	}{Users: users})
}

// createUserRequest is POST /api/users' body.
type createUserRequest struct {
	Username    string   `json:"username"`
	Password    string   `json:"password"`
	Permissions []string `json:"permissions"`
	Email       string   `json:"email"`
}

func (ua *usersAPI) handlePost(w http.ResponseWriter, r *http.Request, cfg *Config) {
	if !ua.requireMultiUser(w, cfg) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, usersMaxBytes)
	var in createUserRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request body too large")
			return
		}
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	var problems []string
	username := strings.TrimSpace(in.Username)
	if !usernamePattern.MatchString(username) {
		problems = append(problems, "username: must be 1-64 characters, lowercase letters/digits/./_/- only")
	} else if reservedUsernames[username] {
		problems = append(problems, "username: reserved")
	}
	if len(in.Password) < minPasswordLength {
		problems = append(problems, fmt.Sprintf("password: must be at least %d characters", minPasswordLength))
	}
	perms, permProblems := normalizePermissions(in.Permissions)
	problems = append(problems, permProblems...)
	email, emailProblem := normalizeEmail("email", in.Email)
	if emailProblem != "" {
		problems = append(problems, emailProblem)
	}
	if len(problems) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, problems...)
		return
	}
	if email != "" && email == cfg.Users.AccessAdminEmail {
		writeAPIError(w, http.StatusConflict, accessAdminEmailCollisionMessage(email))
		return
	}

	u, taken, err := createUser(ua.db, username, in.Password, perms, email)
	if errors.Is(err, errEmailTaken) {
		writeAPIError(w, http.StatusConflict, fmt.Sprintf("email %q is already used by another user", email))
		return
	}
	if err != nil {
		log.Printf("users create: %v", err)
		http.Error(w, "users not writable", http.StatusInternalServerError)
		return
	}
	if taken {
		writeAPIError(w, http.StatusConflict, fmt.Sprintf("username %q already exists", username))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(u)
}

// updateUserRequest is PUT /api/users/{id}'s body: both fields optional; username is immutable.
type updateUserRequest struct {
	Permissions *[]string `json:"permissions"`
	Disabled    *bool     `json:"disabled"`
	// Email replaces the user's Access email when present; "" clears it.
	Email *string `json:"email"`
}

func (ua *usersAPI) handlePut(w http.ResponseWriter, r *http.Request, cfg *Config) {
	if !ua.requireMultiUser(w, cfg) {
		return
	}
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, usersMaxBytes)
	var in updateUserRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}

	var perms *[]string
	if in.Permissions != nil {
		cleaned, problems := normalizePermissions(*in.Permissions)
		if len(problems) > 0 {
			writeAPIError(w, http.StatusUnprocessableEntity, problems...)
			return
		}
		perms = &cleaned
	}

	var email *string
	if in.Email != nil {
		e, problem := normalizeEmail("email", *in.Email)
		if problem != "" {
			writeAPIError(w, http.StatusUnprocessableEntity, problem)
			return
		}
		if e != "" && e == cfg.Users.AccessAdminEmail {
			writeAPIError(w, http.StatusConflict, accessAdminEmailCollisionMessage(e))
			return
		}
		email = &e
	}

	u, found, err := updateUserFields(ua.db, id, perms, in.Disabled, email)
	if errors.Is(err, errEmailTaken) {
		writeAPIError(w, http.StatusConflict, fmt.Sprintf("email %q is already used by another user", *email))
		return
	}
	if err != nil {
		log.Printf("users update: %v", err)
		http.Error(w, "users not writable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "user not found")
		return
	}

	// Disabling kills the user's live sessions immediately; killing an empty set is harmless.
	if in.Disabled != nil && *in.Disabled {
		ua.auth.killSessionsForUsername(u.Username, "")
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(u)
}

// resetPasswordRequest is POST /api/users/{id}/password's body (admin reset; meAPI has the self-service one).
type resetPasswordRequest struct {
	New string `json:"new"`
}

func (ua *usersAPI) handlePassword(w http.ResponseWriter, r *http.Request, cfg *Config) {
	if !ua.requireMultiUser(w, cfg) {
		return
	}
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, usersMaxBytes)
	var in resetPasswordRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(in.New) < minPasswordLength {
		writeAPIError(w, http.StatusUnprocessableEntity, fmt.Sprintf("new password: must be at least %d characters", minPasswordLength))
		return
	}

	username, found, err := setUserPasswordByID(ua.db, id, in.New)
	if err != nil {
		log.Printf("users password reset: %v", err)
		http.Error(w, "users not writable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "user not found")
		return
	}
	// An admin reset kills all of that user's sessions: unlike a self-service change, no session is kept.
	ua.auth.killSessionsForUsername(username, "")

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Ok bool `json:"ok"`
	}{Ok: true})
}

func (ua *usersAPI) handleDelete(w http.ResponseWriter, r *http.Request, cfg *Config) {
	if !ua.requireMultiUser(w, cfg) {
		return
	}
	id := r.PathValue("id")
	username, found, err := deleteUser(ua.db, id)
	if err != nil {
		log.Printf("users delete: %v", err)
		http.Error(w, "users not writable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "user not found")
		return
	}
	ua.auth.killSessionsForUsername(username, "")
	w.WriteHeader(http.StatusNoContent)
}

// GET /api/me, POST /api/me/password: any authenticated user, no adminOnly/requirePermission wrapper, and not
// gated on users.multiUser, since the env admin must always be able to see itself.

type meAPI struct {
	db   *sql.DB
	auth *authState
}

func newMeAPI(db *sql.DB, auth *authState) *meAPI {
	return &meAPI{db: db, auth: auth}
}

// meResponse is GET /api/me's body and /api/config's "user" extra: the same {username, isAdmin, permissions},
// so the SPA needs no second round trip.
type meResponse struct {
	Username    string   `json:"username"`
	IsAdmin     bool     `json:"isAdmin"`
	Permissions []string `json:"permissions"`
	// SignedInVia is "password" (a session), "access" (a verified Access token), or "none" (auth off); the
	// SPA uses it to pick the sign-out target and Account card layout.
	SignedInVia string `json:"signedInVia"`
	// AccessEmail is the token's email when SignedInVia is "access".
	AccessEmail string `json:"accessEmail,omitempty"`
}

// currentUser builds id's meResponse: the admin reports empty permissions (isAdmin implies full access);
// an employee's are read live (userPermissions), as requirePermission does per request.
func currentUser(db *sql.DB, id identity) (meResponse, error) {
	if id.isAdmin {
		return meResponse{Username: id.username, IsAdmin: true, Permissions: []string{}}, nil
	}
	perms, err := userPermissions(db, id.username)
	if err != nil {
		return meResponse{}, err
	}
	return meResponse{Username: id.username, IsAdmin: false, Permissions: perms}, nil
}

// signedInVia reports how r was authenticated: "access" with the token's email, "password", or "none".
func (a *authState) signedInVia(r *http.Request) (via, accessEmail string) {
	if email, ok := accessSignIn(r); ok {
		return "access", email
	}
	if a.enabled {
		return "password", ""
	}
	return "none", ""
}

func (ma *meAPI) handleGet(w http.ResponseWriter, r *http.Request) {
	resp, err := currentUser(ma.db, identityFor(r))
	if err != nil {
		log.Printf("me: %v", err)
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}
	resp.SignedInVia, resp.AccessEmail = ma.auth.signedInVia(r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// selfPasswordRequest is POST /api/me/password body {current, new}; the old one must be proven.
type selfPasswordRequest struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

// handlePassword implements POST /api/me/password. The env admin gets 400: its password is env-only,
// and there is no users row.
func (ma *meAPI) handlePassword(w http.ResponseWriter, r *http.Request) {
	id := identityFor(r)
	if id.isAdmin {
		writeAPIError(w, http.StatusBadRequest, "the admin password is set by ADMIN_PASSWORD and can't be changed here")
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, usersMaxBytes)
	var in selfPasswordRequest
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(in.New) < minPasswordLength {
		writeAPIError(w, http.StatusUnprocessableEntity, fmt.Sprintf("new password: must be at least %d characters", minPasswordLength))
		return
	}

	existing, ok, err := getUserByUsername(ma.db, id.username)
	if err != nil {
		log.Printf("me password: lookup: %v", err)
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}
	if !ok || existing.Disabled {
		// The session should have died when the account was disabled or deleted; answer unauthorized, not a
		// 404/500 that would leak which case it was.
		writeAuthError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(existing.PasswordHash), []byte(in.Current)) != nil {
		writeAPIError(w, http.StatusUnprocessableEntity, "current password is incorrect")
		return
	}

	if _, _, err := setUserPasswordByID(ma.db, existing.ID, in.New); err != nil {
		log.Printf("me password: update: %v", err)
		http.Error(w, "unavailable", http.StatusInternalServerError)
		return
	}

	// Keep the session that just proved the current password; kill every other one.
	var currentToken string
	if c, err := r.Cookie(authCookieName); err == nil {
		currentToken = c.Value
	}
	ma.auth.killSessionsForUsername(existing.Username, currentToken)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Ok bool `json:"ok"`
	}{Ok: true})
}
