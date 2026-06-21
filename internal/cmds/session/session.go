// Package session reports gortk adoption across recent Claude Code sessions:
// how many Bash commands each session ran, how many of those gortk covers
// (either an explicit "gortk ..." invocation or a command gortk would optimize),
// and the adoption percentage. Pragmatic port of rtk's `rtk session`
// (src/analytics/session_cmd.rs), using the shared cchistory reader and the
// registry/tomlfilter-backed classifier instead of rtk's static rule table.
//
//	gortk session
//
// Offline: reads only transcripts under <UserHomeDir>/.claude/projects. Scans the
// 10 most-recently-modified top-level session files from the last 30 days.
package session

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"gortk/internal/cmds/cchistory"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "session",
		Summary: "Report gortk adoption across recent Claude Code sessions",
		Run:     Run,
	})
}

const (
	defaultSinceDays = 30
	maxSessions      = 10
)

// Summary is one session's adoption row.
type Summary struct {
	ID           string
	Date         string
	TotalCmds    int
	GortkCmds    int
	OutputTokens int
}

// AdoptionPct is the share of commands gortk covers (0 when no commands).
func (s Summary) AdoptionPct() float64 {
	if s.TotalCmds == 0 {
		return 0
	}
	return float64(s.GortkCmds) * 100.0 / float64(s.TotalCmds)
}

// CountGortkCommands splits each extracted command into a chain and counts how
// many segments gortk covers: an explicit "gortk ..." call, or a command the
// classifier reports as Supported. Returns (total, covered, outputTokens).
// Faithful port of rtk's count_rtk_commands.
func CountGortkCommands(cmds []cchistory.ExtractedCommand) (total, gortk, output int) {
	for _, c := range cmds {
		for _, part := range cchistory.SplitChain(c.Command) {
			total++
			class := cchistory.Classify(part)
			if class == cchistory.AlreadyGortk || class.Supported() {
				gortk++
			}
		}
		if c.OutputLen > 0 {
			output += c.OutputLen
		}
	}
	return total, gortk, output
}

// progressBar renders a fixed-width @/. bar for pct (0–100). Mirrors rtk's
// progress_bar.
func progressBar(pct float64, width int) string {
	filled := int(pct/100.0*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}
	return strings.Repeat("@", filled) + strings.Repeat(".", width-filled)
}

// Generate builds adoption summaries from the projects directory: the most
// recent maxSessions top-level session files (subagent files skipped), newest
// first. now is injected for deterministic relative-date rendering in tests.
func Generate(projectsDir string, now time.Time) []Summary {
	sessions := cchistory.DiscoverSessions(projectsDir, "", defaultSinceDays)

	// Drop subagent transcripts (only count top-level sessions, like rtk).
	type fileMod struct {
		path string
		mod  time.Time
	}
	var files []fileMod
	for _, p := range sessions {
		if hasPathComponent(p, "subagents") {
			continue
		}
		mod := time.Time{}
		if fi, err := os.Stat(p); err == nil {
			mod = fi.ModTime()
		}
		files = append(files, fileMod{path: p, mod: mod})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	if len(files) > maxSessions {
		files = files[:maxSessions]
	}

	var summaries []Summary
	for _, f := range files {
		cmds, err := cchistory.ExtractCommands(f.path)
		if err != nil || len(cmds) == 0 {
			continue
		}
		total, gortk, output := CountGortkCommands(cmds)
		id := strings.TrimSuffix(filepathBase(f.path), ".jsonl")
		shortID := id
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		date := "?"
		if !f.mod.IsZero() {
			date = relativeDateFrom(f.mod, now)
		}
		summaries = append(summaries, Summary{
			ID:           shortID,
			Date:         date,
			TotalCmds:    total,
			GortkCmds:    gortk,
			OutputTokens: output,
		})
	}
	return summaries
}

func relativeDateFrom(mod, now time.Time) string {
	days := int(now.Sub(mod).Hours() / 24)
	switch {
	case days <= 0:
		return "Today"
	case days == 1:
		return "Yesterday"
	default:
		return fmt.Sprintf("%dd ago", days)
	}
}

// hasPathComponent reports whether name appears as a full slash- or
// backslash-delimited directory component of p (case-insensitive). Used to skip
// subagent transcripts without false-positiving on paths that merely contain the
// substring (e.g. a temp dir whose name embeds "subagents").
func hasPathComponent(p, name string) bool {
	norm := strings.ToLower(strings.ReplaceAll(p, "\\", "/"))
	for _, part := range strings.Split(norm, "/") {
		if part == name {
			return true
		}
	}
	return false
}

func filepathBase(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// FormatText renders the adoption table.
func FormatText(summaries []Summary) string {
	var b strings.Builder
	b.WriteString("gortk session overview (last 10)\n")
	b.WriteString(strings.Repeat("-", 70) + "\n")
	fmt.Fprintf(&b, "%-12s %-12s %5s %5s %9s %-7s %8s\n",
		"Session", "Date", "Cmds", "gortk", "Adoption", "", "Output")
	b.WriteString(strings.Repeat("-", 70) + "\n")

	if len(summaries) == 0 {
		b.WriteString("(no sessions with Bash commands found)\n")
		return b.String()
	}

	totalCmds, totalGortk := 0, 0
	for _, s := range summaries {
		pct := s.AdoptionPct()
		bar := progressBar(pct, 5)
		totalCmds += s.TotalCmds
		totalGortk += s.GortkCmds
		fmt.Fprintf(&b, "%-12s %-12s %5d %5d %7.0f%% %-7s %8s\n",
			s.ID, s.Date, s.TotalCmds, s.GortkCmds, pct, bar, formatTokens(s.OutputTokens))
	}
	b.WriteString(strings.Repeat("-", 70) + "\n")
	avg := 0.0
	if totalCmds > 0 {
		avg = float64(totalGortk) * 100.0 / float64(totalCmds)
	}
	fmt.Fprintf(&b, "Average adoption: %.0f%%\n", avg)
	b.WriteString("Tip: run `gortk discover` to find missed gortk opportunities\n")
	return b.String()
}

func formatTokens(t int) string {
	// Output tokens are byte lengths here (rtk summed output_len); keep K/M scaling.
	switch {
	case t >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(t)/1_000_000)
	case t >= 1_000:
		return fmt.Sprintf("%.1fK", float64(t)/1_000)
	default:
		return fmt.Sprintf("%d", t)
	}
}

// Run implements `gortk session`.
func Run(args []string, verbose int) (int, error) {
	projectsDir, ok := cchistory.ProjectsDir()
	if !ok {
		return 1, fmt.Errorf("gortk session: could not determine home directory")
	}
	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "scanning %s (last %d days, top %d sessions)\n",
			projectsDir, defaultSinceDays, maxSessions)
	}
	summaries := Generate(projectsDir, time.Now())
	fmt.Print(FormatText(summaries))
	return 0, nil
}
