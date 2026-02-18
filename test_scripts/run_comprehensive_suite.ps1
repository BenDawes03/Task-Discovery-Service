param(
    [int]$Port = 5500,
    [string[]]$Modes = @("udp", "tcp", "tls"),
    [string]$CertDir = "certs",
    [switch]$SkipTLS,
    [switch]$KeepServerLogs,
    [switch]$Verbose
)

$ErrorActionPreference = "Stop"

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$repoRoot = Split-Path -Parent $scriptDir
Set-Location $repoRoot

$resultsDir = Join-Path $scriptDir "results"
if (-not (Test-Path $resultsDir)) {
    New-Item -Path $resultsDir -ItemType Directory | Out-Null
}
$runStamp = Get-Date -Format "yyyyMMdd_HHmmss"
$runDir = Join-Path $resultsDir "suite_$runStamp"
New-Item -Path $runDir -ItemType Directory | Out-Null

$probeExe = Join-Path $repoRoot "bin\tds_probe.exe"
$harnessExe = Join-Path $repoRoot "bin\tds_server_harness.exe"
$probeDir = Split-Path -Parent $probeExe
if (-not (Test-Path $probeDir)) {
    New-Item -Path $probeDir -ItemType Directory | Out-Null
}

$script:Passed = 0
$script:Failed = 0
$script:Skipped = 0
$script:CaseRows = New-Object System.Collections.Generic.List[pscustomobject]

function Write-Section {
    param([string]$Text)
    Write-Host ""
    Write-Host "=== $Text ===" -ForegroundColor Cyan
}

function Add-CaseResult {
    param(
        [string]$Mode,
        [string]$Name,
        [string]$Status,
        [string]$Detail
    )

    $script:CaseRows.Add([pscustomobject]@{
        Timestamp = (Get-Date).ToString("s")
        Mode = $Mode
        Test = $Name
        Status = $Status
        Detail = $Detail
    })

    switch ($Status) {
        "PASS" {
            $script:Passed++
            Write-Host "  [PASS] $Name - $Detail" -ForegroundColor Green
        }
        "FAIL" {
            $script:Failed++
            Write-Host "  [FAIL] $Name - $Detail" -ForegroundColor Red
        }
        default {
            $script:Skipped++
            Write-Host "  [SKIP] $Name - $Detail" -ForegroundColor Yellow
        }
    }
}

function Assert-Status {
    param(
        [string]$Mode,
        [string]$Name,
        [pscustomobject]$Response,
        [string]$ExpectedStatus,
        [string]$ExpectedAddress = ""
    )

    if (-not $Response) {
        Add-CaseResult -Mode $Mode -Name $Name -Status "FAIL" -Detail "No response"
        return
    }

    if ($Response.status -ne $ExpectedStatus) {
        Add-CaseResult -Mode $Mode -Name $Name -Status "FAIL" -Detail "Expected status '$ExpectedStatus', got '$($Response.status)'"
        return
    }

    if ($ExpectedAddress -ne "" -and $Response.address -ne $ExpectedAddress) {
        Add-CaseResult -Mode $Mode -Name $Name -Status "FAIL" -Detail "Expected address '$ExpectedAddress', got '$($Response.address)'"
        return
    }

    Add-CaseResult -Mode $Mode -Name $Name -Status "PASS" -Detail "status=$ExpectedStatus"
}

function Invoke-Probe {
    param(
        [string]$Mode,
        [string]$Server,
        [string]$Command,
        [string]$Task,
        [string]$Address,
        [string]$Raw,
        [int]$TimeoutMs = 3000,
        [switch]$AllowFailure,
        [string]$CertDirLocal = "certs"
    )

    $args = @(
        "-mode", $Mode,
        "-server", $Server,
        "-timeout", "${TimeoutMs}ms"
    )

    if ($Raw) {
        $args += @("-raw", $Raw)
    }
    else {
        if ($Command) {
            $args += @("-cmd", $Command)
        }
        if ($Task) {
            $args += @("-task", $Task)
        }
        if ($Address) {
            $args += @("-address", $Address)
        }
    }

    if ($Mode -eq "tls") {
        $args += @(
            "-cert", (Join-Path $CertDirLocal "client.crt"),
            "-key", (Join-Path $CertDirLocal "client.key"),
            "-ca", (Join-Path $CertDirLocal "ca.crt")
        )
    }

    $output = & $probeExe @args 2>&1
    $exitCode = $LASTEXITCODE

    if ($exitCode -ne 0) {
        if ($AllowFailure) {
            return [pscustomobject]@{ status = "PROBE_ERROR"; error = ($output -join "`n") }
        }
        throw "Probe failed: $($output -join "`n")"
    }

    $joined = ($output -join "`n").Trim()
    if ($Verbose) {
        Write-Host "    probe => $joined" -ForegroundColor DarkGray
    }

    try {
        return $joined | ConvertFrom-Json
    }
    catch {
        throw "Probe output was not valid JSON: $joined"
    }
}

function Wait-ServerReady {
    param(
        [string]$Mode,
        [string]$Server,
        [string]$CertDirLocal = "certs",
        [int]$TimeoutSec = 15
    )

    $deadline = (Get-Date).AddSeconds($TimeoutSec)
    while ((Get-Date) -lt $deadline) {
        try {
            $resp = Invoke-Probe -Mode $Mode -Server $Server -Command "QUERY" -Task "__suite_healthcheck__" -TimeoutMs 1200 -AllowFailure -CertDirLocal $CertDirLocal
            if ($resp.status -in @("NOTFOUND", "OK", "ERR")) {
                return $true
            }
        }
        catch {
            Start-Sleep -Milliseconds 300
        }
        Start-Sleep -Milliseconds 250
    }

    return $false
}

function Start-TestServer {
    param(
        [string]$Mode,
        [int]$ListenPort,
        [string]$ServerLogPath,
        [string]$CertDirLocal = "certs"
    )

    $args = @("--mode", $Mode, "--port", "$ListenPort", "--heartbeat-timeout", "5s", "--cleanup-interval", "1s")
    if ($Mode -eq "tls") {
        $args += @("--tls-cert", (Join-Path $CertDirLocal "server.crt"), "--tls-key", (Join-Path $CertDirLocal "server.key"), "--tls-client-ca", (Join-Path $CertDirLocal "ca.crt"))
    }

    $stderrPath = "$ServerLogPath.err"
    $process = Start-Process -FilePath $harnessExe -ArgumentList $args -RedirectStandardOutput $ServerLogPath -RedirectStandardError $stderrPath -NoNewWindow -PassThru

    $serverAddr = "127.0.0.1:$ListenPort"
    if (-not (Wait-ServerReady -Mode $Mode -Server $serverAddr -CertDirLocal $CertDirLocal -TimeoutSec 45)) {
        try { Stop-Process -Id $process.Id -Force -ErrorAction SilentlyContinue } catch {}
        throw "Server failed to become ready ($Mode)"
    }

    return $process
}

function Stop-TestServer {
    param([System.Diagnostics.Process]$Process)

    if ($null -eq $Process) {
        return
    }

    try {
        if (-not $Process.HasExited) {
            Stop-Process -Id $Process.Id -Force -ErrorAction SilentlyContinue
            Start-Sleep -Milliseconds 250
        }
    } catch {}
}

Write-Section "Build Probe Utility"
& go build -o $probeExe ./test_scripts/tds_protocol_probe
if ($LASTEXITCODE -ne 0) {
    throw "Failed to build test probe"
}
Write-Host "Probe: $probeExe" -ForegroundColor Green

Write-Section "Build Server Harness"
& go build -o $harnessExe ./test_scripts/tds_server_harness
if ($LASTEXITCODE -ne 0) {
    throw "Failed to build test server harness"
}
Write-Host "Harness: $harnessExe" -ForegroundColor Green

$modesToRun = @()
foreach ($mode in $Modes) {
    $expandedModes = $mode.Split(',') | ForEach-Object { $_.Trim() } | Where-Object { $_ -ne "" }
    foreach ($expanded in $expandedModes) {
        $m = $expanded.ToLowerInvariant()
    if ($m -eq "tls" -and $SkipTLS) {
        Add-CaseResult -Mode "tls" -Name "TLS suite" -Status "SKIP" -Detail "Skipped by -SkipTLS"
        continue
    }
    if ($m -eq "tls") {
        $required = @("ca.crt", "server.crt", "server.key", "client.crt", "client.key")
        $missing = $required | Where-Object { -not (Test-Path (Join-Path $CertDir $_)) }
        if ($missing.Count -gt 0) {
            Add-CaseResult -Mode "tls" -Name "TLS suite" -Status "SKIP" -Detail "Missing cert files: $($missing -join ', ')"
            continue
        }
    }
    if ($m -in @("udp", "tcp", "tls")) {
        if ($modesToRun -notcontains $m) {
            $modesToRun += $m
        }
    }
    }
}

$portCursor = $Port
foreach ($mode in $modesToRun) {
    Write-Section "Mode: $mode"
    $serverProc = $null
    $serverLog = Join-Path $runDir "server_$mode.log"
    $serverAddr = "127.0.0.1:$portCursor"

    try {
        $serverProc = Start-TestServer -Mode $mode -ListenPort $portCursor -ServerLogPath $serverLog -CertDirLocal $CertDir
        Write-Host "Server ready at $serverAddr (pid=$($serverProc.Id))" -ForegroundColor Green

        $casePrefix = $mode.ToUpperInvariant()

        # 1) malformed JSON
        $resp = Invoke-Probe -Mode $mode -Server $serverAddr -Raw "this is not json"
        if ($resp.status -eq "ERR" -and $resp.error -like "invalid JSON*") {
            Add-CaseResult -Mode $mode -Name "$casePrefix malformed JSON" -Status "PASS" -Detail "Rejected invalid payload"
        } else {
            Add-CaseResult -Mode $mode -Name "$casePrefix malformed JSON" -Status "FAIL" -Detail "Unexpected response: status=$($resp.status), error=$($resp.error)"
        }

        # 2) missing register address
        $resp = Invoke-Probe -Mode $mode -Server $serverAddr -Raw '{"cmd":"REGISTER","task":"svc-a"}'
        Assert-Status -Mode $mode -Name "$casePrefix REGISTER missing address" -Response $resp -ExpectedStatus "ERR"

        # 3) missing query task
        $resp = Invoke-Probe -Mode $mode -Server $serverAddr -Raw '{"cmd":"QUERY"}'
        Assert-Status -Mode $mode -Name "$casePrefix QUERY missing task" -Response $resp -ExpectedStatus "ERR"

        # 4) unknown command
        $resp = Invoke-Probe -Mode $mode -Server $serverAddr -Raw '{"cmd":"NOPE","task":"svc-a"}'
        Assert-Status -Mode $mode -Name "$casePrefix unknown command" -Response $resp -ExpectedStatus "ERR"

        # 5) query not found
        $resp = Invoke-Probe -Mode $mode -Server $serverAddr -Command "QUERY" -Task "never-registered"
        Assert-Status -Mode $mode -Name "$casePrefix QUERY not found" -Response $resp -ExpectedStatus "NOTFOUND"

        # 6) register and query
        $taskName = "suite-$mode-basic"
        $serviceAddress = "10.10.10.1:8001"
        $resp = Invoke-Probe -Mode $mode -Server $serverAddr -Command "REGISTER" -Task $taskName -Address $serviceAddress
        Assert-Status -Mode $mode -Name "$casePrefix REGISTER basic" -Response $resp -ExpectedStatus "OK"

        $resp = Invoke-Probe -Mode $mode -Server $serverAddr -Command "QUERY" -Task $taskName
        Assert-Status -Mode $mode -Name "$casePrefix QUERY basic" -Response $resp -ExpectedStatus "OK" -ExpectedAddress $serviceAddress

        # 7) round robin
        $rrTask = "suite-$mode-rr"
        $rrA = "10.10.10.21:9001"
        $rrB = "10.10.10.22:9002"
        $null = Invoke-Probe -Mode $mode -Server $serverAddr -Command "REGISTER" -Task $rrTask -Address $rrA
        $null = Invoke-Probe -Mode $mode -Server $serverAddr -Command "REGISTER" -Task $rrTask -Address $rrB

        $q1 = Invoke-Probe -Mode $mode -Server $serverAddr -Command "QUERY" -Task $rrTask
        $q2 = Invoke-Probe -Mode $mode -Server $serverAddr -Command "QUERY" -Task $rrTask
        $q3 = Invoke-Probe -Mode $mode -Server $serverAddr -Command "QUERY" -Task $rrTask
        $q4 = Invoke-Probe -Mode $mode -Server $serverAddr -Command "QUERY" -Task $rrTask

        $rrSeq = @($q1.address, $q2.address, $q3.address, $q4.address)
        if (($rrSeq[0] -eq $rrA) -and ($rrSeq[1] -eq $rrB) -and ($rrSeq[2] -eq $rrA) -and ($rrSeq[3] -eq $rrB)) {
            Add-CaseResult -Mode $mode -Name "$casePrefix round robin" -Status "PASS" -Detail ($rrSeq -join " -> ")
        } else {
            Add-CaseResult -Mode $mode -Name "$casePrefix round robin" -Status "FAIL" -Detail ("Unexpected sequence: " + ($rrSeq -join " -> "))
        }

        # 8) cleanup stale entry
        $staleTask = "suite-$mode-stale"
        $staleAddr = "10.10.10.50:9050"
        $null = Invoke-Probe -Mode $mode -Server $serverAddr -Command "REGISTER" -Task $staleTask -Address $staleAddr
        Start-Sleep -Seconds 7
        $resp = Invoke-Probe -Mode $mode -Server $serverAddr -Command "QUERY" -Task $staleTask
        Assert-Status -Mode $mode -Name "$casePrefix cleanup stale" -Response $resp -ExpectedStatus "NOTFOUND"

        # 9) query accounting smoke (no direct stats endpoint, so check stable OK)
        $acctTask = "suite-$mode-accounting"
        $acctAddr = "10.10.10.70:9070"
        $null = Invoke-Probe -Mode $mode -Server $serverAddr -Command "REGISTER" -Task $acctTask -Address $acctAddr
        $allOk = $true
        for ($i = 0; $i -lt 10; $i++) {
            $resp = Invoke-Probe -Mode $mode -Server $serverAddr -Command "QUERY" -Task $acctTask
            if ($resp.status -ne "OK" -or $resp.address -ne $acctAddr) {
                $allOk = $false
                break
            }
        }
        if ($allOk) {
            Add-CaseResult -Mode $mode -Name "$casePrefix repeated query stability" -Status "PASS" -Detail "10/10 OK"
        } else {
            Add-CaseResult -Mode $mode -Name "$casePrefix repeated query stability" -Status "FAIL" -Detail "At least one query returned unexpected result"
        }
    }
    catch {
        Add-CaseResult -Mode $mode -Name "$mode suite setup" -Status "FAIL" -Detail $_.Exception.Message
    }
    finally {
        Stop-TestServer -Process $serverProc
        if (-not $KeepServerLogs -and (Test-Path $serverLog)) {
            Remove-Item $serverLog -Force -ErrorAction SilentlyContinue
        }
        $stderrLog = "$serverLog.err"
        if (-not $KeepServerLogs -and (Test-Path $stderrLog)) {
            Remove-Item $stderrLog -Force -ErrorAction SilentlyContinue
        }
    }

    $portCursor++
}

Write-Section "Summary"
$total = $script:Passed + $script:Failed + $script:Skipped
Write-Host "Total:  $total" -ForegroundColor White
Write-Host "Passed: $($script:Passed)" -ForegroundColor Green
Write-Host "Failed: $($script:Failed)" -ForegroundColor Red
Write-Host "Skipped: $($script:Skipped)" -ForegroundColor Yellow

$reportFile = Join-Path $runDir "suite_results.csv"
$script:CaseRows | Export-Csv -Path $reportFile -NoTypeInformation
Write-Host "Report: $reportFile" -ForegroundColor Cyan

if ($script:Failed -gt 0) {
    exit 1
}
exit 0
