package pipe

import (
	"strings"
	"testing"
)

// --- grep filter (ported from rtk grep_wrapper / resolve_filter("grep")) ---

func TestGrepFilterBasic(t *testing.T) {
	input := "src/main.rs:42:fn main() {\nsrc/lib.rs:10:pub fn helper() {}\n"
	out := grepFilter(input)
	if !strings.Contains(out, "main.rs") || !strings.Contains(out, "matches") {
		t.Errorf("grep filter missing expected content: %s", out)
	}
}

func TestGrepFilterCountsAndGroups(t *testing.T) {
	input := "a.rs:1:x\na.rs:2:y\nb.rs:5:z\n"
	out := grepFilter(input)
	if !strings.Contains(out, "3 matches in 2F:") {
		t.Errorf("want '3 matches in 2F:', got: %s", out)
	}
	if !strings.Contains(out, "[file] a.rs (2):") {
		t.Errorf("want grouped file a.rs (2): %s", out)
	}
	if !strings.Contains(out, "[file] b.rs (1):") {
		t.Errorf("want grouped file b.rs (1): %s", out)
	}
}

func TestGrepFilterNonGrepInputPassthrough(t *testing.T) {
	input := "just some text\nwith no file:line:content shape\n"
	out := grepFilter(input)
	if out != input {
		t.Errorf("non-grep input should pass through unchanged, got: %s", out)
	}
}

func TestGrepFilterTruncatesPerFile(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		b.WriteString("file.rs:")
		b.WriteString(itoa(i * 10))
		b.WriteString(":content\n")
	}
	out := grepFilter(b.String())
	// 20 matches in one file, cap is maxPipeMatches (10) → expect a "+10" overflow marker.
	if !strings.Contains(out, "+10") {
		t.Errorf("expected per-file truncation marker +10, got: %s", out)
	}
}

// Token-savings spirit check ported from rtk test_grep_wrapper_token_savings.
func TestGrepFilterTokenSavings(t *testing.T) {
	var b strings.Builder
	for file := 1; file <= 10; file++ {
		for line := 1; line <= 20; line++ {
			b.WriteString("src/cmds/module")
			b.WriteString(itoa(file))
			b.WriteString("/handler.rs:")
			b.WriteString(itoa(line * 10))
			b.WriteString(":    let result = process_request(ctx, &payload).await?;\n")
		}
	}
	input := b.String()
	out := grepFilter(input)
	savings := 100.0 - (float64(countTokens(out))/float64(countTokens(input)))*100.0
	if savings < 40.0 {
		t.Errorf("grep filter: expected >=40%% savings, got %.1f%% (in=%d out=%d)",
			savings, countTokens(input), countTokens(out))
	}
}

// --- find filter (ported from rtk find_wrapper / resolve_filter("find")) ---

func TestFindFilterBasic(t *testing.T) {
	input := "./src/main.rs\n./src/lib.rs\n./tests/foo.rs\n"
	out := findFilter(input)
	if !strings.Contains(out, "3 files") {
		t.Errorf("find filter missing '3 files': %s", out)
	}
}

func TestFindFilterAbsolutePaths(t *testing.T) {
	input := "/home/user/src/main.rs\n/home/user/src/lib.rs\n/home/user/tests/foo.rs\n"
	out := findFilter(input)
	if !strings.Contains(out, "3 files") {
		t.Errorf("find filter missing '3 files': %s", out)
	}
}

func TestFindFilterEmptyInputPassthrough(t *testing.T) {
	if out := findFilter(""); out != "" {
		t.Errorf("empty input should pass through, got: %s", out)
	}
	blank := "\n  \n\n"
	if out := findFilter(blank); out != blank {
		t.Errorf("all-blank input should pass through, got: %q", out)
	}
}

func TestFindFilterGroupsByDir(t *testing.T) {
	input := "./src/a.rs\n./src/b.rs\n./tests/c.rs\n"
	out := findFilter(input)
	if !strings.Contains(out, "3 files in 2 dirs:") {
		t.Errorf("want '3 files in 2 dirs:', got: %s", out)
	}
	if !strings.Contains(out, "./src/  (2)") {
		t.Errorf("want './src/  (2)', got: %s", out)
	}
}

func TestFindFilterTokenSavings(t *testing.T) {
	var b strings.Builder
	for dir := 1; dir <= 30; dir++ {
		for file := 1; file <= 17; file++ {
			b.WriteString("./src/components/feature")
			b.WriteString(itoa(dir))
			b.WriteString("/sub_")
			b.WriteString(itoa(dir))
			b.WriteString("/component_")
			b.WriteString(itoa(file))
			b.WriteString(".tsx\n")
		}
	}
	input := b.String()
	out := findFilter(input)
	savings := 100.0 - (float64(countTokens(out))/float64(countTokens(input)))*100.0
	if savings < 40.0 {
		t.Errorf("find filter: expected >=40%% savings, got %.1f%% (in=%d out=%d)",
			savings, countTokens(input), countTokens(out))
	}
}

// --- name resolution (ported from rtk resolve_filter tests) ---

func TestResolvePureFilterNames(t *testing.T) {
	for _, name := range []string{"grep", "rg", "find", "fd"} {
		out, ok := applyNamedFilter(name, "src/main.rs:1:x\nsrc/lib.rs:2:y\n")
		if !ok {
			t.Errorf("filter %q should resolve", name)
		}
		_ = out
	}
}

func TestResolveTOMLFilterByName(t *testing.T) {
	// "make" is a known builtin TOML filter; it must resolve standalone.
	if cf := findTOMLFilter("make"); cf == nil {
		t.Fatal("expected builtin TOML filter 'make' to be resolvable")
	}
	_, ok := applyNamedFilter("make", "make[1]: Entering directory\nbuilding\n")
	if !ok {
		t.Error("make filter should resolve via tomlfilter")
	}
}

func TestResolveUnknownFallsBack(t *testing.T) {
	_, ok := applyNamedFilter("nonexistent-filter-xyz", "anything\n")
	if ok {
		t.Error("unknown filter must not resolve (should fall back to passthrough)")
	}
}

func TestSupportedFilterNamesIncludesPureAndTOML(t *testing.T) {
	names := SupportedFilterNames()
	idx := map[string]bool{}
	for _, n := range names {
		idx[n] = true
	}
	for _, want := range []string{"grep", "rg", "find", "fd", "make", "jq"} {
		if !idx[want] {
			t.Errorf("SupportedFilterNames missing %q; got %v", want, names)
		}
	}
}

// --- arg parsing ---

func TestParseArgs(t *testing.T) {
	cases := []struct {
		args   []string
		filter string
		pass   bool
		err    bool
	}{
		{[]string{"-f", "grep"}, "grep", false, false},
		{[]string{"--filter", "find"}, "find", false, false},
		{[]string{"--filter=make"}, "make", false, false},
		{[]string{"-f=jq"}, "jq", false, false},
		{[]string{"-fgrep"}, "grep", false, false},
		{[]string{"--passthrough"}, "", true, false},
		{[]string{"-f", "grep", "--passthrough"}, "grep", true, false},
		{[]string{"-f"}, "", false, true},
		{[]string{"bogus"}, "", false, true},
	}
	for _, c := range cases {
		f, p, err := parseArgs(c.args)
		if c.err {
			if err == nil {
				t.Errorf("parseArgs(%v) expected error", c.args)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseArgs(%v) unexpected error: %v", c.args, err)
			continue
		}
		if f != c.filter || p != c.pass {
			t.Errorf("parseArgs(%v) = (%q,%v), want (%q,%v)", c.args, f, p, c.filter, c.pass)
		}
	}
}

func TestReadStdinCappedRejectsOversize(t *testing.T) {
	big := strings.NewReader(strings.Repeat("x", 11))
	if _, err := readStdinCapped(big, 10); err == nil {
		t.Error("expected oversize stdin to error")
	}
	ok := strings.NewReader("hello")
	if s, err := readStdinCapped(ok, 10); err != nil || s != "hello" {
		t.Errorf("readStdinCapped small input = (%q,%v)", s, err)
	}
}

// --- tiny stdlib-only helpers for tests (avoid strconv import churn) ---

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func countTokens(s string) int {
	return len(strings.Fields(s))
}
