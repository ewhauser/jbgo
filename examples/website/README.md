# `examples/website`

This is a vendored copy of Vercel's `just-bash` website example, adapted to run
`gbash` in the browser via `js/wasm`.

The important difference is the shell boundary:

- upstream browser shell: `just-bash/browser`
- this browser shell: `@ewhauser/gbash-wasm/browser`

## What's in this app

- `app/`
  Vendored website UI, terminal, routes, and styles
- `scripts/sync-gbash-wasm.mjs`
  Builds `packages/gbash-wasm`, then copies `gbash.wasm` and `wasm_exec.js`
  into `public/`
- `scripts/fetch-agent-data.mjs`
  Copies local `gbash` source into `app/api/agent/_agent-data` for the optional
  server-side agent route

## Local development

```bash
cd examples/website
pnpm install
pnpm dev
```

`pnpm dev` does three things before starting Next:

1. builds `@ewhauser/gbash-wasm` from local source
2. copies `gbash.wasm` and `wasm_exec.js` into `public/`
3. copies local repo files into `app/api/agent/_agent-data`
4. starts the Next dev server

## Source-control deployment

This app is intended to be deployable directly from this repository.

For a Vercel project:

- Root Directory: `examples/website`
- Install Command: `pnpm install`
- Build Command: `pnpm build`

`pnpm build` builds the WASM artifact from source and prepares the local
agent-data snapshot before running `next build`.

## Optional agent route

The browser shell does not need the agent backend.

If you want the `agent` command to work, the server runtime also needs:

- `ANTHROPIC_API_KEY`
- optionally `AI_MODEL`

Without those, `/api/agent` returns a `503` with a setup message instead of
crashing.

## Notes

- `public/gbash.wasm` and `public/wasm_exec.js` are generated, not committed.
- The browser bridge now lives in `packages/gbash-wasm`.
- Host-backed filesystems remain unsupported on `js/wasm`; the browser shell
  uses the normal in-memory `gbash` filesystem.
- This app vendors the Vercel website code so deployment does not depend on
  cloning external repositories at build time.
