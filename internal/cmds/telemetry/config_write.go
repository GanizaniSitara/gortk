package telemetry

// Tolerant config.toml writer for the [telemetry] section.
//
// The porting contract keeps the project free of a TOML *encoder* dependency
// (BurntSushi/toml is decode-only in our usage), so we edit config.toml as text
// with a small section-aware merge. The merge preserves every existing key,
// comment, and unrelated section verbatim; it only inserts or replaces the
// specific telemetry keys it is given. A missing file is created with a fresh
// [telemetry] section.

import (
	"os"
	"strings"
)

// quoteTOML renders a Go string as a TOML basic string literal (double-quoted,
// with the handful of escapes a basic string requires).
func quoteTOML(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// mergeTelemetry applies the given key=value updates (values already rendered as
// TOML literals, e.g. "true" or a quoted string) to the [telemetry] section of
// the config file at path, preserving all other content. It creates the file
// (and the section) when absent.
func mergeTelemetry(path string, updates map[string]string) error {
	existing := ""
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return err
	}

	merged := mergeSection(existing, "telemetry", updates)
	return os.WriteFile(path, []byte(merged), 0o644)
}

// mergeSection returns content with the [section] table containing the given
// key=value updates. Existing keys in the section are replaced in place;
// missing keys are appended to the end of the section; all other lines are
// preserved exactly. If the section does not exist it is appended.
//
// This is intentionally minimal: it understands top-level table headers
// ([name]) and `key = value` lines well enough to round-trip gortk's own
// config without disturbing anything else a user may have added.
func mergeSection(content, section string, updates map[string]string) string {
	// Work on a copy of the requested updates we can consume as we apply them.
	pending := make(map[string]string, len(updates))
	for k, v := range updates {
		pending[k] = v
	}

	lines := splitKeepEmpty(content)
	header := "[" + section + "]"

	var out []string
	inSection := false
	sectionStart := -1 // index in out where the section body begins
	sectionEnd := -1   // index in out just after the last body line of the section

	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)

		// A new table header ends the current section's body.
		if isTableHeader(trimmed) {
			if inSection {
				sectionEnd = len(out)
				inSection = false
			}
			if trimmed == header {
				inSection = true
				out = append(out, raw)
				sectionStart = len(out)
				continue
			}
			out = append(out, raw)
			continue
		}

		if inSection {
			if key, ok := tomlKey(trimmed); ok {
				if val, found := pending[key]; found {
					// Replace this key's line, preserving its indentation.
					indent := raw[:len(raw)-len(strings.TrimLeft(raw, " \t"))]
					out = append(out, indent+key+" = "+val)
					delete(pending, key)
					continue
				}
			}
		}

		out = append(out, raw)
	}

	if inSection {
		sectionEnd = len(out)
	}

	// Append any not-yet-written keys.
	if len(pending) > 0 {
		extra := renderKeys(pending)
		if sectionStart >= 0 {
			// Insert the remaining keys at the end of the existing section body.
			insertAt := sectionEnd
			if insertAt < sectionStart {
				insertAt = sectionStart
			}
			merged := make([]string, 0, len(out)+len(extra))
			merged = append(merged, out[:insertAt]...)
			merged = append(merged, extra...)
			merged = append(merged, out[insertAt:]...)
			out = merged
		} else {
			// No existing section — append a fresh one.
			if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
				out = append(out, "")
			}
			out = append(out, header)
			out = append(out, extra...)
		}
	}

	result := strings.Join(out, "\n")
	if !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	return result
}

// renderKeys turns a pending map into sorted "key = value" lines for stable,
// deterministic output (so tests and diffs are reproducible).
func renderKeys(pending map[string]string) []string {
	// Stable order: enabled, endpoint, token, then anything else alphabetically.
	order := []string{"enabled", "endpoint", "token"}
	seen := map[string]bool{}
	var lines []string
	for _, k := range order {
		if v, ok := pending[k]; ok {
			lines = append(lines, k+" = "+v)
			seen[k] = true
		}
	}
	// Any remaining keys, alphabetically.
	var rest []string
	for k := range pending {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	sortStrings(rest)
	for _, k := range rest {
		lines = append(lines, k+" = "+pending[k])
	}
	return lines
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// splitKeepEmpty splits content into lines without inventing a trailing empty
// element for a final newline.
func splitKeepEmpty(content string) []string {
	if content == "" {
		return nil
	}
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	// Drop a single trailing "" produced by a terminating newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// isTableHeader reports whether a trimmed line is a TOML table header [name]
// (we only need top-level tables, not array-of-tables).
func isTableHeader(trimmed string) bool {
	return strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") &&
		!strings.HasPrefix(trimmed, "[[")
}

// tomlKey extracts the bare key from a trimmed "key = value" line. It returns
// ("", false) for comments, blanks, and non key/value lines.
func tomlKey(trimmed string) (string, bool) {
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	eq := strings.IndexByte(trimmed, '=')
	if eq <= 0 {
		return "", false
	}
	key := strings.TrimSpace(trimmed[:eq])
	if key == "" {
		return "", false
	}
	return key, true
}
