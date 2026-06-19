package rake

import (
	"strings"
	"testing"
)

// countTokens mirrors rtk's utils::count_tokens (test-only): whitespace-delimited
// word count, used to verify token-savings claims.
func countTokens(text string) int {
	return len(strings.Fields(text))
}

func TestFilterMinitestAllPass(t *testing.T) {
	output := `Run options: --seed 12345

# Running:

........

Finished in 0.123456s, 64.8 runs/s, 72.9 assertions/s.

8 runs, 9 assertions, 0 failures, 0 errors, 0 skips`

	result := filterMinitestOutput(output)
	for _, want := range []string{"ok rake test", "8 runs", "0 failures"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q: %s", want, result)
		}
	}
}

func TestFilterMinitestWithFailures(t *testing.T) {
	output := `Run options: --seed 54321

# Running:

..F....

Finished in 0.234567s, 29.8 runs/s

  1) Failure:
TestSomething#test_that_fails [/path/to/test.rb:15]:
Expected: true
  Actual: false

7 runs, 7 assertions, 1 failures, 0 errors, 0 skips`

	result := filterMinitestOutput(output)
	for _, want := range []string{"1 failures", "test_that_fails", "Expected: true"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q: %s", want, result)
		}
	}
}

func TestFilterMinitestWithErrors(t *testing.T) {
	output := `Run options: --seed 99999

# Running:

.E....

Finished in 0.345678s, 17.4 runs/s

  1) Error:
TestOther#test_boom [/path/to/test.rb:42]:
RuntimeError: something went wrong
    /path/to/test.rb:42:in ` + "`" + `test_boom'

6 runs, 5 assertions, 0 failures, 1 errors, 0 skips`

	result := filterMinitestOutput(output)
	for _, want := range []string{"1 errors", "test_boom", "RuntimeError"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q: %s", want, result)
		}
	}
}

func TestFilterMinitestEmpty(t *testing.T) {
	result := filterMinitestOutput("")
	if !strings.Contains(result, "no tests ran") {
		t.Errorf("result missing %q: %s", "no tests ran", result)
	}
}

func TestFilterMinitestSkip(t *testing.T) {
	output := `Run options: --seed 11111

# Running:

..S..

Finished in 0.100000s, 50.0 runs/s

5 runs, 4 assertions, 0 failures, 0 errors, 1 skips`

	result := filterMinitestOutput(output)
	for _, want := range []string{"ok rake test", "1 skips"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q: %s", want, result)
		}
	}
}

func TestTokenSavings(t *testing.T) {
	var dots strings.Builder
	for i := 0; i < 20; i++ {
		dots.WriteString("......................................................................\n")
	}
	output := "Run options: --seed 12345\n\n" +
		"# Running:\n\n" +
		dots.String() + "\n" +
		"Finished in 2.345678s, 213.4 runs/s, 428.7 assertions/s.\n\n" +
		"500 runs, 1003 assertions, 0 failures, 0 errors, 0 skips"

	inputTokens := countTokens(output)
	result := filterMinitestOutput(output)
	outputTokens := countTokens(result)

	savings := 100.0 - (float64(outputTokens)/float64(inputTokens))*100.0
	if savings < 80.0 {
		t.Errorf("Expected >= 80%% savings, got %.1f%% (input: %d, output: %d)", savings, inputTokens, outputTokens)
	}
}

func TestParseMinitestSummary(t *testing.T) {
	cases := []struct {
		in                          string
		runs, asrt, fail, err, skip int
	}{
		{"8 runs, 9 assertions, 0 failures, 0 errors, 0 skips", 8, 9, 0, 0, 0},
		{"5 runs, 4 assertions, 1 failures, 1 errors, 2 skips", 5, 4, 1, 1, 2},
		// minitest-reporters uses "tests" instead of "runs"
		{"57 tests, 378 assertions, 0 failures, 0 errors, 0 skips", 57, 378, 0, 0, 0},
	}
	for _, c := range cases {
		runs, asrt, fail, err, skip := parseMinitestSummary(c.in)
		if runs != c.runs || asrt != c.asrt || fail != c.fail || err != c.err || skip != c.skip {
			t.Errorf("parseMinitestSummary(%q) = (%d,%d,%d,%d,%d) want (%d,%d,%d,%d,%d)",
				c.in, runs, asrt, fail, err, skip, c.runs, c.asrt, c.fail, c.err, c.skip)
		}
	}
}

func TestFilterMinitestMultipleFailures(t *testing.T) {
	output := `Run options: --seed 77777

# Running:

.FF.E.

Finished in 0.500000s, 12.0 runs/s

  1) Failure:
TestFoo#test_alpha [/test.rb:10]:
Expected: 1
  Actual: 2

  2) Failure:
TestFoo#test_beta [/test.rb:20]:
Expected: "hello"
  Actual: "world"

  3) Error:
TestBar#test_gamma [/test.rb:30]:
NoMethodError: undefined method ` + "`" + `blah'

6 runs, 5 assertions, 2 failures, 1 errors, 0 skips`

	result := filterMinitestOutput(output)
	for _, want := range []string{"2 failures", "1 errors", "test_alpha", "test_beta", "test_gamma"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q: %s", want, result)
		}
	}
}

func TestFilterMinitestReportersFormat(t *testing.T) {
	output := "Started with run options --seed 37764\n\n" +
		"Progress: |========================================|\n\n" +
		"Finished in 5.79938s\n" +
		"57 tests, 378 assertions, 0 failures, 0 errors, 0 skips"

	result := filterMinitestOutput(output)
	for _, want := range []string{"ok rake test", "57 runs", "0 failures"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q: %s", want, result)
		}
	}
}

func TestFilterMinitestWithANSI(t *testing.T) {
	output := "\x1b[32mRun options: --seed 12345\x1b[0m\n\n" +
		"# Running:\n\n" +
		"\x1b[32m....\x1b[0m\n\n" +
		"Finished in 0.1s, 40.0 runs/s\n\n" +
		"4 runs, 4 assertions, 0 failures, 0 errors, 0 skips"

	result := filterMinitestOutput(output)
	for _, want := range []string{"ok rake test", "4 runs"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q: %s", want, result)
		}
	}
}

// ── selectRunner tests ─────────────────────────────

func argsOf(s string) []string {
	return strings.Fields(s)
}

func TestSelectRunnerSingleFileUsesRake(t *testing.T) {
	tool, _ := selectRunner(argsOf("test TEST=test/models/post_test.rb"))
	if tool != "rake" {
		t.Errorf("tool = %q, want rake", tool)
	}
}

func TestSelectRunnerNoFilesUsesRake(t *testing.T) {
	tool, _ := selectRunner(argsOf("test"))
	if tool != "rake" {
		t.Errorf("tool = %q, want rake", tool)
	}
}

func TestSelectRunnerMultipleFilesUsesRails(t *testing.T) {
	tool, a := selectRunner(argsOf("test test/models/post_test.rb test/models/user_test.rb"))
	if tool != "rails" {
		t.Errorf("tool = %q, want rails", tool)
	}
	want := argsOf("test test/models/post_test.rb test/models/user_test.rb")
	if strings.Join(a, " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", a, want)
	}
}

func TestSelectRunnerLineNumberUsesRails(t *testing.T) {
	tool, _ := selectRunner(argsOf("test test/models/post_test.rb:15"))
	if tool != "rails" {
		t.Errorf("tool = %q, want rails", tool)
	}
}

func TestSelectRunnerMultipleWithLineNumbers(t *testing.T) {
	tool, _ := selectRunner(argsOf("test test/models/post_test.rb:15 test/models/user_test.rb:30"))
	if tool != "rails" {
		t.Errorf("tool = %q, want rails", tool)
	}
}

func TestSelectRunnerNonTestSubcommandUsesRake(t *testing.T) {
	tool, _ := selectRunner(argsOf("db:migrate"))
	if tool != "rake" {
		t.Errorf("tool = %q, want rake", tool)
	}
}

func TestSelectRunnerSinglePositionalFileUsesRails(t *testing.T) {
	tool, _ := selectRunner(argsOf("test test/models/post_test.rb"))
	if tool != "rails" {
		t.Errorf("tool = %q, want rails", tool)
	}
}

func TestSelectRunnerFlagsNotCountedAsFiles(t *testing.T) {
	tool, _ := selectRunner(argsOf("test --verbose --seed 12345"))
	if tool != "rake" {
		t.Errorf("tool = %q, want rake", tool)
	}
}

func TestLooksLikeTestPath(t *testing.T) {
	for _, in := range []string{
		"test/models/post_test.rb",
		"test/models/post_test.rb:15",
		"spec/models/post_spec.rb",
		"my_file.rb",
	} {
		if !looksLikeTestPath(in) {
			t.Errorf("looksLikeTestPath(%q) = false, want true", in)
		}
	}
	for _, in := range []string{"--verbose", "12345"} {
		if looksLikeTestPath(in) {
			t.Errorf("looksLikeTestPath(%q) = true, want false", in)
		}
	}
}
