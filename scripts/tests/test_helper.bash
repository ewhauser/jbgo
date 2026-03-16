#!/usr/bin/env bash

# Bats runs under system bash. Tests invoke scripts under gbash using
# --readwrite-root pointed at a temp directory with stubbed binaries.

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPTS_DIR="${REPO_ROOT}/scripts"

# Build gbash if not already built or if source is newer than the binary.
GBASH_BIN="${REPO_ROOT}/scripts/tests/.gbash-test-bin"
if [[ ! -x "${GBASH_BIN}" ]] || [[ "${REPO_ROOT}/cmd/gbash/main.go" -nt "${GBASH_BIN}" ]]; then
	(cd "${REPO_ROOT}" && go build -o "${GBASH_BIN}" ./cmd/gbash/) || {
		echo "failed to build gbash" >&2
		return 1
	}
fi

export GBASH_BIN
export REPO_ROOT
export SCRIPTS_DIR

# Create a fresh temporary directory for each test.
setup() {
	TEST_TEMP_DIR="$(mktemp -d)"
	SANDBOX="${TEST_TEMP_DIR}/sandbox"
	STUB_BIN="${SANDBOX}/bin"
	mkdir -p "${STUB_BIN}"
}

teardown() {
	rm -rf "${TEST_TEMP_DIR}"
}

# Run a script under gbash with --readwrite-root pointed at the sandbox.
# Usage: run_gbash <script-path-relative-to-sandbox> [args...]
run_gbash() {
	run "${GBASH_BIN}" --readwrite-root "${SANDBOX}" -c "
		export PATH=/bin:/usr/bin
		cd /
		$*
	"
}

# Create a stub executable in the sandbox's bin directory.
# Usage: create_stub <name> <body>
create_stub() {
	local name="$1"
	local body="$2"
	cat > "${STUB_BIN}/${name}" <<-STUB
	#!/bin/sh
	${body}
	STUB
	chmod +x "${STUB_BIN}/${name}"
}
