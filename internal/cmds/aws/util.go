package aws

import (
	"fmt"
	"sort"
	"strings"
)

// shortenARN extracts the short name from an AWS ARN.
// Example: "arn:aws:ecs:region:acct:service/cluster/name" -> "name".
// For simple ARNs like "arn:aws:iam::123:user/alice", returns "alice".
// Mirrors rtk's utils::shorten_arn.
func shortenARN(arn string) string {
	// ARNs use "/" or ":" as separators. Try "/" first (service/cluster/name
	// pattern), then fall back to ":" for Lambda/IAM ARNs.
	if i := strings.LastIndex(arn, "/"); i >= 0 {
		return arn[i+1:]
	}
	if i := strings.LastIndex(arn, ":"); i >= 0 {
		return arn[i+1:]
	}
	return arn
}

// truncateISODate truncates an ISO 8601 datetime string to just the date
// portion (first 10 chars). Mirrors rtk's utils::truncate_iso_date.
func truncateISODate(date string) string {
	if len(date) >= 10 {
		return date[:10]
	}
	return date
}

// humanBytes converts a byte count to a human-readable string (KB, MB, GB, TB).
// Mirrors rtk's utils::human_bytes (note the space, e.g. "5.0 MB").
func humanBytes(bytes uint64) string {
	const (
		kb = 1024
		mb = kb * 1024
		gb = mb * 1024
		tb = gb * 1024
	)
	switch {
	case bytes >= tb:
		return fmt.Sprintf("%.1f TB", float64(bytes)/tb)
	case bytes >= gb:
		return fmt.Sprintf("%.1f GB", float64(bytes)/gb)
	case bytes >= mb:
		return fmt.Sprintf("%.1f MB", float64(bytes)/mb)
	case bytes >= kb:
		return fmt.Sprintf("%.1f KB", float64(bytes)/kb)
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

// truncateText truncates s to at most maxLen characters (by rune), appending
// "..." when cut. Mirrors rtk's utils::truncate.
func truncateText(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	if maxLen < 3 {
		return "..."
	}
	return string(runes[:maxLen-3]) + "..."
}

// joinWithOverflow joins items with newlines and, when total exceeds max,
// appends an overflow marker. Mirrors rtk's utils::join_with_overflow.
func joinWithOverflow(items []string, total, max int, label string) string {
	out := strings.Join(items, "\n")
	if total > max {
		out += fmt.Sprintf("\n… +%d more %s", total-max, label)
	}
	return out
}

// --- generic JSON compactor (port of json_cmd::filter_json_compact) ---

// filterJSONCompact parses json_str and returns a compact, depth-limited
// representation that preserves values. ok=false when the input is not valid
// JSON. Mirrors rtk's json_cmd::filter_json_compact + compact_json.
func filterJSONCompact(jsonStr string, maxDepth int) (string, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return "", false
	}
	return compactJSON(v, 0, maxDepth), true
}

func compactJSON(value any, depth, maxDepth int) string {
	indent := strings.Repeat("  ", depth)

	if depth > maxDepth {
		return indent + "..."
	}

	switch val := value.(type) {
	case nil:
		return indent + "null"
	case bool:
		if val {
			return indent + "true"
		}
		return indent + "false"
	case jsonNumber:
		return indent + string(val)
	case string:
		if len(val) > 80 {
			end := floorCharBoundary(val, 77)
			return fmt.Sprintf("%s\"%s...\"", indent, val[:end])
		}
		return fmt.Sprintf("%s\"%s\"", indent, val)
	case []any:
		if len(val) == 0 {
			return indent + "[]"
		}
		if len(val) > 5 {
			first := compactJSON(val[0], depth+1, maxDepth)
			return fmt.Sprintf("%s[%s, ... +%d more]", indent, strings.TrimSpace(first), len(val)-1)
		}
		items := make([]string, len(val))
		allSimple := true
		for i, v := range val {
			items[i] = compactJSON(v, depth+1, maxDepth)
			if !isSimpleJSON(v) {
				allSimple = false
			}
		}
		if allSimple {
			inline := make([]string, len(items))
			for i, s := range items {
				inline[i] = strings.TrimSpace(s)
			}
			return fmt.Sprintf("%s[%s]", indent, strings.Join(inline, ", "))
		}
		lines := []string{indent + "["}
		for _, item := range items {
			lines = append(lines, item+",")
		}
		lines = append(lines, indent+"]")
		return strings.Join(lines, "\n")
	case *jsonObject:
		if val.len() == 0 {
			return indent + "{}"
		}
		lines := []string{indent + "{"}
		keys := val.keys()
		sort.Strings(keys)
		for i, key := range keys {
			child := val.get(key)
			if isSimpleJSON(child) {
				valStr := compactJSON(child, 0, maxDepth)
				lines = append(lines, fmt.Sprintf("%s  %s: %s", indent, key, strings.TrimSpace(valStr)))
			} else {
				lines = append(lines, fmt.Sprintf("%s  %s:", indent, key))
				lines = append(lines, compactJSON(child, depth+1, maxDepth))
			}
			if i >= 20 {
				lines = append(lines, fmt.Sprintf("%s  ... +%d more keys", indent, len(keys)-i-1))
				break
			}
		}
		lines = append(lines, indent+"}")
		return strings.Join(lines, "\n")
	default:
		return indent + fmt.Sprintf("%v", val)
	}
}

func isSimpleJSON(v any) bool {
	switch v.(type) {
	case nil, bool, jsonNumber, string:
		return true
	default:
		return false
	}
}

// floorCharBoundary returns the largest index <= n that lands on a UTF-8
// boundary, mirroring Rust's str::floor_char_boundary so byte slicing stays
// valid for multi-byte strings.
func floorCharBoundary(s string, n int) int {
	if n >= len(s) {
		return len(s)
	}
	for n > 0 && !utf8Start(s[n]) {
		n--
	}
	return n
}

// utf8Start reports whether b is the first byte of a UTF-8 sequence (i.e. not a
// continuation byte 0b10xxxxxx).
func utf8Start(b byte) bool {
	return b&0xC0 != 0x80
}
