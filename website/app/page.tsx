import Link from "next/link";
import Header from "./components/Header";
import Footer from "./components/Footer";
import HeroTerminalWrapper from "./components/HeroTerminalWrapper";
import { withBasePath } from "./lib/site";

export default function Home() {
  return (
    <>
      <Header />

      {/* Hero: two-column - info left, terminal right */}
      <section className="mx-auto max-w-6xl px-4 sm:px-6 pt-12 pb-10 lg:pt-16 lg:pb-14">
        <div className="grid gap-10 lg:grid-cols-2 lg:gap-12 items-start">
          {/* Left: project info */}
          <div>
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              src={withBasePath("/logo.svg")}
              alt="gbash"
              className="h-28 w-28 sm:h-36 sm:w-36 mb-5"
            />
            <p className="text-base text-fg-secondary leading-relaxed mb-6">
              A deterministic, sandbox-only, bash-like runtime for AI agents,
              implemented in Go. Virtual filesystem, registry-backed command
              execution, policy enforcement, and structured tracing.
            </p>

            {/* Try it */}
            <div className="rounded-lg border border-fg-dim/20 bg-bg-secondary/50 px-4 py-3 mb-6">
              <p className="text-xs text-fg-dim mb-1.5 font-medium uppercase tracking-wider">
                Try it - no install required
              </p>
              <code className="text-sm font-mono text-accent leading-relaxed block">
                go run github.com/ewhauser/gbash/cmd/gbash@latest -c &apos;echo hello; pwd; ls -la&apos;
              </code>
            </div>

            {/* Install */}
            <div className="space-y-2.5 mb-6">
              <div>
                <p className="text-xs text-fg-dim mb-1 font-medium">CLI</p>
                <pre className="rounded-md bg-bg-secondary border border-fg-dim/20 px-3 py-2 text-sm font-mono text-fg-primary overflow-x-auto">
                  go install github.com/ewhauser/gbash/cmd/gbash@latest
                </pre>
              </div>
              <div>
                <p className="text-xs text-fg-dim mb-1 font-medium">
                  Go library
                </p>
                <pre className="rounded-md bg-bg-secondary border border-fg-dim/20 px-3 py-2 text-sm font-mono text-fg-primary overflow-x-auto">
                  go get github.com/ewhauser/gbash
                </pre>
              </div>
            </div>

            {/* Links */}
            <div className="flex flex-wrap gap-x-6 gap-y-2 text-sm">
              <Link
                href="/docs/getting-started"
                className="text-accent hover:underline"
              >
                Docs
              </Link>
              <a
                href="https://github.com/ewhauser/gbash"
                className="text-accent hover:underline"
                target="_blank"
                rel="noopener noreferrer"
              >
                GitHub
              </a>
              <a
                href="https://github.com/ewhauser/gbash/releases"
                className="text-accent hover:underline"
                target="_blank"
                rel="noopener noreferrer"
              >
                Releases
              </a>
              <a
                href="https://github.com/ewhauser/gbash/tree/main/examples"
                className="text-accent hover:underline"
                target="_blank"
                rel="noopener noreferrer"
              >
                Examples
              </a>
              <a
                href="https://pkg.go.dev/github.com/ewhauser/gbash"
                className="text-accent hover:underline"
                target="_blank"
                rel="noopener noreferrer"
              >
                pkg.go.dev
              </a>
            </div>
          </div>

          {/* Right: terminal */}
          <div>
            <HeroTerminalWrapper />
          </div>
        </div>
      </section>

      <Footer />
    </>
  );
}
