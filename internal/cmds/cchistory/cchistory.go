// Package cchistory is the shared Claude Code session-history reader used by
// gortk's analytics commands (discover, learn, session, cc-economics). It is the
// pragmatic Go equivalent of rtk's src/discover/provider.rs (ClaudeProvider):
// it locates Claude Code JSONL transcripts on disk and streams the Bash commands,
// their tool_result output, and per-message token usage out of them.
//
// It registers no command — it is a library shared by the four analytics command
// packages, mirroring how rtk's discover::provider is reused by learn,
// session_cmd, and cc_economics.
//
// Schema assumptions (defensive — unknown fields tolerated, malformed lines
// skipped). Claude Code writes one JSON object per line under
// <UserHomeDir>/.claude/projects/<encoded-project>/**/*.jsonl:
//
//   - assistant turn:
//     {"type":"assistant","timestamp":"...","message":{"model":"claude-...",
//     "usage":{"input_tokens":N,"output_tokens":N,
//     "cache_creation_input_tokens":N,"cache_read_input_tokens":N},
//     "content":[{"type":"tool_use","id":"toolu_x","name":"Bash",
//     "input":{"command":"git status"}}, ...]}}
//   - user turn carrying a tool result:
//     {"type":"user","message":{"content":[{"type":"tool_result",
//     "tool_use_id":"toolu_x","content":"<output>","is_error":false}]}}
//
// Older transcripts may omit cache_* fields or carry no usage block at all; both
// are handled (missing → 0 / no usage). Only "Bash" tool_use blocks yield
// commands; Read/Grep/Edit and friends are ignored, matching rtk.
package cchistory

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ExtractedCommand is one Bash command pulled from a session, matched with the
// tool_result it produced. Mirrors rtk's discover::provider::ExtractedCommand.
type ExtractedCommand struct {
	// Command is the raw shell command string (may be a chain like "a && b").
	Command string
	// OutputLen is the byte length of the matched tool_result content, or -1 if
	// no result was found for this tool_use (mirrors rtk's Option<usize>).
	OutputLen int
	// SessionID is the transcript filename stem.
	SessionID string
	// OutputContent is the first ~1000 chars of the tool_result content (for
	// error detection by the learn command), or "" if none.
	OutputContent string
	// IsError is the tool_result's is_error flag.
	IsError bool
	// SequenceIndex is the chronological order of this tool_use within the session.
	SequenceIndex int
}

// Usage is the per-message token accounting from an assistant turn's usage block.
// Mirrors the fields cc-economics needs; rtk read these from ccusage instead.
type Usage struct {
	InputTokens       int
	OutputTokens      int
	CacheCreateTokens int
	CacheReadTokens   int
	// Model is the assistant model id (e.g. "claude-opus-4-7"); "" if absent.
	Model string
	// Timestamp is the assistant turn's timestamp; zero if unparseable/absent.
	Timestamp time.Time
}

// ---------------------------------------------------------------------------
// Session discovery
// ---------------------------------------------------------------------------

// ProjectsDir returns <UserHomeDir>/.claude/projects, the root under which
// Claude Code stores per-project session transcripts. The bool is false when the
// home directory cannot be determined.
func ProjectsDir() (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	return filepath.Join(home, ".claude", "projects"), true
}

// EncodeProjectPath encodes a filesystem path into Claude Code's project
// directory slug, replacing '/', '.', '_', '\\', ' ', '[', ']' and any
// non-ASCII byte with '-'. Faithful port of rtk's encode_project_path so the
// default "current project only" filter finds the right sessions.
//
//	C:\Users\foo\bar       -> C:-Users-foo-bar
//	/Users/first.last/proj -> -Users-first-last-proj
func EncodeProjectPath(path string) string {
	var b strings.Builder
	b.Grow(len(path))
	for _, r := range path {
		switch {
		case r > 127:
			b.WriteByte('-')
		case r == '/' || r == '.' || r == '_' || r == '\\' || r == ' ' || r == '[' || r == ']':
			b.WriteByte('-')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// DiscoverSessions returns the paths of every *.jsonl transcript under
// projectsDir, optionally restricted to project directories whose name contains
// projectFilter (substring match, like rtk) and to files modified within the
// last sinceDays (0 = no time filter). A missing projectsDir yields an empty
// slice, not an error. Walks recursively to catch subagents/ transcripts.
func DiscoverSessions(projectsDir, projectFilter string, sinceDays int) []string {
	info, err := os.Stat(projectsDir)
	if err != nil || !info.IsDir() {
		return nil
	}

	var cutoff time.Time
	if sinceDays > 0 {
		cutoff = time.Now().AddDate(0, 0, -sinceDays)
	}

	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}

	var sessions []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if projectFilter != "" && !strings.Contains(e.Name(), projectFilter) {
			continue
		}
		projPath := filepath.Join(projectsDir, e.Name())
		_ = filepath.WalkDir(projPath, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() {
				return nil //nolint:nilerr // skip unreadable entries, keep walking
			}
			if !strings.EqualFold(filepath.Ext(path), ".jsonl") {
				return nil
			}
			if !cutoff.IsZero() {
				if fi, statErr := os.Stat(path); statErr == nil && fi.ModTime().Before(cutoff) {
					return nil
				}
			}
			sessions = append(sessions, path)
			return nil
		})
	}
	return sessions
}

// ---------------------------------------------------------------------------
// Transcript parsing
// ---------------------------------------------------------------------------

// rawEntry is the minimal shape we decode from each JSONL line. Unknown fields
// are ignored by encoding/json, satisfying the "tolerate unknown fields" rule.
type rawEntry struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Message   rawMsg `json:"message"`
}

type rawMsg struct {
	Model   string          `json:"model"`
	Usage   *rawUsage       `json:"usage"`
	Content json.RawMessage `json:"content"`
}

type rawUsage struct {
	InputTokens       int `json:"input_tokens"`
	OutputTokens      int `json:"output_tokens"`
	CacheCreateTokens int `json:"cache_creation_input_tokens"`
	CacheReadTokens   int `json:"cache_read_input_tokens"`
}

type rawBlock struct {
	Type      string          `json:"type"`
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	Input     rawInput        `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type rawInput struct {
	Command string `json:"command"`
}

// ExtractCommands parses a single transcript file and returns its Bash commands
// in chronological order, each matched (where possible) to its tool_result.
// Malformed lines are skipped. A line is only JSON-decoded if it could plausibly
// contain a Bash tool_use or a tool_result, mirroring rtk's pre-filter.
func ExtractCommands(path string) ([]ExtractedCommand, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sessionID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))

	type pendingUse struct {
		id      string
		command string
		seq     int
	}
	type result struct {
		outputLen int
		content   string
		isError   bool
	}

	var pending []pendingUse
	results := map[string]result{}
	seq := 0

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		// Pre-filter: skip lines that can't carry a Bash tool_use or a result.
		if !strings.Contains(line, `"Bash"`) && !strings.Contains(line, `"tool_result"`) {
			continue
		}
		var e rawEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		blocks := decodeBlocks(e.Message.Content)
		switch e.Type {
		case "assistant":
			for _, b := range blocks {
				if b.Type == "tool_use" && b.Name == "Bash" && b.ID != "" && b.Input.Command != "" {
					pending = append(pending, pendingUse{id: b.ID, command: b.Input.Command, seq: seq})
					seq++
				}
			}
		case "user":
			for _, b := range blocks {
				if b.Type == "tool_result" && b.ToolUseID != "" {
					content := decodeResultContent(b.Content)
					results[b.ToolUseID] = result{
						outputLen: len(content),
						content:   takeChars(content, 1000),
						isError:   b.IsError,
					}
				}
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	out := make([]ExtractedCommand, 0, len(pending))
	for _, p := range pending {
		ec := ExtractedCommand{
			Command:       p.command,
			OutputLen:     -1,
			SessionID:     sessionID,
			SequenceIndex: p.seq,
		}
		if r, ok := results[p.id]; ok {
			ec.OutputLen = r.outputLen
			ec.OutputContent = r.content
			ec.IsError = r.isError
		}
		out = append(out, ec)
	}
	return out, nil
}

// ExtractUsage parses a transcript and returns one Usage per assistant turn that
// carried a usage block. Used by cc-economics to derive spend from history
// rather than spawning an external tool. Malformed lines are skipped.
func ExtractUsage(path string) ([]Usage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var usages []Usage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.Contains(line, `"usage"`) {
			continue
		}
		var e rawEntry
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if e.Type != "assistant" || e.Message.Usage == nil {
			continue
		}
		u := Usage{
			InputTokens:       e.Message.Usage.InputTokens,
			OutputTokens:      e.Message.Usage.OutputTokens,
			CacheCreateTokens: e.Message.Usage.CacheCreateTokens,
			CacheReadTokens:   e.Message.Usage.CacheReadTokens,
			Model:             e.Message.Model,
		}
		if ts, perr := time.Parse(time.RFC3339, e.Timestamp); perr == nil {
			u.Timestamp = ts
		}
		usages = append(usages, u)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return usages, nil
}

// decodeBlocks decodes message.content into blocks. Claude content is usually an
// array of blocks; for user turns it can also be a bare string (no tool_result),
// which we treat as zero blocks.
func decodeBlocks(raw json.RawMessage) []rawBlock {
	if len(raw) == 0 {
		return nil
	}
	var blocks []rawBlock
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	return blocks
}

// decodeResultContent normalizes a tool_result's content, which may be a plain
// string or an array of {"type":"text","text":"..."} blocks.
func decodeResultContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var b strings.Builder
		for _, p := range parts {
			b.WriteString(p.Text)
		}
		return b.String()
	}
	return ""
}

// takeChars returns the first n runes of s (rune-safe, like rtk's chars().take()).
func takeChars(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
