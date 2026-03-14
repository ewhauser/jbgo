"use client";

import dynamic from "next/dynamic";

const HeroTerminal = dynamic(() => import("./HeroTerminal"), {
  ssr: false,
  loading: () => (
    <div className="crt-terminal">
      <div className="crt-titlebar">
        <div className="crt-dot red" />
        <div className="crt-dot yellow" />
        <div className="crt-dot green" />
        <div className="crt-title">gbash</div>
      </div>
      <div className="crt-body">
        <div className="flex items-center justify-center h-full">
          <div className="text-fg-dim font-mono text-sm animate-pulse">
            Loading WASM runtime...
          </div>
        </div>
      </div>
    </div>
  ),
});

export default function HeroTerminalWrapper() {
  return <HeroTerminal />;
}
