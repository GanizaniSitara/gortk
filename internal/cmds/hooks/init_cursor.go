// init_cursor.go implements `gortk init --agent cursor`: a USER-LEVEL installer
// that wires gortk into the Cursor Agent by patching ~/.cursor/hooks.json. It is
// a native-Windows, offline port of the Cursor slice of rtk's src/hooks/init.rs
// (install_cursor_hooks / patch_cursor_hooks_json / insert_cursor_hook_entry).
//
// What it writes / patches:
//
//   - <cursorHome>/hooks.json — ensures a top-level "version": 1 and adds one
//     gortk entry to the hooks.preToolUse array. Cursor's preToolUse schema is a
//     FLAT array of {command, matcher} objects (NOT the Claude matcher-wrapping-a-
//     nested-hooks-array shape), so the entry is {"command":"gortk hook cursor",
//     "matcher":"Shell"}. Patching is idempotent: an existing entry already
//     invoking `gortk hook cursor` is detected and not duplicated. All other keys
//     are preserved on round-trip. An existing file is refused (not overwritten)
//     if it is invalid JSON, and is backed up to hooks.json.bak before mutation.
//
// The Cursor home is resolved as: env CURSOR_HOME if set, else
// os.UserHomeDir()/.cursor (rtk resolves ~/.cursor unconditionally; we add the
// CURSOR_HOME seam to mirror the Codex installer's CODEX_HOME pattern and to let
// tests redirect cleanly). A base/home seam (runCursorInitAt) lets tests drive a
// t.TempDir() without touching the real profile.
package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Cursor user-level config layout under the Cursor home directory.
const (
	cursorHomeDir      = ".cursor"
	cursorHooksFile    = "hooks.json"
	cursorHomeEnv      = "CURSOR_HOME"
	cursorPreToolUse   = "preToolUse" // Cursor schema event name (camelCase, flat array)
	cursorMatcher      = "Shell"
	cursorCanonicalCmd = "gortk hook cursor" // appears in any gortk Cursor hook command string
	cursorInstallCmd   = "gortk init --agent cursor"
)

// cursorHookEntry is the single preToolUse entry appended to the user's
// hooks.json. Cursor's schema is a flat array of {command, matcher} objects.
func cursorHookEntry() map[string]any {
	return map[string]any{
		"command": cursorCanonicalCmd,
		"matcher": cursorMatcher,
	}
}

// cursorInstallPlan captures the resolved user-level target and current on-disk
// state so --show and --dry-run can report without mutating anything.
type cursorInstallPlan struct {
	cursorHome string // resolved Cursor home (env CURSOR_HOME or ~/.cursor)
	hooksPath  string // <cursorHome>/hooks.json

	hooksExists bool
	hookPresent bool   // a gortk preToolUse entry already exists
	hooksErr    string // non-empty if hooks.json exists but failed to parse
}

// resolveCursorHome returns the Cursor home directory: env CURSOR_HOME if set
// (and non-empty), else <home>/.cursor.
func resolveCursorHome(home string) string {
	if env := os.Getenv(cursorHomeEnv); env != "" {
		return env
	}
	return filepath.Join(home, cursorHomeDir)
}

// runCursorInit is the entry point for `gortk init --agent cursor`. It resolves
// the user home and delegates to runCursorInitAt.
func runCursorInit(show, dryRun bool, verbose int) (int, error) {
	home, code, err := homeDirOr()
	if err != nil {
		return code, err
	}
	return runCursorInitAt(home, show, dryRun, verbose)
}

// runCursorInitAt is runCursorInit relative to an explicit user home. Tests pass
// a t.TempDir() so they never mutate the real profile. The CURSOR_HOME env var
// (if set) still takes precedence over this home, matching production resolution.
func runCursorInitAt(home string, show, dryRun bool, verbose int) (int, error) {
	plan, err := buildCursorPlan(home)
	if err != nil {
		return 1, err
	}

	if show {
		printCursorState(plan)
		return 0, nil
	}
	if dryRun {
		printCursorDryRun(plan)
		return 0, nil
	}

	return applyCursorInstall(plan, verbose)
}

// buildCursorPlan resolves the hooks.json path and inspects current on-disk
// state without modifying anything.
func buildCursorPlan(home string) (*cursorInstallPlan, error) {
	p := &cursorInstallPlan{
		cursorHome: resolveCursorHome(home),
	}
	p.hooksPath = filepath.Join(p.cursorHome, cursorHooksFile)

	if data, err := os.ReadFile(p.hooksPath); err == nil {
		p.hooksExists = true
		root, perr := parseSettings(data)
		if perr != nil {
			p.hooksErr = perr.Error()
		} else {
			p.hookPresent = cursorHookPresent(root)
		}
	}
	return p, nil
}

// ── hooks.json model & patching ───────────────────────────────────────
//
// We reuse parseSettings (init_install.go) to model hooks.json as a generic map
// so unknown top-level keys survive a round-trip. Only hooks.preToolUse is
// touched. Cursor's schema differs from Claude's AND from Copilot's: it is a flat
// array of {command, matcher} objects directly under hooks.preToolUse.

// cursorPreToolUseEntries returns the hooks.preToolUse array (or nil).
func cursorPreToolUseEntries(root map[string]any) []any {
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		return nil
	}
	arr, ok := hooks[cursorPreToolUse].([]any)
	if !ok {
		return nil
	}
	return arr
}

// cursorHookPresent reports whether any preToolUse entry already invokes the
// gortk Cursor hook — detected by an entry whose "command" string contains the
// canonical `gortk hook cursor` marker, so a manual edit still counts as present.
func cursorHookPresent(root map[string]any) bool {
	for _, entry := range cursorPreToolUseEntries(root) {
		obj, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if s, ok := obj["command"].(string); ok && strings.Contains(s, cursorCanonicalCmd) {
			return true
		}
	}
	return false
}

// patchCursorHooks inserts the gortk preToolUse entry into a parsed hooks map,
// creating the version/hooks/preToolUse structure as needed. It is idempotent: if
// a gortk entry is already present the map is returned unchanged with
// patched=false. Mirrors rtk insert_cursor_hook_entry, which also ensures the
// top-level "version": 1 default.
func patchCursorHooks(root map[string]any) (out map[string]any, patched bool) {
	if root == nil {
		root = map[string]any{}
	}
	if cursorHookPresent(root) {
		return root, false
	}

	if _, ok := root["version"]; !ok {
		root["version"] = float64(1) // JSON numbers round-trip as float64
	}

	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		hooks = map[string]any{}
		root["hooks"] = hooks
	}
	arr, _ := hooks[cursorPreToolUse].([]any)
	arr = append(arr, cursorHookEntry())
	hooks[cursorPreToolUse] = arr
	return root, true
}

// ── apply ─────────────────────────────────────────────────────────────

func applyCursorInstall(p *cursorInstallPlan, verbose int) (int, error) {
	if p.hooksErr != "" {
		fmt.Fprintf(os.Stderr, "gortk: refusing to patch %s: %s\n", p.hooksPath, p.hooksErr)
		fmt.Fprintf(os.Stderr, "gortk: fix the JSON (or remove the file), then re-run: %s\n", cursorInstallCmd)
		return 1, fmt.Errorf("refusing to patch %s: %s", p.hooksPath, p.hooksErr)
	}

	// Read (or start) hooks.json, patch idempotently, write back. An empty file
	// starts from {"version":1} (matching rtk's empty-file default).
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

	patched, didPatch := patchCursorHooks(root)
	if !didPatch {
		fmt.Printf("gortk: Cursor preToolUse hook already present in %s\n", p.hooksPath)
		fmt.Println("gortk: Cursor reloads hooks.json automatically. Test with: git status")
		return 0, nil
	}

	out, err := marshalSettings(patched)
	if err != nil {
		return 1, fmt.Errorf("serialize hooks.json: %w", err)
	}

	if err := os.MkdirAll(p.cursorHome, 0o755); err != nil {
		return 1, fmt.Errorf("create %s: %w", p.cursorHome, err)
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
		fmt.Printf("gortk: patched %s (added preToolUse hook; backed up to %s.bak)\n", p.hooksPath, p.hooksPath)
	} else {
		fmt.Printf("gortk: created %s with a preToolUse hook\n", p.hooksPath)
	}
	fmt.Println("gortk: Cursor integration installed (user-level — all repos).")
	fmt.Println("gortk: Cursor reloads hooks.json automatically. Test with: git status")
	return 0, nil
}

// ── reporting (--show / --dry-run) ────────────────────────────────────

func printCursorState(p *cursorInstallPlan) {
	fmt.Println("gortk init --agent cursor — current state (Cursor Agent, user-level):")
	fmt.Printf("  cursor home:  %s\n", p.cursorHome)
	fmt.Printf("  hooks.json:   %s  (%s)\n", p.hooksPath, existsLabel(p.hooksExists, "exists", "absent"))
	if p.hooksErr != "" {
		fmt.Printf("  hooks.json parse error: %s\n", p.hooksErr)
	}
	fmt.Printf("  hook entry:   %s\n", existsLabel(p.hookPresent, "installed", "not installed"))
	fmt.Printf("  command:      %s\n", cursorCanonicalCmd)
}

func printCursorDryRun(p *cursorInstallPlan) {
	fmt.Println("gortk init --agent cursor --dry-run (Cursor Agent, user-level) — nothing will be written:")
	if p.hooksErr != "" {
		fmt.Printf("  [blocked] hooks.json present but unparseable: %s\n", p.hooksErr)
		fmt.Println("  [dry-run] would refuse to patch until hooks.json is valid JSON")
		return
	}
	if p.hookPresent {
		fmt.Printf("  [dry-run] Cursor hook already present in %s (no change)\n", p.hooksPath)
	} else if p.hooksExists {
		fmt.Printf("  [dry-run] would patch %s: add preToolUse hook (existing keys preserved)\n", p.hooksPath)
		fmt.Printf("  [dry-run] would back up to %s.bak\n", p.hooksPath)
	} else {
		fmt.Printf("  [dry-run] would create %s with a preToolUse hook\n", p.hooksPath)
	}
	fmt.Printf("  command:    %s\n", cursorCanonicalCmd)
}
