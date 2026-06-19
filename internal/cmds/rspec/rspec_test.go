package rspec

import (
	"strings"
	"testing"

	"gortk/internal/core"
)

// Faithful port of the #[cfg(test)] mod tests block in rtk's
// src/cmds/ruby/rspec_cmd.rs. The pure filter/strip functions are exercised
// directly. Token-savings assertions use gortk's character-based
// core.EstimateTokens in place of rtk's word-count count_tokens; both agree the
// filters are large net reductions, so the thresholds hold.

func savingsPct(input, output string) float64 {
	in := core.EstimateTokens(input)
	out := core.EstimateTokens(output)
	if in == 0 {
		return 0
	}
	return 100.0 - (float64(out) / float64(in) * 100.0)
}

const allPassJSON = `{
          "version": "3.12.0",
          "examples": [
            {
              "id": "./spec/models/user_spec.rb[1:1]",
              "description": "is valid with valid attributes",
              "full_description": "User is valid with valid attributes",
              "status": "passed",
              "file_path": "./spec/models/user_spec.rb",
              "line_number": 5,
              "run_time": 0.001234,
              "pending_message": null,
              "exception": null
            },
            {
              "id": "./spec/models/user_spec.rb[1:2]",
              "description": "validates email format",
              "full_description": "User validates email format",
              "status": "passed",
              "file_path": "./spec/models/user_spec.rb",
              "line_number": 12,
              "run_time": 0.0008,
              "pending_message": null,
              "exception": null
            }
          ],
          "summary": {
            "duration": 0.015,
            "example_count": 2,
            "failure_count": 0,
            "pending_count": 0,
            "errors_outside_of_examples_count": 0
          },
          "summary_line": "2 examples, 0 failures"
        }`

const withFailuresJSON = `{
          "version": "3.12.0",
          "examples": [
            {
              "id": "./spec/models/user_spec.rb[1:1]",
              "description": "is valid",
              "full_description": "User is valid",
              "status": "passed",
              "file_path": "./spec/models/user_spec.rb",
              "line_number": 5,
              "run_time": 0.001,
              "pending_message": null,
              "exception": null
            },
            {
              "id": "./spec/models/user_spec.rb[1:2]",
              "description": "saves to database",
              "full_description": "User saves to database",
              "status": "failed",
              "file_path": "./spec/models/user_spec.rb",
              "line_number": 10,
              "run_time": 0.002,
              "pending_message": null,
              "exception": {
                "class": "RSpec::Expectations::ExpectationNotMetError",
                "message": "expected true but got false",
                "backtrace": [
                  "/usr/local/lib/ruby/gems/3.2.0/gems/rspec-expectations-3.12.0/lib/rspec/expectations/fail_with.rb:37:in ` + "`fail_with'" + `",
                  "./spec/models/user_spec.rb:11:in ` + "`block (2 levels) in <top (required)>'" + `"
                ]
              }
            }
          ],
          "summary": {
            "duration": 0.123,
            "example_count": 2,
            "failure_count": 1,
            "pending_count": 0,
            "errors_outside_of_examples_count": 0
          },
          "summary_line": "2 examples, 1 failure"
        }`

const withPendingJSON = `{
          "version": "3.12.0",
          "examples": [
            {
              "id": "./spec/models/post_spec.rb[1:1]",
              "description": "creates a post",
              "full_description": "Post creates a post",
              "status": "passed",
              "file_path": "./spec/models/post_spec.rb",
              "line_number": 4,
              "run_time": 0.002,
              "pending_message": null,
              "exception": null
            },
            {
              "id": "./spec/models/post_spec.rb[1:2]",
              "description": "validates title",
              "full_description": "Post validates title",
              "status": "pending",
              "file_path": "./spec/models/post_spec.rb",
              "line_number": 8,
              "run_time": 0.0,
              "pending_message": "Not yet implemented",
              "exception": null
            }
          ],
          "summary": {
            "duration": 0.05,
            "example_count": 2,
            "failure_count": 0,
            "pending_count": 1,
            "errors_outside_of_examples_count": 0
          },
          "summary_line": "2 examples, 0 failures, 1 pending"
        }`

const largeSuiteJSON = `{
          "version": "3.12.0",
          "examples": [
            {"id":"1","description":"test1","full_description":"Suite test1","status":"passed","file_path":"./spec/a_spec.rb","line_number":1,"run_time":0.01,"pending_message":null,"exception":null},
            {"id":"2","description":"test2","full_description":"Suite test2","status":"passed","file_path":"./spec/a_spec.rb","line_number":2,"run_time":0.01,"pending_message":null,"exception":null},
            {"id":"3","description":"test3","full_description":"Suite test3","status":"passed","file_path":"./spec/a_spec.rb","line_number":3,"run_time":0.01,"pending_message":null,"exception":null},
            {"id":"4","description":"test4","full_description":"Suite test4","status":"passed","file_path":"./spec/a_spec.rb","line_number":4,"run_time":0.01,"pending_message":null,"exception":null},
            {"id":"5","description":"test5","full_description":"Suite test5","status":"passed","file_path":"./spec/a_spec.rb","line_number":5,"run_time":0.01,"pending_message":null,"exception":null},
            {"id":"6","description":"test6","full_description":"Suite test6","status":"passed","file_path":"./spec/a_spec.rb","line_number":6,"run_time":0.01,"pending_message":null,"exception":null},
            {"id":"7","description":"test7","full_description":"Suite test7","status":"passed","file_path":"./spec/a_spec.rb","line_number":7,"run_time":0.01,"pending_message":null,"exception":null},
            {"id":"8","description":"test8","full_description":"Suite test8","status":"passed","file_path":"./spec/a_spec.rb","line_number":8,"run_time":0.01,"pending_message":null,"exception":null},
            {"id":"9","description":"test9","full_description":"Suite test9","status":"passed","file_path":"./spec/a_spec.rb","line_number":9,"run_time":0.01,"pending_message":null,"exception":null},
            {"id":"10","description":"test10","full_description":"Suite test10","status":"passed","file_path":"./spec/a_spec.rb","line_number":10,"run_time":0.01,"pending_message":null,"exception":null}
          ],
          "summary": {
            "duration": 1.234,
            "example_count": 10,
            "failure_count": 0,
            "pending_count": 0,
            "errors_outside_of_examples_count": 0
          },
          "summary_line": "10 examples, 0 failures"
        }`

func TestFilterRspecAllPass(t *testing.T) {
	result := filterRspecOutput(allPassJSON)
	if !strings.HasPrefix(result, "✓ RSpec:") {
		t.Errorf("bad prefix: %s", result)
	}
	if !strings.Contains(result, "2 passed") {
		t.Errorf("missing '2 passed': %s", result)
	}
	if !strings.Contains(result, "0.01s") && !strings.Contains(result, "0.02s") {
		t.Errorf("missing duration: %s", result)
	}
}

func TestFilterRspecWithFailures(t *testing.T) {
	result := filterRspecOutput(withFailuresJSON)
	for _, want := range []string{
		"1 passed, 1 failed", "✗ User saves to database", "user_spec.rb:10",
		"ExpectationNotMetError", "expected true but got false",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q in: %s", want, result)
		}
	}
}

func TestFilterRspecWithPending(t *testing.T) {
	result := filterRspecOutput(withPendingJSON)
	if !strings.HasPrefix(result, "✓ RSpec:") {
		t.Errorf("bad prefix: %s", result)
	}
	if !strings.Contains(result, "1 passed") || !strings.Contains(result, "1 pending") {
		t.Errorf("unexpected: %s", result)
	}
}

func TestFilterRspecEmptyOutput(t *testing.T) {
	if got := filterRspecOutput(""); got != "RSpec: No output" {
		t.Errorf("got %q", got)
	}
}

func TestFilterRspecNoExamples(t *testing.T) {
	json := `{
          "version": "3.12.0",
          "examples": [],
          "summary": {
            "duration": 0.001,
            "example_count": 0,
            "failure_count": 0,
            "pending_count": 0,
            "errors_outside_of_examples_count": 0
          }
        }`
	if got := filterRspecOutput(json); got != "RSpec: No examples found" {
		t.Errorf("got %q", got)
	}
}

func TestFilterRspecErrorsOutsideExamples(t *testing.T) {
	json := `{
          "version": "3.12.0",
          "examples": [],
          "summary": {
            "duration": 0.01,
            "example_count": 0,
            "failure_count": 0,
            "pending_count": 0,
            "errors_outside_of_examples_count": 1
          }
        }`
	result := filterRspecOutput(json)
	if strings.Contains(result, "No examples found") {
		t.Errorf("errors outside examples should not be 'no examples': %s", result)
	}
}

func TestFilterRspecTextFallback(t *testing.T) {
	text := `
..F.

Failures:

  1) User is valid
     Failure/Error: expect(user).to be_valid
       expected true got false
     # ./spec/models/user_spec.rb:5

4 examples, 1 failure
`
	result := filterRspecOutput(text)
	for _, want := range []string{"RSpec:", "4 examples, 1 failure", "✗"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q in: %s", want, result)
		}
	}
}

func TestFilterRspecTextFallbackExtractsFailures(t *testing.T) {
	text := `Randomized with seed 12345
..F...E..

Failures:

  1) User#full_name returns first and last name
     Failure/Error: expect(user.full_name).to eq("John Doe")
       expected: "John Doe"
            got: "John D."
     # /usr/local/lib/ruby/gems/3.2.0/gems/rspec-expectations-3.12.0/lib/rspec/expectations/fail_with.rb:37
     # ./spec/models/user_spec.rb:15

  2) Api::Controller#index fails
     Failure/Error: get :index
       expected 200 got 500
     # ./spec/controllers/api_spec.rb:42

9 examples, 2 failures
`
	result := filterRspecText(text)
	for _, want := range []string{"2 failures", "✗", "spec/models/user_spec.rb:15"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q in: %s", want, result)
		}
	}
}

func TestFilterRspecBacktraceFiltersGems(t *testing.T) {
	result := filterRspecOutput(withFailuresJSON)
	if !strings.Contains(result, "user_spec.rb:11") {
		t.Errorf("missing spec backtrace: %s", result)
	}
	if strings.Contains(result, "gems/rspec-expectations") {
		t.Errorf("gem backtrace leaked: %s", result)
	}
}

func TestFilterRspecExceptionClassShortened(t *testing.T) {
	result := filterRspecOutput(withFailuresJSON)
	if !strings.Contains(result, "ExpectationNotMetError") {
		t.Errorf("missing short class: %s", result)
	}
	if strings.Contains(result, "RSpec::Expectations::ExpectationNotMetError") {
		t.Errorf("full class name leaked: %s", result)
	}
}

func TestFilterRspecManyFailuresCapsAtFive(t *testing.T) {
	json := `{
          "version": "3.12.0",
          "examples": [
            {"id":"1","description":"test 1","full_description":"A test 1","status":"failed","file_path":"./spec/a_spec.rb","line_number":5,"run_time":0.001,"pending_message":null,"exception":{"class":"RuntimeError","message":"boom 1","backtrace":["./spec/a_spec.rb:6:in ` + "`block'" + `"]}},
            {"id":"2","description":"test 2","full_description":"A test 2","status":"failed","file_path":"./spec/a_spec.rb","line_number":10,"run_time":0.001,"pending_message":null,"exception":{"class":"RuntimeError","message":"boom 2","backtrace":["./spec/a_spec.rb:11:in ` + "`block'" + `"]}},
            {"id":"3","description":"test 3","full_description":"A test 3","status":"failed","file_path":"./spec/a_spec.rb","line_number":15,"run_time":0.001,"pending_message":null,"exception":{"class":"RuntimeError","message":"boom 3","backtrace":["./spec/a_spec.rb:16:in ` + "`block'" + `"]}},
            {"id":"4","description":"test 4","full_description":"A test 4","status":"failed","file_path":"./spec/a_spec.rb","line_number":20,"run_time":0.001,"pending_message":null,"exception":{"class":"RuntimeError","message":"boom 4","backtrace":["./spec/a_spec.rb:21:in ` + "`block'" + `"]}},
            {"id":"5","description":"test 5","full_description":"A test 5","status":"failed","file_path":"./spec/a_spec.rb","line_number":25,"run_time":0.001,"pending_message":null,"exception":{"class":"RuntimeError","message":"boom 5","backtrace":["./spec/a_spec.rb:26:in ` + "`block'" + `"]}},
            {"id":"6","description":"test 6","full_description":"A test 6","status":"failed","file_path":"./spec/a_spec.rb","line_number":30,"run_time":0.001,"pending_message":null,"exception":{"class":"RuntimeError","message":"boom 6","backtrace":["./spec/a_spec.rb:31:in ` + "`block'" + `"]}}
          ],
          "summary": {
            "duration": 0.05,
            "example_count": 6,
            "failure_count": 6,
            "pending_count": 0,
            "errors_outside_of_examples_count": 0
          },
          "summary_line": "6 examples, 6 failures"
        }`
	result := filterRspecOutput(json)
	if !strings.Contains(result, "1. ✗") {
		t.Errorf("missing first failure: %s", result)
	}
	if !strings.Contains(result, "5. ✗") {
		t.Errorf("missing fifth failure: %s", result)
	}
	if strings.Contains(result, "6. ✗") {
		t.Errorf("should not show sixth inline: %s", result)
	}
	if !strings.Contains(result, "+1 more") {
		t.Errorf("missing overflow count: %s", result)
	}
}

func TestFilterRspecTextFallbackNoSummary(t *testing.T) {
	result := filterRspecOutput("some output\nwithout a summary line")
	if result == "" {
		t.Error("should not be empty")
	}
}

func TestFilterRspecInvalidJSONFallsBack(t *testing.T) {
	result := filterRspecOutput("not json at all { broken")
	if result == "" {
		t.Error("should not panic/empty on invalid JSON")
	}
}

// ── Noise stripping tests ────────────────────────────────────────────────

func TestStripNoiseSpring(t *testing.T) {
	input := "Running via Spring preloader in process 12345\n...\n3 examples, 0 failures"
	result := stripNoise(input)
	if strings.Contains(result, "Spring") {
		t.Errorf("Spring not stripped: %s", result)
	}
	if !strings.Contains(result, "3 examples") {
		t.Errorf("missing summary: %s", result)
	}
}

func TestStripNoiseSimplecov(t *testing.T) {
	input := "...\n\nCoverage report generated for RSpec to /app/coverage.\n142 / 200 LOC (71.0%) covered.\n\n3 examples, 0 failures"
	result := stripNoise(input)
	if strings.Contains(result, "Coverage report") || strings.Contains(result, "LOC") {
		t.Errorf("simplecov not stripped: %s", result)
	}
	if !strings.Contains(result, "3 examples") {
		t.Errorf("missing summary: %s", result)
	}
}

func TestStripNoiseDeprecation(t *testing.T) {
	input := "DEPRECATION WARNING: Using `return` in before callbacks is deprecated.\n...\n3 examples, 0 failures"
	result := stripNoise(input)
	if strings.Contains(result, "DEPRECATION") {
		t.Errorf("deprecation not stripped: %s", result)
	}
	if !strings.Contains(result, "3 examples") {
		t.Errorf("missing summary: %s", result)
	}
}

func TestStripNoiseFinishedIn(t *testing.T) {
	input := "...\nFinished in 12.34 seconds (files took 3.21 seconds to load)\n3 examples, 0 failures"
	result := stripNoise(input)
	if strings.Contains(result, "Finished in 12.34") {
		t.Errorf("'Finished in' not stripped: %s", result)
	}
	if !strings.Contains(result, "3 examples") {
		t.Errorf("missing summary: %s", result)
	}
}

func TestStripNoiseCapybaraScreenshot(t *testing.T) {
	input := "...\n     saved screenshot to /tmp/capybara/screenshots/2026_failed.png\n3 examples, 1 failure"
	result := stripNoise(input)
	if !strings.Contains(result, "[screenshot:") || !strings.Contains(result, "failed.png") {
		t.Errorf("screenshot path not kept: %s", result)
	}
	if strings.Contains(result, "saved screenshot to") {
		t.Errorf("raw screenshot line leaked: %s", result)
	}
}

// ── Token savings tests ──────────────────────────────────────────────────

func TestTokenSavingsAllPass(t *testing.T) {
	output := filterRspecOutput(largeSuiteJSON)
	if s := savingsPct(largeSuiteJSON, output); s < 60.0 {
		t.Errorf("RSpec all-pass: expected >=60%% savings, got %.1f%%", s)
	}
}

func TestTokenSavingsWithFailures(t *testing.T) {
	output := filterRspecOutput(withFailuresJSON)
	if s := savingsPct(withFailuresJSON, output); s < 60.0 {
		t.Errorf("RSpec failures: expected >=60%% savings, got %.1f%%", s)
	}
}

func TestTokenSavingsTextFallback(t *testing.T) {
	input := `Running via Spring preloader in process 12345
Randomized with seed 54321
..F...E..F..

Failures:

  1) User#full_name returns first and last name
     Failure/Error: expect(user.full_name).to eq("John Doe")
       expected: "John Doe"
            got: "John D."
     # /usr/local/lib/ruby/gems/3.2.0/gems/rspec-expectations-3.12.0/lib/rspec/expectations/fail_with.rb:37
     # ./spec/models/user_spec.rb:15
     # /usr/local/lib/ruby/gems/3.2.0/gems/rspec-core-3.12.0/lib/rspec/core/example.rb:258

  2) Api::Controller#index returns success
     Failure/Error: get :index
       expected 200 got 500
     # /usr/local/lib/ruby/gems/3.2.0/gems/rspec-expectations-3.12.0/lib/rspec/expectations/fail_with.rb:37
     # ./spec/controllers/api_spec.rb:42
     # /usr/local/lib/ruby/gems/3.2.0/gems/rspec-core-3.12.0/lib/rspec/core/example.rb:258

Failed examples:

rspec ./spec/models/user_spec.rb:15 # User#full_name returns first and last name
rspec ./spec/controllers/api_spec.rb:42 # Api::Controller#index returns success

12 examples, 2 failures

Coverage report generated for RSpec to /app/coverage.
142 / 200 LOC (71.0%) covered.
`
	output := filterRspecText(input)
	if s := savingsPct(input, output); s < 30.0 {
		t.Errorf("RSpec text fallback: expected >=30%% savings, got %.1f%%", s)
	}
}

// ── ANSI handling tests ──────────────────────────────────────────────────

func TestFilterRspecANSIWrappedJSON(t *testing.T) {
	input := "\x1b[32m{\"version\":\"3.12.0\"\x1b[0m broken json"
	result := filterRspecOutput(input)
	if result == "" {
		t.Error("should not panic/empty on ANSI-wrapped JSON")
	}
}

// ── Text fallback >5 failures truncation ──────────────────────────────────

func TestFilterRspecTextManyFailuresCapsAtFive(t *testing.T) {
	text := `Randomized with seed 12345
.......FFFFFFF

Failures:

  1) User#full_name fails
     Failure/Error: expect(true).to eq(false)
     # ./spec/models/user_spec.rb:5

  2) Post#title fails
     Failure/Error: expect(true).to eq(false)
     # ./spec/models/post_spec.rb:10

  3) Comment#body fails
     Failure/Error: expect(true).to eq(false)
     # ./spec/models/comment_spec.rb:15

  4) Session#token fails
     Failure/Error: expect(true).to eq(false)
     # ./spec/models/session_spec.rb:20

  5) Profile#avatar fails
     Failure/Error: expect(true).to eq(false)
     # ./spec/models/profile_spec.rb:25

  6) Team#members fails
     Failure/Error: expect(true).to eq(false)
     # ./spec/models/team_spec.rb:30

  7) Role#permissions fails
     Failure/Error: expect(true).to eq(false)
     # ./spec/models/role_spec.rb:35

14 examples, 7 failures
`
	result := filterRspecText(text)
	if !strings.Contains(result, "1. ✗") {
		t.Errorf("missing first failure: %s", result)
	}
	if !strings.Contains(result, "5. ✗") {
		t.Errorf("missing fifth failure: %s", result)
	}
	if strings.Contains(result, "6. ✗") {
		t.Errorf("should not show sixth inline: %s", result)
	}
	if !strings.Contains(result, "+2 more") {
		t.Errorf("missing overflow count: %s", result)
	}
}

// ── Header -> FailedExamples transition ────────────────────────────────────

func TestFilterRspecTextHeaderToFailedExamples(t *testing.T) {
	text := `..F..

Failed examples:

rspec ./spec/models/user_spec.rb:5 # User is valid

5 examples, 1 failure
`
	result := filterRspecText(text)
	if !strings.Contains(result, "5 examples, 1 failure") {
		t.Errorf("missing summary: %s", result)
	}
	if !strings.Contains(result, "RSpec:") {
		t.Errorf("missing RSpec prefix: %s", result)
	}
}

// ── Format flag detection tests ──────────────────────────────────────────

func TestHasFormatFlagNone(t *testing.T) {
	if hasFormatFlag([]string{}) {
		t.Error("want false for no args")
	}
}

func TestHasFormatFlagLong(t *testing.T) {
	if !hasFormatFlag([]string{"--format", "documentation"}) {
		t.Error("want true for --format")
	}
}

func TestHasFormatFlagShortCombined(t *testing.T) {
	for _, flag := range []string{"-fjson", "-fj", "-fdocumentation"} {
		if !hasFormatFlag([]string{flag}) {
			t.Errorf("should detect %s", flag)
		}
	}
}

func TestHasFormatFlagEquals(t *testing.T) {
	if !hasFormatFlag([]string{"--format=json"}) {
		t.Error("want true for --format=json")
	}
}
