package telemetry

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gortk/internal/core"
)

// withConfigDir points core.ConfigPath()/DataDir() at a temp dir by overriding
// the env vars os.UserConfigDir consults, so config writes are hermetic.
func withConfigDir(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	for _, k := range []string{"APPDATA", "XDG_CONFIG_HOME", "HOME"} {
		t.Setenv(k, base)
	}
	dir := filepath.Join(base, "gortk")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return dir
}

func TestEnableThenDisableRoundTripsConfig(t *testing.T) {
	withConfigDir(t)
	path := core.ConfigPath()

	// Enable with endpoint + token.
	if code, err := Run([]string{"enable", "--endpoint", "https://sink.example/ingest", "--token", "abc123"}, 0); err != nil || code != 0 {
		t.Fatalf("enable: code=%d err=%v", code, err)
	}

	cfg := core.LoadConfig()
	if !cfg.Telemetry.Enabled {
		t.Errorf("expected Enabled=true after enable")
	}
	if cfg.Telemetry.Endpoint != "https://sink.example/ingest" {
		t.Errorf("endpoint = %q, want https://sink.example/ingest", cfg.Telemetry.Endpoint)
	}
	if cfg.Telemetry.Token != "abc123" {
		t.Errorf("token = %q, want abc123", cfg.Telemetry.Token)
	}

	// Disable: endpoint + token must be preserved, only enabled flips.
	if code, err := Run([]string{"disable"}, 0); err != nil || code != 0 {
		t.Fatalf("disable: code=%d err=%v", code, err)
	}
	cfg = core.LoadConfig()
	if cfg.Telemetry.Enabled {
		t.Errorf("expected Enabled=false after disable")
	}
	if cfg.Telemetry.Endpoint != "https://sink.example/ingest" {
		t.Errorf("disable lost the endpoint: %q", cfg.Telemetry.Endpoint)
	}
	if cfg.Telemetry.Token != "abc123" {
		t.Errorf("disable lost the token: %q", cfg.Telemetry.Token)
	}

	// Re-enable: should set enabled=true again, endpoint unchanged.
	if code, err := Run([]string{"enable", "--endpoint", "https://sink.example/ingest"}, 0); err != nil || code != 0 {
		t.Fatalf("re-enable: code=%d err=%v", code, err)
	}
	cfg = core.LoadConfig()
	if !cfg.Telemetry.Enabled {
		t.Errorf("expected Enabled=true after re-enable")
	}

	_ = path
}

func TestEnablePreservesExistingConfig(t *testing.T) {
	withConfigDir(t)
	path := core.ConfigPath()

	// Pre-seed a config with an unrelated [hooks] section + comment.
	seed := `# my gortk config
[hooks]
exclude_commands = ["curl", "gh"]
`
	if err := os.WriteFile(path, []byte(seed), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if code, err := Run([]string{"enable", "--endpoint", "https://sink.example"}, 0); err != nil || code != 0 {
		t.Fatalf("enable: code=%d err=%v", code, err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	got := string(data)

	// The hooks section, its key, and the comment must survive verbatim.
	for _, want := range []string{
		"# my gortk config",
		"[hooks]",
		`exclude_commands = ["curl", "gh"]`,
		"[telemetry]",
		"enabled = true",
		`endpoint = "https://sink.example"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("merged config missing %q:\n%s", want, got)
		}
	}

	// And it must still parse into the right struct.
	cfg := core.LoadConfig()
	if len(cfg.Hooks.ExcludeCommands) != 2 || cfg.Hooks.ExcludeCommands[0] != "curl" {
		t.Errorf("hooks lost: %+v", cfg.Hooks)
	}
	if !cfg.Telemetry.Enabled || cfg.Telemetry.Endpoint != "https://sink.example" {
		t.Errorf("telemetry not applied: %+v", cfg.Telemetry)
	}
}

func TestEnableRequiresEndpoint(t *testing.T) {
	withConfigDir(t)
	code, err := Run([]string{"enable"}, 0)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if code == 0 {
		t.Errorf("enable without --endpoint should fail (non-zero), got %d", code)
	}
	// No config should have been written enabling telemetry.
	cfg := core.LoadConfig()
	if cfg.Telemetry.Enabled {
		t.Errorf("telemetry enabled despite missing endpoint")
	}
}

func TestDisableOnAbsentConfigCreatesDisabled(t *testing.T) {
	withConfigDir(t)
	if code, err := Run([]string{"disable"}, 0); err != nil || code != 0 {
		t.Fatalf("disable: code=%d err=%v", code, err)
	}
	cfg := core.LoadConfig()
	if cfg.Telemetry.Enabled {
		t.Errorf("expected disabled telemetry after disable on absent config")
	}
}

func TestMaskToken(t *testing.T) {
	cases := map[string]string{
		"":            "",
		"ab":          "**",
		"abcd":        "****",
		"abcdef":      "ab**ef",
		"s3cr3ttoken": "s3*******en",
	}
	for in, want := range cases {
		if got := maskToken(in); got != want {
			t.Errorf("maskToken(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestQuoteTOML(t *testing.T) {
	cases := map[string]string{
		`plain`:          `"plain"`,
		`https://x/y`:    `"https://x/y"`,
		`has "quote"`:    `"has \"quote\""`,
		`back\slash`:     `"back\\slash"`,
		"tab\tand\nnewl": `"tab\tand\nnewl"`,
	}
	for in, want := range cases {
		if got := quoteTOML(in); got != want {
			t.Errorf("quoteTOML(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPreviewDoesNotSendAndReturnsZero(t *testing.T) {
	withConfigDir(t)
	if code, err := Run([]string{"preview"}, 0); err != nil || code != 0 {
		t.Fatalf("preview: code=%d err=%v", code, err)
	}
	// Nothing to assert about the network here (preview never sends); the core
	// telemetry tests cover the no-send guarantee. This just exercises the path.
}

func TestUnknownSubcommand(t *testing.T) {
	withConfigDir(t)
	code, _ := Run([]string{"bogus"}, 0)
	if code == 0 {
		t.Errorf("unknown subcommand should return non-zero, got 0")
	}
}

// Guard against accidental shared-state between the two test files when run in
// the same binary: ensure a fresh sync.Once usage doesn't leak. (No-op marker.)
var _ = sync.Once{}
