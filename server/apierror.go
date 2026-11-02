package main

import (
	"encoding/json"
	"net/http"
)

// apiErrorBody is the wire shape of multi-message JSON errors: {"errors": ["msg", ...]}.
type apiErrorBody struct {
	Errors []string `json:"errors"`
}

// writeAPIError writes a JSON error response shaped as {"errors": [...]}.
// Cache-Control: no-store on /api/* errors comes from noStoreAPIMiddleware.
func writeAPIError(w http.ResponseWriter, status int, msgs ...string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(apiErrorBody{Errors: msgs})
}
