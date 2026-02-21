param(
    [int]$ProxyPort = 5100,
    [int]$NumThreads = 20,
    [int]$QueriesPerThread = 20000,
    [int]$NumTasks = 500,
    [int]$ThinkTimeMs = 0,
    [int]$StatsInterval = 5,
    [string]$LogFile = "p2p_query_load.log",
    [string]$DoneFile = ""
)

$ErrorActionPreference = "Continue"

function Send-Query {
    param(
        [int]$Port,
        [string]$Task
    )

    $message = "QUERY $Task"
    $bytes = [System.Text.Encoding]::ASCII.GetBytes($message)

    $sw = [System.Diagnostics.Stopwatch]::StartNew()

    try {
        $udpClient = New-Object System.Net.Sockets.UdpClient
        $udpClient.Client.ReceiveTimeout = 2000
        $udpClient.Client.SendTimeout = 2000
        $udpClient.Connect("127.0.0.1", $Port)
        $udpClient.Send($bytes, $bytes.Length) | Out-Null

        $remoteEP = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
        $response = $udpClient.Receive([ref]$remoteEP)
        $responseText = [System.Text.Encoding]::ASCII.GetString($response)
        $udpClient.Close()

        $sw.Stop()

        return @{
            Success = $true
            Response = $responseText
            LatencyMs = $sw.ElapsedMilliseconds
        }
    } catch {
        try { if ($udpClient) { $udpClient.Close() } } catch { }
        $sw.Stop()
        return @{
            Success = $false
            Response = "ERROR"
            LatencyMs = $sw.ElapsedMilliseconds
        }
    }
}

$timestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
"[$timestamp] ===== P2P query load start =====" | Out-File -FilePath $LogFile -Append
"[$timestamp] ProxyPort=$ProxyPort Threads=$NumThreads QueriesPerThread=$QueriesPerThread NumTasks=$NumTasks" | Out-File -FilePath $LogFile -Append

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "P2P QUERY Load" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Proxy: 127.0.0.1:$ProxyPort | Threads: $NumThreads | Queries/Thread: $QueriesPerThread | Tasks: $NumTasks" -ForegroundColor Yellow
Write-Host ""

$taskNames = 1..$NumTasks | ForEach-Object { "task_$_" }

$queryWorker = {
    param($ThreadId, $Port, $QueriesPerThread, $ThinkTimeMs, $taskNames)

    function Send-Query-Inner {
        param([int]$Port, [string]$Task)

        $message = "QUERY $Task"
        $bytes = [System.Text.Encoding]::ASCII.GetBytes($message)

        try {
            $udpClient = New-Object System.Net.Sockets.UdpClient
            $udpClient.Client.ReceiveTimeout = 2000
            $udpClient.Client.SendTimeout = 2000
            $udpClient.Connect("127.0.0.1", $Port)
            $udpClient.Send($bytes, $bytes.Length) | Out-Null

            $remoteEP = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
            $response = $udpClient.Receive([ref]$remoteEP)
            $responseText = [System.Text.Encoding]::ASCII.GetString($response)
            $udpClient.Close()

            return $responseText
        } catch {
            try { if ($udpClient) { $udpClient.Close() } } catch { }
            return "ERROR"
        }
    }

    $ok = 0
    $notFound = 0
    $fail = 0
    $total = 0

    for ($i = 0; $i -lt $QueriesPerThread; $i++) {
        $task = $taskNames[($ThreadId + $i) % $taskNames.Count]
        $resp = Send-Query-Inner -Port $Port -Task $task

        if ($resp -eq "NOTFOUND") {
            $notFound++
        } elseif ($resp -eq "ERROR" -or $resp -like "ERR*") {
            $fail++
        } else {
            $ok++
        }

        $total++
        if ($ThinkTimeMs -gt 0) { Start-Sleep -Milliseconds $ThinkTimeMs }
    }

    return @{ ThreadId=$ThreadId; OK=$ok; NotFound=$notFound; Fail=$fail; Total=$total }
}

Write-Host "[START] Starting $NumThreads query workers..." -ForegroundColor Yellow
$jobs = @()
for ($i = 0; $i -lt $NumThreads; $i++) {
    $jobs += Start-Job -ScriptBlock $queryWorker -ArgumentList $i, $ProxyPort, $QueriesPerThread, $ThinkTimeMs, $taskNames
}

$startTime = Get-Date

try {
    while ($true) {
        Start-Sleep -Seconds $StatsInterval
        $running = $jobs | Where-Object { $_.State -eq "Running" }
        $completed = $jobs | Where-Object { $_.State -eq "Completed" }
        $ts = Get-Date -Format "HH:mm:ss"
        Write-Host "[$ts] Running: $($running.Count) | Completed: $($completed.Count)" -ForegroundColor Cyan
        if ($running.Count -eq 0) { break }
    }
} catch {
    Write-Host "[STOP] Interrupted" -ForegroundColor Yellow
}

$jobs | Wait-Job -Timeout 30 | Out-Null

$totalOK = 0
$totalNotFound = 0
$totalFail = 0
$totalQueries = 0

foreach ($job in $jobs) {
    if ($job.State -eq "Completed") {
        $result = Receive-Job -Job $job
        if ($result) {
            $totalOK += $result.OK
            $totalNotFound += $result.NotFound
            $totalFail += $result.Fail
            $totalQueries += $result.Total
        }
    }
    Remove-Job -Job $job -Force
}

$endTime = Get-Date
$elapsed = [math]::Max(0.001, ($endTime - $startTime).TotalSeconds)
$qps = [math]::Round($totalQueries / $elapsed, 2)
$successRate = if ($totalQueries -gt 0) { [math]::Round(($totalOK / $totalQueries) * 100, 2) } else { 0 }

Write-Host "" 
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Final Statistics:" -ForegroundColor Yellow
Write-Host "  Total Queries: $totalQueries" -ForegroundColor White
Write-Host "  OK: $totalOK" -ForegroundColor White
Write-Host "  NOTFOUND: $totalNotFound" -ForegroundColor White
Write-Host "  FAIL: $totalFail" -ForegroundColor White
Write-Host "  QPS: $qps" -ForegroundColor White
Write-Host "  Success Rate: ${successRate}%" -ForegroundColor White
Write-Host "========================================" -ForegroundColor Cyan

$timestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
"[$timestamp] ===== P2P query load done Total=$totalQueries OK=$totalOK NOTFOUND=$totalNotFound FAIL=$totalFail QPS=$qps Success=${successRate}% =====" | Out-File -FilePath $LogFile -Append

if ($DoneFile -and $DoneFile.Trim() -ne "") {
    "done" | Out-File -FilePath $DoneFile -Force
}
