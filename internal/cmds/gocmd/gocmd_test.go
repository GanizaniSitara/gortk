package gocmd

import (
	"strings"
	"testing"
)

// These tests are a faithful port of the #[cfg(test)] cases in rtk's
// src/cmds/go/go_cmd.rs. Inputs/expected outputs match the Rust spec exactly.
//
// rtk's match_go_tool / has_golangci_format_flag tests are intentionally NOT
// ported: they exercise the `go tool golangci-lint` interception, which this
// single-package port drops (see the gocmd package doc). Everything else is here.

func TestFilterGoTestAllPass(t *testing.T) {
	output := `{"Time":"2024-01-01T10:00:00Z","Action":"run","Package":"example.com/foo","Test":"TestBar"}
{"Time":"2024-01-01T10:00:01Z","Action":"output","Package":"example.com/foo","Test":"TestBar","Output":"=== RUN   TestBar\n"}
{"Time":"2024-01-01T10:00:02Z","Action":"pass","Package":"example.com/foo","Test":"TestBar","Elapsed":0.5}
{"Time":"2024-01-01T10:00:02Z","Action":"pass","Package":"example.com/foo","Elapsed":0.5}`

	result := filterGoTestJSON(output)
	for _, want := range []string{"Go test", "1 passed", "1 packages"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
}

func TestFilterGoTestWithFailures(t *testing.T) {
	output := `{"Time":"2024-01-01T10:00:00Z","Action":"run","Package":"example.com/foo","Test":"TestFail"}
{"Time":"2024-01-01T10:00:01Z","Action":"output","Package":"example.com/foo","Test":"TestFail","Output":"=== RUN   TestFail\n"}
{"Time":"2024-01-01T10:00:02Z","Action":"output","Package":"example.com/foo","Test":"TestFail","Output":"    Error: expected 5, got 3\n"}
{"Time":"2024-01-01T10:00:03Z","Action":"fail","Package":"example.com/foo","Test":"TestFail","Elapsed":0.5}
{"Time":"2024-01-01T10:00:03Z","Action":"fail","Package":"example.com/foo","Elapsed":0.5}`

	result := filterGoTestJSON(output)
	for _, want := range []string{"1 failed", "TestFail", "expected 5, got 3"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
}

func TestFilterGoTestPreservesFileLocationAndFollowupContext(t *testing.T) {
	output := `{"Time":"2024-01-01T10:00:00Z","Action":"run","Package":"example.com/foo","Test":"TestFail"}
{"Time":"2024-01-01T10:00:01Z","Action":"output","Package":"example.com/foo","Test":"TestFail","Output":"=== RUN   TestFail\n"}
{"Time":"2024-01-01T10:00:02Z","Action":"output","Package":"example.com/foo","Test":"TestFail","Output":"    foo_test.go:42:\n"}
{"Time":"2024-01-01T10:00:03Z","Action":"output","Package":"example.com/foo","Test":"TestFail","Output":"        values differ after normalization\n"}
{"Time":"2024-01-01T10:00:04Z","Action":"fail","Package":"example.com/foo","Test":"TestFail","Elapsed":0.5}
{"Time":"2024-01-01T10:00:04Z","Action":"fail","Package":"example.com/foo","Elapsed":0.5}`

	result := filterGoTestJSON(output)
	for _, want := range []string{"foo_test.go:42:", "values differ after normalization"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
}

func TestFilterGoTestTimeoutPackageFail(t *testing.T) {
	// When go test times out, the JSON stream has a package-level "fail" with no
	// Test field and no FailedBuild field. This should be reported as a failure,
	// not "No tests found".
	output := `{"Time":"2024-01-01T10:00:00Z","Action":"start","Package":"example.com/foo"}
{"Time":"2024-01-01T10:01:03Z","Action":"output","Package":"example.com/foo","Output":"*** Test killed with quit: ran too long (1m3s).\n"}
{"Time":"2024-01-01T10:01:03Z","Action":"output","Package":"example.com/foo","Output":"FAIL\texample.com/foo\t63.001s\n"}
{"Time":"2024-01-01T10:01:03Z","Action":"fail","Package":"example.com/foo","Elapsed":63.003}`

	result := filterGoTestJSON(output)
	if !strings.Contains(result, "1 failed") {
		t.Errorf("expected '1 failed', got: %s", result)
	}
	if strings.Contains(result, "No tests found") {
		t.Errorf("should not say 'No tests found' on timeout, got: %s", result)
	}
	if !strings.Contains(result, "FAIL") {
		t.Errorf("expected failure output in summary, got: %s", result)
	}
}

func TestFilterGoTestNoDoubleCountOnTestFailure(t *testing.T) {
	// go test -json always emits a package-level {"action":"fail"} after each
	// test-level failure. That event is a cascade, not an additional failure.
	output := `{"Time":"2024-01-01T10:00:00Z","Action":"run","Package":"example.com/foo","Test":"TestFail"}
{"Time":"2024-01-01T10:00:01Z","Action":"output","Package":"example.com/foo","Test":"TestFail","Output":"=== RUN   TestFail\n"}
{"Time":"2024-01-01T10:00:02Z","Action":"output","Package":"example.com/foo","Test":"TestFail","Output":"    Error: expected 5, got 3\n"}
{"Time":"2024-01-01T10:00:03Z","Action":"fail","Package":"example.com/foo","Test":"TestFail","Elapsed":0.5}
{"Time":"2024-01-01T10:00:03Z","Action":"fail","Package":"example.com/foo","Elapsed":0.5}`

	result := filterGoTestJSON(output)
	if !strings.HasPrefix(result, "Go test: 0 passed, 1 failed") {
		t.Errorf("expected header 'Go test: 0 passed, 1 failed', got: %s", result)
	}
	if !strings.Contains(result, "TestFail") {
		t.Errorf("missing TestFail: %s", result)
	}
	if !strings.Contains(result, "expected 5, got 3") {
		t.Errorf("missing assertion text: %s", result)
	}
	// The package must NOT appear twice (once as "[FAIL]" and once with details).
	if n := strings.Count(result, "foo"); n != 1 {
		t.Errorf("package name should appear exactly once, got %d: %s", n, result)
	}
}

func TestFilterGoTestTimeoutWithSignalQuitOutput(t *testing.T) {
	// The signal: quit line appears as a separate JSON output event.
	output := `{"Action":"start","Package":"example.com/pkg"}
{"Action":"output","Package":"example.com/pkg","Output":"*** Test killed with quit: ran too long (1m30s).\n"}
{"Action":"output","Package":"example.com/pkg","Output":"signal: quit\n"}
{"Action":"output","Package":"example.com/pkg","Output":"FAIL\texample.com/pkg\t90.000s\n"}
{"Action":"fail","Package":"example.com/pkg","Elapsed":90.001}`

	result := filterGoTestJSON(output)
	if !strings.HasPrefix(result, "Go test: 0 passed, 1 failed") {
		t.Errorf("expected 'Go test: 0 passed, 1 failed', got: %s", result)
	}
	if strings.Contains(result, "No tests found") {
		t.Errorf("must not say 'No tests found' on timeout, got: %s", result)
	}
	if !strings.Contains(result, "Test killed with quit") {
		t.Errorf("should show the timeout message, got: %s", result)
	}
}

func TestFilterGoTestTimeoutWithPassingTestsBeforeKill(t *testing.T) {
	// Some tests pass before the package times out.
	output := `{"Action":"run","Package":"example.com/foo","Test":"TestFast"}
{"Action":"pass","Package":"example.com/foo","Test":"TestFast","Elapsed":0.001}
{"Action":"run","Package":"example.com/foo","Test":"TestHang"}
{"Action":"output","Package":"example.com/foo","Output":"*** Test killed with quit: ran too long (30s).\n"}
{"Action":"fail","Package":"example.com/foo","Elapsed":30.001}`

	result := filterGoTestJSON(output)
	if !strings.HasPrefix(result, "Go test: 1 passed, 1 failed") {
		t.Errorf("expected 'Go test: 1 passed, 1 failed', got: %s", result)
	}
	if strings.Contains(result, "No tests found") {
		t.Errorf("must not say 'No tests found', got: %s", result)
	}
	if !strings.Contains(result, "Test killed with quit") {
		t.Errorf("should show timeout message, got: %s", result)
	}
}

func TestFilterGoBuildSuccess(t *testing.T) {
	result := filterGoBuild("")
	for _, want := range []string{"Go build", "Success"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
}

func TestFilterGoBuildErrors(t *testing.T) {
	output := `# example.com/foo
main.go:10:5: undefined: missingFunc
main.go:15:2: cannot use x (type int) as type string`

	result := filterGoBuild(output)
	for _, want := range []string{"2 errors", "undefined: missingFunc", "cannot use x"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
}

func TestFilterGoBuildIgnoresDownloadLinesWithErrorInPackageNames(t *testing.T) {
	output := `go: downloading github.com/go-errors/errors v1.5.1
go: finding module for package example.com/foo
go: extracting github.com/pkg/errors v0.9.1
go: downloading github.com/pkg/errors v0.9.1
go: downloading github.com/hashicorp/go-multierror v1.1.1
go: downloading golang.org/x/xerrors v0.0.0-20220907171357-04be3eba64a2`

	result := filterGoBuild(output)
	if result != "Go build: Success" {
		t.Errorf("want 'Go build: Success', got: %s", result)
	}
}

func TestIsGoBuildErrorLineRecognizesRealCompilerErrors(t *testing.T) {
	truthy := []string{
		"undefined: missingFunc",
		`cannot find package "foo/bar"`,
		"found packages a (a.go) and b (b.go) in /tmp/rtk-go-build-probe-mix",
		"imports example.com/cycle/a: import cycle not allowed",
		"package example.com/buildtag: build constraints exclude all Go files in /tmp/rtk-go-build-probe-buildtag",
		"go.mod:3: invalid go version 'not-a-version': must match format 1.23.0",
		"go.work:1: invalid go version 'not-a-version': must match format 1.23.0",
		"go: go.mod file not found in current directory or any parent directory; see 'go help modules'",
		"no Go files in /tmp/example",
		"go: cannot load module missing listed in go.work file: open missing/go.mod: no such file or directory",
		"runtime.main_main·f: function main is undeclared in the main package",
		"main.go:10:5: undefined: missingFunc",
		"error: failed to load module",
	}
	for _, line := range truthy {
		if !isGoBuildErrorLine(line) {
			t.Errorf("expected error line: %q", line)
		}
	}

	falsy := []string{
		"go: downloading github.com/pkg/errors v0.9.1",
		"go: finding module for package example.com/foo",
		"go: extracting github.com/pkg/errors v0.9.1",
		"# example.com/foo",
	}
	for _, line := range falsy {
		if isGoBuildErrorLine(line) {
			t.Errorf("expected non-error line: %q", line)
		}
	}
}

func TestFilterGoBuildPreservesNonFileErrorShapes(t *testing.T) {
	output := `undefined: missingFunc
cannot find package "foo/bar"
found packages a (a.go) and b (b.go) in /tmp/rtk-go-build-probe-mix
imports example.com/cycle/a: import cycle not allowed
package example.com/buildtag: build constraints exclude all Go files in /tmp/rtk-go-build-probe-buildtag
runtime.main_main·f: function main is undeclared in the main package`

	result := filterGoBuild(output)
	for _, want := range []string{
		"6 errors",
		"undefined: missingFunc",
		`cannot find package "foo/bar"`,
		"found packages a (a.go) and b (b.go)",
		"import cycle not allowed",
		"build constraints exclude all Go files",
		"function main is undeclared in the main package",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
}

func TestFilterGoBuildPreservesGoConfigParseErrors(t *testing.T) {
	output := `go: errors parsing go.mod:
go.mod:3: invalid go version 'not-a-version': must match format 1.23.0
go: errors parsing go.work:
go.work:1: invalid go version 'not-a-version': must match format 1.23.0`

	result := filterGoBuild(output)
	for _, want := range []string{"2 errors", "go.mod:3: invalid go version", "go.work:1: invalid go version"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
	for _, bad := range []string{"go: errors parsing go.mod:", "go: errors parsing go.work:"} {
		if strings.Contains(result, bad) {
			t.Errorf("should not contain %q: %s", bad, result)
		}
	}
}

func TestFilterGoBuildPreservesModuleRootAndWorkspaceErrors(t *testing.T) {
	output := `go: go.mod file not found in current directory or any parent directory; see 'go help modules'
no Go files in /tmp/example
go: cannot load module missing listed in go.work file: open missing/go.mod: no such file or directory`

	result := filterGoBuild(output)
	for _, want := range []string{
		"3 errors",
		"go.mod file not found in current directory or any parent directory",
		"no Go files in /tmp/example",
		"go: cannot load module missing listed in go.work file",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
}

func TestFilterGoBuildPreservesPackagePatternErrors(t *testing.T) {
	output := `pattern ./...: directory prefix . does not contain main module or its selected dependencies
pattern ./...: directory prefix . does not contain modules listed in go.work or their selected dependencies`

	result := filterGoBuild(output)
	for _, want := range []string{"2 errors", "does not contain main module", "does not contain modules listed in go.work"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
	if strings.Contains(result, "Success") {
		t.Errorf("should not contain Success: %s", result)
	}
}

func TestFilterGoBuildNonzeroExitNeverReportsSuccess(t *testing.T) {
	output := "opaque go build failure from stderr"

	result := filterGoBuildWithExit(output, 1)
	if !strings.Contains(result, "Go build: failed (exit 1)") {
		t.Errorf("missing failure header: %s", result)
	}
	if !strings.Contains(result, output) {
		t.Errorf("should echo opaque output: %s", result)
	}
	if strings.Contains(result, "Success") {
		t.Errorf("should not contain Success: %s", result)
	}
}

func TestFilterGoVetNoIssues(t *testing.T) {
	result := filterGoVet("")
	for _, want := range []string{"Go vet", "No issues found"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
}

func TestFilterGoVetWithIssues(t *testing.T) {
	output := `main.go:42:2: Printf format %d has arg x of wrong type string
utils.go:15:5: unreachable code`

	result := filterGoVet(output)
	for _, want := range []string{"2 issues", "Printf format", "unreachable code"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
}

func TestCompactPackageName(t *testing.T) {
	cases := map[string]string{
		"github.com/user/repo/pkg": "pkg",
		"example.com/foo":          "foo",
		"simple":                   "simple",
	}
	for in, want := range cases {
		if got := compactPackageName(in); got != want {
			t.Errorf("compactPackageName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestTruncate locks the ported rtk utils::truncate semantics (rune-count based,
// "..." suffix, min usable length 3). Mirrors the doc-test examples in rtk.
func TestTruncate(t *testing.T) {
	cases := []struct {
		in     string
		maxLen int
		want   string
	}{
		{"hello world", 8, "hello..."},
		{"hi", 10, "hi"},
		{"abc", 3, "abc"},
		{"abcd", 2, "..."},
		{"exact", 5, "exact"},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.maxLen); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.maxLen, got, c.want)
		}
	}
}
