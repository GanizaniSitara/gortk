// init_copilot.go implements `gortk init --copilot`: a PROJECT-SCOPED installer
// that wires gortk into GitHub Copilot. It is a native-Windows, offline port of
// the Copilot slice of rtk's src/hooks/init.rs (run_copilot / run_copilot_at).
//
// Unlike the global Claude installer (init_install.go, which writes under
// ~/.claude), this writes into the CURRENT DIRECTORY's ./.github subtree so the
// hook config travels with the repository:
//
//   - ./.github/hooks/gortk-rewrite.json — the Copilot hook config. The
//     PreToolUse key is the VS Code Copilot Chat schema; preToolUse is the
//     Copilot CLI schema; both live in one file and both invoke
//     `gortk hook copilot`. Written write-if-changed (no-op when identical).
//   - ./.github/copilot-instructions.md — UPSERTs a gortk-owned marker block,
//     preserving any user content outside the markers. If the markers already
//     exist their content is replaced in place.
//
// Flags (parsed by RunInit and forwarded here): --dry-run and --show compute and
// print the plan without writing anything.
package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Project-scoped Copilot config layout, relative to the current directory.
const (
	githubDir             = ".github"
	copilotHookFile       = "gortk-rewrite.json"
	copilotInstructFile   = "copilot-instructions.md"
	copilotBlockStart     = "<!-- gortk-instructions -->"
	copilotBlockEnd       = "<!-- /gortk-instructions -->"
	copilotInstallCommand = "gortk init --copilot"
)

// copilotHookJSON is the hook config written to ./.github/hooks/gortk-rewrite.json.
// PreToolUse = VS Code Copilot Chat schema; preToolUse = Copilot CLI schema —
// both hosts read the same file, both invoke `gortk hook copilot`.
const copilotHookJSON = `{
  "version": 1,
  "hooks": {
    "PreToolUse": [
      {
        "type": "command",
        "command": "gortk hook copilot",
        "cwd": ".",
        "timeout": 5
      }
    ],
    "preToolUse": [
      {
        "type": "command",
        "bash": "gortk hook copilot",
        "powershell": "gortk hook copilot",
        "cwd": ".",
        "timeoutSec": 5
      }
    ]
  }
}
`

// copilotInstructions is the gortk-owned marker block upserted into
// ./.github/copilot-instructions.md. The opening/closing markers delimit the
// region gortk manages; everything outside them is left untouched.
const copilotInstructions = `<!-- gortk-instructions -->
# gortk — Token-Optimized CLI

` + "`gortk`" + ` compresses verbose command output, saving 60-90% of tokens.

## Rule
Prefix shell commands with ` + "`gortk`" + ` (unknown commands pass through unchanged, so it is always safe):
- ` + "`git status`" + ` -> ` + "`gortk git status`" + `
- ` + "`go test ./...`" + ` -> ` + "`gortk go test ./...`" + `
- ` + "`npm test`" + ` -> ` + "`gortk npm test`" + `
- ` + "`docker ps`" + ` -> ` + "`gortk docker ps`" + `

When you shell out to read or search, use the gortk equivalents for compact output:
` + "`gortk read <file>`" + `, ` + "`gortk grep <pattern> <path>`" + `, ` + "`gortk ls <dir>`" + `, ` + "`gortk tree <dir>`" + `, ` + "`gortk find`" + `, ` + "`gortk json`" + `, ` + "`gortk wc`" + `.

## Meta
- ` + "`gortk gain`" + ` — cumulative token-savings dashboard
<!-- /gortk-instructions -->`

// copilotInstallPlan captures the resolved project-scoped targets and current
// on-disk state, so --show and --dry-run can report without mutating anything.
type copilotInstallPlan struct {
	baseDir         string // the project root (".") resolved to absolute for display
	githubPath      string // ./.github
	hooksPath       string // ./.github/hooks
	hookPath        string // ./.github/hooks/gortk-rewrite.json
	instructionPath string // ./.github/copilot-instructions.md

	hookExists       bool // hook config already exists with current content
	instructAction   blockUpsertAction
	instructExists   bool   // the instructions file exists at all
	instructParseErr string // non-empty if the file has an unterminated gortk block
}

// runCopilotInit is the entry point for `gortk init --copilot [--show] [--dry-run]`,
// installing into the current directory's ./.github subtree (mirrors rtk
// run_copilot → run_copilot_at(".")).
func runCopilotInit(show, dryRun bool, verbose int) (int, error) {
	return runCopilotInitAt(".", show, dryRun, verbose)
}

// runCopilotInitAt is runCopilotInit relative to an explicit base path. Tests use
// it against a t.TempDir() so they never mutate the process-global working dir.
func runCopilotInitAt(base string, show, dryRun bool, verbose int) (int, error) {
	plan, err := buildCopilotPlan(base)
	if err != nil {
		return 1, err
	}

	if show {
		printCopilotState(plan)
		return 0, nil
	}
	if dryRun {
		printCopilotDryRun(plan)
		return 0, nil
	}

	return applyCopilotInstall(plan, verbose)
}

// buildCopilotPlan resolves all project-scoped paths and inspects current
// on-disk state without modifying anything.
func buildCopilotPlan(base string) (*copilotInstallPlan, error) {
	p := &copilotInstallPlan{
		baseDir:         base,
		githubPath:      filepath.Join(base, githubDir),
		hooksPath:       filepath.Join(base, githubDir, hooksSubdir),
		hookPath:        filepath.Join(base, githubDir, hooksSubdir, copilotHookFile),
		instructionPath: filepath.Join(base, githubDir, copilotInstructFile),
	}

	if existing, err := os.ReadFile(p.hookPath); err == nil {
		p.hookExists = string(existing) == copilotHookJSON
	}

	if data, err := os.ReadFile(p.instructionPath); err == nil {
		p.instructExists = true
		_, action := upsertGortkBlock(string(data), copilotInstructions)
		p.instructAction = action
		if action == blockMalformed {
			p.instructParseErr = "opening marker " + copilotBlockStart + " found without a closing " + copilotBlockEnd
		}
	} else {
		// No file yet → the upsert would add the block fresh.
		p.instructAction = blockAdded
	}

	return p, nil
}

// applyCopilotInstall performs the two-file install. The instructions upsert runs
// BEFORE the hook config write so a malformed instructions file aborts the whole
// install without leaving a stale hook on disk (matches rtk's ordering).
func applyCopilotInstall(p *copilotInstallPlan, verbose int) (int, error) {
	if p.instructParseErr != "" {
		fmt.Fprintf(os.Stderr, "gortk: refusing to modify %s: %s\n", p.instructionPath, p.instructParseErr)
		fmt.Fprintf(os.Stderr, "gortk: remove the incomplete block, then re-run: %s\n", copilotInstallCommand)
		return 1, fmt.Errorf("malformed gortk block in %s", p.instructionPath)
	}

	if err := os.MkdirAll(p.hooksPath, 0o755); err != nil {
		return 1, fmt.Errorf("create %s: %w", p.hooksPath, err)
	}

	// 1. Upsert the instructions marker block (preserving user content).
	if err := upsertCopilotInstructions(p.instructionPath); err != nil {
		return 1, err
	}

	// 2. Write the hook config (write-if-changed).
	if !p.hookExists {
		if err := os.WriteFile(p.hookPath, []byte(copilotHookJSON), 0o644); err != nil {
			return 1, fmt.Errorf("write hook config %s: %w", p.hookPath, err)
		}
		fmt.Printf("gortk: wrote Copilot hook config %s\n", p.hookPath)
	} else if verbose > 0 {
		fmt.Printf("gortk: Copilot hook config already up to date %s\n", p.hookPath)
	}

	fmt.Println("gortk: GitHub Copilot integration installed (project-scoped).")
	fmt.Println("gortk: restart your IDE or Copilot CLI session to activate.")
	return 0, nil
}

// upsertCopilotInstructions reads the instructions file (if any), upserts the
// gortk marker block, and writes it back when changed.
func upsertCopilotInstructions(path string) error {
	existing := ""
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	}
	newContent, action := upsertGortkBlock(existing, copilotInstructions)
	switch action {
	case blockUnchanged:
		fmt.Printf("gortk: Copilot instructions already up to date %s\n", path)
		return nil
	case blockMalformed:
		// Guarded earlier by the plan, but stay defensive.
		return fmt.Errorf("refusing to modify malformed %s", path)
	default: // blockAdded / blockUpdated
		if err := os.WriteFile(path, []byte(newContent), 0o644); err != nil {
			return fmt.Errorf("write Copilot instructions %s: %w", path, err)
		}
		verb := "added"
		if action == blockUpdated {
			verb = "updated"
		}
		fmt.Printf("gortk: %s Copilot instructions block in %s\n", verb, path)
		return nil
	}
}

// ── marker-block upsert (pure) ────────────────────────────────────────
//
// Port of rtk init.rs::upsert_rtk_block: insert or replace the gortk-owned block
// delimited by the start/end markers, preserving any user content outside it.

type blockUpsertAction int

const (
	blockAdded     blockUpsertAction = iota // no existing block — appended
	blockUpdated                            // existing block replaced (content differed)
	blockUnchanged                          // existing block identical — no-op
	blockMalformed                          // opening marker without a closing marker
)

// upsertGortkBlock inserts or replaces the gortk instructions block in content,
// returning (newContent, action). The caller decides whether to write based on
// the action. Faithful port of rtk upsert_rtk_block, keyed on the gortk markers.
func upsertGortkBlock(content, block string) (string, blockUpsertAction) {
	start := strings.Index(content, copilotBlockStart)
	if start >= 0 {
		relEnd := strings.Index(content[start:], copilotBlockEnd)
		if relEnd < 0 {
			// Opening marker without a closing marker — not safe to rewrite.
			return content, blockMalformed
		}
		end := start + relEnd
		endPos := end + len(copilotBlockEnd)

		currentBlock := strings.TrimSpace(content[start:endPos])
		desiredBlock := strings.TrimSpace(block)
		if currentBlock == desiredBlock {
			return content, blockUnchanged
		}

		before := strings.TrimRight(content[:start], " \t\r\n")
		after := strings.TrimLeft(content[endPos:], " \t\r\n")

		var result string
		switch {
		case before == "" && after == "":
			result = desiredBlock
		case before == "":
			result = desiredBlock + "\n\n" + after
		case after == "":
			result = before + "\n\n" + desiredBlock
		default:
			result = before + "\n\n" + desiredBlock + "\n\n" + after
		}
		return result, blockUpdated
	}

	// No existing block — append (after any existing user content).
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return block, blockAdded
	}
	return trimmed + "\n\n" + strings.TrimSpace(block), blockAdded
}

// ── reporting (--show / --dry-run) ────────────────────────────────────

func printCopilotState(p *copilotInstallPlan) {
	fmt.Println("gortk init --copilot — current state (GitHub Copilot, project-scoped):")
	fmt.Printf("  base:          %s\n", p.baseDir)
	fmt.Printf("  hook config:   %s  (%s)\n", p.hookPath, existsLabel(p.hookExists, "present (current)", "missing/outdated"))
	fmt.Printf("  instructions:  %s  (%s)\n", p.instructionPath, existsLabel(p.instructExists, "exists", "absent"))
	if p.instructParseErr != "" {
		fmt.Printf("  instructions parse error: %s\n", p.instructParseErr)
	}
	fmt.Printf("  block state:   %s\n", copilotBlockActionLabel(p.instructAction))
}

func printCopilotDryRun(p *copilotInstallPlan) {
	fmt.Println("gortk init --copilot --dry-run (GitHub Copilot, project-scoped) — nothing will be written:")
	if p.instructParseErr != "" {
		fmt.Printf("  [blocked] instructions present but malformed: %s\n", p.instructParseErr)
		fmt.Printf("  [dry-run] would refuse to modify until the incomplete block is removed\n")
		return
	}
	switch p.instructAction {
	case blockUnchanged:
		fmt.Printf("  [dry-run] instructions block already current: %s\n", p.instructionPath)
	case blockUpdated:
		fmt.Printf("  [dry-run] would update instructions block in %s (user content preserved)\n", p.instructionPath)
	default:
		fmt.Printf("  [dry-run] would add instructions block to %s\n", p.instructionPath)
	}
	if p.hookExists {
		fmt.Printf("  [dry-run] hook config already current: %s\n", p.hookPath)
	} else {
		fmt.Printf("  [dry-run] would write hook config: %s\n", p.hookPath)
	}
}

func copilotBlockActionLabel(a blockUpsertAction) string {
	switch a {
	case blockUnchanged:
		return "installed (current)"
	case blockUpdated:
		return "installed (outdated — would update)"
	case blockMalformed:
		return "malformed (unterminated marker)"
	default:
		return "not installed"
	}
}
