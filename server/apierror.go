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

// sendFailureNonAdminMessage is what a non-admin sees when an email send fails. The raw SMTP error can carry
// the relay host, account name, or server banner, which only an admin needs.
const sendFailureNonAdminMessage = "The email couldn't be sent. Ask an admin to check the email settings."

// sendFailureMessage returns the text for a failed email send: the full error for an admin, a fixed
// message for anyone else.
func sendFailureMessage(r *http.Request, err error) string {
	if identityFor(r).isAdmin {
		return err.Error()
	}
	return sendFailureNonAdminMessage
}
