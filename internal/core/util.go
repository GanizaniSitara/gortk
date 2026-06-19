// Package core holds the building blocks shared across all gortk command
// modules: process execution, output capture, token-savings tracking, the
// source-code comment filter, and assorted text helpers.
//
// gortk is a Go port of rtk (https://github.com/rtk-ai/rtk). It is offline by
// default: it makes no network calls of its own (no telemetry, no update
// checks). The only processes it ever spawns are the dev tools the user
// explicitly asked it to wrap.
package core

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
)

// IsTerminal reports whether f is connected to a character device (a terminal),
// using only the standard library so gortk needs no terminal dependency.
func IsTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// StripANSI removes ANSI SGR/CSI escape sequences from text.
func StripANSI(text string) string {
	return ansiRE.ReplaceAllString(text, "")
}

// NormalizeNewlines converts Windows CRLF and lone CR line endings to LF so the
// line-oriented filters behave identically regardless of the platform the
// wrapped tool emits for.
func NormalizeNewlines(text string) string {
	if !strings.ContainsRune(text, '\r') {
		return text
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(text, "\r", "\n")
}

// ResolvedCommand builds an *exec.Cmd for the named tool, resolving it through
// PATH. On Windows exec.LookPath is PATHEXT-aware, so "ls", "git" and friends
// resolve to ls.exe / git.exe / *.cmd shims transparently. If resolution fails
// we fall back to letting the OS resolve the bare name at spawn time, matching
// rtk's resolved_command behaviour.
func ResolvedCommand(name string, args ...string) *exec.Cmd {
	if path, err := exec.LookPath(name); err == nil {
		return exec.Command(path, args...)
	}
	return exec.Command(name, args...)
}

// ToolExists reports whether a tool is resolvable on PATH (PATHEXT-aware on
// Windows).
func ToolExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// ExitCodeFromError extracts the process exit code from the error returned by
// (*exec.Cmd).Run/Output. Returns 0 on success, the real code on a normal
// non-zero exit, and 127 when the binary could not be started.
func ExitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if code := ee.ExitCode(); code >= 0 {
			return code
		}
		return 1 // terminated by signal
	}
	return 127 // command not found / failed to start
}

// EstimateTokens approximates the token count of a string. We use the common
// ~4-characters-per-token heuristic; gortk only needs relative savings figures,
// not a real tokenizer.
func EstimateTokens(s string) int {
	return (len(s) + 3) / 4
}

// FormatCount renders a count with K/M suffixes for compact display
// (e.g. 1234 -> "1.2K", 2_500_000 -> "2.5M").
func FormatCount(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
