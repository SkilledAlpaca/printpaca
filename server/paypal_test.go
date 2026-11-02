package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type paypalLinkFixture struct {
	Name         string `json:"name"`
	Handle       string `json:"handle"`
	Code         string `json:"code"`
	Decimals     int    `json:"decimals"`
	Status       string `json:"status"`
	BalanceCents int    `json:"balanceCents"`
	Expected     string `json:"expected"`
}

func TestPaypalPayLinkMatchesSharedFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "testdata", "paypal-link.json"))
	if err != nil {
		t.Fatalf("reading shared paypal-link fixture: %v", err)
	}
	var cases []paypalLinkFixture
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatalf("parsing shared paypal-link fixture: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("shared paypal-link fixture is empty")
	}
	for _, c := range cases {
		if got := paypalPayLink(c.Handle, c.Code, c.Decimals, c.Status, c.BalanceCents); got != c.Expected {
			t.Errorf("%s: paypalPayLink = %q, want %q", c.Name, got, c.Expected)
		}
	}
}
