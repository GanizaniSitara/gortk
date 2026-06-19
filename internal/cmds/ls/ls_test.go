package ls

import (
	"strings"
	"testing"
)

func TestCompactBasic(t *testing.T) {
	input := "total 48\n" +
		"drwxr-xr-x  2 user  staff    64 Jan  1 12:00 .\n" +
		"drwxr-xr-x  2 user  staff    64 Jan  1 12:00 ..\n" +
		"drwxr-xr-x  2 user  staff    64 Jan  1 12:00 src\n" +
		"-rw-r--r--  1 user  staff  1234 Jan  1 12:00 Cargo.toml\n" +
		"-rw-r--r--  1 user  staff  5678 Jan  1 12:00 README.md\n"
	entries, _, _ := compactLS(input, false, false)
	for _, want := range []string{"src/", "Cargo.toml", "README.md", "1.2K", "5.5K"} {
		if !strings.Contains(entries, want) {
			t.Errorf("entries missing %q: %s", want, entries)
		}
	}
	for _, bad := range []string{"drwx", "staff", "total", "\n.\n", "\n..\n"} {
		if strings.Contains(entries, bad) {
			t.Errorf("entries should not contain %q: %s", bad, entries)
		}
	}
}

func TestCompactFiltersNoise(t *testing.T) {
	input := "total 8\n" +
		"drwxr-xr-x  2 user  staff  64 Jan  1 12:00 node_modules\n" +
		"drwxr-xr-x  2 user  staff  64 Jan  1 12:00 .git\n" +
		"drwxr-xr-x  2 user  staff  64 Jan  1 12:00 target\n" +
		"drwxr-xr-x  2 user  staff  64 Jan  1 12:00 src\n" +
		"-rw-r--r--  1 user  staff  100 Jan  1 12:00 main.rs\n"
	entries, _, _ := compactLS(input, false, false)
	for _, bad := range []string{"node_modules", ".git", "target"} {
		if strings.Contains(entries, bad) {
			t.Errorf("should filter %q: %s", bad, entries)
		}
	}
	if !strings.Contains(entries, "src/") || !strings.Contains(entries, "main.rs") {
		t.Errorf("missing real entries: %s", entries)
	}
}

func TestCompactShowAll(t *testing.T) {
	input := "total 8\n" +
		"drwxr-xr-x  2 user  staff  64 Jan  1 12:00 .git\n" +
		"drwxr-xr-x  2 user  staff  64 Jan  1 12:00 src\n"
	entries, _, _ := compactLS(input, true, false)
	if !strings.Contains(entries, ".git/") || !strings.Contains(entries, "src/") {
		t.Errorf("show-all should keep .git: %s", entries)
	}
}

func TestCompactEmpty(t *testing.T) {
	entries, summary, _ := compactLS("total 0\n", false, false)
	if entries != "(empty)\n" {
		t.Errorf("want (empty), got %q", entries)
	}
	if summary != "" {
		t.Errorf("want empty summary, got %q", summary)
	}
}

func TestCompactEmptyEnglishLocale(t *testing.T) {
	input := "total 0\n" +
		"drwxr-xr-x  2 lumin  wheel  64 Apr 23 00:37 .\n" +
		"drwxr-xr-x 16 root  wheel 164576 Apr 23 00:37 ..\n"
	entries, summary, parsed := compactLS(input, false, false)
	if parsed != 0 || entries != "(empty)\n" || summary != "" {
		t.Errorf("empty english locale: parsed=%d entries=%q summary=%q", parsed, entries, summary)
	}
}

func TestCompactSummary(t *testing.T) {
	input := "total 48\n" +
		"drwxr-xr-x  2 user  staff    64 Jan  1 12:00 src\n" +
		"-rw-r--r--  1 user  staff  1234 Jan  1 12:00 main.rs\n" +
		"-rw-r--r--  1 user  staff  5678 Jan  1 12:00 lib.rs\n" +
		"-rw-r--r--  1 user  staff   100 Jan  1 12:00 Cargo.toml\n"
	_, summary, _ := compactLS(input, false, false)
	for _, want := range []string{"Summary: 3 files, 1 dirs", ".rs", ".toml"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary missing %q: %s", want, summary)
		}
	}
}

func TestHumanSize(t *testing.T) {
	cases := []struct {
		in   uint64
		want string
	}{
		{0, "0B"}, {500, "500B"}, {1024, "1.0K"}, {1234, "1.2K"},
		{1_048_576, "1.0M"}, {2_500_000, "2.4M"},
	}
	for _, c := range cases {
		if got := humanSize(c.in); got != c.want {
			t.Errorf("humanSize(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseLSLineBasic(t *testing.T) {
	ft, perms, size, name, ok := parseLSLine("-rw-r--r--  1 user staff 1234 Jan  1 12:00 file.txt")
	if !ok || ft != '-' || perms != "-rw-r--r--" || size != 1234 || name != "file.txt" {
		t.Errorf("got ft=%c perms=%q size=%d name=%q ok=%v", ft, perms, size, name, ok)
	}
}

func TestParseLSLineMultilineGroup(t *testing.T) {
	ft, perms, size, name, ok := parseLSLine("-rw-r--r--  1 fjeanne utilisa. du domaine 0 Mar 31 16:18 empty.txt")
	if !ok || ft != '-' || perms != "-rw-r--r--" || size != 0 || name != "empty.txt" {
		t.Errorf("got ft=%c perms=%q size=%d name=%q ok=%v", ft, perms, size, name, ok)
	}
}

func TestParseLSLineSymlink(t *testing.T) {
	ft, perms, size, name, ok := parseLSLine("lrwxr-xr-x  1 user staff 10 Jan  1 12:00 link -> target")
	if !ok || ft != 'l' || size != 10 || name != "link -> target" {
		t.Errorf("got ft=%c perms=%q size=%d name=%q ok=%v", ft, perms, size, name, ok)
	}
}

func TestPermsToOctalCommon(t *testing.T) {
	cases := map[string]string{
		"-rw-r--r--": "644", "-rwxr-xr-x": "755", "drwxr-xr-x": "755",
		"-rw-------": "600", "-rwxrwxrwx": "777", "----------": "000", "lrwxr-xr-x": "755",
	}
	for in, want := range cases {
		got, ok := permsToOctal(in)
		if !ok || got != want {
			t.Errorf("permsToOctal(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
}

func TestPermsToOctalSpecialBits(t *testing.T) {
	cases := map[string]string{
		"-rwsr-xr-x": "4755", "-rwSr--r--": "4644", "-rwxr-sr-x": "2755",
		"drwxrwxrwt": "1777", "-rwsrwsrwt": "7777",
	}
	for in, want := range cases {
		got, ok := permsToOctal(in)
		if !ok || got != want {
			t.Errorf("permsToOctal(%q) = %q,%v want %q", in, got, ok, want)
		}
	}
}

func TestCompactLongFormatIncludesOctal(t *testing.T) {
	input := "total 48\n" +
		"drwxr-xr-x  2 user  staff    64 Jan  1 12:00 src\n" +
		"-rw-r--r--  1 user  staff  1234 Jan  1 12:00 Cargo.toml\n" +
		"-rwxr-xr-x  1 user  staff   500 Jan  1 12:00 build.sh\n"
	entries, _, _ := compactLS(input, false, true)
	for _, want := range []string{"755  src/", "644  Cargo.toml  1.2K", "755  build.sh  500B"} {
		if !strings.Contains(entries, want) {
			t.Errorf("long format missing %q: %s", want, entries)
		}
	}
}

func TestCompactHandlesFilenamesWithSpaces(t *testing.T) {
	input := "total 8\n-rw-r--r--  1 user  staff  1234 Jan  1 12:00 my file.txt\n"
	entries, _, _ := compactLS(input, false, false)
	if !strings.Contains(entries, "my file.txt") {
		t.Errorf("missing spaced filename: %s", entries)
	}
}
