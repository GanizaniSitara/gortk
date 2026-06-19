// Package rubocop is gortk's token-optimized RuboCop linter wrapper. It injects
// `--format json` for structured output, parses offenses grouped by file and
// sorted by severity, and emits a compact summary. It falls back to text
// parsing for autocorrect mode, when the user specifies a custom format, or when
// injected JSON output fails to parse. Faithful port of rtk's
// src/cmds/ruby/rubocop_cmd.rs.
package rubocop

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "rubocop",
		Summary: "RuboCop linter with compact output (Ruby)",
		Run:     Run,
	})
}

// ── JSON structures matching RuboCop's --format json output ─────────────────

type rubocopOutput struct {
	Files   []rubocopFile  `json:"files"`
	Summary rubocopSummary `json:"summary"`
}

type rubocopFile struct {
	Path     string           `json:"path"`
	Offenses []rubocopOffense `json:"offenses"`
}

type rubocopOffense struct {
	CopName     string          `json:"cop_name"`
	Severity    string          `json:"severity"`
	Message     string          `json:"message"`
	Correctable bool            `json:"correctable"`
	Location    rubocopLocation `json:"location"`
}

type rubocopLocation struct {
	StartLine int `json:"start_line"`
}

type rubocopSummary struct {
	OffenseCount             int `json:"offense_count"`
	TargetFileCount          int `json:"target_file_count"`
	InspectedFileCount       int `json:"inspected_file_count"`
	CorrectableOffenseCount  int `json:"correctable_offense_count"`
}

// ── Public entry point ───────────────────────────────────────────────────────

// Run executes the rubocop command. args are the args AFTER the command name.
func Run(args []string, verbose int) (int, error) {
	isAutocorrect := false
	for _, a := range args {
		if a == "-a" || a == "-A" || a == "--auto-correct" || a == "--auto-correct-all" {
			isAutocorrect = true
			break
		}
	}

	hasFormat := false
	for _, a := range args {
		if strings.HasPrefix(a, "--format") || strings.HasPrefix(a, "-f") {
			hasFormat = true
			break
		}
	}

	cmd := rubyExec("rubocop")
	if !hasFormat && !isAutocorrect {
		cmd.Args = append(cmd.Args, "--format", "json")
	}
	cmd.Args = append(cmd.Args, args...)

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: rubocop %s\n", strings.Join(args, " "))
	}

	opts := core.RunOptions{FilterStdoutOnly: true, TeeLabel: "rubocop"}
	return core.RunFiltered(cmd, "rubocop", strings.Join(args, " "), func(stdout string) string {
		if hasFormat || isAutocorrect {
			return filterRubocopText(stdout)
		}
		return filterRubocopJSON(stdout)
	}, opts)
}

// rubyExec builds an *exec.Cmd for a Ruby tool, auto-detecting bundle exec.
// Uses `bundle exec <tool>` when a Gemfile exists (transitive deps like rake
// won't appear in the Gemfile but still need bundler for version isolation).
func rubyExec(tool string) *execCmd {
	if _, err := os.Stat("Gemfile"); err == nil {
		c := core.ResolvedCommand("bundle", "exec", tool)
		return c
	}
	return core.ResolvedCommand(tool)
}

// ── JSON filtering ───────────────────────────────────────────────────────────

// severityRank ranks severity for ordering: lower = more severe.
func severityRank(severity string) int {
	switch severity {
	case "fatal", "error":
		return 0
	case "warning":
		return 1
	case "convention", "refactor", "info":
		return 2
	default:
		return 3
	}
}

func filterRubocopJSON(output string) string {
	if strings.TrimSpace(output) == "" {
		return "RuboCop: No output"
	}

	var rubocop rubocopOutput
	if err := json.Unmarshal([]byte(output), &rubocop); err != nil {
		fmt.Fprintf(os.Stderr, "[gortk] rubocop: JSON parse failed (%v)\n", err)
		return fallbackTail(output, "rubocop (JSON parse error)", 5)
	}

	s := rubocop.Summary

	if s.OffenseCount == 0 {
		return fmt.Sprintf("ok ✓ rubocop (%d files)", s.InspectedFileCount)
	}

	// When CorrectableOffenseCount is 0, it could mean the field was absent
	// (older RuboCop) or genuinely zero. Manual count as consistent fallback.
	correctableCount := s.CorrectableOffenseCount
	if correctableCount == 0 {
		for _, f := range rubocop.Files {
			for _, o := range f.Offenses {
				if o.Correctable {
					correctableCount++
				}
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "rubocop: %d offenses (%d files)\n", s.OffenseCount, s.InspectedFileCount)

	// Build list of files with offenses.
	var filesWithOffenses []*rubocopFile
	for i := range rubocop.Files {
		if len(rubocop.Files[i].Offenses) > 0 {
			filesWithOffenses = append(filesWithOffenses, &rubocop.Files[i])
		}
	}

	// Sort files: worst severity first, then alphabetically by path.
	worst := func(f *rubocopFile) int {
		m := 3
		for _, o := range f.Offenses {
			if r := severityRank(o.Severity); r < m {
				m = r
			}
		}
		return m
	}
	sort.SliceStable(filesWithOffenses, func(i, j int) bool {
		ai, aj := worst(filesWithOffenses[i]), worst(filesWithOffenses[j])
		if ai != aj {
			return ai < aj
		}
		return filesWithOffenses[i].Path < filesWithOffenses[j].Path
	})

	const maxFiles = 10
	const maxOffensesPerFile = 5

	limit := maxFiles
	if limit > len(filesWithOffenses) {
		limit = len(filesWithOffenses)
	}
	for _, file := range filesWithOffenses[:limit] {
		short := compactRubyPath(file.Path)
		fmt.Fprintf(&b, "\n%s\n", short)

		// Sort offenses within file: by severity rank, then by line number.
		sortedOffenses := make([]rubocopOffense, len(file.Offenses))
		copy(sortedOffenses, file.Offenses)
		sort.SliceStable(sortedOffenses, func(i, j int) bool {
			ri, rj := severityRank(sortedOffenses[i].Severity), severityRank(sortedOffenses[j].Severity)
			if ri != rj {
				return ri < rj
			}
			return sortedOffenses[i].Location.StartLine < sortedOffenses[j].Location.StartLine
		})

		offLimit := maxOffensesPerFile
		if offLimit > len(sortedOffenses) {
			offLimit = len(sortedOffenses)
		}
		for _, offense := range sortedOffenses[:offLimit] {
			firstMsgLine := firstLine(offense.Message)
			fmt.Fprintf(&b, "  :%d %s — %s\n", offense.Location.StartLine, offense.CopName, firstMsgLine)
		}
		if len(sortedOffenses) > maxOffensesPerFile {
			fmt.Fprintf(&b, "  … +%d more\n", len(sortedOffenses)-maxOffensesPerFile)
		}
	}

	if len(filesWithOffenses) > maxFiles {
		fmt.Fprintf(&b, "\n… +%d more files\n", len(filesWithOffenses)-maxFiles)
	}

	if correctableCount > 0 {
		fmt.Fprintf(&b, "\n(%d correctable, run `rubocop -A`)", correctableCount)
	}

	return strings.TrimSpace(b.String())
}

// ── Text fallback ────────────────────────────────────────────────────────────

func filterRubocopText(output string) string {
	// Check for Ruby/Bundler errors first -- show error, truncated to avoid
	// excessive tokens.
	for _, line := range splitLines(output) {
		t := strings.TrimSpace(line)
		if strings.Contains(t, "cannot load such file") ||
			strings.Contains(t, "Bundler::GemNotFound") ||
			strings.Contains(t, "Gem::MissingSpecError") ||
			strings.HasPrefix(t, "rubocop: command not found") ||
			strings.HasPrefix(t, "rubocop: No such file") {
			trimmed := strings.TrimSpace(output)
			allLines := splitLines(trimmed)
			take := 20
			if take > len(allLines) {
				take = len(allLines)
			}
			truncated := strings.Join(allLines[:take], "\n")
			totalLines := len(allLines)
			if totalLines > 20 {
				return fmt.Sprintf("RuboCop error:\n%s\n... (%d more lines)", truncated, totalLines-20)
			}
			return fmt.Sprintf("RuboCop error:\n%s", truncated)
		}
	}

	// Detect autocorrect/offense summary, scanning from the bottom.
	lines := splitLines(output)
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if strings.Contains(t, "inspected") && strings.Contains(t, "autocorrected") {
			files := extractLeadingNumber(t)
			corrected := extractAutocorrectCount(t)
			if files > 0 && corrected > 0 {
				return fmt.Sprintf("ok ✓ rubocop -A (%d files, %d autocorrected)", files, corrected)
			}
			return fmt.Sprintf("RuboCop: %s", t)
		}
		if strings.Contains(t, "inspected") && (strings.Contains(t, "offense") || strings.Contains(t, "no offenses")) {
			if strings.Contains(t, "no offenses") {
				files := extractLeadingNumber(t)
				if files > 0 {
					return fmt.Sprintf("ok ✓ rubocop (%d files)", files)
				}
				return "ok ✓ rubocop (no offenses)"
			}
			return fmt.Sprintf("RuboCop: %s", t)
		}
	}
	// Last resort: last 5 lines.
	return fallbackTail(output, "rubocop", 5)
}

// extractLeadingNumber extracts the leading number from a string like
// "15 files inspected".
func extractLeadingNumber(s string) int {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0
	}
	n, err := parseUint(fields[0])
	if err != nil {
		return 0
	}
	return n
}

// extractAutocorrectCount extracts the autocorrect count from a summary like
// "... 3 offenses autocorrected".
func extractAutocorrectCount(s string) int {
	parts := strings.Split(s, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		t := strings.TrimSpace(parts[i])
		if strings.Contains(t, "autocorrected") {
			return extractLeadingNumber(t)
		}
	}
	return 0
}

// compactRubyPath compacts a Ruby file path by finding the nearest Rails
// convention directory and stripping the absolute path prefix.
func compactRubyPath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")

	for _, prefix := range []string{
		"app/models/",
		"app/controllers/",
		"app/views/",
		"app/helpers/",
		"app/services/",
		"app/jobs/",
		"app/mailers/",
		"lib/",
		"spec/",
		"test/",
		"config/",
	} {
		if pos := strings.Index(path, prefix); pos >= 0 {
			return path[pos:]
		}
	}

	// Generic: strip up to last known directory marker.
	if pos := strings.LastIndex(path, "/app/"); pos >= 0 {
		return path[pos+1:]
	}
	if pos := strings.LastIndex(path, "/"); pos >= 0 {
		return path[pos+1:]
	}
	return path
}

// ── small local helpers (mirror rtk core::utils) ────────────────────────────

// fallbackTail returns the last n lines of output, emitting a notice to stderr.
// Mirrors rtk's utils::fallback_tail.
func fallbackTail(output, label string, n int) string {
	fmt.Fprintf(os.Stderr, "[gortk] %s: output format not recognized, showing last %d lines\n", label, n)
	lines := splitLines(output)
	start := len(lines) - n
	if start < 0 {
		start = 0
	}
	return strings.Join(lines[start:], "\n")
}

// splitLines mirrors Rust's str::lines(): splits on '\n' and drops a single
// trailing empty element produced by a trailing newline.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	// str::lines also tolerates trailing '\r'; core.NormalizeNewlines already
	// strips CR, but be defensive here too.
	for i := range parts {
		parts[i] = strings.TrimSuffix(parts[i], "\r")
	}
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// firstLine returns the first line of s (mirrors `s.lines().next().unwrap_or("")`).
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSuffix(s[:i], "\r")
	}
	return s
}

// parseUint parses a non-negative base-10 integer, rejecting empties and
// non-digits (matching Rust's usize::from_str on a whitespace-split word).
func parseUint(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// execCmd is the concrete *exec.Cmd type returned by core.ResolvedCommand.
type execCmd = exec.Cmd

var _ = filepath.Separator
