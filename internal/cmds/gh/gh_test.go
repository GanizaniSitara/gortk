package gh

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// mustJSON parses a JSON literal into a generic value for the formatter tests.
func mustJSON(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("bad test JSON: %v", err)
	}
	return v
}

// --- truncate ----------------------------------------------------------------

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Errorf("truncate short = %q", got)
	}
	if got := truncate("this is a very long string", 15); got != "this is a ve..." {
		t.Errorf("truncate long = %q", got)
	}
}

func TestTruncateMultibyteUTF8(t *testing.T) {
	cases := []struct {
		in     string
		maxLen int
		want   string
	}{
		{"🚀🎉🔥abc", 6, "🚀🎉🔥abc"},      // 6 chars, fits
		{"🚀🎉🔥abcdef", 8, "🚀🎉🔥ab..."}, // 9 chars > 8
		{"🚀🎉🔥🌟🎯", 5, "🚀🎉🔥🌟🎯"},      // exact fit
		{"🚀🎉🔥🌟🎯x", 5, "🚀🎉..."},      // 6 chars > 5
	}
	for _, c := range cases {
		if got := truncate(c.in, c.maxLen); got != c.want {
			t.Errorf("truncate(%q,%d) = %q, want %q", c.in, c.maxLen, got, c.want)
		}
	}
}

func TestTruncateEmptyAndShort(t *testing.T) {
	if got := truncate("", 10); got != "" {
		t.Errorf("got %q", got)
	}
	if got := truncate("ab", 10); got != "ab" {
		t.Errorf("got %q", got)
	}
	if got := truncate("abc", 3); got != "abc" {
		t.Errorf("got %q", got)
	}
}

// --- ok_confirmation ---------------------------------------------------------

func TestOkConfirmationPRCreate(t *testing.T) {
	result := okConfirmation("created", "#42 https://github.com/foo/bar/pull/42")
	if !strings.Contains(result, "ok created") || !strings.Contains(result, "#42") {
		t.Errorf("got %q", result)
	}
}

func TestOkConfirmationPRMerge(t *testing.T) {
	if got := okConfirmation("merged", "#42"); got != "ok merged #42" {
		t.Errorf("got %q", got)
	}
}

func TestOkConfirmationPRComment(t *testing.T) {
	if got := okConfirmation("commented", "#42"); got != "ok commented #42" {
		t.Errorf("got %q", got)
	}
}

func TestOkConfirmationPREdit(t *testing.T) {
	if got := okConfirmation("edited", "#42"); got != "ok edited #42" {
		t.Errorf("got %q", got)
	}
}

// --- has_json_flag -----------------------------------------------------------

func TestHasJSONFlagPresent(t *testing.T) {
	if !hasJSONFlag([]string{"view", "--json", "number,url"}) {
		t.Error("expected true")
	}
}

func TestHasJSONFlagAbsent(t *testing.T) {
	if hasJSONFlag([]string{"view", "42"}) {
		t.Error("expected false")
	}
}

// --- extract_identifier_and_extra_args ---------------------------------------

func TestExtractIdentifierSimple(t *testing.T) {
	id, extra, ok := extractIdentifierAndExtraArgs([]string{"123"})
	if !ok || id != "123" || len(extra) != 0 {
		t.Errorf("got id=%q extra=%v ok=%v", id, extra, ok)
	}
}

func TestExtractIdentifierWithRepoFlagAfter(t *testing.T) {
	id, extra, ok := extractIdentifierAndExtraArgs([]string{"185", "-R", "rtk-ai/rtk"})
	if !ok || id != "185" || !reflect.DeepEqual(extra, []string{"-R", "rtk-ai/rtk"}) {
		t.Errorf("got id=%q extra=%v ok=%v", id, extra, ok)
	}
}

func TestExtractIdentifierWithRepoFlagBefore(t *testing.T) {
	id, extra, ok := extractIdentifierAndExtraArgs([]string{"-R", "rtk-ai/rtk", "185"})
	if !ok || id != "185" || !reflect.DeepEqual(extra, []string{"-R", "rtk-ai/rtk"}) {
		t.Errorf("got id=%q extra=%v ok=%v", id, extra, ok)
	}
}

func TestExtractIdentifierWithLongRepoFlag(t *testing.T) {
	id, extra, ok := extractIdentifierAndExtraArgs([]string{"42", "--repo", "owner/repo"})
	if !ok || id != "42" || !reflect.DeepEqual(extra, []string{"--repo", "owner/repo"}) {
		t.Errorf("got id=%q extra=%v ok=%v", id, extra, ok)
	}
}

func TestExtractIdentifierEmpty(t *testing.T) {
	if _, _, ok := extractIdentifierAndExtraArgs([]string{}); ok {
		t.Error("expected no identifier")
	}
}

func TestExtractIdentifierOnlyFlags(t *testing.T) {
	if _, _, ok := extractIdentifierAndExtraArgs([]string{"-R", "rtk-ai/rtk"}); ok {
		t.Error("expected no identifier")
	}
}

func TestExtractIdentifierWithWebFlag(t *testing.T) {
	id, extra, ok := extractIdentifierAndExtraArgs([]string{"123", "--web"})
	if !ok || id != "123" || !reflect.DeepEqual(extra, []string{"--web"}) {
		t.Errorf("got id=%q extra=%v ok=%v", id, extra, ok)
	}
}

func TestExtractIdentifierWithJobFlagAfter(t *testing.T) {
	id, extra, ok := extractIdentifierAndExtraArgs([]string{"12345", "--job", "67890"})
	if !ok || id != "12345" || !reflect.DeepEqual(extra, []string{"--job", "67890"}) {
		t.Errorf("got id=%q extra=%v ok=%v", id, extra, ok)
	}
}

func TestExtractIdentifierWithJobFlagBefore(t *testing.T) {
	id, extra, ok := extractIdentifierAndExtraArgs([]string{"--job", "67890", "12345"})
	if !ok || id != "12345" || !reflect.DeepEqual(extra, []string{"--job", "67890"}) {
		t.Errorf("got id=%q extra=%v ok=%v", id, extra, ok)
	}
}

func TestExtractIdentifierWithJobAndLogFailed(t *testing.T) {
	id, extra, ok := extractIdentifierAndExtraArgs([]string{"--log-failed", "--job", "67890", "12345"})
	if !ok || id != "12345" || !reflect.DeepEqual(extra, []string{"--log-failed", "--job", "67890"}) {
		t.Errorf("got id=%q extra=%v ok=%v", id, extra, ok)
	}
}

func TestExtractIdentifierWithAttemptFlag(t *testing.T) {
	id, extra, ok := extractIdentifierAndExtraArgs([]string{"12345", "--attempt", "3"})
	if !ok || id != "12345" || !reflect.DeepEqual(extra, []string{"--attempt", "3"}) {
		t.Errorf("got id=%q extra=%v ok=%v", id, extra, ok)
	}
}

// --- parse_optional_identifier -----------------------------------------------

func TestParseOptionalIdentifierEmptyYieldsNoID(t *testing.T) {
	id, hasID, extra := parseOptionalIdentifier(nil)
	if hasID || id != "" || len(extra) != 0 {
		t.Errorf("got id=%q hasID=%v extra=%v", id, hasID, extra)
	}
}

func TestParseOptionalIdentifierOnlyFlagsPreservesFlags(t *testing.T) {
	id, hasID, extra := parseOptionalIdentifier([]string{"-R", "rtk-ai/rtk"})
	if hasID || id != "" || !reflect.DeepEqual(extra, []string{"-R", "rtk-ai/rtk"}) {
		t.Errorf("got id=%q hasID=%v extra=%v", id, hasID, extra)
	}
}

func TestParseOptionalIdentifierWithIDMatchesExtract(t *testing.T) {
	id, hasID, extra := parseOptionalIdentifier([]string{"-R", "rtk-ai/rtk", "42"})
	if !hasID || id != "42" || !reflect.DeepEqual(extra, []string{"-R", "rtk-ai/rtk"}) {
		t.Errorf("got id=%q hasID=%v extra=%v", id, hasID, extra)
	}
}

// --- should_passthrough_run_view ---------------------------------------------

func TestRunViewPassthroughLogFailed(t *testing.T) {
	if !shouldPassthroughRunView([]string{"--log-failed"}) {
		t.Error("expected true")
	}
}

func TestRunViewPassthroughLog(t *testing.T) {
	if !shouldPassthroughRunView([]string{"--log"}) {
		t.Error("expected true")
	}
}

func TestRunViewPassthroughJSON(t *testing.T) {
	if !shouldPassthroughRunView([]string{"--json", "jobs"}) {
		t.Error("expected true")
	}
}

func TestRunViewNoPassthroughEmpty(t *testing.T) {
	if shouldPassthroughRunView(nil) {
		t.Error("expected false")
	}
}

func TestRunViewNoPassthroughOtherFlags(t *testing.T) {
	if shouldPassthroughRunView([]string{"--web"}) {
		t.Error("expected false")
	}
}

// --- format_run_view ---------------------------------------------------------

func TestFormatRunViewWithID(t *testing.T) {
	out := formatRunView("", "12345")
	if !strings.HasPrefix(out, "Workflow Run #12345\n") {
		t.Errorf("got %q", out)
	}
}

func TestFormatRunViewWithoutID(t *testing.T) {
	out := formatRunView("", "")
	if !strings.HasPrefix(out, "Workflow Run\n") {
		t.Errorf("got %q", out)
	}
	if strings.Contains(out, "#\n") {
		t.Errorf("should not render empty #: %q", out)
	}
}

// --- should_passthrough_pr_view ----------------------------------------------

func TestShouldPassthroughPRViewJSON(t *testing.T) {
	if !shouldPassthroughPRView([]string{"--json", "body,comments"}) {
		t.Error("expected true")
	}
}

func TestShouldPassthroughPRViewJQ(t *testing.T) {
	if !shouldPassthroughPRView([]string{"--jq", ".body"}) {
		t.Error("expected true")
	}
}

func TestShouldPassthroughPRViewWeb(t *testing.T) {
	if !shouldPassthroughPRView([]string{"--web"}) {
		t.Error("expected true")
	}
}

func TestShouldPassthroughPRViewDefault(t *testing.T) {
	if shouldPassthroughPRView(nil) {
		t.Error("expected false")
	}
}

func TestShouldPassthroughPRViewComments(t *testing.T) {
	if !shouldPassthroughPRView([]string{"--comments"}) {
		t.Error("expected true")
	}
}

// --- should_passthrough_pr_status --------------------------------------------

func TestShouldPassthroughPRStatusHelp(t *testing.T) {
	if !shouldPassthroughPRStatus([]string{"--help"}) || !shouldPassthroughPRStatus([]string{"-h"}) {
		t.Error("expected true")
	}
}

func TestShouldPassthroughPRStatusOutputTransformFlags(t *testing.T) {
	if !shouldPassthroughPRStatus([]string{"--web"}) {
		t.Error("--web expected true")
	}
	if !shouldPassthroughPRStatus([]string{"--jq", ".currentBranch"}) {
		t.Error("--jq expected true")
	}
	if !shouldPassthroughPRStatus([]string{"--template", "{{.currentBranch.title}}"}) {
		t.Error("--template expected true")
	}
}

func TestShouldPassthroughPRStatusRepoFlagStaysFiltered(t *testing.T) {
	if shouldPassthroughPRStatus([]string{"-R", "owner/repo"}) {
		t.Error("expected false")
	}
}

func TestPRStatusJSONFieldsExcludesCurrentBranch(t *testing.T) {
	fields := prStatusJSONFields()
	if strings.Contains(fields, "currentBranch") {
		t.Error("should not contain currentBranch")
	}
	for _, want := range []string{"number", "title", "reviewDecision", "statusCheckRollup"} {
		if !strings.Contains(fields, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestFormatPRStatusIncludesCurrentBranchSummary(t *testing.T) {
	json := mustJSON(t, `{
		"currentBranch": {
			"number": 934,
			"title": "fix wrappers for standardization and exit codes",
			"reviewDecision": "CHANGES_REQUESTED",
			"statusCheckRollup": [
				{"conclusion": "SUCCESS"},
				{"state": "SUCCESS"},
				{"conclusion": "FAILURE"}
			]
		},
		"createdBy": []
	}`)
	result := formatPRStatus(json)
	for _, want := range []string{"Current Branch", "#934", "CHANGES_REQUESTED", "checks 2/3", "fail 1"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q in:\n%s", want, result)
		}
	}
}

// --- should_passthrough_issue_view -------------------------------------------

func TestShouldPassthroughIssueViewComments(t *testing.T) {
	if !shouldPassthroughIssueView([]string{"--comments"}) {
		t.Error("expected true")
	}
}

func TestShouldPassthroughIssueViewJSON(t *testing.T) {
	if !shouldPassthroughIssueView([]string{"--json", "body,comments"}) {
		t.Error("expected true")
	}
}

func TestShouldPassthroughIssueViewJQ(t *testing.T) {
	if !shouldPassthroughIssueView([]string{"--jq", ".body"}) {
		t.Error("expected true")
	}
}

func TestShouldPassthroughIssueViewWeb(t *testing.T) {
	if !shouldPassthroughIssueView([]string{"--web"}) {
		t.Error("expected true")
	}
}

func TestShouldPassthroughIssueViewDefault(t *testing.T) {
	if shouldPassthroughIssueView(nil) {
		t.Error("expected false")
	}
}

// --- has_non_diff_format_flag ------------------------------------------------

func TestNonDiffFormatFlagNameOnly(t *testing.T) {
	if !hasNonDiffFormatFlag([]string{"--name-only"}) {
		t.Error("expected true")
	}
}

func TestNonDiffFormatFlagStat(t *testing.T) {
	if !hasNonDiffFormatFlag([]string{"--stat"}) {
		t.Error("expected true")
	}
}

func TestNonDiffFormatFlagNameStatus(t *testing.T) {
	if !hasNonDiffFormatFlag([]string{"--name-status"}) {
		t.Error("expected true")
	}
}

func TestNonDiffFormatFlagNumstat(t *testing.T) {
	if !hasNonDiffFormatFlag([]string{"--numstat"}) {
		t.Error("expected true")
	}
}

func TestNonDiffFormatFlagShortstat(t *testing.T) {
	if !hasNonDiffFormatFlag([]string{"--shortstat"}) {
		t.Error("expected true")
	}
}

func TestNonDiffFormatFlagAbsent(t *testing.T) {
	if hasNonDiffFormatFlag(nil) {
		t.Error("expected false")
	}
}

func TestNonDiffFormatFlagRegularArgs(t *testing.T) {
	if hasNonDiffFormatFlag([]string{"123", "--color=always"}) {
		t.Error("expected false")
	}
}

// --- filter_markdown_body ----------------------------------------------------

func TestFilterMarkdownBodyHTMLCommentSingleLine(t *testing.T) {
	result := filterMarkdownBody("Hello\n<!-- this is a comment -->\nWorld")
	if strings.Contains(result, "<!--") {
		t.Errorf("should strip comment: %q", result)
	}
	if !strings.Contains(result, "Hello") || !strings.Contains(result, "World") {
		t.Errorf("missing content: %q", result)
	}
}

func TestFilterMarkdownBodyHTMLCommentMultiline(t *testing.T) {
	result := filterMarkdownBody("Before\n<!--\nmultiline\ncomment\n-->\nAfter")
	if strings.Contains(result, "<!--") || strings.Contains(result, "multiline") {
		t.Errorf("should strip multiline comment: %q", result)
	}
	if !strings.Contains(result, "Before") || !strings.Contains(result, "After") {
		t.Errorf("missing content: %q", result)
	}
}

func TestFilterMarkdownBodyBadgeLines(t *testing.T) {
	input := "# Title\n[![CI](https://img.shields.io/badge.svg)](https://github.com/actions)\nSome text"
	result := filterMarkdownBody(input)
	if strings.Contains(result, "shields.io") {
		t.Errorf("should strip badge: %q", result)
	}
	if !strings.Contains(result, "# Title") || !strings.Contains(result, "Some text") {
		t.Errorf("missing content: %q", result)
	}
}

func TestFilterMarkdownBodyImageOnlyLines(t *testing.T) {
	input := "# Title\n![screenshot](https://example.com/img.png)\nSome text"
	result := filterMarkdownBody(input)
	if strings.Contains(result, "![screenshot]") {
		t.Errorf("should strip image: %q", result)
	}
	if !strings.Contains(result, "# Title") || !strings.Contains(result, "Some text") {
		t.Errorf("missing content: %q", result)
	}
}

func TestFilterMarkdownBodyHorizontalRules(t *testing.T) {
	input := "Section 1\n---\nSection 2\n***\nSection 3\n___\nEnd"
	result := filterMarkdownBody(input)
	if strings.Contains(result, "---") || strings.Contains(result, "***") || strings.Contains(result, "___") {
		t.Errorf("should strip rules: %q", result)
	}
	for _, want := range []string{"Section 1", "Section 2", "Section 3"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %q", want, result)
		}
	}
}

func TestFilterMarkdownBodyBlankLinesCollapse(t *testing.T) {
	result := filterMarkdownBody("Line 1\n\n\n\n\nLine 2")
	if strings.Contains(result, "\n\n\n") {
		t.Errorf("should collapse blanks: %q", result)
	}
	if !strings.Contains(result, "Line 1") || !strings.Contains(result, "Line 2") {
		t.Errorf("missing content: %q", result)
	}
}

func TestFilterMarkdownBodyCodeBlockPreserved(t *testing.T) {
	input := "Text before\n```python\n<!-- not a comment -->\n![not an image](url)\n---\n```\nText after"
	result := filterMarkdownBody(input)
	for _, want := range []string{"<!-- not a comment -->", "![not an image](url)", "---", "Text before", "Text after"} {
		if !strings.Contains(result, want) {
			t.Errorf("code block content missing %q: %q", want, result)
		}
	}
}

func TestFilterMarkdownBodyEmpty(t *testing.T) {
	if got := filterMarkdownBody(""); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestFilterMarkdownBodyMeaningfulContentPreserved(t *testing.T) {
	input := "## Summary\n- Item 1\n- Item 2\n\n[Link](https://example.com)\n\n| Col1 | Col2 |\n| --- | --- |\n| a | b |"
	result := filterMarkdownBody(input)
	for _, want := range []string{"## Summary", "- Item 1", "- Item 2", "[Link](https://example.com)", "| Col1 | Col2 |"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %q", want, result)
		}
	}
}

func TestFilterMarkdownBodyTokenSavings(t *testing.T) {
	input := "<!-- This PR template is auto-generated -->\n" +
		"<!-- Please fill in the following sections -->\n" +
		"\n## Summary\n\n" +
		"Added smart markdown filtering for gh issue/pr view commands.\n\n" +
		"[![CI](https://img.shields.io/github/actions/workflow/status/rtk-ai/rtk/ci.yml)](https://github.com/rtk-ai/rtk/actions)\n" +
		"[![Coverage](https://img.shields.io/codecov/c/github/rtk-ai/rtk)](https://codecov.io/gh/rtk-ai/rtk)\n\n" +
		"![screenshot](https://user-images.githubusercontent.com/123/screenshot.png)\n\n" +
		"---\n\n## Changes\n\n" +
		"- Filter HTML comments\n- Filter badge lines\n- Filter image-only lines\n- Collapse blank lines\n\n" +
		"***\n\n## Test Plan\n\n" +
		"- [x] Unit tests added\n- [x] Snapshot tests pass\n- [ ] Manual testing\n\n" +
		"___\n\n" +
		"<!-- Do not edit below this line -->\n<!-- Auto-generated footer -->"

	result := filterMarkdownBody(input)

	countTokens := func(text string) int { return len(strings.Fields(text)) }
	inputTokens := countTokens(input)
	outputTokens := countTokens(result)
	savings := 100.0 - (float64(outputTokens)/float64(inputTokens))*100.0
	if savings < 30.0 {
		t.Errorf("expected >=30%% savings, got %.1f%% (in=%d out=%d)", savings, inputTokens, outputTokens)
	}
	for _, want := range []string{"## Summary", "## Changes", "## Test Plan", "Filter HTML comments"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %q", want, result)
		}
	}
}

// --- format_pr_view body fallback note ---------------------------------------

func TestFormatPRViewBodyBadgesOnlyShowsFallbackNote(t *testing.T) {
	json := mustJSON(t, `{
		"number": 42,
		"title": "Test PR",
		"state": "OPEN",
		"author": { "login": "octocat" },
		"url": "https://github.com/foo/bar/pull/42",
		"mergeable": "MERGEABLE",
		"body": "<!-- Auto-generated by bot -->\n[![CI](https://shields.io/badge.svg)](https://ci.example.com)\n![screenshot](https://example.com/img.png)\n---\n"
	}`)
	out := formatPRView(json, false)
	if !strings.Contains(out, "(body contained only badges/images/comments)") {
		t.Errorf("expected fallback note, got:\n%s", out)
	}
}

func TestFormatPRViewBodyWithContentNoFallbackNote(t *testing.T) {
	json := mustJSON(t, `{
		"number": 42,
		"title": "Test PR",
		"state": "OPEN",
		"author": { "login": "octocat" },
		"url": "https://github.com/foo/bar/pull/42",
		"mergeable": "MERGEABLE",
		"body": "## Summary\nFix the thing.\n"
	}`)
	out := formatPRView(json, false)
	if strings.Contains(out, "(body contained only badges/images/comments)") {
		t.Errorf("fallback note should not fire, got:\n%s", out)
	}
	if !strings.Contains(out, "## Summary") || !strings.Contains(out, "Fix the thing.") {
		t.Errorf("missing content:\n%s", out)
	}
}

func TestFormatPRViewEmptyBodyNoFallbackNote(t *testing.T) {
	json := mustJSON(t, `{
		"number": 42,
		"title": "Test PR",
		"state": "OPEN",
		"author": { "login": "octocat" },
		"url": "https://github.com/foo/bar/pull/42",
		"mergeable": "MERGEABLE",
		"body": ""
	}`)
	out := formatPRView(json, false)
	if strings.Contains(out, "(body contained only badges/images/comments)") {
		t.Errorf("fallback note should not fire on empty body, got:\n%s", out)
	}
}

func TestFormatIssueViewBodyBadgesOnlyShowsFallbackNote(t *testing.T) {
	json := mustJSON(t, `{
		"number": 99,
		"title": "Test Issue",
		"state": "OPEN",
		"author": { "login": "octocat" },
		"url": "https://github.com/foo/bar/issues/99",
		"body": "<!-- Auto-generated -->\n[![status](https://shields.io/s.svg)](https://example.com)\n"
	}`)
	out := formatIssueView(json)
	if !strings.Contains(out, "(body contained only badges/images/comments)") {
		t.Errorf("expected fallback note, got:\n%s", out)
	}
}
