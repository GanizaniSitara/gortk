package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Copilot hook: VS Code Copilot Chat format (snake_case) ──

// vsCodeInput builds a VS Code Copilot Chat preToolUse payload (snake_case
// tool_name + tool_input.command, with optional extra tool_input fields).
func vsCodeInput(t *testing.T, tool, cmd string, extra map[string]any) []byte {
	t.Helper()
	ti := map[string]any{"command": cmd}
	for k, v := range extra {
		ti[k] = v
	}
	b, err := json.Marshal(map[string]any{
		"tool_name":  tool,
		"tool_input": ti,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCopilotVsCodeRunTerminalCommandHappyPath(t *testing.T) {
	// The distinguishing case vs the Claude hook: tool_name "runTerminalCommand"
	// must be accepted and produce an updatedInput rewrite.
	in := vsCodeInput(t, "runTerminalCommand", "git status", nil)
	out, ok := CopilotHookResponse(in, nil, gitCargo)
	if !ok {
		t.Fatalf("expected a rewrite for runTerminalCommand git status")
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
	if hook["permissionDecisionReason"] != "gortk auto-rewrite" {
		t.Errorf("permissionDecisionReason = %v", hook["permissionDecisionReason"])
	}
	// Rewrite-only: gortk never emits permissionDecision.
	if _, present := hook["permissionDecision"]; present {
		t.Errorf("gortk must not set permissionDecision: %s", out)
	}
	updated, _ := hook["updatedInput"].(map[string]any)
	if updated == nil || updated["command"] != "gortk git status" {
		t.Errorf("updatedInput.command = %v, want %q", updated["command"], "gortk git status")
	}
}

func TestCopilotVsCodeBashAccepted(t *testing.T) {
	// The legacy "Bash"/"bash" names also work through the Copilot path.
	for _, tool := range []string{"Bash", "bash"} {
		in := vsCodeInput(t, tool, "git status", nil)
		out, ok := CopilotHookResponse(in, nil, gitCargo)
		if !ok {
			t.Fatalf("tool %q: expected rewrite", tool)
		}
		if !strings.Contains(out, "gortk git status") {
			t.Errorf("tool %q: missing rewrite in %s", tool, out)
		}
	}
}

func TestCopilotVsCodePreservesToolInputFields(t *testing.T) {
	in := vsCodeInput(t, "runTerminalCommand", "git status", map[string]any{
		"timeout":     float64(30000),
		"description": "Check repo status",
	})
	out, ok := CopilotHookResponse(in, nil, gitCargo)
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

// ── Copilot hook: Copilot CLI format (camelCase, toolArgs JSON string) ──

// copilotCliInputJSON builds a Copilot CLI preToolUse payload: camelCase
// toolName + toolArgs, where toolArgs is a JSON-ENCODED STRING of the args
// object (so we exercise the parse-from-string path).
func copilotCliInputJSON(t *testing.T, toolName string, args map[string]any) []byte {
	t.Helper()
	argsBytes, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{
		"toolName": toolName,
		"toolArgs": string(argsBytes), // note: a string, not an object
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCopilotCliHappyPath(t *testing.T) {
	in := copilotCliInputJSON(t, "bash", map[string]any{
		"command":     "git status",
		"description": "show status",
		"mode":        "sync",
	})
	out, ok := CopilotHookResponse(in, nil, gitCargo)
	if !ok {
		t.Fatalf("expected a rewrite for Copilot CLI bash git status")
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, out)
	}
	if v["permissionDecisionReason"] != "gortk auto-rewrite" {
		t.Errorf("permissionDecisionReason = %v", v["permissionDecisionReason"])
	}
	// Rewrite-only: no permissionDecision key.
	if _, present := v["permissionDecision"]; present {
		t.Errorf("gortk must not set permissionDecision: %s", out)
	}
	mod, _ := v["modifiedArgs"].(map[string]any)
	if mod == nil {
		t.Fatalf("missing modifiedArgs: %s", out)
	}
	if mod["command"] != "gortk git status" {
		t.Errorf("modifiedArgs.command = %v, want %q", mod["command"], "gortk git status")
	}
	// Other toolArgs fields must be preserved untouched.
	if mod["description"] != "show status" {
		t.Errorf("description not preserved: %v", mod["description"])
	}
	if mod["mode"] != "sync" {
		t.Errorf("mode not preserved: %v", mod["mode"])
	}
}

func TestCopilotCliCompoundCommand(t *testing.T) {
	in := copilotCliInputJSON(t, "bash", map[string]any{
		"command": "git add . && cargo test",
	})
	out, ok := CopilotHookResponse(in, nil, gitCargo)
	if !ok {
		t.Fatal("expected rewrite")
	}
	var v map[string]any
	_ = json.Unmarshal([]byte(out), &v)
	cmd := v["modifiedArgs"].(map[string]any)["command"]
	if cmd != "gortk git add . && gortk cargo test" {
		t.Errorf("compound command = %v", cmd)
	}
}

// ── Copilot hook pass-through cases (emit nothing, never crash) ──

func TestCopilotPassthroughCases(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		// VS Code format pass-throughs.
		{"vscode unsupported command", vsCodeInput(t, "runTerminalCommand", "htop", nil)},
		{"vscode already gortk", vsCodeInput(t, "runTerminalCommand", "gortk git status", nil)},
		{"vscode non-bash tool", vsCodeInput(t, "Read", "git status", nil)},
		{"vscode empty command", vsCodeInput(t, "Bash", "", nil)},
		{"vscode substitution", vsCodeInput(t, "Bash", "git status $(rm -rf /tmp/x)", nil)},
		{"vscode file redirect", vsCodeInput(t, "Bash", "git log > /tmp/out.txt", nil)},
		{"vscode heredoc", vsCodeInput(t, "Bash", "cat <<EOF\nhi\nEOF", nil)},
		// Copilot CLI format pass-throughs.
		{"cli unsupported command", copilotCliInputJSON(t, "bash", map[string]any{"command": "htop"})},
		{"cli already gortk", copilotCliInputJSON(t, "bash", map[string]any{"command": "gortk git status"})},
		{"cli non-bash tool", copilotCliInputJSON(t, "edit", map[string]any{"command": "git status"})},
		{"cli empty command", copilotCliInputJSON(t, "bash", map[string]any{"command": ""})},
		{"cli no command field", copilotCliInputJSON(t, "bash", map[string]any{"description": "x"})},
		{"cli substitution", copilotCliInputJSON(t, "bash", map[string]any{"command": "git status `id`"})},
		{"cli toolArgs not a string", []byte(`{"toolName":"bash","toolArgs":{"command":"git status"}}`)},
		{"cli toolArgs invalid json string", []byte(`{"toolName":"bash","toolArgs":"not json {{"}`)},
		// Generic pass-throughs.
		{"malformed json", []byte("not valid json {{{")},
		{"empty input", []byte("")},
		{"whitespace input", []byte("   \n  ")},
		{"empty object", []byte("{}")},
		{"unknown shape", []byte(`{"foo":"bar"}`)},
		{"vscode no tool_input", []byte(`{"tool_name":"Bash"}`)},
		{"vscode tool_input not object", []byte(`{"tool_name":"Bash","tool_input":42}`)},
		{"vscode null tool_input", []byte(`{"tool_name":"Bash","tool_input":null}`)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, ok := CopilotHookResponse(c.payload, nil, gitCargo)
			if ok {
				t.Errorf("expected pass-through (no rewrite), got %q", out)
			}
			if out != "" {
				t.Errorf("pass-through must emit empty string, got %q", out)
			}
		})
	}
}

func TestCopilotBOMTolerated(t *testing.T) {
	bom := []byte{0xEF, 0xBB, 0xBF}
	// VS Code format with a leading BOM.
	in := append(bom, vsCodeInput(t, "runTerminalCommand", "git status", nil)...)
	out, ok := CopilotHookResponse(in, nil, gitCargo)
	if !ok {
		t.Fatalf("BOM-prefixed VS Code payload should still rewrite, got none")
	}
	if !strings.Contains(out, "gortk git status") {
		t.Errorf("BOM payload response missing rewrite: %s", out)
	}
}

func TestCopilotCliPrecedenceOverCli(t *testing.T) {
	// Faithful to rtk detect_format: when snake_case tool_name is present but
	// non-bash, the payload is pass-through even if it ALSO carries a camelCase
	// toolName — the snake_case branch wins and short-circuits.
	payload := []byte(`{"tool_name":"Read","tool_input":{"command":"git status"},"toolName":"bash","toolArgs":"{\"command\":\"git status\"}"}`)
	out, ok := CopilotHookResponse(payload, nil, gitCargo)
	if ok || out != "" {
		t.Errorf("non-bash snake_case tool must short-circuit to pass-through, got %q", out)
	}
}

func TestRunHookCopilotStreams(t *testing.T) {
	// End-to-end through the io.Reader/Writer plumbing for both formats.
	in := bytes.NewReader(vsCodeInput(t, "runTerminalCommand", "git status", nil))
	var out bytes.Buffer
	code, err := runHookCopilot(in, &out, gitCargo)
	if err != nil || code != 0 {
		t.Fatalf("runHookCopilot code=%d err=%v", code, err)
	}
	if !strings.Contains(out.String(), `"updatedInput"`) {
		t.Errorf("expected VS Code hook JSON on stdout, got %q", out.String())
	}

	in2 := bytes.NewReader(copilotCliInputJSON(t, "bash", map[string]any{"command": "git status"}))
	var out2 bytes.Buffer
	code, _ = runHookCopilot(in2, &out2, gitCargo)
	if code != 0 {
		t.Errorf("cli code = %d", code)
	}
	if !strings.Contains(out2.String(), `"modifiedArgs"`) {
		t.Errorf("expected Copilot CLI hook JSON on stdout, got %q", out2.String())
	}

	// Pass-through writes nothing.
	var out3 bytes.Buffer
	code, _ = runHookCopilot(bytes.NewReader(vsCodeInput(t, "Bash", "htop", nil)), &out3, gitCargo)
	if code != 0 || out3.Len() != 0 {
		t.Errorf("pass-through code=%d out=%q", code, out3.String())
	}
}

// ── Copilot installer (project-scoped ./.github) ──

func TestCopilotInstallWritesBothFiles(t *testing.T) {
	base := t.TempDir()
	if code, err := runCopilotInitAt(base, false, false, 0); err != nil || code != 0 {
		t.Fatalf("install code=%d err=%v", code, err)
	}

	// Hook config exists with exact expected content.
	hookPath := filepath.Join(base, ".github", "hooks", "gortk-rewrite.json")
	got, err := os.ReadFile(hookPath)
	if err != nil {
		t.Fatalf("hook config not written: %v", err)
	}
	if string(got) != copilotHookJSON {
		t.Errorf("hook config content mismatch:\n%q", string(got))
	}
	// It is valid JSON and carries both schema keys + the gortk command.
	var hookObj map[string]any
	if err := json.Unmarshal(got, &hookObj); err != nil {
		t.Fatalf("hook config is not valid JSON: %v", err)
	}
	hooks := hookObj["hooks"].(map[string]any)
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Error("hook config missing PreToolUse (VS Code) key")
	}
	if _, ok := hooks["preToolUse"]; !ok {
		t.Error("hook config missing preToolUse (Copilot CLI) key")
	}
	if !strings.Contains(string(got), "gortk hook copilot") {
		t.Error("hook config missing 'gortk hook copilot' command")
	}

	// Instructions file exists with the marked gortk block.
	instrPath := filepath.Join(base, ".github", "copilot-instructions.md")
	instr, err := os.ReadFile(instrPath)
	if err != nil {
		t.Fatalf("instructions not written: %v", err)
	}
	if !strings.Contains(string(instr), copilotBlockStart) || !strings.Contains(string(instr), copilotBlockEnd) {
		t.Errorf("instructions missing gortk markers:\n%s", string(instr))
	}
	if !strings.Contains(string(instr), "gortk — Token-Optimized CLI") {
		t.Errorf("instructions missing expected heading:\n%s", string(instr))
	}
}

func TestCopilotInstallIdempotentUpsert(t *testing.T) {
	base := t.TempDir()
	if code, err := runCopilotInitAt(base, false, false, 0); err != nil || code != 0 {
		t.Fatalf("first install code=%d err=%v", code, err)
	}
	instrPath := filepath.Join(base, ".github", "copilot-instructions.md")
	first, _ := os.ReadFile(instrPath)

	// Second install must be a no-op for the instructions (exactly one block).
	if code, err := runCopilotInitAt(base, false, false, 0); err != nil || code != 0 {
		t.Fatalf("second install code=%d err=%v", code, err)
	}
	second, _ := os.ReadFile(instrPath)
	if string(first) != string(second) {
		t.Errorf("instructions changed on re-install:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
	if n := strings.Count(string(second), copilotBlockStart); n != 1 {
		t.Errorf("expected exactly 1 gortk block after re-install, got %d", n)
	}
}

func TestCopilotInstallPreservesUserContent(t *testing.T) {
	base := t.TempDir()
	githubDir := filepath.Join(base, ".github")
	if err := os.MkdirAll(githubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	instrPath := filepath.Join(githubDir, "copilot-instructions.md")
	userContent := "# My project instructions\n\nUse tabs, not spaces.\n"
	if err := os.WriteFile(instrPath, []byte(userContent), 0o644); err != nil {
		t.Fatal(err)
	}

	if code, err := runCopilotInitAt(base, false, false, 0); err != nil || code != 0 {
		t.Fatalf("install code=%d err=%v", code, err)
	}

	got, _ := os.ReadFile(instrPath)
	if !strings.Contains(string(got), "My project instructions") {
		t.Errorf("user content dropped:\n%s", string(got))
	}
	if !strings.Contains(string(got), "Use tabs, not spaces.") {
		t.Errorf("user content dropped:\n%s", string(got))
	}
	if !strings.Contains(string(got), copilotBlockStart) {
		t.Errorf("gortk block not appended:\n%s", string(got))
	}
}

func TestCopilotInstallReplacesStaleBlock(t *testing.T) {
	base := t.TempDir()
	githubDir := filepath.Join(base, ".github")
	if err := os.MkdirAll(githubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	instrPath := filepath.Join(githubDir, "copilot-instructions.md")
	// A user file with an OLD gortk block plus surrounding content.
	stale := "# Header\n\n" + copilotBlockStart + "\nOLD STALE CONTENT\n" + copilotBlockEnd + "\n\n# Footer\n"
	if err := os.WriteFile(instrPath, []byte(stale), 0o644); err != nil {
		t.Fatal(err)
	}

	if code, err := runCopilotInitAt(base, false, false, 0); err != nil || code != 0 {
		t.Fatalf("install code=%d err=%v", code, err)
	}

	got, _ := os.ReadFile(instrPath)
	if strings.Contains(string(got), "OLD STALE CONTENT") {
		t.Errorf("stale block content not replaced:\n%s", string(got))
	}
	if !strings.Contains(string(got), "gortk — Token-Optimized CLI") {
		t.Errorf("fresh block content missing:\n%s", string(got))
	}
	// Surrounding user content preserved; still exactly one block.
	if !strings.Contains(string(got), "# Header") || !strings.Contains(string(got), "# Footer") {
		t.Errorf("surrounding user content not preserved:\n%s", string(got))
	}
	if n := strings.Count(string(got), copilotBlockStart); n != 1 {
		t.Errorf("expected exactly 1 gortk block, got %d", n)
	}
}

func TestCopilotInstallRefusesMalformedBlock(t *testing.T) {
	base := t.TempDir()
	githubDir := filepath.Join(base, ".github")
	if err := os.MkdirAll(githubDir, 0o755); err != nil {
		t.Fatal(err)
	}
	instrPath := filepath.Join(githubDir, "copilot-instructions.md")
	// Opening marker without a closing marker — must refuse.
	malformed := copilotBlockStart + "\nsome content with no end marker\n"
	if err := os.WriteFile(instrPath, []byte(malformed), 0o644); err != nil {
		t.Fatal(err)
	}

	code, err := runCopilotInitAt(base, false, false, 0)
	if err == nil || code == 0 {
		t.Fatalf("install should refuse malformed instructions (code=%d err=%v)", code, err)
	}
	// The malformed file must be untouched, and no hook config written.
	got, _ := os.ReadFile(instrPath)
	if string(got) != malformed {
		t.Errorf("malformed instructions were modified: %q", string(got))
	}
	if _, err := os.Stat(filepath.Join(githubDir, "hooks", "gortk-rewrite.json")); !os.IsNotExist(err) {
		t.Errorf("hook config should not be written when instructions are malformed (err=%v)", err)
	}
}

func TestCopilotInstallDryRunWritesNothing(t *testing.T) {
	base := t.TempDir()
	if code, err := runCopilotInitAt(base, false, true, 0); err != nil || code != 0 {
		t.Fatalf("dry-run code=%d err=%v", code, err)
	}
	if _, err := os.Stat(filepath.Join(base, ".github")); !os.IsNotExist(err) {
		t.Errorf("dry-run created ./.github (err=%v)", err)
	}
}

func TestCopilotInstallShowWritesNothing(t *testing.T) {
	base := t.TempDir()
	if code, err := runCopilotInitAt(base, true, false, 0); err != nil || code != 0 {
		t.Fatalf("--show code=%d err=%v", code, err)
	}
	if _, err := os.Stat(filepath.Join(base, ".github")); !os.IsNotExist(err) {
		t.Errorf("--show created ./.github (err=%v)", err)
	}
}

// ── RunInit routing: --copilot does not touch ~/.claude; no flag does ──

func TestRunInitCopilotRoutesToProjectScope(t *testing.T) {
	// Run `gortk init --copilot --dry-run` from a temp working directory and
	// confirm it does not error and writes nothing (dry-run).
	base := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	if err := os.Chdir(base); err != nil {
		t.Fatal(err)
	}
	code, err := RunInit([]string{"--copilot", "--dry-run"}, 0)
	if err != nil || code != 0 {
		t.Fatalf("RunInit --copilot --dry-run code=%d err=%v", code, err)
	}
	if _, err := os.Stat(filepath.Join(base, ".github")); !os.IsNotExist(err) {
		t.Errorf("dry-run via RunInit created ./.github (err=%v)", err)
	}
}
