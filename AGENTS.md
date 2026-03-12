# Repository Guidelines

## Project Structure & Module Organization
`gbash` is a small Go workspace centered on a deterministic shell runtime. Use [`cmd/gbash/`](/Users/ewhauser/working/cadencerpm/gbash/cmd/gbash) for the CLI entrypoint, [`runtime/`](/Users/ewhauser/working/cadencerpm/gbash/runtime) for orchestration and execution results, [`shell/`](/Users/ewhauser/working/cadencerpm/gbash/shell) for `mvdan/sh` integration, [`commands/`](/Users/ewhauser/working/cadencerpm/gbash/commands) for built-in core command implementations, [`contrib/`](/Users/ewhauser/working/cadencerpm/jbgo/contrib) for optional heavyweight command modules, and [`fs/`](/Users/ewhauser/working/cadencerpm/gbash/fs) for the virtual filesystem. Policy and tracing live in [`policy/`](/Users/ewhauser/working/cadencerpm/gbash/policy) and [`trace/`](/Users/ewhauser/working/cadencerpm/gbash/trace). Read [`SPEC.md`](/Users/ewhauser/working/cadencerpm/gbash/SPEC.md) before changing runtime boundaries or sandbox behavior.

## Build, Test, and Development Commands
Use Go 1.25+.

- `go build ./... ./contrib/sqlite3/... ./examples/...` builds all packages and catches compile-time regressions.
- `go test ./... ./contrib/sqlite3/... ./examples/...` runs the full test suite across the workspace modules.
- `go test ./runtime -run TestRunSimpleScript` runs a focused sanity check while iterating.
- `go run ./cmd/gbash < script.sh` executes a shell snippet through the local CLI.
- `gofmt -w .` formats the repository with standard Go tooling before review.

## Coding Style & Naming Conventions
Follow idiomatic Go. Let `gofmt` control formatting; do not hand-align code. Package names stay short and lowercase (`runtime`, `trace`). Exported types and constructors use `PascalCase`; unexported helpers use `camelCase`. Keep command implementations narrow and explicit, matching the registry pattern in `commands/`. Preserve the project rule that unknown commands never fall through to the host OS.

## Testing Guidelines
Add table-driven tests beside the package they cover, using `*_test.go` files and `TestXxx` names. Prefer black-box behavior checks over implementation-detail assertions. For runtime changes, cover exit codes, stdout/stderr, and sandboxed filesystem effects. If a change touches shell semantics or policy enforcement, add at least one regression test in [`runtime/runtime_test.go`](/Users/ewhauser/working/cadencerpm/gbash/runtime/runtime_test.go) or a new package-local test file.

## SPEC Sync Rules
`SPEC.md` is the repository's product and architecture contract. When a user asks for a new feature, expanded scope, or a behavior change that affects runtime boundaries, supported commands, policy, tracing, filesystem behavior, or roadmap assumptions, update [`SPEC.md`](/Users/ewhauser/working/cadencerpm/gbash/SPEC.md) in the same turn.

Treat SPEC updates as required when:

- adding or removing built-in commands
- changing sandbox guarantees or policy defaults
- changing filesystem abstractions or backend expectations
- changing `mvdan/sh` integration strategy
- expanding MVP scope or roadmap commitments
- introducing new public packages, interfaces, or execution modes

When doing feature work, keep the spec in sync by:

- reading the relevant `SPEC.md` sections before editing code
- updating the impacted sections after the design is clear
- keeping implementation details and spec language aligned
- noting the SPEC update in the final summary when one was required

If a request is intentionally small and does not change the documented contract, architecture, or scope, `SPEC.md` does not need churn. When in doubt, prefer a small SPEC update over silent drift.

## Skills

- **release** — Manages the tag-driven GitHub release workflow: validates release readiness, runs GoReleaser checks/snapshots, cuts SemVer tags, and troubleshoots failures. Located at `.agents/skills/release/`.
- **upstream-diff** — Diffs this repo against the upstream [vercel-labs/just-bash](https://github.com/vercel-labs/just-bash) TypeScript repo to find missing commands and flags. Run it to refresh the `## Command Parity` section in `TODO.md`. Located at `.claude/skills/upstream-diff/`.

## Commit & Pull Request Guidelines
This checkout does not include `.git` history, so no local commit convention can be derived. Use short, imperative commit subjects such as `runtime: normalize command-not-found errors`. Keep commits scoped to one change. PRs should explain user-visible behavior, list commands run (`go test ./... ./contrib/sqlite3/... ./examples/...`), and note any SPEC updates. Include trace or CLI output when changing execution behavior.
