package grep

import (
	"strconv"
	"strings"
)

// valueFlagsShort are the rg short flags (after the leading `-`) that consume one
// following token (or the inline remainder of the cluster) as their value. `-e`
// is handled separately — its value goes to patterns. `-r`/`-R`/`-E` are not
// here: `-r`/`-R` are stripped, `-E` (dialect) is left as a boolean. A missing
// entry fails loud (the value becomes a positional, a visible wrong result, not
// a silent one). Mirrors rtk's VALUE_FLAGS_SHORT = b"ABCMTdfgjmt".
const valueFlagsShort = "ABCMTdfgjmt"

// valueFlagsLong are the long flags that consume the NEXT token as their value
// (space-separated form). The inline `--flag=value` form is one token and passes
// through unchanged. `--regexp` is handled separately (value goes to patterns).
// Mirrors rtk's VALUE_FLAGS_LONG.
var valueFlagsLong = map[string]bool{
	"--after-context":           true,
	"--before-context":          true,
	"--color":                   true,
	"--colors":                  true,
	"--context":                 true,
	"--context-separator":       true,
	"--encoding":                true,
	"--engine":                  true,
	"--field-context-separator": true,
	"--field-match-separator":   true,
	"--file":                    true,
	"--glob":                    true,
	"--iglob":                   true,
	"--ignore-file":             true,
	"--max-columns":             true,
	"--max-count":               true,
	"--max-depth":               true,
	"--max-filesize":            true,
	"--path-separator":          true,
	"--pre":                     true,
	"--pre-glob":                true,
	"--replace":                 true,
	"--sort":                    true,
	"--sortr":                   true,
	"--threads":                 true,
	"--type":                    true,
	"--type-add":                true,
	"--type-clear":              true,
	"--type-not":                true,
}

// clusterKind classifies the result of parsing a short flag cluster.
type clusterKind int

const (
	// clusterBoolean: all chars were boolean flags or `r`/`R` (stripped).
	clusterBoolean clusterKind = iota
	// clusterValueTaking: a value-taking flag was encountered; scanning stopped.
	clusterValueTaking
)

// clusterResult is the parsed content of a short flag cluster (the part after
// the leading `-`). Mirrors rtk's ClusterResult enum.
type clusterResult struct {
	kind clusterKind
	// prefix holds the boolean flag letters before the value-taking char, with
	// r/R stripped. hasPrefix is false when nothing remains (rtk's Option::None).
	prefix    string
	hasPrefix bool
	// flag is the value-taking flag char (only for clusterValueTaking).
	flag byte
	// inline is the bytes after flag in the cluster — its inline value (only for
	// clusterValueTaking). Empty means "consume the next token instead." Returned
	// verbatim — no r/R stripping.
	inline string
}

// parseCluster parses the content of a short flag cluster (everything after the
// leading `-`).
//
// Scans left-to-right: strips r/R, accumulates boolean flag letters, and stops
// at the first value-taking flag (from valueFlagsShort or `e`). Everything after
// that flag char is its inline value, returned verbatim (no r/R stripping). This
// is the only place that touches cluster bytes. Mirrors rtk's parse_cluster.
func parseCluster(rest string) clusterResult {
	var rawPrefix strings.Builder
	for j := 0; j < len(rest); j++ {
		ch := rest[j]
		if ch == 'e' || strings.IndexByte(valueFlagsShort, ch) >= 0 {
			prefix, has := stripR(rawPrefix.String())
			return clusterResult{
				kind:      clusterValueTaking,
				prefix:    prefix,
				hasPrefix: has,
				flag:      ch,
				inline:    rest[j+1:],
			}
		}
		rawPrefix.WriteByte(ch)
	}
	prefix, has := stripR(rawPrefix.String())
	return clusterResult{kind: clusterBoolean, prefix: prefix, hasPrefix: has}
}

// stripR removes r/R from a string of flag letters. The bool return is false
// when nothing remains (rtk's Option::None). Only called on accumulated flag
// letters, never on inline values — stripR("carrot") == "caot", which is exactly
// why it must not touch value bytes (the original -ecarrot bug). Mirrors rtk's
// strip_r.
func stripR(flagLetters string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(flagLetters); i++ {
		c := flagLetters[i]
		if c != 'r' && c != 'R' {
			b.WriteByte(c)
		}
	}
	s := b.String()
	if s == "" {
		return "", false
	}
	return s, true
}

// stripRecursive drops `--recursive` (a grep-ism); every other long flag passes
// through unchanged. The bool return is false for the dropped flag (rtk's None).
// Mirrors rtk's strip_recursive.
func stripRecursive(arg string) (string, bool) {
	if arg == "--recursive" {
		return "", false
	}
	return arg, true
}

// extractPatternPath extracts (patterns, paths, flags) from the raw trailing
// args.
//
//   - patterns: positional pattern + all -e/--regexp values. Empty -> caller errors.
//   - paths:    subsequent non-flag positionals. Empty -> caller defaults to ["."].
//   - flags:    other flags forwarded to rg (-r/-R/--recursive stripped).
//
// Short clusters are scanned left-to-right; the first value-taking letter
// terminates the cluster — everything after it is its inline value, not a
// separate flag. Long value-taking flags consume the next token. `--` marks
// everything after it as positional. Mirrors rtk's extract_pattern_path.
func extractPatternPath(args []string) (patterns, paths, flags []string) {
	var ePatterns []string
	var positionals []string
	pastDashDash := false

	i := 0
	for i < len(args) {
		arg := args[i]

		if pastDashDash {
			positionals = append(positionals, arg)
			i++
			continue
		}

		if arg == "--" {
			pastDashDash = true
			i++
			continue
		}

		if strings.HasPrefix(arg, "--") {
			// --regexp is the long form of -e: value goes to patterns.
			if arg == "--regexp" {
				if i+1 < len(args) {
					ePatterns = append(ePatterns, args[i+1])
					i += 2
				} else {
					i++
				}
				continue
			}
			// Other long value-taking flags: consume next token as value.
			if valueFlagsLong[arg] {
				flags = append(flags, arg)
				if i+1 < len(args) {
					flags = append(flags, args[i+1])
					i += 2
				} else {
					i++
				}
				continue
			}
			// Drop --recursive; pass everything else through.
			if cleaned, keep := stripRecursive(arg); keep {
				flags = append(flags, cleaned)
			}
			i++
			continue
		}

		// Short flag cluster: starts with `-` and has at least one char after it.
		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			cr := parseCluster(arg[1:])
			switch cr.kind {
			case clusterBoolean:
				if cr.hasPrefix {
					flags = append(flags, "-"+cr.prefix)
				}
				i++
			case clusterValueTaking:
				if cr.hasPrefix {
					flags = append(flags, "-"+cr.prefix)
				}
				if cr.flag == 'e' {
					if cr.inline != "" {
						ePatterns = append(ePatterns, cr.inline)
						i++
					} else if i+1 < len(args) {
						ePatterns = append(ePatterns, args[i+1])
						i += 2
					} else {
						flags = append(flags, "-e")
						i++
					}
				} else {
					flags = append(flags, "-"+string(cr.flag))
					if cr.inline != "" {
						flags = append(flags, cr.inline)
						i++
					} else if i+1 < len(args) {
						flags = append(flags, args[i+1])
						i += 2
					} else {
						i++
					}
				}
			}
			continue
		}

		// Positional (a bare `-` lands here too, matching rtk's `_ =>`).
		positionals = append(positionals, arg)
		i++
	}

	// If -e/--regexp was used: all positionals are paths.
	// Otherwise: first positional is the pattern, rest are paths.
	if len(ePatterns) > 0 {
		patterns = ePatterns
		paths = positionals
	} else if len(positionals) > 0 {
		patterns = positionals[:1]
		paths = append([]string{}, positionals[1:]...)
	}
	return patterns, paths, flags
}

// parseGrepFlags peels the gortk-level grep flags off the front of args, leaving
// the remainder as the trailing args forwarded to rg/grep (rtk's extra_args).
// gortk has no clap layer, so this mirrors rtk's Commands::Grep declaration:
//
//	-l/--max-len <n>   (default 80)   max line length
//	-m/--max <n>       (default 200)  max results to show
//	--context-only                    show only the match context
//	-t/--file-type <s>                filter by file type (e.g. ts, py, rust)
//
// All of these are gortk's own flags and must be consumed before extra_args so
// they are not forwarded to rg. The `=` form (e.g. --max-len=120) is supported.
// Unrecognized flags and the first positional terminate flag parsing, so trailing
// rg/grep flags after the pattern still reach extra_args.
func parseGrepFlags(args []string) (maxLineLen, maxResults int, contextOnly bool, fileType string, rest []string) {
	maxLineLen = defaultMaxLineLen
	maxResults = defaultMaxResults

	i := 0
	for i < len(args) {
		a := args[i]
		consumedValue := false

		switch {
		case a == "-l" || a == "--max-len":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					maxLineLen = n
				}
				i += 2
				consumedValue = true
			} else {
				i++
				consumedValue = true
			}
		case strings.HasPrefix(a, "--max-len="):
			if n, err := strconv.Atoi(strings.TrimPrefix(a, "--max-len=")); err == nil {
				maxLineLen = n
			}
			i++
			consumedValue = true
		case a == "-m" || a == "--max":
			if i+1 < len(args) {
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					maxResults = n
				}
				i += 2
				consumedValue = true
			} else {
				i++
				consumedValue = true
			}
		case strings.HasPrefix(a, "--max="):
			if n, err := strconv.Atoi(strings.TrimPrefix(a, "--max=")); err == nil {
				maxResults = n
			}
			i++
			consumedValue = true
		case a == "--context-only":
			contextOnly = true
			i++
			consumedValue = true
		case a == "-t" || a == "--file-type":
			if i+1 < len(args) {
				fileType = args[i+1]
				i += 2
				consumedValue = true
			} else {
				i++
				consumedValue = true
			}
		case strings.HasPrefix(a, "--file-type="):
			fileType = strings.TrimPrefix(a, "--file-type=")
			i++
			consumedValue = true
		}

		if consumedValue {
			continue
		}

		// First non-gortk token: stop. Everything from here on is extra_args
		// (pattern, paths, and native rg/grep flags). Mirrors clap's
		// trailing_var_arg, which captures the first positional and all that
		// follow it verbatim.
		rest = args[i:]
		return maxLineLen, maxResults, contextOnly, fileType, rest
	}

	return maxLineLen, maxResults, contextOnly, fileType, nil
}
