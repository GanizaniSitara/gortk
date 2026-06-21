// cursor_hook.go implements `gortk hook cursor`: the Cursor Agent preToolUse
// hook processor. It is a native-Windows, offline port of rtk's Cursor hook path
// (src/hooks/hook_cmd.rs: run_cursor / cursor_allow / strip_leading_bom),
// reusing the SAME Rewrite engine and the shared BOM/parse/command helpers from
// hooks.go.
//
// Cursor uses the VS Code snake_case payload shape — `tool_name` with the
// command at `tool_input.command` — the same input shape the Copilot VS Code
// branch and the Claude hook read. What differs is the RESPONSE shape: Cursor
// expects a flat `permission`/`updated_input` object, NOT the Claude
// `hookSpecificOutput`/`updatedInput` envelope:
//
//	{ "continue": true, "permission": "allow",
//	  "updated_input": { "command": "<rewritten>" } }
//
// The `continue: true` field is load-bearing: without it Cursor's preToolUse
// panel collapses to `Output: {}` and the rewrite becomes invisible to the user.
//
// Cursor on Windows ships hook stdin with one or TWO leading UTF-8 BOMs
// (EF BB BF, confirmed doubled on Cursor 3.2.x), which json.Unmarshal rejects —
// stripLeadingBOM (hooks.go) removes them defensively so the rewrite path keeps
// working instead of silently returning "{}".
//
// gortk is rewrite-only and offline: like the Claude path it NEVER emits a
// permission "deny" decision from a permission engine — Cursor keeps its own
// native flow. The ONE permission verdict gortk emits is `permission:"allow"`,
// and ONLY on a genuine rewrite of a gortk-supported command (the rewrite is the
// whole point; an already-trusted gortk-wrapped command is safe). Everything we
// cannot safely rewrite (non-shell tool, missing/empty command, no rewrite,
// already gortk, unattestable construct, malformed JSON, BOM-only) returns the
// empty object "{}" — Cursor requires JSON on every code path, so the
// pass-through is "{}", not empty stdout.
package hooks

import (
	"encoding/json"
	"fmt"
	"io"

	"gortk/internal/core"
)

// cursorEmptyJSON is the Cursor pass-through response. Cursor requires valid JSON
// on every code path, so "nothing to do" is the empty object, not empty stdout.
const cursorEmptyJSON = "{}"

// cursorHookInput is the snake_case subset we read for the Cursor format — the
// same VS Code shape the Copilot/Claude paths read. ToolInput is kept raw so the
// command can be pulled out tolerantly.
type cursorHookInput struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// CursorHookResponse computes the Cursor preToolUse hook response JSON for a raw
// payload. UNLIKE the Claude/Copilot pure functions it always returns a JSON
// string (Cursor requires output on every path): the flat permission/updated_input
// object when a rewrite applies, or the bare "{}" for every pass-through case
// (missing/empty command, no rewrite, already gortk, malformed input, BOM only).
// Pure and total: it never panics and performs no I/O, so it is the unit under
// test for the Cursor hook path.
//
// Cursor's payload carries no tool_name discriminator we gate on (the host only
// invokes the preToolUse hook for shell commands), matching rtk run_cursor which
// reads tool_input.command directly without a tool_name check.
func CursorHookResponse(payload []byte, excluded []string, supported supportedFunc) string {
	// Strip any leading UTF-8 BOM(s) Cursor prepends on Windows, then trim.
	payload = stripLeadingBOM(payload)
	if len(payloadTrimmed(payload)) == 0 {
		return cursorEmptyJSON
	}

	var in cursorHookInput
	if err := json.Unmarshal(payload, &in); err != nil {
		return cursorEmptyJSON // malformed → pass through
	}

	cmd, ok := commandFromToolInput(in.ToolInput)
	if !ok || cmd == "" {
		return cursorEmptyJSON
	}

	rewritten, ok := Rewrite(cmd, excluded, supported)
	if !ok || rewritten == cmd {
		return cursorEmptyJSON
	}

	return cursorAllowJSON(rewritten)
}

// cursorAllowJSON renders the Cursor rewrite response: continue:true,
// permission:"allow", and updated_input.command carrying the rewritten command.
// Mirrors rtk cursor_allow. The `continue` key keeps Cursor's preToolUse panel
// from collapsing to `Output: {}`. Marshalling can only fail on a
// non-serializable value (impossible for plain strings/bools), so on the
// theoretical error we fall back to the empty object rather than panic.
func cursorAllowJSON(rewritten string) string {
	resp := map[string]any{
		"continue":   true,
		"permission": "allow",
		"updated_input": map[string]any{
			"command": rewritten,
		},
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return cursorEmptyJSON
	}
	return string(out)
}

// runHookCursor reads a Cursor preToolUse hook JSON object from r, rewrites the
// shell command via the shared Rewrite logic, and writes the Cursor hook
// response JSON to w. Cursor requires JSON on every path, so this ALWAYS writes a
// line (the bare "{}" on pass-through) and returns 0 — it must never crash or
// block the user's agent. supported is injectable so the plumbing can be tested
// without depending on which command packages are compiled in.
func runHookCursor(r io.Reader, w io.Writer, supported supportedFunc) (int, error) {
	input, err := readLimited(r)
	if err != nil {
		// Could not read stdin — still emit a valid empty object.
		fmt.Fprintln(w, cursorEmptyJSON)
		return 0, nil
	}
	resp := CursorHookResponse(input, core.LoadConfig().Hooks.ExcludeCommands, supported)
	fmt.Fprintln(w, resp)
	return 0, nil
}
