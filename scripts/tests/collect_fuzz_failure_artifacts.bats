#!/usr/bin/env bats
load test_helper

setup() {
	TEST_TEMP_DIR="$(mktemp -d)"
	SANDBOX="${TEST_TEMP_DIR}/sandbox"
	STUB_BIN="${SANDBOX}/bin"
	mkdir -p "${STUB_BIN}" "${SANDBOX}/scripts" "${SANDBOX}/work" "${SANDBOX}/state"

	cp "${SCRIPTS_DIR}/collect_fuzz_failure_artifacts.sh" "${SANDBOX}/scripts/collect_fuzz_failure_artifacts.sh"

	# git stub using state files.
	# /state/tracked -- if this file exists, all corpus files are "tracked".
	# /state/modified -- if this file exists, tracked files report as modified.
	cat > "${STUB_BIN}/git" <<-'STUB'
	#!/bin/sh
	case "$1" in
		ls-files)
			if [ -f /state/tracked ]; then
				exit 0
			fi
			exit 1
			;;
		status)
			if [ -f /state/modified ]; then
				echo " M file"
			fi
			exit 0
			;;
	esac
	exit 0
	STUB
	chmod +x "${STUB_BIN}/git"

	# shasum stub (not a gbash builtin).
	create_stub shasum 'echo "abcdef1234567890  input"'

	# awk stub (not a gbash builtin) -- print first whitespace-delimited field.
	cat > "${STUB_BIN}/awk" <<-'STUB'
	#!/bin/sh
	while read -r first rest; do
		echo "$first"
	done
	STUB
	chmod +x "${STUB_BIN}/awk"
}

teardown() { rm -rf "${TEST_TEMP_DIR}"; }

_create_fuzz_file() {
	local name="$1"
	local content="${2:-fuzz test input}"
	mkdir -p "${SANDBOX}/work/pkg/testdata/fuzz/FuzzFoo"
	echo "${content}" > "${SANDBOX}/work/pkg/testdata/fuzz/FuzzFoo/${name}"
}

@test "no fuzz files found produces empty manifest" {
	mkdir -p "${SANDBOX}/gh_output"
	echo "" > "${SANDBOX}/gh_output/output"
	run_gbash "cd /work && GITHUB_OUTPUT=/gh_output/output source /scripts/collect_fuzz_failure_artifacts.sh /artifacts"
	[ "$status" -eq 0 ]
	# Manifest should say no files detected.
	run cat "${SANDBOX}/artifacts/manifest.md"
	[[ "$output" == *"No new or modified fuzz corpus files were detected"* ]]
	# GITHUB_OUTPUT should have has_files=false.
	run cat "${SANDBOX}/gh_output/output"
	[[ "$output" == *"has_files=false"* ]]
}

@test "new untracked fuzz files are collected" {
	_create_fuzz_file "corpus1" "fuzz input data"
	mkdir -p "${SANDBOX}/gh_output"
	echo "" > "${SANDBOX}/gh_output/output"
	run_gbash "cd /work && GITHUB_OUTPUT=/gh_output/output source /scripts/collect_fuzz_failure_artifacts.sh /artifacts"
	[ "$status" -eq 0 ]
	# Verify file was copied.
	[ -f "${SANDBOX}/artifacts/files/pkg/testdata/fuzz/FuzzFoo/corpus1" ]
	# Verify base64 was created.
	[ -f "${SANDBOX}/artifacts/base64/pkg/testdata/fuzz/FuzzFoo/corpus1.b64" ]
	# Check manifest.
	run cat "${SANDBOX}/artifacts/manifest.md"
	[[ "$output" == *"pkg/testdata/fuzz/FuzzFoo/corpus1"* ]]
	[[ "$output" == *"go test ./pkg"* ]]
	# Check GITHUB_OUTPUT.
	run cat "${SANDBOX}/gh_output/output"
	[[ "$output" == *"has_files=true"* ]]
	[[ "$output" == *"count=1"* ]]
}

@test "tracked unmodified fuzz files are skipped" {
	_create_fuzz_file "corpus1" "fuzz input data"
	# Mark all files as tracked but not modified.
	touch "${SANDBOX}/state/tracked"
	mkdir -p "${SANDBOX}/gh_output"
	echo "" > "${SANDBOX}/gh_output/output"
	run_gbash "cd /work && GITHUB_OUTPUT=/gh_output/output source /scripts/collect_fuzz_failure_artifacts.sh /artifacts"
	[ "$status" -eq 0 ]
	run cat "${SANDBOX}/artifacts/manifest.md"
	[[ "$output" == *"No new or modified fuzz corpus files were detected"* ]]
	run cat "${SANDBOX}/gh_output/output"
	[[ "$output" == *"has_files=false"* ]]
}

@test "tracked but modified fuzz files are collected" {
	_create_fuzz_file "corpus1" "modified fuzz input"
	# Mark all files as tracked AND modified.
	touch "${SANDBOX}/state/tracked"
	touch "${SANDBOX}/state/modified"
	mkdir -p "${SANDBOX}/gh_output"
	echo "" > "${SANDBOX}/gh_output/output"
	run_gbash "cd /work && GITHUB_OUTPUT=/gh_output/output source /scripts/collect_fuzz_failure_artifacts.sh /artifacts"
	[ "$status" -eq 0 ]
	[ -f "${SANDBOX}/artifacts/files/pkg/testdata/fuzz/FuzzFoo/corpus1" ]
	run cat "${SANDBOX}/gh_output/output"
	[[ "$output" == *"has_files=true"* ]]
	[[ "$output" == *"count=1"* ]]
}

@test "writes to GITHUB_STEP_SUMMARY when set" {
	_create_fuzz_file "corpus1" "fuzz input data"
	mkdir -p "${SANDBOX}/gh_output"
	echo "" > "${SANDBOX}/gh_output/output"
	echo "" > "${SANDBOX}/gh_output/summary"
	run_gbash "cd /work && GITHUB_OUTPUT=/gh_output/output GITHUB_STEP_SUMMARY=/gh_output/summary source /scripts/collect_fuzz_failure_artifacts.sh /artifacts"
	[ "$status" -eq 0 ]
	run cat "${SANDBOX}/gh_output/summary"
	[[ "$output" == *"## Fuzz Failure Inputs"* ]]
	[[ "$output" == *"pkg/testdata/fuzz/FuzzFoo/corpus1"* ]]
}

