package pip

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"gortk/internal/core"
)

// pkg is one entry from `pip list` / `pip list --outdated` JSON output.
// latest_version is present only for the --outdated form; serde's #[serde(default)]
// in rtk maps to Go's natural zero value (empty string) when the field is absent.
type pkg struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	LatestVersion string `json:"latest_version"`
}

// filterPipList compresses `pip list --format=json` output: it groups packages
// by their lowercased first letter for easy scanning and keeps the full version
// per entry. `pip list` is an inventory query (dependency audits need every
// package visible), so the per-letter cap is just a safety bound for
// pathological environments. Faithful port of rtk's filter_pip_list.
func filterPipList(output string) string {
	var packages []pkg
	if err := json.Unmarshal([]byte(output), &packages); err != nil {
		return fmt.Sprintf("pip list (JSON parse failed: %s)", err)
	}

	if len(packages) == 0 {
		return "pip list: No packages installed"
	}

	var result strings.Builder
	fmt.Fprintf(&result, "pip list: %d packages\n", len(packages))

	// Group by first letter for easier scanning, preserving input order within
	// each group (Rust pushes in iteration order).
	byLetter := map[rune][]pkg{}
	for _, p := range packages {
		first := '?'
		for _, r := range p.Name {
			first = r
			break
		}
		first = toLowerASCII(first)
		byLetter[first] = append(byLetter[first], p)
	}

	letters := make([]rune, 0, len(byLetter))
	for r := range byLetter {
		letters = append(letters, r)
	}
	sort.Slice(letters, func(i, j int) bool { return letters[i] < letters[j] })

	const maxPerLetter = core.CapInventory
	for _, letter := range letters {
		pkgs := byLetter[letter]
		fmt.Fprintf(&result, "\n[%s]\n", strings.ToUpper(string(letter)))

		limit := len(pkgs)
		if limit > maxPerLetter {
			limit = maxPerLetter
		}
		for _, p := range pkgs[:limit] {
			fmt.Fprintf(&result, "  %s (%s)\n", p.Name, p.Version)
		}
		if len(pkgs) > maxPerLetter {
			fmt.Fprintf(&result, "  ... +%d more\n", len(pkgs)-maxPerLetter)
		}
	}

	return strings.TrimSpace(result.String())
}

// filterPipOutdated compresses `pip list --outdated --format=json` output into a
// numbered current→latest upgrade list. Faithful port of rtk's
// filter_pip_outdated.
func filterPipOutdated(output string) string {
	var packages []pkg
	if err := json.Unmarshal([]byte(output), &packages); err != nil {
		return fmt.Sprintf("pip outdated (JSON parse failed: %s)", err)
	}

	if len(packages) == 0 {
		return "pip outdated: All packages up to date"
	}

	var result strings.Builder
	fmt.Fprintf(&result, "pip outdated: %d packages\n", len(packages))

	const maxPipPackages = core.CapList
	limit := len(packages)
	if limit > maxPipPackages {
		limit = maxPipPackages
	}
	for i, p := range packages[:limit] {
		latest := p.LatestVersion
		if latest == "" {
			latest = "unknown"
		}
		fmt.Fprintf(&result, "%d. %s (%s → %s)\n", i+1, p.Name, p.Version, latest)
	}

	if len(packages) > maxPipPackages {
		fmt.Fprintf(&result, "\n... +%d more packages\n", len(packages)-maxPipPackages)
	}

	result.WriteString("\n[hint] Run `pip install --upgrade <package>` to update\n")

	return strings.TrimSpace(result.String())
}

// toLowerASCII lowercases an ASCII letter, leaving other runes unchanged. Mirrors
// Rust's char::to_ascii_lowercase used for the grouping key.
func toLowerASCII(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + ('a' - 'A')
	}
	return r
}
