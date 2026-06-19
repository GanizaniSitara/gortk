// Package mypy is gortk's token-optimized mypy wrapper. It runs the mypy type
// checker, groups its diagnostics by file, surfaces a top-error-code summary,
// and emits a compact view an agent can scan. Faithful port of rtk's
// src/cmds/python/mypy_cmd.rs.
//
// Like rtk, this wraps the platform `mypy`; gortk resolves it PATHEXT-aware via
// core.ResolvedCommand, falling back to `python3 -m mypy` when mypy is not on
// PATH. The output-compression logic lives in the pure function
// filterMypyOutput so it can be tested directly against the ported Rust spec.
package mypy

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "mypy",
		Summary: "Run mypy with diagnostics grouped by file",
		Run:     Run,
	})
}

// Run executes the gortk `mypy` command. args are the tokens after "mypy";
// verbose is the -v count. It mirrors rtk's run(): resolve `mypy`, falling back
// to `python3 -m mypy` when mypy is not on PATH, forward the args verbatim, and
// filter the combined output (stripped of ANSI) through filterMypyOutput.
func Run(args []string, verbose int) (int, error) {
	cmd := core.ResolvedCommand("mypy")
	if !core.ToolExists("mypy") {
		cmd = core.ResolvedCommand("python3")
		cmd.Args = append(cmd.Args, "-m", "mypy")
	}

	cmd.Args = append(cmd.Args, args...)

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: mypy %s\n", strings.Join(args, " "))
	}

	opts := core.RunOptions{}
	return core.RunFiltered(cmd, "mypy", strings.Join(args, " "), func(raw string) string {
		return filterMypyOutput(core.StripANSI(raw))
	}, opts)
}

// truncate truncates s to maxLen characters (counted as runes), appending "..."
// when truncation occurs. Faithful port of rtk's core::utils::truncate: when
// maxLen < 3 it returns just "...".
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

// mypyError holds one parsed file-anchored diagnostic plus any continuation
// note lines attached to it. Mirrors rtk's MypyError struct.
type mypyError struct {
	file         string
	line         int
	code         string
	message      string
	contextLines []string
}

// mypyDiagRE matches a mypy diagnostic line, anchored per-line:
//
//	file.py:12: error: Message [error-code]
//	file.py:12:5: error: Message [error-code]
//
// Faithful port of rtk's MYPY_DIAG. RE2 supports the non-greedy `.+?` captures
// natively; matched line-by-line so ^ and $ anchor each individual line.
var mypyDiagRE = regexp.MustCompile(`^(.+?):(\d+)(?::\d+)?: (error|warning|note): (.+?)(?:\s+\[(.+)\])?$`)

// filterMypyOutput compresses mypy output: it groups errors by file, surfaces a
// top-error-code summary when there are multiple distinct codes, and shows every
// error (mypy has no per-file/per-error cap). Faithful port of rtk's
// filter_mypy_output. Note the caller passes ANSI-stripped text.
func filterMypyOutput(output string) string {
	lines := strings.Split(output, "\n")
	var errors []mypyError
	var filelessLines []string
	i := 0

	for i < len(lines) {
		line := lines[i]

		// Skip mypy's own summary line.
		if strings.HasPrefix(line, "Found ") && strings.Contains(line, " error") {
			i++
			continue
		}
		// Skip "Success: no issues found".
		if strings.HasPrefix(line, "Success:") {
			i++
			continue
		}

		if caps := mypyDiagRE.FindStringSubmatch(line); caps != nil {
			severity := caps[3]
			file := caps[1]
			lineNum, _ := strconv.Atoi(caps[2]) // unwrap_or(0)
			message := caps[4]
			code := caps[5]

			if severity == "note" {
				// Attach note to preceding error if same file.
				if len(errors) > 0 && errors[len(errors)-1].file == file {
					last := &errors[len(errors)-1]
					last.contextLines = append(last.contextLines, message)
					i++
					continue
				}
				// Standalone note with no parent -- display as fileless.
				filelessLines = append(filelessLines, line)
				i++
				continue
			}

			err := mypyError{
				file:    file,
				line:    lineNum,
				code:    code,
				message: message,
			}

			// Capture continuation note lines.
			i++
			for i < len(lines) {
				if next := mypyDiagRE.FindStringSubmatch(lines[i]); next != nil {
					if next[3] == "note" && next[1] == err.file {
						err.contextLines = append(err.contextLines, next[4])
						i++
						continue
					}
				}
				break
			}

			errors = append(errors, err)
		} else if strings.Contains(line, "error:") && strings.TrimSpace(line) != "" {
			// File-less error (config errors, import errors).
			filelessLines = append(filelessLines, line)
			i++
		} else {
			i++
		}
	}

	// No errors at all.
	if len(errors) == 0 && len(filelessLines) == 0 {
		return "mypy: No issues found"
	}

	// Group by file.
	byFile := map[string][]*mypyError{}
	var fileOrder []string
	for idx := range errors {
		e := &errors[idx]
		if _, seen := byFile[e.file]; !seen {
			fileOrder = append(fileOrder, e.file)
		}
		byFile[e.file] = append(byFile[e.file], e)
	}

	// Count by error code.
	byCode := map[string]int{}
	var codeOrder []string
	for idx := range errors {
		e := &errors[idx]
		if e.code != "" {
			if _, seen := byCode[e.code]; !seen {
				codeOrder = append(codeOrder, e.code)
			}
			byCode[e.code]++
		}
	}

	var result strings.Builder

	// File-less errors first.
	for _, line := range filelessLines {
		result.WriteString(line)
		result.WriteByte('\n')
	}
	if len(filelessLines) > 0 && len(errors) > 0 {
		result.WriteByte('\n')
	}

	if len(errors) > 0 {
		fmt.Fprintf(&result, "mypy: %d errors in %d files\n", len(errors), len(byFile))

		// Top error codes summary (only when 2+ distinct codes).
		type codeCount struct {
			code  string
			count int
		}
		codeCounts := make([]codeCount, 0, len(codeOrder))
		for _, c := range codeOrder {
			codeCounts = append(codeCounts, codeCount{c, byCode[c]})
		}
		sort.SliceStable(codeCounts, func(a, b int) bool {
			if codeCounts[a].count != codeCounts[b].count {
				return codeCounts[a].count > codeCounts[b].count
			}
			return codeCounts[a].code < codeCounts[b].code
		})

		if len(codeCounts) > 1 {
			limit := 5
			if limit > len(codeCounts) {
				limit = len(codeCounts)
			}
			var codesStr []string
			for _, cc := range codeCounts[:limit] {
				codesStr = append(codesStr, fmt.Sprintf("%s (%dx)", cc.code, cc.count))
			}
			fmt.Fprintf(&result, "Top codes: %s\n\n", strings.Join(codesStr, ", "))
		}

		// Files sorted by error count (most errors first), with a stable
		// secondary sort on filename for deterministic tie-breaking.
		filesSorted := make([]string, len(fileOrder))
		copy(filesSorted, fileOrder)
		sort.SliceStable(filesSorted, func(a, b int) bool {
			la, lb := len(byFile[filesSorted[a]]), len(byFile[filesSorted[b]])
			if la != lb {
				return la > lb
			}
			return filesSorted[a] < filesSorted[b]
		})

		for _, file := range filesSorted {
			fileErrors := byFile[file]
			fmt.Fprintf(&result, "%s (%d errors)\n", file, len(fileErrors))

			for _, err := range fileErrors {
				if err.code == "" {
					fmt.Fprintf(&result, "  L%d: %s\n", err.line, truncate(err.message, 120))
				} else {
					fmt.Fprintf(&result, "  L%d: [%s] %s\n", err.line, err.code, truncate(err.message, 120))
				}
				for _, ctx := range err.contextLines {
					fmt.Fprintf(&result, "    %s\n", truncate(ctx, 120))
				}
			}
			result.WriteByte('\n')
		}
	}

	return strings.TrimSpace(result.String())
}
