// Package execwrap is gortk's set of generic command wrappers that run an
// arbitrary command and compress its output. It is a faithful port of rtk's
// src/cmds/rust/runner.rs plus the shell-exec dispatch for `run` and `proxy`
// from rtk's main.rs.
//
// Four subcommands are registered:
//
//   - err   — run a command, show only error/warning lines (and their blocks)
//   - test  — run a test command, show only failures + a summary
//   - proxy — execute a command, stream its output verbatim, track usage
//   - run   — raw shell exec (cmd /C on Windows), no filtering, no tracking
//
// The compression logic lives in the pure functions filterErrors and
// extractTestSummary, which are exercised directly by the ported Rust tests.
package execwrap

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

// Truncation caps for the test summary, mirroring rtk's
// MAX_RUNNER_FAILURES = CAP_WARNINGS and MAX_RUNNER_LINES = CAP_LIST.
const (
	maxRunnerFailures = core.CapWarnings
	maxRunnerLines    = core.CapList
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "err",
		Summary: "Run a command and show only errors/warnings",
		Run:     RunErr,
	})
	registry.Register(&registry.Cmd{
		Name:    "test",
		Summary: "Run tests and show only failures",
		Run:     RunTest,
	})
	registry.Register(&registry.Cmd{
		Name:    "proxy",
		Summary: "Execute a command without filtering but track usage",
		Run:     RunProxy,
	})
	registry.Register(&registry.Cmd{
		Name:    "run",
		Summary: "Execute a shell command (raw, no filtering or tracking)",
		Run:     RunRun,
	})
}

// errorPatterns is the set of regexes that flag a line as an error/warning.
// Ported 1:1 from rtk's ERROR_PATTERNS. Go's regexp has no inline /i besides
// (?i), which we prepend to each case-insensitive pattern just as Rust did.
var errorPatterns = []*regexp.Regexp{
	// Generic errors
	regexp.MustCompile(`(?i)^.*error[\s:\[].*$`),
	regexp.MustCompile(`(?i)^.*\berr\b.*$`),
	regexp.MustCompile(`(?i)^.*warning[\s:\[].*$`),
	regexp.MustCompile(`(?i)^.*\bwarn\b.*$`),
	regexp.MustCompile(`(?i)^.*failed.*$`),
	regexp.MustCompile(`(?i)^.*failure.*$`),
	regexp.MustCompile(`(?i)^.*exception.*$`),
	regexp.MustCompile(`(?i)^.*panic.*$`),
	// Rust specific
	regexp.MustCompile(`^error\[E\d+\]:.*$`),
	regexp.MustCompile(`^\s*--> .*:\d+:\d+$`),
	// Python
	regexp.MustCompile(`^Traceback.*$`),
	regexp.MustCompile(`^\s*File ".*", line \d+.*$`),
	// JavaScript/TypeScript
	regexp.MustCompile(`^\s*at .*:\d+:\d+.*$`),
	// Go
	regexp.MustCompile(`^.*\.go:\d+:.*$`),
}

// isErrorLine reports whether a line matches any error pattern.
func isErrorLine(line string) bool {
	for _, p := range errorPatterns {
		if p.MatchString(line) {
			return true
		}
	}
	return false
}

// splitLines splits raw output into lines with rtk's .lines() semantics: a
// trailing newline does not produce a trailing empty element.
func splitLines(raw string) []string {
	if raw == "" {
		return nil
	}
	lines := strings.Split(raw, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// filterErrors keeps only error/warning lines and their contiguous indented
// continuation blocks. Direct port of rtk's #[cfg(test)] fn filter_errors —
// which is the behavioural spec for the ErrorStreamFilter feed loop.
func filterErrors(output string) string {
	var result []string
	inErrorBlock := false
	blankCount := 0

	for _, line := range splitLines(output) {
		isErr := isErrorLine(line)

		switch {
		case isErr:
			inErrorBlock = true
			blankCount = 0
			result = append(result, line)
		case inErrorBlock:
			if strings.TrimSpace(line) == "" {
				blankCount++
				if blankCount >= 2 {
					inErrorBlock = false
				} else {
					result = append(result, line)
				}
			} else if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				result = append(result, line)
				blankCount = 0
			} else {
				inErrorBlock = false
			}
		}
	}

	return strings.Join(result, "\n")
}

// errFilterOutput produces the final `err` output for a given raw capture and
// exit code, folding ErrorStreamFilter's on_exit fallback into the result:
// when no error lines were emitted, report success or the failing tail.
func errFilterOutput(raw string, exitCode int) string {
	filtered := filterErrors(raw)
	if filtered != "" {
		return filtered
	}
	if exitCode == 0 {
		return "[ok] Command completed successfully (no errors)"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[FAIL] Command failed (exit code: %d)\n", exitCode)
	lines := splitLines(raw)
	start := 0
	if len(lines) > 10 {
		start = len(lines) - 10
	}
	for _, line := range lines[start:] {
		fmt.Fprintf(&b, "  %s\n", line)
	}
	return b.String()
}

// RunErr runs a command (joined into a shell command string) and shows only
// errors/warnings. Mirrors rtk's runner::run_err.
func RunErr(args []string, verbose int) (int, error) {
	command := strings.Join(args, " ")
	if strings.TrimSpace(command) == "" {
		return 0, nil
	}
	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: %s\n", command)
	}

	cmd := buildShellCommand(command)
	opts := core.RunOptions{TeeLabel: "err"}
	return core.RunFilteredWithExit(cmd, "err", command, func(raw string, exit int) string {
		return errFilterOutput(core.NormalizeNewlines(raw), exit)
	}, opts)
}

// RunTest runs a test command and shows only failures + a summary. Mirrors
// rtk's runner::run_test.
func RunTest(args []string, verbose int) (int, error) {
	command := strings.Join(args, " ")
	if strings.TrimSpace(command) == "" {
		return 0, nil
	}
	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running tests: %s\n", command)
	}

	cmd := buildShellCommand(command)
	opts := core.RunOptions{TeeLabel: "test"}
	return core.RunFiltered(cmd, "test", command, func(raw string) string {
		return extractTestSummary(core.NormalizeNewlines(raw), command)
	}, opts)
}

// extractTestSummary condenses test-runner output to failures + a summary,
// selecting per-runner heuristics by inspecting the command string. Direct
// port of rtk's extract_test_summary.
func extractTestSummary(output, command string) string {
	lines := splitLines(output)

	isCargo := strings.Contains(command, "cargo test")
	isPytest := strings.Contains(command, "pytest")
	isJest := strings.Contains(command, "jest") ||
		strings.Contains(command, "npm test") ||
		strings.Contains(command, "yarn test")
	isGo := strings.Contains(command, "go test")

	var result []string
	var failures []string
	var failureLines []string
	inFailure := false

	for _, line := range lines {
		if isCargo {
			if strings.Contains(line, "test result:") {
				result = append(result, line)
			}
			if strings.Contains(line, "FAILED") && !strings.Contains(line, "test result") {
				failures = append(failures, line)
			}
			if strings.HasPrefix(line, "failures:") {
				inFailure = true
			}
			if inFailure && strings.HasPrefix(line, "    ") {
				failureLines = append(failureLines, line)
			}
		}

		if isPytest {
			if strings.Contains(line, " passed") || strings.Contains(line, " failed") || strings.Contains(line, " error") {
				result = append(result, line)
			}
			if strings.Contains(line, "FAILED") {
				failures = append(failures, line)
			}
		}

		if isJest {
			if strings.Contains(line, "Tests:") || strings.Contains(line, "Test Suites:") {
				result = append(result, line)
			}
			if strings.Contains(line, "✕") || strings.Contains(line, "FAIL") {
				failures = append(failures, line)
			}
		}

		if isGo {
			if strings.HasPrefix(line, "ok") || strings.HasPrefix(line, "FAIL") || strings.HasPrefix(line, "---") {
				result = append(result, line)
			}
			if strings.Contains(line, "FAIL") {
				failures = append(failures, line)
			}
		}
	}

	var b strings.Builder

	if len(failures) > 0 {
		b.WriteString("[FAIL] FAILURES:\n")
		for _, f := range takeStr(failures, maxRunnerFailures) {
			fmt.Fprintf(&b, "  %s\n", f)
		}
		if len(failures) > maxRunnerFailures {
			fmt.Fprintf(&b, "  ... +%d more failures\n", len(failures)-maxRunnerFailures)
		}
		for _, f := range takeStr(failureLines, maxRunnerLines) {
			fmt.Fprintf(&b, "  %s\n", strings.TrimSpace(f))
		}
		if len(failureLines) > maxRunnerLines {
			fmt.Fprintf(&b, "  ... +%d more\n", len(failureLines)-maxRunnerLines)
		}
		b.WriteByte('\n')
	}

	if len(result) > 0 {
		b.WriteString("SUMMARY:\n")
		for _, r := range result {
			fmt.Fprintf(&b, "  %s\n", r)
		}
	} else {
		b.WriteString("OUTPUT (last 5 lines):\n")
		start := 0
		if len(lines) > 5 {
			start = len(lines) - 5
		}
		for _, line := range lines[start:] {
			if strings.TrimSpace(line) != "" {
				fmt.Fprintf(&b, "  %s\n", line)
			}
		}
	}

	return b.String()
}

// takeStr returns at most n elements from s (Rust's .iter().take(n)).
func takeStr(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// RunProxy executes a command without filtering but tracks usage. Mirrors
// rtk's Commands::Proxy. We delegate to core.RunPassthrough, which streams
// stdio verbatim and records the run — the streaming/tracking contract the
// Rust proxy implements by hand. The signal-handling / byte-cap machinery in
// the Rust version is an implementation detail of its manual streaming and is
// not reproduced.
func RunProxy(args []string, verbose int) (int, error) {
	if len(args) == 0 {
		return 1, fmt.Errorf("proxy requires a command to execute\nUsage: gortk proxy <command> [args...]")
	}

	// If a single quoted arg contains spaces, split it respecting quotes so
	// `gortk proxy 'head -50 file'` resolves cmd=head, args=["-50","file"].
	var name string
	var rest []string
	if len(args) == 1 {
		parts := shellSplit(args[0])
		if len(parts) > 1 {
			name = parts[0]
			rest = parts[1:]
		} else {
			name = args[0]
		}
	} else {
		name = args[0]
		rest = args[1:]
	}

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Proxy mode: %s %s\n", name, strings.Join(rest, " "))
	}
	return core.RunPassthrough(name, rest, verbose)
}

// RunRun executes a raw shell command (cmd /C on Windows, sh -c elsewhere)
// with no filtering and no tracking. Mirrors rtk's Commands::Run. Supports
// both `gortk run -c "<cmd>"` and `gortk run <cmd> [args...]`.
func RunRun(args []string, verbose int) (int, error) {
	var raw string
	if len(args) >= 2 && (args[0] == "-c" || args[0] == "--command") {
		raw = args[1]
	} else {
		raw = strings.Join(args, " ")
	}
	if strings.TrimSpace(raw) == "" {
		return 0, nil
	}
	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: %s\n", raw)
	}

	cmd := buildShellCommand(raw)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	return core.ExitCodeFromError(err), nil
}
