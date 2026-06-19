package smart

import (
	"strings"
	"testing"

	"gortk/internal/core"
)

// Ported from rtk local_llm.rs test_rust_analysis.
func TestRustAnalysis(t *testing.T) {
	code := "\n" +
		"use anyhow::Result;\n" +
		"use std::fs;\n" +
		"\n" +
		"pub struct Config {\n" +
		"    name: String,\n" +
		"}\n" +
		"\n" +
		"pub fn load_config() -> Result<Config> {\n" +
		"    Ok(Config { name: \"test\".into() })\n" +
		"}\n" +
		"\n" +
		"fn helper() {}\n"
	summary := analyzeCode(code, core.LangRust)
	if !strings.Contains(summary.line1, "Rust") {
		t.Errorf("line1 should contain Rust: %q", summary.line1)
	}
	if !strings.Contains(summary.line1, "fn") {
		t.Errorf("line1 should contain fn: %q", summary.line1)
	}
}

// Ported from rtk local_llm.rs test_python_analysis.
func TestPythonAnalysis(t *testing.T) {
	code := "\n" +
		"import json\n" +
		"from pathlib import Path\n" +
		"\n" +
		"class Config:\n" +
		"    def __init__(self, name):\n" +
		"        self.name = name\n" +
		"\n" +
		"def load_config():\n" +
		"    return Config(\"test\")\n"
	summary := analyzeCode(code, core.LangPython)
	if !strings.Contains(summary.line1, "Python") {
		t.Errorf("line1 should contain Python: %q", summary.line1)
	}
}

// Rust: std/core/alloc imports are filtered; anyhow is kept; "main"/"new" and
// test_* functions are excluded.
func TestRustImportAndFunctionFiltering(t *testing.T) {
	code := "use std::fs;\n" +
		"use anyhow::Result;\n" +
		"fn main() {}\n" +
		"fn new() {}\n" +
		"fn test_thing() {}\n" +
		"pub fn real_one() {}\n"
	summary := analyzeCode(code, core.LangRust)
	// "uses: anyhow" — std should not appear.
	if !strings.Contains(summary.line2, "anyhow") {
		t.Errorf("expected anyhow in line2: %q", summary.line2)
	}
	if strings.Contains(summary.line2, "std") {
		t.Errorf("std should be filtered out: %q", summary.line2)
	}
	// Only real_one counts (main/new/test_* excluded) -> "1 fn".
	if !strings.Contains(summary.line1, "1 fn") {
		t.Errorf("expected 1 fn (main/new/test_ excluded): %q", summary.line1)
	}
}

// Rust pattern detection: derive + error handling + tests.
func TestRustPatternDetection(t *testing.T) {
	code := "use anyhow::Result;\n" +
		"#[derive(Debug)]\n" +
		"pub struct S;\n" +
		"#[test]\n" +
		"fn test_x() {}\n"
	patterns := detectPatterns(code, core.LangRust)
	joined := strings.Join(patterns, ",")
	for _, want := range []string{"derive", "error handling", "tests"} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected pattern %q in %q", want, joined)
		}
	}
	// take(3) caps the count.
	if len(patterns) > 3 {
		t.Errorf("patterns should cap at 3, got %d", len(patterns))
	}
}

// Go analysis: type ... struct and func names, with go imports.
func TestGoAnalysis(t *testing.T) {
	code := "package main\n" +
		"\n" +
		"import (\n" +
		"\t\"fmt\"\n" +
		"\t\"strings\"\n" +
		")\n" +
		"\n" +
		"type Server struct {\n" +
		"\tName string\n" +
		"}\n" +
		"\n" +
		"func (s *Server) Start() {}\n" +
		"func handle() {}\n"
	summary := analyzeCode(code, core.LangGo)
	if !strings.Contains(summary.line1, "Go module") {
		t.Errorf("expected 'Go module' (has struct + fn): %q", summary.line1)
	}
	// imports fmt + strings are not Go std-filtered (only Rust/Python filter), so
	// they should show up.
	if !strings.Contains(summary.line2, "fmt") {
		t.Errorf("expected fmt import in line2: %q", summary.line2)
	}
}

// A bare data/unknown file with nothing structural -> code + general message.
func TestEmptyish(t *testing.T) {
	summary := analyzeCode("just some text\nmore text\n", core.LangUnknown)
	if !strings.Contains(summary.line1, "Code") {
		t.Errorf("expected 'Code' main type: %q", summary.line1)
	}
	if summary.line2 != "General purpose code file" {
		t.Errorf("expected general purpose line2, got %q", summary.line2)
	}
}

// langDisplayName covers every Language variant.
func TestLangDisplayName(t *testing.T) {
	cases := map[core.Language]string{
		core.LangRust:       "Rust",
		core.LangPython:     "Python",
		core.LangJavaScript: "JavaScript",
		core.LangTypeScript: "TypeScript",
		core.LangGo:         "Go",
		core.LangC:          "C",
		core.LangCpp:        "C++",
		core.LangJava:       "Java",
		core.LangRuby:       "Ruby",
		core.LangShell:      "Shell",
		core.LangData:       "Data",
		core.LangUnknown:    "Code",
	}
	for lang, want := range cases {
		if got := langDisplayName(lang); got != want {
			t.Errorf("langDisplayName(%v) = %q, want %q", lang, got, want)
		}
	}
}

// Python detects OOP via __init__ and keeps non-std imports (pathlib), filtering
// json (std).
func TestPythonImportsAndPatterns(t *testing.T) {
	code := "import json\n" +
		"from pathlib import Path\n" +
		"class C:\n" +
		"    def __init__(self):\n" +
		"        pass\n"
	summary := analyzeCode(code, core.LangPython)
	if !strings.Contains(summary.line2, "pathlib") {
		t.Errorf("expected pathlib import: %q", summary.line2)
	}
	if strings.Contains(summary.line2, "json") {
		t.Errorf("json is std and should be filtered: %q", summary.line2)
	}
	if !strings.Contains(summary.line2, "OOP") {
		t.Errorf("expected OOP pattern from __init__: %q", summary.line2)
	}
}
