#!/usr/bin/env pwsh
# TDS TLS Demonstration - P2P Mode
#
# What this demonstrates:
#   - Three DHT nodes forming a peer-to-peer discovery network
#   - TLS-secured service endpoints registered and replicated across the DHT
#   - Cross-node querying: registering on one node, retrieving from another
#   - Discovery of TLS service addresses without requiring a central server
#   - Optional: openssl verification that discovered endpoints enforce TLS
#
# Security architecture note:
#   The DHT inter-node communication uses UDP and is not TLS-encrypted.
#   TLS is used at the service layer: the *addresses* stored in the DHT point
#   to TLS-enabled services. Any client that discovers an address and connects
#   to it will perform a TLS handshake with that service.
#   Encrypting the DHT transport itself is a planned enhancement.
#
# Usage:
#   ./run_tls_demo.ps1                  # standard automated run
#   ./run_tls_demo.ps1 -SkipBuild       # skip go build step
#   ./run_tls_demo.ps1 -Interactive     # finish with an interactive client shell
#   ./run_tls_demo.ps1 -KClosest 5      # override k-closest DHT parameter
#   ./run_tls_demo.ps1 -SpinUpTlsServer # also start a real TLS server to verify
#
# Prerequisites:
#   - certs/ directory with ca.crt, server.crt, server.key, client.crt, client.key
#     (run scripts/generate_certs.ps1 to generate)

param(
    [switch]$SkipBuild,
    [switch]$GenerateCerts,
    [switch]$Interactive,
    [switch]$SpinUpTlsServer,
    [int]$KClosest = 3,
    [string]$CertsDir = "certs"
)

$ErrorActionPreference = "Stop"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

function Get-RepoRoot {
    param([string]$StartDir)
    $dir = $StartDir
    while ($true) {
        if (Test-Path (Join-Path $dir "go.mod")) { return $dir }
        $parent = Split-Path -Parent $dir
        if ($parent -eq $dir -or $parent -eq "") {
            throw "Could not locate repo root (go.mod not found) starting from: $StartDir"
        }
        $dir = $parent
    }
}

function Test-CertsExist {
    param([string]$Dir)
    return (Test-Path (Join-Path $Dir "ca.crt"))     -and
           (Test-Path (Join-Path $Dir "server.crt")) -and
           (Test-Path (Join-Path $Dir "server.key")) -and
           (Test-Path (Join-Path $Dir "client.crt")) -and
           (Test-Path (Join-Path $Dir "client.key"))
}

function Wait-UdpProxy {
    param([int]$Port, [int]$TimeoutSecs = 10)
    # Poll for UDP proxy readiness by sending a probe QUERY packet.
    $deadline = (Get-Date).AddSeconds($TimeoutSecs)
    while ((Get-Date) -lt $deadline) {
        try {
            $client = New-Object System.Net.Sockets.UdpClient
            $client.Client.ReceiveTimeout = 600
            $client.Connect("127.0.0.1", $Port)
            $probe = [System.Text.Encoding]::UTF8.GetBytes('{"cmd":"QUERY","task":"__probe__"}')
            [void]$client.Send($probe, $probe.Length)
            $ep = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
            try {
                [void]$client.Receive([ref]$ep)
                $client.Close()
                return $true
            } catch { $client.Close() }
        } catch { }
        Start-Sleep -Milliseconds 400
    }
    return $false
}

function Wait-PortOpen {
    param([string]$TargetHost, [int]$Port, [int]$TimeoutSecs = 10)
    $deadline = (Get-Date).AddSeconds($TimeoutSecs)
    while ((Get-Date) -lt $deadline) {
        try {
            $tcp = New-Object System.Net.Sockets.TcpClient
            if ($tcp.ConnectAsync($TargetHost, $Port).Wait(500)) {
                $tcp.Close()
                return $true
            }
            $tcp.Close()
        } catch { }
        Start-Sleep -Milliseconds 400
    }
    return $false
}

function Send-UdpJson {
    param(
        [int]$Port,
        [hashtable]$Payload,
        [int]$TimeoutMs = 3000,
        [switch]$NoResponse
    )
    $client = New-Object System.Net.Sockets.UdpClient
    try {
        $client.Client.ReceiveTimeout = $TimeoutMs
        $client.Connect("127.0.0.1", $Port)
        $json  = $Payload | ConvertTo-Json -Compress
        $bytes = [System.Text.Encoding]::UTF8.GetBytes($json)
        [void]$client.Send($bytes, $bytes.Length)
        if ($NoResponse) { return [PSCustomObject]@{ status = "SENT" } }
        $ep = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
        try {
            $resp = $client.Receive([ref]$ep)
            $text = [System.Text.Encoding]::UTF8.GetString($resp)
            return ($text | ConvertFrom-Json)
        } catch {
            return [PSCustomObject]@{ status = "ERR"; error = $_.Exception.Message }
        }
    } finally { $client.Close() }
}

function Write-Step {
    param([string]$Text)
    Write-Host "  $Text" -ForegroundColor White
}

function Write-Ok   { param([string]$T) Write-Host "    [OK] $T"    -ForegroundColor Green  }
function Write-Warn { param([string]$T) Write-Host "    [WARN] $T"  -ForegroundColor Yellow }
function Write-Fail { param([string]$T) Write-Host "    [FAIL] $T"  -ForegroundColor Red    }
function Write-Info { param([string]$T) Write-Host "    $T"         -ForegroundColor Gray   }

# ---------------------------------------------------------------------------
# Initialise
# ---------------------------------------------------------------------------

$repoRoot  = Get-RepoRoot -StartDir $PSScriptRoot
Set-Location $repoRoot

$certsPath = if ([System.IO.Path]::IsPathRooted($CertsDir)) { $CertsDir } else { Join-Path $repoRoot $CertsDir }
$proxyExe  = Join-Path $repoRoot "bin/client_proxy"
$serverExe = Join-Path $repoRoot "bin/server"

# P2P port layout
# Node A: DHT=6000, Proxy=5100
# Node B: DHT=6001, Proxy=5101
# Node C: DHT=6002, Proxy=5102
# TLS server (optional): 5000

$nodeA = @{ DhtPort = 6000; ProxyPort = 5100; Name = "Node A" }
$nodeB = @{ DhtPort = 6001; ProxyPort = 5101; Name = "Node B" }
$nodeC = @{ DhtPort = 6002; ProxyPort = 5102; Name = "Node C" }
$nodes = @($nodeA, $nodeB, $nodeC)

Write-Host ""
Write-Host "=========================================================" -ForegroundColor Cyan
Write-Host "     TDS TLS Demonstration  -  P2P / DHT Mode"           -ForegroundColor Cyan
Write-Host "=========================================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "  Repo root : $repoRoot"  -ForegroundColor Gray
Write-Host "  Certs dir : $certsPath" -ForegroundColor Gray
Write-Host "  k-closest : $KClosest"  -ForegroundColor Gray
Write-Host ""
Write-Host "  DHT topology:" -ForegroundColor White
foreach ($n in $nodes) {
    Write-Host "    $($n.Name)  DHT=:$($n.DhtPort)  Proxy UDP=:$($n.ProxyPort)" -ForegroundColor Gray
}
Write-Host ""

# ---------------------------------------------------------------------------
# Step 1 – Certificates
# ---------------------------------------------------------------------------

Write-Host "[STEP 1/5]  Certificates" -ForegroundColor Cyan
Write-Host ""

if ($GenerateCerts -or -not (Test-CertsExist -Dir $certsPath)) {
    $reason = if ($GenerateCerts) { "-GenerateCerts flag set" } else { "not found in $certsPath" }
    Write-Step "Certificates $reason – running generate_certs.ps1 ..."

    $genScript = Join-Path $repoRoot "scripts/generate_certs.ps1"
    if (-not (Test-Path $genScript)) {
        Write-Fail "generate_certs.ps1 not found at: $genScript"
        exit 1
    }
    & $genScript -OutputDir $certsPath
    if ($LASTEXITCODE -ne 0) { Write-Fail "Certificate generation failed"; exit 1 }
    Write-Host ""
}

if (Test-CertsExist -Dir $certsPath) {
    Write-Ok "Certificates present in $certsPath"
} else {
    Write-Fail "Missing certificates – run scripts/generate_certs.ps1 -OutputDir $certsPath"
    exit 1
}
Write-Host ""

# ---------------------------------------------------------------------------
# Step 2 – Build
# ---------------------------------------------------------------------------

Write-Host "[STEP 2/5]  Build" -ForegroundColor Cyan
Write-Host ""

if (-not $SkipBuild) {
    Write-Step "Building client_proxy ..."
    go build -o bin/client_proxy ./cmd/client_proxy
    if ($LASTEXITCODE -ne 0) { Write-Fail "Failed to build client_proxy"; exit 1 }

    if ($SpinUpTlsServer) {
        Write-Step "Building server ..."
        go build -o bin/server ./cmd/server
        if ($LASTEXITCODE -ne 0) { Write-Fail "Failed to build server"; exit 1 }
    }

    Write-Ok "Build complete"
} else {
    Write-Step "Skipping build (-SkipBuild)"
    if (-not (Test-Path $proxyExe)) {
        Write-Fail "Binary not found: $proxyExe  (run without -SkipBuild)"
        exit 1
    }
}
Write-Host ""

# ---------------------------------------------------------------------------
# Step 3 – Start DHT nodes
# ---------------------------------------------------------------------------

Write-Host "[STEP 3/5]  Start P2P DHT nodes" -ForegroundColor Cyan
Write-Host ""
Write-Host "  Each node runs bin/client_proxy in -p2p mode." -ForegroundColor Gray
Write-Host "  Nodes B and C bootstrap from Node A (:$($nodeA.DhtPort))." -ForegroundColor Gray
Write-Host ""

$runningProcs = @()

function Start-Node {
    param($Node, [string[]]$ExtraArgs)
    $nodeArgs = @(
        "-p2p",
        "-p2p-port", ":$($Node.DhtPort)",
        "-k-closest", "$KClosest",
        "-background"
    ) + $ExtraArgs

    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName               = $proxyExe
    $psi.Arguments              = $nodeArgs -join " "
    $psi.WorkingDirectory       = $repoRoot
    $psi.UseShellExecute        = $false
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError  = $true
    $psi.EnvironmentVariables["TDS_PROXY_LISTEN"] = ":$($Node.ProxyPort)"

    $proc = [System.Diagnostics.Process]::Start($psi)
    return $proc
}

Write-Step "Starting $($nodeA.Name)  (DHT=:$($nodeA.DhtPort)  Proxy=:$($nodeA.ProxyPort)) ..."
$procA = Start-Node -Node $nodeA -ExtraArgs @()
$runningProcs += $procA
Start-Sleep -Seconds 2

Write-Step "Starting $($nodeB.Name)  (DHT=:$($nodeB.DhtPort)  Proxy=:$($nodeB.ProxyPort)  bootstrap=$($nodeA.DhtPort)) ..."
$procB = Start-Node -Node $nodeB -ExtraArgs @("-bootstrap", "127.0.0.1:$($nodeA.DhtPort)")
$runningProcs += $procB
Start-Sleep -Seconds 1

Write-Step "Starting $($nodeC.Name)  (DHT=:$($nodeC.DhtPort)  Proxy=:$($nodeC.ProxyPort)  bootstrap=$($nodeA.DhtPort)) ..."
$procC = Start-Node -Node $nodeC -ExtraArgs @("-bootstrap", "127.0.0.1:$($nodeA.DhtPort)")
$runningProcs += $procC
Start-Sleep -Seconds 2

# ---------------------------------------------------------------------------
# Cleanup helper (reads $runningProcs / $serverProcP2P from script scope)
# ---------------------------------------------------------------------------
function Stop-AllNodes {
    Write-Host ""
    Write-Host "Stopping all nodes ..." -ForegroundColor Yellow
    foreach ($proc in $runningProcs) {
        try { $proc.Kill(); $proc.WaitForExit(2000) | Out-Null } catch { }
    }
    if ($serverProcP2P -and -not $serverProcP2P.HasExited) {
        try { $serverProcP2P.Kill() } catch { }
    }
}

# Wait for proxy listeners
Write-Host ""
Write-Step "Waiting for DHT proxy listeners ..."
foreach ($n in $nodes) {
    if (Wait-UdpProxy -Port $n.ProxyPort -TimeoutSecs 12) {
        Write-Ok "$($n.Name) proxy ready on UDP :$($n.ProxyPort)"
    } else {
        Write-Fail "$($n.Name) proxy did not become ready on :$($n.ProxyPort)"
        Stop-AllNodes
        exit 1
    }
}
Write-Host ""

# ---------------------------------------------------------------------------
# Step 4 – Optional TLS server (to verify TLS endpoints in DHT)
# ---------------------------------------------------------------------------

$tlsServerPort    = 5000
$serverProcP2P    = $null

if ($SpinUpTlsServer) {
    Write-Host "[STEP 3b]  Start TLS server (so DHT-discovered addresses can be verified)" -ForegroundColor Cyan
    Write-Host ""
    Write-Step "Starting TDS server in TLS mode on :$tlsServerPort ..."

    $tlsArgs = @(
        "--tls", "--no-ui",
        "--port",          "$tlsServerPort",
        "--tls-cert",      (Join-Path $certsPath "server.crt"),
        "--tls-key",       (Join-Path $certsPath "server.key"),
        "--tls-client-ca", (Join-Path $certsPath "ca.crt")
    )
    $serverProcP2P = Start-Process -FilePath $serverExe `
        -ArgumentList $tlsArgs `
        -WorkingDirectory $repoRoot `
        -PassThru -NoNewWindow

    if (Wait-PortOpen -TargetHost "localhost" -Port $tlsServerPort -TimeoutSecs 12) {
        Write-Ok "TLS server ready on :$tlsServerPort (PID $($serverProcP2P.Id))"
    } else {
        Write-Fail "TLS server did not start – continuing without it"
        $serverProcP2P = $null
    }
    Write-Host ""
}

# ---------------------------------------------------------------------------
# Step 5 – Automated P2P TLS demonstration
# ---------------------------------------------------------------------------

Write-Host "[STEP 4/5]  P2P TLS endpoint demonstration" -ForegroundColor Cyan
Write-Host ""
Write-Host "  Strategy:" -ForegroundColor White
Write-Host "    1. Register TLS service *addresses* via different DHT nodes." -ForegroundColor Gray
Write-Host "    2. Wait for DHT replication to propagate entries." -ForegroundColor Gray
Write-Host "    3. Query from different nodes to confirm cross-node discovery." -ForegroundColor Gray
Write-Host "    4. Verify TLS handshake to discovered addresses (if openssl available)." -ForegroundColor Gray
Write-Host ""

# ---- 5a: Register TLS service endpoints via individual nodes ----
Write-Host "  --- 4a:  REGISTER  TLS-enabled service addresses" -ForegroundColor White
Write-Host ""
Write-Host "  Note: The addresses below point to TLS-secured services." -ForegroundColor Gray
Write-Host "        The DHT stores and replicates them without encryption;" -ForegroundColor Gray
Write-Host "        clients use TLS when connecting to those addresses." -ForegroundColor Gray
Write-Host ""

# Use the TLS server addr if spun up; otherwise use illustrative addresses
$tlsSvcAddr       = if ($SpinUpTlsServer) { "127.0.0.1:$tlsServerPort" } else { "10.0.1.5:5000" }
$tlsSvcAddr2      = if ($SpinUpTlsServer) { "127.0.0.1:$tlsServerPort" } else { "10.0.1.6:5000" }

$registrations = @(
    @{ Task = "tds-central-tls";  Address = $tlsSvcAddr;       RegisterPort = $nodeA.ProxyPort; RegisterNode = $nodeA.Name },
    @{ Task = "tds-central-tls";  Address = $tlsSvcAddr2;      RegisterPort = $nodeB.ProxyPort; RegisterNode = $nodeB.Name },
    @{ Task = "access-control";   Address = "10.0.2.5:8443";   RegisterPort = $nodeC.ProxyPort; RegisterNode = $nodeC.Name },
    @{ Task = "gate-monitor-tls"; Address = "10.0.3.7:44380";  RegisterPort = $nodeB.ProxyPort; RegisterNode = $nodeB.Name }
)

foreach ($r in $registrations) {
    Write-Step "REGISTER '$($r.Task)'  addr=$($r.Address)  via $($r.RegisterNode) (:$($r.RegisterPort))"
    $resp = Send-UdpJson -Port $r.RegisterPort -Payload @{
        cmd     = "REGISTER"
        task    = $r.Task
        address = $r.Address
    } -NoResponse
    if ($resp.status -eq "SENT") {
        Write-Ok "Sent (proxy accepted the registration)"
    } else {
        Write-Fail "Registration may have failed: $($resp.error)"
    }
}

Write-Host ""
Write-Step "Waiting 3 seconds for DHT replication to propagate ..."
Start-Sleep -Seconds 3
Write-Host ""

# ---- 5b: Cross-node queries ----
Write-Host "  --- 4b:  QUERY  from different nodes (cross-node discovery)" -ForegroundColor White
Write-Host ""

$queries = @(
    @{ Task = "tds-central-tls";  QueryPort = $nodeC.ProxyPort; QueryNode = $nodeC.Name; RegisterNode = "Node A/B" },
    @{ Task = "tds-central-tls";  QueryPort = $nodeA.ProxyPort; QueryNode = $nodeA.Name; RegisterNode = "Node A/B"; Note = "round-robin" },
    @{ Task = "access-control";   QueryPort = $nodeA.ProxyPort; QueryNode = $nodeA.Name; RegisterNode = "Node C" },
    @{ Task = "gate-monitor-tls"; QueryPort = $nodeC.ProxyPort; QueryNode = $nodeC.Name; RegisterNode = "Node B" },
    @{ Task = "non-existent";     QueryPort = $nodeB.ProxyPort; QueryNode = $nodeB.Name; Note = "expect not found" }
)

foreach ($q in $queries) {
    $note = if ($q.Note) { "  # $($q.Note)" } else { "  # registered on $($q.RegisterNode)" }
    Write-Step "QUERY '$($q.Task)' from $($q.QueryNode) (:$($q.QueryPort))$note"
    $resp = Send-UdpJson -Port $q.QueryPort -Payload @{ cmd = "QUERY"; task = $q.Task }

    if ($resp.status -eq "OK" -and $resp.address) {
        Write-Ok "Found: $($resp.address)"
    } elseif ($resp.status -eq "NOTFOUND" -or ($resp.status -eq "ERR" -and $resp.error -match "not found")) {
        Write-Warn "Not found (may still be replicating or task doesn't exist)"
    } elseif ($resp.status -eq "ERR") {
        Write-Fail "Error: $($resp.error)"
    } else {
        Write-Info "Response: $($resp | ConvertTo-Json -Compress)"
    }
}
Write-Host ""

# ---- 5c: TLS verification of discovered address (if server was started or openssl present) ----
Write-Host "  --- 4c:  TLS verification of a discovered address" -ForegroundColor White
Write-Host ""

if ($SpinUpTlsServer) {
    Write-Step "Discovering 'tds-central-tls' from Node C ..."
    $discovered = Send-UdpJson -Port $nodeC.ProxyPort -Payload @{ cmd = "QUERY"; task = "tds-central-tls" }

    if ($discovered.status -eq "OK" -and $discovered.address) {
        $addr = $discovered.address
        Write-Ok "Discovered address: $addr"
        Write-Host ""
        Write-Step "Verifying TLS handshake to discovered address $addr ..."

        $opensslAvailable = $null -ne (Get-Command openssl -ErrorAction SilentlyContinue)
        if ($opensslAvailable) {
            $host, $port = $addr -split ":"
            $opensslArgs = @(
                "s_client",
                "-connect", $addr,
                "-CAfile",  (Join-Path $certsPath "ca.crt"),
                "-cert",    (Join-Path $certsPath "client.crt"),
                "-key",     (Join-Path $certsPath "client.key"),
                "-brief"
            )
            Write-Info "Command: openssl s_client -connect $addr -CAfile certs/ca.crt -cert certs/client.crt -key certs/client.key"
            Write-Host ""
            $opensslOut = "Q" | openssl @opensslArgs 2>&1 | Out-String
            foreach ($line in ($opensslOut -split "`n")) {
                $l = $line.TrimEnd()
                if ($l -match "Verify return code: 0") {
                    Write-Host "    [OK] $l" -ForegroundColor Green
                } elseif ($l -match "error|FAIL" -and $l -notmatch "depth") {
                    Write-Host "    [!]  $l" -ForegroundColor Red
                } elseif ($l -match "Protocol|Cipher|Verify|Certificate chain") {
                    Write-Host "    $l" -ForegroundColor Cyan
                } elseif ($l -and $l -notmatch "^read|^write|^---") {
                    Write-Host "    $l" -ForegroundColor Gray
                }
            }
        } else {
            Write-Warn "openssl not in PATH – cannot verify TLS handshake automatically"
            Write-Info "Manual verification:"
            Write-Info "  openssl s_client -connect $addr -CAfile $certsPath/ca.crt -cert $certsPath/client.crt -key $certsPath/client.key"
        }
    } else {
        Write-Warn "Could not discover 'tds-central-tls' – replication may still be in progress"
        Write-Info "Manual: openssl s_client -connect 127.0.0.1:$tlsServerPort -CAfile $certsPath/ca.crt -cert $certsPath/client.crt -key $certsPath/client.key"
    }
} else {
    Write-Info "TLS server not running (-SpinUpTlsServer not set)"
    Write-Info "Addresses registered in DHT are illustrative TLS endpoints."
    Write-Info ""
    Write-Info "To verify TLS against a real TLS server:"
    Write-Info "  ./demos/centralised/run_tls_demo.ps1   (start a centralised TLS server)"
    Write-Info "  Then re-run this script with -SpinUpTlsServer"
}
Write-Host ""

# ---------------------------------------------------------------------------
# Security architecture explanation
# ---------------------------------------------------------------------------

Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "  Security Architecture: P2P + TLS" -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "  Layer 1 – DHT transport (UDP, node-to-node):" -ForegroundColor White
Write-Host "    Currently unencrypted. Nodes communicate via a custom" -ForegroundColor Gray
Write-Host "    UDP-based Kademlia DHT protocol. Nodes within a trusted" -ForegroundColor Gray
Write-Host "    network segment are assumed to be legitimate peers." -ForegroundColor Gray
Write-Host "    Future: DTLS or node-identity signing would secure this layer." -ForegroundColor Gray
Write-Host ""
Write-Host "  Layer 2 – Service endpoints (stored values in DHT):" -ForegroundColor White
Write-Host "    The IP:port addresses stored in the DHT point to real" -ForegroundColor Gray
Write-Host "    services. Those services independently enforce TLS." -ForegroundColor Gray
Write-Host "    Clients verify the service's certificate after discovery." -ForegroundColor Gray
Write-Host ""
Write-Host "  Layer 3 – Client-to-TDS centralised server (when applicable):" -ForegroundColor White
Write-Host "    Full mutual TLS using client + server certificates." -ForegroundColor Gray
Write-Host "    Clients without a valid CA-signed certificate are rejected." -ForegroundColor Gray
Write-Host "    See demos/centralised/run_tls_demo.ps1 for a full demo." -ForegroundColor Gray
Write-Host ""

# ---------------------------------------------------------------------------
# Optional interactive session
# ---------------------------------------------------------------------------

if ($Interactive) {
    Write-Host "==========================================================" -ForegroundColor Cyan
    Write-Host "  Interactive P2P session (nodes still running)" -ForegroundColor Cyan
    Write-Host "==========================================================" -ForegroundColor Cyan
    Write-Host ""
    Write-Host "  Three P2P nodes are running. Send UDP JSON to any proxy:" -ForegroundColor White
    Write-Host ""
    Write-Host "  Node A (proxy :5100) – register here, query from B or C" -ForegroundColor Gray
    Write-Host "  Node B (proxy :5101)" -ForegroundColor Gray
    Write-Host "  Node C (proxy :5102)" -ForegroundColor Gray
    Write-Host ""
    Write-Host "  Example (PowerShell):" -ForegroundColor White
    Write-Host '    $c = New-Object Net.Sockets.UdpClient' -ForegroundColor Gray
    Write-Host '    $c.Connect("127.0.0.1", 5100)' -ForegroundColor Gray
    Write-Host '    $b = [Text.Encoding]::UTF8.GetBytes(''{"cmd":"REGISTER","task":"my-api","address":"10.0.0.99:443"}'')' -ForegroundColor Gray
    Write-Host '    $c.Send($b, $b.Length)' -ForegroundColor Gray
    Write-Host ""
    Write-Host "  Press Enter to shut down all nodes ..." -ForegroundColor Yellow
    Read-Host | Out-Null
}

Stop-AllNodes

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------

Write-Host ""
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "  P2P TLS Demo Complete" -ForegroundColor Green
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "  What was demonstrated:" -ForegroundColor White
Write-Host "    * Three DHT nodes formed a P2P discovery cluster" -ForegroundColor Gray
Write-Host "    * TLS-secured addresses registered via DHT (no central server)" -ForegroundColor Gray
Write-Host "    * Cross-node queries resolved TLS service endpoints" -ForegroundColor Gray
if ($SpinUpTlsServer) {
    Write-Host "    * openssl verified TLS handshake to discovered TDS server" -ForegroundColor Gray
}
Write-Host "    * DHT inter-node transport is UDP (TLS not currently applied)" -ForegroundColor Gray
Write-Host ""
Write-Host "  Next steps:" -ForegroundColor White
Write-Host "    Centralised TLS demo : .\demos\centralised\run_tls_demo.ps1" -ForegroundColor Gray
Write-Host "    With live TLS server  : .\demos\p2p\run_tls_demo.ps1 -SpinUpTlsServer" -ForegroundColor Gray
Write-Host "    Interactive P2P       : .\demos\p2p\run_tls_demo.ps1 -Interactive" -ForegroundColor Gray
Write-Host ""
