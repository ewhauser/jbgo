# gbash Bugs Found While Building `harness-overlay`

This file tracks gbash behavior gaps that surfaced while making the vendored Harness overlay example work.

## Open

### `set -u` + arithmetic loop counters can fail with `unbound variable`

- Symptom: scripts using patterns like `for (( i=${#arr[@]}-1; i>=0; i-- ))` can fail with `i: unbound variable` unless `i` is initialized first.
- Impact: broke the local `agent` override and vendored Harness hooks such as `send`, `tool_exec`, and `skills` assembly.
- Current workaround in this example: local `.harness` overrides pre-initialize `i=0` before those loops.

### Reverse arithmetic `for (( ...; i-- ))` loops are unreliable

- Symptom: reverse index scans over arrays can stop after the highest-priority entry instead of walking the full list.
- Impact: provider and tool discovery only inspected the final source entry, which broke provider resolution and hook/tool lookup precedence.
- Current workaround in this example: local `.harness` overrides avoid reverse arithmetic loops and instead scan forward while letting the last match win.

### `set -u` + `local i` without initialization can fail immediately

- Symptom: `local i` can itself trip `nounset` before later assignment.
- Impact: broke provider resolution in the local `agent` command override.
- Current workaround in this example: initialize locals like `local i=0`.

### `sed -n 's/.../.../p'` is not fully compatible

- Symptom: `sed -n 's/^cwd: //p'` failed with `sed: unsupported substitute flag "p"`.
- Impact: vendored Harness `start` hook failed before the agent loop could assemble a request.
- Current workaround in this example: local `.harness/hooks.d/start/10-init` uses `grep` and `cut` instead.

### Sourcing the vendored `bin/harness` script inside gbash can hang

- Symptom: `source ./bin/harness` did not complete reliably under gbash.
- Impact: vendored Harness subcommands that source `bin/harness` were not usable directly inside the example.
- Current workaround in this example: gbash-specific command overrides live in `workspace/.harness/commands/` and avoid sourcing the vendored bootstrap script.

## Notes

- This file is for gbash issues, not general example bugs.
- Harness-overlay-specific fixes that are not gbash bugs should stay in code comments, tests, or the README instead of being added here.
