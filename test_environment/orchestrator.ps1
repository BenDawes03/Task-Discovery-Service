# VM Test Orchestrator for TDS Load Testing
# Coordinates load testing across multiple VMs via SSH
# Tests both service discovery (TDS) and service-to-service data transfer
# Requires: OpenSSH client, configured SSH keys or credentials

param(
    [string]$ServerVM = "192.168.0.181",          # TDS server VM IP
    [string[]]$ClientVMs = @("192.168.0.180", "192.168.0.182", "192.168.0.179"),  # Client VM IPs
    [string]$ServerUsername = "trs",               # SSH username for server VM
    [string]$ClientUsername = "user",              # SSH username for client VMs
    [string]$Protocol = "udp",                     # udp, tcp, or tls
    [int]$ServicesPerClient = 10,                  # Dummy services per client VM (reduced to prevent memory issues)
    [int]$QueryThreadsPerClient = 10,              # Query threads per client VM
    [int]$TestDuration = 300,                      # Test duration in seconds (0 = infinite)
    [int]$DataSize = 256,                          # Size of test data to send (bytes)
    [string]$RemoteScriptPath = "/tmp/tds-test",   # Path on remote VMs for scripts
    [switch]$CleanupOnly                           # Only cleanup, don't start test
)

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "TDS VM Test Orchestrator" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Configuration:" -ForegroundColor Yellow
Write-Host "  Server VM: $ServerVM ($ServerUsername)" -ForegroundColor White
Write-Host "  Client VMs: $($ClientVMs -join ', ') ($ClientUsername)" -ForegroundColor White
Write-Host "  Protocol: $Protocol" -ForegroundColor White
Write-Host "  Services per client: $ServicesPerClient" -ForegroundColor White
Write-Host "  Query threads per client: $QueryThreadsPerClient" -ForegroundColor White
Write-Host "  Data payload size: $DataSize bytes" -ForegroundColor White
Write-Host "  Test duration: $(if ($TestDuration -eq 0) { 'Infinite' } else { "${TestDuration}s" })" -ForegroundColor White
Write-Host ""

# Check SSH availability
if (-not (Get-Command ssh -ErrorAction SilentlyContinue)) {
    Write-Host "[ERROR] SSH client not found. Please install OpenSSH client." -ForegroundColor Red
    exit 1
}

# Function to execute SSH command
function Invoke-SSHCommand {
    param(
        [string]$VMHost,
        [string]$Username,
        [string]$Command
    )
    
    $sshKeyPath = "$env:USERPROFILE\.ssh\id_rsa"
    
    try {
        ssh -i $sshKeyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL "${Username}@${VMHost}" "$Command" 2>&1
        return $true
    } catch {
        Write-Host "[ERROR] SSH command failed on ${VMHost}: $_" -ForegroundColor Red
        return $false
    }
}

# Function to copy file via SCP
function Copy-SSHFile {
    param(
        [string]$VMHost,
        [string]$Username,
        [string]$LocalPath,
        [string]$RemotePath
    )
    
    $sshKeyPath = "$env:USERPROFILE\.ssh\id_rsa"
    
    try {
        scp -i $sshKeyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL $LocalPath "${Username}@${VMHost}:${RemotePath}" 2>&1 | Out-Null
        return $true
    } catch {
        Write-Host "[ERROR] SCP failed to ${VMHost}: $_" -ForegroundColor Red
        return $false
    }
}

# Cleanup function
function Stop-RemoteProcesses {
    param([string]$VM, [string]$Username)
    
    Write-Host "[CLEANUP] Stopping processes on $VM..." -ForegroundColor Yellow
    Invoke-SSHCommand -VMHost $VM -Username $Username -Command "pkill -f 'dummy_services.ps1' || true" | Out-Null
    Invoke-SSHCommand -VMHost $VM -Username $Username -Command "pkill -f 'client_queries.ps1' || true" | Out-Null
    Invoke-SSHCommand -VMHost $VM -Username $Username -Command "pkill -f 'pwsh' || true" | Out-Null
    Write-Host "[OK] Cleanup complete on $VM" -ForegroundColor Green
}

# Cleanup if requested
if ($CleanupOnly) {
    Write-Host "[CLEANUP] Performing cleanup on all VMs..." -ForegroundColor Yellow
    foreach ($vm in $ClientVMs) {
        Stop-RemoteProcesses -VM $vm -Username $ClientUsername
    }
    Write-Host ""
    Write-Host "[DONE] Cleanup complete!" -ForegroundColor Green
    exit 0
}

# Check connectivity to all VMs
Write-Host "[CHECK] Testing SSH connectivity to all VMs..." -ForegroundColor Yellow
$allReachable = $true
foreach ($vm in $ClientVMs) {
    $result = Invoke-SSHCommand -VMHost $vm -Username $ClientUsername -Command "echo 'OK'"
    if ($result -match "OK") {
        Write-Host "  [OK] $vm is reachable" -ForegroundColor Green
    } else {
        Write-Host "  [FAIL] $vm is NOT reachable" -ForegroundColor Red
        $allReachable = $false
    }
}

if (-not $allReachable) {
    Write-Host ""
    Write-Host "[ERROR] Not all VMs are reachable. Please check SSH configuration." -ForegroundColor Red
    exit 1
}

Write-Host ""

# Ensure PowerShell is installed on remote VMs
Write-Host "[CHECK] Verifying PowerShell on client VMs..." -ForegroundColor Yellow
foreach ($vm in $ClientVMs) {
    $pwshCheck = Invoke-SSHCommand -VMHost $vm -Username $ClientUsername -Command "which pwsh || which powershell"
    if ($pwshCheck) {
        Write-Host "  [OK] $vm has PowerShell" -ForegroundColor Green
    } else {
        Write-Host "  [WARN] $vm may not have PowerShell installed" -ForegroundColor Yellow
    }
}

Write-Host ""

# Copy scripts to client VMs
Write-Host "[DEPLOY] Copying test scripts to client VMs..." -ForegroundColor Yellow
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path

foreach ($vm in $ClientVMs) {
    Write-Host "  Deploying to $vm..." -ForegroundColor Cyan
    
    # Create remote directory
    Invoke-SSHCommand -VMHost $vm -Username $ClientUsername -Command "mkdir -p $RemoteScriptPath" | Out-Null
    
    # Copy both scripts
    $success = Copy-SSHFile -VMHost $vm -Username $ClientUsername `
        -LocalPath "$scriptDir\dummy_services.ps1" `
        -RemotePath "$RemoteScriptPath/dummy_services.ps1"
    
    if ($success) {
        $success = Copy-SSHFile -VMHost $vm -Username $ClientUsername `
            -LocalPath "$scriptDir\client_queries.ps1" `
            -RemotePath "$RemoteScriptPath/client_queries.ps1"
    }
    
    if ($success) {
        Write-Host "  [OK] Scripts deployed to $vm" -ForegroundColor Green
    } else {
        Write-Host "  [FAIL] Failed to deploy scripts to $vm" -ForegroundColor Red
        exit 1
    }
}

Write-Host ""

# Clear old log files from previous test runs
Write-Host "[CLEAN] Truncating old log files on client VMs..." -ForegroundColor Yellow
foreach ($vm in $ClientVMs) {
    Invoke-SSHCommand -VMHost $vm -Username $ClientUsername -Command "> $RemoteScriptPath/dummy_services.log" | Out-Null
    Invoke-SSHCommand -VMHost $vm -Username $ClientUsername -Command "> $RemoteScriptPath/client_queries.log" | Out-Null
}
Write-Host "  [OK] Log files cleared" -ForegroundColor Green

Write-Host ""

# Start dummy services on all client VMs
Write-Host "[START] Starting dummy services on client VMs..." -ForegroundColor Yellow
foreach ($vm in $ClientVMs) {
    # Create startup script on remote VM
    $scriptContent = "#!/bin/bash`ncd $RemoteScriptPath`nnohup pwsh -File dummy_services.ps1 -ServerAddr ${ServerVM}:5000 -NumServices $ServicesPerClient -Protocol $Protocol -HeartbeatInterval 30 > dummy_services_nohup.out 2>&1 < /dev/null &`nexit 0"
    $createCmd = "echo '$scriptContent' > $RemoteScriptPath/start_dummy.sh && chmod +x $RemoteScriptPath/start_dummy.sh"
    
    Write-Host "  Starting on $vm..." -ForegroundColor Cyan
    Invoke-SSHCommand -VMHost $vm -Username $ClientUsername -Command $createCmd | Out-Null
    
    # Execute the startup script
    $execCmd = "$RemoteScriptPath/start_dummy.sh &"
    Invoke-SSHCommand -VMHost $vm -Username $ClientUsername -Command $execCmd | Out-Null
    
    Start-Sleep -Milliseconds 500
    Write-Host "  [OK] Dummy services started on $vm" -ForegroundColor Green
}

Write-Host ""
Write-Host "[WAIT] Waiting 10 seconds for services to register..." -ForegroundColor Yellow
Start-Sleep -Seconds 10

# Start client queries on all client VMs (these will send data to discovered services)
Write-Host ""
Write-Host "[START] Starting client queries on client VMs..." -ForegroundColor Yellow
foreach ($vm in $ClientVMs) {
    # Calculate queries per thread: account for think time (100ms) + avg query/data time (~20ms) = ~8 qps realistic
    $queriesPerThread = if ($TestDuration -eq 0) { 0 } else { [math]::Floor($TestDuration * 7) }  # ~7 qps per thread to ensure completion
    
    # Create startup script on remote VM
    $scriptContent = "#!/bin/bash`ncd $RemoteScriptPath`nnohup pwsh -File client_queries.ps1 -ServerAddr ${ServerVM}:5000 -NumThreads $QueryThreadsPerClient -QueriesPerThread $queriesPerThread -Protocol $Protocol -ThinkTime 100 -DataSize $DataSize > client_queries_nohup.out 2>&1 < /dev/null &`nexit 0"
    $createCmd = "echo '$scriptContent' > $RemoteScriptPath/start_query.sh && chmod +x $RemoteScriptPath/start_query.sh"
    
    Write-Host "  Starting on $vm..." -ForegroundColor Cyan
    Invoke-SSHCommand -VMHost $vm -Username $ClientUsername -Command $createCmd | Out-Null
    
    # Execute the startup script
    $execCmd = "$RemoteScriptPath/start_query.sh &"
    Invoke-SSHCommand -VMHost $vm -Username $ClientUsername -Command $execCmd | Out-Null
    
    Start-Sleep -Milliseconds 500
    Write-Host "  [OK] Client queries started on $vm" -ForegroundColor Green
}

Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "LOAD TEST RUNNING" -ForegroundColor Green
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "Configuration:" -ForegroundColor Yellow
Write-Host "  Total Services: $($ServicesPerClient * $ClientVMs.Count)" -ForegroundColor White
Write-Host "  Total Query Threads: $($QueryThreadsPerClient * $ClientVMs.Count)" -ForegroundColor White
Write-Host "  Data Payload: $DataSize bytes" -ForegroundColor White
Write-Host "  Duration: $(if ($TestDuration -eq 0) { 'Infinite (Ctrl+C to stop)' } else { "${TestDuration}s" })" -ForegroundColor White
Write-Host ""
Write-Host "Test Flow:" -ForegroundColor Yellow
Write-Host "  1. Services register with TDS server" -ForegroundColor White
Write-Host "  2. Clients query TDS for service addresses" -ForegroundColor White
Write-Host "  3. Clients send $DataSize byte payloads to discovered services" -ForegroundColor White
Write-Host "  4. Services acknowledge receipt" -ForegroundColor White
Write-Host ""
Write-Host "Monitor server logs and TUI for real-time metrics." -ForegroundColor Cyan
Write-Host ""
Write-Host "To check logs on client VMs:" -ForegroundColor Yellow
Write-Host "  ssh $ClientUsername@<VM-IP> 'tail -f $RemoteScriptPath/dummy_services.log'" -ForegroundColor Gray
Write-Host "  ssh $ClientUsername@<VM-IP> 'tail -f $RemoteScriptPath/client_queries.log'" -ForegroundColor Gray
Write-Host ""
Write-Host "To stop the test:" -ForegroundColor Yellow
Write-Host "  pwsh -File orchestrator.ps1 -CleanupOnly" -ForegroundColor Gray
Write-Host ""

# Wait for test duration if specified
if ($TestDuration -gt 0) {
    Write-Host "[WAIT] Test will run for ${TestDuration} seconds..." -ForegroundColor Yellow
    Start-Sleep -Seconds $TestDuration
    
    Write-Host ""
    Write-Host "[DONE] Test duration complete. Stopping client processes..." -ForegroundColor Yellow
    
    # Create stop signal file on each VM for graceful shutdown
    foreach ($vm in $ClientVMs) {
        Write-Host "  Sending stop signal to $vm..." -ForegroundColor Cyan
        Invoke-SSHCommand -VMHost $vm -Username $ClientUsername -Command "touch $RemoteScriptPath/client_queries.stop" | Out-Null
    }
    
    # Give processes time to detect signal, complete current queries, collect results, and write stats
    Write-Host "[WAIT] Waiting 45 seconds for processes to finish gracefully..." -ForegroundColor Yellow
    Start-Sleep -Seconds 45
    
    # Collect logs and statistics from all VMs
    Write-Host ""
    Write-Host "[COLLECT] Gathering results from client VMs..." -ForegroundColor Yellow
    $logDir = "test_results_$(Get-Date -Format 'yyyyMMdd_HHmmss')"
    New-Item -ItemType Directory -Path $logDir -Force | Out-Null
    
    $allStats = @()
    
    foreach ($vm in $ClientVMs) {
        $vmName = $vm -replace '\.', '_'
        Write-Host "  Collecting from $vm..." -ForegroundColor Cyan
        
        # Get query statistics from log file
        $statsCmd = "tail -30 $RemoteScriptPath/client_queries.log | grep -E 'Total Queries:|Successful:|Data Processed:|Overall QPS:|Success Rate:' | tail -8"
        $statsOutput = Invoke-SSHCommand -VMHost $vm -Username $ClientUsername -Command $statsCmd
        
        # Parse statistics
        $totalQueries = 0
        $successfulQueries = 0
        $failedQueries = 0
        $dataSuccess = 0
        $qps = 0
        
        if ($statsOutput) {
            foreach ($line in $statsOutput) {
                if ($line -match 'Total Queries:\s*(\d+)') { $totalQueries = [int]$matches[1] }
                if ($line -match 'Successful:\s*(\d+)' -and $successfulQueries -eq 0) { $successfulQueries = [int]$matches[1] }
                if ($line -match 'Failed:\s*(\d+)' -and $failedQueries -eq 0) { $failedQueries = [int]$matches[1] }
                if ($line -match 'Data Successful:\s*(\d+)') { $dataSuccess = [int]$matches[1] }
                if ($line -match 'Overall QPS:\s*([\d.]+)') { $qps = [double]$matches[1] }
            }
        }
        
        # Get service statistics
        $serviceStatsCmd = "tail -30 $RemoteScriptPath/dummy_services.log | grep -E 'Requests:|Requests Served:|Data Processed:' | tail -3"
        $serviceStatsOutput = Invoke-SSHCommand -VMHost $vm -Username $ClientUsername -Command $serviceStatsCmd
        
        $serviceRequests = 0
        $serviceBytes = 0
        
        if ($serviceStatsOutput) {
            foreach ($line in $serviceStatsOutput) {
                # Match periodic format: "Requests: 1110, Bytes: 484070"
                if ($line -match 'Requests:\s*(\d+).*Bytes:\s*(\d+)') { 
                    $serviceRequests = [int]$matches[1]
                    $serviceBytes = [int]$matches[2]
                }
                # Match final format: "Requests Served: 1110"
                if ($line -match 'Requests Served:\s*(\d+)') { $serviceRequests = [int]$matches[1] }
                if ($line -match 'Data Processed:\s*(\d+)') { $serviceBytes = [int]$matches[1] }
            }
        }
        
        # Create PSCustomObject for Measure-Object compatibility
        $vmStats = [PSCustomObject]@{
            VM = $vm
            TotalQueries = $totalQueries
            SuccessfulQueries = $successfulQueries
            FailedQueries = $failedQueries
            DataSuccess = $dataSuccess
            QPS = $qps
            ServiceRequests = $serviceRequests
            ServiceBytes = $serviceBytes
        }
        
        $allStats += $vmStats
        
        # Download log files
        $sshKeyPath = "$env:USERPROFILE\.ssh\id_rsa"
        scp -i $sshKeyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL "${ClientUsername}@${vm}:${RemoteScriptPath}/dummy_services.log" "$logDir/${vmName}_services.log" 2>&1 | Out-Null
        scp -i $sshKeyPath -o StrictHostKeyChecking=no -o UserKnownHostsFile=NUL "${ClientUsername}@${vm}:${RemoteScriptPath}/client_queries.log" "$logDir/${vmName}_queries.log" 2>&1 | Out-Null
        
        Write-Host "  [OK] $vm - Queries: $($vmStats.TotalQueries) | Service Requests: $($vmStats.ServiceRequests) | Data: $($vmStats.ServiceBytes) bytes" -ForegroundColor Green
    }
    
    # Calculate aggregated statistics
    $totalQueries = ($allStats | Measure-Object -Property TotalQueries -Sum).Sum
    $totalSuccess = ($allStats | Measure-Object -Property SuccessfulQueries -Sum).Sum
    $totalFailed = ($allStats | Measure-Object -Property FailedQueries -Sum).Sum
    $totalServiceRequests = ($allStats | Measure-Object -Property ServiceRequests -Sum).Sum
    $totalServiceBytes = ($allStats | Measure-Object -Property ServiceBytes -Sum).Sum
    $avgQPS = ($allStats | Measure-Object -Property QPS -Average).Average
    
    # Cleanup
    Write-Host ""
    Write-Host "[CLEANUP] Stopping all processes..." -ForegroundColor Yellow
    foreach ($vm in $ClientVMs) {
        Stop-RemoteProcesses -VM $vm -Username $ClientUsername
    }
    
    Write-Host ""
    Write-Host "========================================" -ForegroundColor Cyan
    Write-Host "TEST COMPLETE - RESULTS SUMMARY" -ForegroundColor Green
    Write-Host "========================================" -ForegroundColor Cyan
    Write-Host ""
    Write-Host "TDS Query Statistics:" -ForegroundColor Yellow
    Write-Host "  Total Queries: $totalQueries" -ForegroundColor White
    Write-Host "  Successful: $totalSuccess" -ForegroundColor White
    Write-Host "  Failed: $totalFailed" -ForegroundColor White
    if ($totalQueries -gt 0) {
        Write-Host "  Success Rate: $([math]::Round(($totalSuccess / $totalQueries) * 100, 2))%" -ForegroundColor White
    }
    Write-Host "  Average QPS: $([math]::Round($avgQPS, 2))" -ForegroundColor White
    Write-Host "  Combined QPS: $([math]::Round($totalQueries / $TestDuration, 2))" -ForegroundColor White
    Write-Host ""
    Write-Host "Service Data Transfer Statistics:" -ForegroundColor Yellow
    Write-Host "  Total Requests Served: $totalServiceRequests" -ForegroundColor White
    Write-Host "  Total Data Processed: $([math]::Round($totalServiceBytes / 1024, 2)) KB" -ForegroundColor White
    if ($TestDuration -gt 0) {
        Write-Host "  Data Throughput: $([math]::Round($totalServiceBytes / $TestDuration, 2)) bytes/sec" -ForegroundColor White
    }
    Write-Host ""
    Write-Host "Per-VM Results:" -ForegroundColor Yellow
    foreach ($stat in $allStats) {
        Write-Host "  $($stat.VM):" -ForegroundColor Cyan
        Write-Host "    TDS Queries: $($stat.TotalQueries) ($($stat.SuccessfulQueries) success) @ $([math]::Round($stat.QPS, 2)) QPS" -ForegroundColor White
        Write-Host "    Service Requests: $($stat.ServiceRequests) | Data: $([math]::Round($stat.ServiceBytes / 1024, 2)) KB" -ForegroundColor White
    }
    Write-Host ""
    Write-Host "Logs saved to: $logDir" -ForegroundColor Cyan
    Write-Host "========================================" -ForegroundColor Cyan
    
} else {
    Write-Host "Test running indefinitely. Press Ctrl+C when done, then run cleanup:" -ForegroundColor Yellow
    Write-Host "  pwsh -File orchestrator.ps1 -CleanupOnly" -ForegroundColor Gray
    
    # Keep script alive
    try {
        while ($true) { Start-Sleep -Seconds 60 }
    } catch {
        Write-Host ""
        Write-Host "[INTERRUPTED] To cleanup, run: pwsh -File orchestrator.ps1 -CleanupOnly" -ForegroundColor Yellow
    }
}
