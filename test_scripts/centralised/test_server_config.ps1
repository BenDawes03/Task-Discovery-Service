# Test Server Configuration Options
# Demonstrates the new command-line flags and interactive prompts

# Navigate to project root
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$projectRoot = Split-Path -Parent (Split-Path -Parent $scriptDir)
Set-Location $projectRoot

Write-Host "================================" -ForegroundColor Cyan
Write-Host "TDS Server Configuration Tests" -ForegroundColor Cyan
Write-Host "================================" -ForegroundColor Cyan
Write-Host ""

# Test 1: Show help
Write-Host "[Test 1] Displaying help output..." -ForegroundColor Yellow
go run ./cmd/server --help
Write-Host ""

# Test 2: Custom port
Write-Host "[Test 2] Testing custom port flag..." -ForegroundColor Yellow
Write-Host "Command: go run ./cmd/server --port 6000 --no-ui" -ForegroundColor Gray
Write-Host "Expected: Server starts on port 6000 in headless mode" -ForegroundColor Gray
Write-Host "Press Ctrl+C to stop the server and continue tests" -ForegroundColor Yellow
Write-Host ""

# Test 3: List all flags
Write-Host "[Test 3] Available configuration flags:" -ForegroundColor Yellow
Write-Host "  Server: --port, --heartbeat-timeout, --cleanup-interval" -ForegroundColor White
Write-Host "  Transport: --udp, --tcp, --tls" -ForegroundColor White
Write-Host "  TLS: --tls-cert, --tls-key, --tls-client-ca" -ForegroundColor White
Write-Host "  Database: --store-url, --cache-max-size" -ForegroundColor White
Write-Host "  Logging: --log-dir" -ForegroundColor White
Write-Host "  UI: --ui, --force-ui, --no-ui" -ForegroundColor White
Write-Host ""

# Test 4: Example commands
Write-Host "[Test 4] Example server configurations:" -ForegroundColor Yellow
Write-Host ""
Write-Host "• Development (UDP, in-memory, no UI):" -ForegroundColor Cyan
Write-Host "  go run ./cmd/server --udp --no-ui" -ForegroundColor Gray
Write-Host ""
Write-Host "• Testing (TCP, database, with UI):" -ForegroundColor Cyan
Write-Host '  go run ./cmd/server --tcp --store-url "postgresql://tds:password@localhost:5432/tds?sslmode=disable" --ui' -ForegroundColor Gray
Write-Host ""
Write-Host "• Production (TLS, database, custom timeouts):" -ForegroundColor Cyan
Write-Host '  go run ./cmd/server --tls \\' -ForegroundColor Gray
Write-Host '    --port 5000 \\' -ForegroundColor Gray
Write-Host '    --store-url "postgresql://tds:password@localhost:5432/tds?sslmode=require" \\' -ForegroundColor Gray
Write-Host '    --cache-max-size 500 \\' -ForegroundColor Gray
Write-Host '    --heartbeat-timeout 90s \\' -ForegroundColor Gray
Write-Host '    --no-ui' -ForegroundColor Gray
Write-Host ""

# Test 5: Interactive mode
Write-Host "[Test 5] Interactive prompts" -ForegroundColor Yellow
Write-Host "When starting without flags, you'll be prompted for:" -ForegroundColor White
Write-Host "  1. Transport mode (UDP/TCP/TLS)" -ForegroundColor Gray
Write-Host "  2. Database persistence (yes/no)" -ForegroundColor Gray
Write-Host "  3. Database URL (if yes to #2)" -ForegroundColor Gray
Write-Host "  4. Cache size (if database enabled)" -ForegroundColor Gray
Write-Host "  5. TUI mode (yes/no)" -ForegroundColor Gray
Write-Host ""
Write-Host "Try it: go run ./cmd/server" -ForegroundColor Cyan
Write-Host ""

Write-Host "================================" -ForegroundColor Cyan
Write-Host "Configuration documentation: docs/SERVER_CONFIG.md" -ForegroundColor Green
Write-Host "================================" -ForegroundColor Cyan
