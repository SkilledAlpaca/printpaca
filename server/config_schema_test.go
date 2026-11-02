package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// Guards config.schema.json (the editor-facing schema) against drifting from the Config struct. Because the
// schema sets additionalProperties:false, TestConfigSchemaMatchesStruct walks Config's yaml tags and fails on a
// struct field with no schema property or a schema property with no struct field.

// yamlFieldName returns f's yaml tag name, or "" if the field should be
// skipped (no yaml tag, or explicitly "-").
func yamlFieldName(f reflect.StructField) string {
	tag := f.Tag.Get("yaml")
	if tag == "" {
		return ""
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "-" {
		return ""
	}
	return name
}

// checkSchemaCoverage compares a Config-tree type against its JSON Schema node and reports mismatches; path locates the error.
func checkSchemaCoverage(t *testing.T, path string, rt reflect.Type, schemaNode map[string]any) {
	t.Helper()
	for rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}

	switch rt.Kind() {
	case reflect.Struct:
		propsRaw, ok := schemaNode["properties"].(map[string]any)
		if !ok {
			t.Errorf("%s: schema node has no \"properties\" object for a struct field", path)
			return
		}
		seen := map[string]bool{}
		for f := range rt.Fields() {
			name := yamlFieldName(f)
			if name == "" {
				continue
			}
			seen[name] = true
			propNode, ok := propsRaw[name].(map[string]any)
			if !ok {
				t.Errorf("%s.%s: missing from config.schema.json \"properties\" -- add it, or the schema will reject valid config that sets this field (see config/config.example.yaml)", path, name)
				continue
			}
			childPath := name
			if path != "" {
				childPath = path + "." + name
			}
			checkSchemaCoverage(t, childPath, f.Type, propNode)
		}
		for key := range propsRaw {
			if !seen[key] {
				t.Errorf("%s: config.schema.json declares property %q with no matching Config field -- the schema has drifted ahead of (or never matched) the struct", path, key)
			}
		}

	case reflect.Slice, reflect.Array:
		itemsRaw, ok := schemaNode["items"].(map[string]any)
		if !ok {
			// A slice of a scalar (e.g. []string) with items:{"type":"string"}
			// still needs an "items" node; only a struct/map element type needs
			// items.properties, checked by the recursive call below.
			t.Errorf("%s: schema node has no \"items\" object for an array field", path)
			return
		}
		checkSchemaCoverage(t, path+"[]", rt.Elem(), itemsRaw)

	case reflect.Map:
		apRaw, ok := schemaNode["additionalProperties"].(map[string]any)
		if !ok {
			t.Errorf("%s: schema node has no object-valued \"additionalProperties\" for a map field", path)
			return
		}
		checkSchemaCoverage(t, path+".*", rt.Elem(), apRaw)

	default:
		// Scalar leaf (string, bool, int, float64): presence in the parent's
		// properties map (checked by the caller) is all that's verified here.
	}
}

func TestConfigSchemaMatchesStruct(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "config", "config.schema.json"))
	if err != nil {
		t.Fatalf("reading config.schema.json: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parsing config.schema.json: %v", err)
	}

	checkSchemaCoverage(t, "", reflect.TypeFor[Config](), schema)
}

// Numeric-bound drift: key coverage alone missed pricing.marginPct and invoicing.paymentMethods bounds that crash-looped the container, so the table asserts every schema bound against the constant or literal validate() uses.
// Go's len() counts bytes while JSON Schema maxLength counts characters, so string caps agree only for ASCII; the schema stays the looser side (see publicIntroTextMaxBytes).

// schemaAt walks the parsed schema to a node by property name, with two
// navigation tokens: "[]" descends into an array's "items", "*" into a
// map's "additionalProperties".
func schemaAt(t *testing.T, schema map[string]any, path ...string) map[string]any {
	t.Helper()
	node := schema
	for i, seg := range path {
		var next any
		switch seg {
		case "[]":
			next = node["items"]
		case "*":
			next = node["additionalProperties"]
		default:
			props, ok := node["properties"].(map[string]any)
			if !ok {
				t.Fatalf("schema path %v: no \"properties\" at segment %d", path, i)
			}
			next = props[seg]
		}
		m, ok := next.(map[string]any)
		if !ok {
			t.Fatalf("schema path %v: segment %q (%d) is missing or not an object", path, seg, i)
		}
		node = m
	}
	return node
}

func TestConfigSchemaNumericBoundsMatchGoConstants(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "config", "config.schema.json"))
	if err != nil {
		t.Fatalf("reading config.schema.json: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parsing config.schema.json: %v", err)
	}

	cases := []struct {
		path    []string
		keyword string
		want    float64
		why     string
	}{
		// pricing: the three bounds validate() shares with pricing.go.
		{[]string{"pricing", "marginPct"}, "minimum", 0, "validate(): marginPct < 0"},
		{[]string{"pricing", "marginPct"}, "maximum", maxMarginPct, "validate()/ComputePricing: maxMarginPct, INCLUSIVE"},
		{[]string{"pricing", "markupMultiplier"}, "minimum", 0, "validate(): markupMultiplier < 0"},
		{[]string{"pricing", "markupMultiplier"}, "maximum", maxMarkupMultiplier, "validate()/ComputePricing: maxMarkupMultiplier"},
		{[]string{"pricing", "taxRatePct"}, "minimum", 0, "validate()/ComputePricing: taxRatePct < 0"},
		{[]string{"pricing", "taxRatePct"}, "maximum", maxPercentAmount, "validate()/ComputePricing: maxPercentAmount"},

		// Collection sizes: maxConfigCollectionSize, plus the two "at least
		// one required" floors validate() enforces by hand.
		{[]string{"materials"}, "minItems", 1, "validate(): materials: at least one material required"},
		{[]string{"materials"}, "maxItems", maxConfigCollectionSize, "validate(): maxConfigCollectionSize"},
		{[]string{"printers"}, "minItems", 1, "validate(): printers: at least one printer required"},
		{[]string{"printers"}, "maxItems", maxConfigCollectionSize, "validate(): maxConfigCollectionSize"},
		{[]string{"hardware"}, "maxItems", maxConfigCollectionSize, "validate(): maxConfigCollectionSize"},
		{[]string{"feeSchedules"}, "maxProperties", maxConfigCollectionSize, "validate(): maxConfigCollectionSize"},
		{[]string{"presets"}, "maxProperties", maxConfigCollectionSize, "validate(): maxConfigCollectionSize"},

		// invoicing: the "practical trap" README calls out by name.
		{[]string{"invoicing", "paymentMethods"}, "minItems", 1, "validatePaymentMethods: 1-20 entries"},
		{[]string{"invoicing", "paymentMethods"}, "maxItems", 20, "validatePaymentMethods: 1-20 entries"},
		{[]string{"invoicing", "paymentMethods", "[]"}, "maxLength", paymentMethodMaxBytes, "paymentMethodMaxBytes (bytes vs characters: see the caveat above)"},
		{[]string{"invoicing", "salesTaxPct"}, "minimum", 0, "validate(): salesTaxPct out of range [0,30]"},
		{[]string{"invoicing", "salesTaxPct"}, "maximum", 30, "validate(): salesTaxPct out of range [0,30]"},
		{[]string{"invoicing", "dueDays"}, "minimum", 0, "validate(): dueDays out of range [0,365]"},
		{[]string{"invoicing", "dueDays"}, "maximum", 365, "validate(): dueDays out of range [0,365]"},

		{[]string{"users", "accessAdminEmail"}, "maxLength", maxEmailLength, "normalizeEmail: maxEmailLength"},

		// Free-text byte caps.
		{[]string{"publicQuotes", "introText"}, "maxLength", publicIntroTextMaxBytes, "publicIntroTextMaxBytes"},
		{[]string{"customCss"}, "maxLength", customCssMaxBytes, "customCssMaxBytes"},
		{[]string{"quote", "paymentInstructions"}, "maxLength", quotePaymentInstructionsMaxBytes, "quotePaymentInstructionsMaxBytes"},
		{[]string{"quote", "termsText"}, "maxLength", quoteTermsTextMaxBytes, "quoteTermsTextMaxBytes"},

		// Everything else validate() range-checks.
		{[]string{"currency", "decimals"}, "minimum", 0, "validate(): currency.decimals must be 0-4"},
		{[]string{"currency", "decimals"}, "maximum", 4, "validate(): currency.decimals must be 0-4"},
		{[]string{"defaults", "quantity"}, "minimum", 1, "validate(): defaults.quantity must be >= 1"},
		{[]string{"materials", "[]", "wastePct"}, "minimum", 0, "validate(): materials wastePct must be 0-100"},
		{[]string{"materials", "[]", "wastePct"}, "maximum", 100, "validate(): materials wastePct must be 0-100"},
		{[]string{"materials", "[]", "density"}, "exclusiveMinimum", 0, "validate(): density must be > 0"},
		{[]string{"printers", "[]", "filamentDiameterMm"}, "exclusiveMinimum", 0, "validate(): filamentDiameterMm must be > 0"},
		{[]string{"hardware", "[]", "unitCost"}, "minimum", 0, "validate(): hardware unitCost must be >= 0"},
		{[]string{"smtp", "port"}, "minimum", 1, "validate(): smtp.port out of range [1,65535]"},
		{[]string{"smtp", "port"}, "maximum", 65535, "validate(): smtp.port out of range [1,65535]"},
		// Percent/fraction fields ComputeQuote's own rules bound.
		{[]string{"energy", "tou", "defaultOffPeakFrac"}, "maximum", 1, "ComputeQuote: offPeakFrac rule max 1"},
		{[]string{"presets", "*", "failureRatePct"}, "maximum", maxFailureRatePct, "ComputeQuote: failureRatePct rule max"},
		{[]string{"defaults", "failureRatePct"}, "maximum", maxFailureRatePct, "ComputeQuote: failureRatePct rule max"},
		{[]string{"feeSchedules", "*", "percent", "[]", "pct"}, "maximum", maxPercentAmount, "ComputePricing: fee pct rule max"},
	}

	for _, c := range cases {
		node := schemaAt(t, schema, c.path...)
		raw, ok := node[c.keyword]
		if !ok {
			t.Errorf("%v: schema has no %q (Go enforces %v via %s)", c.path, c.keyword, c.want, c.why)
			continue
		}
		got, ok := raw.(float64)
		if !ok {
			t.Errorf("%v.%s = %v, not a number", c.path, c.keyword, raw)
			continue
		}
		if got != c.want {
			t.Errorf("%v.%s = %v, want %v (%s)", c.path, c.keyword, got, c.want, c.why)
		}
	}

	// marginPct's ceiling must be an inclusive maximum matching maxMarginPct; exclusiveMaximum 100 accepts the [99.9, 100) dead zone validate() rejects.
	if _, present := schemaAt(t, schema, "pricing", "marginPct")["exclusiveMaximum"]; present {
		t.Error("pricing.marginPct still declares exclusiveMaximum; the bound is an INCLUSIVE maxMarginPct (99.9)")
	}
}

// Keeps the schema patterns for invoicing.paypalMe and currency.code in step with the regexps validate() uses.
func TestSchemaPatternsMatchGoValidation(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "config", "config.schema.json"))
	if err != nil {
		t.Fatalf("reading config.schema.json: %v", err)
	}
	var schema struct {
		Properties map[string]struct {
			Properties map[string]struct {
				Pattern string `json:"pattern"`
			} `json:"properties"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		section, field string
		re             *regexp.Regexp
		ok, bad        []string
	}{
		{"invoicing", "paypalMe", paypalHandleRe, []string{"", "yourshop", "A1"}, []string{"a/b", "https://paypal.me/x", strings.Repeat("a", 21)}},
		{"currency", "code", currencyCodeRe, []string{"", "USD"}, []string{"usd", "USDX", "US"}},
	}
	for _, c := range cases {
		pat := schema.Properties[c.section].Properties[c.field].Pattern
		if pat == "" {
			t.Errorf("%s.%s: schema has no pattern", c.section, c.field)
			continue
		}
		sre := regexp.MustCompile(pat)
		for _, s := range c.ok {
			if !sre.MatchString(s) {
				t.Errorf("%s.%s: schema pattern rejects %q", c.section, c.field, s)
			}
		}
		for _, s := range c.bad {
			if sre.MatchString(s) || c.re.MatchString(s) {
				t.Errorf("%s.%s: %q accepted by schema or Go regexp", c.section, c.field, s)
			}
		}
	}
}
