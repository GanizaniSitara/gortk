// Package readcmd is gortk's source-aware file reader. It reads one or more
// files (or stdin via "-"), applies the language-aware comment/whitespace
// filter, and optionally windows the output (max-lines via smart truncation or
// tail-lines) and prefixes line numbers. Faithful port of rtk's
// src/cmds/system/read.rs (Commands::Read), behaving like `cat` for multiple
// files.
package readcmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "read",
		Summary: "Read file(s) with intelligent source filtering",
		Run:     Run,
	})
}

// options holds the parsed read flags.
type options struct {
	level       core.FilterLevel
	maxLines    *int
	tailLines   *int
	lineNumbers bool
	files       []string
}

// Run parses the read flags and reads each file (or stdin for "-") in turn,
// mirroring rtk's cat-like multi-file dispatch: a missing file reports on stderr
// and yields exit code 1, but valid files are still printed.
func Run(args []string, verbose int) (int, error) {
	opts, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gortk: %v\n", err)
		return 2, nil
	}
	if len(opts.files) == 0 {
		fmt.Fprintln(os.Stderr, "gortk: read requires at least one file (use - for stdin)")
		return 2, nil
	}

	hadError := false
	stdinSeen := false
	for _, file := range opts.files {
		if file == "-" {
			if stdinSeen {
				fmt.Fprintln(os.Stderr, "gortk: warning: stdin specified more than once")
				continue
			}
			stdinSeen = true
			if e := readStdin(opts, verbose); e != nil {
				fmt.Fprintf(os.Stderr, "cat: -: %v\n", e)
				hadError = true
			}
			continue
		}
		if e := readFile(file, opts, verbose); e != nil {
			fmt.Fprintf(os.Stderr, "cat: %s: %v\n", file, e)
			hadError = true
		}
	}

	if hadError {
		return 1, nil
	}
	return 0, nil
}

// parseArgs parses the read-specific flags. Everything that is not a recognized
// flag is treated as a file path (including "-" for stdin). Unknown flags that
// start with "-" but are not "-" are reported as errors.
func parseArgs(args []string) (options, error) {
	opts := options{level: core.FilterNone}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-":
			opts.files = append(opts.files, a)
		case a == "-n" || a == "--line-numbers":
			opts.lineNumbers = true
		case a == "-l" || a == "--level":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("missing value for %s", a)
			}
			lvl, err := core.ParseFilterLevel(args[i])
			if err != nil {
				return opts, err
			}
			opts.level = lvl
		case strings.HasPrefix(a, "--level="):
			lvl, err := core.ParseFilterLevel(strings.TrimPrefix(a, "--level="))
			if err != nil {
				return opts, err
			}
			opts.level = lvl
		case strings.HasPrefix(a, "-l") && len(a) > 2 && !strings.HasPrefix(a, "--"):
			// -lminimal style
			lvl, err := core.ParseFilterLevel(a[2:])
			if err != nil {
				return opts, err
			}
			opts.level = lvl
		case a == "-m" || a == "--max-lines":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("missing value for %s", a)
			}
			n, err := parsePositive(args[i])
			if err != nil {
				return opts, fmt.Errorf("invalid max-lines value: %s", args[i])
			}
			opts.maxLines = &n
		case strings.HasPrefix(a, "--max-lines="):
			n, err := parsePositive(strings.TrimPrefix(a, "--max-lines="))
			if err != nil {
				return opts, fmt.Errorf("invalid max-lines value")
			}
			opts.maxLines = &n
		case a == "--tail-lines":
			i++
			if i >= len(args) {
				return opts, fmt.Errorf("missing value for %s", a)
			}
			n, err := parsePositive(args[i])
			if err != nil {
				return opts, fmt.Errorf("invalid tail-lines value: %s", args[i])
			}
			opts.tailLines = &n
		case strings.HasPrefix(a, "--tail-lines="):
			n, err := parsePositive(strings.TrimPrefix(a, "--tail-lines="))
			if err != nil {
				return opts, fmt.Errorf("invalid tail-lines value")
			}
			opts.tailLines = &n
		case strings.HasPrefix(a, "-") && a != "-":
			return opts, fmt.Errorf("unknown flag: %s", a)
		default:
			opts.files = append(opts.files, a)
		}
	}
	// rtk marks max_lines and tail_lines as mutually exclusive; if both somehow
	// arrive, tail_lines wins in apply_line_window, so we mirror that by leaving
	// both set and letting applyLineWindow prefer tail.
	return opts, nil
}

func parsePositive(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

func readFile(file string, opts options, verbose int) error {
	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Reading: %s (filter: %s)\n", file, opts.level)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("Failed to read file: %s", file)
	}
	content := core.NormalizeNewlines(string(data))

	// Detect language from extension (without the leading dot).
	ext := strings.TrimPrefix(filepath.Ext(file), ".")
	lang := core.LanguageFromExt(ext)

	out := processContent(content, lang, opts, file, verbose)
	fmt.Print(out)
	return nil
}

func readStdin(opts options, verbose int) error {
	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Reading from stdin (filter: %s)\n", opts.level)
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("Failed to read from stdin")
	}
	content := core.NormalizeNewlines(string(data))
	// No file extension, so use Unknown language.
	out := processContent(content, core.LangUnknown, opts, "-", verbose)
	fmt.Print(out)
	return nil
}

// processContent applies the filter, the empty-output safety fallback, the line
// window, and optional line numbers — the shared pipeline for files and stdin.
func processContent(content string, lang core.Language, opts options, label string, verbose int) string {
	filtered := core.FilterSource(content, lang, opts.level)

	// Safety: if filter emptied a non-empty file, fall back to raw content. The
	// "rtk" literal is retained in this user-facing warning only to match the
	// ported test fixture; ports below replace tool-name strings with "gortk".
	if strings.TrimSpace(filtered) == "" && strings.TrimSpace(content) != "" {
		if label != "-" {
			fmt.Fprintf(os.Stderr,
				"gortk: warning: filter produced empty output for %s (%d bytes), showing raw content\n",
				label, len(content))
		}
		filtered = content
	}

	if verbose > 0 {
		originalLines := countLines(content)
		filteredLines := countLines(filtered)
		reduction := 0.0
		if originalLines > 0 {
			reduction = float64(originalLines-filteredLines) / float64(originalLines) * 100.0
		}
		fmt.Fprintf(os.Stderr, "Lines: %d -> %d (%.1f%% reduction)\n", originalLines, filteredLines, reduction)
	}

	filtered = applyLineWindow(filtered, opts.maxLines, opts.tailLines)

	if opts.lineNumbers {
		return formatWithLineNumbers(filtered)
	}
	return filtered
}

// countLines mirrors Rust's str::lines().count(): a trailing newline does not
// produce a final empty line.
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n")
	if !strings.HasSuffix(s, "\n") {
		n++
	}
	return n
}

// formatWithLineNumbers prefixes each line (as defined by Rust's str::lines())
// with a right-aligned 1-based line number and a " │ " separator.
func formatWithLineNumbers(content string) string {
	lines := splitLines(content)
	width := len(fmt.Sprintf("%d", len(lines)))
	var b strings.Builder
	for i, line := range lines {
		b.WriteString(fmt.Sprintf("%*d │ %s\n", width, i+1, line))
	}
	return b.String()
}

// applyLineWindow applies the tail-lines window (last N lines, preserving a
// trailing newline) or the max-lines smart truncation. tail-lines takes
// precedence, matching rtk.
func applyLineWindow(content string, maxLines, tailLines *int) string {
	if tailLines != nil {
		tail := *tailLines
		if tail == 0 {
			return ""
		}
		lines := splitLines(content)
		start := len(lines) - tail
		if start < 0 {
			start = 0
		}
		result := strings.Join(lines[start:], "\n")
		if strings.HasSuffix(content, "\n") {
			result += "\n"
		}
		return result
	}

	if maxLines != nil {
		return core.SmartTruncate(content, *maxLines)
	}

	return content
}

// splitLines mirrors Rust's str::lines(): splits on "\n" and drops a single
// trailing empty element produced by a final newline.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}
