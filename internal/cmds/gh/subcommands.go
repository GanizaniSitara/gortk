// subcommands.go implements the gh subcommand dispatch and the pure
// output-compression formatters for pr/issue/run/repo. Faithful port of the
// per-subcommand logic in rtk's src/cmds/git/gh_cmd.rs.

package gh

import (
	"fmt"
	"strings"

	"gortk/internal/core"
)

// ---- pr ---------------------------------------------------------------------

func runPR(args []string, verbose int, ultraCompact bool) (int, error) {
	if len(args) == 0 {
		return runPassthrough("gh", "pr", args)
	}
	switch args[0] {
	case "list":
		return listPRs(args[1:], verbose, ultraCompact)
	case "view":
		return viewPR(args[1:], verbose, ultraCompact)
	case "checks":
		return prChecks(args[1:], verbose, ultraCompact)
	case "status":
		return prStatus(args[1:], verbose, ultraCompact)
	case "create":
		return prCreate(args[1:], verbose)
	case "merge":
		return prMerge(args[1:], verbose)
	case "diff":
		return prDiff(args[1:], verbose)
	case "comment":
		return prAction("commented", args, verbose)
	case "edit":
		return prAction("edited", args, verbose)
	default:
		return runPassthrough("gh", "pr", args)
	}
}

func listPRs(args []string, _ int, ultraCompact bool) (int, error) {
	cmdArgs := []string{"pr", "list", "--json", "number,title,state,author,updatedAt"}
	cmdArgs = append(cmdArgs, args...)
	return runGHJSON(cmdArgs, "pr list", func(v any) string { return formatPRList(v, ultraCompact) })
}

func formatPRList(v any, ultraCompact bool) string {
	prs, ok := jArr(v)
	if !ok {
		return ""
	}
	if len(prs) == 0 {
		if ultraCompact {
			return "No PRs\n"
		}
		return "No Pull Requests\n"
	}
	var out strings.Builder
	if ultraCompact {
		out.WriteString("PRs\n")
	} else {
		out.WriteString("Pull Requests\n")
	}
	var allLines []string
	for _, pr := range prs {
		number := asI64(get(pr, "number"))
		title := asStr(get(pr, "title"), "???")
		state := asStr(get(pr, "state"), "???")
		author := asStr(get(get(pr, "author"), "login"), "???")
		icon := stateIcon(state, ultraCompact)
		allLines = append(allLines, fmt.Sprintf("  %s #%d %s (%s)", icon, number, truncate(title, 60), author))
	}
	const maxList = core.CapList
	limit := maxList
	if limit > len(allLines) {
		limit = len(allLines)
	}
	for _, line := range allLines[:limit] {
		out.WriteString(line)
		out.WriteByte('\n')
	}
	if len(allLines) > maxList {
		out.WriteString(fmt.Sprintf("  … +%d more\n", len(allLines)-maxList))
	}
	return out.String()
}

func stateIcon(state string, ultraCompact bool) string {
	if ultraCompact {
		switch state {
		case "OPEN":
			return "O"
		case "MERGED":
			return "M"
		case "CLOSED":
			return "C"
		default:
			return "?"
		}
	}
	switch state {
	case "OPEN":
		return "[open]"
	case "MERGED":
		return "[merged]"
	case "CLOSED":
		return "[closed]"
	default:
		return "[unknown]"
	}
}

func anyArgEquals(args []string, wanted ...string) bool {
	set := map[string]bool{}
	for _, w := range wanted {
		set[w] = true
	}
	for _, a := range args {
		if set[a] {
			return true
		}
	}
	return false
}

func shouldPassthroughPRView(extra []string) bool {
	return anyArgEquals(extra, "--json", "--jq", "--web", "--comments")
}

func shouldPassthroughIssueView(extra []string) bool {
	return anyArgEquals(extra, "--json", "--jq", "--web", "--comments")
}

func shouldPassthroughPRStatus(args []string) bool {
	return anyArgEquals(args, "--help", "-h", "--web", "--jq", "--template")
}

func prStatusJSONFields() string {
	return "number,title,reviewDecision,statusCheckRollup"
}

func viewPR(args []string, _ int, ultraCompact bool) (int, error) {
	id, hasID, extra := parseOptionalIdentifier(args)
	if shouldPassthroughPRView(extra) {
		base := []string{"pr", "view"}
		if hasID {
			base = append(base, id)
		}
		return runPassthroughWithExtra("gh", base, extra)
	}
	cmdArgs := []string{"pr", "view"}
	if hasID {
		cmdArgs = append(cmdArgs, id)
	}
	cmdArgs = append(cmdArgs, "--json", "number,title,state,author,body,url,mergeable,reviews,statusCheckRollup")
	cmdArgs = append(cmdArgs, extra...)
	label := "pr view"
	if hasID {
		label = "pr view " + id
	}
	return runGHJSON(cmdArgs, label, func(v any) string { return formatPRView(v, ultraCompact) })
}

func formatPRView(v any, ultraCompact bool) string {
	var out strings.Builder
	number := asI64(get(v, "number"))
	title := asStr(get(v, "title"), "???")
	state := asStr(get(v, "state"), "???")
	author := asStr(get(get(v, "author"), "login"), "???")
	url := asStr(get(v, "url"), "")
	mergeable := asStr(get(v, "mergeable"), "UNKNOWN")

	icon := stateIcon(state, ultraCompact)
	out.WriteString(fmt.Sprintf("%s PR #%d: %s\n", icon, number, title))
	out.WriteString(fmt.Sprintf("  %s\n", author))

	mergeableStr := "?"
	switch mergeable {
	case "MERGEABLE":
		mergeableStr = "[ok]"
	case "CONFLICTING":
		mergeableStr = "[x]"
	}
	out.WriteString(fmt.Sprintf("  %s | %s\n", state, mergeableStr))

	if reviews, ok := jArr(get(get(v, "reviews"), "nodes")); ok {
		approved := 0
		changes := 0
		for _, r := range reviews {
			switch asStr(get(r, "state"), "") {
			case "APPROVED":
				approved++
			case "CHANGES_REQUESTED":
				changes++
			}
		}
		if approved > 0 || changes > 0 {
			out.WriteString(fmt.Sprintf("  Reviews: %d approved, %d changes requested\n", approved, changes))
		}
	}

	if checks, ok := jArr(get(v, "statusCheckRollup")); ok {
		total := len(checks)
		passed, failed := countChecks(checks)
		if ultraCompact {
			if failed > 0 {
				out.WriteString(fmt.Sprintf("  [x]%d/%d  %d fail\n", passed, total, failed))
			} else {
				out.WriteString(fmt.Sprintf("  %d/%d\n", passed, total))
			}
		} else {
			out.WriteString(fmt.Sprintf("  Checks: %d/%d passed\n", passed, total))
			if failed > 0 {
				out.WriteString(fmt.Sprintf("  [warn] %d checks failed\n", failed))
			}
		}
	}

	out.WriteString(fmt.Sprintf("  %s\n", url))

	if body, ok := get(v, "body").(string); ok && body != "" {
		bodyFiltered := filterMarkdownBody(body)
		if bodyFiltered != "" {
			out.WriteByte('\n')
			for _, line := range lines(bodyFiltered) {
				out.WriteString(fmt.Sprintf("  %s\n", line))
			}
		} else {
			out.WriteString("\n  (body contained only badges/images/comments)\n")
		}
	}

	return out.String()
}

// countChecks counts SUCCESS (passed) and FAILURE (failed) over a status-check
// rollup, honouring either the "conclusion" or "state" key as rtk does.
func countChecks(checks []any) (passed, failed int) {
	for _, c := range checks {
		concl := asStr(get(c, "conclusion"), "")
		st := asStr(get(c, "state"), "")
		if concl == "SUCCESS" || st == "SUCCESS" {
			passed++
		}
		if concl == "FAILURE" || st == "FAILURE" {
			failed++
		}
	}
	return passed, failed
}

func prChecks(args []string, _ int, _ bool) (int, error) {
	id, hasID, extra := parseOptionalIdentifier(args)
	cmdArgs := []string{"pr", "checks"}
	if hasID {
		cmdArgs = append(cmdArgs, id)
	}
	cmdArgs = append(cmdArgs, extra...)
	label := "pr checks"
	if hasID {
		label = "pr checks " + id
	}
	cmd := core.ResolvedCommand("gh", cmdArgs...)
	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true, NoTrailingNewline: true}
	return core.RunFiltered(cmd, "gh", label, formatPRChecks, opts)
}

func formatPRChecks(stdout string) string {
	passed := 0
	failed := 0
	pending := 0
	var failedChecks []string

	for _, line := range lines(stdout) {
		switch {
		case strings.Contains(line, "[ok]") || strings.Contains(line, "pass"):
			passed++
		case strings.Contains(line, "[x]") || strings.Contains(line, "fail"):
			failed++
			failedChecks = append(failedChecks, strings.TrimSpace(line))
		case strings.Contains(line, "*") || strings.Contains(line, "pending"):
			pending++
		}
	}

	var out strings.Builder
	out.WriteString("CI Checks Summary:\n")
	out.WriteString(fmt.Sprintf("  [ok] Passed: %d\n", passed))
	out.WriteString(fmt.Sprintf("  [FAIL] Failed: %d\n", failed))
	if pending > 0 {
		out.WriteString(fmt.Sprintf("  [pending] Pending: %d\n", pending))
	}
	if len(failedChecks) > 0 {
		out.WriteString("\n  Failed checks:\n")
		for _, check := range failedChecks {
			out.WriteString(fmt.Sprintf("    %s\n", check))
		}
	}
	return out.String()
}

func prStatus(args []string, _ int, _ bool) (int, error) {
	if shouldPassthroughPRStatus(args) {
		passArgs := append([]string{"status"}, args...)
		return runPassthrough("gh", "pr", passArgs)
	}
	cmdArgs := []string{"pr", "status", "--json", prStatusJSONFields()}
	cmdArgs = append(cmdArgs, args...)
	return runGHJSON(cmdArgs, "pr status", formatPRStatus)
}

func formatPRStatus(v any) string {
	var out strings.Builder

	if cb := get(v, "currentBranch"); !isNull(cb) {
		currentBranch := formatPRStatusEntry(cb)
		if currentBranch != "" {
			out.WriteString("Current Branch\n")
			out.WriteString(currentBranch)
			out.WriteByte('\n')
		}
	}

	if createdBy, ok := jArr(get(v, "createdBy")); ok {
		out.WriteString(fmt.Sprintf("Your PRs (%d):\n", len(createdBy)))
		limit := 5
		if limit > len(createdBy) {
			limit = len(createdBy)
		}
		for _, pr := range createdBy[:limit] {
			entry := formatPRStatusEntry(pr)
			if entry != "" {
				out.WriteString(entry)
			}
		}
	}
	return out.String()
}

func formatPRStatusEntry(pr any) string {
	if isNull(pr) {
		return ""
	}
	number := asI64(get(pr, "number"))
	title := asStr(get(pr, "title"), "???")
	reviews := asStr(get(pr, "reviewDecision"), "PENDING")
	var out strings.Builder
	out.WriteString(fmt.Sprintf("  #%d %s [%s]", number, truncate(title, 50), reviews))

	if checks, ok := jArr(get(pr, "statusCheckRollup")); ok {
		total := len(checks)
		if total > 0 {
			passed, failed := countChecks(checks)
			out.WriteString(fmt.Sprintf(" checks %d/%d", passed, total))
			if failed > 0 {
				out.WriteString(fmt.Sprintf(" fail %d", failed))
			}
		}
	}

	out.WriteByte('\n')
	return out.String()
}

func prCreate(args []string, _ int) (int, error) {
	cmdArgs := append([]string{"pr", "create"}, args...)
	cmd := core.ResolvedCommand("gh", cmdArgs...)
	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, "gh", "pr create", func(stdout string) string {
		url := strings.TrimSpace(stdout)
		prNum := url
		if idx := strings.LastIndex(url, "/"); idx >= 0 {
			prNum = url[idx+1:]
		}
		detail := url
		if prNum != "" && allASCIIDigits(prNum) {
			detail = fmt.Sprintf("#%s %s", prNum, url)
		}
		return okConfirmation("created", detail)
	}, opts)
}

func allASCIIDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func prMerge(args []string, _ int) (int, error) {
	// gh pr merge is destructive — pass through the real output.
	passArgs := append([]string{"merge"}, args...)
	return runPassthrough("gh", "pr", passArgs)
}

// hasNonDiffFormatFlag reports flags that change `gh pr diff` output away from a
// unified diff, for which compactDiff would produce empty output.
func hasNonDiffFormatFlag(args []string) bool {
	return anyArgEquals(args, "--name-only", "--name-status", "--stat", "--numstat", "--shortstat")
}

func prDiff(args []string, _ int) (int, error) {
	noCompact := false
	var ghArgs []string
	for _, a := range args {
		if a == "--no-compact" {
			noCompact = true
			continue
		}
		ghArgs = append(ghArgs, a)
	}
	if noCompact || hasNonDiffFormatFlag(ghArgs) {
		return runPassthroughWithExtra("gh", []string{"pr", "diff"}, ghArgs)
	}
	cmdArgs := append([]string{"pr", "diff"}, ghArgs...)
	cmd := core.ResolvedCommand("gh", cmdArgs...)
	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, "gh", "pr diff", func(raw string) string {
		if strings.TrimSpace(raw) == "" {
			return "No diff"
		}
		return compactDiff(raw, 500)
	}, opts)
}

func prAction(action string, args []string, _ int) (int, error) {
	subcmd := args[0]
	prNum := ""
	for _, a := range args[1:] {
		if !strings.HasPrefix(a, "-") {
			prNum = "#" + a
			break
		}
	}
	cmdArgs := append([]string{"pr"}, args...)
	cmd := core.ResolvedCommand("gh", cmdArgs...)
	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, "gh", "pr "+subcmd, func(_ string) string {
		return okConfirmation(action, prNum)
	}, opts)
}

// ---- issue ------------------------------------------------------------------

func runIssue(args []string, verbose int, ultraCompact bool) (int, error) {
	if len(args) == 0 {
		return runPassthrough("gh", "issue", args)
	}
	switch args[0] {
	case "list":
		return listIssues(args[1:], verbose, ultraCompact)
	case "view":
		return viewIssue(args[1:], verbose)
	default:
		return runPassthrough("gh", "issue", args)
	}
}

func listIssues(args []string, _ int, ultraCompact bool) (int, error) {
	cmdArgs := []string{"issue", "list", "--json", "number,title,state,author"}
	cmdArgs = append(cmdArgs, args...)
	return runGHJSON(cmdArgs, "issue list", func(v any) string { return formatIssueList(v, ultraCompact) })
}

func formatIssueList(v any, ultraCompact bool) string {
	issues, ok := jArr(v)
	if !ok {
		return ""
	}
	if len(issues) == 0 {
		return "No Issues\n"
	}
	var out strings.Builder
	out.WriteString("Issues\n")
	var allLines []string
	for _, issue := range issues {
		number := asI64(get(issue, "number"))
		title := asStr(get(issue, "title"), "???")
		state := asStr(get(issue, "state"), "???")
		var icon string
		if ultraCompact {
			if state == "OPEN" {
				icon = "O"
			} else {
				icon = "C"
			}
		} else if state == "OPEN" {
			icon = "[open]"
		} else {
			icon = "[closed]"
		}
		allLines = append(allLines, fmt.Sprintf("  %s #%d %s", icon, number, truncate(title, 60)))
	}
	const maxList = core.CapList
	limit := maxList
	if limit > len(allLines) {
		limit = len(allLines)
	}
	for _, line := range allLines[:limit] {
		out.WriteString(line)
		out.WriteByte('\n')
	}
	if len(allLines) > maxList {
		out.WriteString(fmt.Sprintf("  … +%d more\n", len(allLines)-maxList))
	}
	return out.String()
}

func viewIssue(args []string, _ int) (int, error) {
	id, hasID, extra := parseOptionalIdentifier(args)
	if shouldPassthroughIssueView(extra) {
		base := []string{"issue", "view"}
		if hasID {
			base = append(base, id)
		}
		return runPassthroughWithExtra("gh", base, extra)
	}
	cmdArgs := []string{"issue", "view"}
	if hasID {
		cmdArgs = append(cmdArgs, id)
	}
	cmdArgs = append(cmdArgs, "--json", "number,title,state,author,body,url")
	cmdArgs = append(cmdArgs, extra...)
	label := "issue view"
	if hasID {
		label = "issue view " + id
	}
	return runGHJSON(cmdArgs, label, formatIssueView)
}

func formatIssueView(v any) string {
	var out strings.Builder
	number := asI64(get(v, "number"))
	title := asStr(get(v, "title"), "???")
	state := asStr(get(v, "state"), "???")
	author := asStr(get(get(v, "author"), "login"), "???")
	url := asStr(get(v, "url"), "")

	icon := "[closed]"
	if state == "OPEN" {
		icon = "[open]"
	}
	out.WriteString(fmt.Sprintf("%s Issue #%d: %s\n", icon, number, title))
	out.WriteString(fmt.Sprintf("  Author: @%s\n", author))
	out.WriteString(fmt.Sprintf("  Status: %s\n", state))
	out.WriteString(fmt.Sprintf("  URL: %s\n", url))

	if body, ok := get(v, "body").(string); ok && body != "" {
		bodyFiltered := filterMarkdownBody(body)
		if bodyFiltered != "" {
			out.WriteString("\n  Description:\n")
			for _, line := range lines(bodyFiltered) {
				out.WriteString(fmt.Sprintf("    %s\n", line))
			}
		} else {
			out.WriteString("\n  Description: (body contained only badges/images/comments)\n")
		}
	}
	return out.String()
}

// ---- run (workflow) ---------------------------------------------------------

func runWorkflow(args []string, verbose int, ultraCompact bool) (int, error) {
	if len(args) == 0 {
		return runPassthrough("gh", "run", args)
	}
	switch args[0] {
	case "list":
		return listRuns(args[1:], verbose, ultraCompact)
	case "view":
		return viewRun(args[1:], verbose)
	default:
		return runPassthrough("gh", "run", args)
	}
}

func listRuns(args []string, _ int, ultraCompact bool) (int, error) {
	cmdArgs := []string{"run", "list", "--json", "databaseId,name,status,conclusion,createdAt", "--limit", "10"}
	cmdArgs = append(cmdArgs, args...)
	return runGHJSON(cmdArgs, "run list", func(v any) string { return formatRunList(v, ultraCompact) })
}

func formatRunList(v any, ultraCompact bool) string {
	runs, ok := jArr(v)
	if !ok {
		return ""
	}
	var out strings.Builder
	if ultraCompact {
		out.WriteString("Runs\n")
	} else {
		out.WriteString("Workflow Runs\n")
	}
	for _, r := range runs {
		id := asI64(get(r, "databaseId"))
		name := asStr(get(r, "name"), "???")
		status := asStr(get(r, "status"), "???")
		conclusion := asStr(get(r, "conclusion"), "")
		var icon string
		if ultraCompact {
			switch {
			case conclusion == "success":
				icon = "[ok]"
			case conclusion == "failure":
				icon = "[x]"
			case conclusion == "cancelled":
				icon = "X"
			case status == "in_progress":
				icon = "~"
			default:
				icon = "?"
			}
		} else {
			switch {
			case conclusion == "success":
				icon = "[ok]"
			case conclusion == "failure":
				icon = "[FAIL]"
			case conclusion == "cancelled":
				icon = "[X]"
			case status == "in_progress":
				icon = "[time]"
			default:
				icon = "[pending]"
			}
		}
		out.WriteString(fmt.Sprintf("  %s %s [%d]\n", icon, truncate(name, 50), id))
	}
	return out.String()
}

// shouldPassthroughRunView reports whether run view args produce output the
// filter would incorrectly strip (--log-failed, --log, --json).
func shouldPassthroughRunView(extra []string) bool {
	return anyArgEquals(extra, "--log-failed", "--log", "--json")
}

func viewRun(args []string, _ int) (int, error) {
	id, hasID, extra := parseOptionalIdentifier(args)
	if shouldPassthroughRunView(extra) {
		base := []string{"run", "view"}
		if hasID {
			base = append(base, id)
		}
		return runPassthroughWithExtra("gh", base, extra)
	}
	cmdArgs := []string{"run", "view"}
	if hasID {
		cmdArgs = append(cmdArgs, id)
	}
	cmdArgs = append(cmdArgs, extra...)
	label := "run view"
	if hasID {
		label = "run view " + id
	}
	runID := id // "" when no id present
	cmd := core.ResolvedCommand("gh", cmdArgs...)
	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true, NoTrailingNewline: true}
	return core.RunFiltered(cmd, "gh", label, func(stdout string) string {
		return formatRunView(stdout, runID)
	}, opts)
}

func formatRunView(stdout, runID string) string {
	var out strings.Builder
	inJobs := false

	if runID == "" {
		out.WriteString("Workflow Run\n")
	} else {
		out.WriteString(fmt.Sprintf("Workflow Run #%s\n", runID))
	}
	for _, line := range lines(stdout) {
		if strings.Contains(line, "JOBS") {
			inJobs = true
		}
		if inJobs {
			if strings.ContainsRune(line, '✓') || strings.Contains(line, "success") {
				continue
			}
			if strings.Contains(line, "[x]") || strings.Contains(line, "fail") {
				out.WriteString(fmt.Sprintf("  [FAIL] %s\n", strings.TrimSpace(line)))
			}
		} else if strings.Contains(line, "Status:") || strings.Contains(line, "Conclusion:") {
			out.WriteString(fmt.Sprintf("  %s\n", strings.TrimSpace(line)))
		}
	}
	return out.String()
}

// ---- repo -------------------------------------------------------------------

func runRepo(args []string, _ int, _ bool) (int, error) {
	subcommand := "view"
	var rest []string
	if len(args) > 0 {
		subcommand = args[0]
		rest = args[1:]
	}
	if subcommand != "view" {
		return runPassthrough("gh", "repo", args)
	}
	cmdArgs := []string{"repo", "view"}
	cmdArgs = append(cmdArgs, rest...)
	cmdArgs = append(cmdArgs, "--json", "name,owner,description,url,stargazerCount,forkCount,isPrivate")
	return runGHJSON(cmdArgs, "repo view", formatRepoView)
}

func formatRepoView(v any) string {
	var out strings.Builder
	name := asStr(get(v, "name"), "???")
	owner := asStr(get(get(v, "owner"), "login"), "???")
	description := asStr(get(v, "description"), "")
	url := asStr(get(v, "url"), "")
	stars := asI64(get(v, "stargazerCount"))
	forks := asI64(get(v, "forkCount"))
	private := asBool(get(v, "isPrivate"), false)
	visibility := "[public]"
	if private {
		visibility = "[private]"
	}

	out.WriteString(fmt.Sprintf("%s/%s\n", owner, name))
	out.WriteString(fmt.Sprintf("  %s\n", visibility))
	if description != "" {
		out.WriteString(fmt.Sprintf("  %s\n", truncate(description, 80)))
	}
	out.WriteString(fmt.Sprintf("  %d stars | %d forks\n", stars, forks))
	out.WriteString(fmt.Sprintf("  %s\n", url))
	return out.String()
}

// ---- api --------------------------------------------------------------------

func runAPI(args []string, _ int) (int, error) {
	// gh api is explicit/advanced; preserve the full response (passthrough).
	return runPassthrough("gh", "api", args)
}
