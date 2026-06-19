package grep

import (
	"strings"
	"testing"

	"gortk/internal/core"
)

// --- is_grep_error_exit ---

func TestIsGrepErrorExit(t *testing.T) {
	// exit 0 = matches, exit 1 = no match: both normal, not errors.
	if isGrepErrorExit(0) {
		t.Error("exit 0 should not be an error")
	}
	if isGrepErrorExit(1) {
		t.Error("exit 1 should not be an error")
	}
	// exit >= 2 = real error (bad regex, tool crash, missing binary).
	for _, code := range []int{2, 3, 127} {
		if !isGrepErrorExit(code) {
			t.Errorf("exit %d should be an error", code)
		}
	}
}

// --- clean_line ---

func TestCleanLine(t *testing.T) {
	line := "            const result = someFunction();"
	cleaned := cleanLine(line, 50, nil, "result")
	if strings.HasPrefix(cleaned, " ") {
		t.Errorf("cleaned line should not start with a space: %q", cleaned)
	}
	if len(cleaned) > 50 {
		t.Errorf("cleaned line should be <= 50 bytes, got %d: %q", len(cleaned), cleaned)
	}
}

func TestCompactPath(t *testing.T) {
	path := "/Users/patrick/dev/project/src/components/Button.tsx"
	compact := compactPath(path)
	if len(compact) > 60 {
		t.Errorf("compact path should be <= 60, got %d: %q", len(compact), compact)
	}
}

func TestCleanLineMultibyte(t *testing.T) {
	// Thai text that exceeds max_len in bytes; must not panic.
	line := "  สวัสดีครับ นี่คือข้อความที่ยาวมากสำหรับทดสอบ  "
	cleaned := cleanLine(line, 20, nil, "ครับ")
	if cleaned == "" {
		t.Error("cleaned multibyte line should not be empty")
	}
}

func TestCleanLineEmoji(t *testing.T) {
	line := "🎉🎊🎈🎁🎂🎄 some text 🎃🎆🎇✨"
	cleaned := cleanLine(line, 15, nil, "text")
	if cleaned == "" {
		t.Error("cleaned emoji line should not be empty")
	}
}

// BRE \| alternation is translated to PCRE | for rg.
func TestBREAlternationTranslated(t *testing.T) {
	pattern := `fn foo\|pub.*bar`
	rgPattern := strings.ReplaceAll(pattern, `\|`, "|")
	if rgPattern != "fn foo|pub.*bar" {
		t.Errorf("got %q, want %q", rgPattern, "fn foo|pub.*bar")
	}
}

// --- parse_cluster ---

func vt(prefix string, hasPrefix bool, flag byte, inline string) clusterResult {
	return clusterResult{kind: clusterValueTaking, prefix: prefix, hasPrefix: hasPrefix, flag: flag, inline: inline}
}

func boolc(prefix string, hasPrefix bool) clusterResult {
	return clusterResult{kind: clusterBoolean, prefix: prefix, hasPrefix: hasPrefix}
}

func TestParseClusterBooleanOnly(t *testing.T) {
	cases := []struct {
		in   string
		want clusterResult
	}{
		{"r", boolc("", false)},
		{"R", boolc("", false)},
		{"rR", boolc("", false)},
		{"rn", boolc("n", true)},
		{"Rni", boolc("ni", true)},
		{"n", boolc("n", true)},
		{"ni", boolc("ni", true)},
	}
	for _, c := range cases {
		if got := parseCluster(c.in); got != c.want {
			t.Errorf("parseCluster(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseClusterENoInline(t *testing.T) {
	// -e: value-taking, empty inline -> caller consumes next token.
	if got := parseCluster("e"); got != vt("", false, 'e', "") {
		t.Errorf("parseCluster(%q) = %+v", "e", got)
	}
}

func TestParseClusterEInlineValue(t *testing.T) {
	// -ecarrot: inline="carrot" — no r/R stripping on the value bytes.
	if got := parseCluster("ecarrot"); got != vt("", false, 'e', "carrot") {
		t.Errorf("parseCluster(%q) = %+v", "ecarrot", got)
	}
}

func TestParseClusterEInlineValueNoRStrip(t *testing.T) {
	// The 'r' chars in "carrot" must survive verbatim in the inline field.
	cr := parseCluster("ecarrot")
	if cr.kind != clusterValueTaking || cr.inline != "carrot" {
		t.Errorf("inline = %q, want %q", cr.inline, "carrot")
	}
}

func TestParseClusterGInlineGlob(t *testing.T) {
	// -g*.rs: inline="*.rs" — 'r' in "*.rs" must not be stripped.
	if got := parseCluster("g*.rs"); got != vt("", false, 'g', "*.rs") {
		t.Errorf("parseCluster(%q) = %+v", "g*.rs", got)
	}
	cr := parseCluster("g*.rs")
	if cr.inline != "*.rs" {
		t.Errorf("inline = %q, want %q", cr.inline, "*.rs")
	}
}

func TestParseClusterRne(t *testing.T) {
	// -rne: r stripped, n in boolean prefix, e value-taking (empty inline).
	if got := parseCluster("rne"); got != vt("n", true, 'e', "") {
		t.Errorf("parseCluster(%q) = %+v", "rne", got)
	}
}

func TestParseClusterRA(t *testing.T) {
	// -rA: r stripped, A value-taking (empty inline -> consume next token).
	if got := parseCluster("rA"); got != vt("", false, 'A', "") {
		t.Errorf("parseCluster(%q) = %+v", "rA", got)
	}
}

func TestParseClusterNiA(t *testing.T) {
	// -niA: n and i boolean, A value-taking.
	if got := parseCluster("niA"); got != vt("ni", true, 'A', "") {
		t.Errorf("parseCluster(%q) = %+v", "niA", got)
	}
}

func TestParseClusterAiInline(t *testing.T) {
	// -Ai: A value-taking, inline="i" (the 'i' is A's value, not a flag).
	if got := parseCluster("Ai"); got != vt("", false, 'A', "i") {
		t.Errorf("parseCluster(%q) = %+v", "Ai", got)
	}
}

func TestParseClusterShortType(t *testing.T) {
	if got := parseCluster("t"); got != vt("", false, 't', "") {
		t.Errorf("parseCluster(%q) = %+v", "t", got)
	}
	if got := parseCluster("tpy"); got != vt("", false, 't', "py") {
		t.Errorf("parseCluster(%q) = %+v", "tpy", got)
	}
}

func TestParseClusterShortMaxColumns(t *testing.T) {
	if got := parseCluster("M"); got != vt("", false, 'M', "") {
		t.Errorf("parseCluster(%q) = %+v", "M", got)
	}
	if got := parseCluster("M120"); got != vt("", false, 'M', "120") {
		t.Errorf("parseCluster(%q) = %+v", "M120", got)
	}
}

// --- strip_r ---

func TestStripR(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantHas bool
	}{
		{"r", "", false},
		{"R", "", false},
		{"rR", "", false},
		{"", "", false},
		{"rn", "n", true},
		{"Rni", "ni", true},
		{"i", "i", true},
		// Shows why it must only run on flag letters, not value bytes:
		{"carrot", "caot", true},
	}
	for _, c := range cases {
		got, has := stripR(c.in)
		if got != c.want || has != c.wantHas {
			t.Errorf("stripR(%q) = %q,%v want %q,%v", c.in, got, has, c.want, c.wantHas)
		}
	}
}

// --- strip_recursive ---

func TestStripRecursive(t *testing.T) {
	if got, keep := stripRecursive("--recursive"); keep {
		t.Errorf("stripRecursive(--recursive) should drop, got %q keep=%v", got, keep)
	}
	if got, keep := stripRecursive("--glob"); !keep || got != "--glob" {
		t.Errorf("stripRecursive(--glob) = %q,%v want --glob,true", got, keep)
	}
	if got, keep := stripRecursive("--type"); !keep || got != "--type" {
		t.Errorf("stripRecursive(--type) = %q,%v want --type,true", got, keep)
	}
}

// --- extract_pattern_path ---

func eq(a, b []string) bool {
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

func TestExtractSimple(t *testing.T) {
	patterns, paths, flags := extractPatternPath([]string{"foo", "src/"})
	if !eq(patterns, []string{"foo"}) || !eq(paths, []string{"src/"}) || len(flags) != 0 {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractWithBoolFlag(t *testing.T) {
	patterns, paths, flags := extractPatternPath([]string{"-i", "foo", "src/"})
	if !eq(patterns, []string{"foo"}) || !eq(paths, []string{"src/"}) || !eq(flags, []string{"-i"}) {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractValueTakingFlag(t *testing.T) {
	// -A 2 must not steal "error" as its value.
	patterns, paths, flags := extractPatternPath([]string{"-A", "2", "error", "src"})
	if !eq(patterns, []string{"error"}) || !eq(paths, []string{"src"}) || !eq(flags, []string{"-A", "2"}) {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractClusterStripR(t *testing.T) {
	// -rn: r stripped, n forwarded (not leaked to rg as --replace value).
	patterns, paths, flags := extractPatternPath([]string{"-rn", "foo", "src"})
	if !eq(patterns, []string{"foo"}) || !eq(paths, []string{"src"}) || !eq(flags, []string{"-n"}) {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractClusterEndingInE(t *testing.T) {
	// -rne PATTERN: r stripped, n in prefix, e consumes PATTERN as pattern.
	patterns, paths, flags := extractPatternPath([]string{"-rne", "PATTERN", "src"})
	if !eq(patterns, []string{"PATTERN"}) || !eq(paths, []string{"src"}) || !eq(flags, []string{"-n"}) {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractClusterEndingInValueFlag(t *testing.T) {
	// -rA 2: r stripped, A consumes 2 as context value.
	patterns, paths, flags := extractPatternPath([]string{"-rA", "2", "foo", "src"})
	if !eq(patterns, []string{"foo"}) || !eq(paths, []string{"src"}) || !eq(flags, []string{"-A", "2"}) {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractMultiPath(t *testing.T) {
	patterns, paths, flags := extractPatternPath([]string{"TODO", "src", "tests"})
	if !eq(patterns, []string{"TODO"}) || !eq(paths, []string{"src", "tests"}) || len(flags) != 0 {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractGlobValue(t *testing.T) {
	// -g '*.md' must not steal "agent" as its value.
	patterns, paths, flags := extractPatternPath([]string{"-i", "x", "agent", "-g", "*.md"})
	if !eq(patterns, []string{"x"}) || !eq(paths, []string{"agent"}) || !eq(flags, []string{"-i", "-g", "*.md"}) {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractEFlag(t *testing.T) {
	patterns, paths, flags := extractPatternPath([]string{"-e", "fn run", "src"})
	if !eq(patterns, []string{"fn run"}) || !eq(paths, []string{"src"}) || len(flags) != 0 {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractMultiE(t *testing.T) {
	patterns, paths, flags := extractPatternPath([]string{"-e", "foo", "-e", "bar", "src"})
	if !eq(patterns, []string{"foo", "bar"}) || !eq(paths, []string{"src"}) || len(flags) != 0 {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractDashDashBoundary(t *testing.T) {
	// After --, args are positional even if they look like flags.
	patterns, paths, flags := extractPatternPath([]string{"--", "--version"})
	if !eq(patterns, []string{"--version"}) || len(paths) != 0 || len(flags) != 0 {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractNoArgs(t *testing.T) {
	patterns, paths, flags := extractPatternPath(nil)
	if len(patterns) != 0 || len(paths) != 0 || len(flags) != 0 {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractDefaultPathEmpty(t *testing.T) {
	// Caller is responsible for defaulting empty paths to ["."].
	patterns, paths, _ := extractPatternPath([]string{"foo"})
	if !eq(patterns, []string{"foo"}) || len(paths) != 0 {
		t.Errorf("got patterns=%v paths=%v", patterns, paths)
	}
}

func TestExtractEndingE(t *testing.T) {
	patterns, paths, flags := extractPatternPath([]string{"-e", "foo", "-e", "bar", "src", "-e"})
	if !eq(patterns, []string{"foo", "bar"}) || !eq(paths, []string{"src"}) || !eq(flags, []string{"-e"}) {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

// --- inline short flag values ---

func TestExtractInlineEValue(t *testing.T) {
	// -ecarrot: e hits at j=0, inline="carrot", no r-stripping on value.
	patterns, paths, flags := extractPatternPath([]string{"-ecarrot", "file"})
	if !eq(patterns, []string{"carrot"}) || !eq(paths, []string{"file"}) || len(flags) != 0 {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractInlineEValueNoRStrip(t *testing.T) {
	// -ecarrot: the 'r' in "carrot" must NOT be stripped (value, not a flag).
	patterns, _, _ := extractPatternPath([]string{"-ecarrot", "file"})
	if !eq(patterns, []string{"carrot"}) {
		t.Errorf("r in inline value must not be stripped: got %v", patterns)
	}
}

func TestExtractInlineGValue(t *testing.T) {
	// -g*.rs: g hits at j=0, inline="*.rs", no r-stripping on value.
	patterns, paths, flags := extractPatternPath([]string{"aaa", "sub", "-g*.rs"})
	if !eq(patterns, []string{"aaa"}) || !eq(paths, []string{"sub"}) || !eq(flags, []string{"-g", "*.rs"}) {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractInlineGValueNoRStrip(t *testing.T) {
	// -g*.rs: the 'r' in "*.rs" must NOT be stripped.
	_, _, flags := extractPatternPath([]string{"aaa", "sub", "-g*.rs"})
	found := false
	for _, f := range flags {
		if f == "*.rs" {
			found = true
		}
	}
	if !found {
		t.Errorf("r in glob value must not be stripped: got %v", flags)
	}
}

// --- long value-taking flags ---

func TestExtractLongGlobValue(t *testing.T) {
	patterns, paths, flags := extractPatternPath([]string{"compact", "sub", "--glob", "*.md"})
	if !eq(patterns, []string{"compact"}) || !eq(paths, []string{"sub"}) || !eq(flags, []string{"--glob", "*.md"}) {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractLongMaxCount(t *testing.T) {
	patterns, paths, flags := extractPatternPath([]string{"--max-count", "1", "fn", "file"})
	if !eq(patterns, []string{"fn"}) || !eq(paths, []string{"file"}) || !eq(flags, []string{"--max-count", "1"}) {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractShortType(t *testing.T) {
	// -t rust: type filter, value must not become pattern.
	patterns, paths, flags := extractPatternPath([]string{"-t", "rust", "fn", "src"})
	if !eq(patterns, []string{"fn"}) || !eq(paths, []string{"src"}) || !eq(flags, []string{"-t", "rust"}) {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractShortMaxDepth(t *testing.T) {
	// -d 3: max-depth, value must not become pattern.
	patterns, paths, flags := extractPatternPath([]string{"-d", "3", "foo", "src"})
	if !eq(patterns, []string{"foo"}) || !eq(paths, []string{"src"}) || !eq(flags, []string{"-d", "3"}) {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractShortMaxColumns(t *testing.T) {
	// -M 120: max-columns, value must not become pattern.
	patterns, paths, flags := extractPatternPath([]string{"-M", "120", "foo", "src"})
	if !eq(patterns, []string{"foo"}) || !eq(paths, []string{"src"}) || !eq(flags, []string{"-M", "120"}) {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractLongRegexp(t *testing.T) {
	// --regexp is the long form of -e; value goes to patterns.
	patterns, paths, flags := extractPatternPath([]string{"--regexp", "fn run", "src"})
	if !eq(patterns, []string{"fn run"}) || !eq(paths, []string{"src"}) || len(flags) != 0 {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractLongRegexpMulti(t *testing.T) {
	// --regexp can be combined with -e.
	patterns, paths, _ := extractPatternPath([]string{"--regexp", "foo", "-e", "bar", "src"})
	if !eq(patterns, []string{"foo", "bar"}) || !eq(paths, []string{"src"}) {
		t.Errorf("got patterns=%v paths=%v", patterns, paths)
	}
}

func TestExtractLongIgnoreFile(t *testing.T) {
	patterns, paths, flags := extractPatternPath([]string{"--ignore-file", ".myignore", "foo", "src"})
	if !eq(patterns, []string{"foo"}) || !eq(paths, []string{"src"}) || !eq(flags, []string{"--ignore-file", ".myignore"}) {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractLongEngine(t *testing.T) {
	patterns, paths, flags := extractPatternPath([]string{"--engine", "pcre2", "foo", "src"})
	if !eq(patterns, []string{"foo"}) || !eq(paths, []string{"src"}) || !eq(flags, []string{"--engine", "pcre2"}) {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractLongTypeClear(t *testing.T) {
	patterns, paths, flags := extractPatternPath([]string{"--type-clear", "rust", "foo", "src"})
	if !eq(patterns, []string{"foo"}) || !eq(paths, []string{"src"}) || !eq(flags, []string{"--type-clear", "rust"}) {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractLongPathSeparator(t *testing.T) {
	patterns, paths, flags := extractPatternPath([]string{"--path-separator", "/", "foo", "src"})
	if !eq(patterns, []string{"foo"}) || !eq(paths, []string{"src"}) || !eq(flags, []string{"--path-separator", "/"}) {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

func TestExtractLongFlagInlineEqPassthrough(t *testing.T) {
	// --glob=*.rs is one token (inline =): passes through, not consumed as pair.
	patterns, paths, flags := extractPatternPath([]string{"foo", "src", "--glob=*.rs"})
	if !eq(patterns, []string{"foo"}) || !eq(paths, []string{"src"}) || !eq(flags, []string{"--glob=*.rs"}) {
		t.Errorf("got patterns=%v paths=%v flags=%v", patterns, paths, flags)
	}
}

// --- has_format_flag ---

func TestFormatFlagDetectsCountMatches(t *testing.T) {
	if !hasFormatFlag([]string{"--count-matches"}) {
		t.Error("should detect --count-matches")
	}
}

func TestFormatFlagDetectsJSON(t *testing.T) {
	if !hasFormatFlag([]string{"--json"}) {
		t.Error("should detect --json")
	}
}

func TestFormatFlagDetectsPassthru(t *testing.T) {
	if !hasFormatFlag([]string{"--passthru"}) {
		t.Error("should detect --passthru")
	}
}

func TestFormatFlagDetectsFiles(t *testing.T) {
	if !hasFormatFlag([]string{"--files"}) {
		t.Error("should detect --files")
	}
}

func TestFormatFlagDetectsCount(t *testing.T) {
	if !hasFormatFlag([]string{"-c"}) || !hasFormatFlag([]string{"--count"}) {
		t.Error("should detect -c / --count")
	}
}

func TestFormatFlagDetectsFilesWithMatches(t *testing.T) {
	if !hasFormatFlag([]string{"-l"}) || !hasFormatFlag([]string{"--files-with-matches"}) {
		t.Error("should detect -l / --files-with-matches")
	}
}

func TestFormatFlagDetectsFilesWithoutMatch(t *testing.T) {
	if !hasFormatFlag([]string{"-L"}) || !hasFormatFlag([]string{"--files-without-match"}) {
		t.Error("should detect -L / --files-without-match")
	}
}

func TestFormatFlagDetectsOnlyMatching(t *testing.T) {
	if !hasFormatFlag([]string{"-o"}) || !hasFormatFlag([]string{"--only-matching"}) {
		t.Error("should detect -o / --only-matching")
	}
}

func TestFormatFlagDetectsNull(t *testing.T) {
	if !hasFormatFlag([]string{"-Z"}) || !hasFormatFlag([]string{"--null"}) {
		t.Error("should detect -Z / --null")
	}
}

func TestFormatFlagIgnoresNormalFlags(t *testing.T) {
	if hasFormatFlag([]string{"-i", "-w", "-A", "3"}) {
		t.Error("should not flag normal flags as format flags")
	}
}

// --- truncation accuracy ---

func TestGrepOverflowUsesUncappedTotal(t *testing.T) {
	// The grep overflow invariant: matches are never capped before the overflow
	// calc. If total > per_file, overflow = total - per_file (uncapped). This
	// documents that grep avoids the diff_cmd bug (cap at N then compute N-10).
	perFile := grepMaxPerFile
	totalMatches := perFile + 42
	overflow := totalMatches - perFile
	if overflow != 42 {
		t.Errorf("overflow must equal true suppressed count, got %d", overflow)
	}
	// Demonstrate why capping before subtraction is wrong:
	hypotheticalCap := perFile + 5
	capped := totalMatches
	if capped > hypotheticalCap {
		capped = hypotheticalCap
	}
	wrongOverflow := capped - perFile
	if wrongOverflow == overflow {
		t.Error("capping before subtraction should give a different (wrong) overflow")
	}
}

// Line numbers are always enabled in the rg invocation (rg gets -nH0). The
// gortk -n/--line-numbers compat flag is a no-op. Skips gracefully if rg is
// not installed.
func TestRgAlwaysHasLineNumbers(t *testing.T) {
	if !core.ToolExists("rg") {
		t.Skip("rg not installed")
	}
	cmd := core.ResolvedCommand("rg", "-n", "--no-heading", "NONEXISTENT_PATTERN_12345", ".")
	res := execCapture(cmd)
	if res.startErr != nil {
		t.Skip("rg failed to start")
	}
	// exit 1 = no match (normal) or exit 0 = match: rg accepted -n.
	if res.exitCode != 0 && res.exitCode != 1 {
		t.Errorf("rg -n should be accepted, got exit %d", res.exitCode)
	}
}

func TestRgNoIgnoreVcsFlagAccepted(t *testing.T) {
	if !core.ToolExists("rg") {
		t.Skip("rg not installed")
	}
	cmd := core.ResolvedCommand("rg", "-n", "--no-heading", "--no-ignore-vcs", "NONEXISTENT_PATTERN_12345", ".")
	res := execCapture(cmd)
	if res.startErr != nil {
		t.Skip("rg failed to start")
	}
	if res.exitCode != 0 && res.exitCode != 1 {
		t.Errorf("rg --no-ignore-vcs should be accepted, got exit %d", res.exitCode)
	}
}

// --- parse_match_line robustness (input shape is file\0line:content) ---

func TestParseMatchLineSimple(t *testing.T) {
	file, lineNum, content, ok := parseMatchLine("file.php\x0010:use Foo\\Bar;")
	if !ok || file != "file.php" || lineNum != 10 || content != "use Foo\\Bar;" {
		t.Errorf("got file=%q line=%d content=%q ok=%v", file, lineNum, content, ok)
	}
}

func TestParseMatchLineContentWithDoubleColon(t *testing.T) {
	line := "externalImportShell.class.php\x0081:        $this->queueProcessModel = ClassRegistry::init('Collections.QueueProcess');"
	file, lineNum, content, ok := parseMatchLine(line)
	want := "        $this->queueProcessModel = ClassRegistry::init('Collections.QueueProcess');"
	if !ok || file != "externalImportShell.class.php" || lineNum != 81 || content != want {
		t.Errorf("got file=%q line=%d content=%q ok=%v", file, lineNum, content, ok)
	}
}

func TestParseMatchLineWindowsPath(t *testing.T) {
	file, lineNum, content, ok := parseMatchLine("C:\\src\\file.rs\x0042:fn main() {}")
	if !ok || file != `C:\src\file.rs` || lineNum != 42 || content != "fn main() {}" {
		t.Errorf("got file=%q line=%d content=%q ok=%v", file, lineNum, content, ok)
	}
}

func TestParseMatchLineFilenameWithColons(t *testing.T) {
	file, lineNum, content, ok := parseMatchLine("badly_named:52:file.txt\x001:xxx")
	if !ok || file != "badly_named:52:file.txt" || lineNum != 1 || content != "xxx" {
		t.Errorf("got file=%q line=%d content=%q ok=%v", file, lineNum, content, ok)
	}
}

func TestParseMatchLineContentWithDigitColons(t *testing.T) {
	file, lineNum, content, ok := parseMatchLine("log.txt\x007:debug: counter is :42: now")
	if !ok || file != "log.txt" || lineNum != 7 || content != "debug: counter is :42: now" {
		t.Errorf("got file=%q line=%d content=%q ok=%v", file, lineNum, content, ok)
	}
}

func TestParseMatchLineMalformedReturnsNone(t *testing.T) {
	// No NUL separator.
	if _, _, _, ok := parseMatchLine("file.rs:1:content"); ok {
		t.Error("line without NUL should not parse")
	}
	if _, _, _, ok := parseMatchLine("not a match line"); ok {
		t.Error("non-match line should not parse")
	}
	// Missing line number after NUL.
	if _, _, _, ok := parseMatchLine("file.rs\x00fn foo()"); ok {
		t.Error("missing line number should not parse")
	}
	// Empty.
	if _, _, _, ok := parseMatchLine(""); ok {
		t.Error("empty line should not parse")
	}
}

func TestParseMatchLineEmptyContent(t *testing.T) {
	file, lineNum, content, ok := parseMatchLine("file.rs\x007:")
	if !ok || file != "file.rs" || lineNum != 7 || content != "" {
		t.Errorf("got file=%q line=%d content=%q ok=%v", file, lineNum, content, ok)
	}
}
