#!/usr/bin/env pwsh
# P2P Mode Demo Script (Linux PowerShell edition)
# Starts three background client_proxy nodes and runs the same interactive
# register/query flow as the Windows demo.

param(
    [int]$KClosest = 3
)

Write-Host "=== TDS P2P Mode Demo (Linux PS) ===" -ForegroundColor Cyan
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

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Host "Go is not installed or not on PATH." -ForegroundColor Red
    exit 1
}

if (-not (Get-Command pwsh -ErrorAction SilentlyContinue)) {
    Write-Host "pwsh is not installed or not on PATH." -ForegroundColor Red
    exit 1
}

$binDir = Join-Path $projectRoot "bin"
$logDir = Join-Path $projectRoot "logs"
New-Item -ItemType Directory -Force -Path $binDir | Out-Null
New-Item -ItemType Directory -Force -Path $logDir | Out-Null

$clientProxyPath = Join-Path $binDir "client_proxy"

Write-Host "Building project..." -ForegroundColor Yellow
go build -o $clientProxyPath ./cmd/client_proxy
if ($LASTEXITCODE -ne 0) {
    Write-Host "Build failed!" -ForegroundColor Red
    exit 1
}
Write-Host "Build complete!" -ForegroundColor Green
Write-Host ""

function Start-P2PNode {
    param(
        [string]$Name,
        [string]$ProxyListen,
        [string]$DhtPort,
        [string]$Bootstrap,
        [int]$KClosest,
        [string]$ProjectRoot,
        [string]$ClientProxyPath,
        [string]$LogDir
    )

    $logFile = Join-Path $LogDir ("p2p_{0}.log" -f $Name.ToLower())

    $cmd = "`$env:TDS_PROXY_LISTEN='$ProxyListen'; & '$ClientProxyPath' -p2p -p2p-port $DhtPort -k-closest $KClosest -background"
    if (-not [string]::IsNullOrWhiteSpace($Bootstrap)) {
        $cmd += " -bootstrap $Bootstrap"
    }

    $startArgs = @{
        FilePath               = "pwsh"
        ArgumentList           = @("-NoLogo", "-NoProfile", "-Command", $cmd)
        WorkingDirectory       = $ProjectRoot
        RedirectStandardOutput = $logFile
        RedirectStandardError  = $logFile
        PassThru               = $true
    }

    return Start-Process @startArgs
}

Write-Host "Starting Node A on :6000 (client proxy on :5100)..." -ForegroundColor Yellow
$nodeA = Start-P2PNode -Name "nodeA" -ProxyListen ":5100" -DhtPort ":6000" -Bootstrap "" -KClosest $KClosest -ProjectRoot $projectRoot -ClientProxyPath $clientProxyPath -LogDir $logDir
Start-Sleep -Seconds 2

Write-Host "Starting Node B on :6001 (client proxy on :5101)..." -ForegroundColor Yellow
$nodeB = Start-P2PNode -Name "nodeB" -ProxyListen ":5101" -DhtPort ":6001" -Bootstrap "127.0.0.1:6000" -KClosest $KClosest -ProjectRoot $projectRoot -ClientProxyPath $clientProxyPath -LogDir $logDir
Start-Sleep -Seconds 2

Write-Host "Starting Node C on :6002 (client proxy on :5102)..." -ForegroundColor Yellow
$nodeC = Start-P2PNode -Name "nodeC" -ProxyListen ":5102" -DhtPort ":6002" -Bootstrap "127.0.0.1:6000" -KClosest $KClosest -ProjectRoot $projectRoot -ClientProxyPath $clientProxyPath -LogDir $logDir
Start-Sleep -Seconds 4

Write-Host ""
Write-Host "=== Three P2P nodes started ===" -ForegroundColor Green
Write-Host "Node A: DHT=:6000, Proxy=:5100" -ForegroundColor Cyan
Write-Host "Node B: DHT=:6001, Proxy=:5101" -ForegroundColor Cyan
Write-Host "Node C: DHT=:6002, Proxy=:5102" -ForegroundColor Cyan
Write-Host "Logs: $logDir/p2p_node*.log" -ForegroundColor Gray
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

    $registerResponse = Send-UdpJsonRequest -TargetHost "127.0.0.1" -Port $step.RegisterPort -NoResponse -Payload @{
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

    $queryResponse = Send-UdpJsonRequest -TargetHost "127.0.0.1" -Port $step.QueryPort -Payload @{
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

Write-Host "Press Enter to stop all nodes..." -ForegroundColor Yellow
Read-Host | Out-Null

Write-Host ""
Write-Host "Stopping nodes..." -ForegroundColor Yellow
foreach ($p in @($nodeA, $nodeB, $nodeC)) {
    if ($null -ne $p -and -not $p.HasExited) {
        Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue
    }
}

Write-Host "Demo complete!" -ForegroundColor Green
