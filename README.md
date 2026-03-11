# just-bash-go

`just-bash-go` is a deterministic, sandbox-only, bash-like runtime for AI agents.

> **Note:** This is beta software. It has not been hardened or security tested and is less mature than [Vercel's implementation](https://github.com/vercel-labs/just-bash). Use at your own risk and please provide feedback.

It ports the core product idea behind `just-bash` into Go, using [`mvdan/sh/v3`](https://pkg.go.dev/mvdan.cc/sh/v3) for shell parsing and evaluation semantics while keeping execution inside a Go-native sandbox:

- no host shell dependency
- no host subprocess fallback
- no compatibility mode
- virtual filesystem by default
- explicit Go command registry
- policy-first execution
- structured tracing for agent workflows
- a minimal interactive shell for local sandbox exploration

This project is intentionally not a full Bash reimplementation.

## Status

The repository currently contains:

- a draft architecture spec in [`SPEC.md`](./SPEC.md)
- a runtime scaffold around `mvdan/sh/v3`
- an in-memory filesystem backend
- a sandboxed network client layer with allowlists, redirect checks, and response caps
- a static policy layer
- a trace event buffer
- an initial command registry with:
  - `echo`
  - `pwd`
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
  - `jq`
  - `mkdir`
  - `rm`
  - `curl` when network access is explicitly configured

The implementation is early-stage but coherent and runnable.

The `jq` command is backed by `gojq` and now supports a broader CLI-compatible subset, including `-R`, `-f`, `--arg`, `--argjson`, `--slurpfile`, `--rawfile`, `--args`, `--jsonargs`, `--raw-output0`, `--indent`, and `--tab`.

The text-tool expansion is also in place. `sort` supports lexical and numeric ordering, reverse, unique, case-folded comparison, keyed sorts via `-k`, and custom field separators via `-t`. `uniq` supports adjacent-run deduping with `-c`, `-d`, and `-u`. `cut` supports `-f`, `-c`, `-d`, and `-s`. `sed` is intentionally a subset: it currently supports `-n`, `-e`, `-i`, numeric and regex addresses, `$`, simple ranges, and the `s`, `d`, `p`, and `q` commands with `g`/`i` substitution flags and alternate delimiters. The newer text/search commands are also implemented as practical subsets: `printf` covers the core shell format verbs plus `%b` escape handling; `rg` supports recursive regex search with `-n`, `-i`, `-l`, `-c`, `-g`, `--hidden`, and `--files`; `awk` is backed by `goawk` with `-F`, `-v`, and `-f` while keeping `system()`, file I/O, and shell pipes disabled; `comm`, `paste`, `tr`, `rev`, `nl`, `join`, `split`, `tac`, `diff`, and `base64` all exist with strong agent-oriented subsets rather than full GNU parity.

The shell/process helper batch is also in place. `tee` supports pass-through writes with `-a`; `env` supports `-i`, `-u`, inline assignments, and scoped nested command execution; `printenv` prints the whole environment or named variables; `which` supports `-a` and `-s`; `help` exposes runtime-owned builtin help topics; `date` is intentionally UTC-only and supports `-u`, `-d/--date`, `-I`, `-R`, and `+FORMAT`; `sleep` supports decimal durations plus `s`/`m`/`h`/`d` suffixes; `timeout` runs nested commands with bounded execution time; `xargs` supports `-n`, `-I`, `-0`, `-d`, `-t`, and `-r`; and `bash` / `sh` are nested shell wrappers implemented inside the same sandbox session rather than host-shell escapes.

The file/path batch is also in place. `touch` supports creation, `-c`, and `-d/--date`; `ln` supports hard links plus `-s` and `-f`; `chmod` supports octal and symbolic modes with recursive `-R`; `readlink` supports raw output plus `-f`; `stat` supports default output plus `-c` format strings; `tree` supports `-a`, `-d`, `-L`, and `-f`; `du` supports `-a`, `-s`, `-h`, `-c`, and `--max-depth`; and `file` supports `-b`, `-i`, simple magic-byte detection, shebang detection, and extension-based text detection.

Network access is still off by default. When configured, the runtime exposes a minimal `curl` command backed by a sandboxed client that enforces URL-prefix allowlists, HTTP-method allowlists, manual redirect revalidation, response-size limits, and optional private-range blocking.

## Design Goals

- Support the subset of shell behavior that AI agents actually need
- Keep execution deterministic and observable
- Treat sandboxing as the default and only runtime mode
- Implement commands in Go instead of spawning OS binaries
- Keep boundaries clean:
  - `mvdan/sh` owns shell semantics
  - `just-bash-go` owns filesystem, commands, policy, and tracing

## Non-Goals

`just-bash-go` does not aim to provide:

- full GNU Bash compatibility
- readline-style shell UX
- shell history, advanced TTY behavior, or job control
- host command passthrough
- hidden subprocess escape hatches

If a command is not registered, execution fails with a shell-style error.

## Repository Layout

```text
cmd/just-bash-go/  CLI entrypoint
commands/          Go-native command implementations and registry
fs/                Filesystem interfaces and in-memory backend
network/           Sandboxed HTTP client and URL allowlist enforcement
policy/            Sandbox policy types and default implementation
runtime/           Public runtime API and result capture
shell/             mvdan/sh integration and handler wiring
trace/             Structured event types and recorder
SPEC.md            Technical design spec
```

## Quick Start

### Run the CLI

The CLI reads a script from stdin and executes it inside the sandbox runtime.

```bash
printf 'echo hi\npwd\n' | go run ./cmd/just-bash-go
```

Example:

```text
hi
/home/agent
```

Redirects and file reads are also handled inside the virtual filesystem:

```bash
printf 'echo hi > /tmp.txt\ncat /tmp.txt\n' | go run ./cmd/just-bash-go
```

### Run the interactive shell

When stdin is a terminal, the CLI starts an interactive shell automatically:

```bash
go run ./cmd/just-bash-go
```

You can also force interactive mode explicitly, which is useful when piping a scripted REPL transcript:

```bash
printf 'pwd\ncd /tmp\npwd\nexit\n' | go run ./cmd/just-bash-go -i
```

A typical session looks like this:

```text
~$ pwd
/home/agent
~$ cd /tmp
/tmp$ echo hi > note.txt
/tmp$ cat note.txt
hi
/tmp$ exit
```

REPL behavior:

- start it with `go run ./cmd/just-bash-go`
- force REPL mode with `go run ./cmd/just-bash-go -i`
- leave the shell with `exit` or `exit <code>`
- use multiline shell constructs normally; the prompt switches to `> ` until the statement is complete

Example multiline input:

```text
~$ if true; then
> echo hi
> fi
hi
~$
```

The interactive shell is intentionally minimal:

- it reuses one sandbox session, so filesystem changes persist
- it carries forward the working directory and shell-visible variable state between entries
- it supports multiline input through `mvdan/sh/v3`'s interactive parser
- it does not provide history, line editing, job control, or host TTY emulation

### Run tests

```bash
go test ./...
```

## Network Access

Network access is disabled unless you configure it explicitly in `runtime.Config`. When network access is configured, `curl` is registered automatically; otherwise `curl` is not present in the sandbox at all.

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

The current `curl` subset supports `-L`, `-I`, `-i`, `-X`, `-H`, `-d`, `-o`, `-f`, `-s`, and `-S`.

## Library Example

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

## Current Architecture

Execution currently flows like this:

1. Parse shell source with `mvdan/sh/v3/syntax`
2. Construct a fresh `interp.Runner`
3. Wire custom handlers for:
   - file open
   - stat
   - readdir
   - simple command interception
   - command dispatch
4. Resolve commands through the Go registry
5. Enforce policy on commands and file paths
6. Capture stdout, stderr, exit code, and trace events

One implementation detail matters: the runtime installs an `interp.ExecHandlers(...)` middleware that never falls through to the default host executor, so command execution does not end in `mvdan/sh`'s `DefaultExecHandler`.

The interactive CLI is a thin wrapper around the same runtime. It uses `syntax.Parser.InteractiveSeq` to gather multiline statements and executes each completed entry through the normal session-backed `Exec` path.

## Why `mvdan/sh`

`mvdan/sh/v3` gives the project a solid shell engine layer for:

- parsing
- AST representation
- quoting and expansion behavior
- pipelines and control flow
- function and builtin semantics where feasible

That lets this project stay focused on the product-specific layers:

- sandboxed filesystem
- command registry
- policy enforcement
- tracing
- deterministic result capture

## Roadmap

Near-term priorities:

- expand flag depth and higher-order command support
- strengthen policy enforcement and limits
- expand trace coverage
- add more integration tests for agent-style workflows

Longer-term possibilities:

- overlay and snapshot filesystems
- JSON-oriented helpers
- safe network helpers with allowlisted hosts

## Contributing

Before making larger implementation changes, read [`SPEC.md`](./SPEC.md). The project is opinionated:

- sandbox-only
- no host subprocess execution
- no compatibility mode
- explicit command registry
- narrow filesystem abstraction

Those constraints are part of the product definition, not temporary limitations.

## License

This project is licensed under the [Apache License 2.0](./LICENSE).
