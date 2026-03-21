#!/usr/bin/env pwsh
# P2P Mode Demo Script
# This script demonstrates the DHT-based P2P mode by starting multiple nodes

param(
    [int]$KClosest = 1
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
Write-Host "Demo settings: simplistic UI on all nodes, k-nearest replication factor = $KClosest" -ForegroundColor Gray
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
$nodeACommand = "`$host.UI.RawUI.WindowTitle='TDS P2P - Node A (DHT :6000, Proxy :5100)'; `$env:TDS_PROXY_LISTEN=':5100'; & '.\\bin\\client_proxy.exe' -p2p -simple-ui -p2p-port :6000 -k-closest $KClosest"
$nodeA = Start-Process -FilePath "powershell.exe" -ArgumentList "-NoExit","-Command",$nodeACommand -WorkingDirectory $projectRoot -PassThru -WindowStyle Normal

Start-Sleep -Seconds 2

Write-Host "Starting Node B on :6001 (client proxy on :5101)..." -ForegroundColor Yellow
$nodeBCommand = "`$host.UI.RawUI.WindowTitle='TDS P2P - Node B (DHT :6001, Proxy :5101)'; `$env:TDS_PROXY_LISTEN=':5101'; & '.\\bin\\client_proxy.exe' -p2p -simple-ui -p2p-port :6001 -bootstrap 127.0.0.1:6000  -k-closest $KClosest"
$nodeB = Start-Process -FilePath "powershell.exe" -ArgumentList "-NoExit","-Command",$nodeBCommand -WorkingDirectory $projectRoot -PassThru -WindowStyle Normal

Start-Sleep -Seconds 2

Write-Host "Starting Node C on :6002 (client proxy on :5102)..." -ForegroundColor Yellow
$nodeCCommand = "`$host.UI.RawUI.WindowTitle='TDS P2P - Node C (DHT :6002, Proxy :5102)'; `$env:TDS_PROXY_LISTEN=':5102'; & '.\\bin\\client_proxy.exe' -p2p -simple-ui -p2p-port :6002 -bootstrap 127.0.0.1:6000 -k-closest $KClosest"
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

function Get-Sha256Hex {
    param([string]$Value)

    $sha = [System.Security.Cryptography.SHA256]::Create()
    try {
        $bytes = [System.Text.Encoding]::UTF8.GetBytes($Value)
        $hash = $sha.ComputeHash($bytes)
        return ([BitConverter]::ToString($hash) -replace '-', '').ToLowerInvariant()
    }
    finally {
        $sha.Dispose()
    }
}

function Convert-HexToBigInt {
    param([string]$Hex)

    return [System.Numerics.BigInteger]::Parse(("0" + $Hex), [System.Globalization.NumberStyles]::AllowHexSpecifier)
}

function Get-ClockwiseDistance {
    param(
        [string]$FromHex,
        [string]$ToHex
    )

    $from = Convert-HexToBigInt -Hex $FromHex
    $to = Convert-HexToBigInt -Hex $ToHex
    $dist = $to - $from
    if ($dist -lt 0) {
        $ringSize = [System.Numerics.BigInteger]::Pow([System.Numerics.BigInteger]2, 256)
        $dist += $ringSize
    }

    return $dist
}

function Get-K1OwnerName {
    param(
        [string]$TaskName,
        [object[]]$Nodes
    )

    $taskHash = Get-Sha256Hex -Value $TaskName

    $bestNode = $null
    $bestDist = $null
    foreach ($node in $Nodes) {
        $dist = Get-ClockwiseDistance -FromHex $taskHash -ToHex $node.Hash
        if ($null -eq $bestNode -or $dist -lt $bestDist) {
            $bestNode = $node
            $bestDist = $dist
        }
    }

    return $bestNode.Name
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

Write-Host "=== Load Balancing Demo (Round-Robin) ===" -ForegroundColor Cyan
Write-Host "This registers 3 instances under one task, then queries repeatedly to show rotation." -ForegroundColor Gray
Write-Host ""

$lbTask = "web-api-lb"
$lbServices = @(
    @{ Address = "192.168.1.60:8080"; RegisterPort = 5100; RegisterNode = "Node A" },
    @{ Address = "192.168.1.61:8080"; RegisterPort = 5101; RegisterNode = "Node B" },
    @{ Address = "192.168.1.62:8080"; RegisterPort = 5102; RegisterNode = "Node C" }
)

Write-Host "Press Enter to register load-balanced services for task '$lbTask'..." -ForegroundColor Yellow
Read-Host | Out-Null

foreach ($svc in $lbServices) {
    $lbRegisterResponse = Send-UdpJsonRequest -TargetHost "localhost" -Port $svc.RegisterPort -NoResponse -Payload @{
        cmd = "REGISTER"
        task = $lbTask
        address = $svc.Address
    }

    if ($lbRegisterResponse.status -eq "SENT") {
        Write-Host "  Registered $($svc.Address) on $($svc.RegisterNode)" -ForegroundColor Green
    }
    else {
        $errMsg = if ($null -ne $lbRegisterResponse.error -and $lbRegisterResponse.error -ne "") { $lbRegisterResponse.error } else { "Unknown error" }
        Write-Host "  Register failed for $($svc.Address) on $($svc.RegisterNode): $errMsg" -ForegroundColor Red
    }
}

Start-Sleep -Milliseconds 500
Write-Host ""
Write-Host "Press Enter to run 12 queries from Node A (port 5100) and observe load balancing..." -ForegroundColor Yellow
Read-Host | Out-Null

$lbResults = @()
$lbCounts = @{}
for ($i = 1; $i -le 12; $i++) {
    $lbQueryResponse = Send-UdpJsonRequest -TargetHost "localhost" -Port 5100 -Payload @{
        cmd = "QUERY"
        task = $lbTask
    }

    if ($lbQueryResponse.status -eq "OK" -and $lbQueryResponse.address) {
        $addr = $lbQueryResponse.address
        $lbResults += $addr
        if (-not $lbCounts.ContainsKey($addr)) {
            $lbCounts[$addr] = 0
        }
        $lbCounts[$addr]++

        $color = "Green"
        if ($addr -eq "192.168.1.60:8080") {
            $color = "Green"
        }
        elseif ($addr -eq "192.168.1.61:8080") {
            $color = "Yellow"
        }
        elseif ($addr -eq "192.168.1.62:8080") {
            $color = "Cyan"
        }
        Write-Host "  Query $i -> $addr" -ForegroundColor $color
    }
    else {
        $errMsg = if ($null -ne $lbQueryResponse.error -and $lbQueryResponse.error -ne "") { $lbQueryResponse.error } else { "Unknown error" }
        $lbResults += "ERROR"
        if (-not $lbCounts.ContainsKey("ERROR")) {
            $lbCounts["ERROR"] = 0
        }
        $lbCounts["ERROR"]++
        Write-Host "  Query $i -> FAILED ($errMsg)" -ForegroundColor Red
    }
}

Write-Host ""
Write-Host "Round-robin sequence:" -ForegroundColor Cyan

$addressToNode = @{
    "192.168.1.60:8080" = "Node A"
    "192.168.1.61:8080" = "Node B"
    "192.168.1.62:8080" = "Node C"
}

$labelledResults = @()
foreach ($addr in $lbResults) {
    if ($addressToNode.ContainsKey($addr)) {
        $labelledResults += ($addressToNode[$addr] + " (" + $addr + ")")
    }
    else {
        $labelledResults += $addr
    }
}

Write-Host "  $($labelledResults -join '  ->  ')" -ForegroundColor White
Write-Host ""
Write-Host "Distribution summary:" -ForegroundColor Cyan

$totalLbQueries = $lbResults.Count
$chartOrder = @("192.168.1.60:8080", "192.168.1.61:8080", "192.168.1.62:8080", "ERROR")
foreach ($addr in $chartOrder) {
    if (-not $lbCounts.ContainsKey($addr)) {
        continue
    }

    $count = [int]$lbCounts[$addr]
    $pct = if ($totalLbQueries -gt 0) { [math]::Round(($count * 100.0) / $totalLbQueries, 1) } else { 0 }
    $bar = ""
    if ($count -gt 0) {
        $bar = "#" * $count
    }

    $color = "White"
    if ($addr -eq "192.168.1.60:8080") {
        $color = "Green"
    }
    elseif ($addr -eq "192.168.1.61:8080") {
        $color = "Yellow"
    }
    elseif ($addr -eq "192.168.1.62:8080") {
        $color = "Cyan"
    }
    elseif ($addr -eq "ERROR") {
        $color = "Red"
    }

    $label = $addr
    if ($addressToNode.ContainsKey($addr)) {
        $label = $addressToNode[$addr] + " " + $addr
    }

    $line = "  " + $label.PadRight(28) + "  " + $bar.PadRight(14) + "  (" + $count + " queries, " + $pct + "%)"
    Write-Host $line -ForegroundColor $color
}

Write-Host ""
Write-Host "Expected cycle: Node A -> Node B -> Node C (repeating)." -ForegroundColor Gray

$expectedCycle = @("192.168.1.60:8080", "192.168.1.61:8080", "192.168.1.62:8080")
$cycleMatches = $true
for ($i = 0; $i -lt $lbResults.Count; $i++) {
    if ($lbResults[$i] -ne $expectedCycle[$i % $expectedCycle.Count]) {
        $cycleMatches = $false
        break
    }
}

if ($cycleMatches -and -not $lbCounts.ContainsKey("ERROR")) {
    Write-Host "[PASS] Round-robin order is correct and stable across all queries." -ForegroundColor Green
}
else {
    Write-Host "[FAIL] Sequence did not follow strict A -> B -> C round-robin for all queries." -ForegroundColor Red
}
Write-Host ""

Write-Host "=== Cluster Propagation Demo ===" -ForegroundColor Cyan
Write-Host "Bulk-registering tasks from Node A, then resolving from Nodes B and C." -ForegroundColor Gray
Write-Host "Watch the other node windows for 'Store received:' events to see cross-node propagation." -ForegroundColor Gray
Write-Host ""

$propPrefix = "prop-task"
$propCount = 120
$propTasks = @()

$ringNodes = @(
    @{ Name = "Node A"; Address = "127.0.0.1:6000"; Color = "Green" },
    @{ Name = "Node B"; Address = "127.0.0.1:6001"; Color = "Yellow" },
    @{ Name = "Node C"; Address = "127.0.0.1:6002"; Color = "Cyan" }
)

foreach ($n in $ringNodes) {
    $n["Hash"] = Get-Sha256Hex -Value $n.Address
}

Write-Host "Press Enter to register $propCount tasks via Node A (port 5100)..." -ForegroundColor Yellow
Read-Host | Out-Null

for ($i = 1; $i -le $propCount; $i++) {
    $taskName = "${propPrefix}-${i}"
    $taskAddr = "10.10.0.$i:7$('{0:d3}' -f $i)"
    $expectedOwner = Get-K1OwnerName -TaskName $taskName -Nodes $ringNodes
    $propTasks += @{ Task = $taskName; Address = $taskAddr; ExpectedOwner = $expectedOwner }

    [void](Send-UdpJsonRequest -TargetHost "localhost" -Port 5100 -NoResponse -Payload @{
        cmd = "REGISTER"
        task = $taskName
        address = $taskAddr
    })

    if ($i % 6 -eq 0 -or $i -eq $propCount) {
        Write-Host "  Registered $i/$propCount tasks from Node A" -ForegroundColor Green
    }
}

Start-Sleep -Seconds 1
Write-Host ""

Write-Host "Expected k=1 ownership distribution (hash-ring prediction):" -ForegroundColor Cyan
$ownerCounts = @{ "Node A" = 0; "Node B" = 0; "Node C" = 0 }
foreach ($entry in $propTasks) {
    $ownerCounts[$entry.ExpectedOwner]++
}

foreach ($node in $ringNodes) {
    $count = [int]$ownerCounts[$node.Name]
    $pct = [math]::Round(($count * 100.0) / $propTasks.Count, 1)
    $barUnits = [int][math]::Round($count / 4.0)
    if ($barUnits -lt 1) {
        $barUnits = 0
    }
    $bar = "#" * $barUnits
    Write-Host ("  " + $node.Name + " " + $node.Address + "  " + $bar + "  (" + $count + " tasks, " + $pct + "%)") -ForegroundColor $node.Color
}

Write-Host ""
Write-Host "Press Enter to verify propagation with queries from Node B (5101) and Node C (5102)..." -ForegroundColor Yellow
Read-Host | Out-Null

$verifyNodes = @(
    @{ Name = "Node B"; Port = 5101; Color = "Yellow" },
    @{ Name = "Node C"; Port = 5102; Color = "Cyan" }
)

foreach ($node in $verifyNodes) {
    $ok = 0
    $fail = 0

    foreach ($entry in $propTasks) {
        $resp = Send-UdpJsonRequest -TargetHost "localhost" -Port $node.Port -Payload @{
            cmd = "QUERY"
            task = $entry.Task
        }

        if ($resp.status -eq "OK" -and $resp.address -eq $entry.Address) {
            $ok++
        }
        else {
            $fail++
        }
    }

    $pct = [math]::Round(($ok * 100.0) / $propTasks.Count, 1)
    Write-Host ("  " + $node.Name + " verification: " + $ok + "/" + $propTasks.Count + " OK (" + $pct + "%), " + $fail + " failed") -ForegroundColor $node.Color
}

Write-Host ""
Write-Host "Interpretation:" -ForegroundColor Cyan
Write-Host "  If Node B/C can resolve tasks registered on Node A, the cluster propagated ownership + routing correctly." -ForegroundColor Gray
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
