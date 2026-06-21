package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gortk/internal/cmds/cchistory"

	// Populate the registry so Classify finds real gortk commands.
	_ "gortk/internal/cmds/git"
	_ "gortk/internal/cmds/ls"
)

func mkCmd(command string, outputLen int) cchistory.ExtractedCommand {
	return cchistory.ExtractedCommand{Command: command, OutputLen: outputLen, SessionID: "test"}
}

func TestProgressBarBoundaries(t *testing.T) {
	cases := []struct {
		pct  float64
		want string
	}{
		{0, "....."},
		{100, "@@@@@"},
		{50, "@@@.."},
	}
	for _, c := range cases {
		if got := progressBar(c.pct, 5); got != c.want {
			t.Errorf("progressBar(%v) = %q, want %q", c.pct, got, c.want)
		}
	}
}

func TestCountAllGortk(t *testing.T) {
	cmds := []cchistory.ExtractedCommand{
		mkCmd("gortk git status", 200),
		mkCmd("gortk cargo test", 5000),
		mkCmd("gortk git log -10", 800),
	}
	total, gortk, output := CountGortkCommands(cmds)
	if total != 3 || gortk != 3 || output != 6000 {
		t.Errorf("got total=%d gortk=%d output=%d", total, gortk, output)
	}
}

func TestCountHookRewrittenCommands(t *testing.T) {
	// "git status" and a chained git are gortk-covered; echo is not.
	cmds := []cchistory.ExtractedCommand{
		mkCmd("git status", 500),
		mkCmd("git log -5", 3000),
		mkCmd("echo hello", 100),
	}
	total, gortk, output := CountGortkCommands(cmds)
	if total != 3 || gortk != 2 || output != 3600 {
		t.Errorf("got total=%d gortk=%d output=%d", total, gortk, output)
	}
}

func TestCountMixedExplicitAndHook(t *testing.T) {
	cmds := []cchistory.ExtractedCommand{
		mkCmd("gortk git status", 200),
		mkCmd("git log -5", 1000),
		mkCmd("gortk git diff", 5000),
		mkCmd("echo hello", -1),
	}
	total, gortk, output := CountGortkCommands(cmds)
	if total != 4 || gortk != 3 || output != 6200 {
		t.Errorf("got total=%d gortk=%d output=%d", total, gortk, output)
	}
}

func TestCountUnsupportedNotCounted(t *testing.T) {
	cmds := []cchistory.ExtractedCommand{
		mkCmd("echo hello", 100),
		mkCmd("zzztool run", 10),
	}
	total, gortk, _ := CountGortkCommands(cmds)
	if total != 2 || gortk != 0 {
		t.Errorf("got total=%d gortk=%d", total, gortk)
	}
}

func TestCountChainedSplit(t *testing.T) {
	// "cd ./path && gortk ls" -> 2 commands, 1 covered.
	cmds := []cchistory.ExtractedCommand{mkCmd("cd ./your/app/path && gortk ls", 200)}
	total, gortk, _ := CountGortkCommands(cmds)
	if total != 2 || gortk != 1 {
		t.Errorf("chain split: total=%d gortk=%d, want 2/1", total, gortk)
	}
}

func TestCountChainedAllSupported(t *testing.T) {
	cmds := []cchistory.ExtractedCommand{mkCmd("git status && git log -5", 500)}
	total, gortk, _ := CountGortkCommands(cmds)
	if total != 2 || gortk != 2 {
		t.Errorf("both git: total=%d gortk=%d, want 2/2", total, gortk)
	}
}

func TestCountChainedSemicolon(t *testing.T) {
	cmds := []cchistory.ExtractedCommand{mkCmd("cd /tmp; git status; echo done", 100)}
	total, gortk, _ := CountGortkCommands(cmds)
	if total != 3 || gortk != 1 {
		t.Errorf("semicolon: total=%d gortk=%d, want 3/1", total, gortk)
	}
}

func TestCountEmpty(t *testing.T) {
	total, gortk, output := CountGortkCommands(nil)
	if total != 0 || gortk != 0 || output != 0 {
		t.Errorf("empty: total=%d gortk=%d output=%d", total, gortk, output)
	}
}

func TestAdoptionPct(t *testing.T) {
	if (Summary{TotalCmds: 0}).AdoptionPct() != 0 {
		t.Error("zero division should give 0")
	}
	if (Summary{TotalCmds: 20, GortkCmds: 15}).AdoptionPct() != 75.0 {
		t.Error("15/20 should be 75%")
	}
}

func TestGenerateEndToEnd(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	_ = os.MkdirAll(proj, 0o755)
	lines := []string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"gortk git status"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"On branch main"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"git log -5"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t2","content":"commit abc"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"echo hi"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t3","content":"hi"}]}}`,
	}
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	summaries := Generate(root, time.Now())
	if len(summaries) != 1 {
		t.Fatalf("want 1 session summary, got %d", len(summaries))
	}
	s := summaries[0]
	// 3 commands total, gortk covers 2 (explicit gortk + git log); echo not.
	if s.TotalCmds != 3 || s.GortkCmds != 2 {
		t.Errorf("summary = %+v, want total=3 gortk=2", s)
	}
}

func TestGenerateSkipsSubagents(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	sub := filepath.Join(proj, "subagents")
	_ = os.MkdirAll(sub, 0o755)
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"git status"}}]}}` +
		"\n" + `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"clean"}]}}` + "\n"
	_ = os.WriteFile(filepath.Join(proj, "top.jsonl"), []byte(line), 0o644)
	_ = os.WriteFile(filepath.Join(sub, "agent.jsonl"), []byte(line), 0o644)

	summaries := Generate(root, time.Now())
	if len(summaries) != 1 {
		t.Errorf("subagent transcript should be skipped, got %d summaries", len(summaries))
	}
}

func TestRelativeDateFrom(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		mod  time.Time
		want string
	}{
		{now, "Today"},
		{now.Add(-25 * time.Hour), "Yesterday"},
		{now.Add(-72 * time.Hour), "3d ago"},
	}
	for _, c := range cases {
		if got := relativeDateFrom(c.mod, now); got != c.want {
			t.Errorf("relativeDateFrom(%v) = %q, want %q", c.mod, got, c.want)
		}
	}
}

func TestFormatTextEmpty(t *testing.T) {
	out := FormatText(nil)
	if !strings.Contains(out, "no sessions with Bash commands") {
		t.Errorf("missing empty message: %s", out)
	}
}

func TestFormatTextAverage(t *testing.T) {
	out := FormatText([]Summary{
		{ID: "a", Date: "Today", TotalCmds: 10, GortkCmds: 5},
		{ID: "b", Date: "Today", TotalCmds: 10, GortkCmds: 5},
	})
	if !strings.Contains(out, "Average adoption: 50%") {
		t.Errorf("expected 50%% average: %s", out)
	}
}
