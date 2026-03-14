#!/usr/bin/env bash
set -euo pipefail

package_dir="$(cd "$(dirname "$0")/.." && pwd)"
repo_dir="$(cd "$package_dir/../.." && pwd)"
dist_dir="$package_dir/dist"

mkdir -p "$dist_dir"

cd "$repo_dir"
GOOS=js GOARCH=wasm go build -o "$dist_dir/gbash.wasm" ./packages/gbash-wasm/wasm
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$dist_dir/wasm_exec.js"
cp "$(go env GOROOT)/lib/wasm/wasm_exec_node.js" "$dist_dir/wasm_exec_node.js"

echo "wrote $dist_dir/gbash.wasm"
echo "wrote $dist_dir/wasm_exec.js"
echo "wrote $dist_dir/wasm_exec_node.js"
