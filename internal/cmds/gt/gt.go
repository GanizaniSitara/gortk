// Package gt is gortk's token-optimized wrapper around the Graphite (gt) CLI.
// It compresses the verbose output of stacking workflows — log, submit, sync,
// restack, create, branch — into compact summaries, and passes everything else
// through (routing git-style subcommands to gortk's git filters for token
// savings). Faithful port of rtk's src/cmds/git/gt_cmd.rs.
package gt

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gortk/internal/cmds/git"
	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "gt",
		Summary: "Graphite (gt) stacking workflows with token-optimized output",
		Run:     Run,
	})
}

var (
	emailRE      = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)
	branchNameRE = regexp.MustCompile(`(?:Created|Pushed|pushed|Deleted|deleted)\s+branch\s+["'` + "`" + `]?([a-zA-Z0-9/_.\-+@]+)`)
	prLineRE     = regexp.MustCompile(`(Created|Updated)\s+pull\s+request\s+#(\d+)\s+for\s+([^\s:]+)(?::\s*(\S+))?`)
)

// gt log entries are multi-line — trim the list cap to keep token savings above 60%.
var maxLogEntries = core.Reduced(core.CapList, 5)

// Run dispatches the gortk `gt` command. args are the tokens after "gt"; verbose
// is the -v count. It returns the wrapped tool's exit code.
//
// The explicit subcommands (log/submit/sync/restack/create/branch) are filtered;
// any other subcommand is treated as a passthrough, mirroring how gt itself
// forwards unknown subcommands to git.
func Run(args []string, verbose int) (int, error) {
	if len(args) == 0 {
		return runOther(args, verbose)
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "log":
		return runLog(rest, verbose)
	case "submit":
		return runGtFiltered([]string{"submit"}, rest, verbose, "gt_submit", filterGtSubmit)
	case "sync":
		return runGtFiltered([]string{"sync"}, rest, verbose, "gt_sync", filterGtSync)
	case "restack":
		return runGtFiltered([]string{"restack"}, rest, verbose, "gt_restack", filterGtRestack)
	case "create":
		return runGtFiltered([]string{"create"}, rest, verbose, "gt_create", filterGtCreate)
	case "branch":
		return runGtFiltered([]string{"branch"}, rest, verbose, "gt_branch", filterIdentity)
	default:
		return runOther(args, verbose)
	}
}

func runLog(args []string, verbose int) (int, error) {
	if len(args) > 0 {
		switch args[0] {
		case "short":
			return runGtFiltered([]string{"log", "short"}, args[1:], verbose, "gt_log_short", filterIdentity)
		case "long":
			return runGtFiltered([]string{"log", "long"}, args[1:], verbose, "gt_log_long", filterGtLogEntries)
		}
	}
	return runGtFiltered([]string{"log"}, args, verbose, "gt_log", filterGtLogEntries)
}

// runOther handles non-filtered subcommands. gt forwards unknown subcommands to
// git, so "gt status" behaves like "git status"; route known git subcommands to
// gortk's git filters for token savings, and pass everything else straight
// through to gt.
func runOther(args []string, verbose int) (int, error) {
	if len(args) == 0 {
		return 1, fmt.Errorf("gt: no subcommand specified")
	}

	sub := args[0]
	rest := args[1:]

	switch sub {
	case "status", "diff", "show", "add", "push", "pull", "fetch", "stash", "worktree":
		// Reuse gortk's git filters: git.Run dispatches on the subcommand token.
		gitArgs := append([]string{sub}, rest...)
		return git.Run(gitArgs, verbose)
	default:
		return core.RunPassthrough("gt", args, verbose)
	}
}

// runGtFiltered runs `gt <subcmd...> <args...>`, captures the output, strips ANSI
// from stdout, applies filter, and prints the compact result. Mirrors rtk's
// run_gt_filtered. In verbose mode the cleaned (unfiltered) output is emitted.
func runGtFiltered(subcmd, args []string, verbose int, teeLabel string, filter func(string) string) (int, error) {
	cmd := core.ResolvedCommand("gt")
	cmd.Args = append(cmd.Args, subcmd...)
	cmd.Args = append(cmd.Args, args...)

	subcmdStr := strings.Join(subcmd, " ")
	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: gt %s %s\n", subcmdStr, strings.Join(args, " "))
	}

	argsDisplay := strings.TrimSpace(subcmdStr + " " + strings.Join(args, " "))
	opts := core.RunOptions{TeeLabel: teeLabel, FilterStdoutOnly: true, InheritStdin: true}
	return core.RunFiltered(cmd, "gt", argsDisplay, func(raw string) string {
		clean := core.StripANSI(strings.TrimSpace(raw))
		if verbose > 0 {
			return clean
		}
		return filter(clean)
	}, opts)
}

func filterIdentity(input string) string {
	return input
}

// lines mirrors Rust's str::lines(): split on '\n' and drop a single trailing
// empty element so a trailing newline does not produce a phantom blank line.
func lines(s string) []string {
	out := strings.Split(s, "\n")
	if n := len(out); n > 0 && out[n-1] == "" {
		out = out[:n-1]
	}
	return out
}

// filterGtLogEntries compacts `gt log` output: strips emails, truncates each line
// to 120 chars, and caps the number of graph entries.
func filterGtLogEntries(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}

	ls := lines(trimmed)
	var result []string
	entryCount := 0

	for i, line := range ls {
		if isGraphNode(line) {
			entryCount++
		}

		replaced := emailRE.ReplaceAllString(line, "")
		processed := truncate(strings.TrimRight(replaced, " \t"), 120)
		result = append(result, processed)

		if entryCount >= maxLogEntries {
			remaining := 0
			for _, l := range ls[i+1:] {
				if isGraphNode(l) {
					remaining++
				}
			}
			if remaining > 0 {
				result = append(result, fmt.Sprintf("... +%d more entries", remaining))
			}
			break
		}
	}

	return strings.Join(result, "\n")
}

// filterGtSubmit compacts `gt submit` output into pushed-branch and PR summaries.
func filterGtSubmit(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}

	var pushed []string
	var prs []string

	for _, line := range lines(trimmed) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if strings.Contains(line, "pushed") || strings.Contains(line, "Pushed") {
			pushed = append(pushed, extractBranchName(line))
		} else if caps := prLineRE.FindStringSubmatch(line); caps != nil {
			action := strings.ToLower(caps[1])
			num := caps[2]
			branch := caps[3]
			if caps[4] != "" {
				prs = append(prs, fmt.Sprintf("%s PR #%s %s %s", action, num, branch, caps[4]))
			} else {
				prs = append(prs, fmt.Sprintf("%s PR #%s %s", action, num, branch))
			}
		}
	}

	var summary []string

	if len(pushed) > 0 {
		var branchNames []string
		for _, s := range pushed {
			if s != "" {
				branchNames = append(branchNames, s)
			}
		}
		if len(branchNames) > 0 {
			summary = append(summary, "pushed "+strings.Join(branchNames, ", "))
		} else {
			summary = append(summary, fmt.Sprintf("pushed %d branches", len(pushed)))
		}
	}

	summary = append(summary, prs...)

	if len(summary) == 0 {
		return truncate(trimmed, 200)
	}

	return strings.Join(summary, "\n")
}

// filterGtSync compacts `gt sync` output into synced/deleted counts.
func filterGtSync(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}

	synced := 0
	deleted := 0
	var deletedNames []string

	for _, line := range lines(trimmed) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		if (strings.Contains(line, "Synced") && strings.Contains(line, "branch")) ||
			strings.HasPrefix(line, "Synced with remote") {
			synced++
		}
		if strings.Contains(line, "deleted") || strings.Contains(line, "Deleted") {
			deleted++
			name := extractBranchName(line)
			if name != "" {
				deletedNames = append(deletedNames, name)
			}
		}
	}

	var parts []string

	if synced > 0 {
		parts = append(parts, fmt.Sprintf("%d synced", synced))
	}

	if deleted > 0 {
		if len(deletedNames) == 0 {
			parts = append(parts, fmt.Sprintf("%d deleted", deleted))
		} else {
			parts = append(parts, fmt.Sprintf("%d deleted (%s)", deleted, strings.Join(deletedNames, ", ")))
		}
	}

	if len(parts) == 0 {
		return okConfirmation("synced", "")
	}

	return "ok sync: " + strings.Join(parts, ", ")
}

// filterGtRestack compacts `gt restack` output into a restacked-branch count.
func filterGtRestack(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}

	restacked := 0
	for _, line := range lines(trimmed) {
		line = strings.TrimSpace(line)
		if (strings.Contains(line, "Restacked") || strings.Contains(line, "Rebased")) && strings.Contains(line, "branch") {
			restacked++
		}
	}

	if restacked > 0 {
		return okConfirmation("restacked", fmt.Sprintf("%d branches", restacked))
	}
	return okConfirmation("restacked", "")
}

// filterGtCreate compacts `gt create` output into the created branch name.
func filterGtCreate(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}

	branchName := ""
	for _, line := range lines(trimmed) {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "Created") || strings.Contains(line, "created") {
			branchName = extractBranchName(line)
			break
		}
	}

	if branchName == "" {
		firstLine := ""
		if ls := lines(trimmed); len(ls) > 0 {
			firstLine = ls[0]
		}
		return okConfirmation("created", strings.TrimSpace(firstLine))
	}
	return okConfirmation("created", branchName)
}

// isGraphNode reports whether a line is a Graphite stack-graph commit node, after
// stripping leading vertical-bar gutters.
func isGraphNode(line string) bool {
	stripped := strings.TrimLeft(line, "│|")
	stripped = strings.TrimLeft(stripped, " \t")
	for _, prefix := range []string{"◉", "○", "◯", "◆", "●", "@", "*"} {
		if strings.HasPrefix(stripped, prefix) {
			return true
		}
	}
	return false
}

// extractBranchName pulls the branch name out of a "Created/Pushed/Deleted branch
// <name>" line, or returns "" when none is present.
func extractBranchName(line string) string {
	if caps := branchNameRE.FindStringSubmatch(line); caps != nil {
		return caps[1]
	}
	return ""
}

// truncate shortens s to max_len runes, appending "..." when it overflows.
// Mirrors rtk's core::utils::truncate (rune-aware).
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

// okConfirmation formats a write-operation confirmation: "ok <action>" or
// "ok <action> <detail>". Mirrors rtk's core::utils::ok_confirmation.
func okConfirmation(action, detail string) string {
	if detail == "" {
		return "ok " + action
	}
	return "ok " + action + " " + detail
}
