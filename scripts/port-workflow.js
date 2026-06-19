export const meta = {
  name: 'gortk-port',
  description: 'Port all rtk command modules to gortk Go packages, in parallel, against the frozen core contract',
  phases: [
    { title: 'Port', detail: 'one agent per command module: port + self-verify build/test' },
    { title: 'Integrate', detail: 'generate blank-import wiring, full build/vet/test' },
    { title: 'Repair', detail: 'fix any package that fails the full build/test' },
    { title: 'Final', detail: 'final full build/test/help verification' },
  ],
}

const RTK = 'C:/git-external/rtk/src/cmds'
const CONTRACT = 'C:/git/gortk/docs/PORTING_CONTRACT.md'

// Each group becomes one Go package under internal/cmds/<pkg>/, ported by one agent.
// NOTE: git, grep, cargo, gocmd (and the python set: pytest/ruff/mypy/pip) were
// ported sequentially before switching to the workflow, so they are not listed
// here. The integrate phase discovers and wires ALL packages under internal/cmds.
const GROUPS = [
  // git family (large)
  { pkg: 'gh', cmds: 'gh (pr/issue/run/repo subcommands)', src: ['git/gh_cmd.rs'], variant: 'Commands::Gh', effort: 'high' },
  { pkg: 'glab', cmds: 'glab (mr/issue/ci/pipeline/api)', src: ['git/glab_cmd.rs'], variant: 'Commands::Glab', effort: 'high' },
  { pkg: 'gt', cmds: 'gt (graphite: log/submit/sync/restack/create/branch + passthrough)', src: ['git/gt_cmd.rs'], variant: 'Commands::Gt', effort: 'medium' },
  // cloud
  { pkg: 'aws', cmds: 'aws (force JSON, compress)', src: ['cloud/aws_cmd.rs'], variant: 'Commands::Aws', effort: 'high' },
  { pkg: 'container', cmds: 'docker, kubectl, oc (register all three)', src: ['cloud/container.rs'], variant: 'Commands::Docker / Kubectl / Oc', effort: 'high' },
  { pkg: 'curl', cmds: 'curl (auto-JSON detection, schema output)', src: ['cloud/curl_cmd.rs'], variant: 'Commands::Curl', effort: 'medium' },
  { pkg: 'psql', cmds: 'psql (strip borders, compress tables)', src: ['cloud/psql_cmd.rs'], variant: 'Commands::Psql', effort: 'medium' },
  { pkg: 'wget', cmds: 'wget (strip progress bars)', src: ['cloud/wget_cmd.rs'], variant: 'Commands::Wget', effort: 'medium' },
  // dotnet (one package, four source files)
  { pkg: 'dotnet', cmds: 'dotnet (build/test/restore/format + passthrough)', src: ['dotnet/dotnet_cmd.rs', 'dotnet/binlog.rs', 'dotnet/dotnet_format_report.rs', 'dotnet/dotnet_trx.rs'], variant: 'Commands::Dotnet', effort: 'high' },
  // go
  { pkg: 'golangci', cmds: 'golangci-lint (command Name is "golangci-lint"). Also port rtk go_cmd.rs `go tool golangci-lint` interception note if relevant.', src: ['go/golangci_cmd.rs'], variant: 'Commands::GolangciLint', effort: 'high' },
  // js
  { pkg: 'lint', cmds: 'lint (eslint, grouped rule violations)', src: ['js/lint_cmd.rs'], variant: 'Commands::Lint', effort: 'high' },
  { pkg: 'next', cmds: 'next (Next.js build)', src: ['js/next_cmd.rs'], variant: 'Commands::Next', effort: 'medium' },
  { pkg: 'npm', cmds: 'npm and npx (register both)', src: ['js/npm_cmd.rs'], variant: 'Commands::Npm / Npx', effort: 'medium' },
  { pkg: 'playwright', cmds: 'playwright', src: ['js/playwright_cmd.rs'], variant: 'Commands::Playwright', effort: 'medium' },
  { pkg: 'pnpm', cmds: 'pnpm (list/outdated/install/typecheck + passthrough)', src: ['js/pnpm_cmd.rs'], variant: 'Commands::Pnpm', effort: 'high' },
  { pkg: 'prettier', cmds: 'prettier', src: ['js/prettier_cmd.rs'], variant: 'Commands::Prettier', effort: 'medium' },
  { pkg: 'prisma', cmds: 'prisma (generate/migrate/db-push)', src: ['js/prisma_cmd.rs'], variant: 'Commands::Prisma', effort: 'medium' },
  { pkg: 'tsc', cmds: 'tsc (grouped error output)', src: ['js/tsc_cmd.rs'], variant: 'Commands::Tsc', effort: 'medium' },
  { pkg: 'vitest', cmds: 'vitest, and jest if this module handles it (check main.rs Commands::Jest)', src: ['js/vitest_cmd.rs'], variant: 'Commands::Vitest / Jest', effort: 'medium' },
  // jvm
  { pkg: 'gradlew', cmds: 'gradlew (Android Gradle wrapper)', src: ['jvm/gradlew_cmd.rs'], variant: 'Commands::Gradlew', effort: 'high' },
  { pkg: 'mvn', cmds: 'mvn (Maven wrapper)', src: ['jvm/mvn_cmd.rs'], variant: 'Commands::Mvn', effort: 'high' },
  // ruby
  { pkg: 'rake', cmds: 'rake (Minitest output)', src: ['ruby/rake_cmd.rs'], variant: 'Commands::Rake', effort: 'medium' },
  { pkg: 'rspec', cmds: 'rspec', src: ['ruby/rspec_cmd.rs'], variant: 'Commands::Rspec', effort: 'high' },
  { pkg: 'rubocop', cmds: 'rubocop', src: ['ruby/rubocop_cmd.rs'], variant: 'Commands::Rubocop', effort: 'medium' },
  // generic exec wrappers (rust/runner.rs backs the top-level err & test commands)
  { pkg: 'execwrap', cmds: 'err (run a command, show only errors/warnings), test (run tests, show only failures), proxy (exec + track usage, no filtering), run (raw sh-style exec, no filter/track). Register Names: err, test, proxy, run.', src: ['rust/runner.rs'], variant: 'Commands::Err / Test / Proxy / Run', effort: 'high' },
  // system
  { pkg: 'deps', cmds: 'deps (summarize project dependencies)', src: ['system/deps.rs'], variant: 'Commands::Deps', effort: 'medium' },
  { pkg: 'envcmd', cmds: 'env (filtered, sensitive masked) — command Name is "env"', src: ['system/env_cmd.rs'], variant: 'Commands::Env', effort: 'medium' },
  { pkg: 'find', cmds: 'find (compact tree output)', src: ['system/find_cmd.rs'], variant: 'Commands::Find', effort: 'high' },
  { pkg: 'format', cmds: 'format (universal format checker)', src: ['system/format_cmd.rs'], variant: 'Commands::Format', effort: 'medium' },
  { pkg: 'jsoncmd', cmds: 'json (compact values / keys-only) — command Name is "json"', src: ['system/json_cmd.rs'], variant: 'Commands::Json', effort: 'medium' },
  { pkg: 'smart', cmds: 'smart (2-line heuristic summary of a file) — command Name is "smart". Port the heuristic path only; drop any model-download/network code.', src: ['system/local_llm.rs'], variant: 'Commands::Smart', effort: 'medium' },
  { pkg: 'logcmd', cmds: 'log (filter + dedup log output) — command Name is "log"', src: ['system/log_cmd.rs'], variant: 'Commands::Log', effort: 'medium' },
  { pkg: 'pipe', cmds: 'pipe (read stdin, apply named filter, print) — uses tomlfilter + builtin filter names', src: ['system/pipe_cmd.rs'], variant: 'Commands::Pipe', effort: 'high' },
  { pkg: 'readcmd', cmds: 'read (intelligent file read) — command Name is "read"; use core.FilterSource / core.SmartTruncate', src: ['system/read.rs'], variant: 'Commands::Read', effort: 'medium' },
  { pkg: 'summary', cmds: 'summary (run command, heuristic summary)', src: ['system/summary.rs'], variant: 'Commands::Summary', effort: 'medium' },
  { pkg: 'tree', cmds: 'tree (directory tree, token-optimized)', src: ['system/tree.rs'], variant: 'Commands::Tree', effort: 'medium' },
  { pkg: 'wc', cmds: 'wc (word/line/byte count, strip paths/padding)', src: ['system/wc_cmd.rs'], variant: 'Commands::Wc', effort: 'medium' },
]

function portPrompt(g) {
  const srcList = g.src.map(s => `  - ${RTK}/${s}`).join('\n')
  return `Port one rtk command module into the gortk Go package \`${g.pkg}\` (directory C:/git/gortk/internal/cmds/${g.pkg}/).

STEP 1 — read these files IN FULL before writing anything:
  - ${CONTRACT}            (the porting rules — follow them exactly)
  - C:/git/gortk/internal/cmds/ls/ls.go      (reference port — mirror its shape)
  - C:/git/gortk/internal/cmds/ls/ls_test.go (reference test port)
  - C:/git/gortk/internal/core/runner.go and C:/git/gortk/internal/core/util.go (the API you call)
Rust source to port:
${srcList}
Also skim C:/git-external/rtk/src/main.rs around ${g.variant} to see exactly how args/subcommands are dispatched to this module.

STEP 2 — implement command(s): ${g.cmds}.
  - Create C:/git/gortk/internal/cmds/${g.pkg}/${g.pkg}.go (split into more .go files in the SAME package if large).
  - Register each command from init() via registry.Register (unique Name, matching the names rtk's main.rs uses).
  - Put the output-compression logic in pure functions and PORT THE RUST #[cfg(test)] TESTS into ${g.pkg}_test.go (table tests against those pure functions). The Rust tests are the behavioural spec — match their inputs/expected.

STEP 3 — make it green. Run and fix until all three are clean:
  go -C C:/git/gortk build ./internal/cmds/${g.pkg}/...
  go -C C:/git/gortk vet ./internal/cmds/${g.pkg}/...
  go -C C:/git/gortk test ./internal/cmds/${g.pkg}/...

CONSTRAINTS (from the contract): pure Go stdlib + gortk/internal/* only — NO go get, NO new dependencies; no telemetry/network/update-checks (drop them if present in the Rust); native Windows (use core.ResolvedCommand, path/filepath, core.NormalizeNewlines); write ONLY inside internal/cmds/${g.pkg}/. Do NOT edit core, registry, main.go, go.mod, or any other package.

Return the structured result.`
}

const PORT_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  properties: {
    pkg: { type: 'string' },
    commands: { type: 'array', items: { type: 'string' } },
    buildOK: { type: 'boolean' },
    vetOK: { type: 'boolean' },
    testOK: { type: 'boolean' },
    testsPassing: { type: 'integer' },
    testsTotal: { type: 'integer' },
    notes: { type: 'string', description: 'anything skipped, simplified, or needing follow-up' },
  },
  required: ['pkg', 'commands', 'buildOK', 'vetOK', 'testOK', 'notes'],
}

// ---- extra non-command packages (hooks + meta) ----
const HOOKS_PROMPT = `Port rtk's hook/rewrite system into a gortk package \`hooks\` (C:/git/gortk/internal/cmds/hooks/), adapted to be NATIVE WINDOWS and offline.

Read first: ${CONTRACT}; C:/git/gortk/internal/cmds/ls/ls.go; C:/git/gortk/internal/registry/registry.go; C:/git/gortk/internal/tomlfilter/engine.go; and these rust sources:
  - C:/git-external/rtk/src/hooks/rewrite_cmd.rs
  - C:/git-external/rtk/src/hooks/hook_cmd.rs
  - C:/git-external/rtk/src/main.rs  (Commands::Rewrite, Commands::Hook, Commands::Init — see how they work)

Implement and register these commands:
  1. "rewrite": takes a raw shell command as args (e.g. \`gortk rewrite git status\`), and prints the gortk-wrapped equivalent (e.g. \`gortk git status\`) if gortk has a dedicated command (check registry.Lookup) OR a matching tomlfilter (tomlfilter.FindMatching). Exit 0 + print rewrite if supported; exit 1 with no output if not. This is the single source of truth for hooks.
  2. "hook" with subcommand "claude": read a Claude Code PreToolUse hook JSON object from stdin, extract the bash command, rewrite it via the same logic, and print the Claude hook response JSON to stdout. Handle absent/unknown shapes gracefully (pass through = exit 0, no change).
  3. "init": a Windows-native installer that wires gortk into Claude Code. Write a hook script under the user's .claude directory and patch settings.json to call \`gortk hook claude\`. Support \`--show\` (print what it would do) and \`--dry-run\`. Keep it focused on Claude Code on Windows; you may omit the other agents (cursor/gemini/etc.) and note that.

Mirror the registry/Run contract. Pure-stdlib only (encoding/json for the hook JSON). Write ONLY in internal/cmds/hooks/. Then make these clean:
  go -C C:/git/gortk build ./internal/cmds/hooks/...
  go -C C:/git/gortk vet ./internal/cmds/hooks/...
  go -C C:/git/gortk test ./internal/cmds/hooks/...
Return the structured result (pkg="hooks").`

const META_PROMPT = `Create a gortk package \`meta\` (C:/git/gortk/internal/cmds/meta/) implementing small operational commands. Read ${CONTRACT}, the ls reference, C:/git/gortk/internal/core/tracker.go, C:/git/gortk/internal/core/config.go, and C:/git/gortk/internal/tomlfilter/engine.go first.

Implement and register:
  1. "config": with --create writes a default config.toml at core.ConfigPath(); otherwise prints the current config (read via core.LoadConfig) and core.ConfigPath().
  2. "gain": reads the JSON-lines token-tracking file at <core.DataDir()>/tracking.jsonl, aggregates raw vs out tokens, and prints a compact savings summary (total commands, tokens saved, % reduction). Support a --json flag for machine output. If the file is absent, print a friendly "no data yet" line. (gortk tracks in JSON, not SQLite — keep it simple.)
  3. "verify": runs every builtin tomlfilter's inline tests (tomlfilter.All(), each .Tests(), apply .Apply(input), compare to expected) and prints a pass/fail summary; exit non-zero if any fail. Support --filter <name> to test one.

Pure-stdlib + gortk/internal/* only. Write ONLY in internal/cmds/meta/. Add a meta_test.go with at least a smoke test for the gain aggregation math. Then make clean:
  go -C C:/git/gortk build ./internal/cmds/meta/...
  go -C C:/git/gortk vet ./internal/cmds/meta/...
  go -C C:/git/gortk test ./internal/cmds/meta/...
Return the structured result (pkg="meta").`

// =====================================================================
// PHASE 1 — port every package in parallel; each self-verifies.
// =====================================================================
phase('Port')
log(`Porting ${GROUPS.length} command packages + hooks + meta against the frozen core contract`)

const portThunks = GROUPS.map(g => () =>
  agent(portPrompt(g), { label: `port:${g.pkg}`, phase: 'Port', schema: PORT_SCHEMA, effort: g.effort })
)
portThunks.push(() => agent(HOOKS_PROMPT, { label: 'port:hooks', phase: 'Port', schema: PORT_SCHEMA, effort: 'high' }))
portThunks.push(() => agent(META_PROMPT, { label: 'port:meta', phase: 'Port', schema: PORT_SCHEMA, effort: 'medium' }))

const ported = (await parallel(portThunks)).filter(Boolean)
const greenPkgs = ported.filter(p => p.buildOK).map(p => p.pkg)
const portFailed = ported.filter(p => !p.buildOK).map(p => p.pkg)
log(`Port done: ${greenPkgs.length}/${ported.length} packages self-report building. Build-failed at port time: ${portFailed.join(', ') || 'none'}`)

// =====================================================================
// PHASE 2 — integrate: generate blank-import wiring + full build/test.
// =====================================================================
const INTEGRATE_PROMPT = `You are the integration step for the gortk Go project at C:/git/gortk.

1. Generate C:/git/gortk/internal/cmds/allcmds/all.go. It must be \`package allcmds\` and blank-import EVERY package directory directly under C:/git/gortk/internal/cmds/ that contains Go source (each such dir holds \`package <name>\`), EXCEPT the allcmds dir itself. Discover the dirs by listing internal/cmds. Import path is gortk/internal/cmds/<dir>. Keep the existing ls import. Sort the imports. Include a short package doc comment.

2. From C:/git/gortk run and capture output:
   - go -C C:/git/gortk build ./... 2>&1
   - go -C C:/git/gortk vet ./... 2>&1
   - go -C C:/git/gortk test ./... 2>&1
   - go -C C:/git/gortk run ./cmd/gortk --help   (this triggers every package init(); catches duplicate command-name panics)

3. Report results. For EACH failing package, give its import path (e.g. gortk/internal/cmds/grep) and the verbatim compiler/test/panic error lines (trimmed to what's needed to fix it). If a duplicate-registration panic occurs, name the duplicated command and the packages involved.

Do NOT modify any command package, core, registry, or main.go. ONLY create/overwrite allcmds/all.go and report. Return the structured result.`

const INTEGRATE_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  properties: {
    buildOK: { type: 'boolean' },
    vetOK: { type: 'boolean' },
    testOK: { type: 'boolean' },
    helpOK: { type: 'boolean' },
    failingPackages: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        properties: {
          pkg: { type: 'string', description: 'package dir name under internal/cmds, e.g. grep' },
          errors: { type: 'string', description: 'verbatim error lines needed to fix it' },
        },
        required: ['pkg', 'errors'],
      },
    },
    summary: { type: 'string' },
  },
  required: ['buildOK', 'vetOK', 'testOK', 'helpOK', 'failingPackages', 'summary'],
}

phase('Integrate')
let integ = await agent(INTEGRATE_PROMPT, { label: 'integrate', phase: 'Integrate', schema: INTEGRATE_SCHEMA, effort: 'medium' })
log(`Integration: build=${integ?.buildOK} vet=${integ?.vetOK} test=${integ?.testOK} help=${integ?.helpOK}; ${integ?.failingPackages?.length || 0} failing pkgs`)

// =====================================================================
// PHASE 3 — repair failing packages (up to 2 rounds).
// =====================================================================
function repairPrompt(f) {
  return `Fix the gortk package \`${f.pkg}\` (directory C:/git/gortk/internal/cmds/${f.pkg}/) so the WHOLE project builds, vets, and tests clean.

Observed failures:
${f.errors}

Read C:/git/gortk/docs/PORTING_CONTRACT.md and the files in your package. Fix within internal/cmds/${f.pkg}/ ONLY (do not touch core/registry/main/other packages/go.mod). Honour the contract: stdlib + gortk/internal/* only, no new deps, native Windows, no telemetry/network. If one ported test is genuinely intractable and blocks the build, you may relax that single test with a // TODO note rather than leave the package broken — but prefer a real fix and keep the command working.

Then verify:
  go -C C:/git/gortk build ./internal/cmds/${f.pkg}/...
  go -C C:/git/gortk vet ./internal/cmds/${f.pkg}/...
  go -C C:/git/gortk test ./internal/cmds/${f.pkg}/...
Return the structured result (pkg="${f.pkg}").`
}
const REPAIR_SCHEMA = {
  type: 'object', additionalProperties: false,
  properties: {
    pkg: { type: 'string' }, buildOK: { type: 'boolean' }, vetOK: { type: 'boolean' },
    testOK: { type: 'boolean' }, notes: { type: 'string' },
  },
  required: ['pkg', 'buildOK', 'vetOK', 'testOK', 'notes'],
}

phase('Repair')
let round = 0
while (integ && integ.failingPackages && integ.failingPackages.length > 0 && round < 2) {
  round++
  log(`Repair round ${round}: ${integ.failingPackages.map(f => f.pkg).join(', ')}`)
  await parallel(integ.failingPackages.map(f => () =>
    agent(repairPrompt(f), { label: `repair:${f.pkg}#${round}`, phase: 'Repair', schema: REPAIR_SCHEMA, effort: 'high' })
  ))
  // Re-integrate to get a fresh full-project status.
  integ = await agent(INTEGRATE_PROMPT, { label: `integrate#${round}`, phase: 'Integrate', schema: INTEGRATE_SCHEMA, effort: 'medium' })
  log(`After repair round ${round}: build=${integ?.buildOK} test=${integ?.testOK}; ${integ?.failingPackages?.length || 0} still failing`)
}

// =====================================================================
// PHASE 4 — final verification snapshot.
// =====================================================================
phase('Final')
const FINAL_PROMPT = `Do a final verification of the gortk project at C:/git/gortk. Run and capture:
  go -C C:/git/gortk build ./... 2>&1
  go -C C:/git/gortk vet ./... 2>&1
  go -C C:/git/gortk test ./... 2>&1
  go -C C:/git/gortk run ./cmd/gortk --help 2>&1   (count the dedicated commands listed)
Report whether everything is clean, the count of registered dedicated commands shown by --help, the count of packages under internal/cmds with a *_test.go, and any remaining failures verbatim. Do not modify anything. Return the structured result.`
const FINAL_SCHEMA = {
  type: 'object', additionalProperties: false,
  properties: {
    buildOK: { type: 'boolean' }, vetOK: { type: 'boolean' }, testOK: { type: 'boolean' },
    commandsRegistered: { type: 'integer' }, packagesWithTests: { type: 'integer' },
    remainingFailures: { type: 'string' },
  },
  required: ['buildOK', 'vetOK', 'testOK', 'commandsRegistered', 'remainingFailures'],
}
const final = await agent(FINAL_PROMPT, { label: 'final-verify', phase: 'Final', schema: FINAL_SCHEMA, effort: 'medium' })

return {
  portedCount: ported.length,
  portSelfGreen: greenPkgs.length,
  repairRounds: round,
  integration: integ,
  final,
}
