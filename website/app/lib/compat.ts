import { readFile } from "node:fs/promises";
import path from "node:path";

export interface CompatCoverageBucket {
  selected_total: number;
  filtered_total?: number;
  pass: number;
  fail: number;
  skip: number;
  xfail: number;
  xpass: number;
  error: number;
  unreported: number;
  runnable_total: number;
  pass_pct_selected: number;
  pass_pct_runnable: number;
}

export interface CompatSuiteTest {
  path: string;
  category: string;
  status: string;
  filtered?: boolean;
  filter_reason?: string;
}

export interface CompatCategoryResult {
  name: string;
  summary: CompatCoverageBucket;
  tests?: CompatSuiteTest[];
}

export interface CompatSummary {
  gnu_version: string;
  generated_at: string;
  suite: CompatCoverageBucket & {
    tests: CompatSuiteTest[];
  };
  categories: CompatCategoryResult[];
}

export interface CompatProgressSegment {
  className: string;
  width: string;
}

export async function loadCompatSummary(): Promise<CompatSummary | null> {
  const summaryPath = process.env.GBASH_WEBSITE_COMPAT_SUMMARY_PATH?.trim();
  if (!summaryPath) {
    return null;
  }

  const resolvedPath = path.resolve(summaryPath);
  let raw: string;
  try {
    raw = await readFile(resolvedPath, "utf8");
  } catch (error) {
    throw new Error(
      `failed to read compatibility summary at ${resolvedPath}: ${String(error)}`
    );
  }

  try {
    return JSON.parse(raw) as CompatSummary;
  } catch (error) {
    throw new Error(
      `failed to parse compatibility summary at ${resolvedPath}: ${String(error)}`
    );
  }
}

export function suiteInScopeTotal(summary: CompatSummary): number {
  return Math.max(0, summary.suite.selected_total - compatFilteredTotal(summary.suite));
}

export function suiteInScopePassPct(summary: CompatSummary): number {
  const total = suiteInScopeTotal(summary);
  if (total === 0) {
    return 0;
  }
  return Math.round((summary.suite.pass / total) * 10000) / 100;
}

export function formatPercent(value: number): string {
  if (value === 0) {
    return "0%";
  }
  if (Number.isInteger(value)) {
    return `${value}%`;
  }
  return `${value.toFixed(2).replace(/\.?0+$/, "")}%`;
}

export function bucketCounts(bucket: CompatCoverageBucket): string {
  return `${bucket.pass} / ${bucket.skip + bucket.xfail} / ${bucket.fail + bucket.error + bucket.xpass + bucket.unreported}`;
}

export function bucketRowNote(bucket: CompatCoverageBucket): string {
  const filteredTotal = compatFilteredTotal(bucket);
  if (filteredTotal === 0) {
    return `${bucket.selected_total} tests`;
  }
  return `${bucket.selected_total} tests, ${filteredTotal} out of scope`;
}

export function bucketSegments(
  bucket: CompatCoverageBucket
): CompatProgressSegment[] {
  if (bucket.selected_total === 0) {
    return [];
  }

  const segments = [
    { className: "bg-emerald-400", count: bucket.pass },
    { className: "bg-slate-500/50", count: compatFilteredTotal(bucket) },
    { className: "bg-amber-300", count: bucket.skip + bucket.xfail },
    {
      className: "bg-rose-400",
      count: bucket.fail + bucket.error + bucket.xpass + bucket.unreported,
    },
  ];

  return segments
    .filter((segment) => segment.count > 0)
    .map((segment) => ({
      className: segment.className,
      width: `${((segment.count / bucket.selected_total) * 100).toFixed(4)}%`,
    }));
}

export function testStatusTone(
  status: string,
  filtered: boolean | undefined
): string {
  if (filtered) {
    return "border-slate-500/40 bg-slate-500/15 text-slate-200";
  }
  switch (status) {
    case "pass":
      return "border-emerald-400/30 bg-emerald-400/10 text-emerald-300";
    case "skip":
    case "xfail":
    case "filtered":
      return "border-amber-300/30 bg-amber-300/10 text-amber-200";
    case "fail":
    case "error":
    case "xpass":
    case "unreported":
      return "border-rose-400/30 bg-rose-400/10 text-rose-200";
    default:
      return "border-slate-500/40 bg-slate-500/15 text-slate-200";
  }
}

function compatFilteredTotal(bucket: CompatCoverageBucket): number {
  return bucket.filtered_total ?? 0;
}
