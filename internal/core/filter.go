package core

import (
	"fmt"
	"regexp"
	"strings"
)

// FilterLevel controls how aggressively source content is stripped.
type FilterLevel int

const (
	// FilterNone keeps content verbatim.
	FilterNone FilterLevel = iota
	// FilterMinimal strips comments and collapses blank lines.
	FilterMinimal
	// FilterAggressive additionally drops implementation bodies, keeping
	// signatures, imports and declarations.
	FilterAggressive
)

// ParseFilterLevel parses "none"/"minimal"/"aggressive" (case-insensitive).
func ParseFilterLevel(s string) (FilterLevel, error) {
	switch strings.ToLower(s) {
	case "none":
		return FilterNone, nil
	case "minimal":
		return FilterMinimal, nil
	case "aggressive":
		return FilterAggressive, nil
	default:
		return FilterNone, fmt.Errorf("unknown filter level: %s", s)
	}
}

func (l FilterLevel) String() string {
	switch l {
	case FilterMinimal:
		return "minimal"
	case FilterAggressive:
		return "aggressive"
	default:
		return "none"
	}
}

// Language identifies a source language for comment-pattern selection.
type Language int

const (
	LangUnknown Language = iota
	LangRust
	LangPython
	LangJavaScript
	LangTypeScript
	LangGo
	LangC
	LangCpp
	LangJava
	LangRuby
	LangShell
	// LangData covers JSON/YAML/TOML/XML/CSV/etc. — never comment-stripped.
	LangData
)

// LanguageFromExt maps a file extension (without the dot) to a Language.
func LanguageFromExt(ext string) Language {
	switch strings.ToLower(ext) {
	case "rs":
		return LangRust
	case "py", "pyw":
		return LangPython
	case "js", "mjs", "cjs":
		return LangJavaScript
	case "ts", "tsx":
		return LangTypeScript
	case "go":
		return LangGo
	case "c", "h":
		return LangC
	case "cpp", "cc", "cxx", "hpp", "hh":
		return LangCpp
	case "java":
		return LangJava
	case "rb":
		return LangRuby
	case "sh", "bash", "zsh":
		return LangShell
	case "json", "jsonc", "json5", "yaml", "yml", "toml", "xml", "csv", "tsv",
		"graphql", "gql", "sql", "md", "markdown", "txt", "env", "lock":
		return LangData
	default:
		return LangUnknown
	}
}

type commentPatterns struct {
	line          string
	blockStart    string
	blockEnd      string
	docLine       string
	docBlockStart string
}

func (l Language) commentPatterns() commentPatterns {
	switch l {
	case LangRust:
		return commentPatterns{line: "//", blockStart: "/*", blockEnd: "*/", docLine: "///", docBlockStart: "/**"}
	case LangPython:
		return commentPatterns{line: "#", blockStart: `"""`, blockEnd: `"""`, docBlockStart: `"""`}
	case LangJavaScript, LangTypeScript, LangGo, LangC, LangCpp, LangJava:
		return commentPatterns{line: "//", blockStart: "/*", blockEnd: "*/", docBlockStart: "/**"}
	case LangRuby:
		return commentPatterns{line: "#", blockStart: "=begin", blockEnd: "=end"}
	case LangShell:
		return commentPatterns{line: "#"}
	case LangData:
		return commentPatterns{}
	default:
		return commentPatterns{line: "//", blockStart: "/*", blockEnd: "*/"}
	}
}

var (
	multipleBlankLines = regexp.MustCompile(`\n{3,}`)
	importPattern      = regexp.MustCompile(`^(use |import |from |require\(|#include)`)
	funcSignature      = regexp.MustCompile(`^(pub\s+)?(async\s+)?(fn|def|function|func|class|struct|enum|trait|interface|type)\s+\w+`)
)

// FilterSource applies the comment/whitespace filter at the given level.
func FilterSource(content string, lang Language, level FilterLevel) string {
	switch level {
	case FilterMinimal:
		return minimalFilter(content, lang)
	case FilterAggressive:
		return aggressiveFilter(content, lang)
	default:
		return content
	}
}

func minimalFilter(content string, lang Language) string {
	p := lang.commentPatterns()
	var b strings.Builder
	b.Grow(len(content))
	inBlockComment := false
	inDocstring := false

	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)

		if p.blockStart != "" && p.blockEnd != "" {
			docStart := p.docBlockStart
			if docStart == "" {
				docStart = "###"
			}
			if !inDocstring && strings.Contains(trimmed, p.blockStart) && !strings.HasPrefix(trimmed, docStart) {
				inBlockComment = true
			}
			if inBlockComment {
				if strings.Contains(trimmed, p.blockEnd) {
					inBlockComment = false
				}
				continue
			}
		}

		// Python docstrings are kept in minimal mode.
		if lang == LangPython && strings.HasPrefix(trimmed, `"""`) {
			inDocstring = !inDocstring
			b.WriteString(line)
			b.WriteByte('\n')
			continue
		}
		if inDocstring {
			b.WriteString(line)
			b.WriteByte('\n')
			continue
		}

		if p.line != "" && strings.HasPrefix(trimmed, p.line) {
			// Keep doc comments.
			if p.docLine != "" && strings.HasPrefix(trimmed, p.docLine) {
				b.WriteString(line)
				b.WriteByte('\n')
			}
			continue
		}

		if trimmed == "" {
			b.WriteByte('\n')
			continue
		}

		b.WriteString(line)
		b.WriteByte('\n')
	}

	result := multipleBlankLines.ReplaceAllString(b.String(), "\n\n")
	return strings.TrimSpace(result)
}

func aggressiveFilter(content string, lang Language) string {
	if lang == LangData {
		return minimalFilter(content, lang)
	}

	minimal := minimalFilter(content, lang)
	var b strings.Builder
	b.Grow(len(minimal) / 2)
	braceDepth := 0
	inImplBody := false

	for _, line := range strings.Split(minimal, "\n") {
		trimmed := strings.TrimSpace(line)

		if importPattern.MatchString(trimmed) {
			b.WriteString(line)
			b.WriteByte('\n')
			continue
		}

		if funcSignature.MatchString(trimmed) {
			b.WriteString(line)
			b.WriteByte('\n')
			inImplBody = true
			braceDepth = 0
			continue
		}

		openBraces := strings.Count(trimmed, "{")
		closeBraces := strings.Count(trimmed, "}")

		if inImplBody {
			braceDepth += openBraces
			braceDepth -= closeBraces

			if braceDepth <= 1 && (trimmed == "{" || trimmed == "}" || strings.HasSuffix(trimmed, "{")) {
				b.WriteString(line)
				b.WriteByte('\n')
			}

			if braceDepth <= 0 {
				inImplBody = false
				if trimmed != "" && trimmed != "}" {
					b.WriteString("    // ... implementation\n")
				}
			}
			continue
		}

		if strings.HasPrefix(trimmed, "const ") ||
			strings.HasPrefix(trimmed, "static ") ||
			strings.HasPrefix(trimmed, "let ") ||
			strings.HasPrefix(trimmed, "pub const ") ||
			strings.HasPrefix(trimmed, "pub static ") {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	return strings.TrimSpace(b.String())
}

// SmartTruncate keeps structurally important lines (signatures, imports,
// braces) plus the first half of the window, then appends a single
// "[N more lines]" marker. Mirrors rtk's smart_truncate.
func SmartTruncate(content string, maxLines int) string {
	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return content
	}

	result := make([]string, 0, maxLines+1)
	kept := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		important := funcSignature.MatchString(trimmed) ||
			importPattern.MatchString(trimmed) ||
			strings.HasPrefix(trimmed, "pub ") ||
			strings.HasPrefix(trimmed, "export ") ||
			trimmed == "}" || trimmed == "{"

		if important || kept < maxLines/2 {
			result = append(result, line)
			kept++
		}

		if kept >= maxLines-1 {
			break
		}
	}

	result = append(result, fmt.Sprintf("[%d more lines]", len(lines)-kept))
	return strings.Join(result, "\n")
}
