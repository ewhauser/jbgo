#!/usr/bin/env bats

load test_helper

# compat-docker-build.sh uses `cd -- dir` which gbash does not support.
# See https://github.com/ewhauser/gbash/issues/297
#
# These tests are written and ready to run once that issue is resolved.
# To unblock them, remove the skip lines.

setup() {
	TEST_TEMP_DIR="$(mktemp -d)"
	SANDBOX="${TEST_TEMP_DIR}/sandbox"
	STUB_BIN="${SANDBOX}/bin"
	mkdir -p "${STUB_BIN}"
	mkdir -p "${SANDBOX}/scripts"
	mkdir -p "${SANDBOX}/docker/compat"
	mkdir -p "${SANDBOX}/state"

	cat > "${SANDBOX}/go.mod" <<-'GOMOD'
	module github.com/ewhauser/gbash

	go 1.22.0
	GOMOD

	touch "${SANDBOX}/docker/compat/Dockerfile"

	cp "${SCRIPTS_DIR}/compat-docker-build.sh" "${SANDBOX}/scripts/compat-docker-build.sh"

	# Multi-command docker stub with state-file control.
	cat > "${STUB_BIN}/docker" <<-'STUB'
	#!/bin/sh
	mkdir -p /state
	echo "$*" >> /state/docker_calls

	case "$1" in
	  pull)
	    if [ -f /state/docker_pull_succeed ]; then
	      img="${*##* }"
	      safe="$(echo "$img" | tr '/:' '__')"
	      touch "/state/docker_inspect_${safe}"
	      exit 0
	    fi
	    exit 1
	    ;;
	  image)
	    shift; shift
	    img="$1"
	    safe="$(echo "$img" | tr '/:' '__')"
	    if [ -f "/state/docker_inspect_${safe}" ]; then
	      exit 0
	    fi
	    exit 1
	    ;;
	  tag)
	    exit 0
	    ;;
	  build)
	    exit 0
	    ;;
	esac
	exit 0
	STUB
	chmod +x "${STUB_BIN}/docker"
}

teardown() {
	rm -rf "${TEST_TEMP_DIR}"
}

@test "compat-docker-build: default build calls docker build" {
	skip "blocked: gbash does not support cd -- (issue #297)"
	run_gbash "scripts/compat-docker-build.sh"
	[ "$status" -eq 0 ]
	[[ "$(<"${SANDBOX}/state/docker_calls")" == *"build"* ]]
	[[ "$(<"${SANDBOX}/state/docker_calls")" == *"--build-arg GO_VERSION=1.22.0"* ]]
}

@test "compat-docker-build: reads GO_VERSION from go.mod" {
	skip "blocked: gbash does not support cd -- (issue #297)"
	run_gbash "scripts/compat-docker-build.sh"
	[ "$status" -eq 0 ]
	[[ "$(<"${SANDBOX}/state/docker_calls")" == *"GO_VERSION=1.22.0"* ]]
}

@test "compat-docker-build: uses COMPAT_DOCKER_GO_VERSION when set" {
	skip "blocked: gbash does not support cd -- (issue #297)"
	run_gbash "COMPAT_DOCKER_GO_VERSION=1.23.1 scripts/compat-docker-build.sh"
	[ "$status" -eq 0 ]
	[[ "$(<"${SANDBOX}/state/docker_calls")" == *"GO_VERSION=1.23.1"* ]]
}

@test "compat-docker-build: syncs from base image when PULL_MODE=always" {
	skip "blocked: gbash does not support cd -- (issue #297)"
	touch "${SANDBOX}/state/docker_pull_succeed"
	run_gbash "COMPAT_DOCKER_BASE_IMAGE=registry/base:latest COMPAT_DOCKER_PULL=always scripts/compat-docker-build.sh"
	[ "$status" -eq 0 ]
	[[ "$(<"${SANDBOX}/state/docker_calls")" == *"pull"* ]]
	[[ "$(<"${SANDBOX}/state/docker_calls")" == *"tag"* ]]
	! [[ "$(<"${SANDBOX}/state/docker_calls")" == *"build"* ]]
}

@test "compat-docker-build: tags base image when names differ" {
	skip "blocked: gbash does not support cd -- (issue #297)"
	touch "${SANDBOX}/state/docker_pull_succeed"
	run_gbash "COMPAT_DOCKER_IMAGE=my-image COMPAT_DOCKER_BASE_IMAGE=other-image COMPAT_DOCKER_PULL=always scripts/compat-docker-build.sh"
	[ "$status" -eq 0 ]
	[[ "$(<"${SANDBOX}/state/docker_calls")" == *"tag other-image my-image"* ]]
}

@test "compat-docker-build: skips sync when PULL_MODE=0 and falls through to build" {
	skip "blocked: gbash does not support cd -- (issue #297)"
	run_gbash "COMPAT_DOCKER_BASE_IMAGE=registry/base:latest COMPAT_DOCKER_PULL=0 scripts/compat-docker-build.sh"
	[ "$status" -eq 0 ]
	! [[ "$(<"${SANDBOX}/state/docker_calls")" == *"pull"* ]]
	[[ "$(<"${SANDBOX}/state/docker_calls")" == *"build"* ]]
}

@test "compat-docker-build: passes platform arg when set" {
	skip "blocked: gbash does not support cd -- (issue #297)"
	run_gbash "COMPAT_DOCKER_PLATFORM=linux/arm64 scripts/compat-docker-build.sh"
	[ "$status" -eq 0 ]
	[[ "$(<"${SANDBOX}/state/docker_calls")" == *"--platform linux/arm64"* ]]
}

@test "compat-docker-build: uses custom image name" {
	skip "blocked: gbash does not support cd -- (issue #297)"
	run_gbash "COMPAT_DOCKER_IMAGE=my-custom-image scripts/compat-docker-build.sh"
	[ "$status" -eq 0 ]
	[[ "$(<"${SANDBOX}/state/docker_calls")" == *"-t my-custom-image"* ]]
}
