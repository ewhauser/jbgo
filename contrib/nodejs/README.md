# contrib/nodejs

`contrib/nodejs` provides an experimental sandboxed `nodejs` command for gbash.

It is intentionally not included in `contrib/extras` yet. To use it, register it explicitly from `github.com/ewhauser/gbash/contrib/nodejs`.

**WARNING: this implementation is absolutely not secure as-is. It needs a lot more hardening before it will be included in extras**

## Status

- experimental
- opt-in only
- not part of `gbash.DefaultRegistry()`
- not bundled by `contrib/extras`

## How It Works

Each `nodejs` execution creates a fresh `goja.Runtime` and a fresh `goja_nodejs/require.Registry`.

The command uses a curated Node-like surface:

- `require`, `module`, `exports`, `__filename`, and `__dirname` come from `goja_nodejs`
- `Buffer` is enabled from `goja_nodejs/buffer`
- `console`, `process`, `fs`, and `path` are gbash-owned replacements
- file access always flows through `Invocation.FS`
- stdout and stderr always flow through `Invocation`
- module loading only allows builtin modules plus relative or absolute sandbox paths

The runtime intentionally omits or rejects unsafe host-backed APIs such as:

- `fetch`
- `http`
- `https`
- `net`
- `dns`
- `tls`
- `dgram`
- `child_process`
- `worker_threads`
- `vm`
- `fs/promises`
- `node_modules` package resolution
- timers and the Node event loop

## Sandbox Rules

The sandbox contract is owned by gbash, not by `goja_nodejs`.

- `process.env` is copied from `Invocation.Env` per execution
- `console` writes only to gbash-managed stdout and stderr
- `fs` methods are synchronous wrappers over `Invocation.FS`
- cancellation interrupts the JS runtime and Go-backed helpers also check context directly
- script, stdin, and module reads respect `Invocation.Limits.MaxFileBytes`
- monkeypatches and module cache entries do not survive across executions

## Registering The Command

```go
registry := gbash.DefaultRegistry()
if err := nodejs.Register(registry); err != nil {
    return err
}
```

## Adding More Node Modules

Treat every added module as a sandbox design change.

1. Decide whether the module is actually safe to expose.
2. If it touches filesystem, process state, stdio, time, randomness, networking, subprocesses, threads, or host inspection, do not reuse upstream behavior by default.
3. Prefer a gbash-owned module that wraps narrow capabilities exposed through `commands.Invocation`.
4. Register both the bare name and the `node:` alias.
5. Add the name to the builtin allowlist so `require()` does not fall back to unsupported resolution paths.
6. Freeze exported capability objects before user code runs.
7. Add tests for aliasing, sandbox policy enforcement, and escape-resistant behavior.

There are two supported patterns:

- Safe upstream reuse: import the upstream `goja_nodejs` package, allowlist the module name, and rely on the registry-scoped core module.
- gbash-owned override: add a loader in `nodejs.go`, implement the module in `modules.go`, and register it for both `name` and `node:name`.

When in doubt, prefer omission over compatibility. The command is meant to preserve gbash sandbox semantics first and Node compatibility second.
