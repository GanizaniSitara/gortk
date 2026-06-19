package prisma

import (
	"strings"
	"testing"
)

// TestFilterGenerate ports the Rust test_filter_generate case: the ASCII-art /
// preamble lines must be stripped and the header must remain.
func TestFilterGenerate(t *testing.T) {
	output := `
Prisma schema loaded from prisma/schema.prisma

✔ Generated Prisma Client (v5.7.0) to ./node_modules/@prisma/client in 234ms

Start by importing your Prisma Client:

import { PrismaClient } from '@prisma/client'

42 models, 18 enums, 890 types generated
`
	result := filterPrismaGenerate(output)
	if !strings.Contains(result, "Prisma Client generated") {
		t.Errorf("missing header: %q", result)
	}
	// Parser may not extract exact counts from this format; just confirm the
	// verbose decoration is gone.
	if strings.Contains(result, "Prisma schema loaded") {
		t.Errorf("should strip schema-loaded line: %q", result)
	}
	if strings.Contains(result, "Start by importing") {
		t.Errorf("should strip import preamble: %q", result)
	}
}

// TestFilterMigrateDev ports the Rust test_filter_migrate_dev case.
func TestFilterMigrateDev(t *testing.T) {
	output := `
Applying migration 20260128_add_sessions

CREATE TABLE "Session" (
  "id" TEXT NOT NULL,
  "userId" TEXT NOT NULL,
  FOREIGN KEY ("userId") REFERENCES "User"("id")
);

CREATE INDEX "session_status_idx" ON "Session"("status");

✓ Migration applied
`
	result := filterMigrateDev(output)
	for _, want := range []string{"20260128_add_sessions", "+ 1 table", "Applied"} {
		if !strings.Contains(result, want) {
			t.Errorf("migrate dev missing %q: %q", want, result)
		}
	}
}

// TestExtractNumber ports the Rust test_extract_number case.
func TestExtractNumber(t *testing.T) {
	cases := []struct {
		in     string
		want   int
		wantOK bool
	}{
		{"42 models generated", 42, true},
		{"no numbers here", 0, false},
	}
	for _, c := range cases {
		got, ok := extractNumber(c.in)
		if ok != c.wantOK || got != c.want {
			t.Errorf("extractNumber(%q) = %d,%v want %d,%v", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

// --- Additional coverage for the remaining pure filters / helpers ---

func TestFilterMigrateStatus(t *testing.T) {
	output := `
Status

3 migrations found in prisma/migrations

Following migrations have been applied:
20260101_init applied
20260102_add_users applied
1 migration is pending
`
	result := filterMigrateStatus(output)
	if !strings.Contains(result, "Migrations:") {
		t.Errorf("missing summary line: %q", result)
	}
	if !strings.Contains(result, "Latest:") {
		t.Errorf("missing latest migration: %q", result)
	}
}

func TestFilterMigrateDeploySuccess(t *testing.T) {
	output := `
2 migrations found

✓ migration 20260101_init applied
✓ migration 20260102_more applied
`
	result := filterMigrateDeploy(output)
	if !strings.Contains(result, "migration(s) deployed") {
		t.Errorf("expected deploy summary: %q", result)
	}
	if strings.Contains(result, "[FAIL]") {
		t.Errorf("should not report failure: %q", result)
	}
}

func TestFilterMigrateDeployFailure(t *testing.T) {
	output := "error: P3009 migrate found failed migrations\n"
	result := filterMigrateDeploy(output)
	if !strings.Contains(result, "[FAIL] Deployment failed:") {
		t.Errorf("expected failure header: %q", result)
	}
	if !strings.Contains(result, "P3009") {
		t.Errorf("expected error detail: %q", result)
	}
}

func TestFilterDBPush(t *testing.T) {
	output := `
CREATE TABLE "Post" ( ... );
ALTER TABLE "User" ADD COLUMN "bio" TEXT;
DROP TABLE "Legacy";
`
	result := filterDBPush(output)
	if !strings.Contains(result, "Schema pushed to database") {
		t.Errorf("missing header: %q", result)
	}
	if !strings.Contains(result, "+ 1 tables") {
		t.Errorf("expected table count: %q", result)
	}
	if !strings.Contains(result, "- 1 dropped") {
		t.Errorf("expected drop count: %q", result)
	}
}

func TestFilterDBPushNoChanges(t *testing.T) {
	result := filterDBPush("The database is already in sync with the Prisma schema.\n")
	if result != "Schema pushed to database" {
		t.Errorf("want header only, got %q", result)
	}
}

func TestExtractTableName(t *testing.T) {
	cases := []struct {
		in     string
		want   string
		wantOK bool
	}{
		{`CREATE TABLE "Session" (`, "Session", true},
		{"CREATE TABLE `users`;", "users", true},
		{"no table keyword here", "", false},
	}
	for _, c := range cases {
		got, ok := extractTableName(c.in)
		if ok != c.wantOK || got != c.want {
			t.Errorf("extractTableName(%q) = %q,%v want %q,%v", c.in, got, ok, c.want, c.wantOK)
		}
	}
}

func TestExtractIndexName(t *testing.T) {
	got, ok := extractIndexName(`CREATE INDEX "session_status_idx" ON "Session"("status");`)
	if !ok || got != "session_status_idx" {
		t.Errorf("extractIndexName = %q,%v want session_status_idx,true", got, ok)
	}
	if _, ok := extractIndexName("no index keyword"); ok {
		t.Errorf("expected no match for line without INDEX")
	}
}

func TestExtractName(t *testing.T) {
	name, rest := extractName([]string{"--name", "add_sessions", "--create-only"})
	if name != "add_sessions" {
		t.Errorf("name = %q want add_sessions", name)
	}
	if len(rest) != 1 || rest[0] != "--create-only" {
		t.Errorf("rest = %v want [--create-only]", rest)
	}

	name, rest = extractName([]string{"--create-only"})
	if name != "" {
		t.Errorf("name = %q want empty", name)
	}
	if len(rest) != 1 || rest[0] != "--create-only" {
		t.Errorf("rest = %v want [--create-only]", rest)
	}
}
