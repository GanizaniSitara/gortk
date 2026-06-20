package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Codex hook: PreToolUse JSON in/out ──

// codexInput builds a Codex PreToolUse payload (snake_case tool_name +
// tool_input.command, with optional extra tool_input fields and the extra
// top-level Codex fields hook_event_name/cwd/session_id riding along to confirm
// they are ignored).
func codexInput(t *testing.T, tool, cmd string, extra map[string]any) []byte {
	t.Helper()
	ti := map[string]any{"command": cmd}
	for k, v := range extra {
		ti[k] = v
	}
	b, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       tool,
		"tool_input":      ti,
		"cwd":             "C:/git/gortk",
		"session_id":      "abc-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCodexHookHappyPath(t *testing.T) {
	in := codexInput(t, "Bash", "git status", nil)
	out, ok := CodexHookResponse(in, nil, gitCargo)
	if !ok {
		t.Fatalf("expected a hook response for git status")
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, out)
	}
	hook, _ := v["hookSpecificOutput"].(map[string]any)
	if hook == nil {
		t.Fatalf("missing hookSpecificOutput: %s", out)
	}
	if hook["hookEventName"] != "PreToolUse" {
		t.Errorf("hookEventName = %v", hook["hookEventName"])
	}
	// Codex REQUIRES permissionDecision "allow" alongside updatedInput.
	if hook["permissionDecision"] != "allow" {
		t.Errorf("permissionDecision = %v, want \"allow\"", hook["permissionDecision"])
	}
	if hook["permissionDecisionReason"] != "gortk auto-rewrite" {
		t.Errorf("permissionDecisionReason = %v", hook["permissionDecisionReason"])
	}
	updated, _ := hook["updatedInput"].(map[string]any)
	if updated == nil || updated["command"] != "gortk git status" {
		t.Errorf("updatedInput.command = %v, want %q", updated["command"], "gortk git status")
	}
}

func TestCodexHookPreservesToolInputFields(t *testing.T) {
	// Extra fields on tool_input must survive untouched in updatedInput.
	in := codexInput(t, "Bash", "git status", map[string]any{
		"timeout":     float64(30000),
		"description": "Check repo status",
	})
	out, ok := CodexHookResponse(in, nil, gitCargo)
	if !ok {
		t.Fatal("expected rewrite")
	}
	var v map[string]any
	_ = json.Unmarshal([]byte(out), &v)
	updated := v["hookSpecificOutput"].(map[string]any)["updatedInput"].(map[string]any)
	if updated["command"] != "gortk git status" {
		t.Errorf("command = %v", updated["command"])
	}
	if updated["timeout"] != float64(30000) {
		t.Errorf("timeout not preserved: %v", updated["timeout"])
	}
	if updated["description"] != "Check repo status" {
		t.Errorf("description not preserved: %v", updated["description"])
	}
}

func TestCodexHookCompoundCommand(t *testing.T) {
	in := codexInput(t, "Bash", "git add . && cargo test", nil)
	out, ok := CodexHookResponse(in, nil, gitCargo)
	if !ok {
		t.Fatal("expected rewrite")
	}
	var v map[string]any
	_ = json.Unmarshal([]byte(out), &v)
	cmd := v["hookSpecificOutput"].(map[string]any)["updatedInput"].(map[string]any)["command"]
	if cmd != "gortk git add . && gortk cargo test" {
		t.Errorf("compound command = %v", cmd)
	}
}

func TestCodexHookEnvPrefix(t *testing.T) {
	in := codexInput(t, "Bash", "GIT_PAGER=cat git status", nil)
	out, ok := CodexHookResponse(in, nil, gitCargo)
	if !ok {
		t.Fatal("expected rewrite")
	}
	var v map[string]any
	_ = json.Unmarshal([]byte(out), &v)
	cmd := v["hookSpecificOutput"].(map[string]any)["updatedInput"].(map[string]any)["command"]
	if cmd != "GIT_PAGER=cat gortk git status" {
		t.Errorf("env-prefix command = %v", cmd)
	}
}

func TestCodexHookBashLowercaseAccepted(t *testing.T) {
	// snake_case "bash" (lowercase) is also accepted.
	in := codexInput(t, "bash", "git status", nil)
	out, ok := CodexHookResponse(in, nil, gitCargo)
	if !ok {
		t.Fatalf("lowercase bash tool should rewrite")
	}
	if !strings.Contains(out, "gortk git status") {
		t.Errorf("missing rewrite: %s", out)
	}
}

func TestCodexHookExcludedCommand(t *testing.T) {
	// Excluding "git" passes it through untouched (no output).
	in := codexInput(t, "Bash", "git status", nil)
	out, ok := CodexHookResponse(in, []string{"git"}, gitCargo)
	if ok || out != "" {
		t.Errorf("excluded command should pass through, got %q", out)
	}
}

// ── Codex hook pass-through cases (must emit nothing, never crash) ──

func TestCodexHookPassthroughCases(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{"unsupported command", codexInput(t, "Bash", "htop", nil)},
		{"already gortk", codexInput(t, "Bash", "gortk git status", nil)},
		{"non-bash tool apply_patch", codexInput(t, "apply_patch", "git status", nil)},
		{"non-bash tool Read", codexInput(t, "Read", "git status", nil)},
		{"empty command", codexInput(t, "Bash", "", nil)},
		{"substitution", codexInput(t, "Bash", "git status $(rm -rf /tmp/x)", nil)},
		{"backtick", codexInput(t, "Bash", "git status `rm -rf /tmp/x`", nil)},
		{"file redirect", codexInput(t, "Bash", "git log > /tmp/out.txt", nil)},
		{"heredoc", codexInput(t, "Bash", "cat <<EOF\nhi\nEOF", nil)},
		{"malformed json", []byte("not valid json {{{")},
		{"empty input", []byte("")},
		{"whitespace input", []byte("   \n  ")},
		{"empty object", []byte("{}")},
		{"no tool_input", []byte(`{"tool_name":"Bash"}`)},
		{"tool_input not object", []byte(`{"tool_name":"Bash","tool_input":42}`)},
		{"null tool_input", []byte(`{"tool_name":"Bash","tool_input":null}`)},
		{"unknown shape", []byte(`{"foo":"bar"}`)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, ok := CodexHookResponse(c.payload, nil, gitCargo)
			if ok {
				t.Errorf("expected pass-through (no rewrite), got %q", out)
			}
			if out != "" {
				t.Errorf("pass-through must emit empty string, got %q", out)
			}
		})
	}
}

func TestCodexHookBOMTolerated(t *testing.T) {
	// A leading UTF-8 BOM (some Windows hosts prepend it) must not break parsing.
	bom := []byte{0xEF, 0xBB, 0xBF}
	in := append(bom, codexInput(t, "Bash", "git status", nil)...)
	out, ok := CodexHookResponse(in, nil, gitCargo)
	if !ok {
		t.Fatalf("BOM-prefixed payload should still rewrite, got none")
	}
	if !strings.Contains(out, "gortk git status") {
		t.Errorf("BOM payload response missing rewrite: %s", out)
	}
}

func TestRunHookCodexStreams(t *testing.T) {
	// End-to-end through the io.Reader/Writer plumbing: a valid payload yields a
	// JSON line; an unsupported payload yields nothing.
	in := bytes.NewReader(codexInput(t, "Bash", "git status", nil))
	var out bytes.Buffer
	code, err := runHookCodex(in, &out, gitCargo)
	if err != nil || code != 0 {
		t.Fatalf("runHookCodex code=%d err=%v", code, err)
	}
	if !strings.Contains(out.String(), `"updatedInput"`) {
		t.Errorf("expected hook JSON on stdout, got %q", out.String())
	}
	if !strings.Contains(out.String(), `"permissionDecision":"allow"`) {
		t.Errorf("expected permissionDecision allow on stdout, got %q", out.String())
	}

	var out2 bytes.Buffer
	code, _ = runHookCodex(bytes.NewReader(codexInput(t, "Bash", "htop", nil)), &out2, gitCargo)
	if code != 0 {
		t.Errorf("pass-through code = %d", code)
	}
	if out2.Len() != 0 {
		t.Errorf("pass-through wrote to stdout: %q", out2.String())
	}
}

// ── Codex installer (user-level ~/.codex/hooks.json) ──

// codexHome returns a temp user home for the Codex installer, with CODEX_HOME
// explicitly unset so resolution falls through to <home>/.codex. Returns
// (home, hooksPath).
func codexHome(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv(codexHomeEnv, "") // force <home>/.codex resolution
	home := t.TempDir()
	return home, filepath.Join(home, codexHomeDir, codexHooksFile)
}

// codexPreToolUseArray pulls hooks.PreToolUse out of a parsed hooks.json map.
func codexPreToolUseArray(t *testing.T, root map[string]any) []any {
	t.Helper()
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks.json missing hooks object: %v", root)
	}
	arr, ok := hooks["PreToolUse"].([]any)
	if !ok {
		t.Fatalf("hooks missing PreToolUse array: %v", hooks)
	}
	return arr
}

func TestCodexInstallCreatesHooks(t *testing.T) {
	home, hooksPath := codexHome(t)

	if code, err := runCodexInitAt(home, false, false, 0); err != nil || code != 0 {
		t.Fatalf("install code=%d err=%v", code, err)
	}

	root := readJSON(t, hooksPath)
	arr := codexPreToolUseArray(t, root)
	if len(arr) != 1 {
		t.Fatalf("expected exactly 1 PreToolUse entry, got %d: %v", len(arr), arr)
	}
	entry, ok := arr[0].(map[string]any)
	if !ok {
		t.Fatalf("PreToolUse entry is not an object: %v", arr[0])
	}
	if entry["matcher"] != "Bash" {
		t.Errorf("entry.matcher = %v, want \"Bash\"", entry["matcher"])
	}
	inner, ok := entry["hooks"].([]any)
	if !ok || len(inner) != 1 {
		t.Fatalf("entry.hooks = %v, want a 1-element array", entry["hooks"])
	}
	h, ok := inner[0].(map[string]any)
	if !ok {
		t.Fatalf("nested hook is not an object: %v", inner[0])
	}
	if h["type"] != "command" {
		t.Errorf("nested hook.type = %v, want \"command\"", h["type"])
	}
	if h["command"] != "gortk hook codex" {
		t.Errorf("nested hook.command = %v, want %q", h["command"], "gortk hook codex")
	}
	if h["statusMessage"] != "gortk rewrite" {
		t.Errorf("nested hook.statusMessage = %v, want %q", h["statusMessage"], "gortk rewrite")
	}
	// No backup should exist for a freshly created file.
	if _, err := os.Stat(hooksPath + ".bak"); !os.IsNotExist(err) {
		t.Errorf("backup should not exist for a newly created hooks.json (err=%v)", err)
	}
}

func TestCodexInstallPreservesExistingKeys(t *testing.T) {
	home, hooksPath := codexHome(t)
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing hooks.json with unrelated keys AND an unrelated hook event.
	pre := map[string]any{
		"version": float64(1),
		"nested":  map[string]any{"keep": true},
		"hooks": map[string]any{
			"PostToolUse": []any{
				map[string]any{"matcher": "Bash", "hooks": []any{map[string]any{"type": "command", "command": "echo done"}}},
			},
		},
	}
	preBytes, _ := json.MarshalIndent(pre, "", "  ")
	if err := os.WriteFile(hooksPath, preBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	if code, err := runCodexInitAt(home, false, false, 0); err != nil || code != 0 {
		t.Fatalf("install code=%d err=%v", code, err)
	}

	root := readJSON(t, hooksPath)
	if root["version"] != float64(1) {
		t.Errorf("version not preserved: %v", root["version"])
	}
	nested, _ := root["nested"].(map[string]any)
	if nested == nil || nested["keep"] != true {
		t.Errorf("nested key not preserved: %v", root["nested"])
	}
	// The unrelated PostToolUse hook must survive alongside the new PreToolUse.
	hooks := root["hooks"].(map[string]any)
	if _, ok := hooks["PostToolUse"].([]any); !ok {
		t.Errorf("PostToolUse hook event dropped: %v", hooks)
	}
	arr := codexPreToolUseArray(t, root)
	if len(arr) != 1 {
		t.Errorf("expected 1 PreToolUse entry, got %d", len(arr))
	}
	// A backup of the pre-existing file must exist.
	if _, err := os.Stat(hooksPath + ".bak"); err != nil {
		t.Errorf("backup not created for pre-existing hooks.json: %v", err)
	}
}

func TestCodexInstallIdempotent(t *testing.T) {
	home, hooksPath := codexHome(t)

	if code, err := runCodexInitAt(home, false, false, 0); err != nil || code != 0 {
		t.Fatalf("first install code=%d err=%v", code, err)
	}
	first, _ := os.ReadFile(hooksPath)

	if code, err := runCodexInitAt(home, false, false, 0); err != nil || code != 0 {
		t.Fatalf("second install code=%d err=%v", code, err)
	}
	second, _ := os.ReadFile(hooksPath)

	if string(first) != string(second) {
		t.Errorf("hooks.json changed on re-install:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	root := readJSON(t, hooksPath)
	arr := codexPreToolUseArray(t, root)
	if len(arr) != 1 {
		t.Errorf("expected exactly 1 PreToolUse entry after re-install, got %d", len(arr))
	}
}

func TestCodexInstallIdempotentManualEdit(t *testing.T) {
	// A hand-written entry that references `gortk hook codex` (different matcher /
	// no statusMessage) must be detected as present — no duplicate appended.
	home, hooksPath := codexHome(t)
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	manual := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks":   []any{map[string]any{"type": "command", "command": "gortk hook codex"}},
				},
			},
		},
	}
	mb, _ := json.MarshalIndent(manual, "", "  ")
	if err := os.WriteFile(hooksPath, mb, 0o644); err != nil {
		t.Fatal(err)
	}

	if code, err := runCodexInitAt(home, false, false, 0); err != nil || code != 0 {
		t.Fatalf("install code=%d err=%v", code, err)
	}
	root := readJSON(t, hooksPath)
	arr := codexPreToolUseArray(t, root)
	if len(arr) != 1 {
		t.Errorf("manual gortk entry should be detected; expected 1 entry, got %d: %v", len(arr), arr)
	}
}

func TestCodexInstallRefusesInvalidJSON(t *testing.T) {
	home, hooksPath := codexHome(t)
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := []byte("{ this is not valid json")
	if err := os.WriteFile(hooksPath, bad, 0o644); err != nil {
		t.Fatal(err)
	}

	code, err := runCodexInitAt(home, false, false, 0)
	if err == nil || code == 0 {
		t.Fatalf("install should refuse invalid JSON (code=%d err=%v)", code, err)
	}
	// The bad file must be untouched and no backup written.
	got, _ := os.ReadFile(hooksPath)
	if string(got) != string(bad) {
		t.Errorf("invalid hooks.json was modified: %q", string(got))
	}
	if _, err := os.Stat(hooksPath + ".bak"); !os.IsNotExist(err) {
		t.Errorf("backup should not be written when JSON is invalid (err=%v)", err)
	}
}

func TestCodexDryRunWritesNothing(t *testing.T) {
	home, hooksPath := codexHome(t)
	if code, err := runCodexInitAt(home, false, true, 0); err != nil || code != 0 {
		t.Fatalf("dry-run code=%d err=%v", code, err)
	}
	if _, err := os.Stat(hooksPath); !os.IsNotExist(err) {
		t.Errorf("dry-run created hooks.json (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Dir(hooksPath)); !os.IsNotExist(err) {
		t.Errorf("dry-run created ~/.codex dir (err=%v)", err)
	}
}

func TestCodexShowWritesNothing(t *testing.T) {
	home, hooksPath := codexHome(t)
	if code, err := runCodexInitAt(home, true, false, 0); err != nil || code != 0 {
		t.Fatalf("--show code=%d err=%v", code, err)
	}
	if _, err := os.Stat(hooksPath); !os.IsNotExist(err) {
		t.Errorf("--show created hooks.json (err=%v)", err)
	}
}

func TestCodexRespectsCodexHomeEnv(t *testing.T) {
	// CODEX_HOME, when set, overrides <home>/.codex.
	codexHomeDir := t.TempDir()
	t.Setenv(codexHomeEnv, codexHomeDir)
	// A separate (wrong) home that must NOT be used.
	otherHome := t.TempDir()

	if code, err := runCodexInitAt(otherHome, false, false, 0); err != nil || code != 0 {
		t.Fatalf("install code=%d err=%v", code, err)
	}
	// hooks.json lands directly under CODEX_HOME, not under otherHome/.codex.
	wantPath := filepath.Join(codexHomeDir, codexHooksFile)
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("hooks.json not written to CODEX_HOME: %v", err)
	}
	if _, err := os.Stat(filepath.Join(otherHome, ".codex", codexHooksFile)); !os.IsNotExist(err) {
		t.Errorf("hooks.json wrongly written under home/.codex despite CODEX_HOME (err=%v)", err)
	}
}

// ── RunInit routing: --codex routes to the user-level Codex installer ──

func TestRunInitCodexRouting(t *testing.T) {
	// `gortk init --codex --dry-run` must route to the Codex path and write nothing.
	home, hooksPath := codexHome(t)
	t.Setenv("HOME", home)        // POSIX home (no effect on Windows but harmless)
	t.Setenv("USERPROFILE", home) // Windows home seam for os.UserHomeDir()
	code, err := RunInit([]string{"--codex", "--dry-run"}, 0)
	if err != nil || code != 0 {
		t.Fatalf("RunInit --codex --dry-run code=%d err=%v", code, err)
	}
	if _, err := os.Stat(hooksPath); !os.IsNotExist(err) {
		t.Errorf("dry-run via RunInit created hooks.json (err=%v)", err)
	}
}

func TestRunInitCodexAndCopilotMutuallyExclusive(t *testing.T) {
	// --codex together with --copilot is rejected (exit 2) and writes nothing.
	code, err := RunInit([]string{"--codex", "--copilot", "--dry-run"}, 0)
	if code != 2 {
		t.Errorf("expected exit 2 for --codex --copilot, got code=%d err=%v", code, err)
	}
}
