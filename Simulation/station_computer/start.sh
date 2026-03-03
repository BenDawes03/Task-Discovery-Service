#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

: "${STATION_LISTEN:=:9100}"
: "${STATION_ID:=1}"
: "${STATION_PROXY:=localhost:5100}"
: "${STATION_PROXY_PROTO:=udp}"
: "${STATION_TASK:=}"
: "${STATION_ADVERTISE:=}"
: "${STATION_OYBO_TASK:=sim.cs}"
: "${STATION_PCTRBO_TASK:=sim.pctrbo}"
: "${STATION_BATCH_SIZE:=5}"

cd "${REPO_ROOT}"

args=(
  -listen "${STATION_LISTEN}"
  -station-id "${STATION_ID}"
  -proxy "${STATION_PROXY}"
  -proxy-proto "${STATION_PROXY_PROTO}"
  -oybo-task "${STATION_OYBO_TASK}"
  -pctrbo-task "${STATION_PCTRBO_TASK}"
  -batch-size "${STATION_BATCH_SIZE}"
)
if [[ -n "${STATION_TASK}" ]]; then
  args+=( -task "${STATION_TASK}" )
fi
if [[ -n "${STATION_ADVERTISE}" ]]; then
  args+=( -advertise "${STATION_ADVERTISE}" )
fi

exec go run ./Simulation/station_computer "${args[@]}" "$@"
