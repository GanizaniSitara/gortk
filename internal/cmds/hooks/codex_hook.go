// codex_hook.go implements `gortk hook codex`: the OpenAI Codex CLI PreToolUse
// hook processor. It is a native-Windows, offline addition shaped like the
// Claude hook path (hooks.go: ClaudeHookResponse / runHookClaude) and the
// Copilot hook path (copilot_hook.go), reusing the SAME Rewrite engine and the
// shared BOM/parse/command helpers.
//
// Codex (codex-cli 0.141.0) feeds a PreToolUse hook a JSON object on stdin:
//
//	{ "hook_event_name":"PreToolUse", "tool_name":"Bash",
//	  "tool_input":{"command":"..."}, "cwd":"...", "session_id":"...", ... }
//
// Both the Bash and apply_patch tools carry their payload at tool_input.command;
// we only handle tool_name == "Bash" (snake_case, the shell-command tool).
//
// Unlike the Claude/Copilot paths, Codex requires updatedInput to be paired with
// an explicit permissionDecision of "allow" — Codex will not apply a rewritten
// command otherwise. On a real rewrite we therefore emit:
//
//	{"hookSpecificOutput":{"hookEventName":"PreToolUse",
//	  "permissionDecision":"allow","permissionDecisionReason":"gortk auto-rewrite",
//	  "updatedInput":{"command":"<rewritten>"}}}
//
// This means a gortk rewrite is auto-allowed by Codex. We only ever emit this for
// a genuine rewrite of a gortk-supported command; anything we cannot safely
// rewrite (non-Bash tool, missing/empty command, no rewrite, already gortk,
// unattestable construct, malformed JSON, BOM-only) is a silent pass-through —
// write NOTHING, exit 0, never crash — so Codex keeps its own native permission
// prompt for every unrelated command and never auto-allows it.
package hooks

import (
	"encoding/json"
	"fmt"
	"io"

	"gortk/internal/core"
)

// codexHookInput is the subset of the Codex PreToolUse payload we read. Unknown
// fields (hook_event_name, cwd, session_id, …) are ignored; ToolInput is captured
// raw so we can echo back the caller's other fields untouched in updatedInput.
type codexHookInput struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// CodexHookResponse computes the Codex PreToolUse hook response JSON for a raw
// payload. It returns (json, true) when a rewrite applies, or ("", false) for
// every pass-through case (non-Bash tool, missing/empty command, no rewrite,
// already gortk, malformed input, BOM only). Pure and total: it never panics and
// performs no I/O, so it is the unit under test for the Codex hook path.
func CodexHookResponse(payload []byte, excluded []string, supported supportedFunc) (string, bool) {
	// Strip any leading UTF-8 BOM(s) some Windows hosts prepend, then trim.
	payload = stripLeadingBOM(payload)
	if len(payloadTrimmed(payload)) == 0 {
		return "", false
	}

	var in codexHookInput
	if err := json.Unmarshal(payload, &in); err != nil {
		return "", false // malformed → pass through
	}

	if !isBashTool(in.ToolName) {
		return "", false
	}

	// Extract the command string out of tool_input.
	cmd, ok := commandFromToolInput(in.ToolInput)
	if !ok || cmd == "" {
		return "", false
	}

	rewritten, ok := Rewrite(cmd, excluded, supported)
	if !ok || rewritten == cmd {
		return "", false
	}

	// Build updatedInput by cloning the caller's tool_input object and replacing
	// only "command", so any other fields survive unchanged.
	updated := map[string]json.RawMessage{}
	if len(in.ToolInput) > 0 {
		_ = json.Unmarshal(in.ToolInput, &updated) // best-effort; empty on non-object
	}
	cmdJSON, _ := json.Marshal(rewritten)
	updated["command"] = cmdJSON

	// Codex requires permissionDecision "allow" alongside updatedInput — without
	// it Codex will not apply the rewritten command. We emit "allow" ONLY here, on
	// a genuine gortk rewrite; pass-through paths write nothing, so Codex never
	// auto-allows an unrelated command through gortk.
	resp := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            preToolUseKey,
			"permissionDecision":       "allow",
			"permissionDecisionReason": "gortk auto-rewrite",
			"updatedInput":             updated,
		},
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return "", false
	}
	return string(out), true
}

// runHookCodex reads a Codex PreToolUse hook JSON object from r, rewrites the
// Bash command via the shared Rewrite logic, and writes the Codex hook response
// JSON to w. On any problem (non-Bash tool, missing/empty command, no rewrite,
// malformed JSON) it emits nothing (pass-through) and returns 0 — it must never
// crash or block the user's agent. supported is injectable so the plumbing can be
// tested without depending on which command packages are compiled in.
func runHookCodex(r io.Reader, w io.Writer, supported supportedFunc) (int, error) {
	input, err := readLimited(r)
	if err != nil {
		// Could not read stdin — pass through silently.
		return 0, nil
	}
	resp, ok := CodexHookResponse(input, core.LoadConfig().Hooks.ExcludeCommands, supported)
	if !ok {
		return 0, nil // pass-through: write nothing
	}
	fmt.Fprintln(w, resp)
	return 0, nil
}
