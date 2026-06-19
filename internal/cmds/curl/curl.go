// Package curl is gortk's token-optimized HTTP fetcher. It wraps the native
// `curl` tool, captures the response, and condenses long non-JSON bodies for
// human consumption while passing JSON, piped output, and binary downloads
// through unchanged. Faithful port of rtk's src/cmds/cloud/curl_cmd.rs.
//
// For pipes / redirects (non-TTY) and JSON bodies the full response is passed
// through unchanged — truncating mid-stream would break downstream parsers.
// The condensed-form-with-tee-hint path is reserved for non-JSON bodies on a
// real terminal where a human reads the output and the tee file gives the LLM
// a way to recover the raw response.
//
// Binary downloads (any non-UTF-8 byte sequence) are written through to stdout
// as raw bytes, bypassing the lossy UTF-8 conversion that would otherwise
// replace non-UTF-8 bytes with U+FFFD and corrupt the stream (rtk #1087).
package curl

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"gortk/internal/core"
	"gortk/internal/registry"
)

// maxResponseSize is the byte threshold above which non-JSON bodies on a TTY
// are truncated (mirrors rtk's MAX_RESPONSE_SIZE).
const maxResponseSize = 500

func init() {
	registry.Register(&registry.Cmd{
		Name:    "curl",
		Summary: "Fetch a URL with auto-JSON detection and condensed output",
		Run:     Run,
	})
}

// Run executes the curl command. It captures the response as raw bytes (so
// binary downloads survive intact), then either passes it through or condenses
// it for a human reader.
func Run(args []string, verbose int) (int, error) {
	timer := core.StartTimer()

	cmd := core.ResolvedCommand("curl", "-s") // -s: silent (no progress bar)
	cmd.Args = append(cmd.Args, args...)

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: curl -s %s\n", strings.Join(args, " "))
	}

	// Capture stdout as raw bytes (not a normalized string) so binary downloads
	// survive intact. A lossy UTF-8 conversion would otherwise replace every
	// non-UTF-8 byte with U+FFFD (3 bytes), corrupting e.g. gzip magic
	// 1f 8b 08 00 into 1f ef bf bd 08 00 (rtk #1087).
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	exitCode := core.ExitCodeFromError(runErr)
	if exitCode == 127 {
		return 127, fmt.Errorf("gortk: failed to run curl: %w", runErr)
	}

	argsDisplay := strings.Join(args, " ")

	// Skip filtering on failure: curl can return HTML error bodies that would
	// be misleading to summarize, and we want the real exit code surfaced.
	if exitCode != 0 {
		stderrStr := strings.TrimSpace(errBuf.String())
		stdoutStr := strings.TrimSpace(outBuf.String())
		msg := stderrStr
		if msg == "" {
			msg = stdoutStr
		}
		fmt.Fprintf(os.Stderr, "FAILED: curl %s\n", msg)
		return exitCode, nil
	}

	stdout := outBuf.Bytes()

	// Binary detection: if the body is not valid UTF-8, a lossy conversion would
	// corrupt the stream (gzip, zip, png, pdf, elf, ... — any binary format).
	// Write raw bytes through and skip filtering. Tracking is recorded as
	// passthrough (0% savings) since token counts over binary content have no
	// meaning.
	if isBinary(stdout) {
		if _, err := os.Stdout.Write(stdout); err != nil {
			return exitCode, fmt.Errorf("gortk: failed to write binary response to stdout: %w", err)
		}
		timer.TrackPassthrough("curl "+argsDisplay, "gortk curl "+argsDisplay)
		return exitCode, nil
	}

	raw := core.NormalizeNewlines(string(stdout))
	isTTY := core.IsTerminal(os.Stdout)
	content, truncated := filterCurlOutput(raw, isTTY)

	fmt.Println(content)
	if truncated {
		if hint := writeTeeHint(raw); hint != "" {
			fmt.Println(hint)
		}
	}

	timer.Track("curl "+argsDisplay, "gortk curl "+argsDisplay, raw, content)
	return exitCode, nil
}

// isBinary reports whether b is not valid UTF-8 — exactly the condition under
// which a lossy conversion would replace invalid bytes with U+FFFD and corrupt
// downstream consumers (rtk #1087). Empty input and text containing NUL are
// valid UTF-8 and therefore not binary.
func isBinary(b []byte) bool {
	return !utf8.Valid(b)
}

// filterCurlOutput condenses a curl response for display. It returns the
// content to print and whether the body was truncated (in which case the
// caller should emit a tee-recovery hint).
//
// The body is passed through unchanged when:
//   - it looks like a top-level JSON document (mid-stream truncation produces
//     invalid JSON, rtk #1536),
//   - stdout is not a terminal (pipes / redirects need the full body, #1282),
//   - it fits under the truncation threshold.
func filterCurlOutput(raw string, isTTY bool) (string, bool) {
	trimmed := strings.TrimSpace(raw)

	// Heuristic: looks like a top-level JSON document. Numbers / booleans / null
	// are always under maxResponseSize so they don't need detection here.
	looksLikeJSON := (strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}")) ||
		(strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")) ||
		(strings.HasPrefix(trimmed, "\"") && strings.HasSuffix(trimmed, "\"") && len(trimmed) >= 2)

	if !isTTY || looksLikeJSON || len(trimmed) < maxResponseSize {
		return trimmed, false
	}

	// We're about to truncate for a human reader. Don't cut in the middle of a
	// UTF-8 character — len counts bytes.
	end := maxResponseSize
	for !isCharBoundary(trimmed, end) {
		end--
	}
	return fmt.Sprintf("%s... (%d bytes total)", trimmed[:end], len(trimmed)), true
}

// isCharBoundary reports whether index i in s falls on a UTF-8 rune boundary,
// mirroring Rust's str::is_char_boundary. i==0 and i==len(s) are boundaries; an
// index pointing at a UTF-8 continuation byte (0b10xxxxxx) is not.
func isCharBoundary(s string, i int) bool {
	if i == 0 || i == len(s) {
		return true
	}
	if i < 0 || i > len(s) {
		return false
	}
	return s[i]&0xC0 != 0x80
}

// writeTeeHint writes the raw body to a recovery file under gortk's data dir and
// returns a hint line pointing at it, or "" if the write fails. Best-effort;
// failure simply means no recovery hint is emitted.
func writeTeeHint(raw string) string {
	path := fmt.Sprintf("%s/tee-curl.txt", core.DataDir())
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		return ""
	}
	return fmt.Sprintf("[gortk: full output saved to %s — re-read if the filtered view is insufficient]", path)
}
