package main

import (
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
)

// accentVectorFile is testdata/accent-vectors.json, shared with web/src/lib/accent.test.ts. Regenerate the
// expected values with UPDATE_ACCENT_VECTORS=1 go test ./server -run AccentVectors, then review the diff.
type accentVectorFile struct {
	Description string         `json:"description"`
	Vectors     []accentVector `json:"vectors"`
}

type accentVector struct {
	Name     string       `json:"name"`
	Input    string       `json:"input"`
	Expected *AccentCheck `json:"expected,omitempty"`
	// Problem is accentProblem's message, "" when the color is accepted.
	Problem string `json:"problem"`
}

const accentVectorPath = "../testdata/accent-vectors.json"

var accentVectorInputs = []struct{ name, input string }{
	{"empty is the theme accent, not checked", ""},
	{"shop brown passes", "#9c4212"},
	{"uppercase hex is normalized", "#9C4212"},
	{"deep blue passes", "#1d4ed8"},
	{"dark green passes", "#166534"},
	{"black passes", "#000000"},
	{"theme orange fails as link text", "#e06020"},
	{"bright yellow fails", "#ffd700"},
	{"light gray fails", "#cccccc"},
	{"white fails", "#ffffff"},
	{"mid teal fails narrowly", "#2a9d8f"},
	{"pure red fails narrowly", "#d62828"},
	{"borderline blue", "#2563eb"},
	{"short form is not accepted", "#fff"},
	{"missing hash is not accepted", "9c4212"},
	{"named color is not accepted", "orange"},
}

func TestAccentVectors(t *testing.T) {
	if os.Getenv("UPDATE_ACCENT_VECTORS") != "" {
		out := accentVectorFile{Description: "Shared by server/accent_test.go and web/src/lib/accent.test.ts. Each vector is the result of checkAccentColor / accentProblem for one input. Ratios compare within 0.001; colors and booleans compare exactly. The shop backgrounds are the --bg, --surface and --surface-2 tokens in each scheme. Regenerate with UPDATE_ACCENT_VECTORS=1 go test ./server -run AccentVectors, then review the diff."}
		for _, in := range accentVectorInputs {
			v := accentVector{Name: in.name, Input: in.input, Problem: accentProblem(in.input)}
			if in.input != "" {
				res := checkAccentColor(in.input)
				v.Expected = &res
			}
			out.Vectors = append(out.Vectors, v)
		}
		raw, err := json.MarshalIndent(out, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(accentVectorPath, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := os.ReadFile(accentVectorPath)
	if err != nil {
		t.Fatal(err)
	}
	var file accentVectorFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Vectors) != len(accentVectorInputs) {
		t.Fatalf("fixture has %d vectors, test inputs %d: regenerate it", len(file.Vectors), len(accentVectorInputs))
	}
	near := func(a, b float64) bool { return math.Abs(a-b) <= 0.001 }
	for _, v := range file.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			if got := accentProblem(v.Input); got != v.Problem {
				t.Errorf("accentProblem = %q, want %q", got, v.Problem)
			}
			if v.Expected == nil {
				return
			}
			got, want := checkAccentColor(v.Input), *v.Expected
			if got.OK != want.OK || got.Accent != want.Accent || got.Suggestion != want.Suggestion {
				t.Errorf("result = %+v, want %+v", got, want)
			}
			for name, pair := range map[string][2]AccentScheme{"light": {got.Light, want.Light}, "dark": {got.Dark, want.Dark}} {
				g, w := pair[0], pair[1]
				if g.Accent != w.Accent || g.ButtonText != w.ButtonText || g.Pass != w.Pass || !near(g.LinkRatio, w.LinkRatio) || !near(g.ButtonRatio, w.ButtonRatio) {
					t.Errorf("%s = %+v, want %+v", name, g, w)
				}
			}
		})
	}
}

// Invariants that hold for any input, not just the fixture's: a suggestion always passes, the derived dark
// variant always passes on the dark backgrounds, and a passing color is its own light variant.
func TestAccentInvariants(t *testing.T) {
	for _, hex := range []string{"#e06020", "#ffd700", "#2a9d8f", "#d62828", "#9c4212", "#123456", "#fedcba", "#808080", "#00ff00", "#ff00ff"} {
		res := checkAccentColor(hex)
		if res.Light.Pass {
			if !res.OK || res.Dark.Accent == "" || !res.Dark.Pass || res.Suggestion != "" || res.Light.Accent != hex {
				t.Errorf("%s: passing light but result %+v", hex, res)
			}
			if !passesAccent(res.Dark.Accent, accentDarkBackgrounds) {
				t.Errorf("%s: dark variant %s fails the dark pairs", hex, res.Dark.Accent)
			}
			continue
		}
		if res.OK {
			t.Errorf("%s: failing light but OK", hex)
		}
		if res.Suggestion == "" || !passesAccent(res.Suggestion, accentLightBackgrounds) {
			t.Errorf("%s: suggestion %q does not pass", hex, res.Suggestion)
		}
	}
}

func TestAccentProblemMessages(t *testing.T) {
	if got := accentProblem("#ffd700"); !strings.Contains(got, "links measure") || !strings.Contains(got, "try #") || !strings.Contains(got, "4.5:1") {
		t.Errorf("low-contrast message = %q", got)
	}
	if got := accentProblem("orange"); !strings.Contains(got, "six hex digits") {
		t.Errorf("format message = %q", got)
	}
	if got := accentProblem(""); got != "" {
		t.Errorf("empty = %q, want accepted", got)
	}
}
