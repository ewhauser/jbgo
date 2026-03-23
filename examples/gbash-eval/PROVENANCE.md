# Provenance

`examples/gbash-eval` is a Go port of the upstream [`bashkit`](https://github.com/everruns/bashkit) evaluator crate at [`crates/bashkit-eval`](https://github.com/everruns/bashkit/tree/main/crates/bashkit-eval).

- Upstream repository: `https://github.com/everruns/bashkit`
- Upstream source path: `crates/bashkit-eval`
- Pinned upstream commit: `39e733b004d3726076d8a9a7456fa8a9688d7bef`
- Upstream license: Apache-2.0

Copied upstream artifacts in this example:

- `data/eval-tasks.jsonl`
- `data/smoke-test.jsonl`
- `data/scripting-tool/discovery.jsonl`
- `data/scripting-tool/large-output.jsonl`
- `data/scripting-tool/many-tools.jsonl`
- `data/scripting-tool/paginated.jsonl`

Documented gbash-specific dataset adaptation:

- `data/eval-tasks.jsonl` keeps the upstream schema but updates `sysinfo_env_report` to expect the gbash evaluator identity (`user: agent`, `host: gbash`) instead of the upstream bashkit identity.

Deliberately not copied from upstream:

- anything under upstream `crates/bashkit-eval/results/`

The Go code under `examples/gbash-eval/internal` is repo-owned porting work that adapts the upstream evaluator concepts to `gbash` sessions, `gbash` filesystem scoring, and example-local scripted discovery/help commands.
