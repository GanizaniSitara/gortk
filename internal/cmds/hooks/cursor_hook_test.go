package hooks

import (
	"encoding/json"
	"strings"
	"testing"
)

// ── Cursor hook: preToolUse JSON in/out ──

// cursorInput builds a Cursor preToolUse payload (VS Code snake_case shape:
// tool_name + tool_input.command), with extra tool_input fields riding along.
func cursorInput(t *testing.T, cmd string, extra map[string]any) []byte {
	t.Helper()
	ti := map[string]any{"command": cmd}
	for k, v := range extra {
		ti[k] = v
	}
	b, err := json.Marshal(map[string]any{
		"tool_name":  "Bash",
		"tool_input": ti,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCursorHookHappyPath(t *testing.T) {
	in := cursorInput(t, "git status", nil)
	out := CursorHookResponse(in, nil, gitCargo)

	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, out)
	}
	// Cursor's flat permission/updated_input shape (NOT hookSpecificOutput).
	if v["permission"] != "allow" {
		t.Errorf("permission = %v, want \"allow\"", v["permission"])
	}
	if v["continue"] != true {
		t.Errorf("continue = %v, want true (panel collapses to {} without it)", v["continue"])
	}
	updated, _ := v["updated_input"].(map[string]any)
	if updated == nil || updated["command"] != "gortk git status" {
		t.Errorf("updated_input.command = %v, want %q", updated["command"], "gortk git status")
	}
	if _, ok := v["hookSpecificOutput"]; ok {
		t.Errorf("Cursor response must not carry hookSpecificOutput: %s", out)
	}
}

func TestCursorHookPassThroughUnsupported(t *testing.T) {
	in := cursorInput(t, "htop", nil)
	out := strings.TrimSpace(CursorHookResponse(in, nil, gitCargo))
	if out != cursorEmptyJSON {
		t.Errorf("unsupported command: out = %q, want %q", out, cursorEmptyJSON)
	}
}

func TestCursorHookPassThroughAlreadyGortk(t *testing.T) {
	in := cursorInput(t, "gortk git status", nil)
	out := strings.TrimSpace(CursorHookResponse(in, nil, gitCargo))
	if out != cursorEmptyJSON {
		t.Errorf("already gortk: out = %q, want %q", out, cursorEmptyJSON)
	}
}

func TestCursorHookPassThroughEmptyAndMalformed(t *testing.T) {
	for _, payload := range []string{"", "   ", "not json {{{"} {
		out := strings.TrimSpace(CursorHookResponse([]byte(payload), nil, gitCargo))
		if out != cursorEmptyJSON {
			t.Errorf("payload %q: out = %q, want %q", payload, out, cursorEmptyJSON)
		}
	}
}

func TestCursorHookPassThroughEmptyCommand(t *testing.T) {
	in := cursorInput(t, "", nil)
	out := strings.TrimSpace(CursorHookResponse(in, nil, gitCargo))
	if out != cursorEmptyJSON {
		t.Errorf("empty command: out = %q, want %q", out, cursorEmptyJSON)
	}
}

func TestCursorHookPassThroughUnattestable(t *testing.T) {
	for _, cmd := range []string{
		"git status `rm -rf /tmp/x`",
		"git status $(rm -rf /tmp/x)",
		"git log > /tmp/out.txt",
		"cat <<EOF\nhello\nEOF",
	} {
		in := cursorInput(t, cmd, nil)
		out := strings.TrimSpace(CursorHookResponse(in, nil, gitCargo))
		if out != cursorEmptyJSON {
			t.Errorf("unattestable %q: out = %q, want %q", cmd, out, cursorEmptyJSON)
		}
	}
}

func TestCursorHookStripsSingleBOM(t *testing.T) {
	in := append([]byte{0xEF, 0xBB, 0xBF}, cursorInput(t, "git status", nil)...)
	out := CursorHookResponse(in, nil, gitCargo)
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("single-BOM payload did not parse: %v\n%s", err, out)
	}
	if v["permission"] != "allow" || v["continue"] != true {
		t.Errorf("single-BOM payload not rewritten: %s", out)
	}
}

func TestCursorHookStripsDoubleBOM(t *testing.T) {
	// Cursor on Windows ships hook stdin with TWO leading UTF-8 BOMs.
	bom := []byte{0xEF, 0xBB, 0xBF}
	in := append(append(append([]byte{}, bom...), bom...), cursorInput(t, "git status", nil)...)
	out := CursorHookResponse(in, nil, gitCargo)
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		t.Fatalf("double-BOM payload did not parse: %v\n%s", err, out)
	}
	updated := v["updated_input"].(map[string]any)
	if updated["command"] != "gortk git status" {
		t.Errorf("double-BOM command = %v, want %q", updated["command"], "gortk git status")
	}
}

func TestCursorHookCompoundIncludesContinue(t *testing.T) {
	// fd-dup redirect rides along; the compound rewrite still carries continue:true.
	in := cursorInput(t, "git status 2>&1", nil)
	out := CursorHookResponse(in, nil, gitCargo)
	var v map[string]any
	_ = json.Unmarshal([]byte(out), &v)
	if v["continue"] != true || v["permission"] != "allow" {
		t.Fatalf("fd-dup rewrite missing continue/permission: %s", out)
	}
	updated := v["updated_input"].(map[string]any)
	if updated["command"] != "gortk git status 2>&1" {
		t.Errorf("fd-dup command = %v, want %q", updated["command"], "gortk git status 2>&1")
	}
}
