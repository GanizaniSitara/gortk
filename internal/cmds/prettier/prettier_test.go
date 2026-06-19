package prettier

import (
	"fmt"
	"strings"
	"testing"
)

// Ported from rtk's prettier_cmd.rs #[cfg(test)] mod tests.

func TestFilterAllFormatted(t *testing.T) {
	output := "\nChecking formatting...\nAll matched files use Prettier code style!\n        "
	result := filterPrettierOutput(output)
	if !strings.Contains(result, "Prettier") {
		t.Errorf("result missing %q: %s", "Prettier", result)
	}
	if !strings.Contains(result, "All files formatted correctly") {
		t.Errorf("result missing %q: %s", "All files formatted correctly", result)
	}
}

func TestFilterFilesNeedFormatting(t *testing.T) {
	output := "\nChecking formatting...\n" +
		"src/components/ui/button.tsx\n" +
		"src/lib/auth/session.ts\n" +
		"src/pages/dashboard.tsx\n" +
		"Code style issues found in the above file(s). Forgot to run Prettier?\n        "
	result := filterPrettierOutput(output)
	for _, want := range []string{"3 files need formatting", "button.tsx", "session.ts"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q: %s", want, result)
		}
	}
}

func TestFilterManyFiles(t *testing.T) {
	var b strings.Builder
	b.WriteString("Checking formatting...\n")
	for i := 0; i < 15; i++ {
		fmt.Fprintf(&b, "src/file%d.ts\n", i)
	}
	result := filterPrettierOutput(b.String())
	for _, want := range []string{"15 files need formatting", "... +5 more files"} {
		if !strings.Contains(result, want) {
			t.Errorf("result missing %q: %s", want, result)
		}
	}
}

// --- #221: empty output should not say "All files formatted" ---

func TestFilterEmptyOutput(t *testing.T) {
	result := filterPrettierOutput("")
	if !strings.Contains(result, "Error") {
		t.Errorf("result missing %q: %s", "Error", result)
	}
	if strings.Contains(result, "All files formatted") {
		t.Errorf("result should not contain %q: %s", "All files formatted", result)
	}
}

func TestFilterWhitespaceOnlyOutput(t *testing.T) {
	result := filterPrettierOutput("   \n\n  ")
	if !strings.Contains(result, "Error") {
		t.Errorf("result missing %q: %s", "Error", result)
	}
	if strings.Contains(result, "All files formatted") {
		t.Errorf("result should not contain %q: %s", "All files formatted", result)
	}
}
