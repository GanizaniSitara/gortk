// Package glab is gortk's token-optimized wrapper for the GitLab CLI (`glab`).
// It provides compact alternatives to verbose `glab` commands by parsing their
// JSON/text output and re-emitting a dense summary. Faithful port of rtk's
// src/cmds/git/glab_cmd.rs.
//
// glab-specific differences from the GitHub (`gh`) wrapper it mirrors:
//   - MR notation: `!42` (not `#42`)
//   - States: `opened`/`merged`/`closed` (lowercase, not UPPER)
//   - Author: `author.username` (not `author.login`)
//   - URL: `web_url` (not `url`)
//   - Description: `description` (not `body`)
//   - Merge status: `merge_status` ("can_be_merged") (not `mergeable`)
//   - Pipeline: `head_pipeline.status` (not `statusCheckRollup`)
//
// Like rtk, this wraps the platform `glab`; gortk resolves it PATHEXT-aware so
// `glab.exe` / `glab.cmd` on PATH are found transparently on Windows.
//
// Note: rtk appends explicit tee hints (core::tee::force_tee_tail_hint) when a
// capped list overflows. gortk's RunFiltered already tees the raw output on
// failure, so that side-channel is dropped to avoid duplicating the mechanism.
// The "… +N more" overflow line is preserved.
package glab

import (
	"encoding/json"
	"regexp"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	// rtk exposes glab as a single `glab` command with a positional subcommand
	// (mr/issue/ci/pipeline/api). gortk has no nested command framework, so we
	// register one command and dispatch the subcommand from Run.
	registry.Register(&registry.Cmd{
		Name:    "glab",
		Summary: "GitLab CLI (glab) commands with token-optimized output",
		Run:     Run,
	})
}

// Package-level regexes, mirroring the lazy_static! block in glab_cmd.rs.
var (
	htmlCommentRE   = regexp.MustCompile(`(?s)<!--.*?-->`)
	badgeLineRE     = regexp.MustCompile(`(?m)^[ \t]*\[!\[[^\]]*\]\([^)]*\)\]\([^)]*\)[ \t]*$`)
	imageOnlyLineRE = regexp.MustCompile(`(?m)^[ \t]*!\[[^\]]*\]\([^)]*\)[ \t]*$`)
	horizontalRule  = regexp.MustCompile(`(?m)^[ \t]*(?:---+|\*\*\*+|___+)[ \t]*$`)
	multiBlankRE    = regexp.MustCompile(`\n{3,}`)
	mrURLRE         = regexp.MustCompile(`/-/merge_requests/(\d+)`)
	// sectionMarkerRE matches GitLab CI section markers:
	// section_start/end:timestamp:name[0K
	sectionMarkerRE = regexp.MustCompile(`section_(?:start|end):\d+:[a-z0-9_]+(?:\x1b\[0K|\[0K)*`)
	// bareANSIRE matches bare bracket ANSI-like codes without ESC prefix:
	// [0K, [0;m, [36;1m, etc.
	bareANSIRE = regexp.MustCompile(`\[[\d;]+[A-Za-z]`)
)

// Run dispatches a glab subcommand. args are the tokens after "glab"; the first
// is the subcommand (mr/issue/ci/pipeline/...). verbose is the -v count.
//
// gortk has no global --ultra-compact flag (no command uses one), so this port
// always runs in normal (non-ultra) compaction mode.
func Run(args []string, verbose int) (int, error) {
	const ultraCompact = false

	if len(args) == 0 {
		// No subcommand at all — let glab print its own usage.
		return runPassthrough("glab", "", nil)
	}

	subcommand := args[0]
	rest := args[1:]

	// If the user explicitly requests a specific output format, passthrough
	// unchanged to avoid double JSON injection / destroying their requested form.
	if hasOutputFlag(rest) {
		return runPassthrough("glab", subcommand, rest)
	}

	switch subcommand {
	case "mr":
		return runMR(rest, verbose, ultraCompact)
	case "issue":
		return runIssue(rest, verbose, ultraCompact)
	case "ci", "pipeline":
		return runCI(rest, verbose, ultraCompact)
	case "release":
		return runRelease(rest, verbose, ultraCompact)
	case "api":
		return runAPI(rest, verbose)
	default:
		return runPassthrough("glab", subcommand, rest)
	}
}

// ── shared helpers ──────────────────────────────────────────────────────

// truncate shortens s to at most maxLen runes, appending "..." when cut.
// Mirrors rtk's core::utils::truncate.
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

// okConfirmation formats a write-op confirmation: "ok <action> <detail>".
// Mirrors rtk's core::utils::ok_confirmation.
func okConfirmation(action, detail string) string {
	if detail == "" {
		return "ok " + action
	}
	return "ok " + action + " " + detail
}

// jsonLines splits a filtered markdown body the way Rust's str::lines() does:
// a trailing newline does not yield a final empty element.
func bodyLines(s string) []string {
	parts := strings.Split(s, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}

// filterMarkdownBody removes noise (HTML comments, badge/image-only lines,
// horizontal rules) and collapses excessive blank lines, while leaving code
// blocks (``` / ~~~ fenced) untouched. Faithful port of filter_markdown_body.
func filterMarkdownBody(body string) string {
	if body == "" {
		return ""
	}

	var result strings.Builder
	remaining := body

	for {
		// Find the next fence opener (``` or ~~~), whichever comes first.
		fencePos := -1
		fence := ""
		if p := strings.Index(remaining, "```"); p >= 0 {
			fencePos = p
			fence = "```"
		}
		if p := strings.Index(remaining, "~~~"); p >= 0 && (fencePos < 0 || p < fencePos) {
			fencePos = p
			fence = "~~~"
		}

		if fencePos < 0 {
			result.WriteString(filterMarkdownSegment(remaining))
			break
		}

		start := fencePos
		before := remaining[:start]
		result.WriteString(filterMarkdownSegment(before))

		afterOpen := start + len(fence)
		// codeStart is the byte just past the newline after the opening fence.
		codeStart := len(remaining)
		if p := strings.Index(remaining[afterOpen:], "\n"); p >= 0 {
			codeStart = afterOpen + p + 1
		}

		// Find the closing fence.
		closePos := -1
		if p := strings.Index(remaining[codeStart:], fence); p >= 0 {
			closePos = codeStart + p + len(fence)
		}

		if closePos < 0 {
			// Unterminated code block — emit the rest verbatim and stop.
			result.WriteString(remaining[start:])
			remaining = ""
			result.WriteString(remaining)
			break
		}

		// Emit the fenced block verbatim, including its trailing-line remainder.
		result.WriteString(remaining[start:closePos])
		afterClose := len(remaining)
		if p := strings.Index(remaining[closePos:], "\n"); p >= 0 {
			afterClose = closePos + p + 1
		}
		result.WriteString(remaining[closePos:afterClose])
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
	s = horizontalRule.ReplaceAllString(s, "")
	s = multiBlankRE.ReplaceAllString(s, "\n\n")
	return s
}

// stateIcon returns the state tag for MR/issue states (glab uses lowercase).
func stateIcon(state string, ultraCompact bool) string {
	if ultraCompact {
		switch state {
		case "opened":
			return "O"
		case "merged":
			return "M"
		case "closed":
			return "C"
		default:
			return "?"
		}
	}
	switch state {
	case "opened":
		return "[open]"
	case "merged":
		return "[merged]"
	case "closed":
		return "[closed]"
	default:
		return "?"
	}
}

// pipelineIcon returns the pipeline status tag. Non-compact mode uses text tags
// for parity with the gh wrapper (avoids multi-byte terminal rendering quirks).
func pipelineIcon(status string, ultraCompact bool) string {
	if ultraCompact {
		switch status {
		case "success":
			return "+"
		case "failed":
			return "x"
		case "canceled", "cancelled":
			return "X"
		case "running", "pending":
			return "~"
		case "skipped":
			return "-"
		default:
			return "?"
		}
	}
	switch status {
	case "success":
		return "[ok]"
	case "failed":
		return "[fail]"
	case "canceled", "cancelled":
		return "[cancel]"
	case "running":
		return "[run]"
	case "pending":
		return "[pend]"
	case "skipped":
		return "[skip]"
	default:
		return "?"
	}
}

// extractMRNumber pulls an MR number out of a glab URL or text, if present.
func extractMRNumber(text string) (string, bool) {
	m := mrURLRE.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// extractIdentifierAndExtraArgs returns the first positional identifier
// (MR/issue number or URL) plus the remaining args, skipping glab flags that
// take a value. Returns (id, extra, true) when an identifier is found.
func extractIdentifierAndExtraArgs(args []string) (string, []string, bool) {
	if len(args) == 0 {
		return "", nil, false
	}

	// Known glab flags that take a value — skip these and their values.
	flagsWithValue := map[string]bool{
		"-R": true, "--repo": true,
		"-g": true, "--group": true,
		"-F": true, "--output": true,
		"-m": true, "--message": true,
	}

	identifier := ""
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
// (id="", extra=args, ok=false) when no positional identifier is present, so
// callers can defer the "id required" decision to glab itself (e.g.
// `glab mr view` defaults to the current branch's MR).
func parseOptionalIdentifier(args []string) (string, []string, bool) {
	if id, extra, ok := extractIdentifierAndExtraArgs(args); ok {
		return id, extra, true
	}
	out := make([]string, len(args))
	copy(out, args)
	return "", out, false
}

// hasOutputFlag reports whether the user explicitly requested JSON/custom
// output. When present we passthrough to avoid double JSON injection.
func hasOutputFlag(args []string) bool {
	for _, a := range args {
		if a == "--output" || a == "-F" || a == "--json" {
			return true
		}
	}
	return false
}

// shouldPassthroughView reports whether a view subcommand should passthrough
// (--web, --comments, explicit output format).
func shouldPassthroughView(extraArgs []string) bool {
	for _, a := range extraArgs {
		if a == "--web" || a == "--comments" || a == "--output" || a == "-F" {
			return true
		}
	}
	return false
}

// runGlabJSON runs a glab command that emits JSON and filters it through
// filterFn. On JSON parse failure (glab returns plain text for empty results),
// it falls back to the raw stdout.
func runGlabJSON(cmdArgs []string, label string, filterFn func(json.RawMessage) string) (int, error) {
	cmd := core.ResolvedCommand("glab", cmdArgs...)
	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true, NoTrailingNewline: true}
	return core.RunFiltered(cmd, "glab", label, func(stdout string) string {
		var v json.RawMessage
		if err := json.Unmarshal([]byte(stdout), &v); err != nil {
			return stdout
		}
		return filterFn(v)
	}, opts)
}

// runPassthrough runs `glab <subcommand> <args...>` with no filtering.
func runPassthrough(tool, subcommand string, args []string) (int, error) {
	var osArgs []string
	if subcommand != "" {
		osArgs = append(osArgs, subcommand)
	}
	osArgs = append(osArgs, args...)
	return core.RunPassthrough(tool, osArgs, 0)
}

// runPassthroughWithExtra runs `glab <base...> <extra...>` with no filtering.
func runPassthroughWithExtra(tool string, baseArgs, extraArgs []string) (int, error) {
	osArgs := make([]string, 0, len(baseArgs)+len(extraArgs))
	osArgs = append(osArgs, baseArgs...)
	osArgs = append(osArgs, extraArgs...)
	return core.RunPassthrough(tool, osArgs, 0)
}

// ── small JSON accessors (serde_json::Value parity) ─────────────────────

// jsonStr extracts json[key] as a string, or "" if absent/not a string.
func jsonStr(obj map[string]json.RawMessage, key string) string {
	raw, ok := obj[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// jsonInt extracts json[key] as an integer, or 0 if absent/not a number.
func jsonInt(obj map[string]json.RawMessage, key string) int64 {
	raw, ok := obj[key]
	if !ok {
		return 0
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0
	}
	return n
}

// jsonObj extracts json[key] as an object, or nil if absent/null/not an object.
func jsonObj(obj map[string]json.RawMessage, key string) map[string]json.RawMessage {
	raw, ok := obj[key]
	if !ok {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// jsonArr extracts json[key] as an array of raw elements, or nil if absent/null.
func jsonArr(obj map[string]json.RawMessage, key string) []json.RawMessage {
	raw, ok := obj[key]
	if !ok {
		return nil
	}
	var a []json.RawMessage
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil
	}
	return a
}

// asObject decodes a raw JSON value as an object, reporting whether it was one.
func asObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, false
	}
	return m, true
}

// asArray decodes a raw JSON value as an array, reporting whether it was one.
func asArray(raw json.RawMessage) ([]json.RawMessage, bool) {
	var a []json.RawMessage
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, false
	}
	return a, true
}

// orPlaceholder returns s, or "???" when s is empty — mirroring the Rust
// `.as_str().unwrap_or("???")` pattern used for required display fields.
func orPlaceholder(s string) string {
	if s == "" {
		return "???"
	}
	return s
}

// nestedUsername returns obj[key]["username"] as a string, with "???" fallback
// matching the Rust `.unwrap_or("???")` on `author.username`.
func nestedUsernameOr(obj map[string]json.RawMessage, key, fallback string) string {
	nested := jsonObj(obj, key)
	if nested == nil {
		return fallback
	}
	u := jsonStr(nested, "username")
	if u == "" {
		return fallback
	}
	return u
}
