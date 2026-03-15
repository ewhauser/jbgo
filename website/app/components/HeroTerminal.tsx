"use client";

import { useEffect, useRef, useState } from "react";
import { Bash, defineCommand } from "@/app/lib/gbash-browser";
import { withBasePath } from "@/app/lib/site";
import { LiteTerminal } from "./lite-terminal";

const ASCII_LOGO = [
  "",
  "  \x1b[34m       _               _     \x1b[0m",
  "  \x1b[34m      | |             | |    \x1b[0m",
  "  \x1b[34m  __ _| |__   __ _ ___| |__  \x1b[0m",
  "  \x1b[34m / _` | '_ \\ / _` / __| '_ \\ \x1b[0m",
  "  \x1b[34m| (_| | |_) | (_| \\__ \\ | | |\x1b[0m",
  "  \x1b[34m \\__, |_.__/ \\__,_|___/_| |_|\x1b[0m",
  "  \x1b[34m  __/ |                      \x1b[0m",
  "  \x1b[34m |___/                       \x1b[0m",
  "",
  "  \x1b[2mA deterministic bash runtime for AI agents.\x1b[0m",
  "",
  "  \x1b[34mgo run github.com/ewhauser/gbash/cmd/gbash@latest\x1b[0m",
  "",
  "  \x1b[2mType\x1b[0m \x1b[34mhelp\x1b[0m\x1b[2m or any command to get started.\x1b[0m",
  "",
];

type Terminal = {
  write: (data: string) => void;
  writeln: (data: string) => void;
  clear: () => void;
  onData: (callback: (data: string) => void) => void;
};

function findPrevWordBoundary(str: string, pos: number): number {
  if (pos <= 0) return 0;
  let i = pos - 1;
  while (i > 0 && str[i] === " ") i--;
  while (i > 0 && str[i - 1] !== " ") i--;
  return i;
}

function findNextWordBoundary(str: string, pos: number): number {
  const len = str.length;
  if (pos >= len) return len;
  let i = pos;
  while (i < len && str[i] === " ") i++;
  while (i < len && str[i] !== " ") i++;
  return i;
}

function createInputHandler(term: Terminal, bash: Bash) {
  const HISTORY_KEY = "gbash-website-history";
  const MAX_HISTORY = 100;
  const history: string[] = JSON.parse(
    (typeof sessionStorage !== "undefined"
      ? sessionStorage.getItem(HISTORY_KEY)
      : null) || "[]"
  );
  let cmd = "";
  let cursorPos = 0;
  let historyIndex = history.length;

  const redrawLine = () => {
    term.write("\r\x1b[34m$\x1b[0m " + cmd + "\x1b[K");
    const moveBack = cmd.length - cursorPos;
    if (moveBack > 0) term.write(`\x1b[${moveBack}D`);
  };

  const setCmd = (newCmd: string, newCursorPos?: number) => {
    cmd = newCmd;
    cursorPos = newCursorPos ?? newCmd.length;
    redrawLine();
  };

  const executeCommand = async (command: string) => {
    const trimmed = command.trim();
    if (!trimmed) return;

    history.push(trimmed);
    historyIndex = history.length;
    if (typeof sessionStorage !== "undefined") {
      sessionStorage.setItem(
        HISTORY_KEY,
        JSON.stringify(history.slice(-MAX_HISTORY))
      );
    }

    if (trimmed === "clear") {
      term.write("\x1b[2J\x1b[3J\x1b[H");
    } else {
      const result = await bash.exec(trimmed);
      if (result.stdout) {
        const out = result.stdout.replace(/\n/g, "\r\n");
        term.write(out);
        if (!out.endsWith("\r\n")) term.write("\r\n");
      }
      if (result.stderr) {
        const err = result.stderr.replace(/\n/g, "\r\n");
        term.write(err);
        if (!err.endsWith("\r\n")) term.write("\r\n");
      }
    }

    cmd = "";
    cursorPos = 0;
    term.write("\x1b[34m$\x1b[0m ");
  };

  term.onData(async (e: string) => {
    if (e === "\r") {
      term.writeln("");
      await executeCommand(cmd);
      return;
    }
    if (e === "\x01") { cursorPos = 0; redrawLine(); return; }
    if (e === "\x05") { cursorPos = cmd.length; redrawLine(); return; }
    if (e === "\x15") { cmd = cmd.slice(cursorPos); cursorPos = 0; redrawLine(); return; }
    if (e === "\x0b") { cmd = cmd.slice(0, cursorPos); redrawLine(); return; }
    if (e === "\x17") {
      const newPos = findPrevWordBoundary(cmd, cursorPos);
      cmd = cmd.slice(0, newPos) + cmd.slice(cursorPos);
      cursorPos = newPos;
      redrawLine();
      return;
    }
    if (e === "\x0c") {
      term.write("\x1b[2J\x1b[3J\x1b[H\x1b[34m$\x1b[0m " + cmd + "\x1b[K");
      const moveBack = cmd.length - cursorPos;
      if (moveBack > 0) term.write(`\x1b[${moveBack}D`);
      return;
    }
    if (e === "\x1b\x7f") {
      const newPos = findPrevWordBoundary(cmd, cursorPos);
      cmd = cmd.slice(0, newPos) + cmd.slice(cursorPos);
      cursorPos = newPos;
      redrawLine();
      return;
    }
    if (e === "\x1bd") {
      const newPos = findNextWordBoundary(cmd, cursorPos);
      cmd = cmd.slice(0, cursorPos) + cmd.slice(newPos);
      redrawLine();
      return;
    }
    if (e === "\x1b[A") {
      if (historyIndex > 0) { historyIndex--; setCmd(history[historyIndex]); }
      return;
    }
    if (e === "\x1b[B") {
      if (historyIndex < history.length - 1) {
        historyIndex++;
        setCmd(history[historyIndex]);
      } else if (historyIndex === history.length - 1) {
        historyIndex = history.length;
        setCmd("");
      }
      return;
    }
    if (e === "\x1b[D") { if (cursorPos > 0) { cursorPos--; term.write("\x1b[D"); } return; }
    if (e === "\x1b[C") { if (cursorPos < cmd.length) { cursorPos++; term.write("\x1b[C"); } return; }
    if (e === "\x1b[1;3D" || e === "\x1b[1;5D" || e === "\x1bb") { cursorPos = findPrevWordBoundary(cmd, cursorPos); redrawLine(); return; }
    if (e === "\x1b[1;3C" || e === "\x1b[1;5C" || e === "\x1bf") { cursorPos = findNextWordBoundary(cmd, cursorPos); redrawLine(); return; }
    if (e === "\x7F" || e === "\b") {
      if (cursorPos > 0) {
        cmd = cmd.slice(0, cursorPos - 1) + cmd.slice(cursorPos);
        cursorPos--;
        redrawLine();
      }
      return;
    }
    if (e === "\x1b[3~") {
      if (cursorPos < cmd.length) {
        cmd = cmd.slice(0, cursorPos) + cmd.slice(cursorPos + 1);
        redrawLine();
      }
      return;
    }
    if (e === "\x03") { term.writeln("^C"); cmd = ""; cursorPos = 0; term.write("\x1b[34m$\x1b[0m "); return; }
    if (e >= " " && e <= "~") {
      cmd = cmd.slice(0, cursorPos) + e + cmd.slice(cursorPos);
      cursorPos++;
      redrawLine();
      return;
    }
  });

  return { executeCommand };
}

export default function HeroTerminal() {
  const terminalRef = useRef<HTMLDivElement>(null);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    const container = terminalRef.current;
    if (!container) return;

    const term = new LiteTerminal({
      cursorBlink: true,
      theme: {
        background: "#0c0d0e",
        foreground: "rgba(255, 255, 255, 0.69)",
        cursor: "#70b8ff",
        cyan: "#70b8ff",
        brightCyan: "#a0d0ff",
        brightBlack: "rgba(255, 255, 255, 0.35)",
      },
    });
    term.open(container);

    const helpCmd = defineCommand("help", async () => ({
      stdout: [
        "Available commands:",
        "  help      Show this help message",
        "  about     Learn about gbash",
        "",
        "Plus 60+ built-in commands: ls, cat, grep, sed, find, sort, ...",
        "Type any command to try it out!",
        "",
      ].join("\n"),
      stderr: "",
      exitCode: 0,
    }));

    const aboutCmd = defineCommand("about", async () => ({
      stdout: [
        "gbash - A deterministic bash runtime for AI agents",
        "",
        "  - 60+ built-in commands with GNU coreutils flag parity",
        "  - Virtual in-memory filesystem - no host access",
        "  - Persistent sessions across executions",
        "  - WebAssembly support (you're using it right now!)",
        "  - Execution budgets and policy enforcement",
        "  - Opt-in tracing and logging",
        "",
        "  https://github.com/ewhauser/gbash",
        "",
      ].join("\n"),
      stderr: "",
      exitCode: 0,
    }));

    const bash = new Bash({
      customCommands: [helpCmd, aboutCmd],
      files: {
        "/home/user/README.md":
          "# gbash\n\nA deterministic, sandbox-only, bash-like runtime for AI agents.\n\nSee https://github.com/ewhauser/gbash for more.",
        "/home/user/example.sh":
          '#!/bin/bash\necho "Hello from gbash"\npwd\nls -la\n',
        "/home/user/is-this-really-gbash.txt":
          "Yes! This is a real gbash shell running in your browser via WebAssembly.\n\nThe Go runtime is compiled to WASM using the @ewhauser/gbash-wasm package.\nEvery command you run here executes inside a sandboxed, in-memory\nfilesystem - nothing touches your host machine.\n\nTry it: create files, pipe commands, use grep/sed/awk - it all works.\n\n  cat README.md | head -3\n  echo hello > /tmp/test.txt && cat /tmp/test.txt\n  ls -la\n",
      },
      cwd: "/home/user",
      wasmUrl: withBasePath("/gbash.wasm"),
      wasmExecUrl: withBasePath("/wasm_exec.js"),
    });

    let disposed = false;

    const inputHandler = createInputHandler(term, bash);

    // Show ASCII logo then drop into interactive mode
    requestAnimationFrame(() => {
      if (disposed) return;

      setLoaded(true);

      for (const line of ASCII_LOGO) {
        term.writeln(line);
      }

      term.write("\x1b[34m$\x1b[0m ");
      term.focus();
    });

    return () => {
      disposed = true;
      term.dispose();
    };
  }, []);

  return (
    <div className="crt-terminal">
      <div className="crt-titlebar">
        <div className="crt-dot red" />
        <div className="crt-dot yellow" />
        <div className="crt-dot green" />
        <div className="crt-title">gbash</div>
      </div>
      <div className="crt-body">
        <div ref={terminalRef} />
        <div className="crt-scanlines" />
      </div>
      <div className="crt-led" />
      {!loaded && (
        <div className="absolute inset-0 flex items-center justify-center bg-[#0c0d0e] rounded-xl z-10">
          <div className="text-fg-dim font-mono text-sm animate-pulse">
            Loading WASM runtime...
          </div>
        </div>
      )}
    </div>
  );
}
