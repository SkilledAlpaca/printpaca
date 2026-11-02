package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

// validQuoteInputsJSON is a hand-computed one-part bag: weightG=100 @ 20/kg = 200c material, 1h @ 100W @ 0.1/kWh = 1c electricity, everything else zero, quantity 1, so perUnitTotal=batchTotal=201c.
// At 40% margin, 0% tax, no shipping or fees: recommendedPrice = roundCents(201/0.6) = 335c, resolvedPrice 335, netProfit 134c, achievedMarginPct 40 exactly.
const validQuoteInputsJSON = `{"parts":[{"label":"Part 1","weightG":100,"pricePerKg":20,"wastePct":0,"timeHours":1,"timeMinutes":0,"watts":100,` +
	`"energy":{"mode":"flat","flatRate":0.1},"failureRatePct":0,"prepMinutes":0,"laborRate":0,"machineCost":0,` +
	`"machineLifeHours":0,"monthlyFixed":0,"monthlyHours":0,"quantity":1}],` +
	`"pricing":{"strategy":"margin","marginPct":40,"taxRatePct":0,"shippingCharge":0,"shippingCost":0}}`

func quoteBodyFor(extra string) string {
	body := `{"label":"Benchy","clientName":"Beta","project":"P1","reference":"R1","status":"draft","statusLog":[],` +
		`"inputs":` + validQuoteInputsJSON + `,"options":{}`
	if extra != "" {
		body += "," + extra
	}
	return body + "}"
}

func postQuote(t *testing.T, mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("POST", "/api/quotes", strings.NewReader(body)))
}

func putQuote(t *testing.T, mux *http.ServeMux, id, body string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("PUT", "/api/quotes/"+id, strings.NewReader(body)))
}

func mustUnmarshalQuote(t *testing.T, rec *httptest.ResponseRecorder) Quote {
	t.Helper()
	var q Quote
	if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil {
		t.Fatalf("unmarshal quote: %v (body %s)", err, rec.Body.String())
	}
	return q
}

// formState is stored/echoed as opaque bytes with no guaranteed key ordering, so compare parsed JSON, not raw strings.
func assertJSONObjectEqual(t *testing.T, got json.RawMessage, want string) {
	t.Helper()
	var gotVal, wantVal any
	if err := json.Unmarshal(got, &gotVal); err != nil {
		t.Fatalf("unmarshal got: %v (%s)", err, got)
	}
	if err := json.Unmarshal([]byte(want), &wantVal); err != nil {
		t.Fatalf("unmarshal want: %v (%s)", err, want)
	}
	if !reflect.DeepEqual(gotVal, wantVal) {
		t.Fatalf("json mismatch: got %s, want %s", got, want)
	}
}

func TestQuotesGetEmptyIsEmptySlice(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := serveReq(mux, httptest.NewRequest("GET", "/api/quotes", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET want 200, got %d", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"quotes":[]}` {
		t.Fatalf("body = %q, want {\"quotes\":[]}", got)
	}
}

// POST returns every server-computed output column from ComputeQuote, matching validQuoteInputsJSON's hand-computed values, plus server-assigned id/savedAt.
func TestQuotesPostComputesAuthoritativeOutputs(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postQuote(t, mux, quoteBodyFor(""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d %s", rec.Code, rec.Body.String())
	}
	q := mustUnmarshalQuote(t, rec)

	if q.ID == "" || q.SavedAt == "" {
		t.Fatalf("quote missing server-assigned id/savedAt: %+v", q)
	}
	if q.Label != "Benchy" || q.ClientName != "Beta" || q.Project != "P1" || q.Reference != "R1" || q.Status != "draft" {
		t.Fatalf("quote client-writable fields mismatch: %+v", q)
	}
	if q.Quantity != 1 {
		t.Errorf("quantity = %v, want 1", q.Quantity)
	}
	if q.PerUnitTotalCents != 201 {
		t.Errorf("perUnitTotalCents = %d, want 201", q.PerUnitTotalCents)
	}
	if q.BatchTotalCents != 201 {
		t.Errorf("batchTotalCents = %d, want 201", q.BatchTotalCents)
	}
	if q.RecommendedPriceCents == nil || *q.RecommendedPriceCents != 335 {
		t.Errorf("recommendedPriceCents = %v, want 335", q.RecommendedPriceCents)
	}
	if q.ResolvedPriceCents == nil || *q.ResolvedPriceCents != 335 {
		t.Errorf("resolvedPriceCents = %v, want 335", q.ResolvedPriceCents)
	}
	if q.NetProfitCents == nil || *q.NetProfitCents != 134 {
		t.Errorf("netProfitCents = %v, want 134", q.NetProfitCents)
	}
	if q.AchievedMarginPct == nil || *q.AchievedMarginPct != 40 {
		t.Errorf("achievedMarginPct = %v, want 40", q.AchievedMarginPct)
	}
	if !q.Valid {
		t.Errorf("valid = false, want true")
	}

	// GET by id serves the identical record back.
	rec = serveReq(mux, httptest.NewRequest("GET", "/api/quotes/"+q.ID, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET by id want 200, got %d %s", rec.Code, rec.Body.String())
	}
	got := mustUnmarshalQuote(t, rec)
	if got.PerUnitTotalCents != q.PerUnitTotalCents || got.ResolvedPriceCents == nil || *got.ResolvedPriceCents != 335 {
		t.Fatalf("GET by id = %+v, want it to match the POST response", got)
	}
}

// id/savedAt/outputs are NOT client-writable: quoteInput has no "id", so DisallowUnknownFields rejects it; the server alone assigns identity.
func TestQuotesPostRejectsClientSuppliedID(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postQuote(t, mux, quoteBodyFor(`"id":"client-chosen-id"`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST with a client-supplied id want 400, got %d %s", rec.Code, rec.Body.String())
	}
}

// PUT recomputes: changing marginPct changes every price-derived output and bumps savedAt (last-write semantics).
func TestQuotesPutRecomputesOnChange(t *testing.T) {
	dir := t.TempDir()
	mux := newTestMux(t, dir)
	created := mustUnmarshalQuote(t, postQuote(t, mux, quoteBodyFor("")))

	db := openTestDB(t, dir)
	knownSavedAt := "2020-01-01T00:00:00Z"
	if _, err := db.Exec(`UPDATE quotes SET saved_at = ? WHERE id = ?`, knownSavedAt, created.ID); err != nil {
		t.Fatalf("seed saved_at: %v", err)
	}

	updatedInputs := strings.Replace(validQuoteInputsJSON, `"marginPct":40`, `"marginPct":60`, 1)
	body := `{"label":"Benchy v2","clientName":"Beta","project":"P1","reference":"R1","status":"sent","statusLog":[],` +
		`"inputs":` + updatedInputs + `,"options":{}}`
	rec := putQuote(t, mux, created.ID, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT want 200, got %d %s", rec.Code, rec.Body.String())
	}
	updated := mustUnmarshalQuote(t, rec)

	if updated.Label != "Benchy v2" || updated.Status != "sent" {
		t.Fatalf("PUT did not apply client-writable fields: %+v", updated)
	}
	if updated.SavedAt == knownSavedAt {
		t.Fatalf("savedAt was not bumped by PUT")
	}
	// 201/(1-0.6) = 502.5 -> roundCents ties-away-from-zero -> 503.
	if updated.RecommendedPriceCents == nil || *updated.RecommendedPriceCents != 503 {
		t.Errorf("recommendedPriceCents after margin change = %v, want 503", updated.RecommendedPriceCents)
	}
	if updated.ResolvedPriceCents == nil || *updated.ResolvedPriceCents != 503 {
		t.Errorf("resolvedPriceCents after margin change = %v, want 503", updated.ResolvedPriceCents)
	}

	rec = putQuote(t, mux, "does-not-exist", body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("PUT unknown id want 404, got %d", rec.Code)
	}
}

// formState is an opaque editor snapshot, stored and echoed verbatim through POST/GET/PUT and never validated; it can't be rebuilt from the engine inputs.
func TestQuotesFormStateRoundTrips(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	formState := `{"printerId":"p1","materialId":"m1"}`
	rec := postQuote(t, mux, quoteBodyFor(`"formState":`+formState))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d %s", rec.Code, rec.Body.String())
	}
	created := mustUnmarshalQuote(t, rec)
	assertJSONObjectEqual(t, created.FormState, formState)

	getRec := serveReq(mux, httptest.NewRequest("GET", "/api/quotes/"+created.ID, nil))
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET want 200, got %d %s", getRec.Code, getRec.Body.String())
	}
	assertJSONObjectEqual(t, mustUnmarshalQuote(t, getRec).FormState, formState)

	newFormState := `{"printerId":"p2","materialId":"m2","feeScheduleName":"standard"}`
	body := `{"label":"Benchy","clientName":"Beta","project":"P1","reference":"R1","status":"draft","statusLog":[],` +
		`"inputs":` + validQuoteInputsJSON + `,"options":{},"formState":` + newFormState + `}`
	putRec := putQuote(t, mux, created.ID, body)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT want 200, got %d %s", putRec.Code, putRec.Body.String())
	}
	assertJSONObjectEqual(t, mustUnmarshalQuote(t, putRec).FormState, newFormState)
}

// An absent formState stores and echoes as {} on POST and PUT, never null or "", so clients can rely on an object.
func TestQuotesFormStateDefaultsToEmptyObject(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postQuote(t, mux, quoteBodyFor(""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d %s", rec.Code, rec.Body.String())
	}
	created := mustUnmarshalQuote(t, rec)
	if strings.TrimSpace(string(created.FormState)) != "{}" {
		t.Errorf("POST formState = %s, want {}", created.FormState)
	}

	body := `{"label":"Benchy","clientName":"Beta","project":"P1","reference":"R1","status":"draft","statusLog":[],` +
		`"inputs":` + validQuoteInputsJSON + `,"options":{}}`
	putRec := putQuote(t, mux, created.ID, body)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT want 200, got %d %s", putRec.Code, putRec.Body.String())
	}
	if got := strings.TrimSpace(string(mustUnmarshalQuote(t, putRec).FormState)); got != "{}" {
		t.Errorf("PUT formState = %s, want {}", got)
	}
}

func TestQuotesDeleteThenSecondDeleteIs404(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	created := mustUnmarshalQuote(t, postQuote(t, mux, quoteBodyFor("")))

	rec := serveReq(mux, httptest.NewRequest("DELETE", "/api/quotes/"+created.ID, nil))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE want 204, got %d %s", rec.Code, rec.Body.String())
	}

	rec = serveReq(mux, httptest.NewRequest("DELETE", "/api/quotes/"+created.ID, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("second DELETE want 404, got %d", rec.Code)
	}

	rec = serveReq(mux, httptest.NewRequest("GET", "/api/quotes/"+created.ID, nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET deleted quote want 404, got %d", rec.Code)
	}
}

// Pins the newest-first list order by editing saved_at directly, so it doesn't depend on two POSTs landing in different RFC3339 seconds.
func TestQuotesListOrdersNewestFirst(t *testing.T) {
	dir := t.TempDir()
	mux := newTestMux(t, dir)
	a := mustUnmarshalQuote(t, postQuote(t, mux, quoteBodyFor(`"label":"A"`)))
	b := mustUnmarshalQuote(t, postQuote(t, mux, quoteBodyFor(`"label":"B"`)))

	db := openTestDB(t, dir)
	if _, err := db.Exec(`UPDATE quotes SET saved_at = ? WHERE id = ?`, "2020-01-01T00:00:00Z", a.ID); err != nil {
		t.Fatalf("seed a saved_at: %v", err)
	}
	if _, err := db.Exec(`UPDATE quotes SET saved_at = ? WHERE id = ?`, "2025-01-01T00:00:00Z", b.ID); err != nil {
		t.Fatalf("seed b saved_at: %v", err)
	}

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/quotes", nil))
	var list struct {
		Quotes []Quote `json:"quotes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list.Quotes) != 2 || list.Quotes[0].Label != "B" || list.Quotes[1].Label != "A" {
		t.Fatalf("quotes = %+v, want [B, A] (newest saved_at first)", list.Quotes)
	}
}

// TestQuotesListPaginationLimit pins ?limit=: capped rows in the existing
// order, plus X-Total-Count naming the full row count.
func TestQuotesListPaginationLimit(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	postQuote(t, mux, quoteBodyFor(`"label":"A"`))
	postQuote(t, mux, quoteBodyFor(`"label":"B"`))
	postQuote(t, mux, quoteBodyFor(`"label":"C"`))

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/quotes?limit=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("X-Total-Count"), "3"; got != want {
		t.Errorf("X-Total-Count = %q, want %q", got, want)
	}
	var list struct {
		Quotes []Quote `json:"quotes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list.Quotes) != 2 {
		t.Fatalf("quotes len = %d, want 2", len(list.Quotes))
	}
}

// TestQuotesListPaginationOffset pins ?offset=: skips rows in the existing
// order and still sets X-Total-Count.
func TestQuotesListPaginationOffset(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	postQuote(t, mux, quoteBodyFor(`"label":"A"`))
	postQuote(t, mux, quoteBodyFor(`"label":"B"`))
	postQuote(t, mux, quoteBodyFor(`"label":"C"`))

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/quotes?offset=2", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body = %s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("X-Total-Count"), "3"; got != want {
		t.Errorf("X-Total-Count = %q, want %q", got, want)
	}
	var list struct {
		Quotes []Quote `json:"quotes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list.Quotes) != 1 {
		t.Fatalf("quotes len = %d, want 1 (3 total minus the 2 skipped by offset)", len(list.Quotes))
	}
}

// TestQuotesListAbsentPaginationParamsReturnsAllUnchanged pins the
// backward-compatibility guarantee: no query params means every row, no
// X-Total-Count header, exactly as before pagination existed.
func TestQuotesListAbsentPaginationParamsReturnsAllUnchanged(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	postQuote(t, mux, quoteBodyFor(`"label":"A"`))
	postQuote(t, mux, quoteBodyFor(`"label":"B"`))

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/quotes", nil))
	if got := rec.Header().Get("X-Total-Count"); got != "" {
		t.Errorf("X-Total-Count = %q, want unset when pagination isn't used", got)
	}
	var list struct {
		Quotes []Quote `json:"quotes"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list.Quotes) != 2 {
		t.Fatalf("quotes len = %d, want 2", len(list.Quotes))
	}
}

// TestQuotesListInvalidPaginationParamsIs400 pins validation of ?limit=/
// ?offset=.
func TestQuotesListInvalidPaginationParamsIs400(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	cases := []string{
		"/api/quotes?limit=0",
		"/api/quotes?limit=abc",
		"/api/quotes?limit=501",
		"/api/quotes?offset=-1",
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

func TestQuotesValidation(t *testing.T) {
	mux := newTestMux(t, t.TempDir())

	cases := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"missing inputs", `{"label":"x","clientName":"","project":"","reference":"","status":"draft","statusLog":[],"options":{}}`, http.StatusUnprocessableEntity},
		{"bad status", `{"label":"x","clientName":"","project":"","reference":"","status":"bogus","statusLog":[],"inputs":` + validQuoteInputsJSON + `,"options":{}}`, http.StatusUnprocessableEntity},
		{"label too long", quoteBodyFor(`"label":"` + strings.Repeat("x", 501) + `"`), http.StatusUnprocessableEntity},
		{"malformed inputs JSON", `{"label":"x","clientName":"","project":"","reference":"","status":"draft","statusLog":[],"inputs":{"parts":[{"weightG":"not-a-number"}]},"options":{}}`, http.StatusBadRequest},
		{"removed failureAppliesTo option", `{"label":"x","clientName":"","project":"","reference":"","status":"draft","statusLog":[],"inputs":` + validQuoteInputsJSON + `,"options":{"failureAppliesTo":"bogus"}}`, http.StatusBadRequest},
		{"unknown top-level field", quoteBodyFor(`"favoriteColor":"blue"`), http.StatusBadRequest},
		{"array body", `[1,2,3]`, http.StatusBadRequest},
		{"statusLog not an array", `{"label":"x","clientName":"","project":"","reference":"","status":"draft","statusLog":{},"inputs":` + validQuoteInputsJSON + `,"options":{}}`, http.StatusUnprocessableEntity},
		{"formState an array", quoteBodyFor(`"formState":[1,2,3]`), http.StatusUnprocessableEntity},
		{"formState a number", quoteBodyFor(`"formState":7`), http.StatusUnprocessableEntity},
		{"formState a string", quoteBodyFor(`"formState":"hi"`), http.StatusUnprocessableEntity},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := postQuote(t, mux, tc.body)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

// Quote.FormState is "always a JSON object, never null": a present non-object value is a 422 with a named problem, empty/absent/null become "{}" (like Options), and an object round-trips.
func TestQuotesFormStateMustBeObject(t *testing.T) {
	mux := newTestMux(t, t.TempDir())

	rejected := []struct {
		name string
		body string
	}{
		{"array", quoteBodyFor(`"formState":[1,2,3]`)},
		{"number", quoteBodyFor(`"formState":7`)},
		{"string", quoteBodyFor(`"formState":"hi"`)},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			rec := postQuote(t, mux, tc.body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body: %s", rec.Code, rec.Body.String())
			}
			var errBody apiErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &errBody); err != nil {
				t.Fatalf("unmarshal errors: %v", err)
			}
			if !containsStr(errBody.Errors, "formState: must be a JSON object") {
				t.Fatalf("errors = %v, want to contain formState message", errBody.Errors)
			}
		})
	}

	accepted := []struct {
		name string
		body string
	}{
		{"empty object", quoteBodyFor(`"formState":{}`)},
		{"absent", quoteBodyFor("")},
		{"null", quoteBodyFor(`"formState":null`)},
	}
	for _, tc := range accepted {
		t.Run("accepts "+tc.name, func(t *testing.T) {
			rec := postQuote(t, mux, tc.body)
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body: %s", rec.Code, rec.Body.String())
			}
			var q Quote
			if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil {
				t.Fatalf("unmarshal quote: %v", err)
			}
			if string(bytes.TrimSpace(q.FormState)) != "{}" {
				t.Errorf("formState = %s, want {}", q.FormState)
			}
		})
	}

	rec := postQuote(t, mux, quoteBodyFor(`"formState":{"printerId":"p1"}`))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body: %s", rec.Code, rec.Body.String())
	}
	var q Quote
	if err := json.Unmarshal(rec.Body.Bytes(), &q); err != nil {
		t.Fatalf("unmarshal quote: %v", err)
	}
	if string(q.FormState) != `{"printerId":"p1"}` {
		t.Errorf("formState = %s, want round-tripped object", q.FormState)
	}
}

// Pads with leading whitespace (ignored by the JSON decoder) so MaxBytesReader trips before DisallowUnknownFields can complain: the body is rejected for its SIZE, not misreported as shape.
func TestQuotesPostOversizedBodyIs413(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	body := strings.Repeat(" ", quotesMaxBytes+1) + quoteBodyFor("")
	rec := postQuote(t, mux, body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d", rec.Code)
	}
}

// An incomplete/invalid quote (a draft with no weight) is still accepted and stored with valid=false: the REST layer validates its own fields, and computeQuote scores the pricing content.
func TestQuotesInvalidCostInputsStillSaves(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	blankInputs := `{"parts":[{"label":"Part 1","weightG":null,"pricePerKg":0,"wastePct":0,"timeHours":0,"timeMinutes":0,"watts":0,` +
		`"energy":{"mode":"flat","flatRate":0},"failureRatePct":0,"prepMinutes":0,"laborRate":0,"machineCost":0,` +
		`"machineLifeHours":0,"monthlyFixed":0,"monthlyHours":0,"quantity":1}]}`
	body := `{"label":"Draft","clientName":"","project":"","reference":"","status":"draft","statusLog":[],` +
		`"inputs":` + blankInputs + `,"options":{}}`
	rec := postQuote(t, mux, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST want 201, got %d %s", rec.Code, rec.Body.String())
	}
	q := mustUnmarshalQuote(t, rec)
	if q.Valid {
		t.Errorf("valid = true, want false for a blank-weight draft")
	}
	if q.RecommendedPriceCents != nil {
		t.Errorf("recommendedPriceCents = %v, want nil (no pricing block at all in this input)", q.RecommendedPriceCents)
	}
}

// Close the *sql.DB under the handlers to force every query to fail and exercise the 500 paths; permission bits don't bind root in CI.
func TestQuotesHandlersOnClosedDB(t *testing.T) {
	dir := t.TempDir()
	db := openTestDB(t, dir)
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	cfg, err := defaultConfig()
	if err != nil {
		t.Fatalf("defaultConfig: %v", err)
	}
	mux := routesMux(db, NewConfigStore(cfg), newAuthState("", nil), dir, false)

	rec := serveReq(mux, httptest.NewRequest("GET", "/api/quotes", nil))
	if rec.Code != 500 {
		t.Fatalf("GET list on closed db want 500, got %d", rec.Code)
	}

	rec = postQuote(t, mux, quoteBodyFor(""))
	if rec.Code != 500 {
		t.Fatalf("POST on closed db want 500, got %d %s", rec.Code, rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// POST /api/quote/compute
// ---------------------------------------------------------------------------

func postCompute(t *testing.T, mux *http.ServeMux, body string) *httptest.ResponseRecorder {
	return serveReq(mux, httptest.NewRequest("POST", "/api/quote/compute", strings.NewReader(body)))
}

func TestQuoteComputeReturnsAuthoritativeBreakdown(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postCompute(t, mux, `{"inputs":`+validQuoteInputsJSON+`,"options":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d %s", rec.Code, rec.Body.String())
	}
	var ob OrderBreakdown
	if err := json.Unmarshal(rec.Body.Bytes(), &ob); err != nil {
		t.Fatalf("unmarshal breakdown: %v", err)
	}
	if len(ob.Parts) != 1 {
		t.Fatalf("parts = %d, want 1", len(ob.Parts))
	}
	bd := ob.Parts[0].Breakdown
	if bd.PerUnitTotal != 201 || bd.BatchTotal != 201 || ob.BatchTotal != 201 {
		t.Fatalf("perUnitTotal/batchTotal/order batchTotal = %d/%d/%d, want 201/201/201", bd.PerUnitTotal, bd.BatchTotal, ob.BatchTotal)
	}
	if ob.Pricing == nil || ob.Pricing.ResolvedPrice != 335 || !ob.Pricing.Valid {
		t.Fatalf("pricing = %+v, want resolvedPrice 335, valid true", ob.Pricing)
	}
}

// A present-but-null required field (weightG) decodes to NaN (F.UnmarshalJSON); sanitizeOrderForJSON must keep the NaN LineItem.Inputs entry from making the response unmarshalable (200 with a "nan" warning, not 500).
func TestQuoteComputeSanitizesNaNForJSON(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	inputs := strings.Replace(validQuoteInputsJSON, `"weightG":100`, `"weightG":null`, 1)
	rec := postCompute(t, mux, `{"inputs":`+inputs+`,"options":{}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d %s", rec.Code, rec.Body.String())
	}
	var ob OrderBreakdown
	if err := json.Unmarshal(rec.Body.Bytes(), &ob); err != nil {
		t.Fatalf("unmarshal breakdown: %v", err)
	}
	if ob.Valid {
		t.Errorf("valid = true, want false for a NaN weightG")
	}
	found := false
	for _, w := range ob.Warnings {
		if w.Field == "parts[0].weightG" && w.Code == "nan" {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %+v, want a nan warning on parts[0].weightG", ob.Warnings)
	}
}

func TestQuoteComputeMissingInputsIs422(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postCompute(t, mux, `{"options":{}}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestQuoteComputeMalformedInputsIs400(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postCompute(t, mux, `{"inputs":{"parts":[{"weightG":"not-a-number"}]}}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestQuoteComputeOversizedBodyIs413(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	body := strings.Repeat(" ", quotesMaxBytes+1) + `{"inputs":` + validQuoteInputsJSON + `,"options":{}}`
	rec := postCompute(t, mux, body)
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("want 413, got %d", rec.Code)
	}
}

// Attribution (identity.go): create stamps createdBy/updatedBy from the acting identity, update moves only updatedBy, and no identity falls back to admin (auth disabled).

func TestQuoteCreateStampsCreatedByAndUpdatedByFromIdentity(t *testing.T) {
	mux, db := newTestMuxWithDB(t, t.TempDir())
	if _, _, err := createUser(db, "flo", "password123", []string{"quotes"}, ""); err != nil {
		t.Fatalf("createUser: %v", err)
	}

	req := withCtxIdentity(httptest.NewRequest("POST", "/api/quotes", strings.NewReader(quoteBodyFor(""))), identity{username: "flo"})
	rec := serveReq(mux, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, body %s", rec.Code, rec.Body.String())
	}
	var created Quote
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.CreatedBy != "flo" || created.UpdatedBy != "flo" {
		t.Fatalf("createdBy=%q updatedBy=%q, want both %q", created.CreatedBy, created.UpdatedBy, "flo")
	}
}

func TestQuoteUpdateMovesOnlyUpdatedBy(t *testing.T) {
	mux, db := newTestMuxWithDB(t, t.TempDir())
	if _, _, err := createUser(db, "flo", "password123", []string{"quotes"}, ""); err != nil {
		t.Fatalf("createUser flo: %v", err)
	}
	if _, _, err := createUser(db, "bob", "password123", []string{"quotes"}, ""); err != nil {
		t.Fatalf("createUser bob: %v", err)
	}

	req := withCtxIdentity(httptest.NewRequest("POST", "/api/quotes", strings.NewReader(quoteBodyFor(""))), identity{username: "flo"})
	rec := serveReq(mux, req)
	var created Quote
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}

	req = withCtxIdentity(httptest.NewRequest("PUT", "/api/quotes/"+created.ID, strings.NewReader(quoteBodyFor(`"label":"Updated"`))), identity{username: "bob"})
	rec = serveReq(mux, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status = %d, body %s", rec.Code, rec.Body.String())
	}
	var updated Quote
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

func TestQuoteCreateWithNoContextIdentityStampsAdmin(t *testing.T) {
	mux := newTestMux(t, t.TempDir())
	rec := postQuote(t, mux, quoteBodyFor(""))
	var created Quote
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if created.CreatedBy != "admin" || created.UpdatedBy != "admin" {
		t.Fatalf("createdBy=%q updatedBy=%q, want both %q (identityFor's admin fallback)", created.CreatedBy, created.UpdatedBy, "admin")
	}
}
