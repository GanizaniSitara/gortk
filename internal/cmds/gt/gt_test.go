package gt

import (
	"fmt"
	"strings"
	"testing"
)

// countTokens mirrors the Rust test helper: whitespace-delimited token count.
func countTokens(text string) int {
	return len(strings.Fields(text))
}

func TestFilterGtLogExactFormat(t *testing.T) {
	input := "◉  abc1234 feat/add-auth 2d ago\n" +
		"│  feat(auth): add login endpoint\n" +
		"│\n" +
		"◉  def5678 feat/add-db 3d ago user@example.com\n" +
		"│  feat(db): add migration system\n" +
		"│\n" +
		"◉  ghi9012 main 5d ago admin@corp.io\n" +
		"│  chore: update dependencies\n" +
		"~\n"
	output := filterGtLogEntries(input)
	expected := "◉  abc1234 feat/add-auth 2d ago\n" +
		"│  feat(auth): add login endpoint\n" +
		"│\n" +
		"◉  def5678 feat/add-db 3d ago\n" +
		"│  feat(db): add migration system\n" +
		"│\n" +
		"◉  ghi9012 main 5d ago\n" +
		"│  chore: update dependencies\n" +
		"~"
	if output != expected {
		t.Errorf("got %q\nwant %q", output, expected)
	}
}

func TestFilterGtSubmitExactFormat(t *testing.T) {
	input := "Pushed branch feat/add-auth\n" +
		"Created pull request #42 for feat/add-auth\n" +
		"Pushed branch feat/add-db\n" +
		"Updated pull request #40 for feat/add-db\n"
	output := filterGtSubmit(input)
	expected := "pushed feat/add-auth, feat/add-db\n" +
		"created PR #42 feat/add-auth\n" +
		"updated PR #40 feat/add-db"
	if output != expected {
		t.Errorf("got %q\nwant %q", output, expected)
	}
}

func TestFilterGtSyncExactFormat(t *testing.T) {
	input := "Synced with remote\n" +
		"Deleted branch feat/merged-feature\n" +
		"Deleted branch fix/old-hotfix\n"
	output := filterGtSync(input)
	want := "ok sync: 1 synced, 2 deleted (feat/merged-feature, fix/old-hotfix)"
	if output != want {
		t.Errorf("got %q want %q", output, want)
	}
}

func TestFilterGtRestackExactFormat(t *testing.T) {
	input := "Restacked branch feat/add-auth on main\n" +
		"Restacked branch feat/add-db on feat/add-auth\n" +
		"Restacked branch fix/parsing on feat/add-db\n"
	output := filterGtRestack(input)
	if output != "ok restacked 3 branches" {
		t.Errorf("got %q", output)
	}
}

func TestFilterGtCreateExactFormat(t *testing.T) {
	output := filterGtCreate("Created branch feat/new-feature\n")
	if output != "ok created feat/new-feature" {
		t.Errorf("got %q", output)
	}
}

func TestFilterGtLogTruncation(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString(fmt.Sprintf("◉  hash%02d branch-%d 1d ago dev@example.com\n│  commit message %d\n│\n", i, i, i))
	}
	b.WriteString("~\n")
	output := filterGtLogEntries(b.String())
	if !strings.Contains(output, "... +") {
		t.Errorf("expected truncation marker, got %q", output)
	}
}

func TestFilterGtLogEmpty(t *testing.T) {
	if filterGtLogEntries("") != "" {
		t.Errorf("want empty")
	}
	if filterGtLogEntries("  ") != "" {
		t.Errorf("want empty")
	}
}

func TestFilterGtLogTokenSavings(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString(fmt.Sprintf(
			"◉  hash%02dabc feat/feature-%d %dd ago developer%d@longcompany.example.com\n"+
				"│  feat(module-%d): implement feature %d with detailed description of changes\n│\n",
			i, i, i+1, i, i, i))
	}
	b.WriteString("~\n")
	output := filterGtLogEntries(b.String())
	inputTokens := countTokens(b.String())
	outputTokens := countTokens(output)
	savings := 100.0 - (float64(outputTokens)/float64(inputTokens))*100.0
	if savings < 60.0 {
		t.Errorf("gt log filter: expected >=60%% savings, got %.1f%% (%d -> %d tokens)", savings, inputTokens, outputTokens)
	}
}

func TestFilterGtLogLong(t *testing.T) {
	input := "◉  abc1234 feat/add-auth\n" +
		"│  Author: Dev User <dev@example.com>\n" +
		"│  Date: 2026-02-25 10:30:00 -0800\n" +
		"│\n" +
		"│  feat(auth): add login endpoint with OAuth2 support\n" +
		"│  and session management for web clients\n" +
		"│\n" +
		"◉  def5678 feat/add-db\n" +
		"│  Author: Other Dev <other@example.com>\n" +
		"│  Date: 2026-02-24 14:00:00 -0800\n" +
		"│\n" +
		"│  feat(db): add migration system\n" +
		"~\n"
	output := filterGtLogEntries(input)
	if !strings.Contains(output, "abc1234") {
		t.Errorf("missing abc1234: %q", output)
	}
	if strings.Contains(output, "dev@example.com") || strings.Contains(output, "other@example.com") {
		t.Errorf("emails not stripped: %q", output)
	}
}

func TestFilterGtSubmitEmpty(t *testing.T) {
	if filterGtSubmit("") != "" {
		t.Errorf("want empty")
	}
}

func TestFilterGtSubmitWithURLs(t *testing.T) {
	input := "Created pull request #42 for feat/add-auth: https://github.com/org/repo/pull/42\n"
	output := filterGtSubmit(input)
	for _, want := range []string{"PR #42", "feat/add-auth", "https://github.com/org/repo/pull/42"} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q: %q", want, output)
		}
	}
}

func TestFilterGtSubmitTokenSavings(t *testing.T) {
	input := `
  ✅  Pushing to remote...
  Enumerating objects: 15, done.
  Counting objects: 100% (15/15), done.
  Delta compression using up to 10 threads
  Compressing objects: 100% (8/8), done.
  Writing objects: 100% (10/10), 2.50 KiB | 2.50 MiB/s, done.
  Total 10 (delta 5), reused 0 (delta 0), pack-reused 0
  Pushed branch feat/add-auth to origin
  Creating pull request for feat/add-auth...
  Created pull request #42 for feat/add-auth: https://github.com/org/repo/pull/42
  ✅  Pushing to remote...
  Enumerating objects: 8, done.
  Counting objects: 100% (8/8), done.
  Delta compression using up to 10 threads
  Compressing objects: 100% (4/4), done.
  Writing objects: 100% (5/5), 1.20 KiB | 1.20 MiB/s, done.
  Total 5 (delta 3), reused 0 (delta 0), pack-reused 0
  Pushed branch feat/add-db to origin
  Updating pull request for feat/add-db...
  Updated pull request #40 for feat/add-db: https://github.com/org/repo/pull/40
  ✅  Pushing to remote...
  Enumerating objects: 5, done.
  Counting objects: 100% (5/5), done.
  Delta compression using up to 10 threads
  Compressing objects: 100% (3/3), done.
  Writing objects: 100% (3/3), 890 bytes | 890.00 KiB/s, done.
  Total 3 (delta 2), reused 0 (delta 0), pack-reused 0
  Pushed branch fix/parsing to origin
  All branches submitted successfully!
`
	output := filterGtSubmit(input)
	inputTokens := countTokens(input)
	outputTokens := countTokens(output)
	savings := 100.0 - (float64(outputTokens)/float64(inputTokens))*100.0
	if savings < 60.0 {
		t.Errorf("gt submit filter: expected >=60%% savings, got %.1f%% (%d -> %d tokens)", savings, inputTokens, outputTokens)
	}
}

func TestFilterGtSync(t *testing.T) {
	input := "Synced with remote\n" +
		"Deleted branch feat/merged-feature\n" +
		"Deleted branch fix/old-hotfix\n"
	output := filterGtSync(input)
	for _, want := range []string{"ok sync", "synced", "deleted"} {
		if !strings.Contains(output, want) {
			t.Errorf("missing %q: %q", want, output)
		}
	}
}

func TestFilterGtSyncEmpty(t *testing.T) {
	if filterGtSync("") != "" {
		t.Errorf("want empty")
	}
}

func TestFilterGtSyncNoDeletes(t *testing.T) {
	output := filterGtSync("Synced with remote\n")
	if !strings.Contains(output, "ok sync") || !strings.Contains(output, "synced") {
		t.Errorf("missing markers: %q", output)
	}
	if strings.Contains(output, "deleted") {
		t.Errorf("should not contain deleted: %q", output)
	}
}

func TestFilterGtRestack(t *testing.T) {
	input := "Restacked branch feat/add-auth on main\n" +
		"Restacked branch feat/add-db on feat/add-auth\n" +
		"Restacked branch fix/parsing on feat/add-db\n"
	output := filterGtRestack(input)
	if !strings.Contains(output, "ok restacked") || !strings.Contains(output, "3 branches") {
		t.Errorf("got %q", output)
	}
}

func TestFilterGtRestackEmpty(t *testing.T) {
	if filterGtRestack("") != "" {
		t.Errorf("want empty")
	}
}

func TestFilterGtCreate(t *testing.T) {
	output := filterGtCreate("Created branch feat/new-feature\n")
	if output != "ok created feat/new-feature" {
		t.Errorf("got %q", output)
	}
}

func TestFilterGtCreateEmpty(t *testing.T) {
	if filterGtCreate("") != "" {
		t.Errorf("want empty")
	}
}

func TestFilterGtCreateNoBranchName(t *testing.T) {
	output := filterGtCreate("Some unexpected output\n")
	if !strings.HasPrefix(output, "ok created") {
		t.Errorf("got %q", output)
	}
}

func TestIsGraphNode(t *testing.T) {
	truths := []string{
		"◉  abc1234 main",
		"○  def5678 feat/x",
		"@  ghi9012 (current)",
		"*  jkl3456 branch",
		"│ ◉  nested node",
	}
	for _, l := range truths {
		if !isGraphNode(l) {
			t.Errorf("expected graph node: %q", l)
		}
	}
	falses := []string{
		"│  just a message line",
		"~",
	}
	for _, l := range falses {
		if isGraphNode(l) {
			t.Errorf("expected NOT graph node: %q", l)
		}
	}
}

func TestExtractBranchName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Created branch feat/new-feature", "feat/new-feature"},
		{"Pushed branch fix/bug-123", "fix/bug-123"},
		{"Pushed branch feat/auth+session", "feat/auth+session"},
		{"Created branch user@fix", "user@fix"},
		{"no branch here", ""},
	}
	for _, c := range cases {
		if got := extractBranchName(c.in); got != c.want {
			t.Errorf("extractBranchName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFilterGtLogPreStrippedInput(t *testing.T) {
	input := "◉  abc1234 feat/x 1d ago user@test.com\n│  message\n~\n"
	output := filterGtLogEntries(input)
	if !strings.Contains(output, "abc1234") {
		t.Errorf("missing abc1234: %q", output)
	}
	if strings.Contains(output, "user@test.com") {
		t.Errorf("email not stripped: %q", output)
	}
}

func TestFilterGtSyncTokenSavings(t *testing.T) {
	input := `
  ✅ Syncing with remote...
  Pulling latest changes from main...
  Successfully pulled 5 new commits
  Synced branch feat/add-auth with remote
  Synced branch feat/add-db with remote
  Branch feat/merged-feature has been merged
  Deleted branch feat/merged-feature
  Branch fix/old-hotfix has been merged
  Deleted branch fix/old-hotfix
  All branches synced!
`
	output := filterGtSync(input)
	inputTokens := countTokens(input)
	outputTokens := countTokens(output)
	savings := 100.0 - (float64(outputTokens)/float64(inputTokens))*100.0
	if savings < 60.0 {
		t.Errorf("gt sync filter: expected >=60%% savings, got %.1f%% (%d -> %d tokens)", savings, inputTokens, outputTokens)
	}
}

func TestFilterGtCreateTokenSavings(t *testing.T) {
	input := `
  ✅ Creating new branch...
  Checking out from feat/add-auth...
  Created branch feat/new-feature from feat/add-auth
  Tracking branch set up to follow feat/add-auth
  Branch feat/new-feature is ready for development
`
	output := filterGtCreate(input)
	inputTokens := countTokens(input)
	outputTokens := countTokens(output)
	savings := 100.0 - (float64(outputTokens)/float64(inputTokens))*100.0
	if savings < 60.0 {
		t.Errorf("gt create filter: expected >=60%% savings, got %.1f%% (%d -> %d tokens)", savings, inputTokens, outputTokens)
	}
}

func TestFilterGtRestackTokenSavings(t *testing.T) {
	input := `
  ✅ Restacking branches...
  Restacked branch feat/add-auth on top of main
  Successfully rebased feat/add-auth (3 commits)
  Restacked branch feat/add-db on top of feat/add-auth
  Successfully rebased feat/add-db (2 commits)
  Restacked branch fix/parsing on top of feat/add-db
  Successfully rebased fix/parsing (1 commit)
  All branches restacked!
`
	output := filterGtRestack(input)
	inputTokens := countTokens(input)
	outputTokens := countTokens(output)
	savings := 100.0 - (float64(outputTokens)/float64(inputTokens))*100.0
	if savings < 60.0 {
		t.Errorf("gt restack filter: expected >=60%% savings, got %.1f%%", savings)
	}
}
