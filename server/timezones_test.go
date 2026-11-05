package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

// testdata/timezones.json feeds the Settings time zone picker; regenerate it with
// scripts/gen-timezones.mjs. Every entry must load against the embedded tzdata, so the picker never offers a
// zone the server rejects.
func TestTimezoneListLoads(t *testing.T) {
	raw, err := os.ReadFile("../testdata/timezones.json")
	if err != nil {
		t.Fatal(err)
	}
	var zones []string
	if err := json.Unmarshal(raw, &zones); err != nil {
		t.Fatal(err)
	}
	if len(zones) < 300 || zones[0] != "UTC" {
		t.Fatalf("unexpected list: %d entries, first %q", len(zones), zones[0])
	}
	seen := map[string]bool{}
	for _, z := range zones {
		if seen[z] {
			t.Errorf("duplicate %q", z)
		}
		seen[z] = true
		if _, err := time.LoadLocation(z); err != nil {
			t.Errorf("%q does not load: %v", z, err)
		}
		if _, err := loadTimezone(z); err != nil {
			t.Errorf("%q rejected by loadTimezone: %v", z, err)
		}
	}
}
