// Package rake is gortk's token-optimized Minitest output filter for
// `rake test` and `rails test`. It wraps the native ruby test runner, parses
// the standard Minitest output produced by both `rake test` and `rails test`,
// and emits only the failures/errors plus the summary line. Faithful port of
// rtk's src/cmds/ruby/rake_cmd.rs.
//
// Like rtk, this auto-detects `bundle exec` when a Gemfile is present in the
// working directory; otherwise it runs the bare tool. gortk resolves the tool
// PATHEXT-aware on Windows.
package rake

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

// maxRakeFailures caps how many individual failures we render, mirroring rtk's
// MAX_RAKE_FAILURES = CAP_WARNINGS.
const maxRakeFailures = core.CapWarnings

func init() {
	registry.Register(&registry.Cmd{
		Name:    "rake",
		Summary: "Rake/Rails test with compact Minitest output (Ruby)",
		Run:     Run,
	})
}

// Run executes the rake command: it selects rake vs rails, builds argv via the
// ruby_exec bundle-detection rule, runs it, and filters the Minitest output.
func Run(args []string, verbose int) (int, error) {
	tool, effectiveArgs := selectRunner(args)
	cmd := rubyExec(tool, effectiveArgs)

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: %s %s\n", cmd.Args[0], strings.Join(effectiveArgs, " "))
	}

	opts := core.RunOptions{TeeLabel: "rake"}
	return core.RunFiltered(cmd, "rake", strings.Join(args, " "), filterMinitestOutput, opts)
}

// rubyExec builds the *exec.Cmd for a ruby tool, auto-detecting `bundle exec`
// when a Gemfile exists in the working directory. Mirrors rtk's ruby_exec:
// transitive deps like rake won't appear in the Gemfile but still need bundler
// for version isolation.
func rubyExec(tool string, args []string) *exec.Cmd {
	if _, err := os.Stat("Gemfile"); err == nil {
		bundleArgs := append([]string{"exec", tool}, args...)
		return core.ResolvedCommand("bundle", bundleArgs...)
	}
	return core.ResolvedCommand(tool, args...)
}

// selectRunner decides whether to use `rake test` or `rails test` based on args.
//
// `rake test` only supports a single file via `TEST=path` and ignores positional
// file args. When any positional test file paths are detected, we switch to
// `rails test` which handles single files, multiple files, and line-number
// syntax (`file.rb:15`) natively.
func selectRunner(args []string) (string, []string) {
	hasTestSubcommand := len(args) > 0 && args[0] == "test"
	if !hasTestSubcommand {
		return "rake", append([]string(nil), args...)
	}

	var positionalFiles []string
	for _, a := range args[1:] {
		if strings.Contains(a, "=") || strings.HasPrefix(a, "-") {
			continue
		}
		if looksLikeTestPath(a) {
			positionalFiles = append(positionalFiles, a)
		}
	}

	if len(positionalFiles) > 0 {
		return "rails", append([]string(nil), args...)
	}
	return "rake", append([]string(nil), args...)
}

func looksLikeTestPath(arg string) bool {
	path := arg
	if i := strings.IndexByte(arg, ':'); i >= 0 {
		path = arg[:i]
	}
	return strings.HasSuffix(path, ".rb") ||
		strings.HasPrefix(path, "test/") ||
		strings.HasPrefix(path, "spec/") ||
		strings.Contains(path, "_test.rb") ||
		strings.Contains(path, "_spec.rb")
}

type parseState int

const (
	stateHeader parseState = iota
	stateRunning
	stateFailures
	stateSummary
)

var reFailure = regexp.MustCompile(`^\d+\)\s+(Failure|Error):$`)

func isFailureHeader(line string) bool {
	return reFailure.MatchString(line)
}

// splitLines mirrors Rust's str::lines(): split on '\n' and drop a single
// trailing empty element produced by a final newline.
func splitLines(s string) []string {
	s = core.NormalizeNewlines(s)
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// filterMinitestOutput parses Minitest output using a state machine, keeping
// only failures/errors and the summary line.
func filterMinitestOutput(output string) string {
	clean := core.StripANSI(output)
	state := stateHeader
	var failures []string
	var currentFailure []string
	summaryLine := ""

	for _, line := range splitLines(clean) {
		trimmed := strings.TrimSpace(line)

		// Detect summary line anywhere (it's always the last meaningful line).
		// Handles both "N runs, ..." and "N tests, ..." forms.
		if (strings.Contains(trimmed, " runs,") || strings.Contains(trimmed, " tests,")) &&
			strings.Contains(trimmed, " assertions,") {
			summaryLine = trimmed
			continue
		}

		// State transitions — standard Minitest and minitest-reporters.
		if trimmed == "# Running:" || strings.HasPrefix(trimmed, "Started with run options") {
			state = stateRunning
			continue
		}

		if strings.HasPrefix(trimmed, "Finished in ") {
			state = stateFailures
			continue
		}

		switch state {
		case stateHeader, stateRunning:
			// Skip seed line, blank lines, progress dots.
			continue
		case stateFailures:
			if isFailureHeader(trimmed) {
				if len(currentFailure) > 0 {
					failures = append(failures, strings.Join(currentFailure, "\n"))
					currentFailure = currentFailure[:0]
				}
				currentFailure = append(currentFailure, trimmed)
			} else if trimmed == "" && len(currentFailure) > 0 {
				failures = append(failures, strings.Join(currentFailure, "\n"))
				currentFailure = currentFailure[:0]
			} else if trimmed != "" {
				currentFailure = append(currentFailure, line)
			}
		case stateSummary:
		}
	}

	if len(currentFailure) > 0 {
		failures = append(failures, strings.Join(currentFailure, "\n"))
	}

	return buildMinitestSummary(summaryLine, failures)
}

func buildMinitestSummary(summary string, failures []string) string {
	runs, _, failCount, errorCount, skips := parseMinitestSummary(summary)

	if runs == 0 && summary == "" {
		return "rake test: no tests ran"
	}

	if failCount == 0 && errorCount == 0 {
		msg := fmt.Sprintf("ok rake test: %d runs, 0 failures", runs)
		if skips > 0 {
			msg += fmt.Sprintf(", %d skips", skips)
		}
		return msg
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("rake test: %d runs, %d failures, %d errors", runs, failCount, errorCount))
	if skips > 0 {
		b.WriteString(fmt.Sprintf(", %d skips", skips))
	}
	b.WriteByte('\n')

	if len(failures) == 0 {
		return strings.TrimSpace(b.String())
	}

	b.WriteByte('\n')

	limit := len(failures)
	if limit > maxRakeFailures {
		limit = maxRakeFailures
	}
	for i := 0; i < limit; i++ {
		lines := splitLines(failures[i])
		// First line is like "1) Failure:" or "1) Error:".
		if len(lines) > 0 {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, strings.TrimSpace(lines[0])))
		}
		// Remaining lines: test name, file:line, assertion message.
		shown := 0
		for j := 1; j < len(lines) && shown < 4; j++ {
			trimmed := strings.TrimSpace(lines[j])
			if trimmed != "" {
				b.WriteString(fmt.Sprintf("   %s\n", truncate(trimmed, 120)))
			}
			shown++
		}
		if i < limit-1 {
			b.WriteByte('\n')
		}
	}

	if len(failures) > maxRakeFailures {
		b.WriteString(fmt.Sprintf("\n... +%d more failures\n", len(failures)-maxRakeFailures))
	}

	return strings.TrimSpace(b.String())
}

func parseMinitestSummary(summary string) (runs, assertions, failures, errors, skips int) {
	for _, part := range strings.Split(summary, ",") {
		words := strings.Fields(strings.TrimSpace(part))
		if len(words) >= 2 {
			n, err := parseInt(words[0])
			if err != nil {
				continue
			}
			switch strings.TrimRight(words[1], ",") {
			case "runs", "run", "tests", "test":
				runs = n
			case "assertions", "assertion":
				assertions = n
			case "failures", "failure":
				failures = n
			case "errors", "error":
				errors = n
			case "skips", "skip":
				skips = n
			}
		}
	}
	return
}

func parseInt(s string) (int, error) {
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

// truncate mirrors rtk's utils::truncate: returns s unchanged when within
// max_len, "..." when max_len < 3, otherwise the first (max_len-3) runes plus
// "...". Operates on runes so multibyte content truncates cleanly.
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
