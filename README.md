# gbash

`gbash` is a deterministic, sandbox-only, bash-like runtime for AI agents, implemented in Go.

It ports the core product idea behind [Vercel's `just-bash`](https://github.com/vercel-labs/just-bash) to a Go-native runtime built on [`mvdan/sh/v3`](https://pkg.go.dev/mvdan.cc/sh/v3).

Key properties:

- no host subprocess fallback
- no default compatibility mode
- virtual filesystem by default
- explicit Go command registry
- policy-controlled execution
- structured trace events for agent workflows
- optional allowlisted network access via `curl`

> **Note:** This is beta software. It has not been hardened or security tested and is less mature than the upstream TypeScript implementation. Use it with care.

Requires Go 1.25+.

Install the module with `go get github.com/ewhauser/gbash` and import packages from `github.com/ewhauser/gbash/...`.

## Table of Contents

- [Usage](#usage)
  - [Basic API](#basic-api)
  - [Network-Enabled API](#network-enabled-api)
  - [Persistent Sessions](#persistent-sessions)
  - [CLI](#cli)
  - [Workspace Example](#workspace-example)
- [Configuration](#configuration)
  - [Filesystem Backends](#filesystem-backends)
  - [Network Access](#network-access)
  - [Custom Commands](#custom-commands)
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

	gbruntime "github.com/ewhauser/gbash/runtime"
)

func main() {
	rt, err := gbruntime.New(&gbruntime.Config{})
	if err != nil {
		panic(err)
	}

	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
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

### Network-Enabled API

If you want `curl` inside the sandbox, opt in at runtime construction time and allowlist the destinations you expect to reach.

```go
package main

import (
	"context"
	"fmt"

	gbnetwork "github.com/ewhauser/gbash/network"
	gbruntime "github.com/ewhauser/gbash/runtime"
)

func main() {
	rt, err := gbruntime.New(&gbruntime.Config{
		Network: &gbnetwork.Config{
			AllowedURLPrefixes: []string{"https://api.example.com/v1/"},
			AllowedMethods:     []gbnetwork.Method{gbnetwork.MethodGet, gbnetwork.MethodHead},
			MaxResponseBytes:   10 << 20,
			DenyPrivateRanges:  true,
		},
	})
	if err != nil {
		panic(err)
	}

	result, err := rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "curl -o /tmp/status.json https://api.example.com/v1/status\ncat /tmp/status.json\n",
	})
	if err != nil {
		panic(err)
	}

	fmt.Print(result.Stdout)
}
```

Without `Config.Network` or `Config.NetworkClient`, `curl` is not registered in the sandbox at all. For more detail on allowed methods, redirects, and custom clients, see [Network Access](#network-access).

### Persistent Sessions

Use `Session.Exec` when you want multiple shell executions to share one sandbox filesystem.

```go
package main

import (
	"context"
	"fmt"

	gbruntime "github.com/ewhauser/gbash/runtime"
)

func main() {
	ctx := context.Background()

	rt, err := gbruntime.New(&gbruntime.Config{})
	if err != nil {
		panic(err)
	}

	session, err := rt.NewSession(ctx)
	if err != nil {
		panic(err)
	}

	if _, err := session.Exec(ctx, &gbruntime.ExecutionRequest{
		Script: "echo hello > /shared.txt\n",
	}); err != nil {
		panic(err)
	}

	result, err := session.Exec(ctx, &gbruntime.ExecutionRequest{
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
go install github.com/ewhauser/gbash/cmd/gbash@latest
```

Tagged builds are also published as prebuilt archives on the [GitHub Releases page](https://github.com/ewhauser/gbash/releases).

After installation, run the binary directly. From a local checkout, replace `gbash` with `go run ./cmd/gbash`.

```bash
printf 'echo hi\npwd\n' | gbash
```

Example:

```text
hi
/home/agent
```

Redirects and file reads also stay inside the virtual filesystem:

```bash
printf 'echo hi > /tmp.txt\ncat /tmp.txt\n' | gbash
```

When stdin is a terminal, the CLI starts an interactive shell automatically:

```bash
gbash
```

You can also force interactive mode explicitly:

```bash
printf 'pwd\ncd /tmp\npwd\nexit\n' | gbash -i
```

The interactive shell is intentionally minimal:

- it reuses one sandbox session, so filesystem changes persist
- it carries forward working directory and shell-visible environment state between entries
- it supports multiline input through `mvdan/sh/v3`'s interactive parser
- it does not provide history, line editing, job control, or host TTY emulation

The CLI can also report embedded release metadata:

```bash
gbash --version
```

For developer-only utility execution, the CLI also exposes an opt-in compatibility path:

```bash
gbash compat exec printf '%s\n' hello
```

That path is intentionally separate from the default sandbox script and REPL modes. It exists so external compatibility harnesses can invoke one registered utility at a time against the host filesystem and host environment.

### Workspace Example

The repository includes an `examples/` Go module so integration demos can carry their own dependencies without adding them to the root library module.

`openai-tool-call` uses the OpenAI Go SDK Responses API with a function tool named `bash`. The tool implementation calls `runtime.Run`, so the model sees a normal `bash` tool while execution still happens inside the `gbash` sandbox.

```bash
export OPENAI_API_KEY=your-api-key
go run ./examples/openai-tool-call
```

The example hardcodes `gpt-4.1-mini` and asks the model to run a simple `printf` command through the `bash` tool, then print only the tool's stdout.

`adk-bash-chat` uses [`adk-go`](https://github.com/google/adk-go) to build a local CLI chatbot around a persistent `gbash` bash tool session. It seeds an ops analytics lab under `/home/agent/lab`, prints each bash tool call and result inline, and carries forward filesystem changes, `PWD`, and exported environment variables across turns.

Gemini API:

```bash
export GOOGLE_API_KEY=your-api-key
go run ./examples/adk-bash-chat
```

Vertex AI:

```bash
export GOOGLE_CLOUD_PROJECT=your-project
export GOOGLE_CLOUD_LOCATION=us-central1
go run ./examples/adk-bash-chat --backend=vertex
```

Use `/reset` inside the chat to recreate the ADK conversation and reseed the sandboxed lab.

`sqlite-backed-fs` shows how to implement a custom `gbfs.FileSystem` on top of a host SQLite database file and pass it into `gbruntime.CustomFileSystem(...)`. Each run starts a fresh sandbox session, but the sandbox filesystem contents persist in the SQLite backing store when you reuse the same `--db` path.

```bash
go run ./examples/sqlite-backed-fs --db /tmp/gbash-sandbox.db --script "printf 'hello\\n' > /tmp/hello.txt"
go run ./examples/sqlite-backed-fs --db /tmp/gbash-sandbox.db --script "cat /tmp/hello.txt"
```

It also supports a persistent in-process REPL:

```bash
go run ./examples/sqlite-backed-fs --db /tmp/gbash-sandbox.db --repl
```

## Configuration

The main configuration surface is `runtime.Config`.

### Filesystem Setup

Filesystem setup is controlled through `Config.FileSystem`. It carries both:

- the factory that creates a fresh sandbox filesystem for each session
- the working directory that sessions start in

Most callers should use one of these runtime helpers:

- `gbruntime.InMemoryFileSystem()` for the default mutable sandbox
- `gbruntime.HostProjectFileSystem(root, opts)` for a real host project mounted read-only under an in-memory overlay
- `gbruntime.CustomFileSystem(factory, workingDir)` for seeded or otherwise custom backends

The zero value of `runtime.Config` still gives you an in-memory sandbox rooted at `/home/agent`.

For the closest parity with `just-bash`'s real-directory overlay:

```go
rt, err := gbruntime.New(&gbruntime.Config{
	FileSystem: gbruntime.HostProjectFileSystem("/path/to/project", gbruntime.HostProjectOptions{
		VirtualRoot: "/home/agent/project",
	}),
})
```

That mounts the host directory read-only at `/home/agent/project`, starts the session there, and keeps all writes, deletes, and command stubs in the in-memory upper layer. `VirtualRoot` defaults to `gbfs.DefaultHostVirtualRoot`. `MaxFileReadBytes` defaults to `gbfs.DefaultHostMaxFileReadBytes`. If you want the host tree mounted at `/`, set `VirtualRoot: "/"`.

For seeded or custom backends, implement `gbfs.Factory` and hand it to `CustomFileSystem`:

```go
type myFactory struct {
	base any
}

func (f myFactory) New(ctx context.Context) (gbfs.FileSystem, error) {
	return newMyJBFSAdapter(f.base), nil
}

rt, err := gbruntime.New(&gbruntime.Config{
	FileSystem: gbruntime.CustomFileSystem(
		myFactory{base: os.DirFS("/path/to/workspace")},
		"/home/agent",
	),
})
```

If you want copy-on-write behavior over another backend, wrap it with `gbfs.Overlay(...)` before passing it to `CustomFileSystem`.

Low-level `fs` helpers are available when you need to compose backends directly:

- `gbfs.Memory()` returns a factory for fresh in-memory filesystems
- `gbfs.Overlay(lower)` returns a copy-on-write factory over another `gbfs.Factory`
- `gbfs.Host(opts)` returns a factory for a read-only host-backed directory view
- `gbfs.Snapshot(fsys)` returns a factory that clones an existing filesystem into a read-only snapshot
- `gbfs.NewMemory`, `gbfs.NewOverlay`, `gbfs.NewHost`, and `gbfs.NewSnapshot` create concrete filesystem instances directly

The runtime integration point is still `gbfs.FileSystem`, so plain `io/fs.FS` is not enough by itself. If you already have a Go filesystem implementation, wrap it behind `gbfs.Factory` and adapt the richer contract: mutation, metadata, directory listing, symlinks, and cwd handling. A direct host read-write backend and a general mount router are still out of scope for the default runtime path.

### Network Access

Network access is disabled by default. When you set `Config.Network` or provide `Config.NetworkClient`, the runtime registers `curl` automatically. Otherwise `curl` is not present in the sandbox at all.

```go
import (
	gbnetwork "github.com/ewhauser/gbash/network"
	gbruntime "github.com/ewhauser/gbash/runtime"
)

rt, err := gbruntime.New(&gbruntime.Config{
	Network: &gbnetwork.Config{
		AllowedURLPrefixes: []string{"https://api.example.com/v1/"},
		AllowedMethods:     []gbnetwork.Method{gbnetwork.MethodGet, gbnetwork.MethodHead},
		MaxResponseBytes:   10 << 20,
		DenyPrivateRanges:  true,
	},
})
```

At runtime, network access works like this:

- `curl` is unavailable unless you explicitly opt in
- URL access is controlled by prefix allowlists
- allowed HTTP methods default to `GET` and `HEAD`
- redirects are revalidated before they are followed
- response bodies are capped
- private-range blocking is optional

The sandboxed network client enforces:

- URL-prefix allowlists
- HTTP-method allowlists
- redirect revalidation
- response-size limits
- optional private-range blocking

The current `curl` subset supports `-L`, `-I`, `-i`, `-X`, `-H`, `-d`, `-o`, `-f`, `-s`, and `-S`.

Typical uses:

```bash
# GET an allowlisted endpoint
curl https://api.example.com/v1/status

# Save a response body into the sandbox filesystem
curl -o /tmp/response.json https://api.example.com/v1/items
cat /tmp/response.json

# Opt in to POST by allowing the method in Config.Network
curl -X POST -H 'content-type: application/json' -d '{"name":"demo"}' \
  https://api.example.com/v1/items
```

If you want full control over the transport in tests or embedding code, inject your own `Config.NetworkClient` instead of using `Config.Network`.

### Custom Commands

Use `commands.DefineCommand` with a custom `Config.Registry` when you want to add a command or override one of the defaults.

```go
registry := commands.DefaultRegistry()
registry.Register(commands.DefineCommand("zstd", func(ctx context.Context, inv *commands.Invocation) error {
    // ... Implementation here ...
	return nil
}))

registry.RegisterLazy("echo", func() (commands.Command, error) {
	return commands.DefineCommand("echo", func(ctx context.Context, inv *commands.Invocation) error {
		_, err := fmt.Fprintf(inv.Stdout, "custom:%s\n", strings.Join(inv.Args, ","))
		return err
	}), nil
})

rt, err := runtime.New(&runtime.Config{Registry: registry})
```

See [`examples/custom-zstd/main.go`](./examples/custom-zstd/main.go) for a runnable example that adds a `zstd` command. Run it with `cd examples && make run-custom-zstd`.

### Registry and Policy

- `Config.Registry` lets you replace or extend the command set.
- `Config.Policy` controls command allowlists, path allowlists, symlink behavior, and execution limits.
- Unknown commands never fall through to the host OS; they fail with a shell-style command-not-found error instead.

If you are changing runtime boundaries, command ownership, or sandbox behavior, keep [`SPEC.md`](./SPEC.md) in sync.

## Security Model

- The shell only sees the filesystem and runtime configuration you provide.
- Command execution is registry-backed. Unknown commands never execute host binaries.
- There is no host shell fallback.
- There is no default runtime compatibility mode. The CLI-only `compat exec` path is a developer tool for external test harnesses and is not the default execution contract.
- Network access is off by default. When enabled, requests are constrained by allowlists and runtime limits.
- The default static policy applies execution budgets such as command-count, loop-iteration, glob-expansion, substitution-depth, and stdout/stderr capture limits.
- Trace events capture command execution, file access and mutation, and policy denials for debugging and agent orchestration.

This is not a hardened sandbox. If you need stronger containment against denial-of-service or runtime bugs, use OS- or process-level isolation around it.

## Supported Commands

The default registry currently includes the commands listed in [`commands/registry.go`](./commands/registry.go), grouped here by workflow.

- File and path: `cat`, `cp`, `mv`, `ln`, `ls`, `mkdir`, `rm`, `rmdir`, `touch`, `chmod`, `readlink`, `stat`, `basename`, `dirname`, `tree`, `du`, `file`, `find`
- Search and text: `grep`, `rg`, `awk`, `sed`, `cut`, `sort`, `uniq`, `head`, `tail`, `wc`, `printf`, `tee`, `comm`, `paste`, `tr`, `rev`, `nl`, `join`, `split`, `tac`, `diff`, `base64`
- Archive and data: `tar`, `gzip`, `gunzip`, `zcat`, `jq`, `yq`, `sqlite3`
- Environment and execution: `echo`, `pwd`, `env`, `printenv`, `which`, `help`, `true`, `false`, `date`, `sleep`, `timeout`, `xargs`, `bash`, `sh`
- Network when configured: `curl`

The project targets high-value agent workflows, not full GNU flag parity for every command. Unsupported commands or flags fail normally.

`tar`, `gzip`, `gunzip`, and `zcat` are intentionally focused subsets. The current surface covers create/list/extract, gzip-wrapped archives, `-C`, `-k`, `-O`, stdin/stdout flows, and extraction hardening around parent traversal and unsafe symlink targets. Append/update modes and non-gzip codecs are still out of scope.

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

This repository is a committed Go workspace:

- the root module contains the runtime, CLI, and tests
- [`examples/`](./examples) is a separate Go module for demos and external SDK integrations

Common commands from the repo root:

- `go build ./... ./examples/...`
- `go test ./... ./examples/...`
- `go run ./examples/openai-tool-call`
- `go run ./examples/adk-bash-chat`
- `go run ./examples/sqlite-backed-fs --db /tmp/gbash-sandbox.db --script "pwd"`
- `go run ./examples/sqlite-backed-fs --db /tmp/gbash-sandbox.db --repl`

For architecture and product-boundary work, read [`SPEC.md`](./SPEC.md) before making changes.

### coreutils compatibility

You can evaluate the skew between our implemented commands and [coreutils](https://www.gnu.org/software/coreutils/).

Prepare the pinned GNU source tree:

```bash
make gnu-test-setup
```

Fetch the prepared GNU build cache for your platform:

```bash
make gnu-build-cache-fetch
```

Run the full configured harness or limit it to selected utilities. `make gnu-test`
now prefers a prepared GNU build archive from the local cache, then the dedicated
GitHub Release cache, and only falls back to a local `configure && make` when no
prepared archive is available:

```bash
make gnu-test
make gnu-test GNU_UTILS="printf pwd"
```

Useful overrides:

- `GNU_UTILS` limits the utility list.
- `GNU_TESTS` runs exact GNU test files instead of the manifest-selected utility suites.
- `GNU_KEEP_WORKDIR=1` preserves the temporary patched/build workdir.
- `GNU_FORCE_REBUILD=1` bypasses any prepared archive and forces a fresh local GNU build.

Maintainers can refresh the published prepared build archive set with:

```bash
make gnu-build-cache-publish
```

## License

This project is licensed under the [Apache License 2.0](./LICENSE).
