"use client";

import benchmarkData from "@/content/performance/benchmark-data.json";

interface Stats {
  min_nanos: number;
  median_nanos: number;
  p95_nanos: number;
}

interface RuntimeResult {
  name: string;
  artifact_size_bytes: number;
  status?: string;
  skip_reason?: string;
  success_count: number;
  failure_count: number;
  stats?: Stats;
}

interface Scenario {
  name: string;
  description: string;
  command: string;
  expected_stdout: string;
  workspace: boolean;
  fixture?: { root: string; file_count: number; total_bytes: number };
  results: RuntimeResult[];
}

interface BenchmarkReport {
  generated_at: string;
  runs: number;
  just_bash_spec: string;
  machine: {
    model: string;
    chip: string;
    cores: string;
    memory: string;
    os: string;
    go_version: string;
  };
  scenarios: Scenario[];
}

const data = benchmarkData as BenchmarkReport;

function formatNanos(nanos: number): string {
  if (nanos >= 1_000_000_000) return `${(nanos / 1_000_000_000).toFixed(2)}s`;
  if (nanos >= 1_000_000) return `${(nanos / 1_000_000).toFixed(1)}ms`;
  if (nanos >= 1_000) return `${(nanos / 1_000).toFixed(1)}µs`;
  return `${nanos}ns`;
}

function formatBytes(bytes: number): string {
  if (bytes >= 1024 * 1024 * 1024)
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(1)} GiB`;
  if (bytes >= 1024 * 1024)
    return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  return `${bytes} B`;
}

function ScenarioTable({ scenario }: { scenario: Scenario }) {
  return (
    <div className="mb-8">
      <h3 className="text-lg font-semibold text-[var(--fg-primary)]">
        {scenario.name.replace(/_/g, " ")}
      </h3>
      <p className="text-sm text-[var(--fg-secondary)] mt-1">
        {scenario.description}
      </p>
      <p className="mt-1 mb-3">
        <code className="text-xs text-[var(--accent)]">
          {scenario.command}
        </code>
        {scenario.fixture && (
          <span className="text-xs text-[var(--fg-secondary)] ml-2">
            ({scenario.fixture.file_count} files,{" "}
            {formatBytes(scenario.fixture.total_bytes)})
          </span>
        )}
      </p>
      <div className="overflow-x-auto">
        <table>
          <thead>
            <tr>
              <th>Runtime</th>
              <th>Min</th>
              <th>Median</th>
              <th>p95</th>
              <th>Artifact Size</th>
            </tr>
          </thead>
          <tbody>
            {scenario.results.map((r) => (
              <tr key={r.name}>
                <td>
                  <div>{r.name}</div>
                  {r.status === "skipped" && (
                    <div className="text-xs text-[var(--fg-secondary)] mt-1">
                      Skipped: {r.skip_reason}
                    </div>
                  )}
                </td>
                <td>{r.stats ? formatNanos(r.stats.min_nanos) : "-"}</td>
                <td>{r.stats ? formatNanos(r.stats.median_nanos) : "-"}</td>
                <td>{r.stats ? formatNanos(r.stats.p95_nanos) : "-"}</td>
                <td>{formatBytes(r.artifact_size_bytes)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function ArtifactSizeTable() {
  const results = data.scenarios[0].results;
  return (
    <div className="mb-8">
      <h3 className="text-lg font-semibold text-[var(--fg-primary)]">
        Artifact Size
      </h3>
      <p className="text-sm text-[var(--fg-secondary)] mt-1 mb-3">
        Total distributable size per runtime.
      </p>
      <div className="overflow-x-auto">
        <table>
          <thead>
            <tr>
              <th>Runtime</th>
              <th>Size</th>
            </tr>
          </thead>
          <tbody>
            {results.map((r) => (
              <tr key={r.name}>
                <td>{r.name}</td>
                <td>{formatBytes(r.artifact_size_bytes)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function MachineInfo() {
  const m = data.machine;
  return (
    <div className="mb-8">
      <h3 className="text-lg font-semibold text-[var(--fg-primary)]">
        Test Environment
      </h3>
      <div className="overflow-x-auto">
        <table>
          <tbody>
            {[
              ["Machine", m.model],
              ["Chip", m.chip],
              ["Cores", m.cores],
              ["Memory", m.memory],
              ["OS", m.os],
              ["Go", m.go_version],
              ["Runs per scenario", `${data.runs}`],
              [
                "Generated",
                new Date(data.generated_at).toLocaleDateString("en-US", {
                  year: "numeric",
                  month: "long",
                  day: "numeric",
                }),
              ],
            ].map(([label, value]) => (
              <tr key={label}>
                <td className="font-medium">{label}</td>
                <td>{value}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

export default function BenchmarkChart() {
  return (
    <div>
      <MachineInfo />
      {data.scenarios.map((scenario) => (
        <ScenarioTable key={scenario.name} scenario={scenario} />
      ))}
      <ArtifactSizeTable />
    </div>
  );
}
