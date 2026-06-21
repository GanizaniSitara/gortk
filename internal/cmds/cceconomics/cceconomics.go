// Package cceconomics reports Claude Code spending vs gortk savings: it derives
// per-period token usage and cost from the Claude Code session transcripts
// (input/output/cache tokens in each assistant turn's usage block, priced with a
// per-model rate table), then merges that with gortk's own token-savings tracker
// to show what gortk's output compression is worth against actual spend.
//
// This is the pragmatic, offline port of rtk's `rtk cc-economics`
// (src/analytics/cc_economics.rs). rtk shelled out to the `ccusage` npm package
// for spend; gortk may not spawn external tools (porting contract), so it reads
// the same numbers straight from the transcripts the model already wrote.
//
//	gortk cc-economics [--daily | --weekly | --monthly | --all] [--format text|json|csv]
//
// Offline: reads only <UserHomeDir>/.claude/projects and <DataDir>/tracking.jsonl.
package cceconomics

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gortk/internal/cmds/cchistory"
	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "cc-economics",
		Summary: "Claude Code spending (from history) vs gortk savings (from tracker)",
		Run:     Run,
	})
}

// Granularity is the period bucket for the report.
type Granularity int

const (
	// Monthly is the default summary granularity.
	Monthly Granularity = iota
	Daily
	Weekly
)

// ModelPricing is USD per 1,000,000 tokens. Cache write is 1.25x input and cache
// read is 0.1x input (the standard Claude cache multipliers), derived from the
// per-model input price rather than stored separately.
type ModelPricing struct {
	InputPerM  float64
	OutputPerM float64
}

// pricing maps a model-id prefix to its rate. Matched by HasPrefix so dated or
// suffixed ids (claude-opus-4-7, claude-opus-4-8-fast, ...) resolve to the right
// family. Rates are USD/MTok (input/output): Opus 5/25, Sonnet 3/15, Haiku 1/5,
// Fable 10/50. Unknown models fall back to the Opus rate (conservative).
var pricing = []struct {
	prefix string
	price  ModelPricing
}{
	{"claude-fable", ModelPricing{10.0, 50.0}},
	{"claude-mythos", ModelPricing{10.0, 50.0}},
	{"claude-opus", ModelPricing{5.0, 25.0}},
	{"claude-sonnet", ModelPricing{3.0, 15.0}},
	{"claude-haiku", ModelPricing{1.0, 5.0}},
}

const (
	cacheWriteMultiplier = 1.25
	cacheReadMultiplier  = 0.10
	defaultInputPerM     = 5.0
	defaultOutputPerM    = 25.0
)

// priceFor returns the rate for a model id, defaulting to the Opus rate.
func priceFor(model string) ModelPricing {
	for _, p := range pricing {
		if strings.HasPrefix(model, p.prefix) {
			return p.price
		}
	}
	return ModelPricing{defaultInputPerM, defaultOutputPerM}
}

// usageCost computes the USD cost of one usage record under its model's rates,
// applying the cache write/read multipliers to the input rate.
func usageCost(u cchistory.Usage) float64 {
	p := priceFor(u.Model)
	inputRate := p.InputPerM / 1_000_000.0
	outputRate := p.OutputPerM / 1_000_000.0
	cost := float64(u.InputTokens) * inputRate
	cost += float64(u.OutputTokens) * outputRate
	cost += float64(u.CacheCreateTokens) * inputRate * cacheWriteMultiplier
	cost += float64(u.CacheReadTokens) * inputRate * cacheReadMultiplier
	return cost
}

// ---------------------------------------------------------------------------
// Period aggregation
// ---------------------------------------------------------------------------

// Period is one period's merged spend (from history) + savings (from tracker).
type Period struct {
	Label string `json:"period"`
	// Claude Code spend, derived from transcript usage blocks.
	CCCost              float64 `json:"cc_cost"`
	CCInputTokens       int     `json:"cc_input_tokens"`
	CCOutputTokens      int     `json:"cc_output_tokens"`
	CCCacheCreateTokens int     `json:"cc_cache_create_tokens"`
	CCCacheReadTokens   int     `json:"cc_cache_read_tokens"`
	// gortk savings, from the tracker.
	GortkCommands int `json:"gortk_commands"`
	GortkSaved    int `json:"gortk_saved_tokens"`
	// Derived: weighted input cost-per-token and the USD value of saved tokens.
	WeightedInputCPT float64 `json:"weighted_input_cpt"`
	SavingsUSD       float64 `json:"savings_usd"`
}

// API price weights for the weighted-input-CPT derivation (output is 5x input,
// cache write 1.25x, cache read 0.1x), mirroring rtk's cc_economics weights.
const (
	weightOutput      = 5.0
	weightCacheCreate = 1.25
	weightCacheRead   = 0.10
)

// computeDerived fills WeightedInputCPT and SavingsUSD. The weighted input CPT
// expresses total spend as a price per equivalent input token, so multiplying it
// by gortk's saved (output) tokens estimates the dollar value of the compression.
func (p *Period) computeDerived() {
	weightedUnits := float64(p.CCInputTokens) +
		weightOutput*float64(p.CCOutputTokens) +
		weightCacheCreate*float64(p.CCCacheCreateTokens) +
		weightCacheRead*float64(p.CCCacheReadTokens)
	if weightedUnits > 0 {
		p.WeightedInputCPT = p.CCCost / weightedUnits
		// gortk saves OUTPUT tokens (compressed tool output never entering the
		// context as input next turn); value them at the output rate via the
		// output weight, consistent with rtk's savings_weighted intent.
		p.SavingsUSD = float64(p.GortkSaved) * p.WeightedInputCPT * weightOutput
	}
}

// periodKey buckets a timestamp into its label under the chosen granularity.
func periodKey(ts time.Time, g Granularity) string {
	if ts.IsZero() {
		return "unknown"
	}
	switch g {
	case Daily:
		return ts.Format("2006-01-02")
	case Weekly:
		// ISO week start (Monday).
		wd := int(ts.Weekday())
		if wd == 0 {
			wd = 7 // Sunday -> 7
		}
		monday := ts.AddDate(0, 0, -(wd - 1))
		return monday.Format("2006-01-02")
	default: // Monthly
		return ts.Format("2006-01")
	}
}

// Report bundles the periods and the grand totals.
type Report struct {
	Granularity string   `json:"granularity"`
	Periods     []Period `json:"periods"`
	Totals      Period   `json:"totals"`
}

// Generate builds a cc-economics report from the projects directory (Claude Code
// usage) and the tracker entries (gortk savings). trackerEntries is passed in so
// tests can supply fixtures; in production it is read from <DataDir>/tracking.jsonl.
func Generate(projectsDir string, trackerEntries []core.TrackEntry, g Granularity) Report {
	periods := map[string]*Period{}
	getPeriod := func(label string) *Period {
		p := periods[label]
		if p == nil {
			p = &Period{Label: label}
			periods[label] = p
		}
		return p
	}

	// Claude Code spend: every assistant usage block, bucketed by timestamp.
	sessions := cchistory.DiscoverSessions(projectsDir, "", 0)
	for _, path := range sessions {
		usages, err := cchistory.ExtractUsage(path)
		if err != nil {
			continue
		}
		for _, u := range usages {
			label := periodKey(u.Timestamp, g)
			p := getPeriod(label)
			p.CCCost += usageCost(u)
			p.CCInputTokens += u.InputTokens
			p.CCOutputTokens += u.OutputTokens
			p.CCCacheCreateTokens += u.CacheCreateTokens
			p.CCCacheReadTokens += u.CacheReadTokens
		}
	}

	// gortk savings: tracker entries bucketed by timestamp.
	for _, e := range trackerEntries {
		if e.Passthrough {
			// Passthrough still counts as a command, but carries no savings.
			label := periodKey(e.Timestamp, g)
			getPeriod(label).GortkCommands++
			continue
		}
		label := periodKey(e.Timestamp, g)
		p := getPeriod(label)
		p.GortkCommands++
		saved := e.RawTokens - e.OutTokens
		if saved > 0 {
			p.GortkSaved += saved
		}
	}

	out := make([]Period, 0, len(periods))
	var totals Period
	totals.Label = "TOTAL"
	for _, p := range periods {
		p.computeDerived()
		out = append(out, *p)
		totals.CCCost += p.CCCost
		totals.CCInputTokens += p.CCInputTokens
		totals.CCOutputTokens += p.CCOutputTokens
		totals.CCCacheCreateTokens += p.CCCacheCreateTokens
		totals.CCCacheReadTokens += p.CCCacheReadTokens
		totals.GortkCommands += p.GortkCommands
		totals.GortkSaved += p.GortkSaved
	}
	totals.computeDerived()
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })

	return Report{Granularity: granularityName(g), Periods: out, Totals: totals}
}

func granularityName(g Granularity) string {
	switch g {
	case Daily:
		return "daily"
	case Weekly:
		return "weekly"
	default:
		return "monthly"
	}
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------

// FormatText renders the period table plus a totals summary.
func FormatText(r Report) string {
	var b strings.Builder
	b.WriteString("gortk cc-economics -- Claude Code spend vs gortk savings\n")
	b.WriteString(strings.Repeat("=", 60) + "\n")

	if len(r.Periods) == 0 {
		b.WriteString("\nNo data. Use Claude Code (for spend) and run gortk-wrapped commands (for savings).\n")
		return b.String()
	}

	fmt.Fprintf(&b, "\n%-10s %10s %10s %10s %10s\n",
		"Period", "Spent", "Saved tok", "Savings", "gortk cmds")
	b.WriteString(strings.Repeat("-", 56) + "\n")
	for _, p := range r.Periods {
		fmt.Fprintf(&b, "%-10s %10s %10s %10s %10d\n",
			p.Label, formatUSD(p.CCCost), core.FormatCount(p.GortkSaved),
			formatUSD(p.SavingsUSD), p.GortkCommands)
	}
	b.WriteString(strings.Repeat("-", 56) + "\n")
	t := r.Totals
	fmt.Fprintf(&b, "%-10s %10s %10s %10s %10d\n",
		"TOTAL", formatUSD(t.CCCost), core.FormatCount(t.GortkSaved),
		formatUSD(t.SavingsUSD), t.GortkCommands)

	b.WriteString("\nToken breakdown (all periods):\n")
	fmt.Fprintf(&b, "  input:        %s\n", core.FormatCount(t.CCInputTokens))
	fmt.Fprintf(&b, "  output:       %s\n", core.FormatCount(t.CCOutputTokens))
	fmt.Fprintf(&b, "  cache writes: %s\n", core.FormatCount(t.CCCacheCreateTokens))
	fmt.Fprintf(&b, "  cache reads:  %s\n", core.FormatCount(t.CCCacheReadTokens))
	b.WriteString("\nSpend derived from Claude Code transcript usage blocks; savings from the gortk tracker.\n")
	b.WriteString("Estimates only (chars/4 token heuristic; standard public per-model rates).\n")
	return b.String()
}

// FormatCSV renders the periods as CSV.
func FormatCSV(r Report) string {
	var b strings.Builder
	b.WriteString("period,spent_usd,input_tokens,output_tokens,cache_create,cache_read,gortk_saved_tokens,savings_usd,gortk_commands\n")
	for _, p := range r.Periods {
		fmt.Fprintf(&b, "%s,%.4f,%d,%d,%d,%d,%d,%.4f,%d\n",
			p.Label, p.CCCost, p.CCInputTokens, p.CCOutputTokens,
			p.CCCacheCreateTokens, p.CCCacheReadTokens, p.GortkSaved, p.SavingsUSD, p.GortkCommands)
	}
	return b.String()
}

func formatUSD(v float64) string {
	return fmt.Sprintf("$%.2f", v)
}

// ---------------------------------------------------------------------------
// CLI
// ---------------------------------------------------------------------------

// readTracking parses <DataDir>/tracking.jsonl into TrackEntry values, mirroring
// the meta package's reader. Missing file -> nil; malformed lines skipped.
func readTracking() []core.TrackEntry {
	path := filepath.Join(core.DataDir(), "tracking.jsonl")
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var entries []core.TrackEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e core.TrackEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		entries = append(entries, e)
	}
	return entries
}

// Run implements `gortk cc-economics`.
func Run(args []string, verbose int) (int, error) {
	g := Monthly
	format := "text"
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--daily" || a == "-d":
			g = Daily
		case a == "--weekly" || a == "-w":
			g = Weekly
		case a == "--monthly" || a == "-m":
			g = Monthly
		case a == "--all":
			// rtk's --all shows all breakdowns; gortk reports the finest (daily)
			// so every period is visible in one table.
			g = Daily
		case a == "--format" || a == "-f":
			if i+1 < len(args) {
				format = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--format="):
			format = strings.TrimPrefix(a, "--format=")
		}
	}

	projectsDir, ok := cchistory.ProjectsDir()
	if !ok {
		return 1, fmt.Errorf("gortk cc-economics: could not determine home directory")
	}
	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "scanning %s (%s)\n", projectsDir, granularityName(g))
	}

	rep := Generate(projectsDir, readTracking(), g)

	switch format {
	case "json":
		out, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return 1, fmt.Errorf("gortk cc-economics: %w", err)
		}
		fmt.Println(string(out))
	case "csv":
		fmt.Print(FormatCSV(rep))
	default:
		fmt.Print(FormatText(rep))
	}
	return 0, nil
}
