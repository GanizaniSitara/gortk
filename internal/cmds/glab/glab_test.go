package glab

import (
	_ "embed"
	"encoding/json"
	"strings"
	"testing"

	"gortk/internal/core"
)

// Faithful port of the #[cfg(test)] mod tests block in rtk's
// src/cmds/git/glab_cmd.rs. The pure formatter/helper functions are exercised
// directly. JSON fixtures are embedded copies of rtk's tests/fixtures/*.
//
// rtk's count_tokens() counts whitespace words; gortk uses character-based
// core.EstimateTokens(). The savings assertions are property checks (>=
// threshold) and the filters reduce both metrics substantially, so the
// thresholds hold under EstimateTokens.

//go:embed testdata/glab_mr_list_raw.json
var mrListRaw string

//go:embed testdata/glab_issue_list_raw.json
var issueListRaw string

//go:embed testdata/glab_release_list_raw.txt
var releaseListRaw string

//go:embed testdata/glab_ci_trace_raw.txt
var ciTraceRaw string

//go:embed testdata/glab_release_view_raw.txt
var releaseViewRaw string

func savingsPct(input, output string) float64 {
	in := core.EstimateTokens(input)
	out := core.EstimateTokens(output)
	if in == 0 {
		return 0
	}
	return 100.0 - (float64(out) / float64(in) * 100.0)
}

func rawJSON(s string) json.RawMessage { return json.RawMessage(s) }

// ── state_icon / pipeline_icon ──────────────────────────────────────────

func TestStateIconOpened(t *testing.T) {
	if got := stateIcon("opened", false); got != "[open]" {
		t.Errorf("stateIcon(opened,false) = %q", got)
	}
	if got := stateIcon("opened", true); got != "O" {
		t.Errorf("stateIcon(opened,true) = %q", got)
	}
}

func TestStateIconMerged(t *testing.T) {
	if got := stateIcon("merged", false); got != "[merged]" {
		t.Errorf("stateIcon(merged,false) = %q", got)
	}
	if got := stateIcon("merged", true); got != "M" {
		t.Errorf("stateIcon(merged,true) = %q", got)
	}
}

func TestStateIconClosed(t *testing.T) {
	if got := stateIcon("closed", false); got != "[closed]" {
		t.Errorf("stateIcon(closed,false) = %q", got)
	}
	if got := stateIcon("closed", true); got != "C" {
		t.Errorf("stateIcon(closed,true) = %q", got)
	}
}

func TestPipelineIconSuccess(t *testing.T) {
	if got := pipelineIcon("success", false); got != "[ok]" {
		t.Errorf("pipelineIcon(success,false) = %q", got)
	}
	if got := pipelineIcon("success", true); got != "+" {
		t.Errorf("pipelineIcon(success,true) = %q", got)
	}
}

func TestPipelineIconFailed(t *testing.T) {
	if got := pipelineIcon("failed", false); got != "[fail]" {
		t.Errorf("pipelineIcon(failed,false) = %q", got)
	}
	if got := pipelineIcon("failed", true); got != "x" {
		t.Errorf("pipelineIcon(failed,true) = %q", got)
	}
}

func TestPipelineIconRunning(t *testing.T) {
	if got := pipelineIcon("running", false); got != "[run]" {
		t.Errorf("pipelineIcon(running,false) = %q", got)
	}
	if got := pipelineIcon("running", true); got != "~" {
		t.Errorf("pipelineIcon(running,true) = %q", got)
	}
}

// ── extract_mr_number ───────────────────────────────────────────────────

func TestExtractMRNumberFromURL(t *testing.T) {
	url := "https://gitlab.example.com/group/project/-/merge_requests/42"
	num, ok := extractMRNumber(url)
	if !ok || num != "42" {
		t.Errorf("extractMRNumber = %q,%v want 42,true", num, ok)
	}
}

func TestExtractMRNumberNoMatch(t *testing.T) {
	if _, ok := extractMRNumber("not a url"); ok {
		t.Error("want no match")
	}
}

// ── filter_markdown_body ────────────────────────────────────────────────

func TestFilterMarkdownBodyEmpty(t *testing.T) {
	if got := filterMarkdownBody(""); got != "" {
		t.Errorf("want empty, got %q", got)
	}
}

func TestFilterMarkdownBodyHTMLComments(t *testing.T) {
	result := filterMarkdownBody("Hello\n<!-- comment -->\nWorld")
	if strings.Contains(result, "<!--") {
		t.Errorf("comment not stripped: %s", result)
	}
	if !strings.Contains(result, "Hello") || !strings.Contains(result, "World") {
		t.Errorf("content missing: %s", result)
	}
}

func TestFilterMarkdownBodyCodeBlockPreserved(t *testing.T) {
	result := filterMarkdownBody("Text\n```\n<!-- not stripped -->\n```\nAfter")
	if !strings.Contains(result, "<!-- not stripped -->") {
		t.Errorf("code block comment should be preserved: %s", result)
	}
	if !strings.Contains(result, "Text") || !strings.Contains(result, "After") {
		t.Errorf("content missing: %s", result)
	}
}

func TestFilterMarkdownBodyBlankLinesCollapse(t *testing.T) {
	result := filterMarkdownBody("Line 1\n\n\n\n\nLine 2")
	if strings.Contains(result, "\n\n\n") {
		t.Errorf("blank lines not collapsed: %q", result)
	}
	if !strings.Contains(result, "Line 1") || !strings.Contains(result, "Line 2") {
		t.Errorf("content missing: %s", result)
	}
}

func TestFilterMarkdownBodyBadgesRemoved(t *testing.T) {
	input := "# Title\n[![CI](https://img.shields.io/badge.svg)](https://github.com/actions)\nText"
	result := filterMarkdownBody(input)
	if strings.Contains(result, "shields.io") {
		t.Errorf("badge not removed: %s", result)
	}
	if !strings.Contains(result, "# Title") || !strings.Contains(result, "Text") {
		t.Errorf("content missing: %s", result)
	}
}

func TestFilterMarkdownBodyMeaningfulContentPreserved(t *testing.T) {
	input := "## Summary\n- Item 1\n- Item 2\n\n[Link](https://example.com)"
	result := filterMarkdownBody(input)
	for _, want := range []string{"## Summary", "- Item 1", "[Link](https://example.com)"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %s", want, result)
		}
	}
}

// ── ok_confirmation ─────────────────────────────────────────────────────

func TestOkConfirmationMRCreate(t *testing.T) {
	result := okConfirmation("created", "!42 https://gitlab.example.com/-/merge_requests/42")
	if !strings.Contains(result, "ok created") || !strings.Contains(result, "!42") {
		t.Errorf("unexpected: %s", result)
	}
}

func TestOkConfirmationMRMerge(t *testing.T) {
	if got := okConfirmation("merged", "!42"); got != "ok merged !42" {
		t.Errorf("got %q", got)
	}
}

func TestOkConfirmationMRApprove(t *testing.T) {
	if got := okConfirmation("approved", "!42"); got != "ok approved !42" {
		t.Errorf("got %q", got)
	}
}

// ── MR list ─────────────────────────────────────────────────────────────

func TestMRListTokenSavings(t *testing.T) {
	output := formatMRList(rawJSON(mrListRaw), false)
	if s := savingsPct(mrListRaw, output); s < 60.0 {
		t.Errorf("MR list: expected >=60%% savings, got %.1f%%", s)
	}
}

func TestMRListFormat(t *testing.T) {
	output := formatMRList(rawJSON(mrListRaw), false)
	for _, want := range []string{"Merge Requests", "!314", "[open]", "[merged]", "[closed]"} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q in: %s", want, output)
		}
	}
}

func TestMRListUltraCompact(t *testing.T) {
	output := formatMRList(rawJSON(mrListRaw), true)
	if !strings.HasPrefix(output, "MRs\n") {
		t.Errorf("bad prefix: %s", output)
	}
	for _, want := range []string{"O ", "M ", "C "} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q in: %s", want, output)
		}
	}
}

// ── Issue list ──────────────────────────────────────────────────────────

func TestIssueListTokenSavings(t *testing.T) {
	output := formatIssueList(rawJSON(issueListRaw), false)
	if s := savingsPct(issueListRaw, output); s < 60.0 {
		t.Errorf("Issue list: expected >=60%% savings, got %.1f%%", s)
	}
}

func TestIssueListFormat(t *testing.T) {
	output := formatIssueList(rawJSON(issueListRaw), false)
	for _, want := range []string{"Issues", "#156", "[open]", "[closed]"} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q in: %s", want, output)
		}
	}
}

func TestFormatMRListNonArrayReturnsEmpty(t *testing.T) {
	if output := formatMRList(rawJSON("{}"), false); output != "" {
		t.Errorf("want empty, got %q", output)
	}
}

func TestFormatIssueListNonArrayReturnsEmpty(t *testing.T) {
	if output := formatIssueList(rawJSON("{}"), false); output != "" {
		t.Errorf("want empty, got %q", output)
	}
}

// ── extract_identifier_and_extra_args ───────────────────────────────────

func TestExtractIdentifierSimple(t *testing.T) {
	id, extra, ok := extractIdentifierAndExtraArgs([]string{"42"})
	if !ok || id != "42" || len(extra) != 0 {
		t.Errorf("got id=%q extra=%v ok=%v", id, extra, ok)
	}
}

func TestExtractIdentifierWithRepoFlagBefore(t *testing.T) {
	id, extra, ok := extractIdentifierAndExtraArgs([]string{"-R", "group/project", "42"})
	if !ok || id != "42" {
		t.Errorf("got id=%q ok=%v", id, ok)
	}
	if !equalSlice(extra, []string{"-R", "group/project"}) {
		t.Errorf("extra = %v", extra)
	}
}

func TestExtractIdentifierWithRepoFlagAfter(t *testing.T) {
	id, extra, ok := extractIdentifierAndExtraArgs([]string{"42", "-R", "group/project"})
	if !ok || id != "42" {
		t.Errorf("got id=%q ok=%v", id, ok)
	}
	if !equalSlice(extra, []string{"-R", "group/project"}) {
		t.Errorf("extra = %v", extra)
	}
}

func TestExtractIdentifierWithGroupFlag(t *testing.T) {
	id, extra, ok := extractIdentifierAndExtraArgs([]string{"-g", "mygroup", "7"})
	if !ok || id != "7" {
		t.Errorf("got id=%q ok=%v", id, ok)
	}
	if !equalSlice(extra, []string{"-g", "mygroup"}) {
		t.Errorf("extra = %v", extra)
	}
}

func TestExtractIdentifierEmpty(t *testing.T) {
	if _, _, ok := extractIdentifierAndExtraArgs([]string{}); ok {
		t.Error("want ok=false")
	}
}

func TestExtractIdentifierOnlyFlags(t *testing.T) {
	if _, _, ok := extractIdentifierAndExtraArgs([]string{"-R", "group/project"}); ok {
		t.Error("want ok=false")
	}
}

// ── parse_optional_identifier ───────────────────────────────────────────

func TestParseOptionalIdentifierEmptyYieldsNoID(t *testing.T) {
	id, extra, ok := parseOptionalIdentifier([]string{})
	if ok || id != "" || len(extra) != 0 {
		t.Errorf("got id=%q extra=%v ok=%v", id, extra, ok)
	}
}

func TestParseOptionalIdentifierOnlyFlagsPreservesFlags(t *testing.T) {
	id, extra, ok := parseOptionalIdentifier([]string{"-R", "group/project"})
	if ok || id != "" {
		t.Errorf("got id=%q ok=%v", id, ok)
	}
	if !equalSlice(extra, []string{"-R", "group/project"}) {
		t.Errorf("extra = %v", extra)
	}
}

func TestParseOptionalIdentifierWithIDMatchesExtract(t *testing.T) {
	id, extra, ok := parseOptionalIdentifier([]string{"-R", "group/project", "42"})
	if !ok || id != "42" {
		t.Errorf("got id=%q ok=%v", id, ok)
	}
	if !equalSlice(extra, []string{"-R", "group/project"}) {
		t.Errorf("extra = %v", extra)
	}
}

// ── has_output_flag ─────────────────────────────────────────────────────

func TestHasOutputFlagJSON(t *testing.T) {
	if !hasOutputFlag([]string{"--json"}) {
		t.Error("want true for --json")
	}
}

func TestHasOutputFlagFormat(t *testing.T) {
	if !hasOutputFlag([]string{"-F", "json"}) {
		t.Error("want true for -F")
	}
	if !hasOutputFlag([]string{"--output", "text"}) {
		t.Error("want true for --output")
	}
}

func TestHasOutputFlagNone(t *testing.T) {
	if hasOutputFlag([]string{"mr", "list"}) {
		t.Error("want false")
	}
}

// ── should_passthrough_view ─────────────────────────────────────────────

func TestShouldPassthroughViewWeb(t *testing.T) {
	if !shouldPassthroughView([]string{"--web"}) {
		t.Error("want true for --web")
	}
}

func TestShouldPassthroughViewComments(t *testing.T) {
	if !shouldPassthroughView([]string{"--comments"}) {
		t.Error("want true for --comments")
	}
}

func TestShouldPassthroughViewOutput(t *testing.T) {
	if !shouldPassthroughView([]string{"-F", "json"}) {
		t.Error("want true for -F")
	}
}

func TestShouldPassthroughViewDefault(t *testing.T) {
	if shouldPassthroughView([]string{}) {
		t.Error("want false")
	}
}

// ── mr_action identifier extraction ─────────────────────────────────────

func TestExtractIdentifierWithMessageFlag(t *testing.T) {
	id, extra, ok := extractIdentifierAndExtraArgs([]string{"-m", "comment", "42"})
	if !ok || id != "42" {
		t.Errorf("got id=%q ok=%v", id, ok)
	}
	if !equalSlice(extra, []string{"-m", "comment"}) {
		t.Errorf("extra = %v", extra)
	}
}

// ── release list ────────────────────────────────────────────────────────

func TestFormatReleaseList(t *testing.T) {
	output, ok := formatReleaseList(releaseListRaw)
	if !ok {
		t.Fatal("should parse release list")
	}
	if !strings.HasPrefix(output, "Releases\n") {
		t.Errorf("bad prefix: %s", output)
	}
	if !strings.Contains(output, "v3.2.1") || !strings.Contains(output, "about 2 days ago") {
		t.Errorf("missing content: %s", output)
	}
}

func TestFormatReleaseListTokenSavings(t *testing.T) {
	output, ok := formatReleaseList(releaseListRaw)
	if !ok {
		t.Fatal("should parse")
	}
	if s := savingsPct(releaseListRaw, output); s < 20.0 {
		t.Errorf("Release list: expected >=20%% savings, got %.1f%%", s)
	}
}

func TestFormatReleaseListEmpty(t *testing.T) {
	input := "No releases available on owner/repo.\nName\tTag\tCreated\n"
	if _, ok := formatReleaseList(input); ok {
		t.Error("want ok=false")
	}
}

func TestFormatReleaseListNameDiffersFromTag(t *testing.T) {
	input := "Showing 1 releases\n\nName\tTag\tCreated\nMy Release\tv1.0.0\t2 days ago\n"
	output, ok := formatReleaseList(input)
	if !ok {
		t.Fatal("should parse")
	}
	if !strings.Contains(output, "My Release [v1.0.0]") {
		t.Errorf("missing name [tag]: %s", output)
	}
}

// ── ci trace ────────────────────────────────────────────────────────────

func TestFilterCITraceStripsBoilerplate(t *testing.T) {
	output := filterCITrace(ciTraceRaw)
	for _, bad := range []string{
		"Running with gitlab-runner", "Using Docker executor",
		"Fetching changes with git", "Checking out", "Uploading artifacts",
	} {
		if strings.Contains(output, bad) {
			t.Errorf("boilerplate not stripped %q: %s", bad, output)
		}
	}
	for _, want := range []string{"npm ci", "npm run build", "npm test", "FAIL", "AssertionError", "Job failed"} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q in: %s", want, output)
		}
	}
}

func TestFilterCITraceTokenSavings(t *testing.T) {
	output := filterCITrace(ciTraceRaw)
	if s := savingsPct(ciTraceRaw, output); s < 30.0 {
		t.Errorf("CI trace: expected >=30%% savings, got %.1f%%", s)
	}
}

// ── release view ────────────────────────────────────────────────────────

func TestFilterReleaseViewStripsSources(t *testing.T) {
	output := filterReleaseView(releaseViewRaw)
	for _, bad := range []string{
		"SOURCES", "toolkit-v2.0.0.zip", "toolkit-v2.0.0.tar.gz",
		"--------", "Image:", "<!-- internal",
	} {
		if strings.Contains(output, bad) {
			t.Errorf("should not contain %q: %s", bad, output)
		}
	}
	for _, want := range []string{"Test Release v2.0", "Added widget support", "@alice_dev @bob_dev", "View this release"} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q in: %s", want, output)
		}
	}
}

func TestFilterReleaseViewTokenSavings(t *testing.T) {
	output := filterReleaseView(releaseViewRaw)
	if s := savingsPct(releaseViewRaw, output); s < 20.0 {
		t.Errorf("Release view: expected >=20%% savings, got %.1f%%", s)
	}
}

// ── edge cases ──────────────────────────────────────────────────────────

func TestFormatMRListEmptyArray(t *testing.T) {
	if output := formatMRList(rawJSON("[]"), false); output != "No Merge Requests\n" {
		t.Errorf("got %q", output)
	}
}

func TestFormatMRListEmptyArrayUltraCompact(t *testing.T) {
	if output := formatMRList(rawJSON("[]"), true); output != "No MRs\n" {
		t.Errorf("got %q", output)
	}
}

func TestFormatIssueListEmptyArray(t *testing.T) {
	if output := formatIssueList(rawJSON("[]"), false); output != "No Issues\n" {
		t.Errorf("got %q", output)
	}
}

func TestFormatCIListEmptyArray(t *testing.T) {
	if output := formatCIList(rawJSON("[]"), false); output != "No Pipelines\n" {
		t.Errorf("got %q", output)
	}
}

func TestFormatMRViewNullNestedFields(t *testing.T) {
	json := `{"iid":42,"title":"Edge","state":"opened","author":null,"web_url":"","merge_status":"unknown","description":null}`
	output := formatMRView(rawJSON(json), false)
	if !strings.Contains(output, "MR !42: Edge") {
		t.Errorf("missing title line: %s", output)
	}
	if !strings.Contains(output, "???") {
		t.Errorf("expected author fallback ???: %s", output)
	}
}

func TestFormatIssueViewMissingDescription(t *testing.T) {
	json := `{"iid":10,"title":"X","state":"closed","author":{"username":"u"},"web_url":"http://e","description":null}`
	output := formatIssueView(rawJSON(json))
	if !strings.Contains(output, "[closed] Issue #10: X") {
		t.Errorf("missing title line: %s", output)
	}
	if !strings.Contains(output, "Author: @u") {
		t.Errorf("missing author: %s", output)
	}
	if strings.Contains(output, "Description:") {
		t.Errorf("should not have Description section: %s", output)
	}
}

func TestFormatCIStatusNonEnglishFallback(t *testing.T) {
	raw := "Le pipeline est en cours d'exécution\n"
	if output := formatCIStatus(raw, false); output != raw {
		t.Errorf("want raw fallback, got %q", output)
	}
}

func TestFilterReleaseViewNoSourcesSection(t *testing.T) {
	input := "# Release 1.0\n\nJust a simple changelog entry.\n"
	output := filterReleaseView(input)
	if !strings.Contains(output, "Release 1.0") || !strings.Contains(output, "changelog entry") {
		t.Errorf("content missing: %s", output)
	}
}

// ── mr_view enrichment (branches / labels / reviewers) ───────────────────

const mrViewFull = `{
        "iid": 42,
        "title": "feat: widget",
        "state": "opened",
        "author": {"username": "alice_dev"},
        "web_url": "https://gitlab.example.com/acme/toolkit/-/merge_requests/42",
        "merge_status": "can_be_merged",
        "source_branch": "feat/widget",
        "target_branch": "main",
        "labels": ["enhancement", "cli"],
        "reviewers": [{"username": "bob_review"}, {"username": "carol_review"}],
        "head_pipeline": {"status": "success"},
        "description": null
    }`

func TestFormatMRViewBranches(t *testing.T) {
	output := formatMRView(rawJSON(mrViewFull), false)
	if !strings.Contains(output, "feat/widget -> main") {
		t.Errorf("expected branches line, got:\n%s", output)
	}
}

func TestFormatMRViewLabels(t *testing.T) {
	output := formatMRView(rawJSON(mrViewFull), false)
	if !strings.Contains(output, "Labels: enhancement, cli") {
		t.Errorf("expected labels line, got:\n%s", output)
	}
}

func TestFormatMRViewReviewers(t *testing.T) {
	output := formatMRView(rawJSON(mrViewFull), false)
	if !strings.Contains(output, "Reviewers: @bob_review, @carol_review") {
		t.Errorf("expected reviewers line, got:\n%s", output)
	}
}

func TestFormatMRViewNoLabelsNoReviewers(t *testing.T) {
	json := `{
                "iid":1, "title":"X", "state":"opened",
                "author":{"username":"u1"}, "web_url":"",
                "merge_status":"can_be_merged",
                "source_branch":"a", "target_branch":"b",
                "labels":[], "reviewers":[], "description":null
            }`
	output := formatMRView(rawJSON(json), false)
	if strings.Contains(output, "Labels:") {
		t.Errorf("should not have Labels: %s", output)
	}
	if strings.Contains(output, "Reviewers:") {
		t.Errorf("should not have Reviewers: %s", output)
	}
	if !strings.Contains(output, "a -> b") {
		t.Errorf("missing branches line: %s", output)
	}
}

func TestFormatMRViewMergeableTextTag(t *testing.T) {
	output := formatMRView(rawJSON(mrViewFull), false)
	if !strings.Contains(output, "opened | [ok]") {
		t.Errorf("expected text-tag mergeable indicator, got:\n%s", output)
	}
	for _, emoji := range []string{"✅", "❌", "✓", "✗"} {
		if strings.Contains(output, emoji) {
			t.Errorf("emoji %q should not appear: %s", emoji, output)
		}
	}
}

// equalSlice reports whether two string slices have identical elements.
func equalSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
