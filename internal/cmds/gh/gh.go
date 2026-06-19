// Package gh is gortk's token-optimized wrapper around the GitHub CLI (`gh`).
// It provides compact alternatives to verbose `gh pr/issue/run/repo` output,
// extracting the essential information from gh's JSON. Faithful port of rtk's
// src/cmds/git/gh_cmd.rs.
//
// The output-compression logic lives in pure functions (format_pr_list,
// format_pr_view, filterMarkdownBody, …) so it can be tested directly against
// the ported Rust spec, mirroring the reference ls port.
//
// Notes on the port:
//   - rtk threads a global `ultra_compact` bool from its CLI into this module.
//     gortk's registry Run signature is Run(args, verbose), so we detect the
//     global `--ultra-compact` flag from args here and strip it before
//     dispatching, preserving identical behaviour.
//   - rtk's tee side-channel (force_tee_tail_hint) is not part of gortk's
//     offline core, so the "+N more" overflow line is emitted without the tee
//     pointer. No behaviour the Rust #[cfg(test)] suite exercises depends on it.
package gh

import (
	"encoding/json"
	"regexp"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "gh",
		Summary: "GitHub CLI (gh) commands with token-optimized output",
		Run:     Run,
	})
}

// Run is the gortk entry point. args are the tokens after "gh"; the first is the
// gh subcommand (pr/issue/run/repo/api). A global --ultra-compact flag toggles
// ASCII/inline output, mirroring rtk's cli.ultra_compact.
func Run(args []string, verbose int) (int, error) {
	ultraCompact := false
	filtered := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--ultra-compact" || a == "--ultra_compact" {
			ultraCompact = true
			continue
		}
		filtered = append(filtered, a)
	}
	args = filtered

	if len(args) == 0 {
		return runPassthrough("gh", "", nil)
	}
	subcommand := args[0]
	rest := args[1:]
	return run(subcommand, rest, verbose, ultraCompact)
}

// run dispatches the gh subcommand. Mirrors gh_cmd::run.
func run(subcommand string, args []string, verbose int, ultraCompact bool) (int, error) {
	// When the user explicitly passes --json they want raw gh JSON output, not
	// gortk filtering.
	if hasJSONFlag(args) {
		return runPassthrough("gh", subcommand, args)
	}

	switch subcommand {
	case "pr":
		return runPR(args, verbose, ultraCompact)
	case "issue":
		return runIssue(args, verbose, ultraCompact)
	case "run":
		return runWorkflow(args, verbose, ultraCompact)
	case "repo":
		return runRepo(args, verbose, ultraCompact)
	case "api":
		return runAPI(args, verbose)
	default:
		return runPassthrough("gh", subcommand, args)
	}
}

// hasJSONFlag reports whether args contain --json (user wants specific JSON
// fields, not gortk filtering).
func hasJSONFlag(args []string) bool {
	for _, a := range args {
		if a == "--json" {
			return true
		}
	}
	return false
}

// extractIdentifierAndExtraArgs extracts a positional identifier (PR/issue/run
// number) from args, returning it separately from the remaining flags (-R,
// --repo, etc.). Handles both `view 123 -R owner/repo` and `view -R owner/repo
// 123`. Returns (id, extra, ok); ok is false when no positional id is present.
func extractIdentifierAndExtraArgs(args []string) (string, []string, bool) {
	if len(args) == 0 {
		return "", nil, false
	}

	flagsWithValue := map[string]bool{
		"-R": true, "--repo": true,
		"-q": true, "--jq": true,
		"-t": true, "--template": true,
		"--job": true, "--attempt": true,
	}

	var identifier string
	haveID := false
	extra := []string{}
	skipNext := false

	for _, arg := range args {
		if skipNext {
			extra = append(extra, arg)
			skipNext = false
			continue
		}
		if flagsWithValue[arg] {
			extra = append(extra, arg)
			skipNext = true
			continue
		}
		if strings.HasPrefix(arg, "-") {
			extra = append(extra, arg)
			continue
		}
		// First non-flag arg is the identifier (number/URL).
		if !haveID {
			identifier = arg
			haveID = true
		} else {
			extra = append(extra, arg)
		}
	}

	if !haveID {
		return "", nil, false
	}
	return identifier, extra, true
}

// parseOptionalIdentifier is like extractIdentifierAndExtraArgs but yields
// ("", args, false-id) when no positional identifier is present, so callers can
// defer the "id required" decision to gh itself (e.g. `gh pr view` defaults to
// the current branch's PR). Returns (id, hasID, extra).
func parseOptionalIdentifier(args []string) (string, bool, []string) {
	if id, extra, ok := extractIdentifierAndExtraArgs(args); ok {
		return id, true, extra
	}
	return "", false, append([]string(nil), args...)
}

// runGHJSON runs a gh command that emits JSON, parses it, and applies filterFn.
// On a JSON parse error it returns the raw stdout unchanged (matching rtk).
func runGHJSON(cmdArgs []string, label string, filterFn func(v any) string) (int, error) {
	cmd := core.ResolvedCommand("gh", cmdArgs...)
	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true, NoTrailingNewline: true}
	return core.RunFiltered(cmd, "gh", label, func(stdout string) string {
		var v any
		if err := json.Unmarshal([]byte(stdout), &v); err != nil {
			return stdout
		}
		return filterFn(v)
	}, opts)
}

// truncate truncates s to maxLen runes, appending "..." when truncation occurs.
// Faithful port of rtk's core::utils::truncate: when maxLen < 3 it returns just
// "...".
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen < 3 {
		return "..."
	}
	return string(runes[:maxLen-3]) + "..."
}

// okConfirmation renders a compact success line. Faithful port of rtk's
// core::utils::ok_confirmation.
func okConfirmation(action, detail string) string {
	if detail == "" {
		return "ok " + action
	}
	return "ok " + action + " " + detail
}

// lines splits text with Rust str::lines() semantics: split on "\n" and drop a
// single trailing empty element. The runner normalizes CRLF before the filter
// sees the text.
func lines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// --- JSON accessor helpers (mirror serde_json Value access) ------------------

// jObj returns v as a JSON object, or nil.
func jObj(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

// jArr returns v as a JSON array, or (nil, false).
func jArr(v any) ([]any, bool) {
	if a, ok := v.([]any); ok {
		return a, true
	}
	return nil, false
}

// get indexes into a JSON object by key, returning nil when v is not an object
// or the key is absent. Mirrors serde_json's `v["key"]` (which yields Null).
func get(v any, key string) any {
	if m := jObj(v); m != nil {
		return m[key]
	}
	return nil
}

// asStr returns v as a string with the given default when v is not a string.
func asStr(v any, def string) string {
	if s, ok := v.(string); ok {
		return s
	}
	return def
}

// asI64 returns v as an int64 (0 when absent/non-numeric). JSON numbers decode
// to float64.
func asI64(v any) int64 {
	if f, ok := v.(float64); ok {
		return int64(f)
	}
	return 0
}

// asBool returns v as a bool with the given default.
func asBool(v any, def bool) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	return def
}

// isNull reports whether v is JSON null / absent.
func isNull(v any) bool {
	return v == nil
}

// runPassthrough runs `gh <subcommand> <args...>` with no filtering. An empty
// subcommand omits the leading subcommand token.
func runPassthrough(tool, subcommand string, args []string) (int, error) {
	var ghArgs []string
	if subcommand != "" {
		ghArgs = append(ghArgs, subcommand)
	}
	ghArgs = append(ghArgs, args...)
	return core.RunPassthrough(tool, ghArgs, 0)
}

// runPassthroughWithExtra runs `gh <base...> <extra...>` with no filtering.
func runPassthroughWithExtra(tool string, base []string, extra []string) (int, error) {
	ghArgs := append(append([]string(nil), base...), extra...)
	return core.RunPassthrough(tool, ghArgs, 0)
}

// htmlCommentRE matches HTML comments, including multiline (DOTALL).
var htmlCommentRE = regexp.MustCompile(`(?s)<!--.*?-->`)

// badgeLineRE matches a whole line that is only a clickable badge:
// [![alt](img)](link).
var badgeLineRE = regexp.MustCompile(`(?m)^\s*\[!\[[^\]]*\]\([^)]*\)\]\([^)]*\)\s*$`)

// imageOnlyLineRE matches a whole line that is only an image: ![alt](url).
var imageOnlyLineRE = regexp.MustCompile(`(?m)^\s*!\[[^\]]*\]\([^)]*\)\s*$`)

// horizontalRuleRE matches a markdown horizontal rule line (---, ***, ___).
var horizontalRuleRE = regexp.MustCompile(`(?m)^\s*(?:---+|\*\*\*+|___+)\s*$`)

// multiBlankRE collapses 3+ consecutive newlines to a single blank line.
var multiBlankRE = regexp.MustCompile(`\n{3,}`)

// filterMarkdownBody removes noise from a markdown body while preserving
// meaningful content: strips HTML comments, badge lines, image-only lines, and
// horizontal rules, and collapses excessive blank lines. Code blocks (``` or
// ~~~) are preserved untouched. Faithful port of rtk's filter_markdown_body.
func filterMarkdownBody(body string) string {
	if body == "" {
		return ""
	}

	var result strings.Builder
	remaining := body

	for {
		// Find next code-block opening (``` or ~~~), whichever comes first.
		start := -1
		fence := ""
		if p := strings.Index(remaining, "```"); p >= 0 {
			start = p
			fence = "```"
		}
		if p := strings.Index(remaining, "~~~"); p >= 0 && (start < 0 || p < start) {
			start = p
			fence = "~~~"
		}

		if start < 0 {
			// No more code blocks; filter the rest.
			result.WriteString(filterMarkdownSegment(remaining))
			break
		}

		// Filter the text before the code block.
		result.WriteString(filterMarkdownSegment(remaining[:start]))

		// Skip past the opening fence line.
		afterOpen := start + len(fence)
		codeStart := len(remaining)
		if p := strings.IndexByte(remaining[afterOpen:], '\n'); p >= 0 {
			codeStart = afterOpen + p + 1
		}

		// Find the closing fence.
		closePos := -1
		if p := strings.Index(remaining[codeStart:], fence); p >= 0 {
			closePos = codeStart + p + len(fence)
		}

		if closePos < 0 {
			// Unclosed code block — preserve everything.
			result.WriteString(remaining[start:])
			remaining = ""
			// Fall through to break: nothing left to scan.
			break
		}

		// Preserve the entire code block as-is, including the closing fence line.
		afterClose := len(remaining)
		if p := strings.IndexByte(remaining[closePos:], '\n'); p >= 0 {
			afterClose = closePos + p + 1
		}
		result.WriteString(remaining[start:afterClose])
		remaining = remaining[afterClose:]
	}

	return strings.TrimSpace(result.String())
}

// filterMarkdownSegment filters a markdown segment that is NOT inside a code
// block.
func filterMarkdownSegment(text string) string {
	s := htmlCommentRE.ReplaceAllString(text, "")
	s = badgeLineRE.ReplaceAllString(s, "")
	s = imageOnlyLineRE.ReplaceAllString(s, "")
	s = horizontalRuleRE.ReplaceAllString(s, "")
	s = multiBlankRE.ReplaceAllString(s, "\n\n")
	return s
}
