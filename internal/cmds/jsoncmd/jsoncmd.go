// Package jsoncmd is gortk's JSON inspector. It reads a JSON file (or stdin) and
// emits a compact, token-optimized view — either the structure with values
// preserved (default) or a values-stripped schema (--keys-only). Faithful port
// of rtk's src/cmds/system/json_cmd.rs. The gortk subcommand name is "json".
//
// Offline by default: it only reads the file you point it at; it never makes
// network calls. The compression logic lives in pure functions
// (filterJSONCompact / filterJSONString) so it can be tested directly against
// the ported Rust spec.
package jsoncmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "json",
		Summary: "Inspect JSON structure with token-optimized output",
		Run:     Run,
	})
}

// defaultMaxDepth mirrors rtk's clap default for --depth.
const defaultMaxDepth = 5

// Run executes the json command. args are the tokens after "json"; verbose is
// the -v count. It parses --depth/-d, --keys-only, and a file operand (or "-"
// for stdin), then prints the compacted view. It returns the process exit code.
func Run(args []string, verbose int) (int, error) {
	maxDepth := defaultMaxDepth
	schemaOnly := false
	var file string

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--keys-only":
			schemaOnly = true
		case a == "-d" || a == "--depth":
			if i+1 < len(args) {
				i++
				if v, ok := parseUint(args[i]); ok {
					maxDepth = v
				}
			}
		case strings.HasPrefix(a, "--depth="):
			if v, ok := parseUint(strings.TrimPrefix(a, "--depth=")); ok {
				maxDepth = v
			}
		case strings.HasPrefix(a, "-d") && len(a) > 2:
			if v, ok := parseUint(a[2:]); ok {
				maxDepth = v
			}
		default:
			if file == "" {
				file = a
			}
		}
	}

	if file == "-" {
		return runStdin(maxDepth, schemaOnly, verbose)
	}
	if file == "" {
		return 1, fmt.Errorf("gortk json: no JSON file specified (use - for stdin)")
	}
	return runFile(file, maxDepth, schemaOnly, verbose)
}

// runFile reads, validates, and prints a JSON file.
func runFile(file string, maxDepth int, schemaOnly bool, verbose int) (int, error) {
	if err := validateJSONExtension(file); err != nil {
		return 1, err
	}

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Analyzing JSON: %s\n", file)
	}

	content, err := os.ReadFile(file)
	if err != nil {
		return 1, fmt.Errorf("Failed to read file: %s: %w", file, err)
	}

	out, err := compact(string(content), maxDepth, schemaOnly)
	if err != nil {
		return 1, err
	}
	fmt.Println(out)
	return 0, nil
}

// runStdin reads JSON from stdin and prints the compacted view.
func runStdin(maxDepth int, schemaOnly bool, verbose int) (int, error) {
	if verbose > 0 {
		fmt.Fprintln(os.Stderr, "Analyzing JSON from stdin")
	}

	content, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 1, fmt.Errorf("Failed to read from stdin: %w", err)
	}

	out, err := compact(string(content), maxDepth, schemaOnly)
	if err != nil {
		return 1, err
	}
	fmt.Println(out)
	return 0, nil
}

func compact(content string, maxDepth int, schemaOnly bool) (string, error) {
	if schemaOnly {
		return filterJSONString(content, maxDepth)
	}
	return filterJSONCompact(content, maxDepth)
}

// validateJSONExtension rejects non-JSON files with a clear error before any
// I/O. Faithful port of rtk's validate_json_extension.
func validateJSONExtension(file string) error {
	ext := strings.TrimPrefix(filepath.Ext(file), ".")
	var formatName string
	switch ext {
	case "toml":
		formatName = "TOML"
	case "yaml", "yml":
		formatName = "YAML"
	case "xml":
		formatName = "XML"
	case "csv":
		formatName = "CSV"
	case "ini":
		formatName = "INI"
	case "env":
		formatName = "env"
	case "txt":
		formatName = "plain text"
	default:
		return nil
	}

	msg := fmt.Sprintf(
		"%s is not a JSON file (detected %s). Use `gortk read` for non-JSON files.",
		file, formatName,
	)
	if ext == "toml" && filepath.Base(file) == "Cargo.toml" {
		msg += " Tip: use `gortk deps` for Cargo.toml."
	}
	return fmt.Errorf("%s", msg)
}

// parseJSON decodes a JSON document into a Go value, preserving numbers as
// json.Number so the original textual form and the int/float distinction are
// retained (mirroring serde_json::Value). Objects decode to map[string]any,
// arrays to []any.
func parseJSON(jsonStr string) (any, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(jsonStr)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("Failed to parse JSON: %w", err)
	}
	return v, nil
}

// filterJSONCompact parses a JSON string and returns a compact representation
// with values preserved. Long strings are truncated, large arrays summarized.
// Faithful port of rtk's filter_json_compact.
func filterJSONCompact(jsonStr string, maxDepth int) (string, error) {
	v, err := parseJSON(jsonStr)
	if err != nil {
		return "", err
	}
	return compactJSON(v, 0, maxDepth), nil
}

func compactJSON(value any, depth, maxDepth int) string {
	indent := strings.Repeat("  ", depth)

	if depth > maxDepth {
		return indent + "..."
	}

	switch v := value.(type) {
	case nil:
		return indent + "null"
	case bool:
		return fmt.Sprintf("%s%t", indent, v)
	case json.Number:
		return indent + v.String()
	case string:
		if len(v) > 80 {
			end := floorCharBoundary(v, 77)
			return fmt.Sprintf("%s\"%s...\"", indent, v[:end])
		}
		return fmt.Sprintf("%s\"%s\"", indent, v)
	case []any:
		if len(v) == 0 {
			return indent + "[]"
		}
		if len(v) > 5 {
			first := compactJSON(v[0], depth+1, maxDepth)
			return fmt.Sprintf("%s[%s, ... +%d more]", indent, strings.TrimSpace(first), len(v)-1)
		}
		items := make([]string, len(v))
		for i, e := range v {
			items[i] = compactJSON(e, depth+1, maxDepth)
		}
		allSimple := true
		for _, e := range v {
			if !isSimple(e) {
				allSimple = false
				break
			}
		}
		if allSimple {
			inline := make([]string, len(items))
			for i, s := range items {
				inline[i] = strings.TrimSpace(s)
			}
			return fmt.Sprintf("%s[%s]", indent, strings.Join(inline, ", "))
		}
		lines := []string{indent + "["}
		for _, item := range items {
			lines = append(lines, item+",")
		}
		lines = append(lines, indent+"]")
		return strings.Join(lines, "\n")
	case map[string]any:
		if len(v) == 0 {
			return indent + "{}"
		}
		lines := []string{indent + "{"}
		keys := sortedKeys(v)
		for i, key := range keys {
			val := v[key]
			if isSimple(val) {
				valStr := compactJSON(val, 0, maxDepth)
				lines = append(lines, fmt.Sprintf("%s  %s: %s", indent, key, strings.TrimSpace(valStr)))
			} else {
				lines = append(lines, fmt.Sprintf("%s  %s:", indent, key))
				lines = append(lines, compactJSON(val, depth+1, maxDepth))
			}
			if i >= 20 {
				lines = append(lines, fmt.Sprintf("%s  ... +%d more keys", indent, len(keys)-i-1))
				break
			}
		}
		lines = append(lines, indent+"}")
		return strings.Join(lines, "\n")
	default:
		return indent + "null"
	}
}

// filterJSONString parses a JSON string and returns its schema (types only, no
// values). Faithful port of rtk's filter_json_string.
func filterJSONString(jsonStr string, maxDepth int) (string, error) {
	v, err := parseJSON(jsonStr)
	if err != nil {
		return "", err
	}
	return extractSchema(v, 0, maxDepth), nil
}

func extractSchema(value any, depth, maxDepth int) string {
	indent := strings.Repeat("  ", depth)

	if depth > maxDepth {
		return indent + "..."
	}

	switch v := value.(type) {
	case nil:
		return indent + "null"
	case bool:
		return indent + "bool"
	case json.Number:
		if isInt(v) {
			return indent + "int"
		}
		return indent + "float"
	case string:
		if len(v) > 50 {
			return fmt.Sprintf("%sstring[%d]", indent, len(v))
		} else if v == "" {
			return indent + "string"
		}
		// Check if it looks like a URL, date, etc.
		if strings.HasPrefix(v, "http") {
			return indent + "url"
		} else if strings.Contains(v, "-") && len(v) == 10 {
			return indent + "date?"
		}
		return indent + "string"
	case []any:
		if len(v) == 0 {
			return indent + "[]"
		}
		firstSchema := extractSchema(v[0], depth+1, maxDepth)
		trimmed := strings.TrimSpace(firstSchema)
		if len(v) == 1 {
			return fmt.Sprintf("%s[\n%s\n%s]", indent, firstSchema, indent)
		}
		return fmt.Sprintf("%s[%s] (%d)", indent, trimmed, len(v))
	case map[string]any:
		if len(v) == 0 {
			return indent + "{}"
		}
		lines := []string{indent + "{"}
		keys := sortedKeys(v)
		for i, key := range keys {
			val := v[key]
			valSchema := extractSchema(val, depth+1, maxDepth)
			valTrimmed := strings.TrimSpace(valSchema)

			if isSimple(val) {
				if i < len(keys)-1 {
					lines = append(lines, fmt.Sprintf("%s  %s: %s,", indent, key, valTrimmed))
				} else {
					lines = append(lines, fmt.Sprintf("%s  %s: %s", indent, key, valTrimmed))
				}
			} else {
				lines = append(lines, fmt.Sprintf("%s  %s:", indent, key))
				lines = append(lines, valSchema)
			}

			if i >= 15 {
				lines = append(lines, fmt.Sprintf("%s  ... +%d more keys", indent, len(keys)-i-1))
				break
			}
		}
		lines = append(lines, indent+"}")
		return strings.Join(lines, "\n")
	default:
		return indent + "null"
	}
}

// isSimple reports whether a value is a scalar (null/bool/number/string), the
// matches!(...) check rtk uses to decide inline vs. nested formatting.
func isSimple(value any) bool {
	switch value.(type) {
	case nil, bool, json.Number, string:
		return true
	default:
		return false
	}
}

// isInt reports whether a json.Number is an integer (no fractional or exponent
// part), mirroring serde_json::Number::is_i64() for the schema int/float split.
func isInt(n json.Number) bool {
	s := n.String()
	return !strings.ContainsAny(s, ".eE")
}

// sortedKeys returns the object's keys in ascending order, matching rtk's
// keys.sort().
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// floorCharBoundary returns the largest byte index <= idx that lands on a UTF-8
// character boundary in s, mirroring Rust's str::floor_char_boundary. This
// keeps multibyte-string truncation from splitting a rune.
func floorCharBoundary(s string, idx int) int {
	if idx >= len(s) {
		return len(s)
	}
	for idx > 0 && !isUTF8Boundary(s[idx]) {
		idx--
	}
	return idx
}

// isUTF8Boundary reports whether byte b is the start of a UTF-8 sequence (i.e.
// not a continuation byte 0b10xxxxxx).
func isUTF8Boundary(b byte) bool {
	return b&0xC0 != 0x80
}

// parseUint parses a non-negative base-10 integer.
func parseUint(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}
