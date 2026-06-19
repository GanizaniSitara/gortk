package tomlfilter

import (
	"strings"
	"testing"
)

// TestBuiltinFiltersLoad ensures the embedded builtin filters all compiled.
func TestBuiltinFiltersLoad(t *testing.T) {
	all := All()
	if len(all) < 40 {
		t.Fatalf("expected the full set of builtin filters to load, got %d", len(all))
	}
}

// TestMakeFilterMatches verifies command matching routes `make` correctly.
func TestMakeFilterMatches(t *testing.T) {
	f := FindMatching("make build")
	if f == nil || f.Name != "make" {
		t.Fatalf("expected make filter for 'make build', got %v", f)
	}
}

// TestMakeFilterApply exercises strip_lines_matching + on_empty from make.toml.
func TestMakeFilterApply(t *testing.T) {
	f := FindMatching("make")
	if f == nil {
		t.Fatal("no make filter")
	}

	// Entering/Leaving lines are stripped.
	in := "make[1]: Entering directory '/home/user'\n" +
		"gcc -O2 foo.c\n" +
		"make[1]: Leaving directory '/home/user'\n"
	got := f.Apply(in)
	if got != "gcc -O2 foo.c" {
		t.Errorf("strip: got %q", got)
	}

	// Everything stripped -> on_empty message.
	in2 := "make[1]: Entering directory '/home/user'\n" +
		"make[1]: Leaving directory '/home/user'\n"
	got2 := f.Apply(in2)
	if got2 != "make: ok" {
		t.Errorf("on_empty: got %q", got2)
	}
}

// TestInlineTestsPass runs every builtin filter's own inline tests, so the Go
// engine is validated against the upstream-authored expectations.
func TestInlineTestsPass(t *testing.T) {
	failures := 0
	for _, f := range All() {
		for _, tc := range f.Tests() {
			got := f.Apply(tc.Input)
			if strings.TrimRight(got, "\n") != strings.TrimRight(tc.Expected, "\n") {
				failures++
				if failures <= 20 {
					t.Errorf("filter %q test %q:\n got: %q\nwant: %q", f.Name, tc.Name, got, tc.Expected)
				}
			}
		}
	}
	if failures > 0 {
		t.Logf("%d inline filter test(s) failed", failures)
	}
}
