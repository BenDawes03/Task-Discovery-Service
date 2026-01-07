#!/usr/bin/env pwsh
# P2P Mode Demo Script
# This script demonstrates the DHT-based P2P mode by starting multiple nodes

Write-Host "=== TDS P2P Mode Demo ===" -ForegroundColor Cyan
Write-Host ""

# Change to project root (parent of test_scripts)
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$projectRoot = Split-Path -Parent $scriptDir
Set-Location $projectRoot

Write-Host "Project root: $projectRoot" -ForegroundColor Gray
Write-Host ""

# Build the project first
Write-Host "Building project..." -ForegroundColor Yellow
go build -o client_proxy.exe ./cmd/client_proxy
if ($LASTEXITCODE -ne 0) {
    Write-Host "Build failed!" -ForegroundColor Red
    exit 1
}
Write-Host "Build complete!" -ForegroundColor Green
Write-Host ""

# Start three nodes in background
Write-Host "Starting Node A on :6000 (client proxy on :5100)..." -ForegroundColor Yellow
$nodeA = Start-Process -FilePath ".\client_proxy.exe" -ArgumentList "-p2p","-p2p-port",":6000" -PassThru -WindowStyle Normal

Start-Sleep -Seconds 2

Write-Host "Starting Node B on :6001 (client proxy on :5101)..." -ForegroundColor Yellow
$env:TDS_PROXY_LISTEN=":5101"
$nodeB = Start-Process -FilePath ".\client_proxy.exe" -ArgumentList "-p2p","-p2p-port",":6001","-bootstrap","localhost:6000" -PassThru -WindowStyle Normal

Start-Sleep -Seconds 2

Write-Host "Starting Node C on :6002 (client proxy on :5102)..." -ForegroundColor Yellow
$env:TDS_PROXY_LISTEN=":5102"
$nodeC = Start-Process -FilePath ".\client_proxy.exe" -ArgumentList "-p2p","-p2p-port",":6002","-bootstrap","localhost:6000" -PassThru -WindowStyle Normal

Start-Sleep -Seconds 3

Write-Host ""
Write-Host "=== Three P2P nodes started ===" -ForegroundColor Green
Write-Host "Node A: DHT=:6000, Proxy=:5100" -ForegroundColor Cyan
Write-Host "Node B: DHT=:6001, Proxy=:5101" -ForegroundColor Cyan
Write-Host "Node C: DHT=:6002, Proxy=:5102" -ForegroundColor Cyan
Write-Host ""

Write-Host "Press Enter to run test operations..." -ForegroundColor Yellow
Read-Host

Write-Host ""
Write-Host "=== Testing P2P Operations ===" -ForegroundColor Cyan
Write-Host ""

# Test 1: Register a service on Node A
Write-Host "1. Registering 'web-api' on Node A (port 5100)..." -ForegroundColor Yellow
$result = "REGISTER web-api 192.168.1.50:8080" | & "C:\Windows\System32\CHOICE.exe" /C YN /T 0 /D Y /M "" | Out-Null
# Using a simple UDP send (PowerShell doesn't have nc, so we'll show the command)
Write-Host "   Command: echo 'REGISTER web-api 192.168.1.50:8080' | nc -u localhost 5100" -ForegroundColor Gray
Write-Host "   (Manual test required - nc not available in PowerShell)" -ForegroundColor Gray
Write-Host ""

# Test 2: Query from Node B
Write-Host "2. Querying 'web-api' from Node B (port 5101)..." -ForegroundColor Yellow
Write-Host "   Command: echo 'QUERY web-api' | nc -u localhost 5101" -ForegroundColor Gray
Write-Host "   Expected: 192.168.1.50:8080" -ForegroundColor Gray
Write-Host ""

# Test 3: Register another service on Node C
Write-Host "3. Registering 'database' on Node C (port 5102)..." -ForegroundColor Yellow
Write-Host "   Command: echo 'REGISTER database 192.168.1.51:5432' | nc -u localhost 5102" -ForegroundColor Gray
Write-Host ""

# Test 4: Query from Node A
Write-Host "4. Querying 'database' from Node A (port 5100)..." -ForegroundColor Yellow
Write-Host "   Command: echo 'QUERY database' | nc -u localhost 5100" -ForegroundColor Gray
Write-Host "   Expected: 192.168.1.51:5432" -ForegroundColor Gray
Write-Host ""

Write-Host "=== Manual Testing Instructions ===" -ForegroundColor Cyan
Write-Host ""
Write-Host "The nodes are running. To test manually, use another terminal:" -ForegroundColor Yellow
Write-Host ""
Write-Host "Using netcat (if installed):" -ForegroundColor White
Write-Host '  echo "REGISTER my-service 10.0.0.1:9000" | nc -u localhost 5100' -ForegroundColor Gray
Write-Host '  echo "QUERY my-service" | nc -u localhost 5101' -ForegroundColor Gray
Write-Host ""
Write-Host "Using PowerShell UDP client:" -ForegroundColor White
Write-Host '  $client = New-Object System.Net.Sockets.UdpClient' -ForegroundColor Gray
Write-Host '  $client.Connect("localhost", 5100)' -ForegroundColor Gray
Write-Host '  $bytes = [Text.Encoding]::ASCII.GetBytes("REGISTER api 10.0.0.5:8080")' -ForegroundColor Gray
Write-Host '  $client.Send($bytes, $bytes.Length)' -ForegroundColor Gray
Write-Host '  $endpoint = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)' -ForegroundColor Gray
Write-Host '  $response = $client.Receive([ref]$endpoint)' -ForegroundColor Gray
Write-Host '  [Text.Encoding]::ASCII.GetString($response)' -ForegroundColor Gray
Write-Host ""

Write-Host "Press Enter to stop all nodes..." -ForegroundColor Yellow
Read-Host

# Cleanup
Write-Host ""
Write-Host "Stopping nodes..." -ForegroundColor Yellow
Stop-Process -Id $nodeA.Id -Force -ErrorAction SilentlyContinue
Stop-Process -Id $nodeB.Id -Force -ErrorAction SilentlyContinue
Stop-Process -Id $nodeC.Id -Force -ErrorAction SilentlyContinue

Write-Host "Demo complete!" -ForegroundColor Green
