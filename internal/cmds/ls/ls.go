// Package ls is gortk's token-optimized directory listing. It wraps the native
// `ls` tool, parses its long-format output, and emits a compact tree-like
// summary. Faithful port of rtk's src/cmds/system/ls.rs.
//
// Like rtk, this wraps the platform `ls`. On Windows that means an `ls` from
// Git for Windows / coreutils on PATH; gortk resolves it PATHEXT-aware.
package ls

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "ls",
		Summary: "List directory contents with token-optimized output",
		Run:     Run,
	})
}

// lsDateRE matches the date+time field in `ls -la` output, used as a stable
// anchor regardless of owner/group column width: " Mar 31 16:18 " or
// " Dec 25  2024 ".
var lsDateRE = regexp.MustCompile(`\s+(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2}\s+(?:\d{4}|\d{2}:\d{2})\s+`)

// Run executes the ls command.
func Run(args []string, verbose int) (int, error) {
	showAll := false
	showLong := false
	for _, a := range args {
		if (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "a")) || a == "--all" {
			showAll = true
		}
		if a == "--full-time" || a == "--format=long" || a == "--format=verbose" {
			showLong = true
		} else if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") {
			if strings.ContainsAny(a, "lgno") {
				showLong = true
			}
		}
	}

	var flags, paths []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
		} else {
			paths = append(paths, a)
		}
	}

	cmd := core.ResolvedCommand("ls")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.Args = append(cmd.Args, "-la")
	for _, flag := range flags {
		if strings.HasPrefix(flag, "--") {
			if flag != "--all" {
				cmd.Args = append(cmd.Args, flag)
			}
		} else {
			stripped := strings.TrimLeft(flag, "-")
			var extra strings.Builder
			for _, c := range stripped {
				if c != 'l' && c != 'a' && c != 'h' {
					extra.WriteRune(c)
				}
			}
			if extra.Len() > 0 {
				cmd.Args = append(cmd.Args, "-"+extra.String())
			}
		}
	}
	if len(paths) == 0 {
		cmd.Args = append(cmd.Args, ".")
	} else {
		cmd.Args = append(cmd.Args, paths...)
	}

	targetDisplay := "."
	if len(paths) > 0 {
		targetDisplay = strings.Join(paths, " ")
	}

	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true, NoTrailingNewline: true}
	return core.RunFiltered(cmd, "ls", "-la "+targetDisplay, func(raw string) string {
		entries, summary, parsedCount := compactLS(raw, showAll, showLong)

		hasRealContent := false
		for _, l := range strings.Split(raw, "\n") {
			if !strings.HasPrefix(l, "total ") && l != "" && !isDotDir(l) {
				hasRealContent = true
				break
			}
		}
		if parsedCount == 0 && hasRealContent {
			return raw
		}

		filtered := entries
		if core.IsTerminal(os.Stdout) {
			filtered = entries + summary
		}
		if verbose > 0 {
			reduction := 0
			if len(raw) > 0 {
				reduction = 100 - (len(filtered)*100)/len(raw)
			}
			fmt.Fprintf(os.Stderr, "Chars: %d → %d (%d%% reduction)\n", len(raw), len(filtered), reduction)
		}
		return filtered
	}, opts)
}

func humanSize(bytes uint64) string {
	switch {
	case bytes >= 1_048_576:
		return fmt.Sprintf("%.1fM", float64(bytes)/1_048_576)
	case bytes >= 1024:
		return fmt.Sprintf("%.1fK", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%dB", bytes)
	}
}

func isDotDir(line string) bool {
	t := strings.TrimSpace(line)
	return strings.HasSuffix(t, ".") || strings.HasSuffix(t, "..")
}

// parseLSLine parses one `ls -la` line into (fileType, perms, size, name).
func parseLSLine(line string) (byte, string, uint64, string, bool) {
	if isDotDir(line) {
		return 0, "", 0, "", false
	}
	loc := lsDateRE.FindStringIndex(line)
	if loc == nil {
		return 0, "", 0, "", false
	}
	name := line[loc[1]:]
	beforeDate := line[:loc[0]]
	beforeParts := strings.Fields(beforeDate)
	if len(beforeParts) < 4 {
		return 0, "", 0, "", false
	}
	perms := beforeParts[0]
	if perms == "" {
		return 0, "", 0, "", false
	}
	fileType := perms[0]

	var size uint64
	for i := len(beforeParts) - 1; i >= 0; i-- {
		if v, err := parseUint(beforeParts[i]); err == nil {
			size = v
			break
		}
	}
	return fileType, perms, size, name, true
}

func parseUint(s string) (uint64, error) {
	var n uint64
	if s == "" {
		return 0, fmt.Errorf("empty")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a number")
		}
		n = n*10 + uint64(c-'0')
	}
	return n, nil
}

// permsToOctal converts an ls permission string to octal (e.g. "-rw-r--r--" -> "644").
func permsToOctal(perms string) (string, bool) {
	if len(perms) < 10 {
		return "", false
	}
	for i := 0; i < len(perms); i++ {
		if perms[i] > 127 {
			return "", false
		}
	}
	b := []byte(perms)
	permValue := func(r, w, x bool) uint32 {
		var v uint32
		if r {
			v |= 4
		}
		if w {
			v |= 2
		}
		if x {
			v |= 1
		}
		return v
	}
	ownerX := b[3] == 'x' || b[3] == 's'
	groupX := b[6] == 'x' || b[6] == 's'
	otherX := b[9] == 'x' || b[9] == 't'
	owner := permValue(b[1] == 'r', b[2] == 'w', ownerX)
	group := permValue(b[4] == 'r', b[5] == 'w', groupX)
	other := permValue(b[7] == 'r', b[8] == 'w', otherX)
	setuid := b[3] == 's' || b[3] == 'S'
	setgid := b[6] == 's' || b[6] == 'S'
	sticky := b[9] == 't' || b[9] == 'T'
	special := permValue(setuid, setgid, sticky)
	if special > 0 {
		return fmt.Sprintf("%d%d%d%d", special, owner, group, other), true
	}
	return fmt.Sprintf("%d%d%d", owner, group, other), true
}

// compactLS parses `ls -la` output into compact form. Returns
// (entries, summary, parsedCount).
func compactLS(raw string, showAll, showLong bool) (string, string, int) {
	type dirEntry struct {
		name  string
		octal string
	}
	type fileEntry struct {
		name  string
		size  string
		octal string
	}
	var dirs []dirEntry
	var files []fileEntry
	byExt := map[string]int{}
	linesSeen := 0
	parsedCount := 0
	dotdirs := 0

	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "total ") || line == "" {
			continue
		}
		linesSeen++

		fileType, perms, size, name, ok := parseLSLine(line)
		if !ok {
			if isDotDir(line) {
				dotdirs++
			}
			continue
		}
		parsedCount++

		if !showAll && core.IsNoiseDir(name) {
			continue
		}

		octal := ""
		if showLong {
			if o, ok := permsToOctal(perms); ok {
				octal = o
			}
		}

		if fileType == 'd' {
			dirs = append(dirs, dirEntry{name: name, octal: octal})
		} else {
			ext := "no ext"
			if pos := strings.LastIndex(name, "."); pos >= 0 {
				ext = name[pos:]
			}
			byExt[ext]++
			files = append(files, fileEntry{name: name, size: humanSize(size), octal: octal})
		}
	}

	if len(dirs) == 0 && len(files) == 0 {
		if linesSeen > 0 && parsedCount == 0 {
			if dotdirs == linesSeen {
				return "(empty)\n", "", 0
			}
			return "", "", 0
		}
		return "(empty)\n", "", 0
	}

	var entries strings.Builder
	for _, d := range dirs {
		if d.octal != "" {
			entries.WriteString(d.octal)
			entries.WriteString("  ")
		}
		entries.WriteString(d.name)
		entries.WriteString("/\n")
	}
	for _, f := range files {
		if f.octal != "" {
			entries.WriteString(f.octal)
			entries.WriteString("  ")
		}
		entries.WriteString(f.name)
		entries.WriteString("  ")
		entries.WriteString(f.size)
		entries.WriteByte('\n')
	}

	summary := fmt.Sprintf("\nSummary: %d files, %d dirs", len(files), len(dirs))
	if len(byExt) > 0 {
		const maxExtSummary = core.CapWarnings - 5 // reduced(CAP_WARNINGS, 5) = 5
		type kv struct {
			ext   string
			count int
		}
		var extCounts []kv
		for k, v := range byExt {
			extCounts = append(extCounts, kv{k, v})
		}
		sort.Slice(extCounts, func(i, j int) bool {
			if extCounts[i].count != extCounts[j].count {
				return extCounts[i].count > extCounts[j].count
			}
			return extCounts[i].ext < extCounts[j].ext
		})
		var parts []string
		limit := maxExtSummary
		if limit > len(extCounts) {
			limit = len(extCounts)
		}
		for _, e := range extCounts[:limit] {
			parts = append(parts, fmt.Sprintf("%d %s", e.count, e.ext))
		}
		summary += " (" + strings.Join(parts, ", ")
		if len(extCounts) > maxExtSummary {
			summary += fmt.Sprintf(", +%d more", len(extCounts)-maxExtSummary)
		}
		summary += ")"
	}
	summary += "\n"

	return entries.String(), summary, parsedCount
}
