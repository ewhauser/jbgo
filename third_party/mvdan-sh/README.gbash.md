## gbash fork provenance

This directory is a vendored fork of `mvdan.cc/sh/v3` copied from upstream tag `v3.13.0`.

The code is compiled as part of the `github.com/ewhauser/gbash` module under
`github.com/ewhauser/gbash/third_party/mvdan-sh/...`; it is not a separate Go
module and does not rely on `replace` directives.

`gbash` carries a minimal local patch to add a generic `interp.ProcSubstHandler(...)`
hook so process substitution can be backed by sandbox-owned opaque pipe paths
instead of host FIFOs under `TMPDIR`.

The intended steady state is to keep this patch small and upstreamable.
