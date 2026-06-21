package core

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// withDataDir points DataDir() at a temp dir for the duration of a test by
// overriding the user-config-dir env var that os.UserConfigDir consults. On
// Windows that is APPDATA; we set the common candidates so the test is hermetic
// regardless of platform. It also resets the memoized device hash so each test
// gets a fresh salt under its own temp dir.
func withDataDir(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	// os.UserConfigDir consults these per-platform; set all so DataDir() lands
	// inside our temp dir on any OS the tests run on.
	for _, k := range []string{"APPDATA", "XDG_CONFIG_HOME", "HOME"} {
		t.Setenv(k, base)
	}
	// Reset the memoized device hash so it is recomputed against this temp dir.
	deviceHashOnce.Once = sync.Once{}
	deviceHashOnce.hash = ""
	// DataDir() appends "gortk" under the config base.
	dir := filepath.Join(base, "gortk")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir data dir: %v", err)
	}
	return dir
}

func TestDeviceHashStableAnd64Hex(t *testing.T) {
	withDataDir(t)
	h1 := DeviceHash()
	h2 := DeviceHash()
	if h1 != h2 {
		t.Errorf("device hash not stable: %q != %q", h1, h2)
	}
	if len(h1) != 64 {
		t.Errorf("device hash length = %d, want 64", len(h1))
	}
	if !isHex64(h1) {
		t.Errorf("device hash is not 64 lowercase hex chars: %q", h1)
	}
}

func TestRandomSaltUnique64Hex(t *testing.T) {
	s1 := randomSalt()
	s2 := randomSalt()
	if s1 == s2 {
		t.Errorf("two random salts collided: %q", s1)
	}
	for _, s := range []string{s1, s2} {
		if !isHex64(s) {
			t.Errorf("salt is not 64 hex chars: %q", s)
		}
	}
}

func TestBuildPayloadAggregateOnly_NoCommandsOrPaths(t *testing.T) {
	dir := withDataDir(t)

	// Construct a tracking.jsonl with sensitive-looking command strings and
	// file paths to prove they do NOT leak into the payload.
	entries := []TrackEntry{
		{Timestamp: time.Now(), Command: "git", Label: "log /home/secret/repo", RawTokens: 1000, OutTokens: 200},
		{Timestamp: time.Now(), Command: "grep", Label: "password C:\\Users\\admin\\secrets.txt", RawTokens: 500, OutTokens: 100},
		{Timestamp: time.Now(), Command: "curl", Label: "https://internal.example/api", Passthrough: true},
	}
	writeTracking(t, dir, entries)

	body, err := BuildPayload()
	if err != nil {
		t.Fatalf("BuildPayload: %v", err)
	}

	// Decode and assert the aggregate numbers are correct.
	var p Payload
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if p.Commands != 3 {
		t.Errorf("Commands = %d, want 3", p.Commands)
	}
	if p.RawTokens != 1500 {
		t.Errorf("RawTokens = %d, want 1500", p.RawTokens)
	}
	if p.OutTokens != 300 {
		t.Errorf("OutTokens = %d, want 300", p.OutTokens)
	}
	if p.TokensSaved != 1200 {
		t.Errorf("TokensSaved = %d, want 1200", p.TokensSaved)
	}
	if !isHex64(p.DeviceHash) {
		t.Errorf("payload device hash not 64 hex: %q", p.DeviceHash)
	}

	// The raw JSON must contain NONE of the command strings, labels, or paths.
	raw := string(body)
	for _, leaked := range []string{
		"git", "grep", "curl", // command names
		"secret", "password", "secrets.txt", "/home/", "C:\\Users", // paths
		"internal.example", "log", "https://", // labels / urls
	} {
		if strings.Contains(strings.ToLower(raw), strings.ToLower(leaked)) {
			t.Errorf("payload leaked disallowed content %q:\n%s", leaked, raw)
		}
	}

	// Whitelist the field set: only the allowed aggregate keys may appear.
	var generic map[string]any
	if err := json.Unmarshal(body, &generic); err != nil {
		t.Fatalf("unmarshal generic: %v", err)
	}
	allowed := map[string]bool{
		"device_hash": true, "version": true, "os": true, "arch": true,
		"commands": true, "raw_tokens": true, "out_tokens": true, "tokens_saved": true,
	}
	for k := range generic {
		if !allowed[k] {
			t.Errorf("payload contains disallowed field %q", k)
		}
	}
}

func TestMaybePingNoOpWhenDisabled(t *testing.T) {
	withDataDir(t)

	cases := []struct {
		name string
		cfg  Config
		env  string
	}{
		{
			name: "telemetry disabled (zero value)",
			cfg:  Config{},
		},
		{
			name: "enabled but no endpoint",
			cfg:  Config{Telemetry: Telemetry{Enabled: true}},
		},
		{
			name: "enabled + endpoint but env override set",
			cfg:  Config{Telemetry: Telemetry{Enabled: true, Endpoint: "PLACEHOLDER"}},
			env:  "1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
			}))
			defer srv.Close()

			cfg := tc.cfg
			if cfg.Telemetry.Endpoint == "PLACEHOLDER" {
				cfg.Telemetry.Endpoint = srv.URL
			}
			if tc.env != "" {
				t.Setenv("GORTK_TELEMETRY_DISABLED", tc.env)
			} else {
				os.Unsetenv("GORTK_TELEMETRY_DISABLED")
			}

			MaybePing(cfg)
			// Give any (erroneous) goroutine a moment to fire.
			time.Sleep(50 * time.Millisecond)

			if hits != 0 {
				t.Errorf("expected NO post when disabled, got %d", hits)
			}
			// A disabled ping must not write the throttle marker either.
			if _, err := os.Stat(markerPath()); err == nil {
				t.Errorf("disabled MaybePing wrote a marker file")
			}
		})
	}
}

func TestMaybePingPostsWhenEnabled(t *testing.T) {
	withDataDir(t)
	os.Unsetenv("GORTK_TELEMETRY_DISABLED")

	type got struct {
		auth string
		ct   string
		body []byte
	}
	ch := make(chan got, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ch <- got{auth: r.Header.Get("Authorization"), ct: r.Header.Get("Content-Type"), body: b}
	}))
	defer srv.Close()

	cfg := Config{Telemetry: Telemetry{Enabled: true, Endpoint: srv.URL, Token: "s3cr3t"}}
	MaybePing(cfg)

	select {
	case g := <-ch:
		if g.auth != "Bearer s3cr3t" {
			t.Errorf("Authorization = %q, want %q", g.auth, "Bearer s3cr3t")
		}
		if g.ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", g.ct)
		}
		var p Payload
		if err := json.Unmarshal(g.body, &p); err != nil {
			t.Fatalf("posted body is not valid Payload JSON: %v\n%s", err, g.body)
		}
		if !isHex64(p.DeviceHash) {
			t.Errorf("posted device hash invalid: %q", p.DeviceHash)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected a POST to the sink, none arrived")
	}

	// The marker should now exist (throttle armed).
	if _, err := os.Stat(markerPath()); err != nil {
		t.Errorf("enabled MaybePing did not write a marker: %v", err)
	}
}

func TestMaybePingThrottledBySecondCall(t *testing.T) {
	withDataDir(t)
	os.Unsetenv("GORTK_TELEMETRY_DISABLED")

	var hits int32
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
	}))
	defer srv.Close()

	cfg := Config{Telemetry: Telemetry{Enabled: true, Endpoint: srv.URL}}

	MaybePing(cfg)
	time.Sleep(100 * time.Millisecond) // let first ping land + marker settle
	MaybePing(cfg)                     // should be throttled by the marker
	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if hits != 1 {
		t.Errorf("expected exactly 1 post (second throttled), got %d", hits)
	}
}

// writeTracking writes a tracking.jsonl fixture into dir.
func writeTracking(t *testing.T, dir string, entries []TrackEntry) {
	t.Helper()
	var b strings.Builder
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal entry: %v", err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "tracking.jsonl"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write tracking: %v", err)
	}
}
