import test from "node:test";
import assert from "node:assert/strict";

import { Bash, defineCommand } from "../dist/node.js";

// ---------------------------------------------------------------------------
// Basic command execution
// ---------------------------------------------------------------------------

test("exec runs a simple echo command", async () => {
  const bash = new Bash();
  const result = await bash.exec("echo hello");
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "hello\n");
  assert.equal(result.stderr, "");
  await bash.dispose();
});

test("exec returns non-zero exit code for failing command", async () => {
  const bash = new Bash();
  const result = await bash.exec("false");
  assert.equal(result.exitCode, 1);
  await bash.dispose();
});

test("exec returns exit code 127 for unknown command", async () => {
  const bash = new Bash();
  const result = await bash.exec("nonexistent_command_xyz");
  assert.equal(result.exitCode, 127);
  await bash.dispose();
});

test("exec captures stderr", async () => {
  const bash = new Bash();
  const result = await bash.exec("echo err >&2");
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "");
  assert.equal(result.stderr, "err\n");
  await bash.dispose();
});

// ---------------------------------------------------------------------------
// Session persistence
// ---------------------------------------------------------------------------

test("multiple execs share filesystem state", async () => {
  const bash = new Bash();

  await bash.exec("echo persistent > /tmp/state.txt");
  const result = await bash.exec("cat /tmp/state.txt");
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "persistent\n");

  await bash.dispose();
});

test("environment variables persist across execs", async () => {
  const bash = new Bash();

  await bash.exec("export MY_VAR=hello");
  const result = await bash.exec("echo $MY_VAR");
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "hello\n");

  await bash.dispose();
});

test("working directory persists across execs", async () => {
  const bash = new Bash();

  await bash.exec("mkdir -p /tmp/subdir && cd /tmp/subdir");
  const result = await bash.exec("pwd");
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "/tmp/subdir\n");

  await bash.dispose();
});

// ---------------------------------------------------------------------------
// writeFile / readFile round-trip
// ---------------------------------------------------------------------------

test("writeFile completes before subsequent exec sees the file", async () => {
  const bash = new Bash();

  await bash.writeFile("/home/agent/injected.txt", "injected content\n");
  const result = await bash.exec("cat /home/agent/injected.txt");
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "injected content\n");

  await bash.dispose();
});

// ---------------------------------------------------------------------------
// Custom commands
// ---------------------------------------------------------------------------

test("custom command is dispatched instead of shell execution", async () => {
  const bash = new Bash({
    customCommands: [
      defineCommand("greet", (args) => ({
        stdout: `hello ${args.join(" ")}\n`,
        stderr: "",
        exitCode: 0,
      })),
    ],
  });

  const result = await bash.exec("greet world");
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "hello world\n");

  await bash.dispose();
});

test("async custom command works", async () => {
  const bash = new Bash({
    customCommands: [
      defineCommand("async-cmd", async (args) => {
        await new Promise((r) => setTimeout(r, 5));
        return { stdout: `async:${args[0]}\n`, stderr: "", exitCode: 0 };
      }),
    ],
  });

  const result = await bash.exec("async-cmd value");
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "async:value\n");

  await bash.dispose();
});

test("custom command with shell metacharacters falls through to shell", async () => {
  let called = false;
  const bash = new Bash({
    customCommands: [
      defineCommand("greet", () => {
        called = true;
        return { stdout: "custom\n", stderr: "", exitCode: 0 };
      }),
    ],
  });

  // The pipe makes parseCustomCommand return null, so this goes to the shell
  // rather than the custom command handler. Pipes are not supported in the
  // WASM build so we just verify the custom handler was not called.
  await bash.exec("greet | cat").catch(() => {});
  assert.equal(called, false);

  await bash.dispose();
});

// ---------------------------------------------------------------------------
// Configuration options
// ---------------------------------------------------------------------------

test("cwd option sets initial working directory", async () => {
  const bash = new Bash({ cwd: "/tmp" });
  const result = await bash.exec("pwd");
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "/tmp\n");
  await bash.dispose();
});

test("env option sets environment variables", async () => {
  const bash = new Bash({ env: { GREETING: "hi" } });
  const result = await bash.exec("echo $GREETING");
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "hi\n");
  await bash.dispose();
});

test("cwd as /home/<user> auto-derives HOME and USER", async () => {
  const bash = new Bash({ cwd: "/home/tester" });
  const result = await bash.exec("echo $HOME:$USER");
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "/home/tester:tester\n");
  await bash.dispose();
});

// ---------------------------------------------------------------------------
// Pipelines and shell features
// ---------------------------------------------------------------------------

// Pipes are not implemented in the js/wasm build, so we skip pipeline tests.

test("command substitution works", async () => {
  const bash = new Bash();
  const result = await bash.exec("echo $(echo inner)");
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "inner\n");
  await bash.dispose();
});

test("conditionals work", async () => {
  const bash = new Bash();
  const result = await bash.exec("if true; then echo yes; else echo no; fi");
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "yes\n");
  await bash.dispose();
});

test("loops work", async () => {
  const bash = new Bash();
  const result = await bash.exec("for i in 1 2 3; do echo $i; done");
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "1\n2\n3\n");
  await bash.dispose();
});

// ---------------------------------------------------------------------------
// Initial files
// ---------------------------------------------------------------------------

test("string file content is available on exec", async () => {
  const bash = new Bash({
    files: { "/home/agent/data.txt": "content\n" },
  });
  const result = await bash.exec("cat /home/agent/data.txt");
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "content\n");
  await bash.dispose();
});

test("Uint8Array file content is available on exec", async () => {
  const bash = new Bash({
    files: {
      "/home/agent/bin.txt": new TextEncoder().encode("binary\n"),
    },
  });
  const result = await bash.exec("cat /home/agent/bin.txt");
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "binary\n");
  await bash.dispose();
});

test("lazy file provider is called once on first access", async () => {
  let calls = 0;
  const bash = new Bash({
    files: {
      "/home/agent/lazy.txt": () => {
        calls += 1;
        return "lazy\n";
      },
    },
  });

  await bash.exec("cat /home/agent/lazy.txt");
  await bash.exec("cat /home/agent/lazy.txt");
  assert.equal(calls, 1);

  await bash.dispose();
});

test("async lazy file provider works", async () => {
  const bash = new Bash({
    files: {
      "/home/agent/async.txt": async () => {
        await new Promise((r) => setTimeout(r, 5));
        return "async-lazy\n";
      },
    },
  });

  const result = await bash.exec("cat /home/agent/async.txt");
  assert.equal(result.exitCode, 0);
  assert.equal(result.stdout, "async-lazy\n");

  await bash.dispose();
});
