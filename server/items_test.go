package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

type itemsListResponse struct {
	Items []Item `json:"items"`
}

func postItem(t *testing.T, mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("POST", "/api/items", strings.NewReader(body)))
}

func putItem(t *testing.T, mux *http.ServeMux, id, body string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("PUT", "/api/items/"+id, strings.NewReader(body)))
}

func deleteItem(t *testing.T, mux *http.ServeMux, id string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("DELETE", "/api/items/"+id, nil))
}

func getItems(t *testing.T, mux *http.ServeMux) itemsListResponse {
	t.Helper()
	rec := serveReq(mux, httptest.NewRequest("GET", "/api/items", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/items want 200, got %d %s", rec.Code, rec.Body.String())
	}
	var list itemsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal items list: %v", err)
	}
	return list
}

func TestItemCRUD(t *testing.T) {
	mux := newTestMux(t, t.TempDir())

	rec := postItem(t, mux, `{"name":"Benchy","priceCents":1500,"public":true,
		"optionGroups":[{"label":"Color","required":true,"options":[{"name":"Red","deltaCents":0},{"name":"Glow","deltaCents":250}]}],
		"stockMode":"in_stock","stockQty":3,"sortOrder":1,"description":"A boat"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d %s", rec.Code, rec.Body.String())
	}
	var created Item
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created: %v", err)
	}
	if created.ID == "" {
		t.Fatalf("created item missing id: %+v", created)
	}
	if created.CreatedAt == "" {
		t.Fatalf("created item missing createdAt: %+v", created)
	}
	if created.Name != "Benchy" || created.PriceCents != 1500 || !created.Public {
		t.Fatalf("created item mismatch: %+v", created)
	}
	if len(created.OptionGroups) != 1 || created.OptionGroups[0].Label != "Color" || len(created.OptionGroups[0].Options) != 2 {
		t.Fatalf("created item options mismatch: %+v", created)
	}
	if created.StockMode != "in_stock" || created.StockQty != 3 || created.SortOrder != 1 {
		t.Fatalf("created item stock mismatch: %+v", created)
	}

	list := getItems(t, mux)
	if len(list.Items) != 1 || list.Items[0].ID != created.ID {
		t.Fatalf("GET /api/items after create = %+v", list.Items)
	}

	// Fetch the item fresh via GET and compare, since the POST echo can't prove options_json and the other columns round-tripped.
	fetched := list.Items[0]
	if !reflect.DeepEqual(fetched.OptionGroups, created.OptionGroups) {
		t.Fatalf("GET item option groups round trip = %+v, want %+v", fetched.OptionGroups, created.OptionGroups)
	}
	g := fetched.OptionGroups[0]
	if g.ID == "" || !strings.HasPrefix(g.ID, "g_") || !g.Required || g.Options[1].Name != "Glow" || g.Options[1].DeltaCents != 250 || !strings.HasPrefix(g.Options[1].ID, "o_") {
		t.Fatalf("GET item option group = %+v, want server ids and the posted fields", g)
	}
	if !fetched.Public {
		t.Fatalf("GET item public round trip = %v, want true", fetched.Public)
	}
	if fetched.Description != "A boat" {
		t.Fatalf("GET item description round trip = %q, want %q", fetched.Description, "A boat")
	}
	if fetched.PriceCents != 1500 {
		t.Fatalf("GET item priceCents round trip = %d, want 1500", fetched.PriceCents)
	}

	rec = putItem(t, mux, created.ID, `{"name":"Benchy v2","priceCents":1500,"public":true,
		"optionGroups":[{"label":"Color","required":true,"options":[{"name":"Red","deltaCents":0},{"name":"Glow","deltaCents":250}]}],
		"stockMode":"in_stock","stockQty":9,"sortOrder":1,"description":"A boat"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT want 200, got %d %s", rec.Code, rec.Body.String())
	}
	var updated Item
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal updated: %v", err)
	}
	if updated.Name != "Benchy v2" || updated.StockQty != 9 {
		t.Fatalf("updated item mismatch: %+v", updated)
	}

	rec = deleteItem(t, mux, created.ID)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE want 204, got %d %s", rec.Code, rec.Body.String())
	}

	list = getItems(t, mux)
	if len(list.Items) != 0 {
		t.Fatalf("GET /api/items after delete = %+v, want empty", list.Items)
	}
}

// allowBackorder survives POST, GET and PUT, and is accepted (inert) on a ready_to_print item too.
func TestItemAllowBackorderRoundTrip(t *testing.T) {
	mux := newTestMux(t, t.TempDir())

	rec := postItem(t, mux, `{"name":"Benchy","priceCents":1500,"public":true,
		"stockMode":"in_stock","stockQty":3,"allowBackorder":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d %s", rec.Code, rec.Body.String())
	}
	var created Item
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created: %v", err)
	}
	if !created.AllowBackorder {
		t.Fatalf("created item allowBackorder = %v, want true", created.AllowBackorder)
	}

	list := getItems(t, mux)
	if len(list.Items) != 1 || !list.Items[0].AllowBackorder {
		t.Fatalf("GET /api/items allowBackorder round trip = %+v, want true", list.Items)
	}

	rec = putItem(t, mux, created.ID, `{"name":"Benchy","priceCents":1500,"public":true,
		"stockMode":"in_stock","stockQty":3,"allowBackorder":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT want 200, got %d %s", rec.Code, rec.Body.String())
	}
	var updated Item
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal updated: %v", err)
	}
	if updated.AllowBackorder {
		t.Fatalf("updated item allowBackorder = %v, want false", updated.AllowBackorder)
	}

	// ready_to_print + allowBackorder: inert, not rejected.
	rec = postItem(t, mux, `{"name":"Phone Stand","priceCents":500,"allowBackorder":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST ready_to_print+allowBackorder want 201, got %d %s", rec.Code, rec.Body.String())
	}
	var readyToPrint Item
	if err := json.Unmarshal(rec.Body.Bytes(), &readyToPrint); err != nil {
		t.Fatalf("unmarshal ready_to_print item: %v", err)
	}
	if !readyToPrint.AllowBackorder {
		t.Fatalf("ready_to_print item allowBackorder = %v, want true (stored as given)", readyToPrint.AllowBackorder)
	}
}

func TestItemValidation(t *testing.T) {
	mux := newTestMux(t, t.TempDir())

	cases := []struct {
		name string
		body string
	}{
		{"empty name", `{"name":"","priceCents":100}`},
		{"negative priceCents", `{"name":"x","priceCents":-1}`},
		{"bogus stockMode", `{"name":"x","priceCents":100,"stockMode":"bogus"}`},
		{"negative stockQty", `{"name":"x","priceCents":100,"stockQty":-1}`},
		{"group without options", `{"name":"x","priceCents":100,"optionGroups":[{"label":"Color","required":true,"options":[]}]}`},
		{"group without label", `{"name":"x","priceCents":100,"optionGroups":[{"label":" ","required":true,"options":[{"name":"Red","deltaCents":0}]}]}`},
		{"option without name", `{"name":"x","priceCents":100,"optionGroups":[{"label":"Color","options":[{"name":"","deltaCents":0}]}]}`},
		{"duplicate option names", `{"name":"x","priceCents":100,"optionGroups":[{"label":"Color","options":[{"name":"Red","deltaCents":0},{"name":"red","deltaCents":5}]}]}`},
		{"duplicate group labels (case-insensitive)", `{"name":"x","priceCents":100,"optionGroups":[{"label":"Color","options":[{"name":"Red"}]},{"label":"COLOR","options":[{"name":"Blue"}]}]}`},
		{"six groups", `{"name":"x","priceCents":100,"optionGroups":[{"label":"G0","options":[{"name":"A"}]},{"label":"G1","options":[{"name":"A"}]},{"label":"G2","options":[{"name":"A"}]},{"label":"G3","options":[{"name":"A"}]},{"label":"G4","options":[{"name":"A"}]},{"label":"G5","options":[{"name":"A"}]}]}`},
		{"twenty-one options in a group", `{"name":"x","priceCents":100,"optionGroups":[{"label":"Color","options":[{"name":"O0","deltaCents":0},{"name":"O1","deltaCents":0},{"name":"O2","deltaCents":0},{"name":"O3","deltaCents":0},{"name":"O4","deltaCents":0},{"name":"O5","deltaCents":0},{"name":"O6","deltaCents":0},{"name":"O7","deltaCents":0},{"name":"O8","deltaCents":0},{"name":"O9","deltaCents":0},{"name":"O10","deltaCents":0},{"name":"O11","deltaCents":0},{"name":"O12","deltaCents":0},{"name":"O13","deltaCents":0},{"name":"O14","deltaCents":0},{"name":"O15","deltaCents":0},{"name":"O16","deltaCents":0},{"name":"O17","deltaCents":0},{"name":"O18","deltaCents":0},{"name":"O19","deltaCents":0},{"name":"O20","deltaCents":0}]}]}`},
		{"option delta makes price negative", `{"name":"x","priceCents":100,"optionGroups":[{"label":"Color","required":true,"options":[{"name":"Red","deltaCents":-200}]}]}`},
		{"two required groups together make price negative", `{"name":"x","priceCents":100,"optionGroups":[{"label":"A","required":true,"options":[{"name":"a","deltaCents":-60}]},{"label":"B","required":true,"options":[{"name":"b","deltaCents":-60}]}]}`},
		{"option delta out of range", `{"name":"x","priceCents":100,"optionGroups":[{"label":"Color","options":[{"name":"Red","deltaCents":200000000}]}]}`},
		{"option delta overflow bypass", `{"name":"x","priceCents":100,"optionGroups":[{"label":"Color","options":[{"name":"Red","deltaCents":-9223372036854775808}]}]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := postItem(t, mux, c.body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("%s: want 422, got %d %s", c.name, rec.Code, rec.Body.String())
			}
			var errBody apiErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
				t.Fatalf("%s: unmarshal error body: %v", c.name, err)
			}
			if len(errBody.Errors) == 0 {
				t.Fatalf("%s: want non-empty errors array, got %s", c.name, rec.Body.String())
			}
		})
	}

	t.Run("unknown JSON field", func(t *testing.T) {
		rec := postItem(t, mux, `{"name":"x","priceCents":100,"bogusField":true}`)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("unknown field: want 400, got %d %s", rec.Code, rec.Body.String())
		}
	})
}

// itemImageFileNameRe matches the {16 hex}.png filenames handleImageUpload
// derives from a PNG upload's sha256, mirroring itemImageName's shape
// (server/items.go) for the one extension these tests upload.
var itemImageFileNameRe = regexp.MustCompile(`^[0-9a-f]{16}\.png$`)

// testPNG encodes a single-color 4x4 PNG; a different fill gives different bytes and thus a different sha256-derived filename.
func testPNG(t *testing.T, fill color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := range 4 {
		for x := range 4 {
			img.Set(x, y, fill)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

func uploadItemImage(t *testing.T, mux *http.ServeMux, id string, body []byte) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("POST", "/api/items/"+id+"/image", bytes.NewReader(body)))
}

func TestItemImageUpload(t *testing.T) {
	dir := t.TempDir()
	mux := newTestMux(t, dir)

	rec := postItem(t, mux, `{"name":"Benchy","priceCents":1500}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d %s", rec.Code, rec.Body.String())
	}
	var created Item
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created: %v", err)
	}

	rec = uploadItemImage(t, mux, created.ID, testPNG(t, color.RGBA{255, 0, 0, 255}))
	if rec.Code != http.StatusOK {
		t.Fatalf("image upload want 200, got %d %s", rec.Code, rec.Body.String())
	}
	var first Item
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatalf("unmarshal upload response: %v", err)
	}
	if !itemImageFileNameRe.MatchString(first.ImageFile) {
		t.Fatalf("imageFile = %q, want match of %s", first.ImageFile, itemImageFileNameRe)
	}
	firstPath := filepath.Join(dir, "uploads", "items", first.ImageFile)
	if _, err := os.Stat(firstPath); err != nil {
		t.Fatalf("uploaded file missing: %v", err)
	}

	rec = uploadItemImage(t, mux, created.ID, testPNG(t, color.RGBA{0, 255, 0, 255}))
	if rec.Code != http.StatusOK {
		t.Fatalf("second image upload want 200, got %d %s", rec.Code, rec.Body.String())
	}
	var second Item
	if err := json.Unmarshal(rec.Body.Bytes(), &second); err != nil {
		t.Fatalf("unmarshal second upload response: %v", err)
	}
	if second.ImageFile == first.ImageFile {
		t.Fatalf("second upload kept the same imageFile %q, want a new one", second.ImageFile)
	}
	if _, err := os.Stat(firstPath); !os.IsNotExist(err) {
		t.Fatalf("old image file still present after replace (err = %v)", err)
	}
	secondPath := filepath.Join(dir, "uploads", "items", second.ImageFile)
	if _, err := os.Stat(secondPath); err != nil {
		t.Fatalf("second uploaded file missing: %v", err)
	}
}

func TestItemImageUploadRejects(t *testing.T) {
	dir := t.TempDir()
	mux := newTestMux(t, dir)

	rec := postItem(t, mux, `{"name":"Benchy","priceCents":1500}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d %s", rec.Code, rec.Body.String())
	}
	var created Item
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created: %v", err)
	}

	t.Run("not an image", func(t *testing.T) {
		rec := uploadItemImage(t, mux, created.ID, []byte("hello"))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("want 422, got %d %s", rec.Code, rec.Body.String())
		}
		var errBody apiErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
			t.Fatalf("unmarshal error body: %v", err)
		}
		if len(errBody.Errors) != 1 || errBody.Errors[0] != "image: must be a JPEG, PNG, or WebP file" {
			t.Fatalf("errors = %v", errBody.Errors)
		}
	})

	t.Run("too large", func(t *testing.T) {
		big := bytes.Repeat([]byte("a"), 5<<20+1)
		rec := uploadItemImage(t, mux, created.ID, big)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("want 413, got %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("svg rejected", func(t *testing.T) {
		rec := uploadItemImage(t, mux, created.ID, []byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("want 422, got %d %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("unknown item", func(t *testing.T) {
		rec := uploadItemImage(t, mux, "nope-no-such-item", testPNG(t, color.RGBA{1, 2, 3, 255}))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("want 404, got %d %s", rec.Code, rec.Body.String())
		}
	})
}

// Image filenames are content-addressed, so identical uploads share one file. Deleting or replacing one item's image
// must not delete a file another item still references; the file goes only with the last reference.
func TestItemImageSharedFileSurvivesUntilLastReference(t *testing.T) {
	dir := t.TempDir()
	mux := newTestMux(t, dir)

	recA := postItem(t, mux, `{"name":"A","priceCents":1500}`)
	recB := postItem(t, mux, `{"name":"B","priceCents":1500}`)
	var a, b Item
	if err := json.Unmarshal(recA.Body.Bytes(), &a); err != nil {
		t.Fatalf("unmarshal item A: %v", err)
	}
	if err := json.Unmarshal(recB.Body.Bytes(), &b); err != nil {
		t.Fatalf("unmarshal item B: %v", err)
	}

	sameBytes := testPNG(t, color.RGBA{9, 9, 9, 255})
	rec := uploadItemImage(t, mux, a.ID, sameBytes)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload A want 200, got %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &a); err != nil {
		t.Fatalf("unmarshal A after upload: %v", err)
	}
	rec = uploadItemImage(t, mux, b.ID, sameBytes)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload B want 200, got %d %s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &b); err != nil {
		t.Fatalf("unmarshal B after upload: %v", err)
	}
	if a.ImageFile == "" || a.ImageFile != b.ImageFile {
		t.Fatalf("expected A and B to share one imageFile, got A=%q B=%q", a.ImageFile, b.ImageFile)
	}
	sharedPath := filepath.Join(dir, "uploads", "items", a.ImageFile)
	if _, err := os.Stat(sharedPath); err != nil {
		t.Fatalf("shared image file missing after both uploads: %v", err)
	}

	rec = deleteItem(t, mux, a.ID)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete A want 204, got %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(sharedPath); err != nil {
		t.Fatalf("shared image file should survive while B still references it: %v", err)
	}

	rec = deleteItem(t, mux, b.ID)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete B want 204, got %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(sharedPath); !os.IsNotExist(err) {
		t.Fatalf("shared image file should be removed once the last reference (B) is gone (err = %v)", err)
	}
}

func TestItemListOrder(t *testing.T) {
	mux := newTestMux(t, t.TempDir())

	postItem(t, mux, `{"name":"C","priceCents":100,"sortOrder":2}`)
	postItem(t, mux, `{"name":"A","priceCents":100,"sortOrder":0}`)
	postItem(t, mux, `{"name":"B","priceCents":100,"sortOrder":1}`)

	list := getItems(t, mux)
	if len(list.Items) != 3 {
		t.Fatalf("want 3 items, got %d", len(list.Items))
	}
	if list.Items[0].SortOrder != 0 || list.Items[1].SortOrder != 1 || list.Items[2].SortOrder != 2 {
		t.Fatalf("items not ordered by sortOrder: %+v", list.Items)
	}
}

// Attribution (identity.go): create stamps createdBy/updatedBy; update and the image upload move only updatedBy.

func TestItemCreateAndUpdateStampAttribution(t *testing.T) {
	mux, db := newTestMuxWithDB(t, t.TempDir())
	if _, _, err := createUser(db, "flo", "password123", []string{"items"}, ""); err != nil {
		t.Fatalf("createUser flo: %v", err)
	}
	if _, _, err := createUser(db, "bob", "password123", []string{"items"}, ""); err != nil {
		t.Fatalf("createUser bob: %v", err)
	}

	req := withCtxIdentity(httptest.NewRequest("POST", "/api/items", strings.NewReader(`{"name":"Benchy","priceCents":1500}`)), identity{username: "flo"})
	rec := serveReq(mux, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, body %s", rec.Code, rec.Body.String())
	}
	var created Item
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	if created.CreatedBy != "flo" || created.UpdatedBy != "flo" {
		t.Fatalf("createdBy=%q updatedBy=%q, want both %q", created.CreatedBy, created.UpdatedBy, "flo")
	}

	req = withCtxIdentity(httptest.NewRequest("PUT", "/api/items/"+created.ID, strings.NewReader(`{"name":"Benchy v2","priceCents":1600}`)), identity{username: "bob"})
	rec = serveReq(mux, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d, body %s", rec.Code, rec.Body.String())
	}
	var updated Item
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

func TestItemImageUploadMovesOnlyUpdatedBy(t *testing.T) {
	mux, db := newTestMuxWithDB(t, t.TempDir())
	if _, _, err := createUser(db, "flo", "password123", []string{"items"}, ""); err != nil {
		t.Fatalf("createUser flo: %v", err)
	}
	if _, _, err := createUser(db, "bob", "password123", []string{"items"}, ""); err != nil {
		t.Fatalf("createUser bob: %v", err)
	}

	req := withCtxIdentity(httptest.NewRequest("POST", "/api/items", strings.NewReader(`{"name":"Benchy","priceCents":1500}`)), identity{username: "flo"})
	rec := serveReq(mux, req)
	var created Item
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}

	req = withCtxIdentity(httptest.NewRequest("POST", "/api/items/"+created.ID+"/image", bytes.NewReader(testPNG(t, color.RGBA{255, 0, 0, 255}))), identity{username: "bob"})
	rec = serveReq(mux, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("image upload: status = %d, body %s", rec.Code, rec.Body.String())
	}
	var uploaded Item
	if err := json.Unmarshal(rec.Body.Bytes(), &uploaded); err != nil {
		t.Fatalf("unmarshal upload: %v", err)
	}
	if uploaded.CreatedBy != "flo" {
		t.Fatalf("image upload must not touch createdBy: got %q, want %q", uploaded.CreatedBy, "flo")
	}
	if uploaded.UpdatedBy != "bob" {
		t.Fatalf("image upload: updatedBy = %q, want %q", uploaded.UpdatedBy, "bob")
	}
}

func TestItemCreateWithNoContextIdentityStampsAdmin(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postItem(t, mux, `{"name":"Benchy","priceCents":1500}`)
	var created Item
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.CreatedBy != "admin" || created.UpdatedBy != "admin" {
		t.Fatalf("createdBy=%q updatedBy=%q, want both %q (identityFor's admin fallback)", created.CreatedBy, created.UpdatedBy, "admin")
	}
}
