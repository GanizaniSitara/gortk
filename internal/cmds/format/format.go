// Package format is gortk's code-formatter wrapper. It auto-detects the project
// formatter (Prettier, Ruff, or Black) — or takes one explicitly — runs it in
// check mode, and compresses the output to show only the files that changed.
// Faithful port of rtk's src/cmds/system/format_cmd.rs.
//
// rtk's format dispatches to three filters: prettier, ruff-format, and black.
// gortk's prettier and ruff packages keep their filters unexported, so the
// prettier and ruff-format filters are ported locally here to keep this package
// self-contained (it writes only inside its own directory). The compression
// logic lives in pure functions so it can be tested directly against the ported
// Rust spec.
package format

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "format",
		Summary: "Run code formatters and show only what changed",
		Run:     Run,
	})
}

// detectFormatter detects the formatter from project files or an explicit first
// argument, using the current directory.
func detectFormatter(args []string) string {
	return detectFormatterInDir(args, ".")
}

// detectFormatterInDir detects the formatter with an explicit directory (used by
// tests). Priority: explicit arg > pyproject.toml > package.json/prettier
// config > ruff fallback. Faithful port of rtk's detect_formatter_in_dir.
func detectFormatterInDir(args []string, dir string) string {
	// Check if first arg is a known formatter.
	if len(args) > 0 {
		switch args[0] {
		case "prettier", "black", "ruff", "biome":
			return args[0]
		}
	}

	// Auto-detect from project files.
	// Priority: pyproject.toml > package.json > fallback.
	pyprojectPath := filepath.Join(dir, "pyproject.toml")
	if fileExists(pyprojectPath) {
		if content, err := os.ReadFile(pyprojectPath); err == nil {
			text := string(content)
			// Check for [tool.black] section.
			if strings.Contains(text, "[tool.black]") {
				return "black"
			}
			// Check for [tool.ruff.format] section.
			if strings.Contains(text, "[tool.ruff.format]") || strings.Contains(text, "[tool.ruff]") {
				return "ruff"
			}
		}
	}

	// Check for package.json or prettier config.
	if fileExists(filepath.Join(dir, "package.json")) ||
		fileExists(filepath.Join(dir, ".prettierrc")) ||
		fileExists(filepath.Join(dir, ".prettierrc.json")) ||
		fileExists(filepath.Join(dir, ".prettierrc.js")) {
		return "prettier"
	}

	// Fallback: try ruff -> black -> prettier in order.
	return "ruff"
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Run executes the format command. args are the tokens after "format"; verbose
// is the -v count. It returns the wrapped formatter's exit code.
func Run(args []string, verbose int) (int, error) {
	// Detect formatter.
	formatter := detectFormatter(args)

	// Determine start index for actual arguments.
	startIdx := 0
	if len(args) > 0 && args[0] == formatter {
		startIdx = 1 // Skip formatter name if it was explicitly provided.
	}

	userArgs := args[startIdx:]

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Detected formatter: %s\n", formatter)
		fmt.Fprintf(os.Stderr, "Arguments: %s\n", strings.Join(userArgs, " "))
	}

	// Build command based on formatter.
	var cmd *exec.Cmd
	switch formatter {
	case "prettier", "biome":
		cmd = packageManagerExec(formatter)
	default: // "black", "ruff", or anything else
		cmd = core.ResolvedCommand(formatter)
	}

	// Add formatter-specific flags.
	switch formatter {
	case "black":
		// Inject --check if not present for check mode.
		hasCheckOrDiff := false
		for _, a := range userArgs {
			if a == "--check" || a == "--diff" {
				hasCheckOrDiff = true
				break
			}
		}
		if !hasCheckOrDiff {
			cmd.Args = append(cmd.Args, "--check")
		}
	case "ruff":
		// Add "format" subcommand if not present.
		if len(userArgs) == 0 || !strings.HasPrefix(userArgs[0], "format") {
			cmd.Args = append(cmd.Args, "format")
		}
	}

	// Add user arguments.
	cmd.Args = append(cmd.Args, userArgs...)

	// Default to current directory if no path specified.
	allFlags := true
	for _, a := range userArgs {
		if !strings.HasPrefix(a, "-") {
			allFlags = false
			break
		}
	}
	if allFlags {
		cmd.Args = append(cmd.Args, ".")
	}

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: %s %s\n", formatter, strings.Join(userArgs, " "))
	}

	// rtk combines stdout+stderr into `raw` then dispatches by formatter. We let
	// core.RunFiltered capture both (FilterStdoutOnly: false → filter sees the
	// combined raw output) and dispatch inside the closure. The pure filters are
	// what the ported tests pin.
	opts := core.RunOptions{}
	return core.RunFiltered(cmd, "format", strings.TrimSpace(formatter+" "+strings.Join(userArgs, " ")), func(raw string) string {
		switch formatter {
		case "prettier":
			return filterPrettierOutput(raw)
		case "ruff":
			return filterRuffFormat(raw)
		case "black":
			return filterBlackOutput(raw)
		default:
			return strings.TrimSpace(raw)
		}
	}, opts)
}

// packageManagerExec builds an *exec.Cmd that runs the named tool directly when
// it is on PATH, otherwise through the detected JS package manager's exec
// mechanism. Mirrors rtk's package_manager_exec.
func packageManagerExec(tool string) *exec.Cmd {
	if core.ToolExists(tool) {
		return core.ResolvedCommand(tool)
	}
	switch detectPackageManager() {
	case "pnpm":
		return core.ResolvedCommand("pnpm", "exec", "--", tool)
	case "yarn":
		return core.ResolvedCommand("yarn", "exec", "--", tool)
	default:
		return core.ResolvedCommand("npx", "--no-install", "--", tool)
	}
}

// detectPackageManager picks the JS package manager based on lockfiles in the
// current directory.
func detectPackageManager() string {
	if fileExists("pnpm-lock.yaml") {
		return "pnpm"
	}
	if fileExists("yarn.lock") {
		return "yarn"
	}
	return "npm"
}

// maxFormatFiles caps how many file paths are listed; matches rtk's CAP_WARNINGS.
const maxFormatFiles = core.CapWarnings

// filterBlackOutput compresses black's output, showing the files that need
// formatting. Pure function — faithful port of rtk's filter_black_output.
func filterBlackOutput(output string) string {
	var filesToFormat []string
	filesUnchanged := 0
	filesWouldReformat := 0
	allDone := false
	ohNo := false

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		// "would reformat: path/to/file.py" lines.
		if strings.HasPrefix(lower, "would reformat:") {
			if parts := strings.SplitN(trimmed, ":", 2); len(parts) >= 2 {
				filesToFormat = append(filesToFormat, strings.TrimSpace(parts[1]))
			}
		}

		// Summary line like "2 files would be reformatted, 3 files would be left unchanged."
		if strings.Contains(lower, "would be reformatted") || strings.Contains(lower, "would be left unchanged") {
			for _, part := range strings.Split(trimmed, ",") {
				partLower := strings.ToLower(part)
				words := strings.Fields(part)

				if strings.Contains(partLower, "would be reformatted") {
					if n, ok := countBeforeFileWord(words); ok {
						filesWouldReformat = n
					}
				}
				if strings.Contains(partLower, "would be left unchanged") {
					if n, ok := countBeforeFileWord(words); ok {
						filesUnchanged = n
					}
				}
			}
		}

		// "left unchanged" (standalone).
		if strings.Contains(lower, "left unchanged") && !strings.Contains(lower, "would be") {
			words := strings.Fields(trimmed)
			if n, ok := countBeforeFileWord(words); ok {
				filesUnchanged = n
			}
		}

		// Success/failure indicators.
		if strings.Contains(lower, "all done!") || strings.Contains(lower, "all done ✨") {
			allDone = true
		}
		if strings.Contains(lower, "oh no!") {
			ohNo = true
		}
	}

	var result strings.Builder

	needsFormatting := len(filesToFormat) > 0 || filesWouldReformat > 0 || ohNo

	if !needsFormatting && (allDone || filesUnchanged > 0) {
		// All files formatted correctly.
		result.WriteString("Format (black): All files formatted")
		if filesUnchanged > 0 {
			fmt.Fprintf(&result, " (%d files checked)", filesUnchanged)
		}
	} else if needsFormatting {
		// Files need formatting.
		count := filesWouldReformat
		if len(filesToFormat) > 0 {
			count = len(filesToFormat)
		}

		fmt.Fprintf(&result, "Format (black): %d files need formatting\n", count)

		if len(filesToFormat) > 0 {
			limit := len(filesToFormat)
			if limit > maxFormatFiles {
				limit = maxFormatFiles
			}
			for i, file := range filesToFormat[:limit] {
				fmt.Fprintf(&result, "%d. %s\n", i+1, compactPath(file))
			}
			if len(filesToFormat) > maxFormatFiles {
				fmt.Fprintf(&result, "\n... +%d more files\n", len(filesToFormat)-maxFormatFiles)
			}
		}

		if filesUnchanged > 0 {
			fmt.Fprintf(&result, "\n%d files already formatted\n", filesUnchanged)
		}

		result.WriteString("\n[hint] Run `black .` to format these files\n")
	} else {
		// Fallback: show raw output.
		result.WriteString(strings.TrimSpace(output))
	}

	return strings.TrimSpace(result.String())
}

// countBeforeFileWord finds a "file"/"files" word and returns the integer in the
// word immediately before it, mirroring rtk's per-part count parsing.
func countBeforeFileWord(words []string) (int, bool) {
	for i, word := range words {
		if (word == "file" || word == "files") && i > 0 {
			if n, ok := parseUint(words[i-1]); ok {
				return n, true
			}
		}
	}
	return 0, false
}

// compactPath shortens a file path by anchoring on a recognized source root
// (src/, lib/, tests/) or falling back to the basename. Backslashes are
// normalized to forward slashes first. Faithful port of rtk's compact_path.
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

// filterPrettierOutput compresses Prettier output to show only files that need
// formatting. Locally ported from rtk's prettier_cmd::filter_prettier_output so
// the format dispatcher stays self-contained.
func filterPrettierOutput(output string) string {
	if strings.TrimSpace(output) == "" {
		return "Error: prettier produced no output"
	}

	var filesToFormat []string
	filesChecked := 0
	isCheckMode := true

	for _, line := range lines(output) {
		trimmed := strings.TrimSpace(line)

		if strings.Contains(trimmed, "Checking formatting") {
			isCheckMode = true
		}

		if trimmed != "" &&
			!strings.HasPrefix(trimmed, "Checking") &&
			!strings.HasPrefix(trimmed, "All matched") &&
			!strings.HasPrefix(trimmed, "Code style") &&
			!strings.Contains(trimmed, "[warn]") &&
			!strings.Contains(trimmed, "[error]") &&
			(strings.HasSuffix(trimmed, ".ts") ||
				strings.HasSuffix(trimmed, ".tsx") ||
				strings.HasSuffix(trimmed, ".js") ||
				strings.HasSuffix(trimmed, ".jsx") ||
				strings.HasSuffix(trimmed, ".json") ||
				strings.HasSuffix(trimmed, ".md") ||
				strings.HasSuffix(trimmed, ".css") ||
				strings.HasSuffix(trimmed, ".scss")) {
			filesToFormat = append(filesToFormat, trimmed)
		}

		if strings.Contains(trimmed, "All matched files use Prettier") {
			if fields := strings.Fields(trimmed); len(fields) > 0 {
				if count, ok := parseUint(fields[0]); ok {
					filesChecked = count
				}
			}
		}
	}

	if len(filesToFormat) == 0 && strings.Contains(output, "All matched files use Prettier") {
		return "Prettier: All files formatted correctly"
	}

	if strings.Contains(output, "modified") || strings.Contains(output, "formatted") {
		isCheckMode = false
	}

	var result strings.Builder
	if isCheckMode {
		if len(filesToFormat) == 0 {
			result.WriteString("Prettier: All files formatted correctly\n")
		} else {
			fmt.Fprintf(&result, "Prettier: %d files need formatting\n", len(filesToFormat))
			limit := len(filesToFormat)
			if limit > maxFormatFiles {
				limit = maxFormatFiles
			}
			for i, file := range filesToFormat[:limit] {
				fmt.Fprintf(&result, "%d. %s\n", i+1, file)
			}
			if len(filesToFormat) > maxFormatFiles {
				fmt.Fprintf(&result, "\n... +%d more files\n", len(filesToFormat)-maxFormatFiles)
			}
			if filesChecked > 0 {
				fmt.Fprintf(&result, "\n%d files already formatted\n", filesChecked-len(filesToFormat))
			}
		}
	} else {
		fmt.Fprintf(&result, "Prettier: %d files formatted\n", len(filesToFormat))
	}

	return strings.TrimSpace(result.String())
}

// filterRuffFormat compresses `ruff format` output. Locally ported from rtk's
// ruff_cmd::filter_ruff_format so the format dispatcher stays self-contained.
func filterRuffFormat(output string) string {
	var filesToFormat []string
	filesChecked := 0

	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)

		if strings.Contains(lower, "would reformat:") {
			if parts := strings.SplitN(trimmed, ":", 2); len(parts) >= 2 {
				filesToFormat = append(filesToFormat, strings.TrimSpace(parts[1]))
			}
		}

		if strings.Contains(lower, "left unchanged") {
			for _, part := range strings.Split(trimmed, ",") {
				if strings.Contains(strings.ToLower(part), "left unchanged") {
					if n, ok := countBeforeFileWord(strings.Fields(part)); ok {
						filesChecked = n
					}
					break
				}
			}
		}
	}

	outputLower := strings.ToLower(output)

	if len(filesToFormat) == 0 && strings.Contains(outputLower, "left unchanged") {
		return "Ruff format: All files formatted correctly"
	}

	var result strings.Builder
	if strings.Contains(outputLower, "would reformat") {
		if len(filesToFormat) == 0 {
			result.WriteString("Ruff format: All files formatted correctly\n")
		} else {
			fmt.Fprintf(&result, "Ruff format: %d files need formatting\n", len(filesToFormat))
			limit := len(filesToFormat)
			if limit > maxFormatFiles {
				limit = maxFormatFiles
			}
			for i, file := range filesToFormat[:limit] {
				fmt.Fprintf(&result, "%d. %s\n", i+1, compactPath(file))
			}
			if len(filesToFormat) > maxFormatFiles {
				fmt.Fprintf(&result, "\n... +%d more files\n", len(filesToFormat)-maxFormatFiles)
			}
			if filesChecked > 0 {
				fmt.Fprintf(&result, "\n%d files already formatted\n", filesChecked)
			}
			result.WriteString("\n[hint] Run `ruff format` to format these files\n")
		}
	} else {
		result.WriteString(strings.TrimSpace(output))
	}

	return strings.TrimSpace(result.String())
}

// lines mirrors Rust's str::lines(): splits on '\n' and drops a single trailing
// empty element. CRLF is normalized to LF first.
func lines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	parts := strings.Split(s, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}

// parseUint parses a non-negative base-10 integer, mirroring Rust's
// str::parse::<usize>() (rejects empty, signs, and non-digits).
func parseUint(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}
