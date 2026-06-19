package glab

import (
	"fmt"
	"strings"
)

// compactDiff compresses a unified `git diff` into a token-dense form: a file
// header per changed file, hunk headers, and +/- lines with per-file totals,
// truncating long hunks and the overall output. This is a self-contained port
// of rtk's git::compact_diff (gortk's git package implements the same logic,
// but command packages may only depend on core + their own package).
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

// splitLines splits text into lines with str::lines() semantics: a trailing
// newline does not produce a final empty element.
func splitLines(text string) []string {
	parts := strings.Split(text, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}
