// Package vitest is gortk's token-optimized JS test-runner wrapper. It runs
// vitest (or jest) in JSON-reporter mode, parses the structured output, and
// emits a compact pass/fail summary with only the failing tests. Faithful port
// of rtk's src/cmds/js/vitest_cmd.rs (the Vitest + Jest dispatch).
//
// Like rtk, this wraps the platform test runner. The tool is resolved
// PATHEXT-aware (vitest.cmd / jest.cmd on Windows); when the bare tool is not
// on PATH it falls back to the detected package manager's exec mechanism
// (pnpm exec / yarn exec / npx).
package vitest

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "vitest",
		Summary: "Run vitest with token-optimized failures-only output",
		Run:     runVitest,
	})
	registry.Register(&registry.Cmd{
		Name:    "jest",
		Summary: "Run jest with token-optimized failures-only output",
		Run:     runJest,
	})
}

func runVitest(args []string, verbose int) (int, error) { return runTest("vitest", args, verbose) }
func runJest(args []string, verbose int) (int, error)   { return runTest("jest", args, verbose) }

// runTest is the shared driver for vitest and jest. It mirrors rtk's
// vitest_cmd::run_test: build the framework command, force non-watch + JSON
// output, append user args (dropping flags rtk owns), run, parse, format.
func runTest(framework string, args []string, verbose int) (int, error) {
	cmd := packageManagerExec(framework)
	switch framework {
	case "vitest":
		// Force non-watch mode + JSON structured output.
		cmd.Args = append(cmd.Args, "run", "--reporter=json")
	case "jest":
		cmd.Args = append(cmd.Args, "--no-watch", "--json")
	}

	for _, arg := range args {
		if arg == "run" ||
			strings.HasPrefix(arg, "--json") ||
			strings.HasPrefix(arg, "--reporter") ||
			strings.HasPrefix(arg, "--watch") {
			continue
		}
		cmd.Args = append(cmd.Args, arg)
	}

	mode := formatModeFromVerbosity(verbose)

	opts := core.RunOptions{
		TeeLabel:         framework + "_run",
		FilterStdoutOnly: true,
	}

	exit, err := core.RunFiltered(cmd, framework, "run", func(raw string) string {
		result := parseVitest(raw)
		switch result.tier {
		case tierFull:
			if verbose > 0 {
				fmt.Fprintf(os.Stderr, "%s run (Tier 1: Full JSON parse)\n", framework)
			}
			return formatTestResult(result.data, mode)
		case tierDegraded:
			if verbose > 0 {
				emitDegradationWarning(framework, strings.Join(result.warnings, ", "))
			}
			return formatTestResult(result.data, mode)
		default:
			emitPassthroughWarning(framework, "All parsing tiers failed")
			return result.passthrough
		}
	}, opts)
	if err != nil {
		return exit, err
	}

	// rtk reports exit 0 on success regardless of the runner's own code, and
	// the runner's real exit code on failure. RunFiltered already returns the
	// child's exit code, so a success there is already 0.
	if exit != 0 {
		return exit, nil
	}
	return 0, nil
}

// packageManagerExec builds the command to invoke a JS dev tool, mirroring
// rtk's utils::package_manager_exec. If the tool is directly on PATH it is run
// straight; otherwise it is launched through the detected package manager.
func packageManagerExec(tool string) *exec.Cmd {
	if core.ToolExists(tool) {
		return core.ResolvedCommand(tool)
	}
	switch detectPackageManager() {
	case "pnpm":
		return core.ResolvedCommand("pnpm", "exec", "--", tool)
	case "yarn":
		return core.ResolvedCommand("yarn", "exec", "--", tool)
	default:
		return core.ResolvedCommand("npx", "--no-install", "--", tool)
	}
}

// detectPackageManager mirrors rtk's utils::detect_package_manager: lockfile
// sniffing in the current working directory.
func detectPackageManager() string {
	if fileExists("pnpm-lock.yaml") {
		return "pnpm"
	}
	if fileExists("yarn.lock") {
		return "yarn"
	}
	return "npm"
}

func fileExists(name string) bool {
	_, err := os.Stat(filepath.Clean(name))
	return err == nil
}

// emitDegradationWarning / emitPassthroughWarning mirror the rtk parser helpers,
// keeping the same [GORTK:*] marker shape on stderr.
func emitDegradationWarning(tool, reason string) {
	fmt.Fprintf(os.Stderr, "[GORTK:DEGRADED] %s parser: %s\n", tool, reason)
}

func emitPassthroughWarning(tool, reason string) {
	fmt.Fprintf(os.Stderr, "[GORTK:PASSTHROUGH] %s parser: %s\n", tool, reason)
}

// passthroughMaxChars matches rtk's default limits().passthrough_max_chars.
const passthroughMaxChars = 2000

// truncatePassthrough truncates raw output to passthroughMaxChars, appending a
// passthrough marker. Operates on runes so multibyte text never splits
// mid-character (mirrors rtk's char-based truncate_output).
func truncatePassthrough(output string) string {
	return truncateOutput(output, passthroughMaxChars)
}

func truncateOutput(output string, maxChars int) string {
	runes := []rune(output)
	if len(runes) <= maxChars {
		return output
	}
	truncated := string(runes[:maxChars])
	return fmt.Sprintf("%s\n\n[GORTK:PASSTHROUGH] Output truncated (%d chars → %d chars)",
		truncated, len(runes), maxChars)
}

// --- JSON output structures (vitest/jest reporter format) -------------------

type vitestJSONOutput struct {
	TestResults    []vitestTestFile `json:"testResults"`
	NumTotalTests  int              `json:"numTotalTests"`
	NumPassedTests int              `json:"numPassedTests"`
	NumFailedTests int              `json:"numFailedTests"`
	NumPendingTests int             `json:"numPendingTests"`
}

type vitestTestFile struct {
	Name              string       `json:"name"`
	AssertionResults  []vitestTest `json:"assertionResults"`
}

type vitestTest struct {
	FullName        string   `json:"fullName"`
	Status          string   `json:"status"`
	FailureMessages []string `json:"failureMessages"`
}

// --- Canonical result types (mirror rtk parser::types) ----------------------

type testFailure struct {
	testName     string
	filePath     string
	errorMessage string
	stackTrace   string // empty => None
}

type testResult struct {
	total      int
	passed     int
	failed     int
	skipped    int
	durationMS int  // valid only when hasDuration
	hasDuration bool
	failures   []testFailure
}

// --- Three-tier parse result -----------------------------------------------

type parseTier int

const (
	tierFull parseTier = iota + 1
	tierDegraded
	tierPassthrough
)

type parseResult struct {
	tier        parseTier
	data        testResult
	warnings    []string
	passthrough string
}

// parseVitest mirrors rtk's VitestParser::parse three-tier fallback:
//
//	Tier 1 (Full)        full JSON parse, with prefix-stripping fallback
//	Tier 2 (Degraded)    regex stat extraction (user overrode --reporter)
//	Tier 3 (Passthrough) truncated raw output
func parseVitest(input string) parseResult {
	if data, ok := parseVitestJSON(input); ok {
		return parseResult{tier: tierFull, data: data}
	}

	if data, ok := extractStatsRegex(input); ok {
		return parseResult{
			tier:     tierDegraded,
			data:     data,
			warnings: []string{"JSON parse failed"},
		}
	}

	return parseResult{tier: tierPassthrough, passthrough: truncatePassthrough(input)}
}

// parseVitestJSON attempts a strict JSON decode, then retries against a brace-
// balanced object extracted from prefixed output (pnpm banner, dotenv, etc.).
func parseVitestJSON(input string) (testResult, bool) {
	var out vitestJSONOutput
	if err := json.Unmarshal([]byte(input), &out); err == nil {
		return testResultFromJSON(out), true
	}
	if extracted, ok := extractJSONObject(input); ok {
		var out2 vitestJSONOutput
		if err := json.Unmarshal([]byte(extracted), &out2); err == nil {
			return testResultFromJSON(out2), true
		}
	}
	return testResult{}, false
}

func testResultFromJSON(json vitestJSONOutput) testResult {
	return testResult{
		total:    json.NumTotalTests,
		passed:   json.NumPassedTests,
		failed:   json.NumFailedTests,
		skipped:  json.NumPendingTests,
		failures: extractFailuresFromJSON(json),
	}
}

func extractFailuresFromJSON(json vitestJSONOutput) []testFailure {
	var failures []testFailure
	for _, file := range json.TestResults {
		for _, test := range file.AssertionResults {
			if test.Status == "failed" {
				failures = append(failures, testFailure{
					testName:     test.FullName,
					filePath:     file.Name,
					errorMessage: strings.Join(test.FailureMessages, "\n"),
				})
			}
		}
	}
	return failures
}

// --- Tier 2: regex stat extraction -----------------------------------------

var (
	testsRE    = regexp.MustCompile(`Tests\s+(?:(\d+)\s+failed\s+\|\s+)?(\d+)\s+passed`)
	durationRE = regexp.MustCompile(`Duration\s+([\d.]+)(ms|s)`)
)

func extractStatsRegex(output string) (testResult, bool) {
	clean := core.StripANSI(output)

	passed, failed, total := 0, 0, 0
	if caps := testsRE.FindStringSubmatch(clean); caps != nil {
		if caps[1] != "" {
			failed = atoiOr0(caps[1])
		}
		if caps[2] != "" {
			passed = atoiOr0(caps[2])
		}
		total = passed + failed
	}

	durationMS, hasDuration := 0, false
	if caps := durationRE.FindStringSubmatch(clean); caps != nil {
		if value, err := strconv.ParseFloat(caps[1], 64); err == nil {
			if caps[2] == "ms" {
				durationMS = int(value)
			} else {
				durationMS = int(value * 1000.0)
			}
			hasDuration = true
		}
	}

	if total > 0 {
		return testResult{
			total:       total,
			passed:      passed,
			failed:      failed,
			skipped:     0,
			durationMS:  durationMS,
			hasDuration: hasDuration,
			failures:    extractFailuresRegex(clean),
		}, true
	}
	return testResult{}, false
}

func extractFailuresRegex(output string) []testFailure {
	var failures []testFailure
	lines := splitLines(output)
	i := 0
	for i < len(lines) {
		line := lines[i]
		if strings.Contains(line, "[x]") || strings.Contains(line, "FAIL") {
			errorLines := []string{line}
			i++
			// Collect subsequent indented lines.
			for i < len(lines) && strings.HasPrefix(lines[i], "  ") {
				errorLines = append(errorLines, strings.TrimSpace(lines[i]))
				i++
			}
			// errorLines always has at least the header line here.
			failures = append(failures, testFailure{
				testName:     errorLines[0],
				errorMessage: strings.Join(errorLines[1:], "\n"),
			})
		} else {
			i++
		}
	}
	return failures
}

func atoiOr0(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// splitLines mirrors Rust's str::lines(): split on '\n' and drop a single
// trailing empty element so a trailing newline does not yield a phantom line.
func splitLines(s string) []string {
	parts := strings.Split(s, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}

// --- JSON object extraction (prefix stripping) ------------------------------

// extractJSONObject pulls a complete JSON object out of input that may carry a
// non-JSON prefix (pnpm banner, dotenv lines). Mirrors rtk's
// parser::extract_json_object: prefer the "numTotalTests" marker, else the
// first line starting with '{', then brace-balance forward respecting strings
// and escapes. Indices are over runes to stay UTF-8 safe.
func extractJSONObject(input string) (string, bool) {
	runes := []rune(input)

	startPos := -1
	if idx := strings.Index(input, `"numTotalTests"`); idx >= 0 {
		// idx is a byte offset; convert by walking back over the prefix runes.
		marker := len([]rune(input[:idx]))
		startPos = lastIndexRune(runes, marker, '{')
		if startPos < 0 {
			startPos = 0
		}
	} else {
		// Fallback: first line whose trimmed form starts with '{'.
		offset := 0
		for _, line := range strings.Split(input, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "{") {
				startPos = offset
				break
			}
			offset += len([]rune(line)) + 1 // +1 for the '\n'
		}
		if startPos < 0 {
			return "", false
		}
	}

	depth := 0
	inString := false
	escapeNext := false
	for i := startPos; i < len(runes); i++ {
		ch := runes[i]
		if escapeNext {
			escapeNext = false
			continue
		}
		switch {
		case ch == '\\' && inString:
			escapeNext = true
		case ch == '"':
			inString = !inString
		case ch == '{' && !inString:
			depth++
		case ch == '}' && !inString:
			depth--
			if depth == 0 {
				return string(runes[startPos : i+1]), true
			}
		}
	}
	return "", false
}

// lastIndexRune returns the index of the last r at or before before, or -1.
func lastIndexRune(runes []rune, before int, r rune) int {
	if before > len(runes) {
		before = len(runes)
	}
	for i := before; i >= 0; i-- {
		if i < len(runes) && runes[i] == r {
			return i
		}
	}
	return -1
}

// --- Formatting (mirror rtk parser::formatter TestResult impl) --------------

type formatMode int

const (
	formatCompact formatMode = iota
	formatVerbose
	formatUltra
)

func formatModeFromVerbosity(verbosity int) formatMode {
	switch {
	case verbosity == 0:
		return formatCompact
	case verbosity == 1:
		return formatVerbose
	default:
		return formatUltra
	}
}

func formatTestResult(r testResult, mode formatMode) string {
	switch mode {
	case formatVerbose:
		return formatTestVerbose(r)
	case formatUltra:
		return formatTestUltra(r)
	default:
		return formatTestCompact(r)
	}
}

func formatTestCompact(r testResult) string {
	// Always surface skipped/pending tests so coverage gaps don't hide.
	summary := fmt.Sprintf("PASS (%d) FAIL (%d)", r.passed, r.failed)
	if r.skipped > 0 {
		summary += fmt.Sprintf(" skipped (%d)", r.skipped)
	}
	lines := []string{summary}

	if len(r.failures) > 0 {
		lines = append(lines, "")
		limit := len(r.failures)
		if limit > 5 {
			limit = 5
		}
		for idx := 0; idx < limit; idx++ {
			failure := r.failures[idx]
			lines = append(lines, fmt.Sprintf("%d. %s", idx+1, failure.testName))
			for _, line := range splitLines(failure.errorMessage) {
				lines = append(lines, fmt.Sprintf("   %s", line))
			}
		}
		if len(r.failures) > 5 {
			lines = append(lines, fmt.Sprintf("\n... +%d more failures", len(r.failures)-5))
		}
	}

	if r.hasDuration {
		lines = append(lines, fmt.Sprintf("\nTime: %dms", r.durationMS))
	}

	return strings.Join(lines, "\n")
}

func formatTestVerbose(r testResult) string {
	lines := []string{fmt.Sprintf(
		"Tests: %d passed, %d failed, %d skipped (total: %d)",
		r.passed, r.failed, r.skipped, r.total)}

	if len(r.failures) > 0 {
		lines = append(lines, "\nFailures:")
		for idx, failure := range r.failures {
			lines = append(lines, fmt.Sprintf("\n%d. %s (%s)", idx+1, failure.testName, failure.filePath))
			lines = append(lines, fmt.Sprintf("   %s", failure.errorMessage))
			if failure.stackTrace != "" {
				stackLines := splitLines(failure.stackTrace)
				if len(stackLines) > 3 {
					stackLines = stackLines[:3]
				}
				lines = append(lines, fmt.Sprintf("   %s", strings.Join(stackLines, "\n   ")))
			}
		}
	}

	if r.hasDuration {
		lines = append(lines, fmt.Sprintf("\nDuration: %dms", r.durationMS))
	}

	return strings.Join(lines, "\n")
}

func formatTestUltra(r testResult) string {
	return fmt.Sprintf("[ok]%d [x]%d [skip]%d (%dms)", r.passed, r.failed, r.skipped, r.durationMS)
}
