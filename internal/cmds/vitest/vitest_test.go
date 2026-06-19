package vitest

import (
	"strings"
	"testing"

	"gortk/internal/core"
)

// --- Ported from rtk src/cmds/js/vitest_cmd.rs #[cfg(test)] ------------------

func TestVitestParserJSON(t *testing.T) {
	json := `{
		"numTotalTests": 13,
		"numPassedTests": 13,
		"numFailedTests": 0,
		"numPendingTests": 0,
		"testResults": [],
		"startTime": 1000
	}`
	r := parseVitest(json)
	if r.tier != tierFull {
		t.Fatalf("tier = %d, want %d (Full)", r.tier, tierFull)
	}
	if r.data.total != 13 || r.data.passed != 13 || r.data.failed != 0 {
		t.Errorf("got total=%d passed=%d failed=%d", r.data.total, r.data.passed, r.data.failed)
	}
	if r.data.hasDuration {
		t.Errorf("duration should be unset, got %dms", r.data.durationMS)
	}
}

func TestVitestParserRegexFallback(t *testing.T) {
	text := "" +
		"\n Test Files  2 passed (2)\n" +
		"      Tests  13 passed (13)\n" +
		"   Duration  450ms\n        "
	r := parseVitest(text)
	if r.tier != tierDegraded {
		t.Fatalf("tier = %d, want %d (Degraded)", r.tier, tierDegraded)
	}
	if r.data.passed != 13 || r.data.failed != 0 {
		t.Errorf("got passed=%d failed=%d", r.data.passed, r.data.failed)
	}
}

func TestVitestParserPassthrough(t *testing.T) {
	r := parseVitest("random output with no structure")
	if r.tier != tierPassthrough {
		t.Fatalf("tier = %d, want %d (Passthrough)", r.tier, tierPassthrough)
	}
}

func TestStripANSI(t *testing.T) {
	input := "\x1b[32m✓\x1b[0m test passed"
	output := core_StripANSI(input)
	if output != "✓ test passed" {
		t.Errorf("StripANSI = %q, want %q", output, "✓ test passed")
	}
	if strings.Contains(output, "\x1b") {
		t.Errorf("output still contains escape: %q", output)
	}
}

func TestVitestParserWithPnpmPrefix(t *testing.T) {
	input := `
Scope: all 6 workspace projects
 WARN  deprecated inflight@1.0.6: This module is not supported

{"numTotalTests": 13, "numPassedTests": 13, "numFailedTests": 0, "numPendingTests": 0, "testResults": [], "startTime": 1000}
`
	r := parseVitest(input)
	if r.tier != tierFull {
		t.Fatalf("tier = %d, want %d (Full) for pnpm prefix", r.tier, tierFull)
	}
	if r.data.total != 13 || r.data.passed != 13 || r.data.failed != 0 {
		t.Errorf("got total=%d passed=%d failed=%d", r.data.total, r.data.passed, r.data.failed)
	}
}

func TestVitestParserWithDotenvPrefix(t *testing.T) {
	input := `[dotenv] Loading environment variables from .env
[dotenv] Injected 5 variables

{"numTotalTests": 5, "numPassedTests": 4, "numFailedTests": 1, "numPendingTests": 0, "testResults": [], "startTime": 2000}
`
	r := parseVitest(input)
	if r.tier != tierFull {
		t.Fatalf("tier = %d, want %d (Full) for dotenv prefix", r.tier, tierFull)
	}
	if r.data.total != 5 || r.data.passed != 4 || r.data.failed != 1 {
		t.Errorf("got total=%d passed=%d failed=%d", r.data.total, r.data.passed, r.data.failed)
	}
	if r.data.hasDuration {
		t.Errorf("duration should be unset")
	}
}

func TestVitestParserWithNestedJSON(t *testing.T) {
	input := `prefix text
{"numTotalTests": 2, "numPassedTests": 2, "numFailedTests": 0, "numPendingTests": 0, "testResults": [{"name": "test.js", "assertionResults": [{"fullName": "nested test", "status": "passed", "failureMessages": []}]}], "startTime": 1000}
`
	r := parseVitest(input)
	if r.tier != tierFull {
		t.Fatalf("tier = %d, want %d (Full) for nested json", r.tier, tierFull)
	}
	if r.data.total != 2 || r.data.passed != 2 {
		t.Errorf("got total=%d passed=%d", r.data.total, r.data.passed)
	}
}

// --- Ported from rtk src/parser/mod.rs #[cfg(test)] -------------------------

func TestExtractJSONObjectClean(t *testing.T) {
	input := `{"numTotalTests": 13, "numPassedTests": 13}`
	got, ok := extractJSONObject(input)
	if !ok || got != input {
		t.Errorf("extractJSONObject = %q,%v want %q", got, ok, input)
	}
}

func TestExtractJSONObjectWithPnpmPrefix(t *testing.T) {
	input := `
Scope: all 6 workspace projects
 WARN  deprecated inflight@1.0.6: This module is not supported

{"numTotalTests": 13, "numPassedTests": 13, "numFailedTests": 0}
`
	got, ok := extractJSONObject(input)
	if !ok {
		t.Fatalf("should extract JSON")
	}
	if !strings.Contains(got, "numTotalTests") || !strings.HasPrefix(got, "{") || !strings.HasSuffix(got, "}") {
		t.Errorf("bad extraction: %q", got)
	}
}

func TestExtractJSONObjectWithDotenvPrefix(t *testing.T) {
	input := `[dotenv] Loading environment variables from .env
[dotenv] Injected 5 variables

{"numTotalTests": 5, "testResults": [{"name": "test.js"}]}
`
	got, ok := extractJSONObject(input)
	if !ok {
		t.Fatalf("should extract JSON")
	}
	if !strings.Contains(got, "numTotalTests") || !strings.Contains(got, "testResults") {
		t.Errorf("bad extraction: %q", got)
	}
}

func TestExtractJSONObjectNestedBraces(t *testing.T) {
	input := `prefix text
{"numTotalTests": 2, "testResults": [{"name": "test", "data": {"nested": true}}]}
`
	got, ok := extractJSONObject(input)
	if !ok {
		t.Fatalf("should extract JSON")
	}
	if !strings.Contains(got, `"nested": true`) || !strings.HasPrefix(got, "{") || !strings.HasSuffix(got, "}") {
		t.Errorf("bad extraction: %q", got)
	}
}

func TestExtractJSONObjectNoJSON(t *testing.T) {
	if got, ok := extractJSONObject("Just plain text with no JSON"); ok {
		t.Errorf("want no extraction, got %q", got)
	}
}

func TestExtractJSONObjectStringWithBraces(t *testing.T) {
	input := `{"numTotalTests": 1, "message": "test {should} not confuse parser"}`
	got, ok := extractJSONObject(input)
	if !ok {
		t.Fatalf("should extract JSON")
	}
	if !strings.Contains(got, "test {should} not confuse parser") || got != input {
		t.Errorf("bad extraction: %q", got)
	}
}

func TestTruncateOutput(t *testing.T) {
	if got := truncateOutput("hello", 10); got != "hello" {
		t.Errorf("truncateOutput short = %q, want %q", got, "hello")
	}
	long := strings.Repeat("a", 1000)
	got := truncateOutput(long, 100)
	if !strings.Contains(got, "[GORTK:PASSTHROUGH]") {
		t.Errorf("missing passthrough marker: %q", got)
	}
	if !strings.Contains(got, "1000 chars → 100 chars") {
		t.Errorf("missing char counts: %q", got)
	}
}

func TestTruncateOutputMultibyte(t *testing.T) {
	thai := strings.Repeat("สวัสดีครับ", 100)
	got := truncateOutput(thai, 50)
	if !strings.Contains(got, "[GORTK:PASSTHROUGH]") {
		t.Errorf("missing passthrough marker")
	}
	_ = len(got)
}

func TestTruncateOutputEmoji(t *testing.T) {
	emoji := strings.Repeat("🎉", 200)
	got := truncateOutput(emoji, 100)
	if !strings.Contains(got, "[GORTK:PASSTHROUGH]") {
		t.Errorf("missing passthrough marker")
	}
}

// --- Ported from rtk src/parser/formatter.rs #[cfg(test)] -------------------

func makeFailure(name, errMsg string) testFailure {
	return testFailure{
		testName:     name,
		filePath:     "tests/e2e.spec.ts",
		errorMessage: errMsg,
	}
}

func makeResult(passed int, failures []testFailure) testResult {
	return testResult{
		total:       passed + len(failures),
		passed:      passed,
		failed:      len(failures),
		skipped:     0,
		durationMS:  1500,
		hasDuration: true,
		failures:    failures,
	}
}

func TestCompactShowsFullErrorMessage(t *testing.T) {
	errMsg := "Error: expect(locator).toHaveText(expected)\n\nExpected: 'Submit'\nReceived: 'Loading'\n\nCall log:\n  - waiting for getByRole('button', { name: 'Submit' })"
	r := makeResult(5, []testFailure{makeFailure("should click submit", errMsg)})
	out := formatTestCompact(r)
	for _, want := range []string{"Expected: 'Submit'", "Received: 'Loading'", "Call log:"} {
		if !strings.Contains(out, want) {
			t.Errorf("compact must preserve %q\nGot:\n%s", want, out)
		}
	}
}

func TestCompactSummaryLineIsConcise(t *testing.T) {
	r := makeResult(28, []testFailure{makeFailure("test", "some error")})
	out := formatTestCompact(r)
	first := splitLines(out)[0]
	if !strings.Contains(first, "28") || !strings.Contains(first, "1") {
		t.Errorf("first line must show pass/fail counts, got: %s", first)
	}
}

func TestCompactAllPassIsOneLine(t *testing.T) {
	r := makeResult(10, nil)
	out := formatTestCompact(r)
	if n := len(splitLines(out)); n > 3 {
		t.Errorf("all-pass output should be compact, got %d lines:\n%s", n, out)
	}
}

func TestCompactSingleLineErrorNoTrailingNoise(t *testing.T) {
	r := makeResult(0, []testFailure{makeFailure("should work", "Timeout exceeded")})
	out := formatTestCompact(r)
	if !strings.Contains(out, "Timeout exceeded") {
		t.Errorf("single-line error must appear\nGot:\n%s", out)
	}
}

// core_StripANSI keeps the strip-ansi test self-contained against the core
// helper that backs extractStatsRegex.
func core_StripANSI(s string) string { return core.StripANSI(s) }
