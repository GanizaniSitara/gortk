package ruff

import (
	"fmt"
	"strings"
	"testing"
)

func TestFilterRuffCheckNoIssues(t *testing.T) {
	output := "[]"
	result := filterRuffCheckJSON(output)
	if !strings.Contains(result, "Ruff") {
		t.Errorf("missing %q: %s", "Ruff", result)
	}
	if !strings.Contains(result, "No issues found") {
		t.Errorf("missing %q: %s", "No issues found", result)
	}
}

func TestFilterRuffCheckWithIssues(t *testing.T) {
	output := `[
  {
    "code": "F401",
    "message": "` + "`os`" + ` imported but unused",
    "location": {"row": 1, "column": 8},
    "end_location": {"row": 1, "column": 10},
    "filename": "src/main.py",
    "fix": {"applicability": "safe"}
  },
  {
    "code": "F401",
    "message": "` + "`sys`" + ` imported but unused",
    "location": {"row": 2, "column": 8},
    "end_location": {"row": 2, "column": 11},
    "filename": "src/main.py",
    "fix": null
  },
  {
    "code": "E501",
    "message": "Line too long (100 > 88 characters)",
    "location": {"row": 10, "column": 89},
    "end_location": {"row": 10, "column": 100},
    "filename": "src/utils.py",
    "fix": null
  }
]`
	result := filterRuffCheckJSON(output)
	for _, want := range []string{
		"3 issues", "2 files", "1 fixable", "F401", "E501",
		"main.py", "utils.py",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
	if !strings.Contains(result, "Violations:") {
		t.Errorf("Violations section missing: %s", result)
	}
	if !strings.Contains(result, "1:8") {
		t.Errorf("line:col location missing: %s", result)
	}
}

func TestFilterRuffFormatAllFormatted(t *testing.T) {
	output := "5 files left unchanged"
	result := filterRuffFormat(output)
	if !strings.Contains(result, "Ruff format") {
		t.Errorf("missing %q: %s", "Ruff format", result)
	}
	if !strings.Contains(result, "All files formatted correctly") {
		t.Errorf("missing %q: %s", "All files formatted correctly", result)
	}
}

func TestFilterRuffFormatNeedsFormatting(t *testing.T) {
	output := `Would reformat: src/main.py
Would reformat: tests/test_utils.py
2 files would be reformatted, 3 files left unchanged`
	result := filterRuffFormat(output)
	for _, want := range []string{
		"2 files need formatting", "main.py", "test_utils.py",
		"3 files already formatted",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
}

func TestFilterRuffCheckCapsViolationsAndEmitsHint(t *testing.T) {
	// Mirror ruff's pretty-printed JSON shape so the input-vs-output comparison
	// reflects what a real `ruff check --output-format=json` emits.
	var diags []string
	for i := 0; i < 200; i++ {
		diags = append(diags, fmt.Sprintf(
			"  {\n    \"code\": \"F401\",\n    \"message\": \"`module_%d` imported but unused\",\n    \"location\": {\"row\": %d, \"column\": 4},\n    \"end_location\": {\"row\": %d, \"column\": 20},\n    \"filename\": \"/Users/dev/project/src/feature_%d.py\",\n    \"fix\": null\n  }",
			i, i, i, i,
		))
	}
	jsonStr := fmt.Sprintf("[\n%s\n]", strings.Join(diags, ",\n"))
	result := filterRuffCheckJSON(jsonStr)

	inSection := ""
	if parts := strings.SplitN(result, "Violations:", 2); len(parts) > 1 {
		inSection = parts[1]
	}
	listed := 0
	for _, l := range strings.Split(inSection, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "src/") {
			listed++
		}
	}
	if listed > 50 {
		t.Errorf("violations cap not enforced: got %d", listed)
	}
	if !strings.Contains(result, "… +150 more") {
		t.Errorf("missing '+N more' indicator: %s", result)
	}

	rawTokens := len(strings.Fields(jsonStr))
	outTokens := len(strings.Fields(result))
	savings := 100.0 - (float64(outTokens)/float64(rawTokens))*100.0
	if savings < 60.0 {
		t.Errorf("token savings dropped below 60%%: %.1f%%", savings)
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
