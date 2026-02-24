#!/bin/bash
# quick_test.sh
# Quick end-to-end test of the ticket distribution system.
# This script starts CS, the distributor, seeds tickets, and monitors outputs.
#
# Prerequisites:
#   - PostgreSQL running with a database for TDS
#   - Go installed
#   - On Windows, run this in Git Bash or WSL
#
# Usage:
#   ./quick_test.sh
#

set -e

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CS_BIN="$REPO_ROOT/Simulation/cs/cs"
DIST_BIN="$REPO_ROOT/Simulation/ticketdistributor/ticketdistributor"
SEED_SCRIPT="$REPO_ROOT/scripts/seed_tickets.ps1"

# Configuration
CS_LISTEN=":9101"
DIST_LISTEN=":9110"
DIST_OUTDIR="/tmp/manifests"
DB_DSN="${DATABASE_URL:-postgres://postgres:postgres@localhost:5432/tds?sslmode=disable}"

echo "Ticket Distribution System — Quick Test"
echo "========================================"
echo ""
echo "Configuration:"
echo "  CS listen: $CS_LISTEN"
echo "  Distributor listen: $DIST_LISTEN"
echo "  Output dir: $DIST_OUTDIR"
echo "  DB DSN: $DB_DSN"
echo ""

# Build
echo "Building binaries..."
go build -o "$CS_BIN" ./Simulation/cs 2>/dev/null || go build -o "$CS_BIN.exe" ./Simulation/cs
go build -o "$DIST_BIN" ./Simulation/ticketdistributor 2>/dev/null || go build -o "$DIST_BIN.exe" ./Simulation/ticketdistributor
echo "✓ Binaries built"
echo ""

# Clean up any previous runs
rm -rf "$DIST_OUTDIR"
mkdir -p "$DIST_OUTDIR"

# Function to cleanup on exit
cleanup() {
    echo ""
    echo "Cleaning up..."
    pkill -f "$CS_BIN" || true
    pkill -f "$DIST_BIN" || true
    echo "✓ Services stopped"
}
trap cleanup EXIT

# Start CS
echo "Starting CS..."
export DATABASE_URL="$DB_DSN"
"$CS_BIN" -listen "$CS_LISTEN" -ticket-interval 5m &
CS_PID=$!
sleep 2
echo "✓ CS started (PID: $CS_PID)"

# Start Distributor
echo "Starting Distributor..."
"$DIST_BIN" -listen "$DIST_LISTEN" -cs-base "http://localhost:9101" -outdir "$DIST_OUTDIR" -poll-interval 30s &
DIST_PID=$!
sleep 2
echo "✓ Distributor started (PID: $DIST_PID)"
echo ""

# Seed tickets
echo "Seeding tickets for 90 seconds..."
pwsh -Command "
\$ErrorActionPreference = 'SilentlyContinue'
\$endTime = (Get-Date).AddSeconds(90)
\$batchNum = 0
while ((Get-Date) -lt \$endTime) {
    \$batchNum++
    Write-Host \"[Batch \$batchNum] Inserting 3 tickets...\"
    for (\$i = 0; \$i -lt 3; \$i++) {
        \$st = @('station-1', 'station-2', 'station-3') | Get-Random
        \$minutes = Get-Random -Minimum 1 -Maximum 15
        \$trainTime = (Get-Date).AddMinutes(\$minutes).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ss.ffffffZ')
        \$body = @{
            station_id = \$st
            train_time = \$trainTime
            passenger = 'TestPassenger'
        } | ConvertTo-Json -Compress
        Invoke-RestMethod -Method Post -ContentType 'application/json' -Body \$body -Uri 'http://localhost:9101/ticket' -ErrorAction SilentlyContinue | Out-Null
    }
    Start-Sleep -Seconds 30
}
Write-Host 'Seeding completed'
"

echo ""
echo "Test phase complete. Services are still running."
echo ""
echo "Verify results:"
echo "  Check CS manifests:"
echo "    curl http://localhost:9101/manifests"
echo ""
echo "  Check distributor CSV files:"
echo "    ls -la $DIST_OUTDIR/"
echo ""
echo "  Fetch a file via distributor:"
echo "    curl 'http://localhost:9110/file?manifest_id=m-<id>&station=station-1'"
echo ""
echo "Press Ctrl+C to stop services and exit."
wait
