// helpers.go provides the shell-exec plumbing shared by the execwrap commands:
// a quote-aware tokenizer (shellSplit) and a native-Windows shell command
// builder (buildShellCommand). Ported from rtk's discover::lexer::shell_split
// and src/cmds/rust/runner.rs `build_shell_command`.
//
// Native Windows: buildShellCommand runs the `-c "<string>"` form via
// `cmd /C <string>` (NOT `sh -c`), resolved PATHEXT-aware through
// core.ResolvedCommand.
package execwrap

import (
	"os/exec"

	"gortk/internal/core"
)

// shellSplit splits a string into shell-like tokens, respecting single and
// double quotes and backslash escapes outside single quotes. Faithful port of
// rtk's discover::lexer::shell_split:
//
//	`git log --format="%H %s"` → ["git", "log", "--format=%H %s"]
//	`grep -r 'hello world' .`  → ["grep", "-r", "hello world", "."]
//
// A trailing unclosed quote is tolerated (its contents become the final token),
// matching the Rust behaviour. Quote characters are consumed, not preserved.
func shellSplit(input string) []string {
	var tokens []string
	var current []rune
	inSingle := false
	inDouble := false

	runes := []rune(input)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case c == '\\' && !inSingle:
			// Backslash escape (outside single quotes): consume the next rune
			// literally. A trailing backslash is dropped, matching Rust's
			// `if let Some(next) = chars.next()`.
			if i+1 < len(runes) {
				i++
				current = append(current, runes[i])
			}
		case c == '\'' && !inDouble:
			inSingle = !inSingle
		case c == '"' && !inSingle:
			inDouble = !inDouble
		case (c == ' ' || c == '\t') && !inSingle && !inDouble:
			if len(current) > 0 {
				tokens = append(tokens, string(current))
				current = nil
			}
		default:
			current = append(current, c)
		}
	}

	if len(current) > 0 {
		tokens = append(tokens, string(current))
	}

	return tokens
}

// buildShellCommand builds an *exec.Cmd that runs command through the native
// shell. On Windows that is `cmd /C <command>` (the contract bans `sh -c`); the
// shell binary is resolved PATHEXT-aware via core.ResolvedCommand. Mirrors
// rtk's build_shell_command (its #[cfg(target_os = "windows")] arm).
func buildShellCommand(command string) *exec.Cmd {
	return core.ResolvedCommand("cmd", "/C", command)
}
