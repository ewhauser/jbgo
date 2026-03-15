# gbash

A deterministic, sandbox-only, bash-like runtime for AI agents, implemented in Go.

Shell parsing is delegated to [`mvdan/sh`](https://github.com/mvdan/sh), with project-owned virtual filesystem, registry-backed command execution, policy enforcement, and structured tracing layers around it. Commands never fall through to host binaries, and network access is off by default. Originally inspired by [Vercel's `just-bash`](https://github.com/vercel-labs/just-bash).

> [!WARNING]
> This is beta software. It is likely that additional security hardening is needed. Use with care.

## Table of Contents

- [Features](#features)
- [Public Packages](#public-packages)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Usage](#usage)
  - [Go API](#go-api)
  - [Persistent Sessions](#persistent-sessions)
  - [CLI](#cli)
- [Configuration](#configuration)
  - [Filesystem](#filesystem)
  - [Network Access](#network-access)
- [Security Model](#security-model)
- [Supported Commands](#supported-commands)
  - [Contrib Commands](#contrib-commands)
- [Shell Features](#shell-features)
- [Default Sandbox Layout](#default-sandbox-layout)
- [Examples](#examples)
- [Development](#development)
- [License](#license)

## Features

- Virtual in-memory filesystem — no host access by default
- Registry-backed command execution — unknown commands never run host binaries
- 90+ built-in commands with GNU coreutils flag parity ([compatibility matrix](https://ewhauser.github.io/gbash/compat/latest/))
- Optional allowlisted network access via `curl`
- Persistent sessions with shared filesystem state across executions
- Host directory mounting with read-only overlay for real project workspaces
- Execution budgets — command count, loop iterations, glob expansion, stdout/stderr limits
- Opt-in structured trace events and lifecycle logs for debugging and agent orchestration
- WebAssembly support — runs in the browser ([demo](./website/))

## Public Packages

- `github.com/ewhauser/gbash`: the core Go runtime and embedding API
- `github.com/ewhauser/gbash/contrib/...`: optional Go command modules
- `@ewhauser/gbash-wasm/browser`: the explicit browser entrypoint for the `js/wasm` package. It is versioned in-repo today; npm publishing remains disabled in the release workflow for now.
- `@ewhauser/gbash-wasm/node`: the explicit Node entrypoint for the same `js/wasm` package.

## Installation

Library:

```bash
go get github.com/ewhauser/gbash
```

CLI:

```bash
go install github.com/ewhauser/gbash/cmd/gbash@latest
```

Extras CLI:

```bash
go install github.com/ewhauser/gbash/contrib/extras/cmd/gbash-extras@latest
```

Prebuilt `gbash` and `gbash-extras` archives are also available on the [GitHub Releases page](https://github.com/ewhauser/gbash/releases).
Released Go modules are also requested from the public Go proxy during the release workflow so their API docs stay current on [pkg.go.dev](https://pkg.go.dev/github.com/ewhauser/gbash).

## Quick Start

Try it with `go run` — no install required:

```bash
go run github.com/ewhauser/gbash/cmd/gbash@latest -c 'echo hello; pwd; ls /tmp'
```

```text
hello
/home/agent
```

Everything runs inside a virtual filesystem — nothing touches your host.

## Usage

### Go API

Use `gbash.New` to configure a runtime and `Runtime.Run` for one-shot execution.

```go
package main

import (
	"context"
	"fmt"

	"github.com/ewhauser/gbash"
)

func main() {
	gb, err := gbash.New()
	if err != nil {
		panic(err)
	}

	result, err := gb.Run(context.Background(), &gbash.ExecutionRequest{
		Script: "echo hello\npwd\n",
	})
	if err != nil {
		panic(err)
	}

	fmt.Printf("exit=%d\n", result.ExitCode)
	fmt.Print(result.Stdout)
}
```

```text
exit=0
hello
/home/agent
```

### Persistent Sessions

Use `Session.Exec` when you want multiple shell executions to share one sandbox filesystem.

```go
package main

import (
	"context"
	"fmt"

	"github.com/ewhauser/gbash"
)

func main() {
	ctx := context.Background()

	gb, err := gbash.New()
	if err != nil {
		panic(err)
	}

	session, err := gb.NewSession(ctx)
	if err != nil {
		panic(err)
	}

	if _, err := session.Exec(ctx, &gbash.ExecutionRequest{
		Script: "echo hello > /shared.txt\n",
	}); err != nil {
		panic(err)
	}

	result, err := session.Exec(ctx, &gbash.ExecutionRequest{
		Script: "cat /shared.txt\npwd\n",
	})
	if err != nil {
		panic(err)
	}

	fmt.Print(result.Stdout)
}
```

```text
hello
/home/agent
```

### CLI

Pipe a script to the CLI to execute it inside the sandbox:

```bash
printf 'echo hi\npwd\n' | gbash
```

```text
hi
/home/agent
```

When stdin is a terminal, the CLI starts an interactive shell automatically:

```bash
gbash
```

You can also force interactive mode explicitly:

```bash
printf 'pwd\ncd /tmp\npwd\nexit\n' | gbash -i
```

The interactive shell reuses one sandbox session and carries forward filesystem and environment state. It exposes a session-local `history` command, but it does not provide readline-style line editing or job control.

For host-backed CLI runs, you can switch the filesystem mode explicitly:

```bash
gbash --root /path/to/project --cwd /home/agent/project -c 'pwd; ls'
```

`--root` mounts a host directory read-only at `/home/agent/project` under an in-memory writable overlay. `--cwd` sets the initial sandbox working directory.

Install `gbash-extras` when you want the same CLI surface with the stable official contrib commands (`awk`, `jq`, `sqlite3`, and `yq`) pre-registered:

```bash
gbash-extras -c 'jq -r .name data.json'
```

The shared frontend is also exposed as the public `github.com/ewhauser/gbash/cli` package. Call `cli.Run` with a `cli.Config` to reuse the stock flag parsing, interactive mode, and runtime setup from your own wrapper binary.
## Configuration

### Filesystem

Most callers should use one of these entry points:

- `gbash.New()` — default mutable in-memory sandbox
- `gbash.WithWorkspace(root)` — real host directory mounted read-only under an in-memory overlay
- `gbash.WithFileSystem(gbash.ReadWriteDirectoryFileSystem(...))` — just-bash-style mutable host-backed root
- `gbash.WithFileSystem(gbash.CustomFileSystem(...))` — seeded or custom backends

Mount a host directory as a read-only workspace overlay:

```go
gb, err := gbash.New(
	gbash.WithWorkspace("/path/to/project"),
)
```

This mounts the host directory read-only at `/home/agent/project`, starts the session there, and keeps all writes in the in-memory upper layer.

For full control over the mount point or host-file read cap:

```go
gb, err := gbash.New(
	gbash.WithFileSystem(gbash.HostDirectoryFileSystem("/path/to/project", gbash.HostDirectoryOptions{
		MountPoint: "/home/agent/project",
	})),
)
```

If you want writes to persist directly back to the host directory instead of landing in the in-memory overlay, use the read/write helper:

```go
gb, err := gbash.New(
	gbash.WithFileSystem(gbash.ReadWriteDirectoryFileSystem("/path/to/project", gbash.ReadWriteDirectoryOptions{})),
	gbash.WithWorkingDir("/"),
)
```

This mode maps the host directory to sandbox `/`, so it is best suited to compatibility harnesses and other opt-in developer workflows.


### Network Access

Network access is disabled by default. Enable it to register `curl` in the sandbox.

For simple URL allowlisting, use `WithHTTPAccess`:

```go
package main

import (
	"context"
	"fmt"

	"github.com/ewhauser/gbash"
)

func main() {
	gb, err := gbash.New(
		gbash.WithHTTPAccess("https://api.example.com/v1/"),
	)
	if err != nil {
		panic(err)
	}

	result, err := gb.Run(context.Background(), &gbash.ExecutionRequest{
		Script: "curl -o /tmp/status.json https://api.example.com/v1/status\ncat /tmp/status.json\n",
	})
	if err != nil {
		panic(err)
	}

	fmt.Print(result.Stdout)
}
```

For fine-grained control over methods, response limits, and private-range blocking, use `WithNetwork`:

```go
gb, err := gbash.New(
	gbash.WithNetwork(&gbash.NetworkConfig{
		AllowedURLPrefixes: []string{"https://api.example.com/v1/"},
		AllowedMethods:     []gbash.Method{gbash.MethodGet, gbash.MethodHead},
		MaxResponseBytes:   10 << 20,
		DenyPrivateRanges:  true,
	}),
)
```

For full transport control in tests or embedding, inject your own `Config.NetworkClient`.

### Observability

Tracing and logging are disabled by default.

Use `WithTracing(TraceConfig{Mode: gbash.TraceRedacted})` to populate `ExecutionResult.Events` for non-interactive runs and to receive structured `OnEvent` callbacks for both non-interactive and interactive executions. `TraceRedacted` is the recommended mode for agent workloads. `TraceRaw` preserves full argv and path metadata and should only be used when you control the sink and retention policy.

Use `WithLogger` to receive top-level lifecycle logs: `exec.start`, `stdout`, `stderr`, `exec.finish`, and `exec.error`. Logger callbacks receive the same captured stdout and stderr strings returned in `ExecutionResult`.

## Security Model

- The shell only sees the filesystem and runtime configuration you provide.
- Command execution is registry-backed. Unknown commands never execute host binaries.
- Network access is off by default. When enabled, requests are constrained by allowlists and runtime limits.
- The default static policy applies execution budgets such as command-count, loop-iteration, glob-expansion, substitution-depth, and stdout/stderr capture limits.
- Structured trace events are opt-in. Redacted tracing is the recommended default, and raw tracing is unsafe unless you tightly control where events go.

This is not a hardened sandbox. If you need stronger containment against denial-of-service or runtime bugs, use OS- or process-level isolation around it. For a detailed threat analysis, see [THREAT_MODEL.md](./THREAT_MODEL.md).

## Supported Commands

The default registry includes commands for file ops, text processing, archival, and execution. Use `gbash.DefaultRegistry()` to start from the stock builtin set and register custom commands on top.

| Category | Commands |
|---|---|
| File and path | `basename` `cat` `chmod` `chown` `cp` `dircolors` `dirname` `du` `file` `find` `ln` `link` `ls` `dir` `mkdir` `mktemp` `mv` `readlink` `realpath` `rm` `rmdir` `stat` `touch` `tree` `truncate` `unlink` `vdir` |
| Search and text | `base32` `base64` `basenc` `column` `comm` `csplit` `cut` `diff` `egrep` `fgrep` `grep` `head` `join` `nl` `numfmt` `od` `paste` `printf` `rev` `rg` `sed` `seq` `sort` `split` `strings` `tac` `tail` `tee` `tr` `uniq` `wc` `xan` |
| Archive | `gzip` `gunzip` `tar` `zcat` |
| Environment and execution | `arch` `b2sum` `bash` `cksum` `clear` `date` `echo` `env` `expr` `factor` `false` `help` `history` `id` `md5sum` `printenv` `pwd` `sh` `sha1sum` `sha224sum` `sha256sum` `sha384sum` `sha512sum` `sleep` `sum` `test` `timeout` `true` `tsort` `tty` `uname` `uptime` `which` `who` `whoami` `xargs` `yes` |
| Network (when configured) | `curl` |
| Extras* | `awk` `jq` `sqlite3` `yq` |

\* Use `gbash-extras` for extras commands.

Many commands are ported from [uutils/coreutils](https://github.com/uutils/coreutils) and have full GNU flag parity. See the current [compatibility matrix](https://ewhauser.github.io/gbash/compat/latest/).

### Contrib Commands

Optional commands live in [`contrib/`](./contrib/) as separate Go modules so the core library stays dependency-light. They are not registered by default.

| Command | Module | Backed by |
|---|---|---|
| [`awk`](./contrib/awk/) | `github.com/ewhauser/gbash/contrib/awk` | [`benhoyt/goawk`](https://github.com/benhoyt/goawk) |
| [`jq`](./contrib/jq/) | `github.com/ewhauser/gbash/contrib/jq` | [`itchyny/gojq`](https://github.com/itchyny/gojq) |
| [`sqlite3`](./contrib/sqlite3/) | `github.com/ewhauser/gbash/contrib/sqlite3` | [`ncruces/go-sqlite3`](https://github.com/ncruces/go-sqlite3) |
| [`yq`](./contrib/yq/) | `github.com/ewhauser/gbash/contrib/yq` | [`mikefarah/yq`](https://github.com/mikefarah/yq) |

Use `github.com/ewhauser/gbash/contrib/extras` to register all contrib commands at once:

```go
import "github.com/ewhauser/gbash/contrib/extras"

gb, err := gbash.New(gbash.WithRegistry(extras.FullRegistry()))
```

The same stable set is bundled in the `gbash-extras` CLI at `github.com/ewhauser/gbash/contrib/extras/cmd/gbash-extras`.

See the [`custom-zstd`](./examples/custom-zstd/) example for how to register custom commands.

## Shell Features

Shell parsing and execution are delegated to `mvdan/sh/v3`, with project-owned filesystem, command, policy, and observability layers around it.

The runtime supports a practical shell subset for agent workflows, including:

- pipelines and redirections
- variable expansion and command substitution
- conditionals and loops
- shell functions and common builtins handled by `mvdan/sh`
- virtual `cd` and `pwd` behavior against the sandbox filesystem
- nested `bash` and `sh` execution inside the same sandbox session

It is intentionally not a full Bash reimplementation. It does not aim to provide full GNU Bash compatibility, readline-style UX, history navigation/editing, job control, or host TTY emulation.

## Default Sandbox Layout

Each fresh session starts with a Unix-like virtual layout:

- home and default working directory: `/home/agent`
- scratch directory: `/tmp`
- command directories: `/usr/bin` and `/bin`
- default `PATH`: `/usr/bin:/bin`

Those command paths are virtual stubs used for shell resolution. Command implementations still come from the Go registry, not the host filesystem.

## Examples

| Example | Description |
|---|---|
| [`adk-bash-chat`](./examples/adk-bash-chat/) | Local CLI chatbot using [`adk-go`](https://github.com/google/adk-go) with a persistent `gbash` bash tool session and a seeded ops analytics lab |
| [`custom-zstd`](./examples/custom-zstd/) | Demonstrates custom command registration by adding a `zstd` compression/decompression command |
| [`openai-tool-call`](./examples/openai-tool-call/) | Uses the OpenAI Go SDK Responses API with `gbash` as a `bash` function tool |
| [`sqlite-backed-fs`](./examples/sqlite-backed-fs/) | Custom `gbfs.FileSystem` backed by a SQLite database for persistent sandbox filesystem state |
| [`transactional-workspaces`](./examples/transactional-workspaces/) | Narrated snapshot/rollback/branch demo showing how `gbash` sessions become reversible, inspectable shell workspaces |

## Development

`make build`, `make test`, and `make lint` cover the Go modules. See [CONTRIBUTING.md](./CONTRIBUTING.md) for repository structure, module versioning, release process, benchmarks, and GNU coreutils compatibility testing.

## License

This project is licensed under the [Apache License 2.0](./LICENSE).
