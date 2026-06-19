package psql

import (
	"fmt"
	"strings"
	"testing"
)

func TestSnapshotTableFormat(t *testing.T) {
	input := " id | username    | email             | status\n----+-------------+-------------------+--------\n  1 | alice_smith  | alice@example.com | active\n  2 | bob_jones   | bob@example.com   | active\n(2 rows)\n"
	result := filterTable(input)
	if !strings.Contains(result, "id\tusername\temail\tstatus") {
		t.Errorf("missing header row: %q", result)
	}
	if !strings.Contains(result, "alice_smith\talice@example.com") {
		t.Errorf("missing data row: %q", result)
	}
	if strings.Contains(result, "---+---") {
		t.Errorf("should strip separator: %q", result)
	}
	if strings.Contains(result, "(2 rows)") {
		t.Errorf("should strip row count: %q", result)
	}
}

func TestSnapshotExpandedFormat(t *testing.T) {
	input := "-[ RECORD 1 ]------\nid       | 1\nusername | alice_smith\nemail    | alice@example.com\n-[ RECORD 2 ]------\nid       | 2\nusername | bob_jones\nemail    | bob@example.com\n(2 rows)\n"
	result := filterExpanded(input)
	if !strings.Contains(result, "[1] id=1 username=alice_smith") {
		t.Errorf("missing record 1: %q", result)
	}
	if !strings.Contains(result, "[2] id=2 username=bob_jones") {
		t.Errorf("missing record 2: %q", result)
	}
	if strings.Contains(result, "-[ RECORD") {
		t.Errorf("should strip record header: %q", result)
	}
	if strings.Contains(result, "(2 rows)") {
		t.Errorf("should strip row count: %q", result)
	}
}

func TestIsTableFormatDetectsSeparator(t *testing.T) {
	input := " id | name\n----+------\n  1 | foo\n(1 row)\n"
	if !isTableFormat(input) {
		t.Errorf("should detect table format")
	}
}

func TestIsTableFormatRejectsPlain(t *testing.T) {
	if isTableFormat("COPY 5\n") {
		t.Errorf("should reject COPY")
	}
	if isTableFormat("SET\n") {
		t.Errorf("should reject SET")
	}
}

func TestIsExpandedFormatDetectsRecords(t *testing.T) {
	input := "-[ RECORD 1 ]----\nid | 1\nname | foo\n"
	if !isExpandedFormat(input) {
		t.Errorf("should detect expanded format")
	}
}

func TestIsExpandedFormatRejectsTable(t *testing.T) {
	input := " id | name\n----+------\n  1 | foo\n"
	if isExpandedFormat(input) {
		t.Errorf("should reject table format")
	}
}

func TestFilterTableBasic(t *testing.T) {
	input := " id | name  | email\n----+-------+---------\n  1 | alice | a@b.com\n  2 | bob   | b@b.com\n(2 rows)\n"
	result := filterTable(input)
	for _, want := range []string{"id\tname\temail", "1\talice\ta@b.com", "2\tbob\tb@b.com"} {
		if !strings.Contains(result, want) {
			t.Errorf("missing %q: %q", want, result)
		}
	}
	if strings.Contains(result, "----") {
		t.Errorf("should strip separator: %q", result)
	}
	if strings.Contains(result, "(2 rows)") {
		t.Errorf("should strip row count: %q", result)
	}
}

func TestFilterTableOverflow(t *testing.T) {
	lns := []string{" id | val", "----+-----"}
	for i := 1; i <= 40; i++ {
		lns = append(lns, fmt.Sprintf("  %d | row%d", i, i))
	}
	lns = append(lns, "(40 rows)")
	input := strings.Join(lns, "\n")

	result := filterTable(input)
	if !strings.Contains(result, "... +20 more rows") {
		t.Errorf("missing overflow line: %q", result)
	}
	// Header + maxTableRows data rows + overflow line
	resultLines := strings.Split(result, "\n")
	if len(resultLines) != maxTableRows+2 {
		t.Errorf("want %d lines, got %d: %q", maxTableRows+2, len(resultLines), result)
	}
}

func TestFilterTableEmpty(t *testing.T) {
	result := filterPsqlOutput("")
	if result != "" {
		t.Errorf("want empty, got %q", result)
	}
}

func TestFilterExpandedBasic(t *testing.T) {
	input := "-[ RECORD 1 ]----\nid   | 1\nname | alice\n-[ RECORD 2 ]----\nid   | 2\nname | bob\n"
	result := filterExpanded(input)
	if !strings.Contains(result, "[1] id=1 name=alice") {
		t.Errorf("missing record 1: %q", result)
	}
	if !strings.Contains(result, "[2] id=2 name=bob") {
		t.Errorf("missing record 2: %q", result)
	}
}

func TestFilterExpandedOverflow(t *testing.T) {
	var lns []string
	for i := 1; i <= 25; i++ {
		lns = append(lns, fmt.Sprintf("-[ RECORD %d ]----", i))
		lns = append(lns, fmt.Sprintf("id   | %d", i))
		lns = append(lns, fmt.Sprintf("name | user%d", i))
	}
	input := strings.Join(lns, "\n")

	result := filterExpanded(input)
	if !strings.Contains(result, "... +5 more records") {
		t.Errorf("missing overflow line: %q", result)
	}
}

func TestFilterPsqlPassthrough(t *testing.T) {
	input := "COPY 5\n"
	result := filterPsqlOutput(input)
	if result != "COPY 5\n" {
		t.Errorf("want %q, got %q", input, result)
	}
}

func TestFilterPsqlRoutesToTable(t *testing.T) {
	input := " id | name\n----+------\n  1 | foo\n(1 row)\n"
	result := filterPsqlOutput(input)
	if !strings.Contains(result, "id\tname") {
		t.Errorf("missing header: %q", result)
	}
	if strings.Contains(result, "----") {
		t.Errorf("should strip separator: %q", result)
	}
}

func TestFilterPsqlRoutesToExpanded(t *testing.T) {
	input := "-[ RECORD 1 ]----\nid | 1\nname | foo\n"
	result := filterPsqlOutput(input)
	if !strings.Contains(result, "[1]") {
		t.Errorf("missing record marker: %q", result)
	}
	if !strings.Contains(result, "id=1") {
		t.Errorf("missing key=val: %q", result)
	}
}

func TestFilterTableStripsRowCount(t *testing.T) {
	input := " c\n---\n 1\n(1 row)\n"
	result := filterTable(input)
	if strings.Contains(result, "(1 row)") {
		t.Errorf("should strip row count: %q", result)
	}
}

func TestFilterExpandedStripsRowCount(t *testing.T) {
	input := "-[ RECORD 1 ]----\nid | 1\n(1 row)\n"
	result := filterExpanded(input)
	if strings.Contains(result, "(1 row)") {
		t.Errorf("should strip row count: %q", result)
	}
}

func countTokens(text string) int {
	return len(strings.Fields(text))
}

func TestTableTokenSavings(t *testing.T) {
	input := " id | username          | email                          | status    | created_at          | updated_at          | role\n-------------+-------------------+--------------------------------+-----------+---------------------+---------------------+------------\n           1 | alice_smith       | alice@example.com              | active    | 2024-01-01 09:00:00 | 2024-01-15 14:30:00 | admin\n           2 | bob_jones         | bob.jones@company.org          | active    | 2024-01-02 10:15:00 | 2024-01-16 09:00:00 | user\n           3 | carol_white       | carol.white@example.com        | inactive  | 2024-01-03 11:30:00 | 2024-01-17 11:00:00 | user\n           4 | dave_brown        | dave@business.net              | active    | 2024-01-04 08:45:00 | 2024-01-18 16:00:00 | moderator\n           5 | eve_davis         | eve.davis@example.com          | active    | 2024-01-05 13:00:00 | 2024-01-19 10:30:00 | user\n(5 rows)\n"
	result := filterTable(input)
	inputTokens := countTokens(input)
	outputTokens := countTokens(result)
	savings := 100.0 - (float64(outputTokens)/float64(inputTokens))*100.0
	if savings < 40.0 {
		t.Errorf("Table filter: expected >=40%% savings, got %.1f%%", savings)
	}
}

func TestExpandedTokenSavings(t *testing.T) {
	input := "-[ RECORD 1 ]-------------------------------\nid            | 1\nusername      | alice_smith\nemail         | alice@example.com\nstatus        | active\nrole          | admin\ncreated_at    | 2024-01-01 09:00:00\nupdated_at    | 2024-01-15 14:30:00\nlast_login    | 2024-02-01 08:00:00\nlogin_count   | 42\npreferences   | {\"theme\":\"dark\",\"notifications\":true}\n-[ RECORD 2 ]-------------------------------\nid            | 2\nusername      | bob_jones\nemail         | bob.jones@company.org\nstatus        | active\nrole          | user\ncreated_at    | 2024-01-02 10:15:00\nupdated_at    | 2024-01-16 09:00:00\nlast_login    | 2024-02-02 09:30:00\nlogin_count   | 17\npreferences   | {\"theme\":\"light\",\"notifications\":false}\n(2 rows)\n"
	result := filterExpanded(input)
	inputTokens := countTokens(input)
	outputTokens := countTokens(result)
	savings := 100.0 - (float64(outputTokens)/float64(inputTokens))*100.0
	if savings < 60.0 {
		t.Errorf("Expanded filter: expected >=60%% savings, got %.1f%%", savings)
	}
}
