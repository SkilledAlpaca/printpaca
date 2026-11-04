package main

import (
	"regexp"
	"strings"
)

// Light markdown (D46) for the shop intro and announcement. The browser renders it from a token tree
// (web/src/lib/lightMarkdown.ts); this file is a port of the same parser that produces the plain text for
// <meta name="description">. testdata/light-markdown-vectors.json keeps the two in step: change both together.

type mdInline struct {
	kind string // "text", "strong", "em" or "a"
	text string
	kids []mdInline
}

const mdMaxDepth = 4

// The same URL rules as isSafeUrl in lightMarkdown.ts: https with a host, or mailto with an address.
var (
	mdHTTPSURL  = regexp.MustCompile("(?i)^https://[^\\x00-\\x20\\x7f<>\"'\\x60\\\\/?#][^\\x00-\\x20\\x7f<>\"'\\x60\\\\]*$")
	mdMailtoURL = regexp.MustCompile("(?i)^mailto:[^\\x00-\\x20\\x7f<>\"'\\x60\\\\]+$")
	mdSpaceRun  = regexp.MustCompile(`[ \t\n\r\f\v]+`)
)

func mdSafeURL(u string) bool { return mdHTTPSURL.MatchString(u) || mdMailtoURL.MatchString(u) }

func mdIsSpace(b byte) bool { return b == ' ' || b == '\t' }

func mdPushText(out []mdInline, v string) []mdInline {
	if v == "" {
		return out
	}
	if n := len(out); n > 0 && out[n-1].kind == "text" {
		out[n-1].text += v
		return out
	}
	return append(out, mdInline{kind: "text", text: v})
}

func mdParseInline(s string, depth int, allowLink bool) []mdInline {
	var out []mdInline
	i := 0
	for i < len(s) {
		ch := s[i]
		if ch == '*' && depth < mdMaxDepth {
			if i+1 < len(s) && s[i+1] == '*' {
				if j := mdCloseStrong(s, i+2); j >= 0 {
					out = append(out, mdInline{kind: "strong", kids: mdParseInline(s[i+2:j], depth+1, allowLink)})
					i = j + 2
					continue
				}
				out = mdPushText(out, "**")
				i += 2
				continue
			}
			if i+1 < len(s) && !mdIsSpace(s[i+1]) {
				if j := mdCloseEm(s, i+1); j >= 0 {
					out = append(out, mdInline{kind: "em", kids: mdParseInline(s[i+1:j], depth+1, allowLink)})
					i = j + 1
					continue
				}
			}
		} else if ch == '[' && allowLink && depth < mdMaxDepth {
			if rel := strings.IndexByte(s[i+1:], ']'); rel >= 0 {
				m := i + 1 + rel
				if m > i+1 && m+1 < len(s) && s[m+1] == '(' {
					if rel2 := strings.IndexByte(s[m+2:], ')'); rel2 >= 0 {
						n := m + 2 + rel2
						if mdSafeURL(s[m+2 : n]) {
							out = append(out, mdInline{kind: "a", kids: mdParseInline(s[i+1:m], depth+1, false)})
							i = n + 1
							continue
						}
					}
				}
			}
		}
		out = mdPushText(out, s[i:i+1])
		i++
	}
	return out
}

func mdCloseStrong(s string, from int) int {
	if from >= len(s) || mdIsSpace(s[from]) {
		return -1
	}
	for k := from; k+1 < len(s); k++ {
		if s[k] == '*' && s[k+1] == '*' && k > from && !mdIsSpace(s[k-1]) {
			return k
		}
	}
	return -1
}

func mdCloseEm(s string, from int) int {
	for k := from; k < len(s); k++ {
		if s[k] != '*' {
			continue
		}
		if k+1 < len(s) && s[k+1] == '*' {
			k++
			continue
		}
		if k > from && !mdIsSpace(s[k-1]) {
			return k
		}
	}
	return -1
}

func mdInlinePlain(nodes []mdInline) string {
	var b strings.Builder
	for _, n := range nodes {
		if n.kind == "text" {
			b.WriteString(n.text)
		} else {
			b.WriteString(mdInlinePlain(n.kids))
		}
	}
	return b.String()
}

// lightMarkdownToPlain strips the markup to one line of plain text: paragraphs, line breaks and list items
// join with a space. It mirrors lightMarkdownToPlain in lightMarkdown.ts. The block structure only matters for
// the joins, and every line (paragraph line or list item) becomes one part, so a flat scan gives the same text.
func lightMarkdownToPlain(src string) string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	var parts []string
	for _, raw := range strings.Split(src, "\n") {
		line := strings.Trim(raw, " \t\n\r\f\v")
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "- ") && strings.Trim(line[2:], " \t\n\r\f\v") != "" {
			line = strings.Trim(line[2:], " \t\n\r\f\v")
		}
		parts = append(parts, mdInlinePlain(mdParseInline(line, 0, true)))
	}
	return strings.Trim(mdSpaceRun.ReplaceAllString(strings.Join(parts, " "), " "), " ")
}
