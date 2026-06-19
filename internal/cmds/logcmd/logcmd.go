// Package logcmd is gortk's log filter/deduplicator. It reads a log file (or
// stdin), normalizes volatile fields (timestamps, UUIDs, hex, large numbers,
// paths), groups identical lines, and prints a compact summary with per-message
// repeat counts. Faithful port of rtk's src/cmds/system/log_cmd.rs
// (Commands::Log).
package logcmd

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "log",
		Summary: "Filter and deduplicate log output, showing repeat counts",
		Run:     Run,
	})
}

// Normalization regexes, mirroring rtk's lazy_static set. RE2 supports \b and
// \w, so these port verbatim.
var (
	timestampRE = regexp.MustCompile(`^\d{4}[-/]\d{2}[-/]\d{2}[T ]\d{2}:\d{2}:\d{2}[.,]?\d*\s*`)
	uuidRE      = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	hexRE       = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	numRE       = regexp.MustCompile(`\b\d{4,}\b`)
	pathRE      = regexp.MustCompile(`/[\w./\-]+`)
)

// Run reads a log file (first non-flag arg) or stdin when no file is given,
// analyzes it, and prints the deduplicated summary.
func Run(args []string, verbose int) (int, error) {
	var file string
	haveFile := false
	for _, a := range args {
		if a == "-" {
			// rtk has no explicit "-" for log; treat it as stdin (file absent).
			continue
		}
		if !strings.HasPrefix(a, "-") {
			file = a
			haveFile = true
			break
		}
	}

	var content string
	if haveFile {
		if verbose > 0 {
			fmt.Fprintf(os.Stderr, "Analyzing log: %s\n", file)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gortk: %v\n", err)
			return 1, nil
		}
		content = core.NormalizeNewlines(string(data))
	} else {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "gortk: failed to read from stdin: %v\n", err)
			return 1, nil
		}
		content = core.NormalizeNewlines(string(data))
	}

	fmt.Println(analyzeLogs(content))
	return 0, nil
}

// RunStdinStr exposes the pure analysis for use by other gortk modules, mirroring
// rtk's run_stdin_str.
func RunStdinStr(content string) string {
	return analyzeLogs(content)
}

// lines mirrors Rust's str::lines(): split on "\n", dropping a single trailing
// empty element from a final newline.
func lines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func analyzeLogs(content string) string {
	var result []string

	errorCounts := map[string]int{}
	warnCounts := map[string]int{}
	infoCounts := map[string]int{}
	var uniqueErrors []string
	var uniqueWarnings []string

	for _, line := range lines(content) {
		lineLower := strings.ToLower(line)
		normalized := normalizeLogLine(line)

		switch {
		case strings.Contains(lineLower, "error") ||
			strings.Contains(lineLower, "fatal") ||
			strings.Contains(lineLower, "panic") ||
			strings.Contains(lineLower, "critical") ||
			strings.Contains(lineLower, "alert") ||
			strings.Contains(lineLower, "emerg") ||
			strings.Contains(lineLower, "severe"):
			if errorCounts[normalized] == 0 {
				uniqueErrors = append(uniqueErrors, line)
			}
			errorCounts[normalized]++
		case strings.Contains(lineLower, "warn") || strings.Contains(lineLower, "notice"):
			if warnCounts[normalized] == 0 {
				uniqueWarnings = append(uniqueWarnings, line)
			}
			warnCounts[normalized]++
		case strings.Contains(lineLower, "info"):
			infoCounts[normalized]++
		}
	}

	totalErrors := sumValues(errorCounts)
	totalWarnings := sumValues(warnCounts)
	totalInfo := sumValues(infoCounts)

	result = append(result, "Log Summary")
	result = append(result, fmt.Sprintf("   [error] %d errors (%d unique)", totalErrors, len(errorCounts)))
	result = append(result, fmt.Sprintf("   [warn] %d warnings (%d unique)", totalWarnings, len(warnCounts)))
	result = append(result, fmt.Sprintf("   [info] %d info messages", totalInfo))
	result = append(result, "")

	// Errors with counts.
	if len(uniqueErrors) > 0 {
		result = append(result, "[ERRORS]")

		errorList := sortByCountDesc(errorCounts)
		const maxLogErrors = core.CapWarnings
		limit := len(errorList)
		if limit > maxLogErrors {
			limit = maxLogErrors
		}
		for _, kc := range errorList[:limit] {
			original := findOriginal(uniqueErrors, kc.key)
			result = append(result, formatCounted(original, kc.count))
		}
		if len(errorList) > maxLogErrors {
			result = append(result, fmt.Sprintf("   ... +%d more unique errors", len(errorList)-maxLogErrors))
		}
		result = append(result, "")
	}

	// Warnings with counts.
	if len(uniqueWarnings) > 0 {
		result = append(result, "[WARNINGS]")

		warnList := sortByCountDesc(warnCounts)
		// warnings are lower severity than errors — show fewer.
		maxLogWarns := core.Reduced(core.CapWarnings, 5)
		limit := len(warnList)
		if limit > maxLogWarns {
			limit = maxLogWarns
		}
		for _, kc := range warnList[:limit] {
			original := findOriginal(uniqueWarnings, kc.key)
			result = append(result, formatCounted(original, kc.count))
		}
		if len(warnList) > maxLogWarns {
			result = append(result, fmt.Sprintf("   ... +%d more unique warnings", len(warnList)-maxLogWarns))
		}
	}

	return strings.Join(result, "\n")
}

// formatCounted renders a single (possibly repeated) log message line, with the
// 100-byte/97-rune truncation rtk uses. rtk's original.len() is a byte length;
// Go's len(string) is too, and rune-based truncation matches .chars().take(97).
func formatCounted(original string, count int) string {
	var truncated string
	if len(original) > 100 {
		runes := []rune(original)
		if len(runes) > 97 {
			truncated = string(runes[:97]) + "..."
		} else {
			truncated = original
		}
	} else {
		truncated = original
	}
	if count > 1 {
		return fmt.Sprintf("   [×%d] %s", count, truncated)
	}
	return fmt.Sprintf("   %s", truncated)
}

// findOriginal returns the first unique line whose normalized form equals key,
// falling back to key itself when none matches (mirrors rtk's unwrap_or).
func findOriginal(unique []string, key string) string {
	for _, u := range unique {
		if normalizeLogLine(u) == key {
			return u
		}
	}
	return key
}

type keyCount struct {
	key   string
	count int
}

// sortByCountDesc returns the map entries sorted by count descending. rtk's
// HashMap iteration is unordered and its sort is unstable for ties; we add a key
// tiebreaker so gortk's output is deterministic (this only affects equal-count
// ties, which rtk leaves arbitrary).
func sortByCountDesc(m map[string]int) []keyCount {
	out := make([]keyCount, 0, len(m))
	for k, c := range m {
		out = append(out, keyCount{k, c})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].key < out[j].key
	})
	return out
}

func sumValues(m map[string]int) int {
	total := 0
	for _, v := range m {
		total += v
	}
	return total
}

// normalizeLogLine strips/anonymizes volatile fields so structurally identical
// lines dedup together. Order matches rtk exactly.
func normalizeLogLine(line string) string {
	normalized := timestampRE.ReplaceAllString(line, "")
	normalized = uuidRE.ReplaceAllString(normalized, "<UUID>")
	normalized = hexRE.ReplaceAllString(normalized, "<HEX>")
	normalized = numRE.ReplaceAllString(normalized, "<NUM>")
	normalized = pathRE.ReplaceAllString(normalized, "<PATH>")
	return strings.TrimSpace(normalized)
}
