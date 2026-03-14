export const version = "browser-demo";

export const CMD_ABOUT = `gbash (browser demo)
Deterministic, sandbox-only, bash-like runtime for AI agents.
This page runs the @ewhauser/gbash-wasm/browser entrypoint and keeps one persistent session.

Custom commands:
  about       What this demo is
  install     Go install and embed snippets
  github      Source repository

Suggested commands:
  pwd
  ls
  tree
  cat README.md
  cat go.mod
  sed -n '1,40p' cmd/gbash/version.go
  grep -n "WithHTTPAccess" README.md

Note: the browser shell is gbash. The optional agent backend is separate and needs server-side setup.
`;

export const CMD_INSTALL = `Go module:
  go get github.com/ewhauser/gbash

CLI:
  go install github.com/ewhauser/gbash/cmd/gbash@latest

Basic API:
  gb, err := gbash.New()
  result, err := gb.Run(ctx, &gbash.ExecutionRequest{
    Script: "echo hello\\npwd\\n",
  })

CLI example:
  printf 'echo hi\\npwd\\n' | gbash
`;

export const CMD_GITHUB = "https://github.com/ewhauser/gbash\n";

export const FILE_README = `# gbash

\`gbash\` is a deterministic, sandbox-only, bash-like runtime for AI agents, implemented in Go.

This demo runs \`gbash\` in the browser through the published \`@ewhauser/gbash-wasm/browser\` bridge and keeps a persistent session, so filesystem changes and cwd updates survive across commands.

## Quick start

\`\`\`go
gb, err := gbash.New()
if err != nil {
  panic(err)
}

result, err := gb.Run(context.Background(), &gbash.ExecutionRequest{
  Script: "echo hello\\npwd\\n",
})
\`\`\`

## CLI

\`\`\`bash
go install github.com/ewhauser/gbash/cmd/gbash@latest
printf 'echo hi\\npwd\\n' | gbash
\`\`\`

## Useful features

- In-memory sandbox filesystem by default
- Persistent sessions through \`Runtime.NewSession\`
- Optional HTTP allowlists for \`curl\`
- Large built-in command registry in Go
- Works well as an embeddable shell runtime for agents and tools

## Good demo commands

\`\`\`bash
pwd
ls
tree
cat go.mod
sed -n '1,40p' cmd/gbash/version.go
grep -n "WithHTTPAccess" README.md
\`\`\`
`;

export const FILE_GO_MOD = `module github.com/ewhauser/gbash

go 1.26.0

require (
  golang.org/x/crypto v0.48.0
  golang.org/x/term v0.40.0
  mvdan.cc/sh/v3 v3.13.0
)

require golang.org/x/sys v0.42.0
`;

export const FILE_AGENTS_MD = `# AGENTS.md

This repo is a Go implementation of a sandboxed bash-like runtime.

When adding or changing commands:

- Prefer the shared helpers in \`commands/\`
- Prefer declarative parsing through \`CommandSpec\` and \`RunParsed\`
- Reuse existing helpers for file IO, text processing, and subprocess execution
- Keep command behavior aligned with GNU/coreutils or uutils when that is the goal

Useful helper areas:

- \`commands/command_spec.go\` for parsing and help
- \`commands/io_helpers.go\` for simple reads
- \`commands/source_helpers.go\` for file-or-stdin patterns
- \`commands/file_helpers.go\` for copy/write helpers
- \`commands/subexec_helpers.go\` for external command execution
`;

export const FILE_VERSION_GO = `package main

import (
  "runtime/debug"
  "strings"

  "github.com/ewhauser/gbash/commands"
)

var (
  version = "dev"
  commit  = "unknown"
  date    = ""
  builtBy = ""
)

func versionText() string {
  meta := currentBuildMetadata()
  var b strings.Builder
  _ = commands.RenderDetailedVersion(&b, &commands.VersionInfo{
    Name:    "gbash",
    Version: meta.Version,
    Commit:  meta.Commit,
    Date:    meta.Date,
    BuiltBy: meta.BuiltBy,
  })
  return b.String()
}

func currentBuildMetadata() buildMetadata {
  meta := buildMetadata{
    Version: normalizeBuildValue(version),
    Commit:  strings.TrimSpace(commit),
    Date:    strings.TrimSpace(date),
    BuiltBy: strings.TrimSpace(builtBy),
  }

  if info, ok := debug.ReadBuildInfo(); ok {
    if meta.Version == "" {
      meta.Version = normalizeBuildValue(info.Main.Version)
    }
  }
  return meta
}
`;

export const FILE_WEBSITE_README = `# gbash website integration

This app vendors Vercel's terminal website example and runs the \`@ewhauser/gbash-wasm/browser\` entrypoint in the browser.

Pieces involved:

- \`packages/gbash-wasm/src/browser.ts\`
  The published browser bridge behind \`@ewhauser/gbash-wasm/browser\`
- \`packages/gbash-wasm/wasm/main.go\`
  The JS/WASM bridge around a persistent gbash session
- \`examples/website/scripts/sync-gbash-wasm.mjs\`
  Builds \`@ewhauser/gbash-wasm\` and copies its browser assets into \`public/\`

Scope note:

- The browser shell is gbash
- Host-backed filesystems are disabled on js/wasm
- The optional server-side agent route is separate from this browser shell
`;

export const FILE_WTF_IS_THIS = `# WTF Is This?

This is a vendored terminal website running gbash in the browser via the \`@ewhauser/gbash-wasm/browser\` entrypoint.

What works:

- normal shell commands through gbash
- persistent cwd and filesystem state across commands
- seeded demo files that look like a small gbash repository

What is intentionally out of scope here:

- the optional AI agent route unless the server environment is configured
- host-mounted filesystems inside the browser
- external analytics or preview-toolbar behavior
`;
