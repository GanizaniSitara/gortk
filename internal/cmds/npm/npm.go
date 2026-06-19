// Package npm is gortk's token-optimized wrapper around npm and npx. It strips
// npm boilerplate (lifecycle banners, WARN/notice noise, progress indicators)
// and auto-injects the "run" subcommand when the first argument looks like a
// package.json script rather than a known npm subcommand.
//
// Faithful port of rtk's src/cmds/js/npm_cmd.rs.
//
// Note on npx routing: rtk's main.rs intelligently routes `npx tsc|eslint|
// prisma|next|prettier|playwright` to specialized command modules and only
// falls back to npm_cmd::exec for everything else. Those specialized filters
// are separate gortk packages; to avoid cross-package coupling this module
// implements only the npm_cmd::exec fallback for npx (run npx through the same
// filtered pipeline as npm). The specialized routing is a dispatch-level
// concern wired elsewhere.
package npm

import (
	"fmt"
	"os"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "npm",
		Summary: "npm run with filtered output (strip boilerplate)",
		Run:     RunNpm,
	})
	registry.Register(&registry.Cmd{
		Name:    "npx",
		Summary: "npx with filtered output (strip boilerplate)",
		Run:     RunNpx,
	})
}

// npmSubcommands are known npm subcommands that should NOT get "run" injected.
// Shared between production code and tests to avoid drift (mirrors rtk's
// NPM_SUBCOMMANDS const).
var npmSubcommands = []string{
	"install", "i", "ci", "uninstall", "remove", "rm", "update", "up",
	"list", "ls", "outdated", "init", "create", "publish", "pack", "link",
	"audit", "fund", "exec", "explain", "why", "search", "view", "info",
	"show", "config", "set", "get", "cache", "prune", "dedupe", "doctor",
	"help", "version", "prefix", "root", "bin", "bugs", "docs", "home",
	"repo", "ping", "whoami", "token", "profile", "team", "access", "owner",
	"deprecate", "dist-tag", "star", "stars", "login", "logout", "adduser",
	"unpublish", "pkg", "diff", "rebuild", "test", "t", "start", "stop",
	"restart",
}

// npmSubcommandSet is the lookup form of npmSubcommands.
var npmSubcommandSet = func() map[string]bool {
	m := make(map[string]bool, len(npmSubcommands))
	for _, s := range npmSubcommands {
		m[s] = true
	}
	return m
}()

// needsRunInjection reports whether "run" should be auto-injected before args.
// "rtk npm build" → "npm run build" (assume script name), but a known npm
// subcommand, a flag, an explicit "run", or no args should be left alone.
func needsRunInjection(args []string) bool {
	if len(args) == 0 {
		return false
	}
	first := args[0]
	if first == "run" {
		return false
	}
	if npmSubcommandSet[first] || strings.HasPrefix(first, "-") {
		return false
	}
	return true
}

// RunNpm executes the npm command, injecting "run" when appropriate.
func RunNpm(args []string, verbose int) (int, error) {
	effective := args
	if needsRunInjection(args) {
		effective = append([]string{"run"}, args...)
	}
	return runFiltered("npm", effective, verbose)
}

// RunNpx runs an npx tool through the same filtered pipeline as npm.
func RunNpx(args []string, verbose int) (int, error) {
	if len(args) == 0 {
		return 1, fmt.Errorf("npx requires a command argument")
	}
	return runFiltered("npx", args, verbose)
}

// runFiltered is the shared command-execution path for npm and npx. It builds
// the resolved command, appends args, emits the verbose log line, and routes
// through core.RunFiltered with the npm output filter.
func runFiltered(name string, args []string, verbose int) (int, error) {
	cmd := core.ResolvedCommand(name, args...)

	argsDisplay := strings.Join(args, " ")
	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: %s %s\n", name, argsDisplay)
	}

	return core.RunFiltered(cmd, name, argsDisplay, filterNpmOutput, core.RunOptions{})
}

// filterNpmOutput strips npm run boilerplate: lifecycle banners, npm WARN /
// npm notice lines, progress indicators, and blank lines. Returns "ok" when
// nothing survives.
func filterNpmOutput(output string) string {
	var result []string

	for _, line := range splitLines(output) {
		// Skip npm lifecycle banner lines like "> project@1.0.0 build".
		if strings.HasPrefix(line, ">") && strings.Contains(line, "@") {
			continue
		}
		trimmedStart := strings.TrimLeft(line, " \t")
		// Skip npm WARN / npm notice noise.
		if strings.HasPrefix(trimmedStart, "npm WARN") {
			continue
		}
		if strings.HasPrefix(trimmedStart, "npm notice") {
			continue
		}
		// Skip progress indicators.
		if strings.Contains(line, "⸩") || strings.Contains(line, "⸨") ||
			(strings.Contains(line, "...") && len(line) < 10) {
			continue
		}
		// Skip empty lines.
		if strings.TrimSpace(line) == "" {
			continue
		}

		result = append(result, line)
	}

	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// splitLines mirrors Rust's str::lines(): split on '\n' and drop a single
// trailing empty element produced by a trailing newline.
func splitLines(s string) []string {
	s = core.NormalizeNewlines(s)
	parts := strings.Split(s, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}
