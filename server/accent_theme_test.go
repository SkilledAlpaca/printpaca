package main

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// themeSurfaces returns the --bg, --surface and --surface-2 values of the first rule in theme.css whose
// selector starts with selector.
func themeSurfaces(t *testing.T, css, selector string) []string {
	t.Helper()
	start := strings.Index(css, selector+" {")
	if start < 0 {
		t.Fatalf("selector %q not found in theme.css", selector)
	}
	end := strings.Index(css[start:], "}")
	body := css[start : start+end]
	var out []string
	for _, name := range []string{"--bg", "--surface", "--surface-2"} {
		m := regexp.MustCompile(regexp.QuoteMeta(name) + `:\s*(#[0-9a-fA-F]{6})\s*;`).FindStringSubmatch(body)
		if m == nil {
			t.Fatalf("%s not found in %s", name, selector)
		}
		out = append(out, strings.ToLower(m[1]))
	}
	return out
}

// TestAccentBackgroundsMatchTheme guards against drift between theme.css and the hardcoded accent backgrounds.
func TestAccentBackgroundsMatchTheme(t *testing.T) {
	raw, err := os.ReadFile("../web/src/theme.css")
	if err != nil {
		t.Fatal(err)
	}
	css := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(raw), "")
	cases := []struct {
		selector string
		want     []string
	}{
		{":root", accentLightBackgrounds},
		{`:root:not([data-theme="light"])`, accentDarkBackgrounds},
		{`:root[data-theme="dark"]`, accentDarkBackgrounds},
	}
	for _, c := range cases {
		if got := themeSurfaces(t, css, c.selector); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: theme.css has %v, accent.go has %v", c.selector, got, c.want)
		}
	}
}
