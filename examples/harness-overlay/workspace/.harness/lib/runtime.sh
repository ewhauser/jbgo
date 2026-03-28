#!/usr/bin/env bash

_GBASH_HARNESS_SOURCES=()

harness_log() {
  echo "[harness] $*" >> "${HARNESS_LOG}"
}

harness_die() {
  echo "harness: $*" >&2
  exit 1
}

harness_ensure_source_array() {
  if (( ${#_GBASH_HARNESS_SOURCES[@]} == 0 )) && [[ -n "${HARNESS_SOURCES:-}" ]]; then
    IFS=':' read -r -a _GBASH_HARNESS_SOURCES <<< "${HARNESS_SOURCES}"
  fi
}

harness_call() {
  local stage="$1"
  shift || true

  harness_ensure_source_array

  declare -A hook_map=()
  local src f
  for src in "${_GBASH_HARNESS_SOURCES[@]}"; do
    [[ -d "${src}/hooks.d/${stage}" ]] || continue
    for f in "${src}/hooks.d/${stage}"/*; do
      [[ -x "${f}" ]] && hook_map["$(basename "${f}")"]="${f}"
    done
  done

  local current=""
  current="$(cat)"
  (( ${#hook_map[@]} )) || {
    printf '%s' "${current}"
    return 0
  }

  export HARNESS_SESSION="${SESSION_DIR:-}" HARNESS_STAGE="${stage}"

  local base
  while IFS= read -r base; do
    current="$(echo "${current}" | "${hook_map[$base]}" "$@" 2>>"${HARNESS_LOG}")" || {
      local rc=$?
      harness_log "hook ${hook_map[$base]} exited ${rc} during ${stage}"
      printf '%s' "${current}"
      return "${rc}"
    }
  done < <(printf '%s\n' "${!hook_map[@]}" | sort)

  printf '%s' "${current}"
}

harness_refresh_sources() {
  _GBASH_HARNESS_SOURCES=()

  local d
  for d in "${HARNESS_ROOT}/plugins"/*/; do
    [[ -d "${d}" ]] && _GBASH_HARNESS_SOURCES+=("${d%/}")
  done
  [[ -d "${HARNESS_HOME}" ]] && _GBASH_HARNESS_SOURCES+=("${HARNESS_HOME}")

  local result
  result="$(echo '{}' | harness_call sources)" || return
  mapfile -t _GBASH_HARNESS_SOURCES < <(echo "${result}" | jq -r '.sources[]')

  local IFS=':'
  export HARNESS_SOURCES="${_GBASH_HARNESS_SOURCES[*]}"
}

harness_find_command() {
  local target="$1"
  local result=""
  local src
  for src in "${_GBASH_HARNESS_SOURCES[@]}"; do
    [[ -x "${src}/commands/${target}" ]] && result="${src}/commands/${target}"
  done
  echo "${result}"
}

harness_next_seq() {
  local dir="$1"
  local last
  last="$(ls -1 "${dir}/messages/" 2>/dev/null | sort -n | tail -1)"
  if [[ -z "${last}" ]]; then
    echo "0001"
  else
    printf '%04d' $(( 10#${last%%-*} + 1 ))
  fi
}

harness_new_session() {
  local id
  id="$(date +%Y%m%d-%H%M%S)-$$"
  local dir="${HARNESS_SESSIONS}/${id}"
  mkdir -p "${dir}/messages"
  cat > "${dir}/session.md" <<EOF
---
id: ${id}
model: ${HARNESS_MODEL}
provider: ${HARNESS_PROVIDER}
created: $(date -Iseconds)
cwd: ${PWD}
---
EOF
  echo "${dir}"
}

harness_save_user_message() {
  local dir="$1"
  local content="$2"
  local seq
  seq="$(harness_next_seq "${dir}")"
  cat > "${dir}/messages/${seq}-user.md" <<EOF
---
role: user
seq: ${seq}
timestamp: $(date -Iseconds)
---
${content}
EOF
}

harness_agent_loop() {
  local session_dir="$1"
  export SESSION_DIR="${session_dir}"

  local state="start"
  local context='{}'
  local result=""
  local rc=0
  local iterations=0

  while [[ -n "${state}" ]]; do
    harness_refresh_sources
    result="$(echo "${context}" | harness_call "${state}")" && rc=0 || rc=$?

    if (( rc != 0 )); then
      if [[ "${state}" == "error" ]]; then
        break
      fi
      state="error"
      context="${result}"
      continue
    fi

    state="$(echo "${result}" | jq -r '.next_state // empty' 2>/dev/null)" || true
    context="$(echo "${result}" | jq -c 'del(.next_state)' 2>/dev/null)" || context='{}'

    (( ++iterations > HARNESS_MAX_TURNS * 3 )) && {
      harness_log "safety: exceeded max iterations"
      break
    }
  done

  local output
  output="$(echo "${result:-}" | jq -r '.output // empty' 2>/dev/null)" || true
  [[ -n "${output}" ]] && printf '%s\n' "${output}"
}
