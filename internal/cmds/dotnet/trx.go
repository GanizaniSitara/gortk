// trx.go parses .trx test-result files (Visual Studio XML) into compact
// TestSummary values. Faithful port of rtk's src/cmds/dotnet/dotnet_trx.rs.
//
// rtk used quick_xml's event reader plus chrono for RFC3339 timestamps; gortk
// uses the stdlib encoding/xml token stream and time.Parse(time.RFC3339Nano).
package dotnet

import (
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// localName returns the local part of a possibly-namespaced XML name. (Go's
// xml.Decoder already separates namespace from local name, so this is mostly a
// passthrough; kept to mirror the Rust helper.)
func localName(name xml.Name) string { return name.Local }

func parseTRXDuration(start, finish string) (string, bool) {
	startDt, err := time.Parse(time.RFC3339Nano, start)
	if err != nil {
		return "", false
	}
	finishDt, err := time.Parse(time.RFC3339Nano, finish)
	if err != nil {
		return "", false
	}
	return formatDurationBetween(startDt, finishDt)
}

func formatDurationBetween(startDt, finishDt time.Time) (string, bool) {
	millis := finishDt.Sub(startDt).Milliseconds()
	if millis <= 0 {
		return "", false
	}
	if millis >= 1000 {
		seconds := float64(millis) / 1000.0
		return fmt.Sprintf("%.1f s", seconds), true
	}
	return fmt.Sprintf("%d ms", millis), true
}

func parseTRXTimeBounds(content string) (time.Time, time.Time, bool) {
	dec := xml.NewDecoder(strings.NewReader(content))
	for {
		tok, err := dec.Token()
		if err != nil {
			return time.Time{}, time.Time{}, false
		}
		var se *xml.StartElement
		switch t := tok.(type) {
		case xml.StartElement:
			se = &t
		default:
			continue
		}
		if localName(se.Name) != "Times" {
			continue
		}
		start, okS := attrValue(se, "start")
		finish, okF := attrValue(se, "finish")
		if !okS || !okF {
			return time.Time{}, time.Time{}, false
		}
		startDt, err := time.Parse(time.RFC3339Nano, start)
		if err != nil {
			return time.Time{}, time.Time{}, false
		}
		finishDt, err := time.Parse(time.RFC3339Nano, finish)
		if err != nil {
			return time.Time{}, time.Time{}, false
		}
		return startDt, finishDt, true
	}
}

// parseTRXFile reads and parses a single .trx file.
func parseTRXFile(path string) (TestSummary, bool) {
	content, err := os.ReadFile(path)
	if err != nil {
		return TestSummary{}, false
	}
	return parseTRXContent(string(content))
}

func parseTRXFileSince(path string, since time.Time) (TestSummary, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return TestSummary{}, false
	}
	if info.ModTime().Before(since) {
		return TestSummary{}, false
	}
	return parseTRXFile(path)
}

func parseTRXFilesInDir(dir string) (TestSummary, bool) {
	return parseTRXFilesInDirSince(dir, time.Time{}, false)
}

// parseTRXFilesInDirSince merges every .trx in dir (optionally only those
// modified at/after since) into one summary, computing wall-clock duration from
// the earliest start to the latest finish.
func parseTRXFilesInDirSince(dir string, since time.Time, hasSince bool) (TestSummary, bool) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return TestSummary{}, false
	}

	var summaries []TestSummary
	var minStart, maxFinish time.Time
	haveStart, haveFinish := false, false

	entries, err := os.ReadDir(dir)
	if err != nil {
		return TestSummary{}, false
	}
	for _, entry := range entries {
		name := entry.Name()
		if !strings.EqualFold(filepath.Ext(name), ".trx") {
			continue
		}
		path := filepath.Join(dir, name)

		if hasSince {
			fi, err := entry.Info()
			if err != nil {
				continue
			}
			if fi.ModTime().Before(since) {
				continue
			}
		}

		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)

		if start, finish, ok := parseTRXTimeBounds(content); ok {
			if !haveStart || start.Before(minStart) {
				minStart = start
				haveStart = true
			}
			if !haveFinish || finish.After(maxFinish) {
				maxFinish = finish
				haveFinish = true
			}
		}

		if summary, ok := parseTRXContent(content); ok {
			summaries = append(summaries, summary)
		}
	}

	if len(summaries) == 0 {
		return TestSummary{}, false
	}

	var merged TestSummary
	for _, s := range summaries {
		merged.Passed += s.Passed
		merged.Failed += s.Failed
		merged.Skipped += s.Skipped
		merged.Total += s.Total
		merged.FailedTests = append(merged.FailedTests, s.FailedTests...)
		merged.ProjectCount += maxInt(s.ProjectCount, 1)
		if !merged.HasDuration {
			merged.DurationText = s.DurationText
			merged.HasDuration = s.HasDuration
		}
	}

	if haveStart && haveFinish {
		if d, ok := formatDurationBetween(minStart, maxFinish); ok {
			merged.DurationText = d
			merged.HasDuration = true
		} else {
			merged.DurationText = ""
			merged.HasDuration = false
		}
	}

	return merged, true
}

// findRecentTRXInTestResults returns the newest .trx under ./TestResults.
func findRecentTRXInTestResults() (string, bool) {
	return findRecentTRXInDir("TestResults")
}

func findRecentTRXInDir(dir string) (string, bool) {
	if _, err := os.Stat(dir); err != nil {
		return "", false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	var bestPath string
	var bestMod time.Time
	found := false
	for _, entry := range entries {
		name := entry.Name()
		if !strings.EqualFold(filepath.Ext(name), ".trx") {
			continue
		}
		fi, err := entry.Info()
		if err != nil {
			continue
		}
		mod := fi.ModTime()
		if !found || mod.After(bestMod) {
			bestMod = mod
			bestPath = filepath.Join(dir, name)
			found = true
		}
	}
	if !found {
		return "", false
	}
	return bestPath, true
}

// captureField mirrors the Rust enum used to route text into the right buffer.
type captureField int

const (
	captureNone captureField = iota
	captureMessage
	captureStackTrace
)

// parseTRXContent parses a TRX XML document into a TestSummary. Returns
// (_, false) when the input is not a recognizable TRX (no <TestRun>).
func parseTRXContent(content string) (TestSummary, bool) {
	dec := xml.NewDecoder(strings.NewReader(content))

	var summary TestSummary
	sawTestRun := false
	inFailedResult := false
	inErrorInfo := false
	failedTestName := ""
	var messageBuf, stackBuf strings.Builder
	capture := captureNone

	for {
		tok, err := dec.Token()
		if err != nil {
			if err.Error() == "EOF" {
				break
			}
			// Any XML error mid-stream => not a valid TRX.
			return TestSummary{}, false
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch localName(t.Name) {
			case "TestRun":
				sawTestRun = true
			case "Times":
				start, okS := attrValue(&t, "start")
				finish, okF := attrValue(&t, "finish")
				if okS && okF {
					if d, ok := parseTRXDuration(start, finish); ok {
						summary.DurationText = d
						summary.HasDuration = true
					} else {
						summary.DurationText = ""
						summary.HasDuration = false
					}
				}
			case "Counters":
				summary.Total = usizeAttr(&t, "total")
				summary.Passed = usizeAttr(&t, "passed")
				summary.Failed = usizeAttr(&t, "failed")
			case "UnitTestResult":
				outcome := attrValueOr(&t, "outcome", "Unknown")
				if outcome == "Failed" {
					inFailedResult = true
					inErrorInfo = false
					capture = captureNone
					messageBuf.Reset()
					stackBuf.Reset()
					failedTestName = attrValueOr(&t, "testName", "unknown")
				}
			case "ErrorInfo":
				if inFailedResult {
					inErrorInfo = true
				}
			case "Message":
				if inFailedResult && inErrorInfo {
					capture = captureMessage
					messageBuf.Reset()
				}
			case "StackTrace":
				if inFailedResult && inErrorInfo {
					capture = captureStackTrace
					stackBuf.Reset()
				}
			}

		case xml.CharData:
			if !inFailedResult {
				continue
			}
			text := string(t)
			switch capture {
			case captureMessage:
				messageBuf.WriteString(text)
			case captureStackTrace:
				stackBuf.WriteString(text)
			}

		case xml.EndElement:
			switch localName(t.Name) {
			case "Message", "StackTrace":
				capture = captureNone
			case "ErrorInfo":
				inErrorInfo = false
			case "UnitTestResult":
				if inFailedResult {
					var details []string

					message := strings.TrimSpace(messageBuf.String())
					if message != "" {
						details = append(details, message)
					}

					stack := strings.TrimSpace(stackBuf.String())
					if stack != "" {
						stackLines := splitLines(stack)
						if len(stackLines) > 3 {
							stackLines = stackLines[:3]
						}
						if len(stackLines) > 0 {
							details = append(details, strings.Join(stackLines, "\n"))
						}
					}

					summary.FailedTests = append(summary.FailedTests, FailedTest{
						Name:    failedTestName,
						Details: details,
					})

					inFailedResult = false
					inErrorInfo = false
					capture = captureNone
					messageBuf.Reset()
					stackBuf.Reset()
				}
			}
		}
	}

	if !sawTestRun {
		return TestSummary{}, false
	}

	if summary.Total > 0 {
		summary.Skipped = saturatingSub(summary.Total, summary.Passed+summary.Failed)
	}
	if summary.Total > 0 {
		summary.ProjectCount = 1
	}

	return summary, true
}

// attrValue returns the value of the named attribute (by local name) and
// whether it was present.
func attrValue(se *xml.StartElement, key string) (string, bool) {
	for _, a := range se.Attr {
		if a.Name.Local == key {
			return a.Value, true
		}
	}
	return "", false
}

func attrValueOr(se *xml.StartElement, key, def string) string {
	if v, ok := attrValue(se, key); ok {
		return v
	}
	return def
}

func usizeAttr(se *xml.StartElement, key string) int {
	v, ok := attrValue(se, key)
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}
