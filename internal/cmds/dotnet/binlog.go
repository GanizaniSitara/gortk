// binlog.go reads MSBuild binary log (.binlog) files and extracts errors,
// warnings, and test results, plus the text-fallback parsers used when no
// binlog is available. Faithful port of rtk's src/cmds/dotnet/binlog.rs.
//
// The .binlog format is a gzip-compressed stream of length-prefixed records.
// Rust used flate2; gortk uses the stdlib compress/gzip. The structured
// event walk and the regex-driven text fallbacks mirror the Rust line-for-line.
package dotnet

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"gortk/internal/core"
)

// BinlogIssue is one error/warning extracted from a build, with optional
// source location. Mirrors rtk's BinlogIssue.
type BinlogIssue struct {
	Code    string
	File    string
	Line    uint32
	Column  uint32
	Message string
}

// BuildSummary is the compacted result of a `dotnet build`. Mirrors rtk's
// BuildSummary.
type BuildSummary struct {
	Succeeded    bool
	ProjectCount int
	Errors       []BinlogIssue
	Warnings     []BinlogIssue
	DurationText string // "" == None
	HasDuration  bool   // distinguishes None from Some("")
}

// FailedTest is one failed test name plus detail lines. Mirrors rtk's
// FailedTest.
type FailedTest struct {
	Name    string
	Details []string
}

// TestSummary is the compacted result of a `dotnet test`. Mirrors rtk's
// TestSummary.
type TestSummary struct {
	Passed       int
	Failed       int
	Skipped      int
	Total        int
	ProjectCount int
	FailedTests  []FailedTest
	DurationText string
	HasDuration  bool
}

// RestoreSummary is the compacted result of a `dotnet restore`. Mirrors rtk's
// RestoreSummary.
type RestoreSummary struct {
	RestoredProjects int
	Warnings         int
	Errors           int
	DurationText     string
	HasDuration      bool
}

// Regexes mirroring binlog.rs lazy_static block. Go's regexp (RE2) does not
// support named groups via (?P<name>) the same way as Rust's regex crate, but
// it DOES support (?P<name>...). We use SubexpIndex for the named captures.
var (
	issueRE = regexp.MustCompile(`(?m)^\s*(?P<file>[^\r\n:(]+)\((?P<line>\d+),(?P<column>\d+)\):\s*(?P<kind>error|warning)\s*(?:(?P<code>[A-Za-z]+\d+)\s*:\s*)?(?P<msg>.*)$`)

	buildSummaryRE = regexp.MustCompile(`(?mi)^\s*(?P<count>\d+)\s+(?P<kind>warning|error)\(s\)`)
	errorCountRE   = regexp.MustCompile(`(?i)\b(?P<count>\d+)\s+error\(s\)`)
	warningCountRE = regexp.MustCompile(`(?i)\b(?P<count>\d+)\s+warning\(s\)`)

	fallbackErrorLineRE   = regexp.MustCompile(`(?mi)^.+\(\d+,\d+\):\s*error(?:\s+[A-Za-z]{2,}\d{3,})?(?:\s*:.*)?$`)
	fallbackWarningLineRE = regexp.MustCompile(`(?mi)^.+\(\d+,\d+\):\s*warning(?:\s+[A-Za-z]{2,}\d{3,})?(?:\s*:.*)?$`)

	durationRE = regexp.MustCompile(`(?m)^\s*Time Elapsed\s+(?P<duration>[^\r\n]+)$`)

	testResultRE = regexp.MustCompile(`(?:Passed!|Failed!)\s*-\s*Failed:\s*(?P<failed>\d+),\s*Passed:\s*(?P<passed>\d+),\s*Skipped:\s*(?P<skipped>\d+),\s*Total:\s*(?P<total>\d+),\s*Duration:\s*(?P<duration>[^\r\n-]+)`)

	testSummaryRE = regexp.MustCompile(`(?mi)^\s*Test summary:\s*total:\s*(?P<total>\d+),\s*failed:\s*(?P<failed>\d+),\s*(?:succeeded|passed):\s*(?P<passed>\d+),\s*skipped:\s*(?P<skipped>\d+),\s*duration:\s*(?P<duration>[^\r\n]+)$`)

	failedTestHeadRE = regexp.MustCompile(`(?m)^\s*Failed\s+(?P<name>[^\r\n\[]+)\s+\[[^\]\r\n]+\]\s*$`)

	restoreProjectRE = regexp.MustCompile(`(?m)^\s*Restored\s+.+\.csproj\s*\(`)

	restoreDiagnosticRE = regexp.MustCompile(`(?mi)^\s*(?:(?P<file>.+?)\s+:\s+)?(?P<kind>warning|error)\s+(?P<code>[A-Za-z]{2,}\d{3,})\s*:\s*(?P<msg>.+)$`)

	projectPathRE = regexp.MustCompile(`(?m)^\s*([A-Za-z]:)?[^\r\n]*\.csproj(?:\s|$)`)

	printableRunRE = regexp.MustCompile(`[\x20-\x7E]{5,}`)

	diagnosticCodeRE = regexp.MustCompile(`^[A-Za-z]{2,}\d{3,}$`)

	sourceFileRE = regexp.MustCompile(`(?i)([A-Za-z]:)?[/\\][^\s]+\.(cs|vb|fs)`)

	sensitiveEnvRE *regexp.Regexp
)

// sensitiveEnvVars is the list of environment-variable names whose values are
// scrubbed from any captured build output before display. Mirrors rtk's
// SENSITIVE_ENV_VARS.
var sensitiveEnvVars = []string{
	"PATH", "HOME", "USERPROFILE", "USERNAME", "USER", "APPDATA", "LOCALAPPDATA",
	"TEMP", "TMP", "SSH_AUTH_SOCK", "SSH_AGENT_LAUNCHER", "GH_TOKEN",
	"GITHUB_TOKEN", "GITHUB_PAT", "NUGET_API_KEY", "NUGET_AUTH_TOKEN",
	"VSS_NUGET_EXTERNAL_FEED_ENDPOINTS", "AZURE_DEVOPS_TOKEN", "AZURE_CLIENT_SECRET",
	"AZURE_TENANT_ID", "AZURE_CLIENT_ID", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
	"AWS_SESSION_TOKEN", "API_TOKEN", "AUTH_TOKEN", "ACCESS_TOKEN", "BEARER_TOKEN",
	"PASSWORD", "CONNECTION_STRING", "DATABASE_URL", "DOCKER_CONFIG", "KUBECONFIG",
}

func init() {
	keys := make([]string, len(sensitiveEnvVars))
	for i, k := range sensitiveEnvVars {
		keys[i] = regexp.QuoteMeta(k)
	}
	sensitiveEnvRE = regexp.MustCompile(
		`(?P<prefix>\b(?:` + strings.Join(keys, "|") + `)\s*(?:=|:)\s*)(?P<value>[^\s;]+)`)
}

// MSBuild binlog record kinds.
const (
	recordEndOfFile             = 0
	recordBuildStarted          = 1
	recordBuildFinished         = 2
	recordProjectStarted        = 3
	recordProjectFinished       = 4
	recordError                 = 9
	recordWarning               = 10
	recordMessage               = 11
	recordCriticalBuildMessage  = 13
	recordProjectImportArchive  = 17
	recordNameValueList         = 23
	recordString                = 24
	stringRecordStartIndex      = 10
)

// MSBuild event field flag bits.
const (
	flagBuildEventContext = 1 << 0
	flagMessage           = 1 << 2
	flagTimestamp         = 1 << 5
	flagArguments         = 1 << 14
	flagImportance        = 1 << 15
	flagExtended          = 1 << 16
)

// ---------------------------------------------------------------------------
// Public binlog parsers
// ---------------------------------------------------------------------------

// ParseBuild reads a build .binlog and returns the structured summary, merging
// structured events with a text fallback over the string-record blob.
func ParseBuild(binlogPath string) (BuildSummary, error) {
	parsed, err := parseEventsFromBinlog(binlogPath)
	if err != nil {
		return BuildSummary{}, fmt.Errorf("Failed to parse binlog at %s: %w", binlogPath, err)
	}
	stringsBlob := strings.Join(parsed.stringRecords, "\n")
	textFallback := ParseBuildFromText(stringsBlob)

	summary := BuildSummary{}
	if parsed.buildSucceeded != nil {
		summary.Succeeded = *parsed.buildSucceeded
	}

	if parsed.buildStartedTicks != nil && parsed.buildFinishedTicks != nil &&
		*parsed.buildFinishedTicks >= *parsed.buildStartedTicks {
		summary.DurationText = formatTicksDuration(*parsed.buildFinishedTicks - *parsed.buildStartedTicks)
		summary.HasDuration = true
	}

	parsedProjectCount := len(parsed.projectFiles)
	if parsedProjectCount > 0 {
		summary.ProjectCount = parsedProjectCount
	} else {
		summary.ProjectCount = textFallback.ProjectCount
	}

	summary.Errors = selectBestIssues(parsed.errors, textFallback.Errors)
	summary.Warnings = selectBestIssues(parsed.warnings, textFallback.Warnings)
	return summary, nil
}

// ParseTest reads a test .binlog and returns the test summary derived from the
// string-record blob, overlaying the structured project count.
func ParseTest(binlogPath string) (TestSummary, error) {
	parsed, err := parseEventsFromBinlog(binlogPath)
	if err != nil {
		return TestSummary{}, fmt.Errorf("Failed to parse binlog at %s: %w", binlogPath, err)
	}
	blob := strings.Join(parsed.stringRecords, "\n")
	summary := ParseTestFromText(blob)
	if pc := len(parsed.projectFiles); pc > 0 {
		summary.ProjectCount = pc
	}
	return summary, nil
}

// ParseRestore reads a restore .binlog and returns the restore summary derived
// from the string-record blob, overlaying the structured project count.
func ParseRestore(binlogPath string) (RestoreSummary, error) {
	parsed, err := parseEventsFromBinlog(binlogPath)
	if err != nil {
		return RestoreSummary{}, fmt.Errorf("Failed to parse binlog at %s: %w", binlogPath, err)
	}
	blob := strings.Join(parsed.stringRecords, "\n")
	summary := ParseRestoreFromText(blob)
	if pc := len(parsed.projectFiles); pc > 0 {
		summary.RestoredProjects = pc
	}
	return summary, nil
}

// ---------------------------------------------------------------------------
// Issue selection (structured vs text fallback)
// ---------------------------------------------------------------------------

func selectBestIssues(primary, fallback []BinlogIssue) []BinlogIssue {
	if len(primary) == 0 {
		return fallback
	}
	if len(fallback) == 0 {
		return primary
	}
	allSuspicious := true
	for i := range primary {
		if !isSuspiciousIssue(primary[i]) {
			allSuspicious = false
			break
		}
	}
	anyContextual := false
	for i := range fallback {
		if isContextualIssue(fallback[i]) {
			anyContextual = true
			break
		}
	}
	if allSuspicious && anyContextual {
		return fallback
	}
	if issuesQualityScore(fallback) > issuesQualityScore(primary) {
		return fallback
	}
	return primary
}

func issuesQualityScore(issues []BinlogIssue) int {
	sum := 0
	for i := range issues {
		sum += issueQualityScore(issues[i])
	}
	return sum
}

func issueQualityScore(issue BinlogIssue) int {
	score := 0
	if isContextualIssue(issue) {
		score += 4
	}
	if issue.Code != "" && isLikelyDiagnosticCode(issue.Code) {
		score += 2
	}
	if issue.Line > 0 {
		score++
	}
	if issue.Column > 0 {
		score++
	}
	if issue.Message != "" && issue.Message != "Build issue" {
		score++
	}
	return score
}

func isContextualIssue(issue BinlogIssue) bool {
	return issue.File != "" && !isLikelyDiagnosticCode(issue.File)
}

func isSuspiciousIssue(issue BinlogIssue) bool {
	return issue.Code == "" && isLikelyDiagnosticCode(issue.File)
}

// ---------------------------------------------------------------------------
// Structured binlog walk
// ---------------------------------------------------------------------------

type parsedBinlog struct {
	stringRecords      []string
	messages           []string
	projectFiles       map[string]struct{}
	errors             []BinlogIssue
	warnings           []BinlogIssue
	buildSucceeded     *bool
	buildStartedTicks  *int64
	buildFinishedTicks *int64
}

type parsedEventFields struct {
	message        *string
	timestampTicks *int64
}

func parseEventsFromBinlog(path string) (*parsedBinlog, error) {
	bytesData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to read binlog at %s: %w", path, err)
	}
	if len(bytesData) == 0 {
		return nil, fmt.Errorf("empty file")
	}

	gz, err := gzip.NewReader(bytes.NewReader(bytesData))
	if err != nil {
		return nil, fmt.Errorf("gzip decode failed: %w", err)
	}
	defer gz.Close()
	payload, err := io.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("gzip decode failed: %w", err)
	}

	reader := newBinReader(payload)
	fileFormatVersion, err := reader.readI32LE()
	if err != nil {
		return nil, fmt.Errorf("binlog header missing file format version")
	}
	if _, err := reader.readI32LE(); err != nil {
		return nil, fmt.Errorf("binlog header missing minimum reader version")
	}
	if fileFormatVersion < 18 {
		return nil, fmt.Errorf("unsupported binlog format %d", fileFormatVersion)
	}

	parsed := &parsedBinlog{projectFiles: map[string]struct{}{}}

	for !reader.isEOF() {
		kind, err := reader.read7bitI32()
		if err != nil {
			return nil, fmt.Errorf("failed to read record kind")
		}
		if kind == recordEndOfFile {
			break
		}

		switch kind {
		case recordString:
			text, err := reader.readDotnetString()
			if err != nil {
				return nil, fmt.Errorf("failed to read string record")
			}
			parsed.stringRecords = append(parsed.stringRecords, text)
		case recordNameValueList, recordProjectImportArchive:
			n, err := reader.read7bitI32()
			if err != nil {
				return nil, fmt.Errorf("failed to read record length")
			}
			if n < 0 {
				return nil, fmt.Errorf("negative record length: %d", n)
			}
			if err := reader.skip(int(n)); err != nil {
				return nil, fmt.Errorf("failed to skip auxiliary record payload")
			}
		default:
			n, err := reader.read7bitI32()
			if err != nil {
				return nil, fmt.Errorf("failed to read event length")
			}
			if n < 0 {
				return nil, fmt.Errorf("negative event length: %d", n)
			}
			eventPayload, err := reader.readExact(int(n))
			if err != nil {
				return nil, fmt.Errorf("failed to read event payload")
			}
			er := newBinReader(eventPayload)
			// Best-effort: a malformed event record is skipped, matching rtk's
			// `let _ = parse_event_record(...)`.
			_ = parseEventRecord(kind, er, fileFormatVersion, parsed)
		}
	}

	return parsed, nil
}

func parseEventRecord(kind int, reader *binReader, fileFormatVersion int, parsed *parsedBinlog) error {
	switch kind {
	case recordBuildStarted:
		fields, err := readEventFields(reader, fileFormatVersion, parsed, false)
		if err != nil {
			return err
		}
		parsed.buildStartedTicks = fields.timestampTicks
	case recordBuildFinished:
		fields, err := readEventFields(reader, fileFormatVersion, parsed, false)
		if err != nil {
			return err
		}
		parsed.buildFinishedTicks = fields.timestampTicks
		b, err := reader.readBool()
		if err != nil {
			return err
		}
		parsed.buildSucceeded = &b
	case recordProjectStarted:
		if _, err := readEventFields(reader, fileFormatVersion, parsed, false); err != nil {
			return err
		}
		hasContext, err := reader.readBool()
		if err != nil {
			return err
		}
		if hasContext {
			if err := skipBuildEventContext(reader, fileFormatVersion); err != nil {
				return err
			}
		}
		projectFile, err := readOptionalString(reader, parsed)
		if err != nil {
			return err
		}
		if projectFile != nil && *projectFile != "" {
			parsed.projectFiles[*projectFile] = struct{}{}
		}
	case recordProjectFinished:
		if _, err := readEventFields(reader, fileFormatVersion, parsed, false); err != nil {
			return err
		}
		projectFile, err := readOptionalString(reader, parsed)
		if err != nil {
			return err
		}
		if projectFile != nil && *projectFile != "" {
			parsed.projectFiles[*projectFile] = struct{}{}
		}
		if _, err := reader.readBool(); err != nil {
			return err
		}
	case recordError, recordWarning:
		fields, err := readEventFields(reader, fileFormatVersion, parsed, false)
		if err != nil {
			return err
		}
		if _, err := readOptionalString(reader, parsed); err != nil { // subcategory
			return err
		}
		code, err := readOptionalString(reader, parsed)
		if err != nil {
			return err
		}
		file, err := readOptionalString(reader, parsed)
		if err != nil {
			return err
		}
		if _, err := readOptionalString(reader, parsed); err != nil { // project file
			return err
		}
		lineV, err := reader.read7bitI32()
		if err != nil {
			return err
		}
		colV, err := reader.read7bitI32()
		if err != nil {
			return err
		}
		if _, err := reader.read7bitI32(); err != nil {
			return err
		}
		if _, err := reader.read7bitI32(); err != nil {
			return err
		}

		issue := BinlogIssue{
			Code:    deref(code),
			File:    deref(file),
			Line:    uint32(max32(lineV, 0)),
			Column:  uint32(max32(colV, 0)),
			Message: deref(fields.message),
		}
		if kind == recordError {
			parsed.errors = append(parsed.errors, issue)
		} else {
			parsed.warnings = append(parsed.warnings, issue)
		}
	case recordMessage:
		fields, err := readEventFields(reader, fileFormatVersion, parsed, true)
		if err != nil {
			return err
		}
		if fields.message != nil {
			parsed.messages = append(parsed.messages, *fields.message)
		}
	case recordCriticalBuildMessage:
		fields, err := readEventFields(reader, fileFormatVersion, parsed, false)
		if err != nil {
			return err
		}
		if fields.message != nil {
			parsed.messages = append(parsed.messages, *fields.message)
		}
	}
	return nil
}

func readEventFields(reader *binReader, fileFormatVersion int, parsed *parsedBinlog, readImportance bool) (parsedEventFields, error) {
	flags, err := reader.read7bitI32()
	if err != nil {
		return parsedEventFields{}, err
	}
	var result parsedEventFields

	if flags&flagMessage != 0 {
		s, err := readDeduplicatedString(reader, parsed)
		if err != nil {
			return parsedEventFields{}, err
		}
		result.message = s
	}
	if flags&flagBuildEventContext != 0 {
		if err := skipBuildEventContext(reader, fileFormatVersion); err != nil {
			return parsedEventFields{}, err
		}
	}
	if flags&flagTimestamp != 0 {
		ticks, err := reader.readI64LE()
		if err != nil {
			return parsedEventFields{}, err
		}
		result.timestampTicks = &ticks
		if _, err := reader.read7bitI32(); err != nil {
			return parsedEventFields{}, err
		}
	}
	if flags&flagExtended != 0 {
		if _, err := readOptionalString(reader, parsed); err != nil {
			return parsedEventFields{}, err
		}
		if err := skipStringDictionary(reader, fileFormatVersion); err != nil {
			return parsedEventFields{}, err
		}
		if _, err := readOptionalString(reader, parsed); err != nil {
			return parsedEventFields{}, err
		}
	}
	if flags&flagArguments != 0 {
		count, err := reader.read7bitI32()
		if err != nil {
			return parsedEventFields{}, err
		}
		c := max32(count, 0)
		for i := 0; i < int(c); i++ {
			if _, err := readDeduplicatedString(reader, parsed); err != nil {
				return parsedEventFields{}, err
			}
		}
	}
	if (fileFormatVersion < 13 && readImportance) || (flags&flagImportance != 0) {
		if _, err := reader.read7bitI32(); err != nil {
			return parsedEventFields{}, err
		}
	}
	return result, nil
}

func skipBuildEventContext(reader *binReader, fileFormatVersion int) error {
	count := 6
	if fileFormatVersion > 1 {
		count = 7
	}
	for i := 0; i < count; i++ {
		if _, err := reader.read7bitI32(); err != nil {
			return err
		}
	}
	return nil
}

func skipStringDictionary(reader *binReader, fileFormatVersion int) error {
	if fileFormatVersion < 10 {
		return fmt.Errorf("legacy dictionary format is unsupported")
	}
	_, err := reader.read7bitI32()
	return err
}

func readOptionalString(reader *binReader, parsed *parsedBinlog) (*string, error) {
	return readDeduplicatedString(reader, parsed)
}

func readDeduplicatedString(reader *binReader, parsed *parsedBinlog) (*string, error) {
	index, err := reader.read7bitI32()
	if err != nil {
		return nil, err
	}
	if index == 0 {
		return nil, nil
	}
	if index == 1 {
		empty := ""
		return &empty, nil
	}
	if index < stringRecordStartIndex {
		return nil, nil
	}
	recordIdx := int(index - stringRecordStartIndex)
	if recordIdx < 0 || recordIdx >= len(parsed.stringRecords) {
		return nil, fmt.Errorf("invalid string record index %d", index)
	}
	s := parsed.stringRecords[recordIdx]
	return &s, nil
}

func formatTicksDuration(ticks int64) string {
	totalSeconds := divEuclid(ticks, 10_000_000)
	centiseconds := remEuclid(ticks, 10_000_000) / 100_000
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	seconds := totalSeconds % 60
	return fmt.Sprintf("%02d:%02d:%02d.%02d", hours, minutes, seconds, centiseconds)
}

// ---------------------------------------------------------------------------
// Binary reader for the binlog stream
// ---------------------------------------------------------------------------

type binReader struct {
	data []byte
	pos  int
}

func newBinReader(b []byte) *binReader { return &binReader{data: b} }

func (r *binReader) isEOF() bool { return r.pos >= len(r.data) }

func (r *binReader) readExact(n int) ([]byte, error) {
	end := r.pos + n
	if n < 0 || end > len(r.data) {
		return nil, fmt.Errorf("unexpected end of stream")
	}
	out := r.data[r.pos:end]
	r.pos = end
	return out, nil
}

func (r *binReader) skip(n int) error {
	_, err := r.readExact(n)
	return err
}

func (r *binReader) readU8() (byte, error) {
	b, err := r.readExact(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

func (r *binReader) readBool() (bool, error) {
	b, err := r.readU8()
	return b != 0, err
}

func (r *binReader) readI32LE() (int, error) {
	b, err := r.readExact(4)
	if err != nil {
		return 0, err
	}
	return int(int32(uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24)), nil
}

func (r *binReader) readI64LE() (int64, error) {
	b, err := r.readExact(8)
	if err != nil {
		return 0, err
	}
	var v uint64
	for i := 0; i < 8; i++ {
		v |= uint64(b[i]) << (8 * i)
	}
	return int64(v), nil
}

func (r *binReader) read7bitI32() (int, error) {
	var value uint32
	var shift uint
	for {
		b, err := r.readU8()
		if err != nil {
			return 0, err
		}
		value |= uint32(b&0x7F) << shift
		if b&0x80 == 0 {
			return int(int32(value)), nil
		}
		shift += 7
		if shift >= 35 {
			return 0, fmt.Errorf("invalid 7-bit encoded integer")
		}
	}
}

func (r *binReader) readDotnetString() (string, error) {
	n, err := r.read7bitI32()
	if err != nil {
		return "", err
	}
	if n < 0 {
		return "", fmt.Errorf("negative string length: %d", n)
	}
	b, err := r.readExact(n)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ---------------------------------------------------------------------------
// Env scrubbing
// ---------------------------------------------------------------------------

// scrubSensitiveEnvVars masks the values of known-sensitive environment
// variables in captured build output. Mirrors rtk's scrub_sensitive_env_vars.
func scrubSensitiveEnvVars(input string) string {
	return sensitiveEnvRE.ReplaceAllStringFunc(input, func(m string) string {
		groups := sensitiveEnvRE.FindStringSubmatch(m)
		prefixIdx := sensitiveEnvRE.SubexpIndex("prefix")
		return groups[prefixIdx] + "[REDACTED]"
	})
}

// ---------------------------------------------------------------------------
// Text-fallback parsers
// ---------------------------------------------------------------------------

// ParseBuildFromText extracts a BuildSummary from raw build text (console or
// string-record blob). Faithful port of rtk's parse_build_from_text.
func ParseBuildFromText(text string) BuildSummary {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	clean := core.StripANSI(text)
	scrubbed := scrubSensitiveEnvVars(clean)

	seenErrors := map[string]struct{}{}
	seenWarnings := map[string]struct{}{}

	summary := BuildSummary{
		Succeeded:    strings.Contains(scrubbed, "Build succeeded") && !strings.Contains(scrubbed, "Build FAILED"),
		ProjectCount: countProjects(scrubbed),
	}
	if d, ok := extractDuration(scrubbed); ok {
		summary.DurationText = d
		summary.HasDuration = true
	}

	for _, m := range issueRE.FindAllStringSubmatch(scrubbed, -1) {
		code := namedGroup(issueRE, m, "code")
		file := namedGroup(issueRE, m, "file")
		line := parseU32(namedGroup(issueRE, m, "line"))
		column := parseU32(namedGroup(issueRE, m, "column"))
		msg := strings.TrimSpace(namedGroup(issueRE, m, "msg"))
		if msg == "" {
			msg = "diagnostic without message"
		}
		issue := BinlogIssue{Code: code, File: file, Line: line, Column: column, Message: msg}
		key := issueKey(issue)
		switch namedGroup(issueRE, m, "kind") {
		case "error":
			if _, ok := seenErrors[key]; !ok {
				seenErrors[key] = struct{}{}
				summary.Errors = append(summary.Errors, issue)
			}
		case "warning":
			if _, ok := seenWarnings[key]; !ok {
				seenWarnings[key] = struct{}{}
				summary.Warnings = append(summary.Warnings, issue)
			}
		}
	}

	if len(summary.Errors) == 0 || len(summary.Warnings) == 0 {
		warningCountFromSummary := 0
		errorCountFromSummary := 0

		for _, m := range buildSummaryRE.FindAllStringSubmatch(scrubbed, -1) {
			count := parseInt(namedGroup(buildSummaryRE, m, "count"))
			switch strings.ToLower(namedGroup(buildSummaryRE, m, "kind")) {
			case "warning":
				warningCountFromSummary = maxInt(warningCountFromSummary, count)
			case "error":
				errorCountFromSummary = maxInt(errorCountFromSummary, count)
			}
		}

		inlineErrorCount := maxCountFrom(errorCountRE, scrubbed)
		inlineWarningCount := maxCountFrom(warningCountRE, scrubbed)

		warningCountFromSummary = maxInt(warningCountFromSummary, inlineWarningCount)
		errorCountFromSummary = maxInt(errorCountFromSummary, inlineErrorCount)

		if len(summary.Errors) == 0 {
			for idx := 0; idx < errorCountFromSummary; idx++ {
				summary.Errors = append(summary.Errors, BinlogIssue{
					Message: fmt.Sprintf("Build error #%d (details omitted)", idx+1),
				})
			}
		}
		if len(summary.Warnings) == 0 {
			for idx := 0; idx < warningCountFromSummary; idx++ {
				summary.Warnings = append(summary.Warnings, BinlogIssue{
					Message: fmt.Sprintf("Build warning #%d (details omitted)", idx+1),
				})
			}
		}

		if len(summary.Errors) == 0 {
			fallbackErrorLines := len(fallbackErrorLineRE.FindAllStringIndex(scrubbed, -1))
			for idx := 0; idx < fallbackErrorLines; idx++ {
				summary.Errors = append(summary.Errors, BinlogIssue{
					Message: fmt.Sprintf("Build error #%d (details omitted)", idx+1),
				})
			}
		}
		if len(summary.Warnings) == 0 {
			fallbackWarningLines := len(fallbackWarningLineRE.FindAllStringIndex(scrubbed, -1))
			for idx := 0; idx < fallbackWarningLines; idx++ {
				summary.Warnings = append(summary.Warnings, BinlogIssue{
					Message: fmt.Sprintf("Build warning #%d (details omitted)", idx+1),
				})
			}
		}
	}

	hasErrorSignal := strings.Contains(scrubbed, "Build FAILED") ||
		strings.Contains(scrubbed, ": error ") ||
		hasErrorCountSignal(scrubbed)

	if len(summary.Errors) == 0 || len(summary.Warnings) == 0 {
		diagErrors, diagWarnings := ParseRestoreIssuesFromText(scrubbed)
		if len(summary.Errors) == 0 {
			summary.Errors = diagErrors
		}
		if len(summary.Warnings) == 0 {
			summary.Warnings = diagWarnings
		}
	}

	if len(summary.Errors) == 0 && !summary.Succeeded && hasErrorSignal {
		summary.Errors = extractBinaryLikeIssues(scrubbed)
	}

	if summary.ProjectCount == 0 &&
		(strings.Contains(scrubbed, "Build succeeded") ||
			strings.Contains(scrubbed, "Build FAILED") ||
			strings.Contains(scrubbed, " -> ")) {
		summary.ProjectCount = 1
	}

	return summary
}

func hasErrorCountSignal(scrubbed string) bool {
	for _, m := range buildSummaryRE.FindAllStringSubmatch(scrubbed, -1) {
		isError := strings.ToLower(namedGroup(buildSummaryRE, m, "kind")) == "error"
		count := parseInt(namedGroup(buildSummaryRE, m, "count"))
		if isError && count > 0 {
			return true
		}
	}
	return false
}

// ParseTestFromText extracts a TestSummary from raw test text. Faithful port of
// rtk's parse_test_from_text.
func ParseTestFromText(text string) TestSummary {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	clean := core.StripANSI(text)
	scrubbed := scrubSensitiveEnvVars(clean)

	summary := TestSummary{
		ProjectCount: maxInt(countProjects(scrubbed), 1),
	}
	if d, ok := extractDuration(scrubbed); ok {
		summary.DurationText = d
		summary.HasDuration = true
	}

	foundSummaryLine := false
	fallbackDuration := ""
	haveFallbackDuration := false
	for _, m := range testResultRE.FindAllStringSubmatch(scrubbed, -1) {
		foundSummaryLine = true
		summary.Passed += parseInt(namedGroup(testResultRE, m, "passed"))
		summary.Failed += parseInt(namedGroup(testResultRE, m, "failed"))
		summary.Skipped += parseInt(namedGroup(testResultRE, m, "skipped"))
		summary.Total += parseInt(namedGroup(testResultRE, m, "total"))
		if d := namedGroup(testResultRE, m, "duration"); d != "" {
			fallbackDuration = strings.TrimSpace(d)
			haveFallbackDuration = true
		}
	}

	if foundSummaryLine && !summary.HasDuration && haveFallbackDuration {
		summary.DurationText = fallbackDuration
		summary.HasDuration = true
	}

	tsMatches := testSummaryRE.FindAllStringSubmatch(scrubbed, -1)
	if len(tsMatches) > 0 {
		m := tsMatches[len(tsMatches)-1]
		summary.Passed = parseIntDefault(namedGroup(testSummaryRE, m, "passed"), summary.Passed)
		summary.Failed = parseIntDefault(namedGroup(testSummaryRE, m, "failed"), summary.Failed)
		summary.Skipped = parseIntDefault(namedGroup(testSummaryRE, m, "skipped"), summary.Skipped)
		summary.Total = parseIntDefault(namedGroup(testSummaryRE, m, "total"), summary.Total)
		if d := namedGroup(testSummaryRE, m, "duration"); d != "" {
			summary.DurationText = strings.TrimSpace(d)
			summary.HasDuration = true
		}
	}

	lines := splitLines(scrubbed)
	idx := 0
	for idx < len(lines) {
		line := lines[idx]
		if hm := failedTestHeadRE.FindStringSubmatch(line); hm != nil {
			name := strings.TrimSpace(namedGroup(failedTestHeadRE, hm, "name"))
			if name == "" {
				name = "unknown"
			}
			var details []string
			idx++
			for idx < len(lines) {
				detailLine := strings.TrimRight(lines[idx], " \t")
				if failedTestHeadRE.MatchString(detailLine) {
					idx = saturatingSub(idx, 1)
					break
				}
				detailTrimmed := strings.TrimLeft(detailLine, " \t")
				if strings.HasPrefix(detailTrimmed, "Failed!  -") ||
					strings.HasPrefix(detailTrimmed, "Passed!  -") ||
					strings.HasPrefix(detailTrimmed, "Test summary:") ||
					strings.HasPrefix(detailTrimmed, "Build ") {
					idx = saturatingSub(idx, 1)
					break
				}
				if strings.TrimSpace(detailLine) == "" {
					if len(details) > 0 {
						details = append(details, "")
					}
				} else {
					details = append(details, strings.TrimSpace(detailLine))
				}
				if len(details) >= 20 {
					break
				}
				idx++
			}
			summary.FailedTests = append(summary.FailedTests, FailedTest{Name: name, Details: details})
		}
		idx++
	}

	if summary.Failed == 0 {
		summary.Failed = len(summary.FailedTests)
	}
	if summary.Total == 0 {
		summary.Total = summary.Passed + summary.Failed + summary.Skipped
	}

	return summary
}

// ParseRestoreFromText extracts a RestoreSummary from raw restore text.
// Faithful port of rtk's parse_restore_from_text.
func ParseRestoreFromText(text string) RestoreSummary {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	errors, warnings := ParseRestoreIssuesFromText(text)
	clean := core.StripANSI(text)
	scrubbed := scrubSensitiveEnvVars(clean)

	summary := RestoreSummary{
		RestoredProjects: len(restoreProjectRE.FindAllStringIndex(scrubbed, -1)),
		Warnings:         len(warnings),
		Errors:           len(errors),
	}
	if d, ok := extractDuration(scrubbed); ok {
		summary.DurationText = d
		summary.HasDuration = true
	}
	return summary
}

// ParseRestoreIssuesFromText extracts (errors, warnings) NuGet/restore
// diagnostics from raw text. Faithful port of rtk's
// parse_restore_issues_from_text.
func ParseRestoreIssuesFromText(text string) ([]BinlogIssue, []BinlogIssue) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	clean := core.StripANSI(text)
	scrubbed := scrubSensitiveEnvVars(clean)

	var errors, warnings []BinlogIssue
	seenErrors := map[string]struct{}{}
	seenWarnings := map[string]struct{}{}

	for _, m := range restoreDiagnosticRE.FindAllStringSubmatch(scrubbed, -1) {
		issue := BinlogIssue{
			Code:    strings.TrimSpace(namedGroup(restoreDiagnosticRE, m, "code")),
			File:    strings.TrimSpace(namedGroup(restoreDiagnosticRE, m, "file")),
			Message: strings.TrimSpace(namedGroup(restoreDiagnosticRE, m, "msg")),
		}
		key := issueKey(issue)
		switch strings.ToLower(namedGroup(restoreDiagnosticRE, m, "kind")) {
		case "error":
			if _, ok := seenErrors[key]; !ok {
				seenErrors[key] = struct{}{}
				errors = append(errors, issue)
			}
		case "warning":
			if _, ok := seenWarnings[key]; !ok {
				seenWarnings[key] = struct{}{}
				warnings = append(warnings, issue)
			}
		}
	}

	return errors, warnings
}

func countProjects(text string) int {
	return len(projectPathRE.FindAllStringIndex(text, -1))
}

func extractDuration(text string) (string, bool) {
	m := durationRE.FindStringSubmatch(text)
	if m == nil {
		return "", false
	}
	return strings.TrimSpace(namedGroup(durationRE, m, "duration")), true
}

func extractPrintableRuns(text string) []string {
	var runs []string
	for _, m := range printableRunRE.FindAllString(text, -1) {
		run := strings.TrimSpace(m)
		if len(run) < 5 {
			continue
		}
		runs = append(runs, run)
	}
	return runs
}

func extractBinaryLikeIssues(text string) []BinlogIssue {
	runs := extractPrintableRuns(text)
	if len(runs) == 0 {
		return nil
	}

	var issues []BinlogIssue
	seen := map[string]struct{}{}

	for idx := 0; idx < len(runs); idx++ {
		code := strings.TrimSpace(runs[idx])
		if !diagnosticCodeRE.MatchString(code) || !isLikelyDiagnosticCode(code) {
			continue
		}

		message := "Build issue"
		for delta := 1; delta <= 4; delta++ {
			j := idx - delta
			if j < 0 {
				continue
			}
			candidate := strings.TrimSpace(runs[j])
			if !diagnosticCodeRE.MatchString(candidate) &&
				!sourceFileRE.MatchString(candidate) &&
				hasASCIIAlpha(candidate) &&
				strings.Contains(candidate, " ") &&
				!strings.Contains(candidate, "Copyright") &&
				!strings.Contains(candidate, "Compiler version") {
				message = candidate
				break
			}
		}

		file := ""
		for delta := 1; delta <= 4; delta++ {
			if idx+delta >= len(runs) {
				continue
			}
			candidate := runs[idx+delta]
			if loc := sourceFileRE.FindString(candidate); loc != "" {
				file = loc
				break
			}
		}

		if file == "" && message == "Build issue" {
			continue
		}

		key := code + "\x00" + file + "\x00" + message
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		issues = append(issues, BinlogIssue{Code: code, File: file, Message: message})
	}

	return issues
}

func isLikelyDiagnosticCode(code string) bool {
	allowed := []string{
		"CS", "MSB", "NU", "FS", "BC", "CA", "SA", "IDE", "IL", "VB", "AD", "TS", "C", "LNK",
	}
	for _, prefix := range allowed {
		if strings.HasPrefix(code, prefix) {
			return true
		}
	}
	return false
}
