// format_report.go parses `dotnet format` JSON reports into compact summaries.
// Faithful port of rtk's src/cmds/dotnet/dotnet_format_report.rs.
//
// rtk used serde with #[serde(rename_all = "PascalCase")] and
// #[serde(default)] on file_changes; gortk uses encoding/json with explicit
// PascalCase field tags (a missing FileChanges array decodes to nil, which is
// the Go equivalent of the serde default).
package dotnet

import (
	"encoding/json"
	"fmt"
	"os"
)

// formatReportEntry is one entry in the dotnet format report JSON array.
type formatReportEntry struct {
	FilePath    string           `json:"FilePath"`
	FileChanges []formatFileChng `json:"FileChanges"`
}

// formatFileChng is one change record within a report entry.
type formatFileChng struct {
	LineNumber        uint32 `json:"LineNumber"`
	CharNumber        uint32 `json:"CharNumber"`
	DiagnosticID      string `json:"DiagnosticId"`
	FormatDescription string `json:"FormatDescription"`
}

// ChangeDetail is one formatting change with its location and rule. Mirrors
// rtk's ChangeDetail.
type ChangeDetail struct {
	LineNumber        uint32
	CharNumber        uint32
	DiagnosticID      string
	FormatDescription string
}

// FileWithChanges is a single file plus the changes the formatter would make.
// Mirrors rtk's FileWithChanges.
type FileWithChanges struct {
	Path    string
	Changes []ChangeDetail
}

// FormatSummary is the compacted result of a `dotnet format` run. Mirrors rtk's
// FormatSummary.
type FormatSummary struct {
	FilesWithChanges []FileWithChanges
	FilesUnchanged   int
	TotalFiles       int
}

// ParseFormatReport reads the dotnet format JSON report at path and returns a
// FormatSummary. Faithful port of rtk's parse_format_report.
func ParseFormatReport(path string) (FormatSummary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return FormatSummary{}, fmt.Errorf("Failed to read dotnet format report at %s: %w", path, err)
	}

	var entries []formatReportEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return FormatSummary{}, fmt.Errorf("Failed to parse dotnet format report JSON at %s: %w", path, err)
	}

	totalFiles := len(entries)
	var filesWithChanges []FileWithChanges
	for _, entry := range entries {
		if len(entry.FileChanges) == 0 {
			continue
		}
		changes := make([]ChangeDetail, 0, len(entry.FileChanges))
		for _, c := range entry.FileChanges {
			changes = append(changes, ChangeDetail{
				LineNumber:        c.LineNumber,
				CharNumber:        c.CharNumber,
				DiagnosticID:      c.DiagnosticID,
				FormatDescription: c.FormatDescription,
			})
		}
		filesWithChanges = append(filesWithChanges, FileWithChanges{
			Path:    entry.FilePath,
			Changes: changes,
		})
	}

	filesUnchanged := saturatingSub(totalFiles, len(filesWithChanges))

	return FormatSummary{
		FilesWithChanges: filesWithChanges,
		FilesUnchanged:   filesUnchanged,
		TotalFiles:       totalFiles,
	}, nil
}
