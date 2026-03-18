## gbash fork provenance

This directory is a vendored fork of `mvdan.cc/sh/v3` copied from upstream tag `v3.13.0`.

The code is compiled as part of the `github.com/ewhauser/gbash` module under
`github.com/ewhauser/gbash/internal/shfork/...`; it is not a separate Go
module and does not rely on `replace` directives.

`gbash` carries a minimal local patch to add a generic `interp.ProcSubstHandler(...)`
hook so process substitution can be backed by sandbox-owned opaque pipe paths
instead of host FIFOs under `TMPDIR`.

The intended steady state is to keep this patch small and upstreamable.

## Migration tracker

- 2026-03-18: Moved fork root from `third_party/mvdan-sh` to `internal/shfork`.
  - Move commit: `03b9df332dac9d96f7eff0094ba79de2b0897dab`
