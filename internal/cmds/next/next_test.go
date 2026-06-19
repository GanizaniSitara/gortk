package next

import (
	"strings"
	"testing"
)

// Ported from rtk's next_cmd.rs #[cfg(test)] test_filter_next_build.
func TestFilterNextBuild(t *testing.T) {
	output := `
   ▲ Next.js 15.2.0

   Creating an optimized production build ...
✓ Compiled successfully
✓ Linting and checking validity of types
✓ Collecting page data
○ /                            1.2 kB        132 kB
● /dashboard                   2.5 kB        156 kB
○ /api/auth                    0.5 kB         89 kB

Route (app)                    Size     First Load JS
┌ ○ /                          1.2 kB        132 kB
├ ● /dashboard                 2.5 kB        156 kB
└ ○ /api/auth                  0.5 kB         89 kB

○  (Static)  prerendered as static content
●  (SSG)     prerendered as static HTML
λ  (Server)  server-side renders at runtime

✓ Built in 34.2s
`
	result := filterNextBuild(output)
	if !strings.Contains(result, "Next.js Build") {
		t.Errorf("missing header: %s", result)
	}
	if !strings.Contains(result, "routes") {
		t.Errorf("missing routes summary: %s", result)
	}
	if strings.Contains(result, "Creating an optimized") {
		t.Errorf("should filter verbose logs: %s", result)
	}
}

// Ported from rtk's next_cmd.rs #[cfg(test)] test_extract_time.
func TestExtractTime(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{"Built in 34.2s", "34.2s", true},
		{"Compiled in 1250ms", "1250ms", true},
		{"No time here", "", false},
	}
	for _, c := range cases {
		got, ok := extractTime(c.in)
		if ok != c.wantOK || got != c.want {
			t.Errorf("extractTime(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

// truncate mirrors rtk's utils::truncate; covers the same edge cases the core
// helper relies on (no-op, ellipsis, tiny max_len).
func TestTruncate(t *testing.T) {
	cases := []struct {
		in     string
		maxLen int
		want   string
	}{
		{"short", 30, "short"},
		{"exactly-ten", 11, "exactly-ten"},
		{"this-is-a-very-long-route-name-indeed", 10, "this-is..."},
		{"abcdef", 2, "..."},
		{"/dashboard", 10, "/dashboard"},
	}
	for _, c := range cases {
		if got := truncate(c.in, c.maxLen); got != c.want {
			t.Errorf("truncate(%q,%d) = %q want %q", c.in, c.maxLen, got, c.want)
		}
	}
}
