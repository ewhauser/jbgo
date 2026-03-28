# Contributing to gbash

## Repository Structure

The repo is a committed Go workspace plus a pnpm workspace. The root module has the public `gbash` package, CLI, internal runtime implementation, and core commands. [`contrib/`](./contrib/) and [`examples/`](./examples/) remain separate Go modules to keep optional dependencies out of the core import graph, while [`packages/`](./packages/) contains publishable JavaScript packages such as [`@ewhauser/gbash-wasm`](./packages/gbash-wasm/).

The repo uses both [`go.work`](./go.work) and committed child-module `replace` directives. `go.work` keeps the workspace coherent at the repo root, and the child-module replaces make each nested module buildable on its own while still declaring real tagged dependencies for published consumption.

## Building and Testing

`make build`, `make test`, and `make lint` cover the Go modules. See the [`Makefile`](./Makefile) for additional targets.

Use `npm exec --yes pnpm@10.10.0 -- install --frozen-lockfile` at the repo root when you need the JavaScript workspace dependencies, or `pnpm install` if you already manage pnpm locally.

## Conformance Testing

Conformance tests compare gbash output against pinned bash and curl binaries provided by Nix. This ensures consistent results across macOS and Linux regardless of the system tool versions.

Run the conformance suite:

```bash
make conformance-test
```

This fetches bash and curl from the Nix binary cache automatically (a few seconds on first run; instant on subsequent runs).

Nix installation:

- macOS: `sh <(curl -L https://nixos.org/nix/install)`
- Linux: `sh <(curl -L https://nixos.org/nix/install) --daemon`

After installing, restart your shell or run `. /nix/var/nix/profiles/default/etc/profile.d/nix-daemon.sh`.

To skip Nix entirely, point `GBASH_CONFORMANCE_BASH` and `GBASH_CONFORMANCE_CURL` at any bash and curl binaries:

```bash
export GBASH_CONFORMANCE_BASH=/path/to/bash
export GBASH_CONFORMANCE_CURL=/path/to/curl
make conformance-test
```

## Fuzzing

The repo uses Go native fuzzing locally and in GitHub Actions.

- PRs run the full deep shard set when a changed file touches the dependency graph for the fuzzed core runtime packages or one of the contrib fuzz packages.
- `make fuzz-smoke` runs a smaller local shard set for quick iteration.
- `make fuzz-full` runs the broader shard set used for PR deep fuzz, on `main`, and on the scheduled full-fuzz workflow.
- `make fuzz-shard FUZZ_SHARD=FUZZ_FULL_SHARD_2 FUZZTIME=15s` reruns one shard locally.
- `make fuzz-run FUZZ_TARGETS="./contrib/sqlite3:FuzzSQLiteCommands ./network:FuzzHTTPClientPolicy" FUZZTIME=15s` reruns exact targets.

Fuzz failures can emit minimized corpus files under `testdata/fuzz/Fuzz*/...`. Those files should be kept when they capture a real bug or harness regression, because plain `go test` will replay them as regression inputs.

Typical local workflow:

1. Reproduce the failure with the `go test -run='FuzzName/<hash>'` command printed by Go.
2. Fix the bug or harness issue.
3. Keep the minimized input under `testdata/fuzz/...` if it still adds regression value after the fix.
4. Re-run the owning fuzz target plus the relevant shard.

CI uploads any new or modified fuzz corpus files from failing fuzz jobs, along with a manifest of re-run commands and base64 copies so minimized inputs can be recovered directly from the workflow run.

## Module Versioning

Published versions are coordinated with the root module release line. The root module uses plain tags like `v0.0.7`; contrib modules use nested-module tags like `contrib/jq/v0.0.7` and `contrib/sqlite3/v0.0.7`. The child modules keep real version requirements in `go.mod`, plus committed local `replace` directives so the repo still builds against the local checkout during development. The shipped `gbash-extras` binary lives under the `contrib/extras` module and follows that coordinated version line.

Use `make fix-modules MODULE_VERSION=vX.Y.Z` when preparing the next coordinated root, contrib, and `@ewhauser/gbash-wasm` release line. That updates the nested module requirements, refreshes the local replaces, updates the npm package version, and runs `go mod tidy` in each child Go module.

## Release Process

The supported release path is GitHub Actions driven:

1. Run `make release` or dispatch the `Prepare Release` workflow manually.
2. Review and merge the generated `release/vX.Y.Z` PR into `main`.
3. Let the `Publish Release` workflow create the root plus contrib tags and publish the root GitHub release automatically, including both `gbash` and `gbash-extras` archives plus a shared checksum file.
4. The same workflow requests each released Go module version from `proxy.golang.org`, which is the supported way to make the new docs show up on `pkg.go.dev` for the root module and the release-tagged contrib modules.

`Prepare Release` derives the next release line by taking the latest root `v*` tag and incrementing the patch number.

`make tag-release RELEASE_VERSION=vX.Y.Z` remains available as a local fallback for debugging or manual recovery, but it is no longer the primary release path. If you need to re-request documentation indexing for an existing release, run `./scripts/publish_pkgsite.sh vX.Y.Z`.

## Benchmarks

Run the local comparison benchmark from the repo root:

```bash
make bench-compare
```

Write the same report to JSON with:

```bash
make bench-compare JSON_OUT=bench-compare.json
```

The comparison report includes five runtimes:

- `gbash`: the native Go helper process
- `GNU bash`: the host `bash` interpreter launched with profiles disabled
- `gbash-extras`: the shipped extras CLI with `awk`, `jq`, `sqlite3`, and `yq` pre-registered
- `gbash-node-wasm`: the `packages/gbash-wasm/wasm` artifact booted inside Node.js
- `just-bash`: the published npm package invoked through `npx`

Before timed trials begin, the harness runs one untimed primer launch for each
runtime and scenario. That keeps the reported numbers from being dominated by the
fresh-binary first-exec penalty on macOS while still measuring a fresh process for
every timed trial.

`workspace_inventory` still uses the same generated fixture for every runtime. The
native helpers mount that fixture directly, while `gbash-node-wasm` preloads the
fixture into the in-memory `js/wasm` filesystem because host-backed filesystems are
not available there. The shared command is intentionally pipe-free so it also runs
on the current `js/wasm` target.

`agentic_search` uses a separate mixed fixture with `.go`, `.md`, `.csv`, `.json`,
and `.jsonl` files plus `jq` queries. Runtimes that do not bundle `jq` are reported
as skipped for that scenario instead of failing the whole comparison.

These numbers are a local reference point, not a portability guarantee. Startup
comparisons are still not fully apples to apples, because `just-bash` currently
embeds tools like Python in its base container and `gbash` does not.

When JSON output is enabled, each runtime result includes `artifact_size_bytes`.
For the native runtimes this is the built executable size, for `GNU bash` it is the
host `bash` executable size, for Node/WASM it is the `gbash.wasm` size, and for
`just-bash` it is the installed `node_modules` closure size measured from a
temporary `npm install` plus the host `node` executable size.

Scenario results may also include `status=skipped` with a `skip_reason` when a
runtime cannot run a scenario because a required optional command is unavailable.

## GNU Coreutils Compatibility Testing

You can evaluate the skew between implemented commands and [coreutils](https://www.gnu.org/software/coreutils/).

The compatibility harness now runs inside the compat Docker image. The scheduled GitHub workflow uses the published image from GitHub Container Registry, and local `make gnu-test` / `make compat-docker-run` pull that published image by default, tagging it locally as `gbash-compat-local`. If the published image is unavailable, the helper falls back to a local build.

```bash
make gnu-test
make gnu-test GNU_UTILS="printf pwd"
```

If you want to prefetch or refresh the local compat image explicitly:

```bash
make compat-docker-build
make compat-docker-run
```

Useful overrides:

- `GNU_UTILS` limits the utility list.
- `GNU_TESTS` runs exact GNU test files instead of the manifest-selected utility suites.
- `GNU_KEEP_WORKDIR=1` preserves the patched GNU workdir under the results directory for inspection.
- `COMPAT_DOCKER_BASE_IMAGE` overrides the published image reference.
- `COMPAT_DOCKER_PULL` controls whether Docker should refresh the published image before running.

Force a full local rebuild when you need to bypass the published image:

```bash
COMPAT_DOCKER_BASE_IMAGE= COMPAT_DOCKER_PULL=0 make compat-docker-build
```
