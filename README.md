# just-bash-go

`just-bash-go` is a deterministic, sandbox-only, bash-like runtime for AI agents, implemented in Go.

It ports the core product idea behind [Vercel's `just-bash`](https://github.com/vercel-labs/just-bash) to a Go-native runtime built on [`mvdan/sh/v3`](https://pkg.go.dev/mvdan.cc/sh/v3).

Key properties:

- no host subprocess fallback
- no compatibility mode
- virtual filesystem by default
- explicit Go command registry
- policy-controlled execution
- structured trace events for agent workflows
- optional allowlisted network access via `curl`

> **Note:** This is beta software. It has not been hardened or security tested and is less mature than the upstream TypeScript implementation. Use it with care.

Requires Go 1.25+.

## Table of Contents

- [Usage](#usage)
  - [Basic API](#basic-api)
  - [Persistent Sessions](#persistent-sessions)
  - [CLI](#cli)
- [Configuration](#configuration)
  - [Filesystem Backends](#filesystem-backends)
  - [Network Access](#network-access)
  - [Registry and Policy](#registry-and-policy)
- [Security Model](#security-model)
- [Supported Commands](#supported-commands)
- [Shell Features](#shell-features)
- [Default Sandbox Layout](#default-sandbox-layout)
- [Development](#development)
- [License](#license)

## Usage

### Basic API

Use `runtime.New` to configure a runtime and `Runtime.Run` for one-shot execution.

```go
package main

import (
	"context"
	"fmt"

	jbruntime "github.com/cadencerpm/just-bash-go/runtime"
)

func main() {
	rt, err := jbruntime.New(&jbruntime.Config{})
	if err != nil {
		panic(err)
	}

	result, err := rt.Run(context.Background(), &jbruntime.ExecutionRequest{
		Script: "echo hello\npwd\n",
	})
	if err != nil {
		panic(err)
	}

	fmt.Printf("exit=%d\n", result.ExitCode)
	fmt.Print(result.Stdout)
}
```

Output:

```text
exit=0
hello
/home/agent
```

`Runtime.Run` creates a fresh sandbox session for that call. If you want shared filesystem state across multiple executions, use `Runtime.NewSession`.

Per-execution controls live on `ExecutionRequest`, including `Env`, `WorkDir`, `Timeout`, `ReplaceEnv`, and `Stdin`. Results include `ExitCode`, `Stdout`, `Stderr`, `FinalEnv`, and trace `Events`.

### Persistent Sessions

Use `Session.Exec` when you want multiple shell executions to share one sandbox filesystem.

```go
package main

import (
	"context"
	"fmt"

	jbruntime "github.com/cadencerpm/just-bash-go/runtime"
)

func main() {
	ctx := context.Background()

	rt, err := jbruntime.New(&jbruntime.Config{})
	if err != nil {
		panic(err)
	}

	session, err := rt.NewSession(ctx)
	if err != nil {
		panic(err)
	}

	if _, err := session.Exec(ctx, &jbruntime.ExecutionRequest{
		Script: "echo hello > /shared.txt\n",
	}); err != nil {
		panic(err)
	}

	result, err := session.Exec(ctx, &jbruntime.ExecutionRequest{
		Script: "cat /shared.txt\npwd\n",
	})
	if err != nil {
		panic(err)
	}

	fmt.Print(result.Stdout)
}
```

Output:

```text
hello
/home/agent
```

Session behavior is intentional:

- filesystem state persists across `Session.Exec` calls
- shell-local variables and working directory do not persist across `Session.Exec` calls
- each execution starts from the session's configured base environment and default workdir unless you override them in the request

That differs from the interactive CLI, which carries shell-visible env and cwd forward between entries.

### CLI

The CLI reads a script from stdin and executes it inside the sandbox runtime.

```bash
printf 'echo hi\npwd\n' | go run ./cmd/just-bash-go
```

Example:

```text
hi
/home/agent
```

Redirects and file reads also stay inside the virtual filesystem:

```bash
printf 'echo hi > /tmp.txt\ncat /tmp.txt\n' | go run ./cmd/just-bash-go
```

When stdin is a terminal, the CLI starts an interactive shell automatically:

```bash
go run ./cmd/just-bash-go
```

You can also force interactive mode explicitly:

```bash
printf 'pwd\ncd /tmp\npwd\nexit\n' | go run ./cmd/just-bash-go -i
```

The interactive shell is intentionally minimal:

- it reuses one sandbox session, so filesystem changes persist
- it carries forward working directory and shell-visible environment state between entries
- it supports multiline input through `mvdan/sh/v3`'s interactive parser
- it does not provide history, line editing, job control, or host TTY emulation

## Configuration

The main configuration surface is `runtime.Config`.

### Filesystem Backends

Filesystem choice is controlled through `Config.FSFactory`.

- `jbfs.MemoryFactory{}` is the default: each session gets a fresh in-memory filesystem.
- `jbfs.OverlayFactory{Lower: ...}` creates a copy-on-write filesystem over another `jbfs.Factory`.
- `jbfs.NewSnapshot(ctx, fsys)` creates a read-only point-in-time snapshot of an existing filesystem. If you want snapshot-backed sessions, wrap it behind your own `jbfs.Factory`.

You can also provide your own `jbfs.Factory` implementation for seeded, host-backed, or otherwise custom sandbox filesystems.

### Network Access

Network access is disabled by default. When you set `Config.Network` or provide `Config.NetworkClient`, the runtime registers `curl` automatically. Otherwise `curl` is not present in the sandbox at all.

```go
import (
	jbnetwork "github.com/cadencerpm/just-bash-go/network"
	jbruntime "github.com/cadencerpm/just-bash-go/runtime"
)

rt, err := jbruntime.New(&jbruntime.Config{
	Network: &jbnetwork.Config{
		AllowedURLPrefixes: []string{"https://api.example.com/v1/"},
		AllowedMethods:     []jbnetwork.Method{jbnetwork.MethodGet, jbnetwork.MethodHead},
		MaxResponseBytes:   10 << 20,
		DenyPrivateRanges:  true,
	},
})
```

The sandboxed network client enforces:

- URL-prefix allowlists
- HTTP-method allowlists
- redirect revalidation
- response-size limits
- optional private-range blocking

The current `curl` subset supports `-L`, `-I`, `-i`, `-X`, `-H`, `-d`, `-o`, `-f`, `-s`, and `-S`.

### Registry and Policy

- `Config.Registry` lets you replace or extend the command set.
- `Config.Policy` controls command allowlists, path allowlists, symlink behavior, and execution limits.
- Unknown commands never fall through to the host OS; they fail with a shell-style command-not-found error instead.

If you are changing runtime boundaries, command ownership, or sandbox behavior, keep [`SPEC.md`](./SPEC.md) in sync.

## Security Model

- The shell only sees the filesystem and runtime configuration you provide.
- Command execution is registry-backed. Unknown commands never execute host binaries.
- There is no host shell fallback and no compatibility mode.
- Network access is off by default. When enabled, requests are constrained by allowlists and runtime limits.
- The default static policy applies execution budgets such as command-count, loop-iteration, glob-expansion, substitution-depth, and stdout/stderr capture limits.
- Trace events capture command execution, file access and mutation, and policy denials for debugging and agent orchestration.

This is not a hardened sandbox. If you need stronger containment against denial-of-service or runtime bugs, use OS- or process-level isolation around it.

## Supported Commands

The default registry currently includes the commands listed in [`commands/registry.go`](./commands/registry.go), grouped here by workflow.

- File and path: `cat`, `cp`, `mv`, `ln`, `ls`, `mkdir`, `rm`, `rmdir`, `touch`, `chmod`, `readlink`, `stat`, `basename`, `dirname`, `tree`, `du`, `file`, `find`
- Search and text: `grep`, `rg`, `awk`, `sed`, `cut`, `sort`, `uniq`, `head`, `tail`, `wc`, `printf`, `tee`, `comm`, `paste`, `tr`, `rev`, `nl`, `join`, `split`, `tac`, `diff`, `base64`
- Data: `jq`, `yq`, `sqlite3`
- Environment and execution: `echo`, `pwd`, `env`, `printenv`, `which`, `help`, `true`, `false`, `date`, `sleep`, `timeout`, `bash`, `sh`
- Network when configured: `curl`

The project targets high-value agent workflows, not full GNU flag parity for every command. Unsupported commands or flags fail normally.

`sqlite3` is backed by [`ncruces/go-sqlite3`](https://github.com/ncruces/go-sqlite3) using an in-memory connection plus explicit sandbox filesystem load and writeback. The current subset supports `:memory:` and sandbox file databases, list/CSV/JSON/line/column/table output, `-header`, `-readonly`, `-bail`, `-cmd`, `-echo`, help, and version output. `ATTACH`, `DETACH`, `VACUUM`, virtual-table creation, and `load_extension()` are denied so SQL cannot escape the sandbox filesystem.

## Shell Features

Shell parsing and execution are delegated to `mvdan/sh/v3`, with project-owned filesystem, command, policy, and trace layers around it.

The runtime supports a practical shell subset for agent workflows, including:

- pipelines and redirections
- variable expansion and command substitution
- conditionals and loops
- shell functions and common builtins handled by `mvdan/sh`
- virtual `cd` and `pwd` behavior against the sandbox filesystem
- nested `bash` and `sh` execution inside the same sandbox session

It is intentionally not a full Bash reimplementation. It does not aim to provide full GNU Bash compatibility, readline-style UX, shell history, job control, or host TTY emulation.

## Default Sandbox Layout

Each fresh session starts with a Unix-like virtual layout:

- home and default working directory: `/home/agent`
- scratch directory: `/tmp`
- command directories: `/usr/bin` and `/bin`
- default `PATH`: `/usr/bin:/bin`

Those command paths are virtual stubs used for shell resolution. Command implementations still come from the Go registry, not the host filesystem.

## Development

Common commands:

```bash
go build ./...
go test ./...
go run ./cmd/just-bash-go < script.sh
gofmt -w .
```

For architecture and product-boundary work, read [`SPEC.md`](./SPEC.md) before making changes.

## License

This project is licensed under the [Apache License 2.0](./LICENSE).
