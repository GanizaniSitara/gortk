// init_agents.go implements `gortk init --agent <name>`: the dispatch layer plus
// shared plumbing for the additional agent installers ported from rtk's
// src/hooks/init.rs (the per-agent install_* / run_*_mode functions and their
// hooks/<agent>/ templates).
//
// Three install shapes are covered, matching rtk:
//
//   - JSON hook patch (programmatic): cursor — patches ~/.cursor/hooks.json with
//     a preToolUse entry invoking `gortk hook cursor`. (init_cursor.go)
//   - Rules-file install (prompt-only): windsurf, cline, kilocode, antigravity —
//     write a project-local rules markdown file telling the agent to prefix
//     shell commands with `gortk`. No programmatic hook; the agent has no hook
//     surface, so this is guidance text. (init_rules.go)
//   - Plugin install (programmatic): pi, opencode, hermes — drop a thin plugin
//     that shells out to `gortk rewrite` (the single rewrite source of truth) on
//     each bash command. (init_plugins.go)
//
// Native-Windows adaptation of rtk: where rtk wrote shell scripts or assumed a
// bash hook command, gortk writes the matching `gortk hook <agent>` /
// `gortk rewrite` invocation and resolves paths via os.UserHomeDir() +
// path/filepath. Each installer reuses the marker/round-trip/backup/--show/--dry-run
// patterns established by the Claude/Copilot/Codex installers, and exposes a
// base-path test seam (…At) so tests drive a t.TempDir() without touching the
// real profile.
package hooks

import (
	"fmt"
	"os"
	"sort"
)

// agentInstaller resolves the real user home / current directory and performs
// the install for one agent. show/dryRun mean the same as the other installers:
// report-only and plan-only respectively, writing nothing.
type agentInstaller func(show, dryRun bool, verbose int) (int, error)

// agentInstallers maps each supported `--agent <name>` to its installer. Keeping
// the table in one place lets agentNames() drive the usage text and lets
// runAgentInit reject unknown names with a helpful list.
var agentInstallers = map[string]agentInstaller{
	"cursor":      runCursorInit,
	"windsurf":    runWindsurfInit,
	"cline":       runClineInit,
	"kilocode":    runKilocodeInit,
	"antigravity": runAntigravityInit,
	"pi":          runPiInit,
	"opencode":    runOpencodeInit,
	"hermes":      runHermesInit,
}

// agentNames returns the supported agent names in stable sorted order for usage
// text and error messages.
func agentNames() []string {
	names := make([]string, 0, len(agentInstallers))
	for name := range agentInstallers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// runAgentInit dispatches `gortk init --agent <name>` to the matching installer,
// or reports the supported set when the name is unknown.
func runAgentInit(agent string, show, dryRun bool, verbose int) (int, error) {
	install, ok := agentInstallers[agent]
	if !ok {
		fmt.Fprintf(os.Stderr, "gortk init: unknown agent %q\n", agent)
		fmt.Fprintf(os.Stderr, "gortk init: supported agents: %s\n", joinNames(agentNames()))
		return 2, nil
	}
	return install(show, dryRun, verbose)
}

// joinNames renders a name list as "a, b, c" for messages (avoids importing
// strings just for one Join in this file's error path).
func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}

// homeDirOr resolves the user home directory, returning a wrapped error suitable
// for the installer's (int, error) contract. Shared by the home-scoped agent
// installers (cursor, pi, hermes, opencode).
func homeDirOr() (string, int, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", 1, fmt.Errorf("cannot resolve home directory: %w", err)
	}
	return home, 0, nil
}
