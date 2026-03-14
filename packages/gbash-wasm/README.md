# `@ewhauser/gbash-wasm`

JavaScript-hosted WebAssembly packaging for `gbash`.

This package ships:

- `dist/browser.js`: the browser bridge that exposes `Bash` and `defineCommand`
- `dist/node.js`: the Node bridge that exposes `Bash` and `defineCommand`
- `dist/gbash.wasm`: the Go `js/wasm` module
- `dist/wasm_exec.js`: Go's matching browser runtime shim
- `dist/wasm_exec_node.js`: Go's raw Node runtime shim for low-level consumers

The initial API is the same one used by the website example:

- `import { Bash, defineCommand } from "@ewhauser/gbash-wasm/browser"`
- `import { Bash, defineCommand } from "@ewhauser/gbash-wasm/node"`
- `new Bash({ cwd, env, files, wasmUrl, wasmExecUrl })`
- `defineCommand(name, run)`

The package currently exposes explicit browser and Node entrypoints for JavaScript
hosts. It does not yet expose a host-neutral WASI/component-model interface.
