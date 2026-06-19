// diff.go implements gortk's top-level `diff` command: an ultra-condensed
// comparison of two files (or a unified diff piped on stdin). Faithful port of
// rtk's src/cmds/git/diff_cmd.rs. The comparison logic (computeDiff,
// similarity, renderFileDiff, condenseUnifiedDiff) lives in pure functions so
// it can be tested directly against the ported Rust spec.

package git

import (
	"fmt"
	"io"
	"os"
	"strings"

	"gortk/internal/core"
)

// RunDiff is the entry point for the top-level `diff` command. With two file
// args it renders a condensed file comparison; with one arg (or none) it reads
// a unified diff on stdin and condenses it. Returns the diff-convention exit
// code: 0 if identical, 1 if files differ.
func RunDiff(args []string, verbose int) (int, error) {
	// Mirror rtk's clap shape: Diff { file1: PathBuf, file2: Option<PathBuf> }.
	// file1 == "-" or a missing second file means "read a unified diff from
	// stdin".
	if len(args) >= 2 && args[0] != "-" {
		file1, file2 := args[0], args[1]
		if verbose > 0 {
			fmt.Fprintf(os.Stderr, "Comparing: %s vs %s\n", file1, file2)
		}
		content1, err := readFile(file1)
		if err != nil {
			return 1, err
		}
		content2, err := readFile(file2)
		if err != nil {
			return 1, err
		}
		rendered, exitCode := renderFileDiff(file1, file2, content1, content2)
		fmt.Print(rendered)
		return exitCode, nil
	}

	// stdin mode (piped unified diff).
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return 1, err
	}
	condensed := condenseUnifiedDiff(core.NormalizeNewlines(string(input)))
	fmt.Println(condensed)
	return 0, nil
}

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return core.NormalizeNewlines(string(b)), nil
}

// renderFileDiff renders the condensed file comparison and returns it with the
// diff-convention exit code (0 = identical, 1 = differences found).
func renderFileDiff(file1, file2, content1, content2 string) (string, int) {
	lines1 := splitLines(content1)
	lines2 := splitLines(content2)
	diff := computeDiff(lines1, lines2)

	if len(diff.changes) == 0 {
		return "[ok] Files are identical\n", 0
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s → %s\n", file1, file2)
	fmt.Fprintf(&b, "   +%d added, -%d removed, ~%d modified\n\n", diff.added, diff.removed, diff.modified)
	b.WriteString(formatDiffChanges(diff))
	return b.String(), 1
}

// diffChangeKind classifies a single change in a file comparison.
type diffChangeKind int

const (
	changeAdded diffChangeKind = iota
	changeRemoved
	changeModified
)

type diffChange struct {
	kind diffChangeKind
	line int
	old  string // for Added/Removed this is the content; for Modified the old line
	new  string // only used for Modified
}

type diffResult struct {
	added    int
	removed  int
	modified int
	changes  []diffChange
}

func formatDiffChanges(diff *diffResult) string {
	var out strings.Builder
	for _, c := range diff.changes {
		switch c.kind {
		case changeAdded:
			fmt.Fprintf(&out, "+%4d %s\n", c.line, c.old)
		case changeRemoved:
			fmt.Fprintf(&out, "-%4d %s\n", c.line, c.old)
		case changeModified:
			fmt.Fprintf(&out, "~%4d %s → %s\n", c.line, c.old, c.new)
		}
	}
	return out.String()
}

// computeDiff performs a simple line-by-line comparison of two slices of lines.
// Similar-but-changed lines (Jaccard similarity > 0.5) are reported as
// modifications; dissimilar ones as a removed+added pair.
func computeDiff(lines1, lines2 []string) *diffResult {
	res := &diffResult{}

	maxLen := len(lines1)
	if len(lines2) > maxLen {
		maxLen = len(lines2)
	}

	for i := 0; i < maxLen; i++ {
		l1, ok1 := getLine(lines1, i)
		l2, ok2 := getLine(lines2, i)

		switch {
		case ok1 && ok2 && l1 != l2:
			if similarity(l1, l2) > 0.5 {
				res.changes = append(res.changes, diffChange{kind: changeModified, line: i + 1, old: l1, new: l2})
				res.modified++
			} else {
				res.changes = append(res.changes, diffChange{kind: changeRemoved, line: i + 1, old: l1})
				res.changes = append(res.changes, diffChange{kind: changeAdded, line: i + 1, old: l2})
				res.removed++
				res.added++
			}
		case ok1 && !ok2:
			res.changes = append(res.changes, diffChange{kind: changeRemoved, line: i + 1, old: l1})
			res.removed++
		case !ok1 && ok2:
			res.changes = append(res.changes, diffChange{kind: changeAdded, line: i + 1, old: l2})
			res.added++
		}
	}

	return res
}

func getLine(lines []string, i int) (string, bool) {
	if i < len(lines) {
		return lines[i], true
	}
	return "", false
}

// similarity computes the Jaccard similarity of the unique-character sets of a
// and b. Two empty strings have similarity 1.0 by convention (matching rtk).
func similarity(a, b string) float64 {
	aChars := map[rune]struct{}{}
	for _, r := range a {
		aChars[r] = struct{}{}
	}
	bChars := map[rune]struct{}{}
	for _, r := range b {
		bChars[r] = struct{}{}
	}

	intersection := 0
	for r := range aChars {
		if _, ok := bChars[r]; ok {
			intersection++
		}
	}
	union := map[rune]struct{}{}
	for r := range aChars {
		union[r] = struct{}{}
	}
	for r := range bChars {
		union[r] = struct{}{}
	}

	if len(union) == 0 {
		return 1.0
	}
	return float64(intersection) / float64(len(union))
}

// condenseUnifiedDiff strips diff metadata (headers, @@ hunks) and shows all
// +/- lines per file with a per-file change count. Diff content is never
// truncated — only the display is capped to 10 lines per file with an overflow
// count. Mirrors rtk's condense_unified_diff.
func condenseUnifiedDiff(diff string) string {
	var result []string
	currentFile := ""
	added := 0
	removed := 0
	var changes []string

	flush := func() {
		if currentFile != "" && (added > 0 || removed > 0) {
			result = append(result, fmt.Sprintf("[file] %s (+%d -%d)", currentFile, added, removed))
			for _, c := range changes {
				result = append(result, "  "+c)
			}
			total := added + removed
			if total > 10 {
				result = append(result, fmt.Sprintf("  ... +%d more", total-10))
			}
		}
	}

	for _, line := range splitLines(diff) {
		switch {
		case strings.HasPrefix(line, "diff --git") || strings.HasPrefix(line, "--- ") || strings.HasPrefix(line, "+++ "):
			if strings.HasPrefix(line, "+++ ") {
				flush()
				currentFile = strings.TrimPrefix(strings.TrimPrefix(line, "+++ "), "b/")
				added = 0
				removed = 0
				changes = changes[:0]
			}
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			added++
			changes = append(changes, line)
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			removed++
			changes = append(changes, line)
		}
	}

	// Last file.
	flush()

	return strings.Join(result, "\n")
}
