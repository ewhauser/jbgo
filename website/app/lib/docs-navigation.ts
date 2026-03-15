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
    title: "Configuration",
    items: [
      { title: "Overview", href: "/docs/configuration" },
      { title: "Filesystem", href: "/docs/configuration/filesystem" },
      { title: "Network", href: "/docs/configuration/network" },
      { title: "Environment", href: "/docs/configuration/environment" },
    ],
  },
  {
    title: "API",
    items: [
      { title: "Overview", href: "/docs/api" },
      { title: "Runtime", href: "/docs/api/runtime" },
      { title: "Tracing and Logging", href: "/docs/api/tracing-and-logging" },
      { title: "Sessions", href: "/docs/api/sessions" },
      { title: "Custom Commands", href: "/docs/api/custom-commands" },
      { title: "WebAssembly", href: "/docs/api/wasm" },
    ],
  },
  {
    title: "Commands",
    items: [
      { title: "Overview", href: "/docs/commands" },
      { title: "File and Path", href: "/docs/commands/file-and-path" },
      { title: "Search and Text", href: "/docs/commands/search-and-text" },
      { title: "Archive and Compression", href: "/docs/commands/archive" },
      { title: "Environment and Execution", href: "/docs/commands/environment" },
      { title: "Contrib Commands", href: "/docs/commands/contrib" },
    ],
  },
  {
    title: "Guides",
    items: [
      { title: "Agent Integration", href: "/docs/guides/agent-integration" },
      { title: "Running in the Browser", href: "/docs/guides/browser-wasm" },
      { title: "Examples", href: "/docs/guides/examples" },
      { title: "Contributing", href: "/docs/guides/contributing" },
    ],
  },
  {
    title: "Security",
    items: [
      { title: "Security Model", href: "/docs/security" },
      { title: "Policy and Budgets", href: "/docs/security/policy" },
      { title: "Threat Model", href: "/docs/security/threat-model" },
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
