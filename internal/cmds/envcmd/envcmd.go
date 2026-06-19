// Package envcmd is gortk's token-optimized environment-variable viewer. It
// reads the process environment, masks sensitive values, categorizes variables
// (PATH / language / cloud / tools / other) and prints a compact summary.
// Faithful port of rtk's src/cmds/system/env_cmd.rs.
//
// Unlike most gortk commands this wraps no external tool: it reads os.Environ
// directly, so there is no process to spawn and no telemetry/tracking.
package envcmd

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "env",
		Summary: "Show environment variables (filtered, sensitive masked)",
		Run:     Run,
	})
}

// Run parses gortk's `env` flags and prints categorized environment variables.
//
// Flags (mirroring rtk's clap definition):
//
//	-f, --filter <substr>   filter by name (case-insensitive substring)
//	--show-all              include sensitive values unmasked
func Run(args []string, verbose int) (int, error) {
	var filter string
	hasFilter := false
	showAll := false

	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--show-all":
			showAll = true
		case a == "-f" || a == "--filter":
			if i+1 < len(args) {
				filter = args[i+1]
				hasFilter = true
				i++
			}
		case strings.HasPrefix(a, "--filter="):
			filter = strings.TrimPrefix(a, "--filter=")
			hasFilter = true
		case strings.HasPrefix(a, "-f="):
			filter = strings.TrimPrefix(a, "-f=")
			hasFilter = true
		case strings.HasPrefix(a, "-f") && len(a) > 2 && !strings.HasPrefix(a, "--"):
			// -fVALUE
			filter = a[2:]
			hasFilter = true
		}
	}

	if verbose > 0 {
		fmt.Fprintln(os.Stderr, "Environment variables:")
	}

	vars := envPairs(os.Environ())
	var filterPtr *string
	if hasFilter {
		filterPtr = &filter
	}

	out := compactEnv(vars, filterPtr, showAll)
	fmt.Print(out)
	return 0, nil
}

// kv is a single environment variable entry.
type kv struct {
	key   string
	value string
}

// envPairs converts os.Environ()-style "KEY=VALUE" strings into sorted kv
// pairs. An entry without '=' is treated as a key with an empty value.
func envPairs(environ []string) []kv {
	pairs := make([]kv, 0, len(environ))
	for _, e := range environ {
		eq := strings.IndexByte(e, '=')
		if eq < 0 {
			pairs = append(pairs, kv{key: e})
			continue
		}
		pairs = append(pairs, kv{key: e[:eq], value: e[eq+1:]})
	}
	sort.SliceStable(pairs, func(i, j int) bool { return pairs[i].key < pairs[j].key })
	return pairs
}

// sensitivePatterns are the lowercase substrings that mark a variable's value
// as sensitive (and therefore masked unless --show-all).
var sensitivePatterns = []string{
	"key", "secret", "password", "token", "credential",
	"auth", "private", "api_key", "apikey", "access_key", "jwt",
}

// isSensitive reports whether key contains any sensitive substring.
func isSensitive(key string) bool {
	lower := strings.ToLower(key)
	for _, p := range sensitivePatterns {
		if strings.Contains(lower, p) {
			return true
		}
	}
	return false
}

// maskValue hides a sensitive value, preserving a 2-char prefix and suffix for
// long values and fully masking anything <= 4 chars.
func maskValue(value string) string {
	chars := []rune(value)
	if len(chars) <= 4 {
		return "****"
	}
	prefix := string(chars[:2])
	suffix := string(chars[len(chars)-2:])
	return fmt.Sprintf("%s****%s", prefix, suffix)
}

func isLangVar(key string) bool {
	patterns := []string{
		"RUST", "CARGO", "PYTHON", "PIP", "NODE", "NPM", "YARN", "DENO", "BUN", "JAVA", "MAVEN",
		"GRADLE", "GO", "GOPATH", "GOROOT", "RUBY", "GEM", "PERL", "PHP", "DOTNET", "NUGET",
	}
	up := strings.ToUpper(key)
	for _, p := range patterns {
		if strings.Contains(up, p) {
			return true
		}
	}
	return false
}

func isCloudVar(key string) bool {
	patterns := []string{
		"AWS", "AZURE", "GCP", "GOOGLE_CLOUD", "DOCKER", "KUBERNETES",
		"K8S", "HELM", "TERRAFORM", "VAULT", "CONSUL", "NOMAD",
	}
	up := strings.ToUpper(key)
	for _, p := range patterns {
		if strings.Contains(up, p) {
			return true
		}
	}
	return false
}

func isToolVar(key string) bool {
	patterns := []string{
		"EDITOR", "VISUAL", "SHELL", "TERM", "GIT", "SSH", "GPG",
		"BREW", "HOMEBREW", "XDG", "CLAUDE", "ANTHROPIC",
	}
	up := strings.ToUpper(key)
	for _, p := range patterns {
		if strings.Contains(up, p) {
			return true
		}
	}
	return false
}

func isInterestingVar(key string) bool {
	patterns := []string{"HOME", "USER", "LANG", "LC_", "TZ", "PWD", "OLDPWD"}
	up := strings.ToUpper(key)
	for _, p := range patterns {
		if strings.HasPrefix(up, p) {
			return true
		}
	}
	return false
}

// pathSep is the platform separator between PATH entries. rtk (Unix) splits on
// ':'; on native Windows PATH is ';'-separated, so we use os.PathListSeparator.
var pathSep = string(os.PathListSeparator)

// compactEnv produces the categorized, masked, token-optimized rendering of the
// environment. This is the pure heart of the command; Run only handles I/O.
//
// filter is nil when no --filter was given. show_all unmasks sensitive values.
func compactEnv(vars []kv, filter *string, showAll bool) string {
	var pathVars, langVars, cloudVars, toolVars, otherVars []kv

	for _, v := range vars {
		key, value := v.key, v.value

		if filter != nil {
			if !strings.Contains(strings.ToLower(key), strings.ToLower(*filter)) {
				continue
			}
		}

		sensitive := isSensitive(key)

		var displayValue string
		switch {
		case sensitive && !showAll:
			displayValue = maskValue(value)
		case len([]rune(value)) > 100:
			runes := []rune(value)
			preview := string(runes[:50])
			displayValue = fmt.Sprintf("%s... (%d chars)", preview, len(runes))
		default:
			displayValue = value
		}

		entry := kv{key: key, value: displayValue}

		switch {
		case strings.Contains(key, "PATH"):
			pathVars = append(pathVars, entry)
		case isLangVar(key):
			langVars = append(langVars, entry)
		case isCloudVar(key):
			cloudVars = append(cloudVars, entry)
		case isToolVar(key):
			toolVars = append(toolVars, entry)
		case filter != nil || isInterestingVar(key):
			otherVars = append(otherVars, entry)
		}
	}

	var b strings.Builder

	if len(pathVars) > 0 {
		b.WriteString("PATH Variables:\n")
		for _, e := range pathVars {
			if e.key == "PATH" {
				paths := strings.Split(e.value, pathSep)
				fmt.Fprintf(&b, "  PATH (%d entries):\n", len(paths))
				const maxPathEntries = core.CapWarnings
				limit := maxPathEntries
				if limit > len(paths) {
					limit = len(paths)
				}
				for _, p := range paths[:limit] {
					fmt.Fprintf(&b, "    %s\n", p)
				}
				if len(paths) > maxPathEntries {
					fmt.Fprintf(&b, "    ... +%d more\n", len(paths)-maxPathEntries)
				}
			} else {
				fmt.Fprintf(&b, "  %s=%s\n", e.key, e.value)
			}
		}
	}

	if len(langVars) > 0 {
		b.WriteString("\nLanguage/Runtime:\n")
		for _, e := range langVars {
			fmt.Fprintf(&b, "  %s=%s\n", e.key, e.value)
		}
	}

	if len(cloudVars) > 0 {
		b.WriteString("\nCloud/Services:\n")
		for _, e := range cloudVars {
			fmt.Fprintf(&b, "  %s=%s\n", e.key, e.value)
		}
	}

	if len(toolVars) > 0 {
		b.WriteString("\nTools:\n")
		for _, e := range toolVars {
			fmt.Fprintf(&b, "  %s=%s\n", e.key, e.value)
		}
	}

	if len(otherVars) > 0 {
		const maxOtherVars = core.CapList
		b.WriteString("\nOther:\n")
		limit := maxOtherVars
		if limit > len(otherVars) {
			limit = len(otherVars)
		}
		for _, e := range otherVars[:limit] {
			fmt.Fprintf(&b, "  %s=%s\n", e.key, e.value)
		}
		if len(otherVars) > maxOtherVars {
			fmt.Fprintf(&b, "  ... +%d more\n", len(otherVars)-maxOtherVars)
		}
	}

	total := len(vars)
	otherShown := len(otherVars)
	if otherShown > 20 {
		otherShown = 20
	}
	shown := len(pathVars) + len(langVars) + len(cloudVars) + len(toolVars) + otherShown
	if filter == nil {
		fmt.Fprintf(&b, "\nTotal: %d vars (showing %d relevant)\n", total, shown)
	}

	return b.String()
}
