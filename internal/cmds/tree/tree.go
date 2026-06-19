// Package tree is gortk's token-optimized directory tree. It proxies to the
// native `tree` command and filters the output to reduce token usage while
// preserving structure visibility. Faithful port of rtk's
// src/cmds/system/tree.rs.
//
// Token optimization: noise directories are excluded via a `-I` pattern unless
// the user passes -a (show all) or supplies their own -I/--ignore. Like rtk,
// this wraps the platform `tree`; gortk resolves it PATHEXT-aware via
// core.ResolvedCommand. The compression logic lives in the pure helper
// filterTreeOutput so it can be tested directly against the ported Rust spec.
package tree

import (
	"fmt"
	"os"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "tree",
		Summary: "Directory tree with token-optimized output",
		Run:     Run,
	})
}

// Run executes the tree command. args are the tokens after "tree"; verbose is
// the -v count. It returns the wrapped process exit code.
func Run(args []string, verbose int) (int, error) {
	if !core.ToolExists("tree") {
		return 1, fmt.Errorf("tree command not found. Install it first:\n" +
			" - macOS: brew install tree\n" +
			" - Ubuntu/Debian: sudo apt install tree\n" +
			" - Fedora/RHEL: sudo dnf install tree\n" +
			" - Arch: sudo pacman -S tree")
	}

	cmd := core.ResolvedCommand("tree")

	showAll := false
	hasIgnore := false
	for _, a := range args {
		if a == "-a" || a == "--all" {
			showAll = true
		}
		if a == "-I" || strings.HasPrefix(a, "--ignore=") {
			hasIgnore = true
		}
	}

	if !showAll && !hasIgnore {
		ignorePattern := strings.Join(core.NoiseDirs, "|")
		cmd.Args = append(cmd.Args, "-I", ignorePattern)
	}

	cmd.Args = append(cmd.Args, args...)

	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true, NoTrailingNewline: true}
	return core.RunFiltered(cmd, "tree", strings.Join(args, " "), func(raw string) string {
		filtered := filterTreeOutput(raw)
		if verbose > 0 {
			rawLines := lineCount(raw)
			filteredLines := lineCount(filtered)
			reduction := 0
			if rawLines > 0 {
				reduction = 100 - (filteredLines * 100 / rawLines)
			}
			fmt.Fprintf(os.Stderr, "Lines: %d → %d (%d%% reduction)\n", rawLines, filteredLines, reduction)
		}
		return filtered
	}, opts)
}

// lineCount mirrors Rust's str::lines().count(): the number of LF-separated
// lines, where a trailing LF does not introduce a phantom final line.
func lineCount(s string) int {
	return len(splitLines(s))
}

// splitLines mirrors Rust's str::lines(): splits on '\n' and drops a single
// trailing empty element so a final newline does not yield a phantom blank
// line. CRLF is normalized to LF first.
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}

// filterTreeOutput strips tree's trailing "N directories, M files" summary line
// and trailing blank lines, preserving the box-drawing structure. Pure function
// — the behavioural heart of the command.
func filterTreeOutput(raw string) string {
	lines := splitLines(raw)

	if len(lines) == 0 {
		return "\n"
	}

	var filtered []string
	for _, line := range lines {
		// Skip the final summary line (e.g., "5 directories, 23 files").
		if strings.Contains(line, "director") && strings.Contains(line, "file") {
			continue
		}

		// Skip empty lines at the start (before any real content).
		if strings.TrimSpace(line) == "" && len(filtered) == 0 {
			continue
		}

		filtered = append(filtered, line)
	}

	// Remove trailing empty lines.
	for len(filtered) > 0 && strings.TrimSpace(filtered[len(filtered)-1]) == "" {
		filtered = filtered[:len(filtered)-1]
	}

	return strings.Join(filtered, "\n") + "\n"
}
