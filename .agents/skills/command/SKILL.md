---
name: command
description: >
  Guide for adding new built-in commands or modifying existing ones in gbash.
  Use this skill whenever the user asks to add a shell command (e.g., "add xxd", "implement seq",
  "add a new builtin"), modify an existing command's behavior, add flags to a command, or fix a
  bug in a command implementation. Also trigger when the user mentions command implementations,
  the command registry, or built-in command testing.
---

# Adding or Modifying Built-in Commands

This skill walks you through the full lifecycle of a command change: implementation, registration, testing, fuzz coverage, and spec updates. Every step is required — shipping a command without fuzz tests is not acceptable because the sandbox is a security boundary and fuzz testing is how we verify commands can't escape it.

## Overview

Commands live in `commands/` and implement a two-method interface:

```go
type Command interface {
    Name() string
    Run(ctx context.Context, inv *Invocation) error
}
```

Each command gets an `Invocation` with everything it needs: args, stdin/stdout/stderr, a sandboxed virtual filesystem, policy enforcement, and trace recording. Commands never touch the host OS — all file access goes through `inv.FS`, all policy checks through `inv.Policy`.

## Step-by-step checklist

### 1. Implement the command

Create `commands/<name>.go`. Read `references/command-patterns.md` for the struct conventions, flag parsing, error handling, filesystem access helpers, and trace recording patterns. Study a similar existing command for reference — simple commands like `echo.go` or `touch.go` are good starting points, while `wc.go` shows flag parsing with options structs.

Key rules:
- Struct type named after the command (PascalCase), with `New<Name>()` constructor
- End the file with `var _ Command = (*<Name>)(nil)` to verify interface compliance
- Use `exitf(inv, code, format, args...)` for error messages to stderr
- Use `allowPath()`, `openRead()`, `statPath()` and friends for filesystem access — these enforce policy and record trace events automatically
- Read from `inv.Stdin` when no file arguments are given (standard Unix filter pattern)
- Never shell out or access the host — this is a sandbox

### 2. Register the command

Add `New<Name>()` to the `DefaultRegistry()` call in `commands/registry.go`. Keep the list alphabetically grouped by category (file ops, text ops, data ops, etc.).

### 3. Write table-driven tests

Add runtime-level tests in `runtime/` following the table-driven pattern. Read `references/testing-patterns.md` for the test helper functions (`newSession`, `mustExecSession`, `writeSessionFile`) and assertion conventions. Tests should cover:
- Happy path with expected stdout
- Flag variations
- Error cases (missing args, nonexistent files) with expected exit codes and stderr
- Stdin-based input (pipe behavior)
- Edge cases specific to the command

### 4. Add fuzz coverage (required)

Every command that touches the filesystem or processes user input needs fuzz testing. This is non-negotiable — the sandbox is a security boundary and fuzz testing catches crashes, panics, and escapes that unit tests miss.

Read `references/fuzz-patterns.md` for the full guide on writing fuzz targets, choosing the right oracle, and adding to the Makefile. The short version:
- Add a `Fuzz<Domain>Commands` function in `runtime/fuzz_command_targets_test.go` (or extend an existing one if your command fits a category)
- Use `newFuzzRuntime`, `newFuzzSession`, `runFuzzSessionScript`, and `assertSecureFuzzOutcome` helpers
- Provide 2-3 seed inputs via `f.Add()`
- Add the fuzz target to the `fuzz:` Makefile target
- Run `make fuzz FUZZTIME=30s` to verify it passes

### 5. Update SPEC.md

Per the repository's spec sync rules, adding or removing commands requires a SPEC.md update. Find the command list section and add the new command in the appropriate category. If the command introduces new capabilities (e.g., network access, new file formats), document those too.

### 6. Verify

```bash
go build ./...        # Compiles
go test ./...         # All tests pass
make fuzz FUZZTIME=10s  # Fuzz targets pass
make lint             # No lint issues
```
