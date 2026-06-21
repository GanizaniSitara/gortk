package learn

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsCommandErrorRequiresErrorFlag(t *testing.T) {
	if IsCommandError(false, "error: unknown flag") {
		t.Error("should be false when is_error=false")
	}
	if !IsCommandError(true, "error: unknown flag") {
		t.Error("should be true when is_error=true with error content")
	}
}

func TestIsCommandErrorFiltersUserRejection(t *testing.T) {
	if IsCommandError(true, "The user doesn't want to proceed") {
		t.Error("user rejection should not count")
	}
	if IsCommandError(true, "Operation cancelled by user") {
		t.Error("user cancellation should not count")
	}
	if !IsCommandError(true, "error: permission denied") {
		t.Error("real error should count")
	}
}

func TestIsCommandErrorRequiresErrorContent(t *testing.T) {
	if IsCommandError(true, "All good, success!") {
		t.Error("no error marker should not count")
	}
	for _, out := range []string{"error: something failed", "unknown flag --foo", "invalid option"} {
		if !IsCommandError(true, out) {
			t.Errorf("%q should count as error", out)
		}
	}
}

func TestClassifyError(t *testing.T) {
	cases := map[string]string{
		"error: unexpected argument '--foo'":                         "Unknown Flag",
		"unknown option: --bar":                                      "Unknown Flag",
		"bash: foobar: command not found":                            "Command Not Found",
		"'xyz' is not recognized as an internal or external command": "Command Not Found",
		"No such file or directory: foo.txt":                         "Wrong Path",
		"error: --output requires a value":                           "Missing Argument",
		"permission denied: /etc/shadow":                             "Permission Denied",
		"something went wrong":                                       "General Error",
	}
	for in, want := range cases {
		if got := ClassifyError(in); got != want {
			t.Errorf("ClassifyError(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommandSimilaritySameBase(t *testing.T) {
	if got := commandSimilarity("git commit", "git commit"); got != 1.0 {
		t.Errorf("identical = %v, want 1.0", got)
	}
	if got := commandSimilarity("git status", "npm install"); got != 0.0 {
		t.Errorf("different base = %v, want 0.0", got)
	}
	// Same base + 1 arg each, no overlap → 0.5.
	if got := commandSimilarity("git commit --amend", "git commit --ammend"); got != 0.5 {
		t.Errorf("typo args = %v, want 0.5", got)
	}
}

func TestFindCorrectionsBasic(t *testing.T) {
	cmds := []CommandExecution{
		{Command: "git commit --ammend", IsError: true, Output: "error: unexpected argument '--ammend'"},
		{Command: "git commit --amend", IsError: false, Output: "[main abc123] Fix bug"},
	}
	corr := FindCorrections(cmds)
	if len(corr) != 1 {
		t.Fatalf("want 1 correction, got %d", len(corr))
	}
	if corr[0].Wrong != "git commit --ammend" || corr[0].Right != "git commit --amend" {
		t.Errorf("got %+v", corr[0])
	}
	if corr[0].Confidence < 0.6 {
		t.Errorf("confidence = %v", corr[0].Confidence)
	}
}

func TestFindCorrectionsWindowLimit(t *testing.T) {
	cmds := []CommandExecution{
		{Command: "git commit --ammend", IsError: true, Output: "error: unexpected argument '--ammend'"},
		{Command: "ls", IsError: false, Output: "file1.txt\nfile2.txt"},
		{Command: "pwd", IsError: false, Output: "/home/user"},
		{Command: "echo test", IsError: false, Output: "test"},
		{Command: "git commit --amend", IsError: false, Output: "[main abc123] Fix"},
	}
	if corr := FindCorrections(cmds); len(corr) != 0 {
		t.Errorf("correction outside window should not be found, got %d", len(corr))
	}
}

func TestFindCorrectionsExcludesTDD(t *testing.T) {
	cmds := []CommandExecution{
		{Command: "cargo test", IsError: true, Output: "error[E0425]: cannot find value `x`\ntest result: FAILED"},
		{Command: "cargo test", IsError: false, Output: "test result: ok. 5 passed"},
	}
	if corr := FindCorrections(cmds); len(corr) != 0 {
		t.Errorf("TDD cycle should not be a correction, got %d", len(corr))
	}
}

func TestFindCorrectionsPathExploration(t *testing.T) {
	cmds := []CommandExecution{
		{Command: "cat file1.txt", IsError: true, Output: "cat: file1.txt: No such file or directory"},
		{Command: "cat file2.txt", IsError: false, Output: "content here"},
	}
	// Matches rtk's reference fixture (0 corrections): the first command's output
	// "No such file or directory" contains none of the error markers (error,
	// failed, unknown, invalid, "not found", "permission denied", cannot), so
	// IsCommandError is false and it is never treated as a correctable error.
	if corr := FindCorrections(cmds); len(corr) != 0 {
		t.Errorf("path exploration should yield 0 corrections, got %d", len(corr))
	}
}

func TestFindCorrectionsMinConfidence(t *testing.T) {
	cmds := []CommandExecution{
		{Command: "git commit --foo --bar --baz", IsError: true, Output: "error: unexpected argument '--foo'"},
		{Command: "git commit --qux", IsError: false, Output: "[main abc123] Fix"},
	}
	// sim 0.5 + success boost 0.2 = 0.7 ≥ 0.6 → 1 correction.
	if corr := FindCorrections(cmds); len(corr) != 1 {
		t.Errorf("want 1, got %d", len(corr))
	}
}

func TestDeduplicateMergesSame(t *testing.T) {
	pairs := []CorrectionPair{
		{Wrong: "git commit --ammend", Right: "git commit --amend", ErrorType: "Unknown Flag", Confidence: 0.8},
		{Wrong: "git commit --ammend -m 'fix'", Right: "git commit --amend -m 'fix'", ErrorType: "Unknown Flag", Confidence: 0.9},
		{Wrong: "git commit --ammend", Right: "git commit --amend", ErrorType: "Unknown Flag", Confidence: 0.7},
	}
	rules := DeduplicateCorrections(pairs)
	if len(rules) != 1 {
		t.Fatalf("want 1 merged rule, got %d", len(rules))
	}
	if rules[0].Occurrences != 3 {
		t.Errorf("occurrences = %d, want 3", rules[0].Occurrences)
	}
	if rules[0].BaseCommand != "git commit" {
		t.Errorf("base = %q", rules[0].BaseCommand)
	}
	if !strings.Contains(rules[0].Wrong, "'fix'") {
		t.Errorf("should keep highest-confidence example, got %q", rules[0].Wrong)
	}
}

func TestDeduplicateKeepsDistinct(t *testing.T) {
	pairs := []CorrectionPair{
		{Wrong: "git commit --ammend", Right: "git commit --amend", ErrorType: "Unknown Flag", Confidence: 0.8},
		{Wrong: "git push --force", Right: "git push --force-with-lease", ErrorType: "Wrong Syntax", Confidence: 0.7},
	}
	rules := DeduplicateCorrections(pairs)
	if len(rules) != 2 {
		t.Fatalf("want 2 distinct rules, got %d", len(rules))
	}
}

func TestFormatConsoleEmpty(t *testing.T) {
	out := FormatConsole(Report{SinceDays: 30})
	if !strings.Contains(out, "0 rules") || !strings.Contains(out, "0 corrections") {
		t.Errorf("missing zero counts: %s", out)
	}
	if !strings.Contains(out, "No CLI corrections detected") {
		t.Errorf("missing empty message: %s", out)
	}
}

func TestFormatConsoleWithRules(t *testing.T) {
	rep := Report{
		SessionsScanned:  10,
		TotalCorrections: 4,
		SinceDays:        30,
		Rules: []CorrectionRule{
			{Wrong: "git commit --ammend", Right: "git commit --amend", Occurrences: 3,
				BaseCommand: "git commit", ExampleError: "error: unexpected argument '--ammend'"},
			{Wrong: "gh pr edit -t", Right: "gh pr edit --title", Occurrences: 1, BaseCommand: "gh pr"},
		},
	}
	out := FormatConsole(rep)
	for _, want := range []string{"2 rules", "4 corrections", "[3x]", "--ammend", "--amend",
		"Error: error: unexpected argument"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestWriteRulesFileMarkdown(t *testing.T) {
	rules := []CorrectionRule{
		{Wrong: "git commit --ammend", Right: "git commit --amend", Occurrences: 3, BaseCommand: "git commit"},
	}
	path := filepath.Join(t.TempDir(), "cli-corrections.md")
	if err := WriteRulesFile(rules, path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	for _, want := range []string{
		"# CLI Corrections",
		"## Git commit",
		"Use `git commit --amend` not `git commit --ammend`",
		"(seen 3x)",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("missing %q in:\n%s", want, content)
		}
	}
}

func TestGenerateEndToEnd(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	_ = os.MkdirAll(proj, 0o755)
	lines := []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"git commit --ammend"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"error: unexpected argument '--ammend'","is_error":true}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"git commit --amend"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"[main abc] done","is_error":false}]}}`,
	}
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := Generate(root, Options{All: true, SinceDays: 0, MinConfidence: 0.6, MinOccurrences: 1})
	if rep.SessionsScanned != 1 {
		t.Errorf("sessions = %d", rep.SessionsScanned)
	}
	if len(rep.Rules) != 1 {
		t.Fatalf("want 1 rule, got %d", len(rep.Rules))
	}
	if rep.Rules[0].Wrong != "git commit --ammend" || rep.Rules[0].Right != "git commit --amend" {
		t.Errorf("rule = %+v", rep.Rules[0])
	}
}

func TestAtofDefault(t *testing.T) {
	cases := []struct {
		in  string
		def float64
		out float64
	}{
		{"0.6", 0.1, 0.6},
		{"1", 0.1, 1.0},
		{"0.75", 0.1, 0.75},
		{"abc", 0.5, 0.5},
		{"", 0.5, 0.5},
	}
	for _, c := range cases {
		got := atofDefault(c.in, c.def)
		if diff := got - c.out; diff > 1e-9 || diff < -1e-9 {
			t.Errorf("atofDefault(%q) = %v, want %v", c.in, got, c.out)
		}
	}
}
