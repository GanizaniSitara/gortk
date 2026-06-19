package glab

import (
	"fmt"
	"strings"

	"gortk/internal/core"
)

// ── Release subcommands ──────────────────────────────────────────────────

func runRelease(args []string, _ int, _ bool) (int, error) {
	if len(args) == 0 {
		return runPassthrough("glab", "release", args)
	}

	switch args[0] {
	case "list":
		return releaseList(args[1:])
	case "view":
		return releaseView(args[1:])
	default:
		return runPassthrough("glab", "release", args)
	}
}

// formatReleaseList formats `glab release list` tab-separated output into
// compact form. Input format: "Name\tTag\tCreated\n" header + data rows.
// Returns ("", false) when no data rows were parsed.
func formatReleaseList(raw string) (string, bool) {
	lines := strings.Split(raw, "\n")
	idx := 0

	// Skip "Showing N releases..." preamble and blank lines up to the header.
	for idx < len(lines) {
		trimmed := strings.TrimSpace(lines[idx])
		if strings.HasPrefix(trimmed, "Name\t") || strings.HasPrefix(trimmed, "NAME\t") {
			idx++ // consume header
			break
		}
		idx++
	}

	var filtered strings.Builder
	filtered.WriteString("Releases\n")

	count := 0
	for ; idx < len(lines); idx++ {
		trimmed := strings.TrimSpace(lines[idx])
		if trimmed == "" {
			continue
		}

		parts := strings.Split(trimmed, "\t")
		if len(parts) < 3 {
			continue
		}

		name := strings.TrimSpace(parts[0])
		tag := strings.TrimSpace(parts[1])
		created := strings.TrimSpace(parts[2])

		if name == tag {
			fmt.Fprintf(&filtered, "  %s (%s)\n", name, created)
		} else {
			fmt.Fprintf(&filtered, "  %s [%s] (%s)\n", name, tag, created)
		}

		count++
		if count >= 20 {
			break
		}
	}

	if count == 0 {
		return "", false
	}
	return filtered.String(), true
}

func releaseList(args []string) (int, error) {
	cmdArgs := append([]string{"release", "list"}, args...)
	cmd := core.ResolvedCommand("glab", cmdArgs...)
	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, "glab", "release list", func(stdout string) string {
		if out, ok := formatReleaseList(stdout); ok {
			return out
		}
		return stdout
	}, opts)
}

func releaseView(args []string) (int, error) {
	cmdArgs := append([]string{"release", "view"}, args...)
	cmd := core.ResolvedCommand("glab", cmdArgs...)
	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, "glab", "release view", filterReleaseView, opts)
}

// filterReleaseView filters release view output: strips the SOURCES block,
// image lines, HTML comments, horizontal rules, and collapses blank lines.
func filterReleaseView(raw string) string {
	var filtered strings.Builder
	inSources := false

	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)

		// Skip SOURCES section (archive download URLs).
		if trimmed == "SOURCES" {
			inSources = true
			continue
		}
		if inSources {
			if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
				continue
			}
			inSources = false
		}

		// Strip image-only lines.
		if strings.HasPrefix(trimmed, "![") && strings.HasSuffix(trimmed, ")") && strings.Contains(trimmed, "](") {
			continue
		}
		// Strip glab's "Image: name → url" rendering.
		if strings.HasPrefix(trimmed, "Image:") && strings.ContainsRune(trimmed, '→') {
			continue
		}

		// Strip HTML comments.
		if strings.HasPrefix(trimmed, "<!--") && strings.HasSuffix(trimmed, "-->") {
			continue
		}

		// Strip horizontal rules (--- rendered as --------).
		if len(trimmed) >= 3 && isAllDashes(trimmed) {
			continue
		}

		filtered.WriteString(line)
		filtered.WriteByte('\n')
	}

	// Collapse multiple blank lines.
	return multiBlankRE.ReplaceAllString(filtered.String(), "\n\n")
}

func isAllDashes(s string) bool {
	for _, c := range s {
		if c != '-' {
			return false
		}
	}
	return s != ""
}
