// Package pipe is gortk's Unix-pipe filter mode. It reads stdin, applies a named
// filter, and prints the compressed result to stdout — the building block for
// `<some-tool> | gortk pipe -f <filter>`. It is a port of rtk's
// src/cmds/system/pipe_cmd.rs, adapted to gortk's architecture.
//
// Filter resolution differs from rtk by necessity. In rtk every dedicated
// command exposes a free `filter_*` function that `pipe` can call by name. In
// gortk those per-command compression functions are unexported (they live
// inside each cmds/<tool> package and the porting contract forbids touching
// them), so pipe resolves names against two sources it CAN reach standalone:
//
//  1. The builtin declarative TOML filters (tomlfilter), matched by their
//     CompiledFilter.Name — e.g. "make", "jq", "helm", "ping", "gradle", ...
//  2. A small set of pure, self-contained filters ported directly into this
//     package from the Rust pipe module: "grep"/"rg" and "find"/"fd". These had
//     no external dependencies in rtk, so they port verbatim.
//
// Any other name (cargo-test, pytest, go-test, tsc, vitest, git-log, ...) is one
// whose logic lives only inside a dedicated gortk command package with an
// unexported filter func; pipe cannot reach it standalone. For those, pipe
// falls back to passthrough and prints a clear note to stderr explaining the
// name was not resolvable in standalone pipe mode.
package pipe

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
	"gortk/internal/tomlfilter"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "pipe",
		Summary: "Read stdin, apply a named filter, print filtered output (Unix pipe mode)",
		Run:     Run,
	})
}

// rawCap bounds how much stdin we will buffer, mirroring rtk's RAW_CAP guard so
// a runaway producer can't exhaust memory. 10 MiB is generous for tool output.
const rawCap = 10 * 1024 * 1024

// Pipe-mode grouping caps. These mirror the Rust constants
// (MAX_PIPE_MATCHES/MAX_PIPE_FILES = CAP_WARNINGS, MAX_PIPE_DIRS = CAP_LIST).
const (
	maxPipeMatches = core.CapWarnings
	maxPipeFiles   = core.CapWarnings
	maxPipeDirs    = core.CapList
)

// pureFilter is a self-contained filter func reachable from standalone pipe.
type pureFilter func(string) string

// pureFilters are the filters ported directly into this package. Aliases map to
// the same func (grep≡rg, find≡fd) exactly as rtk's resolve_filter did.
var pureFilters = map[string]pureFilter{
	"grep": grepFilter,
	"rg":   grepFilter,
	"find": findFilter,
	"fd":   findFilter,
}

// Run implements the `pipe` command. args after the command name are parsed for
// -f/--filter <name> and --passthrough. verbose is accepted for signature
// parity; pipe has no extra verbose output of its own.
func Run(args []string, verbose int) (int, error) {
	filterName, passthrough, err := parseArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gortk pipe: %v\n", err)
		return 2, nil
	}

	// Passthrough: relay stdin to stdout untouched, like `cat`.
	if passthrough {
		if _, err := io.Copy(os.Stdout, os.Stdin); err != nil {
			return 1, fmt.Errorf("gortk pipe: failed to relay stdin: %w", err)
		}
		return 0, nil
	}

	input, err := readStdinCapped(os.Stdin, rawCap)
	if err != nil {
		return 1, fmt.Errorf("gortk pipe: %w", err)
	}
	input = core.NormalizeNewlines(input)

	if filterName == "" {
		// No filter named and no passthrough: nothing to do but echo. rtk's
		// pipe always required a filter or passthrough; we keep it forgiving by
		// passing through and noting it.
		fmt.Fprintln(os.Stderr, "gortk pipe: no filter selected (-f <name>); passing stdin through unchanged")
		fmt.Print(input)
		return 0, nil
	}

	output, resolved := applyNamedFilter(filterName, input)
	if !resolved {
		fmt.Fprintf(os.Stderr,
			"gortk pipe: filter %q is not resolvable in standalone pipe mode; passing stdin through unchanged.\n"+
				"  Resolvable names: %s\n",
			filterName, strings.Join(SupportedFilterNames(), ", "))
		fmt.Print(input)
		return 0, nil
	}

	fmt.Print(output)
	return 0, nil
}

// applyNamedFilter resolves name and applies the matching filter to input. The
// bool reports whether the name resolved to a real filter (false → caller should
// fall back to passthrough). Resolution order: pure filters first (they are the
// exact rtk pipe behaviours), then builtin TOML filters by name.
func applyNamedFilter(name, input string) (string, bool) {
	if f, ok := pureFilters[name]; ok {
		return f(input), true
	}
	if cf := findTOMLFilter(name); cf != nil {
		return cf.Apply(input), true
	}
	return input, false
}

// findTOMLFilter returns the builtin TOML filter whose Name equals name.
func findTOMLFilter(name string) *tomlfilter.CompiledFilter {
	for _, cf := range tomlfilter.All() {
		if cf.Name == name {
			return cf
		}
	}
	return nil
}

// SupportedFilterNames returns every filter name pipe can resolve standalone:
// the pure-filter names plus every builtin TOML filter name, sorted.
func SupportedFilterNames() []string {
	seen := map[string]bool{}
	var names []string
	for n := range pureFilters {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, cf := range tomlfilter.All() {
		if !seen[cf.Name] {
			seen[cf.Name] = true
			names = append(names, cf.Name)
		}
	}
	sort.Strings(names)
	return names
}

// parseArgs extracts -f/--filter <name> and --passthrough from the pipe args.
func parseArgs(args []string) (filter string, passthrough bool, err error) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--passthrough":
			passthrough = true
		case a == "-f" || a == "--filter":
			if i+1 >= len(args) {
				return "", false, fmt.Errorf("%s requires a filter name", a)
			}
			filter = args[i+1]
			i++
		case strings.HasPrefix(a, "--filter="):
			filter = strings.TrimPrefix(a, "--filter=")
		case strings.HasPrefix(a, "-f="):
			filter = strings.TrimPrefix(a, "-f=")
		case strings.HasPrefix(a, "-f") && len(a) > 2:
			// -fgrep form
			filter = a[2:]
		default:
			return "", false, fmt.Errorf("unexpected argument %q", a)
		}
	}
	return filter, passthrough, nil
}

// readStdinCapped reads up to cap bytes from r, erroring if the input exceeds
// the cap (mirrors rtk's RAW_CAP+1 take/bail).
func readStdinCapped(r io.Reader, cap int) (string, error) {
	limited := io.LimitReader(r, int64(cap)+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", fmt.Errorf("failed to read stdin: %w", err)
	}
	if len(data) > cap {
		return "", fmt.Errorf("stdin exceeds %d byte limit", cap)
	}
	return string(data), nil
}

// grepFilter groups grep/rg "file:line:content" output by file. Direct port of
// rtk's grep_wrapper. Lines that do not match the file:number:content shape are
// counted as non-matches; if nothing matches, the raw input is returned.
func grepFilter(input string) string {
	type match struct{ lineNum, content string }
	byFile := map[string][]match{}
	var fileOrder []string
	total := 0

	for _, line := range splitLines(input) {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 {
			if isAllDigits(parts[1]) {
				total++
				if _, seen := byFile[parts[0]]; !seen {
					fileOrder = append(fileOrder, parts[0])
				}
				byFile[parts[0]] = append(byFile[parts[0]], match{parts[1], parts[2]})
			}
		}
	}

	if total == 0 {
		return input
	}

	var out strings.Builder
	fmt.Fprintf(&out, "%d matches in %dF:\n\n", total, len(byFile))

	files := append([]string(nil), fileOrder...)
	sort.Strings(files)

	for _, file := range files {
		matches := byFile[file]
		fmt.Fprintf(&out, "[file] %s (%d):\n", file, len(matches))
		shown := matches
		if len(shown) > maxPipeMatches {
			shown = shown[:maxPipeMatches]
		}
		for _, m := range shown {
			fmt.Fprintf(&out, "  %4s: %s\n", m.lineNum, strings.TrimSpace(m.content))
		}
		if len(matches) > maxPipeMatches {
			fmt.Fprintf(&out, "  +%d\n", len(matches)-maxPipeMatches)
		}
		out.WriteByte('\n')
	}

	return out.String()
}

// findFilter groups find/fd path output by directory. Direct port of rtk's
// find_wrapper. If no non-empty path lines are present, raw input is returned.
func findFilter(input string) string {
	var paths []string
	for _, l := range splitLines(input) {
		if strings.TrimSpace(l) != "" {
			paths = append(paths, l)
		}
	}
	if len(paths) == 0 {
		return input
	}

	byDir := map[string][]string{}
	var dirOrder []string
	for _, path := range paths {
		dir := "."
		name := path
		if pos := strings.LastIndex(path, "/"); pos >= 0 {
			dir = path[:pos]
			name = path[pos+1:]
		}
		if _, seen := byDir[dir]; !seen {
			dirOrder = append(dirOrder, dir)
		}
		byDir[dir] = append(byDir[dir], name)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "%d files in %d dirs:\n\n", len(paths), len(byDir))

	dirs := append([]string(nil), dirOrder...)
	sort.Strings(dirs)

	shownDirs := dirs
	if len(shownDirs) > maxPipeDirs {
		shownDirs = shownDirs[:maxPipeDirs]
	}
	for _, dir := range shownDirs {
		files := byDir[dir]
		fmt.Fprintf(&out, "%s/  (%d)\n", dir, len(files))
		shown := files
		if len(shown) > maxPipeFiles {
			shown = shown[:maxPipeFiles]
		}
		for _, f := range shown {
			fmt.Fprintf(&out, "  %s\n", f)
		}
		if len(files) > maxPipeFiles {
			fmt.Fprintf(&out, "  +%d\n", len(files)-maxPipeFiles)
		}
	}

	if len(dirs) > maxPipeDirs {
		fmt.Fprintf(&out, "\n+%d more dirs\n", len(dirs)-maxPipeDirs)
	}

	return out.String()
}

// splitLines splits on "\n" and drops a trailing empty element so it matches
// Rust's str::lines() semantics (a final newline does not yield an empty line).
func splitLines(s string) []string {
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// isAllDigits reports whether s is non-empty and every rune is an ASCII digit
// (mirrors Rust's `parse::<usize>()` success used to detect a line-number field).
func isAllDigits(s string) bool {
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
