package main

import (
	"net/http/httptest"
	"testing"
)

// Unit tests for parsePagination/paginate, independent of any endpoint; other resource tests cover the same end to end.

func TestParsePaginationAbsentParamsNotApplied(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/requests", nil)
	p, ok, msg := parsePagination(r)
	if !ok {
		t.Fatalf("ok = false, want true; msg = %q", msg)
	}
	if p.Applied {
		t.Errorf("Applied = true, want false with no query params")
	}
}

func TestParsePaginationValidLimitAndOffset(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/requests?limit=10&offset=5", nil)
	p, ok, msg := parsePagination(r)
	if !ok {
		t.Fatalf("ok = false, want true; msg = %q", msg)
	}
	if !p.Applied || p.Limit != 10 || p.Offset != 5 {
		t.Errorf("p = %+v, want {Applied:true Limit:10 Offset:5}", p)
	}
}

func TestParsePaginationOffsetOnlyDefaultsLimitToMax(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/requests?offset=5", nil)
	p, ok, _ := parsePagination(r)
	if !ok || !p.Applied || p.Offset != 5 || p.Limit != paginationMaxLimit {
		t.Errorf("p = %+v, ok = %v, want {Applied:true Limit:%d Offset:5}", p, ok, paginationMaxLimit)
	}
}

func TestParsePaginationRejectsInvalidValues(t *testing.T) {
	cases := []string{
		"/api/requests?limit=0",
		"/api/requests?limit=-1",
		"/api/requests?limit=abc",
		"/api/requests?limit=501", // one past paginationMaxLimit
		"/api/requests?offset=-1",
		"/api/requests?offset=abc",
	}
	for _, target := range cases {
		t.Run(target, func(t *testing.T) {
			r := httptest.NewRequest("GET", target, nil)
			_, ok, msg := parsePagination(r)
			if ok {
				t.Fatalf("ok = true, want false (validation failure)")
			}
			if msg == "" {
				t.Error("msg is empty on a validation failure; want an actionable message")
			}
		})
	}
}

func TestParsePaginationAcceptsLimitAtMax(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/requests?limit=500", nil)
	p, ok, msg := parsePagination(r)
	if !ok || p.Limit != paginationMaxLimit {
		t.Fatalf("ok = %v, p = %+v, msg = %q; want ok=true Limit=%d", ok, p, msg, paginationMaxLimit)
	}
}

func TestPaginateNotAppliedReturnsListUnchangedNoHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	list := []int{1, 2, 3}

	got := paginate(rec, list, pagination{})
	if len(got) != 3 {
		t.Fatalf("len(got) = %d, want 3 (unchanged)", len(got))
	}
	if h := rec.Header().Get("X-Total-Count"); h != "" {
		t.Errorf("X-Total-Count = %q, want unset", h)
	}
}

func TestPaginateSlicesAndSetsTotalCount(t *testing.T) {
	rec := httptest.NewRecorder()
	list := []int{10, 20, 30, 40, 50}

	got := paginate(rec, list, pagination{Applied: true, Limit: 2, Offset: 1})
	if len(got) != 2 || got[0] != 20 || got[1] != 30 {
		t.Fatalf("got = %v, want [20 30]", got)
	}
	if h := rec.Header().Get("X-Total-Count"); h != "5" {
		t.Errorf("X-Total-Count = %q, want %q", h, "5")
	}
}

func TestPaginateOffsetPastEndReturnsEmpty(t *testing.T) {
	rec := httptest.NewRecorder()
	list := []int{1, 2, 3}

	got := paginate(rec, list, pagination{Applied: true, Limit: 10, Offset: 100})
	if len(got) != 0 {
		t.Fatalf("len(got) = %d, want 0 (offset past the end)", len(got))
	}
	if h := rec.Header().Get("X-Total-Count"); h != "3" {
		t.Errorf("X-Total-Count = %q, want %q (total count is unaffected by an out-of-range offset)", h, "3")
	}
}

func TestPaginateLimitPastEndReturnsRemainder(t *testing.T) {
	rec := httptest.NewRecorder()
	list := []int{1, 2, 3}

	got := paginate(rec, list, pagination{Applied: true, Limit: 100, Offset: 1})
	if len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("got = %v, want [2 3] (limit larger than the remainder just returns what's left)", got)
	}
}
