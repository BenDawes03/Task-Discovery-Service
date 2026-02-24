#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

: "${PCTRBO_DB_DSN:=postgres://PCTRService:password@localhost:5432/PCTRService?sslmode=disable}"
: "${PCTRBO_LISTEN:=:9102}"
: "${PCTRBO_PROXY:=localhost:5100}"
: "${PCTRBO_PROXY_PROTO:=udp}"
: "${PCTRBO_TASK:=sim.pctrbo}"
: "${PCTRBO_ADVERTISE:=http://192.168.100.12:9102}"
: "${PCTRBO_PA_TASK:=sim.pa}"
: "${PCTRBO_PA_BASE:=}"

cd "${REPO_ROOT}"
export PCTRBO_DB_DSN

args=(
  -listen "${PCTRBO_LISTEN}"
  -proxy "${PCTRBO_PROXY}"
  -proxy-proto "${PCTRBO_PROXY_PROTO}"
  -task "${PCTRBO_TASK}"
  -pa-task "${PCTRBO_PA_TASK}"
)
if [[ -n "${PCTRBO_ADVERTISE}" ]]; then
  args+=( -advertise "${PCTRBO_ADVERTISE}" )
fi
if [[ -n "${PCTRBO_PA_BASE}" ]]; then
  args+=( -pa "${PCTRBO_PA_BASE}" )
fi

exec go run ./Simulation/pctrbo "${args[@]}" "$@"
