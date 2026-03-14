# commands/ Authoring Reference

This document describes the stable command authoring API exposed by `commands/`.

Builtin command implementations no longer live in `commands/`; they live under
`internal/builtins/`. When implementing shipped commands for gbash itself, work
in `internal/builtins/`. When designing reusable authoring APIs for external
command authors, work in `commands/`.

## Core Public API

### command.go
Defines the core command contract and invocation context.

- `Command` — interface: `Name() string`, `Run(ctx, *Invocation) error`
- `CommandFunc` — function adapter for simple commands
- `DefineCommand(name, fn)` — wraps a function as a `Command`
- `Invocation` — runtime context: `Args`, `Env`, `Cwd`, `Stdin`/`Stdout`/`Stderr`, `FS`, `Fetch`, `Exec`, `Interact`, `Limits`
- `ExitError{Code, Err}` — error with exit code; use `ExitCode(err)` to extract
- `Exitf(inv, code, format, args...)` — write to stderr and return an `ExitError`

### command_spec.go
Declarative metadata, parsing, and help/version rendering for commands.

- `SpecProvider` — optional interface: `Spec() CommandSpec`
- `ParsedRunner` — optional interface: `RunParsed(ctx, inv, matches) error`
- `CommandSpec` — command metadata: `Name`, `About`, `Usage`, `AfterHelp`, `Options`, `Args`, `Parse`, `HelpRenderer`, `VersionRenderer`
- `OptionSpec` — option definition, aliases, arity, repeatability, and help metadata
- `ArgSpec` — positional argument metadata, defaults, repeatability, and requiredness
- `ParseConfig` — parser behavior toggles including grouped shorts, attached values, `--`, negative-number positionals, and auto help/version
- `ParsedCommand` — parsed accessors for options and positionals
- `RunCommand(ctx, cmd, inv)` — executes through the spec layer when supported
- `ParseCommandSpec(inv, spec)` — parse `inv.Args` against a `CommandSpec`
- `RenderCommandHelp` / `RenderCommandVersion` — shared help/version rendering

Implementation rule:
- New command implementations should prefer `Spec() CommandSpec` plus `RunParsed(...)` over manual argument walking.
- Prefer the shared help renderer before adding command-specific help text.

### invocation_capabilities.go
Creates `Invocation` instances and exposes the policy-enforced filesystem wrapper.

- `NewInvocation(opts)` — builds an `Invocation` from `InvocationOptions`
- `InvocationOptions` — inputs for constructing an invocation
- `CommandFS` — wraps `gbfs.FileSystem` with policy-aware filesystem operations
- `FetchRequest` / `FetchResponse` / `FetchFunc` — network callback types

### execution.go
Data types for subprocess and interactive shell execution.

- `ExecutionRequest`
- `ExecutionResult`
- `InteractiveRequest`
- `InteractiveResult`

### registry.go
Thread-safe command registry with lazy loading.

- `Registry` — `Register(cmd)`, `RegisterLazy(name, loader)`, `Lookup(name)`, `Names()`
- `CommandRegistry` — interface used by gbash and contrib modules
- `NewRegistry(cmds...)` — construct a registry from commands

The stock builtin registry is now exposed via `gbash.DefaultRegistry()`, not
through `commands`.

## Public Utilities

### redirected_io.go
- `RedirectMetadata`
- `WrapRedirectedFile(...)`

### scanner_helpers.go
- `ScannerTokenLimit(inv)` — scanner token limit derived from invocation limits

### stdin_context.go
- `ReaderWithContext(ctx, reader)` — makes stdin reads observe context cancellation

### version_rendering.go
- `VersionInfo`
- `RenderSimpleVersion(...)`
- `RenderDetailedVersion(...)`

## Scope Rule

Keep `commands/` focused on reusable authoring contracts and helpers.
Implementation-specific parsing helpers, builtin-only filesystem helpers, and
shipped command implementations belong under `internal/`.
