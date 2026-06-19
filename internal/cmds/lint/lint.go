// Package lint is gortk's token-optimized linter wrapper. It runs ESLint (and
// other linters) and compresses their output by grouping violations by rule and
// by file. Faithful port of rtk's src/cmds/js/lint_cmd.rs.
//
// Like rtk, the JS linters are invoked through the local package manager
// (npx / pnpm exec / yarn exec) when the tool is not directly on PATH, and the
// Python linters (ruff, pylint, mypy, flake8) are invoked directly. gortk
// resolves tools PATHEXT-aware on Windows.
//
// rtk delegates ruff and mypy to dedicated modules. gortk ports those as
// separate command packages; here the ruff/mypy linters fall through to the
// generic line-scanning filter so that this package stays self-contained per
// the porting contract (write only inside internal/cmds/lint/).
package lint

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "lint",
		Summary: "ESLint with grouped rule violations",
		Run:     Run,
	})
}

// passthroughMaxChars mirrors rtk's config limits().passthrough_max_chars,
// used to cap raw fallback output when JSON parsing fails.
const passthroughMaxChars = 2000

// eslintMessage is one ESLint diagnostic. Field tags mirror the ESLint JSON
// report keys.
type eslintMessage struct {
	RuleID   *string `json:"ruleId"`
	Severity int     `json:"severity"`
	Message  string  `json:"message"`
	Line     int     `json:"line"`
	Column   int     `json:"column"`
}

// eslintResult is the per-file entry in the ESLint JSON report.
type eslintResult struct {
	FilePath     string          `json:"filePath"`
	Messages     []eslintMessage `json:"messages"`
	ErrorCount   int             `json:"errorCount"`
	WarningCount int             `json:"warningCount"`
}

// pylintDiagnostic is one pylint JSON2 diagnostic. Only the fields the filter
// reads are populated; the rest mirror the schema for documentation.
type pylintDiagnostic struct {
	MsgType   string `json:"type"` // "warning", "error", "convention", "refactor"
	Path      string `json:"path"`
	Symbol    string `json:"symbol"`     // rule code like "unused-variable"
	MessageID string `json:"message-id"` // e.g. "W0612"
}

// isPythonLinter reports whether a linter is Python-based (uses pip/pipx, not
// npm/pnpm) and therefore resolved directly off PATH.
func isPythonLinter(linter string) bool {
	switch linter {
	case "ruff", "pylint", "mypy", "flake8":
		return true
	default:
		return false
	}
}

// stripPMPrefix returns the number of leading package-manager prefix args
// (npx, bunx, pnpm, yarn, exec) to skip.
func stripPMPrefix(args []string) int {
	pmNames := map[string]bool{"npx": true, "bunx": true, "pnpm": true, "yarn": true}
	skip := 0
	for _, arg := range args {
		if pmNames[arg] || arg == "exec" {
			skip++
		} else {
			break
		}
	}
	return skip
}

// detectLinter derives the linter name from args (after PM prefixes are
// stripped). It returns the linter name and whether it was explicitly given.
func detectLinter(args []string) (string, bool) {
	isPathOrFlag := len(args) == 0 ||
		strings.HasPrefix(args[0], "-") ||
		strings.Contains(args[0], "/") ||
		strings.Contains(args[0], ".")

	if isPathOrFlag {
		return "eslint", false
	}
	return args[0], true
}

// packageManagerExec builds the *exec.Cmd for a JS linter, preferring a
// directly-resolvable tool and otherwise routing through the detected package
// manager (pnpm exec / yarn exec / npx). Mirrors rtk's package_manager_exec.
func packageManagerExec(tool string) (string, []string) {
	if core.ToolExists(tool) {
		return tool, nil
	}
	switch detectPackageManager() {
	case "pnpm":
		return "pnpm", []string{"exec", "--", tool}
	case "yarn":
		return "yarn", []string{"exec", "--", tool}
	default:
		return "npx", []string{"--no-install", "--", tool}
	}
}

// detectPackageManager picks a JS package manager based on lockfiles present in
// the working directory. Mirrors rtk's detect_package_manager fallback order.
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
	_, err := os.Stat(name)
	return err == nil
}

// truncate trims s to at most maxLen runes, appending "..." when cut.
// Mirrors rtk's utils::truncate.
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

// Run executes the lint command. args are the args AFTER the command name.
func Run(args []string, verbose int) (int, error) {
	skip := stripPMPrefix(args)
	effectiveArgs := args[skip:]

	linter, explicit := detectLinter(effectiveArgs)

	// Python linters resolve directly off PATH; JS linters route through the
	// package manager when not directly resolvable.
	var toolName string
	var prefixArgs []string
	if isPythonLinter(linter) {
		toolName = linter
	} else {
		toolName, prefixArgs = packageManagerExec(linter)
	}

	var cmdArgs []string
	cmdArgs = append(cmdArgs, prefixArgs...)

	// Add format flags based on linter.
	switch {
	case linter == "eslint":
		cmdArgs = append(cmdArgs, "-f", "json")
	case linter == "ruff" && !containsStr(effectiveArgs, "--output-format"):
		cmdArgs = append(cmdArgs, "check", "--output-format=json")
	case linter == "pylint" && !containsStr(effectiveArgs, "--output-format"):
		cmdArgs = append(cmdArgs, "--output-format=json2")
	case linter == "mypy":
		// mypy uses default text output (no special flags).
	}

	// Determine where the user args start (skip the linter name when explicit,
	// and skip "check" for ruff if we already added it).
	startIdx := 0
	if explicit {
		if linter == "ruff" && len(effectiveArgs) > 0 && effectiveArgs[0] == "ruff" {
			if len(effectiveArgs) > 1 && effectiveArgs[1] == "check" {
				startIdx = 2
			} else {
				startIdx = 1
			}
		} else {
			startIdx = 1
		}
	}

	for _, arg := range effectiveArgs[startIdx:] {
		// Skip --output-format if we already added it.
		if linter == "ruff" && strings.HasPrefix(arg, "--output-format") {
			continue
		}
		if linter == "pylint" && strings.HasPrefix(arg, "--output-format") {
			continue
		}
		cmdArgs = append(cmdArgs, arg)
	}

	// Default to current directory if no path specified.
	switch linter {
	case "ruff", "pylint", "mypy", "eslint":
		hasPath := false
		for _, a := range effectiveArgs[startIdx:] {
			if !strings.HasPrefix(a, "-") && !strings.Contains(a, "=") {
				hasPath = true
				break
			}
		}
		if !hasPath {
			cmdArgs = append(cmdArgs, ".")
		}
	}

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: %s with structured output\n", linter)
	}

	cmd := core.ResolvedCommand(toolName, cmdArgs...)

	// FilterStdoutOnly: linters write the structured report to stdout. We
	// dispatch to the right filter based on the linter; the filter receives
	// stdout, while stderr is passed through verbatim by the runner.
	opts := core.RunOptions{
		FilterStdoutOnly: true,
		TeeLabel:         "lint",
	}
	return core.RunFiltered(cmd, "lint", linter+" "+strings.Join(args, " "), func(stdout string) string {
		switch linter {
		case "eslint":
			return filterESLintJSON(stdout)
		case "pylint":
			return filterPylintJSON(stdout)
		default:
			// ruff/mypy/biome/unknown -> generic line scan. (rtk delegates
			// ruff/mypy to dedicated modules ported separately in gortk.)
			return filterGenericLint(stdout)
		}
	}, opts)
}

func containsStr(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// filterESLintJSON groups ESLint JSON output by rule and by file. Pure
// function: the behavioural spec ported from rtk's filter_eslint_json.
func filterESLintJSON(output string) string {
	var results []eslintResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &results); err != nil {
		return fmt.Sprintf(
			"ESLint output (JSON parse failed: %s)\n%s",
			err,
			truncate(output, passthroughMaxChars),
		)
	}

	totalErrors := 0
	totalWarnings := 0
	totalFiles := 0
	for _, r := range results {
		totalErrors += r.ErrorCount
		totalWarnings += r.WarningCount
		if len(r.Messages) > 0 {
			totalFiles++
		}
	}

	if totalErrors == 0 && totalWarnings == 0 {
		return "ESLint: No issues found"
	}

	// Group messages by rule.
	byRule := map[string]int{}
	for i := range results {
		for _, msg := range results[i].Messages {
			if msg.RuleID != nil {
				byRule[*msg.RuleID]++
			}
		}
	}

	// Group by file (files with messages), sorted by message count desc.
	type fileGroup struct {
		res   *eslintResult
		count int
	}
	var byFile []fileGroup
	for i := range results {
		if len(results[i].Messages) > 0 {
			byFile = append(byFile, fileGroup{res: &results[i], count: len(results[i].Messages)})
		}
	}
	sort.SliceStable(byFile, func(i, j int) bool {
		return byFile[i].count > byFile[j].count
	})

	var b strings.Builder
	fmt.Fprintf(&b, "ESLint: %d errors, %d warnings in %d files\n", totalErrors, totalWarnings, totalFiles)

	// Top rules.
	ruleCounts := sortedCounts(byRule)
	if len(ruleCounts) > 0 {
		b.WriteString("Top rules:\n")
		for _, rc := range takeCounts(ruleCounts, 10) {
			fmt.Fprintf(&b, "  %s (%dx)\n", rc.key, rc.count)
		}
		b.WriteByte('\n')
	}

	// Top files with most issues, plus the top rules in each.
	const maxFiles = core.CapWarnings
	b.WriteString("Top files:\n")
	limit := maxFiles
	if limit > len(byFile) {
		limit = len(byFile)
	}
	for _, fg := range byFile[:limit] {
		shortPath := compactPath(fg.res.FilePath)
		fmt.Fprintf(&b, "  %s (%d issues)\n", shortPath, fg.count)

		fileRules := map[string]int{}
		for _, msg := range fg.res.Messages {
			if msg.RuleID != nil {
				fileRules[*msg.RuleID]++
			}
		}
		for _, rc := range takeCounts(sortedCounts(fileRules), 3) {
			fmt.Fprintf(&b, "    %s (%d)\n", rc.key, rc.count)
		}
	}

	if len(byFile) > maxFiles {
		fmt.Fprintf(&b, "\n… +%d more files\n", len(byFile)-maxFiles)
	}

	return strings.TrimSpace(b.String())
}

// filterPylintJSON groups pylint JSON2 output by symbol and by file. Pure
// function ported from rtk's filter_pylint_json.
func filterPylintJSON(output string) string {
	var diagnostics []pylintDiagnostic
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &diagnostics); err != nil {
		return fmt.Sprintf(
			"Pylint output (JSON parse failed: %s)\n%s",
			err,
			truncate(output, passthroughMaxChars),
		)
	}

	if len(diagnostics) == 0 {
		return "Pylint: No issues found"
	}

	errors, warnings, conventions, refactors := 0, 0, 0, 0
	for _, d := range diagnostics {
		switch d.MsgType {
		case "error":
			errors++
		case "warning":
			warnings++
		case "convention":
			conventions++
		case "refactor":
			refactors++
		}
	}

	// Count unique files.
	uniqueFiles := map[string]bool{}
	for _, d := range diagnostics {
		uniqueFiles[d.Path] = true
	}
	totalFiles := len(uniqueFiles)

	// Group by symbol (rule code).
	bySymbol := map[string]int{}
	for _, d := range diagnostics {
		key := fmt.Sprintf("%s (%s)", d.Symbol, d.MessageID)
		bySymbol[key]++
	}

	// Group by file.
	byFile := map[string]int{}
	for _, d := range diagnostics {
		byFile[d.Path]++
	}
	fileCounts := sortedCounts(byFile)

	var b strings.Builder
	fmt.Fprintf(&b, "Pylint: %d issues in %d files\n", len(diagnostics), totalFiles)

	if errors > 0 || warnings > 0 {
		fmt.Fprintf(&b, "  %d errors, %d warnings", errors, warnings)
		if conventions > 0 || refactors > 0 {
			fmt.Fprintf(&b, ", %d conventions, %d refactors", conventions, refactors)
		}
		b.WriteByte('\n')
	}

	// Top symbols (rules).
	symbolCounts := sortedCounts(bySymbol)
	if len(symbolCounts) > 0 {
		b.WriteString("Top rules:\n")
		for _, sc := range takeCounts(symbolCounts, 10) {
			fmt.Fprintf(&b, "  %s (%dx)\n", sc.key, sc.count)
		}
		b.WriteByte('\n')
	}

	// Top files.
	const maxFiles = core.CapWarnings
	b.WriteString("Top files:\n")
	limit := maxFiles
	if limit > len(fileCounts) {
		limit = len(fileCounts)
	}
	for _, fc := range fileCounts[:limit] {
		shortPath := compactPath(fc.key)
		fmt.Fprintf(&b, "  %s (%d issues)\n", shortPath, fc.count)

		// Top 3 rules in this file.
		fileSymbols := map[string]int{}
		for _, d := range diagnostics {
			if d.Path == fc.key {
				key := fmt.Sprintf("%s (%s)", d.Symbol, d.MessageID)
				fileSymbols[key]++
			}
		}
		for _, sc := range takeCounts(sortedCounts(fileSymbols), 3) {
			fmt.Fprintf(&b, "    %s (%d)\n", sc.key, sc.count)
		}
	}

	if len(fileCounts) > maxFiles {
		fmt.Fprintf(&b, "\n… +%d more files\n", len(fileCounts)-maxFiles)
	}

	return strings.TrimSpace(b.String())
}

// filterGenericLint is the fallback line-scanning filter for non-ESLint/pylint
// linters. Pure function ported from rtk's filter_generic_lint.
func filterGenericLint(output string) string {
	warnings := 0
	errors := 0
	var issues []string

	for _, line := range splitLines(output) {
		lineLower := strings.ToLower(line)
		if strings.Contains(lineLower, "warning") {
			warnings++
			issues = append(issues, line)
		}
		if strings.Contains(lineLower, "error") && !strings.Contains(lineLower, "0 error") {
			errors++
			issues = append(issues, line)
		}
	}

	if errors == 0 && warnings == 0 {
		return "Lint: No issues found"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Lint: %d errors, %d warnings\n", errors, warnings)

	const maxIssues = core.CapErrors
	limit := maxIssues
	if limit > len(issues) {
		limit = len(issues)
	}
	for _, issue := range issues[:limit] {
		fmt.Fprintf(&b, "%s\n", truncate(issue, 100))
	}

	if len(issues) > maxIssues {
		fmt.Fprintf(&b, "\n… +%d more issues\n", len(issues)-maxIssues)
	}

	return strings.TrimSpace(b.String())
}

// compactPath shortens a linter file path by stripping common prefixes.
// Mirrors rtk's compact_path: prefer the segment after the last /src/ or /lib/,
// else the basename. Pure function.
func compactPath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")

	if pos := strings.LastIndex(path, "/src/"); pos >= 0 {
		return "src/" + path[pos+5:]
	}
	if pos := strings.LastIndex(path, "/lib/"); pos >= 0 {
		return "lib/" + path[pos+5:]
	}
	if pos := strings.LastIndex(path, "/"); pos >= 0 {
		return path[pos+1:]
	}
	return path
}

// countEntry is a (key, count) pair used for sorting grouped counts.
type countEntry struct {
	key   string
	count int
}

// sortedCounts converts a count map to a slice sorted by count descending. Ties
// break on key ascending for determinism (Rust's sort_by on the count alone is
// unstable across ties; we make the Go port deterministic without changing the
// observable top-N for the tested inputs).
func sortedCounts(m map[string]int) []countEntry {
	out := make([]countEntry, 0, len(m))
	for k, v := range m {
		out = append(out, countEntry{key: k, count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].key < out[j].key
	})
	return out
}

func takeCounts(c []countEntry, n int) []countEntry {
	if n > len(c) {
		n = len(c)
	}
	return c[:n]
}

// splitLines splits text into lines with .lines() semantics: a trailing empty
// element from a final newline is dropped. The runner normalizes CRLF before
// this filter runs, but we are defensive here.
func splitLines(s string) []string {
	s = core.NormalizeNewlines(s)
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
