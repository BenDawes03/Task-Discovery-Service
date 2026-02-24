#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

: "${CS_DB_DSN:=postgres://CentralSystem:cs@localhost:5432/CentralSystem?sslmode=disable}"
: "${CS_LISTEN:=:9101}"
: "${CS_PROXY:=localhost:5100}"
: "${CS_PROXY_PROTO:=udp}"
: "${CS_TASK:=sim.cs}"

# Set CS_ADVERTISE to a reachable URL when running across VMs (example: http://192.168.100.10:9101)
: "${CS_ADVERTISE:=}"

cd "${REPO_ROOT}"
export CS_DB_DSN

args=(
  -listen "${CS_LISTEN}"
  -proxy "${CS_PROXY}"
  -proxy-proto "${CS_PROXY_PROTO}"
  -task "${CS_TASK}"
)
if [[ -n "${CS_ADVERTISE}" ]]; then
  args+=( -advertise "${CS_ADVERTISE}" )
fi

exec go run ./Simulation/cs "${args[@]}" "$@"
