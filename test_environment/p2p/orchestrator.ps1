# VM Orchestrator for P2P (DHT) testing
# - Deploys client_proxy + P2P load scripts to 3 VMs over SSH
# - Starts a 3-node DHT network (client_proxy -p2p)
# - Runs bulk REGISTER, then concurrent QUERY load

param(
    [string[]]$NodeVMs = @("192.168.0.180", "192.168.0.181", "192.168.0.182"),
    [string]$Username = "user",
    [string]$RemoteScriptPath = "/tmp/tds-p2p-test",

    [int]$BaseP2PPort = 6000,
    [int]$BaseProxyPort = 5100,

    [int]$ServicesPerNode = 20000,
    [int]$NumTasks = 500,

    [int]$QueryThreadsPerNode = 20,
    [int]$QueriesPerThread = 20000,
    [int]$ThinkTimeMs = 0,

    [int]$PropagationDelaySeconds = 5,

    [string]$RemoteGoos = "linux",
    [string]$RemoteGoarch = "amd64",

    [switch]$CleanupOnly
)

$ErrorActionPreference = "Stop"

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "TDS P2P VM Orchestrator (3 nodes)" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Nodes: $($NodeVMs -join ', ')" -ForegroundColor White
Write-Host "Username: $Username" -ForegroundColor White
Write-Host "Remote path: $RemoteScriptPath" -ForegroundColor White
Write-Host "Services/node: $ServicesPerNode | Tasks: $NumTasks" -ForegroundColor White
Write-Host "Query: threads/node=$QueryThreadsPerNode queries/thread=$QueriesPerThread think=${ThinkTimeMs}ms" -ForegroundColor White
Write-Host "" 

if ($NodeVMs.Count -ne 3) {
    throw "This orchestrator expects exactly 3 VMs (got $($NodeVMs.Count))."
}

if (-not (Get-Command ssh -ErrorAction SilentlyContinue)) {
    throw "ssh not found on this machine"
}
if (-not (Get-Command scp -ErrorAction SilentlyContinue)) {
    throw "scp not found on this machine"
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "go not found on this machine"
}

$repoRoot = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path))
Set-Location $repoRoot

$sshKeyPath = "$env:USERPROFILE\.ssh\id_rsa"

function Invoke-SSHCommand {
    param(
        [string]$VMHost,
        [string]$Command
    )

    ssh -i $sshKeyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL "${Username}@${VMHost}" "$Command" 2>&1
}

function Copy-ToSSHFile {
    param(
        [string]$VMHost,
        [string]$LocalPath,
        [string]$RemotePath
    )

    scp -i $sshKeyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL $LocalPath "${Username}@${VMHost}:${RemotePath}" 2>&1 | Out-Null
}

function Copy-FromSSHFile {
    param(
        [string]$VMHost,
        [string]$RemotePath,
        [string]$LocalPath
    )

    scp -i $sshKeyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL "${Username}@${VMHost}:${RemotePath}" $LocalPath 2>&1 | Out-Null
}

function Stop-RemoteProcesses {
    param([string]$VM)

    Write-Host "[CLEANUP] $VM" -ForegroundColor Yellow
    Invoke-SSHCommand -VMHost $VM -Command "pkill -f 'client_proxy.*-p2p' || true; pkill -f 'start_client_proxy_p2p.ps1' || true; pkill -f 'p2p_register_load.ps1' || true; pkill -f 'p2p_query_load.ps1' || true; rm -f $RemoteScriptPath/*.done $RemoteScriptPath/*.pid || true" | Out-Null
}

if ($CleanupOnly) {
    foreach ($vm in $NodeVMs) {
        Stop-RemoteProcesses -VM $vm
    }
    Write-Host "[DONE] Cleanup complete" -ForegroundColor Green
    exit 0
}

Write-Host "[CHECK] SSH connectivity" -ForegroundColor Yellow
foreach ($vm in $NodeVMs) {
    $out = Invoke-SSHCommand -VMHost $vm -Command "echo OK"
    if ($out -notmatch "OK") {
        throw "SSH failed for ${vm}: $out"
    }
    Write-Host "  [OK] $vm" -ForegroundColor Green
}
Write-Host "" 

Write-Host "[CHECK] pwsh availability" -ForegroundColor Yellow
foreach ($vm in $NodeVMs) {
    $out = Invoke-SSHCommand -VMHost $vm -Command "command -v pwsh >/dev/null 2>&1 && echo OK || echo NO"
    if ($out -notmatch "OK") {
        throw "pwsh not found on $vm"
    }
    Write-Host "  [OK] $vm" -ForegroundColor Green
}
Write-Host "" 

# Build client_proxy for remote OS/arch
Write-Host "[BUILD] client_proxy for $RemoteGoos/$RemoteGoarch" -ForegroundColor Yellow
$localBuildDir = Join-Path $repoRoot "build\p2p_test"
New-Item -ItemType Directory -Path $localBuildDir -Force | Out-Null
$localBin = Join-Path $localBuildDir "client_proxy_${RemoteGoos}_${RemoteGoarch}"

$oldGoos = $env:GOOS
$oldGoarch = $env:GOARCH
$env:GOOS = $RemoteGoos
$env:GOARCH = $RemoteGoarch

go build -o $localBin ./cmd/client_proxy
if ($LASTEXITCODE -ne 0) {
    throw "go build failed"
}

$env:GOOS = $oldGoos
$env:GOARCH = $oldGoarch

Write-Host "  [OK] Built $localBin" -ForegroundColor Green
Write-Host "" 

# Deploy scripts + binary
Write-Host "[DEPLOY] Uploading scripts and binary" -ForegroundColor Yellow
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path

foreach ($vm in $NodeVMs) {
    Invoke-SSHCommand -VMHost $vm -Command "mkdir -p $RemoteScriptPath" | Out-Null

    Copy-ToSSHFile -VMHost $vm -LocalPath $localBin -RemotePath "$RemoteScriptPath/client_proxy"
    Copy-ToSSHFile -VMHost $vm -LocalPath (Join-Path $scriptDir "start_client_proxy_p2p.ps1") -RemotePath "$RemoteScriptPath/start_client_proxy_p2p.ps1"
    Copy-ToSSHFile -VMHost $vm -LocalPath (Join-Path $scriptDir "p2p_register_load.ps1") -RemotePath "$RemoteScriptPath/p2p_register_load.ps1"
    Copy-ToSSHFile -VMHost $vm -LocalPath (Join-Path $scriptDir "p2p_query_load.ps1") -RemotePath "$RemoteScriptPath/p2p_query_load.ps1"

    Invoke-SSHCommand -VMHost $vm -Command "chmod +x $RemoteScriptPath/client_proxy" | Out-Null

    Write-Host "  [OK] $vm" -ForegroundColor Green
}
Write-Host "" 

# Start proxies
Write-Host "[START] Starting 3-node P2P network" -ForegroundColor Yellow
$bootstrapVM = $NodeVMs[0]
$bootstrapAddr = "${bootstrapVM}:${BaseP2PPort}"

for ($i = 0; $i -lt 3; $i++) {
    $nodeId = $i + 1
    $vm = $NodeVMs[$i]
    $p2pPort = $BaseP2PPort + $i
    $proxyPort = $BaseProxyPort + $i

    $bootstrap = if ($i -eq 0) { "" } else { $bootstrapAddr }

    $cmd = @(
        "cd $RemoteScriptPath",
        "rm -f client_proxy_node${nodeId}.pid || true",
        "nohup pwsh -File start_client_proxy_p2p.ps1 -ProxyListen ':$proxyPort' -P2PListen ':$p2pPort' -Bootstrap '$bootstrap' -LogFile client_proxy_node${nodeId}.log > start_proxy_node${nodeId}.nohup 2>&1 < /dev/null &",
        "echo \$! > client_proxy_node${nodeId}.pid"
    ) -join "; "

    Invoke-SSHCommand -VMHost $vm -Command $cmd | Out-Null
    Write-Host "  [OK] Node $nodeId on $vm (proxy :$proxyPort, p2p :$p2pPort)" -ForegroundColor Green
    Start-Sleep -Seconds 1
}

Write-Host "" 
Write-Host "[WAIT] ${PropagationDelaySeconds}s for DHT stabilization" -ForegroundColor Yellow
Start-Sleep -Seconds $PropagationDelaySeconds

# Run registrations in parallel (background on each VM)
Write-Host "" 
Write-Host "[PHASE] Bulk REGISTER" -ForegroundColor Cyan
for ($i = 0; $i -lt 3; $i++) {
    $nodeId = $i + 1
    $vm = $NodeVMs[$i]
    $proxyPort = $BaseProxyPort + $i

    $doneFile = "register_node${nodeId}.done"

    $cmd = @(
        "cd $RemoteScriptPath",
        "rm -f $doneFile || true",
        "nohup pwsh -File p2p_register_load.ps1 -ProxyPort $proxyPort -NodeId $nodeId -NumServices $ServicesPerNode -NumTasks $NumTasks -ThinkTimeMs 0 -LogFile register_node${nodeId}.log -DoneFile $doneFile > register_node${nodeId}.nohup 2>&1 < /dev/null &",
        "echo \$! > register_node${nodeId}.pid"
    ) -join "; "

    Invoke-SSHCommand -VMHost $vm -Command $cmd | Out-Null
    Write-Host "  [OK] Started REGISTER on node $nodeId ($vm)" -ForegroundColor Green
}

Write-Host "[WAIT] Waiting for all REGISTER phases to finish..." -ForegroundColor Yellow
while ($true) {
    $doneCount = 0
    for ($i = 0; $i -lt 3; $i++) {
        $nodeId = $i + 1
        $vm = $NodeVMs[$i]
        $out = Invoke-SSHCommand -VMHost $vm -Command "test -f $RemoteScriptPath/register_node${nodeId}.done && echo DONE || echo NO"
        if ($out -match "DONE") { $doneCount++ }
    }

    if ($doneCount -eq 3) { break }
    Write-Host "  ... $doneCount/3 done" -ForegroundColor Gray
    Start-Sleep -Seconds 2
}

Write-Host "[OK] REGISTER complete on all nodes" -ForegroundColor Green
Write-Host "" 
Write-Host "[WAIT] ${PropagationDelaySeconds}s for DHT propagation" -ForegroundColor Yellow
Start-Sleep -Seconds $PropagationDelaySeconds

# Run queries in parallel
Write-Host "" 
Write-Host "[PHASE] Bulk QUERY" -ForegroundColor Cyan
for ($i = 0; $i -lt 3; $i++) {
    $nodeId = $i + 1
    $vm = $NodeVMs[$i]
    $proxyPort = $BaseProxyPort + $i

    $doneFile = "query_node${nodeId}.done"

    $cmd = @(
        "cd $RemoteScriptPath",
        "rm -f $doneFile || true",
        "nohup pwsh -File p2p_query_load.ps1 -ProxyPort $proxyPort -NumThreads $QueryThreadsPerNode -QueriesPerThread $QueriesPerThread -NumTasks $NumTasks -ThinkTimeMs $ThinkTimeMs -LogFile query_node${nodeId}.log -DoneFile $doneFile > query_node${nodeId}.nohup 2>&1 < /dev/null &",
        "echo \$! > query_node${nodeId}.pid"
    ) -join "; "

    Invoke-SSHCommand -VMHost $vm -Command $cmd | Out-Null
    Write-Host "  [OK] Started QUERY on node $nodeId ($vm)" -ForegroundColor Green
}

Write-Host "[WAIT] Waiting for all QUERY phases to finish..." -ForegroundColor Yellow
while ($true) {
    $doneCount = 0
    for ($i = 0; $i -lt 3; $i++) {
        $nodeId = $i + 1
        $vm = $NodeVMs[$i]
        $out = Invoke-SSHCommand -VMHost $vm -Command "test -f $RemoteScriptPath/query_node${nodeId}.done && echo DONE || echo NO"
        if ($out -match "DONE") { $doneCount++ }
    }

    if ($doneCount -eq 3) { break }
    Write-Host "  ... $doneCount/3 done" -ForegroundColor Gray
    Start-Sleep -Seconds 3
}

Write-Host "[OK] QUERY complete on all nodes" -ForegroundColor Green

# Collect logs
Write-Host "" 
Write-Host "[COLLECT] Downloading logs" -ForegroundColor Yellow
$resultsDir = Join-Path $repoRoot ("test_results_p2p_" + (Get-Date -Format 'yyyyMMdd_HHmmss'))
New-Item -ItemType Directory -Path $resultsDir -Force | Out-Null

for ($i = 0; $i -lt 3; $i++) {
    $nodeId = $i + 1
    $vm = $NodeVMs[$i]
    $vmFolder = Join-Path $resultsDir ("node_${nodeId}_" + ($vm -replace '\\.', '_'))
    New-Item -ItemType Directory -Path $vmFolder -Force | Out-Null

    $remoteFiles = @(
        "client_proxy_node${nodeId}.log",
        "start_proxy_node${nodeId}.nohup",
        "register_node${nodeId}.log",
        "register_node${nodeId}.nohup",
        "query_node${nodeId}.log",
        "query_node${nodeId}.nohup"
    )

    foreach ($f in $remoteFiles) {
        try {
            Copy-FromSSHFile -VMHost $vm -RemotePath "$RemoteScriptPath/$f" -LocalPath (Join-Path $vmFolder $f)
        } catch {
            # best-effort; not all files may exist
        }
    }

    $summary = Invoke-SSHCommand -VMHost $vm -Command "tail -n 1 $RemoteScriptPath/query_node${nodeId}.log 2>/dev/null || true"
    if ($summary) {
        Write-Host "  Node $nodeId summary: $summary" -ForegroundColor Cyan
    }
}

Write-Host "" 
Write-Host "[DONE] Results saved to: $resultsDir" -ForegroundColor Green
Write-Host "Tip: run cleanup with -CleanupOnly to stop proxies." -ForegroundColor Gray
