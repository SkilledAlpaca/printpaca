package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Shop categories (v1.2.0). An item has zero or one category. A category has a name, a URL slug
// (#/category/<slug>), a manual order, and a hidden flag. Managed with the "items" permission.

const (
	categoriesMaxBytes = 4 * 1024
	maxCategories      = 50
	categoryNameMax    = 60
	categorySlugMax    = 40
)

// categorySlugRe is the slug shape: lowercase letters and digits in runs joined by single hyphens.
var categorySlugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Category is the JSON shape of every categories endpoint. ItemCount counts all items in the category,
// public or not (admin only; the public payload carries slug and name).
type Category struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	SortOrder int    `json:"sortOrder"`
	Hidden    bool   `json:"hidden"`
	ItemCount int    `json:"itemCount"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// categoryInput is the POST/PUT body. An empty Slug derives one from the name on POST and keeps the stored
// slug on PUT.
type categoryInput struct {
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	SortOrder int    `json:"sortOrder"`
	Hidden    bool   `json:"hidden"`
}

const categoryColumns = `c.id, c.name, c.slug, c.sort_order, c.hidden, c.created_at, c.updated_at,
  (SELECT COUNT(*) FROM items i WHERE i.category_id = c.id)`

func scanCategory(row interface{ Scan(...any) error }) (Category, error) {
	var c Category
	err := row.Scan(&c.ID, &c.Name, &c.Slug, &c.SortOrder, &c.Hidden, &c.CreatedAt, &c.UpdatedAt, &c.ItemCount)
	return c, err
}

// slugify lowercases name, turns every run of other characters into one hyphen, and trims hyphens and
// length. It returns "" when nothing usable is left (a name of only symbols or non-Latin letters).
func slugify(name string) string {
	var b strings.Builder
	pendingHyphen := false
	for _, r := range strings.ToLower(name) {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			if pendingHyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingHyphen = false
			b.WriteRune(r)
		} else {
			pendingHyphen = true
		}
	}
	s := b.String()
	if len(s) > categorySlugMax {
		s = strings.TrimRight(s[:categorySlugMax], "-")
	}
	return s
}

// validateCategoryInput trims in place and returns one message per problem. The slug rules apply only to a
// slug the client supplied.
func validateCategoryInput(in *categoryInput) []string {
	var problems []string
	in.Name = strings.TrimSpace(in.Name)
	in.Slug = strings.TrimSpace(in.Slug)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > categoryNameMax {
		problems = append(problems, fmt.Sprintf("name: required, at most %d characters", categoryNameMax))
	} else if msg := plainTextProblem("name", in.Name, categoryNameMax); msg != "" {
		problems = append(problems, msg)
	}
	if in.Slug != "" && (len(in.Slug) > categorySlugMax || !categorySlugRe.MatchString(in.Slug)) {
		problems = append(problems, fmt.Sprintf("slug: use lowercase letters, numbers, and single hyphens, at most %d characters", categorySlugMax))
	}
	if in.SortOrder < -1_000_000 || in.SortOrder > 1_000_000 {
		problems = append(problems, "sortOrder: must be between -1000000 and 1000000")
	}
	return problems
}

type categories struct {
	db *sql.DB
}

func newCategories(db *sql.DB) *categories { return &categories{db: db} }

// listCategories returns every category in shop order (sort_order, name).
func listCategories(db rowsQuerier) ([]Category, error) {
	rows, err := db.Query(`SELECT ` + categoryColumns + ` FROM categories c ORDER BY c.sort_order, c.name COLLATE NOCASE, c.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Category{}
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, c)
	}
	return list, rows.Err()
}

// rowsQuerier is the Query half of *sql.DB and *sql.Tx.
type rowsQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

func getCategory(db rowQuerier, id string) (Category, bool, error) {
	c, err := scanCategory(db.QueryRow(`SELECT `+categoryColumns+` FROM categories c WHERE c.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Category{}, false, nil
	}
	return c, err == nil, err
}

func (c *categories) handleList(w http.ResponseWriter, r *http.Request) {
	list, err := listCategories(c.db)
	if err != nil {
		log.Printf("categories query: %v", err)
		http.Error(w, "categories unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Categories []Category `json:"categories"`
	}{Categories: list})
}

// slugTaken reports whether another category (not exceptID) uses slug or name (names compare
// case-insensitively, so two chips never read the same).
func (c *categories) conflictFor(slug, name, exceptID string) (string, error) {
	var one int
	err := c.db.QueryRow(`SELECT 1 FROM categories WHERE slug = ? AND id != ?`, slug, exceptID).Scan(&one)
	if err == nil {
		return "slug: another category already uses this slug", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	err = c.db.QueryRow(`SELECT 1 FROM categories WHERE lower(name) = lower(?) AND id != ?`, name, exceptID).Scan(&one)
	if err == nil {
		return "name: another category already has this name", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return "", nil
}

// uniqueSlug derives a free slug from name: slugify, then -2, -3, ... on collision.
func (c *categories) uniqueSlug(name string) (string, error) {
	base := slugify(name)
	if base == "" {
		base = "category"
	}
	slug := base
	for n := 2; n < 1000; n++ {
		var one int
		err := c.db.QueryRow(`SELECT 1 FROM categories WHERE slug = ?`, slug).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return slug, nil
		}
		if err != nil {
			return "", err
		}
		suffix := fmt.Sprintf("-%d", n)
		trimmed := base
		if len(trimmed)+len(suffix) > categorySlugMax {
			trimmed = strings.TrimRight(trimmed[:categorySlugMax-len(suffix)], "-")
		}
		slug = trimmed + suffix
	}
	return "", errors.New("no free slug")
}

func (c *categories) handlePost(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeJSONBody[categoryInput](w, r, categoriesMaxBytes)
	if !ok {
		return
	}
	if problems := validateCategoryInput(&in); len(problems) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, problems...)
		return
	}
	var count int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM categories`).Scan(&count); err != nil {
		log.Printf("categories count: %v", err)
		http.Error(w, "categories not writable", http.StatusInternalServerError)
		return
	}
	if count >= maxCategories {
		writeAPIError(w, http.StatusUnprocessableEntity, fmt.Sprintf("categories: at most %d", maxCategories))
		return
	}
	slug := in.Slug
	if slug == "" {
		var err error
		if slug, err = c.uniqueSlug(in.Name); err != nil {
			log.Printf("categories slug: %v", err)
			http.Error(w, "categories not writable", http.StatusInternalServerError)
			return
		}
	}
	if msg, err := c.conflictFor(slug, in.Name, ""); err != nil {
		log.Printf("categories conflict check: %v", err)
		http.Error(w, "categories not writable", http.StatusInternalServerError)
		return
	} else if msg != "" {
		writeAPIError(w, http.StatusConflict, msg)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	id := newID()
	if _, err := c.db.Exec(`INSERT INTO categories (id, name, slug, sort_order, hidden, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, in.Name, slug, in.SortOrder, in.Hidden, now, now); err != nil {
		log.Printf("categories insert: %v", err)
		http.Error(w, "categories not writable", http.StatusInternalServerError)
		return
	}
	c.writeOne(w, id, http.StatusCreated)
}

func (c *categories) handlePut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	in, ok := decodeJSONBody[categoryInput](w, r, categoriesMaxBytes)
	if !ok {
		return
	}
	if problems := validateCategoryInput(&in); len(problems) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, problems...)
		return
	}
	existing, found, err := getCategory(c.db, id)
	if err != nil {
		log.Printf("categories load: %v", err)
		http.Error(w, "categories unavailable", http.StatusInternalServerError)
		return
	}
	if !found {
		writeAPIError(w, http.StatusNotFound, "category not found")
		return
	}
	slug := in.Slug
	if slug == "" {
		slug = existing.Slug
	}
	if msg, err := c.conflictFor(slug, in.Name, id); err != nil {
		log.Printf("categories conflict check: %v", err)
		http.Error(w, "categories not writable", http.StatusInternalServerError)
		return
	} else if msg != "" {
		writeAPIError(w, http.StatusConflict, msg)
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := c.db.Exec(`UPDATE categories SET name = ?, slug = ?, sort_order = ?, hidden = ?, updated_at = ? WHERE id = ?`,
		in.Name, slug, in.SortOrder, in.Hidden, now, id); err != nil {
		log.Printf("categories update: %v", err)
		http.Error(w, "categories not writable", http.StatusInternalServerError)
		return
	}
	c.writeOne(w, id, http.StatusOK)
}

// handleDelete removes the category and unlinks its items in one transaction (each unlinked item's version
// moves, so a stale editor save conflicts instead of silently re-linking).
func (c *categories) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tx, err := c.db.Begin()
	if err != nil {
		log.Printf("categories begin: %v", err)
		http.Error(w, "categories not writable", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	if _, found, err := getCategory(tx, id); err != nil {
		log.Printf("categories load: %v", err)
		http.Error(w, "categories unavailable", http.StatusInternalServerError)
		return
	} else if !found {
		writeAPIError(w, http.StatusNotFound, "category not found")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`UPDATE items SET category_id = NULL, updated_at = ?, updated_by = ? WHERE category_id = ?`,
		now, identityFor(r).username, id); err != nil {
		log.Printf("categories unlink items: %v", err)
		http.Error(w, "categories not writable", http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(`DELETE FROM categories WHERE id = ?`, id); err != nil {
		log.Printf("categories delete: %v", err)
		http.Error(w, "categories not writable", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("categories commit: %v", err)
		http.Error(w, "categories not writable", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *categories) writeOne(w http.ResponseWriter, id string, status int) {
	cat, found, err := getCategory(c.db, id)
	if err != nil || !found {
		log.Printf("categories read after write: %v (found %v)", err, found)
		http.Error(w, "categories not writable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(cat)
}
