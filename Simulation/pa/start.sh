#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

: "${PA_DB_DSN:=postgres://PaymentService:password@localhost:5432/PaymentService?sslmode=disable}"
: "${PA_LISTEN:=:9103}"
: "${PA_PROXY:=localhost:5100}"
: "${PA_PROXY_PROTO:=udp}"
: "${PA_TASK:=sim.pa}"
: "${PA_PRIVATE_KEY:=${REPO_ROOT}/pctr_private.pem}"
: "${PA_ADVERTISE:=http://192.168.100.11:9103}"
: "${PA_OUTDIR:=.}"
: "${PA_MIN_FUNDS_CENTS:=1}"

cd "${REPO_ROOT}"
export PA_DB_DSN
export PA_PRIVATE_KEY

args=(
  -listen "${PA_LISTEN}"
  -proxy "${PA_PROXY}"
  -proxy-proto "${PA_PROXY_PROTO}"
  -task "${PA_TASK}"
  -private-key "${PA_PRIVATE_KEY}"
  -outdir "${PA_OUTDIR}"
  -min-funds-cents "${PA_MIN_FUNDS_CENTS}"
)
if [[ -n "${PA_ADVERTISE}" ]]; then
  args+=( -advertise "${PA_ADVERTISE}" )
fi

exec go run ./Simulation/pa "${args[@]}" "$@"
