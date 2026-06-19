// Package dotnet is gortk's token-optimized .NET CLI wrapper. It filters
// `dotnet build` / `test` / `restore` / `format` output down to the signal an
// agent needs — build errors/warnings, test pass/fail counts and failing
// tests, restore diagnostics, and format-report summaries — and passes any
// other `dotnet` subcommand through verbatim. Faithful port of rtk's
// src/cmds/dotnet/dotnet_cmd.rs.
//
// The structured parsers live in binlog.go (MSBuild .binlog + text fallbacks),
// trx.go (.trx test results), and format_report.go (dotnet format JSON). This
// file holds the entry point, the dotnet-arg injection (binlog/trx/MTP-runner
// detection), the summary merge/normalize logic, and the output formatters.
//
// Like rtk, this drives a temporary MSBuild binary log: gortk injects
// `-bl:<temp>` for build/restore (and for test when the user asks for one),
// parses it, then deletes it. The TRX logger is injected for `dotnet test` so
// per-test results can be recovered. rtk's tracking/timer side-channels and its
// force-tee overflow file hints are dropped — gortk is offline by default and
// the core runner already provides tee-on-failure via RunOptions.TeeLabel.
package dotnet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"gortk/internal/core"
	"gortk/internal/registry"
)

const (
	dotnetCLIUILanguage      = "DOTNET_CLI_UI_LANGUAGE"
	dotnetCLIUILanguageValue = "en-US"
)

// Truncation caps mirror rtk's CAP_ERRORS / CAP_WARNINGS / CAP_LIST.
const (
	maxBuildErrors     = core.CapErrors
	maxBuildWarnings   = core.CapWarnings
	maxDotnetFailures  = core.CapWarnings
	maxTestErrors      = core.CapWarnings
	maxTestWarnings    = core.CapWarnings
	maxRestoreErrors   = core.CapErrors
	maxRestoreWarnings = core.CapWarnings
	maxFormatFiles     = core.CapList
)

var tempPathCounter uint64

func init() {
	registry.Register(&registry.Cmd{
		Name:    "dotnet",
		Summary: ".NET CLI commands with compact output (build/test/restore/format)",
		Run:     Run,
	})
}

// Run dispatches the gortk `dotnet` command. args are the tokens after
// "dotnet"; verbose is the -v count. It parses the dotnet subcommand itself,
// mirroring rtk's clap dispatch in main.rs (DotnetCommands): build / test /
// restore / format route to their filtered runners, and any other subcommand
// is passed through verbatim.
func Run(args []string, verbose int) (int, error) {
	if len(args) == 0 {
		// No subcommand: pass through bare `dotnet` so it prints its usage.
		return runPassthrough(args, verbose)
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "build":
		return runDotnetWithBinlog("build", subArgs, verbose)
	case "test":
		return runDotnetWithBinlog("test", subArgs, verbose)
	case "restore":
		return runDotnetWithBinlog("restore", subArgs, verbose)
	case "format":
		return runFormat(subArgs, verbose)
	default:
		return runPassthrough(args, verbose)
	}
}

// runPassthrough runs `dotnet <args...>` with no filtering, streaming stdio
// directly. Mirrors rtk's run_passthrough (minus tracking).
func runPassthrough(args []string, verbose int) (int, error) {
	cmd := core.ResolvedCommand("dotnet", args...)
	cmd.Env = append(os.Environ(), dotnetCLIUILanguage+"="+dotnetCLIUILanguageValue)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if verbose > 0 && len(args) > 0 {
		fmt.Fprintf(os.Stderr, "Running: dotnet %s ...\n", args[0])
	}
	err := cmd.Run()
	code := core.ExitCodeFromError(err)
	if code == 127 {
		return 127, fmt.Errorf("gortk: failed to run dotnet: %w", err)
	}
	return code, nil
}

// runDotnetWithBinlog runs a build/test/restore subcommand with an injected
// MSBuild binary log (and TRX logger for test), captures the output, parses the
// structured + text summaries, and emits the compacted form. Faithful port of
// rtk's run_dotnet_with_binlog (timer/tracking dropped).
func runDotnetWithBinlog(subcommand string, args []string, verbose int) (int, error) {
	binlogPath := buildBinlogPath(subcommand)
	shouldExpectBinlog := subcommand != "test" || hasBinlogArg(args)

	trxResultsDir, cleanupTRXResultsDir := resolveTRXResultsDir(subcommand, args)

	cmd := core.ResolvedCommand("dotnet")
	cmd.Env = append(os.Environ(), dotnetCLIUILanguage+"="+dotnetCLIUILanguageValue)
	cmd.Args = append(cmd.Args, subcommand)
	cmd.Args = append(cmd.Args, buildEffectiveDotnetArgs(subcommand, args, binlogPath, trxResultsDir)...)

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: dotnet %s %s\n", subcommand, strings.Join(args, " "))
	}

	commandStartedAt := time.Now()

	defer func() {
		cleanupTempFile(binlogPath)
		if cleanupTRXResultsDir && trxResultsDir != "" {
			cleanupTempDir(trxResultsDir)
		}
		if verbose > 0 {
			fmt.Fprintf(os.Stderr, "Binlog cleaned up: %s\n", binlogPath)
		}
	}()

	// rtk builds `raw = stdout + "\n" + stderr`, filters the combined text,
	// and on failure prepends the trimmed stdout/stderr before the summary.
	// gortk's core runner captures stdout+stderr and hands the closure the
	// combined text; we reproduce rtk's "raw" shape (stdout\nstderr) inside the
	// closure is not possible, so we reconstruct the verbatim prefix from the
	// same combined capture. The filter closure runs regardless of exit code
	// (no SkipFilterOnFailure), matching rtk's with_tee path.
	opts := core.RunOptions{TeeLabel: "dotnet_" + subcommand}
	return core.RunFilteredWithExit(cmd, "dotnet "+subcommand, strings.Join(args, " "),
		func(raw string, exitCode int) string {
			commandSuccess := exitCode == 0
			filtered := buildFilteredSummary(subcommand, raw, binlogPath, shouldExpectBinlog,
				commandSuccess, trxResultsDir, commandStartedAt)

			// rtk prepends the trimmed raw output before the summary on failure
			// so the operator still sees the unfiltered tail. We mirror that by
			// prefixing the trimmed combined capture.
			if !commandSuccess {
				rawTrimmed := strings.TrimSpace(raw)
				if rawTrimmed != "" {
					return rawTrimmed + "\n\n" + filtered
				}
			}
			return filtered
		}, opts)
}

// buildFilteredSummary parses the captured output for the given subcommand and
// returns the compacted summary string. Mirrors the per-subcommand match arms
// in rtk's run_dotnet_with_binlog.
func buildFilteredSummary(subcommand, raw, binlogPath string, shouldExpectBinlog, commandSuccess bool, trxResultsDir string, commandStartedAt time.Time) string {
	switch subcommand {
	case "build":
		binlogSummary := BuildSummary{}
		if shouldExpectBinlog && fileExists(binlogPath) {
			if s, err := ParseBuild(binlogPath); err == nil {
				binlogSummary = normalizeBuildSummary(s, commandSuccess)
			}
		}
		rawSummary := normalizeBuildSummary(ParseBuildFromText(raw), commandSuccess)
		summary := mergeBuildSummaries(binlogSummary, rawSummary)
		return formatBuildOutput(summary)

	case "test":
		parsedSummary := TestSummary{}
		if shouldExpectBinlog && fileExists(binlogPath) {
			if s, err := ParseTest(binlogPath); err == nil {
				parsedSummary = s
			}
		}
		rawSummary := ParseTestFromText(raw)
		mergedSummary := mergeTestSummaries(parsedSummary, rawSummary)
		summary := mergeTestSummaryFromTRX(mergedSummary, trxResultsDir, commandStartedAt)
		summary = normalizeTestSummary(summary, commandSuccess)

		binlogDiagnostics := BuildSummary{}
		if shouldExpectBinlog && fileExists(binlogPath) {
			if s, err := ParseBuild(binlogPath); err == nil {
				binlogDiagnostics = normalizeBuildSummary(s, commandSuccess)
			}
		}
		rawDiagnostics := normalizeBuildSummary(ParseBuildFromText(raw), commandSuccess)
		testBuildSummary := mergeBuildSummaries(binlogDiagnostics, rawDiagnostics)
		return formatTestOutput(summary, testBuildSummary.Errors, testBuildSummary.Warnings)

	case "restore":
		binlogSummary := RestoreSummary{}
		if shouldExpectBinlog && fileExists(binlogPath) {
			if s, err := ParseRestore(binlogPath); err == nil {
				binlogSummary = normalizeRestoreSummary(s, commandSuccess)
			}
		}
		rawSummary := normalizeRestoreSummary(ParseRestoreFromText(raw), commandSuccess)
		summary := mergeRestoreSummaries(binlogSummary, rawSummary)
		rawErrors, rawWarnings := ParseRestoreIssuesFromText(raw)
		return formatRestoreOutput(summary, rawErrors, rawWarnings)

	default:
		return raw
	}
}

// ---------------------------------------------------------------------------
// Temp paths
// ---------------------------------------------------------------------------

func buildBinlogPath(subcommand string) string {
	return filepath.Join(os.TempDir(),
		fmt.Sprintf("gortk_dotnet_%s_%s.binlog", subcommand, uniqueTempSuffix()))
}

func buildTRXResultsDir() string {
	return filepath.Join(os.TempDir(), "gortk_dotnet_testresults_"+uniqueTempSuffix())
}

func buildFormatReportPath() string {
	return filepath.Join(os.TempDir(), "gortk_dotnet_format_"+uniqueTempSuffix()+".json")
}

// uniqueTempSuffix builds a compact, practically-unique suffix from the current
// time, the process id, and a monotonic counter. Mirrors rtk's
// unique_temp_suffix.
func uniqueTempSuffix() string {
	ts := time.Now().UnixMilli()
	pid := os.Getpid()
	seq := atomic.AddUint64(&tempPathCounter, 1) - 1
	return fmt.Sprintf("%x%x%x", ts, pid, seq)
}

func resolveTRXResultsDir(subcommand string, args []string) (string, bool) {
	if subcommand != "test" {
		return "", false
	}
	if userDir, ok := extractResultsDirectoryArg(args); ok {
		return userDir, false
	}
	return buildTRXResultsDir(), true
}

func cleanupTempFile(path string) {
	if fileExists(path) {
		_ = os.Remove(path)
	}
}

func cleanupTempDir(path string) {
	if _, err := os.Stat(path); err == nil {
		_ = os.RemoveAll(path)
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ---------------------------------------------------------------------------
// dotnet format
// ---------------------------------------------------------------------------

func resolveFormatReportPath(args []string) (string, bool) {
	if userReport, ok := extractReportArg(args); ok {
		return userReport, false
	}
	return buildFormatReportPath(), true
}

// runFormat runs `dotnet format`, parses the JSON report it writes, and emits a
// compact summary. Faithful port of rtk's run_format (timer/tracking dropped).
func runFormat(args []string, verbose int) (int, error) {
	reportPath, cleanupReportPath := resolveFormatReportPath(args)

	cmd := core.ResolvedCommand("dotnet")
	cmd.Env = append(os.Environ(), dotnetCLIUILanguage+"="+dotnetCLIUILanguageValue)
	cmd.Args = append(cmd.Args, "format")
	reportArg := ""
	if reportPath != "" {
		reportArg = reportPath
	}
	cmd.Args = append(cmd.Args, buildEffectiveDotnetFormatArgs(args, reportArg)...)

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: dotnet format %s\n", strings.Join(args, " "))
	}

	commandStartedAt := time.Now()
	checkMode := !hasWriteModeOverride(args)

	defer func() {
		if cleanupReportPath && reportPath != "" {
			cleanupTempFile(reportPath)
		}
	}()

	opts := core.RunOptions{TeeLabel: "dotnet_format"}
	return core.RunFiltered(cmd, "dotnet format", strings.Join(args, " "),
		func(raw string) string {
			return formatReportSummaryOrRaw(reportPath, checkMode, raw, commandStartedAt)
		}, opts)
}

// formatReportSummaryOrRaw returns the parsed format summary when a fresh report
// file exists, otherwise the raw output. Mirrors rtk's
// format_report_summary_or_raw.
func formatReportSummaryOrRaw(reportPath string, checkMode bool, raw string, commandStartedAt time.Time) string {
	if reportPath == "" {
		return raw
	}
	if !isFreshReport(reportPath, commandStartedAt) {
		return raw
	}
	summary, err := ParseFormatReport(reportPath)
	if err != nil {
		return raw
	}
	return formatDotnetFormatOutput(summary, checkMode)
}

// isFreshReport reports whether path was modified at or after commandStartedAt
// (i.e. this run wrote it, not a stale file). Mirrors rtk's is_fresh_report.
func isFreshReport(path string, commandStartedAt time.Time) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.ModTime().Before(commandStartedAt)
}

// formatDotnetFormatOutput renders the FormatSummary for stdout. Faithful port
// of rtk's format_dotnet_format_output (force-tee overflow hint dropped).
func formatDotnetFormatOutput(summary FormatSummary, checkMode bool) string {
	changedCount := len(summary.FilesWithChanges)

	if changedCount == 0 {
		return fmt.Sprintf("ok dotnet format: %d files formatted correctly", summary.TotalFiles)
	}

	if !checkMode {
		return fmt.Sprintf("ok dotnet format: formatted %d files (%d already formatted)",
			changedCount, summary.FilesUnchanged)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Format: %d files need formatting", changedCount)

	limit := changedCount
	if limit > maxFormatFiles {
		limit = maxFormatFiles
	}
	for index := 0; index < limit; index++ {
		file := summary.FilesWithChanges[index]
		firstChange := file.Changes[0]
		rule := firstChange.FormatDescription
		if firstChange.DiagnosticID != "" {
			rule = firstChange.DiagnosticID
		}
		fmt.Fprintf(&b, "\n%d. %s (line %d, col %d, %s)",
			index+1, file.Path, firstChange.LineNumber, firstChange.CharNumber, rule)
	}

	if changedCount > maxFormatFiles {
		fmt.Fprintf(&b, "\n… +%d more files", changedCount-maxFormatFiles)
	}

	fmt.Fprintf(&b, "\n\nok %d files already formatted\nRun `dotnet format` to apply fixes",
		summary.FilesUnchanged)
	return b.String()
}

func buildEffectiveDotnetFormatArgs(args []string, reportPath string) []string {
	var effective []string
	for _, arg := range args {
		if strings.EqualFold(arg, "--write") {
			continue
		}
		effective = append(effective, arg)
	}
	forceWriteMode := hasWriteModeOverride(args)

	if !forceWriteMode && !hasVerifyNoChangesArg(args) {
		effective = append(effective, "--verify-no-changes")
	}

	if !hasReportArg(args) && reportPath != "" {
		effective = append(effective, "--report", reportPath)
	}

	return effective
}

// ---------------------------------------------------------------------------
// TRX merge
// ---------------------------------------------------------------------------

// mergeTestSummaryFromTRX overlays per-test results recovered from .trx files
// onto the binlog/text summary. Faithful port of rtk's
// merge_test_summary_from_trx. Unlike rtk, gortk does not pass a separate
// fallback-trx path (./TestResults is consulted internally as the fallback).
func mergeTestSummaryFromTRX(summary TestSummary, trxResultsDir string, commandStartedAt time.Time) TestSummary {
	var trxSummary *TestSummary

	if trxResultsDir != "" && fileExists(trxResultsDir) {
		if s, ok := parseTRXFilesInDirSince(trxResultsDir, commandStartedAt, true); ok {
			trxSummary = &s
		} else if s, ok := parseTRXFilesInDir(trxResultsDir); ok {
			trxSummary = &s
		}
	}

	if trxSummary == nil {
		if path, ok := findRecentTRXInTestResults(); ok {
			if s, ok := parseTRXFileSince(path, commandStartedAt); ok {
				trxSummary = &s
			}
		}
	}

	if trxSummary == nil {
		return summary
	}
	t := *trxSummary

	if t.Total > 0 && (summary.Total == 0 || t.Total >= summary.Total) {
		summary.Passed = t.Passed
		summary.Failed = t.Failed
		summary.Skipped = t.Skipped
		summary.Total = t.Total
	}

	if len(summary.FailedTests) == 0 && len(t.FailedTests) > 0 {
		summary.FailedTests = t.FailedTests
	}

	if t.HasDuration {
		summary.DurationText = t.DurationText
		summary.HasDuration = true
	}

	if t.ProjectCount > summary.ProjectCount {
		summary.ProjectCount = t.ProjectCount
	}

	return summary
}

// ---------------------------------------------------------------------------
// dotnet arg injection
// ---------------------------------------------------------------------------

// testRunnerMode is how the targeted test project(s) run tests. Mirrors rtk's
// TestRunnerMode.
type testRunnerMode int

const (
	runnerClassic testRunnerMode = iota
	runnerMtpNative
	runnerMtpVsTestBridge
)

// mtpProjectKind is which MTP-related property an MSBuild file declares. Mirrors
// rtk's MtpProjectKind.
type mtpProjectKind int

const (
	mtpNone mtpProjectKind = iota
	mtpVsTestBridge
)

// buildEffectiveDotnetArgs assembles the full dotnet argv for a subcommand:
// binlog injection, verbosity, --nologo, TRX/MTP runner wiring, then the user
// args. Faithful port of rtk's build_effective_dotnet_args.
func buildEffectiveDotnetArgs(subcommand string, args []string, binlogPath string, trxResultsDir string) []string {
	var effective []string

	if subcommand != "test" && !hasBinlogArg(args) {
		effective = append(effective, "-bl:"+binlogPath)
	}

	if subcommand != "test" && !hasVerbosityArg(args) {
		effective = append(effective, "-v:minimal")
	}

	runnerMode := runnerClassic
	if subcommand == "test" {
		runnerMode = detectTestRunnerMode(args)
	}

	// --nologo: skip for MtpNative — args pass directly to the MTP runtime
	// which does not understand MSBuild/VSTest flags.
	if runnerMode != runnerMtpNative && !hasNologoArg(args) {
		effective = append(effective, "-nologo")
	}

	if subcommand == "test" {
		switch runnerMode {
		case runnerClassic:
			if !hasTRXLoggerArg(args) {
				effective = append(effective, "--logger", "trx")
			}
			if !hasResultsDirectoryArg(args) && trxResultsDir != "" {
				effective = append(effective, "--results-directory", trxResultsDir)
			}
			effective = append(effective, args...)
		case runnerMtpNative:
			if !hasReportTRXArg(args) {
				effective = append(effective, "--report-trx")
			}
			effective = append(effective, args...)
		case runnerMtpVsTestBridge:
			if !hasReportTRXArg(args) {
				effective = append(effective, injectReportTRXIntoArgs(args)...)
			} else {
				effective = append(effective, args...)
			}
		}
	} else {
		effective = append(effective, args...)
	}

	return effective
}

func hasBinlogArg(args []string) bool {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if strings.HasPrefix(lower, "-bl") || strings.HasPrefix(lower, "/bl") {
			return true
		}
	}
	return false
}

func hasVerbosityArg(args []string) bool {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if strings.HasPrefix(lower, "-v:") ||
			strings.HasPrefix(lower, "/v:") ||
			lower == "-v" ||
			lower == "/v" ||
			lower == "--verbosity" ||
			strings.HasPrefix(lower, "--verbosity=") {
			return true
		}
	}
	return false
}

func hasNologoArg(args []string) bool {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if lower == "-nologo" || lower == "/nologo" {
			return true
		}
	}
	return false
}

func hasTRXLoggerArg(args []string) bool {
	for i := 0; i < len(args); i++ {
		lower := strings.ToLower(args[i])
		if lower == "--logger" {
			if i+1 < len(args) {
				next := strings.ToLower(args[i+1])
				if next == "trx" || strings.HasPrefix(next, "trx;") {
					return true
				}
			}
			continue
		}
		for _, prefix := range []string{"--logger:", "--logger="} {
			if value, ok := strings.CutPrefix(lower, prefix); ok {
				if value == "trx" || strings.HasPrefix(value, "trx;") {
					return true
				}
			}
		}
	}
	return false
}

func hasResultsDirectoryArg(args []string) bool {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if lower == "--results-directory" || strings.HasPrefix(lower, "--results-directory=") {
			return true
		}
	}
	return false
}

func hasReportArg(args []string) bool {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if lower == "--report" || strings.HasPrefix(lower, "--report=") {
			return true
		}
	}
	return false
}

func hasReportTRXArg(args []string) bool {
	for _, arg := range args {
		if strings.EqualFold(arg, "--report-trx") {
			return true
		}
	}
	return false
}

// injectReportTRXIntoArgs injects `--report-trx` after the `--` separator. If
// none exists, appends `-- --report-trx`. Mirrors rtk's
// inject_report_trx_into_args.
func injectReportTRXIntoArgs(args []string) []string {
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep >= 0 {
		result := make([]string, 0, len(args)+1)
		result = append(result, args[:sep+1]...)
		result = append(result, "--report-trx")
		result = append(result, args[sep+1:]...)
		return result
	}
	result := make([]string, 0, len(args)+2)
	result = append(result, args...)
	result = append(result, "--", "--report-trx")
	return result
}

func extractReportArg(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		if strings.EqualFold(args[i], "--report") {
			if i+1 < len(args) {
				return args[i+1], true
			}
			continue
		}
		if key, value, ok := strings.Cut(args[i], "="); ok {
			if strings.EqualFold(key, "--report") {
				return value, true
			}
		}
	}
	return "", false
}

func extractResultsDirectoryArg(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		if strings.EqualFold(args[i], "--results-directory") {
			if i+1 < len(args) {
				return args[i+1], true
			}
			continue
		}
		if key, value, ok := strings.Cut(args[i], "="); ok {
			if strings.EqualFold(key, "--results-directory") {
				return value, true
			}
		}
	}
	return "", false
}

func hasVerifyNoChangesArg(args []string) bool {
	for _, arg := range args {
		lower := strings.ToLower(arg)
		if lower == "--verify-no-changes" || strings.HasPrefix(lower, "--verify-no-changes=") {
			return true
		}
	}
	return false
}

func hasWriteModeOverride(args []string) bool {
	for _, arg := range args {
		if strings.EqualFold(arg, "--write") {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// MTP runner detection
// ---------------------------------------------------------------------------

// scanMtpKindInFile scans an MSBuild file for MTP-related properties and returns
// which kind it is. Faithful port of rtk's scan_mtp_kind_in_file. rtk used a
// streaming XML reader; gortk uses a simple element/text scan over the same
// property names (case-insensitive).
func scanMtpKindInFile(path string) mtpProjectKind {
	data, err := os.ReadFile(path)
	if err != nil {
		return mtpNone
	}
	content := string(data)
	lower := strings.ToLower(content)

	props := []string{
		"usemicrosofttestingplatformrunner",
		"usetestingplatformrunner",
		"testingplatformdotnettestsupport",
	}
	for _, p := range props {
		open := "<" + p + ">"
		idx := strings.Index(lower, open)
		for idx >= 0 {
			rest := content[idx+len(open):]
			closeTag := strings.Index(strings.ToLower(rest), "</"+p+">")
			if closeTag >= 0 {
				if strings.EqualFold(strings.TrimSpace(rest[:closeTag]), "true") {
					return mtpVsTestBridge
				}
			}
			next := strings.Index(lower[idx+len(open):], open)
			if next < 0 {
				break
			}
			idx = idx + len(open) + next
		}
	}
	return mtpNone
}

// parseGlobalJSONMtpMode reports whether a global.json enables native MTP mode
// (`"test": { "runner": "Microsoft.Testing.Platform" }`). Faithful port of
// rtk's parse_global_json_mtp_mode.
func parseGlobalJSONMtpMode(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return false
	}
	testRaw, ok := obj["test"]
	if !ok {
		return false
	}
	var testObj map[string]json.RawMessage
	if err := json.Unmarshal(testRaw, &testObj); err != nil {
		return false
	}
	runnerRaw, ok := testObj["runner"]
	if !ok {
		return false
	}
	var runner string
	if err := json.Unmarshal(runnerRaw, &runner); err != nil {
		return false
	}
	return strings.EqualFold(runner, "Microsoft.Testing.Platform")
}

// isGlobalJSONMtpMode walks up from the current directory to the first
// global.json and reports whether it enables native MTP mode. Mirrors rtk's
// is_global_json_mtp_mode.
func isGlobalJSONMtpMode() bool {
	dir, err := os.Getwd()
	if err != nil {
		return false
	}
	for {
		path := filepath.Join(dir, "global.json")
		if fileExists(path) {
			return parseGlobalJSONMtpMode(path)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return false
}

// detectTestRunnerMode determines which test runner mode the targeted
// project(s) use. Faithful port of rtk's detect_test_runner_mode.
func detectTestRunnerMode(args []string) testRunnerMode {
	if isGlobalJSONMtpMode() {
		return runnerMtpNative
	}

	projectExtensions := []string{"csproj", "fsproj", "vbproj"}

	var explicitProjects []string
	for _, a := range args {
		lower := strings.ToLower(a)
		for _, ext := range projectExtensions {
			if strings.HasSuffix(lower, "."+ext) {
				explicitProjects = append(explicitProjects, a)
				break
			}
		}
	}

	found := mtpNone

	if len(explicitProjects) > 0 {
		for _, p := range explicitProjects {
			if scanMtpKindInFile(p) == mtpVsTestBridge {
				found = mtpVsTestBridge
			}
		}
	} else {
		if entries, err := os.ReadDir("."); err == nil {
			for _, entry := range entries {
				nameLower := strings.ToLower(entry.Name())
				isProj := false
				for _, ext := range projectExtensions {
					if strings.HasSuffix(nameLower, "."+ext) {
						isProj = true
						break
					}
				}
				if isProj && scanMtpKindInFile(entry.Name()) == mtpVsTestBridge {
					found = mtpVsTestBridge
				}
			}
		}
	}

	if found == mtpVsTestBridge {
		return runnerMtpVsTestBridge
	}

	// Walk up from current directory looking for Directory.Build.props.
	if dir, err := os.Getwd(); err == nil {
		for {
			props := filepath.Join(dir, "Directory.Build.props")
			if fileExists(props) {
				if scanMtpKindInFile(props) == mtpVsTestBridge {
					return runnerMtpVsTestBridge
				}
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	return runnerClassic
}

// ---------------------------------------------------------------------------
// Summary normalize / merge
// ---------------------------------------------------------------------------

func normalizeBuildSummary(summary BuildSummary, commandSuccess bool) BuildSummary {
	if commandSuccess {
		summary.Succeeded = true
		if summary.ProjectCount == 0 {
			summary.ProjectCount = 1
		}
	}
	return summary
}

func mergeBuildSummaries(binlogSummary, rawSummary BuildSummary) BuildSummary {
	if len(binlogSummary.Errors) == 0 {
		binlogSummary.Errors = rawSummary.Errors
	}
	if len(binlogSummary.Warnings) == 0 {
		binlogSummary.Warnings = rawSummary.Warnings
	}
	if binlogSummary.ProjectCount == 0 {
		binlogSummary.ProjectCount = rawSummary.ProjectCount
	}
	if !binlogSummary.HasDuration {
		binlogSummary.DurationText = rawSummary.DurationText
		binlogSummary.HasDuration = rawSummary.HasDuration
	}
	return binlogSummary
}

func normalizeTestSummary(summary TestSummary, commandSuccess bool) TestSummary {
	if !commandSuccess && summary.Failed == 0 && len(summary.FailedTests) == 0 {
		summary.Failed = 1
		if summary.Total == 0 {
			summary.Total = 1
		}
	}
	if commandSuccess && summary.Total == 0 && summary.Passed == 0 {
		summary.ProjectCount = maxInt(summary.ProjectCount, 1)
	}
	return summary
}

func mergeTestSummaries(binlogSummary, rawSummary TestSummary) TestSummary {
	if binlogSummary.Total == 0 && rawSummary.Total > 0 {
		binlogSummary.Passed = rawSummary.Passed
		binlogSummary.Failed = rawSummary.Failed
		binlogSummary.Skipped = rawSummary.Skipped
		binlogSummary.Total = rawSummary.Total
	}
	if len(rawSummary.FailedTests) > 0 {
		binlogSummary.FailedTests = rawSummary.FailedTests
	}
	if binlogSummary.ProjectCount == 0 {
		binlogSummary.ProjectCount = rawSummary.ProjectCount
	}
	if !binlogSummary.HasDuration {
		binlogSummary.DurationText = rawSummary.DurationText
		binlogSummary.HasDuration = rawSummary.HasDuration
	}
	return binlogSummary
}

func normalizeRestoreSummary(summary RestoreSummary, commandSuccess bool) RestoreSummary {
	if !commandSuccess && summary.Errors == 0 {
		summary.Errors = 1
	}
	return summary
}

func mergeRestoreSummaries(binlogSummary, rawSummary RestoreSummary) RestoreSummary {
	if binlogSummary.RestoredProjects == 0 {
		binlogSummary.RestoredProjects = rawSummary.RestoredProjects
	}
	if binlogSummary.Errors == 0 {
		binlogSummary.Errors = rawSummary.Errors
	}
	if binlogSummary.Warnings == 0 {
		binlogSummary.Warnings = rawSummary.Warnings
	}
	if !binlogSummary.HasDuration {
		binlogSummary.DurationText = rawSummary.DurationText
		binlogSummary.HasDuration = rawSummary.HasDuration
	}
	return binlogSummary
}

// ---------------------------------------------------------------------------
// Output formatters
// ---------------------------------------------------------------------------

func formatIssue(issue BinlogIssue, kind string) string {
	if issue.File == "" {
		return fmt.Sprintf("  %s %s", kind, truncate(issue.Message, 180))
	}
	if issue.Code == "" {
		return fmt.Sprintf("  %s(%d,%d) %s: %s",
			issue.File, issue.Line, issue.Column, kind, truncate(issue.Message, 180))
	}
	return fmt.Sprintf("  %s(%d,%d) %s %s: %s",
		issue.File, issue.Line, issue.Column, kind, issue.Code, truncate(issue.Message, 180))
}

func durationOr(summary interface{ dur() (string, bool) }) string {
	if d, ok := summary.dur(); ok {
		return d
	}
	return "unknown"
}

// dur accessors let durationOr stay generic over the three summary types.
func (s BuildSummary) dur() (string, bool)   { return s.DurationText, s.HasDuration }
func (s TestSummary) dur() (string, bool)    { return s.DurationText, s.HasDuration }
func (s RestoreSummary) dur() (string, bool) { return s.DurationText, s.HasDuration }

// formatBuildOutput renders a BuildSummary. The status line is emitted last so
// tail-readers get a definitive verdict; warnings precede errors so errors
// survive `| tail -N` immediately above the verdict. Faithful port of rtk's
// format_build_output (overflow tee hint dropped).
func formatBuildOutput(summary BuildSummary) string {
	statusIcon := "fail"
	if summary.Succeeded {
		statusIcon = "ok"
	}
	duration := durationOr(summary)

	var errors strings.Builder
	if len(summary.Errors) > 0 {
		errors.WriteString("Errors:\n")
		for _, issue := range takeIssues(summary.Errors, maxBuildErrors) {
			errors.WriteString(formatIssue(issue, "error"))
			errors.WriteByte('\n')
		}
		if len(summary.Errors) > maxBuildErrors {
			fmt.Fprintf(&errors, "  … +%d more errors\n", len(summary.Errors)-maxBuildErrors)
		}
	}

	var warnings strings.Builder
	if len(summary.Warnings) > 0 {
		warnings.WriteString("Warnings:\n")
		for _, issue := range takeIssues(summary.Warnings, maxBuildWarnings) {
			warnings.WriteString(formatIssue(issue, "warning"))
			warnings.WriteByte('\n')
		}
		if len(summary.Warnings) > maxBuildWarnings {
			fmt.Fprintf(&warnings, "  … +%d more warnings\n", len(summary.Warnings)-maxBuildWarnings)
		}
	}

	verdict := fmt.Sprintf("%s dotnet build: %d projects, %d errors, %d warnings (%s)",
		statusIcon, summary.ProjectCount, len(summary.Errors), len(summary.Warnings), duration)

	return joinNonEmpty([]string{warnings.String(), errors.String(), verdict})
}

// formatTestOutput renders a TestSummary plus build diagnostics. Status line
// last (tail consumers), warnings before errors. Faithful port of rtk's
// format_test_output (overflow tee hints dropped).
func formatTestOutput(summary TestSummary, errors, warnings []BinlogIssue) string {
	hasFailures := summary.Failed > 0 || len(summary.FailedTests) > 0
	statusIcon := "ok"
	if hasFailures {
		statusIcon = "fail"
	}
	duration := durationOr(summary)
	warningCount := len(warnings)
	countsUnavailable := summary.Passed == 0 && summary.Failed == 0 &&
		summary.Skipped == 0 && summary.Total == 0 && len(summary.FailedTests) == 0

	var header string
	switch {
	case countsUnavailable:
		header = fmt.Sprintf("%s dotnet test: completed (binlog-only mode, counts unavailable, %d warnings) (%s)",
			statusIcon, warningCount, duration)
	case hasFailures:
		header = fmt.Sprintf("%s dotnet test: %d passed, %d failed, %d skipped, %d warnings in %d projects (%s)",
			statusIcon, summary.Passed, summary.Failed, summary.Skipped, warningCount, summary.ProjectCount, duration)
	default:
		header = fmt.Sprintf("%s dotnet test: %d tests passed, %d warnings in %d projects (%s)",
			statusIcon, summary.Passed, warningCount, summary.ProjectCount, duration)
	}

	var failedTestsSection strings.Builder
	if hasFailures && len(summary.FailedTests) > 0 {
		failedTestsSection.WriteString("Failed Tests:\n")
		for _, failed := range takeFailed(summary.FailedTests, maxDotnetFailures) {
			fmt.Fprintf(&failedTestsSection, "  %s\n", failed.Name)
			for _, detail := range failed.Details {
				fmt.Fprintf(&failedTestsSection, "    %s\n", truncate(detail, 320))
			}
			failedTestsSection.WriteByte('\n')
		}
		if len(summary.FailedTests) > maxDotnetFailures {
			fmt.Fprintf(&failedTestsSection, "… +%d more failed tests\n",
				len(summary.FailedTests)-maxDotnetFailures)
		}
	}

	var errorsSection strings.Builder
	if len(errors) > 0 {
		errorsSection.WriteString("Errors:\n")
		for _, issue := range takeIssues(errors, maxTestErrors) {
			errorsSection.WriteString(formatIssue(issue, "error"))
			errorsSection.WriteByte('\n')
		}
		if len(errors) > maxTestErrors {
			fmt.Fprintf(&errorsSection, "  … +%d more errors\n", len(errors)-maxTestErrors)
		}
	}

	var warningsSection strings.Builder
	if len(warnings) > 0 {
		warningsSection.WriteString("Warnings:\n")
		for _, issue := range takeIssues(warnings, maxTestWarnings) {
			warningsSection.WriteString(formatIssue(issue, "warning"))
			warningsSection.WriteByte('\n')
		}
		if len(warnings) > maxTestWarnings {
			fmt.Fprintf(&warningsSection, "  … +%d more warnings\n", len(warnings)-maxTestWarnings)
		}
	}

	return joinNonEmpty([]string{
		failedTestsSection.String(),
		warningsSection.String(),
		errorsSection.String(),
		header,
	})
}

// formatRestoreOutput renders a RestoreSummary. Status line last, warnings
// before errors. Faithful port of rtk's format_restore_output (overflow tee
// hints dropped).
func formatRestoreOutput(summary RestoreSummary, errors, warnings []BinlogIssue) string {
	hasErrors := summary.Errors > 0
	statusIcon := "ok"
	if hasErrors {
		statusIcon = "fail"
	}
	duration := durationOr(summary)

	var errorsSection strings.Builder
	if len(errors) > 0 {
		errorsSection.WriteString("Errors:\n")
		for _, issue := range takeIssues(errors, maxRestoreErrors) {
			errorsSection.WriteString(formatIssue(issue, "error"))
			errorsSection.WriteByte('\n')
		}
		if len(errors) > maxRestoreErrors {
			fmt.Fprintf(&errorsSection, "  … +%d more errors\n", len(errors)-maxRestoreErrors)
		}
	}

	var warningsSection strings.Builder
	if len(warnings) > 0 {
		warningsSection.WriteString("Warnings:\n")
		for _, issue := range takeIssues(warnings, maxRestoreWarnings) {
			warningsSection.WriteString(formatIssue(issue, "warning"))
			warningsSection.WriteByte('\n')
		}
		if len(warnings) > maxRestoreWarnings {
			fmt.Fprintf(&warningsSection, "  … +%d more warnings\n", len(warnings)-maxRestoreWarnings)
		}
	}

	verdict := fmt.Sprintf("%s dotnet restore: %d projects, %d errors, %d warnings (%s)",
		statusIcon, summary.RestoredProjects, summary.Errors, summary.Warnings, duration)

	return joinNonEmpty([]string{warningsSection.String(), errorsSection.String(), verdict})
}

// ---------------------------------------------------------------------------
// Local helpers
// ---------------------------------------------------------------------------

func takeIssues(s []BinlogIssue, n int) []BinlogIssue {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func takeFailed(s []FailedTest, n int) []FailedTest {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// joinNonEmpty joins the non-empty segments with "\n", mirroring rtk's
// `[a, b, c].into_iter().filter(|s| !s.is_empty()).collect().join("\n")`.
func joinNonEmpty(parts []string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n")
}
