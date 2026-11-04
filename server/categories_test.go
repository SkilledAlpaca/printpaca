package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func postCategory(t *testing.T, mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	t.Helper()
	return serveReq(mux, httptest.NewRequest("POST", "/api/categories", strings.NewReader(body)))
}

func putCategory(t *testing.T, mux *http.ServeMux, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	return serveReq(mux, httptest.NewRequest("PUT", "/api/categories/"+id, strings.NewReader(body)))
}

func mustCategory(t *testing.T, mux *http.ServeMux, body string) Category {
	t.Helper()
	rec := postCategory(t, mux, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST category %s = %d %s", body, rec.Code, rec.Body.String())
	}
	var c Category
	if err := json.Unmarshal(rec.Body.Bytes(), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

func mustItem(t *testing.T, mux *http.ServeMux, body string) Item {
	t.Helper()
	rec := postItem(t, mux, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST item %s = %d %s", body, rec.Code, rec.Body.String())
	}
	var it Item
	if err := json.Unmarshal(rec.Body.Bytes(), &it); err != nil {
		t.Fatal(err)
	}
	return it
}

func listAdminCategories(t *testing.T, mux *http.ServeMux) []Category {
	t.Helper()
	rec := serveReq(mux, httptest.NewRequest("GET", "/api/categories", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/categories = %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Categories []Category `json:"categories"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Categories
}

// ---------------------------------------------------------------------------
// Migration 5 -> 6
// ---------------------------------------------------------------------------

// seedV11Items builds a database at user_version 5 (the v1.1.0 schema) with two items, one of them bumped.
func seedV11Items(t *testing.T, dir string) {
	t.Helper()
	raw, err := openRawSQLite(filepath.Join(dir, "printpaca.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	defer raw.Close()
	for i := 0; i < 5; i++ {
		if err := runMigration(raw, i); err != nil {
			t.Fatalf("migration %d: %v", i, err)
		}
	}
	for _, s := range []string{
		`INSERT INTO items (id, name, price_cents, public, sort_order, created_at, updated_at) VALUES ('a', 'Alpha', 500, 1, 1, 'x', 'x'), ('b', 'Beta', 700, 0, 2, 'x', 'x')`,
		`UPDATE items SET description = 'bumped' WHERE id = 'b'`,
	} {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("seed: %v\n%s", err, s)
		}
	}
	var v int
	raw.QueryRow("PRAGMA user_version").Scan(&v)
	if v != 5 {
		t.Fatalf("seed user_version = %d, want 5", v)
	}
}

func TestMigrationAddsCategoriesAndFeatured(t *testing.T) {
	dir := t.TempDir()
	seedV11Items(t, dir)
	db := openTestDB(t, dir)

	var version int
	db.QueryRow("PRAGMA user_version").Scan(&version)
	if version != len(migrations) || len(migrations) < 6 {
		t.Fatalf("user_version = %d, migrations = %d", version, len(migrations))
	}
	// Existing items keep every value: no category, not featured, version untouched by the migration.
	for id, wantVersion := range map[string]int{"a": 1, "b": 2} {
		var cat sql.NullString
		var featured, itemVersion int
		var name string
		if err := db.QueryRow(`SELECT name, category_id, featured, version FROM items WHERE id = ?`, id).Scan(&name, &cat, &featured, &itemVersion); err != nil {
			t.Fatal(err)
		}
		if cat.Valid || featured != 0 || itemVersion != wantVersion {
			t.Errorf("item %s: category %v, featured %d, version %d (want NULL, 0, %d)", id, cat, featured, itemVersion, wantVersion)
		}
	}
	// The new table works and enforces its constraints.
	if _, err := db.Exec(`INSERT INTO categories (id, name, slug, created_at, updated_at) VALUES ('c1', 'Toys', 'toys', 'x', 'x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO categories (id, name, slug, created_at, updated_at) VALUES ('c2', 'Other', 'toys', 'x', 'x')`); err == nil {
		t.Error("duplicate slug accepted")
	}
	if _, err := db.Exec(`INSERT INTO categories (id, name, slug, hidden, created_at, updated_at) VALUES ('c3', 'X', 'x', 2, 'x', 'x')`); err == nil {
		t.Error("hidden = 2 accepted")
	}
	if _, err := db.Exec(`UPDATE items SET category_id = 'nope' WHERE id = 'a'`); err == nil {
		t.Error("item linked to a missing category")
	}
	// The foreign key nulls the link even for a delete that bypasses the handler.
	db.Exec(`UPDATE items SET category_id = 'c1', featured = 1 WHERE id = 'a'`)
	if _, err := db.Exec(`DELETE FROM categories WHERE id = 'c1'`); err != nil {
		t.Fatal(err)
	}
	var cat sql.NullString
	db.QueryRow(`SELECT category_id FROM items WHERE id = 'a'`).Scan(&cat)
	if cat.Valid {
		t.Errorf("category_id after delete = %v, want NULL", cat)
	}
}

// ---------------------------------------------------------------------------
// Category CRUD and slug rules
// ---------------------------------------------------------------------------

func TestCategoriesCRUD(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	if got := listAdminCategories(t, mux); got == nil || len(got) != 0 {
		t.Fatalf("fresh list = %#v, want []", got)
	}
	toys := mustCategory(t, mux, `{"name":"  Toys & Games  ","sortOrder":2}`)
	if toys.Name != "Toys & Games" || toys.Slug != "toys-games" || toys.SortOrder != 2 || toys.Hidden || toys.ItemCount != 0 || toys.ID == "" {
		t.Fatalf("created = %+v", toys)
	}
	tools := mustCategory(t, mux, `{"name":"Tools","slug":"workshop","sortOrder":1,"hidden":true}`)
	if tools.Slug != "workshop" || !tools.Hidden {
		t.Fatalf("created with slug = %+v", tools)
	}
	if list := listAdminCategories(t, mux); len(list) != 2 || list[0].ID != tools.ID || list[1].ID != toys.ID {
		t.Errorf("list order = %+v, want sort_order order", list)
	}

	rec := putCategory(t, mux, toys.ID, `{"name":"Toys","sortOrder":5,"hidden":true}`)
	var upd Category
	json.Unmarshal(rec.Body.Bytes(), &upd)
	if rec.Code != http.StatusOK || upd.Name != "Toys" || upd.Slug != "toys-games" || upd.SortOrder != 5 || !upd.Hidden {
		t.Errorf("PUT without slug = %d %+v: the stored slug must stay", rec.Code, upd)
	}
	rec = putCategory(t, mux, toys.ID, `{"name":"Toys","slug":"toys","sortOrder":5}`)
	json.Unmarshal(rec.Body.Bytes(), &upd)
	if rec.Code != http.StatusOK || upd.Slug != "toys" || upd.Hidden {
		t.Errorf("PUT with a new slug = %d %+v", rec.Code, upd)
	}
	if rec := putCategory(t, mux, "missing", `{"name":"x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("PUT unknown = %d", rec.Code)
	}
	if rec := serveReq(mux, httptest.NewRequest("DELETE", "/api/categories/missing", nil)); rec.Code != http.StatusNotFound {
		t.Errorf("DELETE unknown = %d", rec.Code)
	}
	if rec := serveReq(mux, httptest.NewRequest("DELETE", "/api/categories/"+tools.ID, nil)); rec.Code != http.StatusNoContent {
		t.Errorf("DELETE = %d", rec.Code)
	}
	if list := listAdminCategories(t, mux); len(list) != 1 {
		t.Errorf("after delete: %+v", list)
	}
}

func TestCategorySlugRules(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	mustCategory(t, mux, `{"name":"Toys"}`)

	// A derived slug that collides gets a numeric suffix, not an error.
	if c := mustCategory(t, mux, `{"name":"toys!"}`); c.Slug != "toys-2" {
		t.Errorf("derived collision slug = %q, want toys-2", c.Slug)
	}
	if c := mustCategory(t, mux, `{"name":"Ünïcode ✓"}`); c.Slug != "n-code" {
		t.Errorf("non-ASCII derived slug = %q, want n-code", c.Slug)
	}
	if c := mustCategory(t, mux, `{"name":"✓✓"}`); c.Slug != "category" {
		t.Errorf("symbol-only name slug = %q, want category", c.Slug)
	}

	cases := []struct {
		name, body string
		want       int
		contains   string
	}{
		{"explicit duplicate slug", `{"name":"Other","slug":"toys"}`, http.StatusConflict, "slug"},
		{"duplicate name differing in case", `{"name":"TOYS","slug":"fun"}`, http.StatusConflict, "name"},
		{"uppercase slug", `{"name":"A","slug":"Fun"}`, http.StatusUnprocessableEntity, "slug"},
		{"slug with space", `{"name":"A","slug":"a b"}`, http.StatusUnprocessableEntity, "slug"},
		{"slug with slash", `{"name":"A","slug":"a/b"}`, http.StatusUnprocessableEntity, "slug"},
		{"slug with leading hyphen", `{"name":"A","slug":"-a"}`, http.StatusUnprocessableEntity, "slug"},
		{"slug with double hyphen", `{"name":"A","slug":"a--b"}`, http.StatusUnprocessableEntity, "slug"},
		{"slug too long", `{"name":"A","slug":"` + strings.Repeat("a", 41) + `"}`, http.StatusUnprocessableEntity, "slug"},
		{"name required", `{"name":"  "}`, http.StatusUnprocessableEntity, "name"},
		{"name too long", `{"name":"` + strings.Repeat("a", 61) + `"}`, http.StatusUnprocessableEntity, "name"},
		{"name with newline", `{"name":"a\nb"}`, http.StatusUnprocessableEntity, "name"},
		{"unknown field", `{"name":"A","color":"red"}`, http.StatusBadRequest, ""},
		{"valid slug", `{"name":"Fun","slug":"fun-2"}`, http.StatusCreated, ""},
	}
	for _, c := range cases {
		rec := postCategory(t, mux, c.body)
		if rec.Code != c.want || !strings.Contains(rec.Body.String(), c.contains) {
			t.Errorf("%s: %d %s, want %d containing %q", c.name, rec.Code, rec.Body.String(), c.want, c.contains)
		}
	}

	// Renaming into another category's slug or name is also a conflict; keeping your own is fine.
	other := mustCategory(t, mux, `{"name":"Other"}`)
	if rec := putCategory(t, mux, other.ID, `{"name":"Other","slug":"toys"}`); rec.Code != http.StatusConflict {
		t.Errorf("PUT into a taken slug = %d", rec.Code)
	}
	if rec := putCategory(t, mux, other.ID, `{"name":"toys"}`); rec.Code != http.StatusConflict {
		t.Errorf("PUT into a taken name = %d", rec.Code)
	}
	if rec := putCategory(t, mux, other.ID, `{"name":"Other","slug":"other","sortOrder":3}`); rec.Code != http.StatusOK {
		t.Errorf("PUT keeping own slug = %d %s", rec.Code, rec.Body.String())
	}
}

func TestCategoriesLimit(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	for i := 0; i < maxCategories; i++ {
		mustCategory(t, mux, `{"name":"Category `+strings.Repeat("x", i%5)+string(rune('a'+i%26))+string(rune('a'+i/26))+`"}`)
	}
	if rec := postCategory(t, mux, `{"name":"One too many"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("category %d = %d, want 422", maxCategories+1, rec.Code)
	}
}

// ---------------------------------------------------------------------------
// Item fields: categoryId, featured, version concurrency
// ---------------------------------------------------------------------------

func TestItemCategoryAndFeaturedFields(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	toys := mustCategory(t, mux, `{"name":"Toys"}`)

	it := mustItem(t, mux, `{"name":"Dino","priceCents":500,"public":true,"categoryId":"`+toys.ID+`","featured":true}`)
	if it.CategoryID != toys.ID || !it.Featured {
		t.Fatalf("created = %+v", it)
	}
	if rec := postItem(t, mux, `{"name":"Bad","priceCents":1,"categoryId":"nope"}`); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "categoryId") {
		t.Errorf("POST unknown category = %d %s", rec.Code, rec.Body.String())
	}
	if list := listAdminCategories(t, mux); list[0].ItemCount != 1 {
		t.Errorf("itemCount = %d, want 1", list[0].ItemCount)
	}

	// A PUT that omits the fields (an older client) keeps them.
	rec := putItem(t, mux, it.ID, `{"name":"Dino 2","priceCents":500,"public":true,"version":1}`)
	var kept Item
	json.Unmarshal(rec.Body.Bytes(), &kept)
	if rec.Code != http.StatusOK || kept.CategoryID != toys.ID || !kept.Featured || kept.Version != 2 {
		t.Fatalf("PUT omitting fields = %d %+v", rec.Code, kept)
	}
	// An empty categoryId clears it; featured false clears the flag.
	rec = putItem(t, mux, it.ID, `{"name":"Dino 2","priceCents":500,"public":true,"categoryId":"","featured":false,"version":2}`)
	var cleared Item
	json.Unmarshal(rec.Body.Bytes(), &cleared)
	if rec.Code != http.StatusOK || cleared.CategoryID != "" || cleared.Featured || cleared.Version != 3 {
		t.Fatalf("PUT clearing = %d %+v", rec.Code, cleared)
	}
	// Setting an unknown category on update is rejected, and a stale version is still a 409.
	if rec := putItem(t, mux, it.ID, `{"name":"Dino 2","priceCents":500,"categoryId":"nope","version":3}`); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("PUT unknown category = %d", rec.Code)
	}
	if rec := putItem(t, mux, it.ID, `{"name":"Dino 3","priceCents":500,"categoryId":"`+toys.ID+`","version":1}`); rec.Code != http.StatusConflict {
		t.Errorf("PUT stale version = %d, want 409", rec.Code)
	}
	// The list carries the fields too.
	if got := getItems(t, mux).Items; len(got) != 1 || got[0].CategoryID != "" {
		t.Errorf("list = %+v", got)
	}
}

func TestDeleteCategoryUnlinksItems(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	toys := mustCategory(t, mux, `{"name":"Toys"}`)
	a := mustItem(t, mux, `{"name":"A","priceCents":1,"categoryId":"`+toys.ID+`","featured":true}`)
	b := mustItem(t, mux, `{"name":"B","priceCents":1,"categoryId":"`+toys.ID+`"}`)
	c := mustItem(t, mux, `{"name":"C","priceCents":1}`)

	if rec := serveReq(mux, httptest.NewRequest("DELETE", "/api/categories/"+toys.ID, nil)); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE = %d %s", rec.Code, rec.Body.String())
	}
	byID := map[string]Item{}
	for _, it := range getItems(t, mux).Items {
		byID[it.ID] = it
	}
	if len(byID) != 3 {
		t.Fatalf("items deleted along with the category: %d left", len(byID))
	}
	for _, it := range []Item{a, b} {
		got := byID[it.ID]
		if got.CategoryID != "" {
			t.Errorf("item %s still linked: %q", it.Name, got.CategoryID)
		}
		if got.Version != it.Version+1 {
			t.Errorf("item %s version %d, want %d: a stale editor must conflict", it.Name, got.Version, it.Version+1)
		}
	}
	if !byID[a.ID].Featured {
		t.Error("unlinking cleared the featured flag")
	}
	if byID[c.ID].Version != c.Version {
		t.Errorf("an unrelated item changed: version %d", byID[c.ID].Version)
	}
}

// ---------------------------------------------------------------------------
// Public payload
// ---------------------------------------------------------------------------

func getPublicItems(t *testing.T, mux *http.ServeMux) publicItemsResponse {
	t.Helper()
	rec := serveReq(mux, httptest.NewRequest("GET", "/api/public/items", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/public/items = %d %s", rec.Code, rec.Body.String())
	}
	var resp publicItemsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestPublicItemsCategoriesAndFeatured(t *testing.T) {
	prev := publicItemsCacheTTL
	publicItemsCacheTTL = 0
	t.Cleanup(func() { publicItemsCacheTTL = prev })

	e := newShopEnv(t)
	toys := mustCategory(t, e.admin, `{"name":"Toys","sortOrder":2}`)
	tools := mustCategory(t, e.admin, `{"name":"Tools","sortOrder":1}`)
	hidden := mustCategory(t, e.admin, `{"name":"Secret","sortOrder":0,"hidden":true}`)
	empty := mustCategory(t, e.admin, `{"name":"Empty","sortOrder":3}`)
	onlyPrivate := mustCategory(t, e.admin, `{"name":"Private only","sortOrder":4}`)
	_ = empty

	mustItem(t, e.admin, `{"name":"Dino","priceCents":900,"public":true,"sortOrder":1,"categoryId":"`+toys.ID+`"}`)
	mustItem(t, e.admin, `{"name":"Wrench","priceCents":300,"public":true,"sortOrder":2,"categoryId":"`+tools.ID+`"}`)
	mustItem(t, e.admin, `{"name":"Ghost","priceCents":100,"public":true,"sortOrder":3,"categoryId":"`+hidden.ID+`"}`)
	mustItem(t, e.admin, `{"name":"Plain","priceCents":600,"public":true,"sortOrder":4}`)
	mustItem(t, e.admin, `{"name":"Star","priceCents":800,"public":true,"sortOrder":5,"featured":true,"categoryId":"`+tools.ID+`"}`)
	mustItem(t, e.admin, `{"name":"Draft","priceCents":50,"public":false,"sortOrder":0,"categoryId":"`+onlyPrivate.ID+`"}`)

	resp := getPublicItems(t, e.public)
	// Featured first, then the manual order.
	var names []string
	for _, it := range resp.Items {
		names = append(names, it.Name)
	}
	if got := strings.Join(names, ","); got != "Star,Dino,Wrench,Ghost,Plain" {
		t.Errorf("manual order = %s", got)
	}
	// Chips: visible categories with public items only, in category order. Hidden, empty and private-only are gone.
	var chips []string
	for _, c := range resp.Categories {
		chips = append(chips, c.Slug)
	}
	if got := strings.Join(chips, ","); got != "tools,toys" {
		t.Errorf("chips = %s, want tools,toys", got)
	}
	byName := map[string]publicItem{}
	for _, it := range resp.Items {
		byName[it.Name] = it
	}
	if c := byName["Dino"].Category; c == nil || c.Slug != "toys" || c.Name != "Toys" {
		t.Errorf("Dino category = %+v", c)
	}
	// An item in a hidden category stays public, with no category.
	if g, ok := byName["Ghost"]; !ok || g.Category != nil {
		t.Errorf("Ghost = %+v (present %v), want public with a null category", g, ok)
	}
	if byName["Plain"].Category != nil || byName["Plain"].Featured || !byName["Star"].Featured {
		t.Errorf("featured/category flags wrong: %+v / %+v", byName["Plain"], byName["Star"])
	}
	if strings.Contains(serveReq(e.public, httptest.NewRequest("GET", "/api/public/items", nil)).Body.String(), "Draft") {
		t.Error("a non-public item leaked")
	}

	// Un-hiding the category brings its chip and links back.
	putCategory(t, e.admin, hidden.ID, `{"name":"Secret","sortOrder":0,"hidden":false}`)
	resp = getPublicItems(t, e.public)
	if len(resp.Categories) != 3 || resp.Categories[0].Slug != "secret" {
		t.Errorf("chips after unhide = %+v", resp.Categories)
	}
}

func TestPublicItemsSortModes(t *testing.T) {
	prev := publicItemsCacheTTL
	publicItemsCacheTTL = 0
	t.Cleanup(func() { publicItemsCacheTTL = prev })

	e := newShopEnv(t)
	mustItem(t, e.admin, `{"name":"banana","priceCents":300,"public":true,"sortOrder":1}`)
	mustItem(t, e.admin, `{"name":"Apple","priceCents":900,"public":true,"sortOrder":2}`)
	mustItem(t, e.admin, `{"name":"Cherry","priceCents":100,"public":true,"sortOrder":3}`)
	mustItem(t, e.admin, `{"name":"Zed featured","priceCents":999,"public":true,"sortOrder":4,"featured":true}`)

	order := func() string {
		var names []string
		for _, it := range getPublicItems(t, e.public).Items {
			names = append(names, it.Name)
		}
		return strings.Join(names, ",")
	}
	for mode, want := range map[string]string{
		"manual": "Zed featured,banana,Apple,Cherry",
		"name":   "Zed featured,Apple,banana,Cherry",
		"price":  "Zed featured,Cherry,banana,Apple",
	} {
		e.cfg.Shop.ItemSort = mode
		if got := order(); got != want {
			t.Errorf("itemSort %s = %s, want %s", mode, got, want)
		}
	}
}

// The response cache is per sort mode: a settings change shows on the next request even inside the TTL.
func TestPublicItemsCacheFollowsSortMode(t *testing.T) {
	e := newShopEnv(t)
	mustItem(t, e.admin, `{"name":"B","priceCents":100,"public":true,"sortOrder":1}`)
	mustItem(t, e.admin, `{"name":"A","priceCents":200,"public":true,"sortOrder":2}`)
	first := getPublicItems(t, e.public).Items[0].Name
	e.cfg.Shop.ItemSort = "name"
	if got := getPublicItems(t, e.public).Items[0].Name; first != "B" || got != "A" {
		t.Errorf("first %s then %s, want B then A", first, got)
	}
}

// ---------------------------------------------------------------------------
// Export
// ---------------------------------------------------------------------------

func TestExportIncludesCategoriesAndItemFields(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	toys := mustCategory(t, mux, `{"name":"Toys"}`)
	mustItem(t, mux, `{"name":"Dino","priceCents":500,"categoryId":"`+toys.ID+`","featured":true}`)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/export", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		FormatVersion int        `json:"formatVersion"`
		Categories    []Category `json:"categories"`
		Items         []Item     `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Categories) != 1 || payload.Categories[0].Slug != "toys" || payload.Categories[0].ItemCount != 1 {
		t.Errorf("categories = %+v", payload.Categories)
	}
	if len(payload.Items) != 1 || payload.Items[0].CategoryID != toys.ID || !payload.Items[0].Featured {
		t.Errorf("items = %+v", payload.Items)
	}
	// An empty shop still exports "categories": [], never null.
	empty := serveReq(newTestMux(t, t.TempDir()), httptest.NewRequest("GET", "/api/export", nil))
	if !strings.Contains(empty.Body.String(), `"categories":[]`) {
		t.Errorf("empty export lacks \"categories\":[]")
	}
}

// ---------------------------------------------------------------------------
// Permissions
// ---------------------------------------------------------------------------

func TestCategoryRoutesUseTheItemsPermission(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	store := NewConfigStore(loadTestConfig(t, dir))
	auth := newAuthState("secret", nil)
	mux := http.NewServeMux()
	registerRoutes(mux, db, store, auth, dir, false)
	handler := auth.middleware(mux)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/categories"}, {"POST", "/api/categories"}, {"PUT", "/api/categories/x"}, {"DELETE", "/api/categories/x"},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(`{"name":"x"}`))
		req.Header.Set("Content-Type", "application/json")
		if rec := serveReq(handler, req); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusForbidden {
			t.Errorf("%s %s without a session = %d", c.method, c.path, rec.Code)
		}
	}
}
