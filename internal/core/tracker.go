package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// gortk records token-savings stats in a small JSON file rather than the
// bundled SQLite database rtk uses. This keeps the binary pure-Go and
// cgo-free (so it builds with only the Go toolchain on Windows) while
// preserving the `gain`-style reporting feature. All tracking is best-effort:
// failures are silently ignored and never affect command exit codes.

// TrackEntry is one recorded command execution.
type TrackEntry struct {
	Timestamp   time.Time `json:"ts"`
	Command     string    `json:"cmd"`
	Label       string    `json:"label"`
	RawTokens   int       `json:"raw_tokens"`
	OutTokens   int       `json:"out_tokens"`
	Passthrough bool      `json:"passthrough"`
}

var trackMu sync.Mutex

// DataDir returns the per-user gortk data directory, creating it if needed.
func DataDir() string {
	base, err := os.UserConfigDir()
	if err != nil || base == "" {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "gortk")
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

func trackingPath() string {
	return filepath.Join(DataDir(), "tracking.jsonl")
}

// TimedExecution measures wall-clock time around a wrapped command so usage can
// be recorded once it completes.
type TimedExecution struct {
	start time.Time
}

// StartTimer begins timing a command execution.
func StartTimer() TimedExecution {
	return TimedExecution{start: time.Now()}
}

// Track records a filtered command execution and the tokens it saved.
func (t TimedExecution) Track(command, label, raw, filtered string) {
	appendEntry(TrackEntry{
		Timestamp: time.Now(),
		Command:   command,
		Label:     label,
		RawTokens: EstimateTokens(raw),
		OutTokens: EstimateTokens(filtered),
	})
}

// TrackPassthrough records a command that was executed without filtering.
func (t TimedExecution) TrackPassthrough(command, label string) {
	appendEntry(TrackEntry{
		Timestamp:   time.Now(),
		Command:     command,
		Label:       label,
		Passthrough: true,
	})
}

func appendEntry(e TrackEntry) {
	if os.Getenv("GORTK_NO_TRACKING") == "1" {
		return
	}
	trackMu.Lock()
	defer trackMu.Unlock()

	f, err := os.OpenFile(trackingPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()

	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
}
