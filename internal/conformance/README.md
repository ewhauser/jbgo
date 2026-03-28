This package contains the `gbash` conformance harness for vendored OILS shell coverage plus local curl parity specs.

- Shared selected bash corpus: `oils/`
- Local curl corpus: `curl/`
- Shared helper commands: `bin/`
- Shared fixtures: `fixtures/`
- Suite manifest with `skip` and `xfail` entries: `manifest.json`
- Vendored upstream source: `upstream/oils/spec/`

The helper scripts in `bin/` are shell-script replacements for the upstream Python helpers from OILS, and the vendored fixtures under `fixtures/spec/testdata` are patched to call the local `.sh` names so they still run without Python in the sandbox.
The harness honors upstream `## compare_shells:` metadata when loading OILS files, and it can run additional oracle suites when the corresponding `GBASH_CONFORMANCE_<SHELL>` environment variables are set (for example `GBASH_CONFORMANCE_DASH`, `GBASH_CONFORMANCE_MKSH`, or `GBASH_CONFORMANCE_ZSH`).
The same directory also contains small local compatibility helpers, such as `tac`, for cases where the oracle host shell may not provide a command that `gbash` already implements.
The full vendored conformance corpus is gated behind `GBASH_RUN_CONFORMANCE=1` so the default `make test` path can keep using `-race`. Known skips and expected mismatches are tracked in `manifest.json`.
Run the default pinned bash/curl suites locally with `make conformance-test`.
