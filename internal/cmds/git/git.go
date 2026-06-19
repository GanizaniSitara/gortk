// Package git is gortk's token-optimized git wrapper. It filters git output —
// log, status, diff, show, and more — keeping just the essential info an agent
// needs. Faithful port of rtk's src/cmds/git/git.rs (and the top-level `diff`
// command from src/cmds/git/diff_cmd.rs, which lives in diff.go).
//
// Like rtk, this wraps the platform `git`; gortk resolves it PATHEXT-aware via
// core.ResolvedCommand. The output-compression logic lives in pure helper
// functions (compactDiff, filterLogOutput, formatStatusOutput, …) so it can be
// tested directly against the ported Rust spec.
package git

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "git",
		Summary: "Git commands with compact output",
		Run:     Run,
	})
	registry.Register(&registry.Cmd{
		Name:    "diff",
		Summary: "Ultra-condensed diff of two files (only changed lines)",
		Run:     RunDiff,
	})
}

// maxRemoteBranches mirrors rtk's CAP_WARNINGS (core.CapWarnings = 10).
const maxRemoteBranches = core.CapWarnings

// capResult mirrors rtk's core::stream::CaptureResult. We capture the child's
// output rather than streaming it so each subcommand can inspect it and decide
// what compact form to emit, exactly as exec_capture does in rtk.
type capResult struct {
	stdout   string
	stderr   string
	exitCode int
	startErr error
}

func (r capResult) success() bool  { return r.exitCode == 0 }
func (r capResult) combined() string { return r.stdout + r.stderr }

// execCapture runs cmd with stdin detached, captures stdout/stderr, and
// normalizes newlines. Mirrors rtk's exec_capture.
func execCapture(cmd *exec.Cmd) capResult {
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	cmd.Stdin = nil
	err := cmd.Run()
	return capResult{
		stdout:   core.NormalizeNewlines(outBuf.String()),
		stderr:   core.NormalizeNewlines(errBuf.String()),
		exitCode: core.ExitCodeFromError(err),
		startErr: startErr(err),
	}
}

func startErr(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return nil
	}
	return err
}

// gitCmd builds a git Command with global options (e.g. -C, -c, --git-dir,
// --work-tree) prepended before any subcommand arguments.
func gitCmd(globalArgs []string, args ...string) *exec.Cmd {
	cmd := core.ResolvedCommand("git")
	cmd.Args = append(cmd.Args, globalArgs...)
	cmd.Args = append(cmd.Args, args...)
	return cmd
}

// gitCmdCLocale builds a git Command for internal parsing that must be
// locale-stable (gortk depends on git's English status phrases). User-visible
// passthrough keeps the user's locale.
func gitCmdCLocale(globalArgs []string, args ...string) *exec.Cmd {
	cmd := gitCmd(globalArgs, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	return cmd
}

// Run dispatches the gortk `git` command. args are the tokens after "git";
// verbose is the -v count. It parses the global git flags (-C, -c, --git-dir,
// --work-tree, --no-pager, --no-optional-locks, --bare, --literal-pathspecs)
// then the subcommand itself, mirroring rtk's clap dispatch in main.rs.
func Run(args []string, verbose int) (int, error) {
	globalArgs, rest := parseGlobalArgs(args)

	if len(rest) == 0 {
		// No subcommand: pass through bare `git` so it prints its usage.
		return runPassthrough(rest, globalArgs, verbose)
	}

	sub := rest[0]
	subArgs := rest[1:]

	switch sub {
	case "diff":
		return runDiff(subArgs, 500, verbose, globalArgs)
	case "log":
		return runLog(subArgs, verbose, globalArgs)
	case "status":
		return runStatus(subArgs, verbose, globalArgs)
	case "show":
		return runShow(subArgs, 500, verbose, globalArgs)
	case "add":
		return runAdd(subArgs, verbose, globalArgs)
	case "commit":
		return runCommit(subArgs, verbose, globalArgs)
	case "push":
		return runPush(subArgs, verbose, globalArgs)
	case "pull":
		return runPull(subArgs, verbose, globalArgs)
	case "branch":
		return runBranch(subArgs, verbose, globalArgs)
	case "fetch":
		return runFetch(subArgs, verbose, globalArgs)
	case "stash":
		// rtk parses stash's optional subcommand token separately.
		var stashSub *string
		stashArgs := subArgs
		if len(subArgs) > 0 && !strings.HasPrefix(subArgs[0], "-") {
			s := subArgs[0]
			stashSub = &s
			stashArgs = subArgs[1:]
		}
		return runStash(stashSub, stashArgs, verbose, globalArgs)
	case "worktree":
		return runWorktree(subArgs, verbose, globalArgs)
	default:
		// Passthrough: any unsupported git subcommand runs directly.
		return runPassthrough(rest, globalArgs, verbose)
	}
}

// parseGlobalArgs splits the leading global git flags (the ones gortk recognizes
// before the subcommand) from the rest. It mirrors the clap declaration on
// Commands::Git: -C and -c take a value and may repeat; --git-dir / --work-tree
// take a value; --no-pager / --no-optional-locks / --bare / --literal-pathspecs
// are booleans. The first token that is not one of these (or its value) starts
// the subcommand region.
func parseGlobalArgs(args []string) (global, rest []string) {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-C" || a == "-c":
			if i+1 < len(args) {
				global = append(global, a, args[i+1])
				i += 2
			} else {
				global = append(global, a)
				i++
			}
		case a == "--git-dir" || a == "--work-tree":
			if i+1 < len(args) {
				global = append(global, a, args[i+1])
				i += 2
			} else {
				global = append(global, a)
				i++
			}
		case strings.HasPrefix(a, "--git-dir="):
			global = append(global, "--git-dir", strings.TrimPrefix(a, "--git-dir="))
			i++
		case strings.HasPrefix(a, "--work-tree="):
			global = append(global, "--work-tree", strings.TrimPrefix(a, "--work-tree="))
			i++
		case a == "--no-pager" || a == "--no-optional-locks" || a == "--bare" || a == "--literal-pathspecs":
			global = append(global, a)
			i++
		default:
			return global, args[i:]
		}
	}
	return global, nil
}

// ---- status path selection -------------------------------------------------

func usesCompactStatusPath(args []string) bool {
	if len(args) == 0 {
		return true
	}
	sawBranch := false
	for _, arg := range args {
		switch arg {
		case "-b", "--branch":
			sawBranch = true
		case "-sb", "-bs":
			return true
		case "-s", "--short":
			// allowed, keeps scanning
		default:
			return false
		}
	}
	return sawBranch
}

// buildStatusArgs returns the git argv (subcommand + flags) for status.
func buildStatusArgs(args []string) []string {
	if usesCompactStatusPath(args) {
		return []string{"status", "--porcelain", "-b"}
	}
	out := append([]string{"status"}, args...)
	return out
}

// ---- compact_diff -----------------------------------------------------------

// compactDiff compresses unified-diff output into a per-file, per-hunk summary.
// It preserves hunk headers (including trailing function context), shows up to
// maxHunkLines change/context lines per hunk, reports skipped counts, and caps
// the total result at maxLines. A recovery hint is appended whenever anything
// was truncated. Faithful port of rtk's compact_diff.
func compactDiff(diff string, maxLines int) string {
	const maxHunkLines = 100

	var result []string
	currentFile := ""
	added := 0
	removed := 0
	inHunk := false
	hunkShown := 0
	hunkSkipped := 0
	wasTruncated := false

	for _, line := range splitLines(diff) {
		switch {
		case strings.HasPrefix(line, "diff --git"):
			// Flush hunk truncation before starting a new file.
			if hunkSkipped > 0 {
				result = append(result, fmt.Sprintf("  ... (%d lines truncated)", hunkSkipped))
				wasTruncated = true
				hunkSkipped = 0
			}
			if currentFile != "" && (added > 0 || removed > 0) {
				result = append(result, fmt.Sprintf("  +%d -%d", added, removed))
			}
			currentFile = "unknown"
			if idx := strings.Index(line, " b/"); idx >= 0 {
				currentFile = line[idx+len(" b/"):]
			}
			result = append(result, "\n"+currentFile)
			added = 0
			removed = 0
			inHunk = false
			hunkShown = 0
		case strings.HasPrefix(line, "@@"):
			// Flush hunk truncation before starting a new hunk.
			if hunkSkipped > 0 {
				result = append(result, fmt.Sprintf("  ... (%d lines truncated)", hunkSkipped))
				wasTruncated = true
				hunkSkipped = 0
			}
			inHunk = true
			hunkShown = 0
			// Preserve the full unified diff hunk header, including trailing
			// function/symbol context after the second @@ marker.
			result = append(result, "  "+line)
		case inHunk:
			switch {
			case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
				added++
				if hunkShown < maxHunkLines {
					result = append(result, "  "+line)
					hunkShown++
				} else {
					hunkSkipped++
				}
			case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
				removed++
				if hunkShown < maxHunkLines {
					result = append(result, "  "+line)
					hunkShown++
				} else {
					hunkSkipped++
				}
			case hunkShown < maxHunkLines && !strings.HasPrefix(line, "\\"):
				// Context line.
				if hunkShown > 0 {
					result = append(result, "  "+line)
					hunkShown++
				}
			}
		}

		if len(result) >= maxLines {
			result = append(result, "\n... (more changes truncated)")
			wasTruncated = true
			break
		}
	}

	// Flush last hunk.
	if hunkSkipped > 0 {
		result = append(result, fmt.Sprintf("  ... (%d lines truncated)", hunkSkipped))
		wasTruncated = true
	}

	if currentFile != "" && (added > 0 || removed > 0) {
		result = append(result, fmt.Sprintf("  +%d -%d", added, removed))
	}

	if wasTruncated {
		result = append(result, "[full diff: gortk git diff --no-compact]")
	}

	return strings.Join(result, "\n")
}

// ---- run_diff ---------------------------------------------------------------

func runDiff(args []string, maxLines, verbose int, globalArgs []string) (int, error) {
	// rtk re-inserts `--` consumed by clap's trailing_var_arg here. gortk does
	// not pre-parse the diff args through a clap layer, so the tokens arrive
	// intact; no restoration is needed.

	wantsStat := false
	wantsNoCompact := false
	for _, arg := range args {
		if arg == "--stat" || arg == "--numstat" || arg == "--shortstat" {
			wantsStat = true
		}
		if arg == "--no-compact" {
			wantsNoCompact = true
		}
	}

	if wantsStat || wantsNoCompact {
		// User wants stat or explicitly no compacting — pass through directly.
		cmdArgs := []string{"diff"}
		for _, arg := range args {
			if arg == "--no-compact" {
				continue // gortk flag, not a git flag
			}
			cmdArgs = append(cmdArgs, arg)
		}
		res := execCapture(gitCmd(globalArgs, cmdArgs...))
		if res.startErr != nil {
			return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
		}
		if !res.success() {
			fmt.Fprintln(os.Stderr, res.stderr)
			return res.exitCode, nil
		}
		fmt.Println(strings.TrimSpace(res.stdout))
		return 0, nil
	}

	// Default gortk behavior: stat first, then compacted diff.
	statArgs := append([]string{"diff", "--stat"}, args...)
	res := execCapture(gitCmd(globalArgs, statArgs...))
	if res.startErr != nil {
		return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
	}
	if !res.success() {
		if strings.TrimSpace(res.stderr) != "" {
			fmt.Fprint(os.Stderr, res.stderr)
		}
		return res.exitCode, nil
	}

	if verbose > 0 {
		fmt.Fprintln(os.Stderr, "Git diff summary:")
	}

	fmt.Println(strings.TrimSpace(res.stdout))

	diffArgs := append([]string{"diff"}, args...)
	diffRes := execCapture(gitCmd(globalArgs, diffArgs...))
	if diffRes.startErr != nil {
		return 127, fmt.Errorf("gortk: failed to run git: %w", diffRes.startErr)
	}
	if diffRes.stdout != "" {
		fmt.Println("\nChanges:")
		fmt.Println(compactDiff(diffRes.stdout, maxLines))
	}

	return 0, nil
}

// ---- run_show ---------------------------------------------------------------

func runShow(args []string, maxLines, verbose int, globalArgs []string) (int, error) {
	wantsStatOnly := false
	wantsFormat := false
	wantsBlobShow := false
	for _, arg := range args {
		if arg == "--stat" || arg == "--numstat" || arg == "--shortstat" {
			wantsStatOnly = true
		}
		if strings.HasPrefix(arg, "--pretty") || strings.HasPrefix(arg, "--format") {
			wantsFormat = true
		}
		if isBlobShowArg(arg) {
			wantsBlobShow = true
		}
	}

	if wantsStatOnly || wantsFormat || wantsBlobShow {
		cmdArgs := append([]string{"show"}, args...)
		res := execCapture(gitCmd(globalArgs, cmdArgs...))
		if res.startErr != nil {
			return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
		}
		if !res.success() {
			fmt.Fprintln(os.Stderr, res.stderr)
			return res.exitCode, nil
		}
		if wantsBlobShow {
			fmt.Print(res.stdout)
		} else {
			fmt.Println(strings.TrimSpace(res.stdout))
		}
		return 0, nil
	}

	// Step 1: one-line commit summary.
	summaryArgs := append([]string{"show", "--no-patch", "--pretty=format:%h %s (%ar) <%an>"}, args...)
	summaryRes := execCapture(gitCmd(globalArgs, summaryArgs...))
	if summaryRes.startErr != nil {
		return 127, fmt.Errorf("gortk: failed to run git: %w", summaryRes.startErr)
	}
	if !summaryRes.success() {
		fmt.Fprintln(os.Stderr, summaryRes.stderr)
		return summaryRes.exitCode, nil
	}
	fmt.Println(strings.TrimSpace(summaryRes.stdout))

	// Step 2: --stat summary.
	statArgs := append([]string{"show", "--stat", "--pretty=format:"}, args...)
	statRes := execCapture(gitCmd(globalArgs, statArgs...))
	statText := strings.TrimSpace(statRes.stdout)
	if statText != "" {
		fmt.Println(statText)
	}

	// Step 3: compacted diff.
	diffArgs := append([]string{"show", "--pretty=format:"}, args...)
	diffRes := execCapture(gitCmd(globalArgs, diffArgs...))
	diffText := strings.TrimSpace(diffRes.stdout)
	if diffText != "" {
		if verbose > 0 {
			fmt.Println("\nChanges:")
		}
		fmt.Println(compactDiff(diffText, maxLines))
	}

	return 0, nil
}

func isBlobShowArg(arg string) bool {
	// Detect `rev:path` style arguments while ignoring flags like
	// `--pretty=format:...`.
	return !strings.HasPrefix(arg, "-") && strings.Contains(arg, ":")
}

// ---- run_log ----------------------------------------------------------------

func runLog(args []string, verbose int, globalArgs []string) (int, error) {
	cmdArgs := []string{"log"}

	hasFormatFlag := false
	for _, arg := range args {
		if strings.HasPrefix(arg, "--oneline") || strings.HasPrefix(arg, "--pretty") || strings.HasPrefix(arg, "--format") {
			hasFormatFlag = true
			break
		}
	}

	hasLimitFlag := false
	for _, arg := range args {
		if (strings.HasPrefix(arg, "-") && len(arg) > 1 && arg[1] >= '0' && arg[1] <= '9') ||
			arg == "-n" || strings.HasPrefix(arg, "--max-count") {
			hasLimitFlag = true
			break
		}
	}

	if !hasFormatFlag {
		cmdArgs = append(cmdArgs, "--pretty=format:%h %s (%ar) <%an>%n%b%n---END---")
	}

	var limit int
	userSetLimit := false
	switch {
	case hasLimitFlag:
		if n, ok := parseUserLimit(args); ok {
			limit = n
		} else {
			limit = 10
		}
		userSetLimit = true
	case hasFormatFlag:
		cmdArgs = append(cmdArgs, "-50")
		limit = 50
	default:
		cmdArgs = append(cmdArgs, "-10")
		limit = 10
	}

	wantsMerges := false
	for _, arg := range args {
		if arg == "--merges" || arg == "--min-parents=2" || arg == "--no-merges" {
			wantsMerges = true
			break
		}
	}
	if !wantsMerges && !hasLimitFlag {
		cmdArgs = append(cmdArgs, "--no-merges")
	}

	cmdArgs = append(cmdArgs, args...)

	res := execCapture(gitCmd(globalArgs, cmdArgs...))
	if res.startErr != nil {
		return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
	}
	if !res.success() {
		fmt.Fprintln(os.Stderr, res.stderr)
		return res.exitCode, nil
	}

	if verbose > 0 {
		fmt.Fprintln(os.Stderr, "Git log output:")
	}

	fmt.Println(filterLogOutput(res.stdout, limit, userSetLimit, hasFormatFlag))
	return 0, nil
}

// parseUserLimit parses the user-specified limit from git log args. Handles
// -20, -n 20, --max-count=20, --max-count 20.
func parseUserLimit(args []string) (int, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		// -20 (combined digit form)
		if strings.HasPrefix(arg, "-") && len(arg) > 1 && arg[1] >= '0' && arg[1] <= '9' {
			if n, err := strconv.Atoi(arg[1:]); err == nil {
				return n, true
			}
		}
		// -n 20 (two-token form)
		if arg == "-n" && i+1 < len(args) {
			if n, err := strconv.Atoi(args[i+1]); err == nil {
				return n, true
			}
		}
		// --max-count=20
		if rest, ok := strings.CutPrefix(arg, "--max-count="); ok {
			if n, err := strconv.Atoi(rest); err == nil {
				return n, true
			}
		}
		// --max-count 20 (two-token form)
		if arg == "--max-count" && i+1 < len(args) {
			if n, err := strconv.Atoi(args[i+1]); err == nil {
				return n, true
			}
		}
	}
	return 0, false
}

// filterLogOutput filters git log output: truncates long messages and caps
// lines. When userSetLimit is true the user explicitly passed -N, so we skip
// line capping (git already returns exactly N commits) and use a wider
// truncation threshold (120 chars) to preserve commit context.
func filterLogOutput(output string, limit int, userSetLimit, userFormat bool) string {
	truncateWidth := 80
	if userSetLimit {
		truncateWidth = 120
	}

	// User-specified format: gortk did not inject ---END--- markers, so use
	// simple line-based truncation.
	if userFormat {
		lines := splitLines(output)
		maxLines := limit
		if userSetLimit {
			maxLines = len(lines)
		}
		if maxLines > len(lines) {
			maxLines = len(lines)
		}
		out := make([]string, 0, maxLines)
		for _, l := range lines[:maxLines] {
			out = append(out, truncateLine(l, truncateWidth))
		}
		return strings.Join(out, "\n")
	}

	// gortk-injected format: split into commit blocks separated by ---END---.
	commits := strings.Split(output, "---END---")
	maxCommits := limit
	if userSetLimit {
		maxCommits = len(commits)
	}
	if maxCommits > len(commits) {
		maxCommits = len(commits)
	}

	var result []string
	for _, block := range commits[:maxCommits] {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		blockLines := splitLines(block)
		if len(blockLines) == 0 {
			continue
		}
		header := truncateLine(strings.TrimSpace(blockLines[0]), truncateWidth)

		var allBodyLines []string
		for _, l := range blockLines[1:] {
			l = strings.TrimSpace(l)
			if l == "" || strings.HasPrefix(l, "Signed-off-by:") || strings.HasPrefix(l, "Co-authored-by:") {
				continue
			}
			allBodyLines = append(allBodyLines, l)
		}
		bodyKept := len(allBodyLines)
		if bodyKept > 3 {
			bodyKept = 3
		}
		bodyOmitted := len(allBodyLines) - bodyKept
		bodyLines := allBodyLines[:bodyKept]

		if len(bodyLines) == 0 {
			result = append(result, header)
		} else {
			var entry strings.Builder
			entry.WriteString(header)
			for _, body := range bodyLines {
				entry.WriteString("\n  ")
				entry.WriteString(truncateLine(body, truncateWidth))
			}
			if bodyOmitted > 0 {
				fmt.Fprintf(&entry, "\n  [+%d lines omitted]", bodyOmitted)
			}
			result = append(result, entry.String())
		}
	}

	return strings.TrimSpace(strings.Join(result, "\n"))
}

// truncateLine truncates a single line to width characters (counted as runes),
// appending "..." if needed. Mirrors rtk's char-based truncation.
func truncateLine(line string, width int) string {
	runes := []rune(line)
	if len(runes) > width {
		return string(runes[:width-3]) + "..."
	}
	return line
}

// splitLines splits text on "\n" and drops a trailing empty element so it
// matches Rust's str::lines() semantics.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// ---- status formatting ------------------------------------------------------

func formatStatusOutput(porcelain string) string {
	return formatStatusInner(porcelain, "")
}

func formatStatusOutputDetached(porcelain, detachedRef string) string {
	return formatStatusInner(porcelain, detachedRef)
}

func formatStatusInner(porcelain, detached string) string {
	var lines []string
	for _, line := range splitLines(porcelain) {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		return "Clean working tree"
	}

	var output []string
	branchLine := lines[0]
	if strings.HasPrefix(branchLine, "##") {
		branch := strings.TrimPrefix(branchLine, "## ")
		display := branch
		if detached != "" {
			display = detached
		}
		output = append(output, "* "+display)
	} else {
		output = append(output, branchLine)
	}

	for _, line := range lines[1:] {
		output = append(output, line)
	}

	if len(lines) == 1 && strings.HasPrefix(lines[0], "##") {
		output = append(output, "clean — nothing to commit")
	}

	return strings.Join(output, "\n")
}

// gitStatusState enumerates the in-progress repo states extracted from plain
// `git status` output.
type gitStatusState int

const (
	stateRebase gitStatusState = iota
	stateMergeConflicts
	stateMergeReadyToCommit
	stateCherryPick
	stateRevert
	stateBisect
	stateAm
	stateSparseCheckout
)

func (s gitStatusState) summary() string {
	switch s {
	case stateRebase:
		return "rebase in progress"
	case stateMergeConflicts:
		return "merge in progress. unresolved conflicts"
	case stateMergeReadyToCommit:
		return "merge in progress. no conflicts"
	case stateCherryPick:
		return "cherry-pick in progress"
	case stateRevert:
		return "revert in progress"
	case stateBisect:
		return "bisect in progress"
	case stateAm:
		return "am session in progress"
	case stateSparseCheckout:
		return "sparse checkout enabled"
	}
	return ""
}

var rebaseIndicators = []string{
	"rebase in progress",
	"You are currently rebasing",
	"You are currently editing",
	"You are currently splitting",
	"Last command done",
	"Next command to do",
	"No commands remaining",
}

func detectStatusState(line string) (gitStatusState, bool) {
	switch {
	case strings.Contains(line, "All conflicts fixed but you are still merging"):
		return stateMergeReadyToCommit, true
	case strings.Contains(line, "You have unmerged paths"):
		return stateMergeConflicts, true
	case strings.Contains(line, "You are currently cherry-picking"):
		return stateCherryPick, true
	case strings.Contains(line, "You are currently reverting"):
		return stateRevert, true
	case strings.Contains(line, "You are currently bisecting"):
		return stateBisect, true
	case strings.Contains(line, "You are in the middle of an am session"):
		return stateAm, true
	case strings.Contains(line, "You are in a sparse checkout"):
		return stateSparseCheckout, true
	}
	for _, ind := range rebaseIndicators {
		if strings.Contains(line, ind) {
			return stateRebase, true
		}
	}
	return 0, false
}

// extractStateHeader extracts a compact in-progress state summary from plain
// `git status` output. Returns ("", false) when no state is in progress.
func extractStateHeader(raw string) (string, bool) {
	stoppers := []string{
		"Changes to be committed:",
		"Changes not staged for commit:",
		"Untracked files:",
		"Unmerged paths:",
		"no changes added to commit",
		"nothing to commit",
		"nothing added to commit",
	}

	for _, line := range splitLines(raw) {
		stripped := strings.TrimSpace(line)
		for _, s := range stoppers {
			if strings.HasPrefix(stripped, s) {
				return "", false
			}
		}
		if state, ok := detectStatusState(stripped); ok {
			return state.summary(), true
		}
	}
	return "", false
}

// extractDetachedHead extracts the explicit "HEAD detached at/from <ref>" line
// from plain `git status` output. Returns ("", false) when HEAD is on a branch.
func extractDetachedHead(raw string) (string, bool) {
	for _, line := range splitLines(raw) {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "HEAD detached ") {
			return l, true
		}
	}
	return "", false
}

// filterStatusWithArgs applies minimal filtering for git status with
// user-provided args.
func filterStatusWithArgs(output string) string {
	var result []string
	for _, line := range splitLines(output) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Skip git hints — can appear at start or within line.
		if strings.HasPrefix(trimmed, "(use \"git") ||
			strings.HasPrefix(trimmed, "(create/copy files") ||
			strings.Contains(trimmed, "(use \"git add") ||
			strings.Contains(trimmed, "(use \"git restore") {
			continue
		}
		// Special case: clean working tree.
		if strings.Contains(trimmed, "nothing to commit") && strings.Contains(trimmed, "working tree clean") {
			result = append(result, trimmed)
			break
		}
		result = append(result, line)
	}
	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

func runStatus(args []string, verbose int, globalArgs []string) (int, error) {
	if !usesCompactStatusPath(args) {
		res := execCapture(gitCmd(globalArgs, buildStatusArgs(args)...))
		if res.startErr != nil {
			return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
		}
		if !res.success() {
			if strings.TrimSpace(res.stderr) != "" {
				fmt.Fprint(os.Stderr, res.stderr)
			}
			return res.exitCode, nil
		}
		if verbose > 0 || res.stderr != "" {
			fmt.Fprint(os.Stderr, res.stderr)
		}
		fmt.Print(filterStatusWithArgs(res.stdout))
		return 0, nil
	}

	rawArgs := append([]string{"status"}, args...)
	rawRes := execCapture(gitCmdCLocale(globalArgs, rawArgs...))
	rawOutput := rawRes.stdout

	res := execCapture(gitCmd(globalArgs, buildStatusArgs(args)...))
	if res.startErr != nil {
		return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
	}

	if res.stderr != "" && strings.Contains(res.stderr, "not a git repository") {
		fmt.Fprintln(os.Stderr, "Not a git repository")
		return res.exitCode, nil
	}

	var formatted string
	if detachedRef, ok := extractDetachedHead(rawOutput); ok {
		formatted = formatStatusOutputDetached(res.stdout, detachedRef)
	} else {
		formatted = formatStatusOutput(res.stdout)
	}

	finalOutput := formatted
	if state, ok := extractStateHeader(rawOutput); ok {
		finalOutput = state + "\n" + formatted
	}

	fmt.Println(finalOutput)
	return 0, nil
}

// ---- run_add ----------------------------------------------------------------

func runAdd(args []string, verbose int, globalArgs []string) (int, error) {
	cmdArgs := []string{"add"}
	if len(args) == 0 {
		cmdArgs = append(cmdArgs, ".")
	} else {
		cmdArgs = append(cmdArgs, args...)
	}

	res := execCapture(gitCmd(globalArgs, cmdArgs...))
	if res.startErr != nil {
		return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
	}

	if verbose > 0 {
		fmt.Fprintln(os.Stderr, "git add executed")
	}

	if res.success() {
		statRes := execCapture(gitCmd(globalArgs, "diff", "--cached", "--stat", "--shortstat"))
		// Mirror git's own behaviour: a no-op `git add` is silent.
		compact := ""
		if strings.TrimSpace(statRes.stdout) != "" {
			statLines := splitLines(statRes.stdout)
			short := ""
			if len(statLines) > 0 {
				short = strings.TrimSpace(statLines[len(statLines)-1])
			}
			if short == "" {
				compact = "ok"
			} else {
				compact = "ok " + short
			}
		}
		if compact != "" {
			fmt.Println(compact)
		}
		return 0, nil
	}

	fmt.Fprintln(os.Stderr, "FAILED: git add")
	if strings.TrimSpace(res.stderr) != "" {
		fmt.Fprintln(os.Stderr, res.stderr)
	}
	if strings.TrimSpace(res.stdout) != "" {
		fmt.Fprintln(os.Stderr, res.stdout)
	}
	return res.exitCode, nil
}

// ---- run_commit -------------------------------------------------------------

// buildCommitArgs returns the git argv for a commit, preserving the user's
// args verbatim (so multi -m, -am, --amend all pass through).
func buildCommitArgs(args []string) []string {
	return append([]string{"commit"}, args...)
}

// parseCommitOutput parses the first line of `git commit` success output and
// returns a compact token. Handles `[main abc1234def] message`,
// `[main (root-commit) abc1234def] msg`, localized variants, and multibyte
// branch names.
func parseCommitOutput(line string) string {
	if bracketEnd := strings.IndexByte(line, ']'); bracketEnd >= 0 && len(line) > 0 {
		// bracketContent = line[1:bracketEnd] (rune-safe: ASCII '[' and ']').
		bracketContent := line[1:bracketEnd]
		fields := strings.Fields(bracketContent)
		hash := ""
		if len(fields) > 0 {
			hash = fields[len(fields)-1]
		}
		hashRunes := []rune(hash)
		if hash != "" && len(hashRunes) >= 7 {
			return "ok " + string(hashRunes[:7])
		}
		return "ok"
	}
	return "ok"
}

func runCommit(args []string, verbose int, globalArgs []string) (int, error) {
	if verbose > 0 {
		fmt.Fprintln(os.Stderr, "git commit "+strings.Join(args, " "))
	}

	// git commit inherits stdin so the user can author a message in $EDITOR.
	cmd := gitCmd(globalArgs, buildCommitArgs(args)...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	cmd.Stdin = os.Stdin
	err := cmd.Run()
	exitCode := core.ExitCodeFromError(err)
	if se := startErr(err); se != nil {
		return 127, fmt.Errorf("gortk: failed to run git: %w", se)
	}
	stdout := core.NormalizeNewlines(outBuf.String())
	stderr := core.NormalizeNewlines(errBuf.String())

	if exitCode == 0 {
		compact := "ok"
		if lines := splitLines(stdout); len(lines) > 0 {
			compact = parseCommitOutput(lines[0])
		}
		fmt.Println(compact)
		return 0, nil
	}

	if strings.Contains(stderr, "nothing to commit") || strings.Contains(stdout, "nothing to commit") {
		fmt.Println("ok (nothing to commit)")
		return 0, nil
	}

	if strings.TrimSpace(stderr) != "" {
		fmt.Fprint(os.Stderr, stderr)
	}
	if strings.TrimSpace(stdout) != "" {
		fmt.Fprint(os.Stderr, stdout)
	}
	return exitCode, nil
}

// ---- run_push ---------------------------------------------------------------

// gitPushNoisePrefixes are git push progress prefixes (stderr) dropped from the
// stream.
var gitPushNoisePrefixes = []string{
	"Enumerating objects:",
	"Counting objects:",
	"Compressing objects:",
	"Writing objects:",
	"Delta compression using",
	"Total ",
}

// filterPushOutput compacts git push output: it drops progress-noise lines,
// passes the remaining lines through, and on a clean exit appends a one-line
// summary. Mirrors rtk's GitPushLineHandler streaming filter, applied to
// captured output. The returned string ends with a trailing newline when a
// summary is appended (matching the Rust line-stream behaviour the tests pin).
func filterPushOutput(raw string, exitCode int) string {
	var out strings.Builder
	upToDate := false
	pushedRef := ""

	for _, line := range splitLines(raw) {
		// should_skip
		if line == "" {
			continue
		}
		trimmed := strings.TrimLeft(line, " \t")
		skip := false
		for _, p := range gitPushNoisePrefixes {
			if strings.HasPrefix(trimmed, p) {
				skip = true
				break
			}
		}
		// observe_line runs on every (non-skipped) line.
		if !skip {
			if strings.Contains(line, "Everything up-to-date") {
				upToDate = true
			}
			if pushedRef == "" {
				if idx := strings.Index(line, " -> "); idx >= 0 {
					after := line[idx+4:]
					if dest := strings.Fields(after); len(dest) > 0 {
						pushedRef = dest[0]
					}
				}
			}
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}

	if exitCode == 0 {
		var summary string
		switch {
		case upToDate:
			summary = "ok (up-to-date)"
		case pushedRef != "":
			summary = "ok " + pushedRef
		default:
			summary = "ok"
		}
		out.WriteString(summary)
		out.WriteByte('\n')
	}

	return out.String()
}

func runPush(args []string, verbose int, globalArgs []string) (int, error) {
	if verbose > 0 {
		fmt.Fprintln(os.Stderr, "git push")
	}

	cmdArgs := append([]string{"push"}, args...)
	res := execCapture(gitCmd(globalArgs, cmdArgs...))
	if res.startErr != nil {
		return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
	}

	// git push writes progress + ref updates to stderr.
	filtered := filterPushOutput(res.combined(), res.exitCode)
	fmt.Print(filtered)
	return res.exitCode, nil
}

// ---- run_pull ---------------------------------------------------------------

func runPull(args []string, verbose int, globalArgs []string) (int, error) {
	if verbose > 0 {
		fmt.Fprintln(os.Stderr, "git pull")
	}

	cmdArgs := append([]string{"pull"}, args...)
	res := execCapture(gitCmd(globalArgs, cmdArgs...))
	if res.startErr != nil {
		return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
	}

	if res.success() {
		var compact string
		if strings.Contains(res.stdout, "Already up to date") || strings.Contains(res.stdout, "Already up-to-date") {
			compact = "ok (up-to-date)"
		} else {
			files, insertions, deletions := 0, 0, 0
			for _, line := range splitLines(res.stdout) {
				if strings.Contains(line, "file") && strings.Contains(line, "changed") {
					for _, part := range strings.Split(line, ",") {
						part = strings.TrimSpace(part)
						switch {
						case strings.Contains(part, "file"):
							files = firstInt(part)
						case strings.Contains(part, "insertion"):
							insertions = firstInt(part)
						case strings.Contains(part, "deletion"):
							deletions = firstInt(part)
						}
					}
				}
			}
			if files > 0 {
				compact = fmt.Sprintf("ok %d files +%d -%d", files, insertions, deletions)
			} else {
				compact = "ok"
			}
		}
		fmt.Println(compact)
		return 0, nil
	}

	fmt.Fprintln(os.Stderr, "FAILED: git pull")
	if strings.TrimSpace(res.stderr) != "" {
		fmt.Fprintln(os.Stderr, res.stderr)
	}
	if strings.TrimSpace(res.stdout) != "" {
		fmt.Fprintln(os.Stderr, res.stdout)
	}
	return res.exitCode, nil
}

// firstInt parses the first whitespace-separated token of s as an int,
// returning 0 on failure. Mirrors rtk's `.split_whitespace().next().parse()`.
func firstInt(s string) int {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0
	}
	return n
}

// ---- run_branch -------------------------------------------------------------

func runBranch(args []string, verbose int, globalArgs []string) (int, error) {
	if verbose > 0 {
		fmt.Fprintln(os.Stderr, "git branch")
	}

	hasActionFlag := false
	hasShowFlag := false
	hasListFlag := false
	hasPositionalArg := false
	for _, a := range args {
		switch a {
		case "-d", "-D", "-m", "-M", "-c", "-C", "--set-upstream-to", "-u", "--unset-upstream", "--edit-description":
			hasActionFlag = true
		case "--show-current":
			hasShowFlag = true
		case "-a", "--all", "-r", "--remotes", "--list", "--merged", "--no-merged",
			"--contains", "--no-contains", "--format", "--sort", "--points-at":
			hasListFlag = true
		}
		if strings.HasPrefix(a, "--set-upstream-to=") {
			hasActionFlag = true
		}
		if strings.HasPrefix(a, "--format=") || strings.HasPrefix(a, "--sort=") || strings.HasPrefix(a, "--points-at=") {
			hasListFlag = true
		}
		if !strings.HasPrefix(a, "-") {
			hasPositionalArg = true
		}
	}

	// --show-current: passthrough with raw stdout (not "ok").
	if hasShowFlag {
		cmdArgs := append([]string{"branch"}, args...)
		res := execCapture(gitCmd(globalArgs, cmdArgs...))
		if res.startErr != nil {
			return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
		}
		if res.success() {
			fmt.Println(strings.TrimSpace(res.stdout))
			return 0, nil
		}
		fmt.Fprintln(os.Stderr, "FAILED: git branch "+strings.Join(args, " "))
		if strings.TrimSpace(res.stderr) != "" {
			fmt.Fprintln(os.Stderr, res.stderr)
		}
		return res.exitCode, nil
	}

	// Write operation: action flags, or positional args without list flags.
	if hasActionFlag || (hasPositionalArg && !hasListFlag) {
		cmdArgs := append([]string{"branch"}, args...)
		res := execCapture(gitCmd(globalArgs, cmdArgs...))
		if res.startErr != nil {
			return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
		}
		if res.success() {
			fmt.Println("ok")
			return 0, nil
		}
		fmt.Fprintln(os.Stderr, "FAILED: git branch "+strings.Join(args, " "))
		if strings.TrimSpace(res.stderr) != "" {
			fmt.Fprintln(os.Stderr, res.stderr)
		}
		if strings.TrimSpace(res.stdout) != "" {
			fmt.Fprintln(os.Stderr, res.stdout)
		}
		return res.exitCode, nil
	}

	// List mode: show compact branch list.
	cmdArgs := []string{"branch"}
	if !hasListFlag {
		cmdArgs = append(cmdArgs, "-a")
	}
	cmdArgs = append(cmdArgs, "--no-color")
	cmdArgs = append(cmdArgs, args...)

	res := execCapture(gitCmd(globalArgs, cmdArgs...))
	if res.startErr != nil {
		return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
	}
	if !res.success() {
		if strings.TrimSpace(res.stderr) != "" {
			fmt.Fprint(os.Stderr, res.stderr)
		}
		return res.exitCode, nil
	}

	fmt.Println(filterBranchOutput(res.stdout))
	return 0, nil
}

func filterBranchOutput(output string) string {
	current := ""
	var local, remote []string
	seenRemote := map[string]bool{}

	for _, line := range splitLines(output) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if branch, ok := strings.CutPrefix(line, "* "); ok {
			current = branch
		} else if rest, ok := strings.CutPrefix(line, "remotes/"); ok {
			if slashPos := strings.IndexByte(rest, '/'); slashPos >= 0 {
				branch := rest[slashPos+1:]
				if strings.HasPrefix(branch, "HEAD ") {
					continue
				}
				if !seenRemote[branch] {
					seenRemote[branch] = true
					remote = append(remote, branch)
				}
			}
		} else {
			local = append(local, line)
		}
	}

	var result []string
	result = append(result, "* "+current)

	for _, b := range local {
		result = append(result, "  "+b)
	}

	if len(remote) > 0 {
		var remoteOnly []string
		for _, r := range remote {
			if r != current && !contains(local, r) {
				remoteOnly = append(remoteOnly, r)
			}
		}
		if len(remoteOnly) > 0 {
			result = append(result, fmt.Sprintf("  remote-only (%d):", len(remoteOnly)))
			limit := len(remoteOnly)
			if limit > maxRemoteBranches {
				limit = maxRemoteBranches
			}
			for _, b := range remoteOnly[:limit] {
				result = append(result, "    "+b)
			}
			if len(remoteOnly) > maxRemoteBranches {
				result = append(result, fmt.Sprintf("    ... +%d more", len(remoteOnly)-maxRemoteBranches))
			}
		}
	}

	return strings.Join(result, "\n")
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// ---- run_fetch --------------------------------------------------------------

func runFetch(args []string, verbose int, globalArgs []string) (int, error) {
	if verbose > 0 {
		fmt.Fprintln(os.Stderr, "git fetch")
	}

	cmdArgs := append([]string{"fetch"}, args...)
	res := execCapture(gitCmd(globalArgs, cmdArgs...))
	if res.startErr != nil {
		return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
	}

	if !res.success() {
		fmt.Fprintln(os.Stderr, "FAILED: git fetch")
		if strings.TrimSpace(res.stderr) != "" {
			fmt.Fprintln(os.Stderr, res.stderr)
		}
		return res.exitCode, nil
	}

	// git fetch writes new refs to stderr.
	newRefs := 0
	for _, l := range splitLines(res.stderr) {
		if strings.Contains(l, "->") || strings.Contains(l, "[new") {
			newRefs++
		}
	}

	msg := "ok fetched"
	if newRefs > 0 {
		msg = fmt.Sprintf("ok fetched (%d new refs)", newRefs)
	}
	fmt.Println(msg)
	return 0, nil
}

// ---- run_stash --------------------------------------------------------------

// formatStashMessage formats the status message for stash operations.
func formatStashMessage(subcommand string, combined string) string {
	switch subcommand {
	case "", "push", "save":
		if strings.Contains(combined, "No local changes") {
			return "No local changes to save"
		}
		return "ok stashed"
	default:
		return "ok stash " + subcommand
	}
}

func runStash(subcommand *string, args []string, verbose int, globalArgs []string) (int, error) {
	if verbose > 0 {
		if subcommand != nil {
			fmt.Fprintf(os.Stderr, "git stash %q\n", *subcommand)
		} else {
			fmt.Fprintln(os.Stderr, "git stash <none>")
		}
	}

	sub := ""
	if subcommand != nil {
		sub = *subcommand
	}

	switch sub {
	case "list":
		res := execCapture(gitCmd(globalArgs, "stash", "list"))
		if res.startErr != nil {
			return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
		}
		if strings.TrimSpace(res.stdout) == "" {
			fmt.Println("No stashes")
			return 0, nil
		}
		fmt.Println(filterStashList(res.stdout))
		return 0, nil

	case "show":
		cmdArgs := append([]string{"stash", "show", "-p"}, args...)
		res := execCapture(gitCmd(globalArgs, cmdArgs...))
		if res.startErr != nil {
			return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
		}
		if strings.TrimSpace(res.stdout) == "" {
			fmt.Println("Empty stash")
		} else {
			fmt.Println(compactDiff(res.stdout, 100))
		}
		return 0, nil

	case "apply", "branch", "clear", "create", "drop", "export", "import", "pop", "store":
		cmdArgs := append([]string{"stash", sub}, args...)
		res := execCapture(gitCmd(globalArgs, cmdArgs...))
		if res.startErr != nil {
			return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
		}
		if res.success() {
			fmt.Println(formatStashMessage(sub, res.combined()))
			return 0, nil
		}
		fmt.Fprintln(os.Stderr, "FAILED: git stash "+sub)
		if strings.TrimSpace(res.stderr) != "" {
			fmt.Fprintln(os.Stderr, res.stderr)
		}
		return res.exitCode, nil

	default:
		// "git stash [push] [--] [<pathspec>...]" or "git stash save [<message>]".
		gitSub := "push"
		var leadingArg string
		hasLeading := false
		switch sub {
		case "save":
			gitSub = "save"
		case "push", "":
			gitSub = "push"
		default:
			gitSub = "push"
			leadingArg = sub
			hasLeading = true
		}
		cmdArgs := []string{"stash", gitSub}
		if hasLeading {
			cmdArgs = append(cmdArgs, leadingArg)
		}
		cmdArgs = append(cmdArgs, args...)
		res := execCapture(gitCmd(globalArgs, cmdArgs...))
		if res.startErr != nil {
			return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
		}
		if res.success() {
			fmt.Println(formatStashMessage(sub, res.combined()))
			return 0, nil
		}
		fmt.Fprintln(os.Stderr, "FAILED: git stash "+gitSub)
		if strings.TrimSpace(res.stderr) != "" {
			fmt.Fprintln(os.Stderr, res.stderr)
		}
		return res.exitCode, nil
	}
}

func filterStashList(output string) string {
	// Format: "stash@{0}: WIP on main: abc1234 commit message".
	var result []string
	for _, line := range splitLines(output) {
		if colonPos := strings.Index(line, ": "); colonPos >= 0 {
			index := line[:colonPos]
			rest := line[colonPos+2:]
			// Compact: strip "WIP on branch:" prefix if present.
			message := strings.TrimSpace(rest)
			if secondColon := strings.Index(rest, ": "); secondColon >= 0 {
				message = strings.TrimSpace(rest[secondColon+2:])
			}
			result = append(result, index+": "+message)
		} else {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

// ---- run_worktree -----------------------------------------------------------

func runWorktree(args []string, verbose int, globalArgs []string) (int, error) {
	if verbose > 0 {
		fmt.Fprintln(os.Stderr, "git worktree list")
	}

	hasAction := false
	for _, a := range args {
		switch a {
		case "add", "remove", "prune", "lock", "unlock", "move":
			hasAction = true
		}
	}

	if hasAction {
		cmdArgs := append([]string{"worktree"}, args...)
		res := execCapture(gitCmd(globalArgs, cmdArgs...))
		if res.startErr != nil {
			return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
		}
		if res.success() {
			fmt.Println("ok")
			return 0, nil
		}
		fmt.Fprintln(os.Stderr, "FAILED: git worktree "+strings.Join(args, " "))
		if strings.TrimSpace(res.stderr) != "" {
			fmt.Fprintln(os.Stderr, res.stderr)
		}
		return res.exitCode, nil
	}

	res := execCapture(gitCmd(globalArgs, "worktree", "list"))
	if res.startErr != nil {
		return 127, fmt.Errorf("gortk: failed to run git: %w", res.startErr)
	}
	fmt.Println(filterWorktreeList(res.stdout))
	return 0, nil
}

func filterWorktreeList(output string) string {
	home, _ := os.UserHomeDir()

	var result []string
	for _, line := range splitLines(output) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		// Format: "/path/to/worktree  abc1234 [branch]".
		parts := strings.Fields(line)
		if len(parts) >= 3 {
			path := parts[0]
			if home != "" && strings.HasPrefix(path, home) {
				path = "~" + path[len(home):]
			}
			hash := parts[1]
			branch := strings.Join(parts[2:], " ")
			result = append(result, fmt.Sprintf("%s %s %s", path, hash, branch))
		} else {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

// ---- passthrough ------------------------------------------------------------

// runPassthrough runs an unsupported git subcommand by passing it through
// directly, streaming stdio. Mirrors rtk's git::run_passthrough.
func runPassthrough(args, globalArgs []string, verbose int) (int, error) {
	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "git passthrough: %v\n", args)
	}
	full := append(append([]string{}, globalArgs...), args...)
	return core.RunPassthrough("git", full, verbose)
}
