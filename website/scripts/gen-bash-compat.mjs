#!/usr/bin/env node

// Generates bash-compat-data.json from the oils test suite and manifest.json.
// Run from the repo root: node website/scripts/gen-bash-compat.mjs

import { readFileSync, readdirSync, writeFileSync } from "node:fs";
import { join, basename } from "node:path";

const repoRoot = join(import.meta.dirname, "..", "..");
const oilsDir = join(repoRoot, "internal", "conformance", "oils");
const manifestPath = join(repoRoot, "internal", "conformance", "manifest.json");
const outPath = join(
  repoRoot,
  "website",
  "content",
  "compatibility",
  "bash-compat-data.json"
);

const excludedSpecFiles = new Set([
  "errexit-osh.test.sh",
  "posix.test.sh",
  "toysh-posix.test.sh",
  "toysh.test.sh",
  "ysh-builtin-private.test.sh",
  "zsh-idioms.test.sh",
]);

// Count test cases per file by looking for "#### " lines.
const testFiles = readdirSync(oilsDir)
  .filter((f) => f.endsWith(".test.sh") && !excludedSpecFiles.has(f))
  .sort();

const fileCaseCounts = new Map();
for (const file of testFiles) {
  const content = readFileSync(join(oilsDir, file), "utf8");
  const count = content.split("\n").filter((l) => l.startsWith("#### ")).length;
  fileCaseCounts.set(`oils/${file}`, count);
}

// Read manifest.
const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
const entries = manifest.suites.bash.entries;

// Group manifest entries by file.
const fileEntries = new Map();
for (const [key, value] of Object.entries(entries)) {
  const sepIdx = key.indexOf("::");
  if (sepIdx === -1) continue;
  const file = key.slice(0, sepIdx);
  const name = key.slice(sepIdx + 2);
  if (!fileEntries.has(file)) fileEntries.set(file, []);
  fileEntries.get(file).push({
    name,
    mode: value.mode,
    reason: value.reason,
    goos: value.goos ?? null,
  });
}

// Build per-file summaries.
const files = [];
let totalCases = 0;
let totalXfail = 0;
let totalSkip = 0;

for (const [filePath, total] of fileCaseCounts) {
  const entries = fileEntries.get(filePath) ?? [];
  const xfail = entries.filter((e) => e.mode === "xfail").length;
  const skip = entries.filter((e) => e.mode === "skip").length;
  totalCases += total;
  totalXfail += xfail;
  totalSkip += skip;
  files.push({
    name: filePath,
    total,
    xfail,
    skip,
    entries: entries.sort((a, b) => a.name.localeCompare(b.name)),
  });
}

files.sort((a, b) => a.name.localeCompare(b.name));

const data = {
  generated_at: new Date().toISOString(),
  total_cases: totalCases,
  xfail_cases: totalXfail,
  skip_cases: totalSkip,
  passing_cases: totalCases - totalSkip - totalXfail,
  file_count: files.length,
  files,
};

writeFileSync(outPath, JSON.stringify(data, null, 2) + "\n");
console.log(
  `Wrote ${outPath}: ${data.file_count} files, ${data.total_cases} cases, ${data.passing_cases} passing (${((data.passing_cases / (data.total_cases - data.skip_cases)) * 100).toFixed(1)}%)`
);
