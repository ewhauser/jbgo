#!/usr/bin/env bats

load test_helper

setup() {
	TEST_TEMP_DIR="$(mktemp -d)"
	SANDBOX="${TEST_TEMP_DIR}/sandbox"
	STUB_BIN="${SANDBOX}/bin"
	mkdir -p "${STUB_BIN}"
	mkdir -p "${SANDBOX}/work"
	mkdir -p "${SANDBOX}/state/go_results"

	cp "${SCRIPTS_DIR}/open_fuzz_pr.sh" "${SANDBOX}/open_fuzz_pr.sh"

	# --- git stub: multi-command ---
	cat > "${STUB_BIN}/git" <<-'STUB'
	#!/bin/sh
	case "$1" in
	  ls-files)
	    # Treat all corpus files as untracked (exit 1).
	    exit 1
	    ;;
	  status)
	    echo "?? unknown"
	    ;;
	  checkout)
	    if [ "$2" = "--" ]; then
	      rm -f "$3"
	    else
	      echo "git $*"
	    fi
	    ;;
	  add|commit|push)
	    echo "git $*"
	    ;;
	esac
	STUB
	chmod +x "${STUB_BIN}/git"

	# --- go stub: reads /state/go_results/<corpus_name> ---
	cat > "${STUB_BIN}/go" <<-'STUB'
	#!/bin/sh
	run_arg=""
	for arg in "$@"; do
	  case "$arg" in
	    -run=*) run_arg="${arg#-run=}" ;;
	  esac
	done
	# run_arg looks like ^FuzzTarget/corpus_name$
	corpus="${run_arg#^}"
	corpus="${corpus%\$}"
	# corpus is now FuzzTarget/corpus_name -- use just the filename part
	corpus_name="${corpus##*/}"
	state_file="/state/go_results/${corpus_name}"
	if [ -f "$state_file" ]; then
	  cat "$state_file"
	else
	  echo "FAIL"
	fi
	STUB
	chmod +x "${STUB_BIN}/go"

	# --- gh stub ---
	cat > "${STUB_BIN}/gh" <<-'STUB'
	#!/bin/sh
	echo "gh $*"
	STUB
	chmod +x "${STUB_BIN}/gh"
}

teardown() {
	rm -rf "${TEST_TEMP_DIR}"
}

# ---------- helpers ----------

create_corpus_file() {
	local corpus_name="$1"
	local go_output="${2:-FAIL}"
	mkdir -p "${SANDBOX}/work/pkg/testdata/fuzz/FuzzParse"
	echo "fuzz data" > "${SANDBOX}/work/pkg/testdata/fuzz/FuzzParse/${corpus_name}"
	echo "$go_output" > "${SANDBOX}/state/go_results/${corpus_name}"
}

# ---------- tests ----------

@test "requires job-name argument" {
	run_gbash "cd /work && source /open_fuzz_pr.sh"
	[ "$status" -ne 0 ]
}

@test "no corpus files produces clean exit" {
	run_gbash "cd /work && source /open_fuzz_pr.sh nightly-fuzz"
	[ "$status" -eq 0 ]
	[[ "$output" == *"No new corpus files found."* ]]
}

@test "all files deadline exceeded produces clean exit" {
	create_corpus_file "corpus1" "context deadline exceeded"

	run_gbash "cd /work && source /open_fuzz_pr.sh nightly-fuzz"
	[ "$status" -eq 0 ]
	[[ "$output" == *"Skipped (deadline exceeded)"* ]]
	[[ "$output" == *"All reproducers were deadline exceeded."* ]]
}

@test "real failures creates branch commits pushes and opens PR" {
	create_corpus_file "corpus1" "FAIL: FuzzParse/corpus1"

	run_gbash "cd /work && source /open_fuzz_pr.sh nightly-fuzz"
	[ "$status" -eq 0 ]
	[[ "$output" == *"Real failure"* ]]
	[[ "$output" == *"git checkout -b fuzz/reproducers-"* ]]
	[[ "$output" == *"git add"* ]]
	[[ "$output" == *"git commit"* ]]
	[[ "$output" == *"git push origin fuzz/reproducers-"* ]]
	[[ "$output" == *"gh pr create"* ]]
}

@test "single reproducer uses singular in commit message" {
	create_corpus_file "corpus1" "FAIL: FuzzParse/corpus1"

	run_gbash "cd /work && source /open_fuzz_pr.sh nightly-fuzz"
	[ "$status" -eq 0 ]
	[[ "$output" == *"fuzz: add reproducer from nightly-fuzz"* ]]
}

@test "mixed deadline-exceeded and real failures keeps only real ones" {
	create_corpus_file "corpus_timeout" "context deadline exceeded"
	create_corpus_file "corpus_real" "FAIL: FuzzParse/corpus_real"

	run_gbash "cd /work && source /open_fuzz_pr.sh nightly-fuzz"
	[ "$status" -eq 0 ]
	[[ "$output" == *"Skipped (deadline exceeded)"* ]]
	[[ "$output" == *"Real failure"* ]]
	[[ "$output" == *"corpus_real"* ]]
}
