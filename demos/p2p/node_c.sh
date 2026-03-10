#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
K_CLOSEST="${1:-3}"

cd "$REPO_ROOT"

if [[ ! -x "bin/client_proxy" ]]; then
  echo "client_proxy not found. Build first with: go build -o bin/client_proxy ./cmd/client_proxy"
  exit 1
fi

export TDS_PROXY_LISTEN=":5102"
exec ./bin/client_proxy -p2p -p2p-port :6002 -bootstrap 127.0.0.1:6000 -k-closest "$K_CLOSEST"
