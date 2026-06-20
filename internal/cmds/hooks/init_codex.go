// init_codex.go implements `gortk init --codex`: a USER-LEVEL installer that
// wires gortk into the OpenAI Codex CLI once per user, covering ALL repositories.
// It writes ~/.codex/hooks.json, the highest-precedence Codex hook-config
// location. It is shaped like the global Claude installer (init_install.go,
// which patches a settings.json round-trip) and the user-level Copilot installer
// (init_copilot_global.go).
//
// What it writes / patches:
//
//   - <codexHome>/hooks.json — adds one gortk PreToolUse entry under the
//     top-level "hooks" object, event "PreToolUse" (matcher "Bash"), whose nested
//     "hooks" array invokes `gortk hook codex`. Patching is idempotent: an
//     existing entry that already references `gortk hook codex` is detected and
//     not duplicated. All other settings keys and hook events are preserved on
//     round-trip. The file is refused (not overwritten) if it exists but is
//     invalid JSON; an existing file is backed up to hooks.json.bak first.
//
// The Codex home is resolved as: env CODEX_HOME if set, else
// os.UserHomeDir()/.codex. A base/home seam (runCodexInitAt) lets tests drive a
// t.TempDir() without touching the real user profile, mirroring how the other
// installers take an explicit base path.
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

// Codex CLI user-level config layout under the Codex home directory.
const (
	codexHomeDir       = ".codex"
	codexHooksFile     = "hooks.json"
	codexHomeEnv       = "CODEX_HOME"
	codexCanonicalCmd  = "gortk hook codex" // appears in any gortk Codex hook command string
	codexStatusMessage = "gortk rewrite"
	codexInstallCmd    = "gortk init --codex"
)

// codexHookEntry is the single PreToolUse entry appended to the user's
// hooks.json. The Codex schema mirrors Claude's: a matcher object wrapping a
// nested "hooks" array of command entries.
func codexHookEntry() map[string]any {
	return map[string]any{
		"matcher": bashMatcher,
		"hooks": []any{
			map[string]any{
				"type":          "command",
				"command":       codexCanonicalCmd,
				"statusMessage": codexStatusMessage,
			},
		},
	}
}

// codexInstallPlan captures the resolved user-level target and current on-disk
// state so --show and --dry-run can report without mutating anything.
type codexInstallPlan struct {
	codexHome string // resolved Codex home (env CODEX_HOME or ~/.codex)
	hooksPath string // <codexHome>/hooks.json

	hooksExists bool
	hookPresent bool   // a gortk PreToolUse entry already exists
	hooksErr    string // non-empty if hooks.json exists but failed to parse
}

// resolveCodexHome returns the Codex home directory: env CODEX_HOME if set (and
// non-empty), else <home>/.codex. The home argument is the user home seam
// (os.UserHomeDir() in production, t.TempDir() in tests).
func resolveCodexHome(home string) string {
	if env := os.Getenv(codexHomeEnv); env != "" {
		return env
	}
	return filepath.Join(home, codexHomeDir)
}

// runCodexInit is the entry point for
// `gortk init --codex [--show] [--dry-run]`. It resolves the user home and
// delegates to runCodexInitAt.
func runCodexInit(show, dryRun bool, verbose int) (int, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return 1, fmt.Errorf("cannot resolve home directory: %w", err)
	}
	return runCodexInitAt(home, show, dryRun, verbose)
}

// runCodexInitAt is runCodexInit relative to an explicit user home. Tests pass a
// t.TempDir() so they never mutate the real profile. The CODEX_HOME env var (if
// set) still takes precedence over this home, matching production resolution.
func runCodexInitAt(home string, show, dryRun bool, verbose int) (int, error) {
	plan, err := buildCodexPlan(home)
	if err != nil {
		return 1, err
	}

	if show {
		printCodexState(plan)
		return 0, nil
	}
	if dryRun {
		printCodexDryRun(plan)
		return 0, nil
	}

	return applyCodexInstall(plan, verbose)
}

// buildCodexPlan resolves the hooks.json path and inspects current on-disk state
// without modifying anything.
func buildCodexPlan(home string) (*codexInstallPlan, error) {
	p := &codexInstallPlan{
		codexHome: resolveCodexHome(home),
	}
	p.hooksPath = filepath.Join(p.codexHome, codexHooksFile)

	if data, err := os.ReadFile(p.hooksPath); err == nil {
		p.hooksExists = true
		root, perr := parseSettings(data)
		if perr != nil {
			p.hooksErr = perr.Error()
		} else {
			p.hookPresent = codexHookPresent(root)
		}
	}
	return p, nil
}

// ── hooks.json model & patching ───────────────────────────────────────
//
// We reuse parseSettings (init_install.go) to model hooks.json as a generic map
// so unknown top-level keys and other hook events survive a round-trip. Only
// hooks.PreToolUse is touched. The Codex schema matches Claude's: hooks.PreToolUse
// is an array of matcher objects, each wrapping a nested "hooks" array of command
// entries — so we reuse preToolUseEntries / entryHookCommands from init_install.go.

// codexHookPresent reports whether any PreToolUse entry already invokes the
// gortk Codex hook — matched by the canonical `gortk hook codex` marker
// substring, so a manual edit or path tweak still counts as present.
func codexHookPresent(root map[string]any) bool {
	for _, entry := range preToolUseEntries(root) {
		for _, cmd := range entryHookCommands(entry) {
			if strings.Contains(cmd, codexCanonicalCmd) {
				return true
			}
		}
	}
	return false
}

// patchCodexHooks inserts the gortk PreToolUse entry into a parsed hooks map,
// creating the hooks/PreToolUse structure as needed. It is idempotent: if a gortk
// entry is already present the map is returned unchanged with patched=false.
func patchCodexHooks(root map[string]any) (out map[string]any, patched bool) {
	if root == nil {
		root = map[string]any{}
	}
	if codexHookPresent(root) {
		return root, false
	}

	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	arr, _ := hooks[preToolUseKey].([]any)
	arr = append(arr, codexHookEntry())
	hooks[preToolUseKey] = arr
	return root, true
}

// ── apply ─────────────────────────────────────────────────────────────

func applyCodexInstall(p *codexInstallPlan, verbose int) (int, error) {
	if p.hooksErr != "" {
		fmt.Fprintf(os.Stderr, "gortk: refusing to patch %s: %s\n", p.hooksPath, p.hooksErr)
		fmt.Fprintf(os.Stderr, "gortk: fix the JSON (or remove the file), then re-run: %s\n", codexInstallCmd)
		return 1, fmt.Errorf("refusing to patch %s: %s", p.hooksPath, p.hooksErr)
	}

	// Read (or start) hooks.json, patch idempotently, write back.
	root := map[string]any{}
	if p.hooksExists {
		data, err := os.ReadFile(p.hooksPath)
		if err != nil {
			return 1, fmt.Errorf("read %s: %w", p.hooksPath, err)
		}
		root, err = parseSettings(data)
		if err != nil {
			return 1, fmt.Errorf("refusing to patch %s: %w", p.hooksPath, err)
		}
	}

	patched, didPatch := patchCodexHooks(root)
	if !didPatch {
		fmt.Printf("gortk: user-level Codex hook already present in %s\n", p.hooksPath)
		fmt.Println("gortk: restart Codex to apply. It now covers all repos.")
		return 0, nil
	}

	out, err := marshalSettings(patched)
	if err != nil {
		return 1, fmt.Errorf("serialize hooks.json: %w", err)
	}

	if err := os.MkdirAll(p.codexHome, 0o755); err != nil {
		return 1, fmt.Errorf("create %s: %w", p.codexHome, err)
	}
	if p.hooksExists {
		// Back up before mutating an existing file.
		if data, rerr := os.ReadFile(p.hooksPath); rerr == nil {
			if werr := os.WriteFile(p.hooksPath+".bak", data, 0o644); werr != nil {
				return 1, fmt.Errorf("back up %s: %w", p.hooksPath, werr)
			}
		}
	}
	if err := os.WriteFile(p.hooksPath, out, 0o644); err != nil {
		return 1, fmt.Errorf("write %s: %w", p.hooksPath, err)
	}

	if p.hooksExists {
		fmt.Printf("gortk: patched %s (added Bash PreToolUse hook; backed up to %s.bak)\n", p.hooksPath, p.hooksPath)
	} else {
		fmt.Printf("gortk: created %s with a Bash PreToolUse hook\n", p.hooksPath)
	}
	fmt.Println("gortk: OpenAI Codex CLI integration installed (user-level — all repos).")
	fmt.Println("gortk: restart Codex to apply. gortk-rewritten commands are auto-allowed (Codex pairs updatedInput with an 'allow' decision).")
	return 0, nil
}

// ── reporting (--show / --dry-run) ────────────────────────────────────
// (parseSettings / marshalSettings / existsLabel live in init_install.go.)

func printCodexState(p *codexInstallPlan) {
	fmt.Println("gortk init --codex — current state (OpenAI Codex CLI, user-level):")
	fmt.Printf("  codex home:  %s\n", p.codexHome)
	fmt.Printf("  hooks.json:  %s  (%s)\n", p.hooksPath, existsLabel(p.hooksExists, "exists", "absent"))
	if p.hooksErr != "" {
		fmt.Printf("  hooks.json parse error: %s\n", p.hooksErr)
	}
	fmt.Printf("  hook entry:  %s\n", existsLabel(p.hookPresent, "installed", "not installed"))
	fmt.Printf("  command:     %s\n", codexCanonicalCmd)
}

func printCodexDryRun(p *codexInstallPlan) {
	fmt.Println("gortk init --codex --dry-run (OpenAI Codex CLI, user-level) — nothing will be written:")
	if p.hooksErr != "" {
		fmt.Printf("  [blocked] hooks.json present but unparseable: %s\n", p.hooksErr)
		fmt.Println("  [dry-run] would refuse to patch until hooks.json is valid JSON")
		return
	}
	if p.hookPresent {
		fmt.Printf("  [dry-run] user-level Codex hook already present in %s (no change)\n", p.hooksPath)
	} else if p.hooksExists {
		fmt.Printf("  [dry-run] would patch %s: add Bash PreToolUse hook (existing keys preserved)\n", p.hooksPath)
		fmt.Printf("  [dry-run] would back up to %s.bak\n", p.hooksPath)
	} else {
		fmt.Printf("  [dry-run] would create %s with a Bash PreToolUse hook\n", p.hooksPath)
	}
	fmt.Printf("  command:    %s\n", codexCanonicalCmd)
}
