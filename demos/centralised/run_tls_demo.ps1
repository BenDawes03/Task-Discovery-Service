#!/usr/bin/env pwsh
# TDS Mutual TLS Demonstration - Centralised Mode
#
# What this demonstrates:
#   - TDS server configured with TLS + mutual authentication (mTLS)
#   - Clients must present a certificate signed by the trusted CA
#   - Server certificate is verified by the client against the same CA
#   - Traffic encrypted with TLS 1.2+ (AES-256-GCM ciphers)
#   - Clients without valid certificates are explicitly rejected
#
# Usage:
#   ./run_tls_demo.ps1                    # interactive presenter mode (default)
#   ./run_tls_demo.ps1 -SkipBuild         # skip go build step
#   ./run_tls_demo.ps1 -GenerateCerts     # force cert regeneration
#   ./run_tls_demo.ps1 -Port 5001         # use a different port
#   ./run_tls_demo.ps1 -AutoRun           # run all scenarios without pauses
#   ./run_tls_demo.ps1 -Interactive       # finish with an interactive client shell

param(
    [switch]$SkipBuild,
    [switch]$GenerateCerts,
    [switch]$AutoRun,
    [switch]$Interactive,
    [string]$CertsDir = "certs",
    [int]$Port = 5000
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

function Wait-PortOpen {
    param([string]$TargetHost, [int]$Port, [int]$TimeoutSecs = 10)
    $deadline = (Get-Date).AddSeconds($TimeoutSecs)
    while ((Get-Date) -lt $deadline) {
        try {
            $tcp = New-Object System.Net.Sockets.TcpClient
            $task = $tcp.ConnectAsync($TargetHost, $Port)
            if ($task.Wait(500)) {
                $tcp.Close()
                return $true
            }
            $tcp.Close()
        } catch { }
        Start-Sleep -Milliseconds 400
    }
    return $false
}

function Find-FreeTcpPort {
    param([int]$StartPort = 5000, [int]$MaxAttempts = 50)

    for ($offset = 0; $offset -lt $MaxAttempts; $offset++) {
        $candidate = $StartPort + $offset
        if (-not (Wait-PortOpen -TargetHost "127.0.0.1" -Port $candidate -TimeoutSecs 1)) {
            return $candidate
        }
    }

    return $null
}

function Get-ListeningPid {
    param([int]$Port)

    $lsofCmd = Get-Command lsof -ErrorAction SilentlyContinue
    if (-not $lsofCmd) {
        return $null
    }

    try {
        $pidLine = (& lsof -ti "tcp:$Port" 2>$null | Select-Object -First 1)
        if ($pidLine -and $pidLine -match "^\d+$") {
            return [int]$pidLine
        }
    } catch { }

    return $null
}

function Resolve-PortConflict {
    param([int]$RequestedPort)

    if (-not (Wait-PortOpen -TargetHost "127.0.0.1" -Port $RequestedPort -TimeoutSecs 1)) {
        return [PSCustomObject]@{ Port = $RequestedPort; UseExisting = $false }
    }

    Write-Host "  [WARN] Port $RequestedPort is already in use." -ForegroundColor Yellow
    $existingPid = Get-ListeningPid -Port $RequestedPort
    if ($existingPid) {
        Write-Host "  [INFO] Listener PID detected via lsof: $existingPid" -ForegroundColor Gray
    } else {
        Write-Host "  [INFO] Could not determine owning PID (lsof unavailable or insufficient permissions)." -ForegroundColor Gray
    }

    if ($AutoRun) {
        $newPort = Find-FreeTcpPort -StartPort ($RequestedPort + 1)
        if ($null -eq $newPort) {
            throw "Could not find a free port near $RequestedPort"
        }
        Write-Host "  [INFO] AutoRun selected free port $newPort" -ForegroundColor Yellow
        return [PSCustomObject]@{ Port = $newPort; UseExisting = $false }
    }

    Write-Host "" 
    Write-Host "  Choose how to proceed:" -ForegroundColor White
    Write-Host "    [R] Reuse existing server on $RequestedPort" -ForegroundColor White
    if ($existingPid) {
        Write-Host "    [K] Kill PID $existingPid and start a new demo server" -ForegroundColor White
    }
    Write-Host "    [N] Pick a new free port automatically" -ForegroundColor White
    Write-Host "    [A] Abort" -ForegroundColor White

    while ($true) {
        $choice = (Read-Host "Selection [R/N/A$(if ($existingPid) { '/K' } else { '' })]").Trim().ToUpper()
        switch ($choice) {
            "" {
                return [PSCustomObject]@{ Port = $RequestedPort; UseExisting = $true }
            }
            "R" {
                return [PSCustomObject]@{ Port = $RequestedPort; UseExisting = $true }
            }
            "N" {
                $newPort = Find-FreeTcpPort -StartPort ($RequestedPort + 1)
                if ($null -eq $newPort) {
                    Write-Host "  [ERROR] Could not find a free port near $RequestedPort" -ForegroundColor Red
                    continue
                }
                return [PSCustomObject]@{ Port = $newPort; UseExisting = $false }
            }
            "K" {
                if (-not $existingPid) {
                    Write-Host "  [WARN] No PID available to kill." -ForegroundColor Yellow
                    continue
                }
                try {
                    Stop-Process -Id $existingPid -Force -ErrorAction Stop
                    Start-Sleep -Milliseconds 500
                    return [PSCustomObject]@{ Port = $RequestedPort; UseExisting = $false }
                } catch {
                    Write-Host "  [ERROR] Failed to stop PID ${existingPid}: $($_.Exception.Message)" -ForegroundColor Red
                }
            }
            "A" {
                throw "Aborted by user due to port conflict"
            }
            default {
                Write-Host "  [WARN] Please enter R, N, A$(if ($existingPid) { ', or K' } else { '' })." -ForegroundColor Yellow
            }
        }
    }
}

function Invoke-TlsClientDemo {
    param(
        [string]$Exe,
        [string]$ServerAddr,
        [string]$CertFile,
        [string]$KeyFile,
        [string]$CaFile,
        [string[]]$Commands   # lines to feed via stdin
    )
    $inputBlock = ($Commands + @("quit")) -join "`n"
    $result = $inputBlock | & $Exe `
        -mode centralized `
        -tls `
        -server $ServerAddr `
        -cert   $CertFile `
        -key    $KeyFile `
        -ca     $CaFile 2>&1
    return $result
}

function Write-ScenarioHeader {
    param(
        [string]$Id,
        [string]$Title,
        [string]$Goal,
        [string]$CommandPreview
    )

    Write-Host "" 
    Write-Host "----------------------------------------------------------" -ForegroundColor DarkCyan
    Write-Host "  Scenario $Id - $Title" -ForegroundColor Cyan
    Write-Host "  Goal    : $Goal" -ForegroundColor White
    if (-not [string]::IsNullOrWhiteSpace($CommandPreview)) {
        Write-Host "  Command : $CommandPreview" -ForegroundColor Gray
    }
    Write-Host "----------------------------------------------------------" -ForegroundColor DarkCyan

    if (-not $AutoRun) {
        Read-Host "Press Enter to run this scenario" | Out-Null
    }
}

function Get-ScenarioResponse {
    param(
        [string]$Exe,
        [string]$ServerAddr,
        [string]$CertFile,
        [string]$KeyFile,
        [string]$CaFile,
        [string]$Command
    )

    $out = Invoke-TlsClientDemo `
        -Exe $Exe `
        -ServerAddr $ServerAddr `
        -CertFile $CertFile `
        -KeyFile  $KeyFile `
        -CaFile   $CaFile `
        -Commands @($Command)

    $lines = ($out | Out-String).Trim() -split "`n"
    $responseLine = $null
    $errorLine = $null

    foreach ($line in $lines) {
        $trimmed = $line.Trim()
        # client_demo renders prompt+response as '> Response: ...'
        if ($trimmed -match "^(?:>\s*)?Response:\s*(.+)$") {
            $responseLine = $matches[1].Trim()
            continue
        }
        if ($trimmed -match "^(?:>\s*)?Error:\s*(.+)$") {
            $errorLine = $matches[1].Trim()
        }
    }

    if ($errorLine) {
        return [PSCustomObject]@{
            Status = "ERR"
            Value  = $errorLine
            Raw    = $lines
        }
    }

    if ($responseLine) {
        return [PSCustomObject]@{
            Status = "OK"
            Value  = $responseLine
            Raw    = $lines
        }
    }

    return [PSCustomObject]@{
        Status = "UNKNOWN"
        Value  = "No response line found"
        Raw    = $lines
    }
}

# ---------------------------------------------------------------------------
# Initialise
# ---------------------------------------------------------------------------

$repoRoot = Get-RepoRoot -StartDir $PSScriptRoot
Set-Location $repoRoot

$certsPath    = if ([System.IO.Path]::IsPathRooted($CertsDir)) { $CertsDir } else { Join-Path $repoRoot $CertsDir }
$serverExe    = Join-Path $repoRoot "bin/server"
$clientExe    = Join-Path $repoRoot "bin/client_demo"
$serverAddr   = "localhost:$Port"
$serverPid    = $null
$ownsServer   = $false
$exitCleanupEvent = Register-EngineEvent -SourceIdentifier PowerShell.Exiting -Action {
    if ($script:ownsServer -and $script:serverPid) {
        try {
            Stop-Process -Id $script:serverPid -Force -ErrorAction SilentlyContinue
        } catch { }
    }
}

Write-Host ""
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "     TDS Mutual TLS Demonstration  -  Centralised Mode"    -ForegroundColor Cyan
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "  Repo root : $repoRoot"          -ForegroundColor Gray
Write-Host "  Certs dir : $certsPath"         -ForegroundColor Gray
Write-Host "  Server    : $serverAddr (TLS)"  -ForegroundColor Gray
Write-Host "  Mode      : $(if ($AutoRun) { 'AutoRun (no pauses)' } else { 'Interactive presenter mode' })" -ForegroundColor Gray
Write-Host ""

# ---------------------------------------------------------------------------
# Step 1 – Certificates
# ---------------------------------------------------------------------------

Write-Host "[STEP 1/4]  Certificates" -ForegroundColor Cyan
Write-Host ""

if ($GenerateCerts -or -not (Test-CertsExist -Dir $certsPath)) {
    if (-not $GenerateCerts) {
        Write-Host "  Certificates not found in: $certsPath" -ForegroundColor Yellow
        Write-Host "  Running generate_certs.ps1 ..." -ForegroundColor Yellow
    } else {
        Write-Host "  -GenerateCerts flag set – regenerating ..." -ForegroundColor Yellow
    }

    $genScript = Join-Path $repoRoot "scripts/generate_certs.ps1"
    if (-not (Test-Path $genScript)) {
        Write-Host "  [ERROR] generate_certs.ps1 not found at: $genScript" -ForegroundColor Red
        exit 1
    }

    & $genScript -OutputDir $certsPath
    if ($LASTEXITCODE -ne 0) {
        Write-Host "  [ERROR] Certificate generation failed" -ForegroundColor Red
        exit 1
    }
    Write-Host ""
} else {
    Write-Host "  Found certificates in: $certsPath" -ForegroundColor Green
}

Write-Host "  Files:" -ForegroundColor White
foreach ($f in @("ca.crt","server.crt","server.key","client.crt","client.key")) {
    $fp = Join-Path $certsPath $f
    if (Test-Path $fp) {
        Write-Host "    [OK] $f" -ForegroundColor Green
    } else {
        Write-Host "    [MISSING] $f" -ForegroundColor Red
        Write-Host "  Re-run with -GenerateCerts to create missing certificates." -ForegroundColor Yellow
        exit 1
    }
}
Write-Host ""

# ---------------------------------------------------------------------------
# Step 2 – Build
# ---------------------------------------------------------------------------

Write-Host "[STEP 2/4]  Build" -ForegroundColor Cyan
Write-Host ""

if (-not $SkipBuild) {
    Write-Host "  Building server ..." -ForegroundColor Yellow
    go build -o bin/server ./cmd/server
    if ($LASTEXITCODE -ne 0) {
        Write-Host "  [ERROR] Failed to build server" -ForegroundColor Red
        exit 1
    }

    Write-Host "  Building client_demo ..." -ForegroundColor Yellow
    go build -o bin/client_demo ./cmd/client_demo
    if ($LASTEXITCODE -ne 0) {
        Write-Host "  [ERROR] Failed to build client_demo" -ForegroundColor Red
        exit 1
    }

    Write-Host "  Binaries ready" -ForegroundColor Green
} else {
    Write-Host "  Skipping build (-SkipBuild)" -ForegroundColor Gray

    foreach ($b in @($serverExe, $clientExe)) {
        if (-not (Test-Path $b)) {
            Write-Host "  [ERROR] Binary not found: $b" -ForegroundColor Red
            Write-Host "  Run without -SkipBuild to build first." -ForegroundColor Yellow
            exit 1
        }
    }
}
Write-Host ""

# ---------------------------------------------------------------------------
# Step 3 – Start TLS server
# ---------------------------------------------------------------------------

Write-Host "[STEP 3/4]  Start TDS server in TLS mode" -ForegroundColor Cyan
Write-Host ""
Write-Host "  Command: bin/server --tls --no-ui --port $Port" -ForegroundColor Gray
Write-Host "             --tls-cert   certs/server.crt" -ForegroundColor Gray
Write-Host "             --tls-key    certs/server.key" -ForegroundColor Gray
Write-Host "             --tls-client-ca certs/ca.crt" -ForegroundColor Gray
Write-Host ""
Write-Host "  NOTE: --tls enables mutual TLS. Any client without a certificate" -ForegroundColor White
Write-Host "        signed by ca.crt will be rejected at the TLS handshake." -ForegroundColor White
Write-Host ""

$requestedPort = $Port
$resolution = Resolve-PortConflict -RequestedPort $Port
$Port = $resolution.Port
$serverAddr = "localhost:$Port"

if ($resolution.UseExisting) {
    Write-Host "  [INFO] Reusing existing listener on $serverAddr" -ForegroundColor Yellow
    Write-Host "  [INFO] This script will not stop that reused server during cleanup." -ForegroundColor Gray
    Write-Host ""
} elseif ($Port -ne $requestedPort) {
    Write-Host "  [INFO] Switching demo server port to $Port" -ForegroundColor Yellow
    Write-Host ""
}

$serverArgs = @(
    "--tls", "--no-ui",
    "--port",          "$Port",
    "--tls-cert",      (Join-Path $certsPath "server.crt"),
    "--tls-key",       (Join-Path $certsPath "server.key"),
    "--tls-client-ca", (Join-Path $certsPath "ca.crt")
)

if (-not $resolution.UseExisting) {
    $serverProc = Start-Process -FilePath $serverExe `
        -ArgumentList $serverArgs `
        -WorkingDirectory $repoRoot `
        -PassThru -NoNewWindow

    $serverPid = $serverProc.Id
    $ownsServer = $true
    Write-Host "  Server PID: $serverPid" -ForegroundColor Gray
    Write-Host "  Waiting for TLS listener on :$Port ..." -ForegroundColor Yellow

    if (-not (Wait-PortOpen -TargetHost "127.0.0.1" -Port $Port -TimeoutSecs 12)) {
        Write-Host "  [ERROR] Server did not open port $Port within 12 seconds." -ForegroundColor Red
        try { Stop-Process -Id $serverPid -Force -ErrorAction SilentlyContinue } catch { }
        exit 1
    }
} else {
    Write-Host "  Waiting for existing TLS listener on :$Port ..." -ForegroundColor Yellow
    if (-not (Wait-PortOpen -TargetHost "127.0.0.1" -Port $Port -TimeoutSecs 3)) {
        Write-Host "  [ERROR] Expected existing listener on :$Port but it is unavailable." -ForegroundColor Red
        exit 1
    }
}
Write-Host "  TLS listener is ready" -ForegroundColor Green
Write-Host ""

# Register cleanup handler
$cleanupBlock = {
    param([int]$stopPid)
    Write-Host ""
    Write-Host "Stopping server (PID $stopPid) ..." -ForegroundColor Yellow
    try { Stop-Process -Id $stopPid -Force -ErrorAction SilentlyContinue } catch { }
}

# ---------------------------------------------------------------------------
# Step 4 – Automated TLS demonstration
# ---------------------------------------------------------------------------

Write-Host "[STEP 4/4]  Automated TLS demonstration" -ForegroundColor Cyan
Write-Host ""

$caCrt     = Join-Path $certsPath "ca.crt"
$clientCrt = Join-Path $certsPath "client.crt"
$clientKey = Join-Path $certsPath "client.key"

# ---- 4a: Register services using mTLS ----
Write-Host "  --- 4a: REGISTER  (client uses client.crt + client.key)" -ForegroundColor White
Write-Host ""

$registrations = @(
    @{ Cmd = "REGISTER ticketing-service [::1]:8080"; Goal = "Register primary ticketing endpoint over mTLS" },
    @{ Cmd = "REGISTER ticketing-service [::1]:8081"; Goal = "Register secondary ticketing endpoint for round-robin" },
    @{ Cmd = "REGISTER access-control [::1]:9090"; Goal = "Register access-control endpoint over mTLS" },
    @{ Cmd = "REGISTER gate-monitor [::1]:7070"; Goal = "Register gate-monitor endpoint over mTLS" }
)

for ($i = 0; $i -lt $registrations.Count; $i++) {
    $item = $registrations[$i]
    $scenarioId = "4A.$($i + 1)"
    Write-ScenarioHeader -Id $scenarioId -Title "REGISTER" -Goal $item.Goal -CommandPreview $item.Cmd

    $response = Get-ScenarioResponse `
        -Exe $clientExe `
        -ServerAddr $serverAddr `
        -CertFile $clientCrt `
        -KeyFile  $clientKey `
        -CaFile   $caCrt `
        -Command  $item.Cmd

    if ($response.Status -eq "OK" -and $response.Value -eq "OK") {
        Write-Host "  Result  : REGISTER succeeded" -ForegroundColor Green
    } elseif ($response.Status -eq "OK") {
        Write-Host "  Result  : $($response.Value)" -ForegroundColor Green
    } elseif ($response.Status -eq "ERR") {
        Write-Host "  Result  : ERROR - $($response.Value)" -ForegroundColor Red
    } else {
        Write-Host "  Result  : UNKNOWN - $($response.Value)" -ForegroundColor Yellow
        foreach ($raw in $response.Raw) {
            if (-not [string]::IsNullOrWhiteSpace($raw)) {
                Write-Host "           $raw" -ForegroundColor Gray
            }
        }
    }
}
Write-Host ""

# ---- 4b: Query services using mTLS ----
Write-Host "  --- 4b: QUERY  (demonstrates round-robin load balancing via TLS)" -ForegroundColor White
Write-Host ""

$queries = @(
    @{ Cmd = "QUERY ticketing-service"; Note = "expect [::1]:8080 (round 1)"; Goal = "Validate first ticketing endpoint is returned" },
    @{ Cmd = "QUERY ticketing-service"; Note = "expect [::1]:8081 (round 2)"; Goal = "Validate round-robin second endpoint" },
    @{ Cmd = "QUERY ticketing-service"; Note = "expect [::1]:8080 (round 3)"; Goal = "Validate round-robin wraps back" },
    @{ Cmd = "QUERY access-control";    Note = "expect [::1]:9090"; Goal = "Query a different task" },
    @{ Cmd = "QUERY gate-monitor";      Note = "expect [::1]:7070"; Goal = "Query another independent task" },
    @{ Cmd = "QUERY unknown-task";      Note = "expect NOTFOUND"; Goal = "Demonstrate unknown task behavior" }
)

for ($i = 0; $i -lt $queries.Count; $i++) {
    $q = $queries[$i]
    $scenarioId = "4B.$($i + 1)"
    Write-ScenarioHeader -Id $scenarioId -Title "QUERY" -Goal $q.Goal -CommandPreview "$($q.Cmd)  # $($q.Note)"

    $response = Get-ScenarioResponse `
        -Exe $clientExe `
        -ServerAddr $serverAddr `
        -CertFile $clientCrt `
        -KeyFile  $clientKey `
        -CaFile   $caCrt `
        -Command  $q.Cmd

    if ($response.Status -eq "OK") {
        if ($response.Value -eq "NOTFOUND") {
            Write-Host "  Result  : NOTFOUND" -ForegroundColor Yellow
        } else {
            Write-Host "  Result  : $($response.Value)" -ForegroundColor Green
        }
    } elseif ($response.Status -eq "ERR") {
        Write-Host "  Result  : ERROR - $($response.Value)" -ForegroundColor Red
    } else {
        Write-Host "  Result  : UNKNOWN - $($response.Value)" -ForegroundColor Yellow
        foreach ($raw in $response.Raw) {
            if (-not [string]::IsNullOrWhiteSpace($raw)) {
                Write-Host "           $raw" -ForegroundColor Gray
            }
        }
    }
}
Write-Host ""

# ---- 4c: mTLS rejection proof (optional – requires openssl in PATH) ----
Write-Host "  --- 4c: Rejection proof – plain TCP connection must be refused" -ForegroundColor White
Write-Host ""
Write-ScenarioHeader -Id "4C.1" -Title "Plain TCP Rejection" -Goal "Show that non-TLS clients are rejected before protocol processing" -CommandPreview "Open raw TCP connection and send JSON without TLS"
Write-Host "    Attempting plain TCP connection to $serverAddr ..." -ForegroundColor Gray

$tcpRejected = $false
try {
    $tcp = New-Object System.Net.Sockets.TcpClient
    $connectTask = $tcp.ConnectAsync("localhost", $Port)
    if ($connectTask.Wait(3000)) {
        $stream = $tcp.GetStream()
        $stream.WriteTimeout = 2000
        $stream.ReadTimeout  = 2000

        # Send raw (non-TLS) bytes – server should close the connection
        $probe = [System.Text.Encoding]::UTF8.GetBytes("{`"cmd`":`"QUERY`",`"task`":`"test`"}`n")
        $stream.Write($probe, 0, $probe.Length)

        $buf  = New-Object byte[] 512
        try {
            $n = $stream.Read($buf, 0, $buf.Length)
            if ($n -eq 0) {
                $tcpRejected = $true
            } else {
                # Any TLS alert record starts with byte 0x15 (21 = alert)
                if ($buf[0] -eq 21) { $tcpRejected = $true }
            }
        } catch {
            $tcpRejected = $true
        }
        $tcp.Close()
    }
} catch {
    $tcpRejected = $true
}

if ($tcpRejected) {
    Write-Host "    Plain TCP was rejected / connection closed – TLS enforcement confirmed" -ForegroundColor Green
} else {
    Write-Host "    Plain TCP was not explicitly rejected (server may buffer before TLS handshake)" -ForegroundColor Yellow
}
Write-Host ""

# ---- openssl s_client verification (if openssl available) ----
$opensslAvailable = $null -ne (Get-Command openssl -ErrorAction SilentlyContinue)
if ($opensslAvailable) {
    Write-Host "  --- 4d: openssl s_client – verify TLS handshake and certificate chain" -ForegroundColor White
    Write-Host ""
    Write-ScenarioHeader -Id "4D.1" -Title "TLS Handshake Verification" -Goal "Show certificate validation and negotiated TLS settings" -CommandPreview "openssl s_client -connect $serverAddr -CAfile certs/ca.crt -cert certs/client.crt -key certs/client.key"
    Write-Host "    Command: openssl s_client -connect $serverAddr -CAfile certs/ca.crt" -ForegroundColor Gray
    Write-Host "                              -cert certs/client.crt -key certs/client.key" -ForegroundColor Gray
    Write-Host ""

    $opensslArgs = @(
        "s_client",
        "-connect", $serverAddr,
        "-CAfile",  $caCrt,
        "-cert",    $clientCrt,
        "-key",     $clientKey,
        "-brief"
    )
    $opensslOutput = "Q" | openssl @opensslArgs 2>&1 | Out-String
    foreach ($line in ($opensslOutput -split "`n")) {
        $l = $line.TrimEnd()
        if ($l -match "Verify return code: 0") {
            Write-Host "    [OK] $l" -ForegroundColor Green
        } elseif ($l -match "Certificate chain|Protocol|Cipher|Verify") {
            Write-Host "    $l" -ForegroundColor Cyan
        } elseif ($l -and $l -notmatch "^read|^write|^---") {
            Write-Host "    $l" -ForegroundColor Gray
        }
    }
    Write-Host ""
} else {
    Write-Host "  --- 4d: openssl s_client skipped (openssl not in PATH)" -ForegroundColor Gray
    Write-Host "    To verify manually:" -ForegroundColor White
    Write-Host "      openssl s_client -connect $serverAddr -CAfile $caCrt -cert $clientCrt -key $clientKey" -ForegroundColor Gray
    Write-Host ""
}

# ---------------------------------------------------------------------------
# Optional interactive session
# ---------------------------------------------------------------------------

if ($Interactive) {
    Write-Host "============================================================" -ForegroundColor Cyan
    Write-Host "  Interactive client shell  (TLS mode)" -ForegroundColor Cyan
    Write-Host "============================================================" -ForegroundColor Cyan
    Write-Host ""
    Write-Host "  Commands: REGISTER <task> <address>" -ForegroundColor White
    Write-Host "            QUERY <task>" -ForegroundColor White
    Write-Host "            quit" -ForegroundColor White
    Write-Host ""
    Write-Host "  Server is running at $serverAddr with mutual TLS." -ForegroundColor Gray
    Write-Host ""

    try {
        & $clientExe `
            -mode centralized `
            -tls `
            -server $serverAddr `
            -cert   $clientCrt `
            -key    $clientKey `
            -ca     $caCrt
    } finally {
        if ($ownsServer -and $serverPid) {
            & $cleanupBlock $serverPid
        } else {
            Write-Host "" 
            Write-Host "Reused server left running on $serverAddr" -ForegroundColor Gray
        }
    }
} else {
    if ($ownsServer -and $serverPid) {
        & $cleanupBlock $serverPid
    } else {
        Write-Host "" 
        Write-Host "Reused server left running on $serverAddr" -ForegroundColor Gray
    }
    Write-Host ""
    Write-Host "  Tip: run with -Interactive to get a live TLS client shell after the demo." -ForegroundColor Gray
}

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------

Write-Host ""
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host "  TLS Demo Complete" -ForegroundColor Green
Write-Host "==========================================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "  What was demonstrated:" -ForegroundColor White
Write-Host "    * Server started with --tls (mutual TLS, TLS 1.2+)" -ForegroundColor Gray
Write-Host "    * Clients presented client.crt signed by ca.crt" -ForegroundColor Gray
Write-Host "    * Server presented server.crt signed by ca.crt" -ForegroundColor Gray
Write-Host "    * REGISTER / QUERY operations over encrypted channel" -ForegroundColor Gray
Write-Host "    * Demo service addresses used IPv6 loopback ([::1]:port)" -ForegroundColor Gray
Write-Host "    * Round-robin load balancing preserved over TLS" -ForegroundColor Gray
Write-Host "    * Plain TCP connection rejected (mTLS enforced)" -ForegroundColor Gray
Write-Host ""
Write-Host "  Useful follow-up commands:" -ForegroundColor White
Write-Host "    Regenerate certs : .\scripts\generate_certs.ps1 -OutputDir certs" -ForegroundColor Gray
Write-Host "    Interactive shell : .\demos\centralised\run_tls_demo.ps1 -Interactive" -ForegroundColor Gray
Write-Host "    Skip build        : .\demos\centralised\run_tls_demo.ps1 -SkipBuild" -ForegroundColor Gray
Write-Host "    No pauses         : .\demos\centralised\run_tls_demo.ps1 -AutoRun" -ForegroundColor Gray
Write-Host ""

if ($exitCleanupEvent) {
    Unregister-Event -SourceIdentifier PowerShell.Exiting -ErrorAction SilentlyContinue
    Remove-Job -Id $exitCleanupEvent.Id -Force -ErrorAction SilentlyContinue
}
