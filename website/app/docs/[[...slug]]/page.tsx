import { notFound } from "next/navigation";
import type { Metadata } from "next";
import { flattenNav, docsNavigation } from "@/app/lib/docs-navigation";

// Map of slug paths to their MDX imports
const pages: Record<string, () => Promise<{ default: React.ComponentType; metadata?: { title?: string; description?: string } }>> = {
  api: () => import("@/content/api/index.mdx"),
  "api/custom-commands": () => import("@/content/api/custom-commands.mdx"),
  "api/runtime": () => import("@/content/api/runtime.mdx"),
  "api/sessions": () => import("@/content/api/sessions.mdx"),
  "api/tracing-and-logging": () => import("@/content/api/tracing-and-logging.mdx"),
  "api/wasm": () => import("@/content/api/wasm.mdx"),
  commands: () => import("@/content/commands/index.mdx"),
  "commands/archive": () => import("@/content/commands/archive.mdx"),
  "commands/contrib": () => import("@/content/commands/contrib.mdx"),
  "commands/environment": () => import("@/content/commands/environment.mdx"),
  "commands/file-and-path": () => import("@/content/commands/file-and-path.mdx"),
  "commands/search-and-text": () => import("@/content/commands/search-and-text.mdx"),
  configuration: () => import("@/content/configuration/index.mdx"),
  "configuration/environment": () => import("@/content/configuration/environment.mdx"),
  "configuration/filesystem": () => import("@/content/configuration/filesystem.mdx"),
  "configuration/network": () => import("@/content/configuration/network.mdx"),
  faq: () => import("@/content/faq/index.mdx"),
  "getting-started": () => import("@/content/getting-started/index.mdx"),
  "getting-started/installation": () => import("@/content/getting-started/installation.mdx"),
  "getting-started/quick-start": () => import("@/content/getting-started/quick-start.mdx"),
  "guides/agent-integration": () => import("@/content/guides/agent-integration.mdx"),
  "guides/browser-wasm": () => import("@/content/guides/browser-wasm.mdx"),
  "guides/contributing": () => import("@/content/guides/contributing.mdx"),
  "guides/examples": () => import("@/content/guides/examples.mdx"),
  "observability/tracing-and-logging": () => import("@/content/api/tracing-and-logging.mdx"),
  "performance/benchmarks": () => import("@/content/performance/benchmarks.mdx"),
  compatibility: () => import("@/content/compatibility/index.mdx"),
  "compatibility/bash": () => import("@/content/compatibility/bash.mdx"),
  "compatibility/coreutils": () => import("@/content/compatibility/coreutils.mdx"),
  security: () => import("@/content/security/index.mdx"),
  "security/policy": () => import("@/content/security/policy.mdx"),
  "security/threat-model": () => import("@/content/security/threat-model.mdx"),
};

const canonicalSlugs: Record<string, string> = {
  "observability/tracing-and-logging": "api/tracing-and-logging",
  "performance/compatibility": "compatibility/coreutils",
};

interface Props {
  params: Promise<{ slug?: string[] }>;
}

export async function generateStaticParams() {
  return Object.keys(pages).map((slug) => ({
    slug: slug.split("/"),
  }));
}

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const { slug } = await params;
  const key = slug?.join("/") || "getting-started";
  const loader = pages[key];
  if (!loader) return {};
  const canonicalKey = canonicalSlugs[key] ?? key;

  // Find nav item for title
  const allItems = flattenNav(docsNavigation);
  const navItem = allItems.find((item) => item.href === `/docs/${canonicalKey}`);

  return {
    title: navItem?.title || key,
  };
}

export default async function DocsPage({ params }: Props) {
  const { slug } = await params;
  const key = slug?.join("/") || "getting-started";
  const loader = pages[key];

  if (!loader) {
    notFound();
  }

  const { default: MDXContent } = await loader();

  return <MDXContent />;
}
