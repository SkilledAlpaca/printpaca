package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

const clientsMaxBytes = 64 * 1024 // 64 KiB

type clients struct {
	db *sql.DB
}

func newClients(db *sql.DB) *clients {
	return &clients{db: db}
}

// Client is the JSON shape served by every clients endpoint.
type Client struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Email           string `json:"email"`
	Phone           string `json:"phone"`
	BillingAddress  string `json:"billingAddress"`
	ShippingAddress string `json:"shippingAddress"`
	Notes           string `json:"notes"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
	// CreatedBy/UpdatedBy follow Quote.CreatedBy: set from identityFor(r), never client-supplied.
	CreatedBy string `json:"createdBy"`
	UpdatedBy string `json:"updatedBy"`
}

// clientInput is the POST/PUT body: Client minus the server-assigned fields.
type clientInput struct {
	Name            string `json:"name"`
	Email           string `json:"email"`
	Phone           string `json:"phone"`
	BillingAddress  string `json:"billingAddress"`
	ShippingAddress string `json:"shippingAddress"`
	Notes           string `json:"notes"`
}

// decodeJSONBody decodes a body capped at maxBytes into T, rejecting unknown fields.
// On failure it has already written the response (413 oversized, 400 otherwise).
func decodeJSONBody[T any](w http.ResponseWriter, r *http.Request, maxBytes int64) (in T, ok bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			writeAPIError(w, http.StatusBadRequest, err.Error())
		}
		var zero T
		return zero, false
	}
	return in, true
}

// validateClientInput trims fields in place and returns one message per problem.
func validateClientInput(in *clientInput) []string {
	in.Name = strings.TrimSpace(in.Name)
	in.Email = strings.TrimSpace(in.Email)
	in.Phone = strings.TrimSpace(in.Phone)
	in.BillingAddress = strings.TrimSpace(in.BillingAddress)
	in.ShippingAddress = strings.TrimSpace(in.ShippingAddress)
	in.Notes = strings.TrimSpace(in.Notes)

	var problems []string
	if in.Name == "" {
		problems = append(problems, "name: required")
	} else if len(in.Name) > 200 {
		problems = append(problems, "name: max 200 characters")
	}
	if len(in.Email) > 200 {
		problems = append(problems, "email: max 200 characters")
	} else if in.Email != "" && !strings.Contains(in.Email, "@") {
		// Deliberately loose: a contact note, not a login.
		problems = append(problems, "email: must contain @")
	}
	if len(in.Phone) > 200 {
		problems = append(problems, "phone: max 200 characters")
	}
	if len(in.BillingAddress) > 2000 {
		problems = append(problems, "billingAddress: max 2000 characters")
	}
	if len(in.ShippingAddress) > 2000 {
		problems = append(problems, "shippingAddress: max 2000 characters")
	}
	if len(in.Notes) > 2000 {
		problems = append(problems, "notes: max 2000 characters")
	}
	return problems
}

// clientEmailExists reports whether email (case-insensitive) belongs to a client on file.
// Send handlers use it so the server never emails an unknown address, even when `to` is supplied.
// An empty client email never matches.
func clientEmailExists(db *sql.DB, email string) (bool, error) {
	var found int
	err := db.QueryRow(`
		SELECT 1 FROM clients WHERE email <> '' AND LOWER(email) = LOWER(?) LIMIT 1`, email,
	).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (c *clients) handleList(w http.ResponseWriter, r *http.Request) {
	page, ok, msg := parsePagination(r)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, msg)
		return
	}

	rows, err := c.db.Query(`
		SELECT id, name, email, phone, billing_address, shipping_address, notes, created_at, updated_at, created_by, updated_by
		FROM clients ORDER BY name COLLATE NOCASE, id`)
	if err != nil {
		log.Printf("clients query: %v", err)
		http.Error(w, "clients unavailable", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// Empty, not nil, so a fresh database serializes "clients":[].
	list := []Client{}
	for rows.Next() {
		var cl Client
		if err := rows.Scan(&cl.ID, &cl.Name, &cl.Email, &cl.Phone, &cl.BillingAddress, &cl.ShippingAddress, &cl.Notes, &cl.CreatedAt, &cl.UpdatedAt, &cl.CreatedBy, &cl.UpdatedBy); err != nil {
			log.Printf("clients scan: %v", err)
			http.Error(w, "clients unavailable", http.StatusInternalServerError)
			return
		}
		list = append(list, cl)
	}
	if err := rows.Err(); err != nil {
		log.Printf("clients read: %v", err)
		http.Error(w, "clients unavailable", http.StatusInternalServerError)
		return
	}

	list = paginate(w, list, page)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Clients []Client `json:"clients"`
	}{Clients: list})
}

func (c *clients) handlePost(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeJSONBody[clientInput](w, r, clientsMaxBytes)
	if !ok {
		return
	}
	if problems := validateClientInput(&in); len(problems) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, problems...)
		return
	}

	by := identityFor(r).username
	now := time.Now().UTC().Format(time.RFC3339)
	cl := Client{
		ID:              newID(),
		Name:            in.Name,
		Email:           in.Email,
		Phone:           in.Phone,
		BillingAddress:  in.BillingAddress,
		ShippingAddress: in.ShippingAddress,
		Notes:           in.Notes,
		CreatedAt:       now,
		UpdatedAt:       now,
		CreatedBy:       by,
		UpdatedBy:       by,
	}
	_, err := c.db.Exec(`
		INSERT INTO clients (id, name, email, phone, billing_address, shipping_address, notes, created_at, updated_at, created_by, updated_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		cl.ID, cl.Name, cl.Email, cl.Phone, cl.BillingAddress, cl.ShippingAddress, cl.Notes, cl.CreatedAt, cl.UpdatedAt, cl.CreatedBy, cl.UpdatedBy)
	if err != nil {
		log.Printf("clients insert: %v", err)
		http.Error(w, "clients not writable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(cl)
}

func (c *clients) handlePut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	in, ok := decodeJSONBody[clientInput](w, r, clientsMaxBytes)
	if !ok {
		return
	}
	if problems := validateClientInput(&in); len(problems) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, problems...)
		return
	}

	now := time.Now().UTC().Format(time.RFC3339)
	res, err := c.db.Exec(`
		UPDATE clients SET name = ?, email = ?, phone = ?, billing_address = ?, shipping_address = ?, notes = ?, updated_at = ?, updated_by = ?
		WHERE id = ?`,
		in.Name, in.Email, in.Phone, in.BillingAddress, in.ShippingAddress, in.Notes, now, identityFor(r).username, id)
	if err != nil {
		log.Printf("clients update: %v", err)
		http.Error(w, "clients not writable", http.StatusInternalServerError)
		return
	}
	n, err := res.RowsAffected()
	if err != nil {
		log.Printf("clients rows affected: %v", err)
		http.Error(w, "clients not writable", http.StatusInternalServerError)
		return
	}
	if n == 0 {
		writeAPIError(w, http.StatusNotFound, "client not found")
		return
	}

	var cl Client
	err = c.db.QueryRow(`
		SELECT id, name, email, phone, billing_address, shipping_address, notes, created_at, updated_at, created_by, updated_by
		FROM clients WHERE id = ?`, id,
	).Scan(&cl.ID, &cl.Name, &cl.Email, &cl.Phone, &cl.BillingAddress, &cl.ShippingAddress, &cl.Notes, &cl.CreatedAt, &cl.UpdatedAt, &cl.CreatedBy, &cl.UpdatedBy)
	if err != nil {
		log.Printf("clients read after update: %v", err)
		http.Error(w, "clients not writable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cl)
}

func (c *clients) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := c.db.Exec(`DELETE FROM clients WHERE id = ?`, id)
	if err != nil {
		log.Printf("clients delete: %v", err)
		http.Error(w, "clients not writable", http.StatusInternalServerError)
		return
	}
	n, err := res.RowsAffected()
	if err != nil {
		log.Printf("clients rows affected: %v", err)
		http.Error(w, "clients not writable", http.StatusInternalServerError)
		return
	}
	if n == 0 {
		writeAPIError(w, http.StatusNotFound, "client not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
