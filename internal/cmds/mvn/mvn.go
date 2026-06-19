// Package mvn is gortk's token-optimized Apache Maven wrapper. It filters Maven
// output — Surefire/Failsafe block collapse, compile error/warning dedup, and
// the package/install pipeline with mode toggle — keeping only the signal an
// agent needs. Faithful port of rtk's src/cmds/jvm/mvn_cmd.rs.
//
// Like rtk, this wraps the platform `mvn` (or a `./mvnw` / `.\mvnw.cmd` wrapper
// when present); gortk resolves it PATHEXT-aware via core.ResolvedCommand. The
// output-compression logic lives in pure helper functions (filterSurefire,
// filterCompile, filterPackage, filterQuiet) so it can be tested directly
// against the ported Rust spec.
//
// Phase dispatch (test / compile / package / passthrough) is parsed from args
// inside Run, mirroring rtk's detect_phase. rtk's tracking/timer side-channels
// are dropped: gortk is offline by default.
package mvn

import (
	"regexp"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "mvn",
		Summary: "Apache Maven wrapper with compact output",
		Run:     Run,
	})
}

// maxMvnFailingClasses caps emitted failing test-class blocks and
// `[ERROR] Failures:` summary entries — same binding as pytest/rspec/rake.
const maxMvnFailingClasses = core.CapWarnings

// ── Shared regex patterns ────────────────────────────────────────────────────

var (
	// running matches `[INFO] Running com.example.app.FooTest`.
	running = regexp.MustCompile(`^\[INFO\] Running `)

	// closeRE is the Surefire/Failsafe per-class close line. Captures
	// `Failures` and `Errors`. Tolerates the optional `<<< FAILURE!` /
	// `<<< ERROR!` marker. Separator is `-` (Surefire 2.x) or `--` (3.x).
	// Prefix INFO/ERROR/WARNING (3.x emits WARNING for skipped-only classes).
	closeRE = regexp.MustCompile(
		`^\[(?:INFO|ERROR|WARNING)\] Tests run: \d+, Failures: (\d+), Errors: (\d+), Skipped: \d+, Time elapsed: [^ ]+ s(?:\s+<<<\s*(?:FAILURE|ERROR)!)?\s+--?\s+in (.+)$`,
	)

	// buildFoot matches the final BUILD footer.
	buildFoot = regexp.MustCompile(`^\[(?:INFO|ERROR)\] BUILD (?:SUCCESS|FAILURE)$`)

	// agg matches the aggregate counts line (no `Time elapsed`, no ` - in `).
	agg = regexp.MustCompile(
		`^\[(?:INFO|ERROR)\] Tests run: \d+, Failures: \d+, Errors: \d+, Skipped: \d+\s*$`,
	)

	// pluginBanner: `[INFO] --- plugin:goal (id) @ module ---`.
	pluginBanner = regexp.MustCompile(`^\[INFO\] --- .* @ .* ---$`)

	// moduleBanner: module banner with project name in brackets.
	moduleBanner = regexp.MustCompile(`^\[INFO\] -+< .+ >-+$`)

	// resultsRE: `[INFO] Results:` separator before the aggregate.
	resultsRE = regexp.MustCompile(`^\[INFO\] Results:\s*$`)

	// reactorSummary: reactor summary header opening the per-module block.
	reactorSummary = regexp.MustCompile(`^\[INFO\] Reactor Summary for `)

	// fileCoord: compile-error coordinate substring to strip when deduping.
	fileCoord = regexp.MustCompile(`/[^:]+\.java:\[\d+,\d+\]`)
)

// ── Quiet-mode detection ────────────────────────────────────────────────────

// isQuiet reports whether `-q` / `--quiet` is present. Under quiet mode Maven
// 3.x suppresses all `[INFO]` lines: a passing run emits zero bytes; a failing
// run emits only `[ERROR]`-prefixed lines plus the stack trace.
func isQuiet(args []string) bool {
	for _, a := range args {
		if a == "-q" || a == "--quiet" {
			return true
		}
	}
	return false
}

// ── Phase detection ─────────────────────────────────────────────────────────

// MvnPhase is the detected Maven lifecycle phase that selects a filter.
type MvnPhase int

const (
	PhaseTest        MvnPhase = iota // test, integration-test (Failsafe = Surefire shape)
	PhaseCompile                     // compile, test-compile
	PhasePackage                     // package, install, verify, deploy
	PhasePassthrough                 // clean, site, plugin goals, version/help, empty
)

// detectPhase scans args left-to-right, skips flags + `-D…` system props, picks
// the LAST remaining token. Empty, plugin-form (`:`), or `clean`/`site` →
// Passthrough.
func detectPhase(args []string) MvnPhase {
	last := ""
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			continue
		}
		last = a
	}

	if last == "" || strings.Contains(last, ":") {
		return PhasePassthrough
	}
	switch last {
	case "clean", "site", "site-deploy":
		return PhasePassthrough
	case "test", "integration-test":
		return PhaseTest
	case "compile", "test-compile":
		return PhaseCompile
	case "package", "install", "verify", "deploy":
		return PhasePackage
	default:
		return PhasePassthrough
	}
}

// ── Stack-frame deny-list ────────────────────────────────────────────────────

var frameworkFramePrefixes = []string{
	"at org.junit.",
	"at junit.",
	"at org.apache.maven.surefire.",
	"at sun.reflect.",
	"at jdk.internal.reflect.",
	"at jdk.proxy",
	"at java.base/",
	"at java.lang.reflect.",
	"at java.util.",
}

func isFrameworkFrame(trimmed string) bool {
	for _, p := range frameworkFramePrefixes {
		if strings.HasPrefix(trimmed, p) {
			return true
		}
	}
	return false
}

// boilerPrefixes are boilerplate `[ERROR]` lines Maven emits after
// `Failed to execute goal` — pure noise. Deliberately excludes the resume hint
// (`mvn <args> -rf :…`), `After correcting the problems`, and `Failed to
// execute goal` (all signal).
var boilerPrefixes = []string{
	"[ERROR] See ",
	"[ERROR] -> [Help",
	"[ERROR] To see the full stack trace",
	"[ERROR] Re-run Maven",
	"[ERROR] For more information",
	"[ERROR] [Help",
}

// isBoilerplate matches post-failure help boilerplate plus the bare `[ERROR]`
// divider lines Maven emits between boilerplate blocks.
func isBoilerplate(line string) bool {
	for _, p := range boilerPrefixes {
		if strings.HasPrefix(line, p) {
			return true
		}
	}
	return strings.TrimRight(line, " \t") == "[ERROR]"
}

// isPerTestSubline matches `[ERROR] FQN.method -- Time elapsed: … <<< FAILURE!`
// (or `<<< ERROR!`). The `[ERROR]   Class.test:25 …` failures-summary entries
// (3-space indent, no `<<<` marker) do NOT match.
func isPerTestSubline(line string) bool {
	return strings.HasPrefix(line, "[ERROR] ") &&
		(strings.Contains(line, "<<< FAILURE!") || strings.Contains(line, "<<< ERROR!"))
}

// ── English-footer guard ────────────────────────────────────────────────────

func hasEnglishFooter(stripped string) bool {
	for _, l := range splitLines(stripped) {
		t := strings.TrimSpace(l)
		if strings.HasSuffix(t, " BUILD SUCCESS") || strings.HasSuffix(t, " BUILD FAILURE") {
			return true
		}
	}
	return false
}

// splitLines mirrors Rust's str::lines(): split on '\n' and drop a single
// trailing empty element so a trailing newline does not yield a phantom line.
func splitLines(s string) []string {
	parts := strings.Split(s, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}

// ── Outside-block keep list (shared by surefire + package) ──────────────────

// reactorSummaryKeep returns true for every line while the reactor-summary flag
// is set so per-module status rows survive. Toggles the flag on the summary
// header (enter) and BUILD footer (exit). Must be called BEFORE keepOutsideBlock
// so its clears-flag side effect always runs regardless of `||` short-circuit.
func reactorSummaryKeep(line string, inReactorSummary *bool) bool {
	if reactorSummary.MatchString(line) {
		*inReactorSummary = true
		return true
	}
	if buildFoot.MatchString(line) {
		*inReactorSummary = false
		return false
	}
	return *inReactorSummary
}

func keepOutsideBlock(line string) bool {
	// Help boilerplate must be rejected before the `[ERROR]` catch-all below.
	if isBoilerplate(line) {
		return false
	}
	return resultsRE.MatchString(line) ||
		agg.MatchString(line) ||
		buildFoot.MatchString(line) ||
		moduleBanner.MatchString(line) ||
		strings.HasPrefix(line, "[INFO] Total time:") ||
		strings.HasPrefix(line, "[INFO] Finished at:") ||
		strings.HasPrefix(line, "[INFO] Building ") ||
		strings.HasPrefix(line, "[INFO] Scanning ") ||
		strings.HasPrefix(line, "[INFO] Installing ") ||
		strings.HasPrefix(line, "[ERROR] Failures:") ||
		strings.HasPrefix(line, "[ERROR] Errors:") ||
		(strings.HasPrefix(line, "[ERROR]") && !strings.HasPrefix(line, "[ERROR] Tests run:")) ||
		strings.HasPrefix(line, "[INFO] Building war:") ||
		strings.HasPrefix(line, "[INFO] Building jar:") ||
		strings.HasPrefix(line, "[INFO] Building ear:")
}
