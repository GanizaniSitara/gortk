package deps

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// cargoDepRE matches a dependency line in Cargo.toml: either `name = "version"`
// or `name = { ..., version = "x", ... }`.
var cargoDepRE = regexp.MustCompile(`^([a-zA-Z0-9_-]+)\s*=\s*(?:"([^"]+)"|.*version\s*=\s*"([^"]+)")`)

// cargoSectionRE matches a TOML section header `[section]`.
var cargoSectionRE = regexp.MustCompile(`^\[([^\]]+)\]`)

// requirementsRE matches a pip requirement: a package name optionally followed
// by a version specifier.
var requirementsRE = regexp.MustCompile(`^([a-zA-Z0-9_-]+)([=<>!~]+.*)?$`)

func summarizeCargo(content string) string {
	currentSection := ""
	var deps, devDeps []string
	var out strings.Builder

	for _, line := range lines(content) {
		if m := cargoSectionRE.FindStringSubmatch(line); m != nil {
			currentSection = m[1]
		} else if m := cargoDepRE.FindStringSubmatch(line); m != nil {
			name := m[1]
			version := "*"
			if m[2] != "" {
				version = m[2]
			} else if m[3] != "" {
				version = m[3]
			}
			dep := fmt.Sprintf("%s (%s)", name, version)
			switch currentSection {
			case "dependencies":
				deps = append(deps, dep)
			case "dev-dependencies":
				devDeps = append(devDeps, dep)
			}
		}
	}

	if len(deps) > 0 {
		out.WriteString(fmt.Sprintf("  Dependencies (%d):\n", len(deps)))
		writeCapped(&out, deps, maxDeps)
	}
	if len(devDeps) > 0 {
		out.WriteString(fmt.Sprintf("  Dev (%d):\n", len(devDeps)))
		writeCapped(&out, devDeps, maxDevDeps)
	}
	return out.String()
}

func summarizePackageJSON(content string) string {
	var out strings.Builder

	name, hasName := jsonStringField(content, "name")
	if hasName {
		version, hasVer := jsonStringField(content, "version")
		if !hasVer {
			version = "?"
		}
		out.WriteString(fmt.Sprintf("  %s @ %s\n", name, version))
	}

	if deps, ok := jsonObjectPairs(content, "dependencies"); ok {
		out.WriteString(fmt.Sprintf("  Dependencies (%d):\n", len(deps)))
		for i, kv := range deps {
			if i >= maxDeps {
				out.WriteString(fmt.Sprintf("    ... +%d more\n", len(deps)-maxDeps))
				break
			}
			version := kv.value
			if version == "" {
				version = "*"
			}
			out.WriteString(fmt.Sprintf("    %s (%s)\n", kv.key, version))
		}
	}

	if devDeps, ok := jsonObjectPairs(content, "devDependencies"); ok {
		out.WriteString(fmt.Sprintf("  Dev Dependencies (%d):\n", len(devDeps)))
		for i, kv := range devDeps {
			if i >= maxDevDeps {
				out.WriteString(fmt.Sprintf("    ... +%d more\n", len(devDeps)-maxDevDeps))
				break
			}
			out.WriteString(fmt.Sprintf("    %s\n", kv.key))
		}
	}
	return out.String()
}

func summarizeRequirements(content string) string {
	var deps []string
	var out strings.Builder

	for _, line := range lines(content) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if m := requirementsRE.FindStringSubmatch(line); m != nil {
			deps = append(deps, m[1]+m[2])
		}
	}

	out.WriteString(fmt.Sprintf("  Packages (%d):\n", len(deps)))
	writeCapped(&out, deps, maxDeps)
	return out.String()
}

func summarizePyproject(content string) string {
	inDeps := false
	var deps []string
	var out strings.Builder

	for _, line := range lines(content) {
		if strings.Contains(line, "dependencies") && strings.Contains(line, "[") {
			inDeps = true
			continue
		}
		if inDeps {
			if strings.TrimSpace(line) == "]" {
				break
			}
			trimmed := strings.TrimFunc(strings.TrimSpace(line), func(r rune) bool {
				return r == '"' || r == '\'' || r == ','
			})
			if trimmed != "" {
				deps = append(deps, trimmed)
			}
		}
	}

	if len(deps) > 0 {
		out.WriteString(fmt.Sprintf("  Dependencies (%d):\n", len(deps)))
		writeCapped(&out, deps, maxDeps)
	}
	return out.String()
}

func summarizeGoMod(content string) string {
	moduleName := ""
	goVersion := ""
	var deps []string
	inRequire := false
	var out strings.Builder

	for _, line := range lines(content) {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "module "):
			moduleName = strings.TrimPrefix(line, "module ")
		case strings.HasPrefix(line, "go "):
			goVersion = strings.TrimPrefix(line, "go ")
		case line == "require (":
			inRequire = true
		case line == ")":
			inRequire = false
		case inRequire && !strings.HasPrefix(line, "//"):
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				deps = append(deps, parts[0]+" "+parts[1])
			}
		case strings.HasPrefix(line, "require ") && !strings.Contains(line, "("):
			deps = append(deps, strings.TrimPrefix(line, "require "))
		}
	}

	if moduleName != "" {
		out.WriteString(fmt.Sprintf("  %s (go %s)\n", moduleName, goVersion))
	}
	if len(deps) > 0 {
		out.WriteString(fmt.Sprintf("  Dependencies (%d):\n", len(deps)))
		writeCapped(&out, deps, maxDeps)
	}
	return out.String()
}

// writeCapped writes up to cap entries (one per indented line) and a
// "... +N more" line when the list exceeds the cap.
func writeCapped(out *strings.Builder, items []string, cap int) {
	limit := cap
	if limit > len(items) {
		limit = len(items)
	}
	for _, it := range items[:limit] {
		out.WriteString(fmt.Sprintf("    %s\n", it))
	}
	if len(items) > cap {
		out.WriteString(fmt.Sprintf("    ... +%d more\n", len(items)-cap))
	}
}

// --- minimal order-preserving JSON helpers ---------------------------------

type jsonKV struct {
	key   string
	value string // "" for non-string values, matching Rust's as_str().unwrap_or
}

// jsonStringField returns a top-level string field's value.
func jsonStringField(content, field string) (string, bool) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &top); err != nil {
		return "", false
	}
	raw, ok := top[field]
	if !ok {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// jsonObjectPairs returns the key/value pairs of a top-level object field, in
// document order (mirroring serde_json's preserve_order feature). Non-string
// values yield an empty value string.
func jsonObjectPairs(content, field string) ([]jsonKV, bool) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &top); err != nil {
		return nil, false
	}
	raw, ok := top[field]
	if !ok {
		return nil, false
	}
	return orderedObjectPairs(raw)
}

// orderedObjectPairs decodes a JSON object preserving key order via streaming
// token decoding.
func orderedObjectPairs(raw json.RawMessage) ([]jsonKV, bool) {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	tok, err := dec.Token()
	if err != nil {
		return nil, false
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, false
	}
	var pairs []jsonKV
	for dec.More() {
		keyTok, err := dec.Token()
		if err != nil {
			return nil, false
		}
		key, ok := keyTok.(string)
		if !ok {
			return nil, false
		}
		var rawVal json.RawMessage
		if err := dec.Decode(&rawVal); err != nil {
			return nil, false
		}
		val := ""
		var s string
		if json.Unmarshal(rawVal, &s) == nil {
			val = s
		}
		pairs = append(pairs, jsonKV{key: key, value: val})
	}
	return pairs, true
}
