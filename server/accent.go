package main

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Accent color check for shop.accentColor. The same algorithm lives in web/src/lib/accent.ts; both read
// testdata/accent-vectors.json so they can't drift. Pure functions: no config, no I/O.
//
// Which pairs must pass (WCAG 2.2 AA, 4.5:1 for text):
//   - link: the accent used as text color on every shop background of the scheme (page, card, tinted card);
//     the lowest ratio counts.
//   - button: the accent as a button background with its text color (white or near-black, whichever
//     contrasts more).
//
// The light scheme is checked against the colors the owner picked from. A color that fails there is
// rejected with the measured ratios and the nearest passing color. The dark-mode variant is derived (same
// hue and saturation, lightness moved the least that passes the same two pairs on the dark backgrounds), so
// the owner never has to pick it.

// Shop backgrounds the accent is checked against. These mirror web/src/theme.css --bg, --surface and
// --surface-2 in each scheme; update both together.
var (
	accentLightBackgrounds = []string{"#fff5dc", "#fffbef", "#f6e8c4"}
	accentDarkBackgrounds  = []string{"#141414", "#1e1e1e", "#292929"}
	// accentTextCandidates are the colors a button label can use on the accent background.
	accentTextCandidates = []string{"#ffffff", "#141414"}
)

const (
	accentMinRatio  = 4.5
	accentStep      = 0.005 // lightness step of the nearest-color search
	accentMaxSteps  = 200
	accentHexPatStr = `^#[0-9a-fA-F]{6}$`
)

var accentHexRe = regexp.MustCompile(accentHexPatStr)

// AccentScheme is the result for one color scheme: the accent used there, the button label color, the
// measured ratios, and whether both pairs reach 4.5:1.
type AccentScheme struct {
	Accent      string  `json:"accent"`
	ButtonText  string  `json:"buttonText"`
	LinkRatio   float64 `json:"linkRatio"`
	ButtonRatio float64 `json:"buttonRatio"`
	Pass        bool    `json:"pass"`
}

// AccentCheck is checkAccentColor's result. OK is true when the light scheme passes (then Dark is a
// passing derived variant, or OK is false when none exists). Suggestion is the nearest passing light color
// when the input fails, "" otherwise or when none exists.
type AccentCheck struct {
	OK         bool         `json:"ok"`
	Accent     string       `json:"accent"`
	Light      AccentScheme `json:"light"`
	Dark       AccentScheme `json:"dark"`
	Suggestion string       `json:"suggestion"`
}

type accentRGB struct{ r, g, b int }

func parseAccentHex(hex string) (accentRGB, bool) {
	if !accentHexRe.MatchString(hex) {
		return accentRGB{}, false
	}
	var r, g, b int
	fmt.Sscanf(strings.ToLower(hex[1:]), "%02x%02x%02x", &r, &g, &b)
	return accentRGB{r, g, b}, true
}

func accentToHex(c accentRGB) string {
	return fmt.Sprintf("#%02x%02x%02x", c.r, c.g, c.b)
}

func accentLinear(c255 int) float64 {
	c := float64(c255) / 255
	if c <= 0.03928 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func accentLuminance(hex string) float64 {
	c, _ := parseAccentHex(hex)
	return 0.2126*accentLinear(c.r) + 0.7152*accentLinear(c.g) + 0.0722*accentLinear(c.b)
}

// accentContrast is the WCAG contrast ratio of two #rrggbb colors.
func accentContrast(a, b string) float64 {
	l1, l2 := accentLuminance(a), accentLuminance(b)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

// accentRound4 rounds to 4 decimals for stable fixtures.
func accentRound4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

// evalAccentScheme measures hex against backgrounds. The button text is whichever candidate contrasts
// more; ties keep the first (white).
func evalAccentScheme(hex string, backgrounds []string) AccentScheme {
	link := math.Inf(1)
	for _, bg := range backgrounds {
		if r := accentContrast(hex, bg); r < link {
			link = r
		}
	}
	text, button := accentTextCandidates[0], 0.0
	for _, cand := range accentTextCandidates {
		if r := accentContrast(hex, cand); r > button {
			text, button = cand, r
		}
	}
	return AccentScheme{
		Accent:      hex,
		ButtonText:  text,
		LinkRatio:   accentRound4(link),
		ButtonRatio: accentRound4(button),
		Pass:        link >= accentMinRatio && button >= accentMinRatio,
	}
}

// passesAccent reports whether both pairs reach the minimum, on unrounded ratios.
func passesAccent(hex string, backgrounds []string) bool {
	for _, bg := range backgrounds {
		if accentContrast(hex, bg) < accentMinRatio {
			return false
		}
	}
	best := 0.0
	for _, cand := range accentTextCandidates {
		if r := accentContrast(hex, cand); r > best {
			best = r
		}
	}
	return best >= accentMinRatio
}

func accentRGBToHSL(c accentRGB) (h, s, l float64) {
	r, g, b := float64(c.r)/255, float64(c.g)/255, float64(c.b)/255
	max := math.Max(r, math.Max(g, b))
	min := math.Min(r, math.Min(g, b))
	l = (max + min) / 2
	if max == min {
		return 0, 0, l
	}
	d := max - min
	if l > 0.5 {
		s = d / (2 - max - min)
	} else {
		s = d / (max + min)
	}
	switch max {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	default:
		h = (r-g)/d + 4
	}
	return h / 6, s, l
}

func accentHue2rgb(p, q, t float64) float64 {
	if t < 0 {
		t += 1
	}
	if t > 1 {
		t -= 1
	}
	if t < 1.0/6 {
		return p + (q-p)*6*t
	}
	if t < 0.5 {
		return q
	}
	if t < 2.0/3 {
		return p + (q-p)*(2.0/3-t)*6
	}
	return p
}

func accentHSLToHex(h, s, l float64) string {
	var r, g, b float64
	if s == 0 {
		r, g, b = l, l, l
	} else {
		var q float64
		if l < 0.5 {
			q = l * (1 + s)
		} else {
			q = l + s - l*s
		}
		p := 2*l - q
		r = accentHue2rgb(p, q, h+1.0/3)
		g = accentHue2rgb(p, q, h)
		b = accentHue2rgb(p, q, h-1.0/3)
	}
	to := func(v float64) int { return int(math.Round(v * 255)) }
	return accentToHex(accentRGB{to(r), to(g), to(b)})
}

// nearestPassingAccent walks lightness away from hex in accentStep increments, first in preferFirst
// direction (-1 darker, +1 lighter), and returns the first color that passes. "" when none does.
func nearestPassingAccent(hex string, backgrounds []string, preferFirst int) string {
	c, _ := parseAccentHex(hex)
	h, s, l := accentRGBToHSL(c)
	for k := 1; k <= accentMaxSteps; k++ {
		for _, dir := range []int{preferFirst, -preferFirst} {
			nl := l + float64(dir*k)*accentStep
			if nl < 0 || nl > 1 {
				continue
			}
			cand := accentHSLToHex(h, s, nl)
			if passesAccent(cand, backgrounds) {
				return cand
			}
		}
	}
	return ""
}

// checkAccentColor validates a #rrggbb accent for the shop. Input other than six hex digits returns
// ok false with no ratios (callers report the format problem first).
func checkAccentColor(input string) AccentCheck {
	if !accentHexRe.MatchString(input) {
		return AccentCheck{}
	}
	hex := strings.ToLower(input)
	res := AccentCheck{Accent: hex, Light: evalAccentScheme(hex, accentLightBackgrounds)}
	if !res.Light.Pass {
		res.Suggestion = nearestPassingAccent(hex, accentLightBackgrounds, -1)
		return res
	}
	darkAccent := hex
	if !passesAccent(hex, accentDarkBackgrounds) {
		darkAccent = nearestPassingAccent(hex, accentDarkBackgrounds, +1)
	}
	if darkAccent == "" {
		// No lightness of this hue passes on the dark backgrounds; the owner can't be offered a variant.
		res.Dark = evalAccentScheme(hex, accentDarkBackgrounds)
		return res
	}
	res.Dark = evalAccentScheme(darkAccent, accentDarkBackgrounds)
	res.OK = res.Dark.Pass
	return res
}

// accentProblem turns a failed check into the one-line message config validation reports, or "" when the
// color is valid. Ratios are floored to two decimals so "4.50" never means 4.496.
func accentProblem(input string) string {
	if input == "" {
		return ""
	}
	if !accentHexRe.MatchString(input) {
		return "shop.accentColor: must be a color like #9c4212 (six hex digits), or empty for the theme accent"
	}
	res := checkAccentColor(input)
	if res.OK {
		return ""
	}
	fl := func(v float64) float64 { return math.Floor(v*100) / 100 }
	if !res.Light.Pass {
		msg := fmt.Sprintf("shop.accentColor: %s doesn't have enough contrast on the shop background: links measure %.2f:1 and button text %.2f:1, and both need 4.5:1",
			res.Accent, fl(res.Light.LinkRatio), fl(res.Light.ButtonRatio))
		if res.Suggestion != "" {
			msg += "; try " + res.Suggestion
		}
		return msg
	}
	return fmt.Sprintf("shop.accentColor: no dark-mode variant of %s reaches 4.5:1; pick a different color", res.Accent)
}
