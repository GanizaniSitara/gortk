// init_plugins.go implements the PLUGIN-based agent installers:
// `gortk init --agent {opencode,pi,hermes}`. These agents load a thin plugin
// that shells out to `gortk rewrite` (the single rewrite source of truth) on each
// bash command — there is no JSON hook protocol, so the install is a write-the-
// plugin-file operation. It is a native-Windows, offline port of rtk's
// src/hooks/init.rs (ensure_opencode_plugin_installed / run_pi_mode /
// run_hermes_mode_at) and the hooks/{opencode,pi,hermes}/ templates.
//
// Install targets (home-scoped, all repos), matching rtk:
//
//   - opencode → ~/.config/opencode/plugins/gortk.ts   (single TS plugin file)
//   - pi       → ~/.pi/agent/extensions/gortk.ts        (single TS extension file;
//     env PI_CODING_AGENT_DIR overrides ~/.pi/agent)
//   - hermes   → ~/.hermes/plugins/gortk-rewrite/{__init__.py,plugin.yaml}
//     plus a config.yaml plugins-enabled patch (env HERMES_HOME
//     overrides ~/.hermes)
//
// The plugin bodies are gortk-branded ports of the rtk templates, rewritten to
// invoke `gortk rewrite` and to look for the gortk binary. Each installer is
// write-if-changed (no-op when the file content already matches) and exposes a
// home seam (…At) so tests drive a t.TempDir().
package hooks

import (
	"fmt"
	"os"
	"path/filepath"
)

// ── embedded plugin templates (gortk-branded ports of hooks/<agent>/) ──

// opencodePluginTS is the gortk OpenCode plugin (port of hooks/opencode/rtk.ts).
// Thin delegating plugin: all rewrite logic lives in `gortk rewrite`.
const opencodePluginTS = `import type { Plugin } from "@opencode-ai/plugin"

// gortk OpenCode plugin — rewrites commands to use gortk for token savings.
// Requires: gortk in PATH.
//
// This is a thin delegating plugin: all rewrite logic lives in ` + "`gortk rewrite`" + `,
// the single source of truth. To change rewrite rules, edit gortk — not this file.

export const GortkOpenCodePlugin: Plugin = async ({ $ }) => {
  try {
    await $` + "`which gortk`" + `.quiet()
  } catch {
    console.warn("[gortk] gortk binary not found in PATH — plugin disabled")
    return {}
  }

  return {
    "tool.execute.before": async (input, output) => {
      const tool = String(input?.tool ?? "").toLowerCase()
      if (tool !== "bash" && tool !== "shell") return
      const args = output?.args
      if (!args || typeof args !== "object") return

      const command = (args as Record<string, unknown>).command
      if (typeof command !== "string" || !command) return

      try {
        const result = await $` + "`gortk rewrite ${command}`" + `.quiet().nothrow()
        const rewritten = String(result.stdout).trim()
        if (rewritten && rewritten !== command) {
          ;(args as Record<string, unknown>).command = rewritten
        }
      } catch {
        // gortk rewrite failed — pass through unchanged
      }
    },
  }
}
`

// piPluginTS is the gortk Pi extension (port of hooks/pi/rtk.ts). Thin delegating
// extension calling `gortk rewrite`; exit 0 + stdout means rewrite, exit 1 means
// pass through.
const piPluginTS = `// gortk Pi extension — rewrites bash commands to use gortk for token savings.
// Requires: gortk in PATH.
//
// This is a thin delegating extension: all rewrite logic lives in ` + "`gortk rewrite`" + `,
// the single source of truth. To change rewrite rules, edit gortk — not this file.
//
// Exit code contract for ` + "`gortk rewrite`" + `:
//   0 + stdout  Rewrite found → mutate command
//   1           No gortk equivalent → pass through unchanged

import type { ExtensionAPI } from "@earendil-works/pi-coding-agent"
import { isToolCallEventType } from "@earendil-works/pi-coding-agent"

const REWRITE_TIMEOUT_MS = 2_000

// Calls ` + "`gortk rewrite`" + `; returns the rewritten command or null (pass through).
async function rewriteCommand(
  pi: ExtensionAPI,
  cmd: string,
  signal?: AbortSignal
): Promise<string | null> {
  const result = await pi.exec("gortk", ["rewrite", cmd], {
    timeout: REWRITE_TIMEOUT_MS,
    signal,
  })
  if (result.killed) return null
  if (result.code !== 0) return null
  return result.stdout.trim() || null
}

export default async function (pi: ExtensionAPI) {
  // Probe gortk at load time; disables extension if missing.
  const ver = await pi.exec("gortk", ["--version"], { timeout: REWRITE_TIMEOUT_MS })
  if (ver.code !== 0) {
    console.warn("[gortk] gortk binary not found in PATH — extension disabled")
    return
  }

  pi.on("tool_call", async (event, ctx) => {
    try {
      if (!isToolCallEventType("bash", event)) return

      const cmd = event.input.command
      if (typeof cmd !== "string" || cmd.trim() === "") return

      if (cmd.startsWith("gortk ")) return
      if (process.env.GORTK_DISABLED === "1") return

      const rewritten = await rewriteCommand(pi, cmd, ctx.signal)
      if (rewritten && rewritten !== cmd) {
        event.input.command = rewritten
      }
    } catch (err) {
      // Fail open: never block execution on an unexpected error.
      console.warn("[gortk] unexpected error in tool_call handler; passing through command", err)
      return
    }
  })
}
`

// hermesPluginInitPy is the gortk Hermes plugin adapter (port of
// hooks/hermes/rtk-rewrite/__init__.py). Bridges Hermes pre_tool_call payloads to
// `gortk rewrite` and fails open.
const hermesPluginInitPy = `"""Hermes plugin adapter for gortk command rewriting.

All rewrite logic lives in gortk's ` + "``gortk rewrite``" + ` command; this module
only bridges Hermes ` + "``pre_tool_call``" + ` payloads to that command and fails open.
"""

import shutil
import subprocess
import sys


_gortk_available = None
_gortk_missing_warned = False


def register(ctx):
    """Register the Hermes pre-tool callback."""
    if not _check_gortk():
        return

    ctx.register_hook("pre_tool_call", _pre_tool_call)


def _check_gortk():
    """Return whether the gortk binary is in PATH, warning once when missing."""
    global _gortk_available, _gortk_missing_warned

    if _gortk_available is None:
        _gortk_available = shutil.which("gortk") is not None

    if not _gortk_available and not _gortk_missing_warned:
        _warn("gortk binary not found in PATH; Hermes hook not registered")
        _gortk_missing_warned = True

    return _gortk_available


def _pre_tool_call(tool_name=None, args=None, **_kwargs):
    """Rewrite mutable Hermes terminal command args when gortk provides a change."""
    try:
        if tool_name != "terminal" or not isinstance(args, dict):
            return

        command = args.get("command")
        if not isinstance(command, str) or not command.strip():
            return

        try:
            result = subprocess.run(
                ["gortk", "rewrite", command],
                shell=False,
                timeout=2,
                capture_output=True,
                text=True,
            )
        except subprocess.TimeoutExpired:
            _warn("gortk rewrite timed out")
            return

        # gortk rewrite: exit 0 + stdout = rewrite; exit 1 = pass through.
        if result.returncode != 0:
            return

        rewritten = result.stdout.strip()
        if rewritten and rewritten != command:
            args["command"] = rewritten
    except Exception as e:
        _warn(str(e))
        return


def _warn(message):
    print(f"gortk: hermes plugin warning: {message}", file=sys.stderr)
`

// hermesPluginYAML is the gortk Hermes plugin manifest (port of
// hooks/hermes/rtk-rewrite/plugin.yaml).
const hermesPluginYAML = `name: gortk-rewrite
version: "0.1.0"
description: Rewrite Hermes terminal commands through gortk before execution.
author: gortk
hooks:
  - pre_tool_call
provides_hooks:
  - pre_tool_call
`

// ── single-file TS plugin installers (opencode, pi) ───────────────────

const (
	opencodeConfigDir  = ".config"
	opencodeSubdir     = "opencode"
	pluginsSubdir      = "plugins"
	opencodePluginFile = "gortk.ts"

	piHomeDir       = ".pi/agent"
	piExtensionsDir = "extensions"
	piPluginFile    = "gortk.ts"
	piHomeEnv       = "PI_CODING_AGENT_DIR"
)

// tsPluginPlan is the shared plan for the single-file TS plugin installers.
type tsPluginPlan struct {
	agent      string // "opencode" / "pi" for messages
	pluginPath string // resolved plugin file path
	content    string // plugin body to write
	exists     bool   // plugin already exists with current content
}

// resolveOpencodeDir returns ~/.config/opencode (rtk resolve_opencode_dir).
func resolveOpencodeDir(home string) string {
	return filepath.Join(home, opencodeConfigDir, opencodeSubdir)
}

// resolvePiDir returns the Pi agent dir, honouring PI_CODING_AGENT_DIR (rtk
// resolve_pi_dir). The env override points at the Pi agent root (the dir that
// contains "extensions/").
func resolvePiDir(home string) string {
	if env := os.Getenv(piHomeEnv); env != "" {
		return env
	}
	return filepath.Join(home, filepath.FromSlash(piHomeDir))
}

func runOpencodeInit(show, dryRun bool, verbose int) (int, error) {
	home, code, err := homeDirOr()
	if err != nil {
		return code, err
	}
	return runOpencodeInitAt(home, show, dryRun, verbose)
}

// runOpencodeInitAt installs ~/.config/opencode/plugins/gortk.ts.
func runOpencodeInitAt(home string, show, dryRun bool, verbose int) (int, error) {
	path := filepath.Join(resolveOpencodeDir(home), pluginsSubdir, opencodePluginFile)
	return runTSPluginInit(tsPluginPlan{
		agent:      "opencode",
		pluginPath: path,
		content:    opencodePluginTS,
		exists:     fileHasContent(path, opencodePluginTS),
	}, show, dryRun, verbose)
}

func runPiInit(show, dryRun bool, verbose int) (int, error) {
	home, code, err := homeDirOr()
	if err != nil {
		return code, err
	}
	return runPiInitAt(home, show, dryRun, verbose)
}

// runPiInitAt installs <pi-agent-dir>/extensions/gortk.ts.
func runPiInitAt(home string, show, dryRun bool, verbose int) (int, error) {
	path := filepath.Join(resolvePiDir(home), piExtensionsDir, piPluginFile)
	return runTSPluginInit(tsPluginPlan{
		agent:      "pi",
		pluginPath: path,
		content:    piPluginTS,
		exists:     fileHasContent(path, piPluginTS),
	}, show, dryRun, verbose)
}

// runTSPluginInit performs the shared write-if-changed install for a single-file
// TS plugin, with --show / --dry-run reporting.
func runTSPluginInit(p tsPluginPlan, show, dryRun bool, verbose int) (int, error) {
	if show {
		fmt.Printf("gortk init --agent %s — current state (plugin, user-level):\n", p.agent)
		fmt.Printf("  plugin:  %s  (%s)\n", p.pluginPath, existsLabel(p.exists, "present (current)", "missing/outdated"))
		return 0, nil
	}
	if dryRun {
		fmt.Printf("gortk init --agent %s --dry-run (plugin, user-level) — nothing will be written:\n", p.agent)
		if p.exists {
			fmt.Printf("  [dry-run] plugin already current: %s\n", p.pluginPath)
		} else {
			fmt.Printf("  [dry-run] would write plugin: %s\n", p.pluginPath)
		}
		return 0, nil
	}

	if p.exists {
		fmt.Printf("gortk: %s plugin already up to date %s\n", p.agent, p.pluginPath)
		return 0, nil
	}
	dir := filepath.Dir(p.pluginPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 1, fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.WriteFile(p.pluginPath, []byte(p.content), 0o644); err != nil {
		return 1, fmt.Errorf("write %s: %w", p.pluginPath, err)
	}
	fmt.Printf("gortk: wrote %s plugin %s\n", p.agent, p.pluginPath)
	fmt.Printf("gortk: restart %s to activate. Test with: git status\n", p.agent)
	return 0, nil
}

// fileHasContent reports whether path exists and already contains exactly want.
func fileHasContent(path, want string) bool {
	data, err := os.ReadFile(path)
	return err == nil && string(data) == want
}
