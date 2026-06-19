// Package pytest is gortk's token-optimized pytest wrapper. It runs pytest (or
// `python -m pytest` when pytest is not on PATH), then filters the output down
// to the summary line, expected-failure outcomes (xfail/xpass), and the key
// detail of each failure. Faithful port of rtk's src/cmds/python/pytest_cmd.rs.
//
// Like rtk, this wraps the platform `pytest`; gortk resolves it PATHEXT-aware
// via core.ResolvedCommand. The output-compression logic lives in pure helper
// functions (filterPytestOutput, buildPytestSummary, parseSummaryLine) so it can
// be tested directly against the ported Rust spec.
//
// Note: rtk appends explicit tee hints (core::tee::force_tee_hint /
// force_tee_tail_hint) when the xfail/failure lists are capped. gortk's
// RunFiltered already tees the raw output on failure, so those side-channels are
// dropped to avoid duplicating the mechanism.
package pytest

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

// Caps mirror rtk's MAX_XFAIL / MAX_PYTEST_FAILURES (both CAP_WARNINGS).
const (
	maxXfail          = core.CapWarnings
	maxPytestFailures = core.CapWarnings
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "pytest",
		Summary: "Run pytest with compact output (failures + summary only)",
		Run:     Run,
	})
}

// Run executes the gortk `pytest` command. args are the tokens after "pytest";
// verbose is the -v count. It mirrors rtk's run(): resolve pytest (or fall back
// to `python -m pytest`), inject compact-friendly default flags, then capture
// and filter the output stdout-only with a tee on failure.
func Run(args []string, verbose int) (int, error) {
	cmd := core.ResolvedCommand("pytest")
	if !core.ToolExists("pytest") {
		cmd = core.ResolvedCommand("python")
		cmd.Args = append(cmd.Args, "-m", "pytest")
	}

	hasTBFlag := false
	hasQuietFlag := false
	hasReportFlag := false
	for _, a := range args {
		if strings.HasPrefix(a, "--tb") {
			hasTBFlag = true
		}
		if a == "-q" || a == "--quiet" {
			hasQuietFlag = true
		}
		// Only treat a short `-r…` as pytest's report flag (not `--randomly-seed` etc.)
		if strings.HasPrefix(a, "-r") && !strings.HasPrefix(a, "--") {
			hasReportFlag = true
		}
	}

	if !hasTBFlag {
		cmd.Args = append(cmd.Args, "--tb=short")
	}
	if !hasQuietFlag {
		cmd.Args = append(cmd.Args, "-q")
	}
	// Surface xfailed/xpassed (and their reasons) in the short summary section so
	// the compact output can report expected failures and — crucially —
	// unexpected passes (XPASS), which signal a behavior change.
	if !hasReportFlag {
		cmd.Args = append(cmd.Args, "-rxX")
	}

	cmd.Args = append(cmd.Args, args...)

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: pytest --tb=short -q %s\n", strings.Join(args, " "))
	}

	opts := core.RunOptions{FilterStdoutOnly: true, TeeLabel: "pytest"}
	return core.RunFiltered(cmd, "pytest", strings.Join(args, " "), filterPytestOutput, opts)
}

// truncate truncates s to maxLen characters (counted as runes), appending "..."
// when truncation occurs. Faithful port of rtk's core::utils::truncate: when
// maxLen < 3 it returns just "...".
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

// lines splits text into lines with Rust str::lines() semantics: split on "\n"
// and drop a single trailing empty element. (rtk also drops a trailing "\r"; the
// runner already normalizes CRLF to "\n" before the filter sees the text.)
func lines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// parseState tracks which section of pytest output we are parsing. Mirrors rtk's
// ParseState enum.
type parseState int

const (
	stateHeader parseState = iota
	stateTestProgress
	stateFailures
	stateSummary
)

// filterPytestOutput compresses pytest output down to the summary line, any
// xfail/xpass outcomes, and the key detail of each failure. Faithful port of
// rtk's filter_pytest_output.
func filterPytestOutput(output string) string {
	state := stateHeader
	var testFiles []string
	var failures []string
	var currentFailure []string
	var xfailLines []string
	summaryLine := ""

	for _, line := range lines(output) {
		trimmed := strings.TrimSpace(line)

		// State transitions.
		switch {
		case strings.HasPrefix(trimmed, "===") && strings.Contains(trimmed, "test session starts"):
			state = stateHeader
			continue
		case strings.HasPrefix(trimmed, "===") && strings.Contains(trimmed, "FAILURES"):
			state = stateFailures
			continue
		case strings.HasPrefix(trimmed, "===") && strings.Contains(trimmed, "short test summary"):
			state = stateSummary
			// Save current failure if any.
			if len(currentFailure) > 0 {
				failures = append(failures, strings.Join(currentFailure, "\n"))
				currentFailure = nil
			}
			continue
		case strings.HasPrefix(trimmed, "===") &&
			(strings.Contains(trimmed, "passed") ||
				strings.Contains(trimmed, "failed") ||
				strings.Contains(trimmed, "skipped")):
			summaryLine = trimmed
			continue
		case summaryLine == "" &&
			!strings.HasPrefix(trimmed, "===") &&
			!strings.HasPrefix(trimmed, "FAILED") &&
			!strings.HasPrefix(trimmed, "ERROR") &&
			(strings.Contains(trimmed, " passed") ||
				strings.Contains(trimmed, " failed") ||
				strings.Contains(trimmed, " skipped")) &&
			strings.Contains(trimmed, " in "):
			// quiet mode (-q): bare summary without === wrapper, e.g.
			// "5 failed, 1698 passed, 2 skipped in 108.89s".
			summaryLine = trimmed
			continue
		}

		// Process based on state.
		switch state {
		case stateHeader:
			if strings.HasPrefix(trimmed, "collected") {
				state = stateTestProgress
			}
		case stateTestProgress:
			// Lines like "tests/test_foo.py ....  [ 40%]".
			if trimmed != "" &&
				!strings.HasPrefix(trimmed, "===") &&
				(strings.Contains(trimmed, ".py") || strings.Contains(trimmed, "%]")) {
				testFiles = append(testFiles, trimmed)
			}
		case stateFailures:
			// Collect failure details.
			if strings.HasPrefix(trimmed, "___") {
				// New failure section.
				if len(currentFailure) > 0 {
					failures = append(failures, strings.Join(currentFailure, "\n"))
					currentFailure = nil
				}
				currentFailure = append(currentFailure, trimmed)
			} else if trimmed != "" && !strings.HasPrefix(trimmed, "===") {
				currentFailure = append(currentFailure, trimmed)
			}
		case stateSummary:
			// FAILED test lines.
			if strings.HasPrefix(trimmed, "FAILED") || strings.HasPrefix(trimmed, "ERROR") {
				failures = append(failures, trimmed)
			} else if strings.HasPrefix(trimmed, "XFAIL") || strings.HasPrefix(trimmed, "XPASS") {
				xfailLines = append(xfailLines, trimmed)
			}
		}
	}

	// Save last failure if any.
	if len(currentFailure) > 0 {
		failures = append(failures, strings.Join(currentFailure, "\n"))
	}

	return buildPytestSummary(summaryLine, testFiles, failures, xfailLines)
}

// pytestCounts holds the parsed test outcome counts. Mirrors rtk's PytestCounts.
type pytestCounts struct {
	passed  int
	failed  int
	skipped int
	xfailed int
	xpassed int
}

// buildPytestSummary renders the compact summary from the parsed counts,
// xfail/xpass lines, and failure blocks. Faithful port of rtk's
// build_pytest_summary. The unused testFiles param is kept to mirror the Rust
// signature.
func buildPytestSummary(summary string, _ []string, failures []string, xfailLines []string) string {
	counts := parseSummaryLine(summary)

	if counts.passed == 0 && counts.failed == 0 && counts.skipped == 0 &&
		counts.xfailed == 0 && counts.xpassed == 0 {
		return "Pytest: No tests collected"
	}

	extrasPresent := counts.skipped > 0 || counts.xfailed > 0 || counts.xpassed > 0 || len(xfailLines) > 0

	if counts.failed == 0 && counts.passed > 0 && !extrasPresent {
		return fmt.Sprintf("Pytest: %d passed", counts.passed)
	}

	var result strings.Builder
	fmt.Fprintf(&result, "Pytest: %d passed, %d failed", counts.passed, counts.failed)
	if counts.skipped > 0 {
		fmt.Fprintf(&result, ", %d skipped", counts.skipped)
	}
	if counts.xfailed > 0 {
		fmt.Fprintf(&result, ", %d xfailed", counts.xfailed)
	}
	if counts.xpassed > 0 {
		fmt.Fprintf(&result, ", %d xpassed", counts.xpassed)
	}
	result.WriteByte('\n')

	// Surface xfail/xpass entries (with their reasons) — XPASS in particular
	// signals that something expected-to-fail now passes. rtk appends a tee hint
	// when the list is capped; gortk's RunFiltered tees on failure already, so
	// the explicit hint is dropped.
	if len(xfailLines) > 0 {
		result.WriteString("\nExpected-failure outcomes:\n")
		limit := len(xfailLines)
		if limit > maxXfail {
			limit = maxXfail
		}
		for _, line := range xfailLines[:limit] {
			fmt.Fprintf(&result, "  %s\n", truncate(line, 120))
		}
		if len(xfailLines) > maxXfail {
			fmt.Fprintf(&result, "  … +%d more\n", len(xfailLines)-maxXfail)
		}
	}

	if len(failures) == 0 {
		return strings.TrimSpace(result.String())
	}

	// Show failures (limit to key information).
	result.WriteString("\nFailures:\n")

	failLimit := len(failures)
	if failLimit > maxPytestFailures {
		failLimit = maxPytestFailures
	}
	for i := 0; i < failLimit; i++ {
		failure := failures[i]
		// Extract test name and key error info.
		failLines := strings.Split(failure, "\n")

		// First line is usually test name (after ___).
		if len(failLines) > 0 {
			firstLine := failLines[0]
			if strings.HasPrefix(firstLine, "___") {
				// Extract test name between ___.
				testName := strings.TrimSpace(strings.Trim(firstLine, "_"))
				fmt.Fprintf(&result, "%d. [FAIL] %s\n", i+1, testName)
			} else if strings.HasPrefix(firstLine, "FAILED") {
				// Summary format: "FAILED tests/test_foo.py::test_bar - AssertionError".
				parts := strings.Split(firstLine, " - ")
				if len(parts) > 0 {
					testName := strings.TrimPrefix(parts[0], "FAILED ")
					fmt.Fprintf(&result, "%d. [FAIL] %s\n", i+1, testName)
				}
				if len(parts) > 1 {
					fmt.Fprintf(&result, "     %s\n", truncate(parts[1], 100))
				}
				continue
			}
		}

		// Show relevant error lines (assertions, errors, file locations).
		relevantLines := 0
		for _, line := range failLines[1:] {
			lineLower := strings.ToLower(line)
			isRelevant := strings.HasPrefix(strings.TrimSpace(line), ">") ||
				strings.HasPrefix(strings.TrimSpace(line), "E") ||
				strings.Contains(lineLower, "assert") ||
				strings.Contains(lineLower, "error") ||
				strings.Contains(line, ".py:")

			if isRelevant && relevantLines < 3 {
				fmt.Fprintf(&result, "     %s\n", truncate(line, 100))
				relevantLines++
			}
		}

		if i < len(failures)-1 {
			result.WriteByte('\n')
		}
	}

	if len(failures) > maxPytestFailures {
		// rtk appends a tee hint here; gortk's RunFiltered tees on failure already.
		fmt.Fprintf(&result, "\n… +%d more failures\n", len(failures)-maxPytestFailures)
	}

	return strings.TrimSpace(result.String())
}

// parseSummaryLine parses a pytest summary line into counts. Faithful port of
// rtk's parse_summary_line.
func parseSummaryLine(summary string) pytestCounts {
	var counts pytestCounts

	// Parse lines like "=== 4 passed, 1 failed, 2 xfailed, 1 xpassed in 0.50s ===".
	for _, part := range strings.Split(summary, ",") {
		words := strings.Fields(part)
		for i, word := range words {
			if i == 0 {
				continue
			}
			n, err := strconv.Atoi(words[i-1])
			if err != nil {
				continue
			}
			// Order matters: "xpassed"/"xfailed" contain "passed"/"failed".
			switch {
			case strings.Contains(word, "xpassed"):
				counts.xpassed = n
			case strings.Contains(word, "xfailed"):
				counts.xfailed = n
			case strings.Contains(word, "passed"):
				counts.passed = n
			case strings.Contains(word, "failed"):
				counts.failed = n
			case strings.Contains(word, "skipped"):
				counts.skipped = n
			}
		}
	}

	return counts
}
