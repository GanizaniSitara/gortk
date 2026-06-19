package pip

import (
	"strings"
	"testing"
)

// Ported from rtk's pip_cmd.rs #[cfg(test)] mod tests, matching inputs/expected
// exactly.

func TestFilterPipList(t *testing.T) {
	output := `[
  {"name": "requests", "version": "2.31.0"},
  {"name": "pytest", "version": "7.4.0"},
  {"name": "rich", "version": "13.0.0"}
]`
	result := filterPipList(output)
	for _, want := range []string{"3 packages", "requests", "2.31.0", "pytest"} {
		if !strings.Contains(result, want) {
			t.Errorf("filterPipList missing %q: %s", want, result)
		}
	}
}

func TestFilterPipListEmpty(t *testing.T) {
	result := filterPipList("[]")
	if !strings.Contains(result, "No packages installed") {
		t.Errorf("want 'No packages installed', got %q", result)
	}
}

func TestFilterPipOutdatedNone(t *testing.T) {
	result := filterPipOutdated("[]")
	if !strings.Contains(result, "All packages up to date") {
		t.Errorf("want 'All packages up to date', got %q", result)
	}
}

func TestFilterPipOutdatedSome(t *testing.T) {
	output := `[
  {"name": "requests", "version": "2.31.0", "latest_version": "2.32.0"},
  {"name": "pytest", "version": "7.4.0", "latest_version": "8.0.0"}
]`
	result := filterPipOutdated(output)
	for _, want := range []string{
		"2 packages",
		"requests",
		"2.31.0 → 2.32.0",
		"pytest",
		"7.4.0 → 8.0.0",
	} {
		if !strings.Contains(result, want) {
			t.Errorf("filterPipOutdated missing %q: %s", want, result)
		}
	}
}
