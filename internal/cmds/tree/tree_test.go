package tree

import (
	"strings"
	"testing"

	"gortk/internal/core"
)

func TestFilterRemovesSummary(t *testing.T) {
	input := ".\n├── src\n│   └── main.rs\n└── Cargo.toml\n\n2 directories, 3 files\n"
	output := filterTreeOutput(input)
	if strings.Contains(output, "directories") {
		t.Errorf("output should not contain %q: %s", "directories", output)
	}
	if strings.Contains(output, "files") {
		t.Errorf("output should not contain %q: %s", "files", output)
	}
	if !strings.Contains(output, "main.rs") {
		t.Errorf("output missing main.rs: %s", output)
	}
	if !strings.Contains(output, "Cargo.toml") {
		t.Errorf("output missing Cargo.toml: %s", output)
	}
}

func TestFilterPreservesStructure(t *testing.T) {
	input := ".\n├── src\n│   ├── main.rs\n│   └── lib.rs\n└── tests\n    └── test.rs\n"
	output := filterTreeOutput(input)
	for _, want := range []string{"├──", "│", "└──", "main.rs", "test.rs"} {
		if !strings.Contains(output, want) {
			t.Errorf("output missing %q: %s", want, output)
		}
	}
}

func TestFilterHandlesEmpty(t *testing.T) {
	output := filterTreeOutput("")
	if output != "\n" {
		t.Errorf("want %q, got %q", "\n", output)
	}
}

func TestFilterRemovesTrailingEmptyLines(t *testing.T) {
	input := ".\n├── file.txt\n\n\n"
	output := filterTreeOutput(input)
	// Root + file.txt + final newline = exactly 2 newlines.
	if n := strings.Count(output, "\n"); n != 2 {
		t.Errorf("want 2 newlines, got %d: %q", n, output)
	}
}

func TestFilterSummaryVariations(t *testing.T) {
	cases := []struct {
		input           string
		summaryFragment string
	}{
		{".\n└── file.txt\n\n0 directories, 1 file\n", "1 file"},
		{".\n└── file.txt\n\n1 directory, 0 files\n", "1 directory"},
		{".\n└── file.txt\n\n10 directories, 25 files\n", "25 files"},
	}
	for _, c := range cases {
		output := filterTreeOutput(c.input)
		if strings.Contains(output, c.summaryFragment) {
			t.Errorf("should remove summary %q from output: %s", c.summaryFragment, output)
		}
		if !strings.Contains(output, "file.txt") {
			t.Errorf("should preserve file.txt in output: %s", output)
		}
	}
}

func TestNoiseDirsConstant(t *testing.T) {
	for _, want := range []string{"node_modules", ".git", "target", "__pycache__", ".next", "dist", "build"} {
		if !core.IsNoiseDir(want) {
			t.Errorf("core.NoiseDirs should contain %q", want)
		}
	}
}
