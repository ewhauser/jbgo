#!/usr/bin/env bats

load test_helper

# These source-based script harness tests are still skipped.
#
# BASH_SOURCE parity removed the original blocker, but the scripts still hit
# broader compatibility gaps under gbash's sourced-script execution path. Keep
# the skips until that follow-up work is done.

setup() {
	TEST_TEMP_DIR="$(mktemp -d)"
	SANDBOX="${TEST_TEMP_DIR}/sandbox"
	STUB_BIN="${SANDBOX}/bin"
	mkdir -p "${STUB_BIN}"
	mkdir -p "${SANDBOX}/scripts"
	mkdir -p "${SANDBOX}/state"

	for mod in awk htmltomarkdown jq sqlite3 yq extras; do
		mkdir -p "${SANDBOX}/contrib/${mod}"
	done
	mkdir -p "${SANDBOX}/examples"
	mkdir -p "${SANDBOX}/packages/gbash-wasm"

	cp "${SCRIPTS_DIR}/fix_modules.sh" "${SANDBOX}/scripts/fix_modules.sh"

	# Go stub that logs every invocation to /state/go_calls.
	cat > "${STUB_BIN}/go" <<-'STUB'
	#!/bin/sh
	mkdir -p /state
	echo "$(pwd) :: go $*" >> /state/go_calls
	exit 0
	STUB
	chmod +x "${STUB_BIN}/go"

	# Node stub that logs the call (receives JS on stdin, ignores it).
	cat > "${STUB_BIN}/node" <<-'STUB'
	#!/bin/sh
	mkdir -p /state
	cat > /dev/null
	echo "node $*" >> /state/node_calls
	exit 0
	STUB
	chmod +x "${STUB_BIN}/node"
}

teardown() {
	rm -rf "${TEST_TEMP_DIR}"
}

@test "fix_modules: rejects version without v prefix" {
	skip "blocked: gbash does not set BASH_SOURCE"
	run_gbash "source /scripts/fix_modules.sh 1.2.3"
	[ "$status" -eq 1 ]
	[[ "$output" == *"version must look like vX.Y.Z"* ]]
}

@test "fix_modules: runs without version -- no require calls" {
	skip "blocked: gbash does not set BASH_SOURCE"
	run_gbash "source /scripts/fix_modules.sh"
	[ "$status" -eq 0 ]
	! [[ "$(<"${SANDBOX}/state/go_calls")" == *"-require"* ]]
	[[ "$(<"${SANDBOX}/state/go_calls")" == *"-replace"* ]]
	[[ "$(<"${SANDBOX}/state/go_calls")" == *"mod tidy"* ]]
}

@test "fix_modules: with version calls go mod edit -require and -replace" {
	skip "blocked: gbash does not set BASH_SOURCE"
	run_gbash "source /scripts/fix_modules.sh v2.0.0"
	[ "$status" -eq 0 ]
	local calls
	calls="$(<"${SANDBOX}/state/go_calls")"
	[[ "$calls" == *"-require=github.com/ewhauser/gbash@v2.0.0"* ]]
	[[ "$calls" == *"-replace="* ]]
}

@test "fix_modules: calls go mod tidy for each module directory" {
	skip "blocked: gbash does not set BASH_SOURCE"
	run_gbash "source /scripts/fix_modules.sh"
	[ "$status" -eq 0 ]
	local calls
	calls="$(<"${SANDBOX}/state/go_calls")"
	for mod in awk htmltomarkdown jq sqlite3 yq extras; do
		[[ "$calls" == *"/contrib/${mod} :: go mod tidy"* ]]
	done
	[[ "$calls" == *"/examples :: go mod tidy"* ]]
}

@test "fix_modules: calls go list -m all at the end" {
	skip "blocked: gbash does not set BASH_SOURCE"
	run_gbash "source /scripts/fix_modules.sh"
	[ "$status" -eq 0 ]
	local calls
	calls="$(<"${SANDBOX}/state/go_calls")"
	local last_line
	last_line="$(tail -n1 <<<"$calls")"
	[[ "$last_line" == *"go list -m all"* ]]
}

@test "fix_modules: skips package.json sync when file does not exist" {
	skip "blocked: gbash does not set BASH_SOURCE"
	run_gbash "source /scripts/fix_modules.sh v1.0.0"
	[ "$status" -eq 0 ]
	[ ! -f "${SANDBOX}/state/node_calls" ]
}

@test "fix_modules: calls node to sync package.json when file exists and version is set" {
	skip "blocked: gbash does not set BASH_SOURCE"
	echo '{"version":"0.0.0"}' > "${SANDBOX}/packages/gbash-wasm/package.json"
	run_gbash "source /scripts/fix_modules.sh v3.1.0"
	[ "$status" -eq 0 ]
	[ -f "${SANDBOX}/state/node_calls" ]
	[[ "$(<"${SANDBOX}/state/node_calls")" == *"node"* ]]
}
