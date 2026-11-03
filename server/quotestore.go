package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"time"
)

// This file implements quote storage and REST: CRUD over `quotes` plus compute.
// Unlike clients.go, pricing outputs never come from the body: POST/PUT recompute them via ComputeQuote.
// `inputs`/`options` stay raw JSON, never rebuilt from Go types: a blank field can leave a NaN.
const quotesMaxBytes = 256 * 1024 // 256 KiB: room for a 20-part order (each part is a full QuoteInputs bag plus hardware lines)

type quotes struct {
	db *sql.DB
}

func newQuotes(db *sql.DB) *quotes {
	return &quotes{db: db}
}

// quoteStatuses is the allowed vocabulary for Quote.Status, mirroring quoteStatus.ts; "expired" is derived.
var quoteStatuses = map[string]bool{"draft": true, "sent": true, "accepted": true, "declined": true}

// Quote is the JSON shape of every quotes endpoint.
// Fields through Inputs/Options are client-writable; fields from Quantity on are server-computed.
type Quote struct {
	ID         string          `json:"id"`
	SavedAt    string          `json:"savedAt"`
	Label      string          `json:"label"`
	ClientID   *string         `json:"clientId"`
	ClientName string          `json:"clientName"`
	Project    string          `json:"project"`
	Reference  string          `json:"reference"`
	Status     string          `json:"status"`
	StatusLog  json.RawMessage `json:"statusLog"`
	InvoicedAs *string         `json:"invoicedAs"`
	// Inputs is the {parts, pricing} bag verbatim; Options is {laborPerUnit}, also
	// verbatim.
	Inputs  json.RawMessage `json:"inputs"`
	Options json.RawMessage `json:"options"`
	// FormState is an opaque snapshot of the editor's form state, echoed verbatim, never decoded server-side
	// (Inputs/Options can't reconstruct printer/material/fee-schedule identity). Always a JSON object:
	// validateQuoteInput rejects non-objects; normalizeJSONObject maps empty/null to "{}".
	FormState json.RawMessage `json:"formState"`

	// Server-computed, read-only outputs: recomputed from Inputs/Options on every POST/PUT, never from the
	// body.
	Quantity              float64  `json:"quantity"`
	PerUnitTotalCents     int64    `json:"perUnitTotalCents"`
	BatchTotalCents       int64    `json:"batchTotalCents"`
	RecommendedPriceCents *int64   `json:"recommendedPriceCents"`
	ResolvedPriceCents    *int64   `json:"resolvedPriceCents"`
	NetProfitCents        *int64   `json:"netProfitCents"`
	AchievedMarginPct     *float64 `json:"achievedMarginPct"`
	Valid                 bool     `json:"valid"`
	// PartCount is the parts in the stored inputs bag: 1 for a single-part quote, 2..20 for a multi-part
	// order. For 2 or more, the output columns describe the order as one unit (see computeQuoteOutputs).
	PartCount int `json:"partCount"`

	// CreatedBy/UpdatedBy are the identity.username that created/last wrote the row ("" for pre-feature
	// rows). Set from identityFor(r), never client-supplied.
	CreatedBy string `json:"createdBy"`
	UpdatedBy string `json:"updatedBy"`
}

// quoteInput is the POST/PUT body: everything client-writable in Quote, minus id, savedAt, and outputs.
type quoteInput struct {
	Label      string          `json:"label"`
	ClientID   *string         `json:"clientId"`
	ClientName string          `json:"clientName"`
	Project    string          `json:"project"`
	Reference  string          `json:"reference"`
	Status     string          `json:"status"`
	StatusLog  json.RawMessage `json:"statusLog"`
	InvoicedAs *string         `json:"invoicedAs"`
	Inputs     json.RawMessage `json:"inputs"`
	Options    json.RawMessage `json:"options"`
	FormState  json.RawMessage `json:"formState"`
}

// validateQuoteInput trims fields in place and returns one message per problem.
// Defaults Status to "draft" and StatusLog to "[]".
func validateQuoteInput(in *quoteInput) []string {
	in.Label = strings.TrimSpace(in.Label)
	in.ClientName = strings.TrimSpace(in.ClientName)
	in.Project = strings.TrimSpace(in.Project)
	in.Reference = strings.TrimSpace(in.Reference)
	in.Status = strings.TrimSpace(in.Status)
	if in.ClientID != nil {
		trimmed := strings.TrimSpace(*in.ClientID)
		if trimmed == "" {
			in.ClientID = nil
		} else {
			in.ClientID = &trimmed
		}
	}
	if in.InvoicedAs != nil {
		trimmed := strings.TrimSpace(*in.InvoicedAs)
		if trimmed == "" {
			in.InvoicedAs = nil
		} else {
			in.InvoicedAs = &trimmed
		}
	}

	var problems []string
	if len(in.Label) > 500 {
		problems = append(problems, "label: max 500 characters")
	}
	if len(in.ClientName) > 200 {
		problems = append(problems, "clientName: max 200 characters")
	}
	if len(in.Project) > 200 {
		problems = append(problems, "project: max 200 characters")
	}
	if len(in.Reference) > 200 {
		problems = append(problems, "reference: max 200 characters")
	}
	if in.ClientID != nil && len(*in.ClientID) > 100 {
		problems = append(problems, "clientId: max 100 characters")
	}
	if in.InvoicedAs != nil && len(*in.InvoicedAs) > 100 {
		problems = append(problems, "invoicedAs: max 100 characters")
	}

	if in.Status == "" {
		in.Status = "draft"
	} else if !quoteStatuses[in.Status] {
		problems = append(problems, "status: must be one of draft, sent, accepted, declined")
	}

	if len(bytes.TrimSpace(in.StatusLog)) == 0 || string(bytes.TrimSpace(in.StatusLog)) == "null" {
		in.StatusLog = json.RawMessage("[]")
	} else {
		var probe []json.RawMessage
		if err := json.Unmarshal(in.StatusLog, &probe); err != nil {
			problems = append(problems, "statusLog: must be a JSON array")
		}
	}

	// A present FormState must be a JSON object; empty, absent, or null are left to normalizeJSONObject.
	if len(bytes.TrimSpace(in.FormState)) != 0 && string(bytes.TrimSpace(in.FormState)) != "null" {
		var probe map[string]json.RawMessage
		if err := json.Unmarshal(in.FormState, &probe); err != nil {
			problems = append(problems, "formState: must be a JSON object")
		}
	}

	if len(bytes.TrimSpace(in.Inputs)) == 0 {
		problems = append(problems, "inputs: required")
	}

	return problems
}

// decodeEngineOptions strictly unmarshals raw into EngineOptions, defaulting when empty or absent.
func decodeEngineOptions(raw json.RawMessage) (EngineOptions, error) {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return DefaultEngineOptions, nil
	}
	var opt EngineOptions
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&opt); err != nil {
		return EngineOptions{}, err
	}
	return opt, nil
}

// storedQuoteInputs is the shape persisted in quotes.inputs: raw inputs and options in one TEXT column.
type storedQuoteInputs struct {
	Inputs    json.RawMessage `json:"inputs"`
	Options   json.RawMessage `json:"options"`
	FormState json.RawMessage `json:"formState"`
}

// quoteOutputs is the recomputed set of server-authoritative output columns, derived by computeQuoteOutputs.
type quoteOutputs struct {
	Quantity              float64
	PerUnitTotalCents     int64
	BatchTotalCents       int64
	RecommendedPriceCents *int64
	ResolvedPriceCents    *int64
	NetProfitCents        *int64
	AchievedMarginPct     *float64
	Valid                 bool
	PartCount             int
}

// outputsFromPricing fills the pricing-derived columns of out.
func outputsFromPricing(out *quoteOutputs, pr *PricingResult) {
	out.Valid = pr.Valid
	recommended := pr.RecommendedPrice
	resolved := pr.ResolvedPrice
	netProfit := pr.NetProfit
	margin := pr.AchievedMarginPct
	out.RecommendedPriceCents = &recommended
	out.ResolvedPriceCents = &resolved
	out.NetProfitCents = &netProfit
	out.AchievedMarginPct = &margin
}

// computeQuoteOutputs decodes rawInputs/rawOptions, runs ComputeOrder, and flattens to the stored columns;
// its error is a decode failure (errTooManyParts answers 422). A 2+ part order is stored as ONE unit at its
// item revenue (quantity 1), so price x quantity stays correct; a 1-part order is stored as the single quote
// it equals.
func computeQuoteOutputs(rawInputs, rawOptions json.RawMessage) (quoteOutputs, error) {
	order, err := decodeQuoteInputs(rawInputs)
	if err != nil {
		return quoteOutputs{}, fmt.Errorf("inputs: %w", err)
	}
	opt, err := decodeEngineOptions(rawOptions)
	if err != nil {
		return quoteOutputs{}, fmt.Errorf("options: %w", err)
	}

	if len(order.Parts) >= 2 {
		ob := ComputeOrder(*order, opt)
		out := quoteOutputs{
			Quantity:          1,
			PerUnitTotalCents: ob.BatchTotal,
			BatchTotalCents:   ob.BatchTotal,
			Valid:             ob.Valid,
			PartCount:         len(order.Parts),
		}
		if ob.Pricing != nil {
			outputsFromPricing(&out, ob.Pricing)
		}
		return out, nil
	}

	part := order.Parts[0]
	qi := part.QuoteInputs
	if pr := order.Pricing; pr != nil {
		pi := pr.pricingInputs()
		pi.SalePriceOverride = part.SalePriceOverride
		qi.Pricing = &pi
	}
	bd := ComputeQuote(qi, opt)
	out := quoteOutputs{
		Quantity:          bd.Quantity,
		PerUnitTotalCents: bd.PerUnitTotal,
		BatchTotalCents:   bd.BatchTotal,
		// valid also requires a usable price when a pricing block is present (PricingResult.Valid folds in
		// bd.Valid); a costed-only quote is valid on cost inputs alone.
		Valid:     bd.Valid,
		PartCount: 1,
	}
	if bd.Pricing != nil {
		outputsFromPricing(&out, bd.Pricing)
	}
	return out, nil
}

// inputsErrorStatus maps a computeQuoteOutputs error to 422 for an over-limit part count, else 400.
func inputsErrorStatus(err error) int {
	if errors.Is(err, errTooManyParts) {
		return http.StatusUnprocessableEntity
	}
	return http.StatusBadRequest
}

// nullable adapts a pointer to sql.Null for a nullable bind parameter: nil binds NULL.
func nullable[T any](v *T) sql.Null[T] {
	if v == nil {
		return sql.Null[T]{}
	}
	return sql.Null[T]{V: *v, Valid: true}
}

// quoteColumns lists the columns every quotes SELECT reads, in scanQuote's order.
const quoteColumns = `id, saved_at, label, client_id, client_name, project, reference, status, status_log, invoiced_as, inputs, quantity, per_unit_total_cents, batch_total_cents, recommended_price_cents, resolved_price_cents, net_profit_cents, achieved_margin_pct, valid, created_by, updated_by, part_count`

// scanQuote reads one row (quoteColumns order) into a Quote, splitting the inputs/options wrapper back out.
func scanQuote(row rowScanner) (Quote, error) {
	var q Quote
	var clientID, invoicedAs sql.NullString
	var statusLog, storedInputs string
	var recommended, resolved, netProfit sql.NullInt64
	var margin sql.NullFloat64
	var validInt int
	if err := row.Scan(
		&q.ID, &q.SavedAt, &q.Label, &clientID, &q.ClientName, &q.Project, &q.Reference,
		&q.Status, &statusLog, &invoicedAs, &storedInputs,
		&q.Quantity, &q.PerUnitTotalCents, &q.BatchTotalCents,
		&recommended, &resolved, &netProfit, &margin, &validInt,
		&q.CreatedBy, &q.UpdatedBy, &q.PartCount,
	); err != nil {
		return Quote{}, err
	}
	q.StatusLog = json.RawMessage(statusLog)
	if clientID.Valid {
		q.ClientID = &clientID.String
	}
	if invoicedAs.Valid {
		q.InvoicedAs = &invoicedAs.String
	}
	if recommended.Valid {
		q.RecommendedPriceCents = &recommended.Int64
	}
	if resolved.Valid {
		q.ResolvedPriceCents = &resolved.Int64
	}
	if netProfit.Valid {
		q.NetProfitCents = &netProfit.Int64
	}
	if margin.Valid {
		q.AchievedMarginPct = &margin.Float64
	}
	q.Valid = validInt != 0

	var stored storedQuoteInputs
	if err := json.Unmarshal([]byte(storedInputs), &stored); err != nil {
		return Quote{}, err
	}
	q.Inputs = stored.Inputs
	q.Options = stored.Options
	q.FormState = stored.FormState
	return q, nil
}

func (q *quotes) handleList(w http.ResponseWriter, r *http.Request) {
	page, ok, msg := parsePagination(r)
	if !ok {
		writeAPIError(w, http.StatusBadRequest, msg)
		return
	}

	rows, err := q.db.Query(`SELECT ` + quoteColumns + ` FROM quotes ORDER BY saved_at DESC, id`)
	if err != nil {
		log.Printf("quotes query: %v", err)
		http.Error(w, "quotes unavailable", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// Empty, not nil, so a fresh database serializes "quotes":[].
	list := []Quote{}
	for rows.Next() {
		one, err := scanQuote(rows)
		if err != nil {
			log.Printf("quotes scan: %v", err)
			http.Error(w, "quotes unavailable", http.StatusInternalServerError)
			return
		}
		list = append(list, one)
	}
	if err := rows.Err(); err != nil {
		log.Printf("quotes read: %v", err)
		http.Error(w, "quotes unavailable", http.StatusInternalServerError)
		return
	}

	list = paginate(w, list, page)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(struct {
		Quotes []Quote `json:"quotes"`
	}{Quotes: list})
}

func (q *quotes) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	row := q.db.QueryRow(`SELECT `+quoteColumns+` FROM quotes WHERE id = ?`, id)
	one, err := scanQuote(row)
	if errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, http.StatusNotFound, "quote not found")
		return
	}
	if err != nil {
		log.Printf("quotes get: %v", err)
		http.Error(w, "quotes unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(one)
}

func (q *quotes) handlePost(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeJSONBody[quoteInput](w, r, quotesMaxBytes)
	if !ok {
		return
	}
	if problems := validateQuoteInput(&in); len(problems) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, problems...)
		return
	}
	out, err := computeQuoteOutputs(in.Inputs, in.Options)
	if err != nil {
		writeAPIError(w, inputsErrorStatus(err), err.Error())
		return
	}

	options := normalizeJSONObject(in.Options)
	formState := normalizeJSONObject(in.FormState)
	stored, err := json.Marshal(storedQuoteInputs{
		Inputs: in.Inputs, Options: options, FormState: formState,
	})
	if err != nil {
		log.Printf("quotes marshal stored inputs: %v", err)
		http.Error(w, "quotes not writable", http.StatusInternalServerError)
		return
	}

	by := identityFor(r).username
	now := quoteVersionNow()
	rec := Quote{
		ID: newID(), SavedAt: now, Label: in.Label, ClientID: in.ClientID, ClientName: in.ClientName,
		Project: in.Project, Reference: in.Reference, Status: in.Status, StatusLog: in.StatusLog,
		InvoicedAs: in.InvoicedAs, Inputs: in.Inputs, Options: options,
		FormState: formState,
		Quantity:  out.Quantity, PerUnitTotalCents: out.PerUnitTotalCents, BatchTotalCents: out.BatchTotalCents,
		RecommendedPriceCents: out.RecommendedPriceCents, ResolvedPriceCents: out.ResolvedPriceCents,
		NetProfitCents: out.NetProfitCents, AchievedMarginPct: out.AchievedMarginPct, Valid: out.Valid,
		PartCount: out.PartCount, CreatedBy: by, UpdatedBy: by,
	}

	_, err = q.db.Exec(`
		INSERT INTO quotes (id, saved_at, label, client_id, client_name, project, reference, status, status_log, invoiced_as, inputs, quantity, per_unit_total_cents, batch_total_cents, recommended_price_cents, resolved_price_cents, net_profit_cents, achieved_margin_pct, valid, created_by, updated_by, part_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		rec.ID, rec.SavedAt, rec.Label, nullable(rec.ClientID), rec.ClientName, rec.Project, rec.Reference,
		rec.Status, string(rec.StatusLog), nullable(rec.InvoicedAs), string(stored),
		rec.Quantity, rec.PerUnitTotalCents, rec.BatchTotalCents,
		nullable(rec.RecommendedPriceCents), nullable(rec.ResolvedPriceCents), nullable(rec.NetProfitCents),
		nullable(rec.AchievedMarginPct), boolToInt(rec.Valid), rec.CreatedBy, rec.UpdatedBy, rec.PartCount)
	if err != nil {
		log.Printf("quotes insert: %v", err)
		http.Error(w, "quotes not writable", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(rec)
}

// quoteConflictMessage is the 409 text for a PUT whose If-Match no longer matches the stored savedAt.
const quoteConflictMessage = "This quote was changed in another tab or by another user."

// quoteVersionLayout is the savedAt format for new writes: UTC with milliseconds, so two saves in the same
// second still carry different versions. Older rows keep their whole-second value, which sorts and parses
// the same way.
const quoteVersionLayout = "2006-01-02T15:04:05.000Z"

func quoteVersionNow() string {
	return time.Now().UTC().Format(quoteVersionLayout)
}

// nextQuoteVersion is the savedAt for an update of a row whose current value is `current`. It is strictly
// later than `current`, even when the clock reads the same millisecond or went backwards, so a stale
// If-Match can never equal a newer row.
func nextQuoteVersion(current string) string {
	now := time.Now().UTC().Truncate(time.Millisecond)
	if prev, err := time.Parse(time.RFC3339, current); err == nil && !now.After(prev) {
		now = prev.Truncate(time.Millisecond).Add(time.Millisecond)
	}
	return now.Format(quoteVersionLayout)
}

// quoteIfMatch is the savedAt the client loaded, from the If-Match header (quoted or bare), or "".
func quoteIfMatch(r *http.Request) string {
	return strings.Trim(strings.TrimSpace(r.Header.Get("If-Match")), `"`)
}

func (q *quotes) handlePut(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	in, ok := decodeJSONBody[quoteInput](w, r, quotesMaxBytes)
	if !ok {
		return
	}
	if problems := validateQuoteInput(&in); len(problems) > 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, problems...)
		return
	}
	out, err := computeQuoteOutputs(in.Inputs, in.Options)
	if err != nil {
		writeAPIError(w, inputsErrorStatus(err), err.Error())
		return
	}

	options := normalizeJSONObject(in.Options)
	stored, err := json.Marshal(storedQuoteInputs{
		Inputs: in.Inputs, Options: options, FormState: normalizeJSONObject(in.FormState),
	})
	if err != nil {
		log.Printf("quotes marshal stored inputs: %v", err)
		http.Error(w, "quotes not writable", http.StatusInternalServerError)
		return
	}

	tx, err := q.db.Begin()
	if err != nil {
		log.Printf("quotes begin: %v", err)
		http.Error(w, "quotes not writable", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	var current string
	if err := tx.QueryRow(`SELECT saved_at FROM quotes WHERE id = ?`, id).Scan(&current); errors.Is(err, sql.ErrNoRows) {
		writeAPIError(w, http.StatusNotFound, "quote not found")
		return
	} else if err != nil {
		log.Printf("quotes read version: %v", err)
		http.Error(w, "quotes not writable", http.StatusInternalServerError)
		return
	}
	// A client that sends the savedAt it loaded gets optimistic concurrency: a different current value means
	// someone else saved first. Callers that omit the header (status changes, imports) keep last-write-wins.
	if expected := quoteIfMatch(r); expected != "" && expected != current {
		writeAPIError(w, http.StatusConflict, quoteConflictMessage)
		return
	}
	now := nextQuoteVersion(current)
	if _, err := tx.Exec(`
		UPDATE quotes SET saved_at = ?, label = ?, client_id = ?, client_name = ?, project = ?, reference = ?,
			status = ?, status_log = ?, invoiced_as = ?, inputs = ?, quantity = ?, per_unit_total_cents = ?,
			batch_total_cents = ?, recommended_price_cents = ?, resolved_price_cents = ?, net_profit_cents = ?,
			achieved_margin_pct = ?, valid = ?, part_count = ?, updated_by = ?
		WHERE id = ?`,
		now, in.Label, nullable(in.ClientID), in.ClientName, in.Project, in.Reference,
		in.Status, string(in.StatusLog), nullable(in.InvoicedAs), string(stored),
		out.Quantity, out.PerUnitTotalCents, out.BatchTotalCents,
		nullable(out.RecommendedPriceCents), nullable(out.ResolvedPriceCents), nullable(out.NetProfitCents),
		nullable(out.AchievedMarginPct), boolToInt(out.Valid), out.PartCount, identityFor(r).username, id); err != nil {
		log.Printf("quotes update: %v", err)
		http.Error(w, "quotes not writable", http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("quotes commit: %v", err)
		http.Error(w, "quotes not writable", http.StatusInternalServerError)
		return
	}

	row := q.db.QueryRow(`SELECT `+quoteColumns+` FROM quotes WHERE id = ?`, id)
	one, err := scanQuote(row)
	if err != nil {
		log.Printf("quotes read after update: %v", err)
		http.Error(w, "quotes not writable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(one)
}

func (q *quotes) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := q.db.Exec(`DELETE FROM quotes WHERE id = ?`, id)
	if err != nil {
		log.Printf("quotes delete: %v", err)
		http.Error(w, "quotes not writable", http.StatusInternalServerError)
		return
	}
	n, err := res.RowsAffected()
	if err != nil {
		log.Printf("quotes rows affected: %v", err)
		http.Error(w, "quotes not writable", http.StatusInternalServerError)
		return
	}
	if n == 0 {
		writeAPIError(w, http.StatusNotFound, "quote not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// normalizeJSONObject substitutes "{}" for an empty or absent raw value, so clients always get an object.
func normalizeJSONObject(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return json.RawMessage("{}")
	}
	return raw
}

// ---------------------------------------------------------------------------
// POST /api/quote/compute
// ---------------------------------------------------------------------------

// quoteComputeInput is the POST /api/quote/compute body: the {parts, pricing} inputs bag and engine options.
type quoteComputeInput struct {
	Inputs  json.RawMessage `json:"inputs"`
	Options json.RawMessage `json:"options"`
}

// handleQuoteCompute implements POST /api/quote/compute, price on demand: a {parts, pricing} bag answers an
// OrderBreakdown. The response is freshly computed, not an echo of client JSON, so sanitizeOrderForJSON runs
// first.
func handleQuoteCompute(w http.ResponseWriter, r *http.Request) {
	in, ok := decodeJSONBody[quoteComputeInput](w, r, quotesMaxBytes)
	if !ok {
		return
	}
	if len(bytes.TrimSpace(in.Inputs)) == 0 {
		writeAPIError(w, http.StatusUnprocessableEntity, "inputs: required")
		return
	}

	order, err := decodeQuoteInputs(in.Inputs)
	if err != nil {
		writeAPIError(w, inputsErrorStatus(err), "inputs: "+err.Error())
		return
	}
	opt, err := decodeEngineOptions(in.Options)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "options: "+err.Error())
		return
	}

	ob := ComputeOrder(*order, opt)
	sanitizeOrderForJSON(&ob)
	body, err := json.Marshal(ob)
	if err != nil {
		log.Printf("quote compute: marshal: %v", err)
		http.Error(w, "compute unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// sanitizeBreakdownForJSON replaces NaN/Inf in LineItem.Inputs maps with nil (JSON null), as JS's
// JSON.stringify does; encoding/json refuses them. An incomplete breakdown (blank weight
// mid-edit) carries them legitimately. No other field can, so this is the only sanitization point.
func sanitizeBreakdownForJSON(bd *CostBreakdown) {
	for i := range bd.PerUnit {
		for k, v := range bd.PerUnit[i].Inputs {
			if f, ok := v.(float64); ok && (math.IsNaN(f) || math.IsInf(f, 0)) {
				bd.PerUnit[i].Inputs[k] = nil
			}
		}
	}
}
