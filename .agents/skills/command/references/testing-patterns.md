# Testing Patterns for Commands

## Test location

Command tests live in `runtime/` as runtime-level integration tests. This is intentional — commands are tested through the full execution pipeline (parse -> run -> capture output) rather than by calling `Command.Run()` directly. This catches integration bugs like incorrect path resolution or missing policy checks.

## Test helpers

All helpers are in `runtime/test_helpers_test.go` and `runtime/fixture_helpers_test.go`:

| Helper | Signature | Purpose |
|--------|-----------|---------|
| `newSession` | `newSession(t, &Config{})` | Create a test session with default config |
| `mustExecSession` | `mustExecSession(t, session, script)` | Execute a script string, fail test on error |
| `writeSessionFile` | `writeSessionFile(t, session, path, data)` | Write a file into the session's virtual filesystem |
| `newRuntime` | `newRuntime(t, &Config{})` | Create a test runtime (use `newSession` when you need persistent state) |
| `writeFile` | `writeFile(t, rt, path, data)` | Write file to a runtime's filesystem |

## Table-driven test pattern

```go
func TestCommandBasicBehavior(t *testing.T) {
    session := newSession(t, &Config{})

    // Setup: write any files the command needs
    writeSessionFile(t, session, "/home/agent/input.txt", []byte("line1\nline2\nline3\n"))

    result := mustExecSession(t, session, "wc -l /home/agent/input.txt\n")
    if result.ExitCode != 0 {
        t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
    }
    if !strings.Contains(result.Stdout, "3") {
        t.Fatalf("Stdout = %q, want line count 3", result.Stdout)
    }
}
```

## What to test

For every command, cover these categories:

### Happy path
Basic invocation with typical arguments. Verify stdout content and exit code 0.

### Flag variations
Each supported flag in isolation and common flag combinations.

### Error cases
- Missing required arguments → exit code 1, error on stderr
- Nonexistent file → exit code 1, appropriate error message
- Invalid flags → exit code 1 or 2

### Pipe / stdin behavior
If the command reads stdin when no file args given:
```go
result := mustExecSession(t, session, "echo 'hello world' | wc -w\n")
```

### Edge cases
- Empty input / empty files
- Large input (but keep reasonable for unit tests)
- Special characters in filenames
- Multiple files with mixed success/failure

## Assertion patterns

```go
// Exit code
if result.ExitCode != 0 {
    t.Fatalf("ExitCode = %d, want 0; stderr=%q", result.ExitCode, result.Stderr)
}

// Exact stdout match
if got, want := result.Stdout, "expected output\n"; got != want {
    t.Fatalf("Stdout = %q, want %q", got, want)
}

// Partial stdout match
if !strings.Contains(result.Stdout, "expected") {
    t.Fatalf("Stdout = %q, want to contain %q", result.Stdout, "expected")
}

// Expected failure
if result.ExitCode == 0 {
    t.Fatalf("ExitCode = 0, want nonzero for invalid input")
}
if !strings.Contains(result.Stderr, "No such file") {
    t.Fatalf("Stderr = %q, want file-not-found message", result.Stderr)
}

// Filesystem side effects
info, err := session.FileSystem().Stat(context.Background(), "/home/agent/output.txt")
if err != nil {
    t.Fatalf("Stat(output.txt) error = %v", err)
}
```

## Test file naming

Name test files after the command category: `path_commands_test.go`, `text_search_commands_test.go`, `archive_commands_test.go`, etc. If adding tests for a command that fits an existing category file, add to that file rather than creating a new one. For a command that doesn't fit an existing category, create a new `<category>_commands_test.go` file.

## Testing with policy

Most tests use the default policy. To test policy-related behavior:
```go
session := newSession(t, &Config{
    Policy: policy.NewStatic(&policy.Config{
        ReadRoots:  []string{"/home/agent"},
        WriteRoots: []string{"/home/agent"},
    }),
})
```
