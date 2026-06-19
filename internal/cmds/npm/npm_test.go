package npm

import (
	"strings"
	"testing"
)

func TestFilterNpmOutput(t *testing.T) {
	output := "\n" +
		"> project@1.0.0 build\n" +
		"> next build\n" +
		"\n" +
		"npm WARN deprecated inflight@1.0.6: This module is not supported\n" +
		"npm notice\n" +
		"\n" +
		"   Creating an optimized production build...\n" +
		"   ✓ Build completed\n"

	result := filterNpmOutput(output)

	if strings.Contains(result, "npm WARN") {
		t.Errorf("result should not contain npm WARN: %q", result)
	}
	if strings.Contains(result, "npm notice") {
		t.Errorf("result should not contain npm notice: %q", result)
	}
	if strings.Contains(result, "> project@") {
		t.Errorf("result should not contain lifecycle banner: %q", result)
	}
	if !strings.Contains(result, "Build completed") {
		t.Errorf("result should contain Build completed: %q", result)
	}
}

func TestNpmSubcommandRouting(t *testing.T) {
	// Known subcommands should NOT get "run" injected.
	for _, subcmd := range npmSubcommands {
		if needsRunInjection([]string{subcmd}) {
			t.Errorf("'npm %s' should NOT inject 'run'", subcmd)
		}
	}

	// Script names SHOULD get "run" injected.
	for _, script := range []string{"build", "dev", "lint", "typecheck", "deploy"} {
		if !needsRunInjection([]string{script}) {
			t.Errorf("'npm %s' SHOULD inject 'run'", script)
		}
	}

	// Flags should NOT get "run" injected.
	if needsRunInjection([]string{"--version"}) {
		t.Errorf("'npm --version' should NOT inject 'run'")
	}
	if needsRunInjection([]string{"-h"}) {
		t.Errorf("'npm -h' should NOT inject 'run'")
	}

	// Explicit "run" should NOT inject another "run".
	if needsRunInjection([]string{"run", "build"}) {
		t.Errorf("'npm run build' should NOT inject another 'run'")
	}
}

func TestFilterNpmOutputEmpty(t *testing.T) {
	result := filterNpmOutput("\n\n\n")
	if result != "ok" {
		t.Errorf("want %q, got %q", "ok", result)
	}
}
