#!/usr/bin/env node

import {
  copyFileSync,
  existsSync,
  lstatSync,
  mkdirSync,
  readdirSync,
  readlinkSync,
  rmSync,
  symlinkSync,
} from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const websiteRoot = resolve(__dirname, "..");
const repoRoot = resolve(__dirname, "../../..");
const agentDataDir = resolve(websiteRoot, "app/api/agent/_agent-data");
const gbashDataDir = join(agentDataDir, "gbash");

const includedPaths = [
  "README.md",
  "AGENTS.md",
  "go.mod",
  "go.sum",
  "api.go",
  "options.go",
  "network",
  "policy",
  "fs",
  "internal/runtime",
  "shell",
  "commands",
  "cmd/gbash",
  "packages/gbash-wasm",
  "examples/website",
];

function shouldCopy(src) {
  const rel = relative(repoRoot, src);
  if (!rel || rel === "") return true;

  const normalized = rel.replaceAll("\\", "/");
  const blockedFragments = [
    "/.git/",
    "/node_modules/",
    "/dist/",
    "/.next/",
    "/app/api/agent/_agent-data/",
  ];
  if (blockedFragments.some((fragment) => normalized.includes(fragment))) {
    return false;
  }
  if (
    normalized.endsWith("/public/gbash.wasm") ||
    normalized.endsWith("/public/wasm_exec.js")
  ) {
    return false;
  }
  return true;
}

function copyTree(src, dst) {
  if (!shouldCopy(src)) {
    return;
  }

  const stat = lstatSync(src);
  if (stat.isSymbolicLink()) {
    mkdirSync(dirname(dst), { recursive: true });
    symlinkSync(readlinkSync(src), dst);
    return;
  }

  if (stat.isDirectory()) {
    mkdirSync(dst, { recursive: true });
    for (const entry of readdirSync(src, { withFileTypes: true })) {
      copyTree(join(src, entry.name), join(dst, entry.name));
    }
    return;
  }

  mkdirSync(dirname(dst), { recursive: true });
  copyFileSync(src, dst);
}

if (existsSync(agentDataDir)) {
  rmSync(agentDataDir, { recursive: true, force: true });
}
mkdirSync(gbashDataDir, { recursive: true });

for (const relPath of includedPaths) {
  const src = resolve(repoRoot, relPath);
  const dst = resolve(gbashDataDir, relPath);
  console.log(`Copying ${relPath}`);
  copyTree(src, dst);
}

console.log(`Local agent data written to ${agentDataDir}`);
