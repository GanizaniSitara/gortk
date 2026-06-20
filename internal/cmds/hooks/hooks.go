// Package hooks is gortk's command-rewrite and agent-integration layer — the
// piece that makes gortk wrap dev-tool commands automatically inside an LLM
// coding agent (Claude Code). It is a native-Windows, offline port of rtk's
// src/hooks/{rewrite_cmd.rs,hook_cmd.rs,init.rs} plus the slices of
// src/discover/{registry.rs,lexer.rs} that the rewrite decision depends on.
//
// Three commands are registered from init():
//
//   - "rewrite" — `gortk rewrite git status` prints `gortk git status` when
//     gortk can optimize the command, exiting 0; otherwise exits 1 with no
//     output. This is the single source of truth used by the hooks below, so a
//     shell hook can do `REWRITTEN=$(gortk rewrite "$CMD") || exit 0`.
//   - "hook claude" — reads a Claude Code PreToolUse hook JSON object from stdin,
//     rewrites the Bash command through the SAME logic, and writes the Claude
//     hook response JSON to stdout (pass-through on anything it can't handle).
//   - "init" — a Windows-native installer that writes a hook launcher under
//     ~/.claude and patches ~/.claude/settings.json to invoke `gortk hook claude`
//     as a PreToolUse hook for Bash. Supports --show and --dry-run.
//
// Design vs rtk (intentional):
//
//   - rtk's rewrite decision is driven by a large static RULES table
//     (src/discover/rules.rs) mapping concrete command patterns to fixed
//     `rtk <x>` equivalents. gortk instead asks its OWN command registry and TOML
//     filter engine whether a command is optimizable — per the porting brief, a
//     command "can be optimized" when its first token (basename) is a registered
//     gortk command OR a tomlfilter matches the full command line. The rewrite is
//     then simply `gortk <original-segment>`. This keeps the hooks in lock-step
//     with whatever commands/filters gortk actually ships, with no second source
//     of truth to drift.
//   - rtk's permission-verdict machinery (Allow/Ask/Deny exit codes 0/3/2, the
//     #1155 default-to-ask protocol, audit logging, telemetry) is dropped. gortk
//     is offline and does not gate permissions — Claude Code keeps its own
//     native permission prompt. We only ever *rewrite*; we never emit
//     `permissionDecision: "allow"`. The security-relevant passthrough rules
//     (heredocs, command/process substitution, real file-target redirects) are
//     preserved so the hook never launders an unattestable construct into an
//     auto-approved form.
//   - rtk's multi-agent installer (Cursor / Gemini / Copilot / OpenCode / Codex
//     / Windsurf / Pi / Hermes) is out of scope; the installer targets Claude
//     Code on Windows only.
package hooks

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
	"gortk/internal/tomlfilter"
)

// brand is the gortk binary name used in all user-facing strings and rewrites.
const brand = "gortk"

// stdinCap bounds how much hook stdin we read, mirroring rtk's 1 MiB cap so a
// runaway payload can never exhaust memory inside the user's agent.
const stdinCap = 1 << 20 // 1 MiB

func init() {
	registry.Register(&registry.Cmd{
		Name:    "rewrite",
		Summary: "Print the gortk-wrapped equivalent of a raw command (hook source of truth)",
		Run:     RunRewrite,
	})
	registry.Register(&registry.Cmd{
		Name:    "hook",
		Summary: "Agent hook processors (subcommands: claude, copilot) — reads JSON from stdin",
		Run:     RunHook,
	})
	registry.Register(&registry.Cmd{
		Name:    "init",
		Summary: "Install the gortk PreToolUse hook into Claude Code (--show, --dry-run)",
		Run:     RunInit,
	})
}

// supportedFunc reports whether gortk can optimize a single, already-cleaned
// command (env prefixes and redirects stripped). Production wiring uses
// gortkSupports; tests inject a fake so the rewrite logic can be exercised
// without depending on which command packages happen to be blank-imported.
type supportedFunc func(cmd string) bool

// gortkSupports is the real predicate: a command is optimizable when its first
// token's basename is a registered gortk command, or when a builtin TOML filter
// matches the full command line. Mirrors the porting brief's definition of
// "can optimize".
func gortkSupports(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	first := firstToken(cmd)
	if first == "" {
		return false
	}
	base := baseName(first)
	if base == "" {
		return false
	}
	if _, ok := registry.Lookup(base); ok {
		return true
	}
	// TOML filters match on the basename-normalized command line (so an absolute
	// /usr/bin/make still hits a "^make\b" filter), matching main.fallback and
	// rtk's run_fallback lookup construction.
	lookup := base
	if rest := strings.TrimSpace(cmd[len(first):]); rest != "" {
		lookup = base + " " + rest
	}
	return tomlfilter.FindMatching(lookup) != nil
}

// ── pure rewrite logic ────────────────────────────────────────────────

// Rewrite returns the gortk-wrapped form of a raw shell command and whether a
// rewrite applies. ok is false (and the returned string empty) when the command
// has no gortk equivalent, is already gortk, or contains an unattestable
// construct (heredoc / substitution / file-target redirect) that must be passed
// through to the agent unchanged.
//
// excluded lists command names (or anchored patterns) the caller configured to
// leave untouched. supported decides per-segment optimizability.
//
// Compound commands joined by && || ; | and background & are rewritten
// segment-by-segment: each supported segment becomes `gortk <segment>`, each
// unsupported (or already-gortk) segment is passed through verbatim, and the
// operators/whitespace between them are preserved. For pipes, only the segments
// to the LEFT of a `|` are rewritten (pipe consumers stay raw), matching rtk.
func Rewrite(cmd string, excluded []string, supported supportedFunc) (string, bool) {
	trimmed := strings.TrimSpace(collapseLineContinuations(cmd))
	if trimmed == "" {
		return "", false
	}
	// Whole-command unattestable gate: a heredoc, `$((` arithmetic,
	// command/process substitution, or a real file-target redirect anywhere means
	// we cannot safely decompose the command — pass through so the agent sees the
	// original and never auto-allows a laundered form. (fd-dup like `2>&1` and
	// `/dev/null` are exempt and ride along on the rewrite.)
	if hasHeredoc(trimmed) || strings.Contains(trimmed, "$((") ||
		containsSubstitution(trimmed) || containsFileTargetRedirect(trimmed) {
		return "", false
	}

	// Simple (non-compound) command that is already gortk → return as-is, so the
	// hook treats it as "supported" and does not re-prefix. (Matches rtk: a bare
	// `rtk git status` rewrites to itself.)
	if !hasCompound(trimmed) && isAlreadyGortk(trimmed) {
		return trimmed, true
	}

	out, changed := rewriteCompound(trimmed, excluded, supported)
	if !changed {
		return "", false
	}
	return out, true
}

// segment describes one piece of a compound command and the operator text that
// follows it (empty for the final segment).
type segment struct {
	text string // the command text of this segment (trimmed)
	op   string // the operator that terminated it, with surrounding spacing
}

// rewriteCompound splits cmd into segments on shell operators and rewrites each.
// Returns the reassembled command and whether anything changed.
func rewriteCompound(cmd string, excluded []string, supported supportedFunc) (string, bool) {
	segs, afterPipe := splitSegments(cmd)
	var b strings.Builder
	changed := false
	for _, s := range segs {
		rewritten := rewriteSegment(s.text, excluded, supported)
		if rewritten != s.text {
			changed = true
		}
		b.WriteString(rewritten)
		b.WriteString(s.op)
	}
	// Pipe tail (everything from the first `|` onward) is appended verbatim.
	if afterPipe != "" {
		b.WriteString(afterPipe)
	}
	return b.String(), changed
}

// splitSegments tokenizes cmd into rewritable segments. The boolean operators
// && || ; and background & each terminate a segment and are preserved verbatim
// (with their spacing). The first pipe `|` ends the rewritable region: the
// returned afterPipe string holds the pipe operator and everything after it,
// to be re-appended unchanged.
func splitSegments(cmd string) (segs []segment, afterPipe string) {
	toks := tokenize(cmd)
	segStart := 0
	for _, t := range toks {
		if t.offset < segStart {
			continue
		}
		switch t.kind {
		case tokOperator: // && || ;
			seg := strings.TrimSpace(cmd[segStart:t.offset])
			segs = append(segs, segment{text: seg, op: operatorSpacing(cmd, t)})
			segStart = t.offset + len(t.value)
			segStart = skipSpaces(cmd, segStart)
		case tokBackground: // bare &
			seg := strings.TrimSpace(cmd[segStart:t.offset])
			segs = append(segs, segment{text: seg, op: " & "})
			segStart = t.offset + len(t.value)
			segStart = skipSpaces(cmd, segStart)
		case tokPipe: // first | ends the rewritable region
			seg := strings.TrimSpace(cmd[segStart:t.offset])
			segs = append(segs, segment{text: seg, op: ""})
			afterPipe = " " + strings.TrimSpace(cmd[t.offset:])
			return segs, afterPipe
		}
	}
	tail := strings.TrimSpace(cmd[segStart:])
	segs = append(segs, segment{text: tail, op: ""})
	return segs, ""
}

// operatorSpacing renders an operator token with the spacing the reassembled
// command should carry. `;` becomes `; ` (no leading space, trailing space);
// `&&`/`||` become ` && `/` || ` (spaces both sides), matching rtk's
// rewrite_compound output shape.
func operatorSpacing(cmd string, t token) string {
	if t.value == ";" {
		after := t.offset + len(t.value)
		if after < len(cmd) {
			return "; "
		}
		return ";"
	}
	return " " + t.value + " "
}

func skipSpaces(s string, i int) int {
	for i < len(s) && s[i] == ' ' {
		i++
	}
	return i
}

// rewriteSegment rewrites a single command segment, returning it unchanged when
// no gortk equivalent applies. Env prefixes (FOO=bar, sudo, env VAR=val) are
// preserved on the front; trailing fd-dup redirects (2>&1, &>/dev/null) are
// preserved on the back; pipe-incompatible producers (find/fd) are never
// rewritten because gortk's compact output breaks downstream consumers.
func rewriteSegment(seg string, excluded []string, supported supportedFunc) string {
	trimmed := strings.TrimSpace(seg)
	if trimmed == "" {
		return seg
	}

	// find/fd produce streams other tools consume; gortk's reshaped output would
	// break xargs/wc/etc. Leave them raw even when "supported".
	if base := baseName(firstToken(trimmed)); base == "find" || base == "fd" {
		return seg
	}

	envPrefix, rest := splitEnvPrefix(trimmed)
	if rest == "" {
		return seg
	}

	cmdPart, redirectSuffix := splitTrailingRedirects(rest)

	// Already gortk — pass through unchanged.
	if isAlreadyGortk(cmdPart) {
		return seg
	}

	if isExcluded(cmdPart, excluded) {
		return seg
	}

	if !supported(cmdPart) {
		return seg
	}

	return envPrefix + brand + " " + cmdPart + redirectSuffix
}

// isExcluded reports whether cmd matches the user's exclude list. A pattern
// matches when cmd equals it or starts with it followed by whitespace (so
// "curl" excludes "curl https://x" but not "curlfoo"). This is the gortk-stdlib
// analogue of rtk's anchored `^pattern($|\s)` regex, without bringing in regex.
func isExcluded(cmd string, excluded []string) bool {
	for _, p := range excluded {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Tolerate an rtk-style leading '^' anchor.
		p = strings.TrimPrefix(p, "^")
		if cmd == p {
			return true
		}
		if strings.HasPrefix(cmd, p) && len(cmd) > len(p) {
			switch cmd[len(p)] {
			case ' ', '\t':
				return true
			}
		}
	}
	return false
}

// ── string helpers (pure) ─────────────────────────────────────────────

func hasCompound(cmd string) bool {
	return strings.Contains(cmd, "&&") ||
		strings.Contains(cmd, "||") ||
		strings.Contains(cmd, ";") ||
		strings.Contains(cmd, "|") ||
		strings.Contains(cmd, " & ")
}

// isAlreadyGortk reports whether a command already starts with the gortk brand
// (so it should not be wrapped again).
func isAlreadyGortk(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	return cmd == brand || strings.HasPrefix(cmd, brand+" ")
}

// firstToken returns the first whitespace-delimited token of a command.
func firstToken(cmd string) string {
	cmd = strings.TrimLeft(cmd, " \t")
	if i := strings.IndexAny(cmd, " \t"); i >= 0 {
		return cmd[:i]
	}
	return cmd
}

// baseName strips any directory portion of a command token so /usr/bin/ls and
// C:\tools\ls.exe both reduce to "ls". Handles both / and \ separators and
// drops a trailing .exe/.cmd/.bat/.ps1 extension (Windows shims).
func baseName(tok string) string {
	if tok == "" {
		return ""
	}
	// Take the part after the last path separator (either slash style).
	if i := strings.LastIndexAny(tok, `/\`); i >= 0 {
		tok = tok[i+1:]
	}
	// Drop a Windows executable extension so "ls.exe" → "ls".
	lower := strings.ToLower(tok)
	for _, ext := range []string{".exe", ".cmd", ".bat", ".ps1", ".com"} {
		if strings.HasSuffix(lower, ext) {
			return tok[:len(tok)-len(ext)]
		}
	}
	return tok
}

// collapseLineContinuations turns bash line continuations (`\` + newline, with
// surrounding horizontal whitespace) into a single space, mirroring what bash
// does before dispatch so multi-line commands still hit the matcher. Pure
// stdlib (no regex) for the common no-continuation fast path.
func collapseLineContinuations(s string) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\\' {
			// Look past optional CR for an LF, with optional horizontal ws before.
			j := i + 1
			if j < len(s) && s[j] == '\r' {
				j++
			}
			if j < len(s) && s[j] == '\n' {
				// Trim trailing horizontal whitespace we already emitted.
				out := strings.TrimRight(b.String(), " \t\v\f")
				b.Reset()
				b.WriteString(out)
				b.WriteByte(' ')
				j++
				// Skip leading horizontal whitespace on the continuation line.
				for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\v' || s[j] == '\f') {
					j++
				}
				i = j
				continue
			}
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// splitEnvPrefix peels off a leading run of env-style prefixes — `sudo `,
// `env `, and `VAR=value` assignments (quoted values allowed) — returning the
// prefix (including its trailing space) and the remaining command. Mirrors rtk's
// ENV_PREFIX regex without the regexp dependency.
func splitEnvPrefix(cmd string) (prefix, rest string) {
	i := 0
	for {
		// sudo / env wrappers
		if w, ok := matchWordPrefix(cmd[i:], "sudo"); ok {
			i += w
			continue
		}
		if w, ok := matchWordPrefix(cmd[i:], "env"); ok {
			i += w
			continue
		}
		if w, ok := matchEnvAssign(cmd[i:]); ok {
			i += w
			continue
		}
		break
	}
	return cmd[:i], strings.TrimSpace(cmd[i:])
}

// matchWordPrefix matches `word` followed by at least one space, returning the
// number of bytes consumed (word + the run of spaces).
func matchWordPrefix(s, word string) (int, bool) {
	if !strings.HasPrefix(s, word) {
		return 0, false
	}
	j := len(word)
	if j >= len(s) || (s[j] != ' ' && s[j] != '\t') {
		return 0, false
	}
	for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
		j++
	}
	return j, true
}

// matchEnvAssign matches a single `NAME=value` assignment (NAME = [A-Z_][A-Z0-9_]*)
// followed by whitespace, with value either double-quoted, single-quoted, or an
// unquoted run of non-space chars. Returns bytes consumed including trailing ws.
func matchEnvAssign(s string) (int, bool) {
	j := 0
	// NAME: first char A-Z or _.
	if j >= len(s) || !isUpperOrUnderscore(s[j]) {
		return 0, false
	}
	j++
	for j < len(s) && isUpperDigitUnderscore(s[j]) {
		j++
	}
	if j >= len(s) || s[j] != '=' {
		return 0, false
	}
	j++ // consume '='
	// value
	switch {
	case j < len(s) && s[j] == '"':
		j++
		for j < len(s) {
			if s[j] == '\\' && j+1 < len(s) {
				j += 2
				continue
			}
			if s[j] == '"' {
				j++
				break
			}
			j++
		}
	case j < len(s) && s[j] == '\'':
		j++
		for j < len(s) {
			if s[j] == '\\' && j+1 < len(s) {
				j += 2
				continue
			}
			if s[j] == '\'' {
				j++
				break
			}
			j++
		}
	default:
		for j < len(s) && s[j] != ' ' && s[j] != '\t' {
			j++
		}
	}
	// Require trailing whitespace (the assignment must precede a command).
	if j >= len(s) || (s[j] != ' ' && s[j] != '\t') {
		return 0, false
	}
	for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
		j++
	}
	return j, true
}

func isUpperOrUnderscore(b byte) bool { return b == '_' || (b >= 'A' && b <= 'Z') }
func isUpperDigitUnderscore(b byte) bool {
	return b == '_' || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// splitTrailingRedirects peels trailing fd-dup/close redirects (e.g. `2>&1`,
// `&>/dev/null`, `2>/dev/null`, `2>&-`) off the end of a command so the core
// command can be matched, then re-appended. Real file-target redirects are
// caught earlier by the unattestable gate, so what remains here is always an
// attestable redirect that should ride along on the rewrite. Returns
// (commandPart, redirectSuffix) where redirectSuffix retains its leading space.
func splitTrailingRedirects(cmd string) (string, string) {
	// Conservative: if quotes are present, do not strip (apostrophes can fool a
	// naive scanner). Matches rtk's documented safe-fallback behaviour.
	if strings.ContainsAny(cmd, `"'`) {
		return cmd, ""
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return cmd, ""
	}
	// Walk from the end, consuming redirect tokens (and a following target word
	// for `2>` / `>` style redirects with a separate target like /dev/null).
	cut := len(fields)
	for cut > 0 {
		tok := fields[cut-1]
		if isRedirectToken(tok) {
			cut--
			continue
		}
		// A bare target following a redirect token (e.g. "2> /dev/null").
		if cut >= 2 && isRedirectToken(fields[cut-2]) && !isRedirectToken(tok) {
			cut -= 2
			continue
		}
		break
	}
	if cut >= len(fields) || cut == 0 {
		return cmd, ""
	}
	head := strings.Join(fields[:cut], " ")
	tail := strings.Join(fields[cut:], " ")
	return head, " " + tail
}

// isRedirectToken reports whether a whitespace-delimited token is (part of) a
// redirect like `2>&1`, `&>/dev/null`, `2>`, `>`, `>>`, `2>&-`.
func isRedirectToken(tok string) bool {
	if tok == "" {
		return false
	}
	// Strip an optional leading fd number (e.g. "2>&1" → ">&1").
	t := strings.TrimLeft(tok, "0123456789")
	return strings.HasPrefix(t, ">") || strings.HasPrefix(t, "&>") || strings.HasPrefix(t, ">>")
}

// hasHeredoc reports whether the command contains an unquoted heredoc operator
// (`<<` or `<<<`). Quote-aware so `echo "a <<b"` is not flagged.
func hasHeredoc(cmd string) bool {
	for _, t := range tokenize(cmd) {
		if t.kind == tokRedirect && strings.HasPrefix(t.value, "<<") {
			return true
		}
	}
	return false
}

// containsSubstitution reports whether the command contains a backtick or
// `$(...)` command substitution, or a `<(`/`>(` process substitution, outside
// single quotes (bash runs them inside double quotes, so those still count).
// Faithful port of rtk lexer::contains_substitution.
func containsSubstitution(cmd string) bool {
	b := []byte(cmd)
	inSingle, inDouble := false, false
	i := 0
	for i < len(b) {
		c := b[i]
		switch {
		case c == '\\' && !inSingle:
			i += 2
			continue
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case c == '`' && !inSingle:
			return true
		case c == '$' && !inSingle && i+1 < len(b) && b[i+1] == '(':
			return true
		case (c == '<' || c == '>') && !inSingle && !inDouble && i+1 < len(b) && b[i+1] == '(':
			return true
		}
		i++
	}
	return false
}

// containsFileTargetRedirect reports whether the command has a redirect that
// writes to a real file target (`> out.txt`, `>> log`, `2> file`, `>& file`),
// as opposed to an fd-dup/close (`2>&1`, `2>&-`) or the `/dev/null` sink. A real
// file-target redirect makes the command unattestable: the hook must pass it
// through unchanged rather than wrap it. Faithful port of rtk lexer's
// redirect_has_file_target driving contains_unattestable_construct.
func containsFileTargetRedirect(cmd string) bool {
	toks := tokenize(cmd)
	for i, t := range toks {
		if t.kind == tokRedirect && redirectHasFileTarget(toks, i) {
			return true
		}
	}
	return false
}

// redirectHasFileTarget decides whether the redirect token at index i targets a
// file. `>&N`/`>&-`/`N>&M` is fd-dup/close (no file target); `>` or `2>`
// followed by a non-/dev/null argument is a file target.
func redirectHasFileTarget(toks []token, i int) bool {
	value := toks[i].value
	if pos := strings.Index(value, ">&"); pos >= 0 {
		tail := value[pos+2:]
		if tail != "" && allDigitsOrDash(tail) {
			return false // fd dup/close like 2>&1, 2>&-
		}
	}
	if i+1 < len(toks) && toks[i+1].kind == tokArg {
		return toks[i+1].value != "/dev/null"
	}
	return true
}

func allDigitsOrDash(s string) bool {
	for i := 0; i < len(s); i++ {
		if !(s[i] >= '0' && s[i] <= '9') && s[i] != '-' {
			return false
		}
	}
	return true
}

// ── lightweight shell tokenizer ───────────────────────────────────────
//
// A minimal, quote-aware tokenizer sufficient for the rewrite decision: it
// recognizes the boolean operators (&& || ;), the pipe (|), background & and
// redirects, while keeping quoted text intact so operators inside quotes are not
// treated as separators. It is a trimmed port of rtk's discover::lexer::tokenize
// — we only need operator/pipe/redirect/background classification plus byte
// offsets, not the full arg model.

type tokKind int

const (
	tokArg tokKind = iota
	tokOperator
	tokPipe
	tokBackground
	tokRedirect
)

type token struct {
	kind   tokKind
	value  string
	offset int
}

func tokenize(input string) []token {
	var toks []token
	b := []byte(input)
	n := len(b)
	inSingle, inDouble := false, false
	curStart := -1
	flush := func(end int) {
		if curStart >= 0 && end > curStart {
			toks = append(toks, token{kind: tokArg, value: input[curStart:end], offset: curStart})
		}
		curStart = -1
	}
	i := 0
	for i < n {
		c := b[i]
		switch {
		case c == '\\' && !inSingle:
			if curStart < 0 {
				curStart = i
			}
			i += 2
			continue
		case c == '\'' && !inDouble:
			if curStart < 0 {
				curStart = i
			}
			inSingle = !inSingle
			i++
			continue
		case c == '"' && !inSingle:
			if curStart < 0 {
				curStart = i
			}
			inDouble = !inDouble
			i++
			continue
		}
		if inSingle || inDouble {
			if curStart < 0 {
				curStart = i
			}
			i++
			continue
		}

		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f':
			flush(i)
			i++
		case c == '&' && i+1 < n && b[i+1] == '&':
			flush(i)
			toks = append(toks, token{kind: tokOperator, value: "&&", offset: i})
			i += 2
		case c == '|' && i+1 < n && b[i+1] == '|':
			flush(i)
			toks = append(toks, token{kind: tokOperator, value: "||", offset: i})
			i += 2
		case c == ';':
			flush(i)
			toks = append(toks, token{kind: tokOperator, value: ";", offset: i})
			i++
		case c == '|':
			flush(i)
			toks = append(toks, token{kind: tokPipe, value: "|", offset: i})
			i++
		case c == '&':
			// Could be background `&` or the start of a redirect `&>`.
			if i+1 < n && b[i+1] == '>' {
				start := i
				j := i + 1
				for j < n && b[j] == '>' {
					j++
				}
				flush(start)
				toks = append(toks, token{kind: tokRedirect, value: input[start:j], offset: start})
				i = j
			} else {
				flush(i)
				toks = append(toks, token{kind: tokBackground, value: "&", offset: i})
				i++
			}
		case c == '<' || c == '>':
			// Redirect operator, possibly preceded by an fd number that is part of
			// the current arg (e.g. "2>&1"): rewind to capture the leading digits.
			start := i
			if curStart >= 0 {
				// pull the in-progress digit run (if any) into the redirect token
				k := i - 1
				for k >= curStart && b[k] >= '0' && b[k] <= '9' {
					k--
				}
				if k+1 < i { // there were digits
					// flush anything before the digits as an arg
					if k+1 > curStart {
						toks = append(toks, token{kind: tokArg, value: input[curStart : k+1], offset: curStart})
					}
					start = k + 1
					curStart = -1
				} else {
					flush(i)
				}
			}
			j := i
			for j < n && (b[j] == '<' || b[j] == '>' || b[j] == '&' || b[j] == '-') {
				j++
			}
			// Consume an fd target that directly follows `&` (e.g. "2>&1", "2>&-")
			// so the whole fd-dup stays one token and is not mistaken for a file
			// target. A real file target carries a space before its name.
			if j > i && b[j-1] == '&' {
				for j < n && b[j] >= '0' && b[j] <= '9' {
					j++
				}
			}
			toks = append(toks, token{kind: tokRedirect, value: input[start:j], offset: start})
			i = j
		default:
			if curStart < 0 {
				curStart = i
			}
			i++
		}
	}
	flush(n)
	return toks
}

// ── command: rewrite ──────────────────────────────────────────────────

// RunRewrite implements `gortk rewrite <raw command...>`. The trailing args are
// joined into one command line (so both `gortk rewrite "git status"` and
// `gortk rewrite git status` work). On a supported command it prints the rewrite
// to stdout and returns exit 0; otherwise it prints nothing and returns exit 1,
// so a shell hook can do `REWRITTEN=$(gortk rewrite "$CMD") || exit 0`.
func RunRewrite(args []string, verbose int) (int, error) {
	raw := strings.Join(args, " ")
	excluded := core.LoadConfig().Hooks.ExcludeCommands
	rewritten, ok := Rewrite(raw, excluded, gortkSupports)
	if !ok {
		return 1, nil
	}
	// print() without trailing newline so callers capture exactly the rewrite.
	fmt.Print(rewritten)
	return 0, nil
}

// ── command: hook ─────────────────────────────────────────────────────

// RunHook dispatches the `hook` subcommands. "claude" handles the Claude Code
// PreToolUse hook; "copilot" handles the GitHub Copilot preToolUse hook (VS Code
// Copilot Chat + Copilot CLI, auto-detected). Other agents are out of scope for
// this Windows port.
func RunHook(args []string, verbose int) (int, error) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "gortk hook: missing subcommand (expected: claude or copilot)")
		return 2, nil
	}
	switch args[0] {
	case "claude":
		return runHookClaude(os.Stdin, os.Stdout, gortkSupports)
	case "copilot":
		return runHookCopilot(os.Stdin, os.Stdout, gortkSupports)
	default:
		fmt.Fprintf(os.Stderr, "gortk hook: unknown subcommand %q (expected: claude or copilot)\n", args[0])
		return 2, nil
	}
}

// runHookClaude reads a Claude Code PreToolUse hook JSON object from r, rewrites
// the Bash command via the shared Rewrite logic, and writes the Claude hook
// response JSON to w. On any problem (non-Bash tool, missing/empty command, no
// rewrite, malformed JSON) it emits nothing (pass-through) and returns 0 — it
// must never crash or block the user's agent. supported is injectable so the
// plumbing can be tested without depending on which command packages are
// compiled into the test binary.
func runHookClaude(r io.Reader, w io.Writer, supported supportedFunc) (int, error) {
	input, err := readLimited(r)
	if err != nil {
		// Could not read stdin — pass through silently.
		return 0, nil
	}
	resp, ok := ClaudeHookResponse(input, core.LoadConfig().Hooks.ExcludeCommands, supported)
	if !ok {
		return 0, nil // pass-through: write nothing
	}
	// writeln-style: Claude reads one JSON object; a trailing newline is fine.
	fmt.Fprintln(w, resp)
	return 0, nil
}

// claudeHookInput is the subset of the Claude Code PreToolUse payload we read.
// Unknown fields are ignored; ToolInput is captured raw so we can echo back the
// caller's other fields (timeout, description, ...) untouched in updatedInput.
type claudeHookInput struct {
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// ClaudeHookResponse computes the Claude PreToolUse hook response JSON for a raw
// payload. It returns (json, true) when a rewrite applies, or ("", false) for
// every pass-through case (non-Bash tool, no command, no rewrite, malformed
// input). Pure and total: it never panics and performs no I/O, so it is the unit
// under test for the hook path.
func ClaudeHookResponse(payload []byte, excluded []string, supported supportedFunc) (string, bool) {
	// Strip any leading UTF-8 BOM(s) some Windows hosts prepend, then trim.
	payload = stripLeadingBOM(payload)
	if len(payloadTrimmed(payload)) == 0 {
		return "", false
	}

	var in claudeHookInput
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
	// only "command", so fields like timeout/description survive unchanged.
	updated := map[string]json.RawMessage{}
	if len(in.ToolInput) > 0 {
		_ = json.Unmarshal(in.ToolInput, &updated) // best-effort; empty on non-object
	}
	cmdJSON, _ := json.Marshal(rewritten)
	updated["command"] = cmdJSON

	resp := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
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

// commandFromToolInput pulls the "command" string field out of a raw tool_input
// object, tolerating any shape (returns ok=false on non-object / missing field).
func commandFromToolInput(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", false
	}
	cmdRaw, ok := obj["command"]
	if !ok {
		return "", false
	}
	var cmd string
	if err := json.Unmarshal(cmdRaw, &cmd); err != nil {
		return "", false
	}
	return cmd, true
}

func isBashTool(name string) bool {
	switch name {
	case "Bash", "bash":
		return true
	default:
		return false
	}
}

// readLimited reads up to stdinCap+1 bytes and errors if the cap is exceeded,
// mirroring rtk's bounded stdin read.
func readLimited(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, stdinCap+1))
	if err != nil {
		return nil, err
	}
	if len(data) > stdinCap {
		return nil, fmt.Errorf("hook stdin exceeds %d byte limit", stdinCap)
	}
	return data, nil
}

// stripLeadingBOM removes one or more leading UTF-8 BOMs (EF BB BF).
func stripLeadingBOM(b []byte) []byte {
	for len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		b = b[3:]
	}
	return b
}

func payloadTrimmed(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
