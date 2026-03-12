# commands/ Helper Reference

This document describes the shared helper files in `commands/` that are meant to be reused across command implementations. When implementing a new command, prefer these helpers over writing one-off equivalents.

## Core Framework

### command.go
Defines the `Command` interface, `Invocation` struct, and `ExitError` type that every command uses.

- `Command` — interface: `Name() string`, `Run(ctx, *Invocation) error`
- `CommandFunc` — function type for simple commands
- `DefineCommand(name, fn)` — factory for wrapping a function as a `Command`
- `Invocation` — runtime context: `Args`, `Env`, `Cwd`, `Stdin`/`Stdout`/`Stderr`, `FS`, `Fetch`, `Exec`, `Limits`
- `ExitError{Code, Err}` — error with exit code; use `ExitCode(err)` to extract
- `Exitf(inv, code, format, args...)` — write to stderr and return an `ExitError`
- `openRead(ctx, inv, name)` — open a file for reading, returns `(gbfs.File, absPath, error)`
- `readDir(ctx, inv, name)` — list directory entries
- `statPath` / `lstatPath` — stat with path resolution
- `statMaybe` / `lstatMaybe` — stat that returns `(info, abs, exists, error)` instead of erroring on not-found

### command_spec.go
Declarative metadata, parsing, and help/version rendering for command implementations. Prefer this for all new commands and for migrations away from ad hoc flag parsing.

- `SpecProvider` — optional interface: `Spec() CommandSpec`
- `ParsedRunner` — optional interface: `RunParsed(ctx, inv, matches) error`
- `CommandSpec` — command metadata: `Name`, `About`, `Usage`, `AfterHelp`, `Options`, `Args`, `Parse`, `HelpRenderer`, `VersionRenderer`
- `OptionSpec` — option definition: short/long names, help text, arity, aliases, repeatability, and optional-value behavior
- `ArgSpec` — positional argument metadata: requiredness, repeatability, help text, and defaults
- `ParseConfig` — parser behavior toggles: long-option inference, grouped shorts, attached values, `--` handling, negative-number positional mode, and auto help/version
- `ParsedCommand` — parsed accessors for options and positionals: `Has`, `Count`, `Value`, `Values`, `Arg`, `Args`, `Positionals`, `OptionOrder`
- `RunCommand(ctx, cmd, inv)` — executes a command through the spec layer when it implements `SpecProvider` + `ParsedRunner`, otherwise falls back to `Run`
- `ParseCommandSpec(inv, spec)` — parse `inv.Args` against a `CommandSpec`
- `RenderCommandHelp` / `RenderCommandVersion` — shared default renderers for utility help/version output

Implementation rule:
- New command implementations should prefer `Spec() CommandSpec` plus `RunParsed(...)` instead of walking `inv.Args` manually.
- Command-specific help/version rendering should use `HelpRenderer` / `VersionRenderer` only for real uutils compatibility quirks.
- Legacy manual parsers should be converted to this new method

### invocation_capabilities.go
Creates `Invocation` instances and provides `CommandFS`, the policy-enforced filesystem wrapper.

- `NewInvocation(opts)` — builds an `Invocation` from `InvocationOptions`
- `CommandFS` — wraps `gbfs.FileSystem` with transparent policy checks; exposes `Resolve`, `Open`, `OpenFile`, `Stat`, `Lstat`, `ReadDir`, `Readlink`, `Realpath`, `Symlink`, `Link`, `Chown`, `Chmod`, `Chtimes`, `MkdirAll`, `Remove`, `Rename`, `Getwd`, `Chdir`
- `FetchFunc` — type alias for the network fetch callback

### execution.go
Data types for subprocess execution.

- `ExecutionRequest` — input: `Script`, `Args`, `Env`, `WorkDir`, `Timeout`, `Stdin`/`Stdout`/`Stderr`
- `ExecutionResult` — output: `ExitCode`, `Stdout`, `Stderr`, timing, trace events

### registry.go
Thread-safe command registry with lazy loading.

- `Registry` — `Register(cmd)`, `RegisterLazy(name, loader)`, `Lookup(name)`, `Names()`
- `DefaultRegistry()` — returns a registry pre-loaded with all built-in commands

## I/O Helpers

### io_helpers.go
Basic read-all utilities. The most widely used helper (~24 commands).

- `readAllFile(ctx, inv, name) (data, absPath, error)` — read an entire file
- `readAllStdin(inv) (data, error)` — read all of stdin

### source_helpers.go
Multi-input handling for commands that accept multiple files and/or stdin.

- `namedInput{Name, Abs, Data, FromStdin}` — a single input source with metadata
- `readNamedInputs(ctx, inv, names, defaultStdin) ([]namedInput, error)` — reads multiple files; caches stdin so it's only read once; falls back to stdin when `names` is empty and `defaultStdin` is true
- `readTwoInputs(ctx, inv, left, right) (leftData, rightData, error)` — specialized for two-input commands (e.g. `diff`, `comm`); errors if both are stdin

### redirected_io.go
File wrapper that tracks redirect metadata (path, flags, offset).

- `RedirectMetadata` — interface: `RedirectPath()`, `RedirectFlags()`, `RedirectOffset()`
- `WrapRedirectedFile(file, path, flag) io.ReadWriteCloser` — wraps a `gbfs.File` with offset tracking

## Text Processing

### text_helpers.go
Line splitting with newline stripping.

- `textLines(data) []string` — splits bytes into lines with trailing `\n` removed

### head_tail.go
Shared option parsing and line/byte extraction for `head` and `tail`.

- `headTailOptions` — parsed config: `lines`, `bytes`, `hasBytes`, `fromLine`, `quiet`, `verbose`, `files`
- `parseHeadTailArgs(inv, cmdName, allowFromLine)` — parses `-n`, `-c`, `-q`, `-v`, legacy `-NUM` syntax, and size suffixes (K, M, G, T, P, E, b)
- `splitLines(data) [][]byte` — splits on `\n`, preserving line endings
- `lastLines(data, count)` / `linesFrom(data, start)` / `lastBytes(data, count)` — extraction helpers

## File Operations

### file_helpers.go
File copying, writing, and destination resolution.

- `ensureParentDirExists(ctx, inv, targetAbs)` — validates parent directory exists
- `copyFileContents(ctx, inv, srcAbs, dstAbs, perm)` — copy a single file
- `copyTree(ctx, inv, srcAbs, dstAbs)` — recursive directory copy
- `writeFileContents(ctx, inv, targetAbs, data, perm)` — write bytes to a file
- `resolveDestination(ctx, inv, sourceAbs, destArg, multipleSources)` — resolves destination path, handling the "dest is a directory" logic that `cp`, `mv`, and `ln` share

### path_helpers.go
Directory traversal and file metadata formatting.

- `walkPathTree(ctx, inv, root, visit)` — recursive walk using `lstat` (does not follow symlinks)
- `fileTypeName(info) string` — returns `"symbolic link"`, `"directory"`, or `"regular file"`
- `formatModeOctal(mode) string` — e.g. `"0755"`
- `formatModeLong(mode) string` — e.g. `"drwxr-xr-x"`
- `humanizeBytes(size) string` — e.g. `"4.0K"`, `"12M"`

### permission_helpers.go
Full ownership/permission engine for `chown` and `chgrp`.

- `permissionIdentityDB` — cached user/group name-to-ID mappings from `/etc/passwd` and `/etc/group`
- `loadPermissionIdentityDB(ctx, inv)` — builds the DB (env vars + passwd/group files)
- `parsePermissionOwnerSpec(inv, db, spec)` — parses `"user:group"` specs into UID/GID
- `walkPermissionTarget(ctx, inv, target, opts, visit)` — recursive walk with symlink traversal modes (`-H`, `-L`, `-P`), cycle detection, and `--preserve-root` support
- `permissionSuccessMessage` / `permissionFailureMessage` — verbosity-controlled output formatting

## Subprocess Execution

### subexec_helpers.go
Command resolution and execution through the host shell.

- `executeCommand(ctx, inv, opts)` — resolves and executes an external command via the `Exec` callback
- `executeCommandOptions` — config: `Argv`, `Env`, `WorkDir`, `Timeout`, `Stdin`/`Stdout`/`Stderr`
- `resolveCommand(ctx, inv, env, dir, name)` — find first match in PATH
- `resolveAllCommands(ctx, inv, env, dir, name)` — find all matches in PATH (for `which -a`)
- `commandSearchDirs(env, dir) []string` — parse and deduplicate PATH entries
- `shellJoinArgs(args) string` / `shellSingleQuote(value) string` — safe shell quoting
- `writeExecutionOutputs(inv, result)` — write captured stdout/stderr to the invocation streams
- `exitForExecutionResult(result) error` — convert result to `ExitError`
- `sortedEnvPairs(env) []string` — format env map as sorted `KEY=VALUE` pairs

## Encoding & Parsing

### basenc_helpers.go
Shared boilerplate for `base32` and `base64`.

- `parseBaseEncWrap(name, value, inv) (int, error)` — parse `--wrap` size
- `readSingleBaseEncInput(ctx, inv, name, args) ([]byte, error)` — read single input (file or stdin) with extra-operand check
- `writeBaseEncOutput(w, encoded, wrap) error` — write encoded string with line wrapping

### duration_helpers.go
Duration parsing and escape-sequence decoding.

- `parseFlexibleDuration(value) (time.Duration, error)` — parses `"30"`, `"5s"`, `"2m"`, `"1h"`, `"7d"`; defaults to seconds
- `decodeDelimiterValue(value) (string, error)` — decodes `\n`, `\t`, `\0`, `\\` escape sequences

### checksum_sums.go
Shared implementation for `md5sum`, `sha1sum`, and `sha256sum`. Not a helper file per se, but a shared `checksumSum` struct that all three commands instantiate with different hash functions.
