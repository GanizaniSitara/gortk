// diff.go provides the unified-diff compressor used by `gh pr diff`. It is a
// local copy of the git package's compactDiff (which is unexported there);
// gortk's porting contract requires each package to be self-contained, so the
// logic is ported here rather than reaching across packages. Faithful port of
// rtk's compact_diff (the same routine rtk's gh_cmd::pr_diff calls via
// git::compact_diff).

package gh

import (
	"fmt"
	"strings"
)

// compactDiff compresses unified-diff output into a per-file, per-hunk summary.
// It preserves hunk headers (including trailing function context), shows up to
// maxHunkLines change/context lines per hunk, reports skipped counts, and caps
// the total result at maxLines. A recovery hint is appended whenever anything
// was truncated.
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

	for _, line := range lines(diff) {
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

	if hunkSkipped > 0 {
		result = append(result, fmt.Sprintf("  ... (%d lines truncated)", hunkSkipped))
		wasTruncated = true
	}

	if currentFile != "" && (added > 0 || removed > 0) {
		result = append(result, fmt.Sprintf("  +%d -%d", added, removed))
	}

	if wasTruncated {
		result = append(result, "[full diff: gortk gh pr diff --no-compact]")
	}

	return strings.Join(result, "\n")
}
