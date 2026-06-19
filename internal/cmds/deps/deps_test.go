package deps

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// NOTE: rtk's src/cmds/system/deps.rs has NO #[cfg(test)] block, so there are
// no upstream test cases to port. These are characterization tests written
// directly against the existing gortk pure summarizer functions, asserting the
// behaviour documented in the production code (per-ecosystem parsing, dep/dev
// split, count headers, and the maxDeps/maxDevDeps caps). They do not invent
// rtk semantics — they pin the Go port's actual behaviour.

func TestSummarizeCargo(t *testing.T) {
	content := `[package]
name = "demo"
version = "0.1.0"

[dependencies]
serde = "1.0"
regex = { version = "1.10", features = ["std"] }
anyhow = "1"

[dev-dependencies]
criterion = "0.5"
`
	out := summarizeCargo(content)
	for _, want := range []string{
		"Dependencies (3):",
		"serde (1.0)",
		"regex (1.10)", // version pulled from the inline table
		"anyhow (1)",
		"Dev (1):",
		"criterion (0.5)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// The [package] section's name/version must not be treated as deps.
	if strings.Contains(out, "name (demo)") || strings.Contains(out, "version (0.1.0)") {
		t.Errorf("package metadata leaked as dependency:\n%s", out)
	}
}

func TestSummarizeCargoDepsCap(t *testing.T) {
	var b strings.Builder
	b.WriteString("[dependencies]\n")
	for i := 0; i < maxDeps+3; i++ {
		fmt.Fprintf(&b, "dep%d = \"1.0\"\n", i)
	}
	out := summarizeCargo(b.String())
	if !strings.Contains(out, fmt.Sprintf("Dependencies (%d):", maxDeps+3)) {
		t.Errorf("count header wrong:\n%s", out)
	}
	if !strings.Contains(out, "... +3 more") {
		t.Errorf("missing overflow marker:\n%s", out)
	}
}

func TestSummarizePackageJSON(t *testing.T) {
	content := `{
  "name": "widget",
  "version": "2.3.4",
  "dependencies": {
    "react": "^18.0.0",
    "lodash": "4.17.21"
  },
  "devDependencies": {
    "jest": "^29.0.0"
  }
}`
	out := summarizePackageJSON(content)
	for _, want := range []string{
		"widget @ 2.3.4",
		"Dependencies (2):",
		"react (^18.0.0)",
		"lodash (4.17.21)",
		"Dev Dependencies (1):",
		"jest",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestSummarizePackageJSONOrderPreserved(t *testing.T) {
	// dependencies must be listed in document order, not alphabetical.
	content := `{"name":"x","version":"1.0.0","dependencies":{"zebra":"1","apple":"2"}}`
	out := summarizePackageJSON(content)
	zPos := strings.Index(out, "zebra")
	aPos := strings.Index(out, "apple")
	if zPos < 0 || aPos < 0 || zPos >= aPos {
		t.Errorf("dependencies should preserve document order (zebra before apple):\n%s", out)
	}
}

func TestSummarizeRequirements(t *testing.T) {
	content := `# comment
flask==2.0.1
requests>=2.28

numpy
`
	out := summarizeRequirements(content)
	for _, want := range []string{
		"Packages (3):",
		"flask==2.0.1",
		"requests>=2.28",
		"numpy",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "# comment") {
		t.Errorf("comment should be skipped:\n%s", out)
	}
}

func TestSummarizePyproject(t *testing.T) {
	content := `[project]
name = "tool"
dependencies = [
    "click>=8.0",
    "rich",
]
`
	out := summarizePyproject(content)
	for _, want := range []string{"Dependencies (2):", "click>=8.0", "rich"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestSummarizeGoMod(t *testing.T) {
	content := `module github.com/acme/widget

go 1.22

require (
	github.com/foo/bar v1.2.3
	github.com/baz/qux v0.4.0 // indirect
)

require github.com/single/dep v2.0.0
`
	out := summarizeGoMod(content)
	for _, want := range []string{
		"github.com/acme/widget (go 1.22)",
		"github.com/foo/bar v1.2.3",
		"github.com/baz/qux v0.4.0",
		"github.com/single/dep v2.0.0",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestSummarizeNoFiles(t *testing.T) {
	dir := t.TempDir()
	report, raw := Summarize(dir)
	if !strings.Contains(report, "No dependency files found") {
		t.Errorf("expected 'No dependency files found', got: %s", report)
	}
	if raw != "" {
		t.Errorf("raw should be empty when nothing found, got: %q", raw)
	}
}

func TestSummarizeReadsManifests(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, raw := Summarize(dir)
	if !strings.Contains(report, "Go (go.mod):") {
		t.Errorf("missing Go header: %s", report)
	}
	if !strings.Contains(report, "demo (go 1.22)") {
		t.Errorf("missing module line: %s", report)
	}
	if !strings.Contains(raw, "module demo") {
		t.Errorf("raw should contain the manifest content: %q", raw)
	}
}
