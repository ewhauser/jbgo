# Provenance

`examples/harness-overlay` vendors the upstream [`harness`](https://github.com/wedow/harness) shell runtime so it can be executed inside a persistent `gbash` sandbox.

- Upstream repository: `https://github.com/wedow/harness`
- Pinned upstream commit: `474799144f418258abd27ecf073679b2270aa780`
- Upstream license: MIT

Copied upstream artifacts in this example:

- `workspace/bin/harness`
- `workspace/plugins/auth`
- `workspace/plugins/core`
- `workspace/plugins/openai`
- `workspace/plugins/anthropic`
- `workspace/plugins/chatgpt`
- `workspace/plugins/skills`
- `workspace/plugins/subagents`
- `workspace/LICENSE.harness`

Documented gbash-specific additions in this example:

- `workspace/AGENTS.md`
- `workspace/.harness/tools/bash`
- `workspace/.harness/sessions/.gitkeep`
- `main.go`
- `main_test.go`
- `update-harness.sh`

The vendored upstream files stay mechanically copied under `workspace/`. gbash-specific behavior lives in the local `.harness/` override layer so upstream refreshes remain straightforward.
