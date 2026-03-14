# Contributing to gbash

## Repository Structure

The repo is a committed Go workspace plus a pnpm workspace. The root module has the public `gbash` package, CLI, internal runtime implementation, and core commands. [`contrib/`](./contrib/) and [`examples/`](./examples/) remain separate Go modules to keep optional dependencies out of the core import graph, while [`packages/`](./packages/) contains publishable JavaScript packages such as [`@ewhauser/gbash-wasm`](./packages/gbash-wasm/).

The repo uses both [`go.work`](./go.work) and committed child-module `replace` directives. `go.work` keeps the workspace coherent at the repo root, and the child-module replaces make each nested module buildable on its own while still declaring real tagged dependencies for published consumption.

## Building and Testing

`make build`, `make test`, and `make lint` cover the Go modules. See the [`Makefile`](./Makefile) for additional targets.

Use `npm exec --yes pnpm@10.10.0 -- install --frozen-lockfile` at the repo root when you need the JavaScript workspace dependencies, or `pnpm install` if you already manage pnpm locally.

## Module Versioning

Published versions are coordinated with the root module release line. The root module uses plain tags like `v0.0.7`; contrib modules use nested-module tags like `contrib/jq/v0.0.7` and `contrib/sqlite3/v0.0.7`. The child modules keep real version requirements in `go.mod`, plus committed local `replace` directives so the repo still builds against the local checkout during development. The shipped `gbash-extras` binary lives under the `contrib/extras` module and follows that coordinated version line.

Use `make fix-modules MODULE_VERSION=vX.Y.Z` when preparing the next coordinated root, contrib, and `@ewhauser/gbash-wasm` release line. That updates the nested module requirements, refreshes the local replaces, updates the npm package version, and runs `go mod tidy` in each child Go module.

## Release Process

The supported release path is GitHub Actions driven:

1. Run `make release` or dispatch the `Prepare Release` workflow manually.
2. Review and merge the generated `release/vX.Y.Z` PR into `main`.
3. Let the `Publish Release` workflow create the root plus contrib tags and publish the root GitHub release automatically, including both `gbash` and `gbash-extras` archives plus a shared checksum file.

`Prepare Release` derives the next release line by taking the latest root `v*` tag and incrementing the patch number.

`make tag-release RELEASE_VERSION=vX.Y.Z` remains available as a local fallback for debugging or manual recovery, but it is no longer the primary release path.

## Benchmarks

Run the local comparison benchmark from the repo root:

```bash
make bench-compare
```

Sample local results from March 13, 2026, using the default 100 runs:

| Scenario | `gbash` median | `gbash` p95 | `just-bash` median | `just-bash` p95 |
| --- | ---: | ---: | ---: | ---: |
| `startup_echo` | `5.08ms` | `6.86ms` | `618.94ms` | `956.86ms` |
| `workspace_inventory` | `18.50ms` | `51.81ms` | `618.30ms` | `725.79ms` |

These numbers are a local reference point, not a portability guarantee. Startup comparisons may not be fully apples to apples yet, because `just-bash` currently embeds tools like Python in its base container and `gbash` does not.

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
