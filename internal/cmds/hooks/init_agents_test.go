package hooks

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ── Cursor installer (~/.cursor/hooks.json patch) ──

// cursorHome returns a temp user home for the Cursor installer, with CURSOR_HOME
// explicitly unset so resolution falls through to <home>/.cursor.
func cursorHome(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv(cursorHomeEnv, "")
	home := t.TempDir()
	return home, filepath.Join(home, cursorHomeDir, cursorHooksFile)
}

func cursorPreToolUseArray(t *testing.T, root map[string]any) []any {
	t.Helper()
	hooks, ok := root["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks.json missing hooks object: %v", root)
	}
	arr, ok := hooks["preToolUse"].([]any)
	if !ok {
		t.Fatalf("hooks missing preToolUse array: %v", hooks)
	}
	return arr
}

func TestCursorInstallCreatesHooks(t *testing.T) {
	home, hooksPath := cursorHome(t)

	if code, err := runCursorInitAt(home, false, false, 0); err != nil || code != 0 {
		t.Fatalf("install code=%d err=%v", code, err)
	}

	root := readJSON(t, hooksPath)
	if root["version"] != float64(1) {
		t.Errorf("version = %v, want 1", root["version"])
	}
	arr := cursorPreToolUseArray(t, root)
	if len(arr) != 1 {
		t.Fatalf("expected 1 preToolUse entry, got %d: %v", len(arr), arr)
	}
	entry := arr[0].(map[string]any)
	if entry["command"] != "gortk hook cursor" {
		t.Errorf("entry.command = %v, want %q", entry["command"], "gortk hook cursor")
	}
	if entry["matcher"] != "Shell" {
		t.Errorf("entry.matcher = %v, want \"Shell\"", entry["matcher"])
	}
	if _, err := os.Stat(hooksPath + ".bak"); !os.IsNotExist(err) {
		t.Errorf("backup should not exist for a newly created hooks.json (err=%v)", err)
	}
}

func TestCursorInstallPreservesExistingKeys(t *testing.T) {
	home, hooksPath := cursorHome(t)
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	pre := `{
  "version": 1,
  "custom": {"keep": true},
  "hooks": {
    "preToolUse": [
      {"command": "other-tool", "matcher": "Shell"}
    ],
    "postToolUse": [
      {"command": "after", "matcher": "Shell"}
    ]
  }
}`
	if err := os.WriteFile(hooksPath, []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}

	if code, err := runCursorInitAt(home, false, false, 0); err != nil || code != 0 {
		t.Fatalf("install code=%d err=%v", code, err)
	}

	root := readJSON(t, hooksPath)
	custom, _ := root["custom"].(map[string]any)
	if custom == nil || custom["keep"] != true {
		t.Errorf("custom key not preserved: %v", root["custom"])
	}
	hooks := root["hooks"].(map[string]any)
	if _, ok := hooks["postToolUse"].([]any); !ok {
		t.Errorf("postToolUse dropped: %v", hooks)
	}
	arr := cursorPreToolUseArray(t, root)
	if len(arr) != 2 {
		t.Fatalf("expected 2 preToolUse entries (existing + gortk), got %d: %v", len(arr), arr)
	}
	// The existing entry must survive alongside the new gortk entry.
	foundExisting, foundGortk := false, false
	for _, e := range arr {
		obj := e.(map[string]any)
		switch obj["command"] {
		case "other-tool":
			foundExisting = true
		case "gortk hook cursor":
			foundGortk = true
		}
	}
	if !foundExisting || !foundGortk {
		t.Errorf("entries = %v, want both other-tool and gortk hook cursor", arr)
	}
	if _, err := os.Stat(hooksPath + ".bak"); err != nil {
		t.Errorf("backup not created for pre-existing hooks.json: %v", err)
	}
}

func TestCursorInstallIdempotent(t *testing.T) {
	home, hooksPath := cursorHome(t)

	if code, err := runCursorInitAt(home, false, false, 0); err != nil || code != 0 {
		t.Fatalf("first install code=%d err=%v", code, err)
	}
	first, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if code, err := runCursorInitAt(home, false, false, 0); err != nil || code != 0 {
		t.Fatalf("second install code=%d err=%v", code, err)
	}
	second, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("second install changed hooks.json:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
	arr := cursorPreToolUseArray(t, readJSON(t, hooksPath))
	if len(arr) != 1 {
		t.Errorf("idempotent install duplicated entry: %d entries", len(arr))
	}
}

func TestCursorDryRunWritesNothing(t *testing.T) {
	home, hooksPath := cursorHome(t)
	if code, err := runCursorInitAt(home, false, true, 0); err != nil || code != 0 {
		t.Fatalf("dry-run code=%d err=%v", code, err)
	}
	if _, err := os.Stat(hooksPath); !os.IsNotExist(err) {
		t.Errorf("--dry-run wrote hooks.json (err=%v)", err)
	}
}

func TestCursorShowWritesNothing(t *testing.T) {
	home, hooksPath := cursorHome(t)
	if code, err := runCursorInitAt(home, true, false, 0); err != nil || code != 0 {
		t.Fatalf("show code=%d err=%v", code, err)
	}
	if _, err := os.Stat(hooksPath); !os.IsNotExist(err) {
		t.Errorf("--show wrote hooks.json (err=%v)", err)
	}
}

func TestCursorRefusesMalformedJSON(t *testing.T) {
	home, hooksPath := cursorHome(t)
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooksPath, []byte("{ not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, err := runCursorInitAt(home, false, false, 0)
	if code == 0 || err == nil {
		t.Fatalf("expected refusal on malformed JSON, got code=%d err=%v", code, err)
	}
	// File must be left untouched.
	data, _ := os.ReadFile(hooksPath)
	if string(data) != "{ not valid json" {
		t.Errorf("malformed file was modified: %s", data)
	}
}

// ── Windsurf installer (project ./.windsurfrules) ──

func TestWindsurfInstallCreatesRules(t *testing.T) {
	base := t.TempDir()
	rulesPath := filepath.Join(base, ".windsurfrules")

	if code, err := runRulesInitAt(windsurfAgent, base, false, false, 0); err != nil || code != 0 {
		t.Fatalf("install code=%d err=%v", code, err)
	}
	data, err := os.ReadFile(rulesPath)
	if err != nil {
		t.Fatalf("rules file not created: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "gortk") {
		t.Errorf("rules file missing gortk guidance:\n%s", body)
	}
	if !strings.Contains(body, "Windsurf") {
		t.Errorf("rules file missing agent label:\n%s", body)
	}
}

func TestWindsurfPreservesExistingContent(t *testing.T) {
	base := t.TempDir()
	rulesPath := filepath.Join(base, ".windsurfrules")
	existing := "# My project rules\n\nDo the thing.\n"
	if err := os.WriteFile(rulesPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	if code, err := runRulesInitAt(windsurfAgent, base, false, false, 0); err != nil || code != 0 {
		t.Fatalf("install code=%d err=%v", code, err)
	}
	data, _ := os.ReadFile(rulesPath)
	body := string(data)
	if !strings.Contains(body, "My project rules") {
		t.Errorf("existing user content not preserved:\n%s", body)
	}
	if !strings.Contains(body, "gortk") {
		t.Errorf("gortk block not appended:\n%s", body)
	}
	// The user content must come BEFORE the appended block.
	if strings.Index(body, "My project rules") > strings.Index(body, "Token-Optimized CLI") {
		t.Errorf("appended block should follow user content:\n%s", body)
	}
}

func TestWindsurfIdempotent(t *testing.T) {
	base := t.TempDir()
	rulesPath := filepath.Join(base, ".windsurfrules")

	if code, err := runRulesInitAt(windsurfAgent, base, false, false, 0); err != nil || code != 0 {
		t.Fatalf("first install code=%d err=%v", code, err)
	}
	first, _ := os.ReadFile(rulesPath)
	if code, err := runRulesInitAt(windsurfAgent, base, false, false, 0); err != nil || code != 0 {
		t.Fatalf("second install code=%d err=%v", code, err)
	}
	second, _ := os.ReadFile(rulesPath)
	if string(first) != string(second) {
		t.Errorf("second install changed .windsurfrules:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

func TestWindsurfDryRunAndShowWriteNothing(t *testing.T) {
	for _, mode := range []struct {
		name         string
		show, dryRun bool
	}{
		{"dry-run", false, true},
		{"show", true, false},
	} {
		base := t.TempDir()
		rulesPath := filepath.Join(base, ".windsurfrules")
		if code, err := runRulesInitAt(windsurfAgent, base, mode.show, mode.dryRun, 0); err != nil || code != 0 {
			t.Fatalf("%s code=%d err=%v", mode.name, code, err)
		}
		if _, err := os.Stat(rulesPath); !os.IsNotExist(err) {
			t.Errorf("--%s wrote .windsurfrules (err=%v)", mode.name, err)
		}
	}
}

// Kilocode writes into a nested dir — confirm the dir+file are created.
func TestKilocodeInstallCreatesNestedRules(t *testing.T) {
	base := t.TempDir()
	rulesPath := filepath.Join(base, ".kilocode", "rules", "gortk-rules.md")

	if code, err := runRulesInitAt(kilocodeAgent, base, false, false, 0); err != nil || code != 0 {
		t.Fatalf("install code=%d err=%v", code, err)
	}
	if _, err := os.Stat(rulesPath); err != nil {
		t.Fatalf("nested rules file not created: %v", err)
	}
}

// ── OpenCode installer (~/.config/opencode/plugins/gortk.ts) ──

func opencodeHome(t *testing.T) (string, string) {
	t.Helper()
	home := t.TempDir()
	return home, filepath.Join(home, opencodeConfigDir, opencodeSubdir, pluginsSubdir, opencodePluginFile)
}

func TestOpencodeInstallCreatesPlugin(t *testing.T) {
	home, pluginPath := opencodeHome(t)

	if code, err := runOpencodeInitAt(home, false, false, 0); err != nil || code != 0 {
		t.Fatalf("install code=%d err=%v", code, err)
	}
	data, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("plugin not created: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "gortk rewrite") {
		t.Errorf("plugin does not delegate to gortk rewrite:\n%s", body)
	}
	// Guard against an un-ported template: a bare "rtk" reference (the rtk binary
	// name) must not survive. Note "gortk" contains "rtk" as a substring, so check
	// for the standalone rtk invocation tokens instead.
	if strings.Contains(body, "\"rtk\"") || strings.Contains(body, "`rtk ") || strings.Contains(body, " rtk ") {
		t.Errorf("plugin still references the rtk binary:\n%s", body)
	}
}

func TestOpencodeInstallIdempotent(t *testing.T) {
	home, pluginPath := opencodeHome(t)

	if code, err := runOpencodeInitAt(home, false, false, 0); err != nil || code != 0 {
		t.Fatalf("first install code=%d err=%v", code, err)
	}
	info1, err := os.Stat(pluginPath)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(pluginPath)

	if code, err := runOpencodeInitAt(home, false, false, 0); err != nil || code != 0 {
		t.Fatalf("second install code=%d err=%v", code, err)
	}
	second, _ := os.ReadFile(pluginPath)
	if string(first) != string(second) {
		t.Errorf("second install changed the plugin content")
	}
	info2, _ := os.Stat(pluginPath)
	if !info2.ModTime().Equal(info1.ModTime()) {
		t.Errorf("idempotent install rewrote the file (mtime changed)")
	}
}

func TestOpencodeDryRunAndShowWriteNothing(t *testing.T) {
	for _, mode := range []struct {
		name         string
		show, dryRun bool
	}{
		{"dry-run", false, true},
		{"show", true, false},
	} {
		home, pluginPath := opencodeHome(t)
		if code, err := runOpencodeInitAt(home, mode.show, mode.dryRun, 0); err != nil || code != 0 {
			t.Fatalf("%s code=%d err=%v", mode.name, code, err)
		}
		if _, err := os.Stat(pluginPath); !os.IsNotExist(err) {
			t.Errorf("--%s wrote the plugin (err=%v)", mode.name, err)
		}
	}
}

// ── Pi installer (env override + nested extensions dir) ──

func TestPiInstallHonorsEnvOverride(t *testing.T) {
	piRoot := t.TempDir()
	t.Setenv(piHomeEnv, piRoot)
	home := t.TempDir() // should be ignored because the env override is set

	if code, err := runPiInitAt(home, false, false, 0); err != nil || code != 0 {
		t.Fatalf("install code=%d err=%v", code, err)
	}
	pluginPath := filepath.Join(piRoot, piExtensionsDir, piPluginFile)
	data, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("pi extension not created at env-override path: %v", err)
	}
	if !strings.Contains(string(data), "gortk") {
		t.Errorf("pi extension missing gortk delegation:\n%s", data)
	}
}

// ── Hermes installer (plugin dir + config.yaml enable) ──

func hermesHome(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv(hermesHomeEnv, "")
	home := t.TempDir()
	return home, filepath.Join(home, hermesHomeDir)
}

func TestHermesInstallWritesPluginAndConfig(t *testing.T) {
	home, hermesDir := hermesHome(t)

	if code, err := runHermesInitAt(home, false, false, 0); err != nil || code != 0 {
		t.Fatalf("install code=%d err=%v", code, err)
	}
	initPath := filepath.Join(hermesDir, hermesPluginsDir, hermesPluginName, hermesInitFile)
	manifestPath := filepath.Join(hermesDir, hermesPluginsDir, hermesPluginName, hermesManifestFile)
	configPath := filepath.Join(hermesDir, hermesConfigFile)

	initData, err := os.ReadFile(initPath)
	if err != nil {
		t.Fatalf("plugin __init__.py not created: %v", err)
	}
	if !strings.Contains(string(initData), "gortk") {
		t.Errorf("plugin does not delegate to gortk:\n%s", initData)
	}
	if _, err := os.ReadFile(manifestPath); err != nil {
		t.Fatalf("plugin.yaml not created: %v", err)
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config.yaml not created: %v", err)
	}
	if !strings.Contains(string(configData), hermesPluginName) {
		t.Errorf("config.yaml does not enable %s:\n%s", hermesPluginName, configData)
	}
}

func TestHermesInstallIdempotent(t *testing.T) {
	home, _ := hermesHome(t)

	if code, err := runHermesInitAt(home, false, false, 0); err != nil || code != 0 {
		t.Fatalf("first install code=%d err=%v", code, err)
	}
	if code, err := runHermesInitAt(home, false, false, 0); err != nil || code != 0 {
		t.Fatalf("second install code=%d err=%v", code, err)
	}
	// A second install over a config that already enables gortk must not duplicate.
	configPath := filepath.Join(home, hermesHomeDir, hermesConfigFile)
	data, _ := os.ReadFile(configPath)
	if n := strings.Count(string(data), hermesPluginName); n != 1 {
		t.Errorf("config.yaml mentions %s %d times, want 1:\n%s", hermesPluginName, n, data)
	}
}

func TestHermesDryRunWritesNothing(t *testing.T) {
	home, hermesDir := hermesHome(t)
	if code, err := runHermesInitAt(home, false, true, 0); err != nil || code != 0 {
		t.Fatalf("dry-run code=%d err=%v", code, err)
	}
	if _, err := os.Stat(hermesDir); !os.IsNotExist(err) {
		t.Errorf("--dry-run created the Hermes home (err=%v)", err)
	}
}

// ── Dispatch ──

func TestRunAgentInitUnknownAgent(t *testing.T) {
	code, err := runAgentInit("nonsuch", false, false, 0)
	if code != 2 || err != nil {
		t.Fatalf("unknown agent: code=%d err=%v, want code=2 err=nil", code, err)
	}
}

func TestAgentNamesCoverAllInstallers(t *testing.T) {
	names := agentNames()
	want := []string{"antigravity", "cline", "cursor", "hermes", "kilocode", "opencode", "pi", "windsurf"}
	if len(names) != len(want) {
		t.Fatalf("agentNames() = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("agentNames()[%d] = %q, want %q", i, names[i], want[i])
		}
	}
}
