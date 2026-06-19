// Package playwright is gortk's token-optimized wrapper around Playwright E2E
// test runs. It forces the Playwright JSON reporter, parses the result, and
// emits a compact pass/fail summary with only failing-test detail. Faithful
// port of rtk's src/cmds/js/playwright_cmd.rs.
//
// Like rtk, this never runs `which playwright` (which can resolve pyenv shims
// or other non-Node binaries); it always goes through the detected Node package
// manager (pnpm / yarn / npx). gortk resolves those PATHEXT-aware, so the .cmd
// shims that npm installs on Windows are found transparently.
package playwright

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "playwright",
		Summary: "Run Playwright E2E tests with compact failure-only output",
		Run:     Run,
	})
}

// passthroughMaxChars mirrors rtk's config limits().passthrough_max_chars.
const passthroughMaxChars = 2000

// Run executes the playwright command. args are the arguments after the command
// name; verbose is the -v count.
func Run(args []string, verbose int) (int, error) {
	cmd := buildCommand(args)

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: playwright %s\n", strings.Join(args, " "))
	}

	argsDisplay := strings.Join(args, " ")
	mode := formatModeFromVerbosity(verbose)

	// rtk parses result.stdout but tees raw = stdout + "\n" + stderr. We filter
	// stdout only (stderr is passed through verbatim by the runner) and let the
	// runner own the tee hint via TeeLabel.
	opts := core.RunOptions{FilterStdoutOnly: true, TeeLabel: "playwright"}
	return core.RunFiltered(cmd, "playwright", argsDisplay, func(raw string) string {
		res := parsePlaywright(raw)
		switch res.tier {
		case tierFull:
			if verbose > 0 {
				fmt.Fprintln(os.Stderr, "playwright test (Tier 1: Full JSON parse)")
			}
			return formatTestResult(res.data, mode)
		case tierDegraded:
			if verbose > 0 {
				fmt.Fprintf(os.Stderr, "[gortk:DEGRADED] playwright parser: %s\n", strings.Join(res.warnings, ", "))
			}
			return formatTestResult(res.data, mode)
		default:
			fmt.Fprintf(os.Stderr, "[gortk:PASSTHROUGH] playwright parser: %s\n", "All parsing tiers failed")
			return res.passthrough
		}
	}, opts)
}

// buildCommand assembles the package-manager-fronted playwright invocation,
// injecting --reporter=json for `playwright test` runs (stripping any user
// --reporter to avoid conflicts). Mirrors rtk's run().
func buildCommand(args []string) *exec.Cmd {
	pm := detectPackageManager()
	var cmd *exec.Cmd
	switch pm {
	case "pnpm":
		cmd = core.ResolvedCommand("pnpm", "exec", "--", "playwright")
	case "yarn":
		cmd = core.ResolvedCommand("yarn", "exec", "--", "playwright")
	default:
		cmd = core.ResolvedCommand("npx", "--no-install", "--", "playwright")
	}

	isTest := len(args) > 0 && args[0] == "test"
	if isTest {
		cmd.Args = append(cmd.Args, "test", "--reporter=json")
		for _, a := range args[1:] {
			if !strings.HasPrefix(a, "--reporter") {
				cmd.Args = append(cmd.Args, a)
			}
		}
	} else {
		cmd.Args = append(cmd.Args, args...)
	}
	return cmd
}

// detectPackageManager mirrors rtk's detect_package_manager: lockfile sniffing
// in the current working directory.
func detectPackageManager() string {
	if fileExists("pnpm-lock.yaml") {
		return "pnpm"
	}
	if fileExists("yarn.lock") {
		return "yarn"
	}
	return "npm"
}

func fileExists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}

// ---- parsing ---------------------------------------------------------------

type parseTier int

const (
	tierFull parseTier = iota + 1
	tierDegraded
	tierPassthrough
)

// parseOutcome is the Go analogue of rtk's ParseResult<TestResult>.
type parseOutcome struct {
	tier        parseTier
	data        testResult
	warnings    []string
	passthrough string
}

// testResult mirrors parser::types::TestResult.
type testResult struct {
	Total      int
	Passed     int
	Failed     int
	Skipped    int
	DurationMS *uint64
	Failures   []testFailure
}

// testFailure mirrors parser::types::TestFailure.
type testFailure struct {
	TestName     string
	FilePath     string
	ErrorMessage string
	StackTrace   *string
}

// ---- Playwright JSON reporter shapes (only the fields we read) -------------

type pwJSONOutput struct {
	Stats  pwStats   `json:"stats"`
	Suites []pwSuite `json:"suites"`
}

type pwStats struct {
	Expected   int     `json:"expected"`
	Unexpected int     `json:"unexpected"`
	Skipped    int     `json:"skipped"`
	Duration   float64 `json:"duration"`
}

type pwSuite struct {
	Title  string    `json:"title"`
	File   *string   `json:"file"`
	Specs  []pwSpec  `json:"specs"`
	Suites []pwSuite `json:"suites"`
}

type pwSpec struct {
	Title string        `json:"title"`
	Ok    bool          `json:"ok"`
	Tests []pwExecution `json:"tests"`
}

type pwExecution struct {
	Status  string      `json:"status"`
	Results []pwAttempt `json:"results"`
}

type pwAttempt struct {
	Status string    `json:"status"`
	Errors []pwError `json:"errors"`
}

type pwError struct {
	Message string `json:"message"`
}

// parsePlaywright runs the three-tier parse strategy on Playwright's stdout.
func parsePlaywright(input string) parseOutcome {
	// Tier 1: strict JSON parsing.
	var jsonOut pwJSONOutput
	dec := json.NewDecoder(strings.NewReader(input))
	if err := dec.Decode(&jsonOut); err == nil {
		var failures []testFailure
		total := 0
		collectTestResults(jsonOut.Suites, &total, &failures)

		dur := uint64(jsonOut.Stats.Duration)
		return parseOutcome{
			tier: tierFull,
			data: testResult{
				Total:      total,
				Passed:     jsonOut.Stats.Expected,
				Failed:     jsonOut.Stats.Unexpected,
				Skipped:    jsonOut.Stats.Skipped,
				DurationMS: &dur,
				Failures:   failures,
			},
		}
	} else {
		// Tier 2: regex extraction.
		if data, ok := extractPlaywrightRegex(input); ok {
			return parseOutcome{
				tier:     tierDegraded,
				data:     data,
				warnings: []string{fmt.Sprintf("JSON parse failed: %v", err)},
			}
		}
		// Tier 3: passthrough.
		return parseOutcome{tier: tierPassthrough, passthrough: truncatePassthrough(input)}
	}
}

// collectTestResults walks the suite tree, counting specs and gathering
// failures. Mirrors rtk's collect_test_results.
func collectTestResults(suites []pwSuite, total *int, failures *[]testFailure) {
	for i := range suites {
		suite := &suites[i]
		filePath := suite.Title
		if suite.File != nil {
			filePath = *suite.File
		}

		for _, spec := range suite.Specs {
			*total++

			if !spec.Ok {
				errorMsg := "Test failed"
				for _, t := range spec.Tests {
					if t.Status != "unexpected" {
						continue
					}
					for _, r := range t.Results {
						if r.Status == "failed" || r.Status == "timedOut" {
							if len(r.Errors) > 0 {
								errorMsg = r.Errors[0].Message
							}
							break
						}
					}
					break
				}
				*failures = append(*failures, testFailure{
					TestName:     spec.Title,
					FilePath:     filePath,
					ErrorMessage: errorMsg,
				})
			}
		}

		collectTestResults(suite.Suites, total, failures)
	}
}

var (
	summaryRE  = regexp.MustCompile(`(\d+)\s+(passed|failed|flaky|skipped)`)
	durationRE = regexp.MustCompile(`\((\d+(?:\.\d+)?)(ms|s|m)\)`)
	// testPatternRE matches a failing-test line in Playwright's text reporter.
	testPatternRE = regexp.MustCompile(`[×✗]\s+.*?›\s+([^›]+\.spec\.[tj]sx?)`)
)

// extractPlaywrightRegex is Tier 2: pull stats out of the text reporter.
// Mirrors rtk's extract_playwright_regex; returns (result, true) only when a
// non-zero total was found.
func extractPlaywrightRegex(output string) (testResult, bool) {
	clean := core.StripANSI(output)

	passed, failed, skipped := 0, 0, 0
	for _, m := range summaryRE.FindAllStringSubmatch(clean, -1) {
		count, _ := strconv.Atoi(m[1])
		switch m[2] {
		case "passed":
			passed = count
		case "failed":
			failed = count
		case "skipped":
			skipped = count
		}
	}

	var durationMS *uint64
	if m := durationRE.FindStringSubmatch(clean); m != nil {
		if value, err := strconv.ParseFloat(m[1], 64); err == nil {
			var ms uint64
			switch m[2] {
			case "ms":
				ms = uint64(value)
			case "s":
				ms = uint64(value * 1000.0)
			case "m":
				ms = uint64(value * 60000.0)
			default:
				ms = uint64(value)
			}
			durationMS = &ms
		}
	}

	total := passed + failed + skipped
	if total <= 0 {
		return testResult{}, false
	}
	return testResult{
		Total:      total,
		Passed:     passed,
		Failed:     failed,
		Skipped:    skipped,
		DurationMS: durationMS,
		Failures:   extractFailuresRegex(clean),
	}, true
}

// extractFailuresRegex pulls failing test entries from text output.
func extractFailuresRegex(output string) []testFailure {
	var failures []testFailure
	for _, m := range testPatternRE.FindAllStringSubmatch(output, -1) {
		failures = append(failures, testFailure{
			TestName:     m[0],
			FilePath:     m[1],
			ErrorMessage: "",
		})
	}
	return failures
}

// truncatePassthrough truncates raw output to the configured limit, appending a
// passthrough marker. Operates on runes (not bytes) to stay UTF-8 safe, matching
// rtk's char-based truncation.
func truncatePassthrough(output string) string {
	return truncateOutput(output, passthroughMaxChars)
}

func truncateOutput(output string, maxChars int) string {
	runes := []rune(output)
	if len(runes) <= maxChars {
		return output
	}
	truncated := string(runes[:maxChars])
	return fmt.Sprintf("%s\n\n[gortk:PASSTHROUGH] Output truncated (%d chars → %d chars)",
		truncated, len(runes), maxChars)
}
