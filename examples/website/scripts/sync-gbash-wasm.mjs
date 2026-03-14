import { execFileSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const repoDir = resolve(__dirname, "../../..");
const packageDir = join(repoDir, "packages/gbash-wasm");
const packageDistDir = join(packageDir, "dist");
const targetDir = process.argv[2] ? resolve(process.argv[2]) : resolve(__dirname, "..");
const publicDir = join(targetDir, "public");
const localPnpm = join(
  repoDir,
  "node_modules",
  ".bin",
  process.platform === "win32" ? "pnpm.cmd" : "pnpm",
);

if (existsSync(localPnpm)) {
  execFileSync(localPnpm, ["--dir", repoDir, "--filter", "@ewhauser/gbash-wasm", "build"], {
    cwd: repoDir,
    stdio: "inherit",
  });
} else {
  execFileSync(
    "npm",
    ["exec", "--yes", "pnpm@10.10.0", "--", "--dir", repoDir, "--filter", "@ewhauser/gbash-wasm", "build"],
    {
      cwd: repoDir,
      stdio: "inherit",
    },
  );
}

mkdirSync(publicDir, { recursive: true });
cpSync(join(packageDistDir, "gbash.wasm"), join(publicDir, "gbash.wasm"));
cpSync(join(packageDistDir, "wasm_exec.js"), join(publicDir, "wasm_exec.js"));

console.log(`wrote ${join(publicDir, "gbash.wasm")}`);
console.log(`wrote ${join(publicDir, "wasm_exec.js")}`);
