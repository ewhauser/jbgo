Goal (incl. success criteria):
- Complete every item in `TODO.md` by migrating each listed command to `Spec() CommandSpec` plus `RunParsed(...)`.
- While migrating each command, close behavior/flag gaps against `uutils/coreutils` where applicable.
- For each completed TODO item: run lint, repo tests, and the command-specific compatibility tests; update checklist state here; commit the change; continue to the next item.
- Continue using this file as the canonical continuity ledger at the start of every turn.

Constraints/Assumptions:
- Repository root: `/Users/ewhauser/working/command-updates`.
- Follow `AGENTS.md` instructions for this workspace.
- Use `CONTINUITY.md` as the canonical session briefing; do not rely on prior chat unless reflected here.
- Mark unknowns as `UNCONFIRMED`; do not guess.
- Current branch: `command-updates-2`.
- Worktree was clean at ledger initialization.
- Active source of scope: `TODO.md` ("Legacy Option Parser Migration").
- User requested no pauses for guidance; proceed autonomously through the list.
- User requested that each command in the ledger be its own checklist item; do not group commands.
- `implement-uutils-command` skill is available in `.agents/skills/implement-uutils-command/SKILL.md`.
- `command` skill referenced by `AGENTS.md` is missing from `.agents/skills`; fallback is direct repo inspection.

Key decisions:
- Treat the TODO list order as the execution order unless blocked.
- Track TODO progress in this ledger with checklist bullets.
- Use direct repo inspection for the missing `command` skill guidance.

State:
- Status: active
- Repo inspected: yes
- Ledger initialized: yes
- Active task: TODO migration pass
- TODO scope at start: 59 command files / 63 command entrypoints
- Current verification gate: `bash` / `sh` complete; preparing commit and moving to `env`

Done:
- Confirmed `CONTINUITY.md` was missing at start of turn.
- Inspected workspace root contents.
- Confirmed current branch is `command-updates-2`.
- Confirmed worktree was clean when the ledger was created.
- Created baseline `CONTINUITY.md`.
- Read `TODO.md` and confirmed the full migration scope.
- Confirmed `command` skill file is missing from `.agents/skills`.
- Received clarification that the ledger checklist must be one command per item.
- Rewrote `base32` onto `CommandSpec` / `RunParsed(...)`.
- Rewrote `base64` onto `CommandSpec` / `RunParsed(...)`.
- Extended `commands/command_spec.go` with short-option alias support for parity cases like `-D`.
- Added runtime coverage for `base32` / `base64` short alias, inferred long option, and file-input cases.
- Verified `go test ./runtime -run 'TestBase32|TestBase64'` passed.
- Verified `go test ./...` passed.
- Verified `make lint` passed.
- Verified explicit GNU compatibility test `tests/basenc/base64.pl` passed via `go run ./cmd/gbash-gnu --cache-dir .cache/gnu --gbash-bin .cache/gnu/bin/gbash --tests 'tests/basenc/base64.pl'`.
- Committed `base32` / `base64` migration as `1931e84`.
- Explored next-item risks:
- `bash` / `sh`: manual parser today; main migration risk is preserving `-c` semantics and first-positional stop behavior.
- `env` / `printenv`: manual parser today; main migration risk is preserving mixed option / assignment / command grammar, especially after bare `--`.
- Rewrote `bash` / `sh` onto `CommandSpec` / `RunParsed(...)`.
- Added deterministic tests for `bash` / `sh` help, script-file execution, missing-script handling, and `sh -s`.
- Verified `go test ./runtime -run 'TestBash|TestSh'` passed.
- Verified `go test ./cmd/gbash -run 'TestRunCLIMulticallShRunsCommandStringWithArgs'` passed.
- Verified `go test ./...` passed after the `bash` / `sh` migration.
- Verified `make lint` passed after the `bash` / `sh` migration.

Now:
- Commit the completed `bash` / `sh` migration.
- Move to `env`, then `printenv`, using the already-collected exploration notes.

Next:
- Migrate `env` / `printenv` after that.
- Migrate `gzip` / `gunzip` / `zcat` after `env` / `printenv`.

Open questions (UNCONFIRMED if needed):
- UNCONFIRMED: No dedicated GNU test file for `base32` alone was found; `tests/basenc/base64.pl` appears to be the shared GNU compatibility test covering both `base32` and `base64`.
- UNCONFIRMED: No GNU coreutils compatibility harness target exists for `bash` / `sh`; verification for those commands relies on repo runtime/CLI coverage instead.

Working set (files/ids/commands):
- File: `CONTINUITY.md`
- File: `AGENTS.md`
- File: `TODO.md`
- File: `commands/registry.go`
- File: `commands/base32.go`
- File: `commands/base64.go`
- File: `commands/command_spec.go`
- File: `commands/bash.go`
- File: `commands/env.go`
- File: `runtime/process_helper_commands_test.go`
- File: `runtime/base32_commands_test.go`
- File: `runtime/base64_commands_test.go`
- Command: `ls -la`
- Command: `git status --short`
- Command: `git branch --show-current`
- Command: `sed -n '1,240p' TODO.md`
- Command: `go test ./runtime -run 'TestBase32|TestBase64'`
- Command: `go test ./...`
- Command: `make lint`
- Command: `go run ./cmd/gbash-gnu --cache-dir .cache/gnu --setup`
- Command: `go run ./cmd/gbash-gnu --cache-dir .cache/gnu --gbash-bin .cache/gnu/bin/gbash --tests 'tests/basenc/base64.pl'`
- Command: `go test ./runtime -run 'TestBash|TestSh'`
- Command: `go test ./cmd/gbash -run 'TestRunCLIMulticallShRunsCommandStringWithArgs'`

Migration checklist:
- [x] `base32`
- [x] `base64`
- [x] `bash`
- [x] `sh`
- [ ] `env`
- [ ] `printenv`
- [ ] `gzip`
- [ ] `gunzip`
- [ ] `zcat`
- [ ] `head`
- [ ] `tail`
- [ ] `ls`
- [ ] `dir`
- [ ] `basename`
- [ ] `cat`
- [ ] `chmod`
- [ ] `chown`
- [ ] `column`
- [ ] `comm`
- [ ] `cp`
- [ ] `curl`
- [ ] `cut`
- [ ] `date`
- [ ] `diff`
- [ ] `dirname`
- [ ] `du`
- [ ] `echo`
- [ ] `file`
- [ ] `find`
- [ ] `grep`
- [ ] `id`
- [ ] `join`
- [ ] `link`
- [ ] `ln`
- [ ] `mkdir`
- [ ] `mv`
- [ ] `nl`
- [ ] `od`
- [ ] `paste`
- [ ] `pwd`
- [ ] `readlink`
- [ ] `rev`
- [ ] `rg`
- [ ] `rm`
- [ ] `rmdir`
- [ ] `sed`
- [ ] `sleep`
- [ ] `sort`
- [ ] `split`
- [ ] `stat`
- [ ] `tac`
- [ ] `tar`
- [ ] `timeout`
- [ ] `touch`
- [ ] `tr`
- [ ] `tree`
- [ ] `uniq`
- [ ] `uptime`
- [ ] `wc`
- [ ] `which`
- [ ] `whoami`
- [ ] `xargs`
- [ ] `yes`
