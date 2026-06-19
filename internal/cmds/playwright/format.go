package playwright

import (
	"fmt"
	"strings"
)

// formatMode mirrors parser::formatter::FormatMode.
type formatMode int

const (
	modeCompact formatMode = iota
	modeVerbose
	modeUltra
)

// formatModeFromVerbosity maps the -v count to a render mode, like rtk's
// FormatMode::from_verbosity.
func formatModeFromVerbosity(verbosity int) formatMode {
	switch {
	case verbosity <= 0:
		return modeCompact
	case verbosity == 1:
		return modeVerbose
	default:
		return modeUltra
	}
}

// formatTestResult renders a TestResult according to mode. Faithful port of the
// TokenFormatter impl for TestResult.
func formatTestResult(r testResult, mode formatMode) string {
	switch mode {
	case modeVerbose:
		return formatVerbose(r)
	case modeUltra:
		return formatUltra(r)
	default:
		return formatCompact(r)
	}
}

func formatCompact(r testResult) string {
	// Always surface skipped/pending tests — hiding them lets coverage gaps
	// (test.skip / it.skip / xfail) accumulate invisibly.
	summary := fmt.Sprintf("PASS (%d) FAIL (%d)", r.Passed, r.Failed)
	if r.Skipped > 0 {
		summary += fmt.Sprintf(" skipped (%d)", r.Skipped)
	}
	lines := []string{summary}

	if len(r.Failures) > 0 {
		lines = append(lines, "")
		limit := len(r.Failures)
		if limit > 5 {
			limit = 5
		}
		for idx, failure := range r.Failures[:limit] {
			lines = append(lines, fmt.Sprintf("%d. %s", idx+1, failure.TestName))
			for _, line := range splitLines(failure.ErrorMessage) {
				lines = append(lines, fmt.Sprintf("   %s", line))
			}
		}
		if len(r.Failures) > 5 {
			lines = append(lines, fmt.Sprintf("\n... +%d more failures", len(r.Failures)-5))
		}
	}

	if r.DurationMS != nil {
		lines = append(lines, fmt.Sprintf("\nTime: %dms", *r.DurationMS))
	}

	return strings.Join(lines, "\n")
}

func formatVerbose(r testResult) string {
	lines := []string{fmt.Sprintf(
		"Tests: %d passed, %d failed, %d skipped (total: %d)",
		r.Passed, r.Failed, r.Skipped, r.Total,
	)}

	if len(r.Failures) > 0 {
		lines = append(lines, "\nFailures:")
		for idx, failure := range r.Failures {
			lines = append(lines, fmt.Sprintf("\n%d. %s (%s)", idx+1, failure.TestName, failure.FilePath))
			lines = append(lines, fmt.Sprintf("   %s", failure.ErrorMessage))
			if failure.StackTrace != nil {
				stackLines := splitLines(*failure.StackTrace)
				if len(stackLines) > 3 {
					stackLines = stackLines[:3]
				}
				lines = append(lines, fmt.Sprintf("   %s", strings.Join(stackLines, "\n   ")))
			}
		}
	}

	if r.DurationMS != nil {
		lines = append(lines, fmt.Sprintf("\nDuration: %dms", *r.DurationMS))
	}

	return strings.Join(lines, "\n")
}

func formatUltra(r testResult) string {
	var dur uint64
	if r.DurationMS != nil {
		dur = *r.DurationMS
	}
	return fmt.Sprintf("[ok]%d [x]%d [skip]%d (%dms)", r.Passed, r.Failed, r.Skipped, dur)
}

// splitLines mirrors Rust's str::lines(): splits on \n and drops a single
// trailing empty element, so a string ending in "\n" yields no trailing "".
func splitLines(s string) []string {
	parts := strings.Split(s, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}
