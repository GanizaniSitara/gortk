// Package learn scans Claude Code session history for CLI mistakes that were
// corrected on a later attempt — a failed command followed within a short window
// by a similar successful one — and surfaces the recurring fixes. Faithful port
// of rtk's `rtk learn` (src/learn/), using the shared cchistory reader.
//
//	gortk learn [--project P] [--all] [--since N] [--format text|json]
//	            [--write-rules] [--min-confidence F] [--min-occurrences N]
//
// Offline: it only reads transcripts under <UserHomeDir>/.claude/projects. With
// --write-rules it writes a .claude/rules/cli-corrections.md cheatsheet in the
// current directory.
package learn

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gortk/internal/cmds/cchistory"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "learn",
		Summary: "Learn recurring CLI corrections from Claude Code error history",
		Run:     Run,
	})
}

// Options controls a learn scan.
type Options struct {
	Project        string
	All            bool
	SinceDays      int
	Format         string
	WriteRules     bool
	MinConfidence  float64
	MinOccurrences int
}

const (
	correctionWindow = 3
	minConfidence    = 0.6
)

// CommandExecution is one command and its result, in chronological order.
type CommandExecution struct {
	Command string
	IsError bool
	Output  string
}

// CorrectionPair is a single detected wrong→right correction.
type CorrectionPair struct {
	Wrong      string
	Right      string
	ErrorType  string
	Confidence float64
	ErrOutput  string
}

// CorrectionRule is a deduplicated correction with an occurrence count.
type CorrectionRule struct {
	Wrong        string `json:"wrong"`
	Right        string `json:"right"`
	ErrorType    string `json:"error_type"`
	Occurrences  int    `json:"occurrences"`
	BaseCommand  string `json:"base_command"`
	ExampleError string `json:"-"`
}

// IsCommandError reports whether output indicates a real CLI error (not a user
// rejection and not a clean run). Mirrors rtk's is_command_error.
func IsCommandError(isError bool, output string) bool {
	if !isError {
		return false
	}
	if userRejection(output) {
		return false
	}
	low := strings.ToLower(output)
	for _, marker := range []string{
		"error", "failed", "unknown", "invalid", "not found", "permission denied", "cannot",
	} {
		if strings.Contains(low, marker) {
			return true
		}
	}
	return false
}

func userRejection(output string) bool {
	low := strings.ToLower(output)
	for _, p := range []string{
		"user doesn't want", "user declined", "user rejected", "user cancelled",
		"operation cancelled by user", "operation aborted by user",
	} {
		if strings.Contains(low, p) {
			return true
		}
	}
	return false
}

// ClassifyError buckets an error output into a coarse error type. Mirrors rtk's
// classify_error precedence.
func ClassifyError(output string) string {
	low := strings.ToLower(output)
	switch {
	case containsAny(low, "unexpected argument", "unknown option", "unknown flag",
		"unrecognized option", "unrecognized flag", "invalid option", "invalid flag"):
		return "Unknown Flag"
	case containsAny(low, "command not found", "not recognized as an internal"):
		return "Command Not Found"
	case containsAny(low, "requires a value", "requires an argument",
		"missing required argument", "missing argument"):
		return "Missing Argument"
	case containsAny(low, "permission denied", "access denied", "not permitted"):
		return "Permission Denied"
	case containsAny(low, "no such file or directory", "cannot find the path", "file not found"):
		return "Wrong Path"
	default:
		return "General Error"
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// commandSimilarity scores two commands: 0 if their base commands differ, else
// 0.5 + up to 0.5 from Jaccard similarity of their argument sets. Mirrors rtk's
// command_similarity.
func commandSimilarity(a, b string) float64 {
	baseA := cchistory.BaseCommand(a)
	baseB := cchistory.BaseCommand(b)
	if baseA != baseB {
		return 0
	}
	argsA := argSet(strings.TrimPrefix(a, baseA))
	argsB := argSet(strings.TrimPrefix(b, baseB))
	if len(argsA) == 0 && len(argsB) == 0 {
		return 1.0
	}
	inter, union := jaccard(argsA, argsB)
	if union == 0 {
		return 0.5
	}
	return 0.5 + float64(inter)/float64(union)*0.5
}

func argSet(s string) map[string]bool {
	set := map[string]bool{}
	for _, f := range strings.Fields(s) {
		set[f] = true
	}
	return set
}

func jaccard(a, b map[string]bool) (inter, union int) {
	seen := map[string]bool{}
	for k := range a {
		seen[k] = true
		if b[k] {
			inter++
		}
	}
	for k := range b {
		seen[k] = true
	}
	return inter, len(seen)
}

// isTDDCycleError reports compilation/test failures that are TDD iterations, not
// CLI corrections. Mirrors rtk's is_tdd_cycle_error.
func isTDDCycleError(output string) bool {
	if strings.Contains(output, "error[E") || strings.Contains(output, "aborting due to") {
		return true
	}
	if strings.Contains(output, "test result: FAILED") || strings.Contains(output, "tests failed") {
		return true
	}
	return false
}

// differsOnlyByPath reports near-identical commands that differ only by a path
// argument (exploration, not correction). Mirrors rtk's differs_only_by_path.
func differsOnlyByPath(a, b string) bool {
	if cchistory.BaseCommand(a) != cchistory.BaseCommand(b) {
		return false
	}
	sim := commandSimilarity(a, b)
	return sim > 0.9 && sim < 1.0
}

// FindCorrections walks executions in order, pairing each genuine error with a
// later similar successful command within correctionWindow. Mirrors rtk's
// find_corrections.
func FindCorrections(cmds []CommandExecution) []CorrectionPair {
	var out []CorrectionPair
	for i := range cmds {
		c := cmds[i]
		if !IsCommandError(c.IsError, c.Output) {
			continue
		}
		if isTDDCycleError(c.Output) {
			continue
		}
		errType := ClassifyError(c.Output)
		end := i + 1 + correctionWindow
		if end > len(cmds) {
			end = len(cmds)
		}
		for j := i + 1; j < end; j++ {
			cand := cmds[j]
			sim := commandSimilarity(c.Command, cand.Command)
			if sim < 0.5 {
				continue
			}
			if differsOnlyByPath(c.Command, cand.Command) {
				continue
			}
			if c.Command == cand.Command {
				continue
			}
			conf := sim
			if !IsCommandError(cand.IsError, cand.Output) {
				conf += 0.2
				if conf > 1.0 {
					conf = 1.0
				}
			}
			if conf < minConfidence {
				continue
			}
			out = append(out, CorrectionPair{
				Wrong:      c.Command,
				Right:      cand.Command,
				ErrorType:  errType,
				Confidence: conf,
				ErrOutput:  takeChars(c.Output, 500),
			})
			break // first match only
		}
	}
	return out
}

func takeChars(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// extractDiffToken returns the most distinctive token change between wrong and
// right. Mirrors rtk's extract_diff_token (used only as a dedup key here).
func extractDiffToken(wrong, right string) string {
	w := argSet(wrong)
	r := argSet(right)
	var removed, added []string
	for k := range w {
		if !r[k] {
			removed = append(removed, k)
		}
	}
	for k := range r {
		if !w[k] {
			added = append(added, k)
		}
	}
	sort.Strings(removed)
	sort.Strings(added)
	switch {
	case len(removed) > 0 && len(added) > 0:
		return removed[0] + " -> " + added[0]
	case len(removed) > 0:
		return "removed " + removed[0]
	case len(added) > 0:
		return "added " + added[0]
	default:
		return "unknown"
	}
}

// DeduplicateCorrections groups pairs by (base, error type, diff token), keeps
// the highest-confidence example, and counts occurrences. Mirrors rtk's
// deduplicate_corrections (sorted by occurrences desc, then base for stability).
func DeduplicateCorrections(pairs []CorrectionPair) []CorrectionRule {
	type key struct{ base, errType, diff string }
	groups := map[key][]CorrectionPair{}
	for _, p := range pairs {
		k := key{
			base:    cchistory.BaseCommand(p.Wrong),
			errType: p.ErrorType,
			diff:    extractDiffToken(p.Wrong, p.Right),
		}
		groups[k] = append(groups[k], p)
	}
	var rules []CorrectionRule
	for k, group := range groups {
		best := group[0]
		for _, p := range group[1:] {
			if p.Confidence > best.Confidence {
				best = p
			}
		}
		rules = append(rules, CorrectionRule{
			Wrong:        best.Wrong,
			Right:        best.Right,
			ErrorType:    best.ErrorType,
			Occurrences:  len(group),
			BaseCommand:  k.base,
			ExampleError: best.ErrOutput,
		})
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Occurrences != rules[j].Occurrences {
			return rules[i].Occurrences > rules[j].Occurrences
		}
		return rules[i].BaseCommand < rules[j].BaseCommand
	})
	return rules
}

// Report bundles a learn scan's results.
type Report struct {
	SessionsScanned  int
	TotalCorrections int
	SinceDays        int
	Rules            []CorrectionRule
}

// Generate runs a learn scan against the given projects directory.
func Generate(projectsDir string, opts Options) Report {
	filter := ""
	if !opts.All {
		filter = opts.Project
	}
	sessions := cchistory.DiscoverSessions(projectsDir, filter, opts.SinceDays)

	var all []CommandExecution
	for _, path := range sessions {
		cmds, err := cchistory.ExtractCommands(path)
		if err != nil {
			continue
		}
		for _, ext := range cmds {
			if ext.OutputContent == "" {
				continue
			}
			all = append(all, CommandExecution{
				Command: ext.Command,
				IsError: ext.IsError,
				Output:  ext.OutputContent,
			})
		}
	}

	pairs := FindCorrections(all)
	var filtered []CorrectionPair
	for _, p := range pairs {
		if p.Confidence >= opts.MinConfidence {
			filtered = append(filtered, p)
		}
	}
	rules := DeduplicateCorrections(filtered)
	var kept []CorrectionRule
	for _, r := range rules {
		if r.Occurrences >= opts.MinOccurrences {
			kept = append(kept, r)
		}
	}

	return Report{
		SessionsScanned:  len(sessions),
		TotalCorrections: len(filtered),
		SinceDays:        opts.SinceDays,
		Rules:            kept,
	}
}

// FormatConsole renders the human-readable correction list. Mirrors rtk's
// format_console_report.
func FormatConsole(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "gortk learn -- %d rules from %d corrections (%d sessions, %d days)\n",
		len(r.Rules), r.TotalCorrections, r.SessionsScanned, r.SinceDays)
	if len(r.Rules) == 0 {
		b.WriteString("\nNo CLI corrections detected.\n")
		return b.String()
	}
	b.WriteByte('\n')
	for _, rule := range r.Rules {
		marker := "     "
		if rule.Occurrences > 1 {
			marker = fmt.Sprintf("[%dx] ", rule.Occurrences)
		}
		fmt.Fprintf(&b, "%s%s  ->  %s\n", marker, rule.Wrong, rule.Right)
		if line := firstLine(rule.ExampleError); line != "" {
			fmt.Fprintf(&b, "     Error: %s\n", line)
		}
	}
	return b.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

// WriteRulesFile writes a grouped markdown cheatsheet of corrections to path.
// Mirrors rtk's write_rules_file.
func WriteRulesFile(rules []CorrectionRule, path string) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	var b strings.Builder
	b.WriteString("# CLI Corrections (auto-generated by gortk learn)\n")
	b.WriteString("# Run `gortk learn --write-rules` to update\n\n")
	if len(rules) == 0 {
		b.WriteString("No CLI corrections detected yet.\n")
		return os.WriteFile(path, []byte(b.String()), 0o644)
	}
	grouped := map[string][]CorrectionRule{}
	for _, r := range rules {
		grouped[r.BaseCommand] = append(grouped[r.BaseCommand], r)
	}
	bases := make([]string, 0, len(grouped))
	for k := range grouped {
		bases = append(bases, k)
	}
	sort.Strings(bases)
	for _, base := range bases {
		fmt.Fprintf(&b, "## %s\n", capitalizeFirst(base))
		for _, r := range grouped[base] {
			note := ""
			if r.Occurrences > 1 {
				note = fmt.Sprintf(" (seen %dx)", r.Occurrences)
			}
			fmt.Fprintf(&b, "- Use `%s` not `%s`%s\n", r.Right, r.Wrong, note)
		}
		b.WriteByte('\n')
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = []rune(strings.ToUpper(string(r[0])))[0]
	return string(r)
}

// Run implements `gortk learn`.
func Run(args []string, verbose int) (int, error) {
	opts := Options{SinceDays: 30, Format: "text", MinConfidence: minConfidence, MinOccurrences: 1}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--all" || a == "-a":
			opts.All = true
		case a == "--write-rules" || a == "-w":
			opts.WriteRules = true
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
		case a == "--format" || a == "-f":
			if i+1 < len(args) {
				opts.Format = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--format="):
			opts.Format = strings.TrimPrefix(a, "--format=")
		case a == "--min-confidence":
			if i+1 < len(args) {
				opts.MinConfidence = atofDefault(args[i+1], opts.MinConfidence)
				i++
			}
		case strings.HasPrefix(a, "--min-confidence="):
			opts.MinConfidence = atofDefault(strings.TrimPrefix(a, "--min-confidence="), opts.MinConfidence)
		case a == "--min-occurrences":
			if i+1 < len(args) {
				opts.MinOccurrences = atoiDefault(args[i+1], opts.MinOccurrences)
				i++
			}
		case strings.HasPrefix(a, "--min-occurrences="):
			opts.MinOccurrences = atoiDefault(strings.TrimPrefix(a, "--min-occurrences="), opts.MinOccurrences)
		}
	}

	projectsDir, ok := cchistory.ProjectsDir()
	if !ok {
		return 1, fmt.Errorf("gortk learn: could not determine home directory")
	}
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
		payload := struct {
			SessionsScanned  int              `json:"sessions_scanned"`
			TotalCorrections int              `json:"total_corrections"`
			Rules            []CorrectionRule `json:"rules"`
		}{rep.SessionsScanned, rep.TotalCorrections, rep.Rules}
		out, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return 1, fmt.Errorf("gortk learn: %w", err)
		}
		fmt.Println(string(out))
		return 0, nil
	}

	fmt.Print(FormatConsole(rep))
	if opts.WriteRules && len(rep.Rules) > 0 {
		rulesPath := filepath.Join(".claude", "rules", "cli-corrections.md")
		if err := WriteRulesFile(rep.Rules, rulesPath); err != nil {
			return 1, fmt.Errorf("gortk learn: writing rules: %w", err)
		}
		fmt.Printf("\nWritten to: %s\n", rulesPath)
	}
	return 0, nil
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// atofDefault parses a small non-negative decimal (e.g. "0.6") without strconv's
// full surface; falls back to def on any malformed input.
func atofDefault(s string, def float64) float64 {
	if s == "" {
		return def
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	intPart, fracPart, hasFrac := s, "", false
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart, fracPart, hasFrac = s[:dot], s[dot+1:], true
	}
	val := 0.0
	for _, c := range intPart {
		if c < '0' || c > '9' {
			return def
		}
		val = val*10 + float64(c-'0')
	}
	if hasFrac {
		scale := 0.1
		for _, c := range fracPart {
			if c < '0' || c > '9' {
				return def
			}
			val += float64(c-'0') * scale
			scale /= 10
		}
	}
	if neg {
		val = -val
	}
	return val
}
