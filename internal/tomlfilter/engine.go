// Package tomlfilter implements gortk's declarative, TOML-defined output
// filters — the fallback path that compresses output from tools without a
// dedicated Go command module (make, jq, helm, ping, ...). It is a faithful
// port of rtk's src/core/toml_filter.rs.
//
// Builtin filters are embedded at build time from builtin/*.toml. Each file is
// a self-contained document of the form:
//
//	[filters.<name>]
//	match_command = "^make\\b"
//	strip_lines_matching = ["^make\\[\\d+\\]:"]
//	max_lines = 50
//	on_empty = "make: ok"
//
//	[[tests.<name>]]
//	name = "..."
//	input = "..."
//	expected = "..."
package tomlfilter

import (
	"embed"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"gortk/internal/core"
)

//go:embed builtin/*.toml
var builtinFS embed.FS

// ReplaceRule is a line-level regex substitution (stage 2).
type ReplaceRule struct {
	Pattern     string `toml:"pattern"`
	Replacement string `toml:"replacement"`
}

// MatchOutputRule short-circuits the whole blob to Message when Pattern matches
// (stage 3), unless Unless also matches.
type MatchOutputRule struct {
	Pattern string `toml:"pattern"`
	Message string `toml:"message"`
	Unless  string `toml:"unless"`
}

// FilterDef is the raw TOML form of a filter, before compilation.
type FilterDef struct {
	Description        string            `toml:"description"`
	MatchCommand       string            `toml:"match_command"`
	StripANSI          bool              `toml:"strip_ansi"`
	Replace            []ReplaceRule     `toml:"replace"`
	MatchOutput        []MatchOutputRule `toml:"match_output"`
	StripLinesMatching []string          `toml:"strip_lines_matching"`
	KeepLinesMatching  []string          `toml:"keep_lines_matching"`
	TruncateLinesAt    *int              `toml:"truncate_lines_at"`
	HeadLines          *int              `toml:"head_lines"`
	TailLines          *int              `toml:"tail_lines"`
	MaxLines           *int              `toml:"max_lines"`
	OnEmpty            *string           `toml:"on_empty"`
	FilterStderr       bool              `toml:"filter_stderr"`
}

// TestDef is one inline test for a filter (used by `gortk verify`).
type TestDef struct {
	Name     string `toml:"name"`
	Input    string `toml:"input"`
	Expected string `toml:"expected"`
}

type fileDoc struct {
	SchemaVersion int                  `toml:"schema_version"`
	Filters       map[string]FilterDef `toml:"filters"`
	Tests         map[string][]TestDef `toml:"tests"`
}

type compiledReplace struct {
	pattern     *regexp.Regexp
	replacement string
}

type compiledMatchOutput struct {
	pattern *regexp.Regexp
	message string
	unless  *regexp.Regexp
}

type lineFilterKind int

const (
	lineFilterNone lineFilterKind = iota
	lineFilterStrip
	lineFilterKeep
)

// CompiledFilter is a parsed, regex-compiled filter ready to apply.
type CompiledFilter struct {
	Name         string
	Description  string
	FilterStderr bool

	matchRegex      *regexp.Regexp
	stripANSI       bool
	replace         []compiledReplace
	matchOutput     []compiledMatchOutput
	lineFilterKind  lineFilterKind
	lineFilterSet   []*regexp.Regexp
	truncateLinesAt *int
	headLines       *int
	tailLines       *int
	maxLines        *int
	onEmpty         *string

	tests []TestDef
}

var registry []*CompiledFilter

func init() {
	entries, err := builtinFS.ReadDir("builtin")
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".toml") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // deterministic ordering
	for _, n := range names {
		data, err := builtinFS.ReadFile("builtin/" + n)
		if err != nil {
			continue
		}
		// Embedded files may carry CRLF from a Windows checkout; normalize so
		// multi-line string contents (inline test fixtures) use LF.
		data = []byte(core.NormalizeNewlines(string(data)))
		var doc fileDoc
		if err := toml.Unmarshal(data, &doc); err != nil {
			// Skip a malformed builtin rather than crash the whole CLI.
			continue
		}
		for name, def := range doc.Filters {
			cf, err := compile(name, def)
			if err != nil {
				continue
			}
			cf.tests = doc.Tests[name]
			registry = append(registry, cf)
		}
	}
	sort.Slice(registry, func(i, j int) bool { return registry[i].Name < registry[j].Name })
}

func compile(name string, def FilterDef) (*CompiledFilter, error) {
	if len(def.StripLinesMatching) > 0 && len(def.KeepLinesMatching) > 0 {
		return nil, fmt.Errorf("filter %q: strip_lines_matching and keep_lines_matching are mutually exclusive", name)
	}
	matchRe, err := regexp.Compile(def.MatchCommand)
	if err != nil {
		return nil, fmt.Errorf("filter %q: invalid match_command %q: %w", name, def.MatchCommand, err)
	}

	cf := &CompiledFilter{
		Name:            name,
		Description:     def.Description,
		FilterStderr:    def.FilterStderr,
		matchRegex:      matchRe,
		stripANSI:       def.StripANSI,
		truncateLinesAt: def.TruncateLinesAt,
		headLines:       def.HeadLines,
		tailLines:       def.TailLines,
		maxLines:        def.MaxLines,
		onEmpty:         def.OnEmpty,
	}

	for _, r := range def.Replace {
		pat, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("filter %q: invalid replace pattern %q: %w", name, r.Pattern, err)
		}
		cf.replace = append(cf.replace, compiledReplace{pattern: pat, replacement: r.Replacement})
	}

	for _, r := range def.MatchOutput {
		pat, err := regexp.Compile(r.Pattern)
		if err != nil {
			return nil, fmt.Errorf("filter %q: invalid match_output pattern %q: %w", name, r.Pattern, err)
		}
		cmo := compiledMatchOutput{pattern: pat, message: r.Message}
		if r.Unless != "" {
			u, err := regexp.Compile(r.Unless)
			if err != nil {
				return nil, fmt.Errorf("filter %q: invalid match_output unless %q: %w", name, r.Unless, err)
			}
			cmo.unless = u
		}
		cf.matchOutput = append(cf.matchOutput, cmo)
	}

	switch {
	case len(def.StripLinesMatching) > 0:
		cf.lineFilterKind = lineFilterStrip
		cf.lineFilterSet, err = compileSet(def.StripLinesMatching)
	case len(def.KeepLinesMatching) > 0:
		cf.lineFilterKind = lineFilterKeep
		cf.lineFilterSet, err = compileSet(def.KeepLinesMatching)
	}
	if err != nil {
		return nil, fmt.Errorf("filter %q: %w", name, err)
	}

	return cf, nil
}

func compileSet(patterns []string) ([]*regexp.Regexp, error) {
	set := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("invalid line pattern %q: %w", p, err)
		}
		set = append(set, re)
	}
	return set, nil
}

func anyMatch(set []*regexp.Regexp, s string) bool {
	for _, re := range set {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// FindMatching returns the first filter whose match_command regex matches the
// given command string, or nil if none apply.
func FindMatching(command string) *CompiledFilter {
	for _, f := range registry {
		if f.matchRegex.MatchString(command) {
			return f
		}
	}
	return nil
}

// All returns every compiled builtin filter (sorted by name).
func All() []*CompiledFilter {
	out := make([]*CompiledFilter, len(registry))
	copy(out, registry)
	return out
}

// Tests returns the inline tests defined for this filter.
func (f *CompiledFilter) Tests() []TestDef { return f.tests }

// Apply runs the 8-stage compression pipeline over raw output, mirroring rtk's
// apply_filter exactly.
func (f *CompiledFilter) Apply(stdout string) string {
	stdout = core.NormalizeNewlines(stdout)
	lines := strings.Split(stdout, "\n")
	// strings.Split keeps a trailing "" for a final newline; rtk's .lines()
	// drops it. Match that so line counts and truncation align.
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}

	// 1. strip_ansi
	if f.stripANSI {
		for i, l := range lines {
			lines[i] = core.StripANSI(l)
		}
	}

	// 2. replace (chained, line-by-line)
	if len(f.replace) > 0 {
		for i, l := range lines {
			for _, r := range f.replace {
				l = r.pattern.ReplaceAllString(l, r.replacement)
			}
			lines[i] = l
		}
	}

	// 3. match_output — short-circuit on full-blob match (first rule wins)
	if len(f.matchOutput) > 0 {
		blob := strings.Join(lines, "\n")
		for _, rule := range f.matchOutput {
			if rule.pattern.MatchString(blob) {
				if rule.unless != nil && rule.unless.MatchString(blob) {
					continue
				}
				return rule.message
			}
		}
	}

	// 4. strip OR keep (mutually exclusive)
	switch f.lineFilterKind {
	case lineFilterStrip:
		lines = retain(lines, func(l string) bool { return !anyMatch(f.lineFilterSet, l) })
	case lineFilterKeep:
		lines = retain(lines, func(l string) bool { return anyMatch(f.lineFilterSet, l) })
	}

	// 5. truncate_lines_at (rune-safe)
	if f.truncateLinesAt != nil {
		max := *f.truncateLinesAt
		for i, l := range lines {
			lines[i] = truncateRunes(l, max)
		}
	}

	// 6. head + tail
	total := len(lines)
	switch {
	case f.headLines != nil && f.tailLines != nil:
		head, tail := *f.headLines, *f.tailLines
		if total > head+tail {
			out := append([]string{}, lines[:head]...)
			out = append(out, fmt.Sprintf("... (%d lines omitted)", total-head-tail))
			out = append(out, lines[total-tail:]...)
			lines = out
		}
	case f.headLines != nil:
		head := *f.headLines
		if total > head {
			out := append([]string{}, lines[:head]...)
			out = append(out, fmt.Sprintf("... (%d lines omitted)", total-head))
			lines = out
		}
	case f.tailLines != nil:
		tail := *f.tailLines
		if total > tail {
			omitted := total - tail
			out := []string{fmt.Sprintf("... (%d lines omitted)", omitted)}
			out = append(out, lines[omitted:]...)
			lines = out
		}
	}

	// 7. max_lines — absolute cap after head/tail
	if f.maxLines != nil && len(lines) > *f.maxLines {
		truncated := len(lines) - *f.maxLines
		lines = lines[:*f.maxLines]
		lines = append(lines, fmt.Sprintf("... (%d lines truncated)", truncated))
	}

	// 8. on_empty
	result := strings.Join(lines, "\n")
	if strings.TrimSpace(result) == "" && f.onEmpty != nil {
		return *f.onEmpty
	}
	return result
}

func retain(lines []string, keep func(string) bool) []string {
	out := lines[:0]
	for _, l := range lines {
		if keep(l) {
			out = append(out, l)
		}
	}
	return out
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
