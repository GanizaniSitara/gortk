// Package wc is gortk's token-optimized `wc` wrapper. It runs the native `wc`
// tool and strips redundant path columns and alignment padding from its output.
// Faithful port of rtk's src/cmds/system/wc_cmd.rs.
//
// Compression examples:
//   - `wc file.py`     → `30L 96W 978B`
//   - `wc -l file.py`  → `30`
//   - `wc -w file.py`  → `96`
//   - `wc -c file.py`  → `978`
//   - `wc -l *.py`     → table with common path prefix stripped
//
// Like rtk, this wraps the platform `wc`; gortk resolves it PATHEXT-aware via
// core.ResolvedCommand. The compression lives in pure helper functions
// (filterWcOutput and friends) so it can be tested directly against the ported
// Rust spec.
package wc

import (
	"fmt"
	"os"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "wc",
		Summary: "Count lines/words/bytes with compact output",
		Run:     Run,
	})
}

// Run executes the wc command. args are the tokens after "wc"; verbose is the
// -v count. It returns the wrapped process exit code.
func Run(args []string, verbose int) (int, error) {
	cmd := core.ResolvedCommand("wc")
	cmd.Args = append(cmd.Args, args...)

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: wc %s\n", strings.Join(args, " "))
	}

	mode := detectMode(args)

	// No file operands → wc reads from stdin. Forward gortk's stdin to the child
	// so `cat file | gortk wc` counts the piped data instead of reporting zero.
	readsStdin := true
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			readsStdin = false
			break
		}
	}

	opts := core.RunOptions{FilterStdoutOnly: true, InheritStdin: readsStdin}
	return core.RunFiltered(cmd, "wc", strings.Join(args, " "), func(stdout string) string {
		return filterWcOutput(stdout, mode)
	}, opts)
}

// wcMode is which columns the user requested.
type wcMode int

const (
	// modeFull is the default: lines, words, bytes (3 columns).
	modeFull wcMode = iota
	// modeLines is lines only (-l).
	modeLines
	// modeWords is words only (-w).
	modeWords
	// modeBytes is bytes only (-c).
	modeBytes
	// modeChars is chars only (-m).
	modeChars
	// modeMixed is multiple flags combined — keep compact format.
	modeMixed
)

// detectMode inspects the flag arguments to decide which columns wc will emit.
// Combined flags like -lw and separate flags like -l -w both yield modeMixed.
func detectMode(args []string) wcMode {
	var flags []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
		}
	}

	if len(flags) == 0 {
		return modeFull
	}

	// Collect all single-char flags (handles combined flags like -lw).
	hasL, hasW, hasC, hasM := false, false, false, false
	flagCount := 0

	for _, flag := range flags {
		for _, ch := range flag[1:] {
			switch ch {
			case 'l':
				hasL = true
				flagCount++
			case 'w':
				hasW = true
				flagCount++
			case 'c':
				hasC = true
				flagCount++
			case 'm':
				hasM = true
				flagCount++
			}
		}
	}

	if flagCount == 0 {
		return modeFull
	}
	if flagCount > 1 {
		return modeMixed
	}

	switch {
	case hasL:
		return modeLines
	case hasW:
		return modeWords
	case hasC:
		return modeBytes
	case hasM:
		return modeChars
	default:
		return modeFull
	}
}

// filterWcOutput compresses wc output. Pure function — the behavioural heart of
// the command.
func filterWcOutput(raw string, mode wcMode) string {
	lines := splitLines(strings.TrimSpace(raw))

	if len(lines) == 0 {
		return ""
	}

	// Single file (one output line, no "total").
	if len(lines) == 1 {
		return formatSingleLine(lines[0], mode)
	}

	// Multiple files — compact table.
	return formatMultiLine(lines, mode)
}

// formatSingleLine formats a single wc output line (one file or stdin).
func formatSingleLine(line string, mode wcMode) string {
	parts := strings.Fields(line)

	switch mode {
	case modeLines, modeWords, modeBytes, modeChars:
		// First number is the only requested column.
		if len(parts) > 0 {
			return parts[0]
		}
		return ""
	case modeFull:
		if len(parts) >= 3 {
			return fmt.Sprintf("%sL %sW %sB", parts[0], parts[1], parts[2])
		}
		return strings.TrimSpace(line)
	case modeMixed:
		// Strip file path, keep numbers only.
		if len(parts) >= 2 {
			lastIsPath := !isUint(parts[len(parts)-1])
			if lastIsPath {
				return strings.Join(parts[:len(parts)-1], " ")
			}
			return strings.Join(parts, " ")
		}
		return strings.TrimSpace(line)
	default:
		return strings.TrimSpace(line)
	}
}

// formatMultiLine formats multiple files as a compact table.
func formatMultiLine(lines []string, mode wcMode) string {
	var result []string

	// Find common directory prefix to shorten paths.
	var paths []string
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}
		last := parts[len(parts)-1]
		if last != "total" {
			paths = append(paths, last)
		}
	}

	commonPrefix := findCommonPrefix(paths)

	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) == 0 {
			continue
		}

		isTotal := parts[len(parts)-1] == "total"

		switch mode {
		case modeLines, modeWords, modeBytes, modeChars:
			if isTotal {
				result = append(result, "Σ "+first(parts))
			} else {
				name := stripPrefix(parts[len(parts)-1], commonPrefix)
				result = append(result, first(parts)+" "+name)
			}
		case modeFull:
			if isTotal {
				result = append(result, fmt.Sprintf("Σ %sL %sW %sB", at(parts, 0), at(parts, 1), at(parts, 2)))
			} else if len(parts) >= 4 {
				name := stripPrefix(parts[3], commonPrefix)
				result = append(result, fmt.Sprintf("%sL %sW %sB %s", parts[0], parts[1], parts[2], name))
			} else {
				result = append(result, strings.TrimSpace(line))
			}
		case modeMixed:
			if isTotal {
				nums := parts[:len(parts)-1]
				result = append(result, "Σ "+strings.Join(nums, " "))
			} else if len(parts) >= 2 {
				lastIsPath := !isUint(parts[len(parts)-1])
				if lastIsPath {
					name := stripPrefix(parts[len(parts)-1], commonPrefix)
					nums := parts[:len(parts)-1]
					result = append(result, strings.Join(nums, " ")+" "+name)
				} else {
					result = append(result, strings.Join(parts, " "))
				}
			} else {
				result = append(result, strings.TrimSpace(line))
			}
		}
	}

	return strings.Join(result, "\n")
}

// findCommonPrefix finds the common directory prefix among paths, mirroring
// rtk's find_common_prefix. Returns "" when there is none (or 0/1 paths).
func findCommonPrefix(paths []string) string {
	if len(paths) <= 1 {
		return ""
	}

	first := paths[0]
	pos := strings.LastIndex(first, "/")
	if pos < 0 {
		return ""
	}
	prefix := first[:pos+1]

	if allHavePrefix(paths, prefix) {
		return prefix
	}

	// Try shorter prefixes by removing right-most segments.
	candidate := prefix
	for candidate != "" {
		if allHavePrefix(paths, candidate) {
			return candidate
		}
		// Search for the previous '/' before the trailing one.
		if p := strings.LastIndex(candidate[:len(candidate)-1], "/"); p >= 0 {
			candidate = candidate[:p+1]
		} else {
			return ""
		}
	}
	return ""
}

func allHavePrefix(paths []string, prefix string) bool {
	for _, p := range paths {
		if !strings.HasPrefix(p, prefix) {
			return false
		}
	}
	return true
}

// stripPrefix strips a common prefix from a path, mirroring rtk's strip_prefix.
func stripPrefix(path, prefix string) string {
	if prefix == "" {
		return path
	}
	return strings.TrimPrefix(path, prefix)
}

// splitLines mirrors Rust's str::lines(): splits on '\n' and drops a single
// trailing empty element. CRLF is normalized to LF first. An empty input yields
// no lines (matching "".lines().count() == 0).
func splitLines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}

// isUint reports whether s parses as a non-negative base-10 integer, mirroring
// Rust's str::parse::<u64>() (rejects empty, signs, and non-digits).
func isUint(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// first returns parts[0] or "0" when empty, mirroring rtk's
// parts.first().unwrap_or(&"0").
func first(parts []string) string {
	if len(parts) > 0 {
		return parts[0]
	}
	return "0"
}

// at returns parts[i] or "0" when out of range, mirroring rtk's
// parts.get(i).unwrap_or(&"0").
func at(parts []string, i int) string {
	if i < len(parts) {
		return parts[i]
	}
	return "0"
}
