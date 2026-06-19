package golangci

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestFilterGolangciNoIssues(t *testing.T) {
	output := `{"Issues":[]}`
	result := FilterGolangciJSON(output, 1)
	if !strings.Contains(result, "golangci-lint") {
		t.Errorf("missing golangci-lint: %s", result)
	}
	if !strings.Contains(result, "No issues found") {
		t.Errorf("missing 'No issues found': %s", result)
	}
}

func TestFilterGolangciWithIssues(t *testing.T) {
	output := `{
  "Issues": [
    {
      "FromLinter": "errcheck",
      "Text": "Error return value not checked",
      "Pos": {"Filename": "main.go", "Line": 42, "Column": 5}
    },
    {
      "FromLinter": "errcheck",
      "Text": "Error return value not checked",
      "Pos": {"Filename": "main.go", "Line": 50, "Column": 10}
    },
    {
      "FromLinter": "gosimple",
      "Text": "Should use strings.Contains",
      "Pos": {"Filename": "utils.go", "Line": 15, "Column": 2}
    }
  ]
}`
	result := FilterGolangciJSON(output, 1)
	for _, want := range []string{"3 issues", "2 files", "errcheck", "gosimple", "main.go", "utils.go"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q: %s", want, result)
		}
	}
}

func TestCompactPath(t *testing.T) {
	cases := map[string]string{
		"/Users/foo/project/pkg/handler/server.go": "pkg/handler/server.go",
		"/home/user/app/cmd/main/main.go":          "cmd/main/main.go",
		"/project/internal/config/loader.go":       "internal/config/loader.go",
		"relative/file.go":                         "file.go",
	}
	for in, want := range cases {
		if got := compactPath(in); got != want {
			t.Errorf("compactPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseVersionV1Format(t *testing.T) {
	if got := ParseMajorVersion("golangci-lint version 1.59.1"); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}

func TestParseVersionV2Format(t *testing.T) {
	in := "golangci-lint has version 2.10.0 built with go1.26.0 from 95dcb68a on 2026-02-17T13:05:51Z"
	if got := ParseMajorVersion(in); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}

func TestParseVersionEmptyReturns1(t *testing.T) {
	if got := ParseMajorVersion(""); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}

func TestParseVersionMalformedReturns1(t *testing.T) {
	if got := ParseMajorVersion("not a version string"); got != 1 {
		t.Errorf("got %d, want 1", got)
	}
}

func TestClassifyInvocationRunUsesFilteredPath(t *testing.T) {
	inv, ok := classifyInvocation([]string{"run", "./..."})
	if !ok {
		t.Fatalf("expected filtered run")
	}
	want := runInvocation{globalArgs: []string{}, runArgs: []string{"./..."}}
	assertInvocation(t, inv, want)
}

func TestClassifyInvocationWithGlobalFlagValueUsesFilteredPath(t *testing.T) {
	inv, ok := classifyInvocation([]string{"--color", "never", "run", "./..."})
	if !ok {
		t.Fatalf("expected filtered run")
	}
	want := runInvocation{globalArgs: []string{"--color", "never"}, runArgs: []string{"./..."}}
	assertInvocation(t, inv, want)
}

func TestClassifyInvocationWithShortGlobalFlagUsesFilteredPath(t *testing.T) {
	inv, ok := classifyInvocation([]string{"-v", "run", "./..."})
	if !ok {
		t.Fatalf("expected filtered run")
	}
	want := runInvocation{globalArgs: []string{"-v"}, runArgs: []string{"./..."}}
	assertInvocation(t, inv, want)
}

func TestClassifyInvocationWithInlineValueFlagUsesFilteredPath(t *testing.T) {
	inv, ok := classifyInvocation([]string{"--color=never", "run", "./..."})
	if !ok {
		t.Fatalf("expected filtered run")
	}
	want := runInvocation{globalArgs: []string{"--color=never"}, runArgs: []string{"./..."}}
	assertInvocation(t, inv, want)
}

func TestClassifyInvocationWithInlineConfigFlagUsesFilteredPath(t *testing.T) {
	inv, ok := classifyInvocation([]string{"--config=foo.yml", "run", "./..."})
	if !ok {
		t.Fatalf("expected filtered run")
	}
	want := runInvocation{globalArgs: []string{"--config=foo.yml"}, runArgs: []string{"./..."}}
	assertInvocation(t, inv, want)
}

func TestClassifyInvocationBareCommandIsPassthrough(t *testing.T) {
	if _, ok := classifyInvocation(nil); ok {
		t.Errorf("expected passthrough")
	}
}

func TestClassifyInvocationVersionFlagIsPassthrough(t *testing.T) {
	if _, ok := classifyInvocation([]string{"--version"}); ok {
		t.Errorf("expected passthrough")
	}
}

func TestClassifyInvocationVersionSubcommandIsPassthrough(t *testing.T) {
	if _, ok := classifyInvocation([]string{"version"}); ok {
		t.Errorf("expected passthrough")
	}
}

func TestBuildFilteredArgsDoesNotDuplicateRun(t *testing.T) {
	inv := runInvocation{globalArgs: nil, runArgs: []string{"./..."}}
	got := buildFilteredArgs(inv, 2)
	want := []string{"run", "--output.json.path", "stdout", "./..."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBuildFilteredArgsV1(t *testing.T) {
	inv := runInvocation{globalArgs: nil, runArgs: []string{"./..."}}
	got := buildFilteredArgs(inv, 1)
	want := []string{"run", "--out-format=json", "./..."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFilterGolangciV2FieldsParseCleanly(t *testing.T) {
	output := `{
  "Issues": [
    {
      "FromLinter": "errcheck",
      "Text": "Error return value not checked",
      "Severity": "error",
      "SourceLines": ["    if err := foo(); err != nil {"],
      "Pos": {"Filename": "main.go", "Line": 42, "Column": 5, "Offset": 1024}
    }
  ]
}`
	result := FilterGolangciJSON(output, 2)
	if !strings.Contains(result, "errcheck") || !strings.Contains(result, "main.go") {
		t.Errorf("result missing fields: %s", result)
	}
}

func TestFilterV2ShowsSourceLines(t *testing.T) {
	output := `{
  "Issues": [
    {
      "FromLinter": "errcheck",
      "Text": "Error return value not checked",
      "Severity": "error",
      "SourceLines": ["    if err := foo(); err != nil {"],
      "Pos": {"Filename": "main.go", "Line": 42, "Column": 5, "Offset": 0}
    }
  ]
}`
	result := FilterGolangciJSON(output, 2)
	if !strings.Contains(result, "→") {
		t.Errorf("v2 should show source line with → prefix: %s", result)
	}
	if !strings.Contains(result, "if err := foo()") {
		t.Errorf("missing source content: %s", result)
	}
}

func TestFilterV1DoesNotShowSourceLines(t *testing.T) {
	output := `{
  "Issues": [
    {
      "FromLinter": "errcheck",
      "Text": "Error return value not checked",
      "Severity": "error",
      "SourceLines": ["    if err := foo(); err != nil {"],
      "Pos": {"Filename": "main.go", "Line": 42, "Column": 5, "Offset": 0}
    }
  ]
}`
	result := FilterGolangciJSON(output, 1)
	if strings.Contains(result, "→") {
		t.Errorf("v1 should not show source lines: %s", result)
	}
}

func TestFilterV2EmptySourceLinesGraceful(t *testing.T) {
	output := `{
  "Issues": [
    {
      "FromLinter": "errcheck",
      "Text": "Error return value not checked",
      "Severity": "",
      "SourceLines": [],
      "Pos": {"Filename": "main.go", "Line": 42, "Column": 5, "Offset": 0}
    }
  ]
}`
	result := FilterGolangciJSON(output, 2)
	if !strings.Contains(result, "errcheck") {
		t.Errorf("missing errcheck: %s", result)
	}
	if strings.Contains(result, "→") {
		t.Errorf("no source line to show, should degrade gracefully: %s", result)
	}
}

func TestFilterV2SourceLineTruncatedTo80Chars(t *testing.T) {
	longLine := strings.Repeat("x", 120)
	output := `{
  "Issues": [
    {
      "FromLinter": "lll",
      "Text": "line too long",
      "Severity": "",
      "SourceLines": ["` + longLine + `"],
      "Pos": {"Filename": "main.go", "Line": 1, "Column": 1, "Offset": 0}
    }
  ]
}`
	result := FilterGolangciJSON(output, 2)
	// Content truncated at 80 chars; prefix "      → " = 10 bytes (6 spaces +
	// 3-byte arrow + space). Total line max = 80 + 10 = 90 bytes.
	for _, line := range strings.Split(result, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " "), "→") {
			if len(line) > 90 {
				t.Errorf("source line too long: %d", len(line))
			}
		}
	}
}

func TestFilterV2SourceLineTruncatedNonASCII(t *testing.T) {
	// Japanese characters are 3 bytes each; 30 chars = 90 bytes, which a naive
	// byte slice at 80 would split mid-rune.
	longLine := strings.Repeat("日", 30)
	output := `{
  "Issues": [
    {
      "FromLinter": "lll",
      "Text": "line too long",
      "Severity": "",
      "SourceLines": ["` + longLine + `"],
      "Pos": {"Filename": "main.go", "Line": 1, "Column": 1, "Offset": 0}
    }
  ]
}`
	result := FilterGolangciJSON(output, 2)
	for _, line := range strings.Split(result, "\n") {
		trimmed := strings.TrimLeft(line, " ")
		if strings.HasPrefix(trimmed, "→") {
			content := strings.TrimSpace(strings.TrimPrefix(trimmed, "→"))
			if n := len([]rune(content)); n > 80 {
				t.Errorf("content chars: %d", n)
			}
		}
	}
}

func TestFilterGolangciJSONParseFailure(t *testing.T) {
	result := FilterGolangciJSON("this is not json", 1)
	if !strings.Contains(result, "JSON parse failed") {
		t.Errorf("expected parse-failure header: %s", result)
	}
	if !strings.Contains(result, "this is not json") {
		t.Errorf("expected raw output echoed: %s", result)
	}
}

func countTokens(text string) int {
	return len(strings.Fields(text))
}

func TestGolangciV2TokenSavings(t *testing.T) {
	raw, err := os.ReadFile("testdata/golangci_v2_json.txt")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	rawStr := string(raw)
	filtered := FilterGolangciJSON(rawStr, 2)
	savings := 100.0 - (float64(countTokens(filtered))/float64(countTokens(rawStr)))*100.0
	if savings < 60.0 {
		t.Errorf("expected >=60%% token savings, got %.1f%%\nFiltered output:\n%s", savings, filtered)
	}
}

func assertInvocation(t *testing.T, got, want runInvocation) {
	t.Helper()
	if !stringSlicesEqual(got.globalArgs, want.globalArgs) {
		t.Errorf("globalArgs = %v, want %v", got.globalArgs, want.globalArgs)
	}
	if !stringSlicesEqual(got.runArgs, want.runArgs) {
		t.Errorf("runArgs = %v, want %v", got.runArgs, want.runArgs)
	}
}

// stringSlicesEqual treats nil and empty as equal (the Rust spec uses vec![] for
// empty global_args; our classifier produces a non-nil empty slice).
func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
