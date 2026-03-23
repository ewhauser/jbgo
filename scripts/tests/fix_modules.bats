#!/usr/bin/env bats

load test_helper

write_go_mod() {
	local path="$1"
	shift
	cat > "${path}" <<EOF
$*
EOF
}

setup() {
	TEST_TEMP_DIR="$(mktemp -d)"
	SANDBOX="${TEST_TEMP_DIR}/sandbox"
	STUB_BIN="${SANDBOX}/bin"
	mkdir -p "${STUB_BIN}" "${SANDBOX}/scripts" "${SANDBOX}/state"

	for mod in awk bashtool codingtools extras htmltomarkdown jq nodejs sqlite3 yq; do
		mkdir -p "${SANDBOX}/contrib/${mod}"
	done
	mkdir -p "${SANDBOX}/examples" "${SANDBOX}/packages/gbash-wasm"

	cp "${SCRIPTS_DIR}/fix_modules.sh" "${SANDBOX}/scripts/fix_modules.sh"
	chmod +x "${SANDBOX}/scripts/fix_modules.sh"

	write_go_mod "${SANDBOX}/go.mod" "module github.com/ewhauser/gbash

go 1.26.0"

	write_go_mod "${SANDBOX}/contrib/awk/go.mod" "module github.com/ewhauser/gbash/contrib/awk

go 1.26.0

require github.com/ewhauser/gbash v0.0.26"

	write_go_mod "${SANDBOX}/contrib/bashtool/go.mod" "module github.com/ewhauser/gbash/contrib/bashtool

go 1.26.0

require (
	github.com/ewhauser/gbash v0.0.25
	github.com/ewhauser/gbash/contrib/extras v0.0.25
)

require (
	github.com/ewhauser/gbash/contrib/awk v0.0.25 // indirect
	github.com/ewhauser/gbash/contrib/htmltomarkdown v0.0.25 // indirect
	github.com/ewhauser/gbash/contrib/jq v0.0.25 // indirect
	github.com/ewhauser/gbash/contrib/sqlite3 v0.0.25 // indirect
	github.com/ewhauser/gbash/contrib/yq v0.0.25 // indirect
)"

	write_go_mod "${SANDBOX}/contrib/codingtools/go.mod" "module github.com/ewhauser/gbash/contrib/codingtools

go 1.26.0

require github.com/ewhauser/gbash v0.0.25"

	write_go_mod "${SANDBOX}/contrib/extras/go.mod" "module github.com/ewhauser/gbash/contrib/extras

go 1.26.0

require (
	github.com/ewhauser/gbash v0.0.26
	github.com/ewhauser/gbash/contrib/awk v0.0.26
	github.com/ewhauser/gbash/contrib/htmltomarkdown v0.0.26
	github.com/ewhauser/gbash/contrib/jq v0.0.26
	github.com/ewhauser/gbash/contrib/sqlite3 v0.0.26
	github.com/ewhauser/gbash/contrib/yq v0.0.26
)"

	write_go_mod "${SANDBOX}/contrib/htmltomarkdown/go.mod" "module github.com/ewhauser/gbash/contrib/htmltomarkdown

go 1.26.0

require github.com/ewhauser/gbash v0.0.26"

	write_go_mod "${SANDBOX}/contrib/jq/go.mod" "module github.com/ewhauser/gbash/contrib/jq

go 1.26.0

require github.com/ewhauser/gbash v0.0.26"

	write_go_mod "${SANDBOX}/contrib/nodejs/go.mod" "module github.com/ewhauser/gbash/contrib/nodejs

go 1.26.0

require github.com/ewhauser/gbash v0.0.0"

	write_go_mod "${SANDBOX}/contrib/sqlite3/go.mod" "module github.com/ewhauser/gbash/contrib/sqlite3

go 1.26.0

require github.com/ewhauser/gbash v0.0.26"

	write_go_mod "${SANDBOX}/contrib/yq/go.mod" "module github.com/ewhauser/gbash/contrib/yq

go 1.26.0

require github.com/ewhauser/gbash v0.0.26"

	write_go_mod "${SANDBOX}/examples/go.mod" "module github.com/ewhauser/gbash/examples

go 1.26.0

require (
	github.com/ewhauser/gbash v0.0.26
	github.com/ewhauser/gbash/contrib/bashtool v0.0.26
	github.com/ewhauser/gbash/contrib/extras v0.0.26
	github.com/ewhauser/gbash/contrib/sqlite3 v0.0.26
)

require (
	github.com/ewhauser/gbash/contrib/awk v0.0.26 // indirect
	github.com/ewhauser/gbash/contrib/htmltomarkdown v0.0.26 // indirect
	github.com/ewhauser/gbash/contrib/jq v0.0.26 // indirect
	github.com/ewhauser/gbash/contrib/yq v0.0.26 // indirect
)"

	cat > "${STUB_BIN}/go" <<-'STUB'
	#!/bin/sh
	mkdir -p /state
	echo "$(pwd) :: go $*" >> /state/go_calls
	exit 0
	STUB
	chmod +x "${STUB_BIN}/go"

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
	run_gbash "scripts/fix_modules.sh 1.2.3"
	[ "$status" -eq 1 ]
	[[ "$output" == *"version must look like vX.Y.Z"* ]]
}

@test "fix_modules: without version refreshes replaces without require edits" {
	run_gbash "scripts/fix_modules.sh"
	[ "$status" -eq 0 ]
	! [[ "$(<"${SANDBOX}/state/go_calls")" == *"-require="* ]]
	[[ "$(<"${SANDBOX}/state/go_calls")" == *"/examples :: go mod edit -replace=github.com/ewhauser/gbash/contrib/awk=../contrib/awk"* ]]
	[[ "$(<"${SANDBOX}/state/go_calls")" == *"/contrib/bashtool :: go mod edit -replace=github.com/ewhauser/gbash/contrib/yq=../yq"* ]]
}

@test "fix_modules: versioned run updates newly discovered modules and indirect internal deps" {
	run_gbash "scripts/fix_modules.sh v2.0.0"
	[ "$status" -eq 0 ]
	local calls
	calls="$(<"${SANDBOX}/state/go_calls")"
	[[ "$calls" == *"/contrib/nodejs :: go mod edit -require=github.com/ewhauser/gbash@v2.0.0"* ]]
	[[ "$calls" == *"/contrib/bashtool :: go mod edit -require=github.com/ewhauser/gbash/contrib/extras@v2.0.0"* ]]
	[[ "$calls" == *"/contrib/bashtool :: go mod edit -require=github.com/ewhauser/gbash/contrib/awk@v2.0.0"* ]]
	[[ "$calls" == *"/examples :: go mod edit -require=github.com/ewhauser/gbash/contrib/bashtool@v2.0.0"* ]]
	[[ "$calls" == *"/examples :: go mod edit -require=github.com/ewhauser/gbash/contrib/awk@v2.0.0"* ]]
}

@test "fix_modules: versioned run adds local replaces for transitive internal deps" {
	run_gbash "scripts/fix_modules.sh v3.1.0"
	[ "$status" -eq 0 ]
	local calls
	calls="$(<"${SANDBOX}/state/go_calls")"
	[[ "$calls" == *"/examples :: go mod edit -replace=github.com/ewhauser/gbash/contrib/jq=../contrib/jq"* ]]
	[[ "$calls" == *"/examples :: go mod edit -replace=github.com/ewhauser/gbash/contrib/yq=../contrib/yq"* ]]
	[[ "$calls" == *"/contrib/bashtool :: go mod edit -replace=github.com/ewhauser/gbash/contrib/htmltomarkdown=../htmltomarkdown"* ]]
}

@test "fix_modules: calls go mod tidy for each discovered module and go list at the end" {
	run_gbash "scripts/fix_modules.sh"
	[ "$status" -eq 0 ]
	local calls
	calls="$(<"${SANDBOX}/state/go_calls")"
	for mod in awk bashtool codingtools extras htmltomarkdown jq nodejs sqlite3 yq; do
		[[ "$calls" == *"/contrib/${mod} :: go mod tidy"* ]]
	done
	[[ "$calls" == *"/examples :: go mod tidy"* ]]
	local last_line
	last_line="$(tail -n1 <<<"$calls")"
	[[ "$last_line" == *"go list -m all"* ]]
}

@test "fix_modules: skips package.json sync when file does not exist" {
	run_gbash "scripts/fix_modules.sh v1.0.0"
	[ "$status" -eq 0 ]
	[ ! -f "${SANDBOX}/state/node_calls" ]
}

@test "fix_modules: calls node to sync package.json when file exists and version is set" {
	echo '{"version":"0.0.0"}' > "${SANDBOX}/packages/gbash-wasm/package.json"
	run_gbash "scripts/fix_modules.sh v3.1.0"
	[ "$status" -eq 0 ]
	[ -f "${SANDBOX}/state/node_calls" ]
	[[ "$(<"${SANDBOX}/state/node_calls")" == *"node"* ]]
}
