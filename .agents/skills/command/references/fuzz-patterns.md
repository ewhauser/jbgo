# Fuzz Testing Patterns for Commands

Fuzz testing is mandatory for every command. The sandbox is a security boundary — it must not crash, panic, leak host paths, or disclose sensitive information regardless of what input it receives. Fuzz testing is the primary tool for verifying this property.

## Where fuzz tests live

All fuzz targets are in `runtime/fuzz_command_targets_test.go` (for command-specific targets) or `runtime/fuzz_runtime_test.go` (for general runtime targets). Fuzz helpers live in `runtime/fuzz_helpers_test.go` and oracles in `runtime/fuzz_oracles_test.go`.

## Anatomy of a fuzz target

```go
func FuzzNewCommandCategory(f *testing.F) {
    rt := newFuzzRuntime(f)

    // Provide 2-3 seed inputs — representative, not exhaustive
    seeds := []struct {
        name string
        data []byte
    }{
        {"alpha", []byte("hello\n")},
        {"notes-1", []byte("# title\nbody\n")},
        {"data.bin", []byte{0x00, 0x01, 0x02, 0x03, 0xff}},
    }
    for _, seed := range seeds {
        f.Add(seed.name, seed.data)
    }

    f.Fuzz(func(t *testing.T, rawName string, rawData []byte) {
        session := newFuzzSession(t, rt)
        data := clampFuzzData(rawData)
        inputPath := fuzzPath(rawName) + ".txt"

        // Write fuzz-generated data into the sandbox
        writeSessionFile(t, session, inputPath, data)

        // Build a script exercising the command with fuzz inputs
        script := []byte(fmt.Sprintf(
            "mycommand %s > /tmp/out.txt\n",
            shellQuote(inputPath),
        ))

        result, err := runFuzzSessionScript(t, session, script)
        assertSecureFuzzOutcome(t, script, result, err)
    })
}
```

## Key helpers

| Helper | Purpose |
|--------|---------|
| `newFuzzRuntime(tb)` | Creates a runtime with tight limits (200 commands, 200 loop iterations, 16KB output) |
| `newFuzzSession(tb, rt)` | Creates a session on the fuzz runtime |
| `runFuzzSessionScript(t, session, script)` | Executes a fuzz script with size guard and timeout |
| `clampFuzzData(data)` | Truncates data to `fuzzMaxDataBytes` (2KB) |
| `fuzzPath(name)` | Sanitizes a fuzz string into a safe `/tmp/<name>` path |
| `shellQuote(value)` | Single-quote escapes a value for shell embedding |
| `sanitizeFuzzPathComponent(raw)` | Cleans a raw string to safe path characters |
| `sanitizeFuzzToken(raw)` | Cleans a raw string for use as a value token |
| `normalizeFuzzText(data)` | Normalizes bytes to valid UTF-8 text with newline termination |

## Fuzz oracles

Choose the appropriate oracle based on what the command does:

| Oracle | When to use |
|--------|-------------|
| `assertSecureFuzzOutcome` | Default for most commands. Checks no crashes, no host path leaks, no sensitive disclosure, no runaway execution. |
| `assertSuccessfulFuzzExecution` | When the fuzz script should always succeed (exit 0). Includes all security checks plus exit code assertion. |
| `assertBaseFuzzOutcome` | Minimal check — unexpected errors, host path leaks, sensitive disclosure, crash output. Use when commands are expected to fail on some inputs. |

`assertSecureFuzzOutcome` is the right choice for most new commands. It allows non-zero exit codes (the command can reject bad input) but catches panics, crashes, and information leaks.

## What the security oracles check

1. **No panics or crashes** — output must not contain `panic:`, `runtime error:`, `fatal error:`, `SIGSEGV`, `goroutine` stack dumps
2. **No host path leaks** — output must not contain the host's CWD or home directory (unless the fuzz input itself contained those strings)
3. **No sensitive disclosure** — output must not leak `$HOME`, `$USER`, `$LOGNAME`, `$SHELL`, `$TMPDIR`, hostname
4. **No runaway execution** — execution must complete within 2 seconds

## Choosing fuzz parameters

The `f.Add()` seed values should be representative inputs that exercise the command's core logic. The fuzzer will mutate these to find crashes. Good seeds:

- A simple text input (`"hello\n"`)
- A structured input matching what the command expects (`"# title\nbody\n"` for text processing)
- A binary input (`[]byte{0x00, 0x01, 0x02, 0x03, 0xff}`) to test binary-safety

Fuzz function parameters typically include:
- A `string` for filenames/paths (fuzzed by the engine)
- A `[]byte` for file contents (fuzzed by the engine)
- Additional `string` params if the command takes structured arguments

## Adding to an existing fuzz target vs. creating a new one

If the new command fits an existing category, extend that fuzz target. For example:
- File operations (touch, cp, mv, etc.) → `FuzzFilePathCommands`
- Text search (grep, sed, cut, etc.) → `FuzzTextSearchCommands`
- Data processing (jq, base64, etc.) → `FuzzDataCommands`
- Archive (tar, gzip, etc.) → `FuzzArchiveCommands`

If it doesn't fit, create a new `Fuzz<Category>Commands` function.

## Makefile integration

Add the new fuzz target to the `fuzz:` target in `Makefile`:

```makefile
fuzz:
    # ... existing targets ...
    go test ./runtime -run=^$$ -fuzz=FuzzNewCategory -fuzztime=$(FUZZTIME)
```

## Running fuzz tests

```bash
# Run all fuzz targets (10s each by default)
make fuzz

# Run a specific target for longer
go test ./runtime -run=^$ -fuzz=FuzzNewCategory -fuzztime=60s

# Run with custom duration
make fuzz FUZZTIME=30s
```

## Common patterns for specific command types

### Text filter commands (reads input, writes transformed output)
```go
script := []byte(fmt.Sprintf(
    "echo %s | mycommand --flag > /tmp/out.txt\n",
    shellQuote(string(normalizeFuzzText(rawData))),
))
```

### File manipulation commands (operates on files)
```go
writeSessionFile(t, session, inputPath, data)
script := []byte(fmt.Sprintf(
    "mycommand %s %s\nstat %s > /dev/null\n",
    shellQuote(inputPath),
    shellQuote(outputPath),
    shellQuote(outputPath),
))
```

### Multi-step commands (create, operate, verify)
```go
script := []byte(fmt.Sprintf(
    "echo %s > %s\nmycommand %s > /tmp/result.txt\ncat /tmp/result.txt > /dev/null\n",
    shellQuote(string(data)),
    shellQuote(inputPath),
    shellQuote(inputPath),
))
```
