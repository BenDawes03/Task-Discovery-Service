#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

: "${GATE_ID:=gate-1}"
: "${GATE_LISTEN:=:9200}"
: "${GATE_STATION_ID:=station-1}"
: "${GATE_PROXY:=localhost:5100}"
: "${GATE_PROXY_PROTO:=udp}"
: "${GATE_STATION_TASK:=}"
: "${GATE_OYBO_TASK:=sim.cs}"
: "${GATE_PA_TASK:=sim.pa}"
: "${GATE_STATION_BASE:=}"
: "${GATE_OYBO_BASE:=}"
: "${GATE_PA_BASE:=}"
: "${GATE_PCTR_PUBLIC_KEY:=${REPO_ROOT}/Simulation/gate/pctr_public.pem}"

cd "${REPO_ROOT}"
export PCTR_PUBLIC_KEY="${GATE_PCTR_PUBLIC_KEY}"

args=(
  -id "${GATE_ID}"
  -listen "${GATE_LISTEN}"
  -station-id "${GATE_STATION_ID}"
  -proxy "${GATE_PROXY}"
  -proxy-proto "${GATE_PROXY_PROTO}"
  -oybo-task "${GATE_OYBO_TASK}"
  -pa-task "${GATE_PA_TASK}"
  -pctr-public-key "${GATE_PCTR_PUBLIC_KEY}"
)
if [[ -n "${GATE_STATION_TASK}" ]]; then
  args+=( -station-task "${GATE_STATION_TASK}" )
fi
if [[ -n "${GATE_STATION_BASE}" ]]; then
  args+=( -station "${GATE_STATION_BASE}" )
fi
if [[ -n "${GATE_OYBO_BASE}" ]]; then
  args+=( -oybo "${GATE_OYBO_BASE}" )
fi
if [[ -n "${GATE_PA_BASE}" ]]; then
  args+=( -pa "${GATE_PA_BASE}" )
fi

exec go run ./Simulation/gate "${args[@]}" "$@"
