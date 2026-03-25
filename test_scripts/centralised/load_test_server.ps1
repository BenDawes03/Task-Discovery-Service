# Load Test for TDS Server
# Tests both registration and query performance under load

param(
    [int]$NumClients = 10,
    [int]$RegistrationsPerClient = 100,
    [int]$QueriesPerClient = 100,
    [string]$Protocol = "udp",
    [string]$ServerAddr = "127.0.0.1:5000"
)

# Navigate to project root
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$projectRoot = Split-Path -Parent (Split-Path -Parent $scriptDir)
Set-Location $projectRoot

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "TDS Server Load Test" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Configuration:" -ForegroundColor Yellow
Write-Host "  Server: $ServerAddr" -ForegroundColor White
Write-Host "  Protocol: $Protocol" -ForegroundColor White
Write-Host "  Clients: $NumClients" -ForegroundColor White
Write-Host "  Registrations per client: $RegistrationsPerClient" -ForegroundColor White
Write-Host "  Queries per client: $QueriesPerClient" -ForegroundColor White
Write-Host ""

# Build the client demo executable
Write-Host "[BUILD] Building client executable..." -ForegroundColor Yellow
go build -o bin\client_demo.exe ./demos/client
if ($LASTEXITCODE -ne 0) {
    Write-Host "[ERROR] Build failed!" -ForegroundColor Red
    exit 1
}
Write-Host "[BUILD] Build successful!" -ForegroundColor Green
Write-Host ""

# Function to send registration via UDP (JSON protocol)
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
        $udpClient.Client.ReceiveTimeout = 2000
        $serverParts = $Server.Split(':')
        $udpClient.Connect($serverParts[0], [int]$serverParts[1])
        $udpClient.Send($bytes, $bytes.Length) | Out-Null
        
        # Read JSON response
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

# Function to send query via UDP (JSON protocol)
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
    
    try {
        $udpClient = New-Object System.Net.Sockets.UdpClient
        $udpClient.Client.ReceiveTimeout = 2000
        $serverParts = $Server.Split(':')
        $udpClient.Connect($serverParts[0], [int]$serverParts[1])
        $udpClient.Send($bytes, $bytes.Length) | Out-Null
        
        $remoteEP = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
        $response = $udpClient.Receive([ref]$remoteEP)
        $responseText = [System.Text.Encoding]::UTF8.GetString($response)
        $udpClient.Close()
        
        $respObj = $responseText | ConvertFrom-Json
        if ($respObj.status -eq "NOTFOUND") {
            return @{Success=$true; Response="NOTFOUND"}
        } elseif ($respObj.status -eq "OK") {
            return @{Success=$true; Response=$respObj.address}
        } else {
            return @{Success=$false; Response=""}
        }
    } catch {
        return @{Success=$false; Response=""}
    }
}

# Function to send registration via TCP (JSON protocol)
function Send-RegisterTCP {
    param (
        [string]$Server,
        [string]$Task,
        [string]$Address
    )
    
    try {
        $serverParts = $Server.Split(':')
        $tcpClient = New-Object System.Net.Sockets.TcpClient
        $tcpClient.Connect($serverParts[0], [int]$serverParts[1])
        $stream = $tcpClient.GetStream()
        $writer = New-Object System.IO.StreamWriter($stream)
        $reader = New-Object System.IO.StreamReader($stream)
        
        $jsonObj = @{
            cmd = "REGISTER"
            task = $Task
            address = $Address
        }
        $message = $jsonObj | ConvertTo-Json -Compress
        $writer.WriteLine($message)
        $writer.Flush()
        
        $response = $reader.ReadLine()
        $respObj = $response | ConvertFrom-Json
        
        $writer.Close()
        $reader.Close()
        $stream.Close()
        $tcpClient.Close()
        
        return ($respObj.status -eq "OK")
    } catch {
        return $false
    }
}

# Function to send query via TCP (JSON protocol)
function Send-QueryTCP {
    param (
        [string]$Server,
        [string]$Task
    )
    
    try {
        $serverParts = $Server.Split(':')
        $tcpClient = New-Object System.Net.Sockets.TcpClient
        $tcpClient.Connect($serverParts[0], [int]$serverParts[1])
        $stream = $tcpClient.GetStream()
        $writer = New-Object System.IO.StreamWriter($stream)
        $reader = New-Object System.IO.StreamReader($stream)
        
        $jsonObj = @{
            cmd = "QUERY"
            task = $Task
        }
        $message = $jsonObj | ConvertTo-Json -Compress
        $writer.WriteLine($message)
        $writer.Flush()
        
        $response = $reader.ReadLine()
        $respObj = $response | ConvertFrom-Json
        
        $writer.Close()
        $reader.Close()
        $stream.Close()
        $tcpClient.Close()
        
        if ($respObj.status -eq "NOTFOUND") {
            return @{Success=$true; Response="NOTFOUND"}
        } elseif ($respObj.status -eq "OK") {
            return @{Success=$true; Response=$respObj.address}
        } else {
            return @{Success=$false; Response=""}
        }
    } catch {
        return @{Success=$false; Response=""}
    }
}

# Test scriptblock for registration
$registerScript = {
    param($ClientId, $ServerAddr, $Protocol, $NumRegistrations)
    
    function Send-RegisterUDP {
        param([string]$Server, [string]$Task, [string]$Address)
        $jsonObj = @{cmd="REGISTER"; task=$Task; address=$Address}
        $message = $jsonObj | ConvertTo-Json -Compress
        $bytes = [System.Text.Encoding]::UTF8.GetBytes($message)
        try {
            $udpClient = New-Object System.Net.Sockets.UdpClient
            $udpClient.Client.ReceiveTimeout = 2000
            $serverParts = $Server.Split(':')
            $udpClient.Connect($serverParts[0], [int]$serverParts[1])
            $udpClient.Send($bytes, $bytes.Length) | Out-Null
            
            # Wait for OK response
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
    
    function Send-RegisterTCP {
        param([string]$Server, [string]$Task, [string]$Address)
        try {
            $serverParts = $Server.Split(':')
            $tcpClient = New-Object System.Net.Sockets.TcpClient
            $tcpClient.Connect($serverParts[0], [int]$serverParts[1])
            $stream = $tcpClient.GetStream()
            $writer = New-Object System.IO.StreamWriter($stream)
            $reader = New-Object System.IO.StreamReader($stream)
            $jsonObj = @{cmd="REGISTER"; task=$Task; address=$Address}
            $message = $jsonObj | ConvertTo-Json -Compress
            $writer.WriteLine($message)
            $writer.Flush()
            $response = $reader.ReadLine()
            $respObj = $response | ConvertFrom-Json
            $writer.Close()
            $reader.Close()
            $stream.Close()
            $tcpClient.Close()
            return ($respObj.status -eq "OK")
        } catch {
            return $false
        }
    }
    
    $successCount = 0
    $failCount = 0
    $taskPrefix = "task-client$ClientId"
    
    $startTime = Get-Date
    
    for ($i = 0; $i -lt $NumRegistrations; $i++) {
        $task = "$taskPrefix-$i"
        $addr = "127.0.0.1:$((8000 + $ClientId * 1000 + $i))"
        
        if ($Protocol -eq "udp") {
            $result = Send-RegisterUDP -Server $ServerAddr -Task $task -Address $addr
        } else {
            $result = Send-RegisterTCP -Server $ServerAddr -Task $task -Address $addr
        }
        
        if ($result) {
            $successCount++
        } else {
            $failCount++
        }
    }
    
    $endTime = Get-Date
    $duration = ($endTime - $startTime).TotalSeconds
    
    # Return as a PSCustomObject for better serialization
    [PSCustomObject]@{
        ClientId = $ClientId
        Type = "Register"
        Success = $successCount
        Failed = $failCount
        Duration = $duration
        ThroughputPerSec = [math]::Round($successCount / $duration, 2)
    }
}

# Test scriptblock for queries
$queryScript = {
    param($ClientId, $ServerAddr, $Protocol, $NumQueries)
    
    function Send-QueryUDP {
        param([string]$Server, [string]$Task)
        $jsonObj = @{cmd="QUERY"; task=$Task}
        $message = $jsonObj | ConvertTo-Json -Compress
        $bytes = [System.Text.Encoding]::UTF8.GetBytes($message)
        try {
            $udpClient = New-Object System.Net.Sockets.UdpClient
            $udpClient.Client.ReceiveTimeout = 2000
            $serverParts = $Server.Split(':')
            $udpClient.Connect($serverParts[0], [int]$serverParts[1])
            $udpClient.Send($bytes, $bytes.Length) | Out-Null
            $remoteEP = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
            $response = $udpClient.Receive([ref]$remoteEP)
            $responseText = [System.Text.Encoding]::UTF8.GetString($response)
            $udpClient.Close()
            $respObj = $responseText | ConvertFrom-Json
            if ($respObj.status -eq "NOTFOUND") {
                return @{Success=$true; Response="NOTFOUND"}
            } elseif ($respObj.status -eq "OK") {
                return @{Success=$true; Response=$respObj.address}
            } else {
                return @{Success=$false; Response=""; Error="Unknown status"}
            }
        } catch {
            return @{Success=$false; Response=""; Error=$_.Exception.Message}
        }
    }
    
    function Send-QueryTCP {
        param([string]$Server, [string]$Task)
        try {
            $serverParts = $Server.Split(':')
            $tcpClient = New-Object System.Net.Sockets.TcpClient
            $tcpClient.Connect($serverParts[0], [int]$serverParts[1])
            $stream = $tcpClient.GetStream()
            $writer = New-Object System.IO.StreamWriter($stream)
            $reader = New-Object System.IO.StreamReader($stream)
            $jsonObj = @{cmd="QUERY"; task=$Task}
            $message = $jsonObj | ConvertTo-Json -Compress
            $writer.WriteLine($message)
            $writer.Flush()
            $response = $reader.ReadLine()
            $respObj = $response | ConvertFrom-Json
            $writer.Close()
            $reader.Close()
            $stream.Close()
            $tcpClient.Close()
            if ($respObj.status -eq "NOTFOUND") {
                return @{Success=$true; Response="NOTFOUND"}
            } elseif ($respObj.status -eq "OK") {
                return @{Success=$true; Response=$respObj.address}
            } else {
                return @{Success=$false; Response=""}
            }
        } catch {
            return @{Success=$false; Response=""}
        }
    }
    
    $successCount = 0
    $failCount = 0
    $notFoundCount = 0
    $taskPrefix = "task-client$ClientId"
    
    $startTime = Get-Date
    $firstError = $null
    
    for ($i = 0; $i -lt $NumQueries; $i++) {
        # Query both own tasks and other clients' tasks for realistic load
        $targetClient = $i % 10  # Rotate through clients
        $task = "task-client$targetClient-0"
        
        if ($Protocol -eq "udp") {
            $result = Send-QueryUDP -Server $ServerAddr -Task $task
        } else {
            $result = Send-QueryTCP -Server $ServerAddr -Task $task
        }
        
        if ($result.Success) {
            if ($result.Response -and $result.Response -ne "NOTFOUND") {
                $successCount++
            } else {
                $notFoundCount++
            }
        } else {
            $failCount++
            if (-not $firstError -and $result.Error) {
                $firstError = $result.Error
            }
        }
    }
    
    $endTime = Get-Date
    $duration = ($endTime - $startTime).TotalSeconds
    
    # Return as a PSCustomObject for better serialization
    [PSCustomObject]@{
        ClientId = $ClientId
        Type = "Query"
        Success = $successCount
        NotFound = $notFoundCount
        Failed = $failCount
        Duration = $duration
        ThroughputPerSec = [math]::Round(($successCount + $notFoundCount) / $duration, 2)
        FirstError = $firstError
    }
}

# Verify server is reachable
Write-Host "[CHECK] Verifying server is reachable..." -ForegroundColor Yellow
if ($Protocol -eq "udp") {
    $testResult = Send-RegisterUDP -Server $ServerAddr -Task "test-connectivity" -Address "127.0.0.1:9999"
} else {
    $testResult = Send-RegisterTCP -Server $ServerAddr -Task "test-connectivity" -Address "127.0.0.1:9999"
}

if (-not $testResult) {
    Write-Host "[ERROR] Cannot reach server at $ServerAddr using $Protocol" -ForegroundColor Red
    Write-Host "Make sure the server is running: go run ./cmd/server" -ForegroundColor Yellow
    exit 1
}
Write-Host "[CHECK] Server is reachable!" -ForegroundColor Green
Write-Host ""

# Phase 1: Registration Load Test
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Phase 1: Registration Load Test" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan

$jobs = @()

Write-Host "Starting $NumClients concurrent clients..." -ForegroundColor Yellow
$phase1Start = Get-Date

for ($i = 0; $i -lt $NumClients; $i++) {
    $job = Start-Job -ScriptBlock $registerScript -ArgumentList $i, $ServerAddr, $Protocol, $RegistrationsPerClient
    $jobs += $job
}

# Wait for all jobs to complete
Write-Host "Waiting for registrations to complete..." -ForegroundColor Yellow
$jobs | Wait-Job | Out-Null

# Collect results
$registerResults = @()
foreach ($job in $jobs) {
    $result = Receive-Job -Job $job
    if ($result) {
        $registerResults += $result
    }
    Remove-Job -Job $job
}

$phase1End = Get-Date
$phase1Duration = ($phase1End - $phase1Start).TotalSeconds

# Display registration results
Write-Host ""
Write-Host "Registration Results:" -ForegroundColor Green
Write-Host "-------------------------------------" -ForegroundColor Gray

if ($registerResults.Count -eq 0) {
    Write-Host "  ERROR: No results received from jobs!" -ForegroundColor Red
    Write-Host "  This may indicate a problem with the script blocks." -ForegroundColor Yellow
    $totalRegSuccess = 0
    $totalRegFailed = 0
    $avgThroughput = 0
} else {
    $totalRegSuccess = ($registerResults | Measure-Object -Property Success -Sum).Sum
    $totalRegFailed = ($registerResults | Measure-Object -Property Failed -Sum).Sum
    $avgThroughput = ($registerResults | Measure-Object -Property ThroughputPerSec -Average).Average
}

Write-Host "  Total Successful: $totalRegSuccess" -ForegroundColor White
Write-Host "  Total Failed: $totalRegFailed" -ForegroundColor White
Write-Host "  Total Duration: $([math]::Round($phase1Duration, 2))s" -ForegroundColor White
Write-Host "  Overall Throughput: $([math]::Round($totalRegSuccess / $phase1Duration, 2)) registrations/sec" -ForegroundColor White
Write-Host "  Avg Client Throughput: $([math]::Round($avgThroughput, 2)) registrations/sec" -ForegroundColor White
Write-Host ""

# Brief pause to let server settle
Start-Sleep -Seconds 2

# Phase 2: Query Load Test
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Phase 2: Query Load Test" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan

$jobs = @()

Write-Host "Starting $NumClients concurrent clients..." -ForegroundColor Yellow
$phase2Start = Get-Date

for ($i = 0; $i -lt $NumClients; $i++) {
    $job = Start-Job -ScriptBlock $queryScript -ArgumentList $i, $ServerAddr, $Protocol, $QueriesPerClient
    $jobs += $job
}

# Wait for all jobs to complete
Write-Host "Waiting for queries to complete..." -ForegroundColor Yellow
$jobs | Wait-Job | Out-Null

# Collect results
$queryResults = @()
foreach ($job in $jobs) {
    $result = Receive-Job -Job $job
    if ($result) {
        $queryResults += $result
    }
    Remove-Job -Job $job
}

$phase2End = Get-Date
$phase2Duration = ($phase2End - $phase2Start).TotalSeconds

# Display query results
Write-Host ""
Write-Host "Query Results:" -ForegroundColor Green
Write-Host "-------------------------------------" -ForegroundColor Gray

if ($queryResults.Count -eq 0) {
    Write-Host "  ERROR: No results received from jobs!" -ForegroundColor Red
    $totalQuerySuccess = 0
    $totalQueryNotFound = 0
    $totalQueryFailed = 0
    $avgQueryThroughput = 0
} else {
    $totalQuerySuccess = ($queryResults | Measure-Object -Property Success -Sum).Sum
    $totalQueryNotFound = ($queryResults | Measure-Object -Property NotFound -Sum).Sum
    $totalQueryFailed = ($queryResults | Measure-Object -Property Failed -Sum).Sum
    $avgQueryThroughput = ($queryResults | Measure-Object -Property ThroughputPerSec -Average).Average
    
    # Show first error if queries failed
    if ($totalQueryFailed -gt 0) {
        $firstErrorResult = $queryResults | Where-Object { $_.FirstError } | Select-Object -First 1
        if ($firstErrorResult -and $firstErrorResult.FirstError) {
            Write-Host "  First Error: $($firstErrorResult.FirstError)" -ForegroundColor Red
        }
    }
}
$totalQueryFailed = ($queryResults | Measure-Object -Property Failed -Sum).Sum
$avgQueryThroughput = ($queryResults | Measure-Object -Property ThroughputPerSec -Average).Average

Write-Host "  Total Successful: $totalQuerySuccess" -ForegroundColor White
Write-Host "  Total Not Found: $totalQueryNotFound" -ForegroundColor White
Write-Host "  Total Failed: $totalQueryFailed" -ForegroundColor White
Write-Host "  Total Duration: $([math]::Round($phase2Duration, 2))s" -ForegroundColor White
Write-Host "  Overall Throughput: $([math]::Round(($totalQuerySuccess + $totalQueryNotFound) / $phase2Duration, 2)) queries/sec" -ForegroundColor White
Write-Host "  Avg Client Throughput: $([math]::Round($avgQueryThroughput, 2)) queries/sec" -ForegroundColor White
Write-Host ""

# Summary
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Load Test Summary" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Total Operations: $(($totalRegSuccess + $totalRegFailed + $totalQuerySuccess + $totalQueryNotFound + $totalQueryFailed))" -ForegroundColor White
Write-Host "Total Duration: $([math]::Round($phase1Duration + $phase2Duration, 2))s" -ForegroundColor White
Write-Host "Registration Success Rate: $([math]::Round(($totalRegSuccess / ($totalRegSuccess + $totalRegFailed)) * 100, 2))%" -ForegroundColor White
Write-Host "Query Success Rate: $([math]::Round((($totalQuerySuccess + $totalQueryNotFound) / ($totalQuerySuccess + $totalQueryNotFound + $totalQueryFailed)) * 100, 2))%" -ForegroundColor White
Write-Host ""
Write-Host "[DONE] Load test complete!" -ForegroundColor Green
