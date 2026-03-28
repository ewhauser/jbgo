#!/usr/bin/env bash
set -euo pipefail

# If GBASH_CONFORMANCE_RIPGREP is already set, use it directly
if [[ -n "${GBASH_CONFORMANCE_RIPGREP:-}" ]]; then
  echo "$GBASH_CONFORMANCE_RIPGREP"
  exit 0
fi

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

if ! command -v nix >/dev/null 2>&1; then
  echo "error: nix is not installed" >&2
  echo "" >&2
  echo "Install Nix to get the pinned ripgrep binary for oracle tests:" >&2
  echo "" >&2
  echo "  macOS:  sh <(curl -L https://nixos.org/nix/install)" >&2
  echo "  Linux:  sh <(curl -L https://nixos.org/nix/install) --daemon" >&2
  echo "" >&2
  echo "After installation, restart your shell and re-run this command." >&2
  echo "" >&2
  echo "Alternatively, set GBASH_CONFORMANCE_RIPGREP to skip Nix:" >&2
  echo "  export GBASH_CONFORMANCE_RIPGREP=/path/to/rg" >&2
  exit 1
fi

ripgrep_path=""
for out in $(nix build "${REPO_ROOT}#ripgrep" --no-link --print-out-paths --extra-experimental-features 'nix-command flakes' 2>/dev/null); do
  if [[ -x "${out}/bin/rg" ]]; then
    ripgrep_path="${out}/bin/rg"
    break
  fi
done

if [[ -z "$ripgrep_path" ]]; then
  echo "error: nix build failed to produce ripgrep binary" >&2
  exit 1
fi

echo "$ripgrep_path"
