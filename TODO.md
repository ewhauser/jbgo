# TODO

This file is the implementation queue derived from [`SPEC.md`](./SPEC.md), especially the updated session model, runtime roadmap, and `just-bash` gap analysis.

## Current Priority

### 1. Persistent sessions

- [x] Add a `Session` type owned by `runtime/`
- [x] Move filesystem lifetime from per-exec to per-session
- [x] Keep shell-local variables, functions, and options per-execution by default
- [x] Return `FinalEnv` from each execution
- [x] Support `ReplaceEnv` on execution requests

### 2. Default sandbox layout

- [x] Initialize `/home/agent` as the default home and working directory
- [x] Initialize `/tmp`
- [x] Initialize virtual `/bin` and `/usr/bin`
- [x] Export `PATH=/usr/bin:/bin`
- [x] Decide how virtual command paths are represented internally

### 3. Command resolution

- [x] Resolve commands by bare name
- [x] Resolve commands by virtual path such as `/bin/ls`
- [x] Make unknown-command behavior consistent for both forms
- [x] Add tests for `PATH`-based resolution

## Next Wave

### 4. Command invocation model

- [x] Extend `commands.Invocation` with a runtime-owned sub-execution callback
- [x] Allow higher-order commands to run sandboxed subcommands through that callback
- [x] Keep sub-execution inside the same session and policy boundary
- [x] Document which command patterns should use sub-exec vs direct FS access

### 5. Agent-critical commands

- [x] Implement `cp`
- [x] Implement `mv`
- [x] Implement `find`
- [x] Implement `grep`
- [x] Implement `head`
- [x] Implement `tail`
- [x] Implement `wc`
- [x] Add regression tests for each command

### 6. Policy and execution budgets

- [x] Add max command count per execution
- [x] Add loop iteration limits
- [x] Add command substitution depth limits
- [x] Add glob operation limits
- [x] Add cancellation plumbing
- [x] Add timeout handling
- [x] Define default symlink policy
- [x] Return shell-appropriate exit codes for policy denials

## Filesystem Work

### 7. Filesystem evolution

- [x] Keep the core FS interface narrow, but evaluate targeted additions for agent workflows
- [x] Add `Lstat`
- [x] Add `Readlink`
- [x] Add `Realpath`
- [x] Decide whether copy helpers belong in `fs/` or in commands
- [x] Design `OverlayFS`
- [x] Design `SnapshotFS`

### 8. Symlink and path safety

- [x] Specify how symlink traversal is handled in `MemoryFS`
- [x] Ensure path policy checks are applied before backend access
- [x] Add tests for traversal attempts and symlink edge cases

## Observability

### 9. Trace model

- [x] Add session IDs and execution IDs to trace events
- [x] Record command resolution source
- [x] Record policy denials
- [x] Record richer file mutation events
- [x] Keep trace schema stable and structured

## Testing

### 10. Expand the test corpus

- [x] Add unit tests for session lifecycle behavior
- [x] Add integration tests for multi-step session workflows
- [x] Add golden tests for stdout, stderr, exit code, and events
- [x] Add regression tests for redirects, pipelines, substitutions, loops, and conditionals
- [x] Build a curated compatibility corpus for the supported shell subset
- [x] Add determinism tests that compare repeated executions

## Post-MVP

### 11. Command-surface parity

- [x] Implement `sort`
- [x] Implement `uniq`
- [x] Implement `cut`
- [x] Implement a `sed` subset
- [x] Add file/path commands: `touch`, `rmdir`, `ln`, `chmod`, `readlink`, `stat`, `basename`, `dirname`, `tree`, `du`, `file`
- [ ] Add text/search commands: `printf`, `rg`, `awk`, `comm`, `paste`, `tr`, `rev`, `nl`, `join`, `split`, `tac`, `diff`, `base64`
- [ ] Add shell/process helpers: `tee`, `env`, `printenv`, `true`, `false`, `which`, `help`, `date`, `sleep`, `timeout`, `xargs`, `bash`, `sh`
- [ ] Add archive/data commands: `tar`, `gzip`, `gunzip`, `zcat`, `yq`, `sqlite3`

### 12. Agent-oriented data tools

- [x] Add `jq` as the first JSON-aware helper
- [ ] Expand `jq` toward full parity for modules, stream mode, and advanced output flags
- [x] Design a safe network fetch command with allowlisted hosts
- [ ] Expand `curl` toward stronger compatibility for safe agent workflows
- [ ] Add richer resource accounting for CPU and memory budgets
- [ ] Decide which higher-level structured-data helpers belong in core vs follow-on packages

### 13. Interactive shell

- [x] Add a session-backed interactive CLI mode
- [x] Use `mvdan/sh/v3` interactive parsing for multiline input
- [x] Persist virtual cwd and shell-visible variable state across interactive entries
- [x] Stop the interactive shell cleanly on `exit`
- [x] Add focused REPL regression tests
- [ ] Add line-editing/history support if it can be done without weakening sandbox determinism

### 14. Host-backed overlay parity

- [ ] Add a host-backed lower `FileSystem` implementation for `OverlayFS`
- [ ] Keep reads constrained to explicit host roots while leaving all writes in memory
- [ ] Add integration tests for copy-on-write behavior against a real directory tree
- [ ] Document when host-backed overlays are appropriate and when pure virtual FS should remain the default

### 15. Hardening and maturity

- [ ] Expand threat-model-driven security regressions around error sanitization and information disclosure
- [ ] Add more compatibility corpus cases for the larger command surface as commands land
- [ ] Audit command metadata/help/version behavior for consistency across the registry
- [ ] Revisit deterministic virtual identity commands such as `hostname`, `whoami`, and `uname`

## Intentional Non-Goals

Do not add these while working through the backlog:

- [ ] No host subprocess fallback
- [ ] No compatibility mode
- [ ] No unrestricted host read-write filesystem in the default path
- [ ] No browser-runtime parity work unless product requirements change
- [ ] No Vercel Sandbox API compatibility layer as a primary goal
- [ ] No Go analogue of the Node.js monkey-patching defense-in-depth model

## Suggested Order

1. Finish `jq` and `curl` compatibility work for agent data flows.
2. Add the next missing command families in batches, starting with file/path and shell/helper commands.
3. Add a host-backed lower filesystem for `OverlayFS` so sessions can read from real trees and keep writes in memory.
4. Add richer CPU and memory accounting to the policy layer.
5. Expand the compatibility corpus and hardening suite alongside each new command batch.
6. Polish the interactive shell only after the runtime and command surface are stable.
