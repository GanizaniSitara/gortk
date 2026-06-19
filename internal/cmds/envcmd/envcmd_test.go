package envcmd

import (
	"strings"
	"testing"
)

// Faithful port of the #[cfg(test)] mod tests block in rtk's
// src/cmds/system/env_cmd.rs. Each exercises a pure helper directly with the
// same inputs/expected outputs.

func TestMaskValueShort(t *testing.T) {
	if got := maskValue("abc"); got != "****" {
		t.Errorf("maskValue(abc) = %q", got)
	}
	if got := maskValue(""); got != "****" {
		t.Errorf("maskValue(\"\") = %q", got)
	}
}

func TestMaskValueLong(t *testing.T) {
	result := maskValue("supersecrettoken")
	if !strings.Contains(result, "****") {
		t.Errorf("masked value should contain ****: %q", result)
	}
	if !strings.HasPrefix(result, "su") {
		t.Errorf("should preserve 2-char prefix: %q", result)
	}
	if !strings.HasSuffix(result, "en") {
		t.Errorf("should preserve 2-char suffix: %q", result)
	}
}

func TestMaskValueExactlyFour(t *testing.T) {
	if got := maskValue("abcd"); got != "****" {
		t.Errorf("maskValue(abcd) = %q", got)
	}
}

func TestMaskValueFiveChars(t *testing.T) {
	result := maskValue("abcde")
	if !strings.HasPrefix(result, "ab") {
		t.Errorf("should preserve prefix: %q", result)
	}
	if !strings.HasSuffix(result, "de") {
		t.Errorf("should preserve suffix: %q", result)
	}
}

func TestIsLangVarRust(t *testing.T) {
	for _, k := range []string{"RUST_LOG", "CARGO_HOME", "GOPATH", "NODE_ENV"} {
		if !isLangVar(k) {
			t.Errorf("isLangVar(%q) should be true", k)
		}
	}
}

func TestIsLangVarNegative(t *testing.T) {
	for _, k := range []string{"HOME", "PATH", "USER"} {
		if isLangVar(k) {
			t.Errorf("isLangVar(%q) should be false", k)
		}
	}
}

func TestIsCloudVar(t *testing.T) {
	for _, k := range []string{"AWS_ACCESS_KEY_ID", "AZURE_CLIENT_ID", "DOCKER_HOST", "KUBERNETES_SERVICE_HOST"} {
		if !isCloudVar(k) {
			t.Errorf("isCloudVar(%q) should be true", k)
		}
	}
}

func TestIsCloudVarNegative(t *testing.T) {
	for _, k := range []string{"HOME", "RUST_LOG"} {
		if isCloudVar(k) {
			t.Errorf("isCloudVar(%q) should be false", k)
		}
	}
}

func TestIsToolVar(t *testing.T) {
	for _, k := range []string{"EDITOR", "GIT_AUTHOR_NAME", "SSH_AUTH_SOCK", "CLAUDE_API_KEY"} {
		if !isToolVar(k) {
			t.Errorf("isToolVar(%q) should be true", k)
		}
	}
}

func TestIsInterestingVar(t *testing.T) {
	for _, k := range []string{"HOME", "USER", "LANG", "TZ", "PWD"} {
		if !isInterestingVar(k) {
			t.Errorf("isInterestingVar(%q) should be true", k)
		}
	}
}

func TestIsInterestingVarNegative(t *testing.T) {
	for _, k := range []string{"RANDOM_VAR", "MY_CUSTOM_VAR"} {
		if isInterestingVar(k) {
			t.Errorf("isInterestingVar(%q) should be false", k)
		}
	}
}

// rtk's get_sensitive_patterns() returns the sensitive-substring set; the gortk
// equivalent is the package-level sensitivePatterns slice. Assert it contains
// the same core entries.
func TestSensitivePatternsContainsKeys(t *testing.T) {
	want := []string{"key", "secret", "password", "token"}
	for _, w := range want {
		found := false
		for _, p := range sensitivePatterns {
			if p == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("sensitivePatterns missing %q", w)
		}
	}
}
