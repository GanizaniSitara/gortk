package lint

import (
	"strings"
	"testing"
)

func TestFilterESLintJSON(t *testing.T) {
	json := `[
        {
            "filePath": "/Users/test/project/src/utils.ts",
            "messages": [
                {
                    "ruleId": "prefer-const",
                    "severity": 1,
                    "message": "Use const instead of let",
                    "line": 10,
                    "column": 5
                },
                {
                    "ruleId": "prefer-const",
                    "severity": 1,
                    "message": "Use const instead of let",
                    "line": 15,
                    "column": 5
                }
            ],
            "errorCount": 0,
            "warningCount": 2
        },
        {
            "filePath": "/Users/test/project/src/api.ts",
            "messages": [
                {
                    "ruleId": "@typescript-eslint/no-unused-vars",
                    "severity": 2,
                    "message": "Variable x is unused",
                    "line": 20,
                    "column": 10
                }
            ],
            "errorCount": 1,
            "warningCount": 0
        }
    ]`

	result := filterESLintJSON(json)
	for _, want := range []string{"ESLint:", "prefer-const", "no-unused-vars", "src/utils.ts"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q:\n%s", want, result)
		}
	}
}

func TestCompactPath(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"/Users/foo/project/src/utils.ts", "src/utils.ts"},
		{`C:\Users\project\src\api.ts`, "src/api.ts"},
		{"simple.ts", "simple.ts"},
	}
	for _, c := range cases {
		if got := compactPath(c.in); got != c.want {
			t.Errorf("compactPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFilterPylintJSONNoIssues(t *testing.T) {
	result := filterPylintJSON("[]")
	if !strings.Contains(result, "Pylint") {
		t.Errorf("result missing %q: %s", "Pylint", result)
	}
	if !strings.Contains(result, "No issues found") {
		t.Errorf("result missing %q: %s", "No issues found", result)
	}
}

func TestFilterPylintJSONWithIssues(t *testing.T) {
	json := `[
        {
            "type": "warning",
            "module": "main",
            "obj": "",
            "line": 10,
            "column": 0,
            "path": "src/main.py",
            "symbol": "unused-variable",
            "message": "Unused variable 'x'",
            "message-id": "W0612"
        },
        {
            "type": "warning",
            "module": "main",
            "obj": "foo",
            "line": 15,
            "column": 4,
            "path": "src/main.py",
            "symbol": "unused-variable",
            "message": "Unused variable 'y'",
            "message-id": "W0612"
        },
        {
            "type": "error",
            "module": "utils",
            "obj": "bar",
            "line": 20,
            "column": 0,
            "path": "src/utils.py",
            "symbol": "undefined-variable",
            "message": "Undefined variable 'z'",
            "message-id": "E0602"
        }
    ]`

	result := filterPylintJSON(json)
	for _, want := range []string{
		"3 issues",
		"2 files",
		"1 errors, 2 warnings",
		"unused-variable (W0612)",
		"undefined-variable (E0602)",
		"main.py",
		"utils.py",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q:\n%s", want, result)
		}
	}
}

func TestStripPMPrefixNpx(t *testing.T) {
	args := []string{"npx", "eslint", "src/"}
	if got := stripPMPrefix(args); got != 1 {
		t.Errorf("stripPMPrefix(%v) = %d, want 1", args, got)
	}
}

func TestStripPMPrefixBunx(t *testing.T) {
	args := []string{"bunx", "eslint", "."}
	if got := stripPMPrefix(args); got != 1 {
		t.Errorf("stripPMPrefix(%v) = %d, want 1", args, got)
	}
}

func TestStripPMPrefixPnpmExec(t *testing.T) {
	args := []string{"pnpm", "exec", "eslint"}
	if got := stripPMPrefix(args); got != 2 {
		t.Errorf("stripPMPrefix(%v) = %d, want 2", args, got)
	}
}

func TestStripPMPrefixNone(t *testing.T) {
	args := []string{"eslint", "src/"}
	if got := stripPMPrefix(args); got != 0 {
		t.Errorf("stripPMPrefix(%v) = %d, want 0", args, got)
	}
}

func TestStripPMPrefixEmpty(t *testing.T) {
	var args []string
	if got := stripPMPrefix(args); got != 0 {
		t.Errorf("stripPMPrefix(%v) = %d, want 0", args, got)
	}
}

func TestDetectLinterESLint(t *testing.T) {
	linter, explicit := detectLinter([]string{"eslint", "src/"})
	if linter != "eslint" || !explicit {
		t.Errorf("detectLinter = %q,%v want eslint,true", linter, explicit)
	}
}

func TestDetectLinterDefaultOnPath(t *testing.T) {
	linter, explicit := detectLinter([]string{"src/"})
	if linter != "eslint" || explicit {
		t.Errorf("detectLinter = %q,%v want eslint,false", linter, explicit)
	}
}

func TestDetectLinterDefaultOnFlag(t *testing.T) {
	linter, explicit := detectLinter([]string{"--max-warnings=0"})
	if linter != "eslint" || explicit {
		t.Errorf("detectLinter = %q,%v want eslint,false", linter, explicit)
	}
}

func TestDetectLinterAfterNpxStrip(t *testing.T) {
	// rtk lint npx eslint src/ → after strip, args = ["eslint", "src/"]
	fullArgs := []string{"npx", "eslint", "src/"}
	skip := stripPMPrefix(fullArgs)
	effective := fullArgs[skip:]
	linter, _ := detectLinter(effective)
	if linter != "eslint" {
		t.Errorf("detectLinter = %q, want eslint", linter)
	}
}

func TestDetectLinterAfterPnpmExecStrip(t *testing.T) {
	fullArgs := []string{"pnpm", "exec", "biome", "check"}
	skip := stripPMPrefix(fullArgs)
	effective := fullArgs[skip:]
	linter, _ := detectLinter(effective)
	if linter != "biome" {
		t.Errorf("detectLinter = %q, want biome", linter)
	}
}

func TestIsPythonLinter(t *testing.T) {
	for _, l := range []string{"ruff", "pylint", "mypy", "flake8"} {
		if !isPythonLinter(l) {
			t.Errorf("isPythonLinter(%q) = false, want true", l)
		}
	}
	for _, l := range []string{"eslint", "biome", "unknown"} {
		if isPythonLinter(l) {
			t.Errorf("isPythonLinter(%q) = true, want false", l)
		}
	}
}
