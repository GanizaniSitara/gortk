package playwright

import (
	"strings"
	"testing"
)

// --- parser tests (ported from playwright_cmd.rs #[cfg(test)]) --------------

func TestPlaywrightParserJSON(t *testing.T) {
	json := `{
		"config": {},
		"stats": {
			"startTime": "2026-01-01T00:00:00.000Z",
			"expected": 1,
			"unexpected": 0,
			"skipped": 0,
			"flaky": 0,
			"duration": 7300.5
		},
		"suites": [
			{
				"title": "auth",
				"specs": [],
				"suites": [
					{
						"title": "login.spec.ts",
						"specs": [
							{
								"title": "should login",
								"ok": true,
								"tests": [
									{
										"status": "expected",
										"results": [{"status": "passed", "errors": [], "duration": 2300}]
									}
								]
							}
						],
						"suites": []
					}
				]
			}
		],
		"errors": []
	}`

	res := parsePlaywright(json)
	if res.tier != tierFull {
		t.Fatalf("tier = %d, want %d", res.tier, tierFull)
	}
	if res.data.Passed != 1 {
		t.Errorf("passed = %d, want 1", res.data.Passed)
	}
	if res.data.Failed != 0 {
		t.Errorf("failed = %d, want 0", res.data.Failed)
	}
	if res.data.DurationMS == nil || *res.data.DurationMS != 7300 {
		t.Errorf("duration = %v, want 7300", res.data.DurationMS)
	}
}

func TestPlaywrightParserJSONFloatDuration(t *testing.T) {
	json := `{
		"stats": {
			"startTime": "2026-02-18T10:17:53.187Z",
			"expected": 4,
			"unexpected": 0,
			"skipped": 0,
			"flaky": 0,
			"duration": 3519.7039999999997
		},
		"suites": [],
		"errors": []
	}`

	res := parsePlaywright(json)
	if res.tier != tierFull {
		t.Fatalf("tier = %d, want %d", res.tier, tierFull)
	}
	if res.data.Passed != 4 {
		t.Errorf("passed = %d, want 4", res.data.Passed)
	}
	if res.data.DurationMS == nil || *res.data.DurationMS != 3519 {
		t.Errorf("duration = %v, want 3519", res.data.DurationMS)
	}
}

func TestPlaywrightParserJSONWithFailure(t *testing.T) {
	json := `{
		"stats": {
			"expected": 0,
			"unexpected": 1,
			"skipped": 0,
			"duration": 1500.0
		},
		"suites": [
			{
				"title": "my.spec.ts",
				"specs": [
					{
						"title": "should work",
						"ok": false,
						"tests": [
							{
								"status": "unexpected",
								"results": [
									{
										"status": "failed",
										"errors": [{"message": "Expected true to be false"}],
										"duration": 500
									}
								]
							}
						]
					}
				],
				"suites": []
			}
		],
		"errors": []
	}`

	res := parsePlaywright(json)
	if res.tier != tierFull {
		t.Fatalf("tier = %d, want %d", res.tier, tierFull)
	}
	if res.data.Failed != 1 {
		t.Errorf("failed = %d, want 1", res.data.Failed)
	}
	if len(res.data.Failures) != 1 {
		t.Fatalf("failures = %d, want 1", len(res.data.Failures))
	}
	if res.data.Failures[0].TestName != "should work" {
		t.Errorf("test_name = %q, want %q", res.data.Failures[0].TestName, "should work")
	}
	if res.data.Failures[0].ErrorMessage != "Expected true to be false" {
		t.Errorf("error_message = %q, want %q", res.data.Failures[0].ErrorMessage, "Expected true to be false")
	}
}

func TestPlaywrightParserRegexFallback(t *testing.T) {
	text := "3 passed (7.3s)"
	res := parsePlaywright(text)
	if res.tier != tierDegraded {
		t.Fatalf("tier = %d, want %d (degraded)", res.tier, tierDegraded)
	}
	if !res.is_ok() {
		t.Errorf("degraded result should be ok")
	}
	if res.data.Passed != 3 {
		t.Errorf("passed = %d, want 3", res.data.Passed)
	}
	if res.data.Failed != 0 {
		t.Errorf("failed = %d, want 0", res.data.Failed)
	}
}

func TestPlaywrightParserPassthrough(t *testing.T) {
	res := parsePlaywright("random output")
	if res.tier != tierPassthrough {
		t.Fatalf("tier = %d, want %d (passthrough)", res.tier, tierPassthrough)
	}
	if res.is_ok() {
		t.Errorf("passthrough result should not be ok")
	}
}

// is_ok mirrors ParseResult::is_ok (Full or Degraded).
func (p parseOutcome) is_ok() bool { return p.tier != tierPassthrough }

// --- formatter tests (ported from parser/formatter.rs #[cfg(test)]) --------

func makeFailure(name, errMsg string) testFailure {
	return testFailure{
		TestName:     name,
		FilePath:     "tests/e2e.spec.ts",
		ErrorMessage: errMsg,
	}
}

func makeResult(passed int, failures []testFailure) testResult {
	dur := uint64(1500)
	return testResult{
		Total:      passed + len(failures),
		Passed:     passed,
		Failed:     len(failures),
		Skipped:    0,
		DurationMS: &dur,
		Failures:   failures,
	}
}

func TestCompactShowsFullErrorMessage(t *testing.T) {
	errMsg := "Error: expect(locator).toHaveText(expected)\n\nExpected: 'Submit'\nReceived: 'Loading'\n\nCall log:\n  - waiting for getByRole('button', { name: 'Submit' })"
	result := makeResult(5, []testFailure{makeFailure("should click submit", errMsg)})

	output := formatCompact(result)

	for _, want := range []string{"Expected: 'Submit'", "Received: 'Loading'", "Call log:"} {
		if !strings.Contains(output, want) {
			t.Errorf("format_compact must preserve %q\nGot:\n%s", want, output)
		}
	}
}

func TestCompactSummaryLineIsConcise(t *testing.T) {
	result := makeResult(28, []testFailure{makeFailure("test", "some error")})
	output := formatCompact(result)
	firstLine := strings.SplitN(output, "\n", 2)[0]
	if !strings.Contains(firstLine, "28") || !strings.Contains(firstLine, "1") {
		t.Errorf("first line must show pass/fail counts, got: %s", firstLine)
	}
}

func TestCompactAllPassIsOneLine(t *testing.T) {
	result := makeResult(10, nil)
	output := formatCompact(result)
	if n := len(splitLines(output)); n > 3 {
		t.Errorf("all-pass output should be compact, got %d lines:\n%s", n, output)
	}
}

func TestCompactSingleLineErrorNoTrailingNoise(t *testing.T) {
	result := makeResult(0, []testFailure{makeFailure("should work", "Timeout exceeded")})
	output := formatCompact(result)
	if !strings.Contains(output, "Timeout exceeded") {
		t.Errorf("single-line error must appear\nGot:\n%s", output)
	}
}

// --- truncate tests (ported from parser/mod.rs #[cfg(test)]) ----------------

func TestTruncateOutput(t *testing.T) {
	if got := truncateOutput("hello", 10); got != "hello" {
		t.Errorf("truncateOutput short = %q, want %q", got, "hello")
	}

	long := strings.Repeat("a", 1000)
	truncated := truncateOutput(long, 100)
	if !strings.Contains(truncated, "[gortk:PASSTHROUGH]") {
		t.Errorf("truncated output missing marker:\n%s", truncated)
	}
	if !strings.Contains(truncated, "1000 chars → 100 chars") {
		t.Errorf("truncated output missing char counts:\n%s", truncated)
	}
}

func TestTruncateOutputMultibyte(t *testing.T) {
	thai := strings.Repeat("สวัสดีครับ", 100)
	result := truncateOutput(thai, 50)
	if !strings.Contains(result, "[gortk:PASSTHROUGH]") {
		t.Errorf("missing marker for multibyte input")
	}
	_ = len(result)
}

func TestTruncateOutputEmoji(t *testing.T) {
	emoji := strings.Repeat("🎉", 200)
	result := truncateOutput(emoji, 100)
	if !strings.Contains(result, "[gortk:PASSTHROUGH]") {
		t.Errorf("missing marker for emoji input")
	}
}
