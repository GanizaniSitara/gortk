// filters.go holds the Maven output-compression state machines and the four
// public filters (filterSurefire, filterCompile, filterPackage, filterQuiet),
// plus the gortk entry point Run. Faithful port of the matching parts of rtk's
// src/cmds/jvm/mvn_cmd.rs.
//
// The shared regexes and small predicates live in mvn.go; this file holds the
// stateful pieces: the SurefireBlock block/trail machine, the FailuresSummaryCap
// summary cap, the per-phase filters, and the goal dispatch. rtk's
// tracking/timer side-channels are dropped (gortk is offline by default).
package mvn

import (
	"fmt"
	"os"
	"strings"

	"gortk/internal/core"
)

// ── Surefire block state machine ─────────────────────────────────────────────

// surefireStepKind is the disposition the inner block machine returns for a line.
type surefireStepKind int

const (
	stepConsumed     surefireStepKind = iota // inner machine handled it; outer loop continues
	stepFailingClose                         // a CLOSE with failures/errors; outer loop decides
	stepPassthrough                          // not handled; outer loop applies its own keep logic
)

// surefireStep is the result of SurefireBlock.step.
type surefireStep struct {
	kind    surefireStepKind
	running string // running line, "" == None (only valid when hasRunning)
	hasRun  bool
	lines   []string
	close   string
}

// surefireBlock drives the inner Surefire block + failure-trail behaviour
// shared by filterSurefire and filterPackage. See rtk's SurefireBlock for the
// full contract. block_running == None maps to (running="", hasRun=false).
type surefireBlock struct {
	blockLines   []string
	blockRunning string
	hasRunning   bool
	inBlock      bool
	failureTrail bool
	dropTrail    bool
	// trailRearm: when armed, holds the dropTrail decision so the next per-test
	// subline of the same class inherits it. (nil == None.)
	trailRearm *bool
}

func newSurefireBlock() *surefireBlock { return &surefireBlock{} }

func (b *surefireBlock) step(line string, out *strings.Builder) surefireStep {
	if pluginBanner.MatchString(line) {
		return surefireStep{kind: stepConsumed}
	}

	if running.MatchString(line) {
		if b.inBlock {
			b.flushOpenBlockAsKeep(out)
		}
		b.blockLines = nil
		b.blockRunning = line
		b.hasRunning = true
		b.inBlock = true
		b.failureTrail = false
		b.trailRearm = nil
		return surefireStep{kind: stepConsumed}
	}

	if b.inBlock {
		if caps := closeRE.FindStringSubmatch(line); caps != nil {
			fail := caps[1] != "0"
			err := caps[2] != "0"
			if fail || err {
				lines := b.blockLines
				b.blockLines = nil
				running := b.blockRunning
				hasRun := b.hasRunning
				b.blockRunning = ""
				b.hasRunning = false
				b.inBlock = false
				return surefireStep{
					kind:    stepFailingClose,
					running: running,
					hasRun:  hasRun,
					lines:   lines,
					close:   line,
				}
			}
			b.blockLines = nil
			b.blockRunning = ""
			b.hasRunning = false
			b.inBlock = false
			return surefireStep{kind: stepConsumed}
		}
		b.blockLines = append(b.blockLines, line)
		return surefireStep{kind: stepConsumed}
	}

	if b.failureTrail {
		if line == "" {
			if !b.dropTrail {
				out.WriteByte('\n')
			}
			rearm := b.dropTrail
			b.trailRearm = &rearm
			b.failureTrail = false
			b.dropTrail = false
			return surefireStep{kind: stepConsumed}
		}
		t := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(t, "at ") && isFrameworkFrame(t) {
			return surefireStep{kind: stepConsumed}
		}
		if b.dropTrail {
			return surefireStep{kind: stepConsumed}
		}
		out.WriteString(line)
		out.WriteByte('\n')
		return surefireStep{kind: stepConsumed}
	}

	if b.trailRearm != nil {
		dropped := *b.trailRearm
		if line == "" {
			// Tolerate extra blanks between per-test blocks: stay armed.
			return surefireStep{kind: stepPassthrough}
		}
		b.trailRearm = nil // disarm unconditionally on non-blank
		if isPerTestSubline(line) {
			b.failureTrail = true
			b.dropTrail = dropped
			if !dropped {
				out.WriteString(line)
				out.WriteByte('\n')
			}
			return surefireStep{kind: stepConsumed}
		}
		// Non-subline: trail is over; already disarmed — fall through.
	}

	return surefireStep{kind: stepPassthrough}
}

func (b *surefireBlock) dropFailing() {
	b.failureTrail = true
	b.dropTrail = true
	b.trailRearm = nil
}

func (b *surefireBlock) commitFailing(out *strings.Builder, running string, hasRun bool, lines []string, close string) {
	if hasRun {
		out.WriteString(running)
		out.WriteByte('\n')
	}
	for _, l := range lines {
		t := strings.TrimLeft(l, " \t")
		if strings.HasPrefix(t, "at ") && isFrameworkFrame(t) {
			continue
		}
		out.WriteString(l)
		out.WriteByte('\n')
	}
	out.WriteString(close)
	out.WriteByte('\n')
	b.failureTrail = true
	b.trailRearm = nil
}

func (b *surefireBlock) finish(out *strings.Builder) {
	if b.inBlock {
		b.flushOpenBlockAsKeep(out)
	}
}

func (b *surefireBlock) flushOpenBlockAsKeep(out *strings.Builder) {
	if b.hasRunning {
		out.WriteString(b.blockRunning)
		out.WriteByte('\n')
		b.blockRunning = ""
		b.hasRunning = false
	}
	for _, l := range b.blockLines {
		out.WriteString(l)
		out.WriteByte('\n')
	}
	b.blockLines = nil
	b.inBlock = false
}

// ── Failures-summary cap ─────────────────────────────────────────────────────

// failuresSummaryCap caps the `[ERROR] Failures:` summary entries. See rtk's
// FailuresSummaryCap for the contract.
type failuresSummaryCap struct {
	cap       int
	inSummary bool
	emitted   int
	dropped   int
}

func newFailuresSummaryCap(cap int) *failuresSummaryCap {
	return &failuresSummaryCap{cap: cap}
}

func (f *failuresSummaryCap) handleEntry(line string, out *strings.Builder) bool {
	if !f.inSummary || !strings.HasPrefix(line, "[ERROR]   ") {
		return false
	}
	if f.emitted < f.cap {
		out.WriteString(line)
		out.WriteByte('\n')
		f.emitted++
	} else {
		f.dropped++
	}
	return true
}

func (f *failuresSummaryCap) handleHeader(line string) {
	if strings.HasPrefix(line, "[ERROR] Failures:") {
		f.inSummary = true
		f.emitted = 0
		f.dropped = 0
	}
}

func (f *failuresSummaryCap) handleAggregate(line string, out *strings.Builder) {
	if !f.inSummary || !agg.MatchString(line) {
		return
	}
	if f.dropped > 0 {
		fmt.Fprintf(out, "\n… +%d more failures\n", f.dropped)
	}
	f.inSummary = false
	f.emitted = 0
	f.dropped = 0
}

func (f *failuresSummaryCap) finish(out *strings.Builder) {
	if f.inSummary && f.dropped > 0 {
		fmt.Fprintf(out, "\n… +%d more failures\n", f.dropped)
	}
}

// ── Surefire filter ──────────────────────────────────────────────────────────

// filterSurefire is the buffered filter for `mvn test` / `integration-test`.
// Faithful port of rtk's filter_surefire.
func filterSurefire(raw string) string {
	return filterSurefireWithCap(raw, maxMvnFailingClasses)
}

func filterSurefireWithCap(raw string, cap int) string {
	stripped := core.StripANSI(raw)
	if !hasEnglishFooter(stripped) {
		return stripped
	}

	var out strings.Builder
	block := newSurefireBlock()
	keepContinuation := false
	inReactorSummary := false
	emittedFailing := 0
	droppedFailing := 0
	summary := newFailuresSummaryCap(cap)

	for _, line := range splitLines(stripped) {
		step := block.step(line, &out)
		switch step.kind {
		case stepConsumed:
			continue
		case stepFailingClose:
			if emittedFailing < cap {
				block.commitFailing(&out, step.running, step.hasRun, step.lines, step.close)
				emittedFailing++
			} else {
				block.dropFailing()
				droppedFailing++
			}
			keepContinuation = false
			continue
		case stepPassthrough:
		}

		if keepContinuation && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}

		if summary.handleEntry(line, &out) {
			continue
		}

		// Order matters: call reactorSummaryKeep first so its buildFoot
		// clears-flag side effect always runs regardless of `||` short-circuit.
		reactorKeep := reactorSummaryKeep(line, &inReactorSummary)
		if reactorKeep || keepOutsideBlock(line) {
			summary.handleAggregate(line, &out)
			summary.handleHeader(line)
			out.WriteString(line)
			out.WriteByte('\n')
			keepContinuation = strings.HasPrefix(line, "[ERROR]") &&
				!strings.HasPrefix(line, "[ERROR] Tests run:") &&
				!strings.HasPrefix(line, "[ERROR] Failures:") &&
				!strings.HasPrefix(line, "[ERROR] Errors:")
			continue
		}
		keepContinuation = false
	}

	block.finish(&out)
	summary.finish(&out)
	if droppedFailing > 0 {
		fmt.Fprintf(&out, "\n… +%d more failing test classes\n", droppedFailing)
	}
	return out.String()
}

// ── Compile filter ───────────────────────────────────────────────────────────

// filterCompile is the buffered filter for `mvn compile` / `test-compile`.
// Faithful port of rtk's filter_compile.
func filterCompile(raw string) string {
	stripped := core.StripANSI(raw)
	if !hasEnglishFooter(stripped) {
		return stripped
	}

	var out strings.Builder
	keepContinuation := false
	seenWarnings := map[string]struct{}{}

	for _, line := range splitLines(stripped) {
		if moduleBanner.MatchString(line) {
			out.WriteString(line)
			out.WriteByte('\n')
			keepContinuation = false
			continue
		}
		if buildFoot.MatchString(line) ||
			strings.HasPrefix(line, "[INFO] Building ") ||
			strings.HasPrefix(line, "[INFO] Total time:") ||
			strings.HasPrefix(line, "[INFO] Finished at:") ||
			strings.HasPrefix(line, "[INFO] Scanning ") {
			out.WriteString(line)
			out.WriteByte('\n')
			keepContinuation = false
			continue
		}
		if isBoilerplate(line) {
			keepContinuation = false
			continue
		}
		if strings.HasPrefix(line, "[ERROR]") {
			out.WriteString(line)
			out.WriteByte('\n')
			keepContinuation = true
			continue
		}
		if keepContinuation && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}
		if strings.HasPrefix(line, "[WARNING]") {
			payload := strings.TrimPrefix(line, "[WARNING] ")
			norm := fileCoord.ReplaceAllString(payload, "")
			if _, seen := seenWarnings[norm]; !seen {
				seenWarnings[norm] = struct{}{}
				out.WriteString(line)
				out.WriteByte('\n')
			}
			keepContinuation = false
			continue
		}
		keepContinuation = false
	}

	return out.String()
}

// ── Package filter ───────────────────────────────────────────────────────────

// filterPackage is the buffered filter for
// `mvn package`/`install`/`verify`/`deploy`. Faithful port of rtk's
// filter_package.
func filterPackage(raw string) string {
	return filterPackageWithCap(raw, maxMvnFailingClasses)
}

func filterPackageWithCap(raw string, cap int) string {
	stripped := core.StripANSI(raw)
	if !hasEnglishFooter(stripped) {
		return stripped
	}

	var out strings.Builder
	block := newSurefireBlock()
	keepContinuation := false
	inReactorSummary := false
	seenWarnings := map[string]struct{}{}
	emittedFailing := 0
	droppedFailing := 0
	summary := newFailuresSummaryCap(cap)

	for _, line := range splitLines(stripped) {
		step := block.step(line, &out)
		switch step.kind {
		case stepConsumed:
			continue
		case stepFailingClose:
			if emittedFailing < cap {
				block.commitFailing(&out, step.running, step.hasRun, step.lines, step.close)
				emittedFailing++
			} else {
				block.dropFailing()
				droppedFailing++
			}
			keepContinuation = false
			continue
		case stepPassthrough:
		}

		if summary.handleEntry(line, &out) {
			continue
		}

		reactorKeep := reactorSummaryKeep(line, &inReactorSummary)
		if reactorKeep || moduleBanner.MatchString(line) || keepOutsideBlock(line) {
			summary.handleAggregate(line, &out)
			summary.handleHeader(line)
			out.WriteString(line)
			out.WriteByte('\n')
			keepContinuation = strings.HasPrefix(line, "[ERROR]") &&
				!strings.HasPrefix(line, "[ERROR] Tests run:") &&
				!strings.HasPrefix(line, "[ERROR] Failures:") &&
				!strings.HasPrefix(line, "[ERROR] Errors:")
			continue
		}
		if keepContinuation && (strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t")) {
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}
		if strings.HasPrefix(line, "[WARNING]") {
			payload := strings.TrimPrefix(line, "[WARNING] ")
			norm := fileCoord.ReplaceAllString(payload, "")
			if _, seen := seenWarnings[norm]; !seen {
				seenWarnings[norm] = struct{}{}
				out.WriteString(line)
				out.WriteByte('\n')
			}
			keepContinuation = false
			continue
		}
		keepContinuation = false
	}

	block.finish(&out)
	summary.finish(&out)
	if droppedFailing > 0 {
		fmt.Fprintf(&out, "\n… +%d more failing test classes\n", droppedFailing)
	}
	return out.String()
}

// ── Quiet-mode filter ────────────────────────────────────────────────────────

// filterQuiet is the filter for `mvn -q` invocations. Faithful port of rtk's
// filter_quiet.
func filterQuiet(raw string) string {
	stripped := core.StripANSI(raw)
	if strings.TrimSpace(stripped) == "" {
		return ""
	}

	var out strings.Builder
	failureTrail := false

	for _, line := range splitLines(stripped) {
		if closeRE.MatchString(line) {
			out.WriteString(line)
			out.WriteByte('\n')
			failureTrail = strings.Contains(line, "<<< FAILURE!") || strings.Contains(line, "<<< ERROR!")
			continue
		}

		if isPerTestSubline(line) {
			out.WriteString(line)
			out.WriteByte('\n')
			failureTrail = true
			continue
		}

		if failureTrail {
			if strings.TrimSpace(line) == "" {
				out.WriteByte('\n')
				failureTrail = false
				continue
			}
			t := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(t, "at ") && isFrameworkFrame(t) {
				continue
			}
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}

		if strings.HasPrefix(line, "[ERROR] Tests run:") ||
			strings.HasPrefix(line, "[ERROR] Failures:") ||
			strings.HasPrefix(line, "[ERROR] Errors:") ||
			strings.HasPrefix(line, "[ERROR]   ") ||
			strings.HasPrefix(line, "[ERROR] Failed to execute goal") {
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		}

		if isBoilerplate(line) {
			continue
		}

		out.WriteString(line)
		out.WriteByte('\n')
	}

	return out.String()
}

// ── Wrapper detection ────────────────────────────────────────────────────────

// mvnBinary returns the Maven binary to invoke, preferring a project-local
// `.\mvnw.cmd` wrapper on Windows. Mirrors rtk's mvn_binary.
func mvnBinary() string {
	if fileExistsLocal(".\\mvnw.cmd") {
		return ".\\mvnw.cmd"
	}
	return "mvn"
}

func fileExistsLocal(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ── Entry point ──────────────────────────────────────────────────────────────

// Run dispatches the gortk `mvn` command. args are the tokens after "mvn".
// Goals are passed as args; Run detects the lifecycle phase (test/compile/
// package/passthrough), then routes to the matching filter or a verbatim
// passthrough. Faithful port of rtk's run.
func Run(args []string, verbose int) (int, error) {
	// Verbose/debug flags bypass filtering — user wants full output.
	for _, a := range args {
		switch a {
		case "-X", "--debug", "-e", "--errors":
			return runPassthrough(args, verbose)
		}
	}

	argsDisplay := strings.Join(args, " ")

	// Quiet mode: the English-footer guard can't fire (no `BUILD SUCCESS`
	// under `-q`). Route non-passthrough phases to filterQuiet.
	if isQuiet(args) {
		if detectPhase(args) == PhasePassthrough {
			return runPassthrough(args, verbose)
		}
		return runMvnFiltered(args, argsDisplay, "mvn_quiet", filterQuiet, verbose)
	}

	switch detectPhase(args) {
	case PhaseTest:
		return runMvnFiltered(args, argsDisplay, "mvn_test", filterSurefire, verbose)
	case PhaseCompile:
		return runMvnFiltered(args, argsDisplay, "mvn_compile", filterCompile, verbose)
	case PhasePackage:
		return runMvnFiltered(args, argsDisplay, "mvn_package", filterPackage, verbose)
	default:
		return runPassthrough(args, verbose)
	}
}

// runMvnFiltered runs `mvn <args...>`, captures the output, and compresses it
// with filterFn. Mirrors rtk's runner::run_filtered call sites. A project-local
// `.\mvnw.cmd` wrapper is preferred over `mvn` on PATH (rtk's new_mvn_command).
func runMvnFiltered(args []string, argsDisplay, teeLabel string, filterFn func(string) string, verbose int) (int, error) {
	tool := mvnBinary()
	cmd := core.ResolvedCommand(tool, args...)
	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: %s %s\n", tool, argsDisplay)
	}
	opts := core.RunOptions{TeeLabel: teeLabel}
	return core.RunFiltered(cmd, "mvn", argsDisplay, filterFn, opts)
}

// runPassthrough streams `mvn <args...>` verbatim. Mirrors rtk's
// runner::run_passthrough.
func runPassthrough(args []string, verbose int) (int, error) {
	tool := mvnBinary()
	return core.RunPassthrough(tool, args, verbose)
}
