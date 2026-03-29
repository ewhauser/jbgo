# Harness Overlay

This example pins [`wedow/harness`](https://github.com/wedow/harness) in [`UPSTREAM_COMMIT`](./UPSTREAM_COMMIT), clones that ref into a gitignored cache, stages a runnable workspace there, and then starts a persistent `gbash` shell around it.

The repository only keeps the local overlay under `workspace/`. Upstream Harness runtime files are prepared under `.cache/` by [`update-harness.sh`](./update-harness.sh), while gbash-specific behavior stays in `workspace/.harness/`.

The pinned upstream revision is recorded in [`UPSTREAM_COMMIT`](./UPSTREAM_COMMIT) and [`PROVENANCE.md`](./PROVENANCE.md).

## Run

From the repository root:

```bash
export OPENAI_API_KEY=your-api-key
make -C examples run-harness-overlay
```

Run a one-shot shell snippet inside the prepared harness workspace:

```bash
HARNESS_OVERLAY_WORKSPACE="$(./examples/harness-overlay/update-harness.sh)" \
  go run ./examples/harness-overlay --script './bin/harness help'
```

`go run ./examples/harness-overlay` by itself only works when `HARNESS_OVERLAY_WORKSPACE` already points at a prepared cache workspace.

Inside the interactive shell, run harness directly:

```bash
./bin/harness
./bin/harness "summarize this tree"
./bin/harness tools
```

## Notes

- `update-harness.sh` clones the pinned upstream ref into `examples/harness-overlay/.cache/` and stages a runnable workspace there before the example starts.
- The prepared workspace is mounted read-only at `/home/agent/project` with an in-memory writable overlay, so harness state lives only for the lifetime of the example process.
- `workspace/.harness/tools/bash` overrides harness's bundled `bash` tool so tool calls run inside a persistent `gbash` session and keep files, `PWD`, and exported environment across harness turns.
- API-key providers and the bundled compatible variants are supported in v1. Local private OpenAI-compatible endpoints and the ChatGPT OAuth login flow are intentionally out of scope for this example.
