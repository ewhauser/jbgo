# gbash

`gbash` is a deterministic, sandbox-only, bash-like runtime for AI agents, implemented in Go.

It ports the core product idea behind [Vercel's `just-bash`](https://github.com/vercel-labs/just-bash) to a Go-native runtime built on [`mvdan/sh/v3`](https://pkg.go.dev/mvdan.cc/sh/v3).

> **Note:** This is beta software. It has not been hardened or security tested and is less mature than the upstream TypeScript implementation. Use it with care.

Requires Go 1.26+

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

The repository uses a committed Go workspace. `examples/` is a separate Go module for demos, and `contrib/` contains separate opt-in modules for commands that should not bloat the core library dependency graph.

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

Use `commands.DefineCommand` with a custom `Config.Registry` when you want to add a command or override one of the defaults. Contrib commands use this same registry hook and live in separate Go modules so optional heavy dependencies stay out of the core `github.com/ewhauser/gbash` module.

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

To opt into contrib commands such as `sqlite3`, `jq`, and `yq`:

```go
package main

import (
	"context"

	"github.com/ewhauser/gbash/commands"
	contribjq "github.com/ewhauser/gbash/contrib/jq"
	contribsqlite3 "github.com/ewhauser/gbash/contrib/sqlite3"
	contribyq "github.com/ewhauser/gbash/contrib/yq"
	gbruntime "github.com/ewhauser/gbash/runtime"
)

func main() {
	registry := commands.DefaultRegistry()
	if err := contribjq.Register(registry); err != nil {
		panic(err)
	}
	if err := contribsqlite3.Register(registry); err != nil {
		panic(err)
	}
	if err := contribyq.Register(registry); err != nil {
		panic(err)
	}

	rt, err := gbruntime.New(&gbruntime.Config{Registry: registry})
	if err != nil {
		panic(err)
	}

	_, _ = rt.Run(context.Background(), &gbruntime.ExecutionRequest{
		Script: "printf '{\"name\":\"alice\"}\\n' | jq -r '.name'\n" +
			"printf 'name: alice\\n' | yq '.name'\n" +
			`sqlite3 :memory: "select 1;"`,
	})
}
```

The stock `gbash` CLI and zero-config `runtime.New(&runtime.Config{})` only include core commands. Contrib commands are opt-in by design.

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

The default registry includes commands for file ops, text processing, archival, and execution. See [`commands/registry.go`](./commands/registry.go) for the full list.

| Category | Commands |
|---|---|
| File and path | `basename` `cat` `chmod` `chown` `cp` `dirname` `du` `file` `find` `ln` `link` `ls` `dir` `mkdir` `mv` `readlink` `rm` `rmdir` `stat` `touch` `tree` |
| Search and text | `awk` `base32` `base64` `column` `comm` `cut` `diff` `grep` `head` `join` `nl` `paste` `printf` `rev` `rg` `sed` `seq` `sort` `split` `tac` `tail` `tee` `tr` `uniq` `wc` |
| Archive | `gzip` `gunzip` `tar` `zcat` |
| Environment and execution | `bash` `date` `echo` `env` `expr` `false` `help` `id` `md5sum` `printenv` `pwd` `sh` `sha1sum` `sha256sum` `sleep` `timeout` `true` `uptime` `which` `xargs` `yes` |
| Network (when configured) | `curl` |

The goal is high-value agent workflows, not full GNU flag parity. Unsupported commands or flags fail with a shell-style error.

### Contrib commands

Optional commands live in [`contrib/`](./contrib/) as separate Go modules so the core library stays dependency-light. They are not registered by default.

| Command | Module | Backed by |
|---|---|---|
| [`jq`](./contrib/jq/) | `github.com/ewhauser/gbash/contrib/jq` | [`itchyny/gojq`](https://github.com/itchyny/gojq) |
| [`sqlite3`](./contrib/sqlite3/) | `github.com/ewhauser/gbash/contrib/sqlite3` | [`ncruces/go-sqlite3`](https://github.com/ncruces/go-sqlite3) |
| [`yq`](./contrib/yq/) | `github.com/ewhauser/gbash/contrib/yq` | [`mikefarah/yq`](https://github.com/mikefarah/yq) |

See [Custom Commands](#custom-commands) for how to register them.
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

The repo is a Go workspace. The root module has the runtime, CLI, and core commands. [`contrib/`](./contrib/) and [`examples/`](./examples/) are separate modules to keep optional dependencies out of the core import graph.

`make build`, `make test`, and `make lint` cover all modules. See the [`Makefile`](./Makefile) for fuzz, bench, GNU coreutils compat, and release targets.

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

## AI Disclosure

All of the code in this repository was written by AI - the vast majority of it by Codex (Claude Code worked on a couple CI jobs).

## License

This project is licensed under the [Apache License 2.0](./LICENSE).
