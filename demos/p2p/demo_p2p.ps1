#!/usr/bin/env pwsh
# P2P Mode Demo Script
# This script demonstrates the DHT-based P2P mode by starting multiple nodes

param(
    [int]$KClosest = 3
)

Write-Host "=== TDS P2P Mode Demo ===" -ForegroundColor Cyan
Write-Host ""

if ($KClosest -lt 1) {
    Write-Host "KClosest must be at least 1" -ForegroundColor Red
    exit 1
}

function Get-RepoRoot {
    param([string]$StartDir)

    $dir = $StartDir
    while ($true) {
        if (Test-Path (Join-Path $dir "go.mod")) {
            return $dir
        }
        $parent = Split-Path -Parent $dir
        if ($parent -eq $dir -or $parent -eq "") {
            throw "Could not locate repo root (go.mod not found) starting from: $StartDir"
        }
        $dir = $parent
    }
}

$projectRoot = Get-RepoRoot -StartDir $PSScriptRoot
Set-Location $projectRoot

Write-Host "Project root: $projectRoot" -ForegroundColor Gray
Write-Host ""

# Build the project first
Write-Host "Building project..." -ForegroundColor Yellow
go build -o bin\client_proxy.exe ./cmd/client_proxy
if ($LASTEXITCODE -ne 0) {
    Write-Host "Build failed!" -ForegroundColor Red
    exit 1
}
Write-Host "Build complete!" -ForegroundColor Green
Write-Host ""

# Start three nodes in separate visible terminals
Write-Host "Starting Node A on :6000 (client proxy on :5100)..." -ForegroundColor Yellow
$nodeACommand = "`$env:TDS_PROXY_LISTEN=':5100'; & '.\\bin\\client_proxy.exe' -p2p -p2p-port :6000 -k-closest $KClosest"
$nodeA = Start-Process -FilePath "powershell.exe" -ArgumentList "-NoExit","-Command",$nodeACommand -WorkingDirectory $projectRoot -PassThru -WindowStyle Normal

Start-Sleep -Seconds 2

Write-Host "Starting Node B on :6001 (client proxy on :5101)..." -ForegroundColor Yellow
$nodeBCommand = "`$env:TDS_PROXY_LISTEN=':5101'; & '.\\bin\\client_proxy.exe' -p2p -p2p-port :6001 -bootstrap 127.0.0.1:6000 -k-closest $KClosest"
$nodeB = Start-Process -FilePath "powershell.exe" -ArgumentList "-NoExit","-Command",$nodeBCommand -WorkingDirectory $projectRoot -PassThru -WindowStyle Normal

Start-Sleep -Seconds 2

Write-Host "Starting Node C on :6002 (client proxy on :5102)..." -ForegroundColor Yellow
$nodeCCommand = "`$env:TDS_PROXY_LISTEN=':5102'; & '.\\bin\\client_proxy.exe' -p2p -p2p-port :6002 -bootstrap 127.0.0.1:6000 -k-closest $KClosest"
$nodeC = Start-Process -FilePath "powershell.exe" -ArgumentList "-NoExit","-Command",$nodeCCommand -WorkingDirectory $projectRoot -PassThru -WindowStyle Normal

Start-Sleep -Seconds 5

Write-Host ""
Write-Host "=== Three P2P nodes started ===" -ForegroundColor Green
Write-Host "Node A: DHT=:6000, Proxy=:5100" -ForegroundColor Cyan
Write-Host "Node B: DHT=:6001, Proxy=:5101" -ForegroundColor Cyan
Write-Host "Node C: DHT=:6002, Proxy=:5102" -ForegroundColor Cyan
Write-Host ""

function Send-UdpJsonRequest {
    param(
        [string]$TargetHost,
        [int]$Port,
        [hashtable]$Payload,
        [int]$TimeoutMs = 3000,
        [switch]$NoResponse
    )

    $client = New-Object System.Net.Sockets.UdpClient
    try {
        $client.Client.ReceiveTimeout = $TimeoutMs
        $client.Connect($TargetHost, $Port)

        $json = $Payload | ConvertTo-Json -Compress
        $bytes = [System.Text.Encoding]::UTF8.GetBytes($json)
        [void]$client.Send($bytes, $bytes.Length)

        if ($NoResponse) {
            return [PSCustomObject]@{ status = "SENT" }
        }

        $endpoint = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
        try {
            $responseBytes = $client.Receive([ref]$endpoint)
        }
        catch {
            return [PSCustomObject]@{ status = "ERR"; error = $_.Exception.Message }
        }
        $responseText = [System.Text.Encoding]::UTF8.GetString($responseBytes)

        try {
            return ($responseText | ConvertFrom-Json)
        }
        catch {
            return [PSCustomObject]@{ status = "UNKNOWN"; error = "Non-JSON response: $responseText" }
        }
    }
    finally {
        $client.Close()
    }
}

Write-Host "Press Enter to begin interactive register/query flow..." -ForegroundColor Yellow
Read-Host | Out-Null

Write-Host ""
Write-Host "=== Interactive P2P Register + Query Flow ===" -ForegroundColor Cyan
Write-Host "Each service is registered, then queried from a different node." -ForegroundColor Gray
Write-Host ""

$steps = @(
    @{ Task = "web-api"; Address = "192.168.1.50:8080"; RegisterPort = 5100; RegisterNode = "Node A"; QueryPort = 5101; QueryNode = "Node B" },
    @{ Task = "database"; Address = "192.168.1.51:5432"; RegisterPort = 5102; RegisterNode = "Node C"; QueryPort = 5100; QueryNode = "Node A" },
    @{ Task = "cache"; Address = "192.168.1.52:6379"; RegisterPort = 5101; RegisterNode = "Node B"; QueryPort = 5102; QueryNode = "Node C" }
)

for ($i = 0; $i -lt $steps.Count; $i++) {
    $step = $steps[$i]
    $round = $i + 1

    Write-Host "--- Round $round/$($steps.Count) ---" -ForegroundColor Cyan
    Write-Host "Service: '$($step.Task)' -> $($step.Address)" -ForegroundColor Gray

    Write-Host "Press Enter to REGISTER '$($step.Task)' on $($step.RegisterNode) (port $($step.RegisterPort))..." -ForegroundColor Yellow
    Read-Host | Out-Null

    $registerResponse = Send-UdpJsonRequest -TargetHost "localhost" -Port $step.RegisterPort -NoResponse -Payload @{
        cmd = "REGISTER"
        task = $step.Task
        address = $step.Address
    }

    if ($registerResponse.status -eq "SENT") {
        Write-Host "  REGISTER result: SENT (proxy accepted packet)" -ForegroundColor Green
    }
    else {
        $errMsg = if ($null -ne $registerResponse.error -and $registerResponse.error -ne "") { $registerResponse.error } else { "Unknown error" }
        Write-Host "  REGISTER result: FAILED - $errMsg" -ForegroundColor Red
    }

    Write-Host "Press Enter to QUERY '$($step.Task)' from $($step.QueryNode) (port $($step.QueryPort))..." -ForegroundColor Yellow
    Read-Host | Out-Null

    $queryResponse = Send-UdpJsonRequest -TargetHost "localhost" -Port $step.QueryPort -Payload @{
        cmd = "QUERY"
        task = $step.Task
    }

    if ($queryResponse.status -eq "OK" -and $queryResponse.address -eq $step.Address) {
        Write-Host "  QUERY result: OK - $($queryResponse.address)" -ForegroundColor Green
    }
    elseif ($queryResponse.status -eq "OK") {
        Write-Host "  QUERY result: OK but unexpected address - got '$($queryResponse.address)', expected '$($step.Address)'" -ForegroundColor Yellow
    }
    else {
        $errMsg = if ($null -ne $queryResponse.error -and $queryResponse.error -ne "") { $queryResponse.error } else { "Unknown error" }
        Write-Host "  QUERY result: FAILED - $errMsg" -ForegroundColor Red
    }

    Write-Host ""
}

Write-Host "=== Manual Testing Instructions ===" -ForegroundColor Cyan
Write-Host ""
Write-Host "The nodes are running. To test manually, use another terminal:" -ForegroundColor Yellow
Write-Host ""
Write-Host "Using netcat (if installed):" -ForegroundColor White
Write-Host '  echo ''{"cmd":"REGISTER","task":"my-service","address":"10.0.0.1:9000"}'' | nc -u localhost 5100' -ForegroundColor Gray
Write-Host '  echo ''{"cmd":"QUERY","task":"my-service"}'' | nc -u localhost 5101' -ForegroundColor Gray
Write-Host ""
Write-Host "Using PowerShell UDP client:" -ForegroundColor White
Write-Host '  $client = New-Object System.Net.Sockets.UdpClient' -ForegroundColor Gray
Write-Host '  $client.Connect("localhost", 5100)' -ForegroundColor Gray
Write-Host '  $bytes = [Text.Encoding]::UTF8.GetBytes(''{"cmd":"REGISTER","task":"api","address":"10.0.0.5:8080"}'')' -ForegroundColor Gray
Write-Host '  $client.Send($bytes, $bytes.Length)' -ForegroundColor Gray
Write-Host '  $endpoint = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)' -ForegroundColor Gray
Write-Host '  $response = $client.Receive([ref]$endpoint)' -ForegroundColor Gray
Write-Host '  [Text.Encoding]::UTF8.GetString($response)' -ForegroundColor Gray
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
