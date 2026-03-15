import Link from "next/link";
import { withBasePath } from "@/app/lib/site";

export default function Footer() {
  return (
    <footer className="border-t border-fg-dim/20 bg-bg-secondary/50">
      <div className="mx-auto max-w-6xl px-4 sm:px-6 py-10">
        <div className="flex flex-col sm:flex-row items-center justify-between gap-4">
          <div className="flex items-center gap-2">
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img
              src={withBasePath("/logo.svg")}
              alt=""
              className="h-6 w-6 opacity-60"
            />
            <span className="text-sm text-fg-dim">
              gbash - Apache 2.0 License
            </span>
          </div>
          <nav className="flex items-center gap-6 text-sm text-fg-secondary">
            <Link href="/docs/getting-started" className="hover:text-accent transition-colors">
              Docs
            </Link>
            <a
              href="https://github.com/ewhauser/gbash"
              target="_blank"
              rel="noopener noreferrer"
              className="hover:text-accent transition-colors"
            >
              GitHub
            </a>
            <a
              href="https://github.com/ewhauser/gbash/releases"
              target="_blank"
              rel="noopener noreferrer"
              className="hover:text-accent transition-colors"
            >
              Releases
            </a>
          </nav>
        </div>
      </div>
    </footer>
  );
}
