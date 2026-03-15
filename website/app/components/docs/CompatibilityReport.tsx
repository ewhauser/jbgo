import {
  bucketCounts,
  bucketRowNote,
  bucketSegments,
  formatPercent,
  loadCompatSummary,
  suiteInScopePassPct,
  suiteInScopeTotal,
  testStatusTone,
} from "@/app/lib/compat";
import { withBasePath } from "@/app/lib/site";

function formatGeneratedAt(value: string): string {
  return new Intl.DateTimeFormat("en-US", {
    dateStyle: "long",
    timeStyle: "short",
    timeZone: "UTC",
  }).format(new Date(value));
}

export default async function CompatibilityReport() {
  const summary = await loadCompatSummary();

  if (!summary) {
    return (
      <section className="mt-8 rounded-2xl border border-fg-dim/20 bg-bg-secondary/40 p-6">
        <h2 className="text-xl font-semibold text-[var(--fg-primary)]">
          Compatibility Data Unavailable
        </h2>
        <p className="mt-3 text-sm leading-6 text-[var(--fg-secondary)]">
          This build was exported without GNU compatibility metadata. Published
          releases preserve the raw compatibility assets under{" "}
          <code>/compat/latest/</code> and render this page from the same
          summary at build time.
        </p>
      </section>
    );
  }

  const inScopeTotal = suiteInScopeTotal(summary);
  const inScopePassPct = suiteInScopePassPct(summary);
  const rawSummaryHref = withBasePath("/compat/latest/summary.json");
  const rawBadgeHref = withBasePath("/compat/latest/badge.svg");
  const selectedDetail = `${inScopeTotal} in scope, ${summary.suite.filtered_total ?? 0} out of scope, ${summary.suite.pass} passing`;
  const runnableDetail = `${summary.suite.runnable_total} runnable, ${summary.suite.pass} passing, ${summary.suite.skip + summary.suite.xfail} skipped, ${summary.suite.fail + summary.suite.error + summary.suite.xpass + summary.suite.unreported} failing`;

  return (
    <div className="mt-8 space-y-8">
      <section className="space-y-3">
        <p className="text-sm leading-6 text-[var(--fg-secondary)]">
          <code>gbash</code> is evaluated against the GNU coreutils test suite.
          The results below stay grouped by upstream GNU categories rather than
          a derived command matrix.
        </p>
        <p className="text-sm leading-6 text-[var(--fg-secondary)]">
          Generated <strong>{formatGeneratedAt(summary.generated_at)}</strong>{" "}
          against GNU coreutils <strong>{summary.gnu_version}</strong>. Raw
          assets: <a href={rawSummaryHref}>summary.json</a> and{" "}
          <a href={rawBadgeHref}>badge.svg</a>.
        </p>
      </section>

      <section className="grid gap-4 md:grid-cols-3">
        <article className="rounded-2xl border border-fg-dim/20 bg-bg-secondary/40 p-5">
          <p className="text-xs font-medium uppercase tracking-[0.18em] text-[var(--fg-dim)]">
            In-Scope Test Pass
          </p>
          <p className="mt-3 text-3xl font-semibold text-[var(--fg-primary)]">
            {formatPercent(inScopePassPct)}
          </p>
          <p className="mt-3 text-sm text-[var(--fg-secondary)]">
            {selectedDetail}
          </p>
        </article>
        <article className="rounded-2xl border border-fg-dim/20 bg-bg-secondary/40 p-5">
          <p className="text-xs font-medium uppercase tracking-[0.18em] text-[var(--fg-dim)]">
            Runnable Test Pass
          </p>
          <p className="mt-3 text-3xl font-semibold text-[var(--fg-primary)]">
            {formatPercent(summary.suite.pass_pct_runnable)}
          </p>
          <p className="mt-3 text-sm text-[var(--fg-secondary)]">
            {runnableDetail}
          </p>
        </article>
        <article className="rounded-2xl border border-fg-dim/20 bg-bg-secondary/40 p-5">
          <p className="text-xs font-medium uppercase tracking-[0.18em] text-[var(--fg-dim)]">
            Coverage Categories
          </p>
          <p className="mt-3 text-3xl font-semibold text-[var(--fg-primary)]">
            {summary.categories.length}
          </p>
          <p className="mt-3 text-sm text-[var(--fg-secondary)]">
            {summary.suite.selected_total} total tests across upstream GNU
            categories.
          </p>
        </article>
      </section>

      <section className="space-y-4">
        <div>
          <h2 className="text-2xl font-semibold text-[var(--fg-primary)]">
            Coverage Per Category
          </h2>
          <p className="mt-2 text-sm leading-6 text-[var(--fg-secondary)]">
            Expand categories to inspect test-level status. Green indicates a
            pass, amber indicates a skipped test, gray indicates an out-of-scope
            test excluded from the overall pass rate, and red indicates a
            failed, errored, or unreported test.
          </p>
        </div>

        <div className="space-y-3">
          {summary.categories.map((category) => (
            <details
              key={category.name}
              className="overflow-hidden rounded-2xl border border-fg-dim/20 bg-bg-secondary/40"
            >
              <summary className="cursor-pointer list-none px-5 py-4">
                <div className="grid gap-3 lg:grid-cols-[minmax(110px,160px)_minmax(120px,160px)_1fr_minmax(130px,220px)] lg:items-center">
                  <strong className="font-mono text-sm text-[var(--fg-primary)]">
                    {category.name}
                  </strong>
                  <span className="font-mono text-sm text-[var(--fg-primary)]">
                    {bucketCounts(category.summary)}
                  </span>
                  <div className="flex h-3 w-full overflow-hidden rounded-full bg-white/10">
                    {bucketSegments(category.summary).map((segment) => (
                      <span
                        key={`${category.name}-${segment.className}-${segment.width}`}
                        className={segment.className}
                        style={{ width: segment.width }}
                      />
                    ))}
                  </div>
                  <span className="text-sm text-[var(--fg-secondary)]">
                    {bucketRowNote(category.summary)}
                  </span>
                </div>
              </summary>

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
                      {(category.tests ?? []).map((test) => (
                        <tr key={test.path}>
                          <td className="align-top">
                            <code className="text-xs text-[var(--fg-primary)]">
                              {test.path}
                            </code>
                            {test.filtered && test.filter_reason ? (
                              <p className="mt-2 text-xs text-[var(--fg-secondary)]">
                                {test.filter_reason}
                              </p>
                            ) : null}
                          </td>
                          <td className="align-top">
                            <span
                              className={`inline-flex min-w-28 justify-center rounded-full border px-3 py-1 text-xs font-semibold capitalize ${testStatusTone(
                                test.status,
                                test.filtered
                              )}`}
                            >
                              {test.filtered ? "out of scope" : test.status}
                            </span>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              </div>
            </details>
          ))}
        </div>
      </section>
    </div>
  );
}
