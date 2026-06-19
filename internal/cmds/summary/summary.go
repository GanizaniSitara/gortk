// Package summary runs a shell command and prints a heuristic summary of its
// output. It detects the kind of output (test results, build output, logs, a
// flat list, JSON, or generic text) and condenses it accordingly. Faithful port
// of rtk's src/cmds/system/summary.rs (Commands::Summary).
//
// Like rtk, this is the one command whose explicit job is to run an arbitrary
// command line, so on Windows it wraps `cmd /C <command>`. It still makes no
// network calls of its own.
package summary

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

// Caps mirror rtk's MAX_SUMMARY_LIST / MAX_SUMMARY_KEYS = CAP_WARNINGS.
const (
	maxSummaryList = core.CapWarnings
	maxSummaryKeys = core.CapWarnings
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "summary",
		Summary: "Run a command and print a heuristic summary of its output",
		Run:     Run,
	})
}

// Run joins the args into a command line, runs it, and prints a heuristic
// summary. Returns the wrapped command's exit code.
func Run(args []string, verbose int) (int, error) {
	command := strings.Join(args, " ")
	if strings.TrimSpace(command) == "" {
		fmt.Fprintln(os.Stderr, "gortk: summary requires a command to run")
		return 2, nil
	}

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running and summarizing: %s\n", command)
	}

	cmd := core.ResolvedCommand("cmd", "/C", command)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	exitCode := core.ExitCodeFromError(runErr)
	if exitCode == 127 {
		return 127, fmt.Errorf("gortk: failed to execute command: %w", runErr)
	}
	success := exitCode == 0

	stdout := core.NormalizeNewlines(outBuf.String())
	stderr := core.NormalizeNewlines(errBuf.String())
	raw := stdout + "\n" + stderr

	out := summarizeOutput(raw, command, success)
	fmt.Println(out)
	return exitCode, nil
}

// extractNumberRE caches the per-keyword regexes (e.g. `(\d+)\s*passed`).
var extractNumberCache = map[string]*regexp.Regexp{}

func extractNumber(text, after string) (int, bool) {
	re, ok := extractNumberCache[after]
	if !ok {
		re = regexp.MustCompile(`(\d+)\s*` + regexp.QuoteMeta(after))
		extractNumberCache[after] = re
	}
	m := re.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	n := 0
	for _, c := range m[1] {
		n = n*10 + int(c-'0')
	}
	return n, true
}

// truncate shortens s to max_len runes, appending "..." when it overflows.
// Mirrors rtk's utils::truncate (rune-aware).
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

// lines mirrors Rust's str::lines(): split on "\n", dropping a single trailing
// empty element from a final newline.
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

func summarizeOutput(output, command string, success bool) string {
	ls := lines(output)
	var result []string

	statusIcon := "[ok]"
	if !success {
		statusIcon = "[FAIL]"
	}
	result = append(result, fmt.Sprintf("%s Command: %s", statusIcon, truncate(command, 60)))
	result = append(result, fmt.Sprintf("   %d lines of output", len(ls)))
	result = append(result, "")

	switch detectOutputType(output, command) {
	case outputTestResults:
		result = summarizeTests(output, result)
	case outputBuildOutput:
		result = summarizeBuild(output, result)
	case outputLogOutput:
		result = summarizeLogsQuick(output, result)
	case outputListOutput:
		result = summarizeList(output, result)
	case outputJSONOutput:
		result = summarizeJSON(output, result)
	default:
		result = summarizeGeneric(output, result)
	}

	return strings.Join(result, "\n")
}

type outputType int

const (
	outputGeneric outputType = iota
	outputTestResults
	outputBuildOutput
	outputLogOutput
	outputListOutput
	outputJSONOutput
)

func detectOutputType(output, command string) outputType {
	cmdLower := strings.ToLower(command)
	outLower := strings.ToLower(output)

	// NOTE: mirrors Rust precedence exactly. In Rust, `&&` binds tighter than
	// `||`, so the test is: contains("test") OR (contains("passed") AND
	// contains("failed")).
	switch {
	case strings.Contains(cmdLower, "test") ||
		(strings.Contains(outLower, "passed") && strings.Contains(outLower, "failed")):
		return outputTestResults
	case strings.Contains(cmdLower, "build") ||
		strings.Contains(cmdLower, "compile") ||
		strings.Contains(outLower, "compiling"):
		return outputBuildOutput
	case strings.Contains(outLower, "error:") ||
		strings.Contains(outLower, "warn:") ||
		strings.Contains(outLower, "[info]"):
		return outputLogOutput
	case strings.HasPrefix(strings.TrimLeft(output, " \t\r\n"), "{") ||
		strings.HasPrefix(strings.TrimLeft(output, " \t\r\n"), "["):
		return outputJSONOutput
	case allLinesListLike(output):
		return outputListOutput
	default:
		return outputGeneric
	}
}

// allLinesListLike mirrors the Rust predicate: every line is < 200 chars and,
// if it has no tab, has fewer than 10 whitespace-separated words (a tab makes
// the line non-list-like).
func allLinesListLike(output string) bool {
	for _, l := range lines(output) {
		if len([]rune(l)) >= 200 {
			return false
		}
		if strings.Contains(l, "\t") {
			return false
		}
		if len(strings.Fields(l)) >= 10 {
			return false
		}
	}
	return true
}

func summarizeTests(output string, result []string) []string {
	result = append(result, "Test Results:")

	passed, failed, skipped := 0, 0, 0
	var failures []string

	for _, line := range lines(output) {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "passed") || strings.Contains(lower, "✓") || strings.Contains(lower, "ok") {
			if n, ok := extractNumber(lower, "passed"); ok {
				passed = n
			} else {
				passed++
			}
		}
		if strings.Contains(lower, "failed") || strings.Contains(lower, "[x]") || strings.Contains(lower, "fail") {
			if n, ok := extractNumber(lower, "failed"); ok {
				failed = n
			}
			if !strings.Contains(line, "0 failed") {
				failures = append(failures, line)
			}
		}
		if strings.Contains(lower, "skipped") || strings.Contains(lower, "ignored") {
			if n, ok := extractNumber(lower, "skipped"); ok {
				skipped = n
			} else if n, ok := extractNumber(lower, "ignored"); ok {
				skipped = n
			}
		}
	}

	result = append(result, fmt.Sprintf("   [ok] %d passed", passed))
	if failed > 0 {
		result = append(result, fmt.Sprintf("   [FAIL] %d failed", failed))
	}
	if skipped > 0 {
		result = append(result, fmt.Sprintf("   skip %d skipped", skipped))
	}

	if len(failures) > 0 {
		result = append(result, "")
		result = append(result, "   Failures:")
		for i, f := range failures {
			if i >= 5 {
				break
			}
			result = append(result, fmt.Sprintf("   • %s", truncate(f, 70)))
		}
	}
	return result
}

func summarizeBuild(output string, result []string) []string {
	result = append(result, "Build Summary:")

	errors, warnings, compiled := 0, 0, 0
	var errorMsgs []string

	for _, line := range lines(output) {
		lower := strings.ToLower(line)
		if strings.Contains(lower, "error") && !strings.Contains(lower, "0 error") {
			errors++
			if len(errorMsgs) < 5 {
				errorMsgs = append(errorMsgs, line)
			}
		}
		if strings.Contains(lower, "warning") && !strings.Contains(lower, "0 warning") {
			warnings++
		}
		if strings.Contains(lower, "compiling") || strings.Contains(lower, "compiled") {
			compiled++
		}
	}

	if compiled > 0 {
		result = append(result, fmt.Sprintf("   %d crates/files compiled", compiled))
	}
	if errors > 0 {
		result = append(result, fmt.Sprintf("   [error] %d errors", errors))
	}
	if warnings > 0 {
		result = append(result, fmt.Sprintf("   [warn] %d warnings", warnings))
	}
	if errors == 0 && warnings == 0 {
		result = append(result, "   [ok] Build successful")
	}

	if len(errorMsgs) > 0 {
		result = append(result, "")
		result = append(result, "   Errors:")
		for _, e := range errorMsgs {
			result = append(result, fmt.Sprintf("   • %s", truncate(e, 70)))
		}
	}
	return result
}

func summarizeLogsQuick(output string, result []string) []string {
	result = append(result, "Log Summary:")

	errors, warnings, info := 0, 0, 0
	for _, line := range lines(output) {
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "error") || strings.Contains(lower, "fatal"):
			errors++
		case strings.Contains(lower, "warn"):
			warnings++
		case strings.Contains(lower, "info"):
			info++
		}
	}

	result = append(result, fmt.Sprintf("   [error] %d errors", errors))
	result = append(result, fmt.Sprintf("   [warn] %d warnings", warnings))
	result = append(result, fmt.Sprintf("   [info] %d info", info))
	return result
}

func summarizeList(output string, result []string) []string {
	var nonEmpty []string
	for _, l := range lines(output) {
		if strings.TrimSpace(l) != "" {
			nonEmpty = append(nonEmpty, l)
		}
	}
	result = append(result, fmt.Sprintf("List (%d items):", len(nonEmpty)))

	limit := len(nonEmpty)
	if limit > maxSummaryList {
		limit = maxSummaryList
	}
	for _, line := range nonEmpty[:limit] {
		result = append(result, fmt.Sprintf("   • %s", truncate(line, 70)))
	}
	if len(nonEmpty) > maxSummaryList {
		result = append(result, fmt.Sprintf("   ... +%d more", len(nonEmpty)-maxSummaryList))
	}
	return result
}

func summarizeJSON(output string, result []string) []string {
	result = append(result, "JSON Output:")

	kind, count, keys, scalar, ok := parseJSONShape(output)
	if !ok {
		result = append(result, "   (Invalid JSON)")
		return result
	}
	switch kind {
	case jsonArray:
		result = append(result, fmt.Sprintf("   Array with %d items", count))
	case jsonObject:
		result = append(result, fmt.Sprintf("   Object with %d keys:", count))
		limit := len(keys)
		if limit > maxSummaryKeys {
			limit = maxSummaryKeys
		}
		for _, k := range keys[:limit] {
			result = append(result, fmt.Sprintf("   • %s", k))
		}
		if count > maxSummaryKeys {
			result = append(result, fmt.Sprintf("   ... +%d more keys", count-maxSummaryKeys))
		}
	default:
		result = append(result, fmt.Sprintf("   %s", truncate(scalar, 100)))
	}
	return result
}

func summarizeGeneric(output string, result []string) []string {
	ls := lines(output)
	result = append(result, "Output:")

	// First few lines.
	for i, line := range ls {
		if i >= 5 {
			break
		}
		if strings.TrimSpace(line) != "" {
			result = append(result, fmt.Sprintf("   %s", truncate(line, 75)))
		}
	}

	if len(ls) > 10 {
		result = append(result, "   ...")
		// Last few lines.
		for _, line := range ls[len(ls)-3:] {
			if strings.TrimSpace(line) != "" {
				result = append(result, fmt.Sprintf("   %s", truncate(line, 75)))
			}
		}
	}
	return result
}
