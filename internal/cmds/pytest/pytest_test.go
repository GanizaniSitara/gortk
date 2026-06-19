package pytest

import (
	"fmt"
	"strings"
	"testing"
)

func TestFilterPytestAllPass(t *testing.T) {
	output := `=== test session starts ===
platform darwin -- Python 3.11.0
collected 5 items

tests/test_foo.py .....                                            [100%]

=== 5 passed in 0.50s ===`

	result := filterPytestOutput(output)
	if !strings.Contains(result, "Pytest") {
		t.Errorf("result missing %q: %s", "Pytest", result)
	}
	if !strings.Contains(result, "5 passed") {
		t.Errorf("result missing %q: %s", "5 passed", result)
	}
}

func TestFilterPytestWithFailures(t *testing.T) {
	output := `=== test session starts ===
collected 5 items

tests/test_foo.py ..F..                                            [100%]

=== FAILURES ===
___ test_something ___

    def test_something():
>       assert False
E       assert False

tests/test_foo.py:10: AssertionError

=== short test summary info ===
FAILED tests/test_foo.py::test_something - assert False
=== 4 passed, 1 failed in 0.50s ===`

	result := filterPytestOutput(output)
	if !strings.Contains(result, "4 passed, 1 failed") {
		t.Errorf("result missing %q: %s", "4 passed, 1 failed", result)
	}
	if !strings.Contains(result, "test_something") {
		t.Errorf("result missing %q: %s", "test_something", result)
	}
	if !strings.Contains(result, "assert False") {
		t.Errorf("result missing %q: %s", "assert False", result)
	}
}

func TestFilterPytestMultipleFailures(t *testing.T) {
	output := `=== test session starts ===
collected 3 items

tests/test_foo.py FFF                                              [100%]

=== FAILURES ===
___ test_one ___
E   AssertionError: expected 5

___ test_two ___
E   ValueError: invalid value

=== short test summary info ===
FAILED tests/test_foo.py::test_one - AssertionError: expected 5
FAILED tests/test_foo.py::test_two - ValueError: invalid value
FAILED tests/test_foo.py::test_three - KeyError
=== 3 failed in 0.20s ===`

	result := filterPytestOutput(output)
	if !strings.Contains(result, "3 failed") {
		t.Errorf("result missing %q: %s", "3 failed", result)
	}
	if !strings.Contains(result, "test_one") {
		t.Errorf("result missing %q: %s", "test_one", result)
	}
	if !strings.Contains(result, "test_two") {
		t.Errorf("result missing %q: %s", "test_two", result)
	}
	if !strings.Contains(result, "expected 5") {
		t.Errorf("result missing %q: %s", "expected 5", result)
	}
}

func TestFilterPytestNoTests(t *testing.T) {
	output := `=== test session starts ===
collected 0 items

=== no tests ran in 0.00s ===`

	result := filterPytestOutput(output)
	if !strings.Contains(result, "No tests collected") {
		t.Errorf("result missing %q: %s", "No tests collected", result)
	}
}

func TestParseSummaryLine(t *testing.T) {
	c := parseSummaryLine("=== 5 passed in 0.50s ===")
	if c.passed != 5 || c.failed != 0 || c.skipped != 0 {
		t.Errorf("got (passed,failed,skipped)=(%d,%d,%d) want (5,0,0)", c.passed, c.failed, c.skipped)
	}

	c = parseSummaryLine("=== 4 passed, 1 failed in 0.50s ===")
	if c.passed != 4 || c.failed != 1 || c.skipped != 0 {
		t.Errorf("got (passed,failed,skipped)=(%d,%d,%d) want (4,1,0)", c.passed, c.failed, c.skipped)
	}

	c = parseSummaryLine("=== 3 passed, 1 failed, 2 skipped in 1.0s ===")
	if c.passed != 3 || c.failed != 1 || c.skipped != 2 {
		t.Errorf("got (passed,failed,skipped)=(%d,%d,%d) want (3,1,2)", c.passed, c.failed, c.skipped)
	}

	c = parseSummaryLine("=== 2 passed, 1 failed, 2 xfailed, 1 xpassed in 1.0s ===")
	if c.passed != 2 || c.failed != 1 || c.xfailed != 2 || c.xpassed != 1 {
		t.Errorf("got (passed,failed,xfailed,xpassed)=(%d,%d,%d,%d) want (2,1,2,1)",
			c.passed, c.failed, c.xfailed, c.xpassed)
	}
}

func TestFilterPytestXfailCapsAndTeeHint(t *testing.T) {
	var b strings.Builder
	b.WriteString("=== test session starts ===\ncollected 30 items\n\n")
	b.WriteString("test_x.py ")
	for i := 0; i < 15; i++ {
		b.WriteByte('x')
	}
	b.WriteString("\n\n=== short test summary info ===\n")
	for i := 0; i < 15; i++ {
		b.WriteString(fmt.Sprintf("XFAIL test_x.py::test_case_%d - known issue #%d\n", i, i))
	}
	b.WriteString("=== 0 passed, 15 xfailed in 0.05s ===\n")

	result := filterPytestOutput(b.String())

	xfailInSection := ""
	if parts := strings.SplitN(result, "Expected-failure outcomes:", 2); len(parts) > 1 {
		xfailInSection = parts[1]
	}
	listed := 0
	for _, l := range strings.Split(xfailInSection, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "XFAIL") {
			listed++
		}
	}
	if listed > 10 {
		t.Errorf("MAX_XFAIL cap not enforced: listed %d", listed)
	}
	if !strings.Contains(result, "… +5 more") {
		t.Errorf("missing '+N more': %s", result)
	}
}

func TestFilterPytestXfailXpass(t *testing.T) {
	output := `=== test session starts ===
collected 5 items

test_math.py ..xxX                                                 [100%]

=== short test summary info ===
XFAIL test_math.py::test_division_by_zero - known bug in division
XFAIL test_math.py::test_float_precision - float precision issue — bug #42
XPASS test_math.py::test_unexpected_pass - this should fail but currently passes
=== 2 passed, 2 xfailed, 1 xpassed in 0.05s ===`

	result := filterPytestOutput(output)
	if !strings.Contains(result, "xfailed") {
		t.Errorf("got: %s", result)
	}
	if !strings.Contains(result, "xpassed") {
		t.Errorf("got: %s", result)
	}
	if !strings.Contains(result, "XPASS") {
		t.Errorf("got: %s", result)
	}
	if !strings.Contains(result, "float precision") {
		t.Errorf("got: %s", result)
	}
	if !strings.Contains(result, "test_division_by_zero") {
		t.Errorf("got: %s", result)
	}
}

func TestFilterPytestQuietModeFailures(t *testing.T) {
	// In -q mode, the final summary line has NO === wrapper. This was causing
	// "No tests collected" to be reported incorrectly.
	output := `=== test session starts ===
platform linux -- Python 3.12.11, pytest-8.1.0
collected 1705 items

.......F.......

=== FAILURES ===
___ test_something ___

E   AssertionError: expected True

=== short test summary info ===
FAILED tests/test_foo.py::test_something - AssertionError
5 failed, 1698 passed, 2 skipped in 108.89s`

	result := filterPytestOutput(output)
	if strings.Contains(result, "No tests collected") {
		t.Errorf("Should not report 'No tests collected' when tests ran. Got: %s", result)
	}
	if !strings.Contains(result, "1698") && !strings.Contains(result, "5 failed") {
		t.Errorf("Should show actual test counts. Got: %s", result)
	}
}

func TestFilterPytestOnlySkipped(t *testing.T) {
	// If only skipped tests, should NOT say "No tests collected".
	output := `=== test session starts ===
collected 3 items

=== 3 skipped in 0.10s ===`

	result := filterPytestOutput(output)
	if strings.Contains(result, "No tests collected") {
		t.Errorf("Should not say 'No tests collected' when tests were skipped. Got: %s", result)
	}
}
