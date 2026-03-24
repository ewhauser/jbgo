#!/usr/bin/env bats

load test_helper

source_gnu_test_functions() {
  # Source only the helper/function portion of scripts/gnu-test.sh.
  source <(
    awk '
      /^GNU_CACHE_DIR=\$\(resolve_repo_path "\$GNU_CACHE_DIR"\)$/ { exit }
      { print }
    ' "${SCRIPTS_DIR}/gnu-test.sh"
  )
}

setup() {
	TEST_TEMP_DIR="$(mktemp -d)"
	WORKDIR="${TEST_TEMP_DIR}/workdir"
	mkdir -p "${WORKDIR}/build-aux"
	(
		cd "${REPO_ROOT}" &&
			go build -o "${GBASH_BIN}" ./cmd/gbash/
	)

	cat > "${WORKDIR}/build-aux/gen-lists-of-programs.sh" <<'EOF'
#!/bin/sh
set -eu

if [ "${1-}" != "--list-progs" ]; then
  echo "unexpected args: $*" >&2
  exit 1
fi

printf '%s\n' '[' echo false printf pwd test true
EOF
  chmod +x "${WORKDIR}/build-aux/gen-lists-of-programs.sh"

  source_gnu_test_functions
  write_wrappers "${WORKDIR}" "${GBASH_BIN}"
}

teardown() {
  rm -rf "${TEST_TEMP_DIR}"
}

@test "gnu wrapper generation disables builtin-shadowed commands" {
  run grep -F '["|echo|false|printf|pwd|test|true)' "${WORKDIR}/src/bash"
  [ "$status" -eq 0 ]

  run grep -F 'enable -n "$jbgo_builtin_"' "${WORKDIR}/src/bash"
  [ "$status" -eq 0 ]

  run grep -F 'GBASH_DISABLED_BUILTINS="$jbgo_disabled_builtins" exec "/bin/bash" "$@"' "${WORKDIR}/src/bash"
  [ "$status" -eq 0 ]

  run grep -F '["|echo|false|printf|pwd|test|true)' "${WORKDIR}/build-aux/gbash-harness/relink.sh"
  [ "$status" -eq 0 ]

  run grep -F 'enable -n "$jbgo_builtin_"' "${WORKDIR}/build-aux/gbash-harness/relink.sh"
  [ "$status" -eq 0 ]

  run grep -F 'GBASH_DISABLED_BUILTINS=\"\$jbgo_disabled_builtins\" exec \"/bin/$name\" \"\$@\"' "${WORKDIR}/build-aux/gbash-harness/relink.sh"
  [ "$status" -eq 0 ]
}

@test "nested gnu shell wrappers use external commands after disabling builtins" {
  run env PATH=/src:/bin:/usr/bin "${WORKDIR}/src/sh" -c \
    "PATH=/src:/bin:/usr/bin; export PATH; command -v sh; foo=old; printf -v foo %s hi; printf '\\nparent=<%s>\\n' \"\$foo\"; sh -c 'command -v sh; foo=old; printf -v foo %s hi; printf \"\\\\nchild=<%s>\\\\n\" \"\$foo\"; command -v [ echo false printf pwd test true'"

  [ "$status" -eq 0 ]
  [[ "$output" == *"warning: ignoring excess arguments, starting with 'foo'"* ]]
  [[ "$output" == *$'/src/sh\n'* ]]
  [[ "$output" == *$'parent=<old>\n'* ]]
  [[ "$output" == *$'child=<old>\n'* ]]
  [[ "$output" == *'/src/['* ]]
  [[ "$output" == *'/src/echo'* ]]
  [[ "$output" == *'/src/false'* ]]
  [[ "$output" == *'/src/printf'* ]]
  [[ "$output" == *'/src/pwd'* ]]
  [[ "$output" == *'/src/test'* ]]
  [[ "$output" == *'/src/true'* ]]
}

@test "nested gnu shell wrappers disable builtins for script execution" {
  cat > "${WORKDIR}/nested.sh" <<'EOF'
foo=old
printf -v foo %s hi
printf '\nchild-file=<%s>\n' "$foo"
command -v [ echo false printf pwd test true
EOF

  run env PATH=/src:/bin:/usr/bin "${WORKDIR}/src/sh" -c \
    "PATH=/src:/bin:/usr/bin; export PATH; sh /nested.sh"

  [ "$status" -eq 0 ]
  [[ "$output" == *"warning: ignoring excess arguments, starting with 'foo'"* ]]
  [[ "$output" == *$'child-file=<old>\n'* ]]
  [[ "$output" == *'/src/['* ]]
  [[ "$output" == *'/src/echo'* ]]
  [[ "$output" == *'/src/false'* ]]
  [[ "$output" == *'/src/printf'* ]]
  [[ "$output" == *'/src/pwd'* ]]
  [[ "$output" == *'/src/test'* ]]
  [[ "$output" == *'/src/true'* ]]
}
