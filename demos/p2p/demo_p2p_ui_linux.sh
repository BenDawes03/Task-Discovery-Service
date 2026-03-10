#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
K_CLOSEST="${1:-3}"

cd "$REPO_ROOT"

launch_terminal() {
  local title="$1"
  local cmd="$2"

  if command -v gnome-terminal >/dev/null 2>&1; then
    gnome-terminal --title="$title" -- bash -lc "$cmd; exec bash"
    return 0
  fi

  if command -v konsole >/dev/null 2>&1; then
    konsole --new-tab -p tabtitle="$title" -e bash -lc "$cmd; exec bash"
    return 0
  fi

  if command -v xfce4-terminal >/dev/null 2>&1; then
    xfce4-terminal --title="$title" --hold -e "bash -lc '$cmd; exec bash'"
    return 0
  fi

  if command -v xterm >/dev/null 2>&1; then
    xterm -T "$title" -hold -e bash -lc "$cmd"
    return 0
  fi

  if command -v alacritty >/dev/null 2>&1; then
    alacritty -t "$title" -e bash -lc "$cmd; exec bash"
    return 0
  fi

  if command -v kitty >/dev/null 2>&1; then
    kitty --title "$title" bash -lc "$cmd; exec bash"
    return 0
  fi

  return 1
}

echo "=== TDS P2P UI Demo (Linux Bash) ==="
echo

echo "Building client_proxy..."
go build -o bin/client_proxy ./cmd/client_proxy
echo "Build complete."
echo

cmd_a="cd '$REPO_ROOT' && ./demos/p2p/node_a.sh '$K_CLOSEST'"
cmd_b="cd '$REPO_ROOT' && ./demos/p2p/node_b.sh '$K_CLOSEST'"
cmd_c="cd '$REPO_ROOT' && ./demos/p2p/node_c.sh '$K_CLOSEST'"

if ! launch_terminal "TDS Node A" "$cmd_a"; then
  echo "No supported terminal emulator found for auto-launch."
  echo "Run these in 3 separate terminals:"
  echo "  ./demos/p2p/node_a.sh $K_CLOSEST"
  echo "  ./demos/p2p/node_b.sh $K_CLOSEST"
  echo "  ./demos/p2p/node_c.sh $K_CLOSEST"
  exit 1
fi

sleep 2
launch_terminal "TDS Node B" "$cmd_b" || true
sleep 2
launch_terminal "TDS Node C" "$cmd_c" || true

echo "Opened 3 node terminals (A/B/C)."
echo "Proxy ports: 5100, 5101, 5102"
echo "DHT ports:   6000, 6001, 6002"
echo

echo "Quick test commands from another shell:"
echo "  echo '{\"cmd\":\"REGISTER\",\"task\":\"web-api\",\"address\":\"10.0.0.5:8080\"}' | nc -u 127.0.0.1 5100"
echo "  echo '{\"cmd\":\"QUERY\",\"task\":\"web-api\"}' | nc -u 127.0.0.1 5101"
