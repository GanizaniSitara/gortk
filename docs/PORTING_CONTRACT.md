# gortk porting contract (READ THIS FIRST)

You are porting one command module from the Rust project **rtk**
(`C:\git-external\rtk`) into the Go project **gortk** (`C:\git\gortk`). The core
foundation is already built, compiles, and is frozen. Your job: produce ONE Go
package that mirrors the Rust module's behaviour exactly, with ported tests, and
that **compiles and passes `go test` on its own** before you finish.

## Hard rules (non-negotiable)

1. **Pure Go standard library only.** The ONLY third-party dependency allowed in
   the whole project is `github.com/BurntSushi/toml` (already present, used only
   by the core). Do **NOT** run `go get`, do **NOT** add imports of anything
   outside the stdlib + `gortk/internal/*`. Regex → `regexp`. JSON →
   `encoding/json`. Everything you need is in the stdlib.
2. **No telemetry, no network, no phone-home.** gortk is offline by default. The
   only process you may spawn is the dev tool the command wraps (via
   `core.ResolvedCommand`). Never add update checks, analytics, or HTTP calls.
   If the Rust source has telemetry/tracking side-channels, drop them.
3. **Native Windows is the target.** Use `core.ResolvedCommand` (PATHEXT-aware)
   to find tools, `path/filepath` for paths, and tolerate CRLF (the runner and
   the TOML engine already normalize, but if you parse captured text yourself,
   call `core.NormalizeNewlines`). No `sh -c`, no hardcoded `/usr/bin`, no
   reliance on Unix-only syscalls. `#[cfg(unix)]` blocks generally become no-ops.
4. **Write ONLY inside your own package directory**
   `C:\git\gortk\internal\cmds\<pkg>\`. Do not edit core, registry, main.go,
   go.mod, go.sum, or any other package. (The blank-import wiring is generated
   for you later.)
5. **Do not finish until your package builds and tests pass.** Run, from
   `C:\git\gortk`:
   - `go -C C:/git/gortk build ./internal/cmds/<pkg>/...`
   - `go -C C:/git/gortk vet ./internal/cmds/<pkg>/...`
   - `go -C C:/git/gortk test ./internal/cmds/<pkg>/...`
   Fix everything until all three are clean.

## Package layout & registration

Create `internal/cmds/<pkg>/<pkg>.go`. Register each command from `init()`:

```go
package mycmd

import "gortk/internal/registry"

func init() {
    registry.Register(&registry.Cmd{
        Name:    "mycmd",            // the gortk subcommand, e.g. "git", "pytest"
        Aliases: []string{"alias"},  // optional
        Summary: "one-line help",
        Run:     Run,
    })
}

// Run receives the args AFTER the command name, plus the -v count.
// It returns the process exit code.
func Run(args []string, verbose int) (int, error) { ... }
```

Command names MUST be unique across the whole project. If your module exposes
several subcommands (e.g. `container.rs` → `docker`, `kubectl`, `oc`), register
each as its own `registry.Cmd` from the same `init()`.

Commands that have sub-subcommands (e.g. `git status`, `cargo test`) parse those
themselves from `args` — there is no nested command framework. Look at how the
Rust `main.rs` dispatches your command to see the subcommand shape, then handle
it inside `Run`.

## Core API you build on (package `gortk/internal/core`)

- `ResolvedCommand(name string, args ...string) *exec.Cmd` — PATHEXT-aware.
- `RunFiltered(cmd, tool, argsDisplay string, filter func(raw string) string, opts RunOptions) (int, error)`
- `RunFilteredWithExit(cmd, tool, argsDisplay string, filter func(raw string, exit int) string, opts RunOptions) (int, error)`
- `RunPassthrough(tool string, args []string, verbose int) (int, error)`
- `RunOptions{TeeLabel, FilterStdoutOnly, SkipFilterOnFailure, NoTrailingNewline, InheritStdin}`
- `StripANSI(s) string`, `NormalizeNewlines(s) string`, `IsTerminal(*os.File) bool`
- `EstimateTokens(s) int`, `FormatCount(n) string`
- Truncation caps: `CapErrors=20, CapWarnings=10, CapList=20, CapInventory=50`,
  `Reduced(cap, by) int`, `SmartTruncate(content, maxLines) string`.
- Source filter: `FilterLevel`, `ParseFilterLevel`, `Language`, `LanguageFromExt`,
  `FilterSource(content, lang, level) string`.
- `NoiseDirs []string`, `IsNoiseDir(name) bool`.

The Rust `runner::run_filtered(cmd, tool, args_display, |raw| {...}, opts)` maps
directly to `core.RunFiltered`. The closure that compresses output is the heart
of each command — port it as a **pure function** and test it directly.

## The reference implementation

`internal/cmds/ls/ls.go` and `internal/cmds/ls/ls_test.go` are a complete,
idiomatic reference port of `src/cmds/system/ls.rs`. **Mirror its structure**:
thin `Run` that builds argv + calls `core.RunFiltered`, with the compression
logic in pure helper functions, and a `_test.go` that ports the Rust
`#[cfg(test)]` cases as Go table tests.

## Tests — port them, they are the spec

The Rust module's `#[cfg(test)] mod tests { ... }` cases are the behavioural
spec. Port every one you reasonably can as a Go test against your pure helper
functions (not against live process execution). Matching them is how we verify
the port is faithful. Use the same inputs/expected outputs.

## Idiom notes (Rust → Go)

- `Result<i32>` → `(int, error)`. `anyhow::Result` → return `error`.
- `Option<T>` → pointer or `(T, bool)`.
- `Vec<String>` → `[]string`; `HashMap` → `map`; `lazy_static! Regex` →
  package-level `var re = regexp.MustCompile(...)`.
- `s.lines()` drops a trailing empty line; `strings.Split(s, "\n")` does not —
  trim a trailing `""` element when mirroring `.lines()` semantics.
- Keep comments meaningful; match the surrounding Go style in the core packages.
