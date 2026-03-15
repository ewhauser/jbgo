export interface NavItem {
  title: string;
  href?: string;
  items?: NavItem[];
}

export const docsNavigation: NavItem[] = [
  {
    title: "Getting Started",
    items: [
      { title: "Overview", href: "/docs/getting-started" },
      { title: "Installation", href: "/docs/getting-started/installation" },
      { title: "Quick Start", href: "/docs/getting-started/quick-start" },
    ],
  },
  {
    title: "Observability",
    items: [
      { title: "Tracing and Logging", href: "/docs/observability/tracing-and-logging" },
    ],
  },
  {
    title: "Performance",
    items: [
      { title: "Benchmarks", href: "/docs/performance/benchmarks" },
      { title: "Compatibility", href: "/docs/performance/compatibility" },
    ],
  },
  {
    title: "FAQ",
    items: [{ title: "Common Questions", href: "/docs/faq" }],
  },
];

export function flattenNav(items: NavItem[]): NavItem[] {
  const flat: NavItem[] = [];
  for (const item of items) {
    if (item.href) flat.push(item);
    if (item.items) flat.push(...flattenNav(item.items));
  }
  return flat;
}
