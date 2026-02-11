# VM Dummy Service Generator for TDS Load Testing
# Runs multiple dummy services that:
# 1. Register with the TDS server
# 2. Listen for and process incoming data from clients
# Designed to run on client VMs or standalone

param(
    [string]$ServerAddr = "192.168.0.181:5000",  # TDS server address
    [int]$NumServices = 50,                       # Number of dummy services to run on this VM
    [int]$NumTasks = 10,                          # Number of different tasks to distribute services across
    [string]$Protocol = "udp",                    # udp, tcp, or tls
    [int]$HeartbeatInterval = 30,                 # Seconds between heartbeats
    [string]$ServicePortRange = "8000-8500",      # Port range for dummy services
    [string]$LogFile = "dummy_services.log",
    [int]$ServiceTimeout = 5000                   # Timeout for service operations (ms)
)

$ErrorActionPreference = "Continue"

# Get VM's IP address (first non-loopback IPv4) - cross-platform
function Get-LocalIP {
    if ($IsWindows -or $PSVersionTable.PSVersion.Major -le 5) {
        # Windows
        try {
            $ip = Get-NetIPAddress -AddressFamily IPv4 | 
                  Where-Object { $_.IPAddress -ne "127.0.0.1" -and $_.PrefixOrigin -ne "WellKnown" } | 
                  Select-Object -First 1 -ExpandProperty IPAddress
            if ($ip) { return $ip }
        } catch { }
    } else {
        # Linux/Unix
        try {
            $ip = (hostname -I 2>$null).Trim().Split()[0]
            if ($ip -and $ip -ne "127.0.0.1") { return $ip }
        } catch { }
        
        # Fallback: parse ip addr
        try {
            $ipOutput = /sbin/ip -4 addr show 2>$null | Select-String "inet " | Where-Object { $_ -notmatch "127.0.0.1" } | Select-Object -First 1
            if ($ipOutput) {
                $ip = ($ipOutput -replace '.*inet\s+([0-9.]+).*','$1')
                if ($ip) { return $ip }
            }
        } catch { }
    }
    
    return "127.0.0.1"
}

$localIP = Get-LocalIP
$portStart, $portEnd = $ServicePortRange -split '-' | ForEach-Object { [int]$_ }

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "TDS Dummy Service Generator" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Configuration:" -ForegroundColor Yellow
Write-Host "  TDS Server: $ServerAddr" -ForegroundColor White
Write-Host "  Protocol: $Protocol" -ForegroundColor White
Write-Host "  Local IP: $localIP" -ForegroundColor White
Write-Host "  Services: $NumServices" -ForegroundColor White
Write-Host "  Tasks: $NumTasks" -ForegroundColor White
Write-Host "  Heartbeat: ${HeartbeatInterval}s" -ForegroundColor White
Write-Host "  Port Range: $ServicePortRange" -ForegroundColor White
Write-Host "  Log File: $LogFile" -ForegroundColor White
Write-Host ""

# Initialize log file
$timestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
"[$timestamp] ===== Dummy Service Generator Started =====" | Out-File -FilePath $LogFile -Append

# Function to send UDP registration
function Send-RegisterUDP {
    param (
        [string]$Server,
        [string]$Task,
        [string]$Address
    )
    
    $jsonObj = @{
        cmd = "REGISTER"
        task = $Task
        address = $Address
    }
    $message = $jsonObj | ConvertTo-Json -Compress
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($message)
    
    try {
        $udpClient = New-Object System.Net.Sockets.UdpClient
        $udpClient.Client.ReceiveTimeout = 1000
        $serverParts = $Server.Split(':')
        $udpClient.Connect($serverParts[0], [int]$serverParts[1])
        $udpClient.Send($bytes, $bytes.Length) | Out-Null
        
        $remoteEP = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
        $response = $udpClient.Receive([ref]$remoteEP)
        $responseText = [System.Text.Encoding]::UTF8.GetString($response)
        $udpClient.Close()
        
        $respObj = $responseText | ConvertFrom-Json
        return ($respObj.status -eq "OK")
    } catch {
        return $false
    }
}

# Function to send TCP registration
function Send-RegisterTCP {
    param (
        [string]$Server,
        [string]$Task,
        [string]$Address
    )
    
    $jsonObj = @{
        cmd = "REGISTER"
        task = $Task
        address = $Address
    }
    $message = ($jsonObj | ConvertTo-Json -Compress) + "`n"
    
    try {
        $serverParts = $Server.Split(':')
        $tcpClient = New-Object System.Net.Sockets.TcpClient
        $tcpClient.Connect($serverParts[0], [int]$serverParts[1])
        $stream = $tcpClient.GetStream()
        $writer = New-Object System.IO.StreamWriter($stream)
        $reader = New-Object System.IO.StreamReader($stream)
        
        $writer.WriteLine($message)
        $writer.Flush()
        
        $response = $reader.ReadLine()
        
        $writer.Close()
        $reader.Close()
        $stream.Close()
        $tcpClient.Close()
        
        $respObj = $response | ConvertFrom-Json
        return ($respObj.status -eq "OK")
    } catch {
        return $false
    }
}

# Service registration function
function Register-Service {
    param (
        [string]$Task,
        [string]$Address
    )
    
    if ($Protocol -eq "tcp" -or $Protocol -eq "tls") {
        return Send-RegisterTCP -Server $ServerAddr -Task $Task -Address $Address
    } else {
        return Send-RegisterUDP -Server $ServerAddr -Task $Task -Address $Address
    }
}

# UDP Service Listener - processes incoming data
$udpServiceListener = {
    param($Port, $ServiceId, $StatsFile)
    
    $requests = 0
    $bytes = 0
    
    try {
        $listener = New-Object System.Net.Sockets.UdpClient $Port
        $listener.Client.ReceiveTimeout = 100
        
        while ($true) {
            try {
                $remoteEP = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
                $data = $listener.Receive([ref]$remoteEP)
                $message = [System.Text.Encoding]::UTF8.GetString($data)
                
                # Parse incoming message
                try {
                    $msgObj = $message | ConvertFrom-Json
                    
                    # Process the data (simulate work)
                    $processedSize = $message.Length
                    
                    # Send acknowledgment
                    $response = @{
                        status = "OK"
                        service_id = $ServiceId
                        received_bytes = $processedSize
                        timestamp = (Get-Date).ToString("o")
                    } | ConvertTo-Json -Compress
                    
                    $responseBytes = [System.Text.Encoding]::UTF8.GetBytes($response)
                    $listener.Send($responseBytes, $responseBytes.Length, $remoteEP) | Out-Null
                    
                    # Update stats
                    $requests++
                    $bytes += $processedSize
                    
                    # Periodically write stats to file (every 10 requests)
                    if ($requests % 10 -eq 0) {
                        "$ServiceId $requests $bytes" | Out-File -FilePath $StatsFile -Force
                    }
                    
                } catch {
                    # Invalid message format, send error
                    $errorResponse = @{
                        status = "ERROR"
                        message = "Invalid message format"
                    } | ConvertTo-Json -Compress
                    $errorBytes = [System.Text.Encoding]::UTF8.GetBytes($errorResponse)
                    $listener.Send($errorBytes, $errorBytes.Length, $remoteEP) | Out-Null
                }
                
            } catch [System.Net.Sockets.SocketException] {
                # Timeout or socket error - continue listening
                Start-Sleep -Milliseconds 100
            }
        }
    } catch {
        # Suppress errors to prevent memory accumulation
    } finally {
        # Write final stats
        if ($requests -gt 0) {
            "$ServiceId $requests $bytes" | Out-File -FilePath $StatsFile -Force
        }
        if ($listener) {
            $listener.Close()
        }
    }
}

# TCP Service Listener - processes incoming data
$tcpServiceListener = {
    param($Port, $ServiceId, $StatsFile)
    
    $requests = 0
    $bytes = 0
    
    try {
        $listener = New-Object System.Net.Sockets.TcpListener([System.Net.IPAddress]::Any, $Port)
        $listener.Start()
        
        while ($true) {
            try {
                # Non-blocking check for pending connections
                if ($listener.Pending()) {
                    $client = $listener.AcceptTcpClient()
                    $client.ReceiveTimeout = 5000
                    $client.SendTimeout = 5000
                    
                    try {
                        $stream = $client.GetStream()
                        $reader = New-Object System.IO.StreamReader($stream)
                        $writer = New-Object System.IO.StreamWriter($stream)
                        
                        $message = $reader.ReadLine()
                        
                        if ($message) {
                            try {
                                $msgObj = $message | ConvertFrom-Json
                                
                                # Process the data
                                $processedSize = $message.Length
                                
                                # Send acknowledgment
                                $response = @{
                                    status = "OK"
                                    service_id = $ServiceId
                                    received_bytes = $processedSize
                                    timestamp = (Get-Date).ToString("o")
                                } | ConvertTo-Json -Compress
                                
                                $writer.WriteLine($response)
                                $writer.Flush()
                                
                                # Update stats
                                $requests++
                                $bytes += $processedSize
                                
                                # Periodically write stats to file
                                if ($requests % 10 -eq 0) {
                                    "$ServiceId $requests $bytes" | Out-File -FilePath $StatsFile -Force
                                }
                                
                            } catch {
                                # Invalid message
                                $errorResponse = @{
                                    status = "ERROR"
                                    message = "Invalid message format"
                                } | ConvertTo-Json -Compress
                                $writer.WriteLine($errorResponse)
                                $writer.Flush()
                            }
                        }
                        
                        $writer.Close()
                        $reader.Close()
                        $stream.Close()
                    } finally {
                        $client.Close()
                    }
                } else {
                    Start-Sleep -Milliseconds 100
                }
            } catch {
                # Connection error - continue listening
                Start-Sleep -Milliseconds 100
            }
        }
    } catch {
        # Suppress errors
    } finally {
        # Write final stats
        if ($requests -gt 0) {
            "$ServiceId $requests $bytes" | Out-File -FilePath $StatsFile -Force
        }
        if ($listener) {
            $listener.Stop()
        }
    }
}

# Generate service definitions
$services = @()
$taskNames = 1..$NumTasks | ForEach-Object { "task_$_" }
$serviceJobs = @()

# Create temp directory for stats files
$tempDir = [System.IO.Path]::GetTempPath()
$statsDir = Join-Path $tempDir "tds_service_stats_$PID"
if (-not (Test-Path $statsDir)) {
    New-Item -ItemType Directory -Path $statsDir -Force | Out-Null
}

for ($i = 0; $i -lt $NumServices; $i++) {
    $port = $portStart + ($i % ($portEnd - $portStart + 1))
    $taskIndex = $i % $NumTasks
    $task = $taskNames[$taskIndex]
    $address = "${localIP}:${port}"
    $serviceId = "service_$i"
    
    $services += @{
        ServiceId = $serviceId
        Task = $task
        Address = $address
        Port = $port
        LastRegister = $null
        RegisterCount = 0
        FailCount = 0
        Job = $null
        StatsFile = (Join-Path $statsDir "$serviceId.txt")
    }
}

Write-Host "[INFO] Generated $($services.Count) dummy services across $NumTasks tasks" -ForegroundColor Green
Write-Host ""

# Start service listeners
Write-Host "[INIT] Starting service listeners..." -ForegroundColor Yellow
$listenersStarted = 0

foreach ($service in $services) {
    try {
        if ($Protocol -eq "tcp" -or $Protocol -eq "tls") {
            $job = Start-Job -ScriptBlock $tcpServiceListener -ArgumentList $service.Port, $service.ServiceId, $service.StatsFile
        } else {
            $job = Start-Job -ScriptBlock $udpServiceListener -ArgumentList $service.Port, $service.ServiceId, $service.StatsFile
        }
        # Immediately clear any startup output
        Receive-Job -Job $job -ErrorAction SilentlyContinue | Out-Null
        $service.Job = $job
        $listenersStarted++
        Write-Host "  [OK] Listener started: $($service.Address) (Job $($job.Id))" -ForegroundColor Green
    } catch {
        Write-Host "  [FAIL] Failed to start listener on $($service.Address): $_" -ForegroundColor Red
    }
}

Write-Host ""
Write-Host "[INFO] Started $listenersStarted service listeners" -ForegroundColor Green
Write-Host ""

# Initial registration burst
Write-Host "[INIT] Performing initial registration of all services..." -ForegroundColor Yellow
$successCount = 0
$failCount = 0

foreach ($service in $services) {
    $success = Register-Service -Task $service.Task -Address $service.Address
    if ($success) {
        $service.LastRegister = Get-Date
        $service.RegisterCount++
        $successCount++
        Write-Host "  [OK] $($service.Task) -> $($service.Address)" -ForegroundColor Green
    } else {
        $service.FailCount++
        $failCount++
        Write-Host "  [FAIL] $($service.Task) -> $($service.Address)" -ForegroundColor Red
    }
}

$timestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
$logMsg = "[$timestamp] Initial registration: $successCount success, $failCount failed"
Write-Host ""
Write-Host $logMsg -ForegroundColor Cyan
$logMsg | Out-File -FilePath $LogFile -Append

# Continuous heartbeat loop
Write-Host ""
Write-Host "[RUNNING] Starting continuous heartbeat loop (Ctrl+C to stop)..." -ForegroundColor Yellow
Write-Host ""

$loopCount = 0
$statsInterval = 10  # Show stats every N loops

try {
    while ($true) {
        $loopCount++
        $loopStart = Get-Date
        
        $successCount = 0
        $failCount = 0
        
        foreach ($service in $services) {
            $timeSinceLastRegister = if ($service.LastRegister) {
                (Get-Date) - $service.LastRegister
            } else {
                [TimeSpan]::MaxValue
            }
            
            # Re-register if heartbeat interval has passed
            if ($timeSinceLastRegister.TotalSeconds -ge $HeartbeatInterval) {
                $success = Register-Service -Task $service.Task -Address $service.Address
                if ($success) {
                    $service.LastRegister = Get-Date
                    $service.RegisterCount++
                    $successCount++
                } else {
                    $service.FailCount++
                    $failCount++
                }
            }
        }
        
        $loopDuration = ((Get-Date) - $loopStart).TotalSeconds
        
        # Show stats periodically
        if ($loopCount % $statsInterval -eq 0) {
            $totalRegistrations = ($services | Measure-Object -Property RegisterCount -Sum).Sum
            $totalFails = ($services | Measure-Object -Property FailCount -Sum).Sum
            $successRate = if (($totalRegistrations + $totalFails) -gt 0) {
                [math]::Round(($totalRegistrations / ($totalRegistrations + $totalFails)) * 100, 2)
            } else { 0 }
            
            # Calculate service stats from files
            $totalRequests = 0
            $totalBytes = 0
            foreach ($service in $services) {
                if (Test-Path $service.StatsFile) {
                    $stats = Get-Content $service.StatsFile -ErrorAction SilentlyContinue
                    if ($stats -and $stats -match '^\S+\s+(\d+)\s+(\d+)$') {
                        $totalRequests += [int]$matches[1]
                        $totalBytes += [int]$matches[2]
                    }
                }
            }
            
            # Clean up job output to prevent memory leak
            foreach ($service in $services) {
                if ($service.Job) {
                    Receive-Job -Job $service.Job -ErrorAction SilentlyContinue | Out-Null
                }
            }
            
            $timestamp = Get-Date -Format "HH:mm:ss"
            Write-Host "[$timestamp] Loop $loopCount | Registered: $successCount | Failed: $failCount | Total Reg: $totalRegistrations | Success: ${successRate}% | Requests Served: $totalRequests | Data: $totalBytes bytes" -ForegroundColor Cyan
            
            # Log to file
            $logTimestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
            "[$logTimestamp] Total registrations: $totalRegistrations, Fails: $totalFails, Success Rate: ${successRate}%, Requests: $totalRequests, Bytes: $totalBytes" | Out-File -FilePath $LogFile -Append
        }
        
        # Sleep until next heartbeat check
        Start-Sleep -Seconds 5
    }
} catch {
    Write-Host ""
    Write-Host "[STOPPED] Shutting down dummy services..." -ForegroundColor Yellow
}

# Cleanup
Write-Host ""
Write-Host "[CLEANUP] Stopping service listeners..." -ForegroundColor Yellow

foreach ($service in $services) {
    if ($service.Job) {
        Stop-Job -Job $service.Job -ErrorAction SilentlyContinue
        Remove-Job -Job $service.Job -Force -ErrorAction SilentlyContinue
        Write-Host "  [OK] Stopped listener on $($service.Address)" -ForegroundColor Green
    }
}

$timestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
"[$timestamp] ===== Dummy Service Generator Stopped =====" | Out-File -FilePath $LogFile -Append

$totalRegistrations = ($services | Measure-Object -Property RegisterCount -Sum).Sum
$totalFails = ($services | Measure-Object -Property FailCount -Sum).Sum

# Calculate final service stats from files
$totalRequests = 0
$totalBytes = 0
foreach ($service in $services) {
    if (Test-Path $service.StatsFile) {
        $stats = Get-Content $service.StatsFile -ErrorAction SilentlyContinue
        if ($stats -and $stats -match '^\S+\s+(\d+)\s+(\d+)$') {
            $totalRequests += [int]$matches[1]
            $totalBytes += [int]$matches[2]
        }
    }
}

Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Final Statistics:" -ForegroundColor Yellow
Write-Host "  Total Registrations: $totalRegistrations" -ForegroundColor White
Write-Host "  Total Failures: $totalFails" -ForegroundColor White
if (($totalRegistrations + $totalFails) -gt 0) {
    Write-Host "  Success Rate: $([math]::Round(($totalRegistrations / ($totalRegistrations + $totalFails)) * 100, 2))%" -ForegroundColor White
}
Write-Host "  Requests Served: $totalRequests" -ForegroundColor White
Write-Host "  Data Processed: $totalBytes bytes" -ForegroundColor White
Write-Host "========================================" -ForegroundColor Cyan

# Write final statistics to log file for orchestrator parsing
$finalTimestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
"[$finalTimestamp] Final Statistics:" | Out-File -FilePath $LogFile -Append
"[$finalTimestamp] Requests Served: $totalRequests" | Out-File -FilePath $LogFile -Append
"[$finalTimestamp] Data Processed: $totalBytes" | Out-File -FilePath $LogFile -Append

# Cleanup temp stats directory
if (Test-Path $statsDir) {
    Remove-Item -Path $statsDir -Recurse -Force -ErrorAction SilentlyContinue
}
