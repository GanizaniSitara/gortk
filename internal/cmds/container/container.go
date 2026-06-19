// Package container is gortk's token-optimized wrapper for container and
// orchestration tooling — Docker, kubectl, and OpenShift's oc. It filters the
// verbose default output of those tools into compact, agent-friendly summaries.
// Faithful port of rtk's src/cmds/cloud/container.rs.
//
// Like rtk, this wraps the platform `docker` / `kubectl` / `oc` binaries; gortk
// resolves them PATHEXT-aware via core.ResolvedCommand. The output-compression
// logic lives in pure helper functions (formatContainerLine, compactPorts,
// formatKubectlPods, formatKubectlServices, formatComposePS, …) so it can be
// tested directly against the ported Rust spec.
//
// Subcommands are parsed from args inside each Run function, mirroring how rtk's
// main.rs dispatches Docker / Kubectl / Oc to this module. rtk's log
// deduplication engine (a separate `log` command) is out of scope for this
// package: the `logs` subcommands run the tool and label the output rather than
// pulling in another module.
package container

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "docker",
		Summary: "Docker commands with compact output",
		Run:     RunDocker,
	})
	registry.Register(&registry.Cmd{
		Name:    "kubectl",
		Summary: "Kubectl commands with compact output",
		Run:     RunKubectl,
	})
	registry.Register(&registry.Cmd{
		Name:    "oc",
		Summary: "OpenShift (oc) commands with compact output",
		Run:     RunOc,
	})
}

// Truncation caps, mirroring the rtk constants this module uses.
const (
	maxContainers      = core.CapList      // docker ps
	maxContainersAll   = 20                // docker ps -a (rtk hardcodes 20)
	maxImages          = core.CapInventory // docker images
	maxPodsIssues      = core.CapWarnings  // kubectl get pods issues
	maxKubectlServices = core.CapList      // kubectl get services
)

// capResult mirrors rtk's core::stream::CaptureResult. The docker_ps /
// docker_images paths in rtk call exec_capture directly (rather than
// run_filtered) because they invoke docker twice — once plain to record the raw
// baseline, once with --format for parsing — so we capture output the same way.
type capResult struct {
	stdout   string
	stderr   string
	exitCode int
	startErr error
}

func (r capResult) success() bool { return r.exitCode == 0 }

// execCapture runs cmd with stdin detached, captures stdout/stderr, and
// normalizes newlines. Mirrors rtk's exec_capture.
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

func startErr(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(*exec.ExitError); ok {
		return nil
	}
	return err
}

// ── docker ─────────────────────────────────────────────────────────

// RunDocker dispatches `gortk docker <subcommand>`. Mirrors main.rs's
// Commands::Docker arm: ps [-a], images, logs <container>, compose …, and
// passthrough for everything else.
func RunDocker(args []string, verbose int) (int, error) {
	if len(args) == 0 {
		return core.RunPassthrough("docker", args, verbose)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "ps":
		all := hasFlag(rest, "-a", "--all")
		if all {
			return dockerPsAll(verbose)
		}
		return dockerPs(verbose)
	case "images":
		return dockerImages(verbose)
	case "logs":
		return dockerLogs(rest, verbose)
	case "compose":
		return dockerCompose(rest, verbose)
	default:
		return core.RunPassthrough("docker", args, verbose)
	}
}

func hasFlag(args []string, names ...string) bool {
	for _, a := range args {
		for _, n := range names {
			if a == n {
				return true
			}
		}
	}
	return false
}

func dockerPs(verbose int) (int, error) {
	result := execCapture(core.ResolvedCommand("docker",
		"ps", "--format", "{{.ID}}\t{{.Names}}\t{{.Status}}\t{{.Image}}\t{{.Ports}}"))
	if result.startErr != nil {
		return 127, fmt.Errorf("gortk: failed to run docker: %w", result.startErr)
	}
	if !result.success() {
		fmt.Fprint(os.Stderr, result.stderr)
		return result.exitCode, nil
	}
	out := formatDockerPS(result.stdout)
	fmt.Print(out)
	return 0, nil
}

// formatDockerPS compresses `docker ps --format` output (tab-separated:
// ID\tNames\tStatus\tImage\tPorts) into a compact container list.
func formatDockerPS(stdout string) string {
	if strings.TrimSpace(stdout) == "" {
		return "[docker] 0 containers"
	}
	var lines []string
	for _, line := range splitLines(stdout) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if entry, ok := formatContainerLine(line, true); ok {
			lines = append(lines, entry)
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[docker] %d containers:\n", len(lines))
	for _, entry := range take(lines, maxContainers) {
		b.WriteString(entry)
	}
	if len(lines) > maxContainers {
		fmt.Fprintf(&b, "  … +%d more\n", len(lines)-maxContainers)
	}
	return b.String()
}

func dockerPsAll(verbose int) (int, error) {
	result := execCapture(core.ResolvedCommand("docker",
		"ps", "-a", "--format", "{{.State}}\t{{.ID}}\t{{.Names}}\t{{.Status}}\t{{.Image}}\t{{.Ports}}"))
	if result.startErr != nil {
		return 127, fmt.Errorf("gortk: failed to run docker: %w", result.startErr)
	}
	if !result.success() {
		fmt.Fprint(os.Stderr, result.stderr)
		return result.exitCode, nil
	}
	fmt.Print(formatDockerPSAll(result.stdout))
	return 0, nil
}

// formatDockerPSAll compresses `docker ps -a --format` output (tab-separated:
// State\tID\tNames\tStatus\tImage\tPorts) into running and stopped groups.
func formatDockerPSAll(stdout string) string {
	var running, stopped []string
	for _, line := range splitLines(stdout) {
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		state := ""
		if len(parts) > 0 {
			state = parts[0]
		}
		isRunning := state == "running" || state == "restarting"
		if entry, ok := formatContainerLineFromParts(parts[1:], isRunning); ok {
			if isRunning {
				running = append(running, entry)
			} else {
				stopped = append(stopped, entry)
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[docker] %d running:\n", len(running))
	for _, l := range take(running, maxContainersAll) {
		b.WriteString(l)
	}
	if len(running) > maxContainersAll {
		fmt.Fprintf(&b, "  … +%d more\n", len(running)-maxContainersAll)
	}
	if len(stopped) > 0 {
		fmt.Fprintf(&b, "[docker] %d stopped/exited:\n", len(stopped))
		for _, l := range take(stopped, maxContainersAll) {
			b.WriteString(l)
		}
		if len(stopped) > maxContainersAll {
			fmt.Fprintf(&b, "  … +%d more\n", len(stopped)-maxContainersAll)
		}
	}
	return b.String()
}

// formatContainerLine splits a tab-separated container line and formats it.
func formatContainerLine(line string, withPorts bool) (string, bool) {
	return formatContainerLineFromParts(strings.Split(line, "\t"), withPorts)
}

// formatContainerLineFromParts formats container fields [ID, Names, Status,
// Image, (Ports)] into a single compact line, shortening the image to its last
// path segment and the ID to 12 chars.
func formatContainerLineFromParts(parts []string, withPorts bool) (string, bool) {
	if len(parts) < 4 {
		return "", false
	}
	id := parts[0]
	if len(id) > 12 {
		id = id[:12]
	}
	name := parts[1]
	status := strings.TrimSpace(parts[2])
	shortImage := lastPathSegment(parts[3])

	portSuffix := ""
	if withPorts {
		portsField := ""
		if len(parts) > 4 {
			portsField = parts[4]
		}
		ports := compactPorts(portsField)
		if ports != "-" {
			portSuffix = " [" + ports + "]"
		}
	}
	return fmt.Sprintf("  %s %s (%s) %s%s\n", id, name, shortImage, status, portSuffix), true
}

func dockerImages(verbose int) (int, error) {
	result := execCapture(core.ResolvedCommand("docker",
		"images", "--format", "{{.Repository}}:{{.Tag}}\t{{.Size}}"))
	if result.startErr != nil {
		return 127, fmt.Errorf("gortk: failed to run docker: %w", result.startErr)
	}
	if !result.success() {
		fmt.Fprint(os.Stderr, result.stderr)
		return result.exitCode, nil
	}
	fmt.Print(formatDockerImages(result.stdout))
	return 0, nil
}

// formatDockerImages compresses `docker images --format` output (tab-separated:
// Repository:Tag\tSize) into a count + total size header plus a per-image list.
func formatDockerImages(stdout string) string {
	lines := splitLines(stdout)
	if len(lines) == 0 {
		return "[docker] 0 images"
	}

	var totalSizeMB float64
	for _, line := range lines {
		parts := strings.Split(line, "\t")
		if len(parts) < 2 {
			continue
		}
		sizeStr := parts[1]
		switch {
		case strings.Contains(sizeStr, "GB"):
			if n, err := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(sizeStr, "GB", "")), 64); err == nil {
				totalSizeMB += n * 1024.0
			}
		case strings.Contains(sizeStr, "MB"):
			if n, err := strconv.ParseFloat(strings.TrimSpace(strings.ReplaceAll(sizeStr, "MB", "")), 64); err == nil {
				totalSizeMB += n
			}
		}
	}

	var totalDisplay string
	if totalSizeMB > 1024.0 {
		totalDisplay = fmt.Sprintf("%.1fGB", totalSizeMB/1024.0)
	} else {
		totalDisplay = fmt.Sprintf("%.0fMB", totalSizeMB)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[docker] %d images (%s)\n", len(lines), totalDisplay)

	var imageLines []string
	for _, line := range lines {
		parts := strings.Split(line, "\t")
		image := ""
		if len(parts) > 0 {
			image = parts[0]
		}
		size := ""
		if len(parts) > 1 {
			size = parts[1]
		}
		imageLines = append(imageLines, fmt.Sprintf("  %s [%s]\n", image, size))
	}

	for _, l := range take(imageLines, maxImages) {
		b.WriteString(l)
	}
	if len(imageLines) > maxImages {
		fmt.Fprintf(&b, "  … +%d more\n", len(imageLines)-maxImages)
	}
	return b.String()
}

func dockerLogs(args []string, verbose int) (int, error) {
	container := ""
	if len(args) > 0 {
		container = args[0]
	}
	if container == "" {
		fmt.Println("Usage: gortk docker logs <container>")
		return 0, nil
	}
	cmd := core.ResolvedCommand("docker", "logs", "--tail", "100", container)
	opts := core.RunOptions{SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, "docker", "logs "+container, func(raw string) string {
		return fmt.Sprintf("[docker] Logs for %s:\n%s", container, raw)
	}, opts)
}

// ── kubectl / oc ───────────────────────────────────────────────────

// RunKubectl dispatches `gortk kubectl <subcommand>`.
func RunKubectl(args []string, verbose int) (int, error) {
	return runK8s("kubectl", args, verbose)
}

// RunOc dispatches `gortk oc <subcommand>`.
func RunOc(args []string, verbose int) (int, error) {
	return runK8s("oc", args, verbose)
}

// runK8s parses the kubectl/oc subcommand and routes pods/services/logs/get to
// the compact handlers, with passthrough for everything else. Mirrors the
// Commands::Kubectl / Commands::Oc arms in main.rs.
func runK8s(tool string, args []string, verbose int) (int, error) {
	if len(args) == 0 {
		return core.RunPassthrough(tool, args, verbose)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "get":
		return k8sGet(tool, rest, verbose)
	case "pods":
		return k8sPods(tool, rest, verbose)
	case "services":
		return k8sServices(tool, rest, verbose)
	case "logs":
		return k8sLogs(tool, rest, verbose)
	default:
		return core.RunPassthrough(tool, args, verbose)
	}
}

// k8sGet routes `get pods` / `get services` to the compact handlers (when no
// raw-output flag forces passthrough), otherwise passes through.
func k8sGet(tool string, args []string, verbose int) (int, error) {
	if target, rest, ok := k8sGetTarget(args); ok {
		switch target {
		case "pods":
			return k8sPods(tool, rest, verbose)
		case "services":
			return k8sServices(tool, rest, verbose)
		}
	}
	passthrough := append([]string{"get"}, args...)
	return core.RunPassthrough(tool, passthrough, verbose)
}

// k8sGetTarget resolves the resource argument to a compact target ("pods" or
// "services") and the remaining args, or ok=false to fall through to
// passthrough. Output-shaping flags (-o, --watch, …) force passthrough so the
// user's requested raw format is preserved.
func k8sGetTarget(args []string) (string, []string, bool) {
	if len(args) == 0 {
		return "", nil, false
	}
	resource := args[0]
	rest := args[1:]
	if k8sGetRequestsRawOutput(rest) {
		return "", nil, false
	}
	switch resource {
	case "po", "pod", "pods":
		return "pods", rest, true
	case "svc", "service", "services":
		return "services", rest, true
	default:
		return "", nil, false
	}
}

// k8sGetRequestsRawOutput reports whether the args ask for an output format or
// streaming view that the compact JSON path can't honour.
func k8sGetRequestsRawOutput(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "-o", "--output", "-w", "--watch", "--show-labels", "--show-kind":
			return true
		}
		if strings.HasPrefix(arg, "-o") || strings.HasPrefix(arg, "--output=") {
			return true
		}
	}
	return false
}

func k8sPods(tool string, args []string, verbose int) (int, error) {
	cmd := core.ResolvedCommand(tool, "get", "pods", "-o", "json")
	cmd.Args = append(cmd.Args, args...)
	return runK8sJSON(cmd, tool, "get pods", formatKubectlPods)
}

func k8sServices(tool string, args []string, verbose int) (int, error) {
	cmd := core.ResolvedCommand(tool, "get", "services", "-o", "json")
	cmd.Args = append(cmd.Args, args...)
	return runK8sJSON(cmd, tool, "get services", formatKubectlServices)
}

func k8sLogs(tool string, args []string, verbose int) (int, error) {
	pod := ""
	if len(args) > 0 {
		pod = args[0]
	}
	if pod == "" {
		fmt.Printf("Usage: gortk %s logs <pod>\n", tool)
		return 0, nil
	}
	cmd := core.ResolvedCommand(tool, "logs", "--tail", "100", pod)
	if len(args) > 1 {
		cmd.Args = append(cmd.Args, args[1:]...)
	}
	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, tool, "logs "+pod, func(stdout string) string {
		return fmt.Sprintf("Logs for %s:\n%s", pod, stdout)
	}, opts)
}

// runK8sJSON runs a kubectl/oc command whose stdout is JSON, parses it, and
// applies filterFn. On a JSON parse error it falls back to the raw stdout.
func runK8sJSON(cmd *exec.Cmd, tool, label string, filterFn func(jsonText string) (string, bool)) (int, error) {
	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true, NoTrailingNewline: true}
	return core.RunFiltered(cmd, tool, label, func(stdout string) string {
		out, ok := filterFn(stdout)
		if !ok {
			fmt.Fprintf(os.Stderr, "[gortk] %s: JSON parse failed\n", tool)
			return stdout
		}
		return out
	}, opts)
}

// ── small string helpers ───────────────────────────────────────────

// splitLines splits text into lines, dropping a trailing empty element to match
// Rust's str::lines() semantics.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// take returns the first n elements of s (or all of s if shorter).
func take(s []string, n int) []string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// lastPathSegment returns the substring after the final '/', or s unchanged if
// there is none. Mirrors Rust's split('/').next_back().
func lastPathSegment(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// compactPorts reduces a docker ports field to just the published port numbers,
// truncating to "first, second, … +N" when there are more than three.
func compactPorts(ports string) string {
	if ports == "" {
		return "-"
	}
	var portNums []string
	for _, p := range strings.Split(ports, ",") {
		// take the left side of "->", then the segment after the last ':'
		left := strings.SplitN(p, "->", 2)[0]
		if i := strings.LastIndex(left, ":"); i >= 0 {
			portNums = append(portNums, left[i+1:])
		} else {
			portNums = append(portNums, left)
		}
	}

	if len(portNums) <= 3 {
		return strings.Join(portNums, ", ")
	}
	return fmt.Sprintf("%s, … +%d", strings.Join(portNums[:2], ", "), len(portNums)-2)
}
