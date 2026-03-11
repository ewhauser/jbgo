---
name: release
description: Manage the tag-driven GitHub release workflow for the `github.com/ewhauser/jbgo` repository. Use when Codex needs to validate release readiness, run GoReleaser checks or snapshots, inspect or update release automation, prepare or publish a SemVer tag, or troubleshoot missing `jbgo` binaries, checksums, or build metadata in GitHub Releases.
---

# Release

Use this skill for release work in the `github.com/ewhauser/jbgo` repository.

## Load the release surface

- Read `.goreleaser.yaml`, `.github/workflows/release.yml`, `.github/workflows/release-check.yml`, `Makefile`, `README.md`, `cmd/jbgo/cli.go`, and `cmd/jbgo/version.go` before changing release behavior.
- Check `git status --short` and current tags before cutting or troubleshooting a release.
- Prefer `make release-check` and `make release-snapshot` so local validation uses the pinned GoReleaser version from `Makefile`.

## Validate release readiness

- Run `go test ./...` when code or release wiring changed.
- Run `make release-check` after editing `.goreleaser.yaml` or release workflows.
- Run `make release-snapshot` before tagging when packaging, ldflags, archive naming, or workflow behavior changed.
- Confirm snapshot output includes Linux and macOS `.tar.gz` archives, Windows `.zip` archives, and `checksums.txt`.
- Confirm `jbgo --version` reports embedded build metadata by running `go run ./cmd/jbgo --version` locally or by inspecting a produced binary.

## Cut a release

- After a successful readiness/validation pass, explicitly ask the user whether they want you to create and push a release tag now.
- Do not create or push a tag unless the user explicitly confirms.
- Sync `main` before tagging unless the user explicitly asks for another branch.
- Create annotated SemVer tags in the form `vX.Y.Z`.
- Push the tag to `origin`; that triggers the GitHub `Release` workflow.
- Treat published tags as immutable. If a release is bad, cut a new patch version instead of moving the tag.

```bash
git checkout main
git pull --ff-only
git tag -a v0.1.0 -m "v0.1.0"
git push origin v0.1.0
```

## Troubleshoot failures

- If `make release-check` fails, fix `.goreleaser.yaml` or workflow syntax before touching tags.
- If `make release-snapshot` succeeds locally but GitHub release fails, compare the pinned GoReleaser version in `Makefile` with the versions in the workflows and inspect the GitHub Actions logs.
- If artifacts are missing or misnamed, inspect the archive and checksum sections in `.goreleaser.yaml`.
- If `jbgo --version` is wrong, inspect `.goreleaser.yaml` `ldflags` plus `cmd/jbgo/cli.go` and `cmd/jbgo/version.go`.

## Keep the process aligned

- Keep `Makefile`, `.github/workflows/release.yml`, `.github/workflows/release-check.yml`, and `.goreleaser.yaml` on the same GoReleaser version.
- Keep the `README.md` release section aligned with the automation files when the release contract changes.
