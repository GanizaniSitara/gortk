package logcmd

import (
	"strings"
	"testing"
)

// Ported from rtk log_cmd.rs test_analyze_logs.
func TestAnalyzeLogs(t *testing.T) {
	logs := "\n" +
		"2024-01-01 10:00:00 ERROR: Connection failed to /api/server\n" +
		"2024-01-01 10:00:01 ERROR: Connection failed to /api/server\n" +
		"2024-01-01 10:00:02 ERROR: Connection failed to /api/server\n" +
		"2024-01-01 10:00:03 WARN: Retrying connection\n" +
		"2024-01-01 10:00:04 INFO: Connected\n"
	result := analyzeLogs(logs)
	if !strings.Contains(result, "×3") {
		t.Errorf("expected ×3 dedup count, got:\n%s", result)
	}
	if !strings.Contains(result, "ERRORS") {
		t.Errorf("expected ERRORS section, got:\n%s", result)
	}
}

// Ported from rtk log_cmd.rs test_analyze_logs_extended_severity_keywords.
func TestAnalyzeLogsExtendedSeverityKeywords(t *testing.T) {
	logs := "2024-01-01 10:00:00 CRITICAL: disk full\n" +
		"2024-01-01 10:00:01 ALERT: memory pressure\n" +
		"2024-01-01 10:00:02 emerg: system shutdown imminent\n" +
		"2024-01-01 10:00:03 SEVERE: data corruption detected\n" +
		"2024-01-01 10:00:04 notice: config reloaded\n"
	result := analyzeLogs(logs)
	if !strings.Contains(result, "ERRORS") {
		t.Errorf("critical/alert/emerg/severe should count as errors, got:\n%s", result)
	}
	if !strings.Contains(result, "WARNINGS") {
		t.Errorf("notice should count as warning, got:\n%s", result)
	}
}

// Ported from rtk log_cmd.rs test_analyze_logs_multibyte.
func TestAnalyzeLogsMultibyte(t *testing.T) {
	errMsg := strings.Repeat("ข้อผิดพลาด", 15)
	warnMsg := strings.Repeat("คำเตือน", 15)
	logs := "2024-01-01 10:00:00 ERROR: " + errMsg + " connection failed\n" +
		"2024-01-01 10:00:01 WARN: " + warnMsg + " retry attempt\n"
	// Should not panic even with very long multi-byte messages.
	result := analyzeLogs(logs)
	if !strings.Contains(result, "ERRORS") {
		t.Errorf("expected ERRORS, got:\n%s", result)
	}
}

func TestNormalizeLogLine(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// Timestamp stripped from the front.
		{"2024-01-01 10:00:00 ERROR: boom", "ERROR: boom"},
		// UUID anonymized.
		{"req 123e4567-e89b-12d3-a456-426614174000 done", "req <UUID> done"},
		// Hex anonymized.
		{"addr 0xDEADBEEF here", "addr <HEX> here"},
		// 4+ digit number anonymized; 3-digit left alone.
		{"port 8080 vs 200", "port <NUM> vs 200"},
		// Path anonymized.
		{"reading /etc/hosts now", "reading <PATH> now"},
	}
	for _, c := range cases {
		if got := normalizeLogLine(c.in); got != c.want {
			t.Errorf("normalizeLogLine(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Distinct paths normalize to the same form, so lines dedup together.
func TestNormalizeDedupsAcrossVolatileFields(t *testing.T) {
	a := normalizeLogLine("2024-01-01 10:00:00 ERROR: failed /api/server/12345")
	b := normalizeLogLine("2024-01-02 11:30:59 ERROR: failed /api/server/67890")
	if a != b {
		t.Errorf("expected dedup-equal normals, got %q vs %q", a, b)
	}
}

// formatCounted shows the ×N prefix only for counts > 1, and truncates long
// lines (>100 bytes) to 97 runes + "...".
func TestFormatCounted(t *testing.T) {
	if got := formatCounted("hello", 1); got != "   hello" {
		t.Errorf("count 1 = %q, want %q", got, "   hello")
	}
	if got := formatCounted("hello", 3); got != "   [×3] hello" {
		t.Errorf("count 3 = %q, want %q", got, "   [×3] hello")
	}
	long := strings.Repeat("a", 150)
	got := formatCounted(long, 1)
	if !strings.HasSuffix(got, "...") {
		t.Errorf("long line should be truncated with ..., got %q", got)
	}
	// 97 'a' runes + "..." prefixed with the three-space indent.
	if got != "   "+strings.Repeat("a", 97)+"..." {
		t.Errorf("truncation length wrong: %q", got)
	}
}

// The summary header always reports totals and unique counts.
func TestAnalyzeLogsSummaryCounts(t *testing.T) {
	logs := "ERROR: a\nERROR: a\nWARN: b\nINFO: c\nINFO: c\nINFO: c\n"
	result := analyzeLogs(logs)
	if !strings.Contains(result, "[error] 2 errors (1 unique)") {
		t.Errorf("error summary wrong:\n%s", result)
	}
	if !strings.Contains(result, "[warn] 1 warnings (1 unique)") {
		t.Errorf("warn summary wrong:\n%s", result)
	}
	if !strings.Contains(result, "[info] 3 info messages") {
		t.Errorf("info summary wrong:\n%s", result)
	}
}
