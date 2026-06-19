// Command gortk is a CLI proxy that filters and compresses dev-tool output
// before it reaches an LLM context, cutting token consumption. It is a Go port
// of rtk (https://github.com/rtk-ai/rtk), reworked to run natively on Windows
// and to be offline by default: gortk makes no network calls of its own.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	_ "gortk/internal/cmds/allcmds" // self-registers all command modules
	"gortk/internal/core"
	"gortk/internal/registry"
	"gortk/internal/tomlfilter"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "0.1.0-dev"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	// Strip leading global flags (-v/-vv/-vvv, --version, --help) before the
	// subcommand. Anything after the subcommand belongs to that command.
	verbose := 0
	var cmdName string
	var rest []string
	for i := 0; i < len(argv); i++ {
		a := argv[i]
		switch {
		case a == "--version" || a == "-V":
			fmt.Println("gortk", version)
			return 0
		case a == "--help" || a == "-h":
			usage()
			return 0
		case a == "-v" || a == "--verbose":
			verbose++
		case strings.HasPrefix(a, "-v") && strings.Trim(a, "v") == "-":
			verbose += len(a) - 1 // -vv, -vvv
		default:
			cmdName = a
			rest = argv[i+1:]
			i = len(argv)
		}
	}

	if cmdName == "" {
		usage()
		return 1
	}

	if cmd, ok := registry.Lookup(cmdName); ok {
		code, err := cmd.Run(rest, verbose)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gortk:", err)
			if code == 0 {
				code = 1
			}
		}
		return code
	}

	// No dedicated command module: try the declarative TOML filter engine,
	// then fall back to raw passthrough. Mirrors rtk's fallback behaviour.
	return fallback(cmdName, rest, verbose)
}

// fallback handles a command with no dedicated module: it runs the tool, and if
// a TOML filter matches the command line, compresses the output; otherwise it
// streams output through unchanged.
func fallback(cmdName string, rest []string, verbose int) int {
	if os.Getenv("GORTK_NO_TOML") != "1" {
		lookup := strings.TrimSpace(cmdName + " " + strings.Join(rest, " "))
		if f := tomlfilter.FindMatching(lookup); f != nil {
			cmd := core.ResolvedCommand(cmdName, rest...)
			opts := core.RunOptions{FilterStdoutOnly: !f.FilterStderr}
			code, err := core.RunFiltered(cmd, cmdName, strings.Join(rest, " "), f.Apply, opts)
			if err != nil {
				fmt.Fprintln(os.Stderr, "gortk:", err)
			}
			return code
		}
	}
	code, err := core.RunPassthrough(cmdName, rest, verbose)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gortk:", err)
	}
	return code
}

func usage() {
	fmt.Fprintf(os.Stderr, "gortk %s — token-optimized CLI proxy (Go port of rtk)\n\n", version)
	fmt.Fprintln(os.Stderr, "Usage: gortk [-v] <command> [args...]")
	fmt.Fprintln(os.Stderr, "\nDedicated commands:")
	cmds := registry.All()
	for _, c := range cmds {
		fmt.Fprintf(os.Stderr, "  %-14s %s\n", c.Name, c.Summary)
	}
	filters := tomlfilter.All()
	names := make([]string, 0, len(filters))
	for _, f := range filters {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	fmt.Fprintf(os.Stderr, "\nPlus %d declarative TOML filters for: %s\n", len(names), strings.Join(names, ", "))
	fmt.Fprintln(os.Stderr, "\nUnrecognized commands are run unchanged (passthrough).")
}
