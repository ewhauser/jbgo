#!/usr/bin/env bash
set -euo pipefail

# If GBASH_CONFORMANCE_AWK is already set, use it directly
if [[ -n "${GBASH_CONFORMANCE_AWK:-}" ]]; then
  echo "$GBASH_CONFORMANCE_AWK"
  exit 0
fi

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

if ! command -v nix >/dev/null 2>&1; then
  echo "error: nix is not installed" >&2
  echo "" >&2
  echo "Install Nix to get the pinned GNU awk binary for conformance tests:" >&2
  echo "" >&2
  echo "  macOS:  sh <(curl -L https://nixos.org/nix/install)" >&2
  echo "  Linux:  sh <(curl -L https://nixos.org/nix/install) --daemon" >&2
  echo "" >&2
  echo "After installation, restart your shell and re-run this command." >&2
  echo "" >&2
  echo "Alternatively, set GBASH_CONFORMANCE_AWK to skip Nix:" >&2
  echo "  export GBASH_CONFORMANCE_AWK=/path/to/awk" >&2
  exit 1
fi

awk_path=""
gawk_path=""
for out in $(nix build "${REPO_ROOT}#awk" --no-link --print-out-paths --extra-experimental-features 'nix-command flakes' 2>/dev/null); do
  if [[ -x "${out}/bin/awk" ]]; then
    awk_path="${out}/bin/awk"
    break
  fi
  if [[ -z "$gawk_path" && -x "${out}/bin/gawk" ]]; then
    gawk_path="${out}/bin/gawk"
  fi
done

if [[ -z "$awk_path" ]]; then
  awk_path="$gawk_path"
fi

if [[ -z "$awk_path" ]]; then
  echo "error: nix build failed to produce awk binary" >&2
  exit 1
fi

echo "$awk_path"
