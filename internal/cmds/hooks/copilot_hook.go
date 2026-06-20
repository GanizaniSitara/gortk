// copilot_hook.go implements `gortk hook copilot`: the GitHub Copilot
// preToolUse hook processor. It is a native-Windows, offline port of rtk's
// Copilot hook path (src/hooks/hook_cmd.rs: run_copilot / detect_format /
// handle_vscode / handle_copilot_cli / copilot_cli_response).
//
// One hook command serves two distinct host wire formats, auto-detected from
// the incoming JSON:
//
//   - VS Code Copilot Chat (snake_case): `tool_name` ∈ {runTerminalCommand,
//     Bash, bash} with the command at `tool_input.command`. On a rewrite we
//     return an `updatedInput` object (same shape as the Claude PreToolUse
//     response) carrying the caller's tool_input with only `command` replaced.
//   - Copilot CLI (camelCase): `toolName` == "bash" with `toolArgs` carried as a
//     JSON-ENCODED STRING. We parse that string to an object, rewrite its
//     `command`, and return a `modifiedArgs` object (the parsed toolArgs with
//     `command` replaced and every other field preserved).
//
// gortk is rewrite-only and offline: like the Claude path it NEVER emits a
// permission "allow"/"deny" decision — the host keeps its own native permission
// prompt. Anything we cannot safely rewrite (non-bash tool, missing/empty
// command, no rewrite, malformed JSON, BOM-only, unattestable construct) is a
// silent pass-through: write nothing, exit 0, never crash.
package hooks

import (
	"encoding/json"
	"fmt"
	"io"

	"gortk/internal/core"
)

// copilotFormat is the detected wire format of a Copilot preToolUse payload.
type copilotFormat int

const (
	// fmtPassThrough: non-bash tool, already gortk, or unknown shape — emit nothing.
	fmtPassThrough copilotFormat = iota
	// fmtVsCode: VS Code Copilot Chat / Claude-style snake_case, supports updatedInput.
	fmtVsCode
	// fmtCopilotCli: Copilot CLI camelCase, toolArgs is a JSON string, supports modifiedArgs.
	fmtCopilotCli
)

// copilotVsCodeInput is the snake_case subset we read for the VS Code format.
// ToolInput is kept raw so the caller's other fields ride back unchanged in
// updatedInput.
type copilotVsCodeInput struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// copilotCliInput is the camelCase subset we read for the Copilot CLI format.
// ToolArgs is a JSON-ENCODED STRING (note: not an object) per Copilot CLI's
// wire shape.
type copilotCliInput struct {
	ToolName string `json:"toolName"`
	ToolArgs string `json:"toolArgs"`
}

// isVsCodeCopilotTool reports whether a snake_case tool_name is one the VS Code
// Copilot Chat host uses for shell execution. Mirrors rtk detect_format's
// {runTerminalCommand, Bash, bash} match — a superset of the Claude Bash hook.
func isVsCodeCopilotTool(name string) bool {
	switch name {
	case "runTerminalCommand", "Bash", "bash":
		return true
	default:
		return false
	}
}

// CopilotHookResponse computes the Copilot preToolUse hook response JSON for a
// raw payload, auto-detecting the VS Code vs Copilot CLI format. It returns
// (json, true) when a rewrite applies, or ("", false) for every pass-through
// case (non-bash tool, missing/empty command, no rewrite, malformed input, BOM
// only). Pure and total: it never panics and performs no I/O, so it is the unit
// under test for the Copilot hook path.
func CopilotHookResponse(payload []byte, excluded []string, supported supportedFunc) (string, bool) {
	// Strip any leading UTF-8 BOM(s) some Windows hosts prepend, then trim.
	payload = stripLeadingBOM(payload)
	if len(payloadTrimmed(payload)) == 0 {
		return "", false
	}

	switch detectCopilotFormat(payload) {
	case fmtVsCode:
		return copilotVsCodeResponse(payload, excluded, supported)
	case fmtCopilotCli:
		return copilotCliResponse(payload, excluded, supported)
	default:
		return "", false
	}
}

// detectCopilotFormat classifies a payload as VS Code, Copilot CLI, or
// pass-through, faithfully porting rtk detect_format's precedence: snake_case
// `tool_name` is checked first (and, if present, decides the verdict outright —
// a non-bash snake_case tool is pass-through, never falling through to the CLI
// branch), then camelCase `toolName`.
func detectCopilotFormat(payload []byte) copilotFormat {
	// Peek at top-level keys without committing to a struct, so we can honour the
	// "tool_name present → snake_case host" precedence rule.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(payload, &probe); err != nil {
		return fmtPassThrough // malformed → pass through
	}

	if _, ok := probe["tool_name"]; ok {
		var in copilotVsCodeInput
		if err := json.Unmarshal(payload, &in); err != nil {
			return fmtPassThrough
		}
		if isVsCodeCopilotTool(in.ToolName) {
			return fmtVsCode
		}
		return fmtPassThrough
	}

	if _, ok := probe["toolName"]; ok {
		var in copilotCliInput
		if err := json.Unmarshal(payload, &in); err != nil {
			return fmtPassThrough
		}
		if in.ToolName == "bash" {
			return fmtCopilotCli
		}
		return fmtPassThrough
	}

	return fmtPassThrough
}

// copilotVsCodeResponse handles the VS Code Copilot Chat format. On a rewrite it
// emits the same hookSpecificOutput/updatedInput shape as the Claude PreToolUse
// response — clone tool_input, replace only "command" — so timeout/description
// and any other host fields survive. No permissionDecision key (rewrite-only).
func copilotVsCodeResponse(payload []byte, excluded []string, supported supportedFunc) (string, bool) {
	var in copilotVsCodeInput
	if err := json.Unmarshal(payload, &in); err != nil {
		return "", false
	}

	cmd, ok := commandFromToolInput(in.ToolInput)
	if !ok || cmd == "" {
		return "", false
	}

	rewritten, ok := Rewrite(cmd, excluded, supported)
	if !ok || rewritten == cmd {
		return "", false
	}

	// Clone the caller's tool_input object, replacing only "command".
	updated := map[string]json.RawMessage{}
	if len(in.ToolInput) > 0 {
		_ = json.Unmarshal(in.ToolInput, &updated) // best-effort; empty on non-object
	}
	cmdJSON, _ := json.Marshal(rewritten)
	updated["command"] = cmdJSON

	resp := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            preToolUseKey,
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

// copilotCliResponse handles the Copilot CLI format. toolArgs arrives as a
// JSON-encoded STRING; we parse it to an object, rewrite its "command", and
// emit {"permissionDecisionReason": ..., "modifiedArgs": <toolArgs with command
// replaced>} preserving every other toolArgs field (description, mode, …). No
// permissionDecision key (rewrite-only).
func copilotCliResponse(payload []byte, excluded []string, supported supportedFunc) (string, bool) {
	var in copilotCliInput
	if err := json.Unmarshal(payload, &in); err != nil {
		return "", false
	}
	if in.ToolArgs == "" {
		return "", false
	}

	// toolArgs is itself a JSON document encoded as a string — parse it.
	var args map[string]json.RawMessage
	if err := json.Unmarshal([]byte(in.ToolArgs), &args); err != nil {
		return "", false
	}

	cmdRaw, ok := args["command"]
	if !ok {
		return "", false
	}
	var cmd string
	if err := json.Unmarshal(cmdRaw, &cmd); err != nil || cmd == "" {
		return "", false
	}

	rewritten, ok := Rewrite(cmd, excluded, supported)
	if !ok || rewritten == cmd {
		return "", false
	}

	// Replace only "command" in the parsed toolArgs object; everything else rides.
	cmdJSON, _ := json.Marshal(rewritten)
	args["command"] = cmdJSON

	resp := map[string]any{
		"permissionDecisionReason": "gortk auto-rewrite",
		"modifiedArgs":             args,
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return "", false
	}
	return string(out), true
}

// runHookCopilot reads a Copilot preToolUse hook JSON object from r, rewrites
// the bash command via the shared Rewrite logic, and writes the hook response
// JSON to w. On any problem (non-bash tool, missing/empty command, no rewrite,
// malformed JSON) it emits nothing (pass-through) and returns 0 — it must never
// crash or block the user's agent. supported is injectable so the plumbing can
// be tested without depending on which command packages are compiled in.
func runHookCopilot(r io.Reader, w io.Writer, supported supportedFunc) (int, error) {
	input, err := readLimited(r)
	if err != nil {
		// Could not read stdin — pass through silently.
		return 0, nil
	}
	resp, ok := CopilotHookResponse(input, core.LoadConfig().Hooks.ExcludeCommands, supported)
	if !ok {
		return 0, nil // pass-through: write nothing
	}
	fmt.Fprintln(w, resp)
	return 0, nil
}
