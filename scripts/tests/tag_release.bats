#!/usr/bin/env bats

load test_helper

# The git stub reads/writes files under /state/ to simulate git behavior.
# Tags are stored at /state/tags/<tagname> (without refs/tags/ prefix).

setup() {
	TEST_TEMP_DIR="$(mktemp -d)"
	SANDBOX="${TEST_TEMP_DIR}/sandbox"
	STUB_BIN="${SANDBOX}/bin"
	mkdir -p "${STUB_BIN}" "${SANDBOX}/scripts" "${SANDBOX}/contrib/awk" "${SANDBOX}/contrib/jq" "${SANDBOX}/state"

	cp "${SCRIPTS_DIR}/tag_release.sh" "${SANDBOX}/scripts/tag_release.sh"

	# Default state: on main branch, clean worktree, no existing tags.
	echo "main" > "${SANDBOX}/state/branch"
	echo "" > "${SANDBOX}/state/status"
	echo "abc123def" > "${SANDBOX}/state/head"

	create_stub find 'printf "contrib/awk\ncontrib/jq\n"'
	create_stub sort 'sort'

	# git stub that reads state files to simulate different scenarios.
	cat > "${STUB_BIN}/git" << 'GIT_STUB'
#!/bin/sh
case "$1" in
	branch)
		cat /state/branch
		;;
	status)
		cat /state/status
		;;
	rev-parse)
		shift
		case "$1" in
			HEAD)
				cat /state/head
				;;
			-q)
				# git rev-parse -q --verify refs/tags/<tag>
				# Strip the refs/tags/ prefix to find our state file.
				raw_tag="$3"
				tag="${raw_tag#refs/tags/}"
				if [ -f "/state/tags/${tag}" ]; then
					exit 0
				fi
				exit 1
				;;
		esac
		;;
	rev-list)
		# git rev-list -n 1 <tag>
		raw_tag="$4"
		tag="${raw_tag#refs/tags/}"
		if [ -f "/state/tags/${tag}" ]; then
			cat "/state/tags/${tag}"
		fi
		;;
	tag)
		# git tag -a <tag> -m <msg>
		shift
		if [ "$1" = "-a" ]; then
			tag="$2"
			mkdir -p "/state/tags/$(dirname "${tag}")"
			cp /state/head "/state/tags/${tag}"
		fi
		;;
	push)
		shift
		echo "pushed to $1: $*"
		;;
	*)
		echo "git stub: unhandled: $*" >&2
		exit 1
		;;
esac
GIT_STUB
	chmod +x "${STUB_BIN}/git"
}

teardown() {
	rm -rf "${TEST_TEMP_DIR}"
}

@test "requires a version argument" {
	run_gbash "scripts/tag_release.sh"
	[ "$status" -eq 1 ]
	[[ "$output" == *"usage: scripts/tag_release.sh vX.Y.Z"* ]]
}

@test "rejects version without v prefix" {
	run_gbash "scripts/tag_release.sh 1.2.3"
	[ "$status" -eq 1 ]
	[[ "$output" == *"version must look like vX.Y.Z"* ]]
}

@test "rejects non-main branch without override" {
	echo "feature" > "${SANDBOX}/state/branch"
	run_gbash "scripts/tag_release.sh v1.0.0"
	[ "$status" -eq 1 ]
	[[ "$output" == *"release tags must be created from main"* ]]
}

@test "allows non-main branch with ALLOW_NON_MAIN=1" {
	echo "feature" > "${SANDBOX}/state/branch"
	run_gbash "ALLOW_NON_MAIN=1 scripts/tag_release.sh v1.0.0"
	[ "$status" -eq 0 ]
}

@test "rejects dirty worktree" {
	echo "M some/file" > "${SANDBOX}/state/status"
	run_gbash "scripts/tag_release.sh v1.0.0"
	[ "$status" -eq 1 ]
	[[ "$output" == *"git worktree must be clean"* ]]
}

@test "creates root and contrib tags" {
	run_gbash "scripts/tag_release.sh v1.0.0"
	[ "$status" -eq 0 ]
	[[ "$output" == *"created tags:"* ]]
	[[ "$output" == *"v1.0.0"* ]]
	[[ "$output" == *"contrib/awk/v1.0.0"* ]]
	[[ "$output" == *"contrib/jq/v1.0.0"* ]]
}

@test "is idempotent when tags exist on same commit" {
	head_commit="$(cat "${SANDBOX}/state/head")"
	mkdir -p "${SANDBOX}/state/tags/contrib/awk" "${SANDBOX}/state/tags/contrib/jq"
	echo "${head_commit}" > "${SANDBOX}/state/tags/v1.0.0"
	echo "${head_commit}" > "${SANDBOX}/state/tags/contrib/awk/v1.0.0"
	echo "${head_commit}" > "${SANDBOX}/state/tags/contrib/jq/v1.0.0"

	run_gbash "scripts/tag_release.sh v1.0.0"
	[ "$status" -eq 0 ]
	[[ "$output" == *"all release tags already existed"* ]]
}

@test "fails when tag exists on different commit" {
	mkdir -p "${SANDBOX}/state/tags"
	echo "different_commit" > "${SANDBOX}/state/tags/v1.0.0"

	run_gbash "scripts/tag_release.sh v1.0.0"
	[ "$status" -eq 1 ]
	[[ "$output" == *"tag already exists on a different commit"* ]]
}

@test "shows push instructions by default" {
	run_gbash "scripts/tag_release.sh v1.0.0"
	[ "$status" -eq 0 ]
	[[ "$output" == *"to push them:"* ]]
	[[ "$output" == *"git push origin"* ]]
}

@test "pushes tags when PUSH=1" {
	run_gbash "PUSH=1 scripts/tag_release.sh v1.0.0"
	[ "$status" -eq 0 ]
	[[ "$output" == *"pushed to origin"* ]]
}

@test "accepts version from RELEASE_VERSION env var" {
	run_gbash "RELEASE_VERSION=v2.0.0 scripts/tag_release.sh"
	[ "$status" -eq 0 ]
	[[ "$output" == *"v2.0.0"* ]]
}
