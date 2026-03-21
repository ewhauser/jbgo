#!/usr/bin/env node

// Generates posix-compat-data.json from posix-matrix.yaml.
// Run from the repo root: node website/scripts/gen-posix-compat.mjs

import { readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const repoRoot = join(import.meta.dirname, "..", "..");
const matrixPath = join(
  repoRoot,
  "website",
  "content",
  "compatibility",
  "posix-matrix.yaml"
);
const outPath = join(
  repoRoot,
  "website",
  "content",
  "compatibility",
  "posix-compat-data.json"
);

// Minimal YAML parser: handles the subset used by posix-matrix.yaml.
// Avoids adding a runtime dependency on a YAML library.
function parseMatrix(text) {
  const categories = [];
  let currentCategory = null;
  let currentFeature = null;
  let lastKey = null;
  let inSummary = false;
  let summaryLines = [];

  for (const rawLine of text.split("\n")) {
    const line = rawLine;

    // Skip blank lines and comments at the top level
    if (/^\s*#/.test(line) || /^\s*$/.test(line)) {
      if (inSummary && /^\s*$/.test(line)) {
        // end of summary block
        if (currentFeature) {
          currentFeature.summary = summaryLines.join(" ").trim();
        }
        inSummary = false;
        summaryLines = [];
      }
      continue;
    }

    // Top-level scalars (version, spec_edition, generated)
    if (/^(version|spec_edition|generated):/.test(line)) continue;

    // categories: array start
    if (/^categories:/.test(line)) continue;

    // Category-level fields (indented with 4 spaces under "- id:")
    const catMatch = line.match(/^  - id:\s*(.+)/);
    if (catMatch) {
      if (currentFeature && inSummary) {
        currentFeature.summary = summaryLines.join(" ").trim();
        inSummary = false;
        summaryLines = [];
      }
      currentCategory = {
        id: catMatch[1].trim(),
        name: "",
        posix_sections: [],
        features: [],
      };
      categories.push(currentCategory);
      currentFeature = null;
      lastKey = "id";
      continue;
    }

    if (currentCategory && !currentFeature) {
      const nameMatch = line.match(/^\s{4}name:\s*(.+)/);
      if (nameMatch) {
        currentCategory.name = nameMatch[1].trim().replace(/^"(.*)"$/, "$1");
        lastKey = "name";
        continue;
      }
      const secMatch = line.match(/^\s{4}posix_sections:\s*\[(.+)\]/);
      if (secMatch) {
        currentCategory.posix_sections = secMatch[1]
          .split(",")
          .map((s) => s.trim().replace(/"/g, ""));
        continue;
      }
      if (/^\s{4}features:/.test(line)) {
        continue;
      }
    }

    // Feature-level fields (indented with 8 spaces under "- id:")
    const featMatch = line.match(/^\s{6}- id:\s*(.+)/);
    if (featMatch) {
      if (currentFeature && inSummary) {
        currentFeature.summary = summaryLines.join(" ").trim();
        inSummary = false;
        summaryLines = [];
      }
      currentFeature = {
        id: featMatch[1].trim(),
        name: "",
        posix_section: "",
        classification: "",
        gbash_status: "",
        summary: "",
        test_priority: "",
        notes: "",
      };
      if (currentCategory) {
        currentCategory.features.push(currentFeature);
      }
      lastKey = "id";
      continue;
    }

    if (currentFeature) {
      // Handle multi-line summary continuation
      if (inSummary) {
        const trimmed = line.trim();
        if (/^\w+:/.test(trimmed) && !trimmed.startsWith("http")) {
          // New key, end summary
          currentFeature.summary = summaryLines.join(" ").trim();
          inSummary = false;
          summaryLines = [];
          // fall through to key parsing below
        } else {
          summaryLines.push(trimmed);
          continue;
        }
      }

      const kvMatch = line.match(/^\s{8}(\w[\w_]*):\s*(.*)/);
      if (kvMatch) {
        const [, key, rawVal] = kvMatch;
        let val = rawVal.trim().replace(/^"(.*)"$/, "$1");

        if (key === "posix_section") {
          // Handle array-like values: "2.7.5", "2.7.6"
          val = val.replace(/"/g, "").trim();
        }

        if (key === "summary" && val === ">") {
          inSummary = true;
          summaryLines = [];
          lastKey = key;
          continue;
        }

        if (val === '""' || val === "''") val = "";

        currentFeature[key] = val;
        lastKey = key;
      }
    }
  }

  // Flush last summary
  if (currentFeature && inSummary) {
    currentFeature.summary = summaryLines.join(" ").trim();
  }

  return categories;
}

const yamlText = readFileSync(matrixPath, "utf8");
const categories = parseMatrix(yamlText);

// Compute statistics
let totalFeatures = 0;
let totalPass = 0;
let totalPartial = 0;
let totalFail = 0;
let totalNotApplicable = 0;
let totalNotTested = 0;
let totalOutOfScope = 0;

const categorySummaries = categories.map((cat) => {
  const features = cat.features;
  const pass = features.filter((f) => f.gbash_status === "pass").length;
  const partial = features.filter((f) => f.gbash_status === "partial").length;
  const fail = features.filter((f) => f.gbash_status === "fail").length;
  const notApplicable = features.filter(
    (f) => f.gbash_status === "not_applicable"
  ).length;
  const notTested = features.filter(
    (f) => f.gbash_status === "not_tested"
  ).length;
  const outOfScope = features.filter(
    (f) => f.classification === "out_of_scope"
  ).length;
  const extension = features.filter(
    (f) => f.classification === "extension"
  ).length;

  totalFeatures += features.length;
  totalPass += pass;
  totalPartial += partial;
  totalFail += fail;
  totalNotApplicable += notApplicable;
  totalNotTested += notTested;
  totalOutOfScope += outOfScope;

  return {
    id: cat.id,
    name: cat.name,
    posix_sections: cat.posix_sections,
    total: features.length,
    pass,
    partial,
    fail,
    not_applicable: notApplicable,
    not_tested: notTested,
    out_of_scope: outOfScope,
    extension,
    features: features.map((f) => ({
      id: f.id,
      name: f.name,
      posix_section: f.posix_section,
      classification: f.classification,
      gbash_status: f.gbash_status,
      summary: f.summary,
      test_priority: f.test_priority,
      notes: f.notes,
    })),
  };
});

const inScope = totalFeatures - totalNotApplicable - totalOutOfScope;
const passPct = inScope > 0 ? Math.round((totalPass / inScope) * 10000) / 100 : 0;

const data = {
  generated_at: new Date().toISOString(),
  spec_edition: "IEEE Std 1003.1-2024, XCU Chapter 2",
  total_features: totalFeatures,
  pass: totalPass,
  partial: totalPartial,
  fail: totalFail,
  not_applicable: totalNotApplicable,
  not_tested: totalNotTested,
  out_of_scope: totalOutOfScope,
  in_scope: inScope,
  pass_pct: passPct,
  categories: categorySummaries,
};

writeFileSync(outPath, JSON.stringify(data, null, 2) + "\n");
console.log(
  `Wrote ${outPath}: ${data.total_features} features across ${data.categories.length} categories, ${data.pass} passing of ${data.in_scope} in-scope (${passPct}%)`
);
