#!/usr/bin/env bash
set -euo pipefail

ENV_FILE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/orchestrate.env"
GATE_ID=""
GATE_URL=""
CARD_TYPE=""
CARD_ID=""
TAP_LINE=""
NO_PROBE=0
LIST_ONLY=0
ONCE=0

usage() {
  cat <<'EOF'
Usage:
  ./Simulation/tap_gate.sh
  ./Simulation/tap_gate.sh --gate-id gate-2 --card-type OY --card-id 1001 --once
  ./Simulation/tap_gate.sh --gate-url http://192.168.100.14:9200 --tap PCTR:2001 --once

Options:
  --env-file FILE   Simulation env file to read (default: ./Simulation/orchestrate.env)
  --gate-id ID      Preselect a gate by its gate id
  --gate-url URL    Preselect a gate by explicit URL
  --card-type TYPE  OY or PCTR
  --card-id ID      Card identifier
  --tap TAP         Full tap payload, e.g. OY:1001
  --no-probe        Skip health checks
  --list-only       List discovered gates and exit
  --once            Send at most one tap and exit
  -h, --help        Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --env-file) ENV_FILE="$2"; shift 2 ;;
    --gate-id) GATE_ID="$2"; shift 2 ;;
    --gate-url) GATE_URL="$2"; shift 2 ;;
    --card-type) CARD_TYPE="$2"; shift 2 ;;
    --card-id) CARD_ID="$2"; shift 2 ;;
    --tap) TAP_LINE="$2"; shift 2 ;;
    --no-probe) NO_PROBE=1; shift ;;
    --list-only) LIST_ONLY=1; shift ;;
    --once) ONCE=1; shift ;;
    -h|--help) usage; exit 0 ;;
    *) echo "unknown arg: $1" >&2; usage; exit 2 ;;
  esac
done

if [[ ! -f "${ENV_FILE}" ]]; then
  echo "Missing env file: ${ENV_FILE}" >&2
  exit 1
fi

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required" >&2
  exit 1
fi

trim() {
  local value="$1"
  value="${value#${value%%[![:space:]]*}}"
  value="${value%${value##*[![:space:]]}}"
  printf '%s' "$value"
}

unquote_env_value() {
  local value
  value="$(trim "$1")"
  if [[ ${#value} -ge 2 ]]; then
    if [[ ${value:0:1} == '"' && ${value: -1} == '"' ]]; then
      printf '%s' "${value:1:${#value}-2}"
      return
    fi
    if [[ ${value:0:1} == "'" && ${value: -1} == "'" ]]; then
      printf '%s' "${value:1:${#value}-2}"
      return
    fi
  fi
  printf '%s' "$value"
}

read_env_value() {
  local key="$1"
  local line
  while IFS= read -r line; do
    line="$(trim "$line")"
    [[ -z "$line" || ${line:0:1} == "#" ]] && continue
    line="$(printf '%s' "$line" | sed -E 's/[[:space:]]+#.*$//')"
    if [[ "$line" =~ ^${key}=(.*)$ ]]; then
      unquote_env_value "${BASH_REMATCH[1]}"
      return 0
    fi
  done <"${ENV_FILE}"
  return 1
}

port_from_listen() {
  local listen="$1"
  if [[ "$listen" =~ :([0-9]+)$ ]]; then
    printf '%s' "${BASH_REMATCH[1]}"
    return 0
  fi
  return 1
}

normalize_gate_url() {
  local host="$1"
  local listen="$2"
  if [[ "$listen" =~ ^https?:// ]]; then
    printf '%s' "${listen%/}"
    return 0
  fi

  local port
  port="$(port_from_listen "$listen")" || {
    echo "Could not extract port from listen address: $listen" >&2
    exit 1
  }
  printf 'http://%s:%s' "$host" "$port"
}

GATES_RAW="$(read_env_value GATES || true)"
if [[ -z "$GATES_RAW" ]]; then
  echo "No GATES entry found in ${ENV_FILE}" >&2
  exit 1
fi

declare -a GATE_HOSTS GATE_IDS GATE_STATIONS GATE_LISTENS GATE_URLS GATE_HEALTHY GATE_HEALTH

index=0
for item in $GATES_RAW; do
  [[ -z "$item" ]] && continue
  IFS=',' read -r host gate_id station_id listen <<<"$item"
  if [[ -z "$host" || -z "$gate_id" || -z "$station_id" || -z "$listen" ]]; then
    echo "Bad GATES entry: $item" >&2
    exit 1
  fi
  GATE_HOSTS[index]="$(trim "$host")"
  GATE_IDS[index]="$(trim "$gate_id")"
  GATE_STATIONS[index]="$(trim "$station_id")"
  GATE_LISTENS[index]="$(trim "$listen")"
  GATE_URLS[index]="$(normalize_gate_url "${GATE_HOSTS[index]}" "${GATE_LISTENS[index]}")"
  GATE_HEALTHY[index]="?"
  GATE_HEALTH[index]=""
  index=$((index + 1))
done

if (( NO_PROBE == 0 )); then
  for ((i=0; i<index; i++)); do
    http_code="$(curl -sS -o /dev/null -m 2 -w '%{http_code}' "${GATE_URLS[i]}/health" || true)"
    if [[ "$http_code" =~ ^2[0-9][0-9]$ ]]; then
      GATE_HEALTHY[i]="yes"
      GATE_HEALTH[i]="HTTP ${http_code}"
    elif [[ -n "$http_code" && "$http_code" != "000" ]]; then
      GATE_HEALTHY[i]="no"
      GATE_HEALTH[i]="HTTP ${http_code}"
    else
      GATE_HEALTHY[i]="no"
      GATE_HEALTH[i]="unreachable"
    fi
  done
fi

printf '%-5s %-12s %-8s %-16s %-30s %-10s %s\n' 'No.' 'Gate' 'Station' 'Host' 'URL' 'Reachable' 'Health'
for ((i=0; i<index; i++)); do
  printf '%-5s %-12s %-8s %-16s %-30s %-10s %s\n' \
    "$((i + 1))" "${GATE_IDS[i]}" "${GATE_STATIONS[i]}" "${GATE_HOSTS[i]}" "${GATE_URLS[i]}" "${GATE_HEALTHY[i]}" "${GATE_HEALTH[i]}"
done

if (( LIST_ONLY == 1 )); then
  exit 0
fi

select_gate_index() {
  if [[ -n "$GATE_URL" ]]; then
    local normalized="${GATE_URL%/}"
    for ((i=0; i<index; i++)); do
      if [[ "${GATE_URLS[i]}" == "$normalized" ]]; then
        printf '%s' "$i"
        return 0
      fi
    done
    echo "Gate URL not found in env, using explicit URL: $normalized" >&2
    printf '%s' '-1'
    return 0
  fi

  if [[ -n "$GATE_ID" ]]; then
    for ((i=0; i<index; i++)); do
      if [[ "${GATE_IDS[i]}" == "$GATE_ID" ]]; then
        printf '%s' "$i"
        return 0
      fi
    done
    echo "Gate ID not found: $GATE_ID" >&2
    exit 1
  fi

  while true; do
    read -r -p 'Select gate number (or q to quit): ' choice
    if [[ "$choice" =~ ^[Qq]$ ]]; then
      printf '%s' 'quit'
      return 0
    fi
    if [[ "$choice" =~ ^[0-9]+$ ]] && (( choice >= 1 && choice <= index )); then
      printf '%s' "$((choice - 1))"
      return 0
    fi
    echo 'Invalid selection.' >&2
  done
}

read_tap_line() {
  if [[ -n "$TAP_LINE" ]]; then
    printf '%s' "$TAP_LINE"
    return 0
  fi

  local current_type="$CARD_TYPE"
  local current_id="$CARD_ID"

  if [[ -z "$current_type" ]]; then
    read -r -p 'Card type [OY/PCTR] (default OY): ' current_type
    current_type="${current_type:-OY}"
  fi
  current_type="$(printf '%s' "$current_type" | tr '[:lower:]' '[:upper:]')"
  if [[ "$current_type" != 'OY' && "$current_type" != 'PCTR' ]]; then
    echo "Unsupported card type: $current_type" >&2
    exit 1
  fi

  if [[ -z "$current_id" ]]; then
    read -r -p 'Card ID: ' current_id
  fi
  current_id="$(trim "$current_id")"
  if [[ -z "$current_id" ]]; then
    echo 'Card ID is required.' >&2
    exit 1
  fi

  printf '%s:%s' "$current_type" "$current_id"
}

while true; do
  selection="$(select_gate_index)"
  if [[ "$selection" == 'quit' ]]; then
    exit 0
  fi

  tap_value="$(read_tap_line)"
  if [[ "$selection" == '-1' ]]; then
    target_url="${GATE_URL%/}"
    target_name="$target_url"
  else
    target_url="${GATE_URLS[selection]}"
    target_name="${GATE_IDS[selection]}"
  fi

  payload="{\"tap\":\"${tap_value}\"}"
  echo "Sending ${tap_value} to ${target_name} (${target_url})..."
  response="$(curl -sS -X POST "${target_url}/tap" -H 'Content-Type: application/json' --data-raw "$payload")"
  echo "Response: ${response}"

  if (( ONCE == 1 )) || [[ -n "$GATE_ID" || -n "$GATE_URL" || -n "$TAP_LINE" || ( -n "$CARD_TYPE" && -n "$CARD_ID" ) ]]; then
    exit 0
  fi

  read -r -p 'Send another tap? [Y/n]: ' again
  if [[ "$again" =~ ^[Nn]$ ]]; then
    exit 0
  fi
done