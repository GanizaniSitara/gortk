// Package gradlew is gortk's token-optimized Android Gradle wrapper. It wraps
// the project's `./gradlew` (or `gradlew.bat` on Windows, falling back to a
// system `gradle`), classifies the requested task, and applies a task-specific
// output filter that compresses Gradle's verbose logs down to the lines an
// agent actually needs. Faithful port of rtk's src/cmds/jvm/gradlew_cmd.rs.
//
// Verbose flags (--stacktrace / --info / --debug / --full-stacktrace) bypass
// filtering entirely and stream the tool's output unchanged. gortk makes no
// network calls of its own; the only process spawned is the Gradle wrapper the
// user asked for.
package gradlew

import (
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "gradlew",
		Summary: "Android Gradle wrapper with compact output (build, test, lint)",
		Run:     Run,
	})
}

// ── Shared regex patterns (used across multiple filters) ─────────────────────

var (
	taskLineRE    = regexp.MustCompile(`^> Task :`)
	trySectionRE  = regexp.MustCompile(`^\* Try:|^> Run with --|^> Get more help at`)
	buildStatusRE = regexp.MustCompile(`^BUILD (SUCCESSFUL|FAILED)`)
	actionableRE  = regexp.MustCompile(`^\d+ actionable tasks?`)
)

// gradlewTask classifies the invocation so the right output filter is applied.
type gradlewTask int

const (
	taskBuild gradlewTask = iota
	taskTest
	taskConnectedTest
	taskLint
	taskDependencies
	taskOther
)

// detectTask determines which filter to apply from the requested Gradle tasks.
// It uses the last non-flag, non-clean task: e.g. `clean assembleDebug` → Build.
// For mixed-task invocations like `test assemble`, the last task wins.
func detectTask(args []string) gradlewTask {
	task := ""
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		if strings.ToLower(a) == "clean" {
			continue
		}
		task = strings.ToLower(a) // keep the last matching one
	}

	switch {
	case strings.Contains(task, "connected"):
		return taskConnectedTest
	case strings.Contains(task, "test"):
		return taskTest
	case strings.Contains(task, "assemble"),
		strings.Contains(task, "build"),
		strings.Contains(task, "bundle"),
		strings.Contains(task, "install"):
		return taskBuild
	case strings.Contains(task, "lint"),
		strings.Contains(task, "ktlint"),
		strings.Contains(task, "detekt"):
		return taskLint
	case task == "check":
		return taskTest
	case strings.Contains(task, "dependencies"):
		return taskDependencies
	case task == "":
		// Only "clean" was passed (filtered out above) → treat as Build to
		// filter task noise.
		return taskBuild
	default:
		return taskOther
	}
}

// gradlewBinary returns the Gradle executable name: prefers the project wrapper
// (`gradlew.bat` on Windows, `./gradlew` elsewhere), falling back to a system
// `gradle`. Mirrors rtk's gradlew_binary().
func gradlewBinary() string {
	if isWindows() {
		if fileExists(".\\gradlew.bat") {
			return ".\\gradlew.bat"
		}
		return "gradle"
	}
	if fileExists("./gradlew") {
		return "./gradlew"
	}
	return "gradle"
}

// Run executes the gradlew command. args are the tokens after "gradlew";
// verbose is the -v count.
func Run(args []string, verbose int) (int, error) {
	// Verbose flags bypass filtering — the user wants the full output.
	for _, a := range args {
		if a == "--stacktrace" || a == "--info" || a == "--debug" || a == "--full-stacktrace" {
			return core.RunPassthrough(gradlewBinary(), args, verbose)
		}
	}

	tool := gradlewBinary()
	argsDisplay := strings.Join(args, " ")

	switch detectTask(args) {
	case taskBuild:
		cmd := core.ResolvedCommand(tool, args...)
		opts := core.RunOptions{TeeLabel: "gradlew_build"}
		return core.RunFiltered(cmd, tool, argsDisplay, filterBuild, opts)
	case taskTest:
		cmd := core.ResolvedCommand(tool, args...)
		opts := core.RunOptions{TeeLabel: "gradlew_test"}
		return core.RunFiltered(cmd, tool, argsDisplay, filterTest, opts)
	case taskConnectedTest:
		cmd := core.ResolvedCommand(tool, args...)
		opts := core.RunOptions{TeeLabel: "gradlew_connected"}
		return core.RunFiltered(cmd, tool, argsDisplay, filterConnected, opts)
	case taskLint:
		cmd := core.ResolvedCommand(tool, args...)
		opts := core.RunOptions{TeeLabel: "gradlew_lint"}
		return core.RunFiltered(cmd, tool, argsDisplay, filterLint, opts)
	case taskDependencies:
		cmd := core.ResolvedCommand(tool, args...)
		opts := core.RunOptions{TeeLabel: "gradlew_deps"}
		return core.RunFiltered(cmd, tool, argsDisplay, filterDependencies, opts)
	default: // taskOther
		return core.RunPassthrough(tool, args, verbose)
	}
}

// ── Build filter ──────────────────────────────────────────────────────────────

var (
	buildDaemonRE = regexp.MustCompile(`^(Starting a Gradle Daemon|Daemon will be stopped|Reusing configuration cache|Calculating task graph|> Configure project|Deprecated Gradle features|You can use|For more on this|Configuration cache entry)`)
	buildProgress = regexp.MustCompile(`^\s*\d+%|^Downloading|^Configuring|^Resolving|^\[Incubating\]|^Wrote HTML report|^class \S+ could not|^\[android-`)
	buildErrorRE  = regexp.MustCompile(`(?i)(^FAILURE:|^\* What went wrong:|^\* Where:|> Could not|e: |error:|^Execution failed|Lint found \d+ error)`)
	buildWarnRE   = regexp.MustCompile(`^(w: |warning:|Warning:|WARNING:)`)
	buildScanRE   = regexp.MustCompile(`gradle\.com/s/|Publishing build scan`)
)

// filterBuildLine reports whether a single build-output line should be kept.
func filterBuildLine(line string) bool {
	// Always strip these.
	if taskLineRE.MatchString(line) ||
		buildDaemonRE.MatchString(line) ||
		buildProgress.MatchString(line) ||
		trySectionRE.MatchString(line) {
		return false
	}

	// Always keep these.
	return buildStatusRE.MatchString(line) ||
		actionableRE.MatchString(line) ||
		buildErrorRE.MatchString(line) ||
		buildWarnRE.MatchString(line) ||
		buildScanRE.MatchString(line) ||
		strings.TrimSpace(line) == "" // preserve blank lines separating error sections
}

// filterBuild keeps the lines for which filterBuildLine returns true,
// mirroring rtk's line-streamed BuildLineFilter. Each kept line is emitted with
// a trailing newline, so the result ends in "\n" like the streamed version.
func filterBuild(output string) string {
	var b strings.Builder
	for _, line := range splitLines(output) {
		if filterBuildLine(line) {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// ── Test output filter ────────────────────────────────────────────────────────

var (
	testFailedRE      = regexp.MustCompile(`FAILED$| FAILED `)
	testPassedSkipped = regexp.MustCompile(` PASSED$| SKIPPED$`)
	testSummaryRE     = regexp.MustCompile(`\d+ tests? completed|\d+ tests? failed|There were failing tests|See the report at`)
)

// isFrameworkFrame reports whether an "at ..." stack frame belongs to a test
// framework (JUnit, Gradle runner, reflection) rather than user code.
func isFrameworkFrame(trimmed string) bool {
	return strings.HasPrefix(trimmed, "at org.junit.") ||
		strings.HasPrefix(trimmed, "at junit.") ||
		strings.HasPrefix(trimmed, "at java.lang.reflect.") ||
		strings.HasPrefix(trimmed, "at sun.reflect.") ||
		strings.HasPrefix(trimmed, "at org.gradle.")
}

func filterTest(output string) string {
	if output == "" {
		return ""
	}

	var resultLines []string
	inFailureBlock := false

	for _, line := range splitLines(output) {
		// Skip always-noise lines.
		if taskLineRE.MatchString(line) || trySectionRE.MatchString(line) {
			continue
		}

		// Build summary lines always kept.
		if buildStatusRE.MatchString(line) || actionableRE.MatchString(line) || testSummaryRE.MatchString(line) {
			resultLines = append(resultLines, line)
			continue
		}

		// PASSED/SKIPPED per-test lines — strip.
		if testPassedSkipped.MatchString(line) {
			inFailureBlock = false
			continue
		}

		// FAILED per-test lines — keep + enter failure block for stack trace.
		if testFailedRE.MatchString(line) {
			inFailureBlock = true
			resultLines = append(resultLines, line)
			continue
		}

		// Stack trace lines following a failure.
		if inFailureBlock {
			trimmed := strings.TrimSpace(line)
			switch {
			case strings.HasPrefix(trimmed, "java.") || strings.HasPrefix(trimmed, "kotlin."):
				// Exception class + message — always keep.
				resultLines = append(resultLines, line)
			case strings.HasPrefix(trimmed, "at "):
				// Skip framework frames, keep first user-code frame.
				if !isFrameworkFrame(trimmed) {
					resultLines = append(resultLines, line)
					inFailureBlock = false
				}
			case trimmed != "":
				inFailureBlock = false
			}
		}
	}

	filtered := strings.Join(resultLines, "\n")

	// Guarantee non-empty output.
	if strings.TrimSpace(filtered) == "" {
		if strings.Contains(output, "BUILD SUCCESSFUL") {
			return "ok ✓ (no test output — add testLogging to build.gradle for details)"
		}
		return strings.TrimSpace(output)
	}

	return filtered
}

// ── Connected / instrumented test filter ─────────────────────────────────────

var (
	// instrumentationStatusRE faithfully ports rtk's regex
	// `^INSTRUMENTATION_STATUS[_CODE]*:` — the bracket is a character class, so
	// it matches both INSTRUMENTATION_STATUS: and INSTRUMENTATION_STATUS_CODE:.
	instrumentationStatusRE = regexp.MustCompile(`^INSTRUMENTATION_STATUS[_CODE]*:`)
	instrumentationResultRE = regexp.MustCompile(`^INSTRUMENTATION_RESULT:`)
	instrumentationCodeRE   = regexp.MustCompile(`^INSTRUMENTATION_CODE:`)
	startingTestsRE         = regexp.MustCompile(`^Starting \d+ tests? on `)
	installingAPKRE         = regexp.MustCompile(`^Installing APK`)
)

func filterConnected(output string) string {
	if output == "" {
		return ""
	}

	// Special case: no device.
	if strings.Contains(output, "No connected devices!") {
		return "connectedAndroidTest failed: No connected devices! Start an emulator or connect a device."
	}

	var resultLines []string
	for _, line := range splitLines(output) {
		if instrumentationStatusRE.MatchString(line) ||
			instrumentationResultRE.MatchString(line) ||
			instrumentationCodeRE.MatchString(line) ||
			startingTestsRE.MatchString(line) ||
			installingAPKRE.MatchString(line) ||
			taskLineRE.MatchString(line) ||
			trySectionRE.MatchString(line) {
			continue
		}
		resultLines = append(resultLines, line)
	}

	// After stripping instrumentation noise, connected test output uses the same
	// PASSED/FAILED line format as unit tests — delegate to filterTest.
	joined := strings.Join(resultLines, "\n")
	filtered := filterTest(joined)

	if strings.TrimSpace(filtered) == "" {
		return "ok ✓ (connected tests passed)"
	}
	return filtered
}

// ── Lint output filter ────────────────────────────────────────────────────────

var (
	androidLintErrorRE   = regexp.MustCompile(`[^:]+:\d+:.*[Ee]rror:.*\[`)
	androidLintWarningRE = regexp.MustCompile(`[^:]+:\d+:.*[Ww]arning:.*\[`)
	ktlintViolationRE    = regexp.MustCompile(`[^:]+:\d+:\d+:.*[Ll]int`)
	detektViolationRE    = regexp.MustCompile(`[^:]+:\d+:\d+:.*error`)
	lintSummaryRE        = regexp.MustCompile(`\d+ (issues?|errors?|warnings?)`)
	lintReportRE         = regexp.MustCompile(`Wrote (HTML|XML|text) report|file://|/build/reports/lint`)
)

func filterLint(output string) string {
	if output == "" {
		return ""
	}

	// Android lint emits violation + code snippet + caret + explanation,
	// separated from the next violation by a blank line. Keep up to 3 non-empty
	// context lines so the LLM sees the offending code without opening the file.
	const maxContextLines = 3

	var resultLines []string
	contextRemaining := 0

	for _, line := range splitLines(output) {
		if taskLineRE.MatchString(line) || trySectionRE.MatchString(line) || lintReportRE.MatchString(line) {
			contextRemaining = 0
			continue
		}

		isAndroidLint := androidLintErrorRE.MatchString(line) || androidLintWarningRE.MatchString(line)

		if buildStatusRE.MatchString(line) ||
			actionableRE.MatchString(line) ||
			lintSummaryRE.MatchString(line) ||
			isAndroidLint ||
			ktlintViolationRE.MatchString(line) ||
			detektViolationRE.MatchString(line) {
			resultLines = append(resultLines, line)
			// Only Android lint violations have multi-line context;
			// ktlint/detekt/summary lines are single-line.
			if isAndroidLint {
				contextRemaining = maxContextLines
			} else {
				contextRemaining = 0
			}
			continue
		}

		if contextRemaining > 0 {
			if strings.TrimSpace(line) == "" {
				// Blank line terminates the context block.
				contextRemaining = 0
			} else {
				resultLines = append(resultLines, line)
				contextRemaining--
			}
		}
	}

	filtered := strings.Join(resultLines, "\n")

	if strings.TrimSpace(filtered) == "" {
		if strings.Contains(output, "BUILD SUCCESSFUL") {
			return "ok ✓ lint passed"
		}
		return strings.TrimSpace(output)
	}

	return filtered
}

// ── Dependencies output filter ───────────────────────────────────────────────

func filterDependencies(output string) string {
	if output == "" {
		return ""
	}

	type config struct {
		name string
		deps []string
	}
	var configs []config
	currentConfig := ""
	var currentDeps []string
	totalDeps := 0

	flush := func() {
		if currentConfig != "" && len(currentDeps) > 0 {
			configs = append(configs, config{name: currentConfig, deps: currentDeps})
		}
	}

	for _, line := range splitLines(output) {
		trimmed := strings.TrimSpace(line)

		// Skip noise.
		if trimmed == "" ||
			taskLineRE.MatchString(trimmed) ||
			trySectionRE.MatchString(trimmed) ||
			buildStatusRE.MatchString(trimmed) ||
			actionableRE.MatchString(trimmed) ||
			strings.HasPrefix(trimmed, "Downloading") ||
			strings.HasPrefix(trimmed, "Download ") ||
			strings.HasPrefix(trimmed, "Starting a Gradle") ||
			trimmed == "No dependencies" ||
			trimmed == "(n)" {
			continue
		}

		// Configuration header: "compileClasspath - Compile classpath ...".
		// Not indented, not a tree line, contains " - ".
		if !strings.HasPrefix(trimmed, "+") &&
			!strings.HasPrefix(trimmed, "|") &&
			!strings.HasPrefix(trimmed, "\\") &&
			!strings.HasPrefix(trimmed, " ") &&
			strings.Contains(trimmed, " - ") {
			flush()
			currentConfig = strings.SplitN(trimmed, " - ", 2)[0]
			currentDeps = nil
			continue
		}

		// Top-level dependencies only (first level of the tree).
		// Check the *untrimmed* line — top-level deps start at column 0,
		// transitive deps are indented (e.g. "|    +---" or "     \---").
		if (strings.HasPrefix(line, "+---") || strings.HasPrefix(line, "\\---")) && currentConfig != "" {
			dep := strings.TrimPrefix(strings.TrimPrefix(trimmed, "+--- "), "\\--- ")
			currentDeps = append(currentDeps, dep)
			totalDeps++
		}
	}

	// Flush last config.
	flush()

	if len(configs) == 0 {
		if strings.Contains(output, "BUILD SUCCESSFUL") {
			return "ok ✓ no dependencies"
		}
		return strings.TrimSpace(output)
	}

	var result strings.Builder
	fmt.Fprintf(&result, "%d top-level dependencies across %d configurations\n", totalDeps, len(configs))

	const maxGradleDeps = core.CapList
	for _, c := range configs {
		fmt.Fprintf(&result, "\n%s (%d):\n", c.name, len(c.deps))
		limit := maxGradleDeps
		if limit > len(c.deps) {
			limit = len(c.deps)
		}
		for _, dep := range c.deps[:limit] {
			fmt.Fprintf(&result, "  %s\n", dep)
		}
		if len(c.deps) > maxGradleDeps {
			fmt.Fprintf(&result, "  ... +%d more\n", len(c.deps)-maxGradleDeps)
		}
	}

	return strings.TrimRight(result.String(), "\n\r\t ")
}

// ── Helpers ──────────────────────────────────────────────────────────────────

// splitLines mirrors Rust's str::lines(): it splits on "\n" and drops a single
// trailing empty element so a final newline does not yield a phantom blank line.
func splitLines(s string) []string {
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// isWindows reports whether gortk is running on Windows, so the wrapper picker
// can prefer gradlew.bat.
func isWindows() bool {
	return runtime.GOOS == "windows"
}

// fileExists reports whether path names an existing file or directory in the
// current working directory.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
