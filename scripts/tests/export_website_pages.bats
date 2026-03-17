#!/usr/bin/env bats

load test_helper

setup() {
	TEST_TEMP_DIR="$(mktemp -d)"
	SANDBOX="${TEST_TEMP_DIR}/sandbox"
	STUB_BIN="${SANDBOX}/bin"
	mkdir -p "${STUB_BIN}"

	cp "${SCRIPTS_DIR}/export_website_pages.sh" "${SANDBOX}/export_website_pages.sh"

	# Create repo directory structure.
	mkdir -p "${SANDBOX}/repo/website"

	# --- pnpm stub: creates website/out/ with dummy files ---
	cat > "${STUB_BIN}/pnpm" <<-'STUB'
	#!/bin/sh
	# Parse --dir <dir> to find the website directory relative to cwd.
	dir=""
	while [ $# -gt 0 ]; do
	  case "$1" in
	    --dir) dir="$2"; shift 2 ;;
	    *) shift ;;
	  esac
	done
	out_dir="${dir:-.}/out"
	mkdir -p "$out_dir"
	echo "<html>index</html>" > "$out_dir/index.html"
	echo "<html>about</html>" > "$out_dir/about.html"
	STUB
	chmod +x "${STUB_BIN}/pnpm"

	# --- curl stub: creates the output file with dummy content ---
	cat > "${STUB_BIN}/curl" <<-'STUB'
	#!/bin/sh
	dest=""
	while [ $# -gt 0 ]; do
	  case "$1" in
	    --output) dest="$2"; shift 2 ;;
	    --fail|--location|--silent|--show-error) shift ;;
	    *) shift ;;
	  esac
	done
	if [ -n "$dest" ]; then
	  mkdir -p "$(dirname "$dest")"
	  echo "downloaded-content" > "$dest"
	fi
	STUB
	chmod +x "${STUB_BIN}/curl"

	# --- mktemp stub: creates a real temp dir inside sandbox ---
	cat > "${STUB_BIN}/mktemp" <<-STUB
	#!/bin/sh
	dir="/tmp/compat-\$\$"
	mkdir -p "\$dir"
	echo "\$dir"
	STUB
	chmod +x "${STUB_BIN}/mktemp"
}

teardown() {
	rm -rf "${TEST_TEMP_DIR}"
}

# ---------- tests ----------

@test "requires both repo-dir and output-dir arguments" {
	run_gbash "source export_website_pages.sh"
	[ "$status" -ne 0 ]
	[[ "$output" == *"usage:"* ]]

	run_gbash "source export_website_pages.sh /repo"
	[ "$status" -ne 0 ]
	[[ "$output" == *"usage:"* ]]
}

@test "builds website and copies output to output-dir" {
	run_gbash "source export_website_pages.sh /repo /output"
	[ "$status" -eq 0 ]
	[ -f "${SANDBOX}/output/index.html" ]
	[ -f "${SANDBOX}/output/about.html" ]
}

@test "creates .nojekyll file in output" {
	run_gbash "source export_website_pages.sh /repo /output"
	[ "$status" -eq 0 ]
	[ -f "${SANDBOX}/output/.nojekyll" ]
}

@test "copies local compat summary when path env var is set" {
	mkdir -p "${SANDBOX}/assets"
	echo '{"score":100}' > "${SANDBOX}/assets/summary.json"

	run_gbash "
		export GBASH_WEBSITE_COMPAT_SUMMARY_PATH=/assets/summary.json
		source export_website_pages.sh /repo /output
	"
	[ "$status" -eq 0 ]
	[ -f "${SANDBOX}/output/compat/latest/summary.json" ]
}

@test "copies local compat badge when path env var is set" {
	mkdir -p "${SANDBOX}/assets"
	echo '<svg>badge</svg>' > "${SANDBOX}/assets/badge.svg"

	run_gbash "
		export GBASH_WEBSITE_COMPAT_BADGE_PATH=/assets/badge.svg
		source export_website_pages.sh /repo /output
	"
	[ "$status" -eq 0 ]
	[ -f "${SANDBOX}/output/compat/latest/badge.svg" ]
}

@test "downloads compat assets via curl when URL env vars are set" {
	run_gbash "
		export GBASH_WEBSITE_COMPAT_SUMMARY_URL=https://example.com/summary.json
		export GBASH_WEBSITE_COMPAT_BADGE_URL=https://example.com/badge.svg
		source export_website_pages.sh /repo /output
	"
	[ "$status" -eq 0 ]
	[ -f "${SANDBOX}/output/compat/latest/summary.json" ]
	[ -f "${SANDBOX}/output/compat/latest/badge.svg" ]
}

@test "removes compat/latest/index.html from output" {
	mkdir -p "${SANDBOX}/assets"
	echo '{"score":100}' > "${SANDBOX}/assets/summary.json"

	run_gbash "
		export GBASH_WEBSITE_COMPAT_SUMMARY_PATH=/assets/summary.json
		source export_website_pages.sh /repo /output
	"
	[ "$status" -eq 0 ]
	[ ! -f "${SANDBOX}/output/compat/latest/index.html" ]
}
