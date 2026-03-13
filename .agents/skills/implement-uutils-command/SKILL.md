---
name: implement-uutils-command
description: >
  Implement a command by porting it from the uutils Rust repo into this Go sandbox,
  achieving full parity with the uutils behavior. Use this skill whenever the user wants to
  port a command from uutils, implement a standard Unix utility, add POSIX command behavior, or
  reference uutils as the source of truth for a command's behavior. Also trigger when the user
  says "coreutils", "uutils", "GNU compatible", or wants to match GNU/POSIX behavior for a
  command like seq, fold, expand, expr, column, od, strings, md5sum, sha1sum, sha256sum, or
  similar standard Unix utilities.
---

# Implement uutils Command

Port a command from [uutils/coreutils](https://github.com/uutils/coreutils) — a Rust
implementation of GNU coreutils — into this Go codebase with full parity: every flag, every
behavior, no shortcuts.

## Workflow

### 1. Pick the command

Run the upstream-diff skill to identify missing commands and missing flags. Many missing commands
are standard Unix utilities that have canonical behavior defined by uutils. Ask the user
which command they want to implement. If they already told you, skip the asking and proceed.

### 2. Clone uutils and study the Rust source

```bash
UUTILS=$(mktemp -d)/uutils
git clone --depth 1 https://github.com/uutils/coreutils.git "$UUTILS"
```

The uutils repo structure is:
- `src/uu/<command>/src/<command>.rs` — the main command implementation
- `src/uu/<command>/src/*.rs` — additional module files for complex commands
- `tests/by-name/<command>/` — test files (useful for understanding expected behavior)

Read the **entire** Rust source for the command. Do not skim. Pay special attention to:
- The `uu_app()` function — this defines all flags via `clap`. It is the authoritative list
  of supported flags, their short/long forms, aliases, default values, and descriptions.
- `uumain()` — the entry point that parses args and dispatches to the core logic
- How stdin is handled when no file arguments are given
- Error messages and exit codes (uutils uses specific exit codes for different failures)
- Edge cases in the Rust implementation — conditionals, special-cased inputs, platform checks

Also check `tests/by-name/<command>/` for the test suite. These tests reveal expected behaviors
that may not be obvious from the implementation alone.

### 3. Plan the implementation

Before writing code, list out:
- Every flag the command supports (short form, long form, description, default value)
- The stdin behavior (does it read from stdin when no files given? does it accept `-`?)
- Error conditions and their exit codes
- GNU-specific behaviors vs POSIX baseline (note which behaviors are GNU extensions)
- Any edge cases visible in the Rust source or test suite

Present this plan to the user for confirmation. This is a checkpoint — the user should agree
the scope is correct before you start coding. Some GNU flags may not make sense in a sandbox
context (e.g., `--preserve` for filesystem metadata that doesn't exist in a virtual FS) — flag
these for discussion.

### 4. Implement the command

Read the bundled reference files in this skill's `references/` directory — they contain
everything you need without having to search the codebase:
- `references/helpers.md` — all available helper functions (filesystem, IO, text, errors)
- `references/test-template.md` — test helpers and boilerplate
- `references/fuzz-template.md` — fuzz test structure, oracle selection, Makefile integration

Create `commands/<name>.go` following these patterns:

```go
package commands

import (
    "context"
    // other imports as needed
)

type <Name> struct{}

func New<Name>() *<Name> { return &<Name>{} }

func (c *<Name>) Name() string { return "<name>" }

func (c *<Name>) Run(ctx context.Context, inv *Invocation) error {
    // 1. Parse flags manually from inv.Args (no external flag library)
    // 2. Read inputs using readNamedInputs() or readAllFile()/readAllStdin()
    // 3. Process data
    // 4. Write output to inv.Stdout
    return nil
}

var _ Command = (*<Name>)(nil)
```

Key conventions:
- Use `exitf(inv, code, format, args...)` for error messages to stderr
- Use `allowPath()`, `openRead()`, `readAllFile()`, `readNamedInputs()` for filesystem access
  (see `references/helpers.md` for the full list — don't reimplement these)
- Read from `inv.Stdin` when no file arguments are given (if the command is a filter).
  For multi-file commands, use `readNamedInputs(ctx, inv, names, true)` which handles
  stdin fallback and `-` as stdin automatically.
- For flags with values, support both `-f value` and `-fvalue` (attached) and `--flag=value`
  forms. See existing commands like `cut.go` or `wc.go` for examples.

**Full parity means full parity.** Implement every flag from the `uu_app()` clap definition.
Do not skip flags because they seem obscure or rarely used. Do not leave TODO comments for
"later". If the uutils version supports `--zero`, `--complement`, or `--output-delimiter`,
your Go version supports them too. Compare your implementation against the Rust source
function by function to make sure nothing was missed.

**Sandbox considerations:** Some uutils flags interact with real OS features that don't
exist in the virtual filesystem (e.g., file ownership, ACLs, device files). For these:
- Implement the flag parsing so the flag is accepted
- Produce reasonable behavior in the sandbox context
- Document in a code comment what the flag does on a real system vs in the sandbox

### 5. Register the command

Add `New<Name>()` to `DefaultRegistry()` in `commands/registry.go`. Place it in the
appropriate category group, keeping alphabetical order within the group.

### 6. Write tests

See `references/test-template.md` for the full template and helpers. Add integration tests
in the appropriate `runtime/*_commands_test.go` file.

Cover:
- **Every flag** — at least one test per flag, testing it actually works
- **Flag combinations** — common combinations that users would use together
- **Happy path** — basic invocation with typical input
- **Error cases** — missing required args, nonexistent files, invalid flag values
- **Stdin input** — pipe behavior when the command supports it
- **Edge cases** — empty input, binary data, large input, special characters
- **Exit codes** — verify correct exit codes for both success and failure
- **GNU compatibility** — if there are tests in `tests/by-name/<command>/`, port the
  interesting ones to verify your implementation matches GNU behavior

The test coverage should be thorough enough that you could delete the Rust source and
reconstruct the command's behavior entirely from the tests.

### 7. Write fuzz tests

This is mandatory — the sandbox is a security boundary. See `references/fuzz-template.md`
for the complete template, helper list, and oracle selection guide.

Add fuzz coverage in `runtime/fuzz_command_targets_test.go` (or extend an existing fuzz
function if the command fits an existing category — the template lists all existing categories).

Key points:
- Use `newFuzzRuntime`, `newFuzzSession`, `runFuzzSessionScript`
- Provide 2-3 seed inputs with `f.Add()`
- **Use `assertSecureFuzzOutcome` as the oracle** — this is the correct default for all new
  commands. It allows non-zero exits but catches crashes, host path leaks, and sensitive
  disclosure. Do NOT use `assertBaseFuzzOutcome` or `assertSuccessfulFuzzExecution` unless
  you have a specific reason.
- Test the command with various flag combinations and malformed input
- Add the fuzz target to the Makefile

### 8. Verify everything builds and passes

```bash
go build ./...
go test ./...
make fuzz FUZZTIME=10s
make lint
```

Fix any failures before presenting the result to the user. All four commands must pass clean.

## Important reminders

- **Do not skip flags.** The whole point of this skill is achieving full parity. If you're
  tempted to skip a flag, don't. Implement it.
- **Read the Rust source carefully.** The `uu_app()` clap definition is the source of truth
  for flags. The `uumain()` function and helpers contain the behavioral logic. Read both.
- **Check the test suite.** `tests/by-name/<command>/` often reveals edge cases and expected
  behaviors that aren't obvious from the implementation.
- **Test what you implement.** Every flag needs at least one test proving it works. Untested
  code is unfinished code.
- **Fuzz tests are not optional.** The sandbox is a security boundary. Every command that
  processes input needs fuzz coverage.
- **Check your work.** After implementation, go back to the Rust source and verify every flag
  and behavior is accounted for in your Go code.
