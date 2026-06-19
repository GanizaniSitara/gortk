// Package pip is gortk's token-optimized pip/uv wrapper. It filters
// `pip list` / `pip outdated` JSON into a compact, grouped summary and passes
// write operations (install/uninstall/show) through unchanged. Faithful port of
// rtk's src/cmds/python/pip_cmd.rs.
//
// Like rtk, this stays transparent: it runs `pip` so it reports the *same*
// environment the bare command would, and only falls back to `uv pip` when pip
// genuinely isn't on PATH (uv-only environments). gortk resolves both tools
// PATHEXT-aware via core.ResolvedCommand. The output-compression logic lives in
// pure helper functions (filterPipList, filterPipOutdated) so it can be tested
// directly against the ported Rust spec.
//
// Note on tracking: rtk wraps run() in a tracking::TimedExecution timer that
// records token savings. gortk drops that side-channel here — core.RunFiltered
// and core.RunPassthrough already record every execution, so an explicit timer
// would just double-count. gortk is offline by default; there is no telemetry.
package pip

import (
	"fmt"
	"os"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "pip",
		Summary: "pip/uv commands with compact output",
		Run:     Run,
	})
}

// Run dispatches the gortk `pip` command. args are the tokens after "pip";
// verbose is the -v count. It mirrors rtk's run(): it prefers the real `pip`
// (so it reports the active environment) and only falls back to `uv pip` when
// pip is absent and uv is present, then routes list / outdated to their
// filtered runners and install / uninstall / show / anything-else to a
// passthrough.
func Run(args []string, verbose int) (int, error) {
	// Prefer `pip`; only fall back to `uv pip` when pip genuinely isn't on PATH.
	// Auto-substituting `uv pip` unconditionally would make `pip list` report
	// uv's discovered env rather than the active interpreter.
	useUv := !core.ToolExists("pip") && core.ToolExists("uv")
	baseCmd := "pip"
	if useUv {
		baseCmd = "uv"
	}

	if verbose > 0 && useUv {
		fmt.Fprintln(os.Stderr, "pip not found — falling back to `uv pip`")
	}

	subcommand := ""
	if len(args) > 0 {
		subcommand = args[0]
	}

	switch subcommand {
	case "list":
		return runList(baseCmd, args[1:], verbose)
	case "outdated":
		return runOutdated(baseCmd, args[1:], verbose)
	default:
		// install / uninstall / show and any unknown subcommand: passthrough.
		return runPassthrough(baseCmd, args, verbose)
	}
}

// runList runs `<baseCmd> [pip] list --format=json <args...>`, then filters the
// captured JSON stdout into a compact, letter-grouped inventory.
func runList(baseCmd string, args []string, verbose int) (int, error) {
	cmd := core.ResolvedCommand(baseCmd)
	if baseCmd == "uv" {
		cmd.Args = append(cmd.Args, "pip")
	}
	cmd.Args = append(cmd.Args, "list", "--format=json")
	cmd.Args = append(cmd.Args, args...)

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: %s pip list --format=json\n", baseCmd)
	}

	// Filter stdout only: pip may write deprecation/env warnings to stderr that
	// would otherwise corrupt the JSON the filter parses. stderr passes through.
	opts := core.RunOptions{FilterStdoutOnly: true}
	return core.RunFiltered(cmd, baseCmd+" pip list", strings.Join(args, " "), func(stdout string) string {
		return filterPipList(stdout)
	}, opts)
}

// runOutdated runs `<baseCmd> [pip] list --outdated --format=json <args...>`,
// then filters the captured JSON stdout into a compact upgrade list.
func runOutdated(baseCmd string, args []string, verbose int) (int, error) {
	cmd := core.ResolvedCommand(baseCmd)
	if baseCmd == "uv" {
		cmd.Args = append(cmd.Args, "pip")
	}
	cmd.Args = append(cmd.Args, "list", "--outdated", "--format=json")
	cmd.Args = append(cmd.Args, args...)

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: %s pip list --outdated --format=json\n", baseCmd)
	}

	opts := core.RunOptions{FilterStdoutOnly: true}
	return core.RunFiltered(cmd, baseCmd+" pip list --outdated", strings.Join(args, " "), func(stdout string) string {
		return filterPipOutdated(stdout)
	}, opts)
}

// runPassthrough runs a write/unknown subcommand directly with no filtering. For
// uv the real argv must be `uv pip <args...>`, so the "pip" token is spliced in
// front; for pip the args are forwarded verbatim.
func runPassthrough(baseCmd string, args []string, verbose int) (int, error) {
	if baseCmd == "uv" {
		uvArgs := append([]string{"pip"}, args...)
		return core.RunPassthrough("uv", uvArgs, verbose)
	}
	return core.RunPassthrough("pip", args, verbose)
}
