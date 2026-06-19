// Package pnpm is gortk's token-optimized wrapper around the pnpm package
// manager. It mirrors rtk's src/cmds/js/pnpm_cmd.rs: it compresses
// `pnpm list`, `pnpm outdated`, and `pnpm install` output, delegates
// `pnpm typecheck` to a TypeScript-compiler filter (rtk's tsc_cmd), and passes
// any other pnpm subcommand straight through.
//
// rtk exposes pnpm as a clap subcommand group (`rtk pnpm list`, `rtk pnpm
// outdated`, …) with a repeatable `--filter`/`-F` global option that precedes
// the subcommand. gortk has no nested command framework, so this module parses
// the leading `--filter`/`-F` options and the subcommand out of args itself,
// exactly as rtk's main.rs dispatch does.
package pnpm

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

// maxListing caps the per-section package listing (rtk: MAX_LISTING = CAP_LIST).
const maxListing = core.CapList

func init() {
	registry.Register(&registry.Cmd{
		Name:    "pnpm",
		Summary: "pnpm wrapper with ultra-compact list/outdated/install/typecheck output",
		Run:     Run,
	})
}

// Run is the gortk entry point. It receives the args AFTER the "pnpm" command
// name plus the -v count, and dispatches to the matching subcommand, mirroring
// rtk's `Commands::Pnpm` handler in main.rs.
func Run(args []string, verbose int) (int, error) {
	// Pull leading global --filter / -F <value> options (repeatable). In rtk
	// these are a clap option that precedes the subcommand; merge_pnpm_args
	// then prepends them as --filter=<value> to the subcommand args.
	filters, rest := splitFilters(args)

	if len(rest) == 0 {
		// Bare `gortk pnpm` with no subcommand: pass through to pnpm.
		return core.RunPassthrough("pnpm", mergeFilters(filters, nil), verbose)
	}

	sub := rest[0]
	subArgs := rest[1:]

	switch sub {
	case "list", "ls":
		depth, listArgs := parseDepth(subArgs)
		return runList(depth, mergeFilters(filters, listArgs), verbose)
	case "outdated":
		return runOutdated(mergeFilters(filters, subArgs), verbose)
	case "install", "i":
		return runInstall(mergeFilters(filters, subArgs), verbose)
	case "typecheck":
		// Delegates to the tsc filter. rtk warns that --filter is not yet
		// supported for `pnpm typecheck` and ignores leading filters.
		if len(filters) > 0 {
			fmt.Fprintln(os.Stderr, "[gortk] warning: --filter is not yet supported for pnpm tsc, filters preceding the subcommand will be ignored")
		}
		return runTypecheck(subArgs, verbose)
	default:
		// Passthrough: any unsupported pnpm subcommand runs directly.
		return core.RunPassthrough("pnpm", mergeFilters(filters, rest), verbose)
	}
}

// splitFilters extracts leading --filter / -F options and their values from
// args, returning the collected filter values and the remaining args. It only
// consumes options that appear before the subcommand, matching clap's global
// option placement.
func splitFilters(args []string) (filters, rest []string) {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--filter" || a == "-F":
			if i+1 < len(args) {
				filters = append(filters, args[i+1])
				i += 2
			} else {
				i++
			}
		case strings.HasPrefix(a, "--filter="):
			filters = append(filters, strings.TrimPrefix(a, "--filter="))
			i++
		case strings.HasPrefix(a, "-F") && len(a) > 2:
			filters = append(filters, a[2:])
			i++
		default:
			// First non-filter token ends the global-option run.
			return filters, args[i:]
		}
	}
	return filters, nil
}

// mergeFilters prepends filters as --filter=<value> to args, mirroring rtk's
// merge_pnpm_args.
func mergeFilters(filters, args []string) []string {
	if len(filters) == 0 {
		return args
	}
	out := make([]string, 0, len(filters)+len(args))
	for _, f := range filters {
		out = append(out, "--filter="+f)
	}
	return append(out, args...)
}

// parseDepth pulls a leading "--depth N" / "--depth=N" / "-d N" out of the list
// args (rtk default 0), returning the depth and the remaining args. Unknown
// values fall back to the default depth.
func parseDepth(args []string) (int, []string) {
	depth := 0
	var rest []string
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--depth" || a == "-d":
			if i+1 < len(args) {
				if v, ok := atoi(args[i+1]); ok {
					depth = v
				}
				i += 2
				continue
			}
			i++
		case strings.HasPrefix(a, "--depth="):
			if v, ok := atoi(strings.TrimPrefix(a, "--depth=")); ok {
				depth = v
			}
			i++
		default:
			rest = append(rest, a)
			i++
		}
	}
	return depth, rest
}

func atoi(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

// --- list -----------------------------------------------------------------

func runList(depth int, args []string, verbose int) (int, error) {
	cmd := core.ResolvedCommand("pnpm", "list", fmt.Sprintf("--depth=%d", depth), "--json")
	cmd.Args = append(cmd.Args, args...)

	isFiltered := false
	for _, a := range args {
		switch a {
		case "--prod", "-P", "--dev", "-D":
			isFiltered = true
		}
	}

	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, "pnpm", fmt.Sprintf("list --depth=%d", depth), func(raw string) string {
		res := parseList(raw)
		switch res.tier {
		case tierFull:
			if verbose > 0 {
				fmt.Fprintln(os.Stderr, "pnpm list (Tier 1: Full JSON parse)")
			}
		case tierDegraded:
			if verbose > 0 {
				emitDegradationWarning("pnpm list", strings.Join(res.warnings, ", "))
			}
		case tierPassthrough:
			emitPassthroughWarning("pnpm list", "All parsing tiers failed")
			return res.passthrough
		}
		return formatDependencyListing(res.state, !isFiltered)
	}, opts)
}

// --- outdated --------------------------------------------------------------

func runOutdated(args []string, verbose int) (int, error) {
	cmd := core.ResolvedCommand("pnpm", "outdated", "--format", "json")
	cmd.Args = append(cmd.Args, args...)

	// pnpm outdated exits non-zero when packages are outdated; we still want
	// to filter, so do not skip filtering on failure and combine stdout+stderr.
	opts := core.RunOptions{}
	return core.RunFiltered(cmd, "pnpm", "outdated", func(raw string) string {
		res := parseOutdated(raw)
		mode := formatModeFromVerbosity(verbose)
		var filtered string
		switch res.tier {
		case tierFull:
			if verbose > 0 {
				fmt.Fprintln(os.Stderr, "pnpm outdated (Tier 1: Full JSON parse)")
			}
			filtered = formatDependencyState(res.state, mode)
		case tierDegraded:
			if verbose > 0 {
				emitDegradationWarning("pnpm outdated", strings.Join(res.warnings, ", "))
			}
			filtered = formatDependencyState(res.state, mode)
		case tierPassthrough:
			emitPassthroughWarning("pnpm outdated", "All parsing tiers failed")
			filtered = res.passthrough
		}
		if strings.TrimSpace(filtered) == "" {
			return "All packages up-to-date"
		}
		return filtered
	}, opts)
}

// --- install ---------------------------------------------------------------

func runInstall(args []string, verbose int) (int, error) {
	cmd := core.ResolvedCommand("pnpm", "install")
	cmd.Args = append(cmd.Args, args...)

	if verbose > 0 {
		fmt.Fprintln(os.Stderr, "pnpm install running...")
	}

	opts := core.RunOptions{SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, "pnpm", "install", func(raw string) string {
		return filterPnpmInstall(raw)
	}, opts)
}

// --- typecheck (tsc) -------------------------------------------------------

func runTypecheck(args []string, verbose int) (int, error) {
	tscExists := core.ToolExists("tsc")

	var cmd = core.ResolvedCommand("tsc")
	tool := "tsc"
	if !tscExists {
		cmd = core.ResolvedCommand("npx", "tsc")
		tool = "npx tsc"
	}
	cmd.Args = append(cmd.Args, args...)

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: %s %s\n", tool, strings.Join(args, " "))
	}

	opts := core.RunOptions{TeeLabel: "tsc"}
	return core.RunFiltered(cmd, "tsc", strings.Join(args, " "), func(raw string) string {
		return filterTscOutput(raw)
	}, opts)
}

// --- JSON shapes -----------------------------------------------------------

// packageJSONListItem mirrors rtk's PackageJsonListItem: a node in pnpm list's
// recursive JSON tree.
type packageJSONListItem struct {
	Version         string                         `json:"version"`
	Dependencies    map[string]packageJSONListItem `json:"dependencies"`
	DevDependencies map[string]packageJSONListItem `json:"devDependencies"`
}

// pnpmListOutput mirrors rtk's PnpmListOutput: each workspace entry in the
// top-level `pnpm list --json` array.
type pnpmListOutput struct {
	Name            string                         `json:"name"`
	Version         string                         `json:"version"`
	Dependencies    map[string]packageJSONListItem `json:"dependencies"`
	DevDependencies map[string]packageJSONListItem `json:"devDependencies"`
}

// pnpmOutdatedPackage mirrors rtk's PnpmOutdatedPackage.
type pnpmOutdatedPackage struct {
	Current        string `json:"current"`
	Latest         string `json:"latest"`
	Wanted         string `json:"wanted"`
	DependencyType string `json:"dependencyType"`
}

// --- canonical types (rtk parser::types) -----------------------------------

// dependency mirrors rtk's Dependency. latest/wanted use pointers to mirror
// Option<String>.
type dependency struct {
	name           string
	currentVersion string
	latestVersion  *string
	wantedVersion  *string
	devDependency  bool
}

// dependencyState mirrors rtk's DependencyState.
type dependencyState struct {
	totalPackages int
	outdatedCount int
	dependencies  []dependency
}

// --- parse-result tiers (rtk parser::ParseResult) --------------------------

type parseTier int

const (
	tierFull parseTier = iota
	tierDegraded
	tierPassthrough
)

type listParseResult struct {
	tier        parseTier
	state       *dependencyState
	warnings    []string
	passthrough string
}

// --- list parser (rtk PnpmListParser) --------------------------------------

func parseList(input string) listParseResult {
	// Tier 1: JSON.
	var arr []pnpmListOutput
	if err := json.Unmarshal([]byte(input), &arr); err == nil {
		var deps []dependency
		total := 0
		for i := range arr {
			top := packageJSONListItem{
				Version:         arr[i].Version,
				Dependencies:    arr[i].Dependencies,
				DevDependencies: arr[i].DevDependencies,
			}
			collectDependencies(arr[i].Name, &top, false, &deps, &total)
		}
		return listParseResult{
			tier:  tierFull,
			state: &dependencyState{totalPackages: total, outdatedCount: 0, dependencies: deps},
		}
	} else {
		// Tier 2: text extraction.
		if st := extractListText(input); st != nil {
			return listParseResult{
				tier:     tierDegraded,
				state:    st,
				warnings: []string{fmt.Sprintf("JSON parse failed: %v", err)},
			}
		}
		// Tier 3: passthrough.
		return listParseResult{tier: tierPassthrough, passthrough: truncatePassthrough(input)}
	}
}

// collectDependencies walks the pnpm package tree, mirroring rtk's
// collect_dependencies. Go map iteration order is random; rtk's HashMap
// iteration is also unordered, so order is not part of the spec.
func collectDependencies(name string, pkg *packageJSONListItem, isDev bool, deps *[]dependency, count *int) {
	if pkg.Version != "" {
		v := pkg.Version
		*deps = append(*deps, dependency{
			name:           name,
			currentVersion: v,
			devDependency:  isDev,
		})
		*count++
	}
	for depName := range pkg.Dependencies {
		dp := pkg.Dependencies[depName]
		collectDependencies(depName, &dp, isDev, deps, count)
	}
	for depName := range pkg.DevDependencies {
		dp := pkg.DevDependencies[depName]
		collectDependencies(depName, &dp, true, deps, count)
	}
}

// extractListText is rtk's Tier-2 text extraction for `pnpm list`. It tracks
// the dependencies:/devDependencies: section headers and parses "pkg@version".
func extractListText(output string) *dependencyState {
	var deps []dependency
	count := 0
	isDev := false

	for _, line := range lines(output) {
		trimmed := strings.TrimSpace(line)

		if trimmed == "devDependencies:" {
			isDev = true
			continue
		}
		if trimmed == "dependencies:" {
			isDev = false
			continue
		}

		// Skip box-drawing and metadata.
		if strings.ContainsAny(line, "│├└") || strings.Contains(line, "Legend:") || trimmed == "" {
			continue
		}

		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		pkgStr := fields[0]
		if at := strings.LastIndex(pkgStr, "@"); at >= 0 {
			name := pkgStr[:at]
			version := pkgStr[at+1:]
			if name != "" && version != "" {
				deps = append(deps, dependency{
					name:           name,
					currentVersion: version,
					devDependency:  isDev,
				})
				count++
			}
		}
	}

	if count > 0 {
		return &dependencyState{totalPackages: count, outdatedCount: 0, dependencies: deps}
	}
	return nil
}

// --- outdated parser (rtk PnpmOutdatedParser) ------------------------------

func parseOutdated(input string) listParseResult {
	// Tier 1: JSON object keyed by package name.
	var obj map[string]pnpmOutdatedPackage
	if err := json.Unmarshal([]byte(input), &obj); err == nil && obj != nil {
		var deps []dependency
		outdated := 0
		// Sort keys for deterministic output (rtk iterates an unordered map;
		// stable order is a strict improvement and keeps tests reproducible).
		names := make([]string, 0, len(obj))
		for name := range obj {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			pkg := obj[name]
			if pkg.Current != pkg.Latest {
				outdated++
			}
			latest := pkg.Latest
			d := dependency{
				name:           name,
				currentVersion: pkg.Current,
				latestVersion:  &latest,
				devDependency:  pkg.DependencyType == "devDependencies",
			}
			if pkg.Wanted != "" {
				w := pkg.Wanted
				d.wantedVersion = &w
			}
			deps = append(deps, d)
		}
		return listParseResult{
			tier:  tierFull,
			state: &dependencyState{totalPackages: len(deps), outdatedCount: outdated, dependencies: deps},
		}
	} else {
		// Tier 2: text extraction.
		if st := extractOutdatedText(input); st != nil {
			msg := "JSON parse failed"
			if err != nil {
				msg = fmt.Sprintf("JSON parse failed: %v", err)
			}
			return listParseResult{tier: tierDegraded, state: st, warnings: []string{msg}}
		}
		// Tier 3: passthrough.
		return listParseResult{tier: tierPassthrough, passthrough: truncatePassthrough(input)}
	}
}

// extractOutdatedText is rtk's Tier-2 extraction for `pnpm outdated` table text.
func extractOutdatedText(output string) *dependencyState {
	var deps []dependency
	outdated := 0

	for _, line := range lines(output) {
		// Skip box-drawing, headers, legend.
		if strings.ContainsAny(line, "│├└─") ||
			strings.HasPrefix(line, "Legend:") ||
			strings.HasPrefix(line, "Package") ||
			strings.TrimSpace(line) == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) >= 4 {
			name := fields[0]
			current := fields[1]
			latest := fields[3]

			if current != latest {
				outdated++
			}
			lv := latest
			d := dependency{
				name:           name,
				currentVersion: current,
				latestVersion:  &lv,
				devDependency:  false,
			}
			if len(fields) > 2 {
				w := fields[2]
				d.wantedVersion = &w
			}
			deps = append(deps, d)
		}
	}

	if len(deps) > 0 {
		return &dependencyState{totalPackages: len(deps), outdatedCount: outdated, dependencies: deps}
	}
	return nil
}

// --- formatting ------------------------------------------------------------

// formatDependencyListing renders a `pnpm list` result with grouped
// [prod]/[dev] sections. cap=true (plain `pnpm list`) may truncate to
// maxListing per section; cap=false (`--prod`/`--dev`) shows every package.
// Mirrors rtk's format_dependency_listing.
func formatDependencyListing(state *dependencyState, cap bool) string {
	var prod, dev []dependency
	for _, d := range state.dependencies {
		if d.devDependency {
			dev = append(dev, d)
		} else {
			prod = append(prod, d)
		}
	}
	total := state.totalPackages
	if len(state.dependencies) > total {
		total = len(state.dependencies)
	}

	lines := []string{fmt.Sprintf("%d packages (%d prod / %d dev)", total, len(prod), len(dev))}

	appendSection := func(label, slug string, group []dependency) {
		if len(group) == 0 {
			return
		}
		lines = append(lines, label)
		shown := len(group)
		if cap && shown > maxListing {
			shown = maxListing
		}
		for _, dep := range group[:shown] {
			lines = append(lines, fmt.Sprintf("  %s %s", dep.name, dep.currentVersion))
		}
		if cap && len(group) > maxListing {
			lines = append(lines, fmt.Sprintf("  … +%d more", len(group)-maxListing))
			var allLines []string
			for _, dep := range group {
				allLines = append(allLines, fmt.Sprintf("  %s %s", dep.name, dep.currentVersion))
			}
			if hint := forceTeeTailHint(strings.Join(allLines, "\n"), slug, maxListing+1); hint != "" {
				lines = append(lines, "  "+hint)
			}
		}
	}

	appendSection("[prod]", "pnpm-prod", prod)
	appendSection("[dev]", "pnpm-dev", dev)

	return strings.Join(lines, "\n")
}

// formatMode mirrors rtk's parser::FormatMode.
type formatMode int

const (
	modeCompact formatMode = iota
	modeVerbose
	modeUltra
)

func formatModeFromVerbosity(v int) formatMode {
	switch {
	case v == 0:
		return modeCompact
	case v == 1:
		return modeVerbose
	default:
		return modeUltra
	}
}

// maxDepsListing mirrors rtk's MAX_DEPS_LISTING = CAP_INVENTORY in the
// DependencyState TokenFormatter.
const maxDepsListing = core.CapInventory

// formatDependencyState mirrors rtk's TokenFormatter for DependencyState
// (used by `pnpm outdated`).
func formatDependencyState(state *dependencyState, mode formatMode) string {
	switch mode {
	case modeVerbose:
		return formatStateVerbose(state)
	case modeUltra:
		return formatStateUltra(state)
	default:
		return formatStateCompact(state)
	}
}

func formatStateCompact(state *dependencyState) string {
	// A plain listing carries no upgrade info — every dep has no latest. Render
	// the actual packages instead of falsely claiming "up-to-date".
	isListing := state.outdatedCount == 0 && len(state.dependencies) > 0
	if isListing {
		for _, d := range state.dependencies {
			if d.latestVersion != nil {
				isListing = false
				break
			}
		}
	}
	if isListing {
		total := state.totalPackages
		if len(state.dependencies) > total {
			total = len(state.dependencies)
		}
		lines := []string{fmt.Sprintf("%d packages", total)}
		shown := len(state.dependencies)
		if shown > maxDepsListing {
			shown = maxDepsListing
		}
		for _, dep := range state.dependencies[:shown] {
			dev := ""
			if dep.devDependency {
				dev = " (dev)"
			}
			lines = append(lines, fmt.Sprintf("  %s %s%s", dep.name, dep.currentVersion, dev))
		}
		if len(state.dependencies) > maxDepsListing {
			lines = append(lines, fmt.Sprintf("  ... +%d more", len(state.dependencies)-maxDepsListing))
		}
		return strings.Join(lines, "\n")
	}

	if state.outdatedCount == 0 {
		return "All packages up-to-date"
	}

	lines := []string{fmt.Sprintf("%d outdated packages (of %d)", state.outdatedCount, state.totalPackages)}
	shown := 0
	for _, dep := range state.dependencies {
		if shown >= 10 {
			break
		}
		if dep.latestVersion != nil && dep.currentVersion != *dep.latestVersion {
			lines = append(lines, fmt.Sprintf("%s: %s → %s", dep.name, dep.currentVersion, *dep.latestVersion))
		}
		shown++
	}
	if state.outdatedCount > 10 {
		lines = append(lines, fmt.Sprintf("\n... +%d more", state.outdatedCount-10))
	}
	return strings.Join(lines, "\n")
}

func formatStateVerbose(state *dependencyState) string {
	lines := []string{fmt.Sprintf("Total packages: %d (%d outdated)", state.totalPackages, state.outdatedCount)}
	if state.outdatedCount > 0 {
		lines = append(lines, "\nOutdated packages:")
		for _, dep := range state.dependencies {
			if dep.latestVersion != nil && dep.currentVersion != *dep.latestVersion {
				devMarker := ""
				if dep.devDependency {
					devMarker = " (dev)"
				}
				lines = append(lines, fmt.Sprintf("  %s: %s → %s%s", dep.name, dep.currentVersion, *dep.latestVersion, devMarker))
				if dep.wantedVersion != nil && *dep.wantedVersion != *dep.latestVersion {
					lines = append(lines, fmt.Sprintf("    (wanted: %s)", *dep.wantedVersion))
				}
			}
		}
	}
	return strings.Join(lines, "\n")
}

func formatStateUltra(state *dependencyState) string {
	return fmt.Sprintf("pkg:%d ^%d", state.totalPackages, state.outdatedCount)
}

// --- install filter (rtk filter_pnpm_install) ------------------------------

// filterPnpmInstall removes progress bars and keeps error/summary lines.
func filterPnpmInstall(output string) string {
	var result []string
	sawProgress := false

	for _, line := range lines(output) {
		// Skip progress bars.
		if strings.Contains(line, "Progress") || strings.Contains(line, "│") || strings.Contains(line, "%") {
			sawProgress = true
			continue
		}

		if sawProgress && strings.TrimSpace(line) == "" {
			continue
		}

		// Keep error lines.
		if strings.Contains(line, "ERR") || strings.Contains(line, "error") || strings.Contains(line, "ERROR") {
			result = append(result, line)
			continue
		}

		// Keep summary lines.
		if strings.Contains(line, "packages in") ||
			strings.Contains(line, "dependencies") ||
			strings.HasPrefix(line, "+") ||
			strings.HasPrefix(line, "-") {
			result = append(result, strings.TrimSpace(line))
		}
	}

	if len(result) == 0 {
		return "ok"
	}
	return strings.Join(result, "\n")
}

// --- tsc filter (rtk tsc_cmd::filter_tsc_output) ---------------------------

var tscErrorRE = regexp.MustCompile(`^(.+?)\((\d+),(\d+)\):\s+(error|warning)\s+(TS\d+):\s+(.+)$`)

// filterTscOutput groups TypeScript compiler errors by file and error code.
// Faithful port of rtk's tsc_cmd::filter_tsc_output.
func filterTscOutput(output string) string {
	type tsError struct {
		file         string
		line         int
		code         string
		message      string
		contextLines []string
	}

	var errors []tsError
	ls := lines(output)
	i := 0
	for i < len(ls) {
		line := ls[i]
		if m := tscErrorRE.FindStringSubmatch(line); m != nil {
			ln, _ := atoi(m[2])
			err := tsError{
				file:    m[1],
				line:    ln,
				code:    m[5],
				message: m[6],
			}
			// Capture indented continuation lines.
			i++
			for i < len(ls) {
				next := ls[i]
				if next != "" &&
					(strings.HasPrefix(next, "  ") || strings.HasPrefix(next, "\t")) &&
					!tscErrorRE.MatchString(next) {
					err.contextLines = append(err.contextLines, strings.TrimSpace(next))
					i++
				} else {
					break
				}
			}
			errors = append(errors, err)
		} else {
			i++
		}
	}

	if len(errors) == 0 {
		if strings.Contains(output, "Found 0 errors") {
			return "TypeScript: No errors found"
		}
		return "TypeScript compilation completed"
	}

	// Group by file (preserve first-seen order for stable output).
	byFile := map[string][]int{}
	var fileOrder []string
	for idx, err := range errors {
		if _, ok := byFile[err.file]; !ok {
			fileOrder = append(fileOrder, err.file)
		}
		byFile[err.file] = append(byFile[err.file], idx)
	}

	// Count by error code.
	byCode := map[string]int{}
	for _, err := range errors {
		byCode[err.code]++
	}

	var b strings.Builder
	fmt.Fprintf(&b, "TypeScript: %d errors in %d files\n", len(errors), len(byFile))

	// Top error codes (compact, one line) when more than one code present.
	if len(byCode) > 1 {
		type cc struct {
			code  string
			count int
		}
		var counts []cc
		for code, count := range byCode {
			counts = append(counts, cc{code, count})
		}
		sort.Slice(counts, func(a, c int) bool {
			if counts[a].count != counts[c].count {
				return counts[a].count > counts[c].count
			}
			return counts[a].code < counts[c].code
		})
		limit := 5
		if limit > len(counts) {
			limit = len(counts)
		}
		var parts []string
		for _, c := range counts[:limit] {
			parts = append(parts, fmt.Sprintf("%s (%dx)", c.code, c.count))
		}
		fmt.Fprintf(&b, "Top codes: %s\n\n", strings.Join(parts, ", "))
	}

	// Files sorted by error count (most errors first). Stable on equal counts
	// using first-seen order.
	sort.SliceStable(fileOrder, func(a, c int) bool {
		return len(byFile[fileOrder[a]]) > len(byFile[fileOrder[c]])
	})

	for _, file := range fileOrder {
		idxs := byFile[file]
		fmt.Fprintf(&b, "%s (%d errors)\n", file, len(idxs))
		for _, idx := range idxs {
			err := errors[idx]
			fmt.Fprintf(&b, "  L%d: %s %s\n", err.line, err.code, truncate(err.message, 120))
			for _, ctx := range err.contextLines {
				fmt.Fprintf(&b, "    %s\n", truncate(ctx, 120))
			}
		}
		b.WriteByte('\n')
	}

	return strings.TrimSpace(b.String())
}

// --- helpers (ported from rtk core) ----------------------------------------

// lines splits text into lines with Rust's str::lines semantics: it drops a
// single trailing empty element so a final "\n" does not yield a phantom line.
func lines(s string) []string {
	s = core.NormalizeNewlines(s)
	parts := strings.Split(s, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}

// truncate shortens s to maxChars runes with an ellipsis, mirroring rtk's
// utils::truncate.
func truncate(s string, maxChars int) string {
	r := []rune(s)
	if len(r) <= maxChars {
		return s
	}
	if maxChars <= 0 {
		return "..."
	}
	return string(r[:maxChars]) + "..."
}

// truncatePassthrough caps raw passthrough output. gortk has no configurable
// passthrough limit, so it uses CapInventory-scaled char budget via
// SmartTruncate is not appropriate here (it is line-structural); we cap by a
// fixed generous char count matching rtk's default passthrough_max_chars.
func truncatePassthrough(output string) string {
	const maxChars = 2000 // rtk default config limits().passthrough_max_chars
	r := []rune(output)
	if len(r) <= maxChars {
		return output
	}
	truncated := string(r[:maxChars])
	return fmt.Sprintf("%s\n\n[gortk:PASSTHROUGH] Output truncated (%d chars → %d chars)", truncated, len(r), maxChars)
}

// forceTeeTailHint writes content to a tee file under the gortk data dir and
// returns a "[see remaining: …]" hint, or "" when content is empty or the
// write fails. Best-effort, mirroring rtk's tee::force_tee_tail_hint.
func forceTeeTailHint(content, slug string, lineOffset int) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, slug)
	if safe == "" {
		safe = "cmd"
	}
	path := fmt.Sprintf("%s/tee-%s.txt", core.DataDir(), safe)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return ""
	}
	return fmt.Sprintf("[see remaining: tail -n +%d %s]", lineOffset, path)
}

// emitDegradationWarning / emitPassthroughWarning mirror rtk's parser helpers.
func emitDegradationWarning(tool, reason string) {
	fmt.Fprintf(os.Stderr, "[gortk:DEGRADED] %s parser: %s\n", tool, reason)
}

func emitPassthroughWarning(tool, reason string) {
	fmt.Fprintf(os.Stderr, "[gortk:PASSTHROUGH] %s parser: %s\n", tool, reason)
}
