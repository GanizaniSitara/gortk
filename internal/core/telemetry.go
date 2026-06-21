package core

// Optional, opt-in usage telemetry. This is the ONE deliberate exception to
// gortk's no-network rule (see docs/PORTING_CONTRACT.md). It is OFF by default
// and sends NOTHING unless an operator explicitly enables it AND configures a
// destination endpoint (their own sink). There is no compile-time URL and no
// vendor phone-home — the endpoint always comes from the user's config.toml.
//
// The payload is AGGREGATE-ONLY: an anonymized random device hash, the gortk
// version, GOOS/GOARCH, and rolled-up token-savings counts from
// tracking.jsonl. It carries NO command strings, NO file paths, and NO secrets.
//
// Structure mirrors rtk's src/core/telemetry.rs (device hash via sha256 of a
// persisted random salt, a ~23h interval marker, fire-and-forget background
// send) but with a configurable sink instead of an option_env! compile-time URL.

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Version is the gortk version reported in the telemetry payload. It defaults
// to "dev" and may be overridden at build time via -ldflags
// "-X gortk/internal/core.Version=<v>".
var Version = "dev"

// pingInterval throttles pings to roughly once per day, matching rtk's 23h
// marker so a long-running session never double-pings.
const pingInterval = 23 * time.Hour

var deviceHashOnce struct {
	sync.Once
	hash string
}

// Payload is the aggregate-only telemetry body. Every field is a rolled-up
// statistic or an anonymized constant — deliberately NO command strings, NO
// paths, NO secrets. This is the exact shape an enterprise sink receives and
// what `gortk telemetry preview` prints.
type Payload struct {
	DeviceHash  string `json:"device_hash"`  // sha256 of a random persisted salt
	Version     string `json:"version"`      // gortk version (default "dev")
	OS          string `json:"os"`           // runtime.GOOS
	Arch        string `json:"arch"`         // runtime.GOARCH
	Commands    int    `json:"commands"`     // total tracked command count
	RawTokens   int    `json:"raw_tokens"`   // total raw (pre-filter) tokens
	OutTokens   int    `json:"out_tokens"`   // total emitted (post-filter) tokens
	TokensSaved int    `json:"tokens_saved"` // raw - out
}

// salt management ----------------------------------------------------------

func saltFilePath() string {
	return filepath.Join(DataDir(), ".device_salt")
}

// getOrCreateSalt returns the persisted 64-hex-char device salt, creating and
// writing a fresh random one on first use. The salt is the ONLY input to the
// device hash — it is never derived from hostname, user, or hardware, so the
// hash cannot be reversed to an identity.
func getOrCreateSalt() string {
	path := saltFilePath()

	if data, err := os.ReadFile(path); err == nil {
		trimmed := strings.TrimSpace(string(data))
		if isHex64(trimmed) {
			return trimmed
		}
	}

	salt := randomSalt()
	if dir := filepath.Dir(path); dir != "" {
		_ = os.MkdirAll(dir, 0o755)
	}
	// Best-effort write; if it fails we still return a usable (non-persisted)
	// salt so a single ping works and the next run retries persistence.
	_ = os.WriteFile(path, []byte(salt), 0o600)
	return salt
}

// randomSalt returns a fresh 32-byte random salt as 64 lowercase hex chars.
// It falls back to hashing a time/pid seed only if crypto/rand is unavailable,
// so the result is always a valid 64-char hex string.
func randomSalt() string {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		seed := time.Now().UTC().String() + ":" + string(rune(os.Getpid()))
		sum := sha256.Sum256([]byte(seed))
		return hex.EncodeToString(sum[:])
	}
	return hex.EncodeToString(buf[:])
}

func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// DeviceHash returns the stable, anonymized device identifier: the sha256 of
// the persisted random salt, as 64 lowercase hex chars. It is memoized for the
// process lifetime so repeated calls are cheap and identical.
func DeviceHash() string {
	deviceHashOnce.Do(func() {
		sum := sha256.Sum256([]byte(getOrCreateSalt()))
		deviceHashOnce.hash = hex.EncodeToString(sum[:])
	})
	return deviceHashOnce.hash
}

// payload construction -----------------------------------------------------

// aggregateTracking rolls up tracking.jsonl into (commands, raw, out) totals.
// It reuses the same per-entry math as the `gain` command: passthrough entries
// count toward the command total but carry no token figures. Missing or
// malformed data yields zeros — never an error — so it never breaks a ping.
func aggregateTracking() (commands, raw, out int) {
	f, err := os.Open(filepath.Join(DataDir(), "tracking.jsonl"))
	if err != nil {
		return 0, 0, 0
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	for {
		var e TrackEntry
		if err := dec.Decode(&e); err != nil {
			break // EOF or a malformed tail — stop, keep what we have
		}
		commands++
		if e.Passthrough {
			continue
		}
		raw += e.RawTokens
		out += e.OutTokens
	}
	return commands, raw, out
}

// buildPayload assembles the aggregate-only Payload from the current device
// hash, version, runtime, and rolled-up tracking stats.
func buildPayload() Payload {
	commands, raw, out := aggregateTracking()
	return Payload{
		DeviceHash:  DeviceHash(),
		Version:     Version,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		Commands:    commands,
		RawTokens:   raw,
		OutTokens:   out,
		TokensSaved: raw - out,
	}
}

// BuildPayload returns the exact JSON body that WOULD be sent, indented for
// readability. It is pure (no network, no marker writes) so it is testable and
// so `gortk telemetry preview` can show an operator precisely what they'd
// collect before ever enabling a send.
func BuildPayload() ([]byte, error) {
	return json.MarshalIndent(buildPayload(), "", "  ")
}

// marker / throttle --------------------------------------------------------

func markerPath() string {
	return filepath.Join(DataDir(), ".telemetry_last_ping")
}

// recentlyPinged reports whether the marker file was touched within
// pingInterval. A missing/unreadable marker means "not recent" (ok to ping).
func recentlyPinged() bool {
	fi, err := os.Stat(markerPath())
	if err != nil {
		return false
	}
	return time.Since(fi.ModTime()) < pingInterval
}

func touchMarker() {
	_ = os.WriteFile(markerPath(), []byte{}, 0o644)
}

// send ---------------------------------------------------------------------

// telemetryClient is overridable in tests; production uses a short-timeout
// client so a slow/dead sink never stalls the CLI.
var telemetryClient = &http.Client{Timeout: 2 * time.Second}

// postPayload sends body to endpoint as a JSON POST, attaching a Bearer token
// when one is configured. Errors are returned (the caller ignores them) so the
// behaviour is fully testable.
func postPayload(endpoint, token string, body []byte) error {
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := telemetryClient.Do(req)
	if err != nil {
		return err
	}
	// Drain and close so the connection can be reused / released.
	resp.Body.Close()
	return nil
}

// MaybePing fires an aggregate telemetry ping if and only if telemetry is
// fully opted in: config.Telemetry.Enabled is true, a non-empty Endpoint is
// configured, and the GORTK_TELEMETRY_DISABLED env override is not "1". It
// throttles to ~once per 23h via a marker file and sends fire-and-forget in a
// background goroutine over HTTPS with a 2s timeout. It NEVER blocks the CLI
// and NEVER returns or surfaces an error — all failures are silently ignored.
//
// When disabled for any reason, this is a pure no-op: nothing is read, no
// marker is written, and absolutely nothing is sent.
func MaybePing(cfg Config) {
	if !shouldPing(cfg) {
		return
	}

	// Throttle: skip if we pinged within the interval.
	if recentlyPinged() {
		return
	}
	// Touch the marker BEFORE sending so a crash or concurrent invocation can't
	// double-ping (mirrors rtk).
	touchMarker()

	endpoint := cfg.Telemetry.Endpoint
	token := cfg.Telemetry.Token
	body, err := BuildPayload()
	if err != nil {
		return
	}

	// Fire-and-forget: never block the CLI, never propagate errors.
	go func() {
		_ = postPayload(endpoint, token, body)
	}()
}

// shouldPing centralizes the opt-in gate: enabled + endpoint set + not env
// disabled. Extracted so it is unit-testable without touching the network.
func shouldPing(cfg Config) bool {
	if os.Getenv("GORTK_TELEMETRY_DISABLED") == "1" {
		return false
	}
	if !cfg.Telemetry.Enabled {
		return false
	}
	if cfg.Telemetry.Endpoint == "" {
		return false
	}
	return true
}
