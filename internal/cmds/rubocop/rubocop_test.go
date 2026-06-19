package rubocop

import (
	"fmt"
	"strings"
	"testing"

	"gortk/internal/core"
)

// Faithful port of the #[cfg(test)] mod tests block in rtk's
// src/cmds/ruby/rubocop_cmd.rs. The pure filter/path/rank functions are
// exercised directly. The token-savings assertion uses gortk's character-based
// core.EstimateTokens in place of rtk's word-count count_tokens; both agree the
// filter is a large net reduction, so the threshold holds.

func savingsPct(input, output string) float64 {
	in := core.EstimateTokens(input)
	out := core.EstimateTokens(output)
	if in == 0 {
		return 0
	}
	return 100.0 - (float64(out) / float64(in) * 100.0)
}

const noOffensesJSON = `{
          "metadata": {"rubocop_version": "1.60.0"},
          "files": [],
          "summary": {
            "offense_count": 0,
            "target_file_count": 0,
            "inspected_file_count": 15
          }
        }`

const withOffensesJSON = `{
          "metadata": {"rubocop_version": "1.60.0"},
          "files": [
            {
              "path": "app/models/user.rb",
              "offenses": [
                {
                  "severity": "convention",
                  "message": "Trailing whitespace detected.",
                  "cop_name": "Layout/TrailingWhitespace",
                  "correctable": true,
                  "location": {"start_line": 10, "start_column": 5, "last_line": 10, "last_column": 8, "length": 3, "line": 10, "column": 5}
                },
                {
                  "severity": "convention",
                  "message": "Missing frozen string literal comment.",
                  "cop_name": "Style/FrozenStringLiteralComment",
                  "correctable": true,
                  "location": {"start_line": 1, "start_column": 1, "last_line": 1, "last_column": 1, "length": 1, "line": 1, "column": 1}
                },
                {
                  "severity": "warning",
                  "message": "Useless assignment to variable - ` + "`x`" + `.",
                  "cop_name": "Lint/UselessAssignment",
                  "correctable": false,
                  "location": {"start_line": 25, "start_column": 5, "last_line": 25, "last_column": 6, "length": 1, "line": 25, "column": 5}
                }
              ]
            },
            {
              "path": "app/controllers/users_controller.rb",
              "offenses": [
                {
                  "severity": "convention",
                  "message": "Trailing whitespace detected.",
                  "cop_name": "Layout/TrailingWhitespace",
                  "correctable": true,
                  "location": {"start_line": 5, "start_column": 20, "last_line": 5, "last_column": 22, "length": 2, "line": 5, "column": 20}
                },
                {
                  "severity": "error",
                  "message": "Syntax error, unexpected end-of-input.",
                  "cop_name": "Lint/Syntax",
                  "correctable": false,
                  "location": {"start_line": 30, "start_column": 1, "last_line": 30, "last_column": 1, "length": 1, "line": 30, "column": 1}
                }
              ]
            }
          ],
          "summary": {
            "offense_count": 5,
            "target_file_count": 2,
            "inspected_file_count": 20
          }
        }`

func TestFilterRubocopNoOffenses(t *testing.T) {
	if got := filterRubocopJSON(noOffensesJSON); got != "ok ✓ rubocop (15 files)" {
		t.Errorf("got %q", got)
	}
}

func TestFilterRubocopWithOffensesPerFile(t *testing.T) {
	result := filterRubocopJSON(withOffensesJSON)
	for _, want := range []string{
		"5 offenses (20 files)",
		"app/controllers/users_controller.rb",
		"app/models/user.rb",
		":30 Lint/Syntax — Syntax error",
		":10 Layout/TrailingWhitespace — Trailing whitespace",
		":25 Lint/UselessAssignment — Useless assignment",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q in: %s", want, result)
		}
	}
}

func TestFilterRubocopSeverityOrdering(t *testing.T) {
	result := filterRubocopJSON(withOffensesJSON)
	ctrlPos := strings.Index(result, "users_controller.rb")
	modelPos := strings.Index(result, "app/models/user.rb")
	if ctrlPos < 0 || modelPos < 0 || ctrlPos >= modelPos {
		t.Errorf("error-file should appear before convention-file: %s", result)
	}
	errorPos := strings.Index(result, ":30 Lint/Syntax")
	convPos := strings.Index(result, ":5 Layout/TrailingWhitespace")
	if errorPos < 0 || convPos < 0 || errorPos >= convPos {
		t.Errorf("error offense should appear before convention: %s", result)
	}
}

func TestFilterRubocopWithinFileLineOrdering(t *testing.T) {
	result := filterRubocopJSON(withOffensesJSON)
	warningPos := strings.Index(result, ":25 Lint/UselessAssignment")
	conv1Pos := strings.Index(result, ":1 Style/FrozenStringLiteralComment")
	if warningPos < 0 || conv1Pos < 0 || warningPos >= conv1Pos {
		t.Errorf("warning should come before convention within same file: %s", result)
	}
}

func TestFilterRubocopCorrectableHint(t *testing.T) {
	result := filterRubocopJSON(withOffensesJSON)
	if !strings.Contains(result, "3 correctable") || !strings.Contains(result, "rubocop -A") {
		t.Errorf("missing correctable hint: %s", result)
	}
}

func TestFilterRubocopTextFallback(t *testing.T) {
	text := `Inspecting 10 files
..........

10 files inspected, no offenses detected`
	if got := filterRubocopText(text); got != "ok ✓ rubocop (10 files)" {
		t.Errorf("got %q", got)
	}
}

func TestFilterRubocopTextAutocorrect(t *testing.T) {
	text := `Inspecting 15 files
...C..CC.......

15 files inspected, 3 offenses detected, 3 offenses autocorrected`
	if got := filterRubocopText(text); got != "ok ✓ rubocop -A (15 files, 3 autocorrected)" {
		t.Errorf("got %q", got)
	}
}

func TestFilterRubocopEmptyOutput(t *testing.T) {
	if got := filterRubocopJSON(""); got != "RuboCop: No output" {
		t.Errorf("got %q", got)
	}
}

func TestFilterRubocopInvalidJSONFallsBack(t *testing.T) {
	result := filterRubocopJSON("some ruby warning\n{broken json")
	if result == "" {
		t.Error("should not panic/empty on invalid JSON")
	}
}

func TestCompactRubyPath(t *testing.T) {
	cases := map[string]string{
		"/home/user/project/app/models/user.rb": "app/models/user.rb",
		"app/controllers/users_controller.rb":   "app/controllers/users_controller.rb",
		"/project/spec/models/user_spec.rb":     "spec/models/user_spec.rb",
		"lib/tasks/deploy.rake":                 "lib/tasks/deploy.rake",
	}
	for in, want := range cases {
		if got := compactRubyPath(in); got != want {
			t.Errorf("compactRubyPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFilterRubocopCapsOffensesPerFile(t *testing.T) {
	json := `{
          "metadata": {"rubocop_version": "1.60.0"},
          "files": [
            {
              "path": "app/models/big.rb",
              "offenses": [
                {"severity": "convention", "message": "msg1", "cop_name": "Cop/A", "correctable": false, "location": {"start_line": 1, "start_column": 1}},
                {"severity": "convention", "message": "msg2", "cop_name": "Cop/B", "correctable": false, "location": {"start_line": 2, "start_column": 1}},
                {"severity": "convention", "message": "msg3", "cop_name": "Cop/C", "correctable": false, "location": {"start_line": 3, "start_column": 1}},
                {"severity": "convention", "message": "msg4", "cop_name": "Cop/D", "correctable": false, "location": {"start_line": 4, "start_column": 1}},
                {"severity": "convention", "message": "msg5", "cop_name": "Cop/E", "correctable": false, "location": {"start_line": 5, "start_column": 1}},
                {"severity": "convention", "message": "msg6", "cop_name": "Cop/F", "correctable": false, "location": {"start_line": 6, "start_column": 1}},
                {"severity": "convention", "message": "msg7", "cop_name": "Cop/G", "correctable": false, "location": {"start_line": 7, "start_column": 1}}
              ]
            }
          ],
          "summary": {"offense_count": 7, "target_file_count": 1, "inspected_file_count": 5}
        }`
	result := filterRubocopJSON(json)
	if !strings.Contains(result, ":5 Cop/E") {
		t.Errorf("should show 5th offense: %s", result)
	}
	if strings.Contains(result, ":6 Cop/F") {
		t.Errorf("should not show 6th inline: %s", result)
	}
	if !strings.Contains(result, "… +2 more") {
		t.Errorf("should show overflow: %s", result)
	}
}

func TestFilterRubocopTextBundlerError(t *testing.T) {
	text := "Bundler::GemNotFound: Could not find gem 'rubocop' in any sources."
	result := filterRubocopText(text)
	if !strings.HasPrefix(result, "RuboCop error:") {
		t.Errorf("should detect Bundler error: %s", result)
	}
	if !strings.Contains(result, "GemNotFound") {
		t.Errorf("missing GemNotFound: %s", result)
	}
}

func TestFilterRubocopTextLoadError(t *testing.T) {
	text := "/usr/lib/ruby/3.2.0/rubygems.rb:250: cannot load such file -- rubocop (LoadError)"
	result := filterRubocopText(text)
	if !strings.HasPrefix(result, "RuboCop error:") {
		t.Errorf("should detect load error: %s", result)
	}
}

func TestFilterRubocopTextWithOffenses(t *testing.T) {
	text := `Inspecting 5 files
..C..

5 files inspected, 1 offense detected`
	if got := filterRubocopText(text); got != "RuboCop: 5 files inspected, 1 offense detected" {
		t.Errorf("got %q", got)
	}
}

func TestSeverityRank(t *testing.T) {
	if !(severityRank("error") < severityRank("warning")) {
		t.Error("error should rank before warning")
	}
	if !(severityRank("warning") < severityRank("convention")) {
		t.Error("warning should rank before convention")
	}
	if !(severityRank("fatal") < severityRank("warning")) {
		t.Error("fatal should rank before warning")
	}
}

func TestTokenSavings(t *testing.T) {
	output := filterRubocopJSON(withOffensesJSON)
	if s := savingsPct(withOffensesJSON, output); s < 60.0 {
		t.Errorf("RuboCop: expected >=60%% savings, got %.1f%%", s)
	}
}

// ── ANSI handling test ──────────────────────────────────────────────────

func TestFilterRubocopJSONWithANSIPrefix(t *testing.T) {
	input := "\x1b[33mWarning: something\x1b[0m\n{\"broken\": true}"
	result := filterRubocopJSON(input)
	if result == "" {
		t.Error("should not panic/empty on ANSI-prefixed JSON")
	}
}

// ── 10-file cap test ────────────────────────────────────────────────────

func TestFilterRubocopCapsAtTenFiles(t *testing.T) {
	var filesJSON []string
	for i := 1; i <= 12; i++ {
		filesJSON = append(filesJSON, fmt.Sprintf(
			`{"path": "app/models/model_%d.rb", "offenses": [{"severity": "convention", "message": "msg%d", "cop_name": "Cop/X%d", "correctable": false, "location": {"start_line": 1, "start_column": 1}}]}`,
			i, i, i))
	}
	json := fmt.Sprintf(
		`{"metadata": {"rubocop_version": "1.60.0"}, "files": [%s], "summary": {"offense_count": 12, "target_file_count": 12, "inspected_file_count": 12}}`,
		strings.Join(filesJSON, ","))
	result := filterRubocopJSON(json)
	if !strings.Contains(result, "… +2 more files") {
		t.Errorf("should show +2 more files overflow: %s", result)
	}
}
