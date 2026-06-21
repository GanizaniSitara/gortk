package dotnet

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests are ported from rtk's #[cfg(test)] modules in
// src/cmds/dotnet/dotnet_cmd.rs and src/cmds/dotnet/dotnet_format_report.rs.
// The "rtk"/"Rtk" literals in fixture data are kept verbatim where a ported
// test pins them (they are sample build/test output strings, not user-facing
// gortk strings). Tee/overflow-hint assertions are absent in the source.

// ── arg-injection helpers ────────────────────────────────────────────────────

func buildDotnetArgsForTest(subcommand string, args []string, withTRX bool) []string {
	binlogPath := "/tmp/test.binlog"
	trxResultsDir := ""
	if withTRX {
		trxResultsDir = "/tmp/test results"
	}
	return buildEffectiveDotnetArgs(subcommand, args, binlogPath, trxResultsDir)
}

func trxWithCounts(total, passed, failed int) string {
	t, p, f := strconv.Itoa(total), strconv.Itoa(passed), strconv.Itoa(failed)
	return `<?xml version="1.0" encoding="utf-8"?>
<TestRun xmlns="http://microsoft.com/schemas/VisualStudio/TeamTest/2010">
  <ResultSummary outcome="Completed">
    <Counters total="` + t + `" executed="` + t + `" passed="` + p + `" failed="` + f + `" error="0" />
  </ResultSummary>
</TestRun>`
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func fixture(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("testdata", name)
}

// ── has_binlog_arg ───────────────────────────────────────────────────────────

func TestHasBinlogArgDetectsVariants(t *testing.T) {
	if !hasBinlogArg([]string{"-bl:my.binlog"}) {
		t.Error("expected -bl:my.binlog detected")
	}
	if !hasBinlogArg([]string{"/bl"}) {
		t.Error("expected /bl detected")
	}
	if hasBinlogArg([]string{"--configuration", "Release"}) {
		t.Error("did not expect --configuration to count")
	}
}

// ── format_build_output ──────────────────────────────────────────────────────

func TestFormatBuildOutputIncludesErrorsAndWarnings(t *testing.T) {
	summary := BuildSummary{
		Succeeded:    false,
		ProjectCount: 2,
		Errors: []BinlogIssue{{
			Code: "CS0103", File: "src/Program.cs", Line: 42, Column: 15,
			Message: "The name 'foo' does not exist",
		}},
		Warnings: []BinlogIssue{{
			Code: "CS0219", File: "src/Program.cs", Line: 25, Column: 10,
			Message: "Variable 'x' is assigned but never used",
		}},
		DurationText: "00:00:04.20", HasDuration: true,
	}
	output := formatBuildOutput(summary)
	if !contains(output, "dotnet build: 2 projects, 1 errors, 1 warnings") {
		t.Errorf("missing verdict: %s", output)
	}
	if !contains(output, "error CS0103") {
		t.Errorf("missing error CS0103: %s", output)
	}
	if !contains(output, "warning CS0219") {
		t.Errorf("missing warning CS0219: %s", output)
	}
}

func TestFormatTestOutputShowsFailures(t *testing.T) {
	summary := TestSummary{
		Passed: 10, Failed: 1, Skipped: 0, Total: 11, ProjectCount: 1,
		FailedTests:  []FailedTest{{Name: "MyTests.ShouldFail", Details: []string{"Assert.Equal failure"}}},
		DurationText: "1 s", HasDuration: true,
	}
	output := formatTestOutput(summary, nil, nil)
	if !contains(output, "10 passed, 1 failed") {
		t.Errorf("missing counts: %s", output)
	}
	if !contains(output, "MyTests.ShouldFail") {
		t.Errorf("missing failing test name: %s", output)
	}
}

func TestFormatTestOutputSurfacesWarnings(t *testing.T) {
	summary := TestSummary{
		Passed: 940, Failed: 0, Skipped: 7, Total: 947, ProjectCount: 1,
		DurationText: "1 s", HasDuration: true,
	}
	warnings := []BinlogIssue{{
		File: "/sdk/Microsoft.TestPlatform.targets", Line: 48, Column: 5, Message: "Violators:",
	}}
	output := formatTestOutput(summary, nil, warnings)
	if !contains(output, "940 tests passed, 1 warnings") {
		t.Errorf("missing passed/warnings line: %s", output)
	}
	if !contains(output, "Warnings:") {
		t.Errorf("missing Warnings section: %s", output)
	}
	if !contains(output, "Microsoft.TestPlatform.targets") {
		t.Errorf("missing warning file: %s", output)
	}
}

func TestFormatTestOutputSurfacesErrors(t *testing.T) {
	summary := TestSummary{
		Passed: 939, Failed: 1, Skipped: 7, Total: 947, ProjectCount: 1,
		DurationText: "1 s", HasDuration: true,
	}
	errors := []BinlogIssue{{
		Code: "TESTERROR", File: "/repo/MessageMapperTests.cs", Line: 135, Column: 0,
		Message: "CreateInstance_should_initialize_interface_message_type_on_demand",
	}}
	output := formatTestOutput(summary, errors, nil)
	if !contains(output, "Errors:") {
		t.Errorf("missing Errors section: %s", output)
	}
	if !contains(output, "error TESTERROR") {
		t.Errorf("missing error code: %s", output)
	}
	if !contains(output, "CreateInstance_should_initialize_interface_message_type_on_demand") {
		t.Errorf("missing error message: %s", output)
	}
}

func TestFormatRestoreOutputSuccess(t *testing.T) {
	summary := RestoreSummary{RestoredProjects: 3, Warnings: 1, Errors: 0, DurationText: "00:00:01.10", HasDuration: true}
	output := formatRestoreOutput(summary, nil, nil)
	if !strings.HasPrefix(output, "ok dotnet restore") {
		t.Errorf("expected ok prefix: %s", output)
	}
	if !contains(output, "3 projects") {
		t.Errorf("missing 3 projects: %s", output)
	}
	if !contains(output, "1 warnings") {
		t.Errorf("missing 1 warnings: %s", output)
	}
}

func TestFormatRestoreOutputFailure(t *testing.T) {
	summary := RestoreSummary{RestoredProjects: 2, Warnings: 0, Errors: 1, DurationText: "00:00:01.00", HasDuration: true}
	output := formatRestoreOutput(summary, nil, nil)
	if !strings.HasPrefix(output, "fail dotnet restore") {
		t.Errorf("expected fail prefix: %s", output)
	}
	if !contains(output, "1 errors") {
		t.Errorf("missing 1 errors: %s", output)
	}
}

func TestFormatRestoreOutputIncludesErrorDetails(t *testing.T) {
	summary := RestoreSummary{RestoredProjects: 2, Warnings: 0, Errors: 1, DurationText: "00:00:01.00", HasDuration: true}
	issues := []BinlogIssue{{
		Code: "NU1101", File: "/repo/src/App/App.csproj", Message: "Unable to find package Foo.Bar",
	}}
	output := formatRestoreOutput(summary, issues, nil)
	if !contains(output, "Errors:") || !contains(output, "error NU1101") ||
		!contains(output, "Unable to find package Foo.Bar") {
		t.Errorf("missing restore error details: %s", output)
	}
}

func TestFormatTestOutputHandlesBinlogOnlyWithoutCounts(t *testing.T) {
	summary := TestSummary{DurationText: "unknown", HasDuration: true}
	output := formatTestOutput(summary, nil, nil)
	if !contains(output, "counts unavailable") {
		t.Errorf("missing counts unavailable: %s", output)
	}
}

// ── status-line-is-last regression tests (issue #1574) ───────────────────────

func TestFormatBuildOutputStatusLineIsLast(t *testing.T) {
	summary := BuildSummary{
		Succeeded: true, ProjectCount: 1,
		Warnings: []BinlogIssue{{
			Code: "CS0219", File: "src/Program.cs", Line: 25, Column: 10,
			Message: "Variable assigned but never used",
		}},
		DurationText: "00:00:01.23", HasDuration: true,
	}
	output := formatBuildOutput(summary)
	lines := strings.Split(output, "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "ok dotnet build:") {
		t.Errorf("status line must be last, got: %q", last)
	}
}

func TestFormatTestOutputStatusLineIsLast(t *testing.T) {
	summary := TestSummary{
		Passed: 940, Failed: 0, Skipped: 7, Total: 947, ProjectCount: 1,
		DurationText: "1 s", HasDuration: true,
	}
	warnings := []BinlogIssue{{
		File: "/sdk/Microsoft.TestPlatform.targets", Line: 48, Column: 5, Message: "Violators:",
	}}
	output := formatTestOutput(summary, nil, warnings)
	lines := strings.Split(output, "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "ok dotnet test:") {
		t.Errorf("status line must be last, got: %q", last)
	}
}

func TestFormatRestoreOutputStatusLineIsLast(t *testing.T) {
	summary := RestoreSummary{RestoredProjects: 1, Warnings: 0, Errors: 1, DurationText: "00:00:01.00", HasDuration: true}
	issues := []BinlogIssue{{Code: "NU1101", File: "/repo/src/App/App.csproj", Message: "Unable to find package Foo.Bar"}}
	output := formatRestoreOutput(summary, issues, nil)
	lines := strings.Split(output, "\n")
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, "fail dotnet restore:") {
		t.Errorf("status line must be last, got: %q", last)
	}
}

// ── normalize / merge ────────────────────────────────────────────────────────

func TestNormalizeBuildSummarySetsSuccessFloor(t *testing.T) {
	normalized := normalizeBuildSummary(BuildSummary{}, true)
	if !normalized.Succeeded {
		t.Error("expected succeeded")
	}
	if normalized.ProjectCount != 1 {
		t.Errorf("expected project count 1, got %d", normalized.ProjectCount)
	}
}

func TestMergeBuildSummariesKeepsStructuredIssuesWhenPresent(t *testing.T) {
	binlogSummary := BuildSummary{
		ProjectCount: 11,
		Errors:       []BinlogIssue{{File: "IDE0055", Message: "Fix formatting"}},
		DurationText: "00:00:03.54", HasDuration: true,
	}
	rawSummary := BuildSummary{
		ProjectCount: 2,
		Errors: []BinlogIssue{
			{Code: "IDE0055", File: "/repo/src/Behavior.cs", Line: 13, Column: 32, Message: "Fix formatting"},
			{Code: "IDE0055", File: "/repo/src/Behavior.cs", Line: 13, Column: 41, Message: "Fix formatting"},
		},
		DurationText: "00:00:03.54", HasDuration: true,
	}
	merged := mergeBuildSummaries(binlogSummary, rawSummary)
	if merged.ProjectCount != 11 {
		t.Errorf("expected project count 11, got %d", merged.ProjectCount)
	}
	if len(merged.Errors) != 1 {
		t.Fatalf("expected 1 error, got %d", len(merged.Errors))
	}
	if merged.Errors[0].File != "IDE0055" || merged.Errors[0].Line != 0 || merged.Errors[0].Column != 0 {
		t.Errorf("expected structured binlog error kept, got %+v", merged.Errors[0])
	}
}

func TestMergeBuildSummariesKeepsBinlogWhenContextIsGood(t *testing.T) {
	binlogSummary := BuildSummary{
		ProjectCount: 2,
		Errors:       []BinlogIssue{{Code: "CS0103", File: "src/Program.cs", Line: 42, Column: 15, Message: "The name 'foo' does not exist"}},
		DurationText: "00:00:01.00", HasDuration: true,
	}
	rawSummary := BuildSummary{
		ProjectCount: 2,
		Errors:       []BinlogIssue{{Code: "CS0103", Message: "Build error #1 (details omitted)"}},
	}
	merged := mergeBuildSummaries(binlogSummary, rawSummary)
	if len(merged.Errors) != 1 || merged.Errors[0].File != "src/Program.cs" {
		t.Errorf("expected binlog errors kept, got %+v", merged.Errors)
	}
}

func TestNormalizeTestSummarySetsFailureFloor(t *testing.T) {
	normalized := normalizeTestSummary(TestSummary{}, false)
	if normalized.Failed != 1 || normalized.Total != 1 {
		t.Errorf("expected failed=1 total=1, got failed=%d total=%d", normalized.Failed, normalized.Total)
	}
}

func TestMergeTestSummariesKeepsStructuredCountsAndFillsFailedTests(t *testing.T) {
	binlogSummary := TestSummary{Passed: 939, Failed: 1, Skipped: 8, Total: 948, ProjectCount: 1, DurationText: "unknown", HasDuration: true}
	rawSummary := TestSummary{
		Passed: 939, Failed: 1, Skipped: 7, Total: 947,
		FailedTests:  []FailedTest{{Name: "MessageMapperTests.CreateInstance_should_initialize_interface_message_type_on_demand", Details: []string{"Assert.That(messageInstance, Is.Null)"}}},
		DurationText: "1 s", HasDuration: true,
	}
	merged := mergeTestSummaries(binlogSummary, rawSummary)
	if merged.Skipped != 8 || merged.Total != 948 {
		t.Errorf("expected structured counts kept, got skipped=%d total=%d", merged.Skipped, merged.Total)
	}
	if len(merged.FailedTests) != 1 || !contains(merged.FailedTests[0].Name, "CreateInstance_should_initialize") {
		t.Errorf("expected failed tests filled, got %+v", merged.FailedTests)
	}
}

func TestNormalizeRestoreSummarySetsErrorFloorOnFailedCommand(t *testing.T) {
	normalized := normalizeRestoreSummary(RestoreSummary{RestoredProjects: 2}, false)
	if normalized.Errors != 1 {
		t.Errorf("expected errors=1, got %d", normalized.Errors)
	}
}

func TestMergeRestoreSummariesPrefersRawErrorCount(t *testing.T) {
	binlogSummary := RestoreSummary{RestoredProjects: 2, DurationText: "unknown", HasDuration: true}
	rawSummary := RestoreSummary{Errors: 1, DurationText: "unknown", HasDuration: true}
	merged := mergeRestoreSummaries(binlogSummary, rawSummary)
	if merged.Errors != 1 || merged.RestoredProjects != 2 {
		t.Errorf("expected errors=1 restored=2, got errors=%d restored=%d", merged.Errors, merged.RestoredProjects)
	}
}

// ── arg forwarding ───────────────────────────────────────────────────────────

func TestForwardingArgsWithSpaces(t *testing.T) {
	args := []string{"--filter", "FullyQualifiedName~MyTests.Calculator*", "-c", "Release"}
	injected := buildDotnetArgsForTest("test", args, true)
	for _, want := range []string{"--filter", "FullyQualifiedName~MyTests.Calculator*", "-c", "Release"} {
		if !containsArg(injected, want) {
			t.Errorf("missing %q in %v", want, injected)
		}
	}
}

func TestForwardingConfigAndFramework(t *testing.T) {
	args := []string{"--configuration", "Release", "--framework", "net8.0"}
	injected := buildDotnetArgsForTest("test", args, true)
	for _, want := range []string{"--configuration", "Release", "--framework", "net8.0"} {
		if !containsArg(injected, want) {
			t.Errorf("missing %q in %v", want, injected)
		}
	}
}

func TestForwardingProjectFile(t *testing.T) {
	args := []string{"--project", "src/My App.Tests/My App.Tests.csproj"}
	injected := buildDotnetArgsForTest("test", args, true)
	if !containsArg(injected, "--project") || !containsArg(injected, "src/My App.Tests/My App.Tests.csproj") {
		t.Errorf("missing project file args in %v", injected)
	}
}

func TestForwardingNoBuildAndNoRestore(t *testing.T) {
	args := []string{"--no-build", "--no-restore"}
	injected := buildDotnetArgsForTest("test", args, true)
	if !containsArg(injected, "--no-build") || !containsArg(injected, "--no-restore") {
		t.Errorf("missing no-build/no-restore in %v", injected)
	}
}

func TestUserVerboseOverride(t *testing.T) {
	injected := buildDotnetArgsForTest("test", []string{"-v:detailed"}, true)
	count := 0
	for _, a := range injected {
		if strings.HasPrefix(a, "-v:") {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one -v: arg, got %d in %v", count, injected)
	}
	if !containsArg(injected, "-v:detailed") || containsArg(injected, "-v:minimal") {
		t.Errorf("expected -v:detailed and no -v:minimal, got %v", injected)
	}
}

func TestUserLongVerbosityOverride(t *testing.T) {
	injected := buildDotnetArgsForTest("build", []string{"--verbosity", "detailed"}, false)
	if !containsArg(injected, "--verbosity") || !containsArg(injected, "detailed") {
		t.Errorf("missing --verbosity detailed in %v", injected)
	}
	if containsArg(injected, "-v:minimal") {
		t.Errorf("did not expect -v:minimal in %v", injected)
	}
}

func TestTestSubcommandDoesNotInjectMinimalVerbosity(t *testing.T) {
	injected := buildDotnetArgsForTest("test", nil, true)
	if containsArg(injected, "-v:minimal") {
		t.Errorf("did not expect -v:minimal for test, got %v", injected)
	}
}

func TestUserLoggerOverride(t *testing.T) {
	injected := buildDotnetArgsForTest("test", []string{"--logger", "console;verbosity=detailed"}, true)
	if !containsArg(injected, "--logger") || !containsArg(injected, "console;verbosity=detailed") {
		t.Errorf("missing user logger in %v", injected)
	}
	if !containsArg(injected, "trx") || !containsArg(injected, "--results-directory") {
		t.Errorf("expected injected trx logger + results-directory, got %v", injected)
	}
}

func TestTRXLoggerAndResultsDirectoryInjected(t *testing.T) {
	injected := buildDotnetArgsForTest("test", nil, true)
	for _, want := range []string{"--logger", "trx", "--results-directory", "/tmp/test results"} {
		if !containsArg(injected, want) {
			t.Errorf("missing %q in %v", want, injected)
		}
	}
}

func TestUserTRXLoggerDoesNotDuplicate(t *testing.T) {
	injected := buildDotnetArgsForTest("test", []string{"--logger", "trx"}, true)
	count := 0
	for _, a := range injected {
		if a == "trx" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected one trx, got %d in %v", count, injected)
	}
}

func TestUserResultsDirectoryPreventsExtraInjection(t *testing.T) {
	injected := buildDotnetArgsForTest("test", []string{"--results-directory", "/custom/results"}, true)
	if windowsPair(injected, "--results-directory", "/tmp/test results") {
		t.Errorf("did not expect injected results dir, got %v", injected)
	}
	if !windowsPair(injected, "--results-directory", "/custom/results") {
		t.Errorf("expected user results dir, got %v", injected)
	}
}

func windowsPair(args []string, a, b string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == a && args[i+1] == b {
			return true
		}
	}
	return false
}

// ── MTP detection (file-based) ───────────────────────────────────────────────

func writeProj(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func TestScanMtpKindDetectsUseMicrosoftTestingPlatformRunner(t *testing.T) {
	dir := t.TempDir()
	csproj := writeProj(t, dir, "MyProject.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <UseMicrosoftTestingPlatformRunner>true</UseMicrosoftTestingPlatformRunner>
  </PropertyGroup>
</Project>`)
	if scanMtpKindInFile(csproj) != mtpVsTestBridge {
		t.Error("expected VsTestBridge")
	}
}

func TestScanMtpKindDetectsUseTestingPlatformRunner(t *testing.T) {
	dir := t.TempDir()
	csproj := writeProj(t, dir, "MyProject.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <UseTestingPlatformRunner>true</UseTestingPlatformRunner>
  </PropertyGroup>
</Project>`)
	if scanMtpKindInFile(csproj) != mtpVsTestBridge {
		t.Error("expected VsTestBridge")
	}
}

func TestScanMtpKindReturnsNoneForClassicVSTest(t *testing.T) {
	dir := t.TempDir()
	csproj := writeProj(t, dir, "MyProject.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net9.0</TargetFramework>
  </PropertyGroup>
  <ItemGroup>
    <PackageReference Include="xunit" Version="2.9.0" />
  </ItemGroup>
</Project>`)
	if scanMtpKindInFile(csproj) != mtpNone {
		t.Error("expected None")
	}
}

func TestScanMtpKindReturnsNoneWhenValueIsFalse(t *testing.T) {
	dir := t.TempDir()
	csproj := writeProj(t, dir, "MyProject.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <UseMicrosoftTestingPlatformRunner>false</UseMicrosoftTestingPlatformRunner>
  </PropertyGroup>
</Project>`)
	if scanMtpKindInFile(csproj) != mtpNone {
		t.Error("expected None")
	}
}

func TestScanMtpKindDetectsVSTestBridge(t *testing.T) {
	dir := t.TempDir()
	csproj := writeProj(t, dir, "MSTest.Tests.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TestingPlatformDotnetTestSupport>true</TestingPlatformDotnetTestSupport>
  </PropertyGroup>
</Project>`)
	if scanMtpKindInFile(csproj) != mtpVsTestBridge {
		t.Error("expected VsTestBridge")
	}
}

func TestBothMtpPropertiesStillVSTestBridge(t *testing.T) {
	dir := t.TempDir()
	csproj := writeProj(t, dir, "Hybrid.Tests.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TestingPlatformDotnetTestSupport>true</TestingPlatformDotnetTestSupport>
    <UseMicrosoftTestingPlatformRunner>true</UseMicrosoftTestingPlatformRunner>
  </PropertyGroup>
</Project>`)
	if scanMtpKindInFile(csproj) != mtpVsTestBridge {
		t.Error("expected VsTestBridge")
	}
}

func TestDetectModeDirectoryBuildPropsVSTestBridge(t *testing.T) {
	dir := t.TempDir()
	props := writeProj(t, dir, "Directory.Build.props", `<Project>
  <PropertyGroup>
    <TestingPlatformDotnetTestSupport>true</TestingPlatformDotnetTestSupport>
  </PropertyGroup>
</Project>`)
	if scanMtpKindInFile(props) != mtpVsTestBridge {
		t.Error("expected VsTestBridge")
	}
}

func TestDetectModeMtpCsprojInjectsReportTRX(t *testing.T) {
	dir := t.TempDir()
	csproj := writeProj(t, dir, "MTP.Tests.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <UseMicrosoftTestingPlatformRunner>true</UseMicrosoftTestingPlatformRunner>
  </PropertyGroup>
</Project>`)
	args := []string{csproj}
	if detectTestRunnerMode(args) != runnerMtpVsTestBridge {
		t.Fatal("expected MtpVsTestBridge")
	}
	injected := buildEffectiveDotnetArgs("test", args, "/tmp/test.binlog", "")
	if containsArg(injected, "--logger") {
		t.Errorf("did not expect --logger, got %v", injected)
	}
	if !containsArg(injected, "--report-trx") || !containsArg(injected, "--") {
		t.Errorf("expected -- --report-trx, got %v", injected)
	}
}

func TestDetectModeVSTestBridgeInjectsReportTRX(t *testing.T) {
	dir := t.TempDir()
	csproj := writeProj(t, dir, "MSTest.Tests.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TestingPlatformDotnetTestSupport>true</TestingPlatformDotnetTestSupport>
  </PropertyGroup>
</Project>`)
	args := []string{csproj}
	if detectTestRunnerMode(args) != runnerMtpVsTestBridge {
		t.Fatal("expected MtpVsTestBridge")
	}
	injected := buildEffectiveDotnetArgs("test", args, "/tmp/test.binlog", "")
	if containsArg(injected, "--logger") {
		t.Errorf("did not expect --logger, got %v", injected)
	}
	if !containsArg(injected, "--report-trx") || !containsArg(injected, "--") || !containsArg(injected, "-nologo") {
		t.Errorf("expected -- --report-trx and -nologo, got %v", injected)
	}
}

func TestParseGlobalJSONMtpModeDetectsMtpNative(t *testing.T) {
	dir := t.TempDir()
	gj := writeProj(t, dir, "global.json", `{"sdk":{"version":"10.0.100"},"test":{"runner":"Microsoft.Testing.Platform"}}`)
	if !parseGlobalJSONMtpMode(gj) {
		t.Error("expected MTP native")
	}
}

func TestParseGlobalJSONMtpModeReturnsFalseForVSTestRunner(t *testing.T) {
	dir := t.TempDir()
	gj := writeProj(t, dir, "global.json", `{ "sdk": { "version": "9.0.100" } }`)
	if parseGlobalJSONMtpMode(gj) {
		t.Error("expected not MTP native")
	}
}

func TestVSTestBridgeInjectsReportTRXAfterSeparator(t *testing.T) {
	dir := t.TempDir()
	csproj := writeProj(t, dir, "MTP.Tests.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <UseMicrosoftTestingPlatformRunner>true</UseMicrosoftTestingPlatformRunner>
  </PropertyGroup>
</Project>`)
	args := []string{csproj}
	injected := buildEffectiveDotnetArgs("test", args, "/tmp/test.binlog", "")
	sepPos, trxPos := -1, -1
	for i, a := range injected {
		if a == "--" && sepPos < 0 {
			sepPos = i
		}
		if a == "--report-trx" && trxPos < 0 {
			trxPos = i
		}
	}
	if sepPos < 0 || trxPos < 0 || sepPos >= trxPos {
		t.Errorf("expected -- before --report-trx, got %v", injected)
	}
	if containsArg(injected, "--logger") {
		t.Errorf("did not expect --logger, got %v", injected)
	}
}

func TestVSTestBridgeExistingSeparatorInsertsReportTRXAfterIt(t *testing.T) {
	dir := t.TempDir()
	csproj := writeProj(t, dir, "MTP.Tests.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <UseMicrosoftTestingPlatformRunner>true</UseMicrosoftTestingPlatformRunner>
  </PropertyGroup>
</Project>`)
	args := []string{csproj, "--", "--parallel"}
	injected := buildEffectiveDotnetArgs("test", args, "/tmp/test.binlog", "")
	sepPos := -1
	for i, a := range injected {
		if a == "--" {
			sepPos = i
			break
		}
	}
	if sepPos < 0 || sepPos+1 >= len(injected) || injected[sepPos+1] != "--report-trx" {
		t.Errorf("expected --report-trx right after --, got %v", injected)
	}
	if !containsArg(injected, "--parallel") {
		t.Errorf("expected --parallel preserved, got %v", injected)
	}
}

func TestVSTestBridgeRespectsExistingReportTRX(t *testing.T) {
	dir := t.TempDir()
	csproj := writeProj(t, dir, "MTP.Tests.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <UseMicrosoftTestingPlatformRunner>true</UseMicrosoftTestingPlatformRunner>
  </PropertyGroup>
</Project>`)
	args := []string{csproj, "--", "--report-trx"}
	injected := buildEffectiveDotnetArgs("test", args, "/tmp/test.binlog", "")
	count := 0
	for _, a := range injected {
		if a == "--report-trx" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected one --report-trx, got %d in %v", count, injected)
	}
}

func TestDetectModeClassicCsprojInjectsTRX(t *testing.T) {
	dir := t.TempDir()
	csproj := writeProj(t, dir, "Classic.Tests.csproj", `<Project Sdk="Microsoft.NET.Sdk">
  <PropertyGroup>
    <TargetFramework>net9.0</TargetFramework>
  </PropertyGroup>
</Project>`)
	args := []string{csproj}
	if detectTestRunnerMode(args) != runnerClassic {
		t.Fatal("expected Classic")
	}
	injected := buildEffectiveDotnetArgs("test", args, "/tmp/test.binlog", "/tmp/test_results")
	if !containsArg(injected, "--logger") || !containsArg(injected, "trx") {
		t.Errorf("expected --logger trx, got %v", injected)
	}
}

// ── results-directory arg helpers ────────────────────────────────────────────

func TestHasResultsDirectoryArgDetectsVariants(t *testing.T) {
	if !hasResultsDirectoryArg([]string{"--results-directory", "/tmp/trx"}) {
		t.Error("expected detected (space form)")
	}
	if !hasResultsDirectoryArg([]string{"--results-directory=/tmp/trx"}) {
		t.Error("expected detected (= form)")
	}
	if hasResultsDirectoryArg([]string{"--logger", "trx"}) {
		t.Error("did not expect detection")
	}
}

func TestExtractResultsDirectoryArgDetectsVariants(t *testing.T) {
	if v, ok := extractResultsDirectoryArg([]string{"--results-directory", "/tmp/r1"}); !ok || v != "/tmp/r1" {
		t.Errorf("expected /tmp/r1, got %q ok=%v", v, ok)
	}
	if v, ok := extractResultsDirectoryArg([]string{"--results-directory=/tmp/r2"}); !ok || v != "/tmp/r2" {
		t.Errorf("expected /tmp/r2, got %q ok=%v", v, ok)
	}
}

func TestResolveTRXResultsDirUserDirectoryNotMarkedForCleanup(t *testing.T) {
	dir, cleanup := resolveTRXResultsDir("test", []string{"--results-directory", "/custom/results"})
	if dir != "/custom/results" || cleanup {
		t.Errorf("expected /custom/results no-cleanup, got %q cleanup=%v", dir, cleanup)
	}
}

func TestResolveTRXResultsDirGeneratedDirectoryMarkedForCleanup(t *testing.T) {
	dir, cleanup := resolveTRXResultsDir("test", nil)
	if dir == "" || !cleanup {
		t.Errorf("expected generated dir with cleanup, got %q cleanup=%v", dir, cleanup)
	}
}

// ── TRX merge ────────────────────────────────────────────────────────────────

func TestMergeTestSummaryFromTRXUsesPrimaryAndCleansFile(t *testing.T) {
	dir := t.TempDir()
	primary := filepath.Join(dir, "primary.trx")
	if err := os.WriteFile(primary, []byte(trxWithCounts(3, 3, 0)), 0o644); err != nil {
		t.Fatal(err)
	}
	filled := mergeTestSummaryFromTRX(TestSummary{}, dir, time.Now())
	if filled.Total != 3 || filled.Passed != 3 {
		t.Errorf("expected total=3 passed=3, got total=%d passed=%d", filled.Total, filled.Passed)
	}
	if !fileExists(primary) {
		t.Error("primary trx should still exist")
	}
}

func TestMergeTestSummaryFromTRXReturnsDefaultWhenNoTRX(t *testing.T) {
	dir := t.TempDir()
	filled := mergeTestSummaryFromTRX(TestSummary{}, filepath.Join(dir, "missing"), time.Now())
	if filled.Total != 0 {
		t.Errorf("expected total=0, got %d", filled.Total)
	}
}

func TestMergeTestSummaryFromTRXKeepsLargerExistingCounts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "primary.trx"), []byte(trxWithCounts(5, 4, 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	existing := TestSummary{Passed: 10, Failed: 2, Total: 12, ProjectCount: 1, DurationText: "1 s", HasDuration: true}
	merged := mergeTestSummaryFromTRX(existing, dir, time.Now())
	if merged.Total != 12 || merged.Passed != 10 || merged.Failed != 2 {
		t.Errorf("expected larger existing kept, got total=%d passed=%d failed=%d", merged.Total, merged.Passed, merged.Failed)
	}
}

func TestMergeTestSummaryFromTRXOverridesSmallerExistingCounts(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "primary.trx"), []byte(trxWithCounts(12, 10, 2)), 0o644); err != nil {
		t.Fatal(err)
	}
	existing := TestSummary{Passed: 4, Failed: 1, Total: 5, ProjectCount: 1, DurationText: "1 s", HasDuration: true}
	merged := mergeTestSummaryFromTRX(existing, dir, time.Now())
	if merged.Total != 12 || merged.Passed != 10 || merged.Failed != 2 {
		t.Errorf("expected trx override, got total=%d passed=%d failed=%d", merged.Total, merged.Passed, merged.Failed)
	}
}

func TestMergeTestSummaryFromTRXUsesLargerProjectCount(t *testing.T) {
	dir := t.TempDir()
	// commandStartedAt must precede the .trx files: in production the command
	// starts, then the run writes the .trx. Passing time.Now() AFTER writing
	// races the filesystem mtime granularity on Windows and can exclude them.
	startedAt := time.Now().Add(-time.Second)
	if err := os.WriteFile(filepath.Join(dir, "a.trx"), []byte(trxWithCounts(2, 2, 0)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.trx"), []byte(trxWithCounts(3, 3, 0)), 0o644); err != nil {
		t.Fatal(err)
	}
	existing := TestSummary{Passed: 5, Total: 5, ProjectCount: 1, DurationText: "1 s", HasDuration: true}
	merged := mergeTestSummaryFromTRX(existing, dir, startedAt)
	if merged.ProjectCount != 2 {
		t.Errorf("expected project count 2, got %d", merged.ProjectCount)
	}
}

// ── format report ────────────────────────────────────────────────────────────

func TestParseFormatReportAllFormatted(t *testing.T) {
	summary, err := ParseFormatReport(fixture(t, "format_success.json"))
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalFiles != 2 || summary.FilesUnchanged != 2 || len(summary.FilesWithChanges) != 0 {
		t.Errorf("unexpected summary: %+v", summary)
	}
}

func TestParseFormatReportWithChanges(t *testing.T) {
	summary, err := ParseFormatReport(fixture(t, "format_changes.json"))
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalFiles != 3 || summary.FilesUnchanged != 1 || len(summary.FilesWithChanges) != 2 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if !contains(summary.FilesWithChanges[0].Path, "Program.cs") {
		t.Errorf("expected Program.cs, got %q", summary.FilesWithChanges[0].Path)
	}
	if summary.FilesWithChanges[0].Changes[0].LineNumber != 42 {
		t.Errorf("expected line 42, got %d", summary.FilesWithChanges[0].Changes[0].LineNumber)
	}
}

func TestParseFormatReportEmpty(t *testing.T) {
	summary, err := ParseFormatReport(fixture(t, "format_empty.json"))
	if err != nil {
		t.Fatal(err)
	}
	if summary.TotalFiles != 0 || summary.FilesUnchanged != 0 || len(summary.FilesWithChanges) != 0 {
		t.Errorf("unexpected summary: %+v", summary)
	}
}

func TestFormatAllFormatted(t *testing.T) {
	summary, err := ParseFormatReport(fixture(t, "format_success.json"))
	if err != nil {
		t.Fatal(err)
	}
	output := formatDotnetFormatOutput(summary, true)
	if !contains(output, "ok dotnet format: 2 files formatted correctly") {
		t.Errorf("unexpected output: %s", output)
	}
}

func TestFormatNeedsFormatting(t *testing.T) {
	summary, err := ParseFormatReport(fixture(t, "format_changes.json"))
	if err != nil {
		t.Fatal(err)
	}
	output := formatDotnetFormatOutput(summary, true)
	if !contains(output, "Format: 2 files need formatting") {
		t.Errorf("missing header: %s", output)
	}
	if !contains(output, "src/Program.cs (line 42, col 17, WHITESPACE)") {
		t.Errorf("missing change detail: %s", output)
	}
	if !contains(output, "Run `dotnet format` to apply fixes") {
		t.Errorf("missing fix hint: %s", output)
	}
}

func TestFormatTempFileCleanup(t *testing.T) {
	reportPath, cleanup := resolveFormatReportPath(nil)
	if !cleanup {
		t.Fatal("expected cleanup=true for generated path")
	}
	if err := os.WriteFile(reportPath, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanupTempFile(reportPath)
	if fileExists(reportPath) {
		t.Error("temp report should be removed")
	}
}

func TestFormatUserReportArgNoCleanup(t *testing.T) {
	reportPath, cleanup := resolveFormatReportPath([]string{"--report", "/tmp/user-format-report.json"})
	if reportPath != "/tmp/user-format-report.json" || cleanup {
		t.Errorf("expected user path no-cleanup, got %q cleanup=%v", reportPath, cleanup)
	}
}

func TestFormatPreservesPositionalProjectArgumentOrder(t *testing.T) {
	effective := buildEffectiveDotnetFormatArgs([]string{"src/App/App.csproj"}, "/tmp/report.json")
	if len(effective) == 0 || effective[0] != "src/App/App.csproj" {
		t.Errorf("expected positional first, got %v", effective)
	}
}

func TestFormatReportSummaryIgnoresStaleReportFile(t *testing.T) {
	dir := t.TempDir()
	report := filepath.Join(dir, "report.json")
	if err := os.WriteFile(report, []byte("[]"), 0o644); err != nil {
		t.Fatal(err)
	}
	commandStartedAt := time.Now().Add(2 * time.Second)
	raw := "RAW OUTPUT"
	output := formatReportSummaryOrRaw(report, true, raw, commandStartedAt)
	if output != raw {
		t.Errorf("expected raw for stale report, got %q", output)
	}
}

func TestFormatReportSummaryUsesFreshReportFile(t *testing.T) {
	report := fixture(t, "format_success.json")
	raw := "RAW OUTPUT"
	output := formatReportSummaryOrRaw(report, true, raw, time.Unix(0, 0))
	if !contains(output, "ok dotnet format: 2 files formatted correctly") {
		t.Errorf("expected format summary, got %q", output)
	}
}

func TestCleanupTempFileRemovesExistingFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "temp.binlog")
	if err := os.WriteFile(f, []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	cleanupTempFile(f)
	if fileExists(f) {
		t.Error("file should be removed")
	}
}

func TestCleanupTempFileIgnoresMissingFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "missing.binlog")
	cleanupTempFile(f)
	if fileExists(f) {
		t.Error("missing file should stay missing")
	}
}
