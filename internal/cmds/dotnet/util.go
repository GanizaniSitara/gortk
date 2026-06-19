package dotnet

import (
	"regexp"
	"strconv"
	"strings"
)

// namedGroup returns the named capture group from a regexp submatch, or "" if
// absent. Mirrors the Rust `captures.name("x")` accessor.
func namedGroup(re *regexp.Regexp, match []string, name string) string {
	idx := re.SubexpIndex(name)
	if idx < 0 || idx >= len(match) {
		return ""
	}
	return match[idx]
}

func parseInt(s string) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0
	}
	return n
}

func parseIntDefault(s string, def int) int {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return def
	}
	return n
}

func parseU32(s string) uint32 {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 32)
	if err != nil {
		return 0
	}
	return uint32(n)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func max32(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func saturatingSub(a, b int) int {
	if a < b {
		return 0
	}
	return a - b
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func hasASCIIAlpha(s string) bool {
	for _, c := range s {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			return true
		}
	}
	return false
}

// issueKey builds the dedup key matching the Rust 5-tuple
// (code, file, line, column, message).
func issueKey(i BinlogIssue) string {
	return i.Code + "\x00" + i.File + "\x00" +
		strconv.FormatUint(uint64(i.Line), 10) + "\x00" +
		strconv.FormatUint(uint64(i.Column), 10) + "\x00" + i.Message
}

// splitLines mirrors Rust's str::lines(): splits on '\n', strips a trailing
// '\r' from each line, and drops a final empty element produced by a trailing
// newline.
func splitLines(s string) []string {
	parts := strings.Split(s, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	for i := range parts {
		parts[i] = strings.TrimSuffix(parts[i], "\r")
	}
	return parts
}

// divEuclid / remEuclid mirror Rust's i64::div_euclid / rem_euclid (the
// remainder is always non-negative). Binlog tick durations are non-negative in
// practice, but this preserves exact rtk semantics.
func divEuclid(a, b int64) int64 {
	q := a / b
	if a%b < 0 {
		if b > 0 {
			q--
		} else {
			q++
		}
	}
	return q
}

func remEuclid(a, b int64) int64 {
	r := a % b
	if r < 0 {
		if b < 0 {
			r -= b
		} else {
			r += b
		}
	}
	return r
}

func maxCountFrom(re *regexp.Regexp, text string) int {
	best := 0
	for _, m := range re.FindAllStringSubmatch(text, -1) {
		c := parseInt(namedGroup(re, m, "count"))
		if c > best {
			best = c
		}
	}
	return best
}

// truncate caps a string to maxLen runes, appending "..." when cut. Mirrors
// rtk's core::utils::truncate (rtk core helper, reimplemented locally since the
// gortk core does not export it).
func truncate(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	if maxLen < 3 {
		return "..."
	}
	return string(r[:maxLen-3]) + "..."
}
