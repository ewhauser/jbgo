#!/usr/bin/env bash
set -euo pipefail

# If GBASH_BATS is already set, use it directly
if [[ -n "${GBASH_BATS:-}" ]]; then
  echo "$GBASH_BATS"
  exit 0
fi

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

# Check that nix is installed
if ! command -v nix >/dev/null 2>&1; then
  echo "error: nix is not installed" >&2
  echo "" >&2
  echo "Install Nix to get the pinned bats binary:" >&2
  echo "" >&2
  echo "  macOS:  sh <(curl -L https://nixos.org/nix/install)" >&2
  echo "  Linux:  sh <(curl -L https://nixos.org/nix/install) --daemon" >&2
  echo "" >&2
  echo "After installation, restart your shell and re-run this command." >&2
  echo "" >&2
  echo "Alternatively, set GBASH_BATS to skip Nix:" >&2
  echo "  export GBASH_BATS=/path/to/bats" >&2
  exit 1
fi

# Build the bats package from the flake; Nix caches this in /nix/store.
# --print-out-paths may return multiple outputs, so find the one with bin/bats.
bats_path=""
for out in $(nix build "${REPO_ROOT}#bats" --no-link --print-out-paths --extra-experimental-features 'nix-command flakes' 2>/dev/null); do
  if [[ -x "${out}/bin/bats" ]]; then
    bats_path="${out}/bin/bats"
    break
  fi
done

if [[ -z "$bats_path" ]]; then
  echo "error: nix build failed to produce bats binary" >&2
  exit 1
fi

echo "$bats_path"
