package jsoncmd

import (
	"strings"
	"testing"
)

// --- #347: validateJSONExtension ---

func TestTOMLFileRejected(t *testing.T) {
	err := validateJSONExtension("config.toml")
	if err == nil {
		t.Fatal("expected error for .toml file")
	}
	if !strings.Contains(err.Error(), "not a JSON file") {
		t.Errorf("error missing %q: %s", "not a JSON file", err)
	}
	if !strings.Contains(err.Error(), "TOML") {
		t.Errorf("error missing %q: %s", "TOML", err)
	}
}

func TestCargoTOMLSuggestsDeps(t *testing.T) {
	err := validateJSONExtension("Cargo.toml")
	if err == nil {
		t.Fatal("expected error for Cargo.toml")
	}
	// rtk's message says "rtk deps"; gortk uses gortk-facing strings, so the
	// ported fixture is adapted to "gortk deps".
	if !strings.Contains(err.Error(), "gortk deps") {
		t.Errorf("error missing %q: %s", "gortk deps", err)
	}
}

func TestYAMLFileRejected(t *testing.T) {
	err := validateJSONExtension("config.yaml")
	if err == nil {
		t.Fatal("expected error for .yaml file")
	}
	if !strings.Contains(err.Error(), "YAML") {
		t.Errorf("error missing %q: %s", "YAML", err)
	}
}

func TestJSONFileAccepted(t *testing.T) {
	if err := validateJSONExtension("data.json"); err != nil {
		t.Errorf("data.json should be accepted, got: %s", err)
	}
}

func TestUnknownExtensionAccepted(t *testing.T) {
	if err := validateJSONExtension("data.xyz"); err != nil {
		t.Errorf("data.xyz should be accepted, got: %s", err)
	}
}

func TestNoExtensionAccepted(t *testing.T) {
	if err := validateJSONExtension("Makefile"); err != nil {
		t.Errorf("Makefile should be accepted, got: %s", err)
	}
}

func TestExtractSchemaSimple(t *testing.T) {
	v, err := parseJSON(`{"name": "test", "count": 42}`)
	if err != nil {
		t.Fatal(err)
	}
	schema := extractSchema(v, 0, 5)
	for _, want := range []string{"name", "string", "int"} {
		if !strings.Contains(schema, want) {
			t.Errorf("schema missing %q: %s", want, schema)
		}
	}
}

func TestExtractSchemaArray(t *testing.T) {
	v, err := parseJSON(`{"items": [1, 2, 3]}`)
	if err != nil {
		t.Fatal(err)
	}
	schema := extractSchema(v, 0, 5)
	for _, want := range []string{"items", "(3)"} {
		if !strings.Contains(schema, want) {
			t.Errorf("schema missing %q: %s", want, schema)
		}
	}
}

func assertValueTruncated(t *testing.T, payload string) {
	t.Helper()
	jsonStr := `{"key": "` + payload + `"}`
	output, err := filterJSONCompact(jsonStr, 5)
	if err != nil {
		t.Fatalf("filterJSONCompact must not error on valid JSON: %v", err)
	}

	if !strings.Contains(output, "key") {
		t.Errorf("output missing %q: %s", "key", output)
	}
	if !strings.Contains(output, "...") {
		t.Errorf("long string should be truncated, got: %s", output)
	}

	parts := strings.Split(output, `"`)
	if len(parts) < 2 {
		t.Fatalf("output should contain a quoted string value: %s", output)
	}
	value := parts[1]
	if len(value) > 80 {
		t.Errorf("truncated value is %d bytes: %s", len(value), value)
	}
}

func TestCompactTruncatesPureMultibyteString(t *testing.T) {
	assertValueTruncated(t, strings.Repeat("日本語テスト", 85))
}

func TestCompactTruncatesMixedASCIIMultibyteString(t *testing.T) {
	assertValueTruncated(t, strings.Repeat("a", 76)+strings.Repeat("日本語", 5))
}
