// Package smart produces a 2-line heuristic technical summary of a source file.
// It is a faithful port of the HEURISTIC path of rtk's
// src/cmds/system/local_llm.rs (Commands::Smart).
//
// rtk's "smart" command historically could shell out to a downloaded local LLM;
// gortk is offline by default, so ALL model-download / network / local-LLM
// invocation behaviour is dropped. What remains is the pure heuristic analyzer,
// which is the only non-network path rtk's current local_llm.rs actually uses
// (its `model` and `force_download` flags were already inert no-ops there). The
// summary is built entirely from regex-based structural extraction.
package smart

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "smart",
		Summary: "Print a 2-line heuristic summary of a source file",
		Run:     Run,
	})
}

// Run reads the first non-flag argument as the file to summarize and prints the
// 2-line heuristic summary. The dropped `--model` / `--force-download` flags are
// accepted-and-ignored so existing invocations don't error.
func Run(args []string, verbose int) (int, error) {
	var file string
	haveFile := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-m" || a == "--model":
			i++ // consume and ignore the value (model selection is gone)
		case strings.HasPrefix(a, "--model="):
			// ignore
		case a == "--force-download":
			// ignore: gortk never downloads models
		case strings.HasPrefix(a, "-"):
			// ignore any other flag
		default:
			if !haveFile {
				file = a
				haveFile = true
			}
		}
	}

	if !haveFile {
		fmt.Fprintln(os.Stderr, "gortk: smart requires a file to analyze")
		return 2, nil
	}

	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Analyzing: %s\n", file)
	}

	data, err := os.ReadFile(file)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gortk: Failed to read file: %s\n", file)
		return 1, nil
	}
	content := core.NormalizeNewlines(string(data))

	ext := strings.TrimPrefix(filepath.Ext(file), ".")
	lang := core.LanguageFromExt(ext)

	summary := analyzeCode(content, lang)
	fmt.Println(summary.line1)
	fmt.Println(summary.line2)
	return 0, nil
}

// codeSummary is the two-line heuristic result.
type codeSummary struct {
	line1 string
	line2 string
}

// Extraction regexes per language. All are RE2-compatible (no backreferences or
// lookarounds in the originals).
var (
	reRustImport = regexp.MustCompile(`^use\s+([a-zA-Z_][a-zA-Z0-9_]*(?:::[a-zA-Z_][a-zA-Z0-9_]*)?)`)
	rePyImport   = regexp.MustCompile(`^(?:from\s+(\S+)|import\s+(\S+))`)
	reJsImport   = regexp.MustCompile(`(?:import.*from\s+['"]([^'"]+)['"]|require\(['"]([^'"]+)['"]\))`)
	reGoImport   = regexp.MustCompile(`^\s*"([^"]+)"$`)

	reRustFn = regexp.MustCompile(`(?:pub\s+)?(?:async\s+)?fn\s+([a-zA-Z_][a-zA-Z0-9_]*)`)
	rePyFn   = regexp.MustCompile(`def\s+([a-zA-Z_][a-zA-Z0-9_]*)`)
	reJsFn   = regexp.MustCompile(`(?:async\s+)?function\s+([a-zA-Z_][a-zA-Z0-9_]*)|(?:const|let|var)\s+([a-zA-Z_][a-zA-Z0-9_]*)\s*=\s*(?:async\s+)?\(`)
	reGoFn   = regexp.MustCompile(`func\s+(?:\([^)]+\)\s+)?([a-zA-Z_][a-zA-Z0-9_]*)`)

	reRustStruct = regexp.MustCompile(`(?:pub\s+)?(?:struct|enum)\s+([a-zA-Z_][a-zA-Z0-9_]*)`)
	rePyStruct   = regexp.MustCompile(`class\s+([a-zA-Z_][a-zA-Z0-9_]*)`)
	reTsStruct   = regexp.MustCompile(`(?:interface|class|type)\s+([a-zA-Z_][a-zA-Z0-9_]*)`)
	reGoStruct   = regexp.MustCompile(`type\s+([a-zA-Z_][a-zA-Z0-9_]*)\s+struct`)
	reJavaStruct = regexp.MustCompile(`(?:public\s+)?class\s+([a-zA-Z_][a-zA-Z0-9_]*)`)

	reRustTrait = regexp.MustCompile(`(?:pub\s+)?trait\s+([a-zA-Z_][a-zA-Z0-9_]*)`)
	reTsTrait   = regexp.MustCompile(`interface\s+([a-zA-Z_][a-zA-Z0-9_]*)`)
)

// lines mirrors Rust's str::lines(): split on "\n", dropping a single trailing
// empty element from a final newline.
func lines(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

func analyzeCode(content string, lang core.Language) codeSummary {
	totalLines := len(lines(content))

	imports := extractImports(content, lang)
	functions := extractFunctions(content, lang)
	structs := extractStructs(content, lang)
	traits := extractTraits(content, lang)

	patterns := detectPatterns(content, lang)

	// Line 1: what it is.
	langName := langDisplayName(lang)
	var mainType string
	switch {
	case len(structs) > 0 && len(functions) > 0:
		mainType = langName + " module"
	case len(structs) > 0:
		mainType = langName + " data structures"
	case len(functions) > 0:
		mainType = langName + " functions"
	default:
		mainType = langName + " code"
	}

	var components []string
	if len(functions) > 0 {
		components = append(components, fmt.Sprintf("%d fn", len(functions)))
	}
	if len(structs) > 0 {
		components = append(components, fmt.Sprintf("%d struct", len(structs)))
	}
	if len(traits) > 0 {
		components = append(components, fmt.Sprintf("%d trait", len(traits)))
	}

	var line1 string
	if len(components) == 0 {
		line1 = fmt.Sprintf("%s (%d lines)", mainType, totalLines)
	} else {
		line1 = fmt.Sprintf("%s (%s) - %d lines", mainType, strings.Join(components, ", "), totalLines)
	}

	// Line 2: key details.
	var details []string
	if len(imports) > 0 {
		details = append(details, "uses: "+strings.Join(takeN(imports, 3), ", "))
	}
	if len(patterns) > 0 {
		details = append(details, "patterns: "+strings.Join(patterns, ", "))
	}
	if len(functions) > 0 {
		if len(details) == 0 {
			details = append(details, "defines: "+strings.Join(takeN(functions, 3), ", "))
		}
	}

	var line2 string
	if len(details) == 0 {
		line2 = "General purpose code file"
	} else {
		line2 = strings.Join(details, " | ")
	}

	return codeSummary{line1: line1, line2: line2}
}

func langDisplayName(lang core.Language) string {
	switch lang {
	case core.LangRust:
		return "Rust"
	case core.LangPython:
		return "Python"
	case core.LangJavaScript:
		return "JavaScript"
	case core.LangTypeScript:
		return "TypeScript"
	case core.LangGo:
		return "Go"
	case core.LangC:
		return "C"
	case core.LangCpp:
		return "C++"
	case core.LangJava:
		return "Java"
	case core.LangRuby:
		return "Ruby"
	case core.LangShell:
		return "Shell"
	case core.LangData:
		return "Data"
	default:
		return "Code"
	}
}

func extractImports(content string, lang core.Language) []string {
	var re *regexp.Regexp
	switch lang {
	case core.LangRust:
		re = reRustImport
	case core.LangPython:
		re = rePyImport
	case core.LangJavaScript, core.LangTypeScript:
		re = reJsImport
	case core.LangGo:
		re = reGoImport
	default:
		return nil
	}

	var imports []string
	seen := map[string]bool{}
	for _, line := range lines(content) {
		caps := re.FindStringSubmatch(line)
		if caps == nil {
			continue
		}
		imp := firstNonEmptyGroup(caps)
		if imp == "" {
			continue
		}
		base := imp
		if idx := strings.Index(imp, "::"); idx >= 0 {
			base = imp[:idx]
		}
		if !seen[base] && !isStdImport(base, lang) {
			seen[base] = true
			imports = append(imports, base)
		}
	}
	return takeN(imports, 5)
}

func isStdImport(name string, lang core.Language) bool {
	switch lang {
	case core.LangRust:
		return name == "std" || name == "core" || name == "alloc"
	case core.LangPython:
		switch name {
		case "os", "sys", "re", "json", "typing":
			return true
		}
		return false
	default:
		return false
	}
}

func extractFunctions(content string, lang core.Language) []string {
	var re *regexp.Regexp
	switch lang {
	case core.LangRust:
		re = reRustFn
	case core.LangPython:
		re = rePyFn
	case core.LangJavaScript, core.LangTypeScript:
		re = reJsFn
	case core.LangGo:
		re = reGoFn
	default:
		return nil
	}

	var functions []string
	for _, line := range lines(content) {
		caps := re.FindStringSubmatch(line)
		if caps == nil {
			continue
		}
		name := firstNonEmptyGroup(caps)
		if name == "" {
			continue
		}
		if !strings.HasPrefix(name, "test_") && name != "main" && name != "new" {
			functions = append(functions, name)
		}
	}
	return takeN(functions, 10)
}

func extractStructs(content string, lang core.Language) []string {
	var re *regexp.Regexp
	switch lang {
	case core.LangRust:
		re = reRustStruct
	case core.LangPython:
		re = rePyStruct
	case core.LangTypeScript:
		re = reTsStruct
	case core.LangGo:
		re = reGoStruct
	case core.LangJava:
		re = reJavaStruct
	default:
		return nil
	}
	return captureAll(re, content, 10)
}

func extractTraits(content string, lang core.Language) []string {
	var re *regexp.Regexp
	switch lang {
	case core.LangRust:
		re = reRustTrait
	case core.LangTypeScript:
		re = reTsTrait
	default:
		return nil
	}
	return captureAll(re, content, 5)
}

// captureAll mirrors Rust's captures_iter(content).filter_map(group1).take(n):
// it scans the whole content (multiline) and collects group-1 captures.
func captureAll(re *regexp.Regexp, content string, n int) []string {
	matches := re.FindAllStringSubmatch(content, -1)
	var out []string
	for _, m := range matches {
		if len(m) > 1 && m[1] != "" {
			out = append(out, m[1])
			if len(out) >= n {
				break
			}
		}
	}
	return out
}

func detectPatterns(content string, lang core.Language) []string {
	var patterns []string

	if strings.Contains(content, "async") && strings.Contains(content, "await") {
		patterns = append(patterns, "async")
	}

	switch lang {
	case core.LangRust:
		if strings.Contains(content, "impl") && strings.Contains(content, "for") {
			patterns = append(patterns, "trait impl")
		}
		if strings.Contains(content, "#[derive") {
			patterns = append(patterns, "derive")
		}
		if strings.Contains(content, "Result<") || strings.Contains(content, "anyhow::") {
			patterns = append(patterns, "error handling")
		}
		if strings.Contains(content, "#[test]") {
			patterns = append(patterns, "tests")
		}
		if strings.Contains(content, "Box<dyn") || strings.Contains(content, "&dyn") {
			patterns = append(patterns, "dyn dispatch")
		}
	case core.LangPython:
		if strings.Contains(content, "@dataclass") {
			patterns = append(patterns, "dataclass")
		}
		if strings.Contains(content, "def __init__") {
			patterns = append(patterns, "OOP")
		}
	case core.LangJavaScript, core.LangTypeScript:
		if strings.Contains(content, "useState") || strings.Contains(content, "useEffect") {
			patterns = append(patterns, "React hooks")
		}
		if strings.Contains(content, "export default") {
			patterns = append(patterns, "ES modules")
		}
	}

	return takeN(patterns, 3)
}

// firstNonEmptyGroup returns the first non-empty capture group after the full
// match, mirroring Rust's caps.get(1).or(caps.get(2)). Go puts unmatched
// alternation groups as "" rather than absent, so we scan for the first
// non-empty one (Rust .get returns None for an unparticipated group, which the
// .or chain skips — equivalent).
func firstNonEmptyGroup(caps []string) string {
	for i := 1; i < len(caps); i++ {
		if caps[i] != "" {
			return caps[i]
		}
	}
	return ""
}

func takeN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
