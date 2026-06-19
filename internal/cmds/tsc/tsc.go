// Package tsc is gortk's token-optimized TypeScript compiler wrapper. It runs
// `tsc` (falling back to `npx tsc` when tsc isn't on PATH), captures the
// compiler diagnostics, and emits a compact summary grouping errors by file and
// error code. Faithful port of rtk's src/cmds/js/tsc_cmd.rs.
package tsc

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "tsc",
		Summary: "TypeScript compiler with grouped error output",
		Run:     Run,
	})
}

// tscErrorRE matches a tsc diagnostic line:
//
//	src/foo.ts(12,5): error TS2322: Type 'string' is not assignable...
//
// Captures: 1=file, 2=line, 3=col, 4=severity, 5=code (TS####), 6=message.
var tscErrorRE = regexp.MustCompile(`^(.+?)\((\d+),(\d+)\):\s+(error|warning)\s+(TS\d+):\s+(.+)$`)

// Run executes the tsc command.
func Run(args []string, verbose int) (int, error) {
	// Try `tsc` directly first, fall back to `npx tsc` when it isn't on PATH.
	tscExists := core.ToolExists("tsc")

	var cmd *exec.Cmd
	if tscExists {
		cmd = core.ResolvedCommand("tsc")
	} else {
		cmd = core.ResolvedCommand("npx", "tsc")
	}
	cmd.Args = append(cmd.Args, args...)

	if verbose > 0 {
		tool := "tsc"
		if !tscExists {
			tool = "npx tsc"
		}
		fmt.Fprintf(os.Stderr, "Running: %s %s\n", tool, strings.Join(args, " "))
	}

	opts := core.RunOptions{TeeLabel: "tsc"}
	return core.RunFiltered(cmd, "tsc", strings.Join(args, " "), filterTscOutput, opts)
}

// tsError is one parsed tsc diagnostic plus any indented continuation lines tsc
// emits after it.
type tsError struct {
	file         string
	line         int
	code         string
	message      string
	contextLines []string
}

// filterTscOutput compresses raw tsc output into a grouped-by-file summary.
// Pure function — the behavioural spec lives in tsc_test.go.
func filterTscOutput(output string) string {
	var errors []tsError
	lines := splitLines(output)
	i := 0

	for i < len(lines) {
		line := lines[i]
		if caps := tscErrorRE.FindStringSubmatch(line); caps != nil {
			err := tsError{
				file:    caps[1],
				line:    parseInt(caps[2]),
				code:    caps[5],
				message: caps[6],
			}

			// Capture continuation lines (indented context from tsc).
			i++
			for i < len(lines) {
				next := lines[i]
				if next != "" &&
					(strings.HasPrefix(next, "  ") || strings.HasPrefix(next, "\t")) &&
					!tscErrorRE.MatchString(next) {
					err.contextLines = append(err.contextLines, strings.TrimSpace(next))
					i++
				} else {
					break
				}
			}

			errors = append(errors, err)
		} else {
			i++
		}
	}

	if len(errors) == 0 {
		if strings.Contains(output, "Found 0 errors") {
			return "TypeScript: No errors found"
		}
		return "TypeScript compilation completed"
	}

	// Group by file, preserving first-seen order.
	byFile := map[string][]*tsError{}
	var fileOrder []string
	for idx := range errors {
		e := &errors[idx]
		if _, ok := byFile[e.file]; !ok {
			fileOrder = append(fileOrder, e.file)
		}
		byFile[e.file] = append(byFile[e.file], e)
	}

	// Count by error code for summary.
	byCode := map[string]int{}
	var codeOrder []string
	for idx := range errors {
		c := errors[idx].code
		if _, ok := byCode[c]; !ok {
			codeOrder = append(codeOrder, c)
		}
		byCode[c]++
	}

	var result strings.Builder
	result.WriteString(fmt.Sprintf("TypeScript: %d errors in %d files\n", len(errors), len(byFile)))

	// Top error codes summary (compact, one line). Sort by count descending,
	// breaking ties by first-seen order for determinism.
	if len(byCode) > 1 {
		type codeCount struct {
			code  string
			count int
			order int
		}
		var counts []codeCount
		for ord, c := range codeOrder {
			counts = append(counts, codeCount{code: c, count: byCode[c], order: ord})
		}
		sort.SliceStable(counts, func(a, b int) bool {
			return counts[a].count > counts[b].count
		})
		limit := 5
		if limit > len(counts) {
			limit = len(counts)
		}
		var parts []string
		for _, cc := range counts[:limit] {
			parts = append(parts, fmt.Sprintf("%s (%dx)", cc.code, cc.count))
		}
		result.WriteString(fmt.Sprintf("Top codes: %s\n\n", strings.Join(parts, ", ")))
	}

	// Files sorted by error count (most errors first), stable on first-seen
	// order for determinism.
	type fileGroup struct {
		file  string
		errs  []*tsError
		order int
	}
	var groups []fileGroup
	for ord, f := range fileOrder {
		groups = append(groups, fileGroup{file: f, errs: byFile[f], order: ord})
	}
	sort.SliceStable(groups, func(a, b int) bool {
		return len(groups[a].errs) > len(groups[b].errs)
	})

	// Show every error per file — no limits.
	for _, g := range groups {
		result.WriteString(fmt.Sprintf("%s (%d errors)\n", g.file, len(g.errs)))
		for _, e := range g.errs {
			result.WriteString(fmt.Sprintf("  L%d: %s %s\n", e.line, e.code, truncate(e.message, 120)))
			for _, ctx := range e.contextLines {
				result.WriteString(fmt.Sprintf("    %s\n", truncate(ctx, 120)))
			}
		}
		result.WriteByte('\n')
	}

	return strings.TrimSpace(result.String())
}

// truncate shortens s to at most maxLen runes, appending "..." when it must cut.
// Mirrors rtk's utils::truncate (rune-counted, "..." for tiny maxLen).
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

// splitLines mirrors Rust's str::lines(): split on '\n' and drop a single
// trailing empty element produced by a trailing newline.
func splitLines(s string) []string {
	parts := strings.Split(s, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}

func parseInt(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return v
}
