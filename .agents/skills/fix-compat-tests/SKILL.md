---
name: fix-compat-tests
description: >
  Fix failing GNU coreutils compatibility tests in the gnu-test suite. Use this skill when
  the user shares a table of failing GNU compat tests, mentions "compat test", "gnu test",
  "gnu-test", or asks you to investigate failures from `make gnu-test`. Also trigger when
  the user pastes test names like "tests/test/test.pl" or "tests/misc/echo-e.sh" and wants
  them fixed or triaged. This skill covers diagnosis, code fixes, and skip decisions — not
  conformance tests (`make conformance-test`), which are a separate system.
---

# Fix GNU Compat Tests

This skill walks you through diagnosing and fixing failures in the GNU coreutils
compatibility test suite (`make gnu-test`). The suite runs real GNU coreutils test scripts
under gbash and compares behavior against native coreutils.

## Mental Model

Each failing test falls into one of these categories:

1. **Bug in gbash builtin/command** — gbash produces wrong output, exit code, or error
   message. Fix the Go code.
2. **Sandbox limitation** — the test exercises something the sandbox intentionally blocks
   (symlink traversal, device access, /proc). Skip with a reason.
3. **Missing feature in a dependency command** — the test's setup uses a gbash command
   feature that isn't implemented yet (e.g., `touch -d` with relative dates). Note it as a
   dependency and move on; don't try to fix the unrelated command in the same change.
4. **Out-of-scope command** — the test is for a command gbash won't implement. Skip.

Your job is to categorize each failure and take the right action. Resist the urge to skip
tests that could be fixed with a reasonable code change.

## Workflow

### 1. Run the failing tests

```bash
make gnu-test GNU_TESTS="tests/foo/bar.sh,tests/baz/qux.pl" GNU_KEEP_WORKDIR=1
```

`GNU_KEEP_WORKDIR=1` preserves the work directory so you can inspect test sources and logs.

Results land in `.cache/gnu/results/docker-latest/`:
- `<test-base>/<test-base>.log` — per-test log with stdout/stderr
- `workdir/` — the GNU coreutils source tree with test files

### 2. Read the logs

For each failing test, read its log file. The log tells you:
- **exit status mismatch** — gbash returned a different exit code than expected
- **stdout/stderr mismatch** — output differs (shown as a diff)
- **set-up failure (exit 99)** — test framework couldn't prepare prerequisites; usually a
  missing feature in a setup command, not the command under test

### 3. Read the test source

Find the test source in the preserved workdir:

```
.cache/gnu/results/docker-latest/workdir/tests/<category>/<test-file>
```

Read the entire test file. Understand what it's actually testing. GNU tests often use helper
functions from `tests/init.sh` — the important ones:

- `returns_ N cmd` — asserts cmd exits with status N
- `fail=1` — marks the test as failed
- `framework_failure_` — aborts with exit 99 (setup problem)
- `skip_` — skips the test with a message
- `compare` / `compare_` — compares expected vs actual output files

### 4. Diagnose the root cause

For each failure, figure out which category it falls into:

**Is it a bug in the command under test?** Read the builtin implementation in
`internal/builtins/` or the command in `commands/`. Trace the code path that produces the
wrong behavior. Common issues:
- Parser not handling edge cases (operator-looking strings as operands, POSIX arg-count
  rules)
- Wrong exit codes (many GNU commands use exit 2 for usage errors, not 1)
- Missing flag support or incomplete flag behavior
- Error message format differences (gbash says "invalid" vs GNU says "unrecognized")

**Is it a sandbox limitation?** Look for errors like:
- "symlink traversal denied"
- "operation not permitted" on device files
- Failures involving `/dev/`, `/proc/`, `/sys/`
- FIFO/pipe deadlocks

**Is it a missing feature in another command?** The test's setup commands (touch, chmod,
ln, etc.) may use GNU extensions that gbash hasn't implemented. The log usually shows the
setup failure clearly. Note the missing feature but don't fix it here.

### 5. Fix or skip

**Fixing a bug:**
1. Read the full builtin/command implementation
2. Understand the correct behavior (POSIX spec, GNU coreutils source, or bash behavior)
3. Make the minimal fix
4. Run the existing unit tests: `go test ./internal/builtins/ -run TestRelevantName`
5. Run conformance tests if the change touches shell semantics
6. Run `make lint`
7. Re-run the GNU compat test to verify

**Skipping a test (sandbox/out-of-scope only):**

Add an entry to `cmd/gbash-gnu/manifest.json` in the `skip_patterns` array:

```json
{ "pattern": "tests/test/test-file.sh", "reason": "symlink traversal in test -ef denied by gbash sandbox" }
```

Reasons should be specific about what limitation prevents the test from passing. Don't skip
a test just because it's hard to fix.

**Noting a dependency:**

When the failure is caused by a missing feature in another command (e.g., test-N.sh fails
because `touch -d` doesn't support relative dates), tell the user. The test stays as a
known failure — don't skip it, because fixing the dependency command should make it pass
later.

### 6. Verify

After all fixes and skips:

```bash
make gnu-test GNU_TESTS="<all-original-failing-tests>"
```

Confirm that:
- Fixed tests now pass
- Skipped tests no longer appear in the failure list
- Dependency-blocked tests are accounted for

Also verify no regressions:

```bash
make lint
go test ./internal/builtins/ -run TestRelevantPattern
```

## Key Files

| File | Purpose |
|------|---------|
| `cmd/gbash-gnu/manifest.json` | Test-to-command mapping, skip patterns |
| `cmd/gbash-gnu/gnu_tests.go` | Programmatic skip rules (TTY, root, SELinux) |
| `internal/builtins/` | Builtin command implementations |
| `commands/` | External command implementations |
| `scripts/gnu-test.sh` | Test runner orchestration |

## Common Pitfalls

- **Don't skip fixable tests.** If the failure is a real bug in gbash code and can be fixed
  in a reasonable change, fix it. Skipping accumulates tech debt.
- **Read the full test, not just the failing line.** GNU tests often have setup that affects
  what's being tested. A "test -ef" failure might actually be a symlink resolution issue.
- **Check that invalid-opt.pl failures are actually yours.** This test covers every utility.
  Filter the log to find the specific command you care about — other commands' failures are
  noise.
- **Match GNU error message format.** Many tests check stderr. GNU coreutils uses specific
  patterns like `cmd: invalid option -- 'x'` and `Try 'cmd --help' for more information.`
- **POSIX arg-count rules matter for `test`/`[`.** The test builtin uses argument-count-based
  disambiguation, not pure recursive descent. Operator-looking strings (`!`, `(`, `-f`) can
  be operands depending on position and count.
