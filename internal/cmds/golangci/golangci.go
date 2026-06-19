// Package golangci is gortk's token-optimized wrapper for golangci-lint. For a
// `golangci-lint run ...` invocation it asks the tool for JSON output and emits
// a compact summary grouped by linter and file; every other invocation
// (version, help, linters, custom subcommands, bare flags) is passed through
// untouched. Faithful port of rtk's src/cmds/go/golangci_cmd.rs.
//
// The same compression helpers (FilterGolangciJSON, ParseMajorVersion) are
// exported so a future `go` command port can reuse them for the
// `go tool golangci-lint` interception, exactly as rtk's go_cmd.rs reuses
// golangci_cmd's pub(crate) functions.
package golangci

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "golangci-lint",
		Summary: "golangci-lint wrapper with compact `run` output and passthrough for other invocations",
		Run:     Run,
	})
}

// passthroughMaxChars caps the raw text echoed back when JSON parsing fails.
// Mirrors rtk's config default limits().passthrough_max_chars.
const passthroughMaxChars = 2000

// golangciSubcommands are the recognized golangci-lint subcommands. Only "run"
// is filtered; the rest mark the invocation as passthrough.
var golangciSubcommands = map[string]bool{
	"cache":      true,
	"completion": true,
	"config":     true,
	"custom":     true,
	"fmt":        true,
	"formatters": true,
	"help":       true,
	"linters":    true,
	"migrate":    true,
	"run":        true,
	"version":    true,
}

// globalFlagsWithValue are global flags that consume a following separate
// argument (e.g. `--color never`), so the subcommand scanner must skip it.
var globalFlagsWithValue = map[string]bool{
	"-c":               true,
	"--color":          true,
	"--config":         true,
	"--cpu-profile-path": true,
	"--mem-profile-path": true,
	"--trace-path":     true,
}

// runInvocation holds the args split around a `run` subcommand.
type runInvocation struct {
	globalArgs []string
	runArgs    []string
}

// position mirrors the Pos object in golangci-lint JSON. Only Filename is used.
type position struct {
	Filename string `json:"Filename"`
}

// issue mirrors one entry in the Issues array.
type issue struct {
	FromLinter  string   `json:"FromLinter"`
	Pos         position `json:"Pos"`
	SourceLines []string `json:"SourceLines"`
}

// golangciOutput is the top-level golangci-lint JSON document.
type golangciOutput struct {
	Issues []issue `json:"Issues"`
}

// Run executes the golangci-lint command, choosing the filtered `run` path or a
// straight passthrough based on how the args parse.
func Run(args []string, verbose int) (int, error) {
	if inv, ok := classifyInvocation(args); ok {
		return runFiltered(args, inv, verbose)
	}
	return core.RunPassthrough("golangci-lint", args, verbose)
}

func runFiltered(originalArgs []string, inv runInvocation, verbose int) (int, error) {
	version := detectMajorVersion()

	filteredArgs := buildFilteredArgs(inv, version)
	cmd := core.ResolvedCommand("golangci-lint", filteredArgs...)

	if verbose > 0 {
		fmt.Printf("Running: %s\n", formatCommand("golangci-lint", filteredArgs))
	}

	opts := core.RunOptions{FilterStdoutOnly: true}
	exitCode, err := core.RunFiltered(cmd, "golangci-lint", strings.Join(originalArgs, " "), func(stdout string) string {
		// v2 outputs JSON on the first line followed by trailing text; v1
		// outputs only JSON.
		jsonOutput := stdout
		if version >= 2 {
			jsonOutput = firstLine(stdout)
		}
		return FilterGolangciJSON(jsonOutput, version)
	}, opts)
	if err != nil {
		return exitCode, err
	}

	// golangci-lint: exit 0 = clean, exit 1 = lint issues found (not an error),
	// exit 2+ = config/build error. Normalize the "issues found" case to 0.
	if exitCode == 1 {
		return 0, nil
	}
	return exitCode, nil
}

// firstLine returns the text before the first newline, matching Rust's
// lines().next().unwrap_or("").
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// classifyInvocation returns the run split when args contain a `run`
// subcommand, or ok=false for any passthrough invocation.
func classifyInvocation(args []string) (runInvocation, bool) {
	idx, ok := findSubcommandIndex(args)
	if !ok || args[idx] != "run" {
		return runInvocation{}, false
	}
	global := append([]string(nil), args[:idx]...)
	run := append([]string(nil), args[idx+1:]...)
	return runInvocation{globalArgs: global, runArgs: run}, true
}

// findSubcommandIndex locates the first golangci-lint subcommand token,
// skipping over global flags (and their separate values). A bare "--" or a
// non-flag non-subcommand token ends the search with no match.
func findSubcommandIndex(args []string) (int, bool) {
	i := 0
	for i < len(args) {
		arg := args[i]

		if arg == "--" {
			return 0, false
		}

		if !strings.HasPrefix(arg, "-") {
			if golangciSubcommands[arg] {
				return i, true
			}
			return 0, false
		}

		if flag, ok := splitFlagName(arg); ok {
			if golangciFlagTakesSeparateValue(arg, flag) {
				i++
			}
		}

		i++
	}
	return 0, false
}

// splitFlagName returns the flag name portion of arg (the part before any "="
// for long flags, or the whole token for short flags). ok is false for
// non-flag tokens.
func splitFlagName(arg string) (string, bool) {
	if strings.HasPrefix(arg, "--") {
		if eq := strings.IndexByte(arg, '='); eq >= 0 {
			return arg[:eq], true
		}
		return arg, true
	}
	if strings.HasPrefix(arg, "-") {
		return arg, true
	}
	return "", false
}

// golangciFlagTakesSeparateValue reports whether arg consumes a following
// separate argument. An inline `--flag=value` carries its own value, so it does
// not.
func golangciFlagTakesSeparateValue(arg, flag string) bool {
	if !globalFlagsWithValue[flag] {
		return false
	}
	if strings.HasPrefix(arg, "--") && strings.Contains(arg, "=") {
		return false
	}
	return true
}

// buildFilteredArgs assembles the argv for the filtered run: the original
// global args, the `run` subcommand, a JSON-output flag (unless the user
// already requested an output format), then the original run args.
func buildFilteredArgs(inv runInvocation, version uint32) []string {
	args := append([]string(nil), inv.globalArgs...)
	args = append(args, "run")

	if !hasOutputFlag(inv.runArgs) {
		if version >= 2 {
			args = append(args, "--output.json.path", "stdout")
		} else {
			args = append(args, "--out-format=json")
		}
	}

	args = append(args, inv.runArgs...)
	return args
}

// hasOutputFlag reports whether the run args already select an output format,
// in which case gortk leaves the user's choice alone.
func hasOutputFlag(args []string) bool {
	for _, a := range args {
		if a == "--out-format" ||
			strings.HasPrefix(a, "--out-format=") ||
			a == "--output.json.path" ||
			strings.HasPrefix(a, "--output.json.path=") {
			return true
		}
	}
	return false
}

func formatCommand(base string, args []string) string {
	if len(args) == 0 {
		return base
	}
	return base + " " + strings.Join(args, " ")
}

// detectMajorVersion runs `golangci-lint --version` and returns the major
// version number, falling back to 1 (v1 behaviour) on any failure.
func detectMajorVersion() uint32 {
	cmd := core.ResolvedCommand("golangci-lint", "--version")
	out, err := cmd.Output()
	if err != nil {
		// Even on a non-zero exit we may have captured version text on stdout.
		if len(out) == 0 {
			return 1
		}
	}
	return ParseMajorVersion(core.NormalizeNewlines(string(out)))
}

// ParseMajorVersion extracts the major version number from
// `golangci-lint --version` output, returning 1 on any failure (safe v1
// fallback). Handles both:
//
//	"golangci-lint version 1.59.1"
//	"golangci-lint has version 2.10.0 built with ..."
func ParseMajorVersion(versionOutput string) uint32 {
	for _, word := range strings.Fields(versionOutput) {
		if !strings.Contains(word, ".") {
			continue
		}
		first := word
		if dot := strings.IndexByte(word, '.'); dot >= 0 {
			first = word[:dot]
		}
		if major, err := strconv.ParseUint(first, 10, 32); err == nil {
			return uint32(major)
		}
	}
	return 1
}

// FilterGolangciJSON compresses golangci-lint JSON output into a compact
// summary grouped by linter and by file. For v2 it also surfaces the first
// source line per linter-file group. On a JSON parse failure it returns the
// (truncated) raw output with a parse-error header.
func FilterGolangciJSON(output string, version uint32) string {
	var parsed golangciOutput
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		return fmt.Sprintf(
			"golangci-lint (JSON parse failed: %s)\n%s",
			err,
			truncate(output, passthroughMaxChars),
		)
	}

	issues := parsed.Issues
	if len(issues) == 0 {
		return "golangci-lint: No issues found"
	}

	totalIssues := len(issues)

	// Count unique files.
	uniqueFiles := map[string]bool{}
	for i := range issues {
		uniqueFiles[issues[i].Pos.Filename] = true
	}
	totalFiles := len(uniqueFiles)

	// Group by linter.
	byLinter := map[string]int{}
	for i := range issues {
		byLinter[issues[i].FromLinter]++
	}

	// Group by file.
	byFile := map[string]int{}
	for i := range issues {
		byFile[issues[i].Pos.Filename]++
	}

	var b strings.Builder
	fmt.Fprintf(&b, "golangci-lint: %d issues in %d files\n", totalIssues, totalFiles)

	// Top linters, by count descending.
	linterCounts := sortedCounts(byLinter)
	if len(linterCounts) > 0 {
		b.WriteString("Top linters:\n")
		for _, lc := range takeCounts(linterCounts, 10) {
			fmt.Fprintf(&b, "  %s (%dx)\n", lc.key, lc.count)
		}
		b.WriteByte('\n')
	}

	// Top files, by count descending.
	const maxGolangciFiles = core.CapWarnings
	fileCounts := sortedCounts(byFile)
	b.WriteString("Top files:\n")
	for _, fc := range takeCounts(fileCounts, maxGolangciFiles) {
		shortPath := compactPath(fc.key)
		fmt.Fprintf(&b, "  %s (%d issues)\n", shortPath, fc.count)

		// Top 3 linters within this file.
		fileLinters := map[string][]int{} // linter -> indices of issues
		for i := range issues {
			if issues[i].Pos.Filename == fc.key {
				fileLinters[issues[i].FromLinter] = append(fileLinters[issues[i].FromLinter], i)
			}
		}

		flCounts := sortedLinterGroups(fileLinters)
		for _, fl := range takeLinterGroups(flCounts, 3) {
			fmt.Fprintf(&b, "    %s (%d)\n", fl.linter, len(fl.idxs))

			// v2 only: show the first source line for this linter-file group.
			if version >= 2 {
				firstIdx := fl.idxs[0]
				if len(issues[firstIdx].SourceLines) > 0 {
					trimmed := strings.TrimSpace(issues[firstIdx].SourceLines[0])
					display := truncateChars(trimmed, 80)
					fmt.Fprintf(&b, "      → %s\n", display)
				}
			}
		}
	}

	if len(fileCounts) > maxGolangciFiles {
		fmt.Fprintf(&b, "\n... +%d more files\n", len(fileCounts)-maxGolangciFiles)
	}

	return strings.TrimSpace(b.String())
}

// countPair is a key with its count, used for stable descending sorts.
type countPair struct {
	key   string
	count int
}

// sortedCounts returns the map entries sorted by count descending. Ties are
// broken by key ascending for deterministic output (Rust's sort_by on counts is
// stable but iteration order of the HashMap before sorting is not, so we pin a
// total order).
func sortedCounts(m map[string]int) []countPair {
	out := make([]countPair, 0, len(m))
	for k, v := range m {
		out = append(out, countPair{key: k, count: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].count != out[j].count {
			return out[i].count > out[j].count
		}
		return out[i].key < out[j].key
	})
	return out
}

func takeCounts(s []countPair, n int) []countPair {
	if n > len(s) {
		n = len(s)
	}
	return s[:n]
}

// linterGroup is a linter name with the indices of its issues in one file.
type linterGroup struct {
	linter string
	idxs   []int
}

func sortedLinterGroups(m map[string][]int) []linterGroup {
	out := make([]linterGroup, 0, len(m))
	for k, v := range m {
		out = append(out, linterGroup{linter: k, idxs: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].idxs) != len(out[j].idxs) {
			return len(out[i].idxs) > len(out[j].idxs)
		}
		return out[i].linter < out[j].linter
	})
	return out
}

func takeLinterGroups(s []linterGroup, n int) []linterGroup {
	if n > len(s) {
		n = len(s)
	}
	return s[:n]
}

// compactPath shortens a file path by anchoring on a recognized Go layout
// segment (pkg/, cmd/, internal/), or otherwise reducing to the base name.
// Backslashes are normalized to forward slashes first (Windows-friendly).
func compactPath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")

	if pos := strings.LastIndex(path, "/pkg/"); pos >= 0 {
		return "pkg/" + path[pos+5:]
	}
	if pos := strings.LastIndex(path, "/cmd/"); pos >= 0 {
		return "cmd/" + path[pos+5:]
	}
	if pos := strings.LastIndex(path, "/internal/"); pos >= 0 {
		return "internal/" + path[pos+10:]
	}
	if pos := strings.LastIndexByte(path, '/'); pos >= 0 {
		return path[pos+1:]
	}
	return path
}

// truncate shortens s to at most maxLen characters (runes), appending "..." when
// it cuts. Mirrors rtk's utils::truncate char-based semantics.
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

// truncateChars returns the first maxLen characters (runes) of s, no ellipsis.
// Mirrors Rust's char_indices().nth(maxLen) slice used for source lines.
func truncateChars(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen])
}
