package hooks

import (
	"encoding/json"
	"strings"
	"testing"
)

// ── Gemini hook: BeforeTool JSON in/out ──

// geminiInput builds a Gemini BeforeTool payload (snake_case tool_name +
// tool_input.command), with extra top-level fields riding along to confirm they
// are ignored.
func geminiInput(t *testing.T, tool, cmd string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"tool_name":  tool,
		"tool_input": map[string]any{"command": cmd},
		"session_id": "g-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGeminiHookHappyPath(t *testing.T) {
	in := geminiInput(t, "run_shell_command", "git status")
	out := GeminiHookResponse(in, nil, gitCargo)

	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, out)
	}
	if v["decision"] != "allow" {
		t.Errorf("decision = %v, want \"allow\"", v["decision"])
	}
	hook, _ := v["hookSpecificOutput"].(map[string]any)
	if hook == nil {
		t.Fatalf("missing hookSpecificOutput: %s", out)
	}
	ti, _ := hook["tool_input"].(map[string]any)
	if ti == nil || ti["command"] != "gortk git status" {
		t.Errorf("hookSpecificOutput.tool_input.command = %v, want %q", ti["command"], "gortk git status")
	}
	// Gemini's rewrite envelope must NOT carry the Claude-style updatedInput.
	if _, ok := hook["updatedInput"]; ok {
		t.Errorf("unexpected updatedInput key in Gemini response: %s", out)
	}
}

func TestGeminiHookPassThroughUnsupported(t *testing.T) {
	// An unsupported command yields the bare allow (Gemini proceeds natively).
	in := geminiInput(t, "run_shell_command", "htop")
	out := strings.TrimSpace(GeminiHookResponse(in, nil, gitCargo))
	if out != geminiAllowJSON {
		t.Errorf("unsupported command: out = %q, want %q", out, geminiAllowJSON)
	}
}

func TestGeminiHookPassThroughNonShellTool(t *testing.T) {
	// Non-shell tool → bare allow, never a rewrite.
	in := geminiInput(t, "edit_file", "git status")
	out := strings.TrimSpace(GeminiHookResponse(in, nil, gitCargo))
	if out != geminiAllowJSON {
		t.Errorf("non-shell tool: out = %q, want %q", out, geminiAllowJSON)
	}
}

func TestGeminiHookPassThroughAlreadyGortk(t *testing.T) {
	in := geminiInput(t, "run_shell_command", "gortk git status")
	out := strings.TrimSpace(GeminiHookResponse(in, nil, gitCargo))
	if out != geminiAllowJSON {
		t.Errorf("already gortk: out = %q, want %q", out, geminiAllowJSON)
	}
}

func TestGeminiHookPassThroughEmptyCommand(t *testing.T) {
	in := geminiInput(t, "run_shell_command", "")
	out := strings.TrimSpace(GeminiHookResponse(in, nil, gitCargo))
	if out != geminiAllowJSON {
		t.Errorf("empty command: out = %q, want %q", out, geminiAllowJSON)
	}
}

func TestGeminiHookPassThroughMalformedAndEmpty(t *testing.T) {
	for _, payload := range []string{"", "   ", "not json {{{"} {
		out := strings.TrimSpace(GeminiHookResponse([]byte(payload), nil, gitCargo))
		if out != geminiAllowJSON {
			t.Errorf("payload %q: out = %q, want %q", payload, out, geminiAllowJSON)
		}
	}
}

func TestGeminiHookPassThroughUnattestable(t *testing.T) {
	// A command-substitution payload is unattestable → bare allow, no rewrite.
	in := geminiInput(t, "run_shell_command", "git status $(rm -rf /tmp/x)")
	out := strings.TrimSpace(GeminiHookResponse(in, nil, gitCargo))
	if out != geminiAllowJSON {
		t.Errorf("substitution: out = %q, want %q", out, geminiAllowJSON)
	}
}

func TestGeminiHookStripsBOM(t *testing.T) {
	// A leading UTF-8 BOM must not break parsing of a rewritable payload.
	in := append([]byte{0xEF, 0xBB, 0xBF}, geminiInput(t, "run_shell_command", "git status")...)
	out := GeminiHookResponse(in, nil, gitCargo)
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("BOM-prefixed payload did not parse: %v\n%s", err, out)
	}
	if v["decision"] != "allow" || v["hookSpecificOutput"] == nil {
		t.Errorf("BOM payload not rewritten: %s", out)
	}
}

func TestGeminiHookCompoundRewrite(t *testing.T) {
	in := geminiInput(t, "run_shell_command", "git add . && cargo test")
	out := GeminiHookResponse(in, nil, gitCargo)
	var v map[string]any
	_ = json.Unmarshal([]byte(out), &v)
	hook := v["hookSpecificOutput"].(map[string]any)
	ti := hook["tool_input"].(map[string]any)
	if ti["command"] != "gortk git add . && gortk cargo test" {
		t.Errorf("compound command = %v, want %q", ti["command"], "gortk git add . && gortk cargo test")
	}
}

func TestGeminiHookExcludedCommandPassesThrough(t *testing.T) {
	// An excluded command is left untouched → bare allow.
	in := geminiInput(t, "run_shell_command", "curl https://example.com")
	out := strings.TrimSpace(GeminiHookResponse(in, []string{"curl"}, gitCargo))
	if out != geminiAllowJSON {
		t.Errorf("excluded curl: out = %q, want %q", out, geminiAllowJSON)
	}
}
