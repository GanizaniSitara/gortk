// Package cargo is gortk's token-optimized cargo wrapper. It filters cargo
// output — build errors, test results, clippy warnings, install summaries, and
// nextest runs — keeping just the essential info an agent needs. Faithful port
// of rtk's src/cmds/rust/cargo_cmd.rs.
//
// Like rtk, this wraps the platform `cargo`; gortk resolves it PATHEXT-aware via
// core.ResolvedCommand. The output-compression logic lives in pure helper
// functions (filterCargoBuild, filterCargoTest, filterCargoClippy,
// filterCargoInstall, filterCargoNextest) so it can be tested directly against
// the ported Rust spec.
//
// Subcommand dispatch is parsed from args inside Run, mirroring rtk's clap
// CargoCommands enum: build / test / clippy / check / install / nextest, plus an
// external-subcommand passthrough for any other cargo subcommand.
//
// Note on rtk's restore_double_dash: rtk uses clap with trailing_var_arg, which
// consumes "--" separators that must then be restored before exec. gortk parses
// args itself and does no clap pre-pass, so the "--" tokens arrive intact and
// are forwarded verbatim — no restoration is needed (the git diff port documents
// the same situation). rtk's tracking/timer side-channels are likewise dropped:
// gortk is offline by default.
package cargo

import (
	"fmt"
	"os"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "cargo",
		Summary: "Cargo commands with compact output",
		Run:     Run,
	})
}

// Run dispatches the gortk `cargo` command. args are the tokens after "cargo";
// verbose is the -v count. It parses the cargo subcommand itself, mirroring
// rtk's clap dispatch in main.rs (CargoCommands), then routes build / test /
// clippy / check / install / nextest to their filtered runners and everything
// else to a direct passthrough.
func Run(args []string, verbose int) (int, error) {
	if len(args) == 0 {
		// No subcommand: pass through bare `cargo` so it prints its usage.
		return core.RunPassthrough("cargo", args, verbose)
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "build":
		return runCargoFiltered("build", subArgs, verbose, filterCargoBuild)
	case "test":
		return runCargoFiltered("test", subArgs, verbose, filterCargoTest)
	case "clippy":
		return runCargoFiltered("clippy", subArgs, verbose, filterCargoClippy)
	case "check":
		return runCargoFiltered("check", subArgs, verbose, filterCargoBuild)
	case "install":
		return runCargoFiltered("install", subArgs, verbose, filterCargoInstall)
	case "nextest":
		return runCargoFiltered("nextest", subArgs, verbose, filterCargoNextest)
	default:
		// Passthrough: any unsupported cargo subcommand runs directly.
		return core.RunPassthrough("cargo", args, verbose)
	}
}

// runCargoFiltered runs `cargo <subcommand> <args...>`, captures the output, and
// applies filterFn to compress it. It mirrors rtk's run_cargo_filtered /
// run_cargo_streamed: rtk's BlockStreamFilter (build/test/check/nextest) and the
// plain filter path (clippy/install) both reduce to the same pure filter
// functions, so gortk uses the capture-then-filter path uniformly.
//
// gortk drops rtk's restore_double_dash step: the args arrive intact (no clap
// pre-pass consumes "--"), so they are forwarded verbatim.
func runCargoFiltered(subcommand string, args []string, verbose int, filterFn func(string) string) (int, error) {
	cmd := core.ResolvedCommand("cargo")
	cmd.Args = append(cmd.Args, subcommand)
	cmd.Args = append(cmd.Args, args...)

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: cargo %s %s\n", subcommand, strings.Join(args, " "))
	}

	// rtk filters combined stdout+stderr (cargo writes diagnostics to stderr),
	// emits the compacted form, and tees the raw output on failure so an agent
	// can re-read it. The filter is applied regardless of exit code, matching
	// rtk's RunOptions::with_tee (no SkipFilterOnFailure / FilterStdoutOnly).
	opts := core.RunOptions{TeeLabel: "cargo_" + subcommand}
	return core.RunFiltered(cmd, "cargo "+subcommand, strings.Join(args, " "), filterFn, opts)
}
