#!/usr/bin/env bash
set -euo pipefail

# If GBASH_CONFORMANCE_BASH is already set, use it directly
if [[ -n "${GBASH_CONFORMANCE_BASH:-}" ]]; then
  echo "$GBASH_CONFORMANCE_BASH"
  exit 0
fi

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

# Check that nix is installed
if ! command -v nix >/dev/null 2>&1; then
  echo "error: nix is not installed" >&2
  echo "" >&2
  echo "Install Nix to get the pinned bash binary for conformance tests:" >&2
  echo "" >&2
  echo "  macOS:  sh <(curl -L https://nixos.org/nix/install)" >&2
  echo "  Linux:  sh <(curl -L https://nixos.org/nix/install) --daemon" >&2
  echo "" >&2
  echo "After installation, restart your shell and re-run this command." >&2
  echo "" >&2
  echo "Alternatively, set GBASH_CONFORMANCE_BASH to skip Nix:" >&2
  echo "  export GBASH_CONFORMANCE_BASH=/path/to/bash" >&2
  exit 1
fi

# Build the bash package from the flake; Nix caches this in /nix/store.
# --print-out-paths may return multiple outputs (e.g. bash + bash-man),
# so find the one that contains bin/bash.
bash_path=""
for out in $(nix build "${REPO_ROOT}#bash" --no-link --print-out-paths --extra-experimental-features 'nix-command flakes' 2>/dev/null); do
  if [[ -x "${out}/bin/bash" ]]; then
    bash_path="${out}/bin/bash"
    break
  fi
done

if [[ -z "$bash_path" ]]; then
  echo "error: nix build failed to produce bash binary" >&2
  exit 1
fi

echo "$bash_path"
