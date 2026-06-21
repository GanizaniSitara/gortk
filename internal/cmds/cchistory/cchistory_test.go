package cchistory

import (
	"os"
	"path/filepath"
	"testing"

	// Blank-import a couple of real command packages so their init() populates
	// the registry. Classify consults registry.Lookup, which is empty unless some
	// command package has registered — these give the classifier real commands
	// ("git", "ls") to find, exactly as it would at runtime via allcmds.
	_ "gortk/internal/cmds/git"
	_ "gortk/internal/cmds/ls"
)

func writeJSONL(t *testing.T, lines []string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	content := ""
	for _, l := range lines {
		content += l + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestExtractAssistantBash(t *testing.T) {
	path := writeJSONL(t, []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_abc","name":"Bash","input":{"command":"git status"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_abc","content":"On branch master\nnothing to commit"}]}}`,
	})
	cmds, err := ExtractCommands(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cmds) != 1 {
		t.Fatalf("want 1 command, got %d", len(cmds))
	}
	if cmds[0].Command != "git status" {
		t.Errorf("command = %q", cmds[0].Command)
	}
	if cmds[0].OutputLen != len("On branch master\nnothing to commit") {
		t.Errorf("outputLen = %d", cmds[0].OutputLen)
	}
}

func TestExtractNonBashIgnored(t *testing.T) {
	path := writeJSONL(t, []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_abc","name":"Read","input":{"file_path":"/tmp/foo"}}]}}`,
	})
	cmds, _ := ExtractCommands(path)
	if len(cmds) != 0 {
		t.Errorf("want 0, got %d", len(cmds))
	}
}

func TestExtractMultipleTools(t *testing.T) {
	path := writeJSONL(t, []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"git status"}},{"type":"tool_use","id":"toolu_2","name":"Bash","input":{"command":"git diff"}}]}}`,
	})
	cmds, _ := ExtractCommands(path)
	if len(cmds) != 2 || cmds[0].Command != "git status" || cmds[1].Command != "git diff" {
		t.Errorf("got %+v", cmds)
	}
}

func TestExtractMalformedLineSkipped(t *testing.T) {
	path := writeJSONL(t, []string{
		"this is not json at all",
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_ok","name":"Bash","input":{"command":"ls"}}]}}`,
	})
	cmds, _ := ExtractCommands(path)
	if len(cmds) != 1 || cmds[0].Command != "ls" {
		t.Errorf("got %+v", cmds)
	}
}

func TestExtractErrorFlagAndContent(t *testing.T) {
	path := writeJSONL(t, []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_abc","name":"Bash","input":{"command":"git commit --ammend"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_abc","content":"error: unexpected argument '--ammend'","is_error":true}]}}`,
	})
	cmds, _ := ExtractCommands(path)
	if len(cmds) != 1 {
		t.Fatalf("want 1, got %d", len(cmds))
	}
	if !cmds[0].IsError {
		t.Error("expected is_error true")
	}
	if cmds[0].OutputContent != "error: unexpected argument '--ammend'" {
		t.Errorf("content = %q", cmds[0].OutputContent)
	}
}

func TestExtractSequenceOrdering(t *testing.T) {
	path := writeJSONL(t, []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"first"}},{"type":"tool_use","id":"t2","name":"Bash","input":{"command":"second"}},{"type":"tool_use","id":"t3","name":"Bash","input":{"command":"third"}}]}}`,
	})
	cmds, _ := ExtractCommands(path)
	if len(cmds) != 3 {
		t.Fatalf("want 3, got %d", len(cmds))
	}
	for i, want := range []string{"first", "second", "third"} {
		if cmds[i].Command != want || cmds[i].SequenceIndex != i {
			t.Errorf("cmd %d = %q seq=%d", i, cmds[i].Command, cmds[i].SequenceIndex)
		}
	}
}

func TestExtractResultContentAsArray(t *testing.T) {
	// Some transcripts encode tool_result content as an array of text blocks.
	path := writeJSONL(t, []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"echo hi"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"hi there"}]}]}}`,
	})
	cmds, _ := ExtractCommands(path)
	if len(cmds) != 1 || cmds[0].OutputContent != "hi there" {
		t.Errorf("got %+v", cmds)
	}
}

func TestExtractMissingResult(t *testing.T) {
	path := writeJSONL(t, []string{
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}`,
	})
	cmds, _ := ExtractCommands(path)
	if len(cmds) != 1 || cmds[0].OutputLen != -1 {
		t.Errorf("expected outputLen -1 when no result, got %+v", cmds)
	}
}

func TestExtractUsage(t *testing.T) {
	path := writeJSONL(t, []string{
		`{"type":"assistant","timestamp":"2026-01-30T10:00:00Z","message":{"model":"claude-opus-4-7","usage":{"input_tokens":100,"output_tokens":50,"cache_creation_input_tokens":200,"cache_read_input_tokens":3000},"content":[]}}`,
		`{"type":"user","message":{"role":"user","content":"hello"}}`,
		`{"type":"assistant","timestamp":"2026-01-30T10:01:00Z","message":{"model":"claude-opus-4-7","usage":{"input_tokens":10,"output_tokens":5},"content":[]}}`,
	})
	usages, err := ExtractUsage(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(usages) != 2 {
		t.Fatalf("want 2 usages, got %d", len(usages))
	}
	if usages[0].InputTokens != 100 || usages[0].OutputTokens != 50 ||
		usages[0].CacheCreateTokens != 200 || usages[0].CacheReadTokens != 3000 {
		t.Errorf("usage[0] = %+v", usages[0])
	}
	if usages[0].Model != "claude-opus-4-7" {
		t.Errorf("model = %q", usages[0].Model)
	}
	if usages[0].Timestamp.IsZero() {
		t.Error("expected parsed timestamp")
	}
	// Older entry without cache fields: defaults to 0.
	if usages[1].CacheCreateTokens != 0 || usages[1].CacheReadTokens != 0 {
		t.Errorf("usage[1] cache defaults = %+v", usages[1])
	}
}

func TestEncodeProjectPath(t *testing.T) {
	cases := map[string]string{
		`/Users/foo/bar`:          "-Users-foo-bar",
		`/Users/first.last/proj`:  "-Users-first-last-proj",
		`C:\Users\foo\bar`:        "C:-Users-foo-bar",
		`/home/chris/2_project/p`: "-home-chris-2-project-p",
	}
	for in, want := range cases {
		if got := EncodeProjectPath(in); got != want {
			t.Errorf("EncodeProjectPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEncodeProjectPathNonASCII(t *testing.T) {
	got := EncodeProjectPath("/home/user/外主/app")
	if got != "-home-user----app" {
		t.Errorf("got %q", got)
	}
}

func TestDiscoverSessionsMissingDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if got := DiscoverSessions(missing, "", 30); got != nil {
		t.Errorf("expected nil for missing dir, got %v", got)
	}
}

func TestDiscoverSessionsProjectFilter(t *testing.T) {
	root := t.TempDir()
	match := filepath.Join(root, "C--git-gortk")
	other := filepath.Join(root, "C--git-other")
	_ = os.MkdirAll(match, 0o755)
	_ = os.MkdirAll(other, 0o755)
	_ = os.WriteFile(filepath.Join(match, "a.jsonl"), []byte(""), 0o644)
	_ = os.WriteFile(filepath.Join(other, "b.jsonl"), []byte(""), 0o644)

	sessions := DiscoverSessions(root, "gortk", 0)
	if len(sessions) != 1 {
		t.Fatalf("want 1 session, got %d: %v", len(sessions), sessions)
	}
	if filepath.Base(sessions[0]) != "a.jsonl" {
		t.Errorf("got %s", sessions[0])
	}
}

func TestDiscoverSessionsRecursesSubagents(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	sub := filepath.Join(proj, "subagents")
	_ = os.MkdirAll(sub, 0o755)
	_ = os.WriteFile(filepath.Join(proj, "top.jsonl"), []byte(""), 0o644)
	_ = os.WriteFile(filepath.Join(sub, "agent.jsonl"), []byte(""), 0o644)
	sessions := DiscoverSessions(root, "", 0)
	if len(sessions) != 2 {
		t.Errorf("want 2 sessions (top + subagent), got %d: %v", len(sessions), sessions)
	}
}

func TestSplitChain(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"git status", []string{"git status"}},
		{"cd /tmp && git status", []string{"cd /tmp", "git status"}},
		{"cd /tmp; git status; echo done", []string{"cd /tmp", "git status", "echo done"}},
		{"git status | grep main", []string{"git status", "grep main"}},
		{"a || b", []string{"a", "b"}},
		{`echo "a && b"`, []string{`echo "a && b"`}}, // quoted operator not a split
		{"cat <<EOF && ls", []string{"cat <<EOF && ls"}},
	}
	for _, c := range cases {
		got := SplitChain(c.in)
		if len(got) != len(c.want) {
			t.Errorf("SplitChain(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("SplitChain(%q)[%d] = %q, want %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

func TestClassifySupportedByCommand(t *testing.T) {
	// "git" and "ls" are registered gortk commands.
	if c := Classify("git status"); c != SupportedByCommand {
		t.Errorf("git status classified as %v", c)
	}
	if c := Classify("ls -la"); c != SupportedByCommand {
		t.Errorf("ls -la classified as %v", c)
	}
}

func TestClassifyNormalizesAbsolutePathAndExt(t *testing.T) {
	if c := Classify("/usr/bin/git status"); c != SupportedByCommand {
		t.Errorf("/usr/bin/git classified as %v", c)
	}
	if c := Classify(`C:\tools\git.exe diff`); c != SupportedByCommand {
		t.Errorf("windows git.exe classified as %v", c)
	}
}

func TestClassifyEnvPrefixStripped(t *testing.T) {
	if c := Classify("RUST_BACKTRACE=1 git log"); c != SupportedByCommand {
		t.Errorf("env-prefixed git classified as %v", c)
	}
	if c := Classify("sudo git status"); c != SupportedByCommand {
		t.Errorf("sudo git classified as %v", c)
	}
}

func TestClassifyAlreadyGortk(t *testing.T) {
	if c := Classify("gortk git status"); c != AlreadyGortk {
		t.Errorf("classified as %v", c)
	}
}

func TestClassifyIgnoredBuiltins(t *testing.T) {
	for _, cmd := range []string{"cd /tmp", "echo hi", "export FOO=1", "pwd"} {
		if c := Classify(cmd); c != Ignored {
			t.Errorf("%q classified as %v, want Ignored", cmd, c)
		}
	}
}

func TestClassifyUnsupported(t *testing.T) {
	// A made-up tool gortk has no command or filter for.
	if c := Classify("zzznotarealtool --flag"); c != Unsupported {
		t.Errorf("classified as %v, want Unsupported", c)
	}
}

func TestBaseCommand(t *testing.T) {
	cases := map[string]string{
		"git commit --amend -m x":     "git commit",
		"cargo test":                  "cargo test",
		"git --version":               "git",
		"RUST_BACKTRACE=1 cargo test": "cargo test",
		"ls":                          "ls",
		"npm run build":               "npm run",
	}
	for in, want := range cases {
		if got := BaseCommand(in); got != want {
			t.Errorf("BaseCommand(%q) = %q, want %q", in, got, want)
		}
	}
}
