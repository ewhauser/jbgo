#!/usr/bin/env bats
load test_helper

# publish_pkgsite.sh uses BASH_SOURCE[0] with set -u, which fails in gbash
# because BASH_SOURCE is never set. These tests are blocked until gbash
# supports BASH_SOURCE (or the script is updated to use ${BASH_SOURCE[0]:-$0}).
#
# The tests below are written and ready to run once that issue is resolved.
# To unblock them, remove the skip lines.

setup() {
	TEST_TEMP_DIR="$(mktemp -d)"
	SANDBOX="${TEST_TEMP_DIR}/sandbox"
	STUB_BIN="${SANDBOX}/bin"
	mkdir -p "${STUB_BIN}" "${SANDBOX}/scripts" "${SANDBOX}/contrib/awk" "${SANDBOX}/contrib/jq" "${SANDBOX}/state"

	cp "${SCRIPTS_DIR}/publish_pkgsite.sh" "${SANDBOX}/scripts/publish_pkgsite.sh"

	# Root go.mod.
	cat > "${SANDBOX}/go.mod" <<-'EOF'
	module github.com/ewhauser/gbash
	EOF

	# Contrib go.mod files.
	cat > "${SANDBOX}/contrib/awk/go.mod" <<-'EOF'
	module github.com/ewhauser/gbash/contrib/awk
	EOF
	cat > "${SANDBOX}/contrib/jq/go.mod" <<-'EOF'
	module github.com/ewhauser/gbash/contrib/jq
	EOF

	# Default: go list succeeds.
	echo "success" > "${SANDBOX}/state/go_behavior"
	echo "0" > "${SANDBOX}/state/go_attempt"

	# go stub.
	cat > "${STUB_BIN}/go" <<-'STUB'
	#!/bin/sh
	case "$1" in
		list)
			behavior="$(cat /state/go_behavior 2>/dev/null)"
			if [ "${behavior}" = "success" ]; then
				echo "$*"
				exit 0
			elif [ "${behavior}" = "fail" ]; then
				echo "not found" >&2
				exit 1
			elif [ "${behavior}" = "retry" ]; then
				attempt="$(cat /state/go_attempt 2>/dev/null)"
				attempt=$((attempt + 1))
				echo "${attempt}" > /state/go_attempt
				succeed_on="$(cat /state/go_succeed_on 2>/dev/null)"
				if [ "${attempt}" -ge "${succeed_on}" ]; then
					echo "$*"
					exit 0
				fi
				echo "not found" >&2
				exit 1
			fi
			;;
	esac
	exit 0
	STUB
	chmod +x "${STUB_BIN}/go"
}

teardown() { rm -rf "${TEST_TEMP_DIR}"; }

@test "publish_pkgsite: requires a version argument" {
	skip "blocked: gbash does not set BASH_SOURCE"
	run_gbash "source /scripts/publish_pkgsite.sh"
	[ "$status" -eq 1 ]
	[[ "$output" == *"usage: scripts/publish_pkgsite.sh vX.Y.Z"* ]]
}

@test "publish_pkgsite: rejects version without v prefix" {
	skip "blocked: gbash does not set BASH_SOURCE"
	run_gbash "source /scripts/publish_pkgsite.sh 1.2.3"
	[ "$status" -eq 1 ]
	[[ "$output" == *"version must look like vX.Y.Z"* ]]
}

@test "publish_pkgsite: requires go command" {
	skip "blocked: gbash does not set BASH_SOURCE"
	rm -f "${STUB_BIN}/go"
	run_gbash "source /scripts/publish_pkgsite.sh v1.0.0"
	[ "$status" -eq 1 ]
	[[ "$output" == *"go toolchain is required"* ]]
}

@test "publish_pkgsite: publishes all modules successfully" {
	skip "blocked: gbash does not set BASH_SOURCE"
	run_gbash "source /scripts/publish_pkgsite.sh v1.0.0"
	[ "$status" -eq 0 ]
	[[ "$output" == *"published"*"v1.0.0"* ]]
}

@test "publish_pkgsite: retries on failure then succeeds" {
	skip "blocked: gbash does not set BASH_SOURCE"
	echo "retry" > "${SANDBOX}/state/go_behavior"
	echo "0" > "${SANDBOX}/state/go_attempt"
	echo "2" > "${SANDBOX}/state/go_succeed_on"
	run_gbash "PKG_GO_DEV_MAX_ATTEMPTS=5 PKG_GO_DEV_RETRY_DELAY_SECONDS=0 source /scripts/publish_pkgsite.sh v1.0.0"
	[ "$status" -eq 0 ]
	[[ "$output" == *"waiting for proxy to serve"* ]]
	[[ "$output" == *"published"* ]]
}

@test "publish_pkgsite: fails after max attempts" {
	skip "blocked: gbash does not set BASH_SOURCE"
	echo "fail" > "${SANDBOX}/state/go_behavior"
	run_gbash "PKG_GO_DEV_MAX_ATTEMPTS=2 PKG_GO_DEV_RETRY_DELAY_SECONDS=0 source /scripts/publish_pkgsite.sh v1.0.0"
	[ "$status" -eq 1 ]
	[[ "$output" == *"failed to publish"* ]]
}

@test "publish_pkgsite: reads version from RELEASE_VERSION env" {
	skip "blocked: gbash does not set BASH_SOURCE"
	run_gbash "RELEASE_VERSION=v2.0.0 source /scripts/publish_pkgsite.sh"
	[ "$status" -eq 0 ]
	[[ "$output" == *"published"*"v2.0.0"* ]]
}

@test "publish_pkgsite: uses custom GOPROXY_URL" {
	skip "blocked: gbash does not set BASH_SOURCE"
	run_gbash "GOPROXY_URL=https://custom.proxy.example source /scripts/publish_pkgsite.sh v1.0.0"
	[ "$status" -eq 0 ]
	[[ "$output" == *"requesting pkg.go.dev indexing for v1.0.0 via https://custom.proxy.example"* ]]
}
