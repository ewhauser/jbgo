# gbash

Status: Draft v0.1
Last updated: 2026-03-21

## 1. Purpose

`gbash` is a deterministic, policy-controlled, sandbox-only, bash-like runtime for AI agents.

It preserves the product idea behind Vercel's `just-bash` while making different implementation choices:

- shell parsing and evaluation are owned in-tree under `internal/shell`
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
- CSV inspection and reshaping via registry-backed tooling such as `xan`
- directory traversal
- data reshaping pipelines
- persistent multi-step agent sessions
- deterministic replay in tests
- policy-aware execution in long-running agent systems

## 3. Goals

1. Port the `just-bash` concept to a Go-native runtime named `gbash`.
2. Keep parsing, ASTs, expansion semantics, control flow, and interpreter behavior inside the project-owned `internal/shell` tree.
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

### 5.2 The shell core owns shell semantics

We do not reimplement parsing, quoting, command substitution, loops, or shell AST traversal from scratch during execution. Those responsibilities stay inside the in-tree shell packages under `internal/shell`.

The parser, expansion, pattern, and interpreter packages live in-tree as `internal/shell/syntax`, `internal/shell/expand`, `internal/shell/pattern`, and `internal/shell/interp`, and `internal/runtime` only calls the concrete `internal/shell` entrypoints.

The shell core also owns Bash-style stack introspection state. `BASH_SOURCE`, `BASH_LINENO`, `BASH_EXECUTION_STRING`, `FUNCNAME`, `caller`, sourced-file provenance, and top-level file-backed `$0` semantics are tracked inside the in-tree interpreter rather than synthesized in a shell prelude.

The shell core also owns trap and signal semantics. `trap`, pseudo-signals such as `EXIT`, `ERR`, `DEBUG`, and `RETURN`, real-signal registration and printing, subshell trap inheritance rules, and shell-visible signal routing for `kill $$`, `kill $BASHPID`, and background-job targets are implemented in the in-tree interpreter rather than delegated to host process behavior. Catchable host signals such as Ctrl-C are bridged into the active foreground shell family through runtime-owned signal handlers, while non-catchable signals and job control remain outside the product contract. Failed pipelines are part of that contract: `ERR` is evaluated once per failed pipeline after the final pipeline status and `PIPESTATUS` array have been established, rather than once per failing component.

The shell core compiles user input as a sequence of complete parsed chunks before execution. In non-interactive `Run`, the core incrementally reparses the growing script buffer so parse-time state such as aliases and `shopt` flags can affect later commands in the same execution. Each complete chunk then goes through the same validation, budget checks, pipeline rewriting, and loop instrumentation before the runner sees that chunk's AST.

The shell core may also apply small AST normalizations when the in-tree parser or interpreter behavior diverges from the Bash semantics we intend to preserve. One example is wrapping the right-hand side of pipelines in synthetic subshells so default parent-shell state matches Bash's `lastpipe=off` behavior while still allowing the interpreter to unwrap those specific synthetic wrappers when `shopt -s lastpipe` is enabled.

Process substitution is supported as a sandbox-native shell feature. The shell core must provision runtime-owned opaque pipe paths under the sandbox namespace and must not rely on host FIFOs, host-visible `TMPDIR` paths, or host path semantics to implement `<(...)` and `>(...)`.

Registry-backed replacements for Bash builtins should preserve shell-visible Bash coercions when practical. `printf` is a concrete compatibility boundary: numeric conversions must accept quoted character constants such as `"'A"` and `"\"B"`, `%q` and `${var@Q}` must emit Bash-compatible shell-escaped strings, `%b` and bare format-string escapes must honor Bash's escape decoding rules, `%(... )T` must consult only exported `TZ`, and write failures must still surface shell status `1` after any partial output or diagnostics.

Shell builtins that remain implemented inside the in-tree interpreter should preserve the Bash-facing option contracts we depend on for conformance. Current requirements include Bash-compatible `type` resolution and reporting for `-a`, `-f`, `-p`, `-P`, and `-t` across aliases, functions, builtins, keywords, and PATH files, plus Bash-compatible `set -C` / `set +C`, `set -o noclobber` / `set +o noclobber`, and `set -o posix` / `set +o posix` handling. In POSIX mode, direct POSIX special builtins must outrank shell functions during command lookup, report as builtins via `type`, and keep prefix assignments in the current shell rather than treating them as temporary exported command variables.

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

Every major subsystem should have a narrow interface. Callers should be able to replace the filesystem backend, registry, or observability callbacks without understanding shell implementation internals.

## 6. Runtime Architecture

The runtime is composed of five layers:

1. in-tree `syntax` parser layer under `internal/shell`
2. project-owned shell core and runner orchestration
3. sandboxed project-owned filesystem abstraction
4. Go command registry
5. policy and trace layers

Execution flow:

1. Parse the script with `syntax.Parser`, incrementally when shell state like aliases must affect later complete commands in the same execution.
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
4. Run each compiled chunk in order on one `interp.Runner`, preserving shell state across chunks.
5. Normalize shell/interpreter errors into an `ExecutionResult`.
6. Return stdout, stderr, exit code, and structured trace events when tracing is enabled.

The CLI also provides a minimal interactive shell mode. That mode is a front-end over the same runtime, not a second execution engine:

- it keeps one `Session` alive for the duration of the interactive shell
- it uses `syntax.Parser.InteractiveSeq` to gather complete interactive statements and continuation prompts
- it executes each completed entry via `Session.Exec`, using the same runner-backed parser construction that feeds live alias state into parse-time expansion
- it carries forward the virtual cwd and shell-visible variable state between entries at the CLI layer
- it may expose session-local command history via the `history` command, with entries stored in `BASH_HISTORY`
- it supports programmable completion state via the `complete`, `compgen`, and `compopt` shell builtins; bare calls plus `builtin ...` and `command ...` dispatch resolve to those shell builtins, while `/bin/complete`, `/bin/compgen`, and `/bin/compopt` expose the same shared completion logic as command wrappers; the shipped CLI still does not provide a readline/tab-completion frontend

The normal CLI entrypoint also accepts filesystem selection flags before the shell arguments:

- `gbash --root <dir> ...` mounts `<dir>` read-only at `/home/agent/project` with an in-memory writable overlay
- `gbash --cwd <dir> ...` sets the initial sandbox working directory
- `gbash --readwrite-root <dir> ...` mounts `<dir>` as sandbox `/` so writes persist back to the host, but only when `<dir>` is inside the system temp directory
- `gbash --json ...` emits one JSON object for a non-interactive execution with `stdout`, `stderr`, `exitCode`, truncation flags, timing metadata, and optional trace metadata when tracing is enabled
- `gbash --server --socket <path>` serves a long-lived JSON-RPC protocol over a Unix domain socket instead of executing a script
- `gbash --server --listen <host:port>` serves the same protocol over an explicit loopback TCP listener instead of executing a script
- `gbash --session-ttl <duration>` controls how long idle server sessions survive without active work
- when `--cwd` is omitted, `--root` starts at `/home/agent/project` and `--readwrite-root` starts at `/`

External test harnesses should use the normal CLI entrypoint together with the filesystem selection flags above. In particular, GNU-style wrapper scripts may invoke `gbash --readwrite-root <tempdir> --cwd <dir> -c 'exec "$@"' _ <utility> ...` so the harness exercises the same shell and runtime path as normal `gbash` execution.

That frontend is also exposed as a public `cli` package so shipped binaries can reuse the same flag parsing, version rendering, interactive behavior, JSON result rendering, and runtime setup:

- `cmd/gbash` is a thin wrapper over `github.com/ewhauser/gbash/cli`
- `contrib/extras/cmd/gbash-extras` is a thin wrapper over the same package with `contrib/extras` pre-registered into the runtime
- `github.com/ewhauser/gbash/server` is the shared public server surface used by both wrapper binaries to host the same session protocol over Unix sockets or caller-provided listeners

### 6.1 Session model

`gbash` should expose a long-lived session abstraction.

- `Runtime` is a factory for configured sessions
- `Runtime.Run` is a one-shot convenience that creates a fresh session and discards it after execution
- `Session` owns the filesystem instance, command registry, policy, base environment, and default working directory
- each `Exec` call creates a fresh `interp.Runner`
- shell-local variables, shell functions, and option state are per-execution by default
- programmable completion specs created by `complete` and modified by `compopt` are runner-local shell state shared by the shell builtins and the `/bin/complete` and `/bin/compopt` wrappers: they persist within one `Exec` call and within an interactive shell session, but not across separate `Session.Exec` calls
- filesystem state persists across executions within the same session

This matches the agent workflow we care about: a sequence of shell calls operating on a shared sandboxed workspace, without requiring shell-local state to leak between calls unless we explicitly add that feature later.

### 6.2 Server mode

`gbash` should also expose a local-first server mode for hosts that want a long-lived control endpoint instead of direct in-process method calls.

- the server protocol is JSON-RPC 2.0 over either a Unix domain socket or a caller-provided listener such as loopback TCP
- the protocol is JSON-RPC 2.0 request/response, not a custom streaming transport
- the shared CLI requires exactly one transport flag: `--socket` or `--listen`
- the shared CLI restricts `--listen` to loopback hosts because v1 has no authentication layer
- the Unix socket helper must chmod the socket to `0600`, reject an already-active socket path, and only replace stale socket files
- `session_id` maps 1:1 to a persistent `Session`
- filesystem shape is configured once at server startup through the normal runtime options and is not part of the wire protocol
- the primary remote operation is `session.exec`, which runs one non-interactive `Session.Exec` call and returns the full execution result in one response
- multiple sessions may be active concurrently across multiple client connections
- a single session permits at most one active `session.exec` call at a time because `Session.Exec` is serialized
- clients are expected to reconnect or open a second socket when they need concurrent requests; v1 does not require multiplexed event streams on one connection

Recommended v1 methods:

- `system.hello`, `system.ping`
- `session.create`, `session.get`, `session.list`, `session.destroy`
- `session.exec`

Recommended v1 result shape for `session.exec`:

- `exit_code`, `stdout`, `stderr`
- `stdout_truncated`, `stderr_truncated`
- `final_env`, `shell_exited`, `control_stderr`
- timing metadata such as `started_at`, `finished_at`, and `duration_ms`
- the updated session summary

Recommended v1 non-goals:

- interactive shell streaming over the protocol
- attach/detach and replay buffers
- filesystem RPC
- PTY resize and host TTY emulation
- signal forwarding and job control
- restart-persistent sessions

### 6.3 Default sandbox layout

The default in-memory sandbox should look Unix-like enough for agent scripts:

- `/home/agent` as the default home and working directory
- `/tmp` for scratch files, created with sticky-bit semantics
- `/dev` as a small runtime-owned device namespace
- `/dev/null` as a character device that always reads EOF and discards writes
- `/dev/urandom` as a character device that yields a deterministic pseudo-random byte stream and discards writes
- `/dev/zero` as a character device that yields zero bytes on reads and discards writes
- `/bin` and `/usr/bin` as virtual command locations
- deterministic identity defaults via `USER=agent`, `LOGNAME=agent`, `GROUP=agent`, `GROUPS=1000`, `UID=1000`, `EUID=1000`, `GID=1000`, and `EGID=1000`

Commands remain registry-backed Go implementations. `/bin/ls` and similar paths are virtual command identities, not host executables.

Ownership name resolution for commands such as `ls`, `chown`, and `chgrp` must come from those runtime identity defaults plus sandbox-visible `/etc/passwd` and `/etc/group` data when present. The runtime must not consult host account databases outside the sandbox contract.

Programmable user completion follows the same boundary: `compgen -A user` may use `USER` plus sandbox-visible `/etc/passwd`, but it must not read host account databases outside the sandbox contract.

The runtime owns the reserved `/dev` entries rather than relying on each filesystem backend to create backend-specific stand-ins. Additional `/dev/*` paths may exist when tests or callers seed them, but only runtime-defined entries such as `/dev/null`, `/dev/urandom`, and `/dev/zero` are guaranteed by default.

The shell initializes shell-owned startup state rather than inheriting host defaults. When callers omit them, the runner must synthesize `PATH=/usr/bin:/bin`, exported `PWD` matching the virtual working directory, `PS4="+ "`, the default `IFS`, readonly `SHELLOPTS`, and `SHELL=/bin/sh`; `HISTFILE` is only initialized for interactive shells. `HOME` is not synthesized by the shell, so an execution with a cleared environment still observes `HOME` as unset unless the caller explicitly provides it.

The runtime treats the runner's virtual directory plus shell-visible `PWD` as the authoritative working-directory state. Shell variable assignments, shell-owned startup state, `BASH_HISTORY`, `BASH_EXECUTION_STRING`, and `GBASH_UMASK` are synchronized by direct runner mutation APIs rather than by prepending trusted shell code, so syntax errors, traces, and `BASH_LINENO` always use real user line numbers.

Sandbox-facing machine metadata is also runtime-owned: `HOSTNAME`, `hostname`, `OSTYPE`, `BASHPID`, and `PPID` must resolve from sandbox metadata and runner state rather than from host uname or host process inspection.

Signal identity is part of that runtime-owned metadata. Stable `$$` values, per-shell `BASHPID`, shell-family `PPID`, virtual background job IDs, and internal signal delivery for shell-managed `kill` targets must be derived from runner state rather than host PIDs.

## 7. Proposed Package Layout

```text
cli/                   reusable CLI frontend shared by shipped binaries
cmd/gbash/             CLI entrypoint for local execution
server/                public JSON-RPC server surface shared by wrapper binaries
internal/runtime/      internal runtime implementation and execution orchestration
internal/shell/       project-owned shell core plus parser/expand/interpreter packages
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
- `server/`: public shared server implementation that owns JSON-RPC framing and session registries for both shipped CLIs and external hosts, plus Unix-socket listener helpers
- `internal/runtime/`: internal runtime/session creation, run configuration, result collection, output capture
- `internal/shell/`: concrete shell core entrypoints plus the in-tree `syntax`, `expand`, `pattern`, and `interp` packages; no product policy lives here
- `fs/`: POSIX-like path normalization, memory filesystem, host-backed lower layers, overlay, and snapshot backends
- `network/`: runtime-owned HTTP sandbox with origin- and path-boundary-aware allowlists, method controls, redirect revalidation, and response-size limits
- `commands/`: registry and Go-native command implementations such as `clear`, `compadjust`, `complete`, `compgen`, `compopt`, `echo`, `egrep`, `fgrep`, `grep`, `history`, `ls`, `mkfifo`, `pwd`, `strings`, and `xan`
- `contrib/`: opt-in command modules that stay outside the root module dependency graph so heavyweight helpers do not inflate the core runtime. The repository may also expose umbrella contrib helpers such as `contrib/extras` to register the stable official contrib command set without changing the default runtime surface, and may ship official opt-in binaries such as `contrib/extras/cmd/gbash-extras` from the corresponding contrib module. Current examples include `awk`, `html-to-markdown`, `jq`, `nodejs`, `sqlite3`, and `yq`.
- `packages/`: publishable JavaScript and TypeScript packages. `packages/gbash-wasm` owns the `js/wasm` assets plus explicit host entrypoints such as `@ewhauser/gbash-wasm/browser` and `@ewhauser/gbash-wasm/node`.
- `policy/`: allowlists, root restrictions, size limits, network stance, and decision helpers
- `trace/`: event schema, recorder interfaces, and in-memory buffering
- `examples/`: runnable demos that can depend on external SDKs without affecting the root module build list
- `tests/`: black-box runtime tests and corpus-driven shell fixtures

We intentionally do not create a `compat/` package because external harness support should ride on the normal CLI and runtime surfaces, not a second execution API.

The repository itself should be maintained as a committed Go workspace plus a pnpm workspace. The root module stays focused on the runtime, CLI, and core commands, while direct children under `contrib/` are separate modules for optional heavyweight commands, `packages/` contains publishable JavaScript packages, and `examples/` is a separate module used for demos that may need external SDK dependencies or looser version pinning.

Top-level repository directories such as `cmd/`, `contrib/`, `packages/`,
`scripts/`, and `third_party/` may also carry doc-only package comments so
pkg.go.dev can render repository layout pages and directory synopses. Those
overview packages are for navigation and documentation only; supported Go APIs
remain the concrete runtime packages and documented nested modules.

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
    Name          string
    ScriptPath    string
    Script        string
    Command       []string
    Interpreter   string
    PassthroughArgs []string
    Args          []string
    StartupOptions []string
    Env           map[string]string
    WorkDir       string
    Timeout       time.Duration
    ReplaceEnv bool
    Interactive bool
    Stdin       io.Reader
    Stdout      io.Writer
    Stderr      io.Writer
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
    Mkfifo(ctx context.Context, name string, perm fs.FileMode) error
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
func (*CommandFS) Mkfifo(ctx context.Context, name string, perm fs.FileMode) error
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
- `Invocation.Exec` supports two non-interactive modes: `Script` for nested shell execution and `Command` for already-tokenized argv execution without shell parsing
- `Script` and `Command` are mutually exclusive; `Args` applies to script mode positional parameters
- direct filesystem and text-processing commands should prefer `Invocation.FS` over nested shell execution
- commands that need whole-input reads should use `commands.ReadAll`, `commands.ReadAllStdin`, or `Invocation.FS.ReadFile` so `MaxFileBytes` and diagnostic behavior stay consistent
- orchestration-style commands such as `xargs`, `find -exec`, and shell-wrapper helpers should use `Invocation.Exec`
- policy is an explicit interface so embedders can swap simple allowlists for richer policy engines later

## 9. Shell Core Integration Plan

### 9.1 Compile pipeline

`internal/shell` owns one compile pipeline shared by interactive and non-interactive execution.

For script execution, that pipeline:

1. parses the request text into a `*syntax.File`
2. validates unsupported constructs such as descriptor-dup redirections that would otherwise panic the interpreter
3. checks command-substitution depth and accumulates glob-operation budgets as parsed chunks enter the compile pipeline
4. rewrites pipeline right-hand sides into synthetic subshells where needed to preserve default `lastpipe=off` behavior
5. instruments loop guards for `MaxLoopIterations`

The runtime request boundary carries only script text or tokenized command input. Parsing is a private shell-core concern.

### 9.2 Runner construction

For each execution, build a fresh `interp.Runner` through one shell-owned constructor path rather than ad hoc option hooks or split config layers.

That runner construction path carries:

- explicit environment and virtual directory
- stdio, startup options, positional parameters, and interactive mode
- call and exec handlers
- file, stat, readdir, realpath, and process-substitution handlers

Per-run metadata such as the top-level script path for file-backed `$0`/`main` stack frames and synthetic pipeline metadata for `lastpipe` is applied through one shell-owned run-preparation path.

The runtime never inherits the host process environment by default, and the command execution path never falls through to host subprocess execution.

Implementation detail for the current runtime:

- the runner's virtual directory is authoritative for filesystem behavior
- shell-visible `PWD` may differ from the cleaned virtual directory when a visible logical path is still valid
- the runner reset path initializes shell-owned startup variables (`PATH`, exported `PWD`, `PS4`, `IFS`, `SHELLOPTS`, `SHELL`, and interactive `HISTFILE`) while preserving an unset `HOME` when the execution environment omits it
- `let` is handled natively by the in-tree `syntax.LetClause` AST node
- all project path handlers resolve relative paths from virtual `PWD`, not from host cwd
- the in-tree runner keeps an execution-frame stack for `main`, `source`, and shell-function calls and derives `BASH_SOURCE`, `BASH_LINENO`, `BASH_EXECUTION_STRING`, `FUNCNAME`, and `caller` from that stack
- shell vars, `BASH_HISTORY`, and `GBASH_UMASK` are synchronized through direct runner mutation APIs rather than bootstrap `eval` calls
- redirect compatibility work must not implicitly expand the product contract for background execution: job control remains unsupported, and this runtime does not promise separate asynchronous redirect-restoration semantics for `cmd &`

### 9.3 Stdio

Runner stdio is wired to bounded buffers owned by `internal/runtime/`. This gives us:

- deterministic capture for agent frameworks
- policy-controlled output limits
- no direct dependency on host terminal behavior

### 9.4 File handlers

The shell-core file handlers bridge the in-tree parser and interpreter into the project filesystem.

Responsibilities:

- resolve shell-relative paths against virtual `PWD`
- normalize paths using POSIX semantics
- enforce policy before touching the backend
- emit file access trace events when tracing is enabled
- call the selected `fs.FileSystem` backend

### 9.5 Call interception

The runner call handler runs for every simple command, including builtins and functions. We use it for:

- recording expanded argv after shell expansion
- enforcing builtin allow/deny policy
- enforcing the per-execution command-count budget before dispatch
- optionally canonicalizing argv in future features

The call handler does not execute commands. It is a pre-execution interception point.

Implementation detail for the current runtime:

- the default `MaxCommandCount` is `10000` per execution
- the counter resets on each `Session.Exec` or `Runtime.Run`
- commands inside subshells and pipelines count toward the same execution budget
- loop iteration limits are enforced by AST instrumentation that prepends an internal guard command to loop bodies before execution
- command substitution depth and glob-operation budgets are enforced from the shell-core compile pipeline, and glob-operation counts accumulate across all parsed chunks in one execution
- request-level timeouts and caller cancellation are enforced via execution contexts and normalized into shell-style exit codes

### 9.6 Command execution

The runner exec handler is the command dispatch path for non-builtin, non-function calls.

Flow:

1. receive expanded argv from the shell interpreter
2. resolve `argv[0]` against virtual command paths from the current `PATH`, or against an explicit virtual path such as `/bin/ls`
3. if missing, write a shell-style error to stderr and return exit status `127`
4. if present, run the Go command implementation
5. convert command errors into shell exit status errors
6. emit start/finish trace events when tracing is enabled

This preserves shell syntax while keeping all execution inside Go.

User-visible command lookup rules for MVP:

- bare command names only resolve if the current `PATH` includes a virtual command stub for that name
- bare-name resolution is cached per shell session in a Bash-style hash table keyed by command name
- cached bare-name entries store the shell-visible path candidate, so relative PATH entries stay relative until invalidated
- `hash` exposes that table: `hash` prints it, `hash name ...` pre-resolves entries with zero hits, and `hash -r` clears it before optionally re-hashing any remaining names
- cached entries are invalidated only by `hash -r` or any reassignment/unset of `PATH`; otherwise the shell keeps using the cached path even if a different earlier PATH entry appears later
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
- mkfifo
- remove
- rename
- working directory state

Important properties:

- paths use POSIX semantics internally
- the default backend is in-memory
- the default backend exposes a Unix-like virtual layout rooted at `/`
- the runtime may reserve a small synthetic namespace such as `/dev/null`, `/dev/urandom`, and `/dev/zero` above any backend so shell-visible device behavior stays consistent across in-memory and host-backed filesystems
- host-backed filesystems must still satisfy policy checks and must never imply host command execution
- a read-write host-backed filesystem may be enabled explicitly for external test harnesses or advanced embedding, but it is not the default runtime backend
- shell redirects and command file access share the same filesystem view
- symlink support is optional and must default to the safer behavior when policy is ambiguous
- for backends without symlink creation/traversal support, `Lstat` behaves like `Stat`, `Readlink` fails for non-symlinks, and `Realpath` resolves only existing virtual paths

The abstraction should remain narrow, but it must be allowed to grow where agent workflows clearly require it.

Implementation detail for the current runtime:

- `Lstat`, `Readlink`, and `Realpath` are now part of the core interface because path introspection is needed for future agent commands and safer path handling
- command-facing copy semantics stay in `commands/`, where policy and shell-facing errors already live
- `mkfifo` is a shipped registry command, so the filesystem interface exposes named-pipe creation directly and `MemoryFS` can persist FIFO entries alongside regular files and symlinks
- `fs/` may use private clone helpers internally for backend composition, but that is not the same as moving user-visible `cp` semantics into the filesystem layer
- the runtime wraps the configured backend with a tiny virtual-device layer; today that layer reserves `/dev`, `/dev/null`, `/dev/urandom`, and `/dev/zero`, while non-reserved `/dev/*` entries still come from the underlying sandbox filesystem when present
- `MemoryFS` stores symlink entries directly for testing and path-safety enforcement, but the runtime still defaults to `SymlinkDeny`
- `MemoryFS.Stat`, `Open`, `ReadDir`, `Chdir`, and `Realpath` follow symlinks; `Lstat`, `Readlink`, `Remove`, and `Rename` operate on the symlink entry itself
- `MemoryFS` may also hold lazy regular-file providers that materialize on first content-sensitive access such as `Open`, `Stat`, `Lstat`, or `DirEntry.Info`

Current and planned backends:

- `MemoryFS`: default mutable sandbox
- `SeededMemory`: in-memory factory seeded with eager or lazy per-path files for a fresh session
- `TrieFS`: experimental read-optimized in-memory backend that stores a path-segment trie with separate dentries and inodes; intended for static or read-mostly trees, mounted datasets, and shared lower layers
- `HostFS`: read-only host-backed directory view mounted at a configurable virtual root with sanitized errors and a backend-local regular-file read cap
- `ReadWriteFS`: mutable host-backed directory view rooted at `/` with sanitized errors and a backend-local regular-file read cap for opt-in host-backed workflows
- `OverlayFS`: copy-on-write backend with a read-only lower layer, writable in-memory upper layer, merged `readdir`, and tombstones for deletions
- `MountableFS`: multi-mount namespace over a base filesystem plus sibling mount points, with synthetic parent directories and path routing handled inside the backend
- `SnapshotFS`: deterministic read-only clone of another filesystem for tests and replay fixtures

Backend boundary for the current implementation:

- `gbash.Config.FileSystem` is the public setup boundary for session storage and starting directory; callers should not have to coordinate separate runtime knobs to mount a backend and choose the initial working directory
- `SeededMemory` and `gbash.SeededInMemoryFileSystem(...)` are the productized seed path for eager or lazy per-file session bootstrap
- `TrieFS` is an opt-in experimental backend exposed through `gbfs.Trie()` and `gbfs.SeededTrie(...)`
- the preferred shared-lower composition for read-mostly trie data is `gbfs.Reusable(gbfs.SeededTrie(...))`
- `TrieFS` is intended for static or read-mostly in-memory data and mounted datasets; it is not the default `runtime` session backend and is not the recommended path for `WithWorkspace`, `HostDirectoryFileSystem(...)`, or other live host-backed filesystem flows
- `HostFS` is an opt-in lower-layer backend exposed through `gbfs.Host(...)`; it is intended to sit underneath `gbfs.Overlay(...)`, not to replace the default in-memory runtime path
- `ReadWriteFS` is an opt-in mutable backend exposed through `gbfs.ReadWrite(...)`; it is intended for developer tooling, external test harnesses, and embedders that explicitly want host mutations
- `OverlayFS` is intended for internal session use and is exposed through `gbfs.Overlay(...)`
- `MountableFS` is an opt-in namespace backend exposed through `gbfs.Mountable(...)` and `gbash.MountableFileSystem(...)`; live `mount` and `unmount` behavior remains a concrete-backend capability rather than part of the core filesystem interface
- `SnapshotFS` is a read-only backend for deterministic fixtures and direct tests
- `SnapshotFS` is not the default `runtime` session backend because session bootstrap still creates the sandbox layout and command stubs
- the common host-project workflow should be represented as a high-level runtime helper that mounts a read-only host tree under an in-memory overlay and starts the session in that mounted directory
- the `@ewhauser/gbash-wasm` bridge should expose the same seeded-memory model for `files`, including lazy per-path providers, rather than eagerly writing every file after session creation

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
