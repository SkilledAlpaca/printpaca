package main

import (
	"net/http"
	"strconv"
)

// Opt-in ?limit=&offset= pagination shared by the list endpoints, with an X-Total-Count header.
// With neither param set, callers get every row in today's order and response shape.
// Slicing happens in Go, not SQL: these are one self-hoster's tables, and the SQL ORDER BY stays untouched.

// paginationMaxLimit caps rows per paginated response.
const paginationMaxLimit = 500

// pagination is a validated, opt-in {limit, offset} pair. Applied is false when neither param was present.
type pagination struct {
	Applied bool
	Limit   int
	Offset  int
}

// parsePagination reads ?limit=&offset=. ok is false only on a validation failure (respond 400 with msg).
// Neither param present is not a failure: it returns a zero pagination{} with ok true.
func parsePagination(r *http.Request) (p pagination, ok bool, msg string) {
	q := r.URL.Query()
	limitStr := q.Get("limit")
	offsetStr := q.Get("offset")
	if limitStr == "" && offsetStr == "" {
		return pagination{}, true, ""
	}

	limit := paginationMaxLimit
	if limitStr != "" {
		n, err := strconv.Atoi(limitStr)
		if err != nil || n < 1 || n > paginationMaxLimit {
			return pagination{}, false, "limit: must be an integer from 1 to " + strconv.Itoa(paginationMaxLimit)
		}
		limit = n
	}

	offset := 0
	if offsetStr != "" {
		n, err := strconv.Atoi(offsetStr)
		if err != nil || n < 0 {
			return pagination{}, false, "offset: must be a non-negative integer"
		}
		offset = n
	}

	return pagination{Applied: true, Limit: limit, Offset: offset}, true, ""
}

// paginate slices list per p and, when p.Applied, sets X-Total-Count to the full length.
// Call before WriteHeader or the header is silently dropped.
func paginate[T any](w http.ResponseWriter, list []T, p pagination) []T {
	if !p.Applied {
		return list
	}
	w.Header().Set("X-Total-Count", strconv.Itoa(len(list)))

	start := min(p.Offset, len(list))
	end := min(start+p.Limit, len(list))
	return list[start:end]
}
