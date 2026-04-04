# Provenance

`examples/harness-overlay` pins the upstream [`harness`](https://github.com/wedow/harness) shell runtime and prepares it into a gitignored cache so it can be executed inside a persistent `gbash` sandbox.

- Upstream repository: `https://github.com/wedow/harness`
- Pinned upstream commit: `474799144f418258abd27ecf073679b2270aa780`
- Upstream license: MIT

Upstream artifacts staged into the prepared cache workspace by `update-harness.sh`:

- `workspace/bin/harness`
- `workspace/plugins/auth`
- `workspace/plugins/core`
- `workspace/plugins/openai`
- `workspace/plugins/anthropic`
- `workspace/plugins/chatgpt`
- `workspace/plugins/skills`
- `workspace/plugins/subagents`
- `workspace/LICENSE.harness`

The repository does not track the upstream runtime snapshot or any Harness-derived overlay files. `update-harness.sh` clones the pinned upstream ref into `.cache/` and stages only the listed runtime files there.
