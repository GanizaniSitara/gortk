// Package ruff is gortk's token-optimized ruff wrapper. It runs the Ruff linter
// (`ruff check`, defaulting to JSON output) or formatter (`ruff format`) and
// compresses the result — grouping check diagnostics by rule and file, and
// summarizing format runs. Faithful port of rtk's src/cmds/python/ruff_cmd.rs.
//
// Like rtk, this wraps the platform `ruff`; gortk resolves it PATHEXT-aware via
// core.ResolvedCommand. The compression logic lives in pure helper functions
// (filterRuffCheckJSON, filterRuffFormat, compactPath) so it can be tested
// directly against the ported Rust spec.
package ruff

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "ruff",
		Summary: "Ruff lint/format with compact output",
		Run:     Run,
	})
}

// Caps. MAX_RUFF_RULES / MAX_RUFF_FILES / MAX_RUFF_FORMAT_FILES mirror rtk's
// CAP_WARNINGS; MAX_VIOLATIONS is rtk's local const 50.
const (
	maxRuffRules       = core.CapWarnings
	maxRuffFiles       = core.CapWarnings
	maxRuffFormatFiles = core.CapWarnings
	maxViolations      = 50
)

// parseFailMaxChars caps how much of the raw output is echoed back when JSON
// parsing fails. rtk uses config::limits().passthrough_max_chars; gortk has no
// such config, so a fixed local cap is used. This path is only reached on
// malformed JSON and is not pinned by any ported test.
const parseFailMaxChars = 2000

// ruffLocation is the row/column position of a diagnostic.
type ruffLocation struct {
	Row    int `json:"row"`
	Column int `json:"column"`
}

// ruffFix marks a diagnostic as auto-fixable (its presence is what matters).
type ruffFix struct {
	Applicability *string `json:"applicability"`
}

// ruffDiagnostic is one entry in `ruff check --output-format=json`.
type ruffDiagnostic struct {
	Code     string       `json:"code"`
	Message  string       `json:"message"`
	Location ruffLocation `json:"location"`
	Filename string       `json:"filename"`
	Fix      *ruffFix     `json:"fix"`
}

// Run dispatches the gortk `ruff` command. args are the tokens after "ruff";
// verbose is the -v count. It mirrors rtk's run(): it decides whether this is a
// check or format invocation, builds argv (defaulting check to JSON output and
// appending "." when no path is given), then filters the captured stdout.
func Run(args []string, verbose int) (int, error) {
	isCheck := len(args) == 0 ||
		args[0] == "check" ||
		(!strings.HasPrefix(args[0], "-") && args[0] != "format" && args[0] != "version")

	isFormat := false
	for _, a := range args {
		if a == "format" {
			isFormat = true
			break
		}
	}

	cmd := core.ResolvedCommand("ruff")

	if isCheck {
		hasOutputFormat := false
		for _, a := range args {
			if a == "--output-format" {
				hasOutputFormat = true
				break
			}
		}
		if !hasOutputFormat {
			cmd.Args = append(cmd.Args, "check", "--output-format=json")
		} else {
			cmd.Args = append(cmd.Args, "check")
		}

		startIdx := 0
		if len(args) > 0 && args[0] == "check" {
			startIdx = 1
		}
		for _, a := range args[startIdx:] {
			cmd.Args = append(cmd.Args, a)
		}

		// If every forwarded arg is a flag/option, ruff has no target — default
		// to the current directory.
		allFlags := true
		for _, a := range args[startIdx:] {
			if !strings.HasPrefix(a, "-") && !strings.Contains(a, "=") {
				allFlags = false
				break
			}
		}
		if allFlags {
			cmd.Args = append(cmd.Args, ".")
		}
	} else {
		cmd.Args = append(cmd.Args, args...)
	}

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: ruff %s\n", strings.Join(args, " "))
	}

	opts := core.RunOptions{FilterStdoutOnly: true}
	return core.RunFiltered(cmd, "ruff", strings.Join(args, " "), func(stdout string) string {
		if isCheck && strings.TrimSpace(stdout) != "" {
			return filterRuffCheckJSON(stdout)
		} else if isFormat {
			return filterRuffFormat(stdout)
		}
		return strings.TrimSpace(stdout)
	}, opts)
}

// truncate truncates s to maxLen runes, appending "..." when truncation occurs.
// Faithful port of rtk's core::utils::truncate: maxLen < 3 returns just "...".
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

// compactPath shortens a file path by anchoring on a recognized source root
// (src/, lib/, tests/) or falling back to the basename. Faithful port of rtk's
// compact_path. Backslashes are normalized to forward slashes first.
func compactPath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")

	if pos := strings.LastIndex(path, "/src/"); pos >= 0 {
		return "src/" + path[pos+5:]
	} else if pos := strings.LastIndex(path, "/lib/"); pos >= 0 {
		return "lib/" + path[pos+5:]
	} else if pos := strings.LastIndex(path, "/tests/"); pos >= 0 {
		return "tests/" + path[pos+7:]
	} else if pos := strings.LastIndex(path, "/"); pos >= 0 {
		return path[pos+1:]
	}
	return path
}

// countKV is a (key, count) pair used for frequency sorting.
type countKV struct {
	key   string
	count int
}

// sortByCountDesc sorts pairs by count descending, breaking ties on key
// ascending. The tie-break is a deterministic refinement over rtk's
// HashMap-order-dependent sort_by(count) (Rust leaves equal counts in arbitrary
// order); the ported tests don't pin equal-count ordering.
func sortByCountDesc(pairs []countKV) {
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count != pairs[j].count {
			return pairs[i].count > pairs[j].count
		}
		return pairs[i].key < pairs[j].key
	})
}

// filterRuffCheckJSON compresses `ruff check --output-format=json` output: it
// groups diagnostics by rule and file and lists the violations (capped).
// Faithful port of rtk's filter_ruff_check_json.
func filterRuffCheckJSON(output string) string {
	var diagnostics []ruffDiagnostic
	if err := json.Unmarshal([]byte(output), &diagnostics); err != nil {
		// Fallback if JSON parsing fails.
		return fmt.Sprintf(
			"Ruff check (JSON parse failed: %s)\n%s",
			err,
			truncate(output, parseFailMaxChars),
		)
	}

	if len(diagnostics) == 0 {
		return "Ruff: No issues found"
	}

	totalIssues := len(diagnostics)
	fixableCount := 0
	for i := range diagnostics {
		if diagnostics[i].Fix != nil {
			fixableCount++
		}
	}

	// Count unique files.
	uniqueFiles := map[string]struct{}{}
	for i := range diagnostics {
		uniqueFiles[diagnostics[i].Filename] = struct{}{}
	}
	totalFiles := len(uniqueFiles)

	// Group by rule code.
	byRule := map[string]int{}
	for i := range diagnostics {
		byRule[diagnostics[i].Code]++
	}

	// Group by file.
	byFile := map[string]int{}
	for i := range diagnostics {
		byFile[diagnostics[i].Filename]++
	}

	var fileCounts []countKV
	for f, c := range byFile {
		fileCounts = append(fileCounts, countKV{f, c})
	}
	sortByCountDesc(fileCounts)

	var result strings.Builder
	fmt.Fprintf(&result, "Ruff: %d issues in %d files", totalIssues, totalFiles)
	if fixableCount > 0 {
		fmt.Fprintf(&result, " (%d fixable)", fixableCount)
	}
	result.WriteByte('\n')

	// Show top rules.
	var ruleCounts []countKV
	for r, c := range byRule {
		ruleCounts = append(ruleCounts, countKV{r, c})
	}
	sortByCountDesc(ruleCounts)

	if len(ruleCounts) > 0 {
		result.WriteString("Top rules:\n")
		limit := len(ruleCounts)
		if limit > maxRuffRules {
			limit = maxRuffRules
		}
		for _, rc := range ruleCounts[:limit] {
			fmt.Fprintf(&result, "  %s (%dx)\n", rc.key, rc.count)
		}
		result.WriteByte('\n')
	}

	// Show top files.
	result.WriteString("Top files:\n")
	fileLimit := len(fileCounts)
	if fileLimit > maxRuffFiles {
		fileLimit = maxRuffFiles
	}
	for _, fc := range fileCounts[:fileLimit] {
		shortPath := compactPath(fc.key)
		fmt.Fprintf(&result, "  %s (%d issues)\n", shortPath, fc.count)

		// Show top 3 rules in this file.
		fileRules := map[string]int{}
		for i := range diagnostics {
			if diagnostics[i].Filename == fc.key {
				fileRules[diagnostics[i].Code]++
			}
		}
		var fileRuleCounts []countKV
		for r, c := range fileRules {
			fileRuleCounts = append(fileRuleCounts, countKV{r, c})
		}
		sortByCountDesc(fileRuleCounts)
		ruleLim := len(fileRuleCounts)
		if ruleLim > 3 {
			ruleLim = 3
		}
		for _, frc := range fileRuleCounts[:ruleLim] {
			fmt.Fprintf(&result, "    %s (%d)\n", frc.key, frc.count)
		}
	}

	if len(fileCounts) > maxRuffFiles {
		fmt.Fprintf(&result, "\n... +%d more files\n", len(fileCounts)-maxRuffFiles)
	}

	// Build violation lines.
	violationLines := make([]string, 0, len(diagnostics))
	for i := range diagnostics {
		d := &diagnostics[i]
		violationLines = append(violationLines, fmt.Sprintf(
			"  %s:%d:%d %s %s\n",
			compactPath(d.Filename),
			d.Location.Row,
			d.Location.Column,
			d.Code,
			truncate(strings.TrimSpace(d.Message), 100),
		))
	}

	result.WriteString("\nViolations:\n")
	vLimit := len(violationLines)
	if vLimit > maxViolations {
		vLimit = maxViolations
	}
	for _, line := range violationLines[:vLimit] {
		result.WriteString(line)
	}
	if len(violationLines) > maxViolations {
		fmt.Fprintf(&result, "  … +%d more\n", len(violationLines)-maxViolations)
		// rtk appends a tee tail-hint here; gortk's RunFiltered already tees the
		// raw output on failure, so the explicit hint is dropped.
	}

	if fixableCount > 0 {
		fmt.Fprintf(&result, "\n[hint] Run `ruff check --fix` to auto-fix %d issues\n", fixableCount)
	}

	return strings.TrimSpace(result.String())
}

// filterRuffFormat compresses `ruff format` output: in check mode it lists the
// files that would be reformatted; otherwise it reports all-formatted or passes
// the summary through. Faithful port of rtk's filter_ruff_format.
func filterRuffFormat(output string) string {
	var filesToFormat []string
	filesChecked := 0

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		// Count "would reformat:" lines (check mode) — case insensitive.
		if strings.Contains(lower, "would reformat:") {
			// Extract filename from "Would reformat: path/to/file.py".
			parts := strings.SplitN(trimmed, ":", 2)
			if len(parts) >= 2 {
				filesToFormat = append(filesToFormat, strings.TrimSpace(parts[1]))
			}
		}

		// Count total checked files — patterns like "3 files left unchanged".
		if strings.Contains(lower, "left unchanged") {
			// Split by comma to handle
			// "2 files would be reformatted, 3 files left unchanged".
			for _, part := range strings.Split(trimmed, ",") {
				partLower := strings.ToLower(part)
				if strings.Contains(partLower, "left unchanged") {
					words := strings.Fields(part)
					for i, word := range words {
						if (word == "file" || word == "files") && i > 0 {
							if count, err := parseUint(words[i-1]); err == nil {
								filesChecked = count
								break
							}
						}
					}
					break
				}
			}
		}
	}

	outputLower := strings.ToLower(output)

	// All files are formatted.
	if len(filesToFormat) == 0 && strings.Contains(outputLower, "left unchanged") {
		return "Ruff format: All files formatted correctly"
	}

	var result strings.Builder

	if strings.Contains(outputLower, "would reformat") {
		// Check mode: show files that need formatting.
		if len(filesToFormat) == 0 {
			result.WriteString("Ruff format: All files formatted correctly\n")
		} else {
			fmt.Fprintf(&result, "Ruff format: %d files need formatting\n", len(filesToFormat))

			limit := len(filesToFormat)
			if limit > maxRuffFormatFiles {
				limit = maxRuffFormatFiles
			}
			for i, file := range filesToFormat[:limit] {
				fmt.Fprintf(&result, "%d. %s\n", i+1, compactPath(file))
			}

			if len(filesToFormat) > maxRuffFormatFiles {
				fmt.Fprintf(&result, "\n... +%d more files\n", len(filesToFormat)-maxRuffFormatFiles)
			}

			if filesChecked > 0 {
				fmt.Fprintf(&result, "\n%d files already formatted\n", filesChecked)
			}

			result.WriteString("\n[hint] Run `ruff format` to format these files\n")
		}
	} else {
		// Write mode or other output — show summary.
		result.WriteString(strings.TrimSpace(output))
	}

	return strings.TrimSpace(result.String())
}

// parseUint parses a non-negative base-10 integer, mirroring Rust's
// str::parse::<usize>() (rejects empty, signs, and non-digits).
func parseUint(s string) (int, error) {
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}
