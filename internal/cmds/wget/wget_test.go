package wget

import (
	"strings"
	"testing"
)

func TestCompactURLStripsProtocol(t *testing.T) {
	if got := compactURL("https://example.com/file.zip"); got != "example.com/file.zip" {
		t.Errorf("https strip: got %q", got)
	}
	if got := compactURL("http://example.com/file.zip"); got != "example.com/file.zip" {
		t.Errorf("http strip: got %q", got)
	}
}

func TestCompactURLTruncatesLongURL(t *testing.T) {
	long := "https://example.com/very/long/path/that/exceeds/fifty/characters/file.zip"
	result := compactURL(long)
	if !strings.Contains(result, "...") {
		t.Errorf("long URL should be truncated with ...: %q", result)
	}
	if len(result) >= len(long) {
		t.Errorf("truncated result should be shorter: %q", result)
	}
}

func TestCompactURLShortUnchanged(t *testing.T) {
	if got := compactURL("https://x.com/f"); got != "x.com/f" {
		t.Errorf("short unchanged: got %q", got)
	}
}

func TestFormatSizeZero(t *testing.T) {
	if got := formatSize(0); got != "?" {
		t.Errorf("formatSize(0) = %q, want ?", got)
	}
}

func TestFormatSizeBytes(t *testing.T) {
	if got := formatSize(512); got != "512B" {
		t.Errorf("formatSize(512) = %q, want 512B", got)
	}
}

func TestFormatSizeKilobytes(t *testing.T) {
	result := formatSize(2048)
	if !strings.HasSuffix(result, "KB") {
		t.Errorf("expected KB, got %q", result)
	}
}

func TestFormatSizeMegabytes(t *testing.T) {
	result := formatSize(2 * 1024 * 1024)
	if !strings.HasSuffix(result, "MB") {
		t.Errorf("expected MB, got %q", result)
	}
}

func TestParseError404(t *testing.T) {
	if got := parseError("HTTP request failed: 404", ""); got != "404 Not Found" {
		t.Errorf("got %q", got)
	}
}

func TestParseErrorDNS(t *testing.T) {
	if got := parseError("unable to resolve host example.com", ""); got != "DNS lookup failed" {
		t.Errorf("got %q", got)
	}
}

func TestParseErrorSSL(t *testing.T) {
	if got := parseError("SSL certificate verification failed", ""); got != "SSL/TLS error" {
		t.Errorf("got %q", got)
	}
}

func TestParseErrorUnknown(t *testing.T) {
	if got := parseError("", ""); got != "Unknown error" {
		t.Errorf("got %q", got)
	}
}

func TestTruncateLineShort(t *testing.T) {
	if got := truncateLine("hello", 10); got != "hello" {
		t.Errorf("got %q", got)
	}
}

func TestTruncateLineExact(t *testing.T) {
	if got := truncateLine("hello", 5); got != "hello" {
		t.Errorf("got %q", got)
	}
}

func TestTruncateLineLong(t *testing.T) {
	result := truncateLine("hello world this is long", 10)
	if !strings.HasSuffix(result, "...") {
		t.Errorf("should end with ...: %q", result)
	}
	if len(result) > 10 {
		t.Errorf("len should be <= 10: %q (%d)", result, len(result))
	}
}

func TestExtractFilenameFromOutputFlag(t *testing.T) {
	args := []string{"-O", "myfile.zip"}
	if got := extractFilenameFromOutput("", "https://example.com/x", args); got != "myfile.zip" {
		t.Errorf("got %q", got)
	}
}

func TestExtractFilenameFromURLFallback(t *testing.T) {
	if got := extractFilenameFromOutput("", "https://example.com/file.tar.gz", nil); got != "file.tar.gz" {
		t.Errorf("got %q", got)
	}
}

func TestExtractFilenameEmptyURLFallback(t *testing.T) {
	if got := extractFilenameFromOutput("", "https://example.com/", nil); got != "index.html" {
		t.Errorf("got %q", got)
	}
}
