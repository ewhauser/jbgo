# Fork Workflow

This directory is a checked-in mirror of upstream `mvdan/sh` plus repo-owned
patch files.

Rules:

- Do not edit mirrored Go source in this directory directly.
- Direct edits are only allowed in `patches/`, this file, and `UPSTREAM`.
- Refresh the fork by running `./scripts/update_mvdan_sh.sh`.
- If behavior must change, update or add an ordered patch file under
  `third_party/mvdan-sh/patches/`, then rerun the update script.
- Keep patches small and scoped to one behavior or maintenance step.
- After changing patches or refreshing upstream, run the relevant gbash tests.

Expected workflow:

1. Modify or add `third_party/mvdan-sh/patches/NNNN-description.patch`.
2. Run `./scripts/update_mvdan_sh.sh` (optionally with `--ref <upstream-ref>`).
3. Review the regenerated fork tree.
4. Run tests before submitting.
