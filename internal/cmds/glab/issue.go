package glab

import (
	"encoding/json"
	"fmt"
	"strings"

	"gortk/internal/core"
)

// ── Issue subcommands ───────────────────────────────────────────────────

func runIssue(args []string, verbose int, ultraCompact bool) (int, error) {
	if len(args) == 0 {
		return runPassthrough("glab", "issue", args)
	}

	switch args[0] {
	case "list":
		return issueList(args[1:], verbose, ultraCompact)
	case "view":
		return issueView(args[1:], verbose)
	default:
		return runPassthrough("glab", "issue", args)
	}
}

// formatIssueList formats issue list JSON into compact output (pure, testable).
func formatIssueList(jsonVal json.RawMessage, ultraCompact bool) string {
	issues, ok := asArray(jsonVal)
	if !ok {
		return ""
	}
	if len(issues) == 0 {
		return "No Issues\n"
	}

	var filtered strings.Builder
	filtered.WriteString("Issues\n")

	allLines := make([]string, 0, len(issues))
	for _, raw := range issues {
		issue, ok := asObject(raw)
		if !ok {
			issue = map[string]json.RawMessage{}
		}
		iid := jsonInt(issue, "iid")
		title := orPlaceholder(jsonStr(issue, "title"))
		state := orPlaceholder(jsonStr(issue, "state"))
		var icon string
		if ultraCompact {
			if state == "opened" {
				icon = "O"
			} else {
				icon = "C"
			}
		} else if state == "opened" {
			icon = "[open]"
		} else {
			icon = "[closed]"
		}
		allLines = append(allLines, fmt.Sprintf("  %s #%d %s", icon, iid, truncate(title, 60)))
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

func issueList(args []string, _ int, ultraCompact bool) (int, error) {
	cmdArgs := append([]string{"issue", "list", "-F", "json"}, args...)
	return runGlabJSON(cmdArgs, "issue list", func(v json.RawMessage) string {
		return formatIssueList(v, ultraCompact)
	})
}

// formatIssueView formats issue view JSON into compact output (pure, testable).
func formatIssueView(jsonVal json.RawMessage) string {
	obj, ok := asObject(jsonVal)
	if !ok {
		obj = map[string]json.RawMessage{}
	}
	iid := jsonInt(obj, "iid")
	title := orPlaceholder(jsonStr(obj, "title"))
	state := orPlaceholder(jsonStr(obj, "state"))
	author := nestedUsernameOr(obj, "author", "???")
	webURL := jsonStr(obj, "web_url")

	icon := "[closed]"
	if state == "opened" {
		icon = "[open]"
	}

	var filtered strings.Builder
	fmt.Fprintf(&filtered, "%s Issue #%d: %s\n", icon, iid, title)
	fmt.Fprintf(&filtered, "  Author: @%s\n", author)
	fmt.Fprintf(&filtered, "  Status: %s\n", state)
	fmt.Fprintf(&filtered, "  URL: %s\n", webURL)

	if desc := jsonStr(obj, "description"); desc != "" {
		descFiltered := filterMarkdownBody(desc)
		if descFiltered != "" {
			filtered.WriteString("\n  Description:\n")
			for _, line := range bodyLines(descFiltered) {
				fmt.Fprintf(&filtered, "    %s\n", line)
			}
		}
	}

	return filtered.String()
}

func issueView(args []string, _ int) (int, error) {
	// Let glab emit its own error when the identifier is missing rather than pre-rejecting.
	id, extra, haveID := parseOptionalIdentifier(args)

	if shouldPassthroughView(extra) {
		base := []string{"issue", "view"}
		if haveID {
			base = append(base, id)
		}
		return runPassthroughWithExtra("glab", base, extra)
	}

	cmdArgs := []string{"issue", "view"}
	if haveID {
		cmdArgs = append(cmdArgs, id)
	}
	cmdArgs = append(cmdArgs, "-F", "json")
	cmdArgs = append(cmdArgs, extra...)

	label := "issue view"
	if haveID {
		label = "issue view " + id
	}
	return runGlabJSON(cmdArgs, label, formatIssueView)
}
