package pnpm

import (
	"fmt"
	"strings"
	"testing"
)

// --- ported from rtk pnpm_cmd.rs #[cfg(test)] ------------------------------

func TestPnpmListParserJSON(t *testing.T) {
	json := `[
        {
            "name": "my-project",
            "version": "1.0.0",
            "dependencies": {
                "express": {
                    "version": "4.18.2"
                }
            }
        }
    ]`
	res := parseList(json)
	if res.tier != tierFull {
		t.Fatalf("want tier Full, got %v", res.tier)
	}
	if res.state == nil || res.state.totalPackages < 2 {
		t.Errorf("want total_packages >= 2, got %+v", res.state)
	}
}

func TestPnpmOutdatedParserJSON(t *testing.T) {
	json := `{
        "express": {
            "current": "4.18.2",
            "latest": "4.19.0",
            "wanted": "4.18.2"
        }
    }`
	res := parseOutdated(json)
	if res.tier != tierFull {
		t.Fatalf("want tier Full, got %v", res.tier)
	}
	if res.state.outdatedCount != 1 {
		t.Errorf("want outdated_count 1, got %d", res.state.outdatedCount)
	}
	if res.state.dependencies[0].name != "express" {
		t.Errorf("want dependencies[0].name express, got %q", res.state.dependencies[0].name)
	}
}

func makeState(prod, dev []string) *dependencyState {
	var deps []dependency
	for _, name := range prod {
		deps = append(deps, dependency{name: name, currentVersion: "1.0.0", devDependency: false})
	}
	for _, name := range dev {
		deps = append(deps, dependency{name: name, currentVersion: "1.0.0", devDependency: true})
	}
	return &dependencyState{totalPackages: len(deps), outdatedCount: 0, dependencies: deps}
}

func TestFormatListingGroupedSections(t *testing.T) {
	state := makeState([]string{"react", "typescript"}, []string{"eslint", "vitest"})
	out := formatDependencyListing(state, true)
	for _, want := range []string{"[prod]", "[dev]", "react", "eslint"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "(dev)") {
		t.Errorf("per-line (dev) marker should be gone:\n%s", out)
	}
}

func TestFormatListingCapShowsHintWithOffset(t *testing.T) {
	prod := make([]string, 60)
	for i := range prod {
		prod[i] = "pkg"
	}
	state := makeState(prod, []string{"eslint"})
	out := formatDependencyListing(state, true)
	want := fmt.Sprintf("… +%d more", 60-maxListing)
	if !strings.Contains(out, want) {
		t.Errorf("truncation count missing %q in:\n%s", want, out)
	}
}

func TestFormatListingNoCapWhenProdOnly(t *testing.T) {
	prod := make([]string, 60)
	for i := range prod {
		prod[i] = "pkg"
	}
	state := makeState(prod, nil)
	out := formatDependencyListing(state, false)
	if strings.Contains(out, "… +") {
		t.Errorf("should not truncate when cap=false:\n%s", out)
	}
	if strings.Contains(out, "[dev]") {
		t.Errorf("no dev section for prod-only state:\n%s", out)
	}
}

func TestFormatListingNoCapWhenDevOnly(t *testing.T) {
	dev := make([]string, 60)
	for i := range dev {
		dev[i] = "pkg"
	}
	state := makeState(nil, dev)
	out := formatDependencyListing(state, false)
	if strings.Contains(out, "… +") {
		t.Errorf("should not truncate when cap=false:\n%s", out)
	}
	if strings.Contains(out, "[prod]") {
		t.Errorf("no prod section for dev-only state:\n%s", out)
	}
}

func TestExtractListTextTracksDevSection(t *testing.T) {
	input := "dependencies:\nreact@18.0.0\ndevDependencies:\neslint@8.0.0\n"
	state := extractListText(input)
	if state == nil {
		t.Fatal("should parse")
	}
	var react, eslint *dependency
	for i := range state.dependencies {
		switch state.dependencies[i].name {
		case "react":
			react = &state.dependencies[i]
		case "eslint":
			eslint = &state.dependencies[i]
		}
	}
	if react == nil || eslint == nil {
		t.Fatalf("missing deps: %+v", state.dependencies)
	}
	if react.devDependency {
		t.Errorf("react should be prod")
	}
	if !eslint.devDependency {
		t.Errorf("eslint should be dev")
	}
}

// --- ported from rtk parser/formatter.rs #[cfg(test)] (DependencyState) -----

func makeDep(name, version string, latest *string) dependency {
	return dependency{name: name, currentVersion: version, latestVersion: latest}
}

func TestDependencyStatePlainListingShowsPackages(t *testing.T) {
	state := &dependencyState{
		totalPackages: 2,
		outdatedCount: 0,
		dependencies: []dependency{
			makeDep("react", "18.0.0", nil),
			makeDep("typescript", "5.0.0", nil),
		},
	}
	out := formatStateCompact(state)
	if !strings.Contains(out, "react") || !strings.Contains(out, "typescript") {
		t.Errorf("package name missing:\n%s", out)
	}
	if strings.Contains(out, "up-to-date") {
		t.Errorf("false positive: plain listing should not say up-to-date:\n%s", out)
	}
}

func TestDependencyStateAllUpToDate(t *testing.T) {
	state := &dependencyState{totalPackages: 0, outdatedCount: 0, dependencies: nil}
	out := formatStateCompact(state)
	if out != "All packages up-to-date" {
		t.Errorf("want All packages up-to-date, got %q", out)
	}
}

func TestDependencyStateOutdatedCompact(t *testing.T) {
	latest := "4.19.0"
	state := &dependencyState{
		totalPackages: 1,
		outdatedCount: 1,
		dependencies:  []dependency{makeDep("express", "4.18.2", &latest)},
	}
	out := formatStateCompact(state)
	if !strings.Contains(out, "1 outdated packages (of 1)") {
		t.Errorf("missing summary:\n%s", out)
	}
	if !strings.Contains(out, "express: 4.18.2 → 4.19.0") {
		t.Errorf("missing upgrade line:\n%s", out)
	}
}

func TestDependencyStateUltra(t *testing.T) {
	state := &dependencyState{totalPackages: 7, outdatedCount: 3}
	if got := formatStateUltra(state); got != "pkg:7 ^3" {
		t.Errorf("want pkg:7 ^3, got %q", got)
	}
}

// --- ported from rtk tsc_cmd.rs #[cfg(test)] -------------------------------

func TestFilterTscOutput(t *testing.T) {
	output := `
src/server/api/auth.ts(12,5): error TS2322: Type 'string' is not assignable to type 'number'.
src/server/api/auth.ts(15,10): error TS2345: Argument of type 'number' is not assignable to parameter of type 'string'.
src/components/Button.tsx(8,3): error TS2339: Property 'onClick' does not exist on type 'ButtonProps'.
src/components/Button.tsx(10,5): error TS2322: Type 'string' is not assignable to type 'number'.

Found 4 errors in 2 files.
`
	result := filterTscOutput(output)
	for _, want := range []string{"TypeScript: 4 errors in 2 files", "auth.ts (2 errors)", "Button.tsx (2 errors)", "TS2322"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q:\n%s", want, result)
		}
	}
	if strings.Contains(result, "Found 4 errors") {
		t.Errorf("summary line should be replaced:\n%s", result)
	}
}

func TestEveryErrorMessageShown(t *testing.T) {
	output := "" +
		"src/api.ts(10,5): error TS2322: Type 'string' is not assignable to type 'number'.\n" +
		"src/api.ts(20,5): error TS2322: Type 'boolean' is not assignable to type 'string'.\n" +
		"src/api.ts(30,5): error TS2322: Type 'null' is not assignable to type 'object'.\n"
	result := filterTscOutput(output)
	for _, want := range []string{
		"Type 'string' is not assignable to type 'number'",
		"Type 'boolean' is not assignable to type 'string'",
		"Type 'null' is not assignable to type 'object'",
		"L10:", "L20:", "L30:",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q:\n%s", want, result)
		}
	}
}

func TestContinuationLinesPreserved(t *testing.T) {
	output := "" +
		"src/app.tsx(10,3): error TS2322: Type '{ children: Element; }' is not assignable to type 'Props'.\n" +
		"  Property 'children' does not exist on type 'Props'.\n" +
		"src/app.tsx(20,5): error TS2345: Argument of type 'number' is not assignable to parameter of type 'string'.\n"
	result := filterTscOutput(output)
	for _, want := range []string{"Property 'children' does not exist on type 'Props'", "L10:", "L20:"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q:\n%s", want, result)
		}
	}
}

func TestNoFileLimit(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 15; i++ {
		fmt.Fprintf(&sb, "src/file%d.ts(%d,1): error TS2322: Error in file %d.\n", i, i, i)
	}
	result := filterTscOutput(sb.String())
	if !strings.Contains(result, "15 errors in 15 files") {
		t.Errorf("missing summary:\n%s", result)
	}
	for i := 1; i <= 15; i++ {
		if !strings.Contains(result, fmt.Sprintf("file%d.ts", i)) {
			t.Errorf("file%d.ts missing from output:\n%s", i, result)
		}
	}
}

func TestFilterNoErrors(t *testing.T) {
	output := "Found 0 errors. Watching for file changes."
	result := filterTscOutput(output)
	if !strings.Contains(result, "No errors found") {
		t.Errorf("missing No errors found:\n%s", result)
	}
}

// --- install filter (rtk filter_pnpm_install behaviour) --------------------

func TestFilterPnpmInstallStripsProgressEmptyOk(t *testing.T) {
	output := "Progress: resolved 100, reused 80\n│ resolving │\n50% done\n"
	if got := filterPnpmInstall(output); got != "ok" {
		t.Errorf("want ok, got %q", got)
	}
}

func TestFilterPnpmInstallKeepsSummary(t *testing.T) {
	output := "Progress: resolved 10\n+ express 4.18.2\nPackages: +3\ndependencies installed\n"
	got := filterPnpmInstall(output)
	if !strings.Contains(got, "+ express 4.18.2") {
		t.Errorf("missing added package:\n%s", got)
	}
	if !strings.Contains(got, "dependencies installed") {
		t.Errorf("missing dependencies summary:\n%s", got)
	}
}

// --- arg parsing helpers ---------------------------------------------------

func TestSplitFilters(t *testing.T) {
	filters, rest := splitFilters([]string{"--filter", "@app1", "-Fapp2", "--filter=app3", "list", "--depth=2"})
	wantFilters := []string{"@app1", "app2", "app3"}
	if strings.Join(filters, ",") != strings.Join(wantFilters, ",") {
		t.Errorf("filters = %v, want %v", filters, wantFilters)
	}
	if strings.Join(rest, " ") != "list --depth=2" {
		t.Errorf("rest = %v", rest)
	}
}

func TestMergeFilters(t *testing.T) {
	got := mergeFilters([]string{"@app1", "@app2"}, []string{"--prod"})
	want := []string{"--filter=@app1", "--filter=@app2", "--prod"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("mergeFilters = %v, want %v", got, want)
	}
}

func TestParseDepth(t *testing.T) {
	d, rest := parseDepth([]string{"--depth", "3", "--prod"})
	if d != 3 || strings.Join(rest, " ") != "--prod" {
		t.Errorf("got depth=%d rest=%v", d, rest)
	}
	d, _ = parseDepth([]string{"--depth=5"})
	if d != 5 {
		t.Errorf("got depth=%d, want 5", d)
	}
	d, _ = parseDepth([]string{"--prod"})
	if d != 0 {
		t.Errorf("default depth should be 0, got %d", d)
	}
}
