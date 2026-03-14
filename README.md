# gbash

`gbash` is a deterministic, sandbox-only, bash-like runtime for AI agents, implemented in Go. It originally was inspired by [Vercel's `just-bash`](https://github.com/vercel-labs/just-bash).

> **Note:** This is beta software. It is likely that additional security hardening is needed. Use with care.

Requires Go 1.26+

Install the module with `go get github.com/ewhauser/gbash` and import `github.com/ewhauser/gbash` for the embedding API.

## Table of Contents

- [Public Packages](#public-packages)
- [Usage](#usage)
  - [Basic API](#basic-api)
  - [Network-Enabled API](#network-enabled-api)
  - [Persistent Sessions](#persistent-sessions)
  - [CLI](#cli)
  - [Workspace Examples](#workspace-examples)
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

### Basic API

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

Output:

```text
exit=0
hello
/home/agent
```

`Runtime.Run` creates a fresh sandbox session for that call. If you want shared filesystem state across multiple executions, use `Runtime.NewSession`.

### Network-Enabled API

If you want `curl` inside the sandbox, opt in at runtime construction time and allowlist the destinations you expect to reach.

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

Without `Config.Network` or `Config.NetworkClient`, `curl` is not registered in the sandbox at all. For more detail on methods, redirects, and custom clients, see [Network Access](#network-access).

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

Output:

```text
hello
/home/agent
```

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

### Workspace Examples

The repository uses a committed Go workspace. `examples/` is a separate Go module for demos, and `contrib/` contains separate opt-in modules for commands that should not bloat the core library dependency graph.

| Example | Description |
|---|---|
| [`adk-bash-chat`](./examples/adk-bash-chat/) | Local CLI chatbot using [`adk-go`](https://github.com/google/adk-go) with a persistent `gbash` bash tool session and a seeded ops analytics lab |
| [`custom-zstd`](./examples/custom-zstd/) | Demonstrates custom command registration by adding a `zstd` compression/decompression command |
| [`openai-tool-call`](./examples/openai-tool-call/) | Uses the OpenAI Go SDK Responses API with `gbash` as a `bash` function tool |
| [`sqlite-backed-fs`](./examples/sqlite-backed-fs/) | Custom `gbfs.FileSystem` backed by a SQLite database for persistent sandbox filesystem state |
| [`transactional-workspaces`](./examples/transactional-workspaces/) | Narrated snapshot/rollback/branch demo showing how `gbash` sessions become reversible, inspectable shell workspaces |
| [`website`](./examples/website/) | Vendored Next.js terminal website that runs `gbash` in the browser via WebAssembly |

## Configuration

The main configuration surface is `gbash.Config`, but most callers should prefer `gbash.New(...)` with option helpers.

### Filesystem Setup

Filesystem setup is controlled through `Config.FileSystem`. It carries both:

- the factory that creates a fresh sandbox filesystem for each session
- the working directory that sessions start in

Most callers should use one of these entry points:

- `gbash.New()` for the default mutable in-memory sandbox
- `gbash.WithWorkspace(root)` for a real host directory mounted read-only under an in-memory overlay
- `gbash.WithFileSystem(gbash.ReadWriteDirectoryFileSystem(...))` for a just-bash-style mutable host-backed root
- `gbash.WithFileSystem(gbash.CustomFileSystem(...))` for seeded or otherwise custom backends

The zero value of `gbash.Config` still gives you an in-memory sandbox rooted at `/home/agent`.

For the closest parity with `just-bash`'s real-directory overlay:

```go
gb, err := gbash.New(
	gbash.WithWorkspace("/path/to/project"),
)
```

That mounts the host directory read-only at `/home/agent/project`, starts the session there, and keeps all writes, deletes, and command stubs in the in-memory upper layer.

If you need to change the mount point or host-file read cap, drop down to the explicit filesystem helper:

```go
gb, err := gbash.New(
	gbash.WithFileSystem(gbash.HostDirectoryFileSystem("/path/to/project", gbash.HostDirectoryOptions{
		MountPoint: "/home/agent/project",
	})),
)
```

`MountPoint` defaults to `gbash.DefaultWorkspaceMountPoint`. `MaxFileReadBytes` defaults to `gbash.DefaultHostFileReadBytes`. If you want the host tree mounted at `/`, set `MountPoint: "/"`.

If you want writes to persist directly back to the host directory instead of landing in the in-memory overlay, use the read/write helper:

```go
gb, err := gbash.New(
	gbash.WithFileSystem(gbash.ReadWriteDirectoryFileSystem("/path/to/project", gbash.ReadWriteDirectoryOptions{})),
	gbash.WithWorkingDir("/"),
)
```

This mode maps the host directory to sandbox `/`, so it is best suited to compatibility harnesses and other opt-in developer workflows.

### Network Access

Network access is disabled by default. When you set `Config.Network` or provide `Config.NetworkClient`, the runtime registers `curl` automatically. Otherwise `curl` is not present in the sandbox at all.

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

If you want the simplest allowlist-only setup, use `gbash.WithHTTPAccess(...)` instead of `gbash.WithNetwork(...)`. If you want full control over the transport in tests or embedding code, inject your own `Config.NetworkClient` instead of using `Config.Network`.

## Security Model

- The shell only sees the filesystem and runtime configuration you provide.
- Command execution is registry-backed. Unknown commands never execute host binaries.
- Network access is off by default. When enabled, requests are constrained by allowlists and runtime limits.
- The default static policy applies execution budgets such as command-count, loop-iteration, glob-expansion, substitution-depth, and stdout/stderr capture limits.
- Trace events capture command execution, file access and mutation, and policy denials for debugging and agent orchestration.

This is not a hardened sandbox. If you need stronger containment against denial-of-service or runtime bugs, use OS- or process-level isolation around it.

## Supported Commands

The default registry includes commands for file ops, text processing, archival, and execution. Use `gbash.DefaultRegistry()` when you want to start from the stock builtin command set and register custom commands on top.

| Category | Commands |
|---|---|
| File and path | `basename` `cat` `chmod` `chown` `cp` `dirname` `du` `file` `find` `ln` `link` `ls` `dir` `mkdir` `mv` `readlink` `rm` `rmdir` `stat` `touch` `tree` |
| Search and text | `base32` `base64` `column` `comm` `cut` `diff` `grep` `head` `join` `nl` `paste` `printf` `rev` `rg` `sed` `seq` `sort` `split` `tac` `tail` `tee` `tr` `uniq` `wc` |
| Archive | `gzip` `gunzip` `tar` `zcat` |
| Environment and execution | `bash` `date` `echo` `env` `expr` `false` `help` `id` `md5sum` `printenv` `pwd` `sh` `sha1sum` `sha256sum` `sleep` `timeout` `true` `uptime` `which` `xargs` `yes` |
| Network (when configured) | `curl` |

Many commands are ported from [uutils/coreutils](https://github.com/uutils/coreutils) so have full GNU flag parity. You can view the current compatibility matrix with `coreutils` test suite [here](https://ewhauser.github.io/gbash/compat/latest/)

### Contrib commands

Optional commands live in [`contrib/`](./contrib/) as separate Go modules so the core library stays dependency-light. They are not registered by default.

Published versions are coordinated with the root module release line. The root
module uses plain tags like `v0.0.7`; contrib modules use nested-module tags
like `contrib/jq/v0.0.7` and `contrib/sqlite3/v0.0.7`. The child modules keep
real version requirements in `go.mod`, plus committed local `replace`
directives so the repo still builds against the local checkout during
development.

| Command | Module | Backed by |
|---|---|---|
| [`awk`](./contrib/awk/) | `github.com/ewhauser/gbash/contrib/awk` | [`benhoyt/goawk`](https://github.com/benhoyt/goawk) |
| [`jq`](./contrib/jq/) | `github.com/ewhauser/gbash/contrib/jq` | [`itchyny/gojq`](https://github.com/itchyny/gojq) |
| [`sqlite3`](./contrib/sqlite3/) | `github.com/ewhauser/gbash/contrib/sqlite3` | [`ncruces/go-sqlite3`](https://github.com/ncruces/go-sqlite3) |
| [`yq`](./contrib/yq/) | `github.com/ewhauser/gbash/contrib/yq` | [`mikefarah/yq`](https://github.com/mikefarah/yq) |

Use `github.com/ewhauser/gbash/contrib/extras` when you want to build a full registry for the contrib command set in one call.

```go
import "github.com/ewhauser/gbash/contrib/extras"

gb, err := gbash.New(gbash.WithRegistry(extras.FullRegistry()))
```

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

The repo is a Go workspace. The root module has the public `gbash` package, CLI, internal runtime implementation, and core commands. [`contrib/`](./contrib/) and [`examples/`](./examples/) are separate modules to keep optional dependencies out of the core import graph.

`make build`, `make test`, and `make lint` cover all modules. See the [`Makefile`](./Makefile) for fuzz, bench, GNU coreutils compat, and release targets.

The repo uses both [`go.work`](./go.work) and committed child-module `replace`
directives. `go.work` keeps the workspace coherent at the repo root, and the
child-module replaces make each nested module buildable on its own while still
declaring real tagged dependencies for published consumption.

Use `make fix-modules MODULE_VERSION=vX.Y.Z` when preparing the next coordinated
root plus contrib release line. That updates the nested module requirements,
refreshes the local replaces, and runs `go mod tidy` in each child module.

The supported release path is now GitHub Actions driven:

- run `make release` or dispatch the `Prepare Release` workflow manually
- review and merge the generated `release/vX.Y.Z` PR into `main`
- let the `Publish Release` workflow create the root plus contrib tags and publish the root GitHub release automatically

`Prepare Release` derives the next release line by taking the latest root `v*`
tag and incrementing the patch number.

`make tag-release RELEASE_VERSION=vX.Y.Z` remains available as a local fallback
for debugging or manual recovery, but it is no longer the primary release path.

### local comparison benchmark

Run the benchmark from the repo root:

```bash
make bench-compare
```

Sample local results from March 13, 2026, using the default 100 runs:

| Scenario | `gbash` median | `gbash` p95 | `just-bash` median | `just-bash` p95 |
| --- | ---: | ---: | ---: | ---: |
| `startup_echo` | `5.08ms` | `6.86ms` | `618.94ms` | `956.86ms` |
| `workspace_inventory` | `18.50ms` | `51.81ms` | `618.30ms` | `725.79ms` |

These numbers are a local reference point, not a portability guarantee. Startup comparisons may not be fully apples to apples yet, because `just-bash` currently embeds tools like Python in its base container and `gbash` does not.

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
