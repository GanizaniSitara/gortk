// init_install.go implements `gortk init`: a native-Windows installer that
// wires gortk into Claude Code as a PreToolUse hook for Bash. It is a focused
// port of the Claude-Code slice of rtk's src/hooks/init.rs.
//
// What it writes / patches:
//
//   - ~/.claude/hooks/gortk-hook.cmd — a tiny launcher that runs
//     `gortk hook claude`, passing stdin/stdout straight through. Claude Code
//     invokes the hook via the shell, and a .cmd is the most robust native entry
//     point on Windows (no bash assumption). The settings.json command points at
//     this file.
//   - ~/.claude/settings.json — adds one PreToolUse entry with matcher "Bash"
//     whose hook command launches the .cmd. Patching is idempotent: an existing
//     gortk hook entry (by command path or the canonical `gortk hook claude`
//     marker) is detected and not duplicated. All other settings keys are
//     preserved byte-for-byte where the JSON round-trips.
//
// Flags:
//
//   - --show     : print the resolved paths and current install state; write
//     nothing.
//   - --dry-run  : compute and print the changes that WOULD be made; write
//     nothing.
//
// Other agents (Cursor, Gemini, Copilot, OpenCode, Codex, ...) are intentionally
// out of scope; this targets Claude Code on Windows only.
package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Claude Code config layout under the user's home directory.
const (
	claudeDir       = ".claude"
	hooksSubdir     = "hooks"
	settingsFile    = "settings.json"
	hookLauncher    = "gortk-hook.cmd"
	preToolUseKey   = "PreToolUse"
	bashMatcher     = "Bash"
	canonicalMarker = "gortk hook claude" // appears in any gortk hook command string
)

// launcherScript is the .cmd written under ~/.claude/hooks. It forwards stdin
// and the hook protocol to `gortk hook claude`. Kept minimal and dependency-free.
const launcherScript = "@echo off\r\n" +
	"rem gortk PreToolUse hook launcher for Claude Code (managed by `gortk init`).\r\n" +
	"gortk hook claude\r\n"

// installPlan captures the resolved targets and the actions an install would
// take, so --show and --dry-run can report without mutating anything.
type installPlan struct {
	homeDir      string
	claudePath   string // ~/.claude
	hooksPath    string // ~/.claude/hooks
	launcherPath string // ~/.claude/hooks/gortk-hook.cmd
	settingsPath string // ~/.claude/settings.json
	hookCommand  string // command string written into settings.json

	settingsExists bool
	hookPresent    bool   // a gortk hook entry already exists in settings.json
	launcherExists bool   // the launcher .cmd already exists with current content
	settingsErr    string // non-empty if settings.json exists but failed to parse
}

// RunInit implements `gortk init [--copilot] [--show] [--dry-run]`. With no
// target flag it installs the global Claude Code hook (the original behaviour,
// unchanged). With --copilot it routes to the project-scoped GitHub Copilot
// installer instead (writes into ./.github of the current directory).
func RunInit(args []string, verbose int) (int, error) {
	show := false
	dryRun := false
	copilot := false
	for _, a := range args {
		switch a {
		case "--copilot":
			copilot = true
		case "--show":
			show = true
		case "--dry-run":
			dryRun = true
		case "-h", "--help":
			printInitUsage()
			return 0, nil
		default:
			fmt.Fprintf(os.Stderr, "gortk init: unknown flag %q\n", a)
			printInitUsage()
			return 2, nil
		}
	}

	if copilot {
		return runCopilotInit(show, dryRun, verbose)
	}

	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return 1, fmt.Errorf("cannot resolve home directory: %w", err)
	}

	plan, err := buildInstallPlan(home)
	if err != nil {
		return 1, err
	}

	if show {
		printInitState(plan)
		return 0, nil
	}

	if dryRun {
		printInitDryRun(plan)
		return 0, nil
	}

	return applyInstall(plan, verbose)
}

// buildInstallPlan resolves all paths and inspects current on-disk state without
// modifying anything.
func buildInstallPlan(home string) (*installPlan, error) {
	p := &installPlan{
		homeDir:      home,
		claudePath:   filepath.Join(home, claudeDir),
		hooksPath:    filepath.Join(home, claudeDir, hooksSubdir),
		launcherPath: filepath.Join(home, claudeDir, hooksSubdir, hookLauncher),
		settingsPath: filepath.Join(home, claudeDir, settingsFile),
	}
	p.hookCommand = hookCommandFor(p.launcherPath)

	if existing, err := os.ReadFile(p.launcherPath); err == nil {
		p.launcherExists = string(existing) == launcherScript
	}

	if data, err := os.ReadFile(p.settingsPath); err == nil {
		p.settingsExists = true
		root, perr := parseSettings(data)
		if perr != nil {
			p.settingsErr = perr.Error()
		} else {
			p.hookPresent = hookAlreadyPresent(root, p.hookCommand)
		}
	}
	return p, nil
}

// hookCommandFor renders the settings.json command string for the launcher.
// We invoke the .cmd via cmd.exe /c so Claude Code's shell launches it reliably
// regardless of how the hook command is interpreted, while keeping the canonical
// `gortk hook claude` marker present for idempotency detection.
func hookCommandFor(launcherPath string) string {
	// Forward slashes are accepted by cmd.exe and avoid JSON backslash-escaping
	// noise; the canonical marker keeps detection robust if the path changes.
	return "cmd /c \"" + filepath.ToSlash(launcherPath) + "\""
}

// ── settings.json model & patching ────────────────────────────────────
//
// We model settings.json as a generic map so unknown top-level keys are
// preserved on round-trip. Only hooks.PreToolUse is touched.

func parseSettings(data []byte) (map[string]any, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return map[string]any{}, nil
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(trimmed), &root); err != nil {
		return nil, fmt.Errorf("settings.json is not a JSON object: %w", err)
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

// hookAlreadyPresent reports whether any PreToolUse entry already invokes the
// gortk hook — matched either by exact command or by the canonical marker
// substring, so a path change or manual edit still counts as present.
func hookAlreadyPresent(root map[string]any, hookCommand string) bool {
	for _, entry := range preToolUseEntries(root) {
		for _, cmd := range entryHookCommands(entry) {
			if cmd == hookCommand || strings.Contains(cmd, canonicalMarker) {
				return true
			}
		}
	}
	return false
}

// preToolUseEntries returns the hooks.PreToolUse array (or nil).
func preToolUseEntries(root map[string]any) []any {
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	arr, ok := hooks[preToolUseKey].([]any)
	if !ok {
		return nil
	}
	return arr
}

// entryHookCommands extracts the command strings from one PreToolUse entry's
// nested "hooks" array.
func entryHookCommands(entry any) []string {
	obj, ok := entry.(map[string]any)
	if !ok {
		return nil
	}
	inner, ok := obj["hooks"].([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, h := range inner {
		ho, ok := h.(map[string]any)
		if !ok {
			continue
		}
		if cmd, ok := ho["command"].(string); ok {
			out = append(out, cmd)
		}
	}
	return out
}

// patchSettings inserts the gortk PreToolUse hook entry into a parsed settings
// map, creating the hooks/PreToolUse structure as needed. It is idempotent: if
// the hook is already present the map is returned unchanged with patched=false.
func patchSettings(root map[string]any, hookCommand string) (out map[string]any, patched bool) {
	if root == nil {
		root = map[string]any{}
	}
	if hookAlreadyPresent(root, hookCommand) {
		return root, false
	}

	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	arr, _ := hooks[preToolUseKey].([]any)
	arr = append(arr, map[string]any{
		"matcher": bashMatcher,
		"hooks": []any{
			map[string]any{
				"type":    "command",
				"command": hookCommand,
			},
		},
	})
	hooks[preToolUseKey] = arr
	return root, true
}

func marshalSettings(root map[string]any) ([]byte, error) {
	return json.MarshalIndent(root, "", "  ")
}

// ── apply ─────────────────────────────────────────────────────────────

func applyInstall(p *installPlan, verbose int) (int, error) {
	if p.settingsErr != "" {
		return 1, fmt.Errorf("refusing to patch %s: %s", p.settingsPath, p.settingsErr)
	}

	// 1. Ensure ~/.claude/hooks exists and write the launcher.
	if err := os.MkdirAll(p.hooksPath, 0o755); err != nil {
		return 1, fmt.Errorf("create %s: %w", p.hooksPath, err)
	}
	if !p.launcherExists {
		if err := os.WriteFile(p.launcherPath, []byte(launcherScript), 0o755); err != nil {
			return 1, fmt.Errorf("write launcher %s: %w", p.launcherPath, err)
		}
		fmt.Printf("gortk: wrote hook launcher %s\n", p.launcherPath)
	} else if verbose > 0 {
		fmt.Printf("gortk: launcher already up to date %s\n", p.launcherPath)
	}

	// 2. Read (or start) settings.json, patch idempotently, write back.
	root := map[string]any{}
	if p.settingsExists {
		data, err := os.ReadFile(p.settingsPath)
		if err != nil {
			return 1, fmt.Errorf("read %s: %w", p.settingsPath, err)
		}
		root, err = parseSettings(data)
		if err != nil {
			return 1, fmt.Errorf("refusing to patch %s: %w", p.settingsPath, err)
		}
	}

	patched, didPatch := patchSettings(root, p.hookCommand)
	if !didPatch {
		fmt.Printf("gortk: PreToolUse hook already present in %s\n", p.settingsPath)
		fmt.Println("gortk: restart Claude Code to apply. Test with: git status")
		return 0, nil
	}

	out, err := marshalSettings(patched)
	if err != nil {
		return 1, fmt.Errorf("serialize settings.json: %w", err)
	}
	if p.settingsExists {
		// Best-effort backup before mutating an existing file.
		if data, rerr := os.ReadFile(p.settingsPath); rerr == nil {
			_ = os.WriteFile(p.settingsPath+".bak", data, 0o644)
		}
	}
	if err := os.MkdirAll(p.claudePath, 0o755); err != nil {
		return 1, fmt.Errorf("create %s: %w", p.claudePath, err)
	}
	if err := os.WriteFile(p.settingsPath, out, 0o644); err != nil {
		return 1, fmt.Errorf("write %s: %w", p.settingsPath, err)
	}

	fmt.Printf("gortk: patched %s (added Bash PreToolUse hook)\n", p.settingsPath)
	fmt.Println("gortk: restart Claude Code to apply. Test with: git status")
	return 0, nil
}

// ── reporting (--show / --dry-run) ────────────────────────────────────

func printInitState(p *installPlan) {
	fmt.Println("gortk init — current state (Claude Code, Windows):")
	fmt.Printf("  home:        %s\n", p.homeDir)
	fmt.Printf("  launcher:    %s  (%s)\n", p.launcherPath, existsLabel(p.launcherExists, "present (current)", "missing/outdated"))
	fmt.Printf("  settings:    %s  (%s)\n", p.settingsPath, existsLabel(p.settingsExists, "exists", "absent"))
	if p.settingsErr != "" {
		fmt.Printf("  settings parse error: %s\n", p.settingsErr)
	}
	fmt.Printf("  hook entry:  %s\n", existsLabel(p.hookPresent, "installed", "not installed"))
	fmt.Printf("  command:     %s\n", p.hookCommand)
}

func printInitDryRun(p *installPlan) {
	fmt.Println("gortk init --dry-run (Claude Code, Windows) — nothing will be written:")
	if p.settingsErr != "" {
		fmt.Printf("  [blocked] settings.json present but unparseable: %s\n", p.settingsErr)
		fmt.Println("  [dry-run] would refuse to patch until settings.json is valid JSON")
		return
	}
	if !p.launcherExists {
		fmt.Printf("  [dry-run] would write launcher: %s\n", p.launcherPath)
	} else {
		fmt.Printf("  [dry-run] launcher already current: %s\n", p.launcherPath)
	}
	if p.hookPresent {
		fmt.Printf("  [dry-run] PreToolUse hook already present in %s (no change)\n", p.settingsPath)
	} else if p.settingsExists {
		fmt.Printf("  [dry-run] would patch %s: add Bash PreToolUse hook (existing keys preserved)\n", p.settingsPath)
		fmt.Printf("  [dry-run] would back up to %s.bak\n", p.settingsPath)
	} else {
		fmt.Printf("  [dry-run] would create %s with a Bash PreToolUse hook\n", p.settingsPath)
	}
	fmt.Printf("  command:    %s\n", p.hookCommand)
}

func existsLabel(b bool, yes, no string) string {
	if b {
		return yes
	}
	return no
}

func printInitUsage() {
	fmt.Fprintln(os.Stderr, "Usage: gortk init [--copilot] [--show] [--dry-run]")
	fmt.Fprintln(os.Stderr, "  Installs the gortk PreToolUse hook into Claude Code (~/.claude).")
	fmt.Fprintln(os.Stderr, "  --copilot  install the project-scoped GitHub Copilot hook into ./.github instead")
	fmt.Fprintln(os.Stderr, "  --show     print resolved paths and current state; write nothing")
	fmt.Fprintln(os.Stderr, "  --dry-run  print the changes that would be made; write nothing")
}
