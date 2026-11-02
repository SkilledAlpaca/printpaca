package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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
const maxItemOrderQty = 100

const itemColumns = `id, name, description, price_cents, option_label, options_json,
  image_file, public, stock_mode, stock_qty, allow_backorder, sort_order, created_at, updated_at, created_by, updated_by`

// ItemOption is one selectable variant (e.g. a color), stored as JSON in items.options_json: small, and
// always read and written with the item.
type ItemOption struct {
	Name       string `json:"name"`
	DeltaCents int    `json:"deltaCents"`
}

// Item is the JSON shape of every items endpoint. OptionLabel is empty exactly when Options is.
type Item struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description"`
	PriceCents  int          `json:"priceCents"`
	OptionLabel string       `json:"optionLabel"`
	Options     []ItemOption `json:"options"`
	ImageFile   string       `json:"imageFile"`
	Public      bool         `json:"public"`
	StockMode   string       `json:"stockMode"`
	StockQty    int          `json:"stockQty"`
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
}

// itemInput is the POST/PUT body: Item minus the server-assigned id, image file, and timestamps.
type itemInput struct {
	Name           string       `json:"name"`
	Description    string       `json:"description"`
	PriceCents     int          `json:"priceCents"`
	OptionLabel    string       `json:"optionLabel"`
	Options        []ItemOption `json:"options"`
	Public         bool         `json:"public"`
	StockMode      string       `json:"stockMode"`
	StockQty       int          `json:"stockQty"`
	AllowBackorder bool         `json:"allowBackorder"`
	SortOrder      int          `json:"sortOrder"`
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

// removeItemImageIfUnshared removes file unless another item still points at it.
// Filenames are content-addressed (sha256), so items uploaded with identical bytes share one file.
// Called after the row change that stops referencing file has committed.
func removeItemImageIfUnshared(db *sql.DB, configDir, id, file string) {
	if file == "" {
		return
	}
	var one int
	err := db.QueryRow(`SELECT 1 FROM items WHERE image_file = ? AND id <> ? LIMIT 1`, file, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		removeItemImage(configDir, file)
		return
	}
	if err != nil {
		log.Printf("items check shared image: %v", err)
	}
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

	in.OptionLabel = strings.TrimSpace(in.OptionLabel)
	if (in.OptionLabel == "") != (len(in.Options) == 0) {
		problems = append(problems, "options: optionLabel and options must be set together")
	}
	if len(in.Options) > 20 {
		problems = append(problems, "options: at most 20")
	}
	seen := map[string]bool{}
	for i := range in.Options {
		in.Options[i].Name = strings.TrimSpace(in.Options[i].Name)
		n := in.Options[i].Name
		if n == "" || len(n) > 100 {
			problems = append(problems, "options: names required, at most 100 characters")
			break
		}
		if seen[n] {
			problems = append(problems, "options: duplicate name "+n)
			break
		}
		seen[n] = true
		if in.Options[i].DeltaCents < -100_000_000 || in.Options[i].DeltaCents > 100_000_000 {
			// Bounded before the sum below: an unbounded DeltaCents could wrap PriceCents+DeltaCents positive,
			// bypassing the negative-price check, or overflow the order total.
			problems = append(problems, "options: "+n+" deltaCents must be between -100000000 and 100000000")
			break
		}
		if in.PriceCents+in.Options[i].DeltaCents < 0 {
			problems = append(problems, "options: "+n+" makes the price negative")
			break
		}
	}
	return problems
}

// scanItem scans one items row (itemColumns order) and unmarshals options_json; row may be a Row or Rows.
func scanItem(row interface{ Scan(...any) error }) (Item, error) {
	var it Item
	var optionsJSON string
	if err := row.Scan(&it.ID, &it.Name, &it.Description, &it.PriceCents, &it.OptionLabel, &optionsJSON,
		&it.ImageFile, &it.Public, &it.StockMode, &it.StockQty, &it.AllowBackorder, &it.SortOrder, &it.CreatedAt, &it.UpdatedAt,
		&it.CreatedBy, &it.UpdatedBy); err != nil {
		return Item{}, err
	}
	if err := json.Unmarshal([]byte(optionsJSON), &it.Options); err != nil {
		return Item{}, err
	}
	if it.Options == nil {
		it.Options = []ItemOption{}
	}
	return it, nil
}

// getItem loads one item by id; package-level so the public gallery handlers can use it.
// Returns (Item{}, false, nil) when no row matches.
func getItem(db *sql.DB, id string) (Item, bool, error) {
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

	optionsJSON, err := json.Marshal(in.Options)
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
		OptionLabel:    in.OptionLabel,
		Options:        in.Options,
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
	}
	if i.Options == nil {
		i.Options = []ItemOption{}
	}
	_, err = it.db.Exec(`
		INSERT INTO items (id, name, description, price_cents, option_label, options_json,
			image_file, public, stock_mode, stock_qty, allow_backorder, sort_order, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		i.ID, i.Name, i.Description, i.PriceCents, i.OptionLabel, string(optionsJSON),
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

	optionsJSON, err := json.Marshal(in.Options)
	if err != nil {
		log.Printf("items marshal options: %v", err)
		http.Error(w, "items not writable", http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := it.db.Exec(`
		UPDATE items SET name = ?, description = ?, price_cents = ?, option_label = ?, options_json = ?,
			public = ?, stock_mode = ?, stock_qty = ?, allow_backorder = ?, sort_order = ?, updated_at = ?, updated_by = ?
		WHERE id = ?`,
		in.Name, in.Description, in.PriceCents, in.OptionLabel, string(optionsJSON),
		in.Public, in.StockMode, in.StockQty, in.AllowBackorder, in.SortOrder, now, identityFor(r).username, id)
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
		writeAPIError(w, http.StatusNotFound, "item not found")
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
	removeItemImageIfUnshared(it.db, it.configDir, id, i.ImageFile)

	w.WriteHeader(http.StatusNoContent)
}

// handleImageUpload reads the raw body (not multipart) as the new image, gated on http.DetectContentType, not
// the untrusted Content-Type. The name is the body's sha256, so identical re-uploads are idempotent.
// The old image is removed only after the new file is written and the row updated, so a crash is a retry.
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

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, itemImageMaxBytes))
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "image: at most 5 MiB")
			return
		}
		writeAPIError(w, http.StatusBadRequest, err.Error())
		return
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
		return
	}

	sum := sha256.Sum256(body)
	name := hex.EncodeToString(sum[:])[:16] + ext

	dir := filepath.Join(it.configDir, "uploads", "items")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("items image mkdir: %v", err)
		http.Error(w, "items not writable", http.StatusInternalServerError)
		return
	}
	if err := os.WriteFile(itemImagePath(it.configDir, name), body, 0o644); err != nil {
		log.Printf("items image write: %v", err)
		http.Error(w, "items not writable", http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := it.db.Exec(`UPDATE items SET image_file = ?, updated_at = ?, updated_by = ? WHERE id = ?`, name, now, identityFor(r).username, id); err != nil {
		log.Printf("items image update: %v", err)
		http.Error(w, "items not writable", http.StatusInternalServerError)
		return
	}

	if item.ImageFile != "" && item.ImageFile != name {
		removeItemImageIfUnshared(it.db, it.configDir, id, item.ImageFile)
	}

	updated, found, err := getItem(it.db, id)
	if err != nil {
		log.Printf("items read after image upload: %v", err)
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
