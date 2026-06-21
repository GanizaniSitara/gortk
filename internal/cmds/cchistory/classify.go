package cchistory

import (
	"path/filepath"
	"strings"

	"gortk/internal/registry"
	"gortk/internal/tomlfilter"
)

// Classification is the gortk-coverage verdict for a single shell command.
// Where rtk consulted a 3000-line static RULES table, gortk asks its own command
// registry and TOML filter engine instead — so the four states collapse to:
//
//   - Supported via a registered command (registry.Lookup hit), or
//   - Supported via a matching builtin TOML filter (tomlfilter.FindMatching hit), or
//   - AlreadyGortk (the command already invokes "gortk ..."), or
//   - Ignored (shell builtins / navigation with no optimization to offer), or
//   - Unsupported (a real external command gortk has no filter for).
type Classification int

const (
	// Unsupported: a real command gortk currently offers no filter for.
	Unsupported Classification = iota
	// SupportedByCommand: gortk has a dedicated command (registry.Lookup matched
	// the first-token basename).
	SupportedByCommand
	// SupportedByFilter: no dedicated command, but a builtin TOML filter's
	// match_command regex matches the command string.
	SupportedByFilter
	// AlreadyGortk: the command already starts with "gortk ".
	AlreadyGortk
	// Ignored: shell builtins, navigation, and trivially-empty commands that
	// gortk would never wrap (cd, echo, export, ...).
	Ignored
)

// Supported reports whether c is one of the two "gortk could optimize this" states.
func (c Classification) Supported() bool {
	return c == SupportedByCommand || c == SupportedByFilter
}

// ignoredExact are whole commands that carry no optimization opportunity.
// (ls is deliberately absent — it is a real gortk command.)
var ignoredExact = map[string]bool{
	"cd": true, "pwd": true, "clear": true, "exit": true,
	"true": true, "false": true,
}

// ignoredFirstTokens are leading basenames that gortk never wraps — shell
// builtins, navigation, and assignment/flow constructs. These are skipped before
// consulting the registry/filters so they don't pollute the "unsupported" list.
var ignoredFirstTokens = map[string]bool{
	"cd": true, "pwd": true, "echo": true, "export": true, "set": true,
	"unset": true, "source": true, "alias": true, "clear": true, "exit": true,
	"true": true, "false": true, "test": true, "read": true, "wait": true,
	"sleep": true, "exec": true, "eval": true, "trap": true, "return": true,
	"mkdir": true, "rmdir": true, "touch": true, "rm": true, "cp": true,
	"mv": true, "ln": true, "chmod": true, "chown": true, "kill": true,
	"pushd": true, "popd": true, "umask": true,
}

// envAssignToken reports whether tok looks like a leading env assignment
// (NAME=value) that precedes the real command, e.g. "RUST_BACKTRACE=1".
func envAssignToken(tok string) bool {
	eq := strings.IndexByte(tok, '=')
	if eq <= 0 {
		return false
	}
	for i := 0; i < eq; i++ {
		c := tok[i]
		isUpper := c >= 'A' && c <= 'Z'
		isLower := c >= 'a' && c <= 'z'
		isDigit := c >= '0' && c <= '9'
		if !(isUpper || isLower || isDigit || c == '_') {
			return false
		}
	}
	return true
}

// firstToken returns the command's first meaningful token, skipping leading env
// assignments and a leading "sudo"/"env". The returned token is basename-reduced
// so "/usr/bin/grep" and "C:\\tools\\git.exe" classify by their bare name.
func firstToken(cmd string) string {
	fields := strings.Fields(cmd)
	i := 0
	for i < len(fields) {
		tok := fields[i]
		if tok == "sudo" || tok == "env" || envAssignToken(tok) {
			i++
			continue
		}
		break
	}
	if i >= len(fields) {
		return ""
	}
	return basename(fields[i])
}

// basename strips any directory prefix (Unix '/' or Windows '\\') and a trailing
// ".exe"/".cmd"/".bat" so the result matches gortk command names. Mirrors rtk's
// strip_absolute_path normalization, but native to Windows too.
func basename(tok string) string {
	tok = strings.TrimSuffix(tok, "\"")
	tok = strings.TrimPrefix(tok, "\"")
	// Normalize Windows separators so filepath.Base works regardless of host.
	tok = strings.ReplaceAll(tok, "\\", "/")
	tok = filepath.Base(tok)
	lower := strings.ToLower(tok)
	for _, ext := range []string{".exe", ".cmd", ".bat", ".ps1"} {
		if strings.HasSuffix(lower, ext) {
			return tok[:len(tok)-len(ext)]
		}
	}
	return tok
}

// Classify decides how gortk would treat one already-split shell command,
// consulting gortk's own command registry and TOML filter engine rather than a
// static rule table. The command should be a single segment (no &&/;/| chains);
// use SplitChain first.
func Classify(cmd string) Classification {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return Ignored
	}
	if trimmed == "gortk" || strings.HasPrefix(trimmed, "gortk ") {
		return AlreadyGortk
	}

	tok := firstToken(trimmed)
	if tok == "" {
		return Ignored
	}
	if ignoredFirstTokens[tok] && !registryHasReal(tok) {
		return Ignored
	}
	if ignoredExact[trimmed] {
		return Ignored
	}

	// 1. Does gortk have a dedicated command for this tool?
	if _, ok := registry.Lookup(tok); ok {
		return SupportedByCommand
	}

	// 2. Does a builtin TOML filter match the command string? rtk's match_command
	//    regexes anchor on the command, so feed the normalized command (basename
	//    first token + the rest) to FindMatching.
	if tomlfilter.FindMatching(normalizedForFilter(trimmed, tok)) != nil {
		return SupportedByFilter
	}

	return Unsupported
}

// registryHasReal reports whether tok is a registered gortk command. Used so an
// entry in ignoredFirstTokens (e.g. "read") that ALSO happens to be a real gortk
// command is not wrongly ignored.
func registryHasReal(tok string) bool {
	_, ok := registry.Lookup(tok)
	return ok
}

// normalizedForFilter rebuilds the command with its first token reduced to a
// basename, so a filter whose match_command is e.g. `^make\b` still matches a
// "/usr/bin/make all" invocation.
func normalizedForFilter(cmd, basenameTok string) string {
	fields := strings.Fields(cmd)
	// Find the index of the first non-prefix token and replace it with basename.
	i := 0
	for i < len(fields) {
		t := fields[i]
		if t == "sudo" || t == "env" || envAssignToken(t) {
			i++
			continue
		}
		break
	}
	if i >= len(fields) {
		return cmd
	}
	fields[i] = basenameTok
	return strings.Join(fields[i:], " ")
}

// BaseCommand returns a display name for a command: its first token, plus a
// second token when that second token looks like a subcommand (no leading '-',
// '/', or '.'). Mirrors rtk's discover::registry::extract_base_command, used for
// grouping unsupported commands and learn rules.
func BaseCommand(cmd string) string {
	fields := strings.Fields(strings.TrimSpace(cmd))
	// Skip env prefixes for the base name.
	i := 0
	for i < len(fields) {
		t := fields[i]
		if t == "sudo" || t == "env" || envAssignToken(t) {
			i++
			continue
		}
		break
	}
	fields = fields[i:]
	switch len(fields) {
	case 0:
		return ""
	case 1:
		return fields[0]
	default:
		second := fields[1]
		if !strings.HasPrefix(second, "-") && !strings.Contains(second, "/") && !strings.Contains(second, ".") {
			return fields[0] + " " + second
		}
		return fields[0]
	}
}

// SplitChain splits a shell command on top-level &&, ||, ; and | operators,
// returning each segment trimmed. Operators inside single/double quotes are not
// split points. A heredoc (<<) disables splitting (the whole command is one
// segment), matching rtk's conservative behavior. This is a pragmatic
// stdlib-only reimplementation of rtk's lexer split_command_chain — it does not
// need the full lexer because the analytics commands only need approximate
// segmentation to count and classify.
func SplitChain(cmd string) []string {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return nil
	}
	if strings.Contains(trimmed, "<<") {
		return []string{trimmed}
	}

	var segments []string
	var cur strings.Builder
	var quote byte // 0, '\'' or '"'
	runes := []byte(trimmed)
	flush := func() {
		s := strings.TrimSpace(cur.String())
		if s != "" {
			segments = append(segments, s)
		}
		cur.Reset()
	}
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		if quote != 0 {
			cur.WriteByte(c)
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
			cur.WriteByte(c)
		case '&':
			if i+1 < len(runes) && runes[i+1] == '&' {
				flush()
				i++
			} else {
				cur.WriteByte(c)
			}
		case '|':
			if i+1 < len(runes) && runes[i+1] == '|' {
				i++
			}
			flush()
		case ';':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	if len(segments) == 0 {
		return []string{trimmed}
	}
	return segments
}
