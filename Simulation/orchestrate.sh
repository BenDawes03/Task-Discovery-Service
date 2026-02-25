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

# If the env used a tilde (e.g. REMOTE_REPO_DIR=~/bxd280) it may have been expanded
# to the local user's $HOME when the env file was sourced. That makes commands like
# `cd ${REMOTE_REPO_DIR}` run against a path rooted in the local machine rather
# than the remote user's home when executed over SSH. Convert a leading local
# $HOME prefix back to a tilde so the path is interpreted on the remote side.
if [[ -n "${REMOTE_REPO_DIR:-}" ]]; then
  if [[ "${REMOTE_REPO_DIR}" == "${HOME}"* ]]; then
    REMOTE_REPO_DIR="~${REMOTE_REPO_DIR#${HOME}}"
  fi
fi

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
  ${SSH} "${user}@${host}" "$@" < /dev/null
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

  # logs in ~/tds_sim_logs; start the command with nohup, detach and record PID.
  # Use < /dev/null so backgrounded processes do not inherit the SSH session's stdin.
  ssh_run "${host}" "mkdir -p ~/tds_sim_logs; nohup bash -lc ${quoted_cmd} > ~/tds_sim_logs/${name}.log 2>&1 < /dev/null & echo \$! > ~/tds_sim_logs/${name}.pid; echo started ${name}"
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
    "set -euo pipefail; cd ${REMOTE_REPO_DIR}; export GOTOOLCHAIN=local; export TDS_SERVER_ADDR='${server_addr}'; export TDS_PROXY_LISTEN='${PROXY_LISTEN}'; go run -mod=vendor ./cmd/client_proxy -background"
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

# Open an interactive SSH window to a host using the first available terminal emulator.
open_ssh_terminal() {
  local host="$1"
  local name="$2"
  local user
  user="$(ssh_user_for_host "${host}")"
  local ssh_cmd="ssh ${user}@${host}"

  # Try Windows Terminal (wt)
  if command -v wt >/dev/null 2>&1; then
    # wt spawns a new window/tab and runs powershell which then runs ssh and keeps the window open
    wt new-tab powershell -NoExit -Command "${ssh_cmd}" || true
    return 0
  fi

  # GNOME Terminal
  if command -v gnome-terminal >/dev/null 2>&1; then
    gnome-terminal --title="${name}@${host}" -- bash -ic "${ssh_cmd}; exec bash" &
    return 0
  fi

  # Konsole
  if command -v konsole >/dev/null 2>&1; then
    konsole --new-tab -p tabtitle="${name}@${host}" -e bash -ic "${ssh_cmd}; exec bash" &
    return 0
  fi

  # xterm
  if command -v xterm >/dev/null 2>&1; then
    xterm -T "${name}@${host}" -e "${ssh_cmd}" &
    return 0
  fi

  # macOS Terminal via osascript
  if command -v osascript >/dev/null 2>&1; then
    osascript -e "tell application \"Terminal\" to do script \"${ssh_cmd}\"" || true
    return 0
  fi

  # Fallback: run ssh in current terminal (interactive)
  echo "No GUI terminal emulator found to open a new window for ${host}. Running ssh in this terminal..."
  ${ssh_cmd}
}

# Open an interactive SSH window and run a remote command (keeps the window open).
open_ssh_terminal_run() {
  local host="$1"
  local name="$2"
  local remote_cmd="$3"
  local user
  user="$(ssh_user_for_host "${host}")"

  # Quote the remote command for bash -lc on the remote side.
  local quoted_remote
  quoted_remote="$(printf '%q' "${remote_cmd}")"
  local ssh_cmd="ssh ${user}@${host} bash -lc ${quoted_remote}"

  # Try Windows Terminal (wt)
  if command -v wt >/dev/null 2>&1; then
    wt new-tab powershell -NoExit -Command "${ssh_cmd}" || true
    return 0
  fi

  # GNOME Terminal
  if command -v gnome-terminal >/dev/null 2>&1; then
    gnome-terminal --title="${name}@${host}" -- bash -ic "${ssh_cmd}; exec bash" &
    return 0
  fi

  # Konsole
  if command -v konsole >/dev/null 2>&1; then
    konsole --new-tab -p tabtitle="${name}@${host}" -e bash -ic "${ssh_cmd}; exec bash" &
    return 0
  fi

  # xterm
  if command -v xterm >/dev/null 2>&1; then
    xterm -T "${name}@${host}" -e "${ssh_cmd}" &
    return 0
  fi

  # macOS Terminal via osascript
  if command -v osascript >/dev/null 2>&1; then
    osascript -e "tell application \"Terminal\" to do script \"${ssh_cmd}\"" || true
    return 0
  fi

  # Fallback: run ssh in current terminal (interactive)
  echo "No GUI terminal emulator found to open a new window for ${host}. Running ssh in this terminal..."
  ${ssh_cmd}
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
  down)
    echo "Stopping services on all hosts"
    down_failures=0
    # Collect hosts and attempt to stop processes recorded in ~/tds_sim_logs/*.pid
    while IFS= read -r host; do
      [[ -z "${host}" ]] && continue
      echo "Stopping on ${host} (ssh user: $(ssh_user_for_host "${host}"))"

      remote_down_cmd='set +e;
        for f in "$HOME"/tds_sim_logs/*.pid; do
          [ -f "$f" ] || continue
          pid=$(cat "$f" 2>/dev/null)
          if [ -n "$pid" ]; then
            kill "$pid" >/dev/null 2>&1 || true
          fi
          rm -f "$f" || true
        done

        # fallback: try to stop any go-run processes started by this orchestrator
        pkill -f "go run -mod=vendor" >/dev/null 2>&1 || true
        pkill -f "tail -f /dev/null | go run" >/dev/null 2>&1 || true
        echo stopped
        exit 0'
      quoted_remote_down_cmd="$(printf '%q' "${remote_down_cmd}")"

      down_output=""
      if ! down_output="$(ssh_run "${host}" "bash -lc ${quoted_remote_down_cmd}" 2>&1)"; then
        echo "WARN: failed to stop cleanly on ${host}" >&2
        [[ -n "${down_output}" ]] && echo "${down_output}" >&2
        down_failures=$((down_failures+1))
      else
        [[ -n "${down_output}" ]] && echo "${down_output}"
      fi
    done < <(collect_all_hosts)

    if [[ "${down_failures}" -gt 0 ]]; then
      echo "Down completed with ${down_failures} host error(s)." >&2
      exit 1
    fi
    echo "Down completed successfully on all hosts."
    ;;
  attach)
    echo "Opening interactive SSH windows to all hosts..."
    # For each host, open a terminal window with an ssh session (non-blocking)
    while IFS= read -r host; do
      [[ -z "${host}" ]] && continue
      echo "  -> ${host} (ssh user: $(ssh_user_for_host "${host}"))"
      open_ssh_terminal "${host}" "${host}" &
      # small delay to avoid overwhelming the desktop with many windows at once
      sleep 0.15
    done < <(collect_all_hosts)
    wait
    ;;
  attach-up)
    echo "Opening interactive SSH windows and starting services on each host..."

    # Start TDS server in its own window
    echo "  -> TDS server: ${TDS_SERVER_HOST} (ssh user: $(ssh_user_for_host "${TDS_SERVER_HOST}"))"
    remote_cmd="set -euo pipefail; cd ${REMOTE_REPO_DIR}; export GOTOOLCHAIN=local; go run -mod=vendor ./cmd/server -port ${TDS_SERVER_PORT}"
    open_ssh_terminal_run "${TDS_SERVER_HOST}" "tds_server" "${remote_cmd}" &

    # Start client proxy on each host in its own window
    for host in $(collect_all_hosts); do
      echo "  -> client_proxy on ${host} (ssh user: $(ssh_user_for_host "${host}"))"
      remote_cmd="set -euo pipefail; cd ${REMOTE_REPO_DIR}; export GOTOOLCHAIN=local; export TDS_SERVER_ADDR='${server_addr}'; export TDS_PROXY_LISTEN='${PROXY_LISTEN}'; go run -mod=vendor ./cmd/client_proxy -background"
      open_ssh_terminal_run "${host}" "client_proxy_${host}" "${remote_cmd}" &
      sleep 0.08
    done

    # Start other services
    echo "  -> CS on ${CS_HOST}"
    remote_cmd="set -euo pipefail; cd ${REMOTE_REPO_DIR}; export GOTOOLCHAIN=local; export CS_DB_DSN='${CS_DB_DSN}'; go run -mod=vendor ./Simulation/cs -listen ${CS_LISTEN} -proxy ${PROXY_LISTEN} -advertise ${CS_ADVERTISE}"
    open_ssh_terminal_run "${CS_HOST}" "cs" "${remote_cmd}" &

    echo "  -> PCTRBO on ${PCTRBO_HOST}"
    remote_cmd="set -euo pipefail; cd ${REMOTE_REPO_DIR}; export GOTOOLCHAIN=local; export PCTRBO_DB_DSN='${PCTRBO_DB_DSN}'; go run -mod=vendor ./Simulation/pctrbo -listen ${PCTRBO_LISTEN} -proxy ${PROXY_LISTEN} -advertise ${PCTRBO_ADVERTISE}"
    open_ssh_terminal_run "${PCTRBO_HOST}" "pctrbo" "${remote_cmd}" &

    echo "  -> PA on ${PA_HOST}"
    remote_cmd="set -euo pipefail; cd ${REMOTE_REPO_DIR}; export GOTOOLCHAIN=local; export PA_DB_DSN='${PA_DB_DSN}'; go run -mod=vendor ./Simulation/pa -listen ${PA_LISTEN} -proxy ${PROXY_LISTEN} -private-key ${PA_PRIVATE_KEY} -advertise ${PA_ADVERTISE}"
    open_ssh_terminal_run "${PA_HOST}" "pa" "${remote_cmd}" &

    # Stations and gates
    for item in ${STATIONS}; do
      IFS=',' read -r host station_id listen advertise <<<"${item}"
      echo "  -> station ${station_id} on ${host}"
      remote_cmd="set -euo pipefail; cd ${REMOTE_REPO_DIR}; export GOTOOLCHAIN=local; go run -mod=vendor ./Simulation/station_computer -station-id ${station_id} -listen ${listen} -proxy ${PROXY_LISTEN} -advertise ${advertise}"
      open_ssh_terminal_run "${host}" "station_${station_id}" "${remote_cmd}" &
      sleep 0.06
    done

    for item in ${GATES}; do
      IFS=',' read -r host gate_id station_id listen <<<"${item}"
      echo "  -> gate ${gate_id} on ${host}"
      remote_cmd="set -euo pipefail; cd ${REMOTE_REPO_DIR}; export GOTOOLCHAIN=local; export PCTR_PUBLIC_KEY='${PCTR_PUBLIC_KEY}'; go run -mod=vendor ./Simulation/gate -id ${gate_id} -station-id ${station_id} -listen ${listen} -proxy ${PROXY_LISTEN} -pctr-public-key ${PCTR_PUBLIC_KEY}"
      open_ssh_terminal_run "${host}" "gate_${gate_id}" "${remote_cmd}" &
      sleep 0.06
    done

    wait
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
    echo "Usage: $0 sync | up | down | attach | attach-up | logs <host> <name>" >&2
    exit 1
    ;;
esac
