# gbash

A deterministic, sandbox-only, bash-like runtime for AI agents, implemented in Go.

Shell parsing and execution are owned in-tree under `internal/shell`, with a project-owned virtual filesystem, registry-backed command execution, policy enforcement, and structured tracing around that shell core. Commands never fall through to host binaries, and network access is off by default. Originally inspired by [Vercel's `just-bash`](https://github.com/vercel-labs/just-bash).

> [!WARNING]
> This is alpha software. It is likely that additional security hardening is needed. Use with care.

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
  - [Host Platform](#host-platform)
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
- 90+ built-in commands with GNU coreutils compatibility coverage ([compatibility report](https://ewhauser.github.io/gbash/docs/compatibility/coreutils/))
- Optional allowlisted network access via `curl`
- Persistent sessions with shared filesystem state across executions
- Shared JSON-RPC server mode for session-oriented hosts and wrapper binaries
- Host directory mounting with read-only overlay for real project workspaces
- Execution budgets — command count, loop iterations, glob expansion, stdout/stderr limits
- Opt-in structured trace events and lifecycle logs for debugging and agent orchestration
- WebAssembly support — runs in the browser ([demo](./website/))

## Public Packages

- `github.com/ewhauser/gbash`: the core Go runtime and embedding API
- `github.com/ewhauser/gbash/host`: the public host adapter boundary for platform and process behavior
- `github.com/ewhauser/gbash/server`: shared JSON-RPC server mode for hosting persistent gbash sessions
- `github.com/ewhauser/gbash/contrib/...`: optional Go command and tool modules
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
The coordinated release workflow also exports the website and deploys it to [GitHub Pages](https://ewhauser.github.io/gbash/), preserving raw compatibility assets under `/compat/latest/` for the published [compatibility report](https://ewhauser.github.io/gbash/docs/performance/compatibility/).

## Quick Start

Try it with `go run` — no install required:

```bash
go run github.com/ewhauser/gbash/cmd/gbash@latest -c 'echo hello; pwd; ls -la'
```

You should see `hello`, the default working directory `/home/agent`, and the initial listing for the empty sandbox home directory.

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

For programmatic wrappers and harnesses, non-interactive runs can emit one structured JSON object instead of streaming stdout and stderr directly:

```bash
gbash -c 'echo hello' --json
```

The JSON payload includes `stdout`, `stderr`, `exitCode`, truncation flags, timing metadata, and trace metadata when the wrapper enables tracing on the underlying runtime.

For long-lived agent or editor integrations, the same shared CLI frontend can serve a JSON-RPC protocol instead of executing one script:

```bash
gbash --server --socket /tmp/gbash.sock --session-ttl 30m
```

```bash
gbash --server --listen 127.0.0.1:8080 --session-ttl 30m
```

The server speaks JSON-RPC 2.0 over either a Unix socket or an explicit loopback TCP listener. `session_id` maps 1:1 to a persistent sandbox session, and `session.exec` runs one non-interactive shell execution inside that session and returns the full result in one response. Filesystem shape is still chosen at server startup through the normal CLI/runtime options such as `--root`, `--readwrite-root`, and `--cwd`; it is not configured over the wire. The shared CLI requires exactly one transport flag, `--socket PATH` or `--listen HOST:PORT`, and `--listen` is restricted to loopback hosts because the protocol has no built-in authentication.

Install `gbash-extras` when you want the same CLI surface with the stable official contrib commands (`awk`, `html-to-markdown`, `jq`, `sqlite3`, and `yq`) pre-registered:

```bash
gbash-extras -c 'jq -r .name data.json'
```

`gbash-extras --server --socket /tmp/gbash-extras.sock` and `gbash-extras --server --listen 127.0.0.1:8081` expose the same JSON-RPC protocol with the stable extras registry already installed.

The shared frontend is also exposed as the public `github.com/ewhauser/gbash/cli` package. Call `cli.Run` with a `cli.Config` to reuse the stock flag parsing, interactive mode, server mode, and runtime setup from your own wrapper binary. For direct embedding without going through the CLI package, use `github.com/ewhauser/gbash/server`.
## Configuration

### Filesystem

Most callers should use one of these entry points:

- `gbash.New()` — default mutable in-memory sandbox
- `gbash.WithFileSystem(gbash.SeededInMemoryFileSystem(...))` — in-memory sandbox preloaded with eager or lazy files
- `gbash.WithWorkspace(root)` — real host directory mounted read-only under an in-memory overlay
- `gbash.WithFileSystem(gbash.MountableFileSystem(...))` — multi-mount namespace over a base filesystem plus sibling mounts
- `gbash.WithFileSystem(gbash.ReadWriteDirectoryFileSystem(...))` — just-bash-style mutable host-backed root
- `gbash.WithFileSystem(gbash.CustomFileSystem(...))` — custom backends

Preload an in-memory sandbox with eager or lazy files:

```go
gb, err := gbash.New(
	gbash.WithFileSystem(gbash.SeededInMemoryFileSystem(gbfs.InitialFiles{
		"/home/agent/config.json": {Content: []byte("{\"mode\":\"dev\"}\n")},
		"/home/agent/big.txt": {
			Lazy: func(ctx context.Context) ([]byte, error) {
				return fetchLargeFixture(ctx)
			},
		},
	})),
)
```

Compose multiple sandbox mounts under one namespace:

```go
gb, err := gbash.New(
	gbash.WithFileSystem(gbash.MountableFileSystem(gbash.MountableFileSystemOptions{
		Mounts: []gbfs.MountConfig{
			{MountPoint: "/workspace", Factory: gbfs.Overlay(gbfs.Host(gbfs.HostOptions{Root: "/path/to/project", VirtualRoot: "/"}))},
			{MountPoint: "/cache", Factory: gbfs.Memory()},
		},
	})),
)
```

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

### Host Platform

By default, gbash uses an internal virtual host adapter. That preserves the existing sandbox behavior: virtual pipes, virtual process metadata, and build-dependent platform defaults.

Embedders can opt into the underlying process and OS view with `WithHost(host.NewSystem())`:

```go
import (
	"github.com/ewhauser/gbash"
	"github.com/ewhauser/gbash/host"
)

gb, err := gbash.New(
	gbash.WithHost(host.NewSystem()),
)
```

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

Allowed URL prefixes are origin- and path-boundary aware. For example, `https://api.example.com/v1` matches `/v1` and `/v1/...`, but not `/v10`.

For full transport control in tests or embedding, inject your own `Config.NetworkClient`.
See [`examples/oauth-network-extension`](./examples/oauth-network-extension) for a demo that injects OAuth headers from a host-side vault so the sandbox never sees the bearer token.

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

The default runtime exposes registry-backed commands plus shell builtins and shims. Use `gbash.DefaultRegistry()` to start from the stock registry-backed command set and register custom commands on top.

| Category | Commands |
|---|---|
| File and path | `basename` `cat` `chmod` `chown` `cp` `dircolors` `dirname` `du` `file` `find` `ln` `link` `ls` `dir` `mkdir` `mktemp` `mv` `readlink` `realpath` `rm` `rmdir` `stat` `touch` `tree` `truncate` `unlink` `vdir` |
| Search and text | `base32` `base64` `basenc` `column` `comm` `csplit` `cut` `diff` `egrep` `fgrep` `grep` `head` `join` `nl` `numfmt` `od` `paste` `printf` `rev` `sed` `seq` `sort` `split` `strings` `tac` `tail` `tee` `tr` `uniq` `wc` `xan` |
| Archive | `gzip` `gunzip` `tar` `zcat` |
| Builtins | `.` `:` `[` `alias` `break` `builtin` `cd` `command` `continue` `declare` `dirs` `echo` `eval` `exec` `exit` `export` `false` `getopts` `hash` `help` `history` `let` `local` `mapfile` `popd` `printf` `pushd` `pwd` `read` `readarray` `readonly` `return` `set` `shift` `shopt` `source` `test` `trap` `true` `type` `typeset` `unalias` `unset` `wait` |
| Environment and execution | `arch` `b2sum` `bash` `cksum` `clear` `date` `echo` `env` `expr` `factor` `false` `help` `history` `id` `md5sum` `printenv` `pwd` `sh` `sha1sum` `sha224sum` `sha256sum` `sha384sum` `sha512sum` `sleep` `sum` `test` `timeout` `true` `tsort` `tty` `uname` `uptime` `which` `who` `whoami` `xargs` `yes` |
| Network (when configured) | `curl` |
| Extras* | `awk` `html-to-markdown` `jq` `sqlite3` `yq` |

\* Use `gbash-extras` for extras commands.

Some builtin names, such as `echo`, `help`, `history`, `printf`, `pwd`, `test`, `true`, and `false`, are routed to `gbash`'s registry-backed implementations and therefore also appear in the command categories above.

Shell language support also includes indexed arrays and associative arrays.

Many commands are ported from [uutils/coreutils](https://github.com/uutils/coreutils) and have full GNU flag parity. See the current [compatibility report](https://ewhauser.github.io/gbash/docs/performance/compatibility/).

### Contrib Modules

Optional contrib packages live in [`contrib/`](./contrib/) as separate Go modules so the core library stays dependency-light. Command modules are not registered by default, and helper/tool modules do not change the default runtime surface.

| Module | Import Path | Backed by |
|---|---|---|
| [`awk`](./contrib/awk/) | `github.com/ewhauser/gbash/contrib/awk` | [`benhoyt/goawk`](https://github.com/benhoyt/goawk) |
| [`bashtool`](./contrib/bashtool/) | `github.com/ewhauser/gbash/contrib/bashtool` | `gbash` runtime execution plus the upstream [`bashkit`](https://github.com/everruns/bashkit) Bash tool contract |
| [`codingtools`](./contrib/codingtools/) | `github.com/ewhauser/gbash/contrib/codingtools` | `gbash/fs.FileSystem` plus the upstream [`pi-mono` coding-agent](https://github.com/badlogic/pi-mono/tree/main/packages/coding-agent/src/core/tools) `read`, `edit`, and `write` tool semantics |
| [`html-to-markdown`](./contrib/htmltomarkdown/) | `github.com/ewhauser/gbash/contrib/htmltomarkdown` | [`JohannesKaufmann/html-to-markdown`](https://github.com/JohannesKaufmann/html-to-markdown) |
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

Shell parsing and execution are provided by the project-owned shell core under `internal/shell`, with project-owned filesystem, command, policy, and observability layers around it.

The runtime supports a practical shell subset for agent workflows, including:

- pipelines and redirections
- variable expansion and command substitution
- conditionals and loops
- shell functions and common builtins handled by the in-tree shell core
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
| [`agentfs-backed-fs`](./examples/agentfs-backed-fs/) | Uses the upstream AgentFS Go SDK as a custom persistent `gbash` filesystem backend |
| [`custom-zstd`](./examples/custom-zstd/) | Demonstrates custom command registration by adding a `zstd` compression/decompression command |
| [`gbash-eval`](./examples/gbash-eval/) | Standalone LLM evaluator example that ports [`bashkit`](https://github.com/everruns/bashkit)'s `crates/bashkit-eval` design and vendored JSONL datasets onto `gbash` |
| [`openai-tool-call`](./examples/openai-tool-call/) | Uses the OpenAI Go SDK Responses API with `gbash` as a `bash` function tool |
| [`sqlite-backed-fs`](./examples/sqlite-backed-fs/) | Custom `gbfs.FileSystem` backed by a SQLite database for persistent sandbox filesystem state |
| [`transactional-workspaces`](./examples/transactional-workspaces/) | Narrated snapshot/rollback/branch demo showing how `gbash` sessions become reversible, inspectable shell workspaces |

The `gbash-eval` example is intentionally attribution-heavy because it is a direct Go port of upstream evaluator ideas and copied dataset fixtures from [`everruns/bashkit`](https://github.com/everruns/bashkit/tree/main/crates/bashkit-eval), adapted under Apache-2.0 for `gbash`.

Smoke test from the repo root:

```bash
export OPENAI_API_KEY=your-api-key
go run ./examples/gbash-eval run \
  --dataset ./examples/gbash-eval/data/smoke-test.jsonl \
  --provider openresponses \
  --model gpt-5-codex \
  --save
```

To run only selected eval task IDs, add `--task` with repeated or comma-separated values.

## Development

`make build`, `make test`, and `make lint` cover the Go modules. `make test` now resolves the pinned GNU `diff` oracle through Nix via `./scripts/ensure-diffutils.sh`; set `GBASH_CONFORMANCE_DIFF` if you want to override that path explicitly. `make conformance-test` resolves the pinned bash and curl oracles through Nix via `./scripts/ensure-bash.sh` and `./scripts/ensure-curl.sh`; set `GBASH_CONFORMANCE_BASH` or `GBASH_CONFORMANCE_CURL` to override those paths explicitly. See [CONTRIBUTING.md](./CONTRIBUTING.md) for repository structure, module versioning, release process, benchmarks, and GNU coreutils compatibility testing.

For the documentation site, run `make website-dev` to start a local Next.js server seeded with the latest published compatibility assets from `/compat/latest/`.

## License

This project is licensed under the [Apache License 2.0](./LICENSE). Copyright
2026 Eric Hauser.

## Acknowledgements

- [`just-bash`](https://github.com/vercel-labs/just-bash) sparked the idea for `gbash` and provided the starting point for the initial port.
- [`mvdan/sh`](https://github.com/mvdan/sh) provides the shell parser and interpreter foundation that `gbash` forks and builds on in-tree.
- [`uutils/coreutils`](https://github.com/uutils/coreutils) provides the baseline behavior and implementations behind many of the command ports.
- [`bashkit`](https://github.com/everruns/bashkit) provided the approach for bash conformance testing and agent evals
