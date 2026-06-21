// init_hermes.go implements `gortk init --agent hermes`: a USER-LEVEL installer
// that wires gortk into the Hermes CLI by dropping a Python plugin and enabling
// it in config.yaml. It is a native-Windows, offline port of the Hermes slice of
// rtk's src/hooks/init.rs (run_hermes_mode_at / patch_hermes_config) and the
// hooks/hermes/rtk-rewrite/ templates.
//
// What it writes / patches (under the Hermes home, resolved as env HERMES_HOME if
// set, else ~/.hermes):
//
//   - <home>/plugins/gortk-rewrite/__init__.py  — the plugin adapter (port of
//     hooks/hermes/rtk-rewrite/__init__.py), bridging Hermes pre_tool_call to
//     `gortk rewrite`. Written write-if-changed.
//   - <home>/plugins/gortk-rewrite/plugin.yaml  — the plugin manifest. Written
//     write-if-changed.
//   - <home>/config.yaml — ensures the gortk-rewrite plugin is enabled.
//
// config.yaml handling (vs rtk): rtk ships a full line-based YAML splicer that
// can edit an existing inline (`enabled: [a, b]`) or block (`enabled:\n  - a`)
// plugins list in place. gortk ports the realistic install cases without pulling
// in a YAML dependency (pure-stdlib constraint):
//
//   - empty/absent config            → write the canonical plugins block
//   - config already enabling gortk  → no-op (idempotent)
//   - config WITHOUT a plugins block → append the canonical plugins block
//
// A config that already has a hand-authored `plugins:` block but does NOT mention
// gortk is the one shape gortk does not splice into; it appends a second canonical
// block, which Hermes tolerates but is not as tidy as rtk's in-place edit. This is
// the only Hermes behaviour that diverges from rtk and is noted as a known gap.
package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Hermes user-level config layout under the Hermes home directory.
const (
	hermesHomeDir      = ".hermes"
	hermesHomeEnv      = "HERMES_HOME"
	hermesPluginsDir   = "plugins"
	hermesPluginName   = "gortk-rewrite"
	hermesInitFile     = "__init__.py"
	hermesManifestFile = "plugin.yaml"
	hermesConfigFile   = "config.yaml"
)

// hermesPluginsBlock is the canonical plugins-enabled block appended to a fresh
// or plugins-less config.yaml (port of rtk hermes_plugins_block()).
const hermesPluginsBlock = "plugins:\n  enabled:\n    - " + hermesPluginName + "\n"

// hermesInstallPlan captures the resolved targets and current on-disk state so
// --show and --dry-run can report without mutating anything.
type hermesInstallPlan struct {
	hermesHome string // resolved Hermes home (env HERMES_HOME or ~/.hermes)
	pluginDir  string // <home>/plugins/gortk-rewrite
	initPath   string // <pluginDir>/__init__.py
	manifest   string // <pluginDir>/plugin.yaml
	configPath string // <home>/config.yaml

	initExists     bool // __init__.py present with current content
	manifestExists bool // plugin.yaml present with current content
	configExists   bool
	configEnabled  bool   // config.yaml already enables gortk-rewrite
	newConfig      string // config content that would be written ("" = no change)
}

// resolveHermesHome returns the Hermes home: env HERMES_HOME if set, else
// <home>/.hermes (rtk resolve_hermes_home_from_env).
func resolveHermesHome(home string) string {
	if env := os.Getenv(hermesHomeEnv); env != "" {
		return env
	}
	return filepath.Join(home, hermesHomeDir)
}

func runHermesInit(show, dryRun bool, verbose int) (int, error) {
	home, code, err := homeDirOr()
	if err != nil {
		return code, err
	}
	return runHermesInitAt(home, show, dryRun, verbose)
}

// runHermesInitAt is runHermesInit relative to an explicit user home. Tests pass
// a t.TempDir(). HERMES_HOME (if set) still takes precedence, matching production.
func runHermesInitAt(home string, show, dryRun bool, verbose int) (int, error) {
	plan, err := buildHermesPlan(home)
	if err != nil {
		return 1, err
	}

	if show {
		printHermesState(plan)
		return 0, nil
	}
	if dryRun {
		printHermesDryRun(plan)
		return 0, nil
	}

	return applyHermesInstall(plan, verbose)
}

// buildHermesPlan resolves all paths and inspects current on-disk state. The
// config patch is pre-computed so --dry-run/--show can report the exact action.
func buildHermesPlan(home string) (*hermesInstallPlan, error) {
	hermesHome := resolveHermesHome(home)
	pluginDir := filepath.Join(hermesHome, hermesPluginsDir, hermesPluginName)
	p := &hermesInstallPlan{
		hermesHome: hermesHome,
		pluginDir:  pluginDir,
		initPath:   filepath.Join(pluginDir, hermesInitFile),
		manifest:   filepath.Join(pluginDir, hermesManifestFile),
		configPath: filepath.Join(hermesHome, hermesConfigFile),
	}

	p.initExists = fileHasContent(p.initPath, hermesPluginInitPy)
	p.manifestExists = fileHasContent(p.manifest, hermesPluginYAML)

	existing := ""
	if data, err := os.ReadFile(p.configPath); err == nil {
		p.configExists = true
		existing = string(data)
	}
	patched, enabled := patchHermesConfig(existing)
	p.configEnabled = enabled
	if !enabled && patched != existing {
		p.newConfig = patched
	}
	return p, nil
}

// patchHermesConfig returns the config content with the gortk-rewrite plugin
// enabled, plus whether it was ALREADY enabled (in which case the content is
// returned unchanged). See the file header for the supported cases and the one
// known divergence from rtk's full in-place splicer.
func patchHermesConfig(existing string) (out string, alreadyEnabled bool) {
	// Already enabling our plugin → idempotent no-op.
	if hermesConfigEnablesGortk(existing) {
		return existing, true
	}
	if strings.TrimSpace(existing) == "" {
		return hermesPluginsBlock, false
	}
	// Non-empty config that does not enable gortk → append the canonical block.
	patched := existing
	if !strings.HasSuffix(patched, "\n") {
		patched += "\n"
	}
	patched += hermesPluginsBlock
	return patched, false
}

// hermesConfigEnablesGortk reports whether the config text already references the
// gortk-rewrite plugin name (block list item or inline list entry). Substring
// match is sufficient: the plugin name is distinctive enough not to false-match.
func hermesConfigEnablesGortk(config string) bool {
	return strings.Contains(config, hermesPluginName)
}

// ── apply ─────────────────────────────────────────────────────────────

func applyHermesInstall(p *hermesInstallPlan, verbose int) (int, error) {
	if err := os.MkdirAll(p.pluginDir, 0o755); err != nil {
		return 1, fmt.Errorf("create %s: %w", p.pluginDir, err)
	}

	if !p.initExists {
		if err := os.WriteFile(p.initPath, []byte(hermesPluginInitPy), 0o644); err != nil {
			return 1, fmt.Errorf("write %s: %w", p.initPath, err)
		}
		fmt.Printf("gortk: wrote Hermes plugin %s\n", p.initPath)
	} else if verbose > 0 {
		fmt.Printf("gortk: Hermes plugin already up to date %s\n", p.initPath)
	}

	if !p.manifestExists {
		if err := os.WriteFile(p.manifest, []byte(hermesPluginYAML), 0o644); err != nil {
			return 1, fmt.Errorf("write %s: %w", p.manifest, err)
		}
		fmt.Printf("gortk: wrote Hermes plugin manifest %s\n", p.manifest)
	} else if verbose > 0 {
		fmt.Printf("gortk: Hermes plugin manifest already up to date %s\n", p.manifest)
	}

	switch {
	case p.configEnabled:
		fmt.Printf("gortk: Hermes config already enables %s in %s\n", hermesPluginName, p.configPath)
	case p.newConfig != "":
		if p.configExists {
			// Back up before mutating an existing file.
			if data, rerr := os.ReadFile(p.configPath); rerr == nil {
				_ = os.WriteFile(p.configPath+".bak", data, 0o644)
			}
		}
		if err := os.WriteFile(p.configPath, []byte(p.newConfig), 0o644); err != nil {
			return 1, fmt.Errorf("write %s: %w", p.configPath, err)
		}
		verb := "created"
		if p.configExists {
			verb = "patched"
		}
		fmt.Printf("gortk: %s %s (enabled %s)\n", verb, p.configPath, hermesPluginName)
	}

	fmt.Println("gortk: Hermes integration installed (user-level — all repos).")
	fmt.Println("gortk: restart Hermes to activate. Test with: git status")
	return 0, nil
}

// ── reporting (--show / --dry-run) ────────────────────────────────────

func printHermesState(p *hermesInstallPlan) {
	fmt.Println("gortk init --agent hermes — current state (Hermes CLI, user-level):")
	fmt.Printf("  hermes home:  %s\n", p.hermesHome)
	fmt.Printf("  plugin:       %s  (%s)\n", p.initPath, existsLabel(p.initExists, "present (current)", "missing/outdated"))
	fmt.Printf("  manifest:     %s  (%s)\n", p.manifest, existsLabel(p.manifestExists, "present (current)", "missing/outdated"))
	fmt.Printf("  config:       %s  (%s)\n", p.configPath, existsLabel(p.configExists, "exists", "absent"))
	fmt.Printf("  plugin enabled: %s\n", existsLabel(p.configEnabled, "yes", "no"))
}

func printHermesDryRun(p *hermesInstallPlan) {
	fmt.Println("gortk init --agent hermes --dry-run (Hermes CLI, user-level) — nothing will be written:")
	if p.initExists {
		fmt.Printf("  [dry-run] plugin already current: %s\n", p.initPath)
	} else {
		fmt.Printf("  [dry-run] would write plugin: %s\n", p.initPath)
	}
	if p.manifestExists {
		fmt.Printf("  [dry-run] manifest already current: %s\n", p.manifest)
	} else {
		fmt.Printf("  [dry-run] would write manifest: %s\n", p.manifest)
	}
	switch {
	case p.configEnabled:
		fmt.Printf("  [dry-run] config already enables %s: %s\n", hermesPluginName, p.configPath)
	case p.configExists:
		fmt.Printf("  [dry-run] would patch %s to enable %s (backed up to %s.bak)\n", p.configPath, hermesPluginName, p.configPath)
	default:
		fmt.Printf("  [dry-run] would create %s enabling %s\n", p.configPath, hermesPluginName)
	}
}
