package glab

import (
	"encoding/json"
	"fmt"
	"strings"

	"gortk/internal/core"
)

// ── CI/Pipeline subcommands ─────────────────────────────────────────────

func runCI(args []string, verbose int, ultraCompact bool) (int, error) {
	if len(args) == 0 {
		return runPassthrough("glab", "ci", args)
	}

	switch args[0] {
	case "list":
		return ciList(args[1:], verbose, ultraCompact)
	case "status":
		return ciStatus(args[1:], verbose, ultraCompact)
	case "trace":
		return ciTrace(args[1:])
	default:
		// "ci view" is an interactive TUI — must run with inherited stdio.
		return runPassthrough("glab", "ci", args)
	}
}

// formatCIList formats CI list JSON into compact output (pure, testable).
func formatCIList(jsonVal json.RawMessage, ultraCompact bool) string {
	pipelines, ok := asArray(jsonVal)
	if !ok {
		return ""
	}
	if len(pipelines) == 0 {
		return "No Pipelines\n"
	}

	var filtered strings.Builder
	filtered.WriteString("Pipelines\n")

	allLines := make([]string, 0, len(pipelines))
	for _, raw := range pipelines {
		p, ok := asObject(raw)
		if !ok {
			p = map[string]json.RawMessage{}
		}
		id := jsonInt(p, "id")
		status := orPlaceholder(jsonStr(p, "status"))
		refName := orPlaceholder(jsonStr(p, "ref"))
		icon := pipelineIcon(status, ultraCompact)
		allLines = append(allLines, fmt.Sprintf("  %s #%d %s (%s)", icon, id, status, refName))
	}

	const maxCIList = core.CapWarnings
	limit := maxCIList
	if limit > len(allLines) {
		limit = len(allLines)
	}
	for _, line := range allLines[:limit] {
		filtered.WriteString(line)
		filtered.WriteByte('\n')
	}
	if len(allLines) > maxCIList {
		filtered.WriteString(fmt.Sprintf("  … +%d more\n", len(allLines)-maxCIList))
	}

	return filtered.String()
}

func ciList(args []string, _ int, ultraCompact bool) (int, error) {
	cmdArgs := append([]string{"ci", "list", "-F", "json"}, args...)
	return runGlabJSON(cmdArgs, "ci list", func(v json.RawMessage) string {
		return formatCIList(v, ultraCompact)
	})
}

// formatCIStatus formats `glab ci status` text output (English keyword
// parsing, raw fallback). Returns the raw input when no status keyword is
// recognized on any line (e.g. a non-English locale).
func formatCIStatus(raw string, ultraCompact bool) string {
	var filtered strings.Builder
	anyKeywordMatched := false
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		icon := ""
		switch {
		case strings.Contains(trimmed, "passed") || strings.Contains(trimmed, "success"):
			icon = pipelineIcon("success", ultraCompact)
		case strings.Contains(trimmed, "failed"):
			icon = pipelineIcon("failed", ultraCompact)
		case strings.Contains(trimmed, "running"):
			icon = pipelineIcon("running", ultraCompact)
		case strings.Contains(trimmed, "pending"):
			icon = pipelineIcon("pending", ultraCompact)
		case strings.Contains(trimmed, "canceled") || strings.Contains(trimmed, "cancelled"):
			icon = pipelineIcon("canceled", ultraCompact)
		}

		if icon != "" {
			anyKeywordMatched = true
			fmt.Fprintf(&filtered, "%s %s\n", icon, trimmed)
		} else {
			fmt.Fprintf(&filtered, "  %s\n", trimmed)
		}
	}

	if !anyKeywordMatched {
		// Non-English locale or unrecognized format — preserve raw output verbatim.
		return raw
	}
	return filtered.String()
}

func ciStatus(args []string, _ int, ultraCompact bool) (int, error) {
	// glab ci status does not support -F json — text parsing with raw fallback.
	cmdArgs := append([]string{"ci", "status"}, args...)
	cmd := core.ResolvedCommand("glab", cmdArgs...)
	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, "glab", "ci status", func(stdout string) string {
		return formatCIStatus(stdout, ultraCompact)
	}, opts)
}

func ciTrace(args []string) (int, error) {
	cmdArgs := append([]string{"ci", "trace"}, args...)
	cmd := core.ResolvedCommand("glab", cmdArgs...)
	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, "glab", "ci trace", filterCITrace, opts)
}

// filterCITrace filters CI job trace output: strips ANSI codes, section
// markers, and runner boilerplate. Keeps warnings, errors, and build output.
func filterCITrace(raw string) string {
	cleaned := core.StripANSI(raw)
	cleaned = bareANSIRE.ReplaceAllString(cleaned, "")
	cleaned = sectionMarkerRE.ReplaceAllString(cleaned, "")

	var filtered strings.Builder

	for _, line := range strings.Split(cleaned, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		// Skip runner boilerplate.
		if strings.HasPrefix(trimmed, "Running with gitlab-runner") ||
			(strings.HasPrefix(trimmed, "on ") && strings.Contains(trimmed, "system ID:")) ||
			strings.HasPrefix(trimmed, "Using Docker executor") ||
			strings.HasPrefix(trimmed, "Using Shell") ||
			strings.HasPrefix(trimmed, "Running on runner-") ||
			strings.HasPrefix(trimmed, "Running on ") ||
			strings.HasPrefix(trimmed, "Preparing the") ||
			strings.HasPrefix(trimmed, "Preparing environment") ||
			strings.HasPrefix(trimmed, "Getting source from") ||
			strings.HasPrefix(trimmed, "Resolving secrets") ||
			strings.HasPrefix(trimmed, "Cleaning up") ||
			strings.HasPrefix(trimmed, "Uploading artifacts") ||
			strings.HasPrefix(trimmed, "Downloading artifacts") ||
			strings.HasPrefix(trimmed, "Runtime platform") {
			continue
		}

		// Skip git fetch / checkout boilerplate.
		if strings.HasPrefix(trimmed, "Fetching changes with git") ||
			strings.HasPrefix(trimmed, "Initialized empty Git") ||
			strings.HasPrefix(trimmed, "Created fresh repository") ||
			strings.HasPrefix(trimmed, "Checking out ") ||
			strings.HasPrefix(trimmed, "Skipping Git submodules") {
			continue
		}

		filtered.WriteString(trimmed)
		filtered.WriteByte('\n')
	}

	return filtered.String()
}
