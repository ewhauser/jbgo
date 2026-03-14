import * as nodeFS from "node:fs";
import { readFile } from "node:fs/promises";
import { createRequire } from "node:module";
import * as nodePath from "node:path";
import { performance } from "node:perf_hooks";
import { TextDecoder, TextEncoder } from "node:util";
import { fileURLToPath, pathToFileURL } from "node:url";
import { webcrypto } from "node:crypto";

import {
  defineCommand,
  deriveEnv,
  parseCustomCommand,
  type BashOptions,
  type BridgeShell,
  type CommandHandler,
  type CommandResult,
  type GBashRuntime,
} from "./shared.js";

export { defineCommand };
export type {
  BashOptions,
  BridgeShell,
  CommandHandler,
  CommandResult,
  CustomCommand,
  GBashRuntime,
} from "./shared.js";

type NodeGlobal = typeof globalThis & {
  GBashWasm?: GBashRuntime;
  Go?: new () => {
    importObject: WebAssembly.Imports;
    run(instance: WebAssembly.Instance): Promise<void>;
  };
  fs?: typeof nodeFS;
  path?: typeof nodePath;
  require?: NodeJS.Require;
  performance?: typeof globalThis.performance;
  crypto?: typeof globalThis.crypto;
  TextEncoder?: typeof globalThis.TextEncoder;
  TextDecoder?: typeof globalThis.TextDecoder;
};

const runtimeGlobal = globalThis as NodeGlobal;
let runtimePromise: Promise<GBashRuntime> | null = null;

export function defaultNodeAssets(): { wasmUrl: string; wasmExecUrl: string } {
  return {
    wasmUrl: new URL("./gbash.wasm", import.meta.url).toString(),
    wasmExecUrl: new URL("./wasm_exec.js", import.meta.url).toString(),
  };
}

export class Bash {
  private readonly customCommands = new Map<string, CommandHandler>();
  private readonly shellPromise: Promise<BridgeShell>;

  constructor(options: BashOptions = {}) {
    for (const command of options.customCommands ?? []) {
      this.customCommands.set(command.name, command.run);
    }
    this.shellPromise = this.init(options);
  }

  writeFile(path: string, content: string): void {
    void this.shellPromise.then((shell) => {
      shell.writeFile(path, content);
    });
  }

  async exec(command: string): Promise<CommandResult> {
    const customCall = parseCustomCommand(command, this.customCommands);
    if (customCall) {
      return customCall.handler(customCall.args);
    }
    const shell = await this.shellPromise;
    return shell.exec(command);
  }

  async dispose(): Promise<void> {
    const shell = await this.shellPromise;
    shell.dispose();
  }

  private async init(options: BashOptions): Promise<BridgeShell> {
    const runtime = await loadRuntime(options);
    return runtime.createShell({
      cwd: options.cwd,
      env: deriveEnv(options.cwd, options.env),
      files: options.files,
    });
  }
}

async function loadRuntime(options: BashOptions): Promise<GBashRuntime> {
  if (runtimeGlobal.GBashWasm?.createShell) {
    return runtimeGlobal.GBashWasm;
  }
  if (!runtimePromise) {
    runtimePromise = (async () => {
      const assets = defaultNodeAssets();
      ensureNodeGlobals();
      if (!runtimeGlobal.Go) {
        await import(resolveImportSpecifier(options.wasmExecUrl ?? assets.wasmExecUrl));
      }
      if (!runtimeGlobal.Go) {
        throw new Error("wasm_exec.js did not define the Go runtime");
      }

      const go = new runtimeGlobal.Go();
      const wasmUrl = options.wasmUrl ?? assets.wasmUrl;
      const result = await instantiateWasm(wasmUrl, go.importObject);
      void go.run(result.instance);
      return waitForRuntime();
    })();
  }
  return runtimePromise;
}

function ensureNodeGlobals(): void {
  runtimeGlobal.require ??= createRequire(import.meta.url);
  runtimeGlobal.fs ??= nodeFS;
  runtimeGlobal.path ??= nodePath;
  runtimeGlobal.performance ??= performance as unknown as typeof globalThis.performance;
  runtimeGlobal.crypto ??= webcrypto as typeof globalThis.crypto;
  runtimeGlobal.TextEncoder ??= TextEncoder as typeof globalThis.TextEncoder;
  runtimeGlobal.TextDecoder ??= TextDecoder as typeof globalThis.TextDecoder;
}

async function instantiateWasm(
  wasmUrl: string,
  importObject: WebAssembly.Imports,
): Promise<WebAssembly.WebAssemblyInstantiatedSource> {
  const bytes = await loadWasmBytes(wasmUrl);
  return WebAssembly.instantiate(bytes as BufferSource, importObject);
}

async function loadWasmBytes(wasmUrl: string): Promise<ArrayBuffer | Uint8Array> {
  const parsed = parseURL(wasmUrl);
  if (!parsed) {
    return readFile(nodePath.resolve(wasmUrl));
  }
  if (parsed.protocol === "file:") {
    return readFile(fileURLToPath(parsed));
  }
  const response = await fetch(parsed);
  if (!response.ok) {
    throw new Error(`failed to load ${wasmUrl}: ${response.status} ${response.statusText}`);
  }
  return new Uint8Array(await response.arrayBuffer());
}

async function waitForRuntime(): Promise<GBashRuntime> {
  const deadline = Date.now() + 5_000;
  while (Date.now() < deadline) {
    if (runtimeGlobal.GBashWasm?.createShell) {
      return runtimeGlobal.GBashWasm;
    }
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error("gbash.wasm did not register globalThis.GBashWasm");
}

function resolveImportSpecifier(specifier: string): string {
  const parsed = parseURL(specifier);
  if (parsed) {
    if (parsed.protocol === "file:" || parsed.protocol === "data:" || parsed.protocol === "node:") {
      return parsed.href;
    }
    throw new Error(`unsupported wasmExecUrl protocol for node entrypoint: ${parsed.protocol}`);
  }
  return pathToFileURL(nodePath.resolve(specifier)).href;
}

function parseURL(value: string): URL | null {
  try {
    return new URL(value);
  } catch {
    return null;
  }
}
