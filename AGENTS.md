# Repository Guidelines

## Project Overview
`gbash` is a Go workspace for a deterministic shell runtime. `contrib/` houses optional heavyweight modules (`sqlite3`, `jq`, `yq`, `extras`). Read `SPEC.md` before changing runtime boundaries or sandbox behavior.

## Build & Test
Use Go 1.26+. The full workspace build/test command spans multiple modules:

```sh
make build
make test
```

`make test` resolves the pinned GNU `diff` oracle through Nix via `./scripts/ensure-diffutils.sh`. Set `GBASH_CONFORMANCE_DIFF` if you need to point tests at an already-installed pinned `diff`.

Before submitting or updating a PR, run `make lint` from the repo root and fix any reported issues.

## Conformance & Bats Tests

Conformance tests compare gbash behavior against pinned bash and curl oracles. Bats tests validate shell scripts in `scripts/`. The `diff` oracle tests and these suites require Nix installed to fetch pinned binaries unless you provide the corresponding override env vars.

```sh
make conformance-test   # compares gbash vs pinned bash/curl oracles
make bats-test          # scripts/ test suite

# Run a single conformance test file
make conformance-test CONFORMANCE_RUN='TestConformance/bash/oils/append.test.sh'
```

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

## Commits & PRs
Use short, imperative subjects scoped to one change (e.g., `runtime: normalize command-not-found errors`). PRs should explain user-visible behavior, note any SPEC updates, include trace/CLI output when changing execution behavior, and only be submitted after a clean local `make lint`.
