#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage:
  # Generate a shared card list (run once on any machine):
  ./Simulation/tap_cards.sh --generate-cards-file cards_20.txt --from 10000 --to 19999 --count 20

  # Tap those cards ONCE on gate A (station 1):
  ./Simulation/tap_cards.sh --gate http://<gateA-host>:9200 --cards-file cards_20.txt --state gateA.state --require-allow

  # Tap the SAME cards ONCE on gate B (station 2):
  ./Simulation/tap_cards.sh --gate http://<gateB-host>:9200 --cards-file cards_20.txt --state gateB.state --require-allow

  # (CS will pair the 2 taps per card into journeys. Set CS -journey-interval smaller for faster tests.)

Options:
  --generate-cards-file FILE  Generate a file with unique card IDs and exit (no tapping)
  --gate URL        Gate base URL (e.g. http://192.168.100.14:9200)
  --cards CSV       Explicit card IDs (comma-separated), e.g. "10000,10001,10002"
  --cards-file FILE Card IDs (one per line)
  --from N          First card id (inclusive) (used with --to/--count when generating or sampling)
  --to N            Last card id (inclusive)
  --count N         Number of card IDs to generate/sample from the range
  --delay-ms N      Delay between taps (default 0)
  --state FILE      Persist tapped card IDs for this gate (default: ./tap_state_<host>_<port>.txt)
  --require-allow   Exit non-zero if any tap is denied by the gate
  --mod N           Optional partitioning: only tap cards where (id % N) == --mod-value
  --mod-value N     Partition value (default 0)

Notes:
  - Script guarantees it will not tap the same card twice on the same gate (per state file).
  - Run on two different gates by using different --gate URLs and (optionally) different --mod-value.
EOF
}

GENERATE_FILE=""
GATE_URL=""
CARDS_CSV=""
CARDS_FILE=""
FROM=""
TO=""
COUNT=""
DELAY_MS=0
STATE_FILE=""
REQUIRE_ALLOW=0
MOD=""
MOD_VALUE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --generate-cards-file) GENERATE_FILE="$2"; shift 2;;
    --gate) GATE_URL="$2"; shift 2;;
    --cards) CARDS_CSV="$2"; shift 2;;
    --cards-file) CARDS_FILE="$2"; shift 2;;
    --from) FROM="$2"; shift 2;;
    --to) TO="$2"; shift 2;;
    --count) COUNT="$2"; shift 2;;
    --delay-ms) DELAY_MS="$2"; shift 2;;
    --state) STATE_FILE="$2"; shift 2;;
    --require-allow) REQUIRE_ALLOW=1; shift 1;;
    --mod) MOD="$2"; shift 2;;
    --mod-value) MOD_VALUE="$2"; shift 2;;
    -h|--help) usage; exit 0;;
    *) echo "unknown arg: $1"; usage; exit 2;;
  esac
done

if [[ -n "${GENERATE_FILE}" ]]; then
  if [[ -z "${FROM}" || -z "${TO}" || -z "${COUNT}" ]]; then
    echo "--generate-cards-file requires --from --to --count" >&2
    usage
    exit 2
  fi

  tmp_all="$(mktemp)"
  tmp_pool="$(mktemp)"
  cleanup_gen() { rm -f "${tmp_all}" "${tmp_pool}"; }
  trap cleanup_gen EXIT

  : >"${tmp_all}"
  for ((i=FROM; i<=TO; i++)); do
    if [[ -n "${MOD}" ]]; then
      if (( i % MOD != MOD_VALUE )); then
        continue
      fi
    fi
    echo "$i" >>"${tmp_all}"
  done

  available=$(wc -l <"${tmp_all}" | tr -d ' ')
  if (( available <= 0 )); then
    echo "No cards available in range" >&2
    exit 1
  fi
  if (( COUNT > available )); then
    echo "Requested count (${COUNT}) is greater than available cards (${available}); adjust range/mod." >&2
    exit 2
  fi

  if command -v shuf >/dev/null 2>&1; then
    shuf "${tmp_all}" | head -n "${COUNT}" >"${tmp_pool}"
  else
    head -n "${COUNT}" "${tmp_all}" >"${tmp_pool}"
  fi
  mkdir -p "$(dirname "${GENERATE_FILE}")" 2>/dev/null || true
  cp "${tmp_pool}" "${GENERATE_FILE}"
  echo "generated ${COUNT} cards -> ${GENERATE_FILE}"
  exit 0
fi

if [[ -z "${GATE_URL}" ]]; then
  echo "--gate is required for tapping" >&2
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

build_from_sources() {
  : >"${tmp_all}"

  if [[ -n "${CARDS_CSV}" ]]; then
    IFS=',' read -r -a arr <<<"${CARDS_CSV}"
    for c in "${arr[@]}"; do
      c="${c//[[:space:]]/}"
      [[ -n "${c}" ]] && echo "${c}" >>"${tmp_all}"
    done
    return
  fi

  if [[ -n "${CARDS_FILE}" ]]; then
    if [[ ! -f "${CARDS_FILE}" ]]; then
      echo "cards file not found: ${CARDS_FILE}" >&2
      exit 2
    fi
    # accept either plain IDs or lines like "OY:12345"
    while IFS= read -r line; do
      line="${line%%#*}"
      line="${line//$'\r'/}"
      line="${line//[[:space:]]/}"
      [[ -z "${line}" ]] && continue
      line="${line#OY:}"
      echo "${line}" >>"${tmp_all}"
    done <"${CARDS_FILE}"
    return
  fi

  # Range mode.
  if [[ -z "${FROM}" || -z "${TO}" || -z "${COUNT}" ]]; then
    echo "Provide either --cards, --cards-file, or (--from --to --count)." >&2
    usage
    exit 2
  fi
  for ((i=FROM; i<=TO; i++)); do
    if [[ -n "${MOD}" ]]; then
      if (( i % MOD != MOD_VALUE )); then
        continue
      fi
    fi
    echo "$i" >>"${tmp_all}"
  done
}

build_from_sources

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

if [[ -z "${COUNT}" ]]; then
  # If using explicit list/file, default to tapping all available.
  COUNT="${available}"
fi

if (( COUNT > available )); then
  echo "Requested count (${COUNT}) is greater than available unique cards (${available}); reduce --count or adjust card list/state." >&2
  exit 2
fi

# Randomize if possible.
if command -v shuf >/dev/null 2>&1; then
  shuf "${tmp_candidates}" | head -n "${COUNT}" >"${tmp_pool}"
else
  head -n "${COUNT}" "${tmp_candidates}" >"${tmp_pool}"
fi

sent=0
failed=0
while IFS= read -r card; do
  # Send tap as plain text body.
  ts=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
  resp=$(curl -sS -X POST "${TAP_URL}" -H "Content-Type: text/plain" --data-raw "OY:${card}" || true)
  ok=0
  if echo "${resp}" | grep -q '"allowed"[[:space:]]*:[[:space:]]*true'; then
    ok=1
  fi
  printf '%s card=%s allowed=%s resp=%s\n' "${ts}" "${card}" "${ok}" "${resp}"
  echo "${card}" >>"${STATE_FILE}"
  sent=$((sent+1))
  if (( REQUIRE_ALLOW == 1 )) && (( ok == 0 )); then
    failed=$((failed+1))
  fi
  if (( DELAY_MS > 0 )); then
    # sleep supports fractional seconds
    sleep "0.$(printf '%03d' "${DELAY_MS}")"
  fi
done <"${tmp_pool}"

echo "done: gate=${GATE_URL} sent=${sent} denied=${failed} state=${STATE_FILE}"
if (( REQUIRE_ALLOW == 1 )) && (( failed > 0 )); then
  exit 1
fi
