package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// light-markdown-vectors.json is shared with web/src/lib/lightMarkdown.test.ts; both parsers must agree.
func TestLightMarkdownVectors(t *testing.T) {
	raw, err := os.ReadFile("../testdata/light-markdown-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Vectors []struct {
			Name  string `json:"name"`
			Input string `json:"input"`
			Plain string `json:"plain"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Vectors) < 30 {
		t.Fatalf("only %d vectors", len(file.Vectors))
	}
	for _, v := range file.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			if got := lightMarkdownToPlain(v.Input); got != v.Plain {
				t.Errorf("lightMarkdownToPlain(%q) = %q, want %q", v.Input, got, v.Plain)
			}
		})
	}
}

// The shop head carries the plain text, escaped, never the raw markup.
func TestRenderShopHTMLDescriptionIsPlainText(t *testing.T) {
	cfg := publicConfigResponse{IntroText: "Made **to order** by [us](https://example.com).\n- a <b>\n- b"}
	out, ok := renderShopHTML("<html><head><title>x</title></head></html>", cfg)
	if !ok {
		t.Fatal("render failed")
	}
	want := `<meta name="description" content="Made to order by us. a &lt;b&gt; b">`
	if !strings.Contains(string(out), want) {
		t.Fatalf("missing %s in %s", want, out)
	}
}
