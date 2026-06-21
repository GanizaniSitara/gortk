// init_rules.go implements the PROMPT-ONLY agent installers:
// `gortk init --agent {windsurf,cline,kilocode,antigravity}`. These agents have
// no programmatic hook surface, so rtk (and gortk) install a project-local rules
// markdown file that tells the agent to prefix shell commands with `gortk`. It is
// a native-Windows, offline port of rtk's src/hooks/init.rs run_windsurf_mode /
// run_cline_mode / run_kilocode_mode_at / run_antigravity_mode_at and the
// hooks/<agent>/rules.md templates.
//
// All four share one shape (rtk's): write the rules markdown into a project-local
// path; if the file already exists, append the block to any user content UNLESS
// the file already mentions gortk (the idempotency guard — rtk keys on "rtk"/"RTK",
// gortk on "gortk"/"GORTK"). They differ only in target path:
//
//   - windsurf    → ./.windsurfrules                              (flat file)
//   - cline       → ./.clinerules                                 (flat file)
//   - kilocode    → ./.kilocode/rules/gortk-rules.md              (dir + file)
//   - antigravity → ./.agents/rules/antigravity-gortk-rules.md    (dir + file)
//
// These are project-scoped (relative to the current directory), matching rtk
// which writes workspace-scoped rules. A base-path seam (…At) lets tests drive a
// t.TempDir() instead of the process working directory.
package hooks

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// gortkRulesMarkdown is the rules block written for the prompt-only agents. It is
// the gortk-branded analogue of rtk's identical hooks/<agent>/rules.md templates
// (windsurf/cline/kilocode/antigravity all ship byte-identical content); a single
// shared constant mirrors that, with the {{AGENT}} placeholder substituted for
// the agent label at install time (plain string replace, so the literal "%"
// tokens in the body need no escaping).
const rulesAgentPlaceholder = "{{AGENT}}"

const gortkRulesMarkdown = "# gortk — Token-Optimized CLI (" + rulesAgentPlaceholder + ")\n" +
	"\n" +
	"**Usage**: Token-optimized CLI proxy for shell commands.\n" +
	"\n" +
	"## Rule\n" +
	"\n" +
	"Always prefix shell commands with `gortk` to minimize token consumption.\n" +
	"Unknown commands pass through unchanged, so `gortk` is always safe to use.\n" +
	"\n" +
	"Examples:\n" +
	"\n" +
	"```bash\n" +
	"gortk git status\n" +
	"gortk go test ./...\n" +
	"gortk ls src/\n" +
	"gortk grep \"pattern\" src/\n" +
	"gortk find \"*.go\" .\n" +
	"gortk docker ps\n" +
	"gortk gh pr list\n" +
	"```\n" +
	"\n" +
	"## Meta Commands\n" +
	"\n" +
	"```bash\n" +
	"gortk gain            # Show token savings\n" +
	"```\n" +
	"\n" +
	"## Why\n" +
	"\n" +
	"gortk filters and compresses command output before it reaches the LLM context, " +
	"saving 60-90% tokens on common operations. Always use `gortk <cmd>` instead of raw commands.\n"

// rulesAgent describes a prompt-only agent's install target and human label.
type rulesAgent struct {
	name     string // agent name for messages, e.g. "windsurf"
	label    string // label substituted into the rules markdown, e.g. "Windsurf"
	relDir   string // directory under the project root to create ("" = project root)
	fileName string // rules file name written into relDir
	restart  string // a short post-install hint ("" = none)
}

// The four prompt-only agents and their rtk-derived target paths.
var (
	windsurfAgent    = rulesAgent{name: "windsurf", label: "Windsurf", relDir: "", fileName: ".windsurfrules", restart: "Restart Windsurf."}
	clineAgent       = rulesAgent{name: "cline", label: "Cline", relDir: "", fileName: ".clinerules"}
	kilocodeAgent    = rulesAgent{name: "kilocode", label: "Kilo Code", relDir: ".kilocode/rules", fileName: "gortk-rules.md"}
	antigravityAgent = rulesAgent{name: "antigravity", label: "Google Antigravity", relDir: ".agents/rules", fileName: "antigravity-gortk-rules.md"}
)

// rulesInstallPlan captures the resolved project-scoped target and current
// on-disk state so --show and --dry-run can report without mutating anything.
type rulesInstallPlan struct {
	agent     rulesAgent
	baseDir   string // project root (".") for display
	dirPath   string // baseDir/relDir (the directory to ensure exists)
	rulesPath string // dirPath/fileName

	fileExists      bool
	alreadyHasGortk bool // file mentions gortk → treated as already configured
	newContent      string
}

// rulesContentFor renders the agent's rules markdown with its label substituted.
func rulesContentFor(a rulesAgent) string {
	return strings.ReplaceAll(gortkRulesMarkdown, rulesAgentPlaceholder, a.label)
}

// runWindsurfInit/etc. are the per-agent entry points wired into the dispatch
// table; each resolves the current directory and delegates to runRulesInitAt.
func runWindsurfInit(show, dryRun bool, verbose int) (int, error) {
	return runRulesInit(windsurfAgent, show, dryRun, verbose)
}
func runClineInit(show, dryRun bool, verbose int) (int, error) {
	return runRulesInit(clineAgent, show, dryRun, verbose)
}
func runKilocodeInit(show, dryRun bool, verbose int) (int, error) {
	return runRulesInit(kilocodeAgent, show, dryRun, verbose)
}
func runAntigravityInit(show, dryRun bool, verbose int) (int, error) {
	return runRulesInit(antigravityAgent, show, dryRun, verbose)
}

// runRulesInit resolves the current directory and delegates to runRulesInitAt.
func runRulesInit(a rulesAgent, show, dryRun bool, verbose int) (int, error) {
	base, err := os.Getwd()
	if err != nil || base == "" {
		base = "."
	}
	return runRulesInitAt(a, base, show, dryRun, verbose)
}

// runRulesInitAt is runRulesInit relative to an explicit base directory. Tests
// pass a t.TempDir() so they never mutate the process working directory.
func runRulesInitAt(a rulesAgent, base string, show, dryRun bool, verbose int) (int, error) {
	plan, err := buildRulesPlan(a, base)
	if err != nil {
		return 1, err
	}

	if show {
		printRulesState(plan)
		return 0, nil
	}
	if dryRun {
		printRulesDryRun(plan)
		return 0, nil
	}

	return applyRulesInstall(plan, verbose)
}

// buildRulesPlan resolves the rules path and inspects current on-disk state.
// When the file exists without a gortk mention, the appended content is
// pre-computed (existing user content + a blank line + the rules block), matching
// rtk's `format!("{}\n\n{}", existing.trim(), RULES)`.
func buildRulesPlan(a rulesAgent, base string) (*rulesInstallPlan, error) {
	dirPath := base
	if a.relDir != "" {
		dirPath = filepath.Join(base, filepath.FromSlash(a.relDir))
	}
	p := &rulesInstallPlan{
		agent:     a,
		baseDir:   base,
		dirPath:   dirPath,
		rulesPath: filepath.Join(dirPath, a.fileName),
	}

	rules := rulesContentFor(a)
	if data, err := os.ReadFile(p.rulesPath); err == nil {
		p.fileExists = true
		existing := string(data)
		// rtk keys idempotency on a case-variant "rtk" mention; gortk keys on
		// "gortk" so re-running never double-appends the block.
		if strings.Contains(existing, "gortk") || strings.Contains(existing, "GORTK") {
			p.alreadyHasGortk = true
		} else if strings.TrimSpace(existing) == "" {
			p.newContent = rules
		} else {
			p.newContent = strings.TrimRight(strings.TrimSpace(existing), "\n") + "\n\n" + rules
		}
	} else {
		// No file yet — write the block fresh.
		p.newContent = rules
	}
	return p, nil
}

// ── apply ─────────────────────────────────────────────────────────────

func applyRulesInstall(p *rulesInstallPlan, verbose int) (int, error) {
	if p.alreadyHasGortk {
		fmt.Printf("gortk: %s rules already configured in %s\n", p.agent.label, p.rulesPath)
		return 0, nil
	}

	if p.agent.relDir != "" {
		if err := os.MkdirAll(p.dirPath, 0o755); err != nil {
			return 1, fmt.Errorf("create %s: %w", p.dirPath, err)
		}
	}
	if err := os.WriteFile(p.rulesPath, []byte(p.newContent), 0o644); err != nil {
		return 1, fmt.Errorf("write %s: %w", p.rulesPath, err)
	}

	verb := "created"
	if p.fileExists {
		verb = "appended gortk rules to"
	}
	fmt.Printf("gortk: %s %s (%s integration, project-scoped)\n", verb, p.rulesPath, p.agent.label)
	if p.agent.restart != "" {
		fmt.Printf("gortk: %s Test with: git status\n", p.agent.restart)
	} else {
		fmt.Println("gortk: Test with: git status")
	}
	return 0, nil
}

// ── reporting (--show / --dry-run) ────────────────────────────────────

func printRulesState(p *rulesInstallPlan) {
	fmt.Printf("gortk init --agent %s — current state (%s, project-scoped, prompt-only):\n", p.agent.name, p.agent.label)
	fmt.Printf("  base:         %s\n", p.baseDir)
	fmt.Printf("  rules file:   %s  (%s)\n", p.rulesPath, existsLabel(p.fileExists, "exists", "absent"))
	state := "not installed"
	if p.alreadyHasGortk {
		state = "installed (gortk mention present)"
	} else if p.fileExists {
		state = "absent — would append to existing file"
	}
	fmt.Printf("  rules state:  %s\n", state)
}

func printRulesDryRun(p *rulesInstallPlan) {
	fmt.Printf("gortk init --agent %s --dry-run (%s, prompt-only) — nothing will be written:\n", p.agent.name, p.agent.label)
	switch {
	case p.alreadyHasGortk:
		fmt.Printf("  [dry-run] rules already configured in %s (no change)\n", p.rulesPath)
	case p.fileExists:
		fmt.Printf("  [dry-run] would append gortk rules to %s (user content preserved)\n", p.rulesPath)
	default:
		if p.agent.relDir != "" {
			fmt.Printf("  [dry-run] would create directory %s\n", p.dirPath)
		}
		fmt.Printf("  [dry-run] would create %s with gortk rules\n", p.rulesPath)
	}
}
