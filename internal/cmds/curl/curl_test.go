package curl

import (
	"strings"
	"testing"
)

// --- filter_curl_output behavioral spec (ported from rtk #[cfg(test)]) ---

func TestFilterCurlJSONSmallNoTeeHint(t *testing.T) {
	output := `{"r2Ready":true,"status":"ok"}`
	content, truncated := filterCurlOutput(output, true)
	if content != output {
		t.Errorf("content = %q, want %q", content, output)
	}
	if truncated {
		t.Errorf("small JSON must not emit a tee hint")
	}
}

func TestFilterCurlNonJSON(t *testing.T) {
	output := "Hello, World!\nThis is plain text."
	content, _ := filterCurlOutput(output, true)
	if content != output {
		t.Errorf("content = %q, want %q", content, output)
	}
}

func TestFilterCurlLongOutputTruncated(t *testing.T) {
	long := strings.Repeat("x", 1000)
	content, truncated := filterCurlOutput(long, true)
	if !strings.HasPrefix(content, "x") {
		t.Errorf("content should start with x: %q", content)
	}
	if !strings.Contains(content, "bytes total") {
		t.Errorf("content should contain 'bytes total': %q", content)
	}
	if !strings.Contains(content, "1000") {
		t.Errorf("content should contain '1000': %q", content)
	}
	if len(content) >= 600 {
		t.Errorf("content too long: %d", len(content))
	}
	if !truncated {
		t.Errorf("TTY truncation must emit a hint")
	}
}

func TestFilterCurlMultibyteBoundary(t *testing.T) {
	content := strings.Repeat("a", 499) + "é"
	out, _ := filterCurlOutput(content, true)
	if !strings.Contains(out, "bytes total") {
		t.Errorf("content should contain 'bytes total': %q", out)
	}
	if len(out) >= 600 {
		t.Errorf("content too long: %d", len(out))
	}
}

func TestFilterCurlExact500Bytes(t *testing.T) {
	content := strings.Repeat("a", 500)
	out, _ := filterCurlOutput(content, true)
	if !strings.Contains(out, "bytes total") {
		t.Errorf("content should contain 'bytes total': %q", out)
	}
}

// --- #1536: large JSON must remain parseable for downstream tools ---

func TestFilterCurlLargeJSONObjectPassthrough(t *testing.T) {
	payload := strings.Repeat("x", 600)
	json := `{"data":"` + payload + `"}`
	content, truncated := filterCurlOutput(json, true)
	if strings.Contains(content, "bytes total") {
		t.Errorf("large JSON object should not be truncated: %q", content)
	}
	if !strings.HasPrefix(content, "{") || !strings.HasSuffix(content, "}") {
		t.Errorf("JSON object structure lost: %q", content)
	}
	if truncated {
		t.Errorf("JSON passthrough must not emit a tee hint")
	}
}

func TestFilterCurlLargeJSONArrayPassthrough(t *testing.T) {
	var parts []string
	for i := 0; i < 50; i++ {
		parts = append(parts, "{\"id\":"+itoa(i)+",\"name\":\"item-"+pad4(i)+"\"}")
	}
	json := "[" + strings.Join(parts, ",") + "]"
	if len(json) < maxResponseSize {
		t.Fatalf("fixture must exceed cap, got %d", len(json))
	}
	content, _ := filterCurlOutput(json, true)
	if strings.Contains(content, "bytes total") {
		t.Errorf("large JSON array should not be truncated: %q", content)
	}
	if !strings.HasPrefix(content, "[") || !strings.HasSuffix(content, "]") {
		t.Errorf("JSON array structure lost")
	}
}

func TestFilterCurlLargeJSONBareStringPassthrough(t *testing.T) {
	token := strings.Repeat("z", 800)
	json := `"` + token + `"`
	content, _ := filterCurlOutput(json, true)
	if strings.Contains(content, "bytes total") {
		t.Errorf("bare JSON string should not be truncated: %q", content)
	}
	if !strings.HasPrefix(content, "\"") || !strings.HasSuffix(content, "\"") {
		t.Errorf("JSON string structure lost")
	}
}

// --- #1282: pipes / redirects (non-TTY) must receive full body ---

func TestFilterCurlPipeNoTruncationForNonJSON(t *testing.T) {
	long := strings.Repeat("x", 1000)
	content, truncated := filterCurlOutput(long, false)
	if strings.Contains(content, "bytes total") {
		t.Errorf("non-TTY non-JSON should not be truncated: %q", content)
	}
	if len(content) != 1000 {
		t.Errorf("content length = %d, want 1000", len(content))
	}
	if truncated {
		t.Errorf("non-TTY must not emit a tee hint")
	}
}

func TestFilterCurlPipeNoTruncationForJSON(t *testing.T) {
	payload := strings.Repeat("y", 600)
	json := `{"data":"` + payload + `"}`
	content, truncated := filterCurlOutput(json, false)
	if strings.Contains(content, "bytes total") {
		t.Errorf("non-TTY JSON should not be truncated: %q", content)
	}
	if !strings.HasSuffix(content, "}") {
		t.Errorf("JSON structure lost: %q", content)
	}
	if truncated {
		t.Errorf("non-TTY must not emit a tee hint")
	}
}

// --- is_binary tests ----------------------------------------------------

func TestIsBinaryGzipMagicIsNotUTF8(t *testing.T) {
	// gzip magic 1f 8b — 0x8b is an invalid UTF-8 continuation byte.
	b := []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x03}
	if !isBinary(b) {
		t.Errorf("gzip magic should be detected as binary")
	}
}

func TestIsBinaryValidUTF8TextIsNotBinary(t *testing.T) {
	cases := [][]byte{
		[]byte(`{"key": "value"}`),
		[]byte("<!DOCTYPE html>\n<html><body>Hi</body></html>"),
		[]byte("Plain ASCII text"),
		[]byte("Héllo wörld — emojis 🚀 ✓"),
	}
	for _, c := range cases {
		if isBinary(c) {
			t.Errorf("valid UTF-8 wrongly flagged binary: %q", c)
		}
	}
}

func TestIsBinaryEmptyIsNotBinary(t *testing.T) {
	if isBinary([]byte{}) {
		t.Errorf("empty input should not be binary")
	}
}

func TestIsBinaryTextWithNulIsNotBinary(t *testing.T) {
	// NUL is valid UTF-8 (U+0000); only invalid UTF-8 bytes are binary.
	if isBinary([]byte("text with\x00embedded nul")) {
		t.Errorf("NUL-containing text should not be binary")
	}
}

// --- small int helpers (avoid strconv churn in fixtures) ---

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func pad4(n int) string {
	s := itoa(n)
	for len(s) < 4 {
		s = "0" + s
	}
	return s
}
