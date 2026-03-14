# `@ewhauser/gbash-wasm`

Browser-focused WebAssembly packaging for `gbash`.

This package ships:

- `dist/browser.js`: the browser bridge that exposes `Bash` and `defineCommand`
- `dist/gbash.wasm`: the Go `js/wasm` module
- `dist/wasm_exec.js`: Go's matching browser runtime shim
- `dist/wasm_exec_node.js`: Go's matching Node runtime shim for future Node-hosted integrations

The initial API is the same one used by the website example:

- `import { Bash, defineCommand } from "@ewhauser/gbash-wasm/browser"`
- `new Bash({ cwd, env, files, wasmUrl, wasmExecUrl })`
- `defineCommand(name, run)`

The default target is browser JavaScript hosts. This package does not yet expose
a host-neutral WASI/component-model interface.
