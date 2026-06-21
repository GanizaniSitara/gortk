package cceconomics

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gortk/internal/cmds/cchistory"
	"gortk/internal/core"
)

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestPriceFor(t *testing.T) {
	cases := map[string]ModelPricing{
		"claude-opus-4-8":   {5.0, 25.0},
		"claude-opus-4-7":   {5.0, 25.0},
		"claude-sonnet-4-6": {3.0, 15.0},
		"claude-haiku-4-5":  {1.0, 5.0},
		"claude-fable-5":    {10.0, 50.0},
		"something-unknown": {5.0, 25.0}, // default = Opus
	}
	for model, want := range cases {
		got := priceFor(model)
		if got != want {
			t.Errorf("priceFor(%q) = %+v, want %+v", model, got, want)
		}
	}
}

func TestUsageCost(t *testing.T) {
	// Opus: input $5/MTok, output $25/MTok, cache write 1.25x input, read 0.1x input.
	u := cchistory.Usage{
		Model:             "claude-opus-4-8",
		InputTokens:       1_000_000,
		OutputTokens:      1_000_000,
		CacheCreateTokens: 1_000_000,
		CacheReadTokens:   1_000_000,
	}
	// 5 + 25 + (5*1.25) + (5*0.1) = 5 + 25 + 6.25 + 0.5 = 36.75
	got := usageCost(u)
	if !approx(got, 36.75) {
		t.Errorf("usageCost = %v, want 36.75", got)
	}
}

func TestComputeDerived(t *testing.T) {
	p := Period{
		CCCost:              100.0,
		CCInputTokens:       1000,
		CCOutputTokens:      500,
		CCCacheCreateTokens: 200,
		CCCacheReadTokens:   5000,
		GortkSaved:          10_000,
	}
	p.computeDerived()
	// weighted_units = 1000 + 5*500 + 1.25*200 + 0.1*5000 = 1000+2500+250+500 = 4250
	// cpt = 100/4250
	wantCPT := 100.0 / 4250.0
	if !approx(p.WeightedInputCPT, wantCPT) {
		t.Errorf("CPT = %v, want %v", p.WeightedInputCPT, wantCPT)
	}
	// savings = 10000 * cpt * 5
	wantSavings := 10_000.0 * wantCPT * weightOutput
	if !approx(p.SavingsUSD, wantSavings) {
		t.Errorf("savings = %v, want %v", p.SavingsUSD, wantSavings)
	}
}

func TestComputeDerivedZeroTokens(t *testing.T) {
	p := Period{CCCost: 100, GortkSaved: 5000}
	p.computeDerived()
	if p.WeightedInputCPT != 0 || p.SavingsUSD != 0 {
		t.Errorf("expected zero derived metrics, got cpt=%v savings=%v", p.WeightedInputCPT, p.SavingsUSD)
	}
}

func TestPeriodKey(t *testing.T) {
	ts := time.Date(2026, 1, 30, 14, 0, 0, 0, time.UTC) // Friday
	if got := periodKey(ts, Daily); got != "2026-01-30" {
		t.Errorf("daily = %q", got)
	}
	if got := periodKey(ts, Monthly); got != "2026-01" {
		t.Errorf("monthly = %q", got)
	}
	// 2026-01-30 is a Friday → ISO week Monday = 2026-01-26.
	if got := periodKey(ts, Weekly); got != "2026-01-26" {
		t.Errorf("weekly = %q, want 2026-01-26", got)
	}
	// Sunday rolls back to the previous Monday.
	sun := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if got := periodKey(sun, Weekly); got != "2026-01-26" {
		t.Errorf("sunday weekly = %q, want 2026-01-26", got)
	}
}

func writeUsageSession(t *testing.T, dir string, lines []string) {
	t.Helper()
	proj := filepath.Join(dir, "proj")
	_ = os.MkdirAll(proj, 0o755)
	if err := os.WriteFile(filepath.Join(proj, "s.jsonl"),
		[]byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateMergesSpendAndSavings(t *testing.T) {
	root := t.TempDir()
	writeUsageSession(t, root, []string{
		`{"type":"assistant","timestamp":"2026-01-15T10:00:00Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1000000,"output_tokens":0,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[]}}`,
	})
	tracker := []core.TrackEntry{
		{Timestamp: time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC), RawTokens: 1000, OutTokens: 300},
		{Timestamp: time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC), Passthrough: true},
	}
	rep := Generate(root, tracker, Monthly)
	if len(rep.Periods) != 1 {
		t.Fatalf("want 1 period (2026-01), got %d: %+v", len(rep.Periods), rep.Periods)
	}
	p := rep.Periods[0]
	if p.Label != "2026-01" {
		t.Errorf("label = %q", p.Label)
	}
	// 1M input Opus tokens = $5.00.
	if !approx(p.CCCost, 5.0) {
		t.Errorf("cost = %v, want 5.0", p.CCCost)
	}
	// 1 filtered (saved 700) + 1 passthrough = 2 commands; saved 700.
	if p.GortkCommands != 2 {
		t.Errorf("commands = %d, want 2", p.GortkCommands)
	}
	if p.GortkSaved != 700 {
		t.Errorf("saved = %d, want 700", p.GortkSaved)
	}
}

func TestGenerateTotals(t *testing.T) {
	root := t.TempDir()
	writeUsageSession(t, root, []string{
		`{"type":"assistant","timestamp":"2026-01-15T10:00:00Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":1000000,"output_tokens":0},"content":[]}}`,
		`{"type":"assistant","timestamp":"2026-02-15T10:00:00Z","message":{"model":"claude-opus-4-8","usage":{"input_tokens":2000000,"output_tokens":0},"content":[]}}`,
	})
	rep := Generate(root, nil, Monthly)
	if len(rep.Periods) != 2 {
		t.Fatalf("want 2 periods, got %d", len(rep.Periods))
	}
	// Totals = $5 + $10 = $15.
	if !approx(rep.Totals.CCCost, 15.0) {
		t.Errorf("totals cost = %v, want 15.0", rep.Totals.CCCost)
	}
	if rep.Totals.CCInputTokens != 3_000_000 {
		t.Errorf("totals input = %d", rep.Totals.CCInputTokens)
	}
	// Sorted ascending by label.
	if rep.Periods[0].Label != "2026-01" || rep.Periods[1].Label != "2026-02" {
		t.Errorf("periods not sorted: %s, %s", rep.Periods[0].Label, rep.Periods[1].Label)
	}
}

func TestGenerateDailyAndWeekly(t *testing.T) {
	root := t.TempDir()
	writeUsageSession(t, root, []string{
		`{"type":"assistant","timestamp":"2026-01-30T10:00:00Z","message":{"model":"claude-haiku-4-5","usage":{"input_tokens":1000000,"output_tokens":0},"content":[]}}`,
	})
	daily := Generate(root, nil, Daily)
	if len(daily.Periods) != 1 || daily.Periods[0].Label != "2026-01-30" {
		t.Errorf("daily label = %+v", daily.Periods)
	}
	// Haiku input = $1/MTok.
	if !approx(daily.Periods[0].CCCost, 1.0) {
		t.Errorf("haiku cost = %v, want 1.0", daily.Periods[0].CCCost)
	}
	weekly := Generate(root, nil, Weekly)
	if len(weekly.Periods) != 1 || weekly.Periods[0].Label != "2026-01-26" {
		t.Errorf("weekly label = %+v", weekly.Periods)
	}
}

func TestFormatTextEmpty(t *testing.T) {
	out := FormatText(Report{Granularity: "monthly"})
	if !strings.Contains(out, "No data") {
		t.Errorf("missing empty message: %s", out)
	}
}

func TestFormatTextAndCSV(t *testing.T) {
	rep := Report{
		Granularity: "monthly",
		Periods: []Period{
			{Label: "2026-01", CCCost: 5.0, GortkSaved: 700, GortkCommands: 2,
				CCInputTokens: 1_000_000, SavingsUSD: 1.23},
		},
		Totals: Period{Label: "TOTAL", CCCost: 5.0, GortkSaved: 700, GortkCommands: 2,
			CCInputTokens: 1_000_000, SavingsUSD: 1.23},
	}
	text := FormatText(rep)
	for _, want := range []string{"2026-01", "$5.00", "TOTAL", "input:"} {
		if !strings.Contains(text, want) {
			t.Errorf("text missing %q:\n%s", want, text)
		}
	}
	csv := FormatCSV(rep)
	if !strings.HasPrefix(csv, "period,spent_usd,") {
		t.Errorf("csv header wrong: %s", csv)
	}
	if !strings.Contains(csv, "2026-01,5.0000,1000000") {
		t.Errorf("csv row wrong: %s", csv)
	}
}
