package hooks

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSupported builds a supportedFunc whose verdict comes from a fixed set of
// supported base commands, mirroring how production gortkSupports asks the
// registry/tomlfilter — but hermetic, so these tests do not depend on which
// command packages happen to be compiled into the test binary.
func fakeSupported(bases ...string) supportedFunc {
	set := map[string]bool{}
	for _, b := range bases {
		set[b] = true
	}
	return func(cmd string) bool {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			return false
		}
		return set[baseName(firstToken(cmd))]
	}
}

// gitCargo is the common predicate: gortk can optimize git and cargo.
var gitCargo = fakeSupported("git", "cargo", "go", "ls", "grep", "find", "curl")

func mustRewrite(t *testing.T, cmd string) string {
	t.Helper()
	out, ok := Rewrite(cmd, nil, gitCargo)
	if !ok {
		t.Fatalf("Rewrite(%q): expected a rewrite, got none", cmd)
	}
	return out
}

func mustNotRewrite(t *testing.T, cmd string, excluded ...string) {
	t.Helper()
	out, ok := Rewrite(cmd, excluded, gitCargo)
	if ok {
		t.Fatalf("Rewrite(%q): expected NO rewrite, got %q", cmd, out)
	}
}

// ── rewrite decision: registry / tomlfilter hits, unsupported, excluded ──

func TestRewriteRegistryHit(t *testing.T) {
	// git is "registered" (fake): a registry hit rewrites to `gortk git status`.
	if got := mustRewrite(t, "git status"); got != "gortk git status" {
		t.Errorf("git status => %q, want %q", got, "gortk git status")
	}
}

func TestRewriteTomlFilterHit(t *testing.T) {
	// Exercise the real predicate against the real tomlfilter engine: pick a
	// command that no command package registers but a builtin filter matches.
	// We discover one dynamically so the test never goes stale if filters change.
	base := firstTomlOnlyBase()
	if base == "" {
		t.Skip("no tomlfilter-only base command available in this build")
	}
	cmd := base // bare command line; FindMatching keys on the basename-normalized line
	out, ok := Rewrite(cmd, nil, gortkSupports)
	if !ok {
		t.Fatalf("Rewrite(%q) via real predicate: expected rewrite (tomlfilter hit), got none", cmd)
	}
	if want := "gortk " + cmd; out != want {
		t.Errorf("tomlfilter hit %q => %q, want %q", cmd, out, want)
	}
}

func TestRewriteUnsupportedExitsWithoutOutput(t *testing.T) {
	// htop is not registered and matches no filter → no rewrite (caller exits 1).
	mustNotRewrite(t, "htop -d 10")
}

func TestRewriteExcludedCommand(t *testing.T) {
	// git is supported, but excluding "git" passes it through untouched.
	mustNotRewrite(t, "git status", "git")
	// Exclusion is word-boundary: "gi" must NOT exclude "git status".
	if _, ok := Rewrite("git status", []string{"gi"}, gitCargo); !ok {
		t.Errorf(`exclude "gi" should not match "git status"`)
	}
	// A leading '^' anchor (rtk style) is tolerated.
	mustNotRewrite(t, "git status", "^git")
}

func TestRewriteEmpty(t *testing.T) {
	mustNotRewrite(t, "")
	mustNotRewrite(t, "   ")
}

// ── already-gortk passthrough ──

func TestRewriteAlreadyGortkSimple(t *testing.T) {
	// A bare already-gortk command rewrites to itself (ported from rtk
	// test_run_already_rtk_returns_some).
	if got := mustRewrite(t, "gortk git status"); got != "gortk git status" {
		t.Errorf("already-gortk => %q, want unchanged", got)
	}
}

func TestRewriteMixedCompoundPartial(t *testing.T) {
	// First segment already gortk, second gets rewritten.
	// (rtk test_rewrite_mixed_compound_partial)
	got := mustRewrite(t, "gortk git add . && cargo test")
	if got != "gortk git add . && gortk cargo test" {
		t.Errorf("mixed compound => %q", got)
	}
}

// ── env-prefix preservation ──

func TestRewriteEnvPrefixPreserved(t *testing.T) {
	got := mustRewrite(t, "GIT_PAGER=cat git status")
	if got != "GIT_PAGER=cat gortk git status" {
		t.Errorf("env prefix => %q", got)
	}
}

func TestRewriteEnvQuotedValue(t *testing.T) {
	in := `GIT_SSH_COMMAND="ssh -o StrictHostKeyChecking=no" git push`
	want := `GIT_SSH_COMMAND="ssh -o StrictHostKeyChecking=no" gortk git push`
	if got := mustRewrite(t, in); got != want {
		t.Errorf("quoted env => %q, want %q", got, want)
	}
}

func TestRewriteSudoStripped(t *testing.T) {
	// sudo is a recognized env-wrapper prefix; the inner command is rewritten.
	got := mustRewrite(t, "sudo git status")
	if got != "sudo gortk git status" {
		t.Errorf("sudo prefix => %q", got)
	}
}

// ── compound handling ──

func TestRewriteCompoundAnd(t *testing.T) {
	got := mustRewrite(t, "git add . && cargo test")
	if got != "gortk git add . && gortk cargo test" {
		t.Errorf("&& compound => %q", got)
	}
}

func TestRewriteCompoundThreeSegments(t *testing.T) {
	got := mustRewrite(t, "cargo build --all && cargo build && cargo test")
	want := "gortk cargo build --all && gortk cargo build && gortk cargo test"
	if got != want {
		t.Errorf("three segments => %q, want %q", got, want)
	}
}

func TestRewriteCompoundSemicolon(t *testing.T) {
	got := mustRewrite(t, "git add . ; cargo test")
	if got != "gortk git add .; gortk cargo test" {
		t.Errorf("; compound => %q", got)
	}
}

func TestRewriteCompoundOr(t *testing.T) {
	got := mustRewrite(t, "git status || cargo test")
	if got != "gortk git status || gortk cargo test" {
		t.Errorf("|| compound => %q", got)
	}
}

func TestRewriteBackgroundSingleAmp(t *testing.T) {
	got := mustRewrite(t, "cargo test & git status")
	if got != "gortk cargo test & gortk git status" {
		t.Errorf("background & => %q", got)
	}
}

func TestRewriteBackgroundUnsupportedRight(t *testing.T) {
	// Right side unsupported → left rewritten, right passed through.
	got := mustRewrite(t, "cargo test & htop")
	if got != "gortk cargo test & htop" {
		t.Errorf("background & unsupported right => %q", got)
	}
}

func TestRewritePipeFirstOnly(t *testing.T) {
	// After a pipe the consumer stays raw.
	got := mustRewrite(t, "git log -10 | grep feat")
	if got != "gortk git log -10 | grep feat" {
		t.Errorf("pipe => %q", got)
	}
}

func TestRewriteFindPipeSkipped(t *testing.T) {
	// find in a pipe must NOT be rewritten (output format breaks xargs).
	mustNotRewrite(t, "find . -name '*.rs' | xargs grep fn")
}

func TestRewriteFindNoPipeSkipped(t *testing.T) {
	// gortk treats find/fd as pipe-incompatible producers and never rewrites
	// them, even without a pipe — matching the conservative segment rule.
	mustNotRewrite(t, "find . -name '*.rs'")
}

func TestRewriteCompoundUnsupportedFirst(t *testing.T) {
	// Unsupported first segment passes through; supported second rewrites.
	got := mustRewrite(t, "htop && cargo test")
	if got != "htop && gortk cargo test" {
		t.Errorf("unsupported-first compound => %q", got)
	}
}

func TestRewriteCompoundAllUnsupported(t *testing.T) {
	// Nothing changes → no rewrite at all.
	mustNotRewrite(t, "htop && less foo")
}

// ── trailing fd-dup redirects ride along ──

func TestRewriteRedirectFdDup(t *testing.T) {
	got := mustRewrite(t, "git status 2>&1")
	if got != "gortk git status 2>&1" {
		t.Errorf("2>&1 => %q", got)
	}
}

func TestRewriteRedirectFdDupWithAnd(t *testing.T) {
	got := mustRewrite(t, "cargo test 2>&1 && git status")
	if got != "gortk cargo test 2>&1 && gortk git status" {
		t.Errorf("2>&1 + && => %q", got)
	}
}

func TestRewriteRedirectDevNull(t *testing.T) {
	got := mustRewrite(t, "git status 2>/dev/null")
	if got != "gortk git status 2>/dev/null" {
		t.Errorf("2>/dev/null => %q", got)
	}
}

// ── unattestable constructs pass through (security) ──

func TestRewriteSubstitutionPassthrough(t *testing.T) {
	// Command/process substitution must never be rewritten (ported from rtk
	// unattestable_passthrough tests).
	for _, cmd := range []string{
		"git status `rm -rf /tmp/x`",
		"git status $(rm -rf /tmp/x)",
		`git log --pretty="$(rm -rf /tmp/x)"`,
		"git diff <(cat secrets)",
	} {
		mustNotRewrite(t, cmd)
	}
}

func TestRewriteFileRedirectPassthrough(t *testing.T) {
	// A real file-target redirect makes the command unattestable → pass through.
	mustNotRewrite(t, "git log > /tmp/out.txt")
}

func TestRewriteHeredocPassthrough(t *testing.T) {
	mustNotRewrite(t, "cat <<'EOF'\nfoo\nEOF")
	mustNotRewrite(t, "git diff <<EOF\nhi\nEOF")
}

func TestRewriteArithmeticPassthrough(t *testing.T) {
	mustNotRewrite(t, "git log $((1+1))")
}

// ── line continuations ──

func TestRewriteLineContinuation(t *testing.T) {
	got := mustRewrite(t, "git \\\n status")
	if got != "gortk git status" {
		t.Errorf("line continuation => %q", got)
	}
}

// ── basename normalization ──

func TestRewriteAbsolutePathBasename(t *testing.T) {
	// A registry/filter hit is decided on the basename; the rewrite keeps the
	// original segment text (gortk re-resolves the tool PATHEXT-aware).
	got := mustRewrite(t, "/usr/bin/git status")
	if got != "gortk /usr/bin/git status" {
		t.Errorf("absolute-path basename => %q", got)
	}
}

func TestBaseName(t *testing.T) {
	cases := map[string]string{
		"ls":              "ls",
		"/usr/bin/ls":     "ls",
		`C:\tools\ls.exe`: "ls",
		"git.cmd":         "git",
		"make":            "make",
		"":                "",
	}
	for in, want := range cases {
		if got := baseName(in); got != want {
			t.Errorf("baseName(%q) = %q, want %q", in, got, want)
		}
	}
}

// firstTomlOnlyBase returns a command base that the real gortkSupports accepts
// only via tomlfilter (not the registry), or "" if none exists in this build.
func firstTomlOnlyBase() string {
	// Probe a handful of common tool names that rtk ships TOML filters for but
	// has no dedicated Go command module: make/jq/helm/ping. Any that the real
	// predicate accepts (and that is NOT a registered command) qualifies.
	for _, base := range []string{"make", "jq", "helm", "ping", "liquibase"} {
		if gortkSupports(base) {
			return base
		}
	}
	return ""
}

// ── Claude hook JSON in/out ──

func claudeInput(t *testing.T, tool, cmd string, extra map[string]any) []byte {
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

func TestClaudeHookHappyPath(t *testing.T) {
	in := claudeInput(t, "Bash", "git status", nil)
	out, ok := ClaudeHookResponse(in, nil, gitCargo)
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
	if hook["permissionDecisionReason"] != "gortk auto-rewrite" {
		t.Errorf("permissionDecisionReason = %v", hook["permissionDecisionReason"])
	}
	// Security: gortk never auto-allows — no permissionDecision key.
	if _, present := hook["permissionDecision"]; present {
		t.Errorf("gortk must not set permissionDecision: %s", out)
	}
	updated, _ := hook["updatedInput"].(map[string]any)
	if updated == nil || updated["command"] != "gortk git status" {
		t.Errorf("updatedInput.command = %v, want %q", updated["command"], "gortk git status")
	}
}

func TestClaudeHookPreservesToolInputFields(t *testing.T) {
	// Extra fields on tool_input (timeout, description) must survive untouched.
	in := claudeInput(t, "Bash", "git status", map[string]any{
		"timeout":     float64(30000),
		"description": "Check repo status",
	})
	out, ok := ClaudeHookResponse(in, nil, gitCargo)
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

func TestClaudeHookCompoundCommand(t *testing.T) {
	in := claudeInput(t, "Bash", "git add . && cargo test", nil)
	out, ok := ClaudeHookResponse(in, nil, gitCargo)
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

func TestClaudeHookEnvPrefix(t *testing.T) {
	in := claudeInput(t, "Bash", "GIT_PAGER=cat git status", nil)
	out, ok := ClaudeHookResponse(in, nil, gitCargo)
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

// ── Claude hook pass-through cases (must emit nothing, never crash) ──

func TestClaudeHookPassthroughCases(t *testing.T) {
	cases := []struct {
		name    string
		payload []byte
	}{
		{"unsupported command", claudeInput(t, "Bash", "htop", nil)},
		{"already gortk", claudeInput(t, "Bash", "gortk git status", nil)},
		{"non-bash tool", claudeInput(t, "Read", "git status", nil)},
		{"empty command", claudeInput(t, "Bash", "", nil)},
		{"substitution", claudeInput(t, "Bash", "git status $(rm -rf /tmp/x)", nil)},
		{"backtick", claudeInput(t, "Bash", "git status `rm -rf /tmp/x`", nil)},
		{"file redirect", claudeInput(t, "Bash", "git log > /tmp/out.txt", nil)},
		{"heredoc", claudeInput(t, "Bash", "cat <<EOF\nhi\nEOF", nil)},
		{"malformed json", []byte("not valid json {{{")},
		{"empty input", []byte("")},
		{"whitespace input", []byte("   \n  ")},
		{"empty object", []byte("{}")},
		{"no tool_input", []byte(`{"tool_name":"Bash"}`)},
		{"tool_input not object", []byte(`{"tool_name":"Bash","tool_input":42}`)},
		{"null tool_input", []byte(`{"tool_name":"Bash","tool_input":null}`)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, ok := ClaudeHookResponse(c.payload, nil, gitCargo)
			if ok {
				t.Errorf("expected pass-through (no rewrite), got %q", out)
			}
			if out != "" {
				t.Errorf("pass-through must emit empty string, got %q", out)
			}
		})
	}
}

func TestClaudeHookBOMTolerated(t *testing.T) {
	// A leading UTF-8 BOM (some Windows hosts prepend it) must not break parsing.
	bom := []byte{0xEF, 0xBB, 0xBF}
	in := append(bom, claudeInput(t, "Bash", "git status", nil)...)
	out, ok := ClaudeHookResponse(in, nil, gitCargo)
	if !ok {
		t.Fatalf("BOM-prefixed payload should still rewrite, got none")
	}
	if !strings.Contains(out, "gortk git status") {
		t.Errorf("BOM payload response missing rewrite: %s", out)
	}
}

func TestRunHookClaudeStreams(t *testing.T) {
	// End-to-end through the io.Reader/Writer plumbing: a valid payload yields a
	// JSON line; an unsupported payload yields nothing.
	in := bytes.NewReader(claudeInput(t, "Bash", "git status", nil))
	var out bytes.Buffer
	code, err := runHookClaude(in, &out, gitCargo)
	if err != nil || code != 0 {
		t.Fatalf("runHookClaude code=%d err=%v", code, err)
	}
	if !strings.Contains(out.String(), `"updatedInput"`) {
		t.Errorf("expected hook JSON on stdout, got %q", out.String())
	}

	var out2 bytes.Buffer
	code, _ = runHookClaude(bytes.NewReader(claudeInput(t, "Bash", "htop", nil)), &out2, gitCargo)
	if code != 0 {
		t.Errorf("pass-through code = %d", code)
	}
	if out2.Len() != 0 {
		t.Errorf("pass-through wrote to stdout: %q", out2.String())
	}
}

// ── settings.json patch idempotency ──

func TestPatchSettingsIdempotent(t *testing.T) {
	const hookCmd = `cmd /c "C:/Users/x/.claude/hooks/gortk-hook.cmd"`

	// Start empty.
	root := map[string]any{}
	root, patched := patchSettings(root, hookCmd)
	if !patched {
		t.Fatal("first patch should add the hook")
	}
	if !hookAlreadyPresent(root, hookCmd) {
		t.Fatal("hook should be present after first patch")
	}

	// Second patch is a no-op.
	root, patched = patchSettings(root, hookCmd)
	if patched {
		t.Fatal("second patch should be a no-op (idempotent)")
	}

	// Exactly one PreToolUse entry exists.
	entries := preToolUseEntries(root)
	if len(entries) != 1 {
		t.Fatalf("expected exactly 1 PreToolUse entry, got %d", len(entries))
	}
}

func TestPatchSettingsPreservesExistingKeys(t *testing.T) {
	const hookCmd = `cmd /c "C:/h/gortk-hook.cmd"`
	root := map[string]any{
		"model": "opus",
		"permissions": map[string]any{
			"allow": []any{"Bash(ls:*)"},
		},
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{"type": "command", "command": "some-other-hook"},
					},
				},
			},
		},
	}
	root, patched := patchSettings(root, hookCmd)
	if !patched {
		t.Fatal("expected patch to add gortk hook alongside the existing one")
	}
	if root["model"] != "opus" {
		t.Errorf("top-level key 'model' not preserved: %v", root["model"])
	}
	if _, ok := root["permissions"]; !ok {
		t.Errorf("'permissions' key dropped")
	}
	// Both the pre-existing hook and the new gortk hook should be present.
	entries := preToolUseEntries(root)
	if len(entries) != 2 {
		t.Fatalf("expected 2 PreToolUse entries (existing + gortk), got %d", len(entries))
	}
	if !hookAlreadyPresent(root, hookCmd) {
		t.Errorf("gortk hook missing after patch")
	}
}

func TestPatchSettingsDetectsByCanonicalMarker(t *testing.T) {
	// A manually-written entry that uses the canonical marker (different exact
	// command) still counts as present — no duplicate added.
	root := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{
				map[string]any{
					"matcher": "Bash",
					"hooks": []any{
						map[string]any{"type": "command", "command": "gortk hook claude"},
					},
				},
			},
		},
	}
	_, patched := patchSettings(root, `cmd /c "C:/other/path.cmd"`)
	if patched {
		t.Fatal("canonical-marker entry should be detected as already present")
	}
}

// ── init installer round-trip in t.TempDir() ──

func TestInitInstallAndIdempotent(t *testing.T) {
	home := t.TempDir()
	plan, err := buildInstallPlan(home)
	if err != nil {
		t.Fatal(err)
	}

	// First install writes launcher + settings.
	if code, err := applyInstall(plan, 0); err != nil || code != 0 {
		t.Fatalf("first install code=%d err=%v", code, err)
	}

	// Launcher exists with expected content.
	got, err := os.ReadFile(filepath.Join(home, ".claude", "hooks", "gortk-hook.cmd"))
	if err != nil {
		t.Fatalf("launcher not written: %v", err)
	}
	if string(got) != launcherScript {
		t.Errorf("launcher content mismatch:\n%q", string(got))
	}

	// settings.json is valid and contains the hook.
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json not written: %v", err)
	}
	root, err := parseSettings(data)
	if err != nil {
		t.Fatalf("settings.json invalid: %v", err)
	}
	if !hookAlreadyPresent(root, plan.hookCommand) {
		t.Fatal("hook not present after install")
	}

	// Second install is idempotent: still exactly one entry, no error.
	plan2, _ := buildInstallPlan(home)
	if !plan2.hookPresent {
		t.Error("buildInstallPlan should report hookPresent after first install")
	}
	if code, err := applyInstall(plan2, 0); err != nil || code != 0 {
		t.Fatalf("second install code=%d err=%v", code, err)
	}
	data2, _ := os.ReadFile(settingsPath)
	root2, _ := parseSettings(data2)
	if n := len(preToolUseEntries(root2)); n != 1 {
		t.Fatalf("expected 1 PreToolUse entry after re-install, got %d", n)
	}
}

func TestInitPreservesExistingSettingsFile(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-existing settings with a custom key and an unrelated hook.
	existing := `{
  "model": "sonnet",
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "keep-me"}]}
    ]
  }
}`
	settingsPath := filepath.Join(claudeDir, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	plan, _ := buildInstallPlan(home)
	if code, err := applyInstall(plan, 0); err != nil || code != 0 {
		t.Fatalf("install code=%d err=%v", code, err)
	}

	data, _ := os.ReadFile(settingsPath)
	root, err := parseSettings(data)
	if err != nil {
		t.Fatalf("settings.json invalid after patch: %v", err)
	}
	if root["model"] != "sonnet" {
		t.Errorf("custom key not preserved: %v", root["model"])
	}
	entries := preToolUseEntries(root)
	if len(entries) != 2 {
		t.Fatalf("expected 2 PreToolUse entries (keep-me + gortk), got %d", len(entries))
	}
	// A .bak backup of the original was written.
	if _, err := os.Stat(settingsPath + ".bak"); err != nil {
		t.Errorf("expected settings.json.bak backup, got %v", err)
	}
}

func TestInitRefusesUnparseableSettings(t *testing.T) {
	home := t.TempDir()
	claudeDir := filepath.Join(home, ".claude")
	_ = os.MkdirAll(claudeDir, 0o755)
	settingsPath := filepath.Join(claudeDir, "settings.json")
	_ = os.WriteFile(settingsPath, []byte("{ this is not json"), 0o644)

	plan, _ := buildInstallPlan(home)
	if plan.settingsErr == "" {
		t.Fatal("buildInstallPlan should record a parse error")
	}
	code, err := applyInstall(plan, 0)
	if err == nil || code == 0 {
		t.Fatalf("install should refuse to patch invalid settings.json (code=%d err=%v)", code, err)
	}
	// Original file must be untouched.
	data, _ := os.ReadFile(settingsPath)
	if string(data) != "{ this is not json" {
		t.Errorf("invalid settings.json was modified: %q", string(data))
	}
}

func TestInitDryRunWritesNothing(t *testing.T) {
	home := t.TempDir()
	plan, _ := buildInstallPlan(home)
	// --dry-run path: printInitDryRun must not write files.
	printInitDryRun(plan)
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Errorf("dry-run created ~/.claude (err=%v)", err)
	}
}
