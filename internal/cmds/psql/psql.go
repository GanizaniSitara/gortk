// Package psql is gortk's token-optimized PostgreSQL client wrapper. It wraps
// the native `psql` tool, detects table and expanded display formats, strips
// borders/padding, and produces compact tab-separated or key=value output.
// Faithful port of rtk's src/cmds/cloud/psql_cmd.rs.
package psql

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

const (
	maxTableRows       = core.CapList
	maxExpandedRecords = core.CapList
)

var (
	expandedRecordRE = regexp.MustCompile(`-\[ RECORD \d+ \]-`)
	separatorRE      = regexp.MustCompile(`^[-+]+$`)
	rowCountRE       = regexp.MustCompile(`^\(\d+ rows?\)$`)
	recordHeaderRE   = regexp.MustCompile(`^-\[ RECORD (\d+) \]-`)
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "psql",
		Summary: "PostgreSQL client with compact output (strip borders, compress tables)",
		Run:     Run,
	})
}

// Run executes the psql command, wrapping the native `psql` tool and
// compressing its tabular output. Receives the args after the command name
// plus the -v count; returns the child's exit code.
func Run(args []string, verbose int) (int, error) {
	cmd := core.ResolvedCommand("psql", args...)

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: psql %s\n", strings.Join(args, " "))
	}

	opts := core.RunOptions{
		FilterStdoutOnly:    true,
		SkipFilterOnFailure: true,
		NoTrailingNewline:   true,
		TeeLabel:            "psql",
	}
	return core.RunFiltered(cmd, "psql", strings.Join(args, " "), filterPsqlOutput, opts)
}

// filterPsqlOutput routes output to the table or expanded compressor, or passes
// it through unchanged (COPY results, notices, etc.).
func filterPsqlOutput(output string) string {
	if strings.TrimSpace(output) == "" {
		return ""
	}

	if isExpandedFormat(output) {
		return filterExpanded(output)
	} else if isTableFormat(output) {
		return filterTable(output)
	}
	// Passthrough: COPY results, notices, etc.
	return output
}

func isTableFormat(output string) bool {
	for _, line := range lines(output) {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "-+-") || strings.Contains(trimmed, "---+---") {
			return true
		}
	}
	return false
}

func isExpandedFormat(output string) bool {
	return expandedRecordRE.MatchString(output)
}

// filterTable compresses psql table format:
//   - Strip separator lines (----+----)
//   - Strip (N rows) footer
//   - Trim column padding
//   - Output tab-separated
func filterTable(output string) string {
	var result []string
	dataRows := 0
	totalRows := 0

	for _, line := range lines(output) {
		trimmed := strings.TrimSpace(line)

		// Skip separator lines
		if separatorRE.MatchString(trimmed) {
			continue
		}

		// Skip row count footer
		if rowCountRE.MatchString(trimmed) {
			continue
		}

		// Skip empty lines
		if trimmed == "" {
			continue
		}

		// This is a data or header row with | delimiters
		if strings.Contains(trimmed, "|") {
			totalRows++
			// First row is header, don't count it as data
			if totalRows > 1 {
				dataRows++
			}

			if dataRows <= maxTableRows || totalRows == 1 {
				cols := strings.Split(trimmed, "|")
				for i := range cols {
					cols[i] = strings.TrimSpace(cols[i])
				}
				result = append(result, strings.Join(cols, "\t"))
			}
		} else {
			// Non-table line (e.g., command output like SET, NOTICE)
			result = append(result, trimmed)
		}
	}

	if dataRows > maxTableRows {
		result = append(result, fmt.Sprintf("... +%d more rows", dataRows-maxTableRows))
	}

	return strings.Join(result, "\n")
}

// filterExpanded compresses psql expanded format: convert -[ RECORD N ]- blocks
// to one-liner key=val format.
func filterExpanded(output string) string {
	var result []string
	var currentPairs []string
	var currentRecord string
	haveRecord := false
	recordCount := 0

	for _, line := range lines(output) {
		trimmed := strings.TrimSpace(line)

		if rowCountRE.MatchString(trimmed) {
			continue
		}

		if caps := recordHeaderRE.FindStringSubmatch(trimmed); caps != nil {
			// Flush previous record
			if haveRecord {
				if recordCount <= maxExpandedRecords {
					result = append(result, currentRecord+" "+strings.Join(currentPairs, " "))
				}
				currentPairs = nil
			}
			recordCount++
			currentRecord = "[" + caps[1] + "]"
			haveRecord = true
		} else if strings.Contains(trimmed, "|") && haveRecord {
			// key | value line
			parts := strings.SplitN(trimmed, "|", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				currentPairs = append(currentPairs, key+"="+val)
			}
		} else if trimmed == "" {
			continue
		} else if !haveRecord {
			// Non-record line before any record (notices, etc.)
			result = append(result, trimmed)
		}
	}

	// Flush last record
	if haveRecord {
		if recordCount <= maxExpandedRecords {
			result = append(result, currentRecord+" "+strings.Join(currentPairs, " "))
		}
	}

	if recordCount > maxExpandedRecords {
		result = append(result, fmt.Sprintf("... +%d more records", recordCount-maxExpandedRecords))
	}

	return strings.Join(result, "\n")
}

// lines splits text on newlines with Rust's str::lines() semantics: a trailing
// empty element (from a final "\n") is dropped.
func lines(s string) []string {
	parts := strings.Split(s, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}
