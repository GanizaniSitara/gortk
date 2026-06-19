package git

import (
	"strings"
	"testing"
)

// --- similarity (ported from diff_cmd.rs) ---

func TestSimilarityIdentical(t *testing.T) {
	if got := similarity("hello", "hello"); got != 1.0 {
		t.Errorf("similarity identical = %v, want 1.0", got)
	}
}

func TestSimilarityCompletelyDifferent(t *testing.T) {
	if got := similarity("abc", "xyz"); got != 0.0 {
		t.Errorf("similarity completely different = %v, want 0.0", got)
	}
}

func TestSimilarityEmptyStrings(t *testing.T) {
	// Both empty: union is 0, returns 1.0 by convention.
	if got := similarity("", ""); got != 1.0 {
		t.Errorf("similarity empty = %v, want 1.0", got)
	}
}

func TestSimilarityPartialOverlap(t *testing.T) {
	// Shared: a, b. Union: a, b, c, d, e, f = 6. Jaccard = 2/6.
	s := similarity("abcd", "abef")
	want := 2.0 / 6.0
	if diff := s - want; diff > 1e-12 || diff < -1e-12 {
		t.Errorf("similarity partial = %v, want %v", s, want)
	}
}

func TestSimilarityThresholdForModified(t *testing.T) {
	if similarity("let x = 1;", "let x = 2;") <= 0.5 {
		t.Errorf("expected > 0.5 for similar lines")
	}
}

// --- compute_diff ---

func TestComputeDiffIdentical(t *testing.T) {
	a := []string{"line1", "line2", "line3"}
	b := []string{"line1", "line2", "line3"}
	res := computeDiff(a, b)
	if res.added != 0 || res.removed != 0 || res.modified != 0 || len(res.changes) != 0 {
		t.Errorf("identical diff not empty: %+v", res)
	}
}

func TestComputeDiffAddedLines(t *testing.T) {
	res := computeDiff([]string{"line1"}, []string{"line1", "line2", "line3"})
	if res.added != 2 || res.removed != 0 {
		t.Errorf("added=%d removed=%d, want 2/0", res.added, res.removed)
	}
}

func TestComputeDiffRemovedLines(t *testing.T) {
	res := computeDiff([]string{"line1", "line2", "line3"}, []string{"line1"})
	if res.removed != 2 || res.added != 0 {
		t.Errorf("removed=%d added=%d, want 2/0", res.removed, res.added)
	}
}

func TestComputeDiffModifiedLine(t *testing.T) {
	res := computeDiff([]string{"let x = 1;"}, []string{"let x = 2;"})
	if res.modified != 1 || res.added != 0 || res.removed != 0 {
		t.Errorf("modified=%d added=%d removed=%d, want 1/0/0", res.modified, res.added, res.removed)
	}
}

func TestComputeDiffCompletelyDifferentLine(t *testing.T) {
	res := computeDiff([]string{"aaaa"}, []string{"zzzz"})
	if res.modified != 0 || res.added != 1 || res.removed != 1 {
		t.Errorf("modified=%d added=%d removed=%d, want 0/1/1", res.modified, res.added, res.removed)
	}
}

func TestComputeDiffEmptyInputs(t *testing.T) {
	res := computeDiff(nil, nil)
	if res.added != 0 || res.removed != 0 || len(res.changes) != 0 {
		t.Errorf("empty inputs not empty: %+v", res)
	}
}

// --- render_file_diff (issue #2364 regression) ---

func TestRenderModifiedOnlyYamlNotIdentical(t *testing.T) {
	out, code := renderFileDiff("one.yaml", "two.yaml", "a: 1\n", "a: 2\n")
	if strings.Contains(out, "identical") {
		t.Errorf("modified-only diff reported as identical:\n%s", out)
	}
	if !strings.Contains(out, "~1 modified") {
		t.Errorf("missing ~1 modified:\n%s", out)
	}
	if !strings.Contains(out, "a: 1") || !strings.Contains(out, "a: 2") {
		t.Errorf("missing modified content:\n%s", out)
	}
	if code != 1 {
		t.Errorf("differing files must exit 1, got %d", code)
	}
}

func TestRenderModifiedOnlyJsonNotIdentical(t *testing.T) {
	out, code := renderFileDiff("j1.json", "j2.json", "{\"a\": 1}\n", "{\"a\": 2}\n")
	if strings.Contains(out, "identical") {
		t.Errorf("modified-only diff reported as identical:\n%s", out)
	}
	if code != 1 {
		t.Errorf("want exit 1, got %d", code)
	}
}

func TestRenderIdenticalFilesExitZero(t *testing.T) {
	out, code := renderFileDiff("a.yaml", "b.yaml", "a: 1\nb: 2\n", "a: 1\nb: 2\n")
	if !strings.Contains(out, "[ok] Files are identical") {
		t.Errorf("missing identical message:\n%s", out)
	}
	if code != 0 {
		t.Errorf("want exit 0, got %d", code)
	}
}

func TestRenderAddedRemovedExitOne(t *testing.T) {
	out, code := renderFileDiff("t1.txt", "t2.txt", "x\n", "y\n")
	if !strings.Contains(out, "+1 added, -1 removed") {
		t.Errorf("missing added/removed counts:\n%s", out)
	}
	if code != 1 {
		t.Errorf("want exit 1, got %d", code)
	}
}

// --- condense_unified_diff ---

func TestCondenseUnifiedDiffSingleFile(t *testing.T) {
	diff := `diff --git a/src/main.rs b/src/main.rs
--- a/src/main.rs
+++ b/src/main.rs
@@ -1,3 +1,4 @@
 fn main() {
+    println!("hello");
     println!("world");
 }
`
	result := condenseUnifiedDiff(diff)
	if !strings.Contains(result, "src/main.rs") {
		t.Errorf("missing filename:\n%s", result)
	}
	if !strings.Contains(result, "+1") {
		t.Errorf("missing +1 count:\n%s", result)
	}
	if !strings.Contains(result, "println") {
		t.Errorf("missing content:\n%s", result)
	}
}

func TestCondenseUnifiedDiffMultipleFiles(t *testing.T) {
	diff := `diff --git a/a.rs b/a.rs
--- a/a.rs
+++ b/a.rs
+added line
diff --git a/b.rs b/b.rs
--- a/b.rs
+++ b/b.rs
-removed line
`
	result := condenseUnifiedDiff(diff)
	if !strings.Contains(result, "a.rs") || !strings.Contains(result, "b.rs") {
		t.Errorf("missing filenames:\n%s", result)
	}
}

func TestCondenseUnifiedDiffEmpty(t *testing.T) {
	if result := condenseUnifiedDiff(""); result != "" {
		t.Errorf("want empty, got %q", result)
	}
}

// --- truncation accuracy ---

func makeLargeUnifiedDiff(added, removed int) string {
	lines := []string{
		"diff --git a/config.yaml b/config.yaml",
		"--- a/config.yaml",
		"+++ b/config.yaml",
		"@@ -1,200 +1,200 @@",
	}
	for i := 0; i < removed; i++ {
		lines = append(lines, "-old_value_"+itoa(i))
	}
	for i := 0; i < added; i++ {
		lines = append(lines, "+new_value_"+itoa(i))
	}
	return strings.Join(lines, "\n")
}

func TestCondenseUnifiedDiffOverflowCountAccuracy(t *testing.T) {
	// 100 added + 100 removed = 200 total changes, only 10 shown.
	// True overflow = 200 - 10 = 190.
	diff := makeLargeUnifiedDiff(100, 100)
	result := condenseUnifiedDiff(diff)
	if !strings.Contains(result, "+190 more") {
		t.Errorf("expected '+190 more', got:\n%s", result)
	}
	if strings.Contains(result, "+5 more") {
		t.Errorf("bug present: showing '+5 more'")
	}
}

func TestCondenseUnifiedDiffNoFalseOverflow(t *testing.T) {
	// 8 changes total — all fit within the 10-line display cap, no overflow.
	diff := makeLargeUnifiedDiff(4, 4)
	result := condenseUnifiedDiff(diff)
	if strings.Contains(result, "more") {
		t.Errorf("no overflow expected for 8 changes, got:\n%s", result)
	}
}

func TestNoTruncationLargeDiff(t *testing.T) {
	var a, b []string
	for i := 0; i < 500; i++ {
		a = append(a, "line_"+itoa(i))
		if i%3 == 0 {
			b = append(b, "CHANGED_"+itoa(i))
		} else {
			b = append(b, "line_"+itoa(i))
		}
	}
	res := computeDiff(a, b)
	if len(res.changes) <= 100 {
		t.Errorf("expected 100+ changes, got %d", len(res.changes))
	}
}

func TestFormatDiffShowsAllChanges(t *testing.T) {
	var a, b []string
	for i := 0; i < 100; i++ {
		a = append(a, "old_line_"+itoa(i))
		b = append(b, "new_line_"+itoa(i))
	}
	diff := computeDiff(a, b)
	output := formatDiffChanges(diff)
	if !strings.Contains(output, "old_line_0") {
		t.Errorf("should contain first change")
	}
	if !strings.Contains(output, "new_line_99") {
		t.Errorf("should contain last change")
	}
}

func TestLongLinesNotTruncated(t *testing.T) {
	longLine := strings.Repeat("x", 500)
	res := computeDiff([]string{longLine}, []string{"short"})
	if len(res.changes) == 0 {
		t.Fatal("expected at least one change")
	}
	c := res.changes[0]
	switch c.kind {
	case changeRemoved, changeAdded:
		if len(c.old) != 500 {
			t.Errorf("line was truncated: len=%d", len(c.old))
		}
	case changeModified:
		if len(c.old) != 500 {
			t.Errorf("line was truncated: len=%d", len(c.old))
		}
	}
}

// itoa is a tiny dependency-free int→string helper for the table builders.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
