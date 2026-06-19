package wget

import (
	"fmt"
	"os"
	"strings"
)

// extractFilenameFromOutput determines the saved filename. It first honours an
// explicit -O/--output-document arg, then parses wget's "Saving to" /
// "Sauvegarde en" line, then falls back to the basename of the URL.
func extractFilenameFromOutput(stderr, url string, args []string) string {
	// Check for -O argument first.
	for i, arg := range args {
		if arg == "-O" || arg == "--output-document" {
			if i+1 < len(args) {
				return args[i+1]
			}
		}
		if strings.HasPrefix(arg, "-O") && len(arg) > 2 {
			return arg[2:]
		}
	}

	// Parse wget output for "Sauvegarde en" (French) or "Saving to" (English).
	for _, line := range splitLines(stderr) {
		if strings.Contains(line, "Sauvegarde en") || strings.Contains(line, "Saving to") {
			chars := []rune(line)
			startIdx := -1
			endIdx := -1
			for i, c := range chars {
				if c == '«' || (c == '\'' && startIdx == -1) {
					startIdx = i
				}
				if c == '»' || (c == '\'' && startIdx != -1) {
					endIdx = i
				}
			}
			if startIdx >= 0 && endIdx >= 0 && endIdx > startIdx+1 {
				filename := string(chars[startIdx+1 : endIdx])
				return strings.TrimSpace(filename)
			}
		}
	}

	// Fallback: extract from URL.
	path := url
	if idx := strings.LastIndex(url, "://"); idx >= 0 {
		path = url[idx+3:]
	}
	filename := path
	if idx := strings.LastIndex(filename, "/"); idx >= 0 {
		filename = filename[idx+1:]
	}
	if idx := strings.Index(filename, "?"); idx >= 0 {
		filename = filename[:idx]
	}
	if filename == "" || !strings.Contains(filename, ".") {
		return "index.html"
	}
	return filename
}

func getFileSize(filename string) uint64 {
	if fi, err := os.Stat(filename); err == nil {
		size := fi.Size()
		if size < 0 {
			return 0
		}
		return uint64(size)
	}
	return 0
}

// formatSize renders a byte count compactly. Zero renders as "?" (unknown).
func formatSize(bytes uint64) string {
	switch {
	case bytes == 0:
		return "?"
	case bytes < 1024:
		return fmt.Sprintf("%dB", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024.0)
	case bytes < 1024*1024*1024:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1024.0*1024.0))
	default:
		return fmt.Sprintf("%.1fGB", float64(bytes)/(1024.0*1024.0*1024.0))
	}
}

// compactURL strips the protocol prefix and truncates very long URLs to keep
// the result line short.
func compactURL(url string) string {
	withoutProto := url
	if strings.HasPrefix(url, "https://") {
		withoutProto = url[len("https://"):]
	} else if strings.HasPrefix(url, "http://") {
		withoutProto = url[len("http://"):]
	}

	chars := []rune(withoutProto)
	if len(chars) <= 50 {
		return withoutProto
	}
	prefix := string(chars[:25])
	suffix := string(chars[len(chars)-20:])
	return fmt.Sprintf("%s...%s", prefix, suffix)
}

// parseError maps a failed wget's stderr/stdout to a short human error string.
func parseError(stderr, stdout string) string {
	combined := stderr + "\n" + stdout

	switch {
	case strings.Contains(combined, "404"):
		return "404 Not Found"
	case strings.Contains(combined, "403"):
		return "403 Forbidden"
	case strings.Contains(combined, "401"):
		return "401 Unauthorized"
	case strings.Contains(combined, "500"):
		return "500 Server Error"
	case strings.Contains(combined, "Connection refused"):
		return "Connection refused"
	case strings.Contains(combined, "unable to resolve") || strings.Contains(combined, "Name or service not known"):
		return "DNS lookup failed"
	case strings.Contains(combined, "timed out"):
		return "Connection timed out"
	case strings.Contains(combined, "SSL") || strings.Contains(combined, "certificate"):
		return "SSL/TLS error"
	}

	// Return first meaningful line.
	for _, line := range splitLines(stderr) {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !strings.HasPrefix(trimmed, "--") {
			r := []rune(trimmed)
			if len(r) > 60 {
				return string(r[:60]) + "..."
			}
			return trimmed
		}
	}

	return "Unknown error"
}

// truncateLine shortens a line to at most max characters, appending "..." when
// truncated. Matches Rust's char-based truncation with max-3 head.
func truncateLine(line string, max int) string {
	if len(line) <= max {
		return line
	}
	r := []rune(line)
	head := max - 3
	if head < 0 {
		head = 0
	}
	if head > len(r) {
		head = len(r)
	}
	return string(r[:head]) + "..."
}

// compactStdout builds the compact head view of body content piped via -O -.
func compactStdout(url, stdout string) string {
	lines := splitLines(stdout)
	total := len(lines)

	var b strings.Builder
	if total > 20 {
		fmt.Fprintf(&b, "%s ok | %d lines | %s\n", compactURL(url), total, formatSize(uint64(len(stdout))))
		b.WriteString("first 10 lines:\n")
		for _, line := range lines[:10] {
			b.WriteString(truncateLine(line, 100))
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "... +%d more lines", total-10)
	} else {
		fmt.Fprintf(&b, "%s ok | %d lines\n", compactURL(url), total)
		for _, line := range lines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// splitLines mirrors Rust's str::lines(): split on \n and drop a single
// trailing empty element produced by a trailing newline.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}
