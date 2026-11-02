package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type clientsListResponse struct {
	Clients []Client `json:"clients"`
}

func postClient(t *testing.T, mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("POST", "/api/clients", strings.NewReader(body)))
}

func putClient(t *testing.T, mux *http.ServeMux, id, body string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("PUT", "/api/clients/"+id, strings.NewReader(body)))
}

func TestClientsPostThenGet(t *testing.T) {
	mux := newTestMux(t, t.TempDir())

	rec := postClient(t, mux, `{"name":"beta","email":"b@example.com","phone":"555","billingAddress":"1 Main","shippingAddress":"2 Main","notes":"n"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d %s", rec.Code, rec.Body.String())
	}
	var created Client
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created: %v", err)
	}
	if created.ID == "" || created.CreatedAt == "" || created.UpdatedAt == "" {
		t.Fatalf("created client missing server-filled fields: %+v", created)
	}
	if created.Name != "beta" || created.Email != "b@example.com" {
		t.Fatalf("created client fields mismatch: %+v", created)
	}

	postClient(t, mux, `{"name":"Alpha"}`)
	postClient(t, mux, `{"name":"alpha2"}`)

	rec = serveReq(mux, httptest.NewRequest("GET", "/api/clients", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET want 200, got %d %s", rec.Code, rec.Body.String())
	}
	var list clientsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list.Clients) != 3 {
		t.Fatalf("clients len = %d, want 3", len(list.Clients))
	}
	var names []string
	for _, c := range list.Clients {
		names = append(names, c.Name)
	}
	want := []string{"Alpha", "alpha2", "beta"}
	for i, n := range want {
		if names[i] != n {
			t.Fatalf("names = %v, want case-insensitive order %v", names, want)
		}
	}
}

func TestClientsGetEmptyIsEmptySlice(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := serveReq(mux, httptest.NewRequest("GET", "/api/clients", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET want 200, got %d", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"clients":[]}` {
		t.Fatalf("body = %q, want {\"clients\":[]}", got)
	}
}

// TestClientsListPaginationLimit pins ?limit=: capped rows in the existing
// (case-insensitive name) order, plus X-Total-Count naming the full row
// count.
func TestClientsListPaginationLimit(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	postClient(t, mux, `{"name":"Alpha"}`)
	postClient(t, mux, `{"name":"Beta"}`)
	postClient(t, mux, `{"name":"Gamma"}`)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/clients?limit=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("X-Total-Count"), "3"; got != want {
		t.Errorf("X-Total-Count = %q, want %q", got, want)
	}
	var list clientsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list.Clients) != 2 {
		t.Fatalf("clients len = %d, want 2", len(list.Clients))
	}
	if list.Clients[0].Name != "Alpha" || list.Clients[1].Name != "Beta" {
		t.Fatalf("names = [%s %s], want [Alpha Beta] (existing order must be preserved under limit)", list.Clients[0].Name, list.Clients[1].Name)
	}
}

// TestClientsListPaginationOffset pins ?offset=: skips rows in the existing
// order and still sets X-Total-Count.
func TestClientsListPaginationOffset(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	postClient(t, mux, `{"name":"Alpha"}`)
	postClient(t, mux, `{"name":"Beta"}`)
	postClient(t, mux, `{"name":"Gamma"}`)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/clients?offset=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("X-Total-Count"), "3"; got != want {
		t.Errorf("X-Total-Count = %q, want %q", got, want)
	}
	var list clientsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list.Clients) != 1 || list.Clients[0].Name != "Gamma" {
		t.Fatalf("clients = %+v, want [Gamma] (3 total minus the 2 skipped by offset)", list.Clients)
	}
}

// TestClientsListAbsentPaginationParamsReturnsAllUnchanged pins the
// backward-compatibility guarantee: no query params means every row, no
// X-Total-Count header, exactly as before pagination existed.
func TestClientsListAbsentPaginationParamsReturnsAllUnchanged(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	postClient(t, mux, `{"name":"Alpha"}`)
	postClient(t, mux, `{"name":"Beta"}`)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/clients", nil))
	if got := rec.Header().Get("X-Total-Count"); got != "" {
		t.Errorf("X-Total-Count = %q, want unset when pagination isn't used", got)
	}
	var list clientsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list.Clients) != 2 {
		t.Fatalf("clients len = %d, want 2", len(list.Clients))
	}
}

// TestClientsListInvalidPaginationParamsIs400 pins validation of ?limit=/
// ?offset=.
func TestClientsListInvalidPaginationParamsIs400(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	cases := []string{
		"/api/clients?limit=0",
		"/api/clients?limit=abc",
		"/api/clients?limit=501",
		"/api/clients?offset=-1",
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			rec := serveReq(mux, httptest.NewRequest("GET", path, nil))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestClientsPostNameOnly(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postClient(t, mux, `{"name":"Solo"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d %s", rec.Code, rec.Body.String())
	}
	var created Client
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.Email != "" || created.Phone != "" || created.BillingAddress != "" ||
		created.ShippingAddress != "" || created.Notes != "" {
		t.Fatalf("unset fields should default to empty string: %+v", created)
	}
}

func TestClientsPutUpdatesAndBumpsUpdatedAt(t *testing.T) {
	dir := t.TempDir()
	mux := newTestMux(t, dir)

	rec := postClient(t, mux, `{"name":"Original"}`)
	var created Client
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	db := openTestDB(t, dir)
	knownUpdatedAt := "2020-01-01T00:00:00Z"
	if _, err := db.Exec(`UPDATE clients SET updated_at = ? WHERE id = ?`, knownUpdatedAt, created.ID); err != nil {
		t.Fatalf("seed updated_at: %v", err)
	}

	rec = putClient(t, mux, created.ID, `{"name":"Renamed","phone":"999"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT want 200, got %d %s", rec.Code, rec.Body.String())
	}
	var updated Client
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal updated: %v", err)
	}
	if updated.Name != "Renamed" || updated.Phone != "999" {
		t.Fatalf("update did not apply: %+v", updated)
	}
	if updated.UpdatedAt == knownUpdatedAt {
		t.Fatalf("updatedAt was not bumped")
	}
	if updated.CreatedAt != created.CreatedAt {
		t.Fatalf("createdAt changed on update: got %q, want %q", updated.CreatedAt, created.CreatedAt)
	}

	rec = putClient(t, mux, "does-not-exist", `{"name":"X"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PUT unknown id want 404, got %d", rec.Code)
	}
	var errBody apiErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("unmarshal 404 body: %v", err)
	}
	if len(errBody.Errors) != 1 || errBody.Errors[0] != "client not found" {
		t.Fatalf("404 errors = %v, want [client not found]", errBody.Errors)
	}
}

func TestClientsDeleteThenSecondDeleteIs404(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postClient(t, mux, `{"name":"Gone"}`)
	var created Client
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	rec = serveReq(mux, httptest.NewRequest("DELETE", "/api/clients/"+created.ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE want 204, got %d %s", rec.Code, rec.Body.String())
	}

	rec = serveReq(mux, httptest.NewRequest("DELETE", "/api/clients/"+created.ID, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second DELETE want 404, got %d", rec.Code)
	}
}

func TestClientsValidation(t *testing.T) {
	mux := newTestMux(t, t.TempDir())

	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantField  string // substring expected in the errors array (skipped if empty)
	}{
		{"missing name", `{"email":"a@b.com"}`, http.StatusUnprocessableEntity, "name"},
		{"name too long", `{"name":"` + strings.Repeat("x", 201) + `"}`, http.StatusUnprocessableEntity, "name"},
		{"email without @", `{"name":"X","email":"not-an-email"}`, http.StatusUnprocessableEntity, "email"},
		{"unknown field", `{"name":"X","favoriteColor":"blue"}`, http.StatusBadRequest, ""},
		{"array body", `[1,2,3]`, http.StatusBadRequest, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postClient(t, mux, tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantField != "" {
				var errBody apiErrorBody
				if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
					t.Fatalf("unmarshal errors: %v", err)
				}
				found := false
				for _, e := range errBody.Errors {
					if strings.Contains(e, tc.wantField) {
						found = true
					}
				}
				if !found {
					t.Fatalf("errors %v do not mention %q", errBody.Errors, tc.wantField)
				}
			}
		})
	}
}

func TestClientsPostOversizedBodyIs413(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	var buf bytes.Buffer
	buf.WriteString(`{"name":"`)
	buf.WriteString(strings.Repeat("x", clientsMaxBytes+1))
	buf.WriteString(`"}`)

	rec := serveReq(mux, httptest.NewRequest("POST", "/api/clients", bytes.NewReader(buf.Bytes())))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d", rec.Code)
	}
}

// Attribution (identity.go): create stamps createdBy/updatedBy, update moves only updatedBy, as in quotestore_test.go.

func TestClientCreateAndUpdateStampAttribution(t *testing.T) {
	mux, db := newTestMuxWithDB(t, t.TempDir())
	if _, _, err := createUser(db, "flo", "password123", []string{"clients"}, ""); err != nil {
		t.Fatalf("createUser flo: %v", err)
	}
	if _, _, err := createUser(db, "bob", "password123", []string{"clients"}, ""); err != nil {
		t.Fatalf("createUser bob: %v", err)
	}

	req := withCtxIdentity(httptest.NewRequest("POST", "/api/clients", strings.NewReader(`{"name":"Beta Print"}`)), identity{username: "flo"})
	rec := serveReq(mux, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, body %s", rec.Code, rec.Body.String())
	}
	var created Client
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	if created.CreatedBy != "flo" || created.UpdatedBy != "flo" {
		t.Fatalf("createdBy=%q updatedBy=%q, want both %q", created.CreatedBy, created.UpdatedBy, "flo")
	}

	req = withCtxIdentity(httptest.NewRequest("PUT", "/api/clients/"+created.ID, strings.NewReader(`{"name":"Beta Print LLC"}`)), identity{username: "bob"})
	rec = serveReq(mux, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d, body %s", rec.Code, rec.Body.String())
	}
	var updated Client
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal update: %v", err)
	}
	if updated.CreatedBy != "flo" {
		t.Fatalf("update must not touch createdBy: got %q, want %q", updated.CreatedBy, "flo")
	}
	if updated.UpdatedBy != "bob" {
		t.Fatalf("update: updatedBy = %q, want %q", updated.UpdatedBy, "bob")
	}
}

func TestClientCreateWithNoContextIdentityStampsAdmin(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postClient(t, mux, `{"name":"Beta Print"}`)
	var created Client
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.CreatedBy != "admin" || created.UpdatedBy != "admin" {
		t.Fatalf("createdBy=%q updatedBy=%q, want both %q (identityFor's admin fallback)", created.CreatedBy, created.UpdatedBy, "admin")
	}
}
