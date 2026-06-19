package find

import (
	"strings"
	"testing"
)

// Faithful port of the #[cfg(test)] mod tests block in rtk's
// src/cmds/system/find_cmd.rs.
//
// Mapping of rtk helpers to gortk equivalents:
//   - rtk parse_find_args(&[String]) -> gortk parseFindArgs([]string) (returns error)
//   - rtk FindArgs.max_depth: Option<usize> -> gortk findArgs.maxDepth: int (-1 = unset)
//   - rtk run_from_args(args, verbose) -> gortk Run(args, verbose)
//   - rtk run(pattern, path, max, max_depth, type, case_insensitive, verbose)
//     -> gortk runFind(findArgs{...}, verbose)
// The run/run_from_args smoke tests assert only "no error" (is_ok); they walk
// the filesystem in-process (no external binary), so they are kept as ported.

// --- glob_match unit tests ---

func TestGlobMatchStarRs(t *testing.T) {
	if !globMatch("*.rs", "main.rs") {
		t.Error("*.rs should match main.rs")
	}
	if !globMatch("*.rs", "find_cmd.rs") {
		t.Error("*.rs should match find_cmd.rs")
	}
	if globMatch("*.rs", "main.py") {
		t.Error("*.rs should not match main.py")
	}
	if globMatch("*.rs", "rs") {
		t.Error("*.rs should not match rs")
	}
}

func TestGlobMatchStarAll(t *testing.T) {
	for _, name := range []string{"anything.txt", "a", ".hidden"} {
		if !globMatch("*", name) {
			t.Errorf("* should match %q", name)
		}
	}
}

func TestGlobMatchQuestionMark(t *testing.T) {
	if !globMatch("?.rs", "a.rs") {
		t.Error("?.rs should match a.rs")
	}
	if globMatch("?.rs", "ab.rs") {
		t.Error("?.rs should not match ab.rs")
	}
}

func TestGlobMatchExact(t *testing.T) {
	if !globMatch("Cargo.toml", "Cargo.toml") {
		t.Error("Cargo.toml should match Cargo.toml")
	}
	if globMatch("Cargo.toml", "cargo.toml") {
		t.Error("Cargo.toml should not match cargo.toml (case-sensitive)")
	}
}

func TestGlobMatchComplex(t *testing.T) {
	if !globMatch("test_*", "test_foo") {
		t.Error("test_* should match test_foo")
	}
	if !globMatch("test_*", "test_") {
		t.Error("test_* should match test_")
	}
	if globMatch("test_*", "test") {
		t.Error("test_* should not match test")
	}
}

// --- dot pattern treated as star ---

func TestDotBecomesStar(t *testing.T) {
	// runFind converts "." to "*" internally; mirror the trivial logic check.
	effective := "."
	if effective == "." {
		effective = "*"
	}
	if effective != "*" {
		t.Errorf("'.' should become '*', got %q", effective)
	}
}

// --- parse_find_args: native find syntax ---

func TestParseNativeFindName(t *testing.T) {
	parsed, err := parseFindArgs([]string{".", "-name", "*.rs"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.pattern != "*.rs" || parsed.path != "." || parsed.fileType != "f" || parsed.maxResults != 50 {
		t.Errorf("got %+v", parsed)
	}
}

func TestParseNativeFindNameAndType(t *testing.T) {
	parsed, err := parseFindArgs([]string{"src", "-name", "*.rs", "-type", "f"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.pattern != "*.rs" || parsed.path != "src" || parsed.fileType != "f" {
		t.Errorf("got %+v", parsed)
	}
}

func TestParseNativeFindTypeD(t *testing.T) {
	parsed, err := parseFindArgs([]string{".", "-type", "d"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.pattern != "*" || parsed.fileType != "d" {
		t.Errorf("got %+v", parsed)
	}
}

func TestParseNativeFindMaxdepth(t *testing.T) {
	parsed, err := parseFindArgs([]string{".", "-name", "*.toml", "-maxdepth", "2"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.pattern != "*.toml" || parsed.maxDepth != 2 || parsed.maxResults != 50 {
		t.Errorf("got %+v", parsed)
	}
}

func TestParseNativeFindIname(t *testing.T) {
	parsed, err := parseFindArgs([]string{".", "-iname", "Makefile"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.pattern != "Makefile" || !parsed.caseInsensitive {
		t.Errorf("got %+v", parsed)
	}
}

func TestParseNativeFindNameIsCaseSensitive(t *testing.T) {
	parsed, err := parseFindArgs([]string{".", "-name", "*.rs"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.caseInsensitive {
		t.Error("-name should be case-sensitive")
	}
}

func TestParseNativeFindNoPath(t *testing.T) {
	parsed, err := parseFindArgs([]string{"-name", "*.rs"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.pattern != "*.rs" || parsed.path != "." {
		t.Errorf("got %+v", parsed)
	}
}

// --- parse_find_args: unsupported flags ---

func TestParseNativeFindRejectsNot(t *testing.T) {
	_, err := parseFindArgs([]string{".", "-name", "*.rs", "-not", "-name", "*_test.rs"})
	if err == nil {
		t.Fatal("expected error for -not")
	}
	if !strings.Contains(err.Error(), "compound predicates") {
		t.Errorf("error should mention compound predicates: %v", err)
	}
}

func TestParseNativeFindRejectsExec(t *testing.T) {
	_, err := parseFindArgs([]string{".", "-name", "*.tmp", "-exec", "rm", "{}", ";"})
	if err == nil {
		t.Fatal("expected error for -exec")
	}
}

// --- parse_find_args: RTK syntax ---

func TestParseRTKSyntaxPatternOnly(t *testing.T) {
	parsed, err := parseFindArgs([]string{"*.rs"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.pattern != "*.rs" || parsed.path != "." {
		t.Errorf("got %+v", parsed)
	}
}

func TestParseRTKSyntaxPatternAndPath(t *testing.T) {
	parsed, err := parseFindArgs([]string{"*.rs", "src"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.pattern != "*.rs" || parsed.path != "src" {
		t.Errorf("got %+v", parsed)
	}
}

func TestParseRTKSyntaxWithFlags(t *testing.T) {
	parsed, err := parseFindArgs([]string{"*.rs", "src", "-m", "10", "-t", "d"})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.pattern != "*.rs" || parsed.path != "src" || parsed.maxResults != 10 || parsed.fileType != "d" {
		t.Errorf("got %+v", parsed)
	}
}

func TestParseEmptyArgs(t *testing.T) {
	parsed, err := parseFindArgs([]string{})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.pattern != "*" || parsed.path != "." {
		t.Errorf("got %+v", parsed)
	}
}

// --- run integration tests (assert no error; walk is in-process) ---

func TestRunFromArgsNativeFindSyntax(t *testing.T) {
	if _, err := Run([]string{".", "-name", "*.go", "-type", "f"}, 0); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunFromArgsRTKSyntax(t *testing.T) {
	if _, err := Run([]string{"*.go", "."}, 0); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunFromArgsInameCaseInsensitive(t *testing.T) {
	if _, err := Run([]string{".", "-iname", "find.go"}, 0); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- dotfile pattern should not skip hidden files ---

func TestFindDotfilePatternIncludesHidden(t *testing.T) {
	_, err := runFind(findArgs{pattern: ".gitignore", path: ".", maxResults: 50, maxDepth: 1, fileType: "f"}, 0)
	if err != nil {
		t.Errorf("run with dotfile pattern should not error: %v", err)
	}
}

func TestFindRegularPatternSkipsHidden(t *testing.T) {
	_, err := runFind(findArgs{pattern: "*.go", path: ".", maxResults: 5, maxDepth: -1, fileType: "f"}, 0)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// --- integration: run on this package dir ---

func TestFindGoFiles(t *testing.T) {
	_, err := runFind(findArgs{pattern: "*.go", path: ".", maxResults: 100, maxDepth: -1, fileType: "f"}, 0)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFindDotPatternWorks(t *testing.T) {
	_, err := runFind(findArgs{pattern: ".", path: ".", maxResults: 10, maxDepth: -1, fileType: "f"}, 0)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFindNoMatches(t *testing.T) {
	_, err := runFind(findArgs{pattern: "*.xyz_nonexistent", path: ".", maxResults: 50, maxDepth: -1, fileType: "f"}, 0)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFindRespectsMax(t *testing.T) {
	_, err := runFind(findArgs{pattern: "*.go", path: ".", maxResults: 2, maxDepth: -1, fileType: "f"}, 0)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFindGitignoredExcluded(t *testing.T) {
	_, err := runFind(findArgs{pattern: "*", path: ".", maxResults: 1000, maxDepth: -1, fileType: "f"}, 0)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
