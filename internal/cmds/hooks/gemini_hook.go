// gemini_hook.go implements `gortk hook gemini`: the Google Gemini CLI
// BeforeTool hook processor. It is a native-Windows, offline port of rtk's
// Gemini hook path (src/hooks/hook_cmd.rs: run_gemini / print_gemini /
// print_allow), reusing the SAME Rewrite engine and the shared BOM/parse/command
// helpers from hooks.go.
//
// Gemini CLI feeds a BeforeTool hook a JSON object on stdin. The shell-command
// tool is named "run_shell_command" and carries its payload at
// tool_input.command:
//
//	{ "tool_name":"run_shell_command", "tool_input":{"command":"..."}, ... }
//
// Gemini's hook wire shape (per rtk print_gemini) is a top-level "decision"
// string plus, on a rewrite, a "hookSpecificOutput.tool_input.command" carrying
// the rewritten command — NOT the Claude-style updatedInput/permissionDecision
// envelope. The decision vocabulary is {allow, ask_user, deny}.
//
// gortk is rewrite-only and offline: it never gates permissions, so unlike rtk
// (which can emit ask_user/deny from its permission engine) gortk only ever
// distinguishes "I rewrote this" from "nothing to do". Concretely:
//
//   - A genuine gortk rewrite of a supported command emits
//     {"decision":"allow","hookSpecificOutput":{"tool_input":{"command":"<rewritten>"}}}
//     — Gemini applies the rewritten command and allows it (the rewrite is the
//     whole point; an already-trusted gortk-wrapped command is safe).
//   - Every pass-through case (non-shell tool, missing/empty command, no rewrite,
//     already gortk, unattestable construct, malformed JSON) emits the bare
//     {"decision":"allow"} — Gemini proceeds with its OWN native permission flow
//     on the ORIGINAL command, exactly as if the hook were absent. We never emit
//     "deny" (no permission engine) and never auto-allow an un-rewritten command
//     into a laundered form.
//
// Unlike the Claude/Cursor paths, Gemini ALWAYS produces output: the pass-through
// case is a literal {"decision":"allow"} line rather than empty stdout. So this
// hook's pure function returns the JSON string for every input; there is no
// (string, bool) "write nothing" channel.
package hooks

import (
	"encoding/json"
	"fmt"
	"io"

	"gortk/internal/core"
)

// geminiShellTool is the Gemini CLI tool name for shell-command execution.
// Only this tool carries a rewritable command; any other tool passes through.
const geminiShellTool = "run_shell_command"

// geminiAllowJSON is the bare pass-through response: Gemini proceeds with its
// own native permission flow on the original command. Pre-rendered so the
// pass-through path performs no allocation/marshal.
const geminiAllowJSON = `{"decision":"allow"}`

// geminiHookInput is the subset of the Gemini BeforeTool payload we read.
// Unknown fields are ignored; ToolInput is captured raw so we can pull the
// command out of it tolerantly.
type geminiHookInput struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// GeminiHookResponse computes the Gemini BeforeTool hook response JSON for a raw
// payload. UNLIKE the Claude/Cursor pure functions it always returns a JSON
// string (Gemini requires output on every path): a rewrite envelope when a gortk
// rewrite applies, or the bare {"decision":"allow"} for every pass-through case
// (non-shell tool, missing/empty command, no rewrite, already gortk, malformed
// input, BOM only). Pure and total: it never panics and performs no I/O, so it
// is the unit under test for the Gemini hook path.
func GeminiHookResponse(payload []byte, excluded []string, supported supportedFunc) string {
	// Strip any leading UTF-8 BOM(s) some Windows hosts prepend, then trim.
	payload = stripLeadingBOM(payload)
	if len(payloadTrimmed(payload)) == 0 {
		return geminiAllowJSON
	}

	var in geminiHookInput
	if err := json.Unmarshal(payload, &in); err != nil {
		return geminiAllowJSON // malformed → pass through (allow original)
	}

	if in.ToolName != geminiShellTool {
		return geminiAllowJSON
	}

	cmd, ok := commandFromToolInput(in.ToolInput)
	if !ok || cmd == "" {
		return geminiAllowJSON
	}

	rewritten, ok := Rewrite(cmd, excluded, supported)
	if !ok || rewritten == cmd {
		return geminiAllowJSON
	}

	return geminiRewriteJSON(rewritten)
}

// geminiRewriteJSON renders the Gemini rewrite envelope: decision "allow" plus
// hookSpecificOutput.tool_input.command carrying the rewritten command. Mirrors
// rtk's gemini_json("allow", Some(rewritten)). Marshalling can only fail on a
// non-serializable value (impossible for plain strings), so on the theoretical
// error we fall back to the bare allow rather than panic.
func geminiRewriteJSON(rewritten string) string {
	resp := map[string]any{
		"decision": "allow",
		"hookSpecificOutput": map[string]any{
			"tool_input": map[string]any{
				"command": rewritten,
			},
		},
	}
	out, err := json.Marshal(resp)
	if err != nil {
		return geminiAllowJSON
	}
	return string(out)
}

// runHookGemini reads a Gemini BeforeTool hook JSON object from r, rewrites the
// shell command via the shared Rewrite logic, and writes the Gemini hook
// response JSON to w. Gemini requires output on every path, so this ALWAYS
// writes a line (the bare {"decision":"allow"} on pass-through) and returns 0 —
// it must never crash or block the user's agent. supported is injectable so the
// plumbing can be tested without depending on which command packages are
// compiled in.
func runHookGemini(r io.Reader, w io.Writer, supported supportedFunc) (int, error) {
	input, err := readLimited(r)
	if err != nil {
		// Could not read stdin — still emit a valid allow so Gemini proceeds.
		fmt.Fprintln(w, geminiAllowJSON)
		return 0, nil
	}
	resp := GeminiHookResponse(input, core.LoadConfig().Hooks.ExcludeCommands, supported)
	fmt.Fprintln(w, resp)
	return 0, nil
}
