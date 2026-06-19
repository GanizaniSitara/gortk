package mvn

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gortk/internal/core"
)

// These tests are ported from rtk's #[cfg(test)] module in
// src/cmds/jvm/mvn_cmd.rs. The fixture .txt / .txt.gz files under testdata/ are
// copied verbatim from rtk's tests/fixtures (LF-normalized). The "rtk"/"Rtk"
// literals inside fixtures are real captured Maven output, kept as-is. Tests run
// against the pure filter helpers, as the porting contract requires.

func countTokens(s string) int {
	return len(strings.Fields(s))
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	// Normalize CRLF defensively (rtk relies on `tests/fixtures/** -text`).
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

func gunzipFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read gz fixture %s: %v", name, err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gunzip %s: %v", name, err)
	}
	defer gz.Close()
	out, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("read gz %s: %v", name, err)
	}
	return string(out)
}

func sArgs(items ...string) []string { return items }

// ── Phase detection ──────────────────────────────────────────────────────────

func TestPhaseDetection(t *testing.T) {
	cases := []struct {
		args []string
		want MvnPhase
	}{
		{sArgs("test"), PhaseTest},
		{sArgs("integration-test"), PhaseTest},
		{sArgs("compile"), PhaseCompile},
		{sArgs("test-compile"), PhaseCompile},
		{sArgs("install"), PhasePackage},
		{sArgs("package"), PhasePackage},
		{sArgs("verify"), PhasePackage},
		{sArgs("deploy"), PhasePackage},
		{sArgs("clean", "install"), PhasePackage},
		{sArgs("-B", "-DskipTests", "test"), PhaseTest},
		{sArgs("clean"), PhasePassthrough},
		{sArgs("site"), PhasePassthrough},
		{sArgs("dependency:tree"), PhasePassthrough},
		{nil, PhasePassthrough},
		{sArgs("--version"), PhasePassthrough},
		{sArgs("-v"), PhasePassthrough},
		{sArgs("-version"), PhasePassthrough},
		{sArgs("--help"), PhasePassthrough},
	}
	for _, c := range cases {
		if got := detectPhase(c.args); got != c.want {
			t.Errorf("detectPhase(%v) = %v, want %v", c.args, got, c.want)
		}
	}
}

// ── Surefire filter (fixtures) ───────────────────────────────────────────────

func TestSurefirePassOutputCompact(t *testing.T) {
	i := readFixture(t, "mvn_test_pass_slice_raw.txt")
	o := filterSurefire(i)
	if strings.Contains(o, "Running org.apache.commons.cli.help.UtilTest") {
		t.Errorf("passing Running line should be dropped:\n%s", o)
	}
	if strings.Contains(o, "Time elapsed: 1.023 s -- in") {
		t.Errorf("close line should be dropped:\n%s", o)
	}
	savings := 100.0 - (float64(countTokens(o))/float64(countTokens(i)))*100.0
	if savings < 50.0 {
		t.Errorf("expected >=50%% savings, got %.1f%%", savings)
	}
}

func TestSurefireFailKeepsSignal(t *testing.T) {
	i := readFixture(t, "mvn_test_fail_slice_raw.txt")
	o := filterSurefire(i)
	if !strings.Contains(o, "BUILD FAILURE") || !strings.Contains(o, "Failures: 1") {
		t.Errorf("failing signal not kept:\n%s", o)
	}
}

func TestSurefireDropsPassingBlock(t *testing.T) {
	i := readFixture(t, "mvn_test_pass_slice_raw.txt")
	o := filterSurefire(i)
	if strings.Contains(o, "at org.junit.") {
		t.Errorf("framework frames should be stripped:\n%s", o)
	}
	if strings.Contains(o, "Running org.apache.commons.cli.ConverterTests") {
		t.Errorf("passing Running line should be dropped:\n%s", o)
	}
	if !strings.Contains(o, "BUILD SUCCESS") {
		t.Errorf("footer should be preserved:\n%s", o)
	}
	if !strings.Contains(o, "Tests run: 977, Failures: 0") {
		t.Errorf("aggregate should be preserved:\n%s", o)
	}
}

func TestSurefirePreservesFailingSignal(t *testing.T) {
	i := readFixture(t, "mvn_test_fail_slice_raw.txt")
	o := filterSurefire(i)
	if !strings.Contains(o, "Failures: 1") {
		t.Errorf("failing aggregate not kept:\n%s", o)
	}
	if !strings.Contains(o, "AssertionFailedError") {
		t.Errorf("exception class not kept:\n%s", o)
	}
	if !strings.Contains(o, "at org.apache.commons.cli.RtkInducedFailTest.rtkInducedFailure") {
		t.Errorf("user-code frame not kept:\n%s", o)
	}
	if strings.Contains(o, "at org.junit.") {
		t.Errorf("framework frames should be stripped:\n%s", o)
	}
}

func TestSurefireMatchesLegacy2xCloseLine(t *testing.T) {
	i := "[INFO] -----< x >-----\n[INFO] Running x.Foo\n[INFO] Tests run: 3, Failures: 0, Errors: 0, Skipped: 0, Time elapsed: 0.123 s - in x.Foo\n[INFO] BUILD SUCCESS\n"
	o := filterSurefire(i)
	if strings.Contains(o, "Running x.Foo") {
		t.Errorf("2.x close line should match; block dropped:\n%s", o)
	}
	if !strings.Contains(o, "BUILD SUCCESS") {
		t.Errorf("footer should be preserved:\n%s", o)
	}
}

func TestSurefireMatchesWarningSkippedCloseLine(t *testing.T) {
	i := "[INFO] -----< x >-----\n[INFO] Running x.Skip\n[WARNING] Tests run: 5, Failures: 0, Errors: 0, Skipped: 5, Time elapsed: 0.010 s -- in x.Skip\n[INFO] BUILD SUCCESS\n"
	o := filterSurefire(i)
	if strings.Contains(o, "Running x.Skip") {
		t.Errorf("[WARNING] close line should match; block dropped:\n%s", o)
	}
}

func TestSurefirePreserves3xFailureTrail(t *testing.T) {
	i := "[INFO] -----< x >-----\n" +
		"[INFO] Running x.Foo\n" +
		"[ERROR] Tests run: 1, Failures: 1, Errors: 0, Skipped: 0, Time elapsed: 0.033 s <<< FAILURE! -- in x.Foo\n" +
		"[ERROR] x.Foo.bar -- Time elapsed: 0.025 s <<< FAILURE!\n" +
		"org.opentest4j.AssertionFailedError: expected: <a> but was: <b>\n" +
		"\tat x.Foo.bar(Foo.java:25)\n" +
		"\tat org.junit.jupiter.api.Assertions.assertEquals(Assertions.java:1)\n" +
		"\n" +
		"[INFO] BUILD FAILURE\n"
	o := filterSurefire(i)
	if !strings.Contains(o, "AssertionFailedError") {
		t.Errorf("exception not preserved:\n%s", o)
	}
	if !strings.Contains(o, "at x.Foo.bar") {
		t.Errorf("user frame not preserved:\n%s", o)
	}
	if strings.Contains(o, "at org.junit.") {
		t.Errorf("framework frame should be stripped:\n%s", o)
	}
}

// ── Multi-failure class (trail re-arm) ───────────────────────────────────────

func TestSurefireKeepsAllFailuresInMultiFailureClass(t *testing.T) {
	i := readFixture(t, "mvn_test_multifail_slice_raw.txt")
	o := filterSurefire(i)
	for _, want := range []string{
		"AssertionFailedError: failOne: addition should equal five",
		"IllegalStateException: failTwo: induced error",
		"at com.example.rtk.CalcTest.failOne(CalcTest.java:12)",
		"at com.example.rtk.CalcTest.failTwo(CalcTest.java:17)",
	} {
		if !strings.Contains(o, want) {
			t.Errorf("missing %q:\n%s", want, o)
		}
	}
	if strings.Contains(o, "at org.junit.") || strings.Contains(o, "at java.base/") {
		t.Errorf("framework/jdk frames should be stripped:\n%s", o)
	}
}

func TestPackageKeepsAllFailuresInMultiFailureClass(t *testing.T) {
	i := readFixture(t, "mvn_test_multifail_slice_raw.txt")
	o := filterPackage(i)
	if !strings.Contains(o, "AssertionFailedError: failOne: addition should equal five") ||
		!strings.Contains(o, "IllegalStateException: failTwo: induced error") {
		t.Errorf("failure messages not preserved:\n%s", o)
	}
	if strings.Contains(o, "at org.junit.") || strings.Contains(o, "at java.base/") {
		t.Errorf("frames should be stripped:\n%s", o)
	}
}

func TestSurefireDropFailingDropsAllSublinesOfCappedClass(t *testing.T) {
	i := "[INFO] Scanning for projects...\n" +
		"[INFO] -----< x >-----\n" +
		"[INFO] Running x.FailA\n" +
		"[ERROR] Tests run: 1, Failures: 1, Errors: 0, Skipped: 0, Time elapsed: 0.011 s <<< FAILURE! -- in x.FailA\n" +
		"[ERROR] x.FailA.one -- Time elapsed: 0.010 s <<< FAILURE!\n" +
		"org.opentest4j.AssertionFailedError: boomA\n" +
		"\tat x.FailA.one(FailA.java:10)\n" +
		"\n" +
		"[INFO] Running x.MultiFail\n" +
		"[ERROR] Tests run: 2, Failures: 1, Errors: 1, Skipped: 0, Time elapsed: 0.051 s <<< FAILURE! -- in x.MultiFail\n" +
		"[ERROR] x.MultiFail.first -- Time elapsed: 0.020 s <<< FAILURE!\n" +
		"org.opentest4j.AssertionFailedError: boomFirst\n" +
		"\tat x.MultiFail.first(MultiFail.java:20)\n" +
		"\n" +
		"[ERROR] x.MultiFail.second -- Time elapsed: 0.030 s <<< ERROR!\n" +
		"java.lang.IllegalStateException: boomSecond\n" +
		"\tat x.MultiFail.second(MultiFail.java:30)\n" +
		"\n" +
		"[INFO] BUILD FAILURE\n"
	o := filterSurefireWithCap(i, 1)
	if !strings.Contains(o, "boomA") {
		t.Errorf("first class should be kept:\n%s", o)
	}
	if strings.Contains(o, "Running x.MultiFail") || strings.Contains(o, "boomFirst") {
		t.Errorf("capped class first block should be dropped:\n%s", o)
	}
	if strings.Contains(o, "x.MultiFail.second") || strings.Contains(o, "boomSecond") {
		t.Errorf("capped class second per-test block should be dropped:\n%s", o)
	}
	if !strings.Contains(o, "… +1 more failing test classes") {
		t.Errorf("tail should count one class:\n%s", o)
	}
}

func TestSurefireRearmDisarmsAtResultsBoundary(t *testing.T) {
	i := "[INFO] -----< x >-----\n" +
		"[INFO] Running x.MultiFail\n" +
		"[ERROR] Tests run: 2, Failures: 2, Errors: 0, Skipped: 0, Time elapsed: 0.051 s <<< FAILURE! -- in x.MultiFail\n" +
		"[ERROR] x.MultiFail.first -- Time elapsed: 0.020 s <<< FAILURE!\n" +
		"org.opentest4j.AssertionFailedError: boomFirst\n" +
		"\n" +
		"[ERROR] x.MultiFail.second -- Time elapsed: 0.030 s <<< FAILURE!\n" +
		"org.opentest4j.AssertionFailedError: boomSecond\n" +
		"\n" +
		"[INFO] Results:\n" +
		"[ERROR] Tests run: 2, Failures: 2, Errors: 0, Skipped: 0\n" +
		"[INFO] BUILD FAILURE\n"
	o := filterSurefire(i)
	if !strings.Contains(o, "boomSecond") {
		t.Errorf("second block should be kept:\n%s", o)
	}
	if !strings.Contains(o, "[INFO] Results:") {
		t.Errorf("Results boundary should be kept:\n%s", o)
	}
	if !strings.Contains(o, "[ERROR] Tests run: 2, Failures: 2") {
		t.Errorf("aggregate should be kept:\n%s", o)
	}
}

func TestSurefireToleratesDoubleBlankBetweenFailureBlocks(t *testing.T) {
	i := "[INFO] -----< x >-----\n" +
		"[INFO] Running x.MultiFail\n" +
		"[ERROR] Tests run: 2, Failures: 2, Errors: 0, Skipped: 0, Time elapsed: 0.051 s <<< FAILURE! -- in x.MultiFail\n" +
		"[ERROR] x.MultiFail.first -- Time elapsed: 0.020 s <<< FAILURE!\n" +
		"org.opentest4j.AssertionFailedError: boomFirst\n" +
		"\n" +
		"\n" +
		"[ERROR] x.MultiFail.second -- Time elapsed: 0.030 s <<< FAILURE!\n" +
		"org.opentest4j.AssertionFailedError: boomSecond\n" +
		"\n" +
		"[INFO] BUILD FAILURE\n"
	o := filterSurefire(i)
	if !strings.Contains(o, "boomFirst") || !strings.Contains(o, "boomSecond") {
		t.Errorf("both blocks should be kept:\n%s", o)
	}
	if strings.Contains(o, "\n\n\n") {
		t.Errorf("no spurious blank lines should leak:\n%q", o)
	}
}

func TestSurefireSingleFailureOutputUnchanged(t *testing.T) {
	i := readFixture(t, "mvn_test_fail_slice_raw.txt")
	o := filterSurefire(i)
	expected := "[INFO] Scanning for projects...\n" +
		"[INFO] ----------------------< commons-cli:commons-cli >-----------------------\n" +
		"[INFO] Building Apache Commons CLI 1.11.1-SNAPSHOT\n" +
		"[INFO] Running org.apache.commons.cli.RtkInducedFailTest\n" +
		"[ERROR] Tests run: 1, Failures: 1, Errors: 0, Skipped: 0, Time elapsed: 0.033 s <<< FAILURE! -- in org.apache.commons.cli.RtkInducedFailTest\n" +
		"[ERROR] org.apache.commons.cli.RtkInducedFailTest.rtkInducedFailure -- Time elapsed: 0.025 s <<< FAILURE!\n" +
		"org.opentest4j.AssertionFailedError: expected: <expected> but was: <actual>\n" +
		"\tat org.apache.commons.cli.RtkInducedFailTest.rtkInducedFailure(RtkInducedFailTest.java:25)\n" +
		"\n" +
		"[INFO] Results:\n" +
		"[ERROR] Failures:\n" +
		"[ERROR]   RtkInducedFailTest.rtkInducedFailure:25 expected: <expected> but was: <actual>\n" +
		"[ERROR] Tests run: 978, Failures: 1, Errors: 0, Skipped: 61\n" +
		"[INFO] BUILD FAILURE\n" +
		"[INFO] Total time:  01:05 min\n" +
		"[INFO] Finished at: 2026-05-21T14:57:09Z\n" +
		"[ERROR] Failed to execute goal org.apache.maven.plugins:maven-surefire-plugin:3.5.5:test (default-test) on project commons-cli: There are test failures.\n"
	if o != expected {
		t.Errorf("single-failure output not byte-identical.\n got: %q\nwant: %q", o, expected)
	}
}

func TestSavingsMvnTestMultifailSlice(t *testing.T) {
	i := readFixture(t, "mvn_test_multifail_slice_raw.txt")
	o := filterSurefire(i)
	savings := 100.0 - (float64(countTokens(o))/float64(countTokens(i)))*100.0
	if savings < 30.0 {
		t.Errorf("expected >=30%% savings, got %.1f%%", savings)
	}
}

func TestSurefireDropsHelpBoilerplateInNonquietMode(t *testing.T) {
	i := readFixture(t, "mvn_test_multifail_slice_raw.txt")
	o := filterSurefire(i)
	if !strings.Contains(o, "[ERROR] Failed to execute goal") {
		t.Errorf("goal terminator should be kept:\n%s", o)
	}
	for _, drop := range []string{"[Help 1]", "Re-run Maven", "To see the full stack trace", "See dump files"} {
		if strings.Contains(o, drop) {
			t.Errorf("%q should be stripped:\n%s", drop, o)
		}
	}
	for _, l := range splitLines(o) {
		if strings.TrimRight(l, " \t") == "[ERROR]" {
			t.Errorf("bare [ERROR] divider should be stripped:\n%s", o)
		}
	}
}

func TestCloseLineMatchesErrorMarker(t *testing.T) {
	line := "[ERROR] Tests run: 1, Failures: 0, Errors: 1, Skipped: 0, Time elapsed: 0.006 s <<< ERROR! -- in com.example.rtk.BoomTest"
	caps := closeRE.FindStringSubmatch(line)
	if caps == nil {
		t.Fatal("CLOSE must match an ERROR!-marked close line")
	}
	if caps[1] != "0" || caps[2] != "1" {
		t.Errorf("expected failures=0 errors=1, got failures=%q errors=%q", caps[1], caps[2])
	}
}

func TestSurefireKeepsCompileContinuationOnTestPhase(t *testing.T) {
	i := readFixture(t, "mvn_test_compile_fail_slice_raw.txt")
	o := filterSurefire(i)
	if !strings.Contains(o, "cannot find symbol") ||
		!strings.Contains(o, "symbol:   variable bar") ||
		!strings.Contains(o, "location: class org.apache.commons.cli.CompileBreaker") ||
		!strings.Contains(o, "BUILD FAILURE") {
		t.Errorf("compile continuation not preserved:\n%s", o)
	}
}

func TestPackageStillKeepsCompileErrorContinuationAfterRefactor(t *testing.T) {
	i := readFixture(t, "mvn_compile_error_slice_raw.txt")
	o := filterPackage(i)
	if !strings.Contains(o, "cannot find symbol") ||
		!strings.Contains(o, "symbol:   variable bar") ||
		!strings.Contains(o, "location: class org.apache.commons.cli.CompileBreaker") {
		t.Errorf("compile continuation not preserved in package path:\n%s", o)
	}
}

func TestSurefireKeepsModuleBanner(t *testing.T) {
	i := "[INFO] Scanning for projects...\n[INFO] -----< com.example:myapp >-----\n[INFO] BUILD SUCCESS\n"
	o := filterSurefire(i)
	if !strings.Contains(o, "-----< com.example:myapp >-----") {
		t.Errorf("module banner not kept:\n%s", o)
	}
}

func TestSurefirePreservesRealDurations(t *testing.T) {
	i := "[INFO] -----< x >-----\n[INFO] Running x.Foo\n[ERROR] Tests run: 1, Failures: 1, Errors: 0, Skipped: 0, Time elapsed: 2.341 s <<< FAILURE! - in x.Foo\n[INFO] BUILD FAILURE\n[INFO] Total time:  4.567 s\n"
	o := filterSurefire(i)
	if !strings.Contains(o, "2.341 s") || !strings.Contains(o, "Total time:  4.567 s") {
		t.Errorf("real durations not preserved:\n%s", o)
	}
	if strings.Contains(o, "Time elapsed: T s") {
		t.Errorf("no normalisation expected:\n%s", o)
	}
}

// ── Footer guard ─────────────────────────────────────────────────────────────

func TestFooterGuardFrenchPassthrough(t *testing.T) {
	i := readFixture(t, "mvn_locale_fr_raw.txt")
	o := filterSurefire(i)
	if !strings.Contains(o, "BUILD ÉCHEC") {
		t.Errorf("non-English output should pass through:\n%s", o)
	}
	if len(splitLines(o)) != len(splitLines(i)) {
		t.Errorf("footer-guard should return raw input (line count %d != %d)", len(splitLines(o)), len(splitLines(i)))
	}
}

func TestFooterGuardNoPomPassthrough(t *testing.T) {
	i := readFixture(t, "mvn_no_pom_raw.txt")
	o := filterSurefire(i)
	if !strings.Contains(o, "there is no POM") {
		t.Errorf("no-pom error should be preserved:\n%s", o)
	}
}

// ── CRLF compatibility ───────────────────────────────────────────────────────

func TestSurefireHandlesCRLFLineEndings(t *testing.T) {
	iLF := readFixture(t, "mvn_test_pass_slice_raw.txt")
	oLF := filterSurefire(iLF)
	iCRLF := strings.ReplaceAll(iLF, "\n", "\r\n")
	// Production normalizes CRLF before filtering (core.NormalizeNewlines);
	// mirror that here so the filter sees LF, matching rtk's str::lines() which
	// strips \r\n pairs.
	oCRLF := filterSurefire(core.NormalizeNewlines(iCRLF))
	if oLF != strings.ReplaceAll(oCRLF, "\r\n", "\n") {
		t.Errorf("CRLF filtered output must match LF")
	}
}

func TestPackageHandlesCRLFLineEndings(t *testing.T) {
	iLF := readFixture(t, "mvn_install_slice_raw.txt")
	oLF := filterPackage(iLF)
	iCRLF := strings.ReplaceAll(iLF, "\n", "\r\n")
	oCRLF := filterPackage(core.NormalizeNewlines(iCRLF))
	if oLF != strings.ReplaceAll(oCRLF, "\r\n", "\n") {
		t.Errorf("CRLF filtered output must match LF")
	}
}

// ── Cap tests ────────────────────────────────────────────────────────────────

func TestSurefireCapsFailingBlocksEmitsTail(t *testing.T) {
	var b strings.Builder
	b.WriteString("[INFO] Scanning for projects...\n[INFO] -----< x >-----\n")
	for n := 1; n <= 5; n++ {
		b.WriteString("[INFO] Running x.Fail" + itoa(n) + "\n")
		b.WriteString("[ERROR] Tests run: 1, Failures: 1, Errors: 0, Skipped: 0, Time elapsed: 0.0" + itoa(n) + "1 s <<< FAILURE! -- in x.Fail" + itoa(n) + "\n")
		b.WriteString("[ERROR] x.Fail" + itoa(n) + ".bar -- Time elapsed: 0.0" + itoa(n) + "0 s <<< FAILURE!\n")
		b.WriteString("org.opentest4j.AssertionFailedError: boom" + itoa(n) + "\n")
		b.WriteString("\tat x.Fail" + itoa(n) + ".bar(Fail" + itoa(n) + ".java:25)\n")
		b.WriteString("\n")
	}
	b.WriteString("[INFO] BUILD FAILURE\n")
	o := filterSurefireWithCap(b.String(), 3)

	for n := 1; n <= 3; n++ {
		if !strings.Contains(o, "Running x.Fail"+itoa(n)) || !strings.Contains(o, "in x.Fail"+itoa(n)) {
			t.Errorf("Fail%d should be kept:\n%s", n, o)
		}
	}
	for n := 4; n <= 5; n++ {
		if strings.Contains(o, "Running x.Fail"+itoa(n)) || strings.Contains(o, "AssertionFailedError: boom"+itoa(n)) {
			t.Errorf("Fail%d should be dropped:\n%s", n, o)
		}
	}
	if !strings.Contains(o, "… +2 more failing test classes") {
		t.Errorf("tail not emitted:\n%s", o)
	}
}

func TestSurefireCapZeroEmitsSummaryOnly(t *testing.T) {
	var b strings.Builder
	b.WriteString("[INFO] Scanning for projects...\n[INFO] -----< x >-----\n")
	for n := 1; n <= 5; n++ {
		b.WriteString("[INFO] Running x.Fail" + itoa(n) + "\n")
		b.WriteString("[ERROR] Tests run: 1, Failures: 1, Errors: 0, Skipped: 0, Time elapsed: 0.0" + itoa(n) + "1 s <<< FAILURE! -- in x.Fail" + itoa(n) + "\n")
		b.WriteString("\n")
	}
	b.WriteString("[INFO] BUILD FAILURE\n")
	o := filterSurefireWithCap(b.String(), 0)
	for n := 1; n <= 5; n++ {
		if strings.Contains(o, "Running x.Fail"+itoa(n)) {
			t.Errorf("Fail%d should be dropped under cap=0:\n%s", n, o)
		}
	}
	if !strings.Contains(o, "+5 more failing test classes") {
		t.Errorf("tail should count all 5:\n%s", o)
	}
}

func TestFailuresSummaryBlockIsCapped(t *testing.T) {
	var b strings.Builder
	b.WriteString("[INFO] -----< x >-----\n[INFO] Results:\n[INFO]\n[ERROR] Failures:\n")
	for n := 1; n <= 5; n++ {
		b.WriteString("[ERROR]   ClassA.test" + itoa(n) + ":25 expected: <a> but was: <b" + itoa(n) + ">\n")
	}
	b.WriteString("[INFO]\n[ERROR] Tests run: 100, Failures: 5, Errors: 0, Skipped: 0\n[INFO] BUILD FAILURE\n")
	o := filterSurefireWithCap(b.String(), 3)

	for n := 1; n <= 3; n++ {
		if !strings.Contains(o, "ClassA.test"+itoa(n)+":25") {
			t.Errorf("entry %d should be kept:\n%s", n, o)
		}
	}
	for n := 4; n <= 5; n++ {
		if strings.Contains(o, "ClassA.test"+itoa(n)+":25") {
			t.Errorf("entry %d should be dropped:\n%s", n, o)
		}
	}
	tailIdx := strings.Index(o, "… +2 more failures")
	aggIdx := strings.Index(o, "[ERROR] Tests run: 100")
	if tailIdx < 0 || aggIdx < 0 || tailIdx >= aggIdx {
		t.Errorf("tail must precede aggregate (tail@%d agg@%d):\n%s", tailIdx, aggIdx, o)
	}
}

// ── Multi-module reactor summary ─────────────────────────────────────────────

func TestReactorSummaryKeptOnMultiModulePass(t *testing.T) {
	i := readFixture(t, "mvn_reactor_pass_slice_raw.txt")
	o := filterPackage(i)
	for _, want := range []string{
		"Reactor Summary for multi-module-skeleton",
		"[INFO] child-a ............................................ SUCCESS",
		"[INFO] child-b ............................................ SUCCESS",
		"BUILD SUCCESS",
	} {
		if !strings.Contains(o, want) {
			t.Errorf("missing %q:\n%s", want, o)
		}
	}
}

func TestReactorSummaryKeptOnMultiModuleFail(t *testing.T) {
	i := readFixture(t, "mvn_reactor_fail_slice_raw.txt")
	o := filterPackage(i)
	for _, want := range []string{
		"Reactor Summary for multi-module-skeleton",
		"child-a ............................................ SUCCESS",
		"child-b ............................................ FAILURE",
		"BUILD FAILURE",
		"[ERROR] Failed to execute goal",
		"mvn <args> -rf :child-b",
	} {
		if !strings.Contains(o, want) {
			t.Errorf("missing %q:\n%s", want, o)
		}
	}
	if strings.Contains(o, "[Help 1]") || strings.Contains(o, "Re-run Maven") {
		t.Errorf("help boilerplate should be stripped:\n%s", o)
	}
	savings := 100.0 - (float64(countTokens(o))/float64(countTokens(i)))*100.0
	if savings < 30.0 {
		t.Errorf("expected >=30%% savings, got %.1f%%", savings)
	}
}

// ── Compile filter ───────────────────────────────────────────────────────────

func TestFilterCompileErrorCompact(t *testing.T) {
	i := readFixture(t, "mvn_compile_error_slice_raw.txt")
	o := filterCompile(i)
	savings := 100.0 - (float64(countTokens(o))/float64(countTokens(i)))*100.0
	if savings < 30.0 {
		t.Errorf("expected >=30%% savings, got %.1f%%", savings)
	}
}

func TestCompilePreservesErrorContinuation(t *testing.T) {
	i := readFixture(t, "mvn_compile_error_slice_raw.txt")
	o := filterCompile(i)
	if !strings.Contains(o, "cannot find symbol") ||
		!strings.Contains(o, "symbol:   variable bar") ||
		!strings.Contains(o, "BUILD FAILURE") {
		t.Errorf("error continuation not preserved:\n%s", o)
	}
	if strings.Contains(o, "[Help 1]") {
		t.Errorf("help boilerplate should be stripped:\n%s", o)
	}
}

func TestCompileDedupesWarnings(t *testing.T) {
	i := "[INFO] -----< x >-----\n" +
		"[WARNING] /a.java:[1,2] uses deprecated API\n" +
		"[WARNING] /b.java:[3,4] uses deprecated API\n" +
		"[WARNING] /a.java:[5,6] unchecked cast\n" +
		"[INFO] BUILD SUCCESS\n"
	o := filterCompile(i)
	warns := strings.Count(o, "[WARNING]")
	if warns != 2 {
		t.Errorf("expected 2 deduped warnings, got %d:\n%s", warns, o)
	}
}

// ── Package filter ───────────────────────────────────────────────────────────

func TestFilterPackageInstallCompact(t *testing.T) {
	i := readFixture(t, "mvn_install_slice_raw.txt")
	o := filterPackage(i)
	savings := 100.0 - (float64(countTokens(o))/float64(countTokens(i)))*100.0
	if savings < 50.0 {
		t.Errorf("expected >=50%% savings, got %.1f%%", savings)
	}
}

func TestPackageKeepsInstallLines(t *testing.T) {
	i := readFixture(t, "mvn_install_slice_raw.txt")
	o := filterPackage(i)
	if !strings.Contains(o, "Installing") || !strings.Contains(o, "Building jar:") {
		t.Errorf("install/jar lines not preserved:\n%s", o)
	}
	if strings.Contains(o, "at org.junit.") {
		t.Errorf("framework frames should be stripped:\n%s", o)
	}
}

// ── Token-savings (full gzipped fixtures) ────────────────────────────────────

func TestSavingsMvnTestPassFull(t *testing.T) {
	i := gunzipFixture(t, "mvn_test_pass_full_raw.txt.gz")
	o := filterSurefire(i)
	savings := 100.0 - (float64(countTokens(o))/float64(countTokens(i)))*100.0
	if savings < 90.0 {
		t.Errorf("expected >=90%% savings, got %.1f%% (raw=%d filtered=%d)", savings, countTokens(i), countTokens(o))
	}
}

func TestSavingsMvnInstallFull(t *testing.T) {
	i := gunzipFixture(t, "mvn_install_full_raw.txt.gz")
	o := filterPackage(i)
	savings := 100.0 - (float64(countTokens(o))/float64(countTokens(i)))*100.0
	if savings < 85.0 {
		t.Errorf("expected >=85%% savings, got %.1f%% (raw=%d filtered=%d)", savings, countTokens(i), countTokens(o))
	}
}

// ── Quiet mode ───────────────────────────────────────────────────────────────

func TestQuietDetectsShortFlag(t *testing.T) {
	if !isQuiet(sArgs("-q", "test")) || !isQuiet(sArgs("test", "-q")) || !isQuiet(sArgs("-B", "-q", "-DskipFoo", "install")) {
		t.Error("expected -q detected")
	}
}

func TestQuietDetectsLongFlag(t *testing.T) {
	if !isQuiet(sArgs("--quiet", "test")) {
		t.Error("expected --quiet detected")
	}
}

func TestQuietDoesNotMatchUnrelatedFlags(t *testing.T) {
	if isQuiet(sArgs("-Q", "test")) || isQuiet(sArgs("-quiet", "test")) || isQuiet(sArgs("-B", "test")) {
		t.Error("did not expect detection")
	}
}

func TestQuietGreenRunIsEmpty(t *testing.T) {
	if filterQuiet("") != "" {
		t.Error("empty input should yield empty")
	}
	if filterQuiet("   \n\n  \n") != "" {
		t.Error("blank input should yield empty")
	}
}

func TestQuietFailStripsFrameworkAndBoilerplate(t *testing.T) {
	i := readFixture(t, "mvn_quiet_fail_raw.txt")
	o := filterQuiet(i)
	for _, want := range []string{
		"Tests run: 1, Failures: 1, Errors: 0, Skipped: 0",
		"AssertionFailedError",
		"at x.FailTest.this_will_fail",
		"[ERROR] Failures:",
		"[ERROR] Tests run: 6, Failures: 1, Errors: 0, Skipped: 0",
		"[ERROR] Failed to execute goal",
	} {
		if !strings.Contains(o, want) {
			t.Errorf("missing %q:\n%s", want, o)
		}
	}
	for _, drop := range []string{"at org.junit.", "at java.base/", "To see the full stack trace", "[Help 1] http", "See /tmp/", "See dump files"} {
		if strings.Contains(o, drop) {
			t.Errorf("%q should be stripped:\n%s", drop, o)
		}
	}
}

func TestSavingsMvnQuietFail(t *testing.T) {
	i := readFixture(t, "mvn_quiet_fail_raw.txt")
	o := filterQuiet(i)
	savings := 100.0 - (float64(countTokens(o))/float64(countTokens(i)))*100.0
	if savings < 50.0 {
		t.Errorf("expected >=50%% savings, got %.1f%%", savings)
	}
}

func TestQuietUnknownErrorLineKeptAsSafetyNet(t *testing.T) {
	i := "[ERROR] Some unexpected error output we don't classify\n"
	o := filterQuiet(i)
	if !strings.Contains(o, "Some unexpected error output") {
		t.Errorf("unclassified ERROR line should be preserved:\n%s", o)
	}
}

// itoa is a tiny int->decimal-string helper for building synthetic fixtures.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	if neg {
		d = append([]byte{'-'}, d...)
	}
	return string(d)
}
