package summary

import (
	"strings"
	"testing"
)

// rtk's summary.rs ships no #[cfg(test)] block, so these tests pin the ported
// heuristic behaviour directly against the pure helper functions.

func TestTruncate(t *testing.T) {
	cases := []struct {
		in     string
		maxLen int
		want   string
	}{
		{"hello", 10, "hello"},
		{"hello", 5, "hello"},
		{"hello world", 8, "hello..."},
		{"abc", 2, "..."},
		{"abcdef", 3, "..."}, // max_len==3 -> take 0 chars + "..."
		{"abcdef", 4, "a..."},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.maxLen); got != c.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", c.in, c.maxLen, got, c.want)
		}
	}
}

func TestExtractNumber(t *testing.T) {
	if n, ok := extractNumber("12 passed", "passed"); !ok || n != 12 {
		t.Errorf("extractNumber 12 passed = %d,%v want 12,true", n, ok)
	}
	if n, ok := extractNumber("3 failed, 1 passed", "failed"); !ok || n != 3 {
		t.Errorf("extractNumber failed = %d,%v want 3,true", n, ok)
	}
	if _, ok := extractNumber("no numbers here", "passed"); ok {
		t.Errorf("extractNumber should fail when keyword/number absent")
	}
}

func TestDetectOutputTypeTests(t *testing.T) {
	if got := detectOutputType("running 3 tests", "cargo test"); got != outputTestResults {
		t.Errorf("command with 'test' -> %v, want TestResults", got)
	}
	if got := detectOutputType("5 passed; 2 failed", "run"); got != outputTestResults {
		t.Errorf("passed+failed -> %v, want TestResults", got)
	}
}

func TestDetectOutputTypeBuild(t *testing.T) {
	if got := detectOutputType("linking", "make build"); got != outputBuildOutput {
		t.Errorf("command with 'build' -> %v, want BuildOutput", got)
	}
	if got := detectOutputType("Compiling foo v0.1", "make"); got != outputBuildOutput {
		t.Errorf("'compiling' output -> %v, want BuildOutput", got)
	}
}

func TestDetectOutputTypeLog(t *testing.T) {
	if got := detectOutputType("error: something broke", "run"); got != outputLogOutput {
		t.Errorf("'error:' -> %v, want LogOutput", got)
	}
	if got := detectOutputType("[info] starting", "run"); got != outputLogOutput {
		t.Errorf("'[info]' -> %v, want LogOutput", got)
	}
}

func TestDetectOutputTypeJSON(t *testing.T) {
	if got := detectOutputType(`  {"a":1}`, "curl"); got != outputJSONOutput {
		t.Errorf("object -> %v, want JSONOutput", got)
	}
	if got := detectOutputType("[1,2,3]", "curl"); got != outputJSONOutput {
		t.Errorf("array -> %v, want JSONOutput", got)
	}
}

func TestDetectOutputTypeList(t *testing.T) {
	if got := detectOutputType("alpha\nbravo\ncharlie", "ls"); got != outputListOutput {
		t.Errorf("short lines -> %v, want ListOutput", got)
	}
}

func TestDetectOutputTypeGeneric(t *testing.T) {
	// A line with a tab disqualifies the list path; with no other signal it is
	// generic.
	if got := detectOutputType("col1\tcol2\tval", "run"); got != outputGeneric {
		t.Errorf("tabbed line -> %v, want Generic", got)
	}
	// A line with 10+ words is also non-list -> generic.
	long := "one two three four five six seven eight nine ten eleven"
	if got := detectOutputType(long, "run"); got != outputGeneric {
		t.Errorf("10+ word line -> %v, want Generic", got)
	}
}

func TestSummarizeTests(t *testing.T) {
	out := "test result: ok. 5 passed; 2 failed; 1 skipped\nFAILED tests::foo\n"
	got := strings.Join(summarizeTests(out, nil), "\n")
	if !strings.Contains(got, "Test Results:") {
		t.Errorf("missing header: %s", got)
	}
	if !strings.Contains(got, "[ok] 5 passed") {
		t.Errorf("missing passed count: %s", got)
	}
	if !strings.Contains(got, "[FAIL] 2 failed") {
		t.Errorf("missing failed count: %s", got)
	}
	if !strings.Contains(got, "skip 1 skipped") {
		t.Errorf("missing skipped count: %s", got)
	}
	if !strings.Contains(got, "Failures:") {
		t.Errorf("missing failures section: %s", got)
	}
}

func TestSummarizeBuildSuccess(t *testing.T) {
	out := "Compiling foo\nCompiling bar\nFinished\n"
	got := strings.Join(summarizeBuild(out, nil), "\n")
	if !strings.Contains(got, "2 crates/files compiled") {
		t.Errorf("missing compiled count: %s", got)
	}
	if !strings.Contains(got, "[ok] Build successful") {
		t.Errorf("missing success line: %s", got)
	}
}

func TestSummarizeBuildErrors(t *testing.T) {
	out := "error[E0001]: bad thing\nwarning: unused var\n"
	got := strings.Join(summarizeBuild(out, nil), "\n")
	if !strings.Contains(got, "[error] 1 errors") {
		t.Errorf("missing error count: %s", got)
	}
	if !strings.Contains(got, "[warn] 1 warnings") {
		t.Errorf("missing warning count: %s", got)
	}
	if !strings.Contains(got, "Errors:") {
		t.Errorf("missing errors section: %s", got)
	}
}

func TestSummarizeLogsQuick(t *testing.T) {
	out := "error: boom\nWARN: careful\ninfo: ok\nfatal: dead\n"
	got := strings.Join(summarizeLogsQuick(out, nil), "\n")
	// error + fatal = 2 errors; warn = 1; info line follows error/warn so only
	// the pure "info:" line counts (else-if chain).
	if !strings.Contains(got, "[error] 2 errors") {
		t.Errorf("expected 2 errors: %s", got)
	}
	if !strings.Contains(got, "[warn] 1 warnings") {
		t.Errorf("expected 1 warning: %s", got)
	}
	if !strings.Contains(got, "[info] 1 info") {
		t.Errorf("expected 1 info: %s", got)
	}
}

func TestSummarizeList(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 15; i++ {
		sb.WriteString("item\n")
	}
	got := strings.Join(summarizeList(sb.String(), nil), "\n")
	if !strings.Contains(got, "List (15 items):") {
		t.Errorf("missing list header: %s", got)
	}
	// 15 > CAP_WARNINGS (10) so a "+5 more" suffix appears.
	if !strings.Contains(got, "... +5 more") {
		t.Errorf("missing overflow suffix: %s", got)
	}
}

func TestSummarizeJSONObject(t *testing.T) {
	// preserve_order: keys appear in insertion order, not sorted.
	out := `{"zebra": 1, "apple": 2, "mango": 3}`
	got := strings.Join(summarizeJSON(out, nil), "\n")
	if !strings.Contains(got, "Object with 3 keys:") {
		t.Errorf("missing object header: %s", got)
	}
	zi := strings.Index(got, "zebra")
	ai := strings.Index(got, "apple")
	mi := strings.Index(got, "mango")
	if !(zi >= 0 && ai > zi && mi > ai) {
		t.Errorf("keys not in insertion order: %s", got)
	}
}

func TestSummarizeJSONArray(t *testing.T) {
	got := strings.Join(summarizeJSON("[10, 20, 30]", nil), "\n")
	if !strings.Contains(got, "Array with 3 items") {
		t.Errorf("missing array count: %s", got)
	}
}

func TestSummarizeJSONInvalid(t *testing.T) {
	got := strings.Join(summarizeJSON("{not valid", nil), "\n")
	if !strings.Contains(got, "(Invalid JSON)") {
		t.Errorf("expected invalid-JSON message: %s", got)
	}
}

func TestSummarizeJSONScalar(t *testing.T) {
	got := strings.Join(summarizeJSON("42", nil), "\n")
	if !strings.Contains(got, "42") {
		t.Errorf("expected scalar value 42: %s", got)
	}
}

func TestSummarizeGenericTruncatesAndTails(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 12; i++ {
		sb.WriteString("line\n")
	}
	got := strings.Join(summarizeGeneric(sb.String(), nil), "\n")
	if !strings.Contains(got, "Output:") {
		t.Errorf("missing output header: %s", got)
	}
	// >10 lines -> shows a "..." separator before the tail.
	if !strings.Contains(got, "   ...") {
		t.Errorf("expected tail separator for >10 lines: %s", got)
	}
}

func TestSummarizeOutputStatusLine(t *testing.T) {
	gotOK := summarizeOutput("hello\n", "echo hello", true)
	if !strings.HasPrefix(gotOK, "[ok] Command: echo hello") {
		t.Errorf("ok status line wrong: %q", gotOK)
	}
	gotFail := summarizeOutput("boom\n", "false", false)
	if !strings.HasPrefix(gotFail, "[FAIL] Command: false") {
		t.Errorf("fail status line wrong: %q", gotFail)
	}
}

func TestParseJSONShapeOrderedKeys(t *testing.T) {
	kind, count, keys, _, ok := parseJSONShape(`{"b":1,"a":2,"c":3}`)
	if !ok || kind != jsonObject || count != 3 {
		t.Fatalf("got kind=%v count=%d ok=%v", kind, count, ok)
	}
	if strings.Join(keys, ",") != "b,a,c" {
		t.Errorf("keys = %v, want [b a c]", keys)
	}
}

func TestParseJSONShapeRejectsTrailing(t *testing.T) {
	if _, _, _, _, ok := parseJSONShape(`{"a":1} garbage`); ok {
		t.Errorf("trailing garbage should be rejected")
	}
}
