#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  ./Simulation/tap_cards.sh --gate http://<gate-host>:<port> --from 10000 --to 19999 --count 500

Options:
  --gate URL        Gate base URL (e.g. http://192.168.100.14:9200)
  --from N          First card id (inclusive)
  --to N            Last card id (inclusive)
  --count N         Number of taps to send (unique per gate)
  --delay-ms N      Delay between taps (default 0)
  --state FILE      Persist tapped card IDs for this gate (default: ./tap_state_<host>_<port>.txt)
  --mod N           Optional partitioning: only tap cards where (id % N) == --mod-value
  --mod-value N     Partition value (default 0)

Notes:
  - Script guarantees it will not tap the same card twice on the same gate (per state file).
  - Run on two different gates by using different --gate URLs and (optionally) different --mod-value.
EOF
}

GATE_URL=""
FROM=""
TO=""
COUNT=""
DELAY_MS=0
STATE_FILE=""
MOD=""
MOD_VALUE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --gate) GATE_URL="$2"; shift 2;;
    --from) FROM="$2"; shift 2;;
    --to) TO="$2"; shift 2;;
    --count) COUNT="$2"; shift 2;;
    --delay-ms) DELAY_MS="$2"; shift 2;;
    --state) STATE_FILE="$2"; shift 2;;
    --mod) MOD="$2"; shift 2;;
    --mod-value) MOD_VALUE="$2"; shift 2;;
    -h|--help) usage; exit 0;;
    *) echo "unknown arg: $1"; usage; exit 2;;
  esac
done

if [[ -z "${GATE_URL}" || -z "${FROM}" || -z "${TO}" || -z "${COUNT}" ]]; then
  usage
  exit 2
fi

GATE_URL="${GATE_URL%/}"
TAP_URL="${GATE_URL}/tap"

hostport="${GATE_URL#http://}"
hostport="${hostport#https://}"
hostport="${hostport%%/*}"
safe_hostport="${hostport//[:\[\]]/_}"

if [[ -z "${STATE_FILE}" ]]; then
  STATE_FILE="./tap_state_${safe_hostport}.txt"
fi

touch "${STATE_FILE}"

tmp_all="$(mktemp)"
tmp_candidates="$(mktemp)"
tmp_pool="$(mktemp)"
cleanup() {
  rm -f "${tmp_all}" "${tmp_candidates}" "${tmp_pool}"
}
trap cleanup EXIT

# Build candidate list (optionally partitioned for multi-gate runs).
for ((i=FROM; i<=TO; i++)); do
  if [[ -n "${MOD}" ]]; then
    if (( i % MOD != MOD_VALUE )); then
      continue
    fi
  fi
  echo "$i" >>"${tmp_all}"
done

# Remove already-tapped cards for this gate (state file).
if [[ -s "${STATE_FILE}" ]]; then
  grep -Fvx -f "${STATE_FILE}" "${tmp_all}" >"${tmp_candidates}" || true
else
  cp "${tmp_all}" "${tmp_candidates}"
fi

available=$(wc -l <"${tmp_candidates}" | tr -d ' ')
if (( available <= 0 )); then
  echo "No available untapped cards in range (state file exhausted): ${STATE_FILE}" >&2
  exit 1
fi

if (( COUNT > available )); then
  echo "Requested count (${COUNT}) is greater than available unique cards (${available}); reduce --count or widen --from/--to." >&2
  exit 2
fi

# Randomize if possible.
if command -v shuf >/dev/null 2>&1; then
  shuf "${tmp_candidates}" | head -n "${COUNT}" >"${tmp_pool}"
else
  head -n "${COUNT}" "${tmp_candidates}" >"${tmp_pool}"
fi

sent=0
while IFS= read -r card; do
  # Send tap as plain text body.
  resp=$(curl -sS -X POST "${TAP_URL}" -H "Content-Type: text/plain" --data-raw "OY:${card}" || true)
  echo "${card} ${resp}"
  echo "${card}" >>"${STATE_FILE}"
  sent=$((sent+1))
  if (( DELAY_MS > 0 )); then
    # sleep supports fractional seconds
    sleep "0.$(printf '%03d' "${DELAY_MS}")"
  fi
done <"${tmp_pool}"

echo "done: gate=${GATE_URL} sent=${sent} state=${STATE_FILE}"
