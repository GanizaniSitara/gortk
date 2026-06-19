package glab

import (
	"encoding/json"
	"fmt"
	"strings"

	"gortk/internal/core"
)

// ── MR subcommands ──────────────────────────────────────────────────────

func runMR(args []string, verbose int, ultraCompact bool) (int, error) {
	if len(args) == 0 {
		return runPassthrough("glab", "mr", args)
	}

	switch args[0] {
	case "list":
		return mrList(args[1:], verbose, ultraCompact)
	case "view":
		return mrView(args[1:], verbose, ultraCompact)
	case "create":
		return mrCreate(args[1:], verbose)
	case "merge":
		return mrAction("merge", "merged", args[1:], verbose)
	case "approve":
		return mrAction("approve", "approved", args[1:], verbose)
	case "diff":
		return mrDiff(args[1:], verbose)
	case "note":
		return mrAction("note", "noted", args[1:], verbose)
	case "update":
		return mrAction("update", "updated", args[1:], verbose)
	default:
		return runPassthrough("glab", "mr", args)
	}
}

// formatMRList formats MR list JSON into compact output (pure, testable).
func formatMRList(jsonVal json.RawMessage, ultraCompact bool) string {
	mrs, ok := asArray(jsonVal)
	if !ok {
		return ""
	}
	if len(mrs) == 0 {
		if ultraCompact {
			return "No MRs\n"
		}
		return "No Merge Requests\n"
	}

	var filtered strings.Builder
	if ultraCompact {
		filtered.WriteString("MRs\n")
	} else {
		filtered.WriteString("Merge Requests\n")
	}

	allLines := make([]string, 0, len(mrs))
	for _, raw := range mrs {
		mr, ok := asObject(raw)
		if !ok {
			mr = map[string]json.RawMessage{}
		}
		iid := jsonInt(mr, "iid")
		title := orPlaceholder(jsonStr(mr, "title"))
		state := orPlaceholder(jsonStr(mr, "state"))
		author := nestedUsernameOr(mr, "author", "???")
		icon := stateIcon(state, ultraCompact)
		allLines = append(allLines, fmt.Sprintf("  %s !%d %s (%s)", icon, iid, truncate(title, 60), author))
	}

	const maxList = core.CapList
	limit := maxList
	if limit > len(allLines) {
		limit = len(allLines)
	}
	for _, line := range allLines[:limit] {
		filtered.WriteString(line)
		filtered.WriteByte('\n')
	}
	if len(allLines) > maxList {
		filtered.WriteString(fmt.Sprintf("  … +%d more\n", len(allLines)-maxList))
	}

	return filtered.String()
}

func mrList(args []string, _ int, ultraCompact bool) (int, error) {
	cmdArgs := append([]string{"mr", "list", "-F", "json"}, args...)
	return runGlabJSON(cmdArgs, "mr list", func(v json.RawMessage) string {
		return formatMRList(v, ultraCompact)
	})
}

// formatMRView formats MR view JSON into compact output (pure, testable).
func formatMRView(jsonVal json.RawMessage, ultraCompact bool) string {
	obj, ok := asObject(jsonVal)
	if !ok {
		obj = map[string]json.RawMessage{}
	}
	iid := jsonInt(obj, "iid")
	title := orPlaceholder(jsonStr(obj, "title"))
	state := orPlaceholder(jsonStr(obj, "state"))
	author := nestedUsernameOr(obj, "author", "???")
	webURL := jsonStr(obj, "web_url")
	mergeStatus := jsonStr(obj, "merge_status")
	if mergeStatus == "" {
		mergeStatus = "unknown"
	}
	sourceBranch := orPlaceholder(jsonStr(obj, "source_branch"))
	targetBranch := orPlaceholder(jsonStr(obj, "target_branch"))

	icon := stateIcon(state, ultraCompact)

	var filtered strings.Builder
	fmt.Fprintf(&filtered, "%s MR !%d: %s\n", icon, iid, title)
	fmt.Fprintf(&filtered, "  %s\n", author)

	mergeableStr := "[?]"
	switch mergeStatus {
	case "can_be_merged":
		mergeableStr = "[ok]"
	case "cannot_be_merged":
		mergeableStr = "[conflict]"
	}
	fmt.Fprintf(&filtered, "  %s | %s\n", state, mergeableStr)
	fmt.Fprintf(&filtered, "  %s -> %s\n", sourceBranch, targetBranch)

	if labels := jsonArr(obj, "labels"); labels != nil {
		var joined []string
		for _, l := range labels {
			var s string
			if json.Unmarshal(l, &s) == nil {
				joined = append(joined, s)
			}
		}
		if len(joined) > 0 {
			fmt.Fprintf(&filtered, "  Labels: %s\n", strings.Join(joined, ", "))
		}
	}

	if reviewers := jsonArr(obj, "reviewers"); reviewers != nil {
		var names []string
		for _, r := range reviewers {
			ro, ok := asObject(r)
			if !ok {
				continue
			}
			if u := jsonStr(ro, "username"); u != "" {
				names = append(names, "@"+u)
			}
		}
		if len(names) > 0 {
			fmt.Fprintf(&filtered, "  Reviewers: %s\n", strings.Join(names, ", "))
		}
	}

	if pipeline := jsonObj(obj, "head_pipeline"); pipeline != nil {
		pStatus := jsonStr(pipeline, "status")
		if pStatus == "" {
			pStatus = "unknown"
		}
		fmt.Fprintf(&filtered, "  Pipeline: %s %s\n", pipelineIcon(pStatus, ultraCompact), pStatus)
	}

	fmt.Fprintf(&filtered, "  %s\n", webURL)

	if desc := jsonStr(obj, "description"); desc != "" {
		descFiltered := filterMarkdownBody(desc)
		if descFiltered != "" {
			filtered.WriteByte('\n')
			for _, line := range bodyLines(descFiltered) {
				fmt.Fprintf(&filtered, "  %s\n", line)
			}
		}
	}

	return filtered.String()
}

func mrView(args []string, _ int, ultraCompact bool) (int, error) {
	// `glab mr view` without an identifier defaults to the MR for the current branch.
	id, extra, haveID := parseOptionalIdentifier(args)

	// Passthrough for --web, --comments, or explicit output format.
	if shouldPassthroughView(extra) {
		base := []string{"mr", "view"}
		if haveID {
			base = append(base, id)
		}
		return runPassthroughWithExtra("glab", base, extra)
	}

	cmdArgs := []string{"mr", "view"}
	if haveID {
		cmdArgs = append(cmdArgs, id)
	}
	cmdArgs = append(cmdArgs, "-F", "json")
	cmdArgs = append(cmdArgs, extra...)

	label := "mr view"
	if haveID {
		label = "mr view " + id
	}
	return runGlabJSON(cmdArgs, label, func(v json.RawMessage) string {
		return formatMRView(v, ultraCompact)
	})
}

func mrCreate(args []string, _ int) (int, error) {
	cmdArgs := append([]string{"mr", "create"}, args...)
	cmd := core.ResolvedCommand("glab", cmdArgs...)
	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, "glab", "mr create", func(stdout string) string {
		// glab mr create outputs the URL on success.
		url := strings.TrimSpace(stdout)
		detail := url
		if num, ok := extractMRNumber(url); ok {
			detail = fmt.Sprintf("!%s %s", num, url)
		}
		return okConfirmation("created", detail)
	}, opts)
}

func mrDiff(args []string, _ int) (int, error) {
	cmdArgs := append([]string{"mr", "diff"}, args...)
	cmd := core.ResolvedCommand("glab", cmdArgs...)
	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, "glab", "mr diff", func(stdout string) string {
		if strings.TrimSpace(stdout) == "" {
			return "No diff\n"
		}
		return compactDiff(stdout, 500)
	}, opts)
}

// mrAction is the generic MR action handler for merge/approve/note/update.
// It uses extractIdentifierAndExtraArgs so the MR number is found even when it
// appears after flags (e.g. `glab mr note -m "msg" 42`).
func mrAction(subcmd, label string, args []string, _ int) (int, error) {
	cmdArgs := append([]string{"mr", subcmd}, args...)
	cmd := core.ResolvedCommand("glab", cmdArgs...)

	mrNum := ""
	if id, _, ok := extractIdentifierAndExtraArgs(args); ok {
		mrNum = "!" + id
	}
	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, "glab", "mr "+subcmd, func(_ string) string {
		return okConfirmation(label, mrNum)
	}, opts)
}
