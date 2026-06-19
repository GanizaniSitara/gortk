package readcmd

import (
	"strings"
	"testing"

	"gortk/internal/core"
)

// Ported from rtk read.rs test_apply_line_window_tail_lines.
func TestApplyLineWindowTailLines(t *testing.T) {
	input := "a\nb\nc\nd\n"
	tail := 2
	got := applyLineWindow(input, nil, &tail)
	if got != "c\nd\n" {
		t.Errorf("apply_line_window tail = %q, want %q", got, "c\nd\n")
	}
}

// Ported from rtk read.rs test_apply_line_window_tail_lines_no_trailing_newline.
func TestApplyLineWindowTailLinesNoTrailingNewline(t *testing.T) {
	input := "a\nb\nc\nd"
	tail := 2
	got := applyLineWindow(input, nil, &tail)
	if got != "c\nd" {
		t.Errorf("apply_line_window tail = %q, want %q", got, "c\nd")
	}
}

// Ported from rtk read.rs test_apply_line_window_max_lines_still_works.
func TestApplyLineWindowMaxLinesStillWorks(t *testing.T) {
	input := "a\nb\nc\nd\n"
	max := 2
	got := applyLineWindow(input, &max, nil)
	if !strings.HasPrefix(got, "a\n") {
		t.Errorf("apply_line_window max should start with %q, got %q", "a\n", got)
	}
	if !strings.Contains(got, "more lines") {
		t.Errorf("apply_line_window max should contain %q, got %q", "more lines", got)
	}
}

// tail of 0 returns empty (Rust: tail == 0 => String::new()).
func TestApplyLineWindowTailZero(t *testing.T) {
	tail := 0
	if got := applyLineWindow("a\nb\nc\n", nil, &tail); got != "" {
		t.Errorf("tail 0 = %q, want empty", got)
	}
}

// tail larger than line count keeps everything.
func TestApplyLineWindowTailLargerThanContent(t *testing.T) {
	input := "a\nb\n"
	tail := 10
	if got := applyLineWindow(input, nil, &tail); got != "a\nb\n" {
		t.Errorf("tail oversized = %q, want %q", got, "a\nb\n")
	}
}

// no window passes content through unchanged.
func TestApplyLineWindowNone(t *testing.T) {
	input := "a\nb\nc\n"
	if got := applyLineWindow(input, nil, nil); got != input {
		t.Errorf("no window = %q, want %q", got, input)
	}
}

// processContent: minimal filter strips a // comment from a rust file, mirroring
// the behaviour exercised by rtk's test_read_rust_file (which just verified the
// minimal path ran without panicking).
func TestProcessContentMinimalRust(t *testing.T) {
	content := "// Comment\nfn main() {\n    println!(\"Hello\");\n}\n"
	opts := options{level: core.FilterMinimal}
	got := processContent(content, core.LangRust, opts, "x.rs", 0)
	if strings.Contains(got, "// Comment") {
		t.Errorf("minimal filter should drop the line comment, got %q", got)
	}
	if !strings.Contains(got, "fn main") {
		t.Errorf("minimal filter should keep the signature, got %q", got)
	}
}

// processContent: empty-filter-output safety fallback restores raw content.
func TestProcessContentEmptyFallback(t *testing.T) {
	// A file that is nothing but a line comment filters to empty under minimal.
	content := "// only a comment\n"
	opts := options{level: core.FilterMinimal}
	got := processContent(content, core.LangRust, opts, "x.rs", 0)
	if strings.TrimSpace(got) == "" {
		t.Errorf("empty-output safety should restore raw content, got %q", got)
	}
}

// line numbers are right-aligned and 1-based.
func TestFormatWithLineNumbers(t *testing.T) {
	got := formatWithLineNumbers("a\nb\nc")
	want := "1 │ a\n2 │ b\n3 │ c\n"
	if got != want {
		t.Errorf("line numbers = %q, want %q", got, want)
	}
}

// width grows with line count.
func TestFormatWithLineNumbersWidth(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 12; i++ {
		sb.WriteString("x\n")
	}
	got := formatWithLineNumbers(sb.String())
	// 12 lines -> width 2; line 1 is " 1 │ x".
	if !strings.HasPrefix(got, " 1 │ x\n") {
		t.Errorf("expected zero-pad-aligned first line, got prefix %q", got[:8])
	}
	if !strings.Contains(got, "12 │ x\n") {
		t.Errorf("expected line 12, got %q", got)
	}
}

// countLines mirrors Rust str::lines().count().
func TestCountLines(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"a", 1},
		{"a\n", 1},
		{"a\nb", 2},
		{"a\nb\n", 2},
		{"a\nb\nc\n", 3},
	}
	for _, c := range cases {
		if got := countLines(c.in); got != c.want {
			t.Errorf("countLines(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// parseArgs: stdin "-" parsing and flags.
func TestParseArgsFlags(t *testing.T) {
	opts, err := parseArgs([]string{"-n", "--level", "minimal", "--max-lines", "5", "file1.rs", "-"})
	if err != nil {
		t.Fatalf("parseArgs error: %v", err)
	}
	if !opts.lineNumbers {
		t.Errorf("expected lineNumbers")
	}
	if opts.level != core.FilterMinimal {
		t.Errorf("expected minimal level, got %v", opts.level)
	}
	if opts.maxLines == nil || *opts.maxLines != 5 {
		t.Errorf("expected maxLines 5, got %v", opts.maxLines)
	}
	if len(opts.files) != 2 || opts.files[0] != "file1.rs" || opts.files[1] != "-" {
		t.Errorf("unexpected files: %v", opts.files)
	}
}

// parseArgs: --level=aggressive equals form.
func TestParseArgsEqualsForm(t *testing.T) {
	opts, err := parseArgs([]string{"--level=aggressive", "--tail-lines=3", "f.go"})
	if err != nil {
		t.Fatalf("parseArgs error: %v", err)
	}
	if opts.level != core.FilterAggressive {
		t.Errorf("expected aggressive, got %v", opts.level)
	}
	if opts.tailLines == nil || *opts.tailLines != 3 {
		t.Errorf("expected tailLines 3, got %v", opts.tailLines)
	}
}

// parseArgs: unknown flag is an error.
func TestParseArgsUnknownFlag(t *testing.T) {
	if _, err := parseArgs([]string{"--bogus", "f.go"}); err == nil {
		t.Errorf("expected error for unknown flag")
	}
}
