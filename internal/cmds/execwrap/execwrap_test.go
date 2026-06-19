package execwrap

import (
	"reflect"
	"strings"
	"testing"
)

// These tests are ported from rtk's #[cfg(test)] modules:
//   - shellSplit cases come from discover::lexer::shell_split tests in
//     src/discover/lexer.rs (and the mirrored ones in main.rs).
//   - filterErrors comes from src/cmds/rust/runner.rs's test_filter_errors.
// They exercise the pure helpers directly, as the porting contract requires.

func TestShellSplitSimple(t *testing.T) {
	got := shellSplit("head -50 file.php")
	want := []string{"head", "-50", "file.php"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestShellSplitDoubleQuotes(t *testing.T) {
	got := shellSplit(`git log --format="%H %s"`)
	want := []string{"git", "log", "--format=%H %s"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestShellSplitSingleQuotes(t *testing.T) {
	got := shellSplit("grep -r 'hello world' .")
	want := []string{"grep", "-r", "hello world", "."}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestShellSplitSingleWord(t *testing.T) {
	got := shellSplit("ls")
	want := []string{"ls"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestShellSplitEmpty(t *testing.T) {
	got := shellSplit("")
	if len(got) != 0 {
		t.Errorf("expected empty, got %#v", got)
	}
}

func TestShellSplitBackslashEscape(t *testing.T) {
	got := shellSplit(`echo hello\ world`)
	want := []string{"echo", "hello world"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestShellSplitUnclosedQuote(t *testing.T) {
	got := shellSplit("echo 'hello")
	want := []string{"echo", "hello"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestShellSplitMixedQuotes(t *testing.T) {
	got := shellSplit(`echo "it's" 'a "test"'`)
	want := []string{"echo", "it's", `a "test"`}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestShellSplitTabs(t *testing.T) {
	got := shellSplit("a\tb\tc")
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

func TestShellSplitMultipleSpaces(t *testing.T) {
	got := shellSplit("a   b   c")
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
}

// test_filter_errors from src/cmds/rust/runner.rs.
func TestFilterErrors(t *testing.T) {
	output := "info: compiling\nerror: something failed\n  at line 10\ninfo: done"
	filtered := filterErrors(output)
	if !strings.Contains(filtered, "error") {
		t.Errorf("expected 'error' in filtered, got %q", filtered)
	}
	if strings.Contains(filtered, "info") {
		t.Errorf("did not expect 'info' in filtered, got %q", filtered)
	}
}
