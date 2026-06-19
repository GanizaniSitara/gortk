package mypy

import (
	"fmt"
	"strings"
	"testing"
)

func TestFilterMypyErrorsGroupedByFile(t *testing.T) {
	output := `src/server/auth.py:12: error: Incompatible return value type (got "str", expected "int")  [return-value]
src/server/auth.py:15: error: Argument 1 has incompatible type "int"; expected "str"  [arg-type]
src/models/user.py:8: error: Name "foo" is not defined  [name-defined]
src/models/user.py:10: error: Incompatible types in assignment  [assignment]
src/models/user.py:20: error: Missing return statement  [return]
Found 5 errors in 2 files (checked 10 source files)
`
	result := filterMypyOutput(output)
	if !strings.Contains(result, "mypy: 5 errors in 2 files") {
		t.Errorf("missing summary line: %s", result)
	}
	// user.py has 3 errors, auth.py has 2 -- user.py should come first.
	userPos := strings.Index(result, "user.py")
	authPos := strings.Index(result, "auth.py")
	if userPos < 0 || authPos < 0 || !(userPos < authPos) {
		t.Errorf("user.py (3 errors) should appear before auth.py (2 errors): %s", result)
	}
	if !strings.Contains(result, "user.py (3 errors)") {
		t.Errorf("missing user.py (3 errors): %s", result)
	}
	if !strings.Contains(result, "auth.py (2 errors)") {
		t.Errorf("missing auth.py (2 errors): %s", result)
	}
}

func TestFilterMypyWithColumnNumbers(t *testing.T) {
	output := `src/api.py:10:5: error: Incompatible return value type  [return-value]
`
	result := filterMypyOutput(output)
	for _, want := range []string{"L10:", "[return-value]", "Incompatible return value type"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
}

func TestFilterMypyTopCodesSummary(t *testing.T) {
	output := `a.py:1: error: Error one  [return-value]
a.py:2: error: Error two  [return-value]
a.py:3: error: Error three  [return-value]
b.py:1: error: Error four  [name-defined]
c.py:1: error: Error five  [arg-type]
Found 5 errors in 3 files
`
	result := filterMypyOutput(output)
	for _, want := range []string{"Top codes:", "return-value (3x)", "name-defined (1x)", "arg-type (1x)"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
}

func TestFilterMypySingleCodeNoSummary(t *testing.T) {
	output := `a.py:1: error: Error one  [return-value]
a.py:2: error: Error two  [return-value]
b.py:1: error: Error three  [return-value]
Found 3 errors in 2 files
`
	result := filterMypyOutput(output)
	if strings.Contains(result, "Top codes:") {
		t.Errorf("Top codes should not appear with only one distinct code: %s", result)
	}
}

func TestFilterMypyEveryErrorShown(t *testing.T) {
	output := `src/api.py:10: error: Type "str" not assignable to "int"  [assignment]
src/api.py:20: error: Missing return statement  [return]
src/api.py:30: error: Name "bar" is not defined  [name-defined]
`
	result := filterMypyOutput(output)
	for _, want := range []string{
		`Type "str" not assignable to "int"`,
		"Missing return statement",
		`Name "bar" is not defined`,
		"L10:", "L20:", "L30:",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
}

func TestFilterMypyNoteContinuation(t *testing.T) {
	output := `src/app.py:10: error: Incompatible types in assignment  [assignment]
src/app.py:10: note: Expected type "int"
src/app.py:10: note: Got type "str"
src/app.py:20: error: Missing return statement  [return]
`
	result := filterMypyOutput(output)
	for _, want := range []string{
		"Incompatible types in assignment",
		`Expected type "int"`,
		`Got type "str"`,
		"L10:", "L20:",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
}

func TestFilterMypyFilelessErrors(t *testing.T) {
	output := `mypy: error: No module named 'nonexistent'
src/api.py:10: error: Name "foo" is not defined  [name-defined]
Found 1 error in 1 file
`
	result := filterMypyOutput(output)
	// File-less error should appear verbatim before grouped output.
	if !strings.Contains(result, "mypy: error: No module named 'nonexistent'") {
		t.Errorf("missing fileless error: %s", result)
	}
	if !strings.Contains(result, "api.py (1 error") {
		t.Errorf("missing api.py group header: %s", result)
	}
	filelessPos := strings.Index(result, "No module named")
	groupedPos := strings.Index(result, "api.py")
	if filelessPos < 0 || groupedPos < 0 || !(filelessPos < groupedPos) {
		t.Errorf("File-less errors should appear before grouped file errors: %s", result)
	}
}

func TestFilterMypyNoErrors(t *testing.T) {
	output := "Success: no issues found in 5 source files\n"
	result := filterMypyOutput(output)
	if result != "mypy: No issues found" {
		t.Errorf("want %q, got %q", "mypy: No issues found", result)
	}
}

func TestFilterMypyNoFileLimit(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 15; i++ {
		fmt.Fprintf(&sb, "src/file%d.py:%d: error: Error in file %d.  [assignment]\n", i, i, i)
	}
	sb.WriteString("Found 15 errors in 15 files\n")
	result := filterMypyOutput(sb.String())
	if !strings.Contains(result, "15 errors in 15 files") {
		t.Errorf("missing summary: %s", result)
	}
	for i := 1; i <= 15; i++ {
		if !strings.Contains(result, fmt.Sprintf("file%d.py", i)) {
			t.Errorf("file%d.py missing from output: %s", i, result)
		}
	}
}
