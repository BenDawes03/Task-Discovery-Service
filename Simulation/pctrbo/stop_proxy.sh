#!/usr/bin/env bash
set -euo pipefail

: "${STOP_WAIT_SECONDS:=5}"

kill_matching() {
  local label="$1"
  local pattern="$2"
  local pids

  pids="$(pgrep -f "${pattern}" || true)"
  if [[ -z "${pids}" ]]; then
    echo "No ${label} process found"
    return 0
  fi

  echo "Stopping ${label}: ${pids//$'\n'/, }"
  kill ${pids} || true
  sleep "${STOP_WAIT_SECONDS}"

  local remaining=()
  local pid
  for pid in ${pids}; do
    if kill -0 "${pid}" 2>/dev/null; then
      remaining+=("${pid}")
    fi
  done

  if (( ${#remaining[@]} > 0 )); then
    echo "Force stopping ${label}: ${remaining[*]}"
    kill -9 "${remaining[@]}" || true
  fi
}

kill_matching "client_proxy" '(^|/)client_proxy([[:space:]]|$)|go run .*cmd/client_proxy([[:space:]]|$)'
