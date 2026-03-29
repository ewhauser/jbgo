# Harness Overlay

This example vendors a pinned copy of [`wedow/harness`](https://github.com/wedow/harness) into `workspace/`, mounts that host directory into `gbash` with the normal read-only workspace mount plus in-memory overlay, and then starts a persistent `gbash` shell around it.

The vendored harness runtime files stay mechanically copied under `workspace/`. gbash-specific behavior lives in `workspace/.harness/`, so upgrading the upstream harness snapshot does not require editing vendored upstream files in place.

The pinned upstream revision is recorded in [`UPSTREAM_COMMIT`](./UPSTREAM_COMMIT) and [`PROVENANCE.md`](./PROVENANCE.md).

## Run

From the repository root:

```bash
export OPENAI_API_KEY=your-api-key
go run ./examples/harness-overlay
```

Run a one-shot shell snippet inside the mounted harness workspace:

```bash
go run ./examples/harness-overlay --script './bin/harness help'
```

Inside the interactive shell, run harness directly:

```bash
./bin/harness
./bin/harness "summarize this tree"
./bin/harness tools
```

## Notes

- The host `workspace/` tree is mounted read-only at `/home/agent/project` with an in-memory writable overlay, so harness state lives only for the lifetime of the example process.
- `workspace/.harness/tools/bash` overrides harness's bundled `bash` tool so tool calls run inside a persistent `gbash` session and keep files, `PWD`, and exported environment across harness turns.
- API-key providers and the bundled compatible variants are supported in v1. Local private OpenAI-compatible endpoints and the ChatGPT OAuth login flow are intentionally out of scope for this example.
