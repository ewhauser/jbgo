import data from "@/content/compatibility/posix-compat-data.json";

interface PosixFeature {
  id: string;
  name: string;
  posix_section: string;
  classification: string;
  gbash_status: string;
  summary: string;
  test_priority: string;
  notes: string;
}

interface PosixCategory {
  id: string;
  name: string;
  posix_sections: string[];
  total: number;
  pass: number;
  partial: number;
  fail: number;
  not_applicable: number;
  not_tested: number;
  out_of_scope: number;
  extension: number;
  features: PosixFeature[];
}

interface PosixCompatData {
  generated_at: string;
  spec_edition: string;
  total_features: number;
  pass: number;
  partial: number;
  fail: number;
  not_applicable: number;
  not_tested: number;
  out_of_scope: number;
  in_scope: number;
  pass_pct: number;
  categories: PosixCategory[];
}

const compat = data as PosixCompatData;

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

function statusTone(status: string): string {
  switch (status) {
    case "pass":
      return "border-emerald-400/30 bg-emerald-400/10 text-emerald-300";
    case "partial":
      return "border-amber-300/30 bg-amber-300/10 text-amber-200";
    case "fail":
      return "border-rose-400/30 bg-rose-400/10 text-rose-200";
    case "not_applicable":
      return "border-slate-500/40 bg-slate-500/15 text-slate-200";
    case "not_tested":
      return "border-slate-500/40 bg-slate-500/15 text-slate-300";
    default:
      return "border-slate-500/40 bg-slate-500/15 text-slate-200";
  }
}

function classificationTone(classification: string): string {
  switch (classification) {
    case "required":
      return "border-blue-400/30 bg-blue-400/10 text-blue-300";
    case "extension":
      return "border-violet-400/30 bg-violet-400/10 text-violet-300";
    case "out_of_scope":
      return "border-slate-500/40 bg-slate-500/15 text-slate-300";
    case "implementation_defined":
      return "border-cyan-400/30 bg-cyan-400/10 text-cyan-300";
    case "unspecified":
      return "border-yellow-400/30 bg-yellow-400/10 text-yellow-300";
    default:
      return "border-slate-500/40 bg-slate-500/15 text-slate-200";
  }
}

function formatStatus(status: string): string {
  return status.replace(/_/g, " ");
}

function categoryProgressSegments(cat: PosixCategory) {
  if (cat.total === 0) return [];
  const segments = [
    { className: "bg-emerald-400", count: cat.pass },
    { className: "bg-amber-300", count: cat.partial },
    { className: "bg-rose-400", count: cat.fail + cat.not_tested },
    { className: "bg-slate-500/50", count: cat.not_applicable + cat.out_of_scope },
  ];
  return segments
    .filter((s) => s.count > 0)
    .map((s) => ({
      className: s.className,
      width: `${((s.count / cat.total) * 100).toFixed(4)}%`,
    }));
}

function categoryCounts(cat: PosixCategory): string {
  return `${cat.pass} / ${cat.partial} / ${cat.fail}`;
}

function categoryNote(cat: PosixCategory): string {
  const parts = [`${cat.total} features`];
  if (cat.extension > 0) parts.push(`${cat.extension} extensions`);
  if (cat.out_of_scope > 0) parts.push(`${cat.out_of_scope} out of scope`);
  return parts.join(", ");
}

export default function PosixCompatibilityReport() {
  return (
    <div className="mt-8 space-y-8">
      <section className="space-y-3">
        <p className="text-sm leading-6 text-[var(--fg-secondary)]">
          This matrix catalogs <code>gbash</code> conformance to the POSIX Shell
          Command Language. All feature descriptions are original summaries
          derived from reading the specification requirements.
        </p>
        <p className="text-sm leading-6 text-[var(--fg-secondary)]">
          Generated{" "}
          <strong>{formatGeneratedAt(compat.generated_at)}</strong>.
          Reference: {compat.spec_edition}.
        </p>
      </section>

      <section className="grid gap-4 md:grid-cols-4">
        <article className="rounded-2xl border border-fg-dim/20 bg-bg-secondary/40 p-5">
          <p className="text-xs font-medium uppercase tracking-[0.18em] text-[var(--fg-dim)]">
            In-Scope Pass Rate
          </p>
          <p className="mt-3 text-3xl font-semibold text-[var(--fg-primary)]">
            {formatPercent(compat.pass_pct)}
          </p>
          <p className="mt-3 text-sm text-[var(--fg-secondary)]">
            {compat.pass} of {compat.in_scope} in-scope features
          </p>
        </article>
        <article className="rounded-2xl border border-fg-dim/20 bg-bg-secondary/40 p-5">
          <p className="text-xs font-medium uppercase tracking-[0.18em] text-[var(--fg-dim)]">
            Partial
          </p>
          <p className="mt-3 text-3xl font-semibold text-[var(--fg-primary)]">
            {compat.partial}
          </p>
          <p className="mt-3 text-sm text-[var(--fg-secondary)]">
            features with incomplete coverage
          </p>
        </article>
        <article className="rounded-2xl border border-fg-dim/20 bg-bg-secondary/40 p-5">
          <p className="text-xs font-medium uppercase tracking-[0.18em] text-[var(--fg-dim)]">
            Out of Scope
          </p>
          <p className="mt-3 text-3xl font-semibold text-[var(--fg-primary)]">
            {compat.out_of_scope + compat.not_applicable}
          </p>
          <p className="mt-3 text-sm text-[var(--fg-secondary)]">
            features excluded by sandbox model
          </p>
        </article>
        <article className="rounded-2xl border border-fg-dim/20 bg-bg-secondary/40 p-5">
          <p className="text-xs font-medium uppercase tracking-[0.18em] text-[var(--fg-dim)]">
            Total Features
          </p>
          <p className="mt-3 text-3xl font-semibold text-[var(--fg-primary)]">
            {compat.total_features}
          </p>
          <p className="mt-3 text-sm text-[var(--fg-secondary)]">
            across {compat.categories.length} categories
          </p>
        </article>
      </section>

      <section className="space-y-4">
        <div>
          <h2 className="text-2xl font-semibold text-[var(--fg-primary)]">
            Coverage by Category
          </h2>
          <p className="mt-2 text-sm leading-6 text-[var(--fg-secondary)]">
            Expand categories to inspect individual features. Green indicates
            full conformance, amber indicates partial support, red indicates
            a failure, and gray indicates features outside scope.
          </p>
          <p className="mt-1 text-sm leading-6 text-[var(--fg-secondary)]">
            Counts shown as{" "}
            <span className="font-mono">pass / partial / fail</span>.
          </p>
        </div>

        <div className="space-y-3">
          {compat.categories.map((cat) => (
            <details
              key={cat.id}
              className="overflow-hidden rounded-2xl border border-fg-dim/20 bg-bg-secondary/40"
            >
              <summary className="cursor-pointer list-none px-5 py-4">
                <CategoryRow category={cat} />
              </summary>
              <div className="border-t border-fg-dim/20 px-5 py-4">
                <div className="overflow-x-auto">
                  <table>
                    <thead>
                      <tr>
                        <th>Feature</th>
                        <th>Type</th>
                        <th>Status</th>
                      </tr>
                    </thead>
                    <tbody>
                      {cat.features.map((feature) => (
                        <tr key={feature.id}>
                          <td className="align-top">
                            <code className="text-xs text-[var(--fg-dim)]">
                              {feature.id}
                            </code>
                            <span className="ml-2 text-sm text-[var(--fg-primary)]">
                              {feature.name}
                            </span>
                            <p className="mt-1 text-xs text-[var(--fg-secondary)]">
                              {feature.summary}
                            </p>
                            {feature.notes && (
                              <p className="mt-1 text-xs text-[var(--fg-dim)] italic">
                                {feature.notes}
                              </p>
                            )}
                          </td>
                          <td className="align-top">
                            <span
                              className={`inline-flex min-w-24 justify-center rounded-full border px-2 py-0.5 text-xs font-semibold ${classificationTone(feature.classification)}`}
                            >
                              {feature.classification.replace(/_/g, " ")}
                            </span>
                          </td>
                          <td className="align-top">
                            <span
                              className={`inline-flex min-w-20 justify-center rounded-full border px-2 py-0.5 text-xs font-semibold ${statusTone(feature.gbash_status)}`}
                            >
                              {formatStatus(feature.gbash_status)}
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

function CategoryRow({ category }: { category: PosixCategory }) {
  return (
    <div className="grid gap-3 lg:grid-cols-[minmax(140px,280px)_minmax(100px,140px)_1fr_minmax(130px,260px)] lg:items-center">
      <strong className="text-sm text-[var(--fg-primary)]">
        {category.name}
      </strong>
      <span className="font-mono text-sm text-[var(--fg-primary)]">
        {categoryCounts(category)}
      </span>
      <div className="flex h-3 w-full overflow-hidden rounded-full bg-white/10">
        {categoryProgressSegments(category).map((segment) => (
          <span
            key={`${category.id}-${segment.className}-${segment.width}`}
            className={segment.className}
            style={{ width: segment.width }}
          />
        ))}
      </div>
      <span className="text-sm text-[var(--fg-secondary)]">
        {categoryNote(category)}
      </span>
    </div>
  );
}
