package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const itemsMaxBytes = 64 * 1024 // 64 KiB

// itemImageMaxBytes caps an uploaded item image's raw body.
const itemImageMaxBytes = 5 << 20 // 5 MiB

// stockMode values: printed on demand (the default) or sold from a fixed on-hand quantity.
const (
	stockModeReadyToPrint = "ready_to_print"
	stockModeInStock      = "in_stock"
)

// maxItemOrderQty caps the units of one item in a gallery order (handlePublicItemOrder), backorders included.
const maxItemOrderQty = 10000

// itemConflictMessage is the 409 body for a PUT sent with a stale version.
const itemConflictMessage = "this item was changed in another tab or by another user; reload to see the latest"

const itemColumns = `id, name, description, price_cents, option_groups_json,
  image_file, public, stock_mode, stock_qty, allow_backorder, sort_order, created_at, updated_at, created_by, updated_by, version`

// Option group limits.
const (
	maxOptionGroups   = 5
	maxOptionsPerGrp  = 20
	optionGroupIDPref = "g_"
	optionIDPref      = "o_"
)

// ItemOption is one selectable variant (e.g. a color) inside an ItemOptionGroup. ID is server-generated and
// stable across edits. ImageFile is the content-addressed filename of the option's photo ("" for none); it is
// set only by the option image endpoints, never by the item body.
type ItemOption struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	DeltaCents int    `json:"deltaCents"`
	ImageFile  string `json:"imageFile"`
}

// ItemOptionGroup is one choice an item offers (e.g. "Color"). Required groups must be answered in an order;
// optional ones default to none. Array order, here and in Options, is display order. The groups are stored as
// JSON in items.option_groups_json: small, and always read and written with the item.
type ItemOptionGroup struct {
	ID       string       `json:"id"`
	Label    string       `json:"label"`
	Required bool         `json:"required"`
	Options  []ItemOption `json:"options"`
}

// Item is the JSON shape of every items endpoint.
type Item struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	PriceCents   int               `json:"priceCents"`
	OptionGroups []ItemOptionGroup `json:"optionGroups"`
	ImageFile    string            `json:"imageFile"`
	Public       bool              `json:"public"`
	StockMode    string            `json:"stockMode"`
	StockQty     int               `json:"stockQty"`
	// AllowBackorder matters only for stockModeInStock: a public order may exceed StockQty (to
	// maxItemOrderQty). Stored as given for stockModeReadyToPrint too, where it is inert.
	AllowBackorder bool   `json:"allowBackorder"`
	SortOrder      int    `json:"sortOrder"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
	// CreatedBy/UpdatedBy follow Quote.CreatedBy. UpdatedBy also moves on handleImageUpload and on
	// requests.go's handleStatus stock decrement.
	CreatedBy string `json:"createdBy"`
	UpdatedBy string `json:"updatedBy"`
	// Version counts every write to the row (a trigger from migration 3 bumps it, so photo uploads and
	// stock decrements count too). A PUT that sends the version it loaded is rejected with 409 when the
	// row has moved on since.
	Version int `json:"version"`
}

// itemInput is the POST/PUT body: Item minus the server-assigned id, image file, and timestamps. Group and
// option ids are honored only when they match the stored item (reconcileOptionGroups); imageFile is ignored.
type itemInput struct {
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	PriceCents     int               `json:"priceCents"`
	OptionGroups   []ItemOptionGroup `json:"optionGroups"`
	Public         bool              `json:"public"`
	StockMode      string            `json:"stockMode"`
	StockQty       int               `json:"stockQty"`
	AllowBackorder bool              `json:"allowBackorder"`
	SortOrder      int               `json:"sortOrder"`
	// Version is the Item.Version the form was loaded with. Optional: a PUT without it skips the check.
	Version *int `json:"version"`
}

type items struct {
	db        *sql.DB
	configDir string
}

func newItems(db *sql.DB, configDir string) *items {
	return &items{db: db, configDir: configDir}
}

// itemImagePath returns the on-disk path for an item's uploaded image.
func itemImagePath(configDir, file string) string {
	return filepath.Join(configDir, "uploads", "items", file)
}

// removeItemImage best-effort deletes an item's image file.
// A missing file is not an error; other failures are logged, not propagated, since the row is already gone.
func removeItemImage(configDir, file string) {
	if file == "" {
		return
	}
	if err := os.Remove(itemImagePath(configDir, file)); err != nil && !os.IsNotExist(err) {
		log.Printf("items delete image: %v", err)
	}
}

// removeItemImageIfUnshared removes file unless another item's photo, or any item's option photo, still
// points at it. Filenames are content-addressed (sha256), so items and options uploaded with identical bytes
// share one file. Called after the row change that stops referencing file has committed, so the item that
// changed no longer counts as a reference.
func removeItemImageIfUnshared(db *sql.DB, configDir, file string) {
	if file == "" {
		return
	}
	var one int
	err := db.QueryRow(`SELECT 1 FROM items WHERE image_file = ? OR instr(option_groups_json, ?) > 0 LIMIT 1`,
		file, `"`+file+`"`).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		removeItemImage(configDir, file)
		return
	}
	if err != nil {
		log.Printf("items check shared image: %v", err)
	}
}

// optionImageFiles returns every non-empty option photo filename in groups.
func optionImageFiles(groups []ItemOptionGroup) []string {
	var files []string
	for _, g := range groups {
		for _, o := range g.Options {
			if o.ImageFile != "" {
				files = append(files, o.ImageFile)
			}
		}
	}
	return files
}

// validateItemInput trims fields in place, defaults a blank StockMode, and returns one message per problem.
func validateItemInput(in *itemInput) []string {
	var problems []string

	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 200 {
		problems = append(problems, "name: required, at most 200 characters")
	}
	if len(in.Description) > 5000 {
		problems = append(problems, "description: at most 5000 characters")
	}
	if in.PriceCents < 0 || in.PriceCents > 100_000_000 {
		problems = append(problems, "priceCents: must be between 0 and 100000000")
	}
	if in.StockMode == "" {
		in.StockMode = stockModeReadyToPrint
	}
	if in.StockMode != stockModeReadyToPrint && in.StockMode != stockModeInStock {
		problems = append(problems, "stockMode: must be ready_to_print or in_stock")
	}
	if in.StockQty < 0 || in.StockQty > 1_000_000 {
		problems = append(problems, "stockQty: must be between 0 and 1000000")
	}
	// AllowBackorder is stored as given regardless of StockMode: rejecting or clearing it for
	// stockModeReadyToPrint would lose the setting when an item flips between modes.

	problems = append(problems, validateOptionGroups(in.OptionGroups, in.PriceCents)...)
	return problems
}

// validateOptionGroups trims labels and names in place and returns one message per problem. price is the
// item price: the cheapest possible selection (the lowest delta of each required group, 0 for an optional
// group when no option is cheaper) must not make it negative.
func validateOptionGroups(groups []ItemOptionGroup, price int) []string {
	var problems []string
	if len(groups) > maxOptionGroups {
		return []string{fmt.Sprintf("optionGroups: at most %d groups", maxOptionGroups)}
	}
	labels := map[string]bool{}
	minSum := 0
	for gi := range groups {
		g := &groups[gi]
		g.Label = strings.TrimSpace(g.Label)
		if g.Label == "" || len(g.Label) > 100 {
			problems = append(problems, fmt.Sprintf("optionGroups[%d].label: required, at most 100 characters", gi))
			continue
		}
		if key := strings.ToLower(g.Label); labels[key] {
			problems = append(problems, "optionGroups: duplicate label "+g.Label)
			continue
		} else {
			labels[key] = true
		}
		if len(g.Options) == 0 {
			problems = append(problems, fmt.Sprintf("optionGroups: %s needs at least one option", g.Label))
			continue
		}
		if len(g.Options) > maxOptionsPerGrp {
			problems = append(problems, fmt.Sprintf("optionGroups: %s has more than %d options", g.Label, maxOptionsPerGrp))
			continue
		}
		names := map[string]bool{}
		lowest, bad := 0, false
		for oi := range g.Options {
			o := &g.Options[oi]
			o.Name = strings.TrimSpace(o.Name)
			if o.Name == "" || len(o.Name) > 100 {
				problems = append(problems, fmt.Sprintf("optionGroups: %s option names required, at most 100 characters", g.Label))
				bad = true
				break
			}
			if key := strings.ToLower(o.Name); names[key] {
				problems = append(problems, fmt.Sprintf("optionGroups: %s has duplicate option %s", g.Label, o.Name))
				bad = true
				break
			} else {
				names[key] = true
			}
			// Bounded before any sum: an unbounded DeltaCents could wrap the price positive, bypassing the
			// negative-price check, or overflow the order total.
			if o.DeltaCents < -100_000_000 || o.DeltaCents > 100_000_000 {
				problems = append(problems, fmt.Sprintf("optionGroups: %s / %s deltaCents must be between -100000000 and 100000000", g.Label, o.Name))
				bad = true
				break
			}
			if oi == 0 || o.DeltaCents < lowest {
				lowest = o.DeltaCents
			}
		}
		if bad {
			continue
		}
		if !g.Required && lowest > 0 {
			lowest = 0
		}
		minSum += lowest
	}
	if len(problems) == 0 && price+minSum < 0 {
		problems = append(problems, "optionGroups: the cheapest selection makes the price negative")
	}
	return problems
}

// newOptionID returns prefix plus 8 random hex characters not already in used, and records it there.
func newOptionID(prefix string, used map[string]bool) string {
	for {
		b := make([]byte, 4)
		rand.Read(b)
		id := prefix + hex.EncodeToString(b)
		if !used[id] {
			used[id] = true
			return id
		}
	}
}

// reconcileOptionGroups returns in with server-owned fields filled: a group or option whose id matches one in
// existing keeps that id (and, for an option, its photo); anything else gets a fresh id and no photo, however
// the client filled id and imageFile. in is not modified.
func reconcileOptionGroups(existing, in []ItemOptionGroup) []ItemOptionGroup {
	groupByID := map[string]ItemOptionGroup{}
	optByID := map[string]ItemOption{}
	used := map[string]bool{}
	for _, g := range existing {
		groupByID[g.ID] = g
		for _, o := range g.Options {
			optByID[o.ID] = o
		}
	}
	out := make([]ItemOptionGroup, 0, len(in))
	seenG := map[string]bool{}
	seenO := map[string]bool{}
	for _, g := range in {
		ng := ItemOptionGroup{Label: g.Label, Required: g.Required, Options: make([]ItemOption, 0, len(g.Options))}
		if _, ok := groupByID[g.ID]; ok && !seenG[g.ID] {
			ng.ID = g.ID
			seenG[g.ID] = true
			used[g.ID] = true
		}
		for _, o := range g.Options {
			no := ItemOption{Name: o.Name, DeltaCents: o.DeltaCents}
			if old, ok := optByID[o.ID]; ok && !seenO[o.ID] {
				no.ID = o.ID
				no.ImageFile = old.ImageFile
				seenO[o.ID] = true
				used[o.ID] = true
			}
			ng.Options = append(ng.Options, no)
		}
		out = append(out, ng)
	}
	for gi := range out {
		if out[gi].ID == "" {
			out[gi].ID = newOptionID(optionGroupIDPref, used)
		}
		for oi := range out[gi].Options {
			if out[gi].Options[oi].ID == "" {
				out[gi].Options[oi].ID = newOptionID(optionIDPref, used)
			}
		}
	}
	return out
}

// scanItem scans one items row (itemColumns order) and unmarshals option_groups_json; row may be a Row or Rows.
func scanItem(row interface{ Scan(...any) error }) (Item, error) {
	var it Item
	var groupsJSON string
	if err := row.Scan(&it.ID, &it.Name, &it.Description, &it.PriceCents, &groupsJSON,
		&it.ImageFile, &it.Public, &it.StockMode, &it.StockQty, &it.AllowBackorder, &it.SortOrder, &it.CreatedAt, &it.UpdatedAt,
		&it.CreatedBy, &it.UpdatedBy, &it.Version); err != nil {
		return Item{}, err
	}
	if err := json.Unmarshal([]byte(groupsJSON), &it.OptionGroups); err != nil {
		return Item{}, err
	}
	if it.OptionGroups == nil {
		it.OptionGroups = []ItemOptionGroup{}
	}
	for gi := range it.OptionGroups {
		if it.OptionGroups[gi].Options == nil {
			it.OptionGroups[gi].Options = []ItemOption{}
		}
	}
	return it, nil
}

// rowQuerier is the QueryRow half of *sql.DB and *sql.Tx, so getItem runs on either.
type rowQuerier interface {
	QueryRow(query string, args ...any) *sql.Row
}

// getItem loads one item by id; package-level so the public gallery handlers can use it.
// Returns (Item{}, false, nil) when no row matches.
func getItem(db rowQuerier, id string) (Item, bool, error) {
	row := db.QueryRow(`SELECT `+itemColumns+` FROM items WHERE id = ?`, id)
	it, err := scanItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, false, nil
	}
	if err != nil {
		return Item{}, false, err
	}
	return it, true, nil
}

func (it *items) handleList(w http.ResponseWriter, r *http.Request) {
	rows, err := it.db.Query(`SELECT ` + itemColumns + ` FROM items ORDER BY sort_order, name`)
	if err != nil {
		log.Printf("items query: %v", err)
		http.Error(w, "items unavailable", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// Empty, not nil, so a fresh database serializes "items":[].
	list := []Item{}
	for rows.Next() {
		i, err := scanItem(rows)
		if err != nil {
			log.Printf("items scan: %v", err)
			http.Error(w, "items unavailable", http.StatusInternalServerError)
			return
		}
		list = append(list, i)
	}
	if err := rows.Err(); err != nil {
		log.Printf("items read: %v", err)
		http.Error(w, "items unavailable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Items []Item `json:"items"`
	}{Items: list})
}

func (it *items) handlePost(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeJSONBody[itemInput](w, r, itemsMaxBytes)
	if !ok {
		return
	}
	if problems := validateItemInput(&in); len(problems) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, problems...)
		return
	}

	groups := reconcileOptionGroups(nil, in.OptionGroups)
	groupsJSON, err := json.Marshal(groups)
	if err != nil {
		log.Printf("items marshal options: %v", err)
		http.Error(w, "items not writable", http.StatusInternalServerError)
		return
	}

	by := identityFor(r).username
	now := time.Now().UTC().Format(time.RFC3339)
	i := Item{
		ID:             newID(),
		Name:           in.Name,
		Description:    in.Description,
		PriceCents:     in.PriceCents,
		OptionGroups:   groups,
		ImageFile:      "",
		Public:         in.Public,
		StockMode:      in.StockMode,
		StockQty:       in.StockQty,
		AllowBackorder: in.AllowBackorder,
		SortOrder:      in.SortOrder,
		CreatedAt:      now,
		UpdatedAt:      now,
		CreatedBy:      by,
		UpdatedBy:      by,
		Version:        1,
	}
	_, err = it.db.Exec(`
		INSERT INTO items (id, name, description, price_cents, option_groups_json,
			image_file, public, stock_mode, stock_qty, allow_backorder, sort_order, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		i.ID, i.Name, i.Description, i.PriceCents, string(groupsJSON),
		i.ImageFile, i.Public, i.StockMode, i.StockQty, i.AllowBackorder, i.SortOrder, i.CreatedAt, i.UpdatedAt, i.CreatedBy, i.UpdatedBy)
	if err != nil {
		log.Printf("items insert: %v", err)
		http.Error(w, "items not writable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(i)
}

func (it *items) handlePut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	in, ok := decodeJSONBody[itemInput](w, r, itemsMaxBytes)
	if !ok {
		return
	}
	if problems := validateItemInput(&in); len(problems) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, problems...)
		return
	}

	existing, found, err := getItem(it.db, id)
	if err != nil {
		log.Printf("items load for update: %v", err)
		http.Error(w, "items unavailable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "item not found")
		return
	}
	if in.Version != nil && *in.Version != existing.Version {
		writeAPIError(w, http.StatusConflict, itemConflictMessage)
		return
	}
	groups := reconcileOptionGroups(existing.OptionGroups, in.OptionGroups)
	groupsJSON, err := json.Marshal(groups)
	if err != nil {
		log.Printf("items marshal options: %v", err)
		http.Error(w, "items not writable", http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	// The version is checked again in the WHERE so a write landing between the read above and this UPDATE
	// still loses (0 rows affected, reported as a conflict below).
	res, err := it.db.Exec(`
		UPDATE items SET name = ?, description = ?, price_cents = ?, option_groups_json = ?,
			public = ?, stock_mode = ?, stock_qty = ?, allow_backorder = ?, sort_order = ?, updated_at = ?, updated_by = ?
		WHERE id = ? AND version = ?`,
		in.Name, in.Description, in.PriceCents, string(groupsJSON),
		in.Public, in.StockMode, in.StockQty, in.AllowBackorder, in.SortOrder, now, identityFor(r).username, id, existing.Version)
	if err != nil {
		log.Printf("items update: %v", err)
		http.Error(w, "items not writable", http.StatusInternalServerError)
		return
	}
	n, err := res.RowsAffected()
	if err != nil {
		log.Printf("items rows affected: %v", err)
		http.Error(w, "items not writable", http.StatusInternalServerError)
		return
	}
	if n == 0 {
		writeAPIError(w, http.StatusConflict, itemConflictMessage)
		return
	}

	i, found, err := getItem(it.db, id)
	if err != nil {
		log.Printf("items read after update: %v", err)
		http.Error(w, "items not writable", http.StatusInternalServerError)
		return
	}
	if !found {
		// Can't happen: RowsAffected confirmed the row; the single connection allows no concurrent delete.
		writeAPIError(w, http.StatusNotFound, "item not found")
		return
	}

	// Photos of options the edit removed: best-effort, after the row change committed.
	kept := map[string]bool{}
	for _, f := range optionImageFiles(groups) {
		kept[f] = true
	}
	for _, f := range optionImageFiles(existing.OptionGroups) {
		if !kept[f] {
			removeItemImageIfUnshared(it.db, it.configDir, f)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(i)
}

func (it *items) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	i, found, err := getItem(it.db, id)
	if err != nil {
		log.Printf("items load: %v", err)
		http.Error(w, "items unavailable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "item not found")
		return
	}

	if _, err := it.db.Exec(`DELETE FROM items WHERE id = ?`, id); err != nil {
		log.Printf("items delete: %v", err)
		http.Error(w, "items not writable", http.StatusInternalServerError)
		return
	}

	// Best-effort, after the row is gone: an orphaned file is a smaller problem than a file removed under a
	// row DELETE that then fails. Guarded against sharing because filenames are content-addressed.
	removeItemImageIfUnshared(it.db, it.configDir, i.ImageFile)
	for _, f := range optionImageFiles(i.OptionGroups) {
		removeItemImageIfUnshared(it.db, it.configDir, f)
	}

	w.WriteHeader(http.StatusNoContent)
}

// storeUploadedImage reads the raw request body (not multipart) as an image, gated on http.DetectContentType,
// not the untrusted Content-Type, and writes it to the uploads directory under its content-addressed name
// (the body's sha256, so identical re-uploads are idempotent). On failure it has written the response and
// returns ok false.
func (it *items) storeUploadedImage(w http.ResponseWriter, r *http.Request) (name string, ok bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, itemImageMaxBytes))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "image: at most 5 MiB")
			return "", false
		}
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return "", false
	}

	ext := ""
	switch http.DetectContentType(body) {
	case "image/jpeg":
		ext = ".jpg"
	case "image/png":
		ext = ".png"
	case "image/webp":
		ext = ".webp"
	default:
		writeAPIError(w, http.StatusUnprocessableEntity, "image: must be a JPEG, PNG, or WebP file")
		return "", false
	}

	sum := sha256.Sum256(body)
	name = hex.EncodeToString(sum[:])[:16] + ext

	dir := filepath.Join(it.configDir, "uploads", "items")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("items image mkdir: %v", err)
		http.Error(w, "items not writable", http.StatusInternalServerError)
		return "", false
	}
	if err := os.WriteFile(itemImagePath(it.configDir, name), body, 0o644); err != nil {
		log.Printf("items image write: %v", err)
		http.Error(w, "items not writable", http.StatusInternalServerError)
		return "", false
	}
	return name, true
}

// handleImageUpload stores the body as the item's photo. The old image is removed only after the new file is
// written and the row updated, so a crash is a retry.
func (it *items) handleImageUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, found, err := getItem(it.db, id)
	if err != nil {
		log.Printf("items load for image upload: %v", err)
		http.Error(w, "items unavailable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "item not found")
		return
	}

	name, ok := it.storeUploadedImage(w, r)
	if !ok {
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := it.db.Exec(`UPDATE items SET image_file = ?, updated_at = ?, updated_by = ? WHERE id = ?`, name, now, identityFor(r).username, id); err != nil {
		log.Printf("items image update: %v", err)
		http.Error(w, "items not writable", http.StatusInternalServerError)
		return
	}

	if item.ImageFile != "" && item.ImageFile != name {
		removeItemImageIfUnshared(it.db, it.configDir, item.ImageFile)
	}

	it.writeItem(w, id)
}

// writeItem responds with the stored item after a row change that already succeeded.
func (it *items) writeItem(w http.ResponseWriter, id string) {
	updated, found, err := getItem(it.db, id)
	if err != nil {
		log.Printf("items read after write: %v", err)
		http.Error(w, "items not writable", http.StatusInternalServerError)
		return
	}
	if !found {
		// Can't happen: the UPDATE confirmed the row exists, and the single connection allows no concurrent
		// delete.
		writeAPIError(w, http.StatusNotFound, "item not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updated)
}

// setOptionImage sets (or, with file "", clears) the photo of option optionID on item id and returns the
// file it replaced. found is false when the item or option does not exist.
func (it *items) setOptionImage(id, optionID, file, by string) (old string, found bool, err error) {
	// The read and the write share one transaction. The pool is one connection (see openDB), so the
	// transaction holds it and a concurrent PUT of the item waits instead of interleaving between them;
	// without that, the PUT's groups would be overwritten by this stale copy, or this imageFile lost.
	tx, err := it.db.Begin()
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	item, ok, err := getItem(tx, id)
	if err != nil || !ok {
		return "", false, err
	}
	for gi := range item.OptionGroups {
		for oi := range item.OptionGroups[gi].Options {
			o := &item.OptionGroups[gi].Options[oi]
			if o.ID != optionID {
				continue
			}
			old = o.ImageFile
			o.ImageFile = file
			groupsJSON, err := json.Marshal(item.OptionGroups)
			if err != nil {
				return "", false, err
			}
			now := time.Now().UTC().Format(time.RFC3339)
			if _, err := tx.Exec(`UPDATE items SET option_groups_json = ?, updated_at = ?, updated_by = ? WHERE id = ?`,
				string(groupsJSON), now, by, id); err != nil {
				return "", false, err
			}
			if err := tx.Commit(); err != nil {
				return "", false, err
			}
			return old, true, nil
		}
	}
	return "", false, nil
}

// hasOption reports whether item id has an option optionID; err is a lookup failure.
func (it *items) hasOption(id, optionID string) (item bool, option bool, err error) {
	i, ok, err := getItem(it.db, id)
	if err != nil || !ok {
		return false, false, err
	}
	for _, g := range i.OptionGroups {
		for _, o := range g.Options {
			if o.ID == optionID {
				return true, true, nil
			}
		}
	}
	return true, false, nil
}

// handleOptionImageUpload is POST /api/items/{id}/options/{optionId}/image: same body, limits and naming as
// handleImageUpload, stored on the option.
func (it *items) handleOptionImageUpload(w http.ResponseWriter, r *http.Request) {
	id, optionID := r.PathValue("id"), r.PathValue("optionId")
	hasItem, hasOpt, err := it.hasOption(id, optionID)
	if err != nil {
		log.Printf("items load for option image upload: %v", err)
		http.Error(w, "items unavailable", http.StatusInternalServerError)
		return
	}
	if !hasItem {
		writeAPIError(w, http.StatusNotFound, "item not found")
		return
	}
	if !hasOpt {
		writeAPIError(w, http.StatusNotFound, "option not found")
		return
	}
	name, ok := it.storeUploadedImage(w, r)
	if !ok {
		return
	}
	old, found, err := it.setOptionImage(id, optionID, name, identityFor(r).username)
	if err != nil {
		log.Printf("items option image update: %v", err)
		http.Error(w, "items not writable", http.StatusInternalServerError)
		return
	}
	if !found {
		// The item or option was removed between the check above and the write: drop the new file.
		removeItemImageIfUnshared(it.db, it.configDir, name)
		writeAPIError(w, http.StatusNotFound, "option not found")
		return
	}
	if old != "" && old != name {
		removeItemImageIfUnshared(it.db, it.configDir, old)
	}
	it.writeItem(w, id)
}

// handleOptionImageDelete is DELETE /api/items/{id}/options/{optionId}/image: clears the option's photo.
// Deleting when there is none is a no-op success.
func (it *items) handleOptionImageDelete(w http.ResponseWriter, r *http.Request) {
	id, optionID := r.PathValue("id"), r.PathValue("optionId")
	hasItem, hasOpt, err := it.hasOption(id, optionID)
	if err != nil {
		log.Printf("items load for option image delete: %v", err)
		http.Error(w, "items unavailable", http.StatusInternalServerError)
		return
	}
	if !hasItem {
		writeAPIError(w, http.StatusNotFound, "item not found")
		return
	}
	if !hasOpt {
		writeAPIError(w, http.StatusNotFound, "option not found")
		return
	}
	old, _, err := it.setOptionImage(id, optionID, "", identityFor(r).username)
	if err != nil {
		log.Printf("items option image delete: %v", err)
		http.Error(w, "items not writable", http.StatusInternalServerError)
		return
	}
	removeItemImageIfUnshared(it.db, it.configDir, old)
	it.writeItem(w, id)
}

// itemImageName matches the {16 hex}.{jpg|png|webp} filenames handleImageUpload creates.
// Gating on it before any path join blocks traversal: nothing matching can escape configDir/uploads/items.
var itemImageName = regexp.MustCompile(`^[0-9a-f]{16}\.(jpg|png|webp)$`)

// itemImageHandler serves one item's image, cached aggressively since the filename is content-derived.
// Registered on GET /item-images/{file} on the admin mux (static.go) and the public mux (public.go).
func itemImageHandler(configDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("file")
		if !itemImageName.MatchString(name) {
			http.NotFound(w, r)
			return
		}
		path := itemImagePath(configDir, name)
		// Stat before setting Cache-Control: a missing file's 404 must not carry the year-long immutable
		// header, or a client or CDN caches the 404 for a year.
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		http.ServeFile(w, r, path)
	}
}
