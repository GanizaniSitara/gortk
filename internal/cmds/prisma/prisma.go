// Package prisma is gortk's token-optimized wrapper around the Prisma CLI. It
// strips ASCII art and verbose decoration from `prisma generate`,
// `prisma migrate {dev,status,deploy}`, and `prisma db push`, emitting a
// compact summary. Faithful port of rtk's src/cmds/js/prisma_cmd.rs.
//
// Like rtk, this wraps the platform `prisma` binary, falling back to
// `npx prisma` when a global install is not on PATH. gortk resolves tools
// PATHEXT-aware on Windows.
package prisma

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "prisma",
		Summary: "Prisma commands with compact output (no ASCII art)",
		Run:     Run,
	})
}

// Run dispatches the prisma subcommand. It mirrors rtk's main.rs dispatch:
//
//	prisma generate [args...]
//	prisma migrate dev [--name <n>] [args...]
//	prisma migrate status [args...]
//	prisma migrate deploy [args...]
//	prisma db push [args...]
func Run(args []string, verbose int) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("prisma: expected a subcommand (generate, migrate, db push)")
	}

	switch args[0] {
	case "generate":
		return runGenerate(args[1:], verbose)
	case "migrate":
		return runMigrate(args[1:], verbose)
	case "db":
		if len(args) >= 2 && args[1] == "push" {
			return runDBPush(args[2:], verbose)
		}
		return 2, fmt.Errorf("prisma: unsupported db subcommand (expected: db push)")
	default:
		return 2, fmt.Errorf("prisma: unsupported subcommand %q (expected: generate, migrate, db push)", args[0])
	}
}

// createPrismaCommand builds the command to invoke prisma, preferring a global
// install and falling back to `npx prisma`. Mirrors rtk's create_prisma_command.
func createPrismaCommand(prismaArgs ...string) *exec.Cmd {
	if core.ToolExists("prisma") {
		return core.ResolvedCommand("prisma", prismaArgs...)
	}
	return core.ResolvedCommand("npx", append([]string{"prisma"}, prismaArgs...)...)
}

func runGenerate(args []string, verbose int) (int, error) {
	cmd := createPrismaCommand(append([]string{"generate"}, args...)...)
	if verbose > 0 {
		fmt.Fprintln(os.Stderr, "Running: prisma generate")
	}
	opts := core.RunOptions{SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, "prisma", "generate", filterPrismaGenerate, opts)
}

func runMigrate(args []string, verbose int) (int, error) {
	if len(args) == 0 {
		return 2, fmt.Errorf("prisma migrate: expected a subcommand (dev, status, deploy)")
	}

	sub := args[0]
	rest := args[1:]

	var prismaArgs []string
	var cmdName string
	var filter func(string) string

	switch sub {
	case "dev":
		prismaArgs = []string{"migrate", "dev"}
		// rtk lifts an optional --name <n> into the prisma invocation; any
		// remaining trailing args are appended as-is.
		name, remaining := extractName(rest)
		if name != "" {
			prismaArgs = append(prismaArgs, "--name", name)
		}
		prismaArgs = append(prismaArgs, remaining...)
		cmdName = "prisma migrate dev"
		filter = filterMigrateDev
	case "status":
		prismaArgs = append([]string{"migrate", "status"}, rest...)
		cmdName = "prisma migrate status"
		filter = filterMigrateStatus
	case "deploy":
		prismaArgs = append([]string{"migrate", "deploy"}, rest...)
		cmdName = "prisma migrate deploy"
		filter = filterMigrateDeploy
	default:
		return 2, fmt.Errorf("prisma migrate: unsupported subcommand %q (expected: dev, status, deploy)", sub)
	}

	cmd := createPrismaCommand(prismaArgs...)
	if verbose > 0 {
		fmt.Fprintf(os.Stderr, "Running: %s\n", cmdName)
	}
	opts := core.RunOptions{SkipFilterOnFailure: true}
	argsDisplay := strings.TrimPrefix(cmdName, "prisma ")
	return core.RunFiltered(cmd, "prisma", argsDisplay, filter, opts)
}

func runDBPush(args []string, verbose int) (int, error) {
	cmd := createPrismaCommand(append([]string{"db", "push"}, args...)...)
	if verbose > 0 {
		fmt.Fprintln(os.Stderr, "Running: prisma db push")
	}
	opts := core.RunOptions{SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, "prisma", "db push", filterDBPush, opts)
}

// extractName pulls an optional `--name <value>` pair out of migrate dev args,
// returning the name (or "") and the remaining args with that pair removed.
func extractName(args []string) (string, []string) {
	var remaining []string
	name := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--name" && i+1 < len(args) {
			name = args[i+1]
			i++
			continue
		}
		remaining = append(remaining, args[i])
	}
	return name, remaining
}

// lines splits text the way Rust's str::lines() does: on '\n', dropping a
// single trailing empty element produced by a final newline.
func lines(s string) []string {
	s = core.NormalizeNewlines(s)
	parts := strings.Split(s, "\n")
	if n := len(parts); n > 0 && parts[n-1] == "" {
		parts = parts[:n-1]
	}
	return parts
}

// filterPrismaGenerate strips ASCII art and extracts model/enum/type counts.
func filterPrismaGenerate(output string) string {
	models, enums, types := 0, 0, 0
	outputPath := ""

	for _, line := range lines(output) {
		// Skip ASCII art and box drawing.
		if strings.Contains(line, "█") || // █
			strings.Contains(line, "▀") || // ▀
			strings.Contains(line, "▄") || // ▄
			strings.Contains(line, "┌") || // ┌
			strings.Contains(line, "└") || // └
			strings.Contains(line, "│") { // │
			continue
		}

		if strings.Contains(line, "model") && strings.Contains(line, "generated") {
			if num, ok := extractNumber(line); ok {
				models = num
			}
		}
		if strings.Contains(line, "enum") {
			if num, ok := extractNumber(line); ok {
				enums = num
			}
		}
		if strings.Contains(line, "type") {
			if num, ok := extractNumber(line); ok {
				types = num
			}
		}

		if strings.Contains(line, "node_modules") && strings.Contains(line, "@prisma") {
			outputPath = strings.TrimSpace(line)
		}
	}

	var b strings.Builder
	b.WriteString("Prisma Client generated\n")

	if models > 0 || enums > 0 || types > 0 {
		fmt.Fprintf(&b, "  • %d models, %d enums, %d types\n", models, enums, types)
	}

	if outputPath != "" {
		b.WriteString("  • Output: node_modules/@prisma/client\n")
	}

	return strings.TrimSpace(b.String())
}

// filterMigrateDev extracts migration changes from `migrate dev` output.
func filterMigrateDev(output string) string {
	migrationName := ""
	tablesAdded := 0
	tablesModified := 0
	var relations []string
	var indexes []string
	applied := false

	for _, line := range lines(output) {
		if strings.Contains(line, "migration") && strings.Contains(line, "_") {
			if pos := strings.Index(line, "202"); pos >= 0 {
				rest := line[pos:]
				end := strings.IndexFunc(rest, func(c rune) bool {
					return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
				})
				if end < 0 {
					end = len(rest)
				}
				migrationName = rest[:end]
			}
		}

		if strings.Contains(line, "CREATE TABLE") {
			tablesAdded++
		}
		if strings.Contains(line, "ALTER TABLE") {
			tablesModified++
		}
		if strings.Contains(line, "FOREIGN KEY") || strings.Contains(line, "REFERENCES") {
			if table, ok := extractTableName(line); ok {
				relations = append(relations, table)
			}
		}
		if strings.Contains(line, "CREATE INDEX") || strings.Contains(line, "CREATE UNIQUE INDEX") {
			if idx, ok := extractIndexName(line); ok {
				indexes = append(indexes, idx)
			}
		}

		if strings.Contains(line, "applied") || strings.Contains(line, "✓") { // ✓
			applied = true
		}
	}

	var b strings.Builder

	if migrationName != "" {
		fmt.Fprintf(&b, "Migration: %s\n", migrationName)
	}

	b.WriteString("Changes:\n")
	if tablesAdded > 0 {
		fmt.Fprintf(&b, "  + %d table(s)\n", tablesAdded)
	}
	if tablesModified > 0 {
		fmt.Fprintf(&b, "  ~ %d table(s) modified\n", tablesModified)
	}
	if len(relations) > 0 {
		fmt.Fprintf(&b, "  + %d relation(s)\n", len(relations))
	}
	if len(indexes) > 0 {
		fmt.Fprintf(&b, "  ~ %d index(es)\n", len(indexes))
	}

	b.WriteByte('\n')
	if applied {
		b.WriteString("Applied | Pending: 0\n")
	}

	return strings.TrimSpace(b.String())
}

// filterMigrateStatus summarizes `migrate status` output.
func filterMigrateStatus(output string) string {
	appliedCount := 0
	pendingCount := 0
	latestMigration := ""

	for _, line := range lines(output) {
		if strings.Contains(line, "applied") {
			appliedCount++
			if latestMigration == "" && strings.Contains(line, "202") {
				if pos := strings.Index(line, "202"); pos >= 0 {
					rest := line[pos:]
					end := strings.IndexFunc(rest, func(c rune) bool {
						return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
					})
					if end < 0 {
						// rtk's unwrap_or(20) fallback when no whitespace found.
						end = 20
						if end > len(rest) {
							end = len(rest)
						}
					}
					latestMigration = rest[:end]
				}
			}
		}
		if strings.Contains(line, "pending") || strings.Contains(line, "unapplied") {
			pendingCount++
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Migrations: %d applied, %d pending\n", appliedCount, pendingCount)

	if latestMigration != "" {
		fmt.Fprintf(&b, "Latest: %s\n", latestMigration)
	}

	return strings.TrimSpace(b.String())
}

// filterMigrateDeploy summarizes `migrate deploy` output.
func filterMigrateDeploy(output string) string {
	deployed := 0
	var errs []string

	for _, line := range lines(output) {
		if strings.Contains(line, "applied") || strings.Contains(line, "✓") { // ✓
			deployed++
		}
		if strings.Contains(line, "error") || strings.Contains(line, "ERROR") {
			errs = append(errs, strings.TrimSpace(line))
		}
	}

	var b strings.Builder

	if len(errs) == 0 {
		fmt.Fprintf(&b, "%d migration(s) deployed\n", deployed)
	} else {
		b.WriteString("[FAIL] Deployment failed:\n")
		limit := len(errs)
		if limit > 5 {
			limit = 5
		}
		for _, e := range errs[:limit] {
			fmt.Fprintf(&b, "  %s\n", e)
		}
	}

	return strings.TrimSpace(b.String())
}

// filterDBPush summarizes `db push` output.
func filterDBPush(output string) string {
	tablesAdded := 0
	columnsModified := 0
	dropped := 0

	for _, line := range lines(output) {
		if strings.Contains(line, "CREATE TABLE") {
			tablesAdded++
		}
		if strings.Contains(line, "ALTER") || strings.Contains(line, "ADD COLUMN") {
			columnsModified++
		}
		if strings.Contains(line, "DROP") {
			dropped++
		}
	}

	var b strings.Builder
	b.WriteString("Schema pushed to database\n")

	if tablesAdded > 0 || columnsModified > 0 || dropped > 0 {
		fmt.Fprintf(&b, "  + %d tables, ~ %d columns, - %d dropped\n", tablesAdded, columnsModified, dropped)
	}

	return strings.TrimSpace(b.String())
}

// extractNumber returns the first whitespace-delimited word that parses as a
// non-negative integer. Mirrors rtk's extract_number (parse::<usize>).
func extractNumber(line string) (int, bool) {
	for _, word := range strings.Fields(line) {
		if n, err := strconv.ParseUint(word, 10, 64); err == nil {
			return int(n), true
		}
	}
	return 0, false
}

// extractTableName pulls the identifier following the TABLE keyword.
func extractTableName(line string) (string, bool) {
	if !strings.Contains(line, "TABLE") {
		return "", false
	}
	parts := strings.Fields(line)
	for i, part := range parts {
		if part == "TABLE" && i+1 < len(parts) {
			return trimSQLIdent(parts[i+1]), true
		}
	}
	return "", false
}

// extractIndexName pulls the identifier following the INDEX keyword.
func extractIndexName(line string) (string, bool) {
	if !strings.Contains(line, "INDEX") {
		return "", false
	}
	parts := strings.Fields(line)
	for i, part := range parts {
		if part == "INDEX" && i+1 < len(parts) {
			return trimSQLIdent(parts[i+1]), true
		}
	}
	return "", false
}

// trimSQLIdent strips surrounding SQL quoting/terminator characters, matching
// rtk's trim_matches(|c| c == '`' || c == '"' || c == ';').
func trimSQLIdent(s string) string {
	return strings.Trim(s, "`\";")
}
