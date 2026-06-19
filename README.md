# gortk

**gortk** is a CLI proxy that filters and compresses dev-tool output before it
reaches an LLM context, cutting token consumption by a large margin on common
commands (`git`, `ls`, `grep`, `cargo`, `go test`, `pytest`, `npm`, `docker`,
`kubectl`, and many more).

It is a Go port of [rtk](https://github.com/rtk-ai/rtk), reworked with two
goals:

- **Native Windows is a first-class target.** Pure Go, `CGO_ENABLED=0`, no C
  toolchain required — `go build` produces a self-contained `gortk.exe`. Tool
  resolution is `PATHEXT`-aware and output is CRLF-tolerant.
- **Offline and dependency-minimal by design.** gortk makes **no network calls
  of its own** — no telemetry, no analytics, no update checks. The only
  processes it ever spawns are the dev tools you explicitly ask it to wrap. The
  entire build has a single third-party dependency
  ([`BurntSushi/toml`](https://github.com/BurntSushi/toml)), used only to parse
  the declarative filter definitions.

## How it works

For each wrapped tool, gortk runs the native command, captures its output, and
applies a compression filter before printing — stripping ANSI, deduplicating,
grouping by file, truncating noise, and surfacing only what an LLM needs (errors,
failures, summaries). Tools without a dedicated module are handled by a
**declarative TOML filter engine** (see `internal/tomlfilter/builtin/`), and
anything unrecognized is passed through unchanged.

```
gortk ls            # compact directory listing
gortk git status    # condensed status
gortk grep TODO src # grouped, truncated matches
gortk go test ./... # failures only
gortk <anything>    # passthrough if no filter applies
```

`gortk rewrite "<cmd>"` returns the gortk-wrapped equivalent of a raw command,
and `gortk hook claude` / `gortk init` wire it into an LLM coding agent's
pre-tool hook so wrapping happens automatically.

## Build

Requires Go 1.23+.

```
go build -o bin/gortk.exe ./cmd/gortk      # Windows
go build -o bin/gortk     ./cmd/gortk      # Linux/macOS
go test ./...
```

## Project layout

```
cmd/gortk/            CLI entry point (stdlib dispatch via the command registry)
internal/core/        execution skeleton, output capture, token tracking, source filter
internal/registry/    self-registration command registry
internal/tomlfilter/  declarative TOML filter engine + embedded builtin filters
internal/cmds/<name>/ one package per wrapped command (mirrors rtk's src/cmds/*)
docs/PORTING_CONTRACT.md  the contract every command port follows
```

## Differences from rtk

- No telemetry and no SQLite dependency. Usage stats (for `gortk gain`) are kept
  in a small JSON-lines file under the user config dir instead of a bundled
  SQLite database, so the binary stays pure-Go and cgo-free.
- Hooks/installer are focused on native Windows and Claude Code first.

## License

Apache License 2.0 — see [LICENSE](LICENSE) and [NOTICE](NOTICE). gortk is a
derivative work of rtk, which is also Apache-2.0 licensed.
