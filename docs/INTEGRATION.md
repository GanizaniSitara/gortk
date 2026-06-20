# Integrating gortk with coding agents (Claude Code, Codex, Copilot)

This guide is written to be **handed to an LLM agent** on a new machine: it can
read it and wire gortk up itself, then validate it. It covers all three agents,
the full command list, and the one thing everybody gets wrong — agents
**bypassing** the optimizer via their built-in tools.

> TL;DR — gortk saves tokens two ways, and you need **both**:
> 1. **Shell hook** (Claude only today): auto-rewrites shell commands like
>    `git status` → `gortk git status`.
> 2. **Instructions** (all agents): tell the agent to *prefer gortk commands*,
>    especially for file read/search/list, because the agent's built-in
>    `Read`/`Grep`/`Glob` tools **never touch the shell and so bypass the hook**.

---

## 0. Mental model: two layers, because a hook isn't enough

A PreToolUse hook can only see **shell/Bash tool calls**. But coding agents
read files and search code with *built-in* tools (`Read`, `Grep`, `Glob`,
`Edit`, file-search) that run in-process and never spawn a shell. Those calls
are invisible to any hook. So:

| Layer | Catches | Works on |
|---|---|---|
| **Hook** (`gortk hook claude` / `gortk hook copilot`) | shell commands (`git`, `go test`, `npm`, …) | Claude Code (native, global) **and** GitHub Copilot (native, project-scoped — VS Code + CLI). Codex: no rewrite hook exists. |
| **Instructions** (CLAUDE.md / AGENTS.md / copilot-instructions) | the rest — built-in `Read`/`Grep`/`Glob`, and *all* commands on agents without a hook | every agent |

Rule of thumb: **the hook is a convenience for Claude; the instructions are what
actually deliver savings everywhere.**

---

## 1. Build & install gortk

Requires Go 1.23+. Pure Go, `CGO_ENABLED=0` — no C toolchain.

**Windows (primary target):**
```bat
git clone https://github.com/GanizaniSitara/gortk %USERPROFILE%\src\gortk
cd %USERPROFILE%\src\gortk
go build -o gortk.exe ./cmd/gortk
:: put it on PATH — any dir already on PATH works, e.g.:
copy gortk.exe "%LOCALAPPDATA%\Microsoft\WindowsApps\gortk.exe"   REM (already on PATH on Win10/11)
gortk --version
```

**macOS / Linux:**
```sh
git clone https://github.com/GanizaniSitara/gortk ~/src/gortk
cd ~/src/gortk
go build -o gortk ./cmd/gortk
install -m755 gortk ~/.local/bin/gortk     # ensure ~/.local/bin is on PATH
gortk --version
```

Validate: `gortk --version` prints a version, and `gortk rewrite "git status"`
prints `gortk git status`. If both work, gortk is installed and on PATH.

---

## 2. The command list (what gets optimized)

60 dedicated commands. Anything **not** listed is passed through unchanged, so
it is always safe to prefix a command with `gortk`.

**Version control:** `git` (status/log/diff/show/add/commit/push/pull/branch/
fetch/stash/worktree) · `gh` · `glab` · `gt` · `diff`

**Build / test / lint:** `go` · `cargo` · `dotnet` · `mvn` · `gradlew` ·
`golangci-lint` · `pytest` · `ruff` · `mypy` · `pip` · `rake` · `rspec` ·
`rubocop` · `jest` · `vitest` · `tsc` · `lint` · `prettier` · `next` · `prisma` ·
`pnpm` · `npm` · `npx` · `playwright`

**Cloud / containers:** `aws` · `docker` · `kubectl` · `oc` · `curl` · `wget` ·
`psql`

**Files & search — use these INSTEAD of built-in Read/Grep/Glob:** `read` ·
`grep` · `ls` · `tree` · `find` · `wc` · `json`

**Other readers:** `deps` · `env` · `log` · `smart` · `summary`

**Generic wrappers:** `err` · `test` · `proxy` · `run` · `pipe`

**Control / meta (you run these, not the agent):** `init` · `rewrite` · `hook` ·
`config` · `gain` · `verify`

Plus **58 declarative TOML filters** for tools without a dedicated module
(`make jq helm df ps gcc terraform-plan ssh systemctl-status xcodebuild …`),
applied automatically. Run `gortk --help` for the live list and `gortk verify`
to confirm all builtin filters pass their tests.

---

## 3. Wire up each agent

### 3a. Claude Code — native hook + instructions

**Hook (Windows):**
```bat
gortk init --dry-run     :: preview; writes nothing
gortk init               :: writes ~/.claude/hooks/gortk-hook.cmd and patches ~/.claude/settings.json
gortk init --show        :: confirm "hook entry: installed"
```
`gortk init` is idempotent, preserves your other settings, and backs up
`settings.json.bak`. **Restart Claude Code to apply.**

**Hook (macOS/Linux — no installer yet, add it manually)** to
`~/.claude/settings.json`:
```json
{
  "hooks": {
    "PreToolUse": [
      { "matcher": "Bash",
        "hooks": [ { "type": "command", "command": "gortk hook claude" } ] }
    ]
  }
}
```

**Instructions:** append the block from [§4](#4-the-instruction-block-prevents-bypass)
to your `CLAUDE.md` (global `~/.claude/CLAUDE.md` or per-project).

### 3b. Codex CLI — instructions only

Codex has **no command-rewrite hook**; it reads `~/.codex/AGENTS.md` (global)
or `AGENTS.md` in the project. Append the block from §4 there. On Codex the
agent must **prefix `gortk` itself** (there's no hook backstop), so the
instruction wording for "always prefix gortk" is what does the work.

### 3c. GitHub Copilot — native hook (project-scoped) + instructions

gortk has a native Copilot hook covering **both** VS Code Copilot Chat and the
GitHub Copilot CLI. Copilot reads hook/instruction config from a project's
`.github/`, so the installer is **per-repo**:
```sh
cd <your-repo>
gortk init --copilot --dry-run    # preview; writes nothing
gortk init --copilot              # writes .github/hooks/gortk-rewrite.json + .github/copilot-instructions.md
```
The hook config carries both schemas — VS Code's `PreToolUse` (transparent
`updatedInput` rewrite) and the Copilot CLI's `preToolUse` (`modifiedArgs`
rewrite) — both invoking `gortk hook copilot`, alongside the
`copilot-instructions.md` block from §4. **Restart the IDE / Copilot CLI session
to activate**, then validate with the §5 hook test.

Run it once per repo, or commit the generated `.github/` files to share the
integration with your team.

---

## 4. The instruction block (prevents bypass)

Paste this into the agent's instruction file (`CLAUDE.md` / `AGENTS.md` /
`.github/copilot-instructions.md`). This is the part that stops the agent from
bypassing gortk with its built-in tools.

```markdown
## gortk — token-optimized commands (prefer these)

`gortk` is installed here. It compresses verbose tool output before it reaches
you, cutting 60–90% of the tokens. Use it:

- **Run supported commands through gortk.** Prefer `gortk git status`,
  `gortk go test ./...`, `gortk cargo build`, `gortk npm test`, `gortk docker ps`,
  etc. Unknown commands pass through unchanged, so prefixing `gortk` is always
  safe. (On Claude Code a hook does this automatically; on Codex/Copilot you
  must add the `gortk` prefix yourself.)
- **For reading and searching, use gortk instead of your built-in tools** —
  these are the ones your Read/Grep/Glob tools would otherwise bypass:
  - read a file:        `gortk read <file>`     (not the Read tool, esp. large/source files)
  - search code:        `gortk grep <pattern> <path>`  (not Grep)
  - list a directory:   `gortk ls <dir>`  /  `gortk tree <dir>`  (not Glob)
  - find files:         `gortk find <dir> -name '<glob>'`
  - inspect JSON:       `gortk json <file>`     count lines: `gortk wc <file>`
- Use `gortk` for build/test/lint/VCS/cloud commands; it shows errors/failures/
  summaries and strips the passing-noise.
- Never prefix `gortk` onto an interactive command or one whose raw output you
  must parse byte-for-byte; for those, run the tool directly.
```

### Optional: hard-stop the bypass (Claude Code)

Instructions are a *nudge*. To force the agent off its built-in readers for big
files/searches, deny them in `~/.claude/settings.json` so it must shell out
(and the hook/instructions then route it through gortk):
```json
{ "permissions": { "deny": ["Read", "Grep", "Glob"] } }
```
This is aggressive (the built-ins are good) — most users prefer the instruction
nudge and reserve `deny` for token-critical workflows.

### Protect specific commands from rewriting

If automation parses a tool's raw output, exclude it (the hook will pass it
through untouched):
```bat
gortk config --create
```
then edit `%APPDATA%\gortk\config.toml` (or `~/.config/gortk/config.toml`):
```toml
[hooks]
exclude_commands = ["git push", "npm"]
```

---

## 5. Validate (per machine)

Run these to confirm a working install:

```sh
gortk --version                       # installed + on PATH
gortk rewrite "git status"            # -> "gortk git status"  (rewrite engine works)
gortk verify                          # -> "144 tests, 144 passed, 0 failed"  (filters intact)
```

**Claude hook end-to-end** (proves the PreToolUse wiring without a restart):
```sh
echo '{"tool_name":"Bash","tool_input":{"command":"git status"}}' | gortk hook claude
# expect: {"hookSpecificOutput":{...,"updatedInput":{"command":"gortk git status"}}}
gortk init --show                     # "hook entry: installed"
```

**Copilot hook end-to-end** (both formats):
```sh
# VS Code Copilot Chat (snake_case -> updatedInput):
echo '{"tool_name":"runTerminalCommand","tool_input":{"command":"git status"}}' | gortk hook copilot
# Copilot CLI (camelCase, toolArgs is a JSON string -> modifiedArgs):
echo '{"toolName":"bash","toolArgs":"{\"command\":\"git status\"}"}' | gortk hook copilot
```
Then restart the agent and run a verbose command (`go test ./...`, `git log`),
and confirm the output is compact.

**Prove the savings** — compare raw vs gortk and check the running tally:
```sh
# pick any verbose command, e.g. a test run:
go test -v ./... 2>&1 | wc -c         # raw size
gortk go test ./... 2>&1 | wc -c      # compressed size
gortk gain                            # cumulative tokens saved across your sessions
```

---

## 6. Rollback

**Claude hook:**
```bat
copy /Y "%USERPROFILE%\.claude\settings.json.bak" "%USERPROFILE%\.claude\settings.json"
del "%USERPROFILE%\.claude\hooks\gortk-hook.cmd"
```
(macOS/Linux: remove the `PreToolUse` entry you added.)

**Instructions:** delete the gortk block from the instruction file.

**Binary:** delete `gortk` from wherever you installed it.

---

## 7. Known gaps (so the doc stays honest)

- Native rewrite hooks exist for **Claude Code** (global) and **GitHub Copilot**
  (project-scoped, VS Code + CLI). **Codex** has no command-rewrite hook
  (`gortk hook codex` is not implemented) — it is instruction-driven via
  `AGENTS.md`.
- The `gortk init` installer writes a Windows `.cmd` launcher; on macOS/Linux,
  add the hook to `settings.json` manually (see §3a).
- `gortk gain` records commands that flow through the shared runner; some
  direct-exec command modules may under-report. The raw-vs-gortk comparison in
  §5 is the ground truth for savings.
