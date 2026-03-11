# just-bash-go

Status: Draft v0.1
Last updated: 2026-03-11

## 1. Purpose

`just-bash-go` is a deterministic, policy-controlled, sandbox-only, bash-like runtime for AI agents.

It preserves the product idea behind Vercel's `just-bash` while making different implementation choices:

- shell parsing and evaluation are delegated to `mvdan/sh/v3`
- filesystem access is virtualized by default
- commands are implemented in Go and resolved through an explicit registry
- unknown commands never fall through to the host OS
- sandbox policy is part of the core runtime, not an optional mode

The target is not "Bash in Go". The target is a practical shell-shaped runtime for agentic coding and data workflows that must be deterministic, inspectable, and safe by default.

## 2. Product Definition

`just-bash-go` is a shell runtime with the following product contract:

- it accepts shell-like scripts and command snippets
- it evaluates a pragmatic subset of shell semantics
- it runs entirely inside a sandboxed runtime
- it exposes structured traces for agent debugging and orchestration
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

1. Port the `just-bash` concept to a Go-native runtime named `just-bash-go`.
2. Use `mvdan/sh/v3` for parsing, ASTs, expansion semantics, control flow, and interpreter behavior where feasible.
3. Support only sandbox mode.
4. Use explicit Go command implementations instead of host subprocesses.
5. Default to an in-memory or otherwise virtualized filesystem.
6. Expose deterministic traces and execution results suitable for agent frameworks.
7. Keep the implementation small, explicit, and easy to reason about.

## 4. Non-Goals

`just-bash-go` will not:

- implement full GNU Bash behavior
- provide job control, shell history, readline-style editing, or host TTY emulation
- support host subprocess passthrough
- support a compatibility mode
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

Every major subsystem should have a narrow interface. Callers should be able to replace the filesystem backend, registry, or trace sink without understanding `mvdan/sh` internals.

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
   - trace recorder
   - bounded stdout/stderr capture
3. Configure `interp.Runner` with project handlers for:
   - file open
   - stat
   - readdir
   - simple-call interception
   - command execution
4. Run the parsed program.
5. Normalize shell/interpreter errors into an `ExecutionResult`.
6. Return stdout, stderr, exit code, and structured trace events.

The CLI also provides a minimal interactive shell mode. That mode is a front-end over the same runtime, not a second execution engine:

- it keeps one `Session` alive for the duration of the interactive shell
- it uses `syntax.Parser.InteractiveSeq` to gather complete interactive statements and continuation prompts
- it executes each completed entry via `Session.Exec`
- it carries forward the virtual cwd and shell-visible variable state between entries at the CLI layer

### 6.1 Session model

`just-bash-go` should expose a long-lived session abstraction.

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

Commands remain registry-backed Go implementations. `/bin/ls` and similar paths are virtual command identities, not host executables.

Because `mvdan/sh` currently validates `interp.Dir(...)` against the host filesystem, the runtime treats `PWD` as the authoritative virtual working directory and injects a small shell prelude that preserves virtual `cd` and `pwd` behavior without host directory access.

## 7. Proposed Package Layout

```text
cmd/jbgo/             CLI entrypoint for local execution and demos
runtime/              top-level runtime API and execution orchestration
shell/                mvdan/sh integration and handler wiring
fs/                   project-owned filesystem interfaces and virtual backends
network/              sandboxed HTTP client, allowlist matching, redirect checks
commands/             command registry, invocation context, core Go commands
policy/               sandbox policy types and enforcement decisions
trace/                structured event model and recorder implementations
tests/                integration fixtures and compatibility-style harnesses
```

Package responsibilities:

- `runtime/`: public API, runtime/session creation, run configuration, result collection, output capture
- `shell/`: parser and runner adapter; no product policy lives here
- `fs/`: POSIX-like path normalization, memory filesystem, future overlay/snapshot backends
- `network/`: runtime-owned HTTP sandbox with URL-prefix allowlists, method controls, redirect revalidation, and response-size limits
- `commands/`: registry and Go-native command implementations such as `echo`, `cat`, `ls`, and `pwd`
- `policy/`: allowlists, root restrictions, size limits, network stance, and decision helpers
- `trace/`: event schema, recorder interfaces, and in-memory buffering
- `tests/`: black-box runtime tests and corpus-driven shell fixtures

We intentionally do not create a `compat/` package because compatibility mode is not a product feature.

## 8. Core Interfaces

The initial API should stay small and stable.

```go
type Runtime struct {
    cfg Config
}

type Config struct {
    FSFactory     fs.Factory
    Registry      commands.CommandRegistry
    Policy        Policy
    Engine        shell.Engine
    BaseEnv       map[string]string
    DefaultDir    string
    Network       *network.Config
    NetworkClient network.Client
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

type Invocation struct {
    Args   []string
    Env    map[string]string
    Dir    string
    Stdin  io.Reader
    Stdout io.Writer
    Stderr io.Writer
    FS     FileSystem
    Net    network.Client
    Policy Policy
    Trace  trace.Recorder
    Exec   func(context.Context, *ExecutionRequest) (*ExecutionResult, error)
}

type CommandRegistry interface {
    Register(cmd Command) error
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

Key design decisions:

- `Runtime` is a concrete type. Callers should not need to mock it.
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
- the shim shadows `cd` and `pwd` so shell-visible cwd behavior stays virtual
- all project path handlers resolve relative paths from virtual `PWD`, not from `HandlerContext.Dir`

### 9.3 Stdio

`StdIO` is wired to bounded buffers owned by `runtime/`. This gives us:

- deterministic capture for agent frameworks
- policy-controlled output limits
- no direct dependency on host terminal behavior

### 9.4 File handlers

`OpenHandler`, `StatHandler`, and `ReadDirHandler2` bridge `mvdan/sh` into the project filesystem.

Responsibilities:

- resolve shell-relative paths against virtual `PWD`
- normalize paths using POSIX semantics
- enforce policy before touching the backend
- emit file access trace events
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
6. emit start/finish trace events

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
- host-backed filesystems, if ever added, must still satisfy policy checks and must never imply host command execution
- developer-only test harnesses may use a host-backed filesystem adapter, but that adapter is not the default runtime backend and does not change the no-host-command-execution rule
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
- `OverlayFS`: copy-on-write backend with a read-only lower layer, writable in-memory upper layer, merged `readdir`, and tombstones for deletions
- `SnapshotFS`: deterministic read-only clone of another filesystem for tests and replay fixtures

Backend boundary for the current implementation:

- `OverlayFS` is intended for runtime/session use and is exposed through `OverlayFactory`
- `SnapshotFS` is a read-only backend for deterministic fixtures and direct tests
- `SnapshotFS` is not the default `runtime` session backend because session bootstrap still creates the sandbox layout and command stubs

## 11. Command Execution Model

There are two categories of executable behavior:

1. shell builtins and shell control-flow behavior from `mvdan/sh`
2. registered Go commands owned by `just-bash-go`

Rules:

- builtins remain interpreter-owned unless we intentionally shadow them to preserve sandbox semantics
- external-style utilities are registry-owned
- unregistered commands fail
- there is no subprocess escape hatch
- command resolution should work by bare name and by virtual path such as `/bin/ls`
- command implementations may compose other shell commands only through the session's own execution callback

For MVP, `cd` and `pwd` are provided via a runtime-owned shell shim so they operate on the virtual cwd instead of `mvdan/sh`'s host-backed directory state.

Initial MVP command set:

- `echo`
- `cat`
- `cp`
- `mv`
- `ls`
- `touch`
- `rmdir`
- `ln`
- `chmod`
- `readlink`
- `stat`
- `basename`
- `dirname`
- `tree`
- `du`
- `file`
- `find`
- `grep`
- `rg`
- `awk`
- `head`
- `tail`
- `wc`
- `sort`
- `uniq`
- `cut`
- `sed`
- `printf`
- `tee`
- `env`
- `printenv`
- `true`
- `false`
- `which`
- `help`
- `date`
- `sleep`
- `timeout`
- `xargs`
- `bash`
- `sh`
- `comm`
- `paste`
- `tr`
- `rev`
- `nl`
- `join`
- `split`
- `tac`
- `diff`
- `base64`
- `tar`
- `gzip`
- `gunzip`
- `zcat`
- `jq`
- `yq`
- `sqlite3`
- `curl` when network access is configured
- `mkdir`
- `rm`
- `pwd`

For `jq`, the runtime should support a practical CLI-compatible subset for agent workflows, including raw-input mode, file-backed filters, variable injection flags, positional argument injection, and basic output-formatting flags. Module loading and stream-mode parity can follow later.

For `yq`, the runtime should wrap `mikefarah/yq`'s `yqlib` evaluator rather than embedding the upstream Cobra CLI. The supported subset should cover agent-oriented `eval` / `eval-all` flows, input and output format selection, null-input document creation, pretty-print rewriting, exit-status handling, scalar-unwrapping controls, NUL-separated output, expression files, and in-place file updates. All input and output must continue to route through the sandbox filesystem, and `yqlib` file/env operators such as `load()` and `env()` must stay disabled so expressions cannot bypass policy.

For `sqlite3`, the runtime should wrap `ncruces/go-sqlite3` directly rather than embedding the upstream CLI. The initial implementation should open an in-memory SQLite connection, deserialize database bytes from the sandbox filesystem when a file path is requested, execute SQL inside that in-memory connection, and serialize the database back to the sandbox filesystem only after successful writes. The supported subset should cover `:memory:` and file-backed databases, list / CSV / JSON / line / column / table output, `-header`, `-readonly`, `-bail`, `-cmd`, `-echo`, help, and version output. `ATTACH`, `DETACH`, `VACUUM`, virtual-table creation, and `load_extension()` must stay disabled so SQL cannot escape the sandbox filesystem or reach host file APIs.

For archive and compression helpers, the runtime should expose explicit subsets rather than imply GNU `tar` or `gzip` parity. `gzip`, `gunzip`, and `zcat` should support file and stdin/stdout flows, `-c`, `-d`, `-f`, `-k`, `-S`, `-t`, and `-v`, with binary-safe streaming and no host-tempfile fallback. `tar` should support create, list, and extract flows with `-c`, `-x`, `-t`, `-f`, `-C`, `-z`, `-v`, `-O`, and `-k`, while rejecting unsupported codecs and append/update modes. Extraction must strip leading slashes, reject parent-traversal entries, and reject symlink targets that escape the extraction root.

For file/path commands, the runtime now supports a practical agent-oriented subset rather than full GNU parity:

- `touch` supports creation, `-c`, and `-d/--date`
- `cat` supports stdin or file concatenation plus `-n/--number` line numbering
- `ln` supports hard links plus `-s` and `-f`
- `chmod` supports octal and symbolic modes plus recursive `-R`
- `readlink` supports raw link-target output and `-f` canonicalization
- `stat` supports default output and `-c` formatting for common fields such as name, size, type, and mode
- `basename` and `dirname` support multi-operand path trimming, trailing-suffix removal, and Unix-style slash normalization
- `tree` supports `-a`, `-d`, `-L`, and `-f`
- `du` supports `-a`, `-s`, `-h`, `-c`, and `--max-depth`
- `file` supports `-b/--brief`, `-i/--mime`, basic magic-byte detection, shebang detection, and extension-based text detection

For `sed`, the runtime should continue to expose an explicitly documented subset rather than imply GNU `sed` parity. The supported subset is:

- `-n`, `-e`, and simple `-i` in-place editing
- numeric addresses, `$`, regex addresses, and `addr1,addr2` ranges
- commands `s`, `d`, `p`, and `q`
- substitution flags `g` and `i`
- alternate substitution delimiters such as `s#/old#/new#`

The unsupported `sed` surface remains broad by design: no hold-space commands, multiline pattern-space commands, branching, grouping, file side-effect commands, or shell-evaluating `e`.

For the text/search batch, the runtime should expose useful, explicitly documented subsets instead of implying parity with GNU coreutils, ripgrep, or awk implementations:

- `printf` supports the core shell format verbs used by automation scripts, including `%b` escape decoding and `\c` early termination
- `rg` supports recursive regex search with `-n`, `-i`, `-l`, `-c`, `-g`, `--hidden`, and `--files`
- `awk` is backed by `goawk` and supports `-F`, `-v`, and `-f`, but keeps `system()`, shell pipes, file writes, and extra file reads disabled inside the sandbox
- `cut` supports character and field selection, custom delimiters, suppressing non-delimited lines, and `--only-delimited`
- `comm` supports two-input comparisons plus column suppression via `-1`, `-2`, and `-3`
- `uniq` supports adjacent-run filtering plus counts, duplicate-only / unique-only modes, and `-i/--ignore-case`
- `paste` supports parallel and serial modes via `-s` and `-d`, including repeated `-` stdin inputs
- `tr` supports translate, delete, squeeze, complement, ranges, escapes, and a focused set of POSIX character classes
- `rev` and `tac` support Unicode-safe line reversal and reverse-line streaming
- `nl` supports body numbering styles plus width, separator, start, and increment controls
- `join` supports keyed joins via `-1`, `-2`, `-t`, `-a`, `-v`, `-e`, `-o`, and `-i`
- `split` supports line-based and byte-based splits via `-l`, `-b`, `-d`, and `-a`
- `diff` supports unified output plus `-q/--brief`, `-s/--report-identical-files`, and `-i/--ignore-case`, and accepts `-u/--unified` as an explicit alias for the default unified format
- `base64` supports encode/decode, `-w/--wrap` line wrapping control, and whitespace-tolerant decoding

For the shell/process helper batch, the runtime should expose practical, sandbox-owned subsets:

- `tee` supports stdout passthrough, writing one or more files, and `-a` append mode
- `env` supports `-i/--ignore-environment`, `-u NAME`, inline `NAME=value` assignments, and nested command execution with scoped environment replacement
- `printenv` prints either the whole environment or named variables and exits non-zero when a requested variable is missing
- `true` and `false` exist as explicit virtual commands, while bare shell builtins remain interpreter-owned unless intentionally shadowed
- `which` supports `-a` and `-s` over the virtual `PATH`
- `help` exposes runtime-owned help topics for the supported shell builtin surface
- `date` is intentionally UTC-only and supports `-u/--utc`, `-d/--date`, `-I/--iso-8601`, `-R/--rfc-email`, and `+FORMAT`
- `sleep` supports decimal durations and `s`, `m`, `h`, and `d` suffixes with a bounded maximum delay
- `timeout` supports duration-bounded nested command execution and accepts `--foreground`, `-k/--kill-after`, and `-s/--signal` as compatibility flags without host signal semantics
- `xargs` supports the default `echo` behavior plus `-n`, `-I`, `-0`, `-d`, `-t`, and `-r`
- `bash` and `sh` are nested shell wrappers for `-c`, script files, and stdin scripts; they do not escape to host shells

For network access, the runtime now exposes a safe `curl` subset instead of ambient host networking. That subset is enabled only when `runtime.Config.Network` or a prebuilt `NetworkClient` is provided. The sandboxed network layer must:

- keep network fully disabled by default
- validate request URLs against exact origin plus path-prefix allowlists
- validate HTTP methods against an explicit allowlist, defaulting to `GET` and `HEAD`
- revalidate every redirect target before following it
- cap response body size
- optionally block localhost, private IP literals, and hostnames that resolve to private ranges

The initial `curl` subset is intentionally small and agent-oriented:

- `-L`, `-I`, `-i`, `-X`, `-H`, `-d`, `-o`, `-f`, `-s`, `-S`
- no cookie jar, auth helpers, multipart forms, or full curl write-out compatibility yet

Second-wave commands:

- JSON-aware utilities beyond `jq`
- richer curl compatibility and higher-level fetch helpers

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

Tracing is a first-class output.

Each execution should emit structured events such as:

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

Implementation detail for the current runtime:

- the schema is project-owned and versioned as `just-bash-go.trace.v1`
- the core runtime does not adopt OpenTelemetry as its event schema or transport contract
- every event carries `session_id` and `execution_id`
- command events carry `resolved_name`, `resolved_path`, and `resolution_source`
- path-policy and command-policy failures emit explicit `policy.denied` events
- file mutations emit `file.mutation` events alongside lower-level file access events when useful
- the trace schema should grow by additive fields and new event kinds rather than by overloading free-form messages

## 14. Error Handling

Errors fall into four categories:

1. parse errors
2. policy denials
3. command-level execution failures
4. runtime/internal errors

Behavior rules:

- expected shell failures return shell-style exit statuses
- unknown commands return `127`
- denied commands return `126`, including command allowlist denials, path-policy denials during command resolution, and redirect/open/readdir/stat denials
- request timeouts return `124`
- caller cancellation returns `130`
- file operation failures use path-specific errors when possible
- unexpected runtime faults should still return a structured `ExecutionResult`

We should keep interpreter errors and internal Go errors distinguishable in traces.

## 15. MVP Scope

MVP includes:

- parse and run shell snippets with `mvdan/sh/v3`
- persistent sessions with per-session filesystem state
- in-memory sandbox filesystem
- Unix-like default layout (`/home/agent`, `/tmp`, `/bin`, `/usr/bin`, `PATH`)
- virtual `PWD` with runtime-provided `cd` and `pwd` shims
- explicit Go command registry
- at least six core commands
- command resolution by name and virtual path
- stdout/stderr capture
- policy enforcement for commands and file paths
- execution limits for runaway scripts
- structured in-memory tracing
- integration tests for common agent workflows
- a minimal interactive CLI shell built on top of the session runtime

MVP excludes:

- host subprocess execution
- compatibility mode
- networking
- advanced Bash edge-case parity
- history, line-editing, and job-control shell ergonomics

## 16. Implementation Plan

### Phase 1: Runtime, session, and parser integration

- create `runtime`, `shell`, `trace`, and `policy` packages
- parse scripts with `syntax.NewParser`
- construct an `interp.Runner`
- introduce `Session` as the persistent unit of execution
- normalize exit statuses into `ExecutionResult`

### Phase 2: Virtual filesystem, default layout, and shell handlers

- implement `fs.MemoryFS`
- create `/home/agent`, `/tmp`, `/bin`, and `/usr/bin` in the default session layout
- wire `OpenHandler`, `StatHandler`, and `ReadDirHandler2`
- normalize paths with POSIX rules
- add directory and file operation tests

### Phase 3: Command registry, path resolution, and core commands

- implement `Command` and `Registry`
- dispatch through `ExecHandlers` middleware
- resolve commands from `PATH` and virtual command paths
- add `echo`, `pwd`, `cat`, `cp`, `mv`, `ls`, `find`, `grep`, `head`, `tail`, `wc`, `mkdir`, and `rm`

### Phase 4: Policy enforcement and execution budgets

- deny unknown commands cleanly
- enforce read/write roots
- enforce output and file size limits
- add loop, command-count, and substitution limits, starting with default `10000`-command and `10000`-iteration budgets
- add explicit denial tests

### Phase 5: Invocation model and higher-order commands

- allow commands to invoke sub-executions through runtime-owned callbacks
- broaden command flag coverage and higher-order patterns without leaving the sandbox
- add regression tests for compound command behavior

### Phase 6: Tracing and observability

- emit command and file events
- include session/execution IDs and policy denials
- attach timing and exit metadata
- expose a recorder API for embedders

### Phase 7: Test harness and workflow corpus

- add focused shell-subset regressions for redirects, pipelines, substitutions, loops, and conditionals
- add committed fixture files under `runtime/testdata/` for compatibility-style cases
- add scenario-first multi-step workflow tests for persistent sessions
- add golden tests for stdout/stderr/exit code/events
- add determinism tests that compare normalized repeated executions
- add Go built-in fuzz targets for general scripts, malformed inputs, and multi-exec session sequences

## 17. Testing Strategy

We should test at four layers, following the same broad split that makes Vercel's `just-bash` test suite maintainable:

1. focused regressions for shell/runtime semantics
2. fixture-backed compatibility corpus
3. scenario-first workflow tests
4. determinism and stability probes

### 17.1 Unit tests and focused regressions

- path normalization
- memory filesystem operations
- registry lookup
- policy allow/deny logic
- individual command behavior
- session lifecycle behavior
- pipelines
- redirects
- command substitution
- loops and conditionals
- unknown command failures
- policy denials
- command-substitution depth regressions
- glob-operation budget regressions
- execution-budget regressions for sequential commands, subshells, pipelines, and reset-between-exec behavior
- loop-budget regressions for `for`, `while`, `until`, nested loops, and C-style `for`
- timeout and cancellation regressions, including nested sub-execution behavior
- symlink regressions for `lstat`/`readlink`/`realpath`, default-deny traversal, and resolved-target root checks
- golden traces for stdout, stderr, exit code, and normalized event shapes

### 17.2 Fixture-backed compatibility corpus

- keep a curated committed corpus under `runtime/testdata/compatibility/`
- drive cases from JSON fixtures instead of ad hoc inline scripts
- cover the supported shell subset and the current Go-native command surface
- prefer practical agent behaviors over exhaustive Bash conformance

### 17.3 Scenario-first workflow tests

- model realistic multi-step agent sessions instead of only isolated commands
- keep scenario names task-oriented, such as codebase exploration or refactor preparation
- assert that filesystem artifacts persist across `Session.Exec` calls while shell-local state stays per-execution

### 17.4 Determinism and stability tests

- repeat the same script twice in fresh sessions and compare:
  - exit code
  - stdout
  - stderr
  - normalized trace events
- repeat multi-step workflows across fresh sessions and compare the normalized results for each step

### 17.5 Built-in fuzzing

Use Go's built-in `testing` fuzzing framework as the base fuzz harness.

The initial target set should live in `runtime/` and cover:

- single-script execution against a runtime with tight policy and timeout limits
- malformed and byte-injected inputs that should fail gracefully without internal panics or host-path leaks
- multi-exec session sequences to exercise persistent filesystem state under fuzzed scripts
- command-specific targets for the current file/path, text/search, shell/process-helper, structured-data, and archive/compression command batches
- metadata-driven generated programs that compose command/flag variants into pipelines and broader shell-shape coverage
- committed known-attack corpora whose seeds are also mutated under the fuzz harness

The fuzz harness should:

- seed each target with a small curated set of valid and malformed scripts
- keep request-level timeouts and policy limits intentionally tight
- treat parser errors as acceptable outcomes
- fail on unexpected internal errors, host-path leakage in error paths, or unbounded execution
- use generator-driven inputs for shell syntax, pipelines, and command-flag combinations as command surface expands
- add security-focused fuzz oracles for sandbox escape, information disclosure, and denial-of-service outcomes
- keep a known-attack fuzz corpus and promote interesting discoveries into permanent regression tests
- record lightweight feature and command-flag coverage so fuzzing can show which surfaces are actually exercised
- keep project-owned per-command fuzz metadata so registered commands and supported flag variants stay exercised as the command set grows

As command surface grows, add command-specific fuzz targets and richer seed corpora without replacing the focused regression suite.
- canonicalize trace event ordering for concurrent pipeline stages before comparison, while still keeping exact event-order goldens for simple serial scripts

The compatibility harness should stay curated. It is not a Bash conformance suite, and it should not imply future host-shell fallback.

## 18. Future Roadmap

The gap analysis against `just-bash` yields two categories: gaps we should close because they improve the agent runtime, and differences we should keep because they preserve the product boundary.

### 18.1 Gaps To Close

- broader command coverage for agent workflows, especially deeper parity for the newer archive/compression, helper, and text/search tools
- stronger execution budgets and policy enforcement, including richer CPU and memory accounting
- host-backed overlay filesystem support so real directories can be mounted read-only underneath an in-memory writable layer
- fuller `jq` and `curl` compatibility for structured data flows and safe networked workflows
- richer tracing and compatibility corpus
- continued fuzzing depth as new commands land, including additional attack-corpus entries, richer metadata variants, and longer-running schedules outside the default CI path
- more polished interactive-shell ergonomics, such as optional line editing and history that do not weaken sandbox determinism

### 18.2 Intentional Divergences

- use `mvdan/sh/v3` instead of a project-owned parser/interpreter
- do not pursue browser-targeted runtime parity
- do not pursue Vercel Sandbox API compatibility as a primary goal
- do not copy JavaScript-specific defense-in-depth mechanisms into the Go design
- do not add unrestricted host read-write filesystem access to the default runtime path

### 18.3 Post-MVP Investments

- `sort`, `uniq`, `cut`, and `sed` subset
- JSON-aware utilities for agent data flows
- richer trace correlation IDs
- safe HTTP fetch with policy allowlists
- resource accounting for CPU and memory budgets
- host-backed overlay lower filesystems for copy-on-write sessions
- broader command-family parity with `just-bash`
- optional line-editing support for the interactive CLI if it can be added without weakening sandbox determinism

We should avoid adding features that weaken the product boundary:

- host command passthrough
- compatibility mode
- hidden escape hatches

## 19. Open Questions

These questions should be decided early but do not block the initial scaffold:

1. Which shell builtins should be explicitly denied even if `mvdan/sh` supports them?
2. Do we want a first-class JSON command set in MVP+1?
3. When we add a host-backed lower filesystem for `OverlayFS`, do we want it limited to read-only roots at first or to support a narrower write-through mode under policy control?
