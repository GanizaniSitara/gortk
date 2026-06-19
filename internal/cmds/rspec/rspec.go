// Package rspec is gortk's token-optimized RSpec test runner wrapper. It
// injects `--format json` to get structured output, parses it to show only
// failures, and falls back to a state-machine text parser when JSON is
// unavailable (e.g. the user passed `--format documentation`) or when the
// injected JSON output fails to parse. Faithful port of rtk's
// src/cmds/ruby/rspec_cmd.rs.
package rspec

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "rspec",
		Summary: "RSpec test runner with compact output (Rails/Ruby)",
		Run:     Run,
	})
}

// rspec failures carry full backtraces — show fewer than a generic warning
// list. reduced(CAP_WARNINGS, 5) = 10 - 5 = 5.
const maxRspecFailures = core.CapWarnings - 5

// ── Noise-stripping regex patterns ──────────────────────────────────────────

var (
	reSpring      = regexp.MustCompile(`(?i)running via spring preloader`)
	reSimpleCov   = regexp.MustCompile(`(?i)(coverage report|simplecov|coverage/|\.simplecov|All Files.*Lines)`)
	reDeprecation = regexp.MustCompile(`^DEPRECATION WARNING:`)
	reFinishedIn  = regexp.MustCompile(`^Finished in \d`)
	reScreenshot  = regexp.MustCompile(`saved screenshot to (.+)`)
	reRspecSummary = regexp.MustCompile(`(\d+) examples?, (\d+) failures?`)
)

// ── JSON structures matching RSpec's --format json output ───────────────────

type rspecOutput struct {
	Examples []rspecExample `json:"examples"`
	Summary  rspecSummary   `json:"summary"`
}

type rspecExample struct {
	FullDescription string           `json:"full_description"`
	Status          string           `json:"status"`
	FilePath        string           `json:"file_path"`
	LineNumber      uint32           `json:"line_number"`
	Exception       *rspecException  `json:"exception"`
}

type rspecException struct {
	Class     string   `json:"class"`
	Message   string   `json:"message"`
	Backtrace []string `json:"backtrace"`
}

type rspecSummary struct {
	Duration                       float64 `json:"duration"`
	ExampleCount                   int     `json:"example_count"`
	FailureCount                   int     `json:"failure_count"`
	PendingCount                   int     `json:"pending_count"`
	ErrorsOutsideOfExamplesCount   int     `json:"errors_outside_of_examples_count"`
}

// ── Public entry point ───────────────────────────────────────────────────────

// Run executes the rspec command. args are the arguments after "rspec".
func Run(args []string, verbose int) (int, error) {
	hasFormat := hasFormatFlag(args)

	// Build the rspec command, auto-detecting bundle exec (Gemfile present).
	cmd := rubyExec("rspec")
	if !hasFormat {
		cmd.Args = append(cmd.Args, "--format", "json")
	}
	cmd.Args = append(cmd.Args, args...)

	if verbose > 0 {
		injected := ""
		if !hasFormat {
			injected = " --format json"
		}
		fmt.Fprintf(os.Stderr, "Running: rspec%s %s\n", injected, strings.Join(args, " "))
	}

	opts := core.RunOptions{FilterStdoutOnly: true, TeeLabel: "rspec"}
	return core.RunFiltered(cmd, "rspec", strings.Join(args, " "), func(stdout string) string {
		if hasFormat {
			stripped := stripNoise(stdout)
			return filterRspecText(stripped)
		}
		return filterRspecOutput(stdout)
	}, opts)
}

// hasFormatFlag reports whether the user already specified an output format.
func hasFormatFlag(args []string) bool {
	for _, a := range args {
		if a == "--format" ||
			a == "-f" ||
			strings.HasPrefix(a, "--format=") ||
			(strings.HasPrefix(a, "-f") && len(a) > 2 && !strings.HasPrefix(a, "--")) {
			return true
		}
	}
	return false
}

// rubyExec builds an *exec.Cmd for a Ruby tool, auto-detecting bundle exec.
// Uses `bundle exec <tool>` when a Gemfile exists in the current directory
// (transitive deps like rake won't appear in the Gemfile but still need
// bundler for version isolation).
func rubyExec(tool string) *execCmd {
	if _, err := os.Stat("Gemfile"); err == nil {
		c := core.ResolvedCommand("bundle", "exec", tool)
		return c
	}
	return core.ResolvedCommand(tool)
}

// execCmd is an alias to keep the import surface obvious; core.ResolvedCommand
// returns *exec.Cmd.
type execCmd = exec.Cmd

// ── Noise stripping ─────────────────────────────────────────────────────────

// stripNoise removes noise lines: Spring preloader, SimpleCov, DEPRECATION
// warnings, the "Finished in" timing line, and Capybara screenshot details
// (keeping the path only).
func stripNoise(output string) string {
	var result []string
	inSimpleCovBlock := false

	for _, line := range splitLines(output) {
		trimmed := strings.TrimSpace(line)

		// Skip Spring preloader messages.
		if reSpring.MatchString(trimmed) {
			continue
		}

		// Skip lines starting with "DEPRECATION WARNING:" (single-line only).
		if reDeprecation.MatchString(trimmed) {
			continue
		}

		// Skip "Finished in N seconds" line.
		if reFinishedIn.MatchString(trimmed) {
			continue
		}

		// SimpleCov block detection: once seen, skip until a blank line.
		if reSimpleCov.MatchString(trimmed) {
			inSimpleCovBlock = true
			continue
		}
		if inSimpleCovBlock {
			if trimmed == "" {
				inSimpleCovBlock = false
			}
			continue
		}

		// Capybara screenshots: keep only the path.
		if caps := reScreenshot.FindStringSubmatch(trimmed); caps != nil {
			result = append(result, fmt.Sprintf("[screenshot: %s]", strings.TrimSpace(caps[1])))
			continue
		}

		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// ── Output filtering ─────────────────────────────────────────────────────────

func filterRspecOutput(output string) string {
	if strings.TrimSpace(output) == "" {
		return "RSpec: No output"
	}

	// Try parsing as JSON first (happy path when --format json is injected).
	if rspec, ok := parseRspecJSON(output); ok {
		return buildRspecSummary(rspec)
	}

	// Strip noise (Spring, SimpleCov, etc.) and retry the JSON parse.
	stripped := stripNoise(output)
	if rspec, ok := parseRspecJSON(stripped); ok {
		return buildRspecSummary(rspec)
	}
	fmt.Fprintln(os.Stderr, "[gortk] rspec: JSON parse failed, using text fallback")

	return filterRspecText(stripped)
}

// parseRspecJSON parses RSpec --format json output. Returns (parsed, true) on
// success. Mirrors serde_json::from_str: the whole document must be valid JSON
// (trailing garbage fails), unknown fields are ignored, and missing optional
// fields default to their zero value.
func parseRspecJSON(output string) (*rspecOutput, bool) {
	var rspec rspecOutput
	if err := json.Unmarshal([]byte(output), &rspec); err != nil {
		return nil, false
	}
	return &rspec, true
}

func buildRspecSummary(rspec *rspecOutput) string {
	s := &rspec.Summary

	if s.ExampleCount == 0 && s.ErrorsOutsideOfExamplesCount == 0 {
		return "RSpec: No examples found"
	}

	if s.ExampleCount == 0 && s.ErrorsOutsideOfExamplesCount > 0 {
		return fmt.Sprintf("RSpec: %d errors outside of examples (%.2fs)",
			s.ErrorsOutsideOfExamplesCount, s.Duration)
	}

	if s.FailureCount == 0 && s.ErrorsOutsideOfExamplesCount == 0 {
		passed := satSub(s.ExampleCount, s.PendingCount)
		result := fmt.Sprintf("✓ RSpec: %d passed", passed)
		if s.PendingCount > 0 {
			result += fmt.Sprintf(", %d pending", s.PendingCount)
		}
		result += fmt.Sprintf(" (%.2fs)", s.Duration)
		return result
	}

	passed := satSub(s.ExampleCount, s.FailureCount+s.PendingCount)
	result := fmt.Sprintf("RSpec: %d passed, %d failed", passed, s.FailureCount)
	if s.PendingCount > 0 {
		result += fmt.Sprintf(", %d pending", s.PendingCount)
	}
	result += fmt.Sprintf(" (%.2fs)\n", s.Duration)

	var failures []*rspecExample
	for i := range rspec.Examples {
		if rspec.Examples[i].Status == "failed" {
			failures = append(failures, &rspec.Examples[i])
		}
	}

	if len(failures) == 0 {
		return strings.TrimSpace(result)
	}

	result += "\nFailures:\n"

	limit := maxRspecFailures
	if limit > len(failures) {
		limit = len(failures)
	}
	for i := 0; i < limit; i++ {
		example := failures[i]
		result += fmt.Sprintf("%d. ✗ %s\n   %s:%d\n",
			i+1, example.FullDescription, example.FilePath, example.LineNumber)

		if exc := example.Exception; exc != nil {
			shortClass := exc.Class
			if idx := strings.LastIndex(exc.Class, "::"); idx >= 0 {
				shortClass = exc.Class[idx+2:]
			}
			firstMsg := firstLine(exc.Message)
			result += fmt.Sprintf("   %s: %s\n", shortClass, truncate(firstMsg, 120))

			// First backtrace line not from gems/rspec internals.
			for _, bt := range exc.Backtrace {
				if !strings.Contains(bt, "/gems/") && !strings.Contains(bt, "lib/rspec") {
					result += fmt.Sprintf("   %s\n", truncate(bt, 120))
					break
				}
			}
		}

		// Blank separator between failures (matches the Rust min() bound).
		bound := len(failures)
		if maxRspecFailures < bound {
			bound = maxRspecFailures
		}
		if i < bound-1 {
			result += "\n"
		}
	}

	if len(failures) > maxRspecFailures {
		result += fmt.Sprintf("\n... +%d more failures\n", len(failures)-maxRspecFailures)
	}

	return strings.TrimSpace(result)
}

// textState is the state-machine state for the text fallback parser.
type textState int

const (
	stateHeader textState = iota
	stateFailures
	stateFailedExamples
	stateSummary
)

// filterRspecText is the state-machine text fallback parser used when JSON is
// unavailable.
func filterRspecText(output string) string {
	state := stateHeader
	var failures []string
	var currentFailure strings.Builder
	summaryLine := ""

	for _, line := range splitLines(output) {
		trimmed := strings.TrimSpace(line)

		switch state {
		case stateHeader:
			switch {
			case trimmed == "Failures:":
				state = stateFailures
			case trimmed == "Failed examples:":
				state = stateFailedExamples
			case reRspecSummary.MatchString(trimmed):
				summaryLine = trimmed
				state = stateSummary
			}
		case stateFailures:
			switch {
			case isNumberedFailure(trimmed):
				// A new failure block starts with a numbered pattern, e.g. "  1) ...".
				if strings.TrimSpace(currentFailure.String()) != "" {
					failures = append(failures, compactFailureBlock(currentFailure.String()))
				}
				currentFailure.Reset()
				currentFailure.WriteString(trimmed)
				currentFailure.WriteByte('\n')
			case trimmed == "Failed examples:":
				if strings.TrimSpace(currentFailure.String()) != "" {
					failures = append(failures, compactFailureBlock(currentFailure.String()))
				}
				currentFailure.Reset()
				state = stateFailedExamples
			case reRspecSummary.MatchString(trimmed):
				if strings.TrimSpace(currentFailure.String()) != "" {
					failures = append(failures, compactFailureBlock(currentFailure.String()))
				}
				currentFailure.Reset()
				summaryLine = trimmed
				state = stateSummary
			case trimmed != "":
				// Skip gem-internal backtrace lines.
				if isGemBacktrace(trimmed) {
					continue
				}
				currentFailure.WriteString(trimmed)
				currentFailure.WriteByte('\n')
			}
		case stateFailedExamples:
			if reRspecSummary.MatchString(trimmed) {
				summaryLine = trimmed
				state = stateSummary
			}
			// Skip "Failed examples:" section (just rspec commands to re-run).
		case stateSummary:
			// Done.
		}
		if state == stateSummary {
			break
		}
	}

	// Capture remaining failure.
	if strings.TrimSpace(currentFailure.String()) != "" && state == stateFailures {
		failures = append(failures, compactFailureBlock(currentFailure.String()))
	}

	// If we found a summary line, build the result.
	if summaryLine != "" {
		if len(failures) == 0 {
			return fmt.Sprintf("RSpec: %s", summaryLine)
		}
		result := fmt.Sprintf("RSpec: %s\n", summaryLine)
		limit := maxRspecFailures
		if limit > len(failures) {
			limit = len(failures)
		}
		for i := 0; i < limit; i++ {
			result += fmt.Sprintf("%d. ✗ %s\n", i+1, failures[i])
			bound := len(failures)
			if maxRspecFailures < bound {
				bound = maxRspecFailures
			}
			if i < bound-1 {
				result += "\n"
			}
		}
		if len(failures) > maxRspecFailures {
			result += fmt.Sprintf("\n... +%d more failures\n", len(failures)-maxRspecFailures)
		}
		return strings.TrimSpace(result)
	}

	// Fallback: look for a summary anywhere, scanning from the bottom.
	lines := splitLines(output)
	for i := len(lines) - 1; i >= 0; i-- {
		t := strings.TrimSpace(lines[i])
		if strings.Contains(t, "example") && (strings.Contains(t, "failure") || strings.Contains(t, "pending")) {
			return fmt.Sprintf("RSpec: %s", t)
		}
	}

	// Last resort: last 5 lines.
	return fallbackTail(output, "rspec", 5)
}

// isNumberedFailure reports whether a line is a numbered failure like
// "1) User#full_name...".
func isNumberedFailure(line string) bool {
	trimmed := strings.TrimSpace(line)
	pos := strings.IndexByte(trimmed, ')')
	if pos < 0 {
		return false
	}
	prefix := trimmed[:pos]
	if prefix == "" {
		return false
	}
	for _, c := range prefix {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isGemBacktrace reports whether a backtrace line is from gems/rspec internals.
func isGemBacktrace(line string) bool {
	return strings.Contains(line, "/gems/") ||
		strings.Contains(line, "lib/rspec") ||
		strings.Contains(line, "lib/ruby/") ||
		strings.Contains(line, "vendor/bundle")
}

// compactFailureBlock compacts a failure block: extract key info, strip the
// verbose backtrace.
func compactFailureBlock(block string) string {
	var lines []string
	for _, l := range splitLines(block) {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}

	// Extract spec file:line (lines starting with # ./spec/ or # ./test/).
	specFile := ""
	var keptLines []string

	for _, line := range lines {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "# ./spec/") || strings.HasPrefix(t, "# ./test/"):
			specFile = strings.TrimPrefix(t, "# ")
		case strings.HasPrefix(t, "#") && (strings.Contains(t, "/gems/") || strings.Contains(t, "lib/rspec")):
			// Skip gem backtrace.
			continue
		default:
			keptLines = append(keptLines, t)
		}
	}

	result := strings.Join(keptLines, "\n   ")
	if specFile != "" {
		result += "\n   " + specFile
	}
	return result
}

// ── Small helpers (Rust core::utils equivalents) ─────────────────────────────

// truncate truncates s to at most maxLen runes, appending "..." when cut.
// Rune-aware to mirror Rust's .chars()-based truncation.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen < 3 {
		return "..."
	}
	return string(runes[:maxLen-3]) + "..."
}

// fallbackTail returns the last n lines of output and notes the unrecognized
// format on stderr.
func fallbackTail(output, label string, n int) string {
	fmt.Fprintf(os.Stderr, "[gortk] %s: output format not recognized, showing last %d lines\n", label, n)
	lines := splitLines(output)
	start := len(lines) - n
	if start < 0 {
		start = 0
	}
	return strings.Join(lines[start:], "\n")
}

// satSub is a saturating subtraction for non-negative counts.
func satSub(a, b int) int {
	if b > a {
		return 0
	}
	return a - b
}

// firstLine returns the first line of s (Rust's s.lines().next()).
func firstLine(s string) string {
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		return s[:idx]
	}
	return s
}

// splitLines mirrors Rust's str::lines(): splits on '\n' and drops a single
// trailing empty element so a trailing newline does not yield a phantom "".
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// ensure filepath stays referenced for native-path discipline even though
// rubyExec relies on cwd-relative "Gemfile"; kept explicit for clarity.
var _ = filepath.Separator
