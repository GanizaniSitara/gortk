// Package grep is gortk's token-optimized grep wrapper. It runs ripgrep (rg),
// falling back to the system grep, then compresses the result: each match line
// is whitespace-stripped and truncated to a max length, matches are grouped by
// file, and the whole thing is capped to a result budget. Faithful port of
// rtk's src/cmds/system/grep_cmd.rs.
//
// Like rtk, this wraps the platform `rg`/`grep`; gortk resolves them PATHEXT-aware
// via core.ResolvedCommand. The output-compression logic (clean_line,
// parse_match_line, compact_path, …) lives in pure helper functions so it can be
// tested directly against the ported Rust spec.
//
// gortk has no clap layer, so Run parses the gortk-level flags (-l/--max-len,
// -m/--max, --context-only, -t/--file-type) out of args itself, leaving the
// remainder as the trailing args that get forwarded to rg/grep (mirroring rtk's
// extra_args after clap's trailing_var_arg).
package grep

import (
	"bytes"
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
		Name:    "grep",
		Summary: "Compact grep - strips whitespace, truncates, groups by file",
		Run:     Run,
	})
}

// grepMaxPerFile mirrors rtk's config::limits().grep_max_per_file (default 25):
// the maximum matches shown per file before the overflow count kicks in.
const grepMaxPerFile = 25

// Defaults mirror rtk's clap declaration on Commands::Grep:
//
//	-l/--max-len      default 80
//	-m/--max          default 200
//	--context-only    bool
//	-t/--file-type    Option<String>
const (
	defaultMaxLineLen = 80
	defaultMaxResults = 200
)

// matchLineRE parses a single rg/grep match line of the form
// `file\0line_number:content` (NUL-separated, from rg -0 / grep --null).
// Mirrors rtk's MATCH_LINE_RE.
var matchLineRE = regexp.MustCompile("^([^\x00]+)\x00(\\d+):(.*)$")

// capResult mirrors rtk's core::stream::CaptureResult. We capture the child's
// output so we can inspect and recompress it, exactly as exec_capture does in
// rtk.
type capResult struct {
	stdout   string
	stderr   string
	exitCode int
	startErr error
}

// execCapture runs cmd with stdin detached, captures stdout/stderr, and
// normalizes newlines. Mirrors rtk's exec_capture (and git.go's execCapture).
func execCapture(cmd *exec.Cmd) capResult {
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	cmd.Stdin = nil
	err := cmd.Run()
	return capResult{
		stdout:   core.NormalizeNewlines(outBuf.String()),
		stderr:   core.NormalizeNewlines(errBuf.String()),
		exitCode: core.ExitCodeFromError(err),
		startErr: startErr(err),
	}
}

// startErr returns a non-nil error only when the process failed to start (the
// binary could not be found / spawned), not when it merely exited non-zero.
func startErr(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return nil
	}
	return err
}

// grepCommandLabel builds the "grep <args>" command string for tracking.
func grepCommandLabel(args []string) string {
	if len(args) == 0 {
		return "grep"
	}
	return "grep " + strings.Join(args, " ")
}

// Run dispatches the gortk `grep` command. args are the tokens after "grep";
// verbose is the -v count. It returns the process exit code.
func Run(args []string, verbose int) (int, error) {
	timer := core.StartTimer()

	// --version / --help / -h: pass through to rg without filtering. rtk handles
	// this before any other parsing; we do the same so e.g. `gortk grep --help`
	// shows rg's help verbatim.
	for _, a := range args {
		if a == "--version" || a == "--help" || a == "-h" {
			code, err := runVersionHelp(args)
			// --version/--help echo rg's output verbatim (no compaction).
			timer.TrackPassthrough(grepCommandLabel(args), "gortk grep")
			return code, err
		}
	}

	// Parse the gortk-level flags out of args, leaving the rest as the trailing
	// args forwarded to rg/grep (rtk's extra_args). There is no clap layer here.
	maxLineLen, maxResults, contextOnly, fileType, rest := parseGrepFlags(args)

	patterns, paths, extraArgs := extractPatternPath(rest)

	if len(patterns) == 0 {
		fmt.Fprintln(os.Stderr, "gortk grep: pattern required (positional or -e)")
		return 1, nil
	}

	patternDisplay := patterns[0]
	if len(patterns) > 1 {
		patternDisplay = strings.Join(patterns, "|")
	}

	if len(paths) == 0 {
		paths = []string{"."}
	}
	pathDisplay := strings.Join(paths, " ")

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "grep: '%s' in %s\n", patternDisplay, pathDisplay)
	}

	res := runRgOrGrep(fileType, extraArgs, patterns, paths)
	if res.startErr != nil {
		return 127, fmt.Errorf("gortk: grep/rg failed: %w", res.startErr)
	}

	// Passthrough output flags that already produce small output.
	if hasFormatFlag(extraArgs) {
		fmt.Print(res.stdout)
		if strings.TrimSpace(res.stderr) != "" {
			fmt.Fprint(os.Stderr, strings.TrimSpace(res.stderr))
		}
		timer.TrackPassthrough(grepCommandLabel(args), "gortk grep")
		return res.exitCode, nil
	}

	exitCode := res.exitCode

	if strings.TrimSpace(res.stdout) == "" {
		if isGrepErrorExit(exitCode) {
			if strings.TrimSpace(res.stderr) != "" {
				fmt.Fprintln(os.Stderr, strings.TrimSpace(res.stderr))
			}
			fmt.Fprintf(os.Stderr, "grep failed with exit code %d\n", exitCode)
			timer.TrackPassthrough(grepCommandLabel(args), "gortk grep")
			return exitCode, nil
		}
		noMatch := fmt.Sprintf("0 matches for '%s'\n", patternDisplay)
		fmt.Print(noMatch)
		// raw = empty rg/grep output; filtered = the "0 matches" line.
		timer.Track(grepCommandLabel(args), "gortk grep", res.stdout, noMatch)
		return exitCode, nil
	}

	output := compactMatches(res.stdout, maxLineLen, maxResults, contextOnly, patternDisplay)
	fmt.Print(output)
	// raw = full rg/grep match output; filtered = gortk's compacted report.
	timer.Track(grepCommandLabel(args), "gortk grep", res.stdout, output)
	return exitCode, nil
}

// runVersionHelp passes args straight to rg (then grep) for --version/--help and
// prints the raw output. Mirrors rtk's early version/help passthrough.
func runVersionHelp(args []string) (int, error) {
	cmd := core.ResolvedCommand("rg", args...)
	res := execCapture(cmd)
	if res.startErr != nil {
		// rg unavailable: fall back to system grep.
		res = execCapture(core.ResolvedCommand("grep", args...))
		if res.startErr != nil {
			return 127, fmt.Errorf("gortk: grep/rg failed: %w", res.startErr)
		}
	}
	fmt.Print(res.stdout)
	if res.stderr != "" {
		fmt.Fprint(os.Stderr, res.stderr)
	}
	return res.exitCode, nil
}

// runRgOrGrep runs rg with gortk's standard flags and the parsed patterns/paths,
// falling back to system grep if rg cannot be started. Mirrors rtk's run() body
// from the rg invocation through the grep fallback.
func runRgOrGrep(fileType string, extraArgs, patterns, paths []string) capResult {
	// rg invocation flags (rtk grep_cmd.rs):
	//   -nH0          line numbers, always show filename, NUL-separate filename.
	//   --no-heading  flat file:line:content form for the parser.
	//   --no-ignore-vcs  match `grep -r` (don't skip .gitignore'd files), while
	//                    still respecting .ignore/.rgignore (false negatives here
	//                    make agents draw wrong conclusions).
	rgArgs := []string{"-nH0", "--no-heading", "--no-ignore-vcs"}
	if fileType != "" {
		rgArgs = append(rgArgs, "--type", fileType)
	}
	// extraArgs is already stripped of -r/-R/--recursive by extractPatternPath.
	rgArgs = append(rgArgs, extraArgs...)
	// All patterns as -e flags (BRE \| -> | translation for rg's PCRE engine).
	// Using -e keeps `--` as a flag/path separator, not part of the pattern.
	for _, p := range patterns {
		rgArgs = append(rgArgs, "-e", strings.ReplaceAll(p, `\|`, "|"))
	}
	// `--` after all flags: stop rg treating path args starting with `-` as flags.
	rgArgs = append(rgArgs, "--")
	rgArgs = append(rgArgs, paths...)

	res := execCapture(core.ResolvedCommand("rg", rgArgs...))
	if res.startErr == nil {
		return res
	}

	// rg unavailable: fall back to system grep with the original, untranslated
	// patterns (grep interprets BRE natively).
	grepArgs := append([]string{}, extraArgs...)
	for _, p := range patterns {
		grepArgs = append(grepArgs, "-e", p)
	}
	// --null (not -Z): on BSD/macOS grep -Z means --decompress, not the NUL
	// filename separator parseMatchLine needs (rtk issue #2310).
	grepArgs = append(grepArgs, "-rnH", "--null", "--")
	grepArgs = append(grepArgs, paths...)
	return execCapture(core.ResolvedCommand("grep", grepArgs...))
}

// compactMatches groups rg/grep output by file, cleans each line, caps per-file
// and total matches, and renders the compact report. Pure function so it can be
// tested directly. Mirrors the second half of rtk's run().
func compactMatches(rawStdout string, maxLineLen, maxResults int, contextOnly bool, patternDisplay string) string {
	var contextRE *regexp.Regexp
	if contextOnly {
		// (?i).{0,20}<escaped-pattern>.*
		if re, err := regexp.Compile("(?i)" + ".{0,20}" + regexp.QuoteMeta(patternDisplay) + ".*"); err == nil {
			contextRE = re
		}
	}

	type match struct {
		lineNum int
		content string
	}
	byFile := map[string][]match{}
	for _, line := range splitLines(rawStdout) {
		file, lineNum, content, ok := parseMatchLine(line)
		if !ok {
			continue
		}
		cleaned := cleanLine(content, maxLineLen, contextRE, patternDisplay)
		byFile[file] = append(byFile[file], match{lineNum, cleaned})
	}

	// Derive total from parsed results so the header matches what we show.
	totalMatches := 0
	for _, v := range byFile {
		totalMatches += len(v)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "%d matches in %d files:\n\n", totalMatches, len(byFile))

	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)

	shown := 0
	for _, file := range files {
		if shown >= maxResults {
			break
		}
		fileDisplay := compactPath(file)
		matches := byFile[file]
		limit := len(matches)
		if limit > grepMaxPerFile {
			limit = grepMaxPerFile
		}
		for _, m := range matches[:limit] {
			if shown >= maxResults {
				break
			}
			fmt.Fprintf(&out, "%s:%d:%s\n", fileDisplay, m.lineNum, m.content)
			shown++
		}
	}

	if totalMatches > shown {
		fmt.Fprintf(&out, "[+%d more]\n", totalMatches-shown)
	}

	return out.String()
}

// parseMatchLine parses a single rg/grep match line of the form
// `file\0line_number:content`. Returns ok=false for lines that do not match the
// expected NUL-separated shape (e.g. rg -A/-B context lines using `-`).
func parseMatchLine(line string) (file string, lineNum int, content string, ok bool) {
	m := matchLineRE.FindStringSubmatch(line)
	if m == nil {
		return "", 0, "", false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, "", false
	}
	return m[1], n, m[3], true
}

// hasFormatFlag reports whether extraArgs contains an output-altering flag whose
// result is already small and must bypass gortk's recompression. Mirrors rtk's
// has_format_flag.
func hasFormatFlag(extraArgs []string) bool {
	for _, a := range extraArgs {
		switch a {
		case "-c", "--count", "--count-matches",
			"-l", "--files-with-matches",
			"-L", "--files-without-match",
			"-o", "--only-matching",
			"-Z", "--null",
			"--json", "--passthru", "--files":
			return true
		}
	}
	return false
}

// cleanLine trims a match line and, if longer than maxLen, truncates it around
// the match (or the head) with ellipses. When contextRE is set and matches, a
// short context slice is returned instead. All slicing is rune-based so multibyte
// / emoji content never panics. Mirrors rtk's clean_line.
func cleanLine(line string, maxLen int, contextRE *regexp.Regexp, pattern string) string {
	trimmed := strings.TrimSpace(line)

	if contextRE != nil {
		if loc := contextRE.FindString(trimmed); loc != "" {
			// rtk compares matched.len() (bytes) against max_len.
			if len(loc) <= maxLen {
				return loc
			}
		}
	}

	// rtk compares trimmed.len() (bytes) against max_len for the fast path.
	if len(trimmed) <= maxLen {
		return trimmed
	}

	lower := strings.ToLower(trimmed)
	patternLower := strings.ToLower(pattern)

	if pos := strings.Index(lower, patternLower); pos >= 0 {
		// char_pos = number of chars before the byte offset pos in lower.
		charPos := len([]rune(lower[:pos]))
		chars := []rune(trimmed)
		charLen := len(chars)

		start := charPos - maxLen/3
		if start < 0 {
			start = 0
		}
		end := start + maxLen
		if end > charLen {
			end = charLen
		}
		if end == charLen {
			start = end - maxLen
			if start < 0 {
				start = 0
			}
		}

		slice := string(chars[start:end])
		switch {
		case start > 0 && end < charLen:
			return "..." + slice + "..."
		case start > 0:
			return "..." + slice
		default:
			return slice + "..."
		}
	}

	// No match found in the (long) line: take the first maxLen-3 chars + "...".
	chars := []rune(trimmed)
	take := maxLen - 3
	if take < 0 {
		take = 0
	}
	if take > len(chars) {
		take = len(chars)
	}
	return string(chars[:take]) + "..."
}

// compactPath shortens long, deep paths to `first/.../parent/file`. Mirrors
// rtk's compact_path (and its `/` split — rg emits forward-slash paths).
func compactPath(path string) string {
	if len(path) <= 50 {
		return path
	}
	parts := strings.Split(path, "/")
	if len(parts) <= 3 {
		return path
	}
	return fmt.Sprintf("%s/.../%s/%s", parts[0], parts[len(parts)-2], parts[len(parts)-1])
}

// isGrepErrorExit reports whether a grep/rg exit code is a real error. Per the
// grep/rg convention: exit 0 = matches, exit 1 = no match (both normal), exit
// >= 2 = real error (bad regex, tool crash, missing binary). Errors must surface
// to the user, never be reported as a false "0 matches".
func isGrepErrorExit(exitCode int) bool {
	return exitCode >= 2
}

// splitLines splits text on "\n" and drops a trailing empty element so it matches
// Rust's str::lines() semantics (mirrors git.go's splitLines).
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
