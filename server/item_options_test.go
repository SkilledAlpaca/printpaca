package main

import (
	"bytes"
	"encoding/json"
	"image/color"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const twoGroupItem = `{"name":"Clip","priceCents":1000,"public":true,"optionGroups":[
	{"label":"Color","required":true,"options":[{"name":"Black","deltaCents":0},{"name":"White","deltaCents":50}]},
	{"label":"Mount","required":false,"options":[{"name":"Strip","deltaCents":100}]}]}`

func mustCreateItem(t *testing.T, mux *http.ServeMux, body string) Item {
	t.Helper()
	rec := postItem(t, mux, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST item: %d %s", rec.Code, rec.Body.String())
	}
	var it Item
	if err := json.Unmarshal(rec.Body.Bytes(), &it); err != nil {
		t.Fatal(err)
	}
	return it
}

func optionID(t *testing.T, it Item, label, name string) string {
	t.Helper()
	return selOf(t, it, label, name).OptionID
}

func uploadOptionImage(mux *http.ServeMux, itemID, optionID string, body []byte) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("POST", "/api/items/"+itemID+"/options/"+optionID+"/image", bytes.NewReader(body)))
}

func deleteOptionImage(mux *http.ServeMux, itemID, optionID string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("DELETE", "/api/items/"+itemID+"/options/"+optionID+"/image", nil))
}

func fileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, "uploads", "items", name))
	return err == nil
}

// Migration 1 -> 2 turns option_label + options_json into one required group with generated ids, leaves
// items without options on [], and keeps the legacy columns.
func TestMigrationOptionGroups(t *testing.T) {
	db, err := openRawSQLite(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err := runMigration(db, 0); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ id, label, opts string }{
		{"a", "Color", `[{"name":"Red","deltaCents":0},{"name":"Glow","deltaCents":250}]`},
		{"b", "", `[]`},
	} {
		if _, err := db.Exec(`INSERT INTO items (id, name, price_cents, option_label, options_json, created_at, updated_at)
			VALUES (?, ?, 100, ?, ?, 'x', 'x')`, row.id, row.id, row.label, row.opts); err != nil {
			t.Fatal(err)
		}
	}
	if err := runMigration(db, 1); err != nil {
		t.Fatalf("migration 2: %v", err)
	}

	read := func(id string) []ItemOptionGroup {
		var js string
		if err := db.QueryRow(`SELECT option_groups_json FROM items WHERE id = ?`, id).Scan(&js); err != nil {
			t.Fatal(err)
		}
		var g []ItemOptionGroup
		if err := json.Unmarshal([]byte(js), &g); err != nil {
			t.Fatalf("groups json %q: %v", js, err)
		}
		return g
	}
	g := read("a")
	if len(g) != 1 || g[0].Label != "Color" || !g[0].Required || !strings.HasPrefix(g[0].ID, "g_") || len(g[0].Options) != 2 {
		t.Fatalf("groups = %+v", g)
	}
	if o := g[0].Options[1]; o.Name != "Glow" || o.DeltaCents != 250 || !strings.HasPrefix(o.ID, "o_") || o.ImageFile != "" || o.ID == g[0].Options[0].ID {
		t.Errorf("option = %+v", o)
	}
	if g[0].Options[0].Name != "Red" {
		t.Errorf("option order not kept: %+v", g[0].Options)
	}
	if got := read("b"); len(got) != 0 {
		t.Errorf("item without options got groups %+v", got)
	}
	var legacy string
	if err := db.QueryRow(`SELECT option_label FROM items WHERE id = 'a'`).Scan(&legacy); err != nil || legacy != "Color" {
		t.Errorf("legacy option_label = %q, %v; want kept", legacy, err)
	}
}

func TestPutKeepsOptionIDsAndPhotos(t *testing.T) {
	dir := t.TempDir()
	mux := newTestMux(t, dir)
	it := mustCreateItem(t, mux, twoGroupItem)
	black := optionID(t, it, "Color", "Black")
	if rec := uploadOptionImage(mux, it.ID, black, testPNG(t, color.RGBA{1, 2, 3, 255})); rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}

	// Reorder groups, rename an option, add a new option, and smuggle in a foreign id and an imageFile.
	body := map[string]any{"name": "Clip", "priceCents": 1000, "public": true, "optionGroups": []any{
		map[string]any{"id": it.OptionGroups[1].ID, "label": "Mount", "required": false, "options": []any{
			map[string]any{"id": it.OptionGroups[1].Options[0].ID, "name": "Strip", "deltaCents": 100}}},
		map[string]any{"id": it.OptionGroups[0].ID, "label": "Colour", "required": true, "options": []any{
			map[string]any{"id": black, "name": "Jet", "deltaCents": 0, "imageFile": "ffffffffffffffff.png"},
			map[string]any{"id": "o_attacker", "name": "Red", "deltaCents": 0, "imageFile": "eeeeeeeeeeeeeeee.png"},
		}},
	}}
	b, _ := json.Marshal(body)
	rec := putItem(t, mux, it.ID, string(b))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	var got Item
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.OptionGroups[0].Label != "Mount" || got.OptionGroups[1].ID != it.OptionGroups[0].ID {
		t.Fatalf("order/ids not kept: %+v", got.OptionGroups)
	}
	jet := got.OptionGroups[1].Options[0]
	if jet.ID != black || jet.Name != "Jet" || jet.ImageFile != it.OptionGroups[0].Options[0].ImageFile+"" && jet.ImageFile == "" {
		t.Errorf("renamed option = %+v, want same id and its photo", jet)
	}
	if jet.ImageFile == "" || jet.ImageFile == "ffffffffffffffff.png" {
		t.Errorf("photo = %q, want the uploaded one kept, client value ignored", jet.ImageFile)
	}
	red := got.OptionGroups[1].Options[1]
	if red.ID == "o_attacker" || !strings.HasPrefix(red.ID, "o_") || red.ImageFile != "" {
		t.Errorf("new option = %+v, want a fresh id and no photo", red)
	}
}

func TestOptionImageLifecycle(t *testing.T) {
	dir := t.TempDir()
	mux := newTestMux(t, dir)
	it := mustCreateItem(t, mux, twoGroupItem)
	black, white := optionID(t, it, "Color", "Black"), optionID(t, it, "Color", "White")

	rec := uploadOptionImage(mux, it.ID, black, testPNG(t, color.RGBA{9, 0, 0, 255}))
	if rec.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body.String())
	}
	var up Item
	json.Unmarshal(rec.Body.Bytes(), &up)
	file := up.OptionGroups[0].Options[0].ImageFile
	if !itemImageFileNameRe.MatchString(file) || !fileExists(dir, file) {
		t.Fatalf("option photo = %q, want a content-addressed file on disk", file)
	}
	if up.OptionGroups[0].Options[1].ImageFile != "" {
		t.Error("photo leaked to a sibling option")
	}

	// Identical bytes on a second option share one file; deleting one keeps it.
	if rec := uploadOptionImage(mux, it.ID, white, testPNG(t, color.RGBA{9, 0, 0, 255})); rec.Code != http.StatusOK {
		t.Fatalf("upload second: %d", rec.Code)
	}
	if rec := deleteOptionImage(mux, it.ID, black); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if !fileExists(dir, file) {
		t.Fatal("shared option photo removed while another option still uses it")
	}
	// An item photo with the same bytes also protects it.
	if rec := uploadItemImage(t, mux, it.ID, testPNG(t, color.RGBA{9, 0, 0, 255})); rec.Code != http.StatusOK {
		t.Fatalf("item photo: %d", rec.Code)
	}
	if rec := deleteOptionImage(mux, it.ID, white); rec.Code != http.StatusOK {
		t.Fatalf("delete second: %d", rec.Code)
	}
	if !fileExists(dir, file) {
		t.Fatal("file removed while the item photo uses it")
	}

	// Replacing an option photo removes the old file once unshared.
	uploadOptionImage(mux, it.ID, black, testPNG(t, color.RGBA{0, 9, 0, 255}))
	rec = uploadOptionImage(mux, it.ID, black, testPNG(t, color.RGBA{0, 0, 9, 255}))
	var rep Item
	json.Unmarshal(rec.Body.Bytes(), &rep)
	newFile := rep.OptionGroups[0].Options[0].ImageFile
	entries, _ := os.ReadDir(filepath.Join(dir, "uploads", "items"))
	if len(entries) != 2 { // the item photo and the current option photo
		t.Errorf("uploads = %d files, want 2 (item photo + current option photo)", len(entries))
	}

	// Removing the option in a PUT cleans its photo up.
	b, _ := json.Marshal(map[string]any{"name": "Clip", "priceCents": 1000, "optionGroups": []any{
		map[string]any{"id": it.OptionGroups[0].ID, "label": "Color", "required": true, "options": []any{
			map[string]any{"id": white, "name": "White", "deltaCents": 50}}}}})
	if rec := putItem(t, mux, it.ID, string(b)); rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	if fileExists(dir, newFile) {
		t.Error("photo of a removed option was not cleaned up")
	}

	// Deleting the item cleans the rest.
	if rec := deleteItem(t, mux, it.ID); rec.Code != http.StatusNoContent {
		t.Fatalf("delete item: %d", rec.Code)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "uploads", "items")); len(entries) != 0 {
		t.Errorf("%d files left after deleting the item", len(entries))
	}
}

func TestOptionImageErrors(t *testing.T) {
	dir := t.TempDir()
	mux := newTestMux(t, dir)
	it := mustCreateItem(t, mux, twoGroupItem)
	black := optionID(t, it, "Color", "Black")

	if rec := uploadOptionImage(mux, "nope", black, testPNG(t, color.RGBA{1, 1, 1, 255})); rec.Code != http.StatusNotFound {
		t.Errorf("unknown item: %d, want 404", rec.Code)
	}
	if rec := uploadOptionImage(mux, it.ID, "o_nope", testPNG(t, color.RGBA{1, 1, 1, 255})); rec.Code != http.StatusNotFound {
		t.Errorf("unknown option: %d, want 404", rec.Code)
	}
	if rec := deleteOptionImage(mux, it.ID, "o_nope"); rec.Code != http.StatusNotFound {
		t.Errorf("delete unknown option: %d, want 404", rec.Code)
	}
	if rec := uploadOptionImage(mux, it.ID, black, []byte("not an image")); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("non-image: %d, want 422", rec.Code)
	}
	big := make([]byte, itemImageMaxBytes+1)
	if rec := uploadOptionImage(mux, it.ID, black, big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversize: %d, want 413", rec.Code)
	}
	if rec := deleteOptionImage(mux, it.ID, black); rec.Code != http.StatusOK {
		t.Errorf("delete with no photo: %d, want 200 no-op", rec.Code)
	}
}

func TestOptionImageCSRFContentType(t *testing.T) {
	if !isItemImageUploadRoute("POST", "/api/items/abc/options/o_1/image") || !isItemImageUploadRoute("POST", "/api/items/abc/image") {
		t.Error("option and item image uploads must both use the image Content-Type allowlist")
	}
	if isItemImageUploadRoute("DELETE", "/api/items/abc/options/o_1/image") {
		t.Error("DELETE is not an upload")
	}
}

func TestPublicItemOrderOptionGroups(t *testing.T) {
	dir := t.TempDir()
	adminMux, publicMux, db, _ := newRequestsTestSetup(t, dir)
	it := mustCreateItem(t, adminMux, twoGroupItem)
	white := optionID(t, it, "Color", "White")
	if rec := uploadOptionImage(adminMux, it.ID, white, testPNG(t, color.RGBA{5, 5, 5, 255})); rec.Code != http.StatusOK {
		t.Fatalf("upload: %d", rec.Code)
	}

	// Public payload: ids, required, imageUrl per option, no imageFile.
	rec := serveReq(publicMux, httptest.NewRequest("GET", "/api/public/items", nil))
	if strings.Contains(rec.Body.String(), "imageFile") {
		t.Errorf("public payload leaks imageFile: %s", rec.Body.String())
	}
	var list struct{ Items []publicItem }
	json.Unmarshal(rec.Body.Bytes(), &list)
	pg := list.Items[0].OptionGroups
	if len(pg) != 2 || !pg[0].Required || pg[1].Required || !strings.HasPrefix(pg[0].Options[1].ImageURL, "/item-images/") || pg[0].Options[0].ImageURL != "" {
		t.Fatalf("public groups = %+v", pg)
	}

	order := func(sels []optionSelection, qty int) (*httptest.ResponseRecorder, string) {
		rec := postPublicItemOrder(t, publicMux, validItemOrderBody(it.ID, sels, qty))
		var acc struct {
			ID         string `json:"id"`
			PriceCents int    `json:"priceCents"`
		}
		json.Unmarshal(rec.Body.Bytes(), &acc)
		return rec, acc.ID
	}
	// Sum of deltas times quantity; the summary lists selected groups in display order whatever the request order.
	rec, id := order([]optionSelection{selOf(t, it, "Mount", "Strip"), selOf(t, it, "Color", "White")}, 3)
	if rec.Code != http.StatusCreated {
		t.Fatalf("order: %d %s", rec.Code, rec.Body.String())
	}
	var price int
	var opt string
	if err := db.QueryRow(`SELECT price_cents, option_name FROM quote_requests WHERE id = ?`, id).Scan(&price, &opt); err != nil {
		t.Fatal(err)
	}
	if price != (1000+50+100)*3 || opt != "Color: White · Mount: Strip" {
		t.Errorf("price %d option %q, want 3450 and %q", price, opt, "Color: White · Mount: Strip")
	}
	// Optional group left out.
	rec, id = order([]optionSelection{selOf(t, it, "Color", "Black")}, 1)
	if rec.Code != http.StatusCreated {
		t.Fatalf("order: %d", rec.Code)
	}
	db.QueryRow(`SELECT price_cents, option_name FROM quote_requests WHERE id = ?`, id).Scan(&price, &opt)
	if price != 1000 || opt != "Color: Black" {
		t.Errorf("price %d option %q, want 1000 and %q", price, opt, "Color: Black")
	}
	// Rejections.
	for name, sels := range map[string][]optionSelection{
		"only the optional group":   {selOf(t, it, "Mount", "Strip")},
		"unknown group":             {{GroupID: "g_zzz", OptionID: "o_zzz"}},
		"option from another group": {{GroupID: it.OptionGroups[0].ID, OptionID: it.OptionGroups[1].Options[0].ID}},
		"two in one group":          {selOf(t, it, "Color", "Black"), selOf(t, it, "Color", "White")},
	} {
		if rec, _ := order(sels, 1); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "selections:") {
			t.Errorf("%s: %d %s, want 422 selections error", name, rec.Code, rec.Body.String())
		}
	}
	// The retired field is rejected outright.
	bad := validItemOrderBody(it.ID, nil, 1)
	bad["optionName"] = "Black"
	if rec := postPublicItemOrder(t, publicMux, bad); rec.Code != http.StatusBadRequest {
		t.Errorf("optionName: %d, want 400", rec.Code)
	}
}

func TestOptionSummaryDelta(t *testing.T) {
	groups := []ItemOptionGroup{
		{Label: "Color: Hue", Options: []ItemOption{{Name: "Red", DeltaCents: 10}, {Name: "A · B", DeltaCents: 20}}},
		{Label: "Size", Options: []ItemOption{{Name: "L", DeltaCents: 5}}},
	}
	for s, want := range map[string]int{
		"": 0, "Color: Hue: Red": 10, "Size: L": 5, "Size: L · Color: Hue: A · B": 25, "Color: Hue: A · B": 20,
	} {
		if d, ok := optionSummaryDelta(groups, s); !ok || d != want {
			t.Errorf("%q = %d, %v; want %d", s, d, ok, want)
		}
	}
	for _, s := range []string{"Red", "Size: XL", "Size: L · Size: L", "Size: L ·", "Color: Hue: Red · "} {
		if _, ok := optionSummaryDelta(groups, s); ok {
			t.Errorf("%q accepted", s)
		}
	}
}

// setOptionImage reports found=false and writes nothing when the option (or item) is gone, which is what
// the upload handler sees when a PUT removed the option after its first check.
func TestSetOptionImageNotFoundWritesNothing(t *testing.T) {
	dir := t.TempDir()
	mux, db := newTestMuxWithDB(t, dir)
	it := mustCreateItem(t, mux, twoGroupItem)
	store := newItems(db, dir)
	var before string
	if err := db.QueryRow(`SELECT option_groups_json || updated_at FROM items WHERE id = ?`, it.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ item, opt string }{{it.ID, "o_gone"}, {"nope", it.OptionGroups[0].Options[0].ID}} {
		old, found, err := store.setOptionImage(c.item, c.opt, "aaaaaaaaaaaaaaaa.png", "tester")
		if err != nil || found || old != "" {
			t.Errorf("setOptionImage(%q, %q) = %q, %v, %v; want not found", c.item, c.opt, old, found, err)
		}
	}
	var after string
	if err := db.QueryRow(`SELECT option_groups_json || updated_at FROM items WHERE id = ?`, it.ID).Scan(&after); err != nil || after != before {
		t.Errorf("row changed on a not-found write: %v", err)
	}
	// The transaction was released: the single connection still serves queries.
	if rec := putItem(t, mux, it.ID, twoGroupItem); rec.Code != http.StatusOK {
		t.Errorf("PUT after not-found write: %d", rec.Code)
	}
}

// An upload for a missing option stores no file.
func TestOptionImageUploadNotFoundLeavesNoFile(t *testing.T) {
	dir := t.TempDir()
	mux := newTestMux(t, dir)
	it := mustCreateItem(t, mux, twoGroupItem)
	if rec := uploadOptionImage(mux, it.ID, "o_gone", testPNG(t, color.RGBA{9, 9, 9, 255})); rec.Code != http.StatusNotFound {
		t.Fatalf("upload to missing option: %d", rec.Code)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "uploads", "items"))
	if len(entries) != 0 {
		t.Errorf("files left behind: %v", entries)
	}
}
