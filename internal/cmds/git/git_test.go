package git

import (
	"path/filepath"
	"strings"
	"testing"
)

// --- git_cmd / global args (ported from git.rs Command-builder tests) ---

func TestGitCmdNoGlobalArgs(t *testing.T) {
	cmd := gitCmd(nil)
	// On Windows resolved_command returns a full path (git.exe); the basename
	// without extension must be "git".
	base := filepath.Base(cmd.Path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base != "git" {
		t.Errorf("program basename = %q, want git", base)
	}
	// cmd.Args[0] is the program; there should be no further args.
	if len(cmd.Args) != 1 {
		t.Errorf("expected no extra args, got %v", cmd.Args[1:])
	}
}

func TestGitCmdWithDirectory(t *testing.T) {
	cmd := gitCmd([]string{"-C", "/tmp"})
	got := cmd.Args[1:]
	want := []string{"-C", "/tmp"}
	if !equalArgs(got, want) {
		t.Errorf("args = %v, want %v", got, want)
	}
}

func TestGitCmdWithMultipleGlobalArgs(t *testing.T) {
	global := []string{"-C", "/tmp", "-c", "user.name=test", "--git-dir", "/foo/.git"}
	cmd := gitCmd(global)
	if !equalArgs(cmd.Args[1:], global) {
		t.Errorf("args = %v, want %v", cmd.Args[1:], global)
	}
}

func TestGitCmdWithBooleanFlags(t *testing.T) {
	global := []string{"--no-pager", "--bare"}
	cmd := gitCmd(global)
	if !equalArgs(cmd.Args[1:], global) {
		t.Errorf("args = %v, want %v", cmd.Args[1:], global)
	}
}

func TestGitCmdCLocaleSetsStableEnv(t *testing.T) {
	cmd := gitCmdCLocale(nil)
	found := false
	for _, e := range cmd.Env {
		if e == "LC_ALL=C" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("LC_ALL=C not set in env: %v", cmd.Env)
	}
}

// parseGlobalArgs is the gortk equivalent of clap's Commands::Git flag parsing.
func TestParseGlobalArgs(t *testing.T) {
	cases := []struct {
		in         []string
		wantGlobal []string
		wantRest   []string
	}{
		{
			in:         []string{"-C", "/tmp", "status"},
			wantGlobal: []string{"-C", "/tmp"},
			wantRest:   []string{"status"},
		},
		{
			in:         []string{"-C", "/tmp", "-c", "user.name=test", "--git-dir", "/foo/.git", "log"},
			wantGlobal: []string{"-C", "/tmp", "-c", "user.name=test", "--git-dir", "/foo/.git"},
			wantRest:   []string{"log"},
		},
		{
			in:         []string{"--no-pager", "--bare", "branch"},
			wantGlobal: []string{"--no-pager", "--bare"},
			wantRest:   []string{"branch"},
		},
		{
			in:         []string{"status", "-s"},
			wantGlobal: nil,
			wantRest:   []string{"status", "-s"},
		},
		{
			in:         []string{"--git-dir=/foo/.git", "status"},
			wantGlobal: []string{"--git-dir", "/foo/.git"},
			wantRest:   []string{"status"},
		},
	}
	for _, c := range cases {
		g, r := parseGlobalArgs(c.in)
		if !equalArgs(g, c.wantGlobal) {
			t.Errorf("parseGlobalArgs(%v) global = %v, want %v", c.in, g, c.wantGlobal)
		}
		if !equalArgs(r, c.wantRest) {
			t.Errorf("parseGlobalArgs(%v) rest = %v, want %v", c.in, r, c.wantRest)
		}
	}
}

// --- build_status_command / uses_compact_status_path ---

func TestBuildStatusCommandDefaultCompact(t *testing.T) {
	got := buildStatusArgs(nil)
	want := []string{"status", "--porcelain", "-b"}
	if !equalArgs(got, want) {
		t.Errorf("buildStatusArgs(nil) = %v, want %v", got, want)
	}
}

func TestUsesCompactStatusPathForBranchAndShortFlags(t *testing.T) {
	truthy := [][]string{
		{"-b"}, {"--branch"}, {"-sb"},
		{"-s", "-b"}, {"--short", "--branch"},
	}
	for _, args := range truthy {
		if !usesCompactStatusPath(args) {
			t.Errorf("usesCompactStatusPath(%v) = false, want true", args)
		}
	}
	falsy := [][]string{
		{"-s"}, {"--short"}, {"--porcelain"}, {"-uno"},
	}
	for _, args := range falsy {
		if usesCompactStatusPath(args) {
			t.Errorf("usesCompactStatusPath(%v) = true, want false", args)
		}
	}
}

func TestBuildStatusCommandWithUserArgsPassthrough(t *testing.T) {
	got := buildStatusArgs([]string{"--short", "--branch"})
	want := []string{"status", "--porcelain", "-b"}
	if !equalArgs(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestBuildStatusCommandWithIncompatibleUserArgsPassthrough(t *testing.T) {
	got := buildStatusArgs([]string{"--porcelain", "-uno"})
	want := []string{"status", "--porcelain", "-uno"}
	if !equalArgs(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// --- compact_diff ---

func TestCompactDiff(t *testing.T) {
	diff := `diff --git a/foo.rs b/foo.rs
--- a/foo.rs
+++ b/foo.rs
@@ -1,3 +1,4 @@
 fn main() {
+    println!("hello");
 }
`
	result := compactDiff(diff, 100)
	if !strings.Contains(result, "foo.rs") {
		t.Errorf("missing filename:\n%s", result)
	}
	if !strings.Contains(result, "+") {
		t.Errorf("missing +:\n%s", result)
	}
}

func TestCompactDiffPreservesFullHunkHeaderContext(t *testing.T) {
	diff := `diff --git a/foo.rs b/foo.rs
--- a/foo.rs
+++ b/foo.rs
@@ -10,3 +10,4 @@ fn important_context() {
 fn main() {
+    println!("hello");
 }
`
	result := compactDiff(diff, 100)
	if !strings.Contains(result, "@@ -10,3 +10,4 @@ fn important_context() {") {
		t.Errorf("expected full hunk header with trailing context, got:\n%s", result)
	}
}

func TestCompactDiffIncreasedHunkLimit(t *testing.T) {
	var b strings.Builder
	b.WriteString("diff --git a/big.rs b/big.rs\n--- a/big.rs\n+++ b/big.rs\n@@ -1,25 +1,25 @@\n")
	for i := 1; i <= 25; i++ {
		b.WriteString("+line")
		b.WriteString(itoa(i))
		b.WriteByte('\n')
	}
	result := compactDiff(b.String(), 500)
	if strings.Contains(result, "... (truncated)") {
		t.Errorf("25 lines should not be truncated, got:\n%s", result)
	}
	if !strings.Contains(result, "+line25") {
		t.Errorf("missing +line25:\n%s", result)
	}
}

func TestCompactDiffIncreasedTotalLimit(t *testing.T) {
	var b strings.Builder
	for f := 1; f <= 5; f++ {
		fs := itoa(f)
		b.WriteString("diff --git a/file" + fs + ".rs b/file" + fs + ".rs\n--- a/file" + fs + ".rs\n+++ b/file" + fs + ".rs\n@@ -1,20 +1,20 @@\n")
		for i := 1; i <= 20; i++ {
			b.WriteString("+line" + fs + "_" + itoa(i) + "\n")
		}
	}
	result := compactDiff(b.String(), 500)
	if strings.Contains(result, "more changes truncated") {
		t.Errorf("5 files x 20 lines should not exceed max_lines=500, got:\n%s", result)
	}
}

func TestCompactDiffRecoveryHintPresent(t *testing.T) {
	var b strings.Builder
	b.WriteString("diff --git a/large.rs b/large.rs\n--- a/large.rs\n+++ b/large.rs\n@@ -1,150 +1,150 @@\n")
	for i := 0; i < 110; i++ {
		b.WriteString("+added line " + itoa(i) + "\n")
	}
	result := compactDiff(b.String(), 500)
	if !strings.Contains(result, "[full diff: gortk git diff --no-compact]") {
		t.Errorf("expected recovery hint when hunk truncated, got:\n%s", result)
	}
}

func TestCompactDiffHunkTruncationCountAccurate(t *testing.T) {
	var b strings.Builder
	b.WriteString("diff --git a/large.rs b/large.rs\n--- a/large.rs\n+++ b/large.rs\n@@ -1,150 +1,150 @@\n")
	for i := 0; i < 150; i++ {
		b.WriteString("+line " + itoa(i) + "\n")
	}
	result := compactDiff(b.String(), 500)
	if !strings.Contains(result, "50 lines truncated") {
		t.Errorf("expected '50 lines truncated' (150-100), got:\n%s", result)
	}
}

// --- is_blob_show_arg ---

func TestIsBlobShowArg(t *testing.T) {
	if !isBlobShowArg("develop:modules/pairs_backtest.py") {
		t.Error("develop:... should be blob show arg")
	}
	if !isBlobShowArg("HEAD:src/main.rs") {
		t.Error("HEAD:... should be blob show arg")
	}
	if isBlobShowArg("--pretty=format:%h") {
		t.Error("--pretty=format: should not be blob show arg")
	}
	if isBlobShowArg("--format=short") {
		t.Error("--format=short should not be blob show arg")
	}
	if isBlobShowArg("HEAD") {
		t.Error("HEAD should not be blob show arg")
	}
}

// --- filter_branch_output ---

func TestFilterBranchOutput(t *testing.T) {
	output := "* main\n  feature/auth\n  fix/bug-123\n  remotes/origin/HEAD -> origin/main\n  remotes/origin/main\n  remotes/origin/feature/auth\n  remotes/origin/release/v2\n"
	result := filterBranchOutput(output)
	for _, want := range []string{"* main", "feature/auth", "fix/bug-123", "remote-only", "release/v2"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q:\n%s", want, result)
		}
	}
}

func TestFilterBranchNoRemotes(t *testing.T) {
	result := filterBranchOutput("* main\n  develop\n")
	if !strings.Contains(result, "* main") || !strings.Contains(result, "develop") {
		t.Errorf("missing locals:\n%s", result)
	}
	if strings.Contains(result, "remote-only") {
		t.Errorf("should not contain remote-only:\n%s", result)
	}
}

func TestFilterBranchMultiRemote(t *testing.T) {
	output := "* main\n  develop\n  remotes/origin/HEAD -> origin/main\n  remotes/origin/main\n  remotes/origin/feature-x\n  remotes/upstream/main\n  remotes/upstream/release-v3\n  remotes/fork/main\n  remotes/fork/experiment\n"
	result := filterBranchOutput(output)
	for _, want := range []string{"* main", "develop", "feature-x", "release-v3", "experiment"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q:\n%s", want, result)
		}
	}
	if strings.Contains(result, "remotes/") {
		t.Errorf("remote prefix not stripped:\n%s", result)
	}
	if n := strings.Count(result, "main"); n > 2 {
		t.Errorf("main not deduplicated (found %d):\n%s", n, result)
	}
}

// --- filter_stash_list ---

func TestFilterStashList(t *testing.T) {
	output := "stash@{0}: WIP on main: abc1234 fix login\nstash@{1}: On feature: def5678 wip\n"
	result := filterStashList(output)
	if !strings.Contains(result, "stash@{0}: abc1234 fix login") {
		t.Errorf("missing first stash:\n%s", result)
	}
	if !strings.Contains(result, "stash@{1}: def5678 wip") {
		t.Errorf("missing second stash:\n%s", result)
	}
}

// --- filter_worktree_list ---

func TestFilterWorktreeList(t *testing.T) {
	output := "/home/user/project  abc1234 [main]\n/home/user/worktrees/feat  def5678 [feature]\n"
	result := filterWorktreeList(output)
	for _, want := range []string{"abc1234", "[main]", "[feature]"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q:\n%s", want, result)
		}
	}
}

// --- format_status_output ---

func TestFormatStatusOutputClean(t *testing.T) {
	result := formatStatusOutput("## main...origin/main\n")
	want := "* main...origin/main\nclean — nothing to commit"
	if result != want {
		t.Errorf("got %q, want %q", result, want)
	}
}

func TestFormatStatusOutputPreservesNestedUntrackedPaths(t *testing.T) {
	result := formatStatusOutput("## main\n?? tmp/c.txt\n?? tmp/nested/d.txt\n")
	for _, want := range []string{"* main", "?? tmp/c.txt", "?? tmp/nested/d.txt"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q:\n%s", want, result)
		}
	}
	for _, line := range strings.Split(result, "\n") {
		if line == "?? tmp/" {
			t.Errorf("nested untracked collapsed to dir marker:\n%s", result)
		}
	}
}

func TestFormatStatusOutputMixedChanges(t *testing.T) {
	porcelain := "## main\nM  staged.rs\n M modified.rs\nA  added.rs\n?? untracked.txt\n"
	result := formatStatusOutput(porcelain)
	for _, want := range []string{"* main", "M  staged.rs", " M modified.rs", "A  added.rs", "?? untracked.txt"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q:\n%s", want, result)
		}
	}
	for _, bad := range []string{"Staged", "Modified", "Untracked"} {
		if strings.Contains(result, bad) {
			t.Errorf("should not contain %q:\n%s", bad, result)
		}
	}
}

func TestFormatStatusOutputPreservesRenameAndConflictLines(t *testing.T) {
	porcelain := "## main\nR  old.rs -> new.rs\nUU conflict.rs\nMM mixed.rs\n"
	result := formatStatusOutput(porcelain)
	for _, want := range []string{"* main", "R  old.rs -> new.rs", "UU conflict.rs", "MM mixed.rs"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q:\n%s", want, result)
		}
	}
	if strings.Contains(result, "conflicts:") {
		t.Errorf("should not contain conflicts::\n%s", result)
	}
}

func TestFormatStatusOutputShowsEveryFileWhenManyAreDirty(t *testing.T) {
	var b strings.Builder
	b.WriteString("## main...origin/main\n")
	for i := 0; i < 25; i++ {
		b.WriteString("M  staged_file_" + itoa(i) + ".rs\n")
	}
	result := formatStatusOutput(b.String())
	if !strings.Contains(result, "staged_file_24.rs") {
		t.Errorf("last staged file missing:\n%s", result)
	}
	if n := len(strings.Split(result, "\n")); n != 26 {
		t.Errorf("expected branch + 25 files = 26 lines, got %d:\n%s", n, result)
	}
	if strings.Contains(result, "... +") {
		t.Errorf("must not hide dirty paths behind overflow:\n%s", result)
	}
}

func TestFormatStatusOutputThaiFilename(t *testing.T) {
	result := formatStatusOutput("## main\n M สวัสดี.txt\n?? ทดสอบ.rs\n")
	for _, want := range []string{"* main", "สวัสดี.txt", "ทดสอบ.rs"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q:\n%s", want, result)
		}
	}
}

func TestFormatStatusOutputEmojiFilename(t *testing.T) {
	result := formatStatusOutput("## main\nA  🎉-party.txt\n M 日本語ファイル.rs\n")
	if !strings.Contains(result, "* main") {
		t.Errorf("missing * main:\n%s", result)
	}
}

func TestFormatStatusOutputDetachedHead(t *testing.T) {
	result := formatStatusOutputDetached("## HEAD (no branch)\n M src/main.rs\n", "HEAD detached at abc1234")
	if !strings.Contains(result, "HEAD detached at abc1234") {
		t.Errorf("should use explicit detached ref, got: %s", result)
	}
	if strings.Contains(result, "HEAD (no branch)") {
		t.Errorf("should not show opaque porcelain string, got: %s", result)
	}
}

// --- extract_state_header ---

func TestExtractStateHeaderCleanReturnsNone(t *testing.T) {
	raw := "On branch main\nYour branch is up to date with 'origin/main'.\n\nnothing to commit, working tree clean\n"
	if _, ok := extractStateHeader(raw); ok {
		t.Error("expected no state")
	}
}

func TestExtractStateHeaderNoStateWithChangesReturnsNone(t *testing.T) {
	raw := "On branch main\nChanges not staged for commit:\n  (use \"git add <file>...\" to update what will be committed)\n\tmodified:   src/main.rs\n\nno changes added to commit\n"
	if _, ok := extractStateHeader(raw); ok {
		t.Error("expected no state")
	}
}

func TestExtractStateHeaderEditingWhileRebasing(t *testing.T) {
	raw := "On branch feature\n\ninteractive rebase in progress; onto abc1234\nLast command done (1 command done):\n   edit abc123 some message\nNo commands remaining.\nYou are currently editing a commit while rebasing branch 'feature' on 'abc1234'.\n  (use \"git commit --amend\" to amend the current commit)\n  (use \"git rebase --continue\" once you are satisfied with your changes)\n\nnothing to commit, working tree clean\n"
	out, ok := extractStateHeader(raw)
	if !ok || out != "rebase in progress" {
		t.Errorf("got %q,%v want 'rebase in progress'", out, ok)
	}
}

func TestExtractStateHeaderMergeUnresolved(t *testing.T) {
	raw := "On branch main\nYou have unmerged paths.\n  (fix conflicts and run \"git commit\")\n  (use \"git merge --abort\" to abort the merge)\n\nUnmerged paths:\n\tboth modified:   src/main.rs\n"
	out, ok := extractStateHeader(raw)
	if !ok || out != "merge in progress. unresolved conflicts" {
		t.Errorf("got %q,%v", out, ok)
	}
}

func TestExtractStateHeaderCherryPick(t *testing.T) {
	raw := "On branch main\n\nYou are currently cherry-picking commit abc1234.\n  (fix conflicts and run \"git cherry-pick --continue\")\n  (use \"git cherry-pick --abort\" to cancel the cherry-pick operation)\n\nnothing to commit, working tree clean\n"
	out, ok := extractStateHeader(raw)
	if !ok || out != "cherry-pick in progress" {
		t.Errorf("got %q,%v", out, ok)
	}
}

func TestExtractStateHeaderBisect(t *testing.T) {
	raw := "On branch main\n\nYou are currently bisecting, started from branch 'main'.\n  (use \"git bisect reset\" to get back to the original branch)\n\nnothing to commit, working tree clean\n"
	out, ok := extractStateHeader(raw)
	if !ok || out != "bisect in progress" {
		t.Errorf("got %q,%v", out, ok)
	}
}

func TestExtractStateHeaderRevert(t *testing.T) {
	raw := "On branch main\n\nYou are currently reverting commit abc1234.\n  (fix conflicts and run \"git revert --continue\")\n  (use \"git revert --abort\" to cancel the revert operation)\n\nnothing to commit, working tree clean\n"
	out, ok := extractStateHeader(raw)
	if !ok || out != "revert in progress" {
		t.Errorf("got %q,%v", out, ok)
	}
}

func TestExtractStateHeaderMergeInMiddle(t *testing.T) {
	raw := "On branch main\n\nAll conflicts fixed but you are still merging.\n  (use \"git commit\" to conclude merge)\n\nChanges to be committed:\n\tmodified:   src/main.rs\n"
	out, ok := extractStateHeader(raw)
	if !ok || out != "merge in progress. no conflicts" {
		t.Errorf("got %q,%v", out, ok)
	}
}

func TestExtractStateHeaderAmSession(t *testing.T) {
	raw := "On branch main\n\nYou are in the middle of an am session.\n  (use \"git am --continue\" to continue)\n  (use \"git am --abort\" to restore the original branch)\n\nnothing to commit, working tree clean\n"
	out, ok := extractStateHeader(raw)
	if !ok || out != "am session in progress" {
		t.Errorf("got %q,%v", out, ok)
	}
}

func TestExtractStateHeaderSparseCheckout(t *testing.T) {
	raw := "On branch main\n\nYou are in a sparse checkout with 17% of tracked files present.\n\nnothing to commit, working tree clean\n"
	out, ok := extractStateHeader(raw)
	if !ok || out != "sparse checkout enabled" {
		t.Errorf("got %q,%v", out, ok)
	}
}

// --- extract_detached_head ---

func TestExtractDetachedHeadReturnsLine(t *testing.T) {
	raw := "HEAD detached at abc1234\nnothing to commit, working tree clean\n"
	out, ok := extractDetachedHead(raw)
	if !ok || out != "HEAD detached at abc1234" {
		t.Errorf("got %q,%v", out, ok)
	}
}

func TestExtractDetachedHeadOnBranchIsNone(t *testing.T) {
	raw := "On branch main\nnothing to commit, working tree clean\n"
	if _, ok := extractDetachedHead(raw); ok {
		t.Error("expected none on branch")
	}
}

// --- filter_log_output ---

func TestFilterLogOutput(t *testing.T) {
	output := "abc1234 This is a commit message (2 days ago) <author>\n\n---END---\ndef5678 Another commit (1 week ago) <other>\n\n---END---\n"
	result := filterLogOutput(output, 10, false, false)
	if !strings.Contains(result, "abc1234") || !strings.Contains(result, "def5678") {
		t.Errorf("missing commits:\n%s", result)
	}
	if n := lineCount(result); n != 2 {
		t.Errorf("expected 2 lines, got %d:\n%s", n, result)
	}
}

func TestFilterLogOutputWithBody(t *testing.T) {
	output := "abc1234 feat: add feature (2 days ago) <author>\nBREAKING CHANGE: removed old API\nSigned-off-by: Author <a@b.com>\n---END---\ndef5678 fix: typo (1 day ago) <other>\n\n---END---\n"
	result := filterLogOutput(output, 10, false, false)
	if !strings.Contains(result, "abc1234") {
		t.Errorf("missing abc1234:\n%s", result)
	}
	if !strings.Contains(result, "BREAKING CHANGE: removed old API") {
		t.Errorf("missing body line:\n%s", result)
	}
	if strings.Contains(result, "Signed-off-by:") {
		t.Errorf("should not contain trailer:\n%s", result)
	}
	if !strings.Contains(result, "def5678") {
		t.Errorf("missing def5678:\n%s", result)
	}
	if n := lineCount(result); n != 3 {
		t.Errorf("expected 3 lines, got %d:\n%s", n, result)
	}
}

func TestFilterLogOutputSkipsTrailers(t *testing.T) {
	output := "abc1234 chore: bump (1 day ago) <bot>\nSigned-off-by: Bot <bot@ci>\nCo-authored-by: Human <h@b>\n---END---\n"
	result := filterLogOutput(output, 10, false, false)
	if !strings.Contains(result, "abc1234") {
		t.Errorf("missing abc1234:\n%s", result)
	}
	if strings.Contains(result, "Signed-off-by:") || strings.Contains(result, "Co-authored-by:") {
		t.Errorf("should not contain trailers:\n%s", result)
	}
	if n := lineCount(result); n != 1 {
		t.Errorf("expected 1 line, got %d:\n%s", n, result)
	}
}

func TestFilterLogOutputTruncateLong(t *testing.T) {
	longLine := "abc1234 " + strings.Repeat("x", 100) + " (2 days ago) <author>"
	result := filterLogOutput(longLine, 10, false, false)
	if len([]rune(result)) >= len([]rune(longLine)) {
		t.Errorf("expected truncation")
	}
	if !strings.Contains(result, "...") {
		t.Errorf("expected ellipsis:\n%s", result)
	}
	if len([]rune(result)) > 80 {
		t.Errorf("expected <= 80 chars, got %d", len([]rune(result)))
	}
}

func TestFilterLogOutputCapLines(t *testing.T) {
	var parts []string
	for i := 0; i < 20; i++ {
		parts = append(parts, "hash"+itoa(i)+" message "+itoa(i)+" (1 day ago) <author>\n\n---END---")
	}
	output := strings.Join(parts, "\n")
	result := filterLogOutput(output, 5, false, false)
	if n := lineCount(result); n != 5 {
		t.Errorf("expected 5 lines, got %d", n)
	}
}

func TestFilterLogOutputUserLimitNoCap(t *testing.T) {
	var parts []string
	for i := 0; i < 20; i++ {
		parts = append(parts, "hash"+itoa(i)+" message "+itoa(i)+" (1 day ago) <author>\n\n---END---")
	}
	output := strings.Join(parts, "\n")
	result := filterLogOutput(output, 20, true, false)
	if n := lineCount(result); n != 20 {
		t.Errorf("user -20 should return 20 lines, got %d", n)
	}
}

func TestFilterLogOutputUserLimitWiderTruncation(t *testing.T) {
	line90 := "abc1234 " + strings.Repeat("x", 60) + " (2 days ago) <author>"
	resultDefault := filterLogOutput(line90, 10, false, false)
	resultUser := filterLogOutput(line90, 10, true, false)
	if !strings.Contains(resultDefault, "...") {
		t.Errorf("default should truncate at 80 chars:\n%s", resultDefault)
	}
	if strings.Contains(resultUser, "...") {
		t.Errorf("user limit should not truncate 90-char line:\n%s", resultUser)
	}
}

func TestFilterLogOutputBodyOmissionIndicator(t *testing.T) {
	var bodyLines []string
	for i := 1; i <= 6; i++ {
		bodyLines = append(bodyLines, "body line "+itoa(i))
	}
	output := "abc1234 feat: big change (1 day ago) <author>\n" + strings.Join(bodyLines, "\n") + "\n---END---\n"
	result := filterLogOutput(output, 10, false, false)
	if !strings.Contains(result, "+3 lines omitted") {
		t.Errorf("expected '+3 lines omitted', got:\n%s", result)
	}
}

func TestFilterLogOutputUserFormatOneline(t *testing.T) {
	oneline := "abc1234 feat: add feature\n" +
		"def5678 fix: typo\n" +
		"ghi9012 chore: bump deps\n" +
		"jkl3456 docs: update readme\n" +
		"mno7890 test: add tests\n"
	result := filterLogOutput(oneline, 10, false, true)
	if n := lineCount(result); n != 5 {
		t.Errorf("expected 5 lines, got %d", n)
	}
	if !strings.Contains(result, "abc1234") || !strings.Contains(result, "mno7890") {
		t.Errorf("missing commits:\n%s", result)
	}
}

func TestFilterLogOutputUserFormatWithLimit(t *testing.T) {
	oneline := "abc1234 feat: add feature\n" +
		"def5678 fix: typo\n" +
		"ghi9012 chore: bump deps\n" +
		"jkl3456 docs: update readme\n" +
		"mno7890 test: add tests\n"
	if n := lineCount(filterLogOutput(oneline, 3, true, true)); n != 5 {
		t.Errorf("user_set_limit=true should keep all 5, got %d", n)
	}
	if n := lineCount(filterLogOutput(oneline, 3, false, true)); n != 3 {
		t.Errorf("user_set_limit=false should cap at 3, got %d", n)
	}
}

func TestFilterLogOutputMultibyte(t *testing.T) {
	thaiMsg := "abc1234 " + strings.Repeat("ก", 30) + " (2 days ago) <author>"
	result := filterLogOutput(thaiMsg, 10, false, false)
	if !strings.Contains(result, "abc1234") {
		t.Errorf("should not panic / drop header:\n%s", result)
	}
}

func TestFilterLogOutputEmoji(t *testing.T) {
	emojiMsg := "abc1234 🎉🎊🎈🎁🎂🎄🎃🎆🎇✨🎉🎊🎈🎁🎂🎄🎃🎆🎇✨ (1 day ago) <user>"
	result := filterLogOutput(emojiMsg, 10, false, false)
	if !strings.Contains(result, "abc1234") {
		t.Errorf("should not panic:\n%s", result)
	}
}

func TestFilterLogOutputTokenSavings(t *testing.T) {
	countTokens := func(s string) int { return len(strings.Fields(s)) }
	var parts []string
	for i := 0; i < 20; i++ {
		parts = append(parts, "commit abc123"+itoa(i)+"\nAuthor: User Name <user@example.com>\nDate:   Mon Mar 10 10:00:00 2026 +0000\n\n    fix: commit message number "+itoa(i)+"\n\n    Extended body with details about the change.\n")
	}
	input := strings.Join(parts, "\n")
	output := filterLogOutput(input, 10, false, false)
	savings := 100.0 - (float64(countTokens(output))/float64(countTokens(input)))*100.0
	if savings < 60.0 {
		t.Errorf("expected >=60%% token savings, got %.1f%%", savings)
	}
}

// --- parse_user_limit ---

func TestParseUserLimitCombined(t *testing.T) {
	if n, ok := parseUserLimit([]string{"-20"}); !ok || n != 20 {
		t.Errorf("got %d,%v want 20", n, ok)
	}
}

func TestParseUserLimitNSpace(t *testing.T) {
	if n, ok := parseUserLimit([]string{"-n", "15"}); !ok || n != 15 {
		t.Errorf("got %d,%v want 15", n, ok)
	}
}

func TestParseUserLimitMaxCountEq(t *testing.T) {
	if n, ok := parseUserLimit([]string{"--max-count=30"}); !ok || n != 30 {
		t.Errorf("got %d,%v want 30", n, ok)
	}
}

func TestParseUserLimitMaxCountSpace(t *testing.T) {
	if n, ok := parseUserLimit([]string{"--max-count", "25"}); !ok || n != 25 {
		t.Errorf("got %d,%v want 25", n, ok)
	}
}

func TestParseUserLimitNone(t *testing.T) {
	if _, ok := parseUserLimit([]string{"--oneline"}); ok {
		t.Error("expected none")
	}
}

// --- filter_status_with_args ---

func TestFilterStatusWithArgs(t *testing.T) {
	output := `On branch main
Your branch is up to date with 'origin/main'.

Changes not staged for commit:
  (use "git add <file>..." to update what will be committed)
  (use "git restore <file>..." to discard changes in working directory)
	modified:   src/main.rs

no changes added to commit (use "git add" and/or "git commit -a")
`
	result := filterStatusWithArgs(output)
	if !strings.Contains(result, "On branch main") {
		t.Errorf("missing branch:\n%s", result)
	}
	if !strings.Contains(result, "modified:   src/main.rs") {
		t.Errorf("missing modified file:\n%s", result)
	}
	if strings.Contains(result, "(use \"git") {
		t.Errorf("should not contain git hints:\n%s", result)
	}
}

func TestFilterStatusWithArgsClean(t *testing.T) {
	result := filterStatusWithArgs("nothing to commit, working tree clean\n")
	if !strings.Contains(result, "nothing to commit") {
		t.Errorf("missing clean message:\n%s", result)
	}
}

// --- parse_commit_output ---

func TestParseCommitOutputNormal(t *testing.T) {
	if got := parseCommitOutput("[main abc1234def] add feature"); got != "ok abc1234" {
		t.Errorf("got %q want 'ok abc1234'", got)
	}
}

func TestParseCommitOutputRootCommit(t *testing.T) {
	if got := parseCommitOutput("[main (root-commit) abc1234def] initial commit"); got != "ok abc1234" {
		t.Errorf("got %q want 'ok abc1234'", got)
	}
}

func TestParseCommitOutputMultibyteBranch(t *testing.T) {
	if got := parseCommitOutput("[分支名 abc1234def] 提交消息"); got != "ok abc1234" {
		t.Errorf("got %q want 'ok abc1234'", got)
	}
}

func TestParseCommitOutputThaiBranch(t *testing.T) {
	if got := parseCommitOutput("[สาขา abc1234def] commit message"); got != "ok abc1234" {
		t.Errorf("got %q want 'ok abc1234'", got)
	}
}

func TestParseCommitOutputNoBracket(t *testing.T) {
	if got := parseCommitOutput("some other output"); got != "ok" {
		t.Errorf("got %q want 'ok'", got)
	}
}

func TestParseCommitOutputShortHash(t *testing.T) {
	if got := parseCommitOutput("[main abc12] message"); got != "ok" {
		t.Errorf("got %q want 'ok'", got)
	}
}

func TestParseCommitOutputEmpty(t *testing.T) {
	if got := parseCommitOutput(""); got != "ok" {
		t.Errorf("got %q want 'ok'", got)
	}
}

// --- build_commit_command ---

func TestCommitSingleMessage(t *testing.T) {
	got := buildCommitArgs([]string{"-m", "fix: typo"})
	want := []string{"commit", "-m", "fix: typo"}
	if !equalArgs(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestCommitMultipleMessages(t *testing.T) {
	got := buildCommitArgs([]string{"-m", "feat: add multi-paragraph support", "-m", "This allows git commit -m \"title\" -m \"body\"."})
	want := []string{"commit", "-m", "feat: add multi-paragraph support", "-m", "This allows git commit -m \"title\" -m \"body\"."}
	if !equalArgs(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestCommitAmFlag(t *testing.T) {
	got := buildCommitArgs([]string{"-am", "quick fix"})
	want := []string{"commit", "-am", "quick fix"}
	if !equalArgs(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestCommitAmend(t *testing.T) {
	got := buildCommitArgs([]string{"--amend", "-m", "new msg"})
	want := []string{"commit", "--amend", "-m", "new msg"}
	if !equalArgs(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

// --- push filter (ported from GitPushLineHandler tests) ---

func TestPushFilterDropsProgressPhases(t *testing.T) {
	input := "Enumerating objects: 5, done.\n" +
		"Counting objects: 100% (5/5), done.\n" +
		"Delta compression using up to 8 threads\n" +
		"Compressing objects: 100% (3/3), done.\n" +
		"Writing objects: 100% (3/3), 312 bytes | 312.00 KiB/s, done.\n" +
		"Total 3 (delta 2), reused 0 (delta 0)\n" +
		"To https://github.com/foo/bar.git\n" +
		"   abc1234..def5678  master -> master\n"
	result := filterPushOutput(input, 0)
	for _, prefix := range gitPushNoisePrefixes {
		if strings.Contains(result, prefix) {
			t.Errorf("noise prefix %q leaked through:\n%s", prefix, result)
		}
	}
	if !strings.Contains(result, "To https://github.com/foo/bar.git") {
		t.Errorf("missing destination line:\n%s", result)
	}
	if !strings.Contains(result, "master -> master") {
		t.Errorf("missing ref update:\n%s", result)
	}
	if !strings.HasSuffix(result, "ok master\n") {
		t.Errorf("expected 'ok master' summary, got:\n%s", result)
	}
}

func TestPushFilterUpToDateSummary(t *testing.T) {
	result := filterPushOutput("Everything up-to-date\n", 0)
	if !strings.Contains(result, "Everything up-to-date") {
		t.Errorf("missing pass-through:\n%s", result)
	}
	if !strings.HasSuffix(result, "ok (up-to-date)\n") {
		t.Errorf("expected up-to-date summary, got:\n%s", result)
	}
}

func TestPushFilterPassesRemoteMessagesThrough(t *testing.T) {
	input := "remote: Resolving deltas: 100% (2/2), completed with 2 local objects.\n" +
		"remote: GitHub found 1 vulnerability on foo/bar's default branch (1 moderate).\n" +
		"To https://github.com/foo/bar.git\n" +
		"   abc1234..def5678  feature -> feature\n"
	result := filterPushOutput(input, 0)
	if !strings.Contains(result, "remote: Resolving deltas") {
		t.Errorf("missing remote line:\n%s", result)
	}
	if !strings.Contains(result, "remote: GitHub found 1 vulnerability") {
		t.Errorf("missing remote vuln line:\n%s", result)
	}
	if !strings.HasSuffix(result, "ok feature\n") {
		t.Errorf("expected 'ok feature', got:\n%s", result)
	}
}

func TestPushFilterNoSummaryOnFailure(t *testing.T) {
	input := "To https://github.com/foo/bar.git\n" +
		" ! [rejected]        master -> master (non-fast-forward)\n" +
		"error: failed to push some refs to 'https://github.com/foo/bar.git'\n"
	result := filterPushOutput(input, 1)
	if !strings.Contains(result, "[rejected]") {
		t.Errorf("missing rejected line:\n%s", result)
	}
	if !strings.Contains(result, "error: failed to push") {
		t.Errorf("missing error line:\n%s", result)
	}
	if strings.Contains(result, "ok ") {
		t.Errorf("summary leaked on failure:\n%s", result)
	}
}

func TestPushFilterFirstRefWinsForSummary(t *testing.T) {
	input := "To https://github.com/foo/bar.git\n" +
		"   abc1234..def5678  feat/a -> feat/a\n" +
		"   1111111..2222222  feat/b -> feat/b\n"
	result := filterPushOutput(input, 0)
	if !strings.HasSuffix(result, "ok feat/a\n") {
		t.Errorf("expected 'ok feat/a', got:\n%s", result)
	}
}

func TestPushFilterTokenSavingsOnVerboseOutput(t *testing.T) {
	input := "Enumerating objects: 142, done.\n" +
		"Counting objects: 100% (142/142), done.\n" +
		"Delta compression using up to 8 threads\n" +
		"Compressing objects: 100% (88/88), done.\n" +
		"Writing objects: 100% (104/104), 28.50 KiB | 14.25 MiB/s, done.\n" +
		"Total 104 (delta 64), reused 0 (delta 0), pack-reused 0\n" +
		"remote: Resolving deltas: 100% (64/64), completed with 24 local objects.\n" +
		"To https://github.com/foo/bar.git\n" +
		"   abc1234..def5678  master -> master\n"
	result := filterPushOutput(input, 0)
	countTokens := func(s string) int { return len(strings.Fields(s)) }
	inTok := countTokens(input)
	outTok := countTokens(result)
	savings := 100.0 - (float64(outTok)/float64(inTok))*100.0
	if savings < 60.0 {
		t.Errorf("expected >=60%% savings, got %.1f%% (in=%d out=%d)", savings, inTok, outTok)
	}
}

// --- test helpers ---

func equalArgs(a, b []string) bool {
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

// lineCount mirrors Rust's str::lines().count() (drops a trailing empty line).
func lineCount(s string) int {
	return len(splitLines(s))
}
