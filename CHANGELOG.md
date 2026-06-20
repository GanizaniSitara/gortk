# Changelog

All notable changes to gortk are documented here. gortk is a Go port of rtk
(https://github.com/rtk-ai/rtk), Apache-2.0.

## [Unreleased]

### Foundation (core contract — complete)

- **Project scaffold**: Go module `gortk` (Go 1.23, `CGO_ENABLED=0`), single
  third-party dependency `github.com/BurntSushi/toml v1.4.0`.
- `cmd/gortk/main.go` — stdlib CLI dispatch via the command registry, with a
  fallback path (TOML filter engine, then raw passthrough) for commands without
  a dedicated module.
- `internal/core/` — execution + helpers ported from rtk's `src/core/`:
  - `runner.go` — capture/filter/print skeleton (`RunFiltered`,
    `RunFilteredWithExit`, `RunPassthrough`, `RunOptions`, tee-on-failure hint).
  - `util.go` — `StripANSI`, Windows-aware `ResolvedCommand`/`ToolExists`,
    `ExitCodeFromError`, `NormalizeNewlines`, `IsTerminal`, token estimation.
  - `filter.go` — source comment/whitespace filter (`FilterLevel`, `Language`,
    minimal/aggressive, `SmartTruncate`).
  - `truncate.go` — truncation caps + `Reduced`; `NoiseDirs`.
  - `tracker.go` — JSON-lines token-savings tracking (replaces rtk's bundled
    SQLite to keep the build pure-Go/cgo-free).
  - `config.go` — TOML config loader (no telemetry section — gortk never phones
    home).
- `internal/registry/` — self-registration command registry; command packages
  register from `init()`, wired via the blank-import aggregator
  `internal/cmds/allcmds`.
- `internal/tomlfilter/` — faithful port of rtk's `toml_filter.rs` declarative
  filter engine (8-stage pipeline: strip_ansi → replace → match_output →
  strip/keep → truncate_lines_at → head/tail → max_lines → on_empty). The 58
  builtin filters are embedded via `go:embed` and **all pass their upstream
  inline test suites** in the Go engine.

### Commands — 60 registered; whole project builds/vets/tests clean

Every rtk command module is ported (one Go package per module under
`internal/cmds/`, ~50 packages, ~1,200 ported/characterization tests). Each
wraps a native tool, captures its output, and compresses it before printing.

- **File / source:** `ls`, `tree`, `find`, `read`, `grep`, `wc`, `json`,
  `format`, `smart`, `log`, `deps`, `env`, `diff`.
- **Version control:** `git` (status/log/diff/show/add/commit/push/pull/branch/
  fetch/stash/worktree + passthrough), `gh`, `glab`, `gt`.
- **Build / test / lint:** `cargo`, `go`, `golangci-lint`, `dotnet`, `mvn`,
  `gradlew`, `pytest`, `ruff`, `mypy`, `rake`, `rspec`, `rubocop`, `jest`,
  `vitest`, `tsc`, `lint`, `prettier`, `next`, `prisma`, `pnpm`, `npm`/`npx`,
  `pip`, `playwright`.
- **Cloud / containers:** `aws`, `docker`/`kubectl`/`oc`, `curl`, `wget`, `psql`.
- **Generic exec:** `err`, `test`, `proxy`, `run`, `summary`, `pipe`.
- **Hooks / integration:** `rewrite`; `hook` for **Claude Code** (PreToolUse) and
  **GitHub Copilot** (`hook copilot` auto-detects VS Code Copilot Chat
  `updatedInput` and Copilot CLI `modifiedArgs`); `init` (global Windows-native
  Claude installer) and `init --copilot` (project-scoped `.github/` installer
  writing `hooks/gortk-rewrite.json` + `copilot-instructions.md`); all with
  `--show`/`--dry-run`. Compound commands (`&&`/`||`/`;`/`|`) are rewritten
  per-segment; unattestable constructs (substitutions, heredocs, file redirects)
  pass through.
- **Operational:** `config`, `gain` (token-savings report from the JSON tracker),
  `verify` (runs all 58 builtin filters' inline tests — 144/144 pass).

Anything without a dedicated module is handled by the declarative TOML filter
engine (58 builtin filters) and otherwise passed through unchanged.

### Agent integration

- **[docs/INTEGRATION.md](docs/INTEGRATION.md)** — hand-it-to-an-LLM guide for
  wiring gortk into Claude Code, Codex, and Copilot across machines: the full
  command list, per-agent setup + validation, and the instruction block that
  stops agents bypassing the optimizer via their built-in `Read`/`Grep`/`Glob`
  tools. Codex is instruction-only (no rewrite hook exists); Claude and Copilot
  have native hooks.

### Removed vs rtk (intentional)

- Telemetry / usage ping (`core/telemetry.rs`) — omitted entirely.
- Bundled SQLite tracking — replaced with JSON-lines.
- curl-based release installer (`install.sh`) — gortk builds from source.
