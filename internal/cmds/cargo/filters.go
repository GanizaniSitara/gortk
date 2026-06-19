package cargo

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gortk/internal/core"
)

// splitLines splits text on "\n" and drops a trailing empty element so it
// matches Rust's str::lines() semantics (used pervasively by the rtk filters).
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

// formatCrateInfo formats a crate name + version into a display string. Faithful
// port of rtk's format_crate_info.
func formatCrateInfo(name, version, fallback string) string {
	switch {
	case name == "":
		return fallback
	case version == "":
		return name
	default:
		return name + " " + version
	}
}

// ---- cargo build / check ----------------------------------------------------

// cargoBuildHandler mirrors rtk's CargoBuildHandler. It tracks crate counts,
// error/warning counts, and the Finished line while classifying lines into
// skip / block-start / block-continuation, exactly as the Rust streaming
// handler does. filterCargoBuild drives it as a pure function.
type cargoBuildHandler struct {
	compiled     int
	warnings     int
	errorCount   int
	finishedLine string
	hasFinished  bool
}

func (h *cargoBuildHandler) shouldSkip(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(trimmed, "Compiling") || strings.HasPrefix(trimmed, "Checking") {
		h.compiled++
		return true
	}
	if strings.HasPrefix(trimmed, "Downloading") || strings.HasPrefix(trimmed, "Downloaded") {
		return true
	}
	if strings.HasPrefix(trimmed, "Finished") {
		h.finishedLine = trimmed
		h.hasFinished = true
		return true
	}
	if strings.HasPrefix(line, "warning:") && strings.Contains(line, "generated") && strings.Contains(line, "warning") {
		return true
	}
	if (strings.HasPrefix(line, "error:") || strings.HasPrefix(line, "error[")) &&
		(strings.Contains(line, "aborting due to") || strings.Contains(line, "could not compile")) {
		return true
	}
	return false
}

func (h *cargoBuildHandler) isBlockStart(line string) bool {
	if strings.HasPrefix(line, "error[") || strings.HasPrefix(line, "error:") {
		h.errorCount++
		return true
	}
	if strings.HasPrefix(line, "warning:") || strings.HasPrefix(line, "warning[") {
		h.warnings++
		return true
	}
	return false
}

func (h *cargoBuildHandler) isBlockContinuation(line string, block []string) bool {
	return !(strings.TrimSpace(line) == "" && len(block) > 3)
}

// filterCargoBuild compresses cargo build/check output: it strips compilation
// progress, counts crates, and on errors/warnings emits the diagnostic blocks
// (capped) with a count header. Faithful port of rtk's filter_cargo_build.
func filterCargoBuild(output string) string {
	h := &cargoBuildHandler{}
	var blocks [][]string
	var currentBlock []string
	inBlock := false

	for _, line := range splitLines(output) {
		if h.shouldSkip(line) {
			continue
		}
		if h.isBlockStart(line) {
			if inBlock && len(currentBlock) > 0 {
				blocks = append(blocks, currentBlock)
				currentBlock = nil
			}
			inBlock = true
			currentBlock = append(currentBlock, line)
		} else if inBlock {
			if h.isBlockContinuation(line, currentBlock) {
				currentBlock = append(currentBlock, line)
			} else {
				blocks = append(blocks, currentBlock)
				currentBlock = nil
				inBlock = false
			}
		}
	}
	if len(currentBlock) > 0 {
		blocks = append(blocks, currentBlock)
	}

	if h.errorCount == 0 && h.warnings == 0 {
		s := fmt.Sprintf("cargo build (%d crates compiled)", h.compiled)
		if h.hasFinished {
			s = s + "\n" + h.finishedLine
		}
		return s
	}

	var result strings.Builder
	fmt.Fprintf(&result, "cargo build: %d errors, %d warnings (%d crates)\n",
		h.errorCount, h.warnings, h.compiled)

	const maxCheckBlocks = core.CapErrors
	limit := len(blocks)
	if limit > maxCheckBlocks {
		limit = maxCheckBlocks
	}
	for i := 0; i < limit; i++ {
		result.WriteString(strings.Join(blocks[i], "\n"))
		result.WriteByte('\n')
		if i < len(blocks)-1 {
			result.WriteByte('\n')
		}
	}
	if len(blocks) > maxCheckBlocks {
		fmt.Fprintf(&result, "\n… +%d more issues\n", len(blocks)-maxCheckBlocks)
		// rtk appends a tee hint here; gortk's RunFiltered already tees the raw
		// output on failure, so the explicit hint is dropped to avoid duplicating
		// the side-channel.
	}
	return strings.TrimSpace(result.String())
}

// ---- cargo test -------------------------------------------------------------

// aggregatedTestResult mirrors rtk's AggregatedTestResult: it accumulates pass
// counts across test suites for a compact one-line summary.
type aggregatedTestResult struct {
	passed       int
	failed       int
	ignored      int
	measured     int
	filteredOut  int
	suites       int
	durationSecs float64
	hasDuration  bool
}

// testResultRE matches a cargo test summary line. Format:
// "test result: ok. 15 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.01s".
var testResultRE = regexp.MustCompile(
	`test result: (\w+)\.\s+(\d+) passed;\s+(\d+) failed;\s+(\d+) ignored;\s+(\d+) measured;\s+(\d+) filtered out(?:;\s+finished in ([\d.]+)s)?`,
)

// parseTestResultLine parses a "test result:" summary line. It returns a result
// and true only when the status is "ok" (all passed) and the line parses
// cleanly, matching rtk's AggregatedTestResult::parse_line.
func parseTestResultLine(line string) (aggregatedTestResult, bool) {
	caps := testResultRE.FindStringSubmatch(line)
	if caps == nil {
		return aggregatedTestResult{}, false
	}
	if caps[1] != "ok" {
		return aggregatedTestResult{}, false
	}
	passed, err1 := strconv.Atoi(caps[2])
	failed, err2 := strconv.Atoi(caps[3])
	ignored, err3 := strconv.Atoi(caps[4])
	measured, err4 := strconv.Atoi(caps[5])
	filteredOut, err5 := strconv.Atoi(caps[6])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil {
		return aggregatedTestResult{}, false
	}

	durationSecs := 0.0
	hasDuration := false
	if caps[7] != "" {
		if d, err := strconv.ParseFloat(caps[7], 64); err == nil {
			durationSecs = d
		}
		hasDuration = true
	}

	return aggregatedTestResult{
		passed:       passed,
		failed:       failed,
		ignored:      ignored,
		measured:     measured,
		filteredOut:  filteredOut,
		suites:       1,
		durationSecs: durationSecs,
		hasDuration:  hasDuration,
	}, true
}

// merge folds another suite's results into this one.
func (a *aggregatedTestResult) merge(other aggregatedTestResult) {
	a.passed += other.passed
	a.failed += other.failed
	a.ignored += other.ignored
	a.measured += other.measured
	a.filteredOut += other.filteredOut
	a.suites += other.suites
	a.durationSecs += other.durationSecs
	a.hasDuration = a.hasDuration && other.hasDuration
}

// formatCompact renders the aggregated result as a single compact line.
func (a aggregatedTestResult) formatCompact() string {
	parts := []string{fmt.Sprintf("%d passed", a.passed)}
	if a.ignored > 0 {
		parts = append(parts, fmt.Sprintf("%d ignored", a.ignored))
	}
	if a.filteredOut > 0 {
		parts = append(parts, fmt.Sprintf("%d filtered out", a.filteredOut))
	}
	counts := strings.Join(parts, ", ")

	suiteText := fmt.Sprintf("%d suites", a.suites)
	if a.suites == 1 {
		suiteText = "1 suite"
	}

	if a.hasDuration {
		return fmt.Sprintf("cargo test: %s (%s, %.2fs)", counts, suiteText, a.durationSecs)
	}
	return fmt.Sprintf("cargo test: %s (%s)", counts, suiteText)
}

// filterCargoTest compresses cargo test output: it aggregates pass results into
// one line, lists failures (capped), and falls back to a build-style summary on
// compile errors. Faithful port of rtk's filter_cargo_test.
func filterCargoTest(output string) string {
	var failures []string
	var summaryLines []string
	inFailureSection := false
	var currentFailure []string

	for _, line := range splitLines(output) {
		trimStart := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimStart, "Compiling") ||
			strings.HasPrefix(trimStart, "Downloading") ||
			strings.HasPrefix(trimStart, "Downloaded") ||
			strings.HasPrefix(trimStart, "Finished") {
			continue
		}

		if strings.HasPrefix(line, "running ") ||
			(strings.HasPrefix(line, "test ") && strings.HasSuffix(line, "... ok")) {
			continue
		}

		if line == "failures:" {
			inFailureSection = true
			continue
		}

		if inFailureSection {
			switch {
			case strings.HasPrefix(line, "test result:"):
				inFailureSection = false
				summaryLines = append(summaryLines, line)
			case strings.HasPrefix(line, "    ") || strings.HasPrefix(line, "---- "):
				currentFailure = append(currentFailure, line)
			case strings.TrimSpace(line) == "" && len(currentFailure) > 0:
				failures = append(failures, strings.Join(currentFailure, "\n"))
				currentFailure = nil
			case strings.TrimSpace(line) != "":
				currentFailure = append(currentFailure, line)
			}
		}

		if !inFailureSection && strings.HasPrefix(line, "test result:") {
			summaryLines = append(summaryLines, line)
		}
	}

	if len(currentFailure) > 0 {
		failures = append(failures, strings.Join(currentFailure, "\n"))
	}

	var result strings.Builder

	if len(failures) == 0 && len(summaryLines) > 0 {
		// All passed — try to aggregate.
		var aggregated aggregatedTestResult
		haveAgg := false
		allParsed := true
		for _, line := range summaryLines {
			if parsed, ok := parseTestResultLine(line); ok {
				if haveAgg {
					aggregated.merge(parsed)
				} else {
					aggregated = parsed
					haveAgg = true
				}
			} else {
				allParsed = false
				break
			}
		}

		if allParsed && haveAgg && aggregated.suites > 0 {
			return aggregated.formatCompact()
		}

		// Fallback: original behavior if regex failed.
		for _, line := range summaryLines {
			result.WriteString(line)
			result.WriteByte('\n')
		}
		return strings.TrimSpace(result.String())
	}

	if len(failures) > 0 {
		fmt.Fprintf(&result, "FAILURES (%d):\n", len(failures))
		const maxFailures = core.CapWarnings
		limit := len(failures)
		if limit > maxFailures {
			limit = maxFailures
		}
		for i := 0; i < limit; i++ {
			fmt.Fprintf(&result, "%d. %s\n", i+1, truncate(failures[i], 200))
		}
		if len(failures) > maxFailures {
			fmt.Fprintf(&result, "\n… +%d more failures\n", len(failures)-maxFailures)
			// rtk appends a tee hint; gortk's RunFiltered tees on failure already.
		}
		result.WriteByte('\n')
	}

	for _, line := range summaryLines {
		result.WriteString(line)
		result.WriteByte('\n')
	}

	if strings.TrimSpace(result.String()) == "" {
		hasCompileErrors := false
		for _, line := range splitLines(output) {
			trimmed := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(trimmed, "error[") || strings.HasPrefix(trimmed, "error:") {
				hasCompileErrors = true
				break
			}
		}

		if hasCompileErrors {
			buildFiltered := filterCargoBuild(output)
			if strings.HasPrefix(buildFiltered, "cargo build:") {
				return strings.Replace(buildFiltered, "cargo build:", "cargo test:", 1)
			}
		}

		// Fallback: show last 5 meaningful lines.
		var meaningful []string
		for _, l := range splitLines(output) {
			if strings.TrimSpace(l) != "" && !strings.HasPrefix(strings.TrimLeft(l, " \t"), "Compiling") {
				meaningful = append(meaningful, l)
			}
		}
		for _, l := range lastN(meaningful, 5) {
			result.WriteString(l)
			result.WriteByte('\n')
		}
	}

	return strings.TrimSpace(result.String())
}

// lastN returns the last n elements of xs (or all of them when len < n),
// preserving order. Mirrors rtk's `.iter().rev().take(n).rev()`.
func lastN(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return xs[len(xs)-n:]
}

// ---- cargo clippy -----------------------------------------------------------

// filterCargoClippy compresses cargo clippy output: it shows full error blocks
// and groups warnings by lint rule with location samples. Faithful port of
// rtk's filter_cargo_clippy.
func filterCargoClippy(output string) string {
	byRule := map[string][]string{}
	var ruleOrder []string // insertion order, to break ties deterministically
	errorCount := 0
	warningCount := 0
	var errorBlocks [][]string

	currentRule := ""
	inError := false
	var currentBlock []string

	for _, line := range splitLines(output) {
		trimStart := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimStart, "Compiling") ||
			strings.HasPrefix(trimStart, "Checking") ||
			strings.HasPrefix(trimStart, "Downloading") ||
			strings.HasPrefix(trimStart, "Downloaded") ||
			strings.HasPrefix(trimStart, "Finished") {
			if inError && len(currentBlock) > 0 {
				errorBlocks = append(errorBlocks, currentBlock)
				currentBlock = nil
				inError = false
			}
			continue
		}

		if (strings.Contains(line, "generated") && strings.Contains(line, "warning")) ||
			strings.Contains(line, "aborting due to") ||
			strings.Contains(line, "could not compile") {
			continue
		}

		isErrorLine := strings.HasPrefix(line, "error:") || strings.HasPrefix(line, "error[")
		isWarningLine := strings.HasPrefix(line, "warning:") || strings.HasPrefix(line, "warning[")

		switch {
		case isErrorLine || isWarningLine:
			if inError && len(currentBlock) > 0 {
				errorBlocks = append(errorBlocks, currentBlock)
				currentBlock = nil
			}
			inError = false

			if isErrorLine {
				errorCount++
				inError = true
				currentBlock = append(currentBlock, line)
			} else {
				warningCount++
			}

			currentRule = extractClippyRule(line, isErrorLine)

		case strings.HasPrefix(trimStart, "--> "):
			location := strings.TrimPrefix(strings.TrimLeft(line, " \t"), "--> ")
			if currentRule != "" {
				if _, seen := byRule[currentRule]; !seen {
					ruleOrder = append(ruleOrder, currentRule)
				}
				byRule[currentRule] = append(byRule[currentRule], location)
			}
			if inError {
				currentBlock = append(currentBlock, line)
			}

		case inError:
			if strings.TrimSpace(line) == "" {
				if len(currentBlock) > 0 {
					errorBlocks = append(errorBlocks, currentBlock)
					currentBlock = nil
				}
				inError = false
			} else if len(currentBlock) < 15 {
				currentBlock = append(currentBlock, line)
			}
		}
	}

	if inError && len(currentBlock) > 0 {
		errorBlocks = append(errorBlocks, currentBlock)
	}

	if errorCount == 0 && warningCount == 0 {
		return "cargo clippy: No issues found"
	}

	var result strings.Builder
	fmt.Fprintf(&result, "cargo clippy: %d errors, %d warnings\n", errorCount, warningCount)

	if len(errorBlocks) > 0 {
		const maxClippyErrors = core.CapWarnings
		result.WriteString("\nErrors:\n")
		limit := len(errorBlocks)
		if limit > maxClippyErrors {
			limit = maxClippyErrors
		}
		for _, block := range errorBlocks[:limit] {
			for _, blockLine := range block {
				fmt.Fprintf(&result, "  %s\n", truncate(blockLine, 160))
			}
			result.WriteByte('\n')
		}
		if len(errorBlocks) > maxClippyErrors {
			fmt.Fprintf(&result, "  … +%d more errors\n", len(errorBlocks)-maxClippyErrors)
		}
	}

	// Sort warning rules by frequency (descending), stable on insertion order to
	// match rtk's sort_by_key with Reverse (which is a stable sort).
	rules := make([]string, len(ruleOrder))
	copy(rules, ruleOrder)
	sort.SliceStable(rules, func(i, j int) bool {
		return len(byRule[rules[i]]) > len(byRule[rules[j]])
	})

	const maxRules = core.CapList
	shown := len(rules)
	if shown > maxRules {
		shown = maxRules
	}
	for _, rule := range rules[:shown] {
		locations := byRule[rule]
		fmt.Fprintf(&result, "  %s (%dx)\n", rule, len(locations))
		locLimit := len(locations)
		if locLimit > 3 {
			locLimit = 3
		}
		for _, loc := range locations[:locLimit] {
			fmt.Fprintf(&result, "    %s\n", loc)
		}
		if len(locations) > 3 {
			fmt.Fprintf(&result, "    … +%d more\n", len(locations)-3)
		}
	}

	if len(byRule) > maxRules {
		fmt.Fprintf(&result, "\n… +%d more rules\n", len(byRule)-maxRules)
	}

	return strings.TrimSpace(result.String())
}

// extractClippyRule pulls the lint rule / error-code used for warning grouping
// out of a diagnostic line: the bracketed code if present, else the message
// after the "error: "/"warning: " prefix. Mirrors rtk's inline extraction.
func extractClippyRule(line string, isErrorLine bool) string {
	if bracketStart := strings.LastIndexByte(line, '['); bracketStart >= 0 {
		if bracketEnd := strings.LastIndexByte(line, ']'); bracketEnd >= 0 {
			return line[bracketStart+1 : bracketEnd]
		}
		return line
	}
	prefix := "warning: "
	if isErrorLine {
		prefix = "error: "
	}
	return strings.TrimPrefix(line, prefix)
}

// ---- cargo install ----------------------------------------------------------

// filterCargoInstall compresses cargo install output: it strips dep compilation
// noise and keeps the installed/replaced lines, actionable warnings, and error
// blocks. Faithful port of rtk's filter_cargo_install.
func filterCargoInstall(output string) string {
	var errors []string
	errorCount := 0
	compiled := 0
	inError := false
	var currentError []string
	installedCrate := ""
	installedVersion := ""
	var replacedLines []string
	alreadyInstalled := false
	ignoredLine := ""

	for _, line := range splitLines(output) {
		trimmed := strings.TrimLeft(line, " \t")

		if strings.HasPrefix(trimmed, "Compiling") {
			compiled++
			continue
		}
		if strings.HasPrefix(trimmed, "Downloading") ||
			strings.HasPrefix(trimmed, "Downloaded") ||
			strings.HasPrefix(trimmed, "Locking") ||
			strings.HasPrefix(trimmed, "Updating") ||
			strings.HasPrefix(trimmed, "Adding") ||
			strings.HasPrefix(trimmed, "Finished") ||
			strings.HasPrefix(trimmed, "Blocking waiting for file lock") {
			continue
		}

		// Installing line: extract crate name + version.
		if strings.HasPrefix(trimmed, "Installing") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "Installing"))
			if rest != "" && !strings.HasPrefix(rest, "/") {
				if name, version, found := strings.Cut(rest, " "); found {
					installedCrate = name
					installedVersion = version
				} else {
					installedCrate = rest
				}
			}
			continue
		}

		// Installed line: extract crate + version if not already set.
		if strings.HasPrefix(trimmed, "Installed") {
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "Installed"))
			if rest != "" && installedCrate == "" {
				parts := strings.Fields(rest)
				if len(parts) >= 2 {
					installedCrate = parts[0]
					installedVersion = parts[1]
				}
			}
			continue
		}

		if strings.HasPrefix(trimmed, "Replacing") || strings.HasPrefix(trimmed, "Replaced") {
			replacedLines = append(replacedLines, trimmed)
			continue
		}

		if strings.HasPrefix(trimmed, "Ignored package") {
			alreadyInstalled = true
			ignoredLine = trimmed
			continue
		}

		// Actionable warnings (e.g. "be sure to add ... to your PATH"); skip the
		// "generated N warnings" summary noise.
		if strings.HasPrefix(line, "warning:") {
			if !(strings.Contains(line, "generated") && strings.Contains(line, "warning")) {
				replacedLines = append(replacedLines, line)
			}
			continue
		}

		// Error blocks.
		if strings.HasPrefix(line, "error[") || strings.HasPrefix(line, "error:") {
			if strings.Contains(line, "aborting due to") || strings.Contains(line, "could not compile") {
				continue
			}
			if inError && len(currentError) > 0 {
				errors = append(errors, strings.Join(currentError, "\n"))
				currentError = nil
			}
			errorCount++
			inError = true
			currentError = append(currentError, line)
		} else if inError {
			if strings.TrimSpace(line) == "" && len(currentError) > 3 {
				errors = append(errors, strings.Join(currentError, "\n"))
				currentError = nil
				inError = false
			} else {
				currentError = append(currentError, line)
			}
		}
	}

	if len(currentError) > 0 {
		errors = append(errors, strings.Join(currentError, "\n"))
	}

	// Already installed / up to date.
	if alreadyInstalled {
		info := ignoredLine
		if parts := strings.Split(ignoredLine, "`"); len(parts) >= 2 {
			info = parts[1]
		}
		return fmt.Sprintf("cargo install: %s already installed", info)
	}

	// Errors.
	if errorCount > 0 {
		crateInfo := formatCrateInfo(installedCrate, installedVersion, "")
		depsInfo := ""
		if compiled > 0 {
			depsInfo = fmt.Sprintf(", %d deps compiled", compiled)
		}

		var result strings.Builder
		plural := ""
		if errorCount > 1 {
			plural = "s"
		}
		if crateInfo == "" {
			fmt.Fprintf(&result, "cargo install: %d error%s%s\n", errorCount, plural, depsInfo)
		} else {
			fmt.Fprintf(&result, "cargo install: %d error%s (%s%s)\n", errorCount, plural, crateInfo, depsInfo)
		}

		const maxInstallErrors = core.CapErrors
		limit := len(errors)
		if limit > maxInstallErrors {
			limit = maxInstallErrors
		}
		for i := 0; i < limit; i++ {
			result.WriteString(errors[i])
			result.WriteByte('\n')
			if i < len(errors)-1 {
				result.WriteByte('\n')
			}
		}
		if len(errors) > maxInstallErrors {
			fmt.Fprintf(&result, "\n… +%d more issues\n", len(errors)-maxInstallErrors)
		}
		return strings.TrimSpace(result.String())
	}

	// Success.
	crateInfo := formatCrateInfo(installedCrate, installedVersion, "package")
	var result strings.Builder
	fmt.Fprintf(&result, "cargo install (%s, %d deps compiled)", crateInfo, compiled)
	for _, line := range replacedLines {
		fmt.Fprintf(&result, "\n  %s", line)
	}
	return result.String()
}

// ---- cargo nextest ----------------------------------------------------------

// nextestSummaryRE matches the nextest run summary line, e.g.
// "Summary [   0.192s] 301 tests run: 301 passed, 0 skipped".
var nextestSummaryRE = regexp.MustCompile(
	`Summary \[\s*([\d.]+)s\]\s+(\d+) tests? run:\s+(\d+) passed(?:,\s+(\d+) failed)?(?:,\s+(\d+) skipped)?`,
)

// nextestStartingRE matches "Starting 301 tests across 1 binary".
var nextestStartingRE = regexp.MustCompile(`Starting \d+ tests? across (\d+) binar(?:y|ies)`)

// flushFailureBlock appends a completed failure block (header + body) to
// failures, then clears the buffers. Faithful port of rtk's flush_failure_block.
func flushFailureBlock(header *string, body *[]string, failures *[]string) {
	if *header == "" {
		return
	}
	block := *header
	if len(*body) > 0 {
		block += "\n" + strings.Join(*body, "\n")
	}
	*failures = append(*failures, block)
	*header = ""
	*body = nil
}

// filterCargoNextest compresses cargo-nextest output: it strips PASS/compilation
// noise, collects FAIL blocks with their stderr detail, and emits a compact
// summary. Faithful port of rtk's filter_cargo_nextest.
func filterCargoNextest(output string) string {
	var failures []string
	inFailureBlock := false
	pastSummary := false
	currentFailureHeader := ""
	var currentFailureBody []string
	summaryLine := ""
	binaries := 0
	hasCancelLine := false

	for _, line := range splitLines(output) {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "Compiling") ||
			strings.HasPrefix(trimmed, "Downloading") ||
			strings.HasPrefix(trimmed, "Downloaded") ||
			strings.HasPrefix(trimmed, "Finished") ||
			strings.HasPrefix(trimmed, "Locking") ||
			strings.HasPrefix(trimmed, "Updating") {
			continue
		}

		if strings.HasPrefix(trimmed, "────") {
			continue
		}

		if pastSummary {
			continue
		}

		if strings.HasPrefix(trimmed, "Starting") {
			if caps := nextestStartingRE.FindStringSubmatch(trimmed); caps != nil {
				if n, err := strconv.Atoi(caps[1]); err == nil {
					binaries = n
				}
			}
			continue
		}

		if strings.HasPrefix(trimmed, "PASS") {
			if inFailureBlock {
				flushFailureBlock(&currentFailureHeader, &currentFailureBody, &failures)
				inFailureBlock = false
			}
			continue
		}

		if strings.HasPrefix(trimmed, "FAIL") {
			if inFailureBlock {
				flushFailureBlock(&currentFailureHeader, &currentFailureBody, &failures)
			}
			currentFailureHeader = trimmed
			inFailureBlock = true
			continue
		}

		if strings.HasPrefix(trimmed, "Cancelling") || strings.HasPrefix(trimmed, "Canceling") {
			hasCancelLine = true
			continue
		}

		if strings.HasPrefix(trimmed, "Nextest run ID") {
			continue
		}

		if strings.HasPrefix(trimmed, "Summary") {
			summaryLine = trimmed
			if inFailureBlock {
				flushFailureBlock(&currentFailureHeader, &currentFailureBody, &failures)
				inFailureBlock = false
			}
			pastSummary = true
			continue
		}

		if inFailureBlock {
			currentFailureBody = append(currentFailureBody, line)
		}
	}

	if inFailureBlock {
		flushFailureBlock(&currentFailureHeader, &currentFailureBody, &failures)
	}

	if caps := nextestSummaryRE.FindStringSubmatch(summaryLine); caps != nil {
		duration := caps[1]
		if duration == "" {
			duration = "?"
		}
		passed := atoiOr(caps[3], 0)
		failed := atoiOr(caps[4], 0)
		skipped := atoiOr(caps[5], 0)

		binaryText := ""
		switch {
		case binaries > 1:
			binaryText = fmt.Sprintf("%d binaries", binaries)
		case binaries == 1:
			binaryText = "1 binary"
		}

		if failed == 0 {
			parts := []string{fmt.Sprintf("%d passed", passed)}
			if skipped > 0 {
				parts = append(parts, fmt.Sprintf("%d skipped", skipped))
			}
			meta := fmt.Sprintf("%ss", duration)
			if binaryText != "" {
				meta = fmt.Sprintf("%s, %ss", binaryText, duration)
			}
			return fmt.Sprintf("cargo nextest: %s (%s)", strings.Join(parts, ", "), meta)
		}

		var result strings.Builder
		for _, failure := range failures {
			result.WriteString(failure)
			result.WriteByte('\n')
		}
		if hasCancelLine {
			result.WriteString("Cancelling due to test failure\n")
		}

		summaryParts := []string{fmt.Sprintf("%d passed", passed)}
		if failed > 0 {
			summaryParts = append(summaryParts, fmt.Sprintf("%d failed", failed))
		}
		if skipped > 0 {
			summaryParts = append(summaryParts, fmt.Sprintf("%d skipped", skipped))
		}
		meta := fmt.Sprintf("%ss", duration)
		if binaryText != "" {
			meta = fmt.Sprintf("%s, %ss", binaryText, duration)
		}
		fmt.Fprintf(&result, "cargo nextest: %s (%s)", strings.Join(summaryParts, ", "), meta)
		return strings.TrimSpace(result.String())
	}

	// Fallback: summary regex didn't match.
	if len(failures) > 0 {
		var result strings.Builder
		for _, failure := range failures {
			result.WriteString(failure)
			result.WriteByte('\n')
		}
		if summaryLine != "" {
			result.WriteString(summaryLine)
		}
		return strings.TrimSpace(result.String())
	}

	if summaryLine != "" {
		return summaryLine
	}

	return ""
}

// atoiOr parses s as an int, returning def on empty/invalid input.
func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
