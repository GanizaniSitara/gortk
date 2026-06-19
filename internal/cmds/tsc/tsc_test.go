package tsc

import (
	"fmt"
	"strings"
	"testing"
)

func TestFilterTscOutput(t *testing.T) {
	output := "\n" +
		"src/server/api/auth.ts(12,5): error TS2322: Type 'string' is not assignable to type 'number'.\n" +
		"src/server/api/auth.ts(15,10): error TS2345: Argument of type 'number' is not assignable to parameter of type 'string'.\n" +
		"src/components/Button.tsx(8,3): error TS2339: Property 'onClick' does not exist on type 'ButtonProps'.\n" +
		"src/components/Button.tsx(10,5): error TS2322: Type 'string' is not assignable to type 'number'.\n" +
		"\n" +
		"Found 4 errors in 2 files.\n"
	result := filterTscOutput(output)
	for _, want := range []string{
		"TypeScript: 4 errors in 2 files",
		"auth.ts (2 errors)",
		"Button.tsx (2 errors)",
		"TS2322",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q: %s", want, result)
		}
	}
	if strings.Contains(result, "Found 4 errors") {
		t.Errorf("summary line should be replaced: %s", result)
	}
}

func TestEveryErrorMessageShown(t *testing.T) {
	output := "" +
		"src/api.ts(10,5): error TS2322: Type 'string' is not assignable to type 'number'.\n" +
		"src/api.ts(20,5): error TS2322: Type 'boolean' is not assignable to type 'string'.\n" +
		"src/api.ts(30,5): error TS2322: Type 'null' is not assignable to type 'object'.\n"
	result := filterTscOutput(output)
	for _, want := range []string{
		"Type 'string' is not assignable to type 'number'",
		"Type 'boolean' is not assignable to type 'string'",
		"Type 'null' is not assignable to type 'object'",
		"L10:", "L20:", "L30:",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q: %s", want, result)
		}
	}
}

func TestContinuationLinesPreserved(t *testing.T) {
	output := "" +
		"src/app.tsx(10,3): error TS2322: Type '{ children: Element; }' is not assignable to type 'Props'.\n" +
		"  Property 'children' does not exist on type 'Props'.\n" +
		"src/app.tsx(20,5): error TS2345: Argument of type 'number' is not assignable to parameter of type 'string'.\n"
	result := filterTscOutput(output)
	for _, want := range []string{
		"Property 'children' does not exist on type 'Props'",
		"L10:", "L20:",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q: %s", want, result)
		}
	}
}

func TestNoFileLimit(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 15; i++ {
		sb.WriteString(fmt.Sprintf("src/file%d.ts(%d,1): error TS2322: Error in file %d.\n", i, i, i))
	}
	result := filterTscOutput(sb.String())
	if !strings.Contains(result, "15 errors in 15 files") {
		t.Errorf("result missing count line: %s", result)
	}
	for i := 1; i <= 15; i++ {
		want := fmt.Sprintf("file%d.ts", i)
		if !strings.Contains(result, want) {
			t.Errorf("file%d.ts missing from output: %s", i, result)
		}
	}
}

func TestFilterNoErrors(t *testing.T) {
	output := "Found 0 errors. Watching for file changes."
	result := filterTscOutput(output)
	if !strings.Contains(result, "No errors found") {
		t.Errorf("result missing 'No errors found': %s", result)
	}
}

// --- Ported from the Rust streaming-handler tests; the same grouped-output
// behaviour is exercised against the pure buffered filter. ---

func TestTscStreamErrors(t *testing.T) {
	input := "" +
		"src/server/api/auth.ts(12,5): error TS2322: Type 'string' is not assignable to type 'number'.\n" +
		"src/server/api/auth.ts(15,10): error TS2345: Argument of type 'number' is not assignable to parameter of type 'string'.\n" +
		"src/components/Button.tsx(8,3): error TS2339: Property 'onClick' does not exist on type 'ButtonProps'.\n" +
		"\n" +
		"Found 3 errors in 2 files.\n"
	result := filterTscOutput(input)
	for _, want := range []string{"TS2322", "TS2345", "3 errors in 2 files"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q: %s", want, result)
		}
	}
	if strings.Contains(result, "Found 3") {
		t.Errorf("summary line should be replaced: %s", result)
	}
}

func TestTscStreamNoErrors(t *testing.T) {
	input := "Found 0 errors. Watching for file changes.\n"
	result := filterTscOutput(input)
	if !strings.Contains(result, "No errors found") {
		t.Errorf("result missing 'No errors found': %s", result)
	}
}

func TestTscStreamContinuationLines(t *testing.T) {
	input := "" +
		"src/app.tsx(10,3): error TS2322: Type '{ children: Element; }' is not assignable to type 'Props'.\n" +
		"  Property 'children' does not exist on type 'Props'.\n" +
		"src/app.tsx(20,5): error TS2345: Argument of type 'number' is not assignable.\n"
	result := filterTscOutput(input)
	for _, want := range []string{"Property 'children' does not exist", "TS2322", "TS2345"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q: %s", want, result)
		}
	}
}

func TestTruncate(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := truncate(long, 120)
	if len([]rune(got)) != 120 {
		t.Errorf("truncate len = %d, want 120", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncate should append ...: %q", got)
	}
	if got := truncate("short", 120); got != "short" {
		t.Errorf("truncate(short) = %q, want short", got)
	}
}
