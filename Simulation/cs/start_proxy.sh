#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

: "${TDS_SERVER_ADDR:=192.168.100.13:5000}"
: "${TDS_PROXY_LISTEN:=127.0.0.1:5100}"

export TDS_SERVER_ADDR
export TDS_PROXY_LISTEN

cd "${REPO_ROOT}"

# Optional flags:
#   CLIENT_PROXY_FLAGS="-tcp" or CLIENT_PROXY_FLAGS="-p2p -p2p-port 6000 -bootstrap 10.0.0.1:6000" etc.
exec go run ./cmd/client_proxy -background ${CLIENT_PROXY_FLAGS:-}

