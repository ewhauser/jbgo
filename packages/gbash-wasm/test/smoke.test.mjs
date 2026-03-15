import test from "node:test";
import assert from "node:assert/strict";

import { Bash } from "../dist/node.js";

test("Bash files supports eager, lazy, and metadata-backed entries", async () => {
  let syncCalls = 0;
  let asyncCalls = 0;

  const bash = new Bash({
    files: {
      "/home/agent/string.txt": "string\n",
      "/home/agent/bytes.txt": new TextEncoder().encode("bytes\n"),
      "/home/agent/lazy-sync.txt": () => {
        syncCalls += 1;
        return "lazy-sync\n";
      },
      "/home/agent/lazy-async.txt": async () => {
        asyncCalls += 1;
        return new TextEncoder().encode("lazy-async\n");
      },
      "/home/agent/meta.txt": {
        content: "meta\n",
        mode: 0o640,
        mtime: new Date("2024-01-02T03:04:05Z"),
      },
    },
  });

  await assert.doesNotReject(async () => {
    const result = await bash.exec(
      "cat /home/agent/string.txt /home/agent/bytes.txt /home/agent/lazy-sync.txt /home/agent/lazy-async.txt /home/agent/meta.txt",
    );
    assert.equal(result.exitCode, 0);
    assert.equal(
      result.stdout,
      "string\nbytes\nlazy-sync\nlazy-async\nmeta\n",
    );
  });

  assert.equal(syncCalls, 1);
  assert.equal(asyncCalls, 1);

  const statResult = await bash.exec("stat -c '%a %Y' /home/agent/meta.txt");
  assert.equal(statResult.exitCode, 0);
  assert.equal(statResult.stdout.trim(), "0640 1704164645");

  await bash.dispose();
});
