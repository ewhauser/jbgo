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
  FileContent,
  InitialFile,
  InitialFiles,
  LazyFileProvider,
} from "./shared.js";

declare global {
  interface Window {
    GBashWasm?: GBashRuntime;
    Go?: new () => {
      importObject: WebAssembly.Imports;
      run(instance: WebAssembly.Instance): void;
    };
  }
}

let runtimePromise: Promise<GBashRuntime> | null = null;

export function defaultBrowserAssets(): { wasmUrl: string; wasmExecUrl: string } {
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

  async writeFile(path: string, content: string): Promise<void> {
    const shell = await this.shellPromise;
    shell.writeFile(path, content);
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
  if (window.GBashWasm?.createShell) {
    return window.GBashWasm;
  }
  if (!runtimePromise) {
    runtimePromise = (async () => {
      const assets = defaultBrowserAssets();
      if (!window.Go) {
        await loadScript(options.wasmExecUrl ?? assets.wasmExecUrl);
      }
      if (!window.Go) {
        throw new Error("wasm_exec.js did not define the Go runtime");
      }

      const go = new window.Go();
      const wasmUrl = options.wasmUrl ?? assets.wasmUrl;
      const result = await instantiateWasm(wasmUrl, go.importObject);
      go.run(result.instance);
      return waitForRuntime();
    })().catch((err) => {
      runtimePromise = null;
      throw err;
    });
  }
  return runtimePromise;
}

async function instantiateWasm(
  wasmUrl: string,
  importObject: WebAssembly.Imports,
): Promise<WebAssembly.WebAssemblyInstantiatedSource> {
  if ("instantiateStreaming" in WebAssembly) {
    try {
      return await WebAssembly.instantiateStreaming(fetch(wasmUrl), importObject);
    } catch {
      // Fall back when the host serves wasm with an unexpected MIME type.
    }
  }
  const response = await fetch(wasmUrl);
  const bytes = await response.arrayBuffer();
  return WebAssembly.instantiate(bytes, importObject);
}

function loadScript(src: string): Promise<void> {
  if (window.Go) {
    return Promise.resolve();
  }
  return new Promise((resolve, reject) => {
    const existing = document.querySelector<HTMLScriptElement>(`script[src="${src}"]`);
    if (existing) {
      existing.addEventListener("load", () => resolve(), { once: true });
      existing.addEventListener("error", () => reject(new Error(`failed to load ${src}`)), {
        once: true,
      });
      if ((existing as HTMLScriptElement).dataset.loaded === "true") {
        resolve();
      }
      return;
    }

    const script = document.createElement("script");
    script.src = src;
    script.async = true;
    script.addEventListener("load", () => {
      script.dataset.loaded = "true";
      resolve();
    }, { once: true });
    script.addEventListener("error", () => reject(new Error(`failed to load ${src}`)), {
      once: true,
    });
    document.head.appendChild(script);
  });
}

async function waitForRuntime(): Promise<GBashRuntime> {
  const deadline = Date.now() + 5_000;
  while (Date.now() < deadline) {
    if (window.GBashWasm?.createShell) {
      return window.GBashWasm;
    }
    await new Promise((resolve) => window.setTimeout(resolve, 10));
  }
  throw new Error("gbash.wasm did not register window.GBashWasm");
}
