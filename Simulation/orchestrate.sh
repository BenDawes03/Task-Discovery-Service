#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENV_FILE="${ROOT_DIR}/Simulation/orchestrate.env"

if [[ ! -f "${ENV_FILE}" ]]; then
  echo "Missing ${ENV_FILE}. Copy Simulation/orchestrate.env.example -> Simulation/orchestrate.env and edit it." >&2
  exit 1
fi

# shellcheck disable=SC1090
source "${ENV_FILE}"

SSH="ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new"

server_addr="${TDS_SERVER_HOST}:${TDS_SERVER_PORT}"

# Optional: set to 1 in Simulation/orchestrate.env to auto-sync repo before starting.
SYNC_BEFORE_UP="${SYNC_BEFORE_UP:-0}"

# Exclude patterns for sync (tar). Space-separated, relative to repo root.
# Note: we intentionally do NOT exclude vendor/ so `go run -mod=vendor` works offline on VMs.
SYNC_EXCLUDES_DEFAULT=(
  ".git"
  "build"
  "bin"
  "logs"
  "test_results_*"
  "test_scripts/results"
  "Simulation/orchestrate.env"
)

SYNC_EXCLUDES=("${SYNC_EXCLUDES_DEFAULT[@]}")
if [[ -n "${SYNC_EXCLUDES_EXTRA:-}" ]]; then
  # shellcheck disable=SC2206
  SYNC_EXCLUDES+=( ${SYNC_EXCLUDES_EXTRA} )
fi

ssh_run() {
  local host="$1"; shift
  local user
  user="$(ssh_user_for_host "${host}")"
  ${SSH} "${user}@${host}" "$@"
}

ssh_user_for_host() {
  local host="$1"

  # Default user (backwards compatible with older env files that used SSH_USER)
  local default_user="${SSH_USER_DEFAULT:-${SSH_USER:-}}"
  if [[ -z "${default_user}" ]]; then
    default_user="${USER:-user}"
  fi

  # Optional mapping list:
  #   SSH_USERS="192.168.100.10,CentralSystem 192.168.100.11,PaymentService"
  # Match either by IP/hostname used in env.
  if [[ -n "${SSH_USERS:-}" ]]; then
    local item mapped_host mapped_user
    for item in ${SSH_USERS}; do
      IFS=',' read -r mapped_host mapped_user <<<"${item}"
      if [[ "${mapped_host}" == "${host}" && -n "${mapped_user}" ]]; then
        echo "${mapped_user}"
        return 0
      fi
    done
  fi

  echo "${default_user}"
}

start_bg() {
  local host="$1"; shift
  local name="$1"; shift
  local cmd="$*"
  local quoted_cmd
  quoted_cmd="$(printf '%q' "${cmd}")"

  # logs in ~/tds_sim_logs
  ssh_run "${host}" "mkdir -p ~/tds_sim_logs; (nohup bash -lc ${quoted_cmd} > ~/tds_sim_logs/${name}.log 2>&1 & disown); echo started ${name}"
}

require_repo_on_host() {
  local host="$1"
  # Ensure repo exists and looks like the correct project.
  ssh_run "${host}" "test -f ${REMOTE_REPO_DIR}/go.mod" \
    || {
      echo "ERROR: repo not found on ${host} at ${REMOTE_REPO_DIR} (missing go.mod)." >&2
      echo "Hint: run './Simulation/orchestrate.sh sync' (or set SYNC_BEFORE_UP=1), and ensure SSH user mapping is correct." >&2
      exit 1
    }
}

start_proxy() {
  local host="$1"
  require_repo_on_host "${host}"
  start_bg "${host}" "client_proxy" \
    "set -euo pipefail; cd ${REMOTE_REPO_DIR}; export GOTOOLCHAIN=local; export TDS_SERVER_ADDR='${server_addr}'; export TDS_PROXY_LISTEN='${PROXY_LISTEN}'; tail -f /dev/null | go run -mod=vendor ./cmd/client_proxy"
}

start_tds_server() {
  require_repo_on_host "${TDS_SERVER_HOST}"
  start_bg "${TDS_SERVER_HOST}" "tds_server" \
    "set -euo pipefail; cd ${REMOTE_REPO_DIR}; export GOTOOLCHAIN=local; go run -mod=vendor ./cmd/server -port ${TDS_SERVER_PORT}"
}

start_cs() {
  require_repo_on_host "${CS_HOST}"
  start_bg "${CS_HOST}" "cs" \
    "set -euo pipefail; cd ${REMOTE_REPO_DIR}; export GOTOOLCHAIN=local; export CS_DB_DSN='${CS_DB_DSN}'; go run -mod=vendor ./Simulation/cs -listen ${CS_LISTEN} -proxy ${PROXY_LISTEN} -advertise ${CS_ADVERTISE}"
}

start_pctrbo() {
  require_repo_on_host "${PCTRBO_HOST}"
  start_bg "${PCTRBO_HOST}" "pctrbo" \
    "set -euo pipefail; cd ${REMOTE_REPO_DIR}; export GOTOOLCHAIN=local; export PCTRBO_DB_DSN='${PCTRBO_DB_DSN}'; go run -mod=vendor ./Simulation/pctrbo -listen ${PCTRBO_LISTEN} -proxy ${PROXY_LISTEN} -advertise ${PCTRBO_ADVERTISE}"
}

start_pa() {
  require_repo_on_host "${PA_HOST}"
  start_bg "${PA_HOST}" "pa" \
    "set -euo pipefail; cd ${REMOTE_REPO_DIR}; export GOTOOLCHAIN=local; export PA_DB_DSN='${PA_DB_DSN}'; go run -mod=vendor ./Simulation/pa -listen ${PA_LISTEN} -proxy ${PROXY_LISTEN} -private-key ${PA_PRIVATE_KEY} -advertise ${PA_ADVERTISE}"
}

start_stations() {
  for item in ${STATIONS}; do
    IFS=',' read -r host station_id listen advertise <<<"${item}"
    require_repo_on_host "${host}"
    start_bg "${host}" "station_${station_id}" \
      "set -euo pipefail; cd ${REMOTE_REPO_DIR}; export GOTOOLCHAIN=local; go run -mod=vendor ./Simulation/station_computer -station-id ${station_id} -listen ${listen} -proxy ${PROXY_LISTEN} -advertise ${advertise}"
  done
}

start_gates() {
  for item in ${GATES}; do
    IFS=',' read -r host gate_id station_id listen <<<"${item}"
    require_repo_on_host "${host}"
    # Gate advertises nothing; it just runs and queries station by station-id.
    start_bg "${host}" "gate_${gate_id}" \
      "set -euo pipefail; cd ${REMOTE_REPO_DIR}; export GOTOOLCHAIN=local; export PCTR_PUBLIC_KEY='${PCTR_PUBLIC_KEY}'; go run -mod=vendor ./Simulation/gate -id ${gate_id} -station-id ${station_id} -listen ${listen} -proxy ${PROXY_LISTEN} -pctr-public-key ${PCTR_PUBLIC_KEY}"
  done
}

start_proxies_all_hosts() {
  declare -A hosts=()

  hosts["${CS_HOST}"]=1
  hosts["${PCTRBO_HOST}"]=1
  hosts["${PA_HOST}"]=1

  for item in ${STATIONS}; do
    IFS=',' read -r host _ _ _ <<<"${item}"
    [[ -n "${host}" ]] && hosts["${host}"]=1
  done

  for item in ${GATES}; do
    IFS=',' read -r host _ _ _ <<<"${item}"
    [[ -n "${host}" ]] && hosts["${host}"]=1
  done

  for host in "${!hosts[@]}"; do
    start_proxy "${host}"
  done
}

collect_all_hosts() {
  declare -A hosts=()

  # Always include the TDS server host.
  hosts["${TDS_SERVER_HOST}"]=1
  hosts["${CS_HOST}"]=1
  hosts["${PCTRBO_HOST}"]=1
  hosts["${PA_HOST}"]=1

  for item in ${STATIONS}; do
    IFS=',' read -r host _ _ _ <<<"${item}"
    [[ -n "${host}" ]] && hosts["${host}"]=1
  done

  for item in ${GATES}; do
    IFS=',' read -r host _ _ _ <<<"${item}"
    [[ -n "${host}" ]] && hosts["${host}"]=1
  done

  for host in "${!hosts[@]}"; do
    echo "${host}"
  done
}

sync_host_tar() {
  local host="$1"
  local user
  user="$(ssh_user_for_host "${host}")"
  echo "Syncing repo -> ${user}@${host}:${REMOTE_REPO_DIR}"

  # Ensure destination exists.
  ssh_run "${host}" "mkdir -p ${REMOTE_REPO_DIR}"

  local tar_excludes=()
  local ex
  for ex in "${SYNC_EXCLUDES[@]}"; do
    tar_excludes+=("--exclude=${ex}")
  done

  # Stream a tarball to the VM and extract in-place.
  # This overwrites updated files but does not delete removed ones.
  tar -C "${ROOT_DIR}" -czf - "${tar_excludes[@]}" . \
    | ${SSH} "${user}@${host}" "tar -xzf - -C ${REMOTE_REPO_DIR}"

  require_repo_on_host "${host}"
}

sync_all_hosts() {
  local host
  local -a host_list=()

  while IFS= read -r host; do
    [[ -z "${host}" ]] && continue
    host_list+=("${host}")
  done < <(collect_all_hosts)

  echo "Hosts to sync: ${#host_list[@]}"
  for host in "${host_list[@]}"; do
    echo "  - ${host} (ssh user: $(ssh_user_for_host "${host}"))"
  done

  for host in "${host_list[@]}"; do
    sync_host_tar "${host}"
  done
}

case "${1:-}" in
  sync)
    echo "SYNC_BEFORE_UP=${SYNC_BEFORE_UP} (ignored for 'sync')"
    sync_all_hosts
    ;;
  up)
    if [[ "${SYNC_BEFORE_UP}" == "1" ]]; then
      echo "SYNC_BEFORE_UP=1 -> syncing repo to all hosts first"
      sync_all_hosts
    else
      echo "SYNC_BEFORE_UP=0 -> not syncing; assuming repo already present on all hosts"
    fi

    echo "Starting TDS server on ${TDS_SERVER_HOST}:${TDS_SERVER_PORT}"
    start_tds_server

    echo "Starting client_proxy on all hosts"
    start_proxies_all_hosts

    echo "Starting services"
    start_cs
    start_pctrbo
    start_pa
    start_stations
    start_gates

    echo "Done. Logs are under ~/tds_sim_logs on each VM."
    ;;
  logs)
    echo "Tail logs for a host:"
    echo "  ${0} logs <host> <name>"
    echo "Example: ${0} logs ${CS_HOST} cs"
    host="${2:-}"
    name="${3:-}"
    if [[ -z "${host}" || -z "${name}" ]]; then
      exit 1
    fi
    ssh_run "${host}" "tail -n 200 -f ~/tds_sim_logs/${name}.log"
    ;;
  *)
    echo "Usage: $0 sync | up | logs <host> <name>" >&2
    exit 1
    ;;
esac
