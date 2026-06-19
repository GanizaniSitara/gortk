// Package gocmd is gortk's token-optimized `go` wrapper. It filters Go toolchain
// output — test results (via `go test -json` streaming), build errors, and vet
// warnings — keeping just the essentials an agent needs. Faithful port of rtk's
// src/cmds/go/go_cmd.rs.
//
// The package is named `gocmd` because `go` is a Go keyword and cannot name a
// package, but the registered command Name is "go".
//
// Like rtk, this wraps the platform `go`; gortk resolves it PATHEXT-aware via
// core.ResolvedCommand. The output-compression logic lives in pure helper
// functions (filterGoTestJSON, filterGoBuildWithExit, filterGoVet) so it can be
// tested directly against the ported Rust spec.
//
// Subcommand dispatch is parsed from args inside Run, mirroring rtk's clap
// GoCommands enum: test / build / vet, plus an external-subcommand passthrough
// for any other go subcommand.
//
// Simplifications vs rtk (documented intentionally):
//   - rtk intercepts `go tool golangci-lint` to apply its golangci JSON filter
//     (run_go_tool_golangci_lint / match_go_tool). That path depends on rtk's
//     separate golangci_cmd module, which is out of scope for this single-package
//     port; gortk lets `go tool ...` fall through to a plain passthrough. The
//     golangci wrapper can be ported on its own later.
//   - rtk's tracking/timer side-channels are dropped: gortk is offline by default
//     and the core runner already records token savings.
package gocmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "go",
		Summary: "Go commands with compact output (test/build/vet + passthrough)",
		Run:     Run,
	})
}

// maxGoBuildErrors / maxGoVetIssues mirror rtk's CAP_ERRORS-based caps.
const (
	maxGoBuildErrors = core.CapErrors
	maxGoVetIssues   = core.CapErrors
)

// Run dispatches the gortk `go` command. args are the tokens after "go";
// verbose is the -v count. It parses the go subcommand itself, mirroring rtk's
// clap dispatch in main.rs (GoCommands), then routes test / build / vet to their
// filtered runners and everything else to a direct passthrough.
func Run(args []string, verbose int) (int, error) {
	if len(args) == 0 {
		// No subcommand: rtk's run_other bails with an error; gortk passes the
		// bare `go` through so it prints its own usage.
		return core.RunPassthrough("go", args, verbose)
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "test":
		return runTest(subArgs, verbose)
	case "build":
		return runBuild(subArgs, verbose)
	case "vet":
		return runVet(subArgs, verbose)
	default:
		// Passthrough: any unsupported go subcommand runs directly. This includes
		// `go tool ...` (see package-doc note on the dropped golangci interception).
		return core.RunPassthrough("go", args, verbose)
	}
}

// runTest runs `go test`, defaulting to `-json` streaming for ~90% token
// reduction, then compresses the NDJSON event stream via filterGoTestJSON.
// Mirrors rtk's go_cmd::run_test.
func runTest(args []string, verbose int) (int, error) {
	// rtk skips -json when the user already passed -json or any -bench flag
	// (benchmarks don't play well with JSON streaming here).
	skipJSON := false
	for _, a := range args {
		if a == "-json" || strings.HasPrefix(a, "-bench") {
			skipJSON = true
			break
		}
	}

	cmd := core.ResolvedCommand("go")
	cmd.Args = append(cmd.Args, "test")
	if !skipJSON {
		cmd.Args = append(cmd.Args, "-json")
	}
	cmd.Args = append(cmd.Args, args...)

	if verbose > 0 {
		prefix := ""
		if !skipJSON {
			prefix = "-json "
		}
		fmt.Fprintf(os.Stderr, "Running: go test %s%s\n", prefix, strings.Join(args, " "))
	}

	filter := filterGoTestJSON
	if skipJSON {
		// Without -json there is no structured stream to compress; pass the raw
		// captured stdout through unchanged.
		filter = func(s string) string { return s }
	}

	// rtk: RunOptions::stdout_only().tee("go_test"). The JSON stream is on stdout;
	// stderr (compiler diagnostics for a failed build) is passed through verbatim.
	opts := core.RunOptions{FilterStdoutOnly: true, TeeLabel: "go_test"}
	return core.RunFiltered(cmd, "go test", strings.Join(args, " "), filter, opts)
}

// runBuild runs `go build` and shows only errors. Mirrors rtk's go_cmd::run_build,
// which uses run_filtered_with_exit so a non-zero exit never reports success.
func runBuild(args []string, verbose int) (int, error) {
	cmd := core.ResolvedCommand("go")
	cmd.Args = append(cmd.Args, "build")
	cmd.Args = append(cmd.Args, args...)

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: go build %s\n", strings.Join(args, " "))
	}

	opts := core.RunOptions{TeeLabel: "go_build"}
	return core.RunFilteredWithExit(cmd, "go build", strings.Join(args, " "), filterGoBuildWithExit, opts)
}

// runVet runs `go vet` and shows the reported issues. Mirrors rtk's
// go_cmd::run_vet.
func runVet(args []string, verbose int) (int, error) {
	cmd := core.ResolvedCommand("go")
	cmd.Args = append(cmd.Args, "vet")
	cmd.Args = append(cmd.Args, args...)

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: go vet %s\n", strings.Join(args, " "))
	}

	opts := core.RunOptions{TeeLabel: "go_vet"}
	return core.RunFiltered(cmd, "go vet", strings.Join(args, " "), filterGoVet, opts)
}

// ---------------------------------------------------------------------------
// go test -json filtering
// ---------------------------------------------------------------------------

// goTestEvent mirrors rtk's GoTestEvent: one decoded line of `go test -json`
// NDJSON output. Pointer fields distinguish "absent" from "empty", matching
// Rust's Option<T>.
type goTestEvent struct {
	Action      string   `json:"Action"`
	Package     *string  `json:"Package"`
	Test        *string  `json:"Test"`
	Output      *string  `json:"Output"`
	Elapsed     *float64 `json:"Elapsed"`
	ImportPath  *string  `json:"ImportPath"`
	FailedBuild *string  `json:"FailedBuild"`
}

// failedTest is a (name, output lines) pair for a failed test.
type failedTest struct {
	name    string
	outputs []string
}

// packageResult accumulates per-package test results, mirroring rtk's
// PackageResult.
type packageResult struct {
	pass             int
	fail             int
	skip             int
	buildFailed      bool
	buildErrors      []string
	failedTests      []failedTest
	packageFailed    bool     // package-level failure (timeout, signal, etc.)
	packageFailOutput []string // output lines collected before the package fail
}

// testKey identifies a single test within a package.
type testKey struct {
	pkg  string
	test string
}

// filterGoTestJSON parses `go test -json` output (NDJSON) into a compact
// summary. Faithful port of rtk's filter_go_test_json.
func filterGoTestJSON(output string) string {
	packages := map[string]*packageResult{}
	// pkgOrder preserves first-seen package order so the output is deterministic
	// (Rust's HashMap iteration order is arbitrary; the ported tests only assert
	// on substrings, but stable order keeps the result reproducible).
	var pkgOrder []string
	currentTestOutput := map[testKey][]string{} // (package, test) -> outputs
	buildOutput := map[string][]string{}        // import_path -> error lines

	getPkg := func(name string) *packageResult {
		pr, ok := packages[name]
		if !ok {
			pr = &packageResult{}
			packages[name] = pr
			pkgOrder = append(pkgOrder, name)
		}
		return pr
	}

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		var event goTestEvent
		if err := json.Unmarshal([]byte(trimmed), &event); err != nil {
			continue // Skip non-JSON lines
		}

		// Handle build-output/build-fail events (use ImportPath, no Package).
		switch event.Action {
		case "build-output":
			if event.ImportPath != nil && event.Output != nil {
				text := strings.TrimRight(*event.Output, " \t\r\n")
				if text != "" {
					buildOutput[*event.ImportPath] = append(buildOutput[*event.ImportPath], text)
				}
			}
			continue
		case "build-fail":
			// build-fail has ImportPath — handled when the package-level fail arrives.
			continue
		}

		pkgName := "unknown"
		if event.Package != nil {
			pkgName = *event.Package
		}
		pr := getPkg(pkgName)

		switch event.Action {
		case "pass":
			if event.Test != nil {
				pr.pass++
			}
		case "fail":
			switch {
			case event.Test != nil:
				// Individual test failure.
				pr.fail++
				key := testKey{pkg: pkgName, test: *event.Test}
				outputs := currentTestOutput[key]
				delete(currentTestOutput, key)
				pr.failedTests = append(pr.failedTests, failedTest{name: *event.Test, outputs: outputs})
			case event.FailedBuild != nil:
				// Package-level build failure.
				pr.buildFailed = true
				if errs, ok := buildOutput[*event.FailedBuild]; ok {
					pr.buildErrors = errs
					delete(buildOutput, *event.FailedBuild)
				}
			default:
				// Package-level failure without a specific test or build error
				// (timeout, signal kill, panic before test execution, etc.).
				pr.packageFailed = true
			}
		case "skip":
			if event.Test != nil {
				pr.skip++
			}
		case "output":
			if event.Output != nil {
				if event.Test != nil {
					key := testKey{pkg: pkgName, test: *event.Test}
					currentTestOutput[key] = append(currentTestOutput[key], strings.TrimRight(*event.Output, " \t\r\n"))
				} else {
					t := strings.TrimSpace(*event.Output)
					if t != "" {
						pr.packageFailOutput = append(pr.packageFailOutput, t)
					}
				}
			}
		}
		// run, pause, cont, start, etc. are ignored.
	}

	// Build summary.
	totalPackages := len(packages)
	totalPass := 0
	totalFail := 0
	totalSkip := 0
	totalBuildFail := 0
	totalPkgFail := 0
	for _, pr := range packages {
		totalPass += pr.pass
		totalFail += pr.fail
		totalSkip += pr.skip
		if pr.buildFailed {
			totalBuildFail++
		}
		// Only count package-level fails for packages with no individual test or
		// build failures. go test -json emits a trailing package-level
		// {"action":"fail"} after any test failure too, but that event is just a
		// cascade — the individual test failures are already counted.
		if pr.packageFailed && pr.fail == 0 && !pr.buildFailed {
			totalPkgFail++
		}
	}

	hasFailures := totalFail > 0 || totalBuildFail > 0 || totalPkgFail > 0

	if !hasFailures && totalPass == 0 {
		return "Go test: No tests found"
	}

	if !hasFailures {
		return fmt.Sprintf("Go test: %d passed in %d packages", totalPass, totalPackages)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Go test: %d passed, %d failed", totalPass, totalFail+totalBuildFail+totalPkgFail)
	if totalSkip > 0 {
		fmt.Fprintf(&b, ", %d skipped", totalSkip)
	}
	fmt.Fprintf(&b, " in %d packages\n", totalPackages)

	// Show package-level failures first (timeouts, signals, panics). Skip packages
	// that already have individual test-level failures — those are displayed in the
	// per-package section below and the package-level event is just a cascade.
	for _, pkg := range pkgOrder {
		pr := packages[pkg]
		if !pr.packageFailed || pr.fail > 0 || pr.buildFailed {
			continue
		}
		fmt.Fprintf(&b, "\n%s [FAIL]\n", compactPackageName(pkg))
		for _, line := range pr.packageFailOutput {
			t := strings.TrimSpace(line)
			if t != "" {
				fmt.Fprintf(&b, "  %s\n", truncate(t, 120))
			}
		}
	}

	// Show build failures.
	for _, pkg := range pkgOrder {
		pr := packages[pkg]
		if !pr.buildFailed {
			continue
		}
		fmt.Fprintf(&b, "\n%s [build failed]\n", compactPackageName(pkg))
		for _, line := range pr.buildErrors {
			t := strings.TrimSpace(line)
			// Skip the "# package" header line.
			if !strings.HasPrefix(t, "#") && t != "" {
				fmt.Fprintf(&b, "  %s\n", truncate(t, 120))
			}
		}
	}

	// Show failed tests grouped by package.
	for _, pkg := range pkgOrder {
		pr := packages[pkg]
		if pr.fail == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n%s (%d passed, %d failed)\n", compactPackageName(pkg), pr.pass, pr.fail)
		for _, ft := range pr.failedTests {
			fmt.Fprintf(&b, "  [FAIL] %s\n", ft.name)
			for _, line := range selectGoTestFailureLines(ft.outputs) {
				fmt.Fprintf(&b, "     %s\n", truncate(line, 100))
			}
		}
	}

	return strings.TrimSpace(b.String())
}

// selectGoTestFailureLines picks the most relevant lines from a failed test's
// output (locations, assertion text, immediate follow-up context), capped at 5.
// Faithful port of rtk's select_go_test_failure_lines.
func selectGoTestFailureLines(outputs []string) []string {
	var relevant []string
	keepNextContextLine := false

	for _, line := range outputs {
		trimmed := strings.TrimSpace(line)

		if trimmed == "" ||
			strings.HasPrefix(trimmed, "=== RUN") ||
			strings.HasPrefix(trimmed, "--- FAIL") ||
			strings.HasPrefix(trimmed, "--- PASS") {
			keepNextContextLine = false
			continue
		}

		isLocation := isGoTestLocationLine(trimmed)
		isFailure := isGoTestFailureLine(trimmed)

		if isLocation || isFailure || keepNextContextLine {
			relevant = append(relevant, trimmed)
			keepNextContextLine = isLocation
		} else {
			keepNextContextLine = false
		}

		if len(relevant) >= 5 {
			break
		}
	}

	if len(relevant) == 0 {
		for _, line := range outputs {
			t := strings.TrimSpace(line)
			if t != "" &&
				!strings.HasPrefix(t, "=== RUN") &&
				!strings.HasPrefix(t, "--- FAIL") &&
				!strings.HasPrefix(t, "--- PASS") {
				relevant = append(relevant, t)
				break
			}
		}
	}

	return relevant
}

// isGoTestLocationLine reports whether a line looks like a source location
// ("foo_test.go:42:"). Faithful port of rtk's is_go_test_location_line.
func isGoTestLocationLine(line string) bool {
	if idx := strings.Index(line, ".go:"); idx >= 0 {
		rest := line[idx+len(".go:"):]
		if rest == "" {
			return false
		}
		c := rest[0]
		return c >= '0' && c <= '9'
	}
	return false
}

// isGoTestFailureLine reports whether a line carries failure signal (assertion
// keywords, panics, errors). Faithful port of rtk's is_go_test_failure_line.
func isGoTestFailureLine(line string) bool {
	lower := strings.ToLower(line)

	return strings.HasPrefix(lower, "panic:") ||
		strings.HasPrefix(lower, "error:") ||
		strings.Contains(lower, " error:") ||
		strings.Contains(lower, "expected") ||
		strings.Contains(lower, "got") ||
		strings.Contains(lower, "want") ||
		strings.Contains(lower, "actual") ||
		strings.Contains(lower, "assert") ||
		strings.Contains(lower, "mismatch") ||
		strings.Contains(lower, "unexpected") ||
		strings.Contains(lower, "fatal") ||
		strings.HasPrefix(line, "at ")
}

// ---------------------------------------------------------------------------
// go build filtering
// ---------------------------------------------------------------------------

// filterGoBuild filters go build output with an assumed-success exit code,
// exposed for the ported tests. Faithful port of rtk's filter_go_build.
func filterGoBuild(output string) string {
	return filterGoBuildWithExit(output, 0)
}

// filterGoBuildWithExit shows only build errors; a non-zero exit with no
// recognized error lines never reports success. Faithful port of rtk's
// filter_go_build_with_exit.
func filterGoBuildWithExit(output string, exitCode int) string {
	var errors []string
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if isGoBuildErrorLine(trimmed) {
			errors = append(errors, trimmed)
		}
	}

	if len(errors) == 0 {
		if exitCode == 0 {
			return "Go build: Success"
		}
		return formatGoBuildFailure(output, exitCode)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Go build: %d errors\n", len(errors))

	limit := len(errors)
	if limit > maxGoBuildErrors {
		limit = maxGoBuildErrors
	}
	for i, e := range errors[:limit] {
		fmt.Fprintf(&b, "%d. %s\n", i+1, truncate(e, 120))
	}
	if len(errors) > maxGoBuildErrors {
		// rtk also emits a force-tee tail hint here; the gortk core runner already
		// tees the full raw output on failure (RunOptions.TeeLabel), so the extra
		// per-filter tail file is dropped — the summary line is preserved.
		fmt.Fprintf(&b, "\n… +%d more errors\n", len(errors)-maxGoBuildErrors)
	}

	return strings.TrimSpace(b.String())
}

// formatGoBuildFailure renders an opaque non-zero build failure (no recognized
// error lines). Faithful port of rtk's format_go_build_failure.
func formatGoBuildFailure(output string, exitCode int) string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			lines = append(lines, t)
		}
	}

	if len(lines) == 0 {
		return fmt.Sprintf("Go build: failed (exit %d)", exitCode)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Go build: failed (exit %d)\n", exitCode)
	b.WriteString("═══════════════════════════════════════\n")

	limit := len(lines)
	if limit > maxGoBuildErrors {
		limit = maxGoBuildErrors
	}
	for i, line := range lines[:limit] {
		fmt.Fprintf(&b, "%d. %s\n", i+1, truncate(line, 120))
	}
	if len(lines) > maxGoBuildErrors {
		fmt.Fprintf(&b, "\n… +%d more output lines\n", len(lines)-maxGoBuildErrors)
	}

	return strings.TrimSpace(b.String())
}

// isGoBuildErrorLine reports whether a line is a real compilation/config error
// (as opposed to a download/progress/header line). Faithful port of rtk's
// is_go_build_error_line.
func isGoBuildErrorLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}

	lower := strings.ToLower(trimmed)

	// Go download/progress lines often contain package names like pkg/errors,
	// xerrors, or multierror. These are not compilation failures.
	if strings.HasPrefix(lower, "go: downloading ") ||
		strings.HasPrefix(lower, "go: finding ") ||
		strings.HasPrefix(lower, "go: extracting ") {
		return false
	}

	// Package headers are context, not errors by themselves.
	if strings.HasPrefix(trimmed, "#") {
		return false
	}

	// Canonical compiler/config error locations: file:line:col: ...
	isGoConfigLocation := !strings.HasPrefix(lower, "go: ") &&
		(strings.Contains(lower, "go.mod:") || strings.Contains(lower, "go.work:") || strings.Contains(lower, "go.sum:"))
	if strings.Contains(trimmed, ".go:") || isGoConfigLocation {
		return true
	}

	// Some compiler/module failures do not include a file.go:line:col location.
	nonFileErrorPrefixes := []string{
		"undefined: ",
		"cannot use ",
		"cannot find package ",
		"no required module provides package ",
		"missing go.sum entry for module providing package ",
		"found packages ",
		"go: go.mod file not found in current directory or any parent directory",
		"go: cannot load module ",
		"go: build failed",
		"go: error ",
		"error: ",
		"pattern ",
		"go: updates to go.mod needed",
		"go: inconsistent vendoring",
		"no go files in ",
	}
	for _, prefix := range nonFileErrorPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return strings.Contains(lower, "import cycle not allowed") ||
		strings.Contains(lower, "build constraints exclude all go files") ||
		strings.Contains(lower, "function main is undeclared in the main package")
}

// ---------------------------------------------------------------------------
// go vet filtering
// ---------------------------------------------------------------------------

// filterGoVet shows go vet issues (file:line:col lines). Faithful port of rtk's
// filter_go_vet.
func filterGoVet(output string) string {
	var issues []string
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		// vet reports issues with file:line:col format.
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") && strings.Contains(trimmed, ".go:") {
			issues = append(issues, trimmed)
		}
	}

	if len(issues) == 0 {
		return "Go vet: No issues found"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Go vet: %d issues\n", len(issues))

	limit := len(issues)
	if limit > maxGoVetIssues {
		limit = maxGoVetIssues
	}
	for i, issue := range issues[:limit] {
		fmt.Fprintf(&b, "%d. %s\n", i+1, truncate(issue, 120))
	}
	if len(issues) > maxGoVetIssues {
		// rtk emits a force-tee tail hint; gortk's core runner tees the full raw
		// output on failure, so the per-filter tail file is dropped.
		fmt.Fprintf(&b, "\n… +%d more issues\n", len(issues)-maxGoVetIssues)
	}

	return strings.TrimSpace(b.String())
}

// ---------------------------------------------------------------------------
// shared helpers
// ---------------------------------------------------------------------------

// compactPackageName strips a long module path down to its final segment
// (e.g. "github.com/user/repo/pkg" -> "pkg"). Faithful port of rtk's
// compact_package_name.
func compactPackageName(pkg string) string {
	if pos := strings.LastIndex(pkg, "/"); pos >= 0 {
		return pkg[pos+1:]
	}
	return pkg
}

// truncate shortens s to at most maxLen runes, appending "..." when it must cut.
// Faithful port of rtk's utils::truncate (counts runes, min usable len 3).
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
