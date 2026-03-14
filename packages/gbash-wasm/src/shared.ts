export type CommandResult = {
  stdout: string;
  stderr: string;
  exitCode: number;
  shellExited?: boolean;
};

export type CommandHandler = (args: string[]) => CommandResult | Promise<CommandResult>;

export type BridgeShell = {
  exec(command: string): Promise<CommandResult>;
  writeFile(path: string, content: string): void;
  readFile(path: string): string;
  dispose(): void;
};

export type GBashRuntime = {
  createShell(options?: {
    cwd?: string;
    env?: Record<string, string>;
    files?: Record<string, string>;
  }): Promise<BridgeShell>;
};

export type CustomCommand = {
  name: string;
  run: CommandHandler;
};

export type BashOptions = {
  cwd?: string;
  env?: Record<string, string>;
  files?: Record<string, string>;
  customCommands?: CustomCommand[];
  wasmUrl?: string;
  wasmExecUrl?: string;
};

export function defineCommand(name: string, run: CommandHandler): CustomCommand {
  return { name, run };
}

export function deriveEnv(
  cwd: string | undefined,
  provided: Record<string, string> | undefined,
): Record<string, string> | undefined {
  const env = { ...(provided ?? {}) };
  if (!cwd) {
    return Object.keys(env).length === 0 ? undefined : env;
  }
  if (!env.HOME && /^\/home\/[^/]+$/.test(cwd)) {
    env.HOME = cwd;
  }
  if (env.HOME && /^\/home\/[^/]+$/.test(env.HOME)) {
    const user = env.HOME.slice("/home/".length);
    if (!env.USER) env.USER = user;
    if (!env.LOGNAME) env.LOGNAME = user;
    if (!env.GROUP) env.GROUP = user;
  }
  return Object.keys(env).length === 0 ? undefined : env;
}

export function parseCustomCommand(
  command: string,
  customCommands: Map<string, CommandHandler>,
): { handler: CommandHandler; args: string[] } | null {
  const tokens = tokenizeSimpleCommand(command);
  if (!tokens || tokens.length === 0) {
    return null;
  }
  const handler = customCommands.get(tokens[0]);
  if (!handler) {
    return null;
  }
  return { handler, args: tokens.slice(1) };
}

function tokenizeSimpleCommand(command: string): string[] | null {
  const tokens: string[] = [];
  let current = "";
  let quote: "'" | "\"" | null = null;
  let escaped = false;

  for (let i = 0; i < command.length; i++) {
    const ch = command[i];

    if (escaped) {
      current += ch;
      escaped = false;
      continue;
    }

    if (ch === "\\") {
      escaped = true;
      continue;
    }

    if (quote) {
      if (ch === quote) {
        quote = null;
      } else {
        current += ch;
      }
      continue;
    }

    if (ch === "'" || ch === "\"") {
      quote = ch;
      continue;
    }

    if (isShellControl(ch)) {
      return null;
    }

    if (/\s/.test(ch)) {
      if (current) {
        tokens.push(current);
        current = "";
      }
      continue;
    }

    current += ch;
  }

  if (escaped || quote) {
    return null;
  }
  if (current) {
    tokens.push(current);
  }
  return tokens;
}

function isShellControl(ch: string): boolean {
  return ch === "|" || ch === "&" || ch === ";" || ch === "<" || ch === ">" || ch === "(" || ch === ")";
}
