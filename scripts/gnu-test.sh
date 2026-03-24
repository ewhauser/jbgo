#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_ROOT=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

GNU_CACHE_DIR=${GNU_CACHE_DIR:-"$REPO_ROOT/.cache/gnu"}
GNU_RESULTS_DIR=${GNU_RESULTS_DIR:-"$REPO_ROOT/.cache/gnu/results/docker-latest"}
GNU_GBASH_BIN=${GNU_GBASH_BIN:-"$GNU_CACHE_DIR/bin/gbash"}
GNU_SOURCE_DIR=${GNU_SOURCE_DIR:-/opt/gnu/coreutils-9.10}

resolve_repo_path() {
  local path=$1
  if [[ "$path" = /* ]]; then
    printf '%s\n' "$path"
    return
  fi
  printf '%s/%s\n' "$REPO_ROOT" "$path"
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required tool: $1" >&2
    exit 1
  }
}

shell_quote() {
  local value=${1-}
  value=${value//\'/\'\"\'\"\'}
  printf "'%s'" "$value"
}

prepare_workdir() {
  local workdir
  workdir=$(mktemp -d "${TMPDIR:-/tmp}/gbash-gnu-work-XXXXXX")

  mkdir -p "$workdir"
  cp -a "$GNU_SOURCE_DIR"/. "$workdir"/
  relocate_workdir "$workdir"
  printf '%s\n' "$workdir"
}

preserve_workdir() {
  local source_dir=$1
  local preserved_dir=$GNU_RESULTS_DIR/workdir
  rm -rf "$preserved_dir"
  mv "$source_dir" "$preserved_dir"
  printf '%s\n' "$preserved_dir"
}

relocate_workdir() {
  local workdir=$1
  python3 - "$workdir" <<'PY'
import os
import pathlib
import stat
import sys

workdir = pathlib.Path(sys.argv[1])
config_status = workdir / "config.status"
if not config_status.exists():
    sys.exit(0)

original = None
for line in config_status.read_text(encoding="utf-8", errors="replace").splitlines():
    if line.startswith("ac_pwd='") and line.endswith("'"):
        original = line[len("ac_pwd='"):-1]
        break

if not original or original == str(workdir):
    sys.exit(0)

needle = original.encode()
replacement = str(workdir).encode()

for path in workdir.rglob("*"):
    if path.is_dir() or path.is_symlink():
        continue
    try:
        data = path.read_bytes()
    except OSError:
        continue
    if b"\0" in data or needle not in data:
        continue
    st = path.stat()
    path.write_bytes(data.replace(needle, replacement))
    os.utime(path, ns=(st.st_atime_ns, st.st_mtime_ns))
    os.chmod(path, stat.S_IMODE(st.st_mode))
PY
}

write_launcher() {
  local workdir=$1
  local gbash_bin=$2
  local hook_dir=$workdir/build-aux/gbash-harness
  mkdir -p "$hook_dir"
  cat > "$hook_dir/gbash" <<EOF
#!/bin/sh
set -eu

exec $(shell_quote "$gbash_bin") "\$@"
EOF
  chmod 755 "$hook_dir/gbash"
}

write_wrapper() {
  local src_dir=$1
  local name=$2
  local command_name=${3-}
  local path=$src_dir/$name
  rm -rf "$path"
  cat > "$path" <<EOF
#!/bin/sh
set -eu

script_path=\$0
case "\$script_path" in
  */*) script_dir=\$(CDPATH= cd -- "\${script_path%/*}" && pwd -P) ;;
  *) script_dir=\$(pwd -P) ;;
esac
root_dir=\$(CDPATH= cd -- "\$script_dir/.." && pwd -P)
host_pwd=\${PWD-}
if [ -n "\$host_pwd" ]; then
  case "\$host_pwd" in
    "\$root_dir"|"\$root_dir"/*) : ;;
    *) host_pwd= ;;
  esac
fi
if [ -z "\$host_pwd" ]; then
  host_pwd=\$(pwd -P)
fi
sandbox_cwd=/
case "\$host_pwd" in
  "\$root_dir") ;;
  "\$root_dir"/*) sandbox_cwd="/\${host_pwd#"\$root_dir"/}" ;;
esac
gbash_bin=\$root_dir/build-aux/gbash-harness/gbash
gnu_disabled_builtins() {
  jbgo_programs_file=\$root_dir/build-aux/gbash-harness/gnu-programs.txt
  [ -f "\$jbgo_programs_file" ] || return 0
  while IFS= read -r jbgo_name || [ -n "\$jbgo_name" ]; do
    case "\$jbgo_name" in
      "["|echo|false|printf|pwd|test|true)
        printf '%s\n' "\$jbgo_name"
        ;;
    esac
  done < "\$jbgo_programs_file"
}
PWD=\$sandbox_cwd
GBASH_UMASK=\$(umask)
export GBASH_UMASK
export PWD
GBASH_UMASK=\$(umask)
jbgo_disabled_builtins=\$(gnu_disabled_builtins)
EOF
  if [[ -z "$command_name" ]]; then
    cat >> "$path" <<EOF
if [ -e /build-aux/gbash-harness/gnu-programs.txt ]; then
  if [ "\${1-}" = "-c" ]; then
    jbgo_command_string=\${2-}
    shift 2
    if [ \$# -gt 0 ]; then
      jbgo_shell_name=\$1
      shift
    else
    jbgo_shell_name=$name
  fi
  jbgo_enable_prefix='if [ -n "\$1" ]; then for jbgo_builtin_ in \$1; do enable -n "\$jbgo_builtin_"; done; fi; shift; '
  exec "/bin/$name" -c "\$jbgo_enable_prefix\$jbgo_command_string" "\$jbgo_shell_name" "\$jbgo_disabled_builtins" "\$@"
  fi
  GBASH_DISABLED_BUILTINS="\$jbgo_disabled_builtins" exec "/bin/$name" "\$@"
fi

if [ "\${1-}" = "-c" ]; then
  jbgo_command_string=\${2-}
  shift 2
  if [ \$# -gt 0 ]; then
    jbgo_shell_name=\$1
    shift
  else
    jbgo_shell_name=$name
  fi
  if [ -n "\${POSIXLY_CORRECT-}" ]; then
    export POSIXLY_CORRECT
  else
    unset POSIXLY_CORRECT
  fi
  jbgo_enable_prefix='if [ -n "\$1" ]; then for jbgo_builtin_ in \$1; do enable -n "\$jbgo_builtin_"; done; fi; shift; '
  exec "\$gbash_bin" --readwrite-root "\$root_dir" --cwd "\$sandbox_cwd" -c "\$jbgo_enable_prefix\$jbgo_command_string" "\$jbgo_shell_name" "\$jbgo_disabled_builtins" "\$@"
fi
EOF
  else
    cat >> "$path" <<EOF
if [ -e /build-aux/gbash-harness/gnu-programs.txt ]; then
  exec "/bin/$command_name" "\$@"
fi
EOF
  fi
  if [[ -n "$command_name" ]]; then
    printf '%s\n' "exec \"\$gbash_bin\" --readwrite-root \"\$root_dir\" --cwd \"\$sandbox_cwd\" -c 'GBASH_UMASK=\$1; export GBASH_UMASK; POSIXLY_CORRECT=\$2; if [ -n \"\$POSIXLY_CORRECT\" ]; then export POSIXLY_CORRECT; else unset POSIXLY_CORRECT; fi; jbgo_disabled_builtins_=\$3; shift 3; if [ -n \"\$jbgo_disabled_builtins_\" ]; then for jbgo_builtin_ in \$jbgo_disabled_builtins_; do enable -n \"\$jbgo_builtin_\"; done; fi; exec \"\$@\"' _ \"\$GBASH_UMASK\" \"\${POSIXLY_CORRECT-}\" \"\$jbgo_disabled_builtins\" $command_name \"\$@\"" >> "$path"
  else
    printf '%s\n' 'exec "$gbash_bin" --readwrite-root "$root_dir" --cwd "$sandbox_cwd" -c '\''GBASH_UMASK=$1; export GBASH_UMASK; POSIXLY_CORRECT=$2; if [ -n "$POSIXLY_CORRECT" ]; then export POSIXLY_CORRECT; else unset POSIXLY_CORRECT; fi; jbgo_disabled_builtins_=$3; shift 3; if [ -n "$jbgo_disabled_builtins_" ]; then for jbgo_builtin_ in $jbgo_disabled_builtins_; do enable -n "$jbgo_builtin_"; done; fi; exec "$@"'\'' _ "$GBASH_UMASK" "${POSIXLY_CORRECT-}" "$jbgo_disabled_builtins" "$@"' >> "$path"
  fi
  chmod 755 "$path"
}

write_relink_script() {
  local workdir=$1
  local hook_dir=$workdir/build-aux/gbash-harness
  mkdir -p "$hook_dir"
cat > "$hook_dir/relink.sh" <<'EOF'
#!/bin/sh
set -eu

src_dir=$1
script_path=$0
case "$script_path" in
  */*) script_dir=$(CDPATH= cd -- "${script_path%/*}" && pwd -P) ;;
  *) script_dir=$(pwd -P) ;;
esac

write_wrapper() {
  name=$1
  command_name=${2-}
  path=$src_dir/$name

  rm -rf "$path"
  {
    printf '%s\n' '#!/bin/sh'
    printf '%s\n' 'set -eu'
    printf '\n'
    printf '%s\n' 'script_path=$0'
    printf '%s\n' 'case "$script_path" in'
    printf '%s\n' '  */*) script_dir=$(CDPATH= cd -- "${script_path%/*}" && pwd -P) ;;'
    printf '%s\n' '  *) script_dir=$(pwd -P) ;;'
    printf '%s\n' 'esac'
    printf '%s\n' 'root_dir=$(CDPATH= cd -- "$script_dir/.." && pwd -P)'
    printf '%s\n' 'host_pwd=${PWD-}'
    printf '%s\n' 'if [ -n "$host_pwd" ]; then'
    printf '%s\n' '  case "$host_pwd" in'
    printf '%s\n' '    "$root_dir"|"$root_dir"/*) : ;;'
    printf '%s\n' '    *) host_pwd= ;;'
    printf '%s\n' '  esac'
    printf '%s\n' 'fi'
    printf '%s\n' 'if [ -z "$host_pwd" ]; then'
    printf '%s\n' '  host_pwd=$(pwd -P)'
    printf '%s\n' 'fi'
    printf '%s\n' 'sandbox_cwd=/'
    printf '%s\n' 'case "$host_pwd" in'
    printf '%s\n' '  "$root_dir") ;;'
    printf '%s\n' '  "$root_dir"/*) sandbox_cwd="/${host_pwd#"$root_dir"/}" ;;'
    printf '%s\n' 'esac'
    printf '%s\n' 'gbash_bin=$root_dir/build-aux/gbash-harness/gbash'
    printf '%s\n' 'gnu_disabled_builtins() {'
    printf '%s\n' '  jbgo_programs_file=$root_dir/build-aux/gbash-harness/gnu-programs.txt'
    printf '%s\n' '  [ -f "$jbgo_programs_file" ] || return 0'
    printf '%s\n' '  while IFS= read -r jbgo_name || [ -n "$jbgo_name" ]; do'
    printf '%s\n' '    case "$jbgo_name" in'
    printf '%s\n' '      "["|echo|false|printf|pwd|test|true)'
    printf '%s\n' '        printf "%s\n" "$jbgo_name"'
    printf '%s\n' '        ;;'
    printf '%s\n' '    esac'
    printf '%s\n' '  done < "$jbgo_programs_file"'
    printf '%s\n' '}'
    printf '%s\n' 'PWD=$sandbox_cwd'
    printf '%s\n' 'GBASH_UMASK=$(umask)'
    printf '%s\n' 'export GBASH_UMASK'
    printf '%s\n' 'export PWD'
    printf '%s\n' 'GBASH_UMASK=$(umask)'
    printf '%s\n' 'jbgo_disabled_builtins=$(gnu_disabled_builtins)'
    if [ -z "$command_name" ]; then
      printf '%s\n' 'if [ -e /build-aux/gbash-harness/gnu-programs.txt ]; then'
      printf '%s\n' '  if [ "${1-}" = "-c" ]; then'
      printf '%s\n' '    jbgo_command_string=${2-}'
      printf '%s\n' '    shift 2'
      printf '%s\n' '    if [ $# -gt 0 ]; then'
      printf '%s\n' '      jbgo_shell_name=$1'
      printf '%s\n' '      shift'
      printf '%s\n' '    else'
      printf '%s\n' "      jbgo_shell_name=$name"
      printf '%s\n' '    fi'
      printf '%s\n' '    jbgo_enable_prefix='\''if [ -n "$1" ]; then for jbgo_builtin_ in $1; do enable -n "$jbgo_builtin_"; done; fi; shift; '\'''
      printf '%s\n' "    exec \"/bin/$name\" -c \"\$jbgo_enable_prefix\$jbgo_command_string\" \"\$jbgo_shell_name\" \"\$jbgo_disabled_builtins\" \"\$@\""
      printf '%s\n' '  fi'
      printf '%s\n' "  GBASH_DISABLED_BUILTINS=\"\$jbgo_disabled_builtins\" exec \"/bin/$name\" \"\$@\""
      printf '%s\n' 'fi'

      printf '%s\n' 'if [ "${1-}" = "-c" ]; then'
      printf '%s\n' '  jbgo_command_string=${2-}'
      printf '%s\n' '  shift 2'
      printf '%s\n' '  if [ $# -gt 0 ]; then'
      printf '%s\n' '    jbgo_shell_name=$1'
      printf '%s\n' '    shift'
      printf '%s\n' '  else'
      printf '%s\n' "    jbgo_shell_name=$name"
      printf '%s\n' '  fi'
      printf '%s\n' '  if [ -n "${POSIXLY_CORRECT-}" ]; then'
      printf '%s\n' '    export POSIXLY_CORRECT'
      printf '%s\n' '  else'
      printf '%s\n' '    unset POSIXLY_CORRECT'
      printf '%s\n' '  fi'
      printf '%s\n' '  jbgo_enable_prefix='\''if [ -n "$1" ]; then for jbgo_builtin_ in $1; do enable -n "$jbgo_builtin_"; done; fi; shift; '\'''
      printf '%s\n' '  exec "$gbash_bin" --readwrite-root "$root_dir" --cwd "$sandbox_cwd" -c "$jbgo_enable_prefix$jbgo_command_string" "$jbgo_shell_name" "$jbgo_disabled_builtins" "$@"'
      printf '%s\n' 'fi'
    else
      printf '%s\n' 'if [ -e /build-aux/gbash-harness/gnu-programs.txt ]; then'
      printf '%s\n' "  exec \"/bin/$command_name\" \"\$@\""
      printf '%s\n' 'fi'
    fi
    if [ -n "$command_name" ]; then
      printf '%s\n' "exec \"\$gbash_bin\" --readwrite-root \"\$root_dir\" --cwd \"\$sandbox_cwd\" -c 'GBASH_UMASK=\$1; export GBASH_UMASK; POSIXLY_CORRECT=\$2; if [ -n \"\$POSIXLY_CORRECT\" ]; then export POSIXLY_CORRECT; else unset POSIXLY_CORRECT; fi; jbgo_disabled_builtins_=\$3; shift 3; if [ -n \"\$jbgo_disabled_builtins_\" ]; then for jbgo_builtin_ in \$jbgo_disabled_builtins_; do enable -n \"\$jbgo_builtin_\"; done; fi; exec \"\$@\"' _ \"\$GBASH_UMASK\" \"\${POSIXLY_CORRECT-}\" \"\$jbgo_disabled_builtins\" $command_name \"\$@\""
    else
      printf '%s\n' 'exec "$gbash_bin" --readwrite-root "$root_dir" --cwd "$sandbox_cwd" -c '\''GBASH_UMASK=$1; export GBASH_UMASK; POSIXLY_CORRECT=$2; if [ -n "$POSIXLY_CORRECT" ]; then export POSIXLY_CORRECT; else unset POSIXLY_CORRECT; fi; jbgo_disabled_builtins_=$3; shift 3; if [ -n "$jbgo_disabled_builtins_" ]; then for jbgo_builtin_ in $jbgo_disabled_builtins_; do enable -n "$jbgo_builtin_"; done; fi; exec "$@"'\'' _ "$GBASH_UMASK" "${POSIXLY_CORRECT-}" "$jbgo_disabled_builtins" "$@"'
    fi
  } > "$path"
  chmod 755 "$path"
}

while IFS= read -r name || [ -n "$name" ]; do
  [ -n "$name" ] || continue
  write_wrapper "$name" "$name"
done < "$script_dir/gnu-programs.txt"

write_wrapper bash
write_wrapper sh
write_wrapper ginstall install
EOF
  chmod 755 "$hook_dir/relink.sh"
}

write_wrappers() {
  local workdir=$1
  local gbash_bin=$2
  local src_dir=$workdir/src
  local hook_dir=$workdir/build-aux/gbash-harness
  local programs
  mkdir -p "$src_dir" "$hook_dir"
  programs=$("$workdir/build-aux/gen-lists-of-programs.sh" --list-progs)
  printf '%s\n' $programs | sort -u > "$hook_dir/gnu-programs.txt"

  while IFS= read -r name || [ -n "$name" ]; do
    [[ -n "$name" ]] || continue
    write_wrapper "$src_dir" "$name" "$name"
  done < "$hook_dir/gnu-programs.txt"

  write_wrapper "$src_dir" bash
  write_wrapper "$src_dir" sh
  write_wrapper "$src_dir" ginstall install
  write_launcher "$workdir" "$gbash_bin"
  write_relink_script "$workdir"
}

patch_makefile() {
  local workdir=$1
  python3 - "$workdir/Makefile" <<'PY'
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
contents = path.read_text(encoding="utf-8")
needle = "TESTS_ENVIRONMENT = \\\n"
insert = "TESTS_ENVIRONMENT = \\\n  $(SHELL) '$(abs_top_builddir)/build-aux/gbash-harness/relink.sh' '$(abs_top_builddir)/src' || exit $$?; \\\n"
if "gbash-harness/relink.sh" in contents:
    sys.exit(0)
updated = contents.replace(needle, insert, 1)
if updated == contents:
    raise SystemExit(f"TESTS_ENVIRONMENT declaration not found in {path}")
path.write_text(updated, encoding="utf-8")
PY
}

disable_check_rebuild() {
  local workdir=$1
  python3 - "$workdir/Makefile" <<'PY'
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
contents = path.read_text(encoding="utf-8")
updated = contents.replace("check-am: all-am", "check-am:", 1)
if updated != contents:
    path.write_text(updated, encoding="utf-8")
PY
}

patch_init_sh() {
  local workdir=$1
  python3 - "$workdir/tests/init.sh" <<'PY'
import pathlib
import sys

path = pathlib.Path(sys.argv[1])
contents = path.read_text(encoding="utf-8")
needle = """setup_ "$@"
# This trap is here, rather than in the setup_ function, because some
# shells run the exit trap at shell function exit, rather than script exit.
trap remove_tmp_ EXIT
"""
replacement = """jbgo_path_before_setup_=$PATH
if test "${abs_top_builddir+set}" = set; then
  jbgo_src_dir_=$abs_top_builddir/src
  case $PATH in
    "$jbgo_src_dir_$PATH_SEPARATOR"*) PATH=${PATH#"$jbgo_src_dir_$PATH_SEPARATOR"} ;;
    "$jbgo_src_dir_") PATH= ;;
  esac
  export PATH
fi

setup_ "$@"
PATH=$jbgo_path_before_setup_
export PATH

jbgo_host_chmod_=/bin/chmod
test -x "$jbgo_host_chmod_" || jbgo_host_chmod_=chmod
jbgo_host_rm_=/bin/rm
test -x "$jbgo_host_rm_" || jbgo_host_rm_=rm
jbgo_host_sleep_=/bin/sleep
test -x "$jbgo_host_sleep_" || jbgo_host_sleep_=sleep

remove_tmp_ ()
{
  __st=$?
  cleanup_
  if test "$KEEP" = yes; then
    echo "Not removing temporary directory $test_dir_"
  else
    cd "$initial_cwd_" || cd / || cd /tmp
    if test -e "$test_dir_"; then
      "$jbgo_host_chmod_" -R u+rwx "$test_dir_" 2>/dev/null || :
    fi
    "$jbgo_host_rm_" -rf "$test_dir_" 2>/dev/null \
      || { "$jbgo_host_sleep_" 1 && "$jbgo_host_rm_" -rf "$test_dir_"; } \
      || { test $__st = 0 && __st=1; }
  fi
  exit $__st
}
# This trap is here, rather than in the setup_ function, because some
# shells run the exit trap at shell function exit, rather than script exit.
trap remove_tmp_ EXIT
"""
if "jbgo_path_before_setup_=$PATH" in contents:
    sys.exit(0)
updated = contents.replace(needle, replacement, 1)
if updated == contents:
    raise SystemExit(f"tests/init.sh setup block not found in {path}")
path.write_text(updated, encoding="utf-8")
PY
}

test_base_from_path() {
  local test_path=$1
  case "$test_path" in
    *.pl|*.sh|*.xpl) printf '%s\n' "${test_path%.*}" ;;
    *) printf '%s\n' "$test_path" ;;
  esac
}

status_from_trs() {
  local trs_path=$1
  if [[ ! -f "$trs_path" ]]; then
    return 1
  fi
  awk -F': ' '/^:test-result:/ { print $2; exit }' "$trs_path"
}

run_make_check() {
  local workdir=$1
  local log_path=$2
  shift 2
  local tests=("$@")
  local config_shell=$workdir/src/bash
  local overall_status=0

  local per_test_timeout=${GNU_TEST_TIMEOUT:-120}

  : > "$log_path"
  for test_path in "${tests[@]}"; do
    local test_base log_target trs_path make_status status
    test_base=$(test_base_from_path "$test_path")
    log_target="$test_base.log"
    trs_path="$workdir/$test_base.trs"
    rm -f "$workdir/$log_target" "$trs_path"

    printf '>>> running: %s\n' "$test_path" >&2

    make_status=0
    if (
      cd "$workdir"
      if [[ "$per_test_timeout" -gt 0 ]] && command -v timeout >/dev/null 2>&1; then
        env CONFIG_SHELL="$config_shell" timeout "${per_test_timeout}s" make "$log_target" VERBOSE=no "srcdir=$workdir"
      elif command -v setsid >/dev/null 2>&1; then
        env CONFIG_SHELL="$config_shell" setsid make "$log_target" VERBOSE=no "srcdir=$workdir"
      else
        env CONFIG_SHELL="$config_shell" make "$log_target" VERBOSE=no "srcdir=$workdir"
      fi
    ) >>"$log_path" 2>&1; then
      make_status=0
    else
      make_status=$?
    fi

    status=$(status_from_trs "$trs_path" || true)
    if [[ -z "$status" ]]; then
      status=ERROR
    fi
    printf '%s: %s\n' "$status" "$test_path" >> "$log_path"

    if [[ $make_status -ne 0 ]]; then
      overall_status=$make_status
      continue
    fi
    case "$status" in
      PASS|SKIP|XFAIL) ;;
      *) overall_status=1 ;;
    esac
  done

  return "$overall_status"
}

GNU_CACHE_DIR=$(resolve_repo_path "$GNU_CACHE_DIR")
GNU_RESULTS_DIR=$(resolve_repo_path "$GNU_RESULTS_DIR")
GNU_GBASH_BIN=$(resolve_repo_path "$GNU_GBASH_BIN")

require_tool go
require_tool make
require_tool perl
require_tool python3

if [[ ! -d "$GNU_SOURCE_DIR" ]]; then
  echo "prepared GNU source tree not found: $GNU_SOURCE_DIR" >&2
  exit 1
fi

rm -rf "$GNU_RESULTS_DIR"
mkdir -p "$GNU_RESULTS_DIR" "$(dirname "$GNU_GBASH_BIN")"

go build -o "$GNU_GBASH_BIN" ./cmd/gbash

workdir=$(prepare_workdir)
cleanup() {
  if [[ "${GNU_KEEP_WORKDIR:-0}" != "1" && -n "${workdir:-}" && -d "$workdir" ]]; then
    rm -rf "$workdir"
  fi
}
trap cleanup EXIT

write_wrappers "$workdir" "$GNU_GBASH_BIN"
disable_check_rebuild "$workdir"
patch_makefile "$workdir"
patch_init_sh "$workdir"

selected_tests=()
if mapfile -t selected_tests < <(go run ./cmd/gbash-gnu --workdir "$workdir" --utils "${GNU_UTILS:-}" --tests "${GNU_TESTS:-}" --print-tests); then
  :
fi

log_path="$GNU_RESULTS_DIR/compat.log"
status=0
if ((${#selected_tests[@]} > 0)); then
  if run_make_check "$workdir" "$log_path" "${selected_tests[@]}"; then
    status=0
  else
    status=$?
  fi
else
  : > "$log_path"
fi

for test_path in "${selected_tests[@]}"; do
  test_base=$(test_base_from_path "$test_path")
  test_log="$workdir/$test_base.log"
  if [[ -f "$test_log" ]]; then
    dest_dir="$GNU_RESULTS_DIR/${test_base%/*}"
    mkdir -p "$dest_dir"
    cp "$test_log" "$dest_dir/"
  fi
done

if [[ "${GNU_KEEP_WORKDIR:-0}" == "1" ]]; then
  workdir=$(preserve_workdir "$workdir")
fi

summary_status=0
if go run ./cmd/gbash-gnu \
  --workdir "$workdir" \
  --utils "${GNU_UTILS:-}" \
  --tests "${GNU_TESTS:-}" \
  --results-dir "$GNU_RESULTS_DIR" \
  --log "$log_path" \
  --exit-code "$status"; then
  summary_status=0
else
  summary_status=$?
fi

if [[ -f "$GNU_RESULTS_DIR/summary.json" ]]; then
  go run ./scripts/compat-report --summary "$GNU_RESULTS_DIR/summary.json" --output "$GNU_RESULTS_DIR"
fi

echo ""
echo "results: $GNU_RESULTS_DIR"
if [[ -f "$GNU_RESULTS_DIR/index.html" ]]; then
  echo "report:  $GNU_RESULTS_DIR/index.html"
fi
if [[ "${GNU_KEEP_WORKDIR:-0}" == "1" ]]; then
  echo "workdir: $workdir"
fi

if [[ $summary_status -ne 0 ]]; then
  echo ""
  echo "failed tests:"
  for test_path in "${selected_tests[@]}"; do
    test_base=$(test_base_from_path "$test_path")
    test_log="$GNU_RESULTS_DIR/$test_base.log"
    if [[ -f "$test_log" ]] && grep -q "^FAIL: $test_path\$" "$log_path"; then
      echo "  $test_path -> $test_log"
    fi
  done
fi

exit "$summary_status"
