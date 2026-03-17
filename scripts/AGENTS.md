# scripts/

Shell scripts for CI/CD, releases, and compatibility testing.

## Bats tests

Tests live in `scripts/tests/` and use [bats-core](https://github.com/bats-core/bats-core).
Bats itself runs under the system bash. Each test invokes scripts under gbash
using `--readwrite-root` pointed at a temporary sandbox directory.

### Running

```
make bats-test
```

This builds gbash into `scripts/tests/.gbash-test-bin` and runs all `.bats`
files in `scripts/tests/`.

Bats must be installed (`brew install bats-core`).

### How the sandbox works

`test_helper.bash` provides two key functions:

- `run_gbash "<command>"` -- runs a command string inside gbash with
  `--readwrite-root` set to `$SANDBOX`. The sandbox root becomes `/` inside
  gbash. Scripts, stubs, and state files all go under `$SANDBOX`.
- `create_stub <name> <body>` -- writes an executable shell script into
  `$SANDBOX/bin/` so it appears in gbash's PATH at `/bin/`.

The default `setup` creates `$TEST_TEMP_DIR`, `$SANDBOX`, and `$STUB_BIN`.
Override `setup` in your `.bats` file to add script copies, directory layout,
and stubs. Always call `rm -rf "${TEST_TEMP_DIR}"` in `teardown`.

### Writing a new test file

1. Create `scripts/tests/<script_name>.bats`.
2. Start with `load test_helper`.
3. In `setup`, copy the script into the sandbox and create stubs for any
   external commands (git, docker, curl, etc.). Use state files under
   `$SANDBOX/state/` to control stub behavior per test.
4. Use `run_gbash "scripts/foo.sh arg1 arg2"` to invoke the script.
5. Assert on `$status` and `$output`.

See `tag_release.bats` for a full example with a multi-case git stub.

### Stub tips

Stubs run as `/bin/sh` inside the gbash sandbox, which is gbash itself. Keep
stub logic simple -- `case` statements dispatching on `$1` work well.

Use files under `/state/` to make stubs configurable per test. For example, the
git stub in `tag_release.bats` reads `/state/branch` to return the current
branch and `/state/tags/<name>` to simulate existing tags.

When a command has positional args you need to extract (like
`git rev-list -n 1 <tag>`), count positions carefully. There is no `shift`
happening implicitly -- `$1` is the subcommand, and everything after follows in
order.

### gbash compatibility

Scripts must parse and run under gbash to be testable. Some bash features are
not supported. When you hit one, adapt the script to use a compatible
alternative that still works under regular bash.

Known limitations:

- `BASH_SOURCE` is not set when gbash runs a script file. Use
  `${BASH_SOURCE[0]:-$0}` as a fallback.
- The default PATH inside gbash is `/usr/bin:/bin`. External tools like git,
  docker, and curl are not available unless stubbed into `/bin/` via the
  sandbox. This is intentional -- tests should not depend on host tools.
- `--readwrite-root` must point to a directory inside the system temp directory.
  You cannot mount arbitrary host paths.
