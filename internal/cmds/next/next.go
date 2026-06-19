// Package next is gortk's token-optimized Next.js build wrapper. It runs
// `next build` (falling back to `npx next build`), captures the verbose build
// log, and emits a compact summary of route counts, the largest bundles, and
// the error/warning tally. Faithful port of rtk's src/cmds/js/next_cmd.rs.
package next

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "next",
		Summary: "Next.js build with compact output",
		Run:     Run,
	})
}

// Bundle size pattern: <symbol> <route> <size> <unit> <total> <unit>
var bundlePattern = regexp.MustCompile(`^[○●◐λ✓]\s+([\w/\-.]+)\s+(\d+(?:\.\d+)?)\s*(kB|B)\s+(\d+(?:\.\d+)?)\s*(kB|B)`)

// timeRE matches a duration like "34.2s" or "1250ms".
var timeRE = regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(s|ms)`)

// Run executes the next build command.
func Run(args []string, verbose int) (int, error) {
	// Try `next` directly first, fall back to `npx next` when it isn't on PATH.
	nextExists := core.ToolExists("next")

	var cmd *exec.Cmd
	if nextExists {
		cmd = core.ResolvedCommand("next")
	} else {
		cmd = core.ResolvedCommand("npx", "next")
	}
	cmd.Args = append(cmd.Args, "build")
	cmd.Args = append(cmd.Args, args...)

	if verbose > 0 {
		tool := "next"
		if !nextExists {
			tool = "npx next"
		}
		fmt.Fprintf(os.Stderr, "Running: %s build\n", tool)
	}

	return core.RunFiltered(cmd, "next build", strings.Join(args, " "), filterNextBuild, core.RunOptions{})
}

// bundle holds a parsed bundle row: route name, total first-load size, and the
// percentage increase over the route's own size (nil when not computable).
type bundle struct {
	route    string
	total    float64
	pctSet   bool
	pctValue float64
}

// filterNextBuild compresses Next.js build output to route metrics and the
// largest bundles. Pure function — the behavioural spec lives in next_test.go.
func filterNextBuild(output string) string {
	routesStatic := 0
	routesDynamic := 0
	routesTotal := 0
	var bundles []bundle
	warnings := 0
	errors := 0
	buildTime := ""

	clean := core.StripANSI(output)

	for _, line := range splitLines(clean) {
		// Count route types by symbol.
		switch {
		case strings.HasPrefix(line, "○"):
			routesStatic++
			routesTotal++
		case strings.HasPrefix(line, "●") || strings.HasPrefix(line, "◐"):
			routesDynamic++
			routesTotal++
		case strings.HasPrefix(line, "λ"):
			routesTotal++
		}

		// Extract bundle information (route + size + total size).
		if caps := bundlePattern.FindStringSubmatch(line); caps != nil {
			route := caps[1]
			size := parseFloat(caps[2])
			total := parseFloat(caps[4])

			b := bundle{route: route, total: total}
			if total > 0.0 {
				b.pctSet = true
				b.pctValue = ((total - size) / size) * 100.0
			}
			bundles = append(bundles, b)
		}

		// Count warnings and errors.
		if strings.Contains(strings.ToLower(line), "warning") {
			warnings++
		}
		if strings.Contains(strings.ToLower(line), "error") && !strings.Contains(line, "0 error") {
			errors++
		}

		// Extract build time.
		if strings.Contains(line, "Compiled") || strings.Contains(line, "in") {
			if t, ok := extractTime(line); ok {
				buildTime = t
			}
		}
	}

	// Detect if build was skipped (already built).
	alreadyBuilt := strings.Contains(clean, "already optimized") ||
		strings.Contains(clean, "Cache") ||
		(routesTotal == 0 && strings.Contains(clean, "Ready"))

	var result strings.Builder
	result.WriteString("Next.js Build\n")

	if alreadyBuilt && routesTotal == 0 {
		result.WriteString("Already built (using cache)\n\n")
	} else if routesTotal > 0 {
		result.WriteString(fmt.Sprintf("%d routes (%d static, %d dynamic)\n\n",
			routesTotal, routesStatic, routesDynamic))
	}

	if len(bundles) > 0 {
		result.WriteString("Bundles:\n")

		// Sort by size (descending) and show top MAX_BUNDLES.
		sort.SliceStable(bundles, func(i, j int) bool {
			return bundles[i].total > bundles[j].total
		})

		const maxBundles = core.CapWarnings
		limit := maxBundles
		if limit > len(bundles) {
			limit = len(bundles)
		}
		for _, b := range bundles[:limit] {
			warningMarker := ""
			if b.pctSet && b.pctValue > 10.0 {
				warningMarker = fmt.Sprintf(" [warn] (+%.0f%%)", b.pctValue)
			}
			result.WriteString(fmt.Sprintf("  %-30s %6.0f kB%s\n",
				truncate(b.route, 30), b.total, warningMarker))
		}

		if len(bundles) > maxBundles {
			result.WriteString(fmt.Sprintf("\n  ... +%d more routes\n", len(bundles)-maxBundles))
		}

		result.WriteByte('\n')
	}

	if buildTime != "" {
		result.WriteString(fmt.Sprintf("Time: %s | ", buildTime))
	}

	result.WriteString(fmt.Sprintf("Errors: %d | Warnings: %d\n", errors, warnings))

	return strings.TrimSpace(result.String())
}

// extractTime pulls a duration like "34.2s" or "1250ms" out of a build line.
func extractTime(line string) (string, bool) {
	caps := timeRE.FindStringSubmatch(line)
	if caps == nil {
		return "", false
	}
	return caps[1] + caps[2], true
}

// truncate shortens s to at most maxLen runes, appending "..." when it must cut.
// Mirrors rtk's utils::truncate (rune-counted, "..." for tiny maxLen).
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen < 3 {
		return "..."
	}
	return string(runes[:maxLen-3]) + "..."
}

// splitLines mirrors Rust's str::lines(): split on '\n' and drop a single
// trailing empty element produced by a trailing newline.
func splitLines(s string) []string {
	parts := strings.Split(s, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}

func parseFloat(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0.0
	}
	return v
}
