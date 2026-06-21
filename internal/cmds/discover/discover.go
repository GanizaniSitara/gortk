// Package discover scans Claude Code session history for shell commands that
// gortk could have optimized — a command whose tool is a registered gortk
// command, or one a builtin TOML filter matches — but that were run raw. It
// reports the missed token savings. Pragmatic port of rtk's `rtk discover`
// (src/discover/), with rtk's 3000-line static RULES table replaced by gortk's
// own command registry + tomlfilter engine via the shared cchistory classifier.
//
//	gortk discover [--project P] [--all] [--since N] [--limit N] [--format text|json]
//
// Defaults: current project only (cwd encoded to its Claude project slug),
// last 30 days, top 15 rows per section, text output. Offline: it only reads
// files under <UserHomeDir>/.claude/projects.
package discover

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"gortk/internal/cmds/cchistory"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "discover",
		Summary: "Scan Claude Code history for commands gortk could have optimized",
		Run:     Run,
	})
}

// Options controls a discover scan.
type Options struct {
	Project   string
	All       bool
	SinceDays int
	Limit     int
	Format    string
}

// SupportedEntry is one bucket of raw commands gortk could have handled.
type SupportedEntry struct {
	Command     string  `json:"command"`
	Count       int     `json:"count"`
	Via         string  `json:"via"` // "command" or "filter"
	SavedTokens int     `json:"saved_tokens"`
	SavingsPct  float64 `json:"savings_pct"`
}

// UnsupportedEntry is one bucket of raw commands gortk has no filter for.
type UnsupportedEntry struct {
	BaseCommand string `json:"base_command"`
	Count       int    `json:"count"`
	Example     string `json:"example"`
}

// Report is the full discover result.
type Report struct {
	SessionsScanned int                `json:"sessions_scanned"`
	TotalCommands   int                `json:"total_commands"`
	AlreadyGortk    int                `json:"already_gortk"`
	SinceDays       int                `json:"since_days"`
	ParseErrors     int                `json:"parse_errors"`
	Supported       []SupportedEntry   `json:"supported"`
	Unsupported     []UnsupportedEntry `json:"unsupported"`
}

// estimatedSavingsPct is the fraction of a command's raw output that gortk's
// filtering is assumed to strip. rtk used per-rule pcts; gortk has no per-tool
// table here, so it uses a single conservative default for command-handled and a
// lower one for filter-handled tools (filters are lighter touch than dedicated
// commands).
const (
	commandSavingsPct = 0.70
	filterSavingsPct  = 0.50
)

// fallbackOutputTokens estimates output tokens when a command has no matched
// tool_result (rtk fell back to a category average; gortk uses one flat figure
// since it has no per-category table).
const fallbackOutputTokens = 150

type supportedBucket struct {
	via         string
	count       int
	savedTokens int
	rawTokens   int
	displays    map[string]int
}

type unsupportedBucket struct {
	count   int
	example string
}

// Generate runs a discover scan against the given projects directory.
func Generate(projectsDir string, opts Options) Report {
	rep := Report{SinceDays: opts.SinceDays}

	filter := ""
	if !opts.All {
		filter = opts.Project
	}
	sessions := cchistory.DiscoverSessions(projectsDir, filter, opts.SinceDays)
	rep.SessionsScanned = len(sessions)

	supported := map[string]*supportedBucket{}
	unsupported := map[string]*unsupportedBucket{}

	for _, path := range sessions {
		cmds, err := cchistory.ExtractCommands(path)
		if err != nil {
			rep.ParseErrors++
			continue
		}
		for _, ext := range cmds {
			for _, part := range cchistory.SplitChain(ext.Command) {
				class := cchistory.Classify(part)
				switch class {
				case cchistory.AlreadyGortk:
					rep.TotalCommands++
					rep.AlreadyGortk++
				case cchistory.SupportedByCommand, cchistory.SupportedByFilter:
					rep.TotalCommands++
					addSupported(supported, part, ext, class)
				case cchistory.Ignored:
					// counted neither for/against; skip entirely
				default: // Unsupported
					rep.TotalCommands++
					base := cchistory.BaseCommand(part)
					if base == "" {
						continue
					}
					b := unsupported[base]
					if b == nil {
						b = &unsupportedBucket{example: strings.TrimSpace(part)}
						unsupported[base] = b
					}
					b.count++
				}
			}
		}
	}

	rep.Supported = finalizeSupported(supported)
	rep.Unsupported = finalizeUnsupported(unsupported)
	return rep
}

func addSupported(buckets map[string]*supportedBucket, part string, ext cchistory.ExtractedCommand, class cchistory.Classification) {
	via := "command"
	pct := commandSavingsPct
	if class == cchistory.SupportedByFilter {
		via = "filter"
		pct = filterSavingsPct
	}
	// Bucket key = the gortk-relevant base command (e.g. "git status").
	key := cchistory.BaseCommand(part)
	if key == "" {
		key = strings.TrimSpace(part)
	}
	b := buckets[key]
	if b == nil {
		b = &supportedBucket{via: via, displays: map[string]int{}}
		buckets[key] = b
	}
	b.count++

	rawTokens := fallbackOutputTokens
	if ext.OutputLen >= 0 {
		// chars/4 heuristic, same as core.EstimateTokens for a string of this length.
		rawTokens = (ext.OutputLen + 3) / 4
	}
	b.rawTokens += rawTokens
	b.savedTokens += int(float64(rawTokens) * pct)
	b.displays[displayName(part)]++
}

// displayName keeps the first two words of a command for display.
func displayName(cmd string) string {
	fields := strings.Fields(strings.TrimSpace(cmd))
	switch len(fields) {
	case 0:
		return ""
	case 1:
		return fields[0]
	default:
		return fields[0] + " " + fields[1]
	}
}

func finalizeSupported(buckets map[string]*supportedBucket) []SupportedEntry {
	var out []SupportedEntry
	for key, b := range buckets {
		display := key
		best := 0
		for name, n := range b.displays {
			if n > best {
				best, display = n, name
			}
		}
		pct := 0.0
		if b.rawTokens > 0 {
			pct = float64(b.savedTokens) * 100.0 / float64(b.rawTokens)
		}
		out = append(out, SupportedEntry{
			Command:     display,
			Count:       b.count,
			Via:         b.via,
			SavedTokens: b.savedTokens,
			SavingsPct:  pct,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SavedTokens != out[j].SavedTokens {
			return out[i].SavedTokens > out[j].SavedTokens
		}
		return out[i].Command < out[j].Command
	})
	return out
}

func finalizeUnsupported(buckets map[string]*unsupportedBucket) []UnsupportedEntry {
	var out []UnsupportedEntry
	for base, b := range buckets {
		out = append(out, UnsupportedEntry{BaseCommand: base, Count: b.count, Example: b.example})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].BaseCommand < out[j].BaseCommand
	})
	return out
}

// TotalSaveable sums the saveable tokens across all supported buckets.
func (r Report) TotalSaveable() int {
	t := 0
	for _, s := range r.Supported {
		t += s.SavedTokens
	}
	return t
}

// TotalSupportedCount sums the supported command occurrences.
func (r Report) TotalSupportedCount() int {
	t := 0
	for _, s := range r.Supported {
		t += s.Count
	}
	return t
}

// FormatText renders the report as the human-readable table.
func FormatText(r Report, limit int) string {
	var b strings.Builder
	b.WriteString("gortk discover -- missed savings\n")
	b.WriteString(strings.Repeat("=", 52) + "\n")
	fmt.Fprintf(&b, "Scanned: %d sessions (last %d days), %d Bash commands\n",
		r.SessionsScanned, r.SinceDays, r.TotalCommands)
	pct := 0.0
	if r.TotalCommands > 0 {
		pct = float64(r.AlreadyGortk) * 100.0 / float64(r.TotalCommands)
	}
	fmt.Fprintf(&b, "Already using gortk: %d commands (%.1f%%)\n", r.AlreadyGortk, pct)

	if len(r.Supported) == 0 && len(r.Unsupported) == 0 {
		b.WriteString("\nNo missed savings found. gortk usage looks good!\n")
		return b.String()
	}

	if len(r.Supported) > 0 {
		b.WriteString("\nMISSED SAVINGS -- commands gortk handles\n")
		b.WriteString(strings.Repeat("-", 64) + "\n")
		fmt.Fprintf(&b, "%-24s %5s    %-10s %12s\n", "Command", "Count", "Via", "Est. Savings")
		for _, e := range take(r.Supported, limit) {
			fmt.Fprintf(&b, "%-24s %5d    %-10s ~%s\n",
				truncate(e.Command, 23), e.Count, e.Via, formatTokens(e.SavedTokens))
		}
		b.WriteString(strings.Repeat("-", 64) + "\n")
		fmt.Fprintf(&b, "Total: %d commands -> ~%s saveable\n",
			r.TotalSupportedCount(), formatTokens(r.TotalSaveable()))
	}

	if len(r.Unsupported) > 0 {
		b.WriteString("\nTOP UNHANDLED COMMANDS\n")
		b.WriteString(strings.Repeat("-", 52) + "\n")
		fmt.Fprintf(&b, "%-24s %5s    %s\n", "Command", "Count", "Example")
		for _, e := range takeUnsup(r.Unsupported, limit) {
			fmt.Fprintf(&b, "%-24s %5d    %s\n",
				truncate(e.BaseCommand, 23), e.Count, truncate(e.Example, 40))
		}
	}

	b.WriteString("\n~estimated from tool_result output sizes (chars/4)\n")
	return b.String()
}

func formatTokens(t int) string {
	switch {
	case t >= 1_000_000:
		return fmt.Sprintf("%.1fM tokens", float64(t)/1_000_000)
	case t >= 1_000:
		return fmt.Sprintf("%.1fK tokens", float64(t)/1_000)
	default:
		return fmt.Sprintf("%d tokens", t)
	}
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max <= 2 {
		return string(r[:max])
	}
	return string(r[:max-2]) + ".."
}

func take(s []SupportedEntry, n int) []SupportedEntry {
	if n > 0 && len(s) > n {
		return s[:n]
	}
	return s
}

func takeUnsup(s []UnsupportedEntry, n int) []UnsupportedEntry {
	if n > 0 && len(s) > n {
		return s[:n]
	}
	return s
}

// Run implements `gortk discover`.
func Run(args []string, verbose int) (int, error) {
	opts := Options{SinceDays: 30, Limit: 15, Format: "text"}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--all" || a == "-a":
			opts.All = true
		case a == "--project" || a == "-p":
			if i+1 < len(args) {
				opts.Project = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--project="):
			opts.Project = strings.TrimPrefix(a, "--project=")
		case a == "--since" || a == "-s":
			if i+1 < len(args) {
				opts.SinceDays = atoiDefault(args[i+1], opts.SinceDays)
				i++
			}
		case strings.HasPrefix(a, "--since="):
			opts.SinceDays = atoiDefault(strings.TrimPrefix(a, "--since="), opts.SinceDays)
		case a == "--limit" || a == "-l":
			if i+1 < len(args) {
				opts.Limit = atoiDefault(args[i+1], opts.Limit)
				i++
			}
		case strings.HasPrefix(a, "--limit="):
			opts.Limit = atoiDefault(strings.TrimPrefix(a, "--limit="), opts.Limit)
		case a == "--format" || a == "-f":
			if i+1 < len(args) {
				opts.Format = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--format="):
			opts.Format = strings.TrimPrefix(a, "--format=")
		}
	}

	projectsDir, ok := cchistory.ProjectsDir()
	if !ok {
		return 1, fmt.Errorf("gortk discover: could not determine home directory")
	}

	// Default project filter: the current working directory's Claude slug.
	if !opts.All && opts.Project == "" {
		if cwd, err := os.Getwd(); err == nil {
			opts.Project = cchistory.EncodeProjectPath(cwd)
		}
	}

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "scanning %s (project=%q all=%v since=%dd)\n",
			projectsDir, opts.Project, opts.All, opts.SinceDays)
	}

	rep := Generate(projectsDir, opts)

	if opts.Format == "json" {
		out, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return 1, fmt.Errorf("gortk discover: %w", err)
		}
		fmt.Println(string(out))
		return 0, nil
	}
	fmt.Print(FormatText(rep, opts.Limit))
	return 0, nil
}

func atoiDefault(s string, def int) int {
	n := 0
	if s == "" {
		return def
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}
