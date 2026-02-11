# Client Query Script for TDS Load Testing
# Queries TDS server for services, then sends data to discovered services
# Simulates realistic client behavior: discover → communicate

param(
    [string]$ServerAddr = "192.168.0.181:5000",  # TDS server address
    [int]$NumThreads = 10,                        # Number of concurrent query threads
    [int]$QueriesPerThread = 1000,                # Queries per thread (0 = infinite)
    [int]$ThinkTime = 0,                          # Milliseconds between queries per thread
    [string]$Protocol = "udp",                    # udp, tcp, or tls
    [string]$TaskPattern = "task_*",              # Task name pattern to query (supports wildcards)
    [int]$NumTasks = 10,                          # Number of tasks to round-robin through
    [string]$LogFile = "client_queries.log",
    [int]$StatsInterval = 5,                      # Show stats every N seconds
    [int]$DataSize = 256,                         # Size of test data to send (bytes)
    [string]$DataPayload = "auto"                 # Data to send: "auto" generates random, or specify custom
)

$ErrorActionPreference = "Continue"

# Define stop signal file
$stopSignalFile = "client_queries.stop"

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "TDS Client Query & Data Sender" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Configuration:" -ForegroundColor Yellow
Write-Host "  TDS Server: $ServerAddr" -ForegroundColor White
Write-Host "  Protocol: $Protocol" -ForegroundColor White
Write-Host "  Threads: $NumThreads" -ForegroundColor White
Write-Host "  Queries per thread: $(if ($QueriesPerThread -eq 0) { 'Infinite' } else { $QueriesPerThread })" -ForegroundColor White
Write-Host "  Think time: ${ThinkTime}ms" -ForegroundColor White
Write-Host "  Tasks: $NumTasks" -ForegroundColor White
Write-Host "  Data size: $DataSize bytes" -ForegroundColor White
Write-Host "  Log File: $LogFile" -ForegroundColor White
Write-Host "  Stop signal: $stopSignalFile" -ForegroundColor White
Write-Host ""

# Remove any existing stop signal file
if (Test-Path $stopSignalFile) {
    Remove-Item $stopSignalFile -Force
}

# Initialize log file
$timestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
"[$timestamp] ===== Client Query & Data Sender Started =====" | Out-File -FilePath $LogFile -Append

# Generate task names
$taskNames = 1..$NumTasks | ForEach-Object { "task_$_" }

# Generate test payload
if ($DataPayload -eq "auto") {
    $random = New-Object System.Random
    $dataBytes = New-Object byte[] $DataSize
    $random.NextBytes($dataBytes)
    $payloadData = [Convert]::ToBase64String($dataBytes)
} else {
    $payloadData = $DataPayload
}

# Function to send UDP query to TDS
function Send-QueryUDP {
    param (
        [string]$Server,
        [string]$Task
    )
    
    $jsonObj = @{
        cmd = "QUERY"
        task = $Task
    }
    $message = $jsonObj | ConvertTo-Json -Compress
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($message)
    
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    
    try {
        $udpClient = New-Object System.Net.Sockets.UdpClient
        $udpClient.Client.ReceiveTimeout = 1000
        $udpClient.Client.SendTimeout = 1000
        $serverParts = $Server.Split(':')
        $udpClient.Connect($serverParts[0], [int]$serverParts[1])
        $sent = $udpClient.Send($bytes, $bytes.Length)
        if ($sent -ne $bytes.Length) {
            throw "Failed to send complete message"
        }
        
        $remoteEP = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
        try {
            $response = $udpClient.Receive([ref]$remoteEP)
        } catch [System.Net.Sockets.SocketException] {
            $udpClient.Close()
            $sw.Stop()
            return @{ Success = $false; Status = "TIMEOUT"; Error = "No response from server"; LatencyMs = $sw.ElapsedMilliseconds }
        }
        $responseText = [System.Text.Encoding]::UTF8.GetString($response)
        $udpClient.Close()
        
        $sw.Stop()
        $latency = $sw.ElapsedMilliseconds
        
        $respObj = $responseText | ConvertFrom-Json
        
        return @{
            Success = $true
            Status = $respObj.status
            Address = $respObj.address
            LatencyMs = $latency
        }
    } catch {
        $sw.Stop()
        return @{
            Success = $false
            Status = "ERROR"
            Error = $_.Exception.Message
            LatencyMs = $sw.ElapsedMilliseconds
        }
    }
}

# Function to send TCP query to TDS
function Send-QueryTCP {
    param (
        [string]$Server,
        [string]$Task
    )
    
    $jsonObj = @{
        cmd = "QUERY"
        task = $Task
    }
    $message = ($jsonObj | ConvertTo-Json -Compress) + "`n"
    
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    
    try {
        $serverParts = $Server.Split(':')
        $tcpClient = New-Object System.Net.Sockets.TcpClient
        $tcpClient.SendTimeout = 1000
        $tcpClient.ReceiveTimeout = 1000
        $connectTask = $tcpClient.ConnectAsync($serverParts[0], [int]$serverParts[1])
        if (-not $connectTask.Wait(2000)) {
            $tcpClient.Close()
            $sw.Stop()
            return @{ Success = $false; Status = "TIMEOUT"; Error = "Connection timeout"; LatencyMs = $sw.ElapsedMilliseconds }
        }
        $stream = $tcpClient.GetStream()
        $stream.ReadTimeout = 1000
        $stream.WriteTimeout = 1000
        $writer = New-Object System.IO.StreamWriter($stream)
        $reader = New-Object System.IO.StreamReader($stream)
        
        $writer.WriteLine($message)
        $writer.Flush()
        
        $response = $reader.ReadLine()
        
        $writer.Close()
        $reader.Close()
        $stream.Close()
        $tcpClient.Close()
        
        $sw.Stop()
        $latency = $sw.ElapsedMilliseconds
        
        $respObj = $response | ConvertFrom-Json
        
        return @{
            Success = $true
            Status = $respObj.status
            Address = $respObj.address
            LatencyMs = $latency
        }
    } catch {
        $sw.Stop()
        return @{
            Success = $false
            Status = "ERROR"
            Error = $_.Exception.Message
            LatencyMs = $sw.ElapsedMilliseconds
        }
    }
}

# Function to send data to discovered service via UDP
function Send-DataUDP {
    param (
        [string]$ServiceAddress,
        [string]$Data,
        [string]$Task
    )
    
    $jsonObj = @{
        task = $Task
        data = $Data
        timestamp = (Get-Date).ToString("o")
        client_id = $env:COMPUTERNAME
    }
    $message = $jsonObj | ConvertTo-Json -Compress
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($message)
    
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    
    try {
        $udpClient = New-Object System.Net.Sockets.UdpClient
        $udpClient.Client.ReceiveTimeout = 500  # Reduced from 2000ms to 500ms
        $udpClient.Client.SendTimeout = 500
        $addrParts = $ServiceAddress.Split(':')
        $udpClient.Connect($addrParts[0], [int]$addrParts[1])
        $udpClient.Send($bytes, $bytes.Length) | Out-Null
        
        # Wait for acknowledgment
        $remoteEP = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
        $response = $udpClient.Receive([ref]$remoteEP)
        $responseText = [System.Text.Encoding]::UTF8.GetString($response)
        $udpClient.Close()
        
        $sw.Stop()
        
        $respObj = $responseText | ConvertFrom-Json
        
        return @{
            Success = ($respObj.status -eq "OK")
            Status = $respObj.status
            ServiceId = $respObj.service_id
            LatencyMs = $sw.ElapsedMilliseconds
        }
    } catch {
        $sw.Stop()
        return @{
            Success = $false
            Status = "ERROR"
            Error = $_.Exception.Message
            LatencyMs = $sw.ElapsedMilliseconds
        }
    }
}

# Function to send data to discovered service via TCP
function Send-DataTCP {
    param (
        [string]$ServiceAddress,
        [string]$Data,
        [string]$Task
    )
    
    $jsonObj = @{
        task = $Task
        data = $Data
        timestamp = (Get-Date).ToString("o")
        client_id = $env:COMPUTERNAME
    }
    $message = ($jsonObj | ConvertTo-Json -Compress) + "`n"
    
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    
    try {
        $addrParts = $ServiceAddress.Split(':')
        $tcpClient = New-Object System.Net.Sockets.TcpClient
        $tcpClient.Connect($addrParts[0], [int]$addrParts[1])
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
        
        $sw.Stop()
        
        $respObj = $response | ConvertFrom-Json
        
        return @{
            Success = ($respObj.status -eq "OK")
            Status = $respObj.status
            ServiceId = $respObj.service_id
            LatencyMs = $sw.ElapsedMilliseconds
        }
    } catch {
        $sw.Stop()
        return @{
            Success = $false
            Status = "ERROR"
            Error = $_.Exception.Message
            LatencyMs = $sw.ElapsedMilliseconds
        }
    }
}

# Query worker thread
$queryWorker = {
    param($ThreadId, $ServerAddr, $Protocol, $QueriesPerThread, $ThinkTime, $TaskNames, $PayloadData, $StopSignalFile)
    
    # Define query functions inside scriptblock for job access
    function Send-QueryUDP {
        param ([string]$Server, [string]$Task)
        $jsonObj = @{ cmd = "QUERY"; task = $Task }
        $message = $jsonObj | ConvertTo-Json -Compress
        $bytes = [System.Text.Encoding]::UTF8.GetBytes($message)
        $sw = [System.Diagnostics.Stopwatch]::StartNew()
        try {
            $udpClient = New-Object System.Net.Sockets.UdpClient
            $udpClient.Client.ReceiveTimeout = 1000  # Reduced to 1 second
            $udpClient.Client.SendTimeout = 1000
            $serverParts = $Server.Split(':')
            $udpClient.Connect($serverParts[0], [int]$serverParts[1])
            $sent = $udpClient.Send($bytes, $bytes.Length)
            if ($sent -ne $bytes.Length) {
                throw "Failed to send complete message"
            }
            $remoteEP = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
            try {
                $response = $udpClient.Receive([ref]$remoteEP)
            } catch [System.Net.Sockets.SocketException] {
                # Timeout - treat as failure
                $udpClient.Close()
                $sw.Stop()
                return @{ Success = $false; Status = "TIMEOUT"; Error = "No response from server"; LatencyMs = $sw.ElapsedMilliseconds }
            }
            $responseText = [System.Text.Encoding]::UTF8.GetString($response)
            $udpClient.Close()
            $sw.Stop()
            $respObj = $responseText | ConvertFrom-Json
            return @{ Success = $true; Status = $respObj.status; Address = $respObj.address; LatencyMs = $sw.ElapsedMilliseconds }
        } catch {
            $sw.Stop()
            return @{ Success = $false; Status = "ERROR"; Error = $_.Exception.Message; LatencyMs = $sw.ElapsedMilliseconds }
        }
    }
    
    function Send-QueryTCP {
        param ([string]$Server, [string]$Task)
        $jsonObj = @{ cmd = "QUERY"; task = $Task }
        $message = ($jsonObj | ConvertTo-Json -Compress) + "`n"
        $sw = [System.Diagnostics.Stopwatch]::StartNew()
        try {
            $serverParts = $Server.Split(':')
            $tcpClient = New-Object System.Net.Sockets.TcpClient
            $tcpClient.SendTimeout = 1000
            $tcpClient.ReceiveTimeout = 1000
            $connectTask = $tcpClient.ConnectAsync($serverParts[0], [int]$serverParts[1])
            if (-not $connectTask.Wait(2000)) {
                $tcpClient.Close()
                $sw.Stop()
                return @{ Success = $false; Status = "TIMEOUT"; Error = "Connection timeout"; LatencyMs = $sw.ElapsedMilliseconds }
            }
            $stream = $tcpClient.GetStream()
            $stream.ReadTimeout = 1000
            $stream.WriteTimeout = 1000
            $writer = New-Object System.IO.StreamWriter($stream)
            $reader = New-Object System.IO.StreamReader($stream)
            $writer.WriteLine($message)
            $writer.Flush()
            $response = $reader.ReadLine()
            $writer.Close(); $reader.Close(); $stream.Close(); $tcpClient.Close()
            $sw.Stop()
            $respObj = $response | ConvertFrom-Json
            return @{ Success = $true; Status = $respObj.status; Address = $respObj.address; LatencyMs = $sw.ElapsedMilliseconds }
        } catch {
            $sw.Stop()
            return @{ Success = $false; Status = "ERROR"; Error = $_.Exception.Message; LatencyMs = $sw.ElapsedMilliseconds }
        }
    }
    
    function Send-DataUDP {
        param ([string]$ServiceAddress, [string]$Data, [string]$Task)
        $jsonObj = @{ task = $Task; data = $Data; timestamp = (Get-Date).ToString("o"); client_id = $env:COMPUTERNAME }
        $message = $jsonObj | ConvertTo-Json -Compress
        $bytes = [System.Text.Encoding]::UTF8.GetBytes($message)
        $sw = [System.Diagnostics.Stopwatch]::StartNew()
        try {
            $udpClient = New-Object System.Net.Sockets.UdpClient
            $udpClient.Client.ReceiveTimeout = 500  # Reduced from 2000ms to 500ms
            $udpClient.Client.SendTimeout = 500
            $addrParts = $ServiceAddress.Split(':')
            $udpClient.Connect($addrParts[0], [int]$addrParts[1])
            $sent = $udpClient.Send($bytes, $bytes.Length)
            if ($sent -ne $bytes.Length) {
                throw "Failed to send complete message"
            }
            $remoteEP = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
            try {
                $response = $udpClient.Receive([ref]$remoteEP)
            } catch [System.Net.Sockets.SocketException] {
                $udpClient.Close()
                $sw.Stop()
                return @{ Success = $false; Status = "TIMEOUT"; Error = "No response"; LatencyMs = $sw.ElapsedMilliseconds }
            }
            $responseText = [System.Text.Encoding]::UTF8.GetString($response)
            $udpClient.Close()
            $sw.Stop()
            $respObj = $responseText | ConvertFrom-Json
            return @{ Success = ($respObj.status -eq "OK"); Status = $respObj.status; LatencyMs = $sw.ElapsedMilliseconds }
        } catch {
            $sw.Stop()
            return @{ Success = $false; Status = "ERROR"; Error = $_.Exception.Message; LatencyMs = $sw.ElapsedMilliseconds }
        }
    }
    
    function Send-DataTCP {
        param ([string]$ServiceAddress, [string]$Data, [string]$Task)
        $jsonObj = @{ task = $Task; data = $Data; timestamp = (Get-Date).ToString("o"); client_id = $env:COMPUTERNAME }
        $message = ($jsonObj | ConvertTo-Json -Compress) + "`n"
        $sw = [System.Diagnostics.Stopwatch]::StartNew()
        try {
            $addrParts = $ServiceAddress.Split(':')
            $tcpClient = New-Object System.Net.Sockets.TcpClient
            $tcpClient.Connect($addrParts[0], [int]$addrParts[1])
            $stream = $tcpClient.GetStream()
            $writer = New-Object System.IO.StreamWriter($stream)
            $reader = New-Object System.IO.StreamReader($stream)
            $writer.WriteLine($message)
            $writer.Flush()
            $response = $reader.ReadLine()
            $writer.Close(); $reader.Close(); $stream.Close(); $tcpClient.Close()
            $sw.Stop()
            $respObj = $response | ConvertFrom-Json
            return @{ Success = ($respObj.status -eq "OK"); Status = $respObj.status; LatencyMs = $sw.ElapsedMilliseconds }
        } catch {
            $sw.Stop()
            return @{ Success = $false; Status = "ERROR"; Error = $_.Exception.Message; LatencyMs = $sw.ElapsedMilliseconds }
        }
    }
    
    $localQuerySuccess = 0
    $localQueryFailed = 0
    $localQueryNotFound = 0
    $localDataSuccess = 0
    $localDataFailed = 0
    $queryCount = 0
    
    $infinite = ($QueriesPerThread -eq 0)
    
    while ($infinite -or $queryCount -lt $QueriesPerThread) {
        # Check for stop signal
        if (Test-Path $StopSignalFile) {
            break
        }
        
        # Round-robin through tasks
        $task = $TaskNames[$queryCount % $TaskNames.Count]
        
        # Step 1: Query TDS for service
        if ($Protocol -eq "tcp" -or $Protocol -eq "tls") {
            $queryResult = Send-QueryTCP -Server $ServerAddr -Task $task
        } else {
            $queryResult = Send-QueryUDP -Server $ServerAddr -Task $task
        }
        
        # Update query statistics
        if ($queryResult.Success) {
            if ($queryResult.Status -eq "OK") {
                $localQuerySuccess++
                
                # Step 2: Send data to discovered service
                $serviceAddr = $queryResult.Address
                if ($Protocol -eq "tcp" -or $Protocol -eq "tls") {
                    $dataResult = Send-DataTCP -ServiceAddress $serviceAddr -Data $PayloadData -Task $task
                } else {
                    $dataResult = Send-DataUDP -ServiceAddress $serviceAddr -Data $PayloadData -Task $task
                }
                
                # Update data send statistics
                if ($dataResult.Success) {
                    $localDataSuccess++
                } else {
                    $localDataFailed++
                }
                
            } elseif ($queryResult.Status -eq "NOTFOUND") {
                $localQueryNotFound++
            } else {
                $localQueryFailed++
            }
        } else {
            $localQueryFailed++
        }
        
        $queryCount++
        
        # Think time
        if ($ThinkTime -gt 0) {
            Start-Sleep -Milliseconds $ThinkTime
        }
    }
    
    return @{
        ThreadId = $ThreadId
        QuerySuccess = $localQuerySuccess
        QueryFailed = $localQueryFailed
        QueryNotFound = $localQueryNotFound
        DataSuccess = $localDataSuccess
        DataFailed = $localDataFailed
        Total = $queryCount
    }
}

# Start worker threads
Write-Host "[START] Starting $NumThreads client threads..." -ForegroundColor Yellow
$jobs = @()

for ($i = 0; $i -lt $NumThreads; $i++) {
    $job = Start-Job -ScriptBlock $queryWorker -ArgumentList $i, $ServerAddr, $Protocol, $QueriesPerThread, $ThinkTime, $taskNames, $payloadData, $stopSignalFile
    $jobs += $job
    Write-Host "  [OK] Thread $i started (Job ID: $($job.Id))" -ForegroundColor Green
}

Write-Host ""
Write-Host "[RUNNING] Client query & data transfer in progress (Ctrl+C to stop)..." -ForegroundColor Yellow
Write-Host ""

# Stats monitoring loop
$startTime = Get-Date

try {
    while ($true) {
        Start-Sleep -Seconds $StatsInterval
        
        # Check for stop signal file
        if (Test-Path $stopSignalFile) {
            Write-Host ""
            Write-Host "[SIGNAL] Stop signal received, shutting down gracefully..." -ForegroundColor Yellow
            break
        }
        
        # Check if all jobs completed
        $runningJobs = $jobs | Where-Object { $_.State -eq "Running" }
        $completedJobs = $jobs | Where-Object { $_.State -eq "Completed" }
        
        $timestamp = Get-Date -Format "HH:mm:ss"
        Write-Host "[$timestamp] Running: $($runningJobs.Count) threads | Completed: $($completedJobs.Count) threads" -ForegroundColor Cyan
        
        if ($runningJobs.Count -eq 0 -and $QueriesPerThread -ne 0) {
            Write-Host ""
            Write-Host "[COMPLETE] All threads finished!" -ForegroundColor Green
            break
        }
    }
} catch {
    Write-Host ""
    Write-Host "[STOPPED] Stopping client query & data transfer..." -ForegroundColor Yellow
}

# Wait for jobs to complete
Write-Host ""
Write-Host "[CLEANUP] Waiting for threads to complete..." -ForegroundColor Yellow
$jobs | Wait-Job -Timeout 30 | Out-Null

# Collect results
$threadResults = @()
$totalQuerySuccess = 0
$totalQueryFailed = 0
$totalQueryNotFound = 0
$totalDataSuccess = 0
$totalDataFailed = 0
$totalQueries = 0

foreach ($job in $jobs) {
    if ($job.State -eq "Completed") {
        $result = Receive-Job -Job $job
        if ($result) {
            $threadResults += $result
            $totalQuerySuccess += $result.QuerySuccess
            $totalQueryFailed += $result.QueryFailed
            $totalQueryNotFound += $result.QueryNotFound
            $totalDataSuccess += $result.DataSuccess
            $totalDataFailed += $result.DataFailed
            $totalQueries += $result.Total
            Write-Host "  [OK] Thread $($result.ThreadId): $($result.Total) queries | Query OK: $($result.QuerySuccess) | Data OK: $($result.DataSuccess)" -ForegroundColor Green
        }
    } else {
        Write-Host "  [WARN] Thread (Job $($job.Id)): Did not complete cleanly" -ForegroundColor Yellow
    }
    Remove-Job -Job $job -Force
}

# Final statistics
$endTime = Get-Date
$totalElapsed = ($endTime - $startTime).TotalSeconds

Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Final Statistics:" -ForegroundColor Yellow
Write-Host "  Duration: ${totalElapsed}s" -ForegroundColor White
Write-Host ""
Write-Host "  Query Statistics:" -ForegroundColor Yellow
Write-Host "    Total Queries: $totalQueries" -ForegroundColor White
Write-Host "    Successful: $totalQuerySuccess" -ForegroundColor White
Write-Host "    Failed: $totalQueryFailed" -ForegroundColor White
Write-Host "    Not Found: $totalQueryNotFound" -ForegroundColor White
if ($totalQueries -gt 0) {
    Write-Host "    Success Rate: $([math]::Round(($totalQuerySuccess / $totalQueries) * 100, 2))%" -ForegroundColor White
    Write-Host "    Overall QPS: $([math]::Round($totalQueries / $totalElapsed, 2))" -ForegroundColor White
}
Write-Host ""
Write-Host "  Data Transfer Statistics:" -ForegroundColor Yellow
Write-Host "    Total Attempts: $totalQuerySuccess" -ForegroundColor White
Write-Host "    Successful: $totalDataSuccess" -ForegroundColor White
Write-Host "    Failed: $totalDataFailed" -ForegroundColor White
if ($totalQuerySuccess -gt 0) {
    Write-Host "    Success Rate: $([math]::Round(($totalDataSuccess / $totalQuerySuccess) * 100, 2))%" -ForegroundColor White
    Write-Host "    Total Data Sent: $([math]::Round(($totalDataSuccess * $DataSize) / 1024, 2)) KB" -ForegroundColor White
}
Write-Host "========================================" -ForegroundColor Cyan

$timestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
"[$timestamp] ===== Client Query & Data Sender Stopped =====" | Out-File -FilePath $LogFile -Append
"[$timestamp] Total Queries: $totalQueries" | Out-File -FilePath $LogFile -Append
"[$timestamp] Successful: $totalQuerySuccess" | Out-File -FilePath $LogFile -Append
"[$timestamp] Failed: $totalQueryFailed" | Out-File -FilePath $LogFile -Append
"[$timestamp] Not Found: $totalQueryNotFound" | Out-File -FilePath $LogFile -Append
if ($totalQueries -gt 0) {
    $qps = [math]::Round($totalQueries / $totalElapsed, 2)
    $successRate = [math]::Round(($totalQuerySuccess / $totalQueries) * 100, 2)
    "[$timestamp] Success Rate: ${successRate}%" | Out-File -FilePath $LogFile -Append
    "[$timestamp] Overall QPS: $qps" | Out-File -FilePath $LogFile -Append
}
"[$timestamp] Data Attempts: $totalQuerySuccess" | Out-File -FilePath $LogFile -Append
"[$timestamp] Data Successful: $totalDataSuccess" | Out-File -FilePath $LogFile -Append
"[$timestamp] Data Failed: $totalDataFailed" | Out-File -FilePath $LogFile -Append
if ($totalQuerySuccess -gt 0) {
    $dataBytes = $totalDataSuccess * $DataSize
    "[$timestamp] Data Processed: $dataBytes bytes" | Out-File -FilePath $LogFile -Append
}

# Clean up stop signal file
if (Test-Path $stopSignalFile) {
    Remove-Item $stopSignalFile -Force
}
