package container

import (
	"fmt"
	"os"
	"strings"

	"gortk/internal/core"
)

const maxComposeServices = core.CapList

// dockerCompose dispatches `gortk docker compose <subcommand>`: ps [-a], logs
// [service], build [service], and passthrough for everything else. Mirrors the
// ComposeCommands arm in main.rs.
func dockerCompose(args []string, verbose int) (int, error) {
	if len(args) == 0 {
		return core.RunPassthrough("docker", []string{"compose"}, verbose)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "ps":
		all := hasFlag(rest, "-a", "--all")
		return composePS(all, verbose)
	case "logs":
		return composeLogs(rest, verbose)
	case "build":
		return composeBuild(rest, verbose)
	default:
		return core.RunPassthrough("docker", append([]string{"compose"}, args...), verbose)
	}
}

func composePS(all bool, verbose int) (int, error) {
	formatArgs := []string{"compose", "ps"}
	if all {
		formatArgs = append(formatArgs, "-a")
	}
	formatArgs = append(formatArgs, "--format", "{{.Name}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}")

	result := execCapture(core.ResolvedCommand("docker", formatArgs...))
	if result.startErr != nil {
		return 127, fmt.Errorf("gortk: failed to run docker: %w", result.startErr)
	}
	if !result.success() {
		fmt.Fprintln(os.Stderr, result.stderr)
		return result.exitCode, nil
	}
	fmt.Println(formatComposePS(result.stdout))
	return 0, nil
}

func composeLogs(args []string, verbose int) (int, error) {
	cmdArgs := []string{"compose", "logs", "--tail", "100"}
	svcLabel := "all"
	// first non-flag arg is the optional service name
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			cmdArgs = append(cmdArgs, a)
			svcLabel = a
			break
		}
	}
	cmd := core.ResolvedCommand("docker", cmdArgs...)
	opts := core.RunOptions{SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, "docker", "compose logs "+svcLabel, formatComposeLogs, opts)
}

func composeBuild(args []string, verbose int) (int, error) {
	cmdArgs := []string{"compose", "build"}
	svcLabel := "all"
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			cmdArgs = append(cmdArgs, a)
			svcLabel = a
			break
		}
	}
	cmd := core.ResolvedCommand("docker", cmdArgs...)
	opts := core.RunOptions{SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, "docker", "compose build "+svcLabel, formatComposeBuild, opts)
}

// formatComposePS compresses `docker compose ps --format` output into compact
// form. Expects tab-separated headerless lines: Name\tImage\tStatus\tPorts.
//
// Faithful port of rtk's format_compose_ps.
func formatComposePS(raw string) string {
	var lines []string
	for _, l := range splitLines(raw) {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return "[compose] 0 services"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[compose] %d services:\n", len(lines))

	var allFormatted []string
	for _, line := range lines {
		parts := strings.Split(line, "\t")
		if len(parts) < 4 {
			continue
		}
		name := parts[0]
		image := parts[1]
		status := parts[2]
		ports := parts[3]
		shortImage := lastPathSegment(image)
		portStr := ""
		if strings.TrimSpace(ports) != "" {
			compact := compactPorts(strings.TrimSpace(ports))
			if compact != "-" {
				portStr = " [" + compact + "]"
			}
		}
		allFormatted = append(allFormatted, fmt.Sprintf("  %s (%s) %s%s", name, shortImage, status, portStr))
	}

	for _, line := range take(allFormatted, maxComposeServices) {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if len(allFormatted) > maxComposeServices {
		fmt.Fprintf(&b, "  … +%d more\n", len(allFormatted)-maxComposeServices)
	}

	return strings.TrimRight(b.String(), "\n")
}

// formatComposeLogs labels `docker compose logs` output. rtk runs this through
// its separate log-deduplication engine; that engine is a different command
// module and out of scope for this port, so here we preserve the header and
// empty-input behaviour without inventing a dedup engine.
//
// Faithful (structural) port of rtk's format_compose_logs.
func formatComposeLogs(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "[compose] No logs"
	}
	return fmt.Sprintf("[compose] Logs:\n%s", raw)
}

// formatComposeBuild compresses `docker compose build` output into a summary:
// the FINISHED line (or first Building line), the set of service names from
// build steps like "[web 1/4]", and a count of build steps.
//
// Faithful port of rtk's format_compose_build.
func formatComposeBuild(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "[compose] Build: no output"
	}

	var result strings.Builder

	// Extract the summary line: "[+] Building 12.3s (8/8) FINISHED"
	foundFinished := false
	for _, line := range splitLines(raw) {
		if strings.Contains(line, "Building") && strings.Contains(line, "FINISHED") {
			fmt.Fprintf(&result, "[compose] %s\n", strings.TrimSpace(line))
			foundFinished = true
			break
		}
	}

	if !foundFinished {
		// No FINISHED line — might still be building or errored.
		building := ""
		for _, line := range splitLines(raw) {
			if strings.Contains(line, "Building") {
				building = line
				break
			}
		}
		if building != "" {
			fmt.Fprintf(&result, "[compose] %s\n", strings.TrimSpace(building))
		} else {
			result.WriteString("[compose] Build:\n")
		}
	}

	// Collect unique service names from build steps like "[web 1/4]".
	var services []string
	seen := map[string]bool{}
	for _, line := range splitLines(raw) {
		start := strings.Index(line, "[")
		if start < 0 {
			continue
		}
		rest := line[start+1:]
		end := strings.Index(rest, "]")
		if end < 0 {
			continue
		}
		bracket := rest[:end]
		svc := ""
		if fields := strings.Fields(bracket); len(fields) > 0 {
			svc = fields[0]
		}
		if svc != "" && svc != "+" && !seen[svc] {
			seen[svc] = true
			services = append(services, svc)
		}
	}
	if len(services) > 0 {
		fmt.Fprintf(&result, "  Services: %s\n", strings.Join(services, ", "))
	}

	// Count build steps (lines starting with "=> ").
	stepCount := 0
	for _, line := range splitLines(raw) {
		if strings.HasPrefix(strings.TrimLeft(line, " \t"), "=> ") {
			stepCount++
		}
	}
	if stepCount > 0 {
		fmt.Fprintf(&result, "  Steps: %d", stepCount)
	}

	return strings.TrimRight(result.String(), "\n")
}
