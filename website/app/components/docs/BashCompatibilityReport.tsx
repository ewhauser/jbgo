import data from "@/content/compatibility/bash-compat-data.json";

interface BashEntry {
  name: string;
  mode: string;
  reason: string;
  goos: string[] | null;
}

interface BashFile {
  name: string;
  total: number;
  xfail: number;
  skip: number;
  entries: BashEntry[];
}

interface BashCompatData {
  generated_at: string;
  total_cases: number;
  xfail_cases: number;
  skip_cases: number;
  passing_cases: number;
  file_count: number;
  files: BashFile[];
}

const compat = data as BashCompatData;

function formatGeneratedAt(value: string): string {
  return new Intl.DateTimeFormat("en-US", {
    dateStyle: "long",
    timeStyle: "short",
    timeZone: "UTC",
  }).format(new Date(value));
}

function formatPercent(value: number): string {
  if (value === 0) return "0%";
  if (Number.isInteger(value)) return `${value}%`;
  return `${value.toFixed(2).replace(/\.?0+$/, "")}%`;
}

function modeTone(mode: string): string {
  switch (mode) {
    case "xfail":
      return "border-amber-300/30 bg-amber-300/10 text-amber-200";
    case "skip":
      return "border-slate-500/40 bg-slate-500/15 text-slate-200";
    default:
      return "border-slate-500/40 bg-slate-500/15 text-slate-200";
  }
}

function fileProgressSegments(file: BashFile) {
  const inScope = file.total - file.skip;
  if (inScope === 0) return [];
  const passing = inScope - file.xfail;
  const segments = [
    { className: "bg-emerald-400", count: passing },
    { className: "bg-amber-300", count: file.xfail },
    { className: "bg-slate-500/50", count: file.skip },
  ];
  return segments
    .filter((s) => s.count > 0)
    .map((s) => ({
      className: s.className,
      width: `${((s.count / file.total) * 100).toFixed(4)}%`,
    }));
}

function fileCounts(file: BashFile): string {
  const passing = file.total - file.skip - file.xfail;
  return `${passing} / ${file.xfail} / ${file.skip}`;
}

function fileNote(file: BashFile): string {
  if (file.skip === 0) return `${file.total} tests`;
  return `${file.total} tests, ${file.skip} skipped`;
}

export default function BashCompatibilityReport() {
  const inScope = compat.total_cases - compat.skip_cases;
  const passPct =
    inScope > 0
      ? Math.round((compat.passing_cases / inScope) * 10000) / 100
      : 0;

  return (
    <div className="mt-8 space-y-8">
      <section className="space-y-3">
        <p className="text-sm leading-6 text-[var(--fg-secondary)]">
          <code>gbash</code> is evaluated against the{" "}
          <a href="https://github.com/oils-for-unix/oils">OILS</a> spec test
          corpus, comparing output against a pinned version of bash. Tests are
          grouped by upstream spec file.
        </p>
        <p className="text-sm leading-6 text-[var(--fg-secondary)]">
          Generated{" "}
          <strong>{formatGeneratedAt(compat.generated_at)}</strong>.
        </p>
      </section>

      <section className="grid gap-4 md:grid-cols-3">
        <article className="rounded-2xl border border-fg-dim/20 bg-bg-secondary/40 p-5">
          <p className="text-xs font-medium uppercase tracking-[0.18em] text-[var(--fg-dim)]">
            Pass Rate
          </p>
          <p className="mt-3 text-3xl font-semibold text-[var(--fg-primary)]">
            {formatPercent(passPct)}
          </p>
          <p className="mt-3 text-sm text-[var(--fg-secondary)]">
            {compat.passing_cases} passing of {inScope} in-scope tests
          </p>
        </article>
        <article className="rounded-2xl border border-fg-dim/20 bg-bg-secondary/40 p-5">
          <p className="text-xs font-medium uppercase tracking-[0.18em] text-[var(--fg-dim)]">
            Known Failures
          </p>
          <p className="mt-3 text-3xl font-semibold text-[var(--fg-primary)]">
            {compat.xfail_cases}
          </p>
          <p className="mt-3 text-sm text-[var(--fg-secondary)]">
            expected failures across {compat.file_count} spec files
          </p>
        </article>
        <article className="rounded-2xl border border-fg-dim/20 bg-bg-secondary/40 p-5">
          <p className="text-xs font-medium uppercase tracking-[0.18em] text-[var(--fg-dim)]">
            Spec Files
          </p>
          <p className="mt-3 text-3xl font-semibold text-[var(--fg-primary)]">
            {compat.file_count}
          </p>
          <p className="mt-3 text-sm text-[var(--fg-secondary)]">
            {compat.total_cases} total test cases, {compat.skip_cases} skipped
          </p>
        </article>
      </section>

      <section className="space-y-4">
        <div>
          <h2 className="text-2xl font-semibold text-[var(--fg-primary)]">
            Coverage Per Spec File
          </h2>
          <p className="mt-2 text-sm leading-6 text-[var(--fg-secondary)]">
            Expand files with known failures to inspect individual test status.
            Green indicates passing, amber indicates an expected failure (xfail),
            and gray indicates a skipped test excluded from the pass rate.
          </p>
        </div>

        <div className="space-y-3">
          {compat.files.map((file) => {
            const hasEntries = file.entries.length > 0;
            const Tag = hasEntries ? "details" : "div";
            return (
              <Tag
                key={file.name}
                className="overflow-hidden rounded-2xl border border-fg-dim/20 bg-bg-secondary/40"
              >
                {hasEntries ? (
                  <summary className="cursor-pointer list-none px-5 py-4">
                    <FileRow file={file} />
                  </summary>
                ) : (
                  <div className="px-5 py-4">
                    <FileRow file={file} />
                  </div>
                )}

                {hasEntries && (
                  <div className="border-t border-fg-dim/20 px-5 py-4">
                    <div className="overflow-x-auto">
                      <table>
                        <thead>
                          <tr>
                            <th>Test</th>
                            <th>Status</th>
                          </tr>
                        </thead>
                        <tbody>
                          {file.entries.map((entry) => (
                            <tr key={entry.name}>
                              <td className="align-top">
                                <code className="text-xs text-[var(--fg-primary)]">
                                  {entry.name}
                                </code>
                                <p className="mt-2 text-xs text-[var(--fg-secondary)]">
                                  {entry.reason}
                                  {entry.goos && (
                                    <span className="ml-2 text-[var(--fg-dim)]">
                                      ({entry.goos.join(", ")} only)
                                    </span>
                                  )}
                                </p>
                              </td>
                              <td className="align-top">
                                <span
                                  className={`inline-flex min-w-28 justify-center rounded-full border px-3 py-1 text-xs font-semibold ${modeTone(entry.mode)}`}
                                >
                                  {entry.mode}
                                </span>
                              </td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </div>
                )}
              </Tag>
            );
          })}
        </div>
      </section>
    </div>
  );
}

function FileRow({ file }: { file: BashFile }) {
  return (
    <div className="grid gap-3 lg:grid-cols-[minmax(110px,220px)_minmax(120px,160px)_1fr_minmax(130px,220px)] lg:items-center">
      <strong className="font-mono text-sm text-[var(--fg-primary)]">
        {file.name.replace("oils/", "")}
      </strong>
      <span className="font-mono text-sm text-[var(--fg-primary)]">
        {fileCounts(file)}
      </span>
      <div className="flex h-3 w-full overflow-hidden rounded-full bg-white/10">
        {fileProgressSegments(file).map((segment) => (
          <span
            key={`${file.name}-${segment.className}-${segment.width}`}
            className={segment.className}
            style={{ width: segment.width }}
          />
        ))}
      </div>
      <span className="text-sm text-[var(--fg-secondary)]">
        {fileNote(file)}
      </span>
    </div>
  );
}
