package format

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectFormatterFromExplicitArg(t *testing.T) {
	if got := detectFormatter([]string{"black", "--check"}); got != "black" {
		t.Errorf("got %q, want %q", got, "black")
	}
	if got := detectFormatter([]string{"prettier", "."}); got != "prettier" {
		t.Errorf("got %q, want %q", got, "prettier")
	}
	if got := detectFormatter([]string{"ruff", "format"}); got != "ruff" {
		t.Errorf("got %q, want %q", got, "ruff")
	}
}

func TestDetectFormatterFromPyprojectBlack(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pyproject.toml"), "[tool.black]\nline-length = 88\n")
	if got := detectFormatterInDir(nil, dir); got != "black" {
		t.Errorf("got %q, want %q", got, "black")
	}
}

func TestDetectFormatterFromPyprojectRuff(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pyproject.toml"), "[tool.ruff.format]\nindent-width = 4\n")
	if got := detectFormatterInDir(nil, dir); got != "ruff" {
		t.Errorf("got %q, want %q", got, "ruff")
	}
}

func TestDetectFormatterFromPackageJSON(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "package.json"), `{"name": "test"}`+"\n")
	if got := detectFormatterInDir(nil, dir); got != "prettier" {
		t.Errorf("got %q, want %q", got, "prettier")
	}
}

func TestFilterBlackAllFormatted(t *testing.T) {
	output := "All done! ✨ 🍰 ✨\n5 files left unchanged."
	result := filterBlackOutput(output)
	for _, want := range []string{"Format (black)", "All files formatted", "5 files checked"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q: %s", want, result)
		}
	}
}

func TestFilterBlackNeedsFormatting(t *testing.T) {
	output := "would reformat: src/main.py\n" +
		"would reformat: tests/test_utils.py\n" +
		"Oh no! 💥 💔 💥\n" +
		"2 files would be reformatted, 3 files would be left unchanged."

	result := filterBlackOutput(output)
	for _, want := range []string{
		"2 files need formatting",
		"main.py",
		"test_utils.py",
		"3 files already formatted",
		"Run `black .`",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q: %s", want, result)
		}
	}
}

func TestCompactPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/Users/foo/project/src/main.py", "src/main.py"},
		{"/home/user/app/lib/utils.py", "lib/utils.py"},
		{"C:\\Users\\foo\\project\\tests\\test.py", "tests/test.py"},
		{"relative/file.py", "file.py"},
	}
	for _, c := range cases {
		if got := compactPath(c.in); got != c.want {
			t.Errorf("compactPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile %s: %v", path, err)
	}
}
