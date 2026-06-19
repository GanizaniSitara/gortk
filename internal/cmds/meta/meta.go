// Package meta provides gortk's operational commands: "config", "gain", and
// "verify". These are the offline, JSON-lines counterparts to rtk's analytics
// commands (rtk uses a bundled SQLite database; gortk reads the simple
// <DataDir>/tracking.jsonl token-tracking file the core writes).
//
//   - config [--create] : print the current config + its path, or write a
//     default config.toml to core.ConfigPath().
//   - gain   [--json]   : aggregate the token-tracking log and print a compact
//     savings summary (total commands, raw vs out tokens, tokens saved, %).
//   - verify [--filter NAME] : run every builtin TOML filter's inline tests and
//     print a pass/fail summary; exit non-zero if any test fails.
package meta

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
	"gortk/internal/tomlfilter"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "config",
		Summary: "Show or create the gortk configuration file",
		Run:     RunConfig,
	})
	registry.Register(&registry.Cmd{
		Name:    "gain",
		Summary: "Show token-savings summary from the gortk tracking log",
		Run:     RunGain,
	})
	registry.Register(&registry.Cmd{
		Name:    "verify",
		Summary: "Run every builtin filter's inline tests and report pass/fail",
		Run:     RunVerify,
	})
}

// ---------------------------------------------------------------------------
// config
// ---------------------------------------------------------------------------

// defaultConfigTOML is the default gortk config written by `config --create`.
// We emit it as a literal (rather than importing a TOML encoder into this
// package) to honour the porting contract's stdlib-only import rule. It mirrors
// the zero-value core.Config: an empty hooks.exclude_commands list.
const defaultConfigTOML = `# gortk configuration
# gortk is offline by default: no telemetry, no network, nothing to consent to.

[hooks]
# Command names the rewrite engine should leave untouched (passed through to
# the native tool unchanged). Example: exclude_commands = ["curl", "gh"]
exclude_commands = []
`

// RunConfig implements `config`. With --create it writes the default config to
// core.ConfigPath(); otherwise it prints the current config and its path.
func RunConfig(args []string, verbose int) (int, error) {
	create := false
	for _, a := range args {
		if a == "--create" {
			create = true
		}
	}

	path := core.ConfigPath()

	if create {
		if err := os.WriteFile(path, []byte(defaultConfigTOML), 0o644); err != nil {
			return 1, fmt.Errorf("gortk config: failed to write %s: %w", path, err)
		}
		fmt.Printf("Created: %s\n", path)
		return 0, nil
	}

	fmt.Printf("Config: %s\n\n", path)
	if _, err := os.Stat(path); err != nil {
		fmt.Println("(default config, file not created)")
		fmt.Println()
		fmt.Print(defaultConfigTOML)
		return 0, nil
	}
	cfg := core.LoadConfig()
	fmt.Print(renderConfig(cfg))
	return 0, nil
}

// renderConfig formats a loaded Config back into the readable TOML-ish form
// shown by `config` (stdlib-only; no TOML encoder dependency).
func renderConfig(cfg core.Config) string {
	var b strings.Builder
	b.WriteString("[hooks]\n")
	if len(cfg.Hooks.ExcludeCommands) == 0 {
		b.WriteString("exclude_commands = []\n")
	} else {
		quoted := make([]string, len(cfg.Hooks.ExcludeCommands))
		for i, c := range cfg.Hooks.ExcludeCommands {
			quoted[i] = fmt.Sprintf("%q", c)
		}
		fmt.Fprintf(&b, "exclude_commands = [%s]\n", strings.Join(quoted, ", "))
	}
	return b.String()
}

// ---------------------------------------------------------------------------
// gain
// ---------------------------------------------------------------------------

// GainStats is the aggregate of the token-tracking log.
type GainStats struct {
	TotalCommands       int     `json:"total_commands"`
	PassthroughCommands int     `json:"passthrough_commands"`
	FilteredCommands    int     `json:"filtered_commands"`
	RawTokens           int     `json:"raw_tokens"`
	OutTokens           int     `json:"out_tokens"`
	TokensSaved         int     `json:"tokens_saved"`
	PercentReduction    float64 `json:"percent_reduction"`
}

// Aggregate computes GainStats from a slice of tracker entries. Passthrough
// entries contribute to the command count but carry no token figures (the core
// records zero tokens for them), so they are excluded from the savings math.
func Aggregate(entries []core.TrackEntry) GainStats {
	var s GainStats
	s.TotalCommands = len(entries)
	for _, e := range entries {
		if e.Passthrough {
			s.PassthroughCommands++
			continue
		}
		s.FilteredCommands++
		s.RawTokens += e.RawTokens
		s.OutTokens += e.OutTokens
	}
	s.TokensSaved = s.RawTokens - s.OutTokens
	if s.RawTokens > 0 {
		s.PercentReduction = float64(s.TokensSaved) / float64(s.RawTokens) * 100.0
	}
	return s
}

// RunGain implements `gain`. It reads the tracking log and prints a summary.
func RunGain(args []string, verbose int) (int, error) {
	asJSON := false
	for _, a := range args {
		if a == "--json" {
			asJSON = true
		}
	}

	entries, err := readTracking()
	if err != nil {
		return 1, fmt.Errorf("gortk gain: %w", err)
	}

	if len(entries) == 0 {
		if asJSON {
			out, _ := json.Marshal(Aggregate(nil))
			fmt.Println(string(out))
			return 0, nil
		}
		fmt.Println("gortk gain: no data yet — run some wrapped commands first.")
		return 0, nil
	}

	stats := Aggregate(entries)

	if asJSON {
		out, err := json.MarshalIndent(stats, "", "  ")
		if err != nil {
			return 1, fmt.Errorf("gortk gain: %w", err)
		}
		fmt.Println(string(out))
		return 0, nil
	}

	fmt.Print(formatGain(stats))
	return 0, nil
}

// formatGain renders a compact human summary of the savings.
func formatGain(s GainStats) string {
	var b strings.Builder
	b.WriteString("gortk token savings\n")
	fmt.Fprintf(&b, "  commands:      %s (%s filtered, %s passthrough)\n",
		core.FormatCount(s.TotalCommands),
		core.FormatCount(s.FilteredCommands),
		core.FormatCount(s.PassthroughCommands))
	fmt.Fprintf(&b, "  raw tokens:    %s\n", core.FormatCount(s.RawTokens))
	fmt.Fprintf(&b, "  out tokens:    %s\n", core.FormatCount(s.OutTokens))
	fmt.Fprintf(&b, "  tokens saved:  %s\n", core.FormatCount(s.TokensSaved))
	fmt.Fprintf(&b, "  reduction:     %.1f%%\n", s.PercentReduction)
	return b.String()
}

// trackingPath returns the path to the JSON-lines token-tracking file the core
// writes (<DataDir>/tracking.jsonl). The core's own trackingPath is unexported,
// so we reconstruct the same path here from the exported core.DataDir().
func trackingPath() string {
	return filepath.Join(core.DataDir(), "tracking.jsonl")
}

// readTracking parses <DataDir>/tracking.jsonl into TrackEntry values. A missing
// file is not an error (returns nil). Malformed lines are skipped so a single
// bad record never poisons the whole report.
func readTracking() ([]core.TrackEntry, error) {
	return parseTrackingFile(trackingPath())
}

// parseTrackingFile parses a JSON-lines tracking file at path. Factored out of
// readTracking so tests can point it at a t.TempDir fixture. A missing file is
// not an error (returns nil).
func parseTrackingFile(path string) ([]core.TrackEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var entries []core.TrackEntry
	sc := bufio.NewScanner(f)
	// Allow generously long lines (large captured outputs produce big labels).
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e core.TrackEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // skip malformed line
		}
		entries = append(entries, e)
	}
	if err := sc.Err(); err != nil {
		return entries, err
	}
	return entries, nil
}

// ---------------------------------------------------------------------------
// verify
// ---------------------------------------------------------------------------

// FilterResult is the verification outcome for a single filter.
type FilterResult struct {
	Name    string
	Total   int
	Passed  int
	Failed  int
	Missing bool // had no inline tests
}

// VerifySummary aggregates per-filter results.
type VerifySummary struct {
	Filters    []FilterResult
	TotalTests int
	TotalPass  int
	TotalFail  int
}

// VerifyFilters runs the inline tests for every compiled filter, or just the one
// named by onlyName when non-empty. It returns a summary; a filter named but not
// found yields an empty summary with found=false.
func VerifyFilters(onlyName string) (VerifySummary, bool) {
	var sum VerifySummary
	found := onlyName == ""
	for _, cf := range tomlfilter.All() {
		if onlyName != "" && cf.Name != onlyName {
			continue
		}
		found = true
		res := FilterResult{Name: cf.Name}
		tests := cf.Tests()
		res.Total = len(tests)
		if len(tests) == 0 {
			res.Missing = true
		}
		for _, td := range tests {
			got := cf.Apply(td.Input)
			// CompiledFilter.Apply mirrors Rust's str::lines() and drops a
			// trailing newline; builtin fixtures' `expected` may carry one from
			// the TOML multi-line string. Compare ignoring a trailing newline.
			if strings.TrimRight(got, "\n") == strings.TrimRight(td.Expected, "\n") {
				res.Passed++
			} else {
				res.Failed++
			}
		}
		sum.Filters = append(sum.Filters, res)
		sum.TotalTests += res.Total
		sum.TotalPass += res.Passed
		sum.TotalFail += res.Failed
	}
	sort.Slice(sum.Filters, func(i, j int) bool { return sum.Filters[i].Name < sum.Filters[j].Name })
	return sum, found
}

// RunVerify implements `verify`. It runs builtin filter inline tests and prints
// a pass/fail summary, exiting non-zero if any test failed.
func RunVerify(args []string, verbose int) (int, error) {
	onlyName := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--filter":
			if i+1 < len(args) {
				onlyName = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--filter="):
			onlyName = strings.TrimPrefix(a, "--filter=")
		}
	}

	sum, found := VerifyFilters(onlyName)
	if !found {
		fmt.Fprintf(os.Stderr, "gortk verify: no builtin filter named %q\n", onlyName)
		return 1, nil
	}

	// Per-filter detail: show failures always; show passes only when verbose.
	for _, r := range sum.Filters {
		switch {
		case r.Failed > 0:
			fmt.Printf("FAIL  %s  (%d/%d passed)\n", r.Name, r.Passed, r.Total)
		case r.Missing:
			if verbose > 0 {
				fmt.Printf("----  %s  (no inline tests)\n", r.Name)
			}
		default:
			if verbose > 0 {
				fmt.Printf("ok    %s  (%d/%d passed)\n", r.Name, r.Passed, r.Total)
			}
		}
	}

	fmt.Printf("\ngortk verify: %d filters, %d tests, %d passed, %d failed\n",
		len(sum.Filters), sum.TotalTests, sum.TotalPass, sum.TotalFail)

	if sum.TotalFail > 0 {
		return 1, nil
	}
	return 0, nil
}
