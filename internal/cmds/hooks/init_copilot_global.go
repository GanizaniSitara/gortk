// init_copilot_global.go implements `gortk init --copilot --global`: a USER-LEVEL
// (global) installer that wires gortk into the GitHub Copilot CLI once per user,
// covering ALL repositories. It is the user-level counterpart to the
// project-scoped installer in init_copilot.go (which writes ./.github per repo)
// and is shaped like the global Claude installer in init_install.go (which
// patches a settings.json round-trip).
//
// What it writes / patches:
//
//   - <copilotHome>/settings.json — adds the gortk user-level hook under the
//     top-level "hooks" object, event "preToolUse" (the Copilot CLI schema,
//     camelCase). The entry invokes `gortk hook copilot` for both bash and
//     powershell hosts. Patching is idempotent: an existing entry that already
//     invokes `gortk hook copilot` (by either bash or powershell) is detected and
//     not duplicated. All other settings keys are preserved on round-trip.
//
// The Copilot home is resolved as: env COPILOT_HOME if set, else
// os.UserHomeDir()/.copilot. A base/home seam (runCopilotGlobalInitAt) lets tests
// drive a t.TempDir() without touching the real user profile, mirroring how
// init_install.go / init_copilot.go take an explicit base path.
//
// Flags (parsed by RunInit and forwarded here): --dry-run and --show compute and
// print the plan without writing anything.
package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Copilot CLI user-level config layout under the Copilot home directory.
const (
	copilotHomeDir          = ".copilot"
	copilotSettingsFile     = "settings.json"
	copilotHomeEnv          = "COPILOT_HOME"
	copilotPreToolUseKey    = "preToolUse" // Copilot CLI schema event name (camelCase)
	copilotGlobalCommand    = "gortk hook copilot"
	copilotGlobalInstallCmd = "gortk init --copilot --global"
)

// copilotGlobalHookEntry is the single preToolUse entry appended to the user's
// Copilot settings.json. zod requires type:"command" plus at least one of
// bash/powershell; cwd/timeoutSec are optional but mirror the project-scoped
// config so both hosts behave identically.
func copilotGlobalHookEntry() map[string]any {
	return map[string]any{
		"type":       "command",
		"bash":       copilotGlobalCommand,
		"powershell": copilotGlobalCommand,
		"cwd":        ".",
		"timeoutSec": 5,
	}
}

// copilotGlobalPlan captures the resolved user-level target and current on-disk
// state so --show and --dry-run can report without mutating anything.
type copilotGlobalPlan struct {
	copilotHome  string // resolved Copilot home (env COPILOT_HOME or ~/.copilot)
	settingsPath string // <copilotHome>/settings.json

	settingsExists bool
	hookPresent    bool   // a gortk preToolUse entry already exists
	settingsErr    string // non-empty if settings.json exists but failed to parse
}

// resolveCopilotHome returns the Copilot home directory: env COPILOT_HOME if set
// (and non-empty), else <home>/.copilot. The home argument is the user home seam
// (os.UserHomeDir() in production, t.TempDir() in tests).
func resolveCopilotHome(home string) string {
	if env := os.Getenv(copilotHomeEnv); env != "" {
		return env
	}
	return filepath.Join(home, copilotHomeDir)
}

// runCopilotGlobalInit is the entry point for
// `gortk init --copilot --global [--show] [--dry-run]`. It resolves the user home
// and delegates to runCopilotGlobalInitAt.
func runCopilotGlobalInit(show, dryRun bool, verbose int) (int, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return 1, fmt.Errorf("cannot resolve home directory: %w", err)
	}
	return runCopilotGlobalInitAt(home, show, dryRun, verbose)
}

// runCopilotGlobalInitAt is runCopilotGlobalInit relative to an explicit user
// home. Tests pass a t.TempDir() so they never mutate the real profile. The
// COPILOT_HOME env var (if set) still takes precedence over this home, matching
// production resolution.
func runCopilotGlobalInitAt(home string, show, dryRun bool, verbose int) (int, error) {
	plan, err := buildCopilotGlobalPlan(home)
	if err != nil {
		return 1, err
	}

	if show {
		printCopilotGlobalState(plan)
		return 0, nil
	}
	if dryRun {
		printCopilotGlobalDryRun(plan)
		return 0, nil
	}

	return applyCopilotGlobalInstall(plan, verbose)
}

// buildCopilotGlobalPlan resolves the settings path and inspects current on-disk
// state without modifying anything.
func buildCopilotGlobalPlan(home string) (*copilotGlobalPlan, error) {
	p := &copilotGlobalPlan{
		copilotHome: resolveCopilotHome(home),
	}
	p.settingsPath = filepath.Join(p.copilotHome, copilotSettingsFile)

	if data, err := os.ReadFile(p.settingsPath); err == nil {
		p.settingsExists = true
		root, perr := parseSettings(data)
		if perr != nil {
			p.settingsErr = perr.Error()
		} else {
			p.hookPresent = copilotGlobalHookPresent(root)
		}
	}
	return p, nil
}

// ── settings.json model & patching ────────────────────────────────────
//
// We reuse parseSettings (init_install.go) to model settings.json as a generic
// map so unknown top-level keys survive a round-trip. Only hooks.preToolUse is
// touched. The Copilot CLI schema differs from Claude's: hooks.preToolUse is a
// flat array of command entries (each carrying bash/powershell directly), not an
// array of matcher objects wrapping a nested "hooks" array.

// copilotPreToolUseEntries returns the hooks.preToolUse array (or nil).
func copilotPreToolUseEntries(root map[string]any) []any {
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	arr, ok := hooks[copilotPreToolUseKey].([]any)
	if !ok {
		return nil
	}
	return arr
}

// copilotGlobalHookPresent reports whether any preToolUse entry already invokes
// gortk — detected by an entry whose bash OR powershell string contains the
// canonical `gortk hook copilot` marker, so a manual edit or cwd tweak still
// counts as present.
func copilotGlobalHookPresent(root map[string]any) bool {
	for _, entry := range copilotPreToolUseEntries(root) {
		obj, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		for _, field := range []string{"bash", "powershell"} {
			if s, ok := obj[field].(string); ok && strings.Contains(s, copilotGlobalCommand) {
				return true
			}
		}
	}
	return false
}

// patchCopilotGlobalSettings inserts the gortk preToolUse entry into a parsed
// settings map, creating the hooks/preToolUse structure as needed. It is
// idempotent: if a gortk entry is already present the map is returned unchanged
// with patched=false.
func patchCopilotGlobalSettings(root map[string]any) (out map[string]any, patched bool) {
	if root == nil {
		root = map[string]any{}
	}
	if copilotGlobalHookPresent(root) {
		return root, false
	}

	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	arr, _ := hooks[copilotPreToolUseKey].([]any)
	arr = append(arr, copilotGlobalHookEntry())
	hooks[copilotPreToolUseKey] = arr
	return root, true
}

// ── apply ─────────────────────────────────────────────────────────────

func applyCopilotGlobalInstall(p *copilotGlobalPlan, verbose int) (int, error) {
	if p.settingsErr != "" {
		fmt.Fprintf(os.Stderr, "gortk: refusing to patch %s: %s\n", p.settingsPath, p.settingsErr)
		fmt.Fprintf(os.Stderr, "gortk: fix the JSON (or remove the file), then re-run: %s\n", copilotGlobalInstallCmd)
		return 1, fmt.Errorf("refusing to patch %s: %s", p.settingsPath, p.settingsErr)
	}

	// Read (or start) settings.json, patch idempotently, write back.
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

	patched, didPatch := patchCopilotGlobalSettings(root)
	if !didPatch {
		fmt.Printf("gortk: user-level Copilot hook already present in %s\n", p.settingsPath)
		fmt.Println("gortk: restart the Copilot CLI/IDE session to apply. It now covers all repos.")
		return 0, nil
	}

	out, err := marshalSettings(patched)
	if err != nil {
		return 1, fmt.Errorf("serialize settings.json: %w", err)
	}

	if err := os.MkdirAll(p.copilotHome, 0o755); err != nil {
		return 1, fmt.Errorf("create %s: %w", p.copilotHome, err)
	}
	if p.settingsExists {
		// Back up before mutating an existing file.
		if data, rerr := os.ReadFile(p.settingsPath); rerr == nil {
			if werr := os.WriteFile(p.settingsPath+".bak", data, 0o644); werr != nil {
				return 1, fmt.Errorf("back up %s: %w", p.settingsPath, werr)
			}
		}
	}
	if err := os.WriteFile(p.settingsPath, out, 0o644); err != nil {
		return 1, fmt.Errorf("write %s: %w", p.settingsPath, err)
	}

	if p.settingsExists {
		fmt.Printf("gortk: patched %s (added user-level preToolUse hook; backed up to %s.bak)\n", p.settingsPath, p.settingsPath)
	} else {
		fmt.Printf("gortk: created %s with a user-level preToolUse hook\n", p.settingsPath)
	}
	fmt.Println("gortk: GitHub Copilot integration installed (user-level — all repos).")
	fmt.Println("gortk: restart the Copilot CLI/IDE session to activate.")
	return 0, nil
}

// ── reporting (--show / --dry-run) ────────────────────────────────────
// (parseSettings / marshalSettings / existsLabel live in init_install.go.)

func printCopilotGlobalState(p *copilotGlobalPlan) {
	fmt.Println("gortk init --copilot --global — current state (GitHub Copilot CLI, user-level):")
	fmt.Printf("  copilot home:  %s\n", p.copilotHome)
	fmt.Printf("  settings:      %s  (%s)\n", p.settingsPath, existsLabel(p.settingsExists, "exists", "absent"))
	if p.settingsErr != "" {
		fmt.Printf("  settings parse error: %s\n", p.settingsErr)
	}
	fmt.Printf("  hook entry:    %s\n", existsLabel(p.hookPresent, "installed", "not installed"))
	fmt.Printf("  command:       %s\n", copilotGlobalCommand)
}

func printCopilotGlobalDryRun(p *copilotGlobalPlan) {
	fmt.Println("gortk init --copilot --global --dry-run (GitHub Copilot CLI, user-level) — nothing will be written:")
	if p.settingsErr != "" {
		fmt.Printf("  [blocked] settings.json present but unparseable: %s\n", p.settingsErr)
		fmt.Println("  [dry-run] would refuse to patch until settings.json is valid JSON")
		return
	}
	if p.hookPresent {
		fmt.Printf("  [dry-run] user-level Copilot hook already present in %s (no change)\n", p.settingsPath)
	} else if p.settingsExists {
		fmt.Printf("  [dry-run] would patch %s: add preToolUse hook (existing keys preserved)\n", p.settingsPath)
		fmt.Printf("  [dry-run] would back up to %s.bak\n", p.settingsPath)
	} else {
		fmt.Printf("  [dry-run] would create %s with a preToolUse hook\n", p.settingsPath)
	}
	fmt.Printf("  command:    %s\n", copilotGlobalCommand)
}
