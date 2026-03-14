# Repository Guidelines

## Project Overview
`gbash` is a Go workspace for a deterministic shell runtime. `contrib/` houses optional heavyweight modules (`sqlite3`, `jq`, `yq`, `extras`). Read `SPEC.md` before changing runtime boundaries or sandbox behavior.

## Build & Test
Use Go 1.26+. The full workspace build/test command spans multiple modules:

```sh
go build ./... ./contrib/extras/... ./contrib/sqlite3/... ./contrib/jq/... ./contrib/yq/... ./examples/...
go test ./... ./contrib/extras/... ./contrib/sqlite3/... ./contrib/jq/... ./contrib/yq/... ./examples/...
```

Before submitting or updating a PR, run `make lint` from the repo root and fix any reported issues.

## Key Project Rules
- Unknown commands must never fall through to the host OS.
- Match the registry pattern in `commands/` when adding new built-in commands.
- For runtime changes, test exit codes, stdout/stderr, and sandboxed filesystem effects.
- If a change touches shell semantics or policy, add a regression test in `runtime/` or the relevant package.

## SPEC Sync
`SPEC.md` is the product and architecture contract. Update it in the same turn when:
- adding/removing built-in commands
- changing sandbox guarantees, policy defaults, or filesystem abstractions
- changing `mvdan/sh` integration strategy
- expanding scope, roadmap, or introducing new public packages/interfaces

Read the relevant `SPEC.md` sections before editing code, and update them once the design is clear. When in doubt, prefer a small SPEC update over silent drift.

## Skills
- **command** — Guide for adding or modifying built-in commands. Located at `.agents/skills/command/`.
- **implement-coreutils-command** — Port a command from uutils/coreutils Rust repo into Go. Located at `.agents/skills/implement-coreutils-command/`.
- **release** — Tag-driven GitHub release workflow with GoReleaser. Located at `.agents/skills/release/`.
- **upstream-diff** — Diff against upstream [vercel-labs/just-bash](https://github.com/vercel-labs/just-bash) to find missing commands/flags. Located at `.claude/skills/upstream-diff/`.

## Commits & PRs
Use short, imperative subjects scoped to one change (e.g., `runtime: normalize command-not-found errors`). PRs should explain user-visible behavior, note any SPEC updates, include trace/CLI output when changing execution behavior, and only be submitted after a clean local `make lint`.
