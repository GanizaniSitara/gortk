package discover

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	// Populate the registry so Classify can find real gortk commands.
	_ "gortk/internal/cmds/git"
	_ "gortk/internal/cmds/ls"
)

// writeSession writes a project dir + one transcript with the given JSONL lines,
// returning the projects-root directory to pass to Generate.
func writeSession(t *testing.T, projectName string, lines []string) string {
	t.Helper()
	root := t.TempDir()
	proj := filepath.Join(root, projectName)
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func bash(id, cmd string) string {
	return `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"` + id +
		`","name":"Bash","input":{"command":"` + cmd + `"}}]}}`
}

func result(id, content string) string {
	return `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"` + id +
		`","content":"` + content + `"}]}}`
}

func TestGenerateBasicSupported(t *testing.T) {
	// git status is a registered gortk command run raw; echo is ignored.
	root := writeSession(t, "proj", []string{
		bash("t1", "git status"),
		result("t1", strings.Repeat("x", 400)),
		bash("t2", "echo hello"),
		result("t2", "hello"),
	})
	rep := Generate(root, Options{SinceDays: 0, All: true})
	if rep.SessionsScanned != 1 {
		t.Errorf("sessions = %d", rep.SessionsScanned)
	}
	// git status counted (supported), echo ignored (not counted at all).
	if rep.TotalCommands != 1 {
		t.Errorf("total commands = %d, want 1", rep.TotalCommands)
	}
	if len(rep.Supported) != 1 {
		t.Fatalf("supported = %d, want 1", len(rep.Supported))
	}
	s := rep.Supported[0]
	if s.Command != "git status" || s.Count != 1 || s.Via != "command" {
		t.Errorf("supported entry = %+v", s)
	}
	// 400 chars / 4 = 100 raw tokens; 70% saved = 70.
	if s.SavedTokens != 70 {
		t.Errorf("saved tokens = %d, want 70", s.SavedTokens)
	}
}

func TestGenerateAlreadyGortk(t *testing.T) {
	root := writeSession(t, "proj", []string{
		bash("t1", "gortk git status"),
		result("t1", "clean"),
	})
	rep := Generate(root, Options{All: true})
	if rep.AlreadyGortk != 1 {
		t.Errorf("alreadyGortk = %d, want 1", rep.AlreadyGortk)
	}
	if len(rep.Supported) != 0 {
		t.Errorf("supported should be empty, got %d", len(rep.Supported))
	}
}

func TestGenerateUnsupported(t *testing.T) {
	root := writeSession(t, "proj", []string{
		bash("t1", "zzztool --run"),
		result("t1", "done"),
	})
	rep := Generate(root, Options{All: true})
	if len(rep.Unsupported) != 1 {
		t.Fatalf("unsupported = %d, want 1", len(rep.Unsupported))
	}
	if rep.Unsupported[0].BaseCommand != "zzztool" || rep.Unsupported[0].Count != 1 {
		t.Errorf("unsupported = %+v", rep.Unsupported[0])
	}
}

func TestGenerateChainSplit(t *testing.T) {
	// "cd /x && git status" -> cd ignored, git status supported.
	root := writeSession(t, "proj", []string{
		bash("t1", "cd /x && git status"),
		result("t1", strings.Repeat("y", 800)),
	})
	rep := Generate(root, Options{All: true})
	if rep.TotalCommands != 1 {
		t.Errorf("total = %d (cd ignored, git counted)", rep.TotalCommands)
	}
	if len(rep.Supported) != 1 || rep.Supported[0].Command != "git status" {
		t.Errorf("supported = %+v", rep.Supported)
	}
}

func TestGenerateAggregatesAcrossSessions(t *testing.T) {
	root := writeSession(t, "proj", []string{
		bash("t1", "git status"),
		result("t1", strings.Repeat("x", 400)),
		bash("t2", "git status"),
		result("t2", strings.Repeat("x", 400)),
	})
	rep := Generate(root, Options{All: true})
	if len(rep.Supported) != 1 {
		t.Fatalf("want 1 bucket, got %d", len(rep.Supported))
	}
	if rep.Supported[0].Count != 2 {
		t.Errorf("count = %d, want 2", rep.Supported[0].Count)
	}
	// 2 x 100 raw -> 200 raw; 70% -> 140 saved.
	if rep.Supported[0].SavedTokens != 140 {
		t.Errorf("saved = %d, want 140", rep.Supported[0].SavedTokens)
	}
}

func TestGenerateProjectFilter(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"C--git-gortk", "C--git-other"} {
		p := filepath.Join(root, name)
		_ = os.MkdirAll(p, 0o755)
		_ = os.WriteFile(filepath.Join(p, "s.jsonl"),
			[]byte(bash("t1", "git status")+"\n"+result("t1", "clean")+"\n"), 0o644)
	}
	rep := Generate(root, Options{Project: "gortk", All: false})
	if rep.SessionsScanned != 1 {
		t.Errorf("project filter should scan 1 session, got %d", rep.SessionsScanned)
	}
}

func TestFormatTextNoMissed(t *testing.T) {
	out := FormatText(Report{SessionsScanned: 2, TotalCommands: 0, SinceDays: 30}, 15)
	if !strings.Contains(out, "No missed savings found") {
		t.Errorf("missing clean message: %s", out)
	}
}

func TestFormatTextPercentDecimal(t *testing.T) {
	// 3/1000 must render 0.3%, not integer-truncated 0%.
	out := FormatText(Report{TotalCommands: 1000, AlreadyGortk: 3, SinceDays: 30,
		Supported: []SupportedEntry{{Command: "git status", Count: 1, Via: "command", SavedTokens: 70}}}, 15)
	if !strings.Contains(out, "0.3%") {
		t.Errorf("expected 0.3%%: %s", out)
	}
}

func TestFormatTextZeroDivision(t *testing.T) {
	out := FormatText(Report{SessionsScanned: 0, TotalCommands: 0, SinceDays: 30}, 15)
	if !strings.Contains(out, "(0.0%)") {
		t.Errorf("expected (0.0%%): %s", out)
	}
}

func TestFormatTokens(t *testing.T) {
	cases := map[int]string{
		500:       "500 tokens",
		1500:      "1.5K tokens",
		2_500_000: "2.5M tokens",
	}
	for in, want := range cases {
		if got := formatTokens(in); got != want {
			t.Errorf("formatTokens(%d) = %q, want %q", in, got, want)
		}
	}
}
