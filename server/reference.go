package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The short order reference a public visitor sees ("HL-1001"), so a phone call can quote it. It is a per-shop
// sequential number behind a prefix. The hex quote_requests.id stays the primary key and the only identifier
// the API accepts; the reference is display data stored in quote_requests.reference.
//
// The counter is the app_state row 'request_ref_seq' holding the last number issued. It is bumped inside the
// same transaction as the request INSERT, so a failed insert leaves no gap, and the single DB connection
// serializes writers, so two requests never share a number. The prefix is not part of the counter: changing
// quote.referencePrefix continues the numbering under the new prefix, and old references keep the prefix they
// were issued with.

const (
	requestRefSeqKey = "request_ref_seq"
	// requestRefFirstSeq is the first number issued, so the first reference has four digits.
	requestRefFirstSeq = 1001
	// requestRefFallbackPrefix is used when the business name yields no usable letters or digits.
	requestRefFallbackPrefix = "REQ"
	requestRefMaxDerived     = 4
)

// referencePrefix returns the prefix for new references: quote.referencePrefix when set (uppercased), else the
// initials derived from quote.businessName.
func referencePrefix(cfg *Config) string {
	if p := strings.TrimSpace(cfg.Quote.ReferencePrefix); p != "" {
		return strings.ToUpper(p)
	}
	return derivedReferencePrefix(cfg.Quote.BusinessName)
}

// derivedReferencePrefix builds a prefix from a business name: the initials of up to four words ("Hot Layer
// Prints" -> "HLP"), or the first three characters of a single word ("Printpaca" -> "PRI"). Only ASCII letters
// and digits count, because the prefix must satisfy isAlnumPrefix. A name with none gives "REQ".
func derivedReferencePrefix(name string) string {
	name = strings.NewReplacer("'", "", "\u2019", "").Replace(name) // "Ben's" is one word
	words := strings.FieldsFunc(name, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
	})
	switch {
	case len(words) == 0:
		return requestRefFallbackPrefix
	case len(words) == 1:
		w := words[0]
		if len(w) > 3 {
			w = w[:3]
		}
		return strings.ToUpper(w)
	}
	var b strings.Builder
	for _, w := range words {
		if b.Len() == requestRefMaxDerived {
			break
		}
		b.WriteByte(w[0])
	}
	return strings.ToUpper(b.String())
}

// formatRequestReference joins a prefix and a sequence number.
func formatRequestReference(prefix string, seq int) string {
	return fmt.Sprintf("%s-%d", prefix, seq)
}

// nextRequestReference issues the next reference inside the caller's transaction.
func nextRequestReference(tx *sql.Tx, prefix string) (string, error) {
	var seq int
	err := tx.QueryRow(`
		INSERT INTO app_state (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = CAST(CAST(value AS INTEGER) + 1 AS TEXT)
		RETURNING CAST(value AS INTEGER)`, requestRefSeqKey, strconv.Itoa(requestRefFirstSeq)).Scan(&seq)
	if err != nil {
		return "", fmt.Errorf("next request reference: %w", err)
	}
	return formatRequestReference(prefix, seq), nil
}

// peekRequestReference returns the reference the next request would get, without issuing it. Demo mode uses it
// so the simulated confirmation looks real and the counter does not move.
func peekRequestReference(db *sql.DB, prefix string) string {
	var v string
	err := db.QueryRow(`SELECT value FROM app_state WHERE key = ?`, requestRefSeqKey).Scan(&v)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return formatRequestReference(prefix, requestRefFirstSeq)
	}
	n, convErr := strconv.Atoi(v)
	if err != nil || convErr != nil {
		return formatRequestReference(prefix, requestRefFirstSeq)
	}
	return formatRequestReference(prefix, n+1)
}
