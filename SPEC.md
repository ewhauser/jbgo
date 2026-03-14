# gbash

Status: Draft v0.1
Last updated: 2026-03-14

## 1. Purpose

`gbash` is a deterministic, policy-controlled, sandbox-only, bash-like runtime for AI agents.

It preserves the product idea behind Vercel's `just-bash` while making different implementation choices:

- shell parsing and evaluation are delegated to `mvdan/sh/v3`
- filesystem access is virtualized by default
- commands are implemented in Go and resolved through an explicit registry
- unknown commands never fall through to the host OS
- sandbox policy is part of the core runtime, not an optional mode

The target is not "Bash in Go". The target is a practical shell-shaped runtime for agentic coding and data workflows that must be deterministic, inspectable, and safe by default.

## 2. Product Definition

`gbash` is a shell runtime with the following product contract:

- it accepts shell-like scripts and command snippets
- it evaluates a pragmatic subset of shell semantics
- it runs entirely inside a sandboxed runtime
- it can expose structured traces and lifecycle logs for agent debugging and orchestration when the embedder opts in
- it uses a virtual filesystem unless a caller explicitly installs another sandboxed backend
- it never executes unknown commands on the host

The runtime is optimized for LLM and agent workloads:

- file inspection and transformation
- grep-like content search
- directory traversal
- data reshaping pipelines
- persistent multi-step agent sessions
- deterministic replay in tests
- policy-aware execution in long-running agent systems

## 3. Goals

1. Port the `just-bash` concept to a Go-native runtime named `gbash`.
2. Use `mvdan/sh/v3` for parsing, ASTs, expansion semantics, control flow, and interpreter behavior where feasible.
3. Support only sandbox mode.
4. Use explicit Go command implementations instead of host subprocesses.
5. Default to an in-memory or otherwise virtualized filesystem.
6. Expose deterministic observability hooks and execution results suitable for agent frameworks.
7. Keep the implementation small, explicit, and easy to reason about.

## 4. Non-Goals

`gbash` will not:

- implement full GNU Bash behavior
- provide job control, readline-style history navigation/editing, or host TTY emulation
- support host subprocess passthrough
- support a user-facing compatibility mode as part of the default runtime contract
- default to the host filesystem
- silently emulate missing commands with host binaries
- optimize for human shell convenience over agent determinism

## 5. Design Principles

### 5.1 Sandbox-only is a product decision

There is no runtime mode where command execution falls back to `exec.Command`, `/bin/sh`, or `/bin/bash`. Unknown commands return a clear shell-style failure, typically exit status `127`.

### 5.2 `mvdan/sh` owns shell semantics

We do not reimplement parsing, quoting, command substitution, loops, or shell AST traversal from scratch. Those responsibilities stay in `mvdan/sh/v3`.

The shell adapter may pre-validate parsed AST forms that are known to trigger `mvdan/sh` interpreter panics and convert them into normal shell errors instead. Unsupported descriptor-dup redirections are one example: they should surface as `invalid redirection`, not crash the runtime.

### 5.3 Project-owned boundaries

The runtime owns:

- filesystem abstraction
- command registry
- policy enforcement
- output limiting
- tracing
- execution result normalization

### 5.4 Determinism over compatibility

When Bash compatibility conflicts with deterministic, inspectable behavior for agents, we choose the deterministic option.

### 5.5 Small explicit surfaces

Every major subsystem should have a narrow interface. Callers should be able to replace the filesystem backend, registry, or observability callbacks without understanding `mvdan/sh` internals.

## 6. Runtime Architecture

The runtime is composed of five layers:

1. `syntax` parser layer from `mvdan/sh/v3`
2. shell execution adapter around `interp.Runner`
3. sandboxed project-owned filesystem abstraction
4. Go command registry
5. policy and trace layers

Execution flow:

1. Parse the script with `syntax.Parser`.
2. Construct an execution context from the current session with:
   - session-owned virtual filesystem
   - command registry
   - policy
   - optional trace recorder and logging callbacks
   - bounded stdout/stderr capture
3. Configure `interp.Runner` with project handlers for:
   - file open
   - stat
   - readdir
   - simple-call interception
   - command execution
4. Run the parsed program.
5. Normalize shell/interpreter errors into an `ExecutionResult`.
6. Return stdout, stderr, exit code, and structured trace events when tracing is enabled.

The CLI also provides a minimal interactive shell mode. That mode is a front-end over the same runtime, not a second execution engine:

- it keeps one `Session` alive for the duration of the interactive shell
- it uses `syntax.Parser.InteractiveSeq` to gather complete interactive statements and continuation prompts
- it executes each completed entry via `Session.Exec`
- it carries forward the virtual cwd and shell-visible variable state between entries at the CLI layer
- it may expose session-local command history via the `history` command, with entries stored in `BASH_HISTORY`

The normal CLI entrypoint also accepts filesystem selection flags before the shell arguments:

- `gbash --root <dir> ...` mounts `<dir>` read-only at `/home/agent/project` with an in-memory writable overlay
- `gbash --cwd <dir> ...` sets the initial sandbox working directory
- `gbash --readwrite-root <dir> ...` mounts `<dir>` as sandbox `/` so writes persist back to the host, but only when `<dir>` is inside the system temp directory
- when `--cwd` is omitted, `--root` starts at `/home/agent/project` and `--readwrite-root` starts at `/`

External test harnesses should use the normal CLI entrypoint together with the filesystem selection flags above. In particular, GNU-style wrapper scripts may invoke `gbash --readwrite-root <tempdir> --cwd <dir> -c 'exec "$@"' _ <utility> ...` so the harness exercises the same shell and runtime path as normal `gbash` execution.

That frontend is also exposed as a public `cli` package so shipped binaries can reuse the same flag parsing, version rendering, interactive behavior, and runtime setup:

- `cmd/gbash` is a thin wrapper over `github.com/ewhauser/gbash/cli`
- `contrib/extras/cmd/gbash-extras` is a thin wrapper over the same package with `contrib/extras` pre-registered into the runtime

### 6.1 Session model

`gbash` should expose a long-lived session abstraction.

- `Runtime` is a factory for configured sessions
- `Runtime.Run` is a one-shot convenience that creates a fresh session and discards it after execution
- `Session` owns the filesystem instance, command registry, policy, base environment, and default working directory
- each `Exec` call creates a fresh `interp.Runner`
- shell-local variables, shell functions, and option state are per-execution by default
- filesystem state persists across executions within the same session

This matches the agent workflow we care about: a sequence of shell calls operating on a shared sandboxed workspace, without requiring shell-local state to leak between calls unless we explicitly add that feature later.

### 6.2 Default sandbox layout

The default in-memory sandbox should look Unix-like enough for agent scripts:

- `/home/agent` as the default home and working directory
- `/tmp` for scratch files
- `/bin` and `/usr/bin` as virtual command locations
- `PATH=/usr/bin:/bin`
- deterministic identity defaults via `USER=agent`, `LOGNAME=agent`, `GROUP=agent`, `GROUPS=1000`, `UID=1000`, `EUID=1000`, `GID=1000`, `EGID=1000`, and `SHELL=/bin/sh`

Commands remain registry-backed Go implementations. `/bin/ls` and similar paths are virtual command identities, not host executables.

Because `mvdan/sh` currently validates `interp.Dir(...)` against the host filesystem, the runtime treats `PWD` as the authoritative virtual working directory and injects a small shell prelude that preserves virtual `cd` and `pwd` behavior without host directory access.

## 7. Proposed Package Layout

```text
cli/                   reusable CLI frontend shared by shipped binaries
cmd/gbash/             CLI entrypoint for local execution
internal/runtime/      internal runtime implementation and execution orchestration
shell/                mvdan/sh integration and handler wiring
fs/                   project-owned filesystem interfaces and virtual backends
network/              sandboxed HTTP client, allowlist matching, redirect checks
commands/             command registry, invocation context, core Go commands
contrib/<name>/       separate Go modules for optional heavyweight commands
packages/<name>/      publishable JavaScript/TypeScript packages
policy/               sandbox policy types and enforcement decisions
trace/                structured event model and recorder implementations
examples/             separate Go module for SDK demos and integration examples
tests/                integration fixtures and compatibility-style harnesses
```

Package responsibilities:

- `cli/`: reusable CLI frontend that parses shell flags, renders help/version output, handles interactive mode, and provisions runtimes for thin wrapper binaries
- `internal/runtime/`: internal runtime/session creation, run configuration, result collection, output capture
- `shell/`: parser and runner adapter; no product policy lives here
- `fs/`: POSIX-like path normalization, memory filesystem, host-backed lower layers, overlay, and snapshot backends
- `network/`: runtime-owned HTTP sandbox with URL-prefix allowlists, method controls, redirect revalidation, and response-size limits
- `commands/`: registry and Go-native command implementations such as `clear`, `echo`, `egrep`, `fgrep`, `grep`, `history`, `ls`, `pwd`, and `strings`
- `contrib/`: opt-in command modules that stay outside the root module dependency graph so heavyweight helpers do not inflate the core runtime. The repository may also expose umbrella contrib helpers such as `contrib/extras` to register the stable official contrib command set without changing the default runtime surface, and may ship official opt-in binaries such as `contrib/extras/cmd/gbash-extras` from the corresponding contrib module. Current examples include `awk`, `html-to-markdown`, `jq`, `nodejs`, `sqlite3`, and `yq`.
- `packages/`: publishable JavaScript and TypeScript packages. `packages/gbash-wasm` owns the `js/wasm` assets plus explicit host entrypoints such as `@ewhauser/gbash-wasm/browser` and `@ewhauser/gbash-wasm/node`.
- `policy/`: allowlists, root restrictions, size limits, network stance, and decision helpers
- `trace/`: event schema, recorder interfaces, and in-memory buffering
- `examples/`: runnable demos that can depend on external SDKs without affecting the root module build list
- `tests/`: black-box runtime tests and corpus-driven shell fixtures

We intentionally do not create a `compat/` package because external harness support should ride on the normal CLI and runtime surfaces, not a second execution API.

The repository itself should be maintained as a committed Go workspace plus a pnpm workspace. The root module stays focused on the runtime, CLI, and core commands, while direct children under `contrib/` are separate modules for optional heavyweight commands, `packages/` contains publishable JavaScript packages, and `examples/` is a separate module used for demos that may need external SDK dependencies or looser version pinning.

Optional language runtimes in `contrib/` must preserve the same sandbox contract as core commands. The current `contrib/nodejs` design is experimental and intentionally excluded from `contrib/extras` until its surface stabilizes. It uses `goja` plus a curated `goja_nodejs` allowlist, with gbash-owned replacements for host-sensitive modules such as `process`, `console`, `fs`, and `path`. It does not expose host subprocesses, host filesystem access, or unrestricted network APIs, and any supported file access must flow through `Invocation.FS`.

## 8. Core Interfaces

The initial API should stay small and stable.

```go
type Runtime struct {
    cfg Config
}

type Option func(*Config) error

type Config struct {
    FileSystem    FileSystemConfig
    Registry      commands.CommandRegistry
    Policy        Policy
    Engine        shell.Engine
    BaseEnv       map[string]string
    Network       *network.Config
    NetworkClient network.Client
    Tracing       TraceConfig
    Logger        LogCallback
}

func New(options ...Option) (*Runtime, error)

type FileSystemConfig struct {
    Factory    fs.Factory
    WorkingDir string
}

type Session struct {
    cfg Config
}

type ExecutionRequest struct {
    Name    string
    Script  string
    Args    []string
    Env     map[string]string
    WorkDir string
    ReplaceEnv bool
    Stdin   io.Reader
}

type ExecutionResult struct {
    ExitCode        int
    ShellExited     bool
    Stdout          string
    Stderr          string
    FinalEnv        map[string]string
    StartedAt       time.Time
    FinishedAt      time.Time
    Duration        time.Duration
    Events          []trace.Event
    StdoutTruncated bool
    StderrTruncated bool
}

type TraceMode uint8

const (
    TraceOff TraceMode = iota
    TraceRedacted
    TraceRaw
)

type TraceConfig struct {
    Mode    TraceMode
    OnEvent func(context.Context, trace.Event)
}

type LogKind string

type LogEvent struct {
    Kind        LogKind
    SessionID   string
    ExecutionID string
    Name        string
    WorkDir     string
    ExitCode    int
    Duration    time.Duration
    Output      string
    Truncated   bool
    ShellExited bool
    Error       string
}

type LogCallback func(context.Context, LogEvent)

type FileSystem interface {
    Open(ctx context.Context, name string) (File, error)
    OpenFile(ctx context.Context, name string, flag int, perm fs.FileMode) (File, error)
    Stat(ctx context.Context, name string) (fs.FileInfo, error)
    ReadDir(ctx context.Context, name string) ([]fs.DirEntry, error)
    MkdirAll(ctx context.Context, name string, perm fs.FileMode) error
    Remove(ctx context.Context, name string, recursive bool) error
    Rename(ctx context.Context, oldName, newName string) error
    Getwd() string
    Chdir(name string) error
}

type Command interface {
    Name() string
    Run(ctx context.Context, inv *Invocation) error
}

type CommandFunc func(ctx context.Context, inv *Invocation) error

func DefineCommand(name string, fn CommandFunc) Command

type Invocation struct {
    Args                  []string
    Env                   map[string]string
    Cwd                   string
    Stdin                 io.Reader
    Stdout                io.Writer
    Stderr                io.Writer
    FS                    *CommandFS
    Fetch                 FetchFunc
    Exec                  func(context.Context, *ExecutionRequest) (*ExecutionResult, error)
    Limits                Limits
    GetRegisteredCommands func() []string
}

type CommandFS struct {
    // runtime-owned, policy-aware filesystem facade for commands
}

func ReadAll(ctx context.Context, inv *Invocation, reader io.Reader) ([]byte, error)
func ReadAllStdin(ctx context.Context, inv *Invocation) ([]byte, error)
func (*CommandFS) ReadFile(ctx context.Context, name string) ([]byte, error)

type FetchFunc func(context.Context, *network.Request) (*network.Response, error)

type LazyCommandLoader func() (Command, error)

type CommandRegistry interface {
    Register(cmd Command) error
    RegisterLazy(name string, loader LazyCommandLoader) error
    Lookup(name string) (Command, bool)
    Names() []string
}

type Policy interface {
    AllowCommand(ctx context.Context, name string, argv []string) error
    AllowBuiltin(ctx context.Context, name string, argv []string) error
    AllowPath(ctx context.Context, action FileAction, path string) error
    Limits() Limits
}

type Event struct {
    Schema      string
    SessionID   string
    ExecutionID string
    Kind        trace.Kind
    At          time.Time
    Redacted    bool
    Command     *CommandEvent
    File        *FileEvent
    Policy      *PolicyEvent
    Message     string
    Error       string
}

type CommandEvent struct {
    Name             string
    Argv             []string
    Dir              string
    ExitCode         int
    Builtin          bool
    Position         string
    Duration         time.Duration
    ResolvedName     string
    ResolvedPath     string
    ResolutionSource string
}

type FileEvent struct {
    Action   string
    Path     string
    FromPath string
    ToPath   string
}

type PolicyEvent struct {
    Subject          string
    Reason           string
    Action           string
    Path             string
    Command          string
    ExitCode         int
    ResolutionSource string
}
```

The command-facing `Invocation` is capability-only. Custom commands get sandboxed filesystem and fetch helpers plus nested execution and limits metadata, but they do not receive the raw policy object, raw trace recorder, or raw network client. Policy checks and file-access tracing happen behind `Invocation.FS`, and `Invocation.Fetch` is nil unless network access is configured.

Registry semantics are override-friendly: later registrations replace earlier ones so embedders can swap in custom implementations for built-ins, and `RegisterLazy` defers expensive command setup until first execution while still participating in `PATH` resolution and `Names()`.

Key design decisions:

- `Runtime` is a concrete type. Callers should not need to mock it.
- `New` should accept composable runtime options, with helpers such as `WithRegistry`, `WithFileSystem`, `WithWorkspace`, `WithNetwork`, `WithHTTPAccess`, and `WithConfig` for callers that prefer either direct options or an existing `Config` value.
- `Session` is the primary unit of agent interaction.
- `FileSystem` is narrow and POSIX-shaped.
- filesystem state persists at the session level; shell-local state does not persist across executions by default
- `ReplaceEnv` starts from an empty per-execution environment instead of the session base environment
- `FinalEnv` reports the shell-visible variable state at the end of one execution and does not mutate the session base environment
- `ShellExited` reports that the execution invoked shell termination, such as the `exit` builtin; interactive front-ends should stop when it is true
- command implementations receive project context through `Invocation`, not through globals
- commands that need sub-execution should use the injected `Invocation.Exec` callback rather than reaching around the runtime
- `Invocation.Exec` inherits the current command environment and virtual working directory by default while staying inside the same session and policy boundary
- direct filesystem and text-processing commands should prefer `Invocation.FS` over nested shell execution
- commands that need whole-input reads should use `commands.ReadAll`, `commands.ReadAllStdin`, or `Invocation.FS.ReadFile` so `MaxFileBytes` and diagnostic behavior stay consistent
- orchestration-style commands such as `xargs`, `find -exec`, and shell-wrapper helpers should use `Invocation.Exec`
- policy is an explicit interface so embedders can swap simple allowlists for richer policy engines later

## 9. `mvdan/sh/v3` Integration Plan

### 9.1 Parser

Use `syntax.NewParser()` to parse the script into a `*syntax.File`.

We keep parsing separate from execution so we can:

- cache ASTs
- expose syntax errors clearly
- add trace/source references later

### 9.2 Runner construction

For each execution, build a fresh `interp.Runner` with:

- `interp.Env(...)`
- `interp.Dir(...)`
- `interp.Params("--", args...)`
- `interp.StdIO(stdin, stdout, stderr)`
- `interp.OpenHandler(...)`
- `interp.ReadDirHandler2(...)`
- `interp.StatHandler(...)`
- `interp.CallHandler(...)`
- `interp.ExecHandlers(...)`

We intentionally use `interp.ExecHandlers` with a middleware that never falls through to `DefaultExecHandler`. That preserves the sandbox-only contract while staying on the current `mvdan/sh` API.

We also always install an explicit environment with `interp.Env(...)`. The runtime must not inherit the host process environment by default, because that would weaken determinism and leak ambient state into agent execution.

Any middleware chain must remain closed over Go-native command execution. It must never delegate to `DefaultExecHandler`.

Implementation detail for MVP:

- `interp.Dir(...)` is set to a host-safe existing directory such as `/`, not the virtual sandbox cwd
- the runtime prepends a shell shim that initializes `PWD` and `OLDPWD`
- the shim owns virtual `cd` and wraps shell-visible `pwd` to the Go `pwd` command so `-L` / `-P` still honor virtual `PWD`
- all project path handlers resolve relative paths from virtual `PWD`, not from `HandlerContext.Dir`

### 9.3 Stdio

`StdIO` is wired to bounded buffers owned by `internal/runtime/`. This gives us:

- deterministic capture for agent frameworks
- policy-controlled output limits
- no direct dependency on host terminal behavior

### 9.4 File handlers

`OpenHandler`, `StatHandler`, and `ReadDirHandler2` bridge `mvdan/sh` into the project filesystem.

Responsibilities:

- resolve shell-relative paths against virtual `PWD`
- normalize paths using POSIX semantics
- enforce policy before touching the backend
- emit file access trace events when tracing is enabled
- call the selected `fs.FileSystem` backend

### 9.5 Call interception

`CallHandler` runs for every simple command, including builtins and functions. We use it for:

- recording expanded argv after shell expansion
- enforcing builtin allow/deny policy
- enforcing the per-execution command-count budget before dispatch
- optionally canonicalizing argv in future features

`CallHandler` does not execute commands. It is a pre-execution interception point.

Implementation detail for the current runtime:

- the default `MaxCommandCount` is `10000` per execution
- the counter resets on each `Session.Exec` or `Runtime.Run`
- commands inside subshells and pipelines count toward the same execution budget
- runtime-injected prelude commands for virtual `cd` and `pwd` must not consume the user-visible command budget
- loop iteration limits are enforced by AST instrumentation that prepends an internal guard command to loop bodies before execution
- command substitution depth and glob-operation budgets are validated against the parsed user program before runtime prelude injection
- request-level timeouts and caller cancellation are enforced via execution contexts and normalized into shell-style exit codes

### 9.6 Command execution

Our `ExecHandlers` middleware is the command dispatch path for non-builtin, non-function calls.

Flow:

1. receive expanded argv from `mvdan/sh`
2. resolve `argv[0]` against virtual command paths from the current `PATH`, or against an explicit virtual path such as `/bin/ls`
3. if missing, write a shell-style error to stderr and return exit status `127`
4. if present, run the Go command implementation
5. convert command errors into shell exit status errors
6. emit start/finish trace events when tracing is enabled

This preserves shell syntax while keeping all execution inside Go.

User-visible command lookup rules for MVP:

- bare command names only resolve if the current `PATH` includes a virtual command stub for that name
- changing `PATH` can intentionally disable bare-name resolution
- explicit virtual paths such as `/bin/ls` bypass `PATH`
- there is no direct registry fallback for user-visible commands

## 10. Filesystem Model

The filesystem abstraction is deliberately smaller than `os`:

- file open
- stat
- lstat
- readdir
- readlink
- realpath
- mkdir
- remove
- rename
- working directory state

Important properties:

- paths use POSIX semantics internally
- the default backend is in-memory
- the default backend exposes a Unix-like virtual layout rooted at `/`
- host-backed filesystems must still satisfy policy checks and must never imply host command execution
- a read-write host-backed filesystem may be enabled explicitly for external test harnesses or advanced embedding, but it is not the default runtime backend
- shell redirects and command file access share the same filesystem view
- symlink support is optional and must default to the safer behavior when policy is ambiguous
- for backends without symlink creation/traversal support, `Lstat` behaves like `Stat`, `Readlink` fails for non-symlinks, and `Realpath` resolves only existing virtual paths

The abstraction should remain narrow, but it must be allowed to grow where agent workflows clearly require it.

Implementation detail for the current runtime:

- `Lstat`, `Readlink`, and `Realpath` are now part of the core interface because path introspection is needed for future agent commands and safer path handling
- command-facing copy semantics stay in `commands/`, where policy and shell-facing errors already live
- `fs/` may use private clone helpers internally for backend composition, but that is not the same as moving user-visible `cp` semantics into the filesystem layer
- `MemoryFS` stores symlink entries directly for testing and path-safety enforcement, but the runtime still defaults to `SymlinkDeny`
- `MemoryFS.Stat`, `Open`, `ReadDir`, `Chdir`, and `Realpath` follow symlinks; `Lstat`, `Readlink`, `Remove`, and `Rename` operate on the symlink entry itself

Current and planned backends:

- `MemoryFS`: default mutable sandbox
- `HostFS`: read-only host-backed directory view mounted at a configurable virtual root with sanitized errors and a backend-local regular-file read cap
- `ReadWriteFS`: mutable host-backed directory view rooted at `/` with sanitized errors and a backend-local regular-file read cap for opt-in host-backed workflows
- `OverlayFS`: copy-on-write backend with a read-only lower layer, writable in-memory upper layer, merged `readdir`, and tombstones for deletions
- `SnapshotFS`: deterministic read-only clone of another filesystem for tests and replay fixtures

Backend boundary for the current implementation:

- `gbash.Config.FileSystem` is the public setup boundary for session storage and starting directory; callers should not have to coordinate separate runtime knobs to mount a backend and choose the initial working directory
- `HostFS` is an opt-in lower-layer backend exposed through `gbfs.Host(...)`; it is intended to sit underneath `gbfs.Overlay(...)`, not to replace the default in-memory runtime path
- `ReadWriteFS` is an opt-in mutable backend exposed through `gbfs.ReadWrite(...)`; it is intended for developer tooling, external test harnesses, and embedders that explicitly want host mutations
- `OverlayFS` is intended for internal session use and is exposed through `gbfs.Overlay(...)`
- `SnapshotFS` is a read-only backend for deterministic fixtures and direct tests
- `SnapshotFS` is not the default `runtime` session backend because session bootstrap still creates the sandbox layout and command stubs
- the common host-project workflow should be represented as a high-level runtime helper that mounts a read-only host tree under an in-memory overlay and starts the session in that mounted directory

## 12. Policy Model

Policy is evaluated in-process and is mandatory.

Initial policy surface:

- allowed command set
- allowed builtin set
- allowed read roots
- allowed write roots
- stdout/stderr byte limits
- maximum file size
- maximum commands per execution
- maximum loop iterations
- maximum command substitution depth
- maximum glob operations
- symlink policy
- cancellation and timeout handling
- network disabled unless a sandboxed network client is explicitly configured

Default policy stance for MVP:

- command allowlist derived from the registered command set
- builtin allowlist permissive for core shell features
- reads and writes allowed inside `/`
- maximum command count defaults to `10000` per execution
- maximum loop iterations default to `10000` per loop
- maximum command substitution depth defaults to `50` per execution
- maximum glob operations default to `100000` per execution
- symlink traversal denied unless a filesystem backend and policy explicitly allow it
- request-level timeout is opt-in, but when it fires the execution must stop with exit code `124`
- caller-driven cancellation must stop the execution with exit code `130`
- no ambient network access; `curl` is absent unless the runtime enables the sandboxed network client

The policy package should be able to answer three questions:

1. may this command run?
2. may this builtin run?
3. may this path be read or written?

Network policy for the current runtime is enforced by the dedicated `network/` layer rather than by generic shell evaluation. Commands never receive host `http.Client` access directly; they only receive the runtime-owned sandboxed `network.Client`.

Path-policy enforcement rule for the current runtime:

- the lexical path the user asked for is checked first
- if the backend resolves that path through a symlink, the resolved target path is checked before backend access
- in `SymlinkDeny` mode, any attempted traversal through a symlink fails even if both lexical and resolved paths would otherwise be allowed

## 13. Trace Model

Tracing is opt-in at the runtime boundary.

When enabled, each execution should emit structured events such as:

- command argv after expansion
- command start and finish
- command resolution source
- file read/open/stat/readdir
- file write/create/remove/rename
- policy denials
- working directory
- timestamps and durations
- exit code
- session identifier and execution identifier

Tracing should be useful both for debugging and for building higher-level agent tooling. The event model should favor stable, structured fields over log-style strings.

The root runtime also exposes top-level logging callbacks for `exec.start`, `stdout`, `stderr`, `exec.finish`, and `exec.error`. Logging is callback-only and does not add new fields to `ExecutionResult`.

Implementation detail for the current runtime:

- the schema is project-owned and versioned as `gbash.trace.v1`
- the core runtime does not adopt OpenTelemetry as its event schema or transport contract
- tracing is disabled by default; `ExecutionResult.Events` is empty unless the embedder enables tracing
- `TraceRedacted` is the recommended default and redacts secret-bearing argv values before events are returned or emitted
- `TraceRaw` preserves full argv and path metadata and is unsafe unless the embedder controls sinks and retention
- interactive executions only emit trace callbacks; they do not return events
- every event carries `session_id` and `execution_id`
- redacted events set `redacted=true`
- command events carry `resolved_name`, `resolved_path`, and `resolution_source`
- path-policy and command-policy failures emit explicit `policy.denied` events
- file mutations emit `file.mutation` events alongside lower-level file access events when useful
- the trace schema should grow by additive fields and new event kinds rather than by overloading free-form messages

## 14. Error Handling

Errors fall into four categories:

1. parse errors
2. policy denials
3. command-level execution failures
4. internal runtime errors
