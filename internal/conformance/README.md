This package contains the `gbash` bash-focused OILS-based conformance harness.

- Shared selected bash corpus: `oils/`
- Shared helper commands: `bin/`
- Shared fixtures: `fixtures/`
- Suite manifest with `skip` and `xfail` entries: `manifest.json`
- Vendored upstream source: `upstream/oils/spec/`

The helper scripts in `bin/` are shell-script replacements for the upstream Python helpers from OILS, and the selected corpus is rewritten to call the local `.sh` names so it can run without Python in the sandbox.
Non-bash-targeted upstream spec files are excluded from discovery so the harness stays scoped to bash compatibility.
The same directory also contains small local compatibility helpers, such as `tac`, for cases where the oracle host shell may not provide a command that `gbash` already implements.
The full vendored conformance corpus is gated behind `GBASH_RUN_CONFORMANCE=1` so the default `make test` path can keep using `-race`. Known skips and expected bash mismatches are tracked in `manifest.json`.
Run it locally with `make conformance-test`.
