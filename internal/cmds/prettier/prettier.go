// Package prettier is gortk's token-optimized Prettier wrapper. It runs the
// `prettier` formatter (resolving it via the project's package manager when not
// directly on PATH) and compresses its output to show only the files that need
// formatting. Faithful port of rtk's src/cmds/js/prettier_cmd.rs.
package prettier

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
		Name:    "prettier",
		Summary: "Prettier format checker with compact output",
		Run:     Run,
	})
}

// Run executes the prettier command.
func Run(args []string, verbose int) (int, error) {
	cmd := packageManagerExec("prettier")
	cmd.Args = append(cmd.Args, args...)

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: prettier %s\n", strings.Join(args, " "))
	}

	opts := core.RunOptions{FilterStdoutOnly: true}
	return core.RunFiltered(cmd, "prettier", strings.Join(args, " "), filterPrettierOutput, opts)
}

// detectPackageManager picks the package manager based on lockfiles in the
// current directory, mirroring rtk's detect_package_manager.
func detectPackageManager() string {
	if fileExists("pnpm-lock.yaml") {
		return "pnpm"
	}
	if fileExists("yarn.lock") {
		return "yarn"
	}
	return "npm"
}

func fileExists(name string) bool {
	_, err := os.Stat(filepath.Clean(name))
	return err == nil
}

// packageManagerExec builds an *exec.Cmd that runs the named tool. If the tool
// is directly resolvable on PATH it is invoked directly; otherwise it is run
// through the detected package manager's exec mechanism. Mirrors rtk's
// package_manager_exec.
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

// maxPrettierFiles caps how many file paths are listed; matches rtk's
// CAP_WARNINGS (10).
const maxPrettierFiles = core.CapWarnings

// filterPrettierOutput compresses Prettier output to show only the files that
// need formatting. Pure function — the behavioural heart of the command.
func filterPrettierOutput(output string) string {
	// #221: empty or whitespace-only output means prettier didn't run.
	if strings.TrimSpace(output) == "" {
		return "Error: prettier produced no output"
	}

	var filesToFormat []string
	filesChecked := 0
	isCheckMode := true

	for _, line := range lines(output) {
		trimmed := strings.TrimSpace(line)

		// Detect check mode vs write mode.
		if strings.Contains(trimmed, "Checking formatting") {
			isCheckMode = true
		}

		// Count files that need formatting (check mode).
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

		// Count total files checked.
		if strings.Contains(trimmed, "All matched files use Prettier") {
			if fields := strings.Fields(trimmed); len(fields) > 0 {
				if count, err := parseUint(fields[0]); err == nil {
					filesChecked = count
				}
			}
		}
	}

	// Check if all files are formatted.
	if len(filesToFormat) == 0 && strings.Contains(output, "All matched files use Prettier") {
		return "Prettier: All files formatted correctly"
	}

	// Check if files were written (write mode).
	if strings.Contains(output, "modified") || strings.Contains(output, "formatted") {
		isCheckMode = false
	}

	var result strings.Builder

	if isCheckMode {
		// Check mode: show files that need formatting.
		if len(filesToFormat) == 0 {
			result.WriteString("Prettier: All files formatted correctly\n")
		} else {
			fmt.Fprintf(&result, "Prettier: %d files need formatting\n", len(filesToFormat))

			limit := maxPrettierFiles
			if limit > len(filesToFormat) {
				limit = len(filesToFormat)
			}
			for i, file := range filesToFormat[:limit] {
				fmt.Fprintf(&result, "%d. %s\n", i+1, file)
			}

			if len(filesToFormat) > maxPrettierFiles {
				fmt.Fprintf(&result, "\n... +%d more files\n", len(filesToFormat)-maxPrettierFiles)
			}

			if filesChecked > 0 {
				fmt.Fprintf(&result, "\n%d files already formatted\n", filesChecked-len(filesToFormat))
			}
		}
	} else {
		// Write mode: show what was formatted.
		fmt.Fprintf(&result, "Prettier: %d files formatted\n", len(filesToFormat))
	}

	return strings.TrimSpace(result.String())
}

// lines mirrors Rust's str::lines(): splits on '\n' and drops a single trailing
// empty element so a final newline does not yield a phantom blank line.
func lines(s string) []string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	parts := strings.Split(s, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}

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
