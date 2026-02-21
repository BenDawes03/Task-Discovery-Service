param(
    [int]$ProxyPort = 5100,
    [int]$NodeId = 1,
    [int]$NumServices = 20000,
    [int]$NumTasks = 500,
    [int]$BaseServicePort = 8000,
    [int]$ThinkTimeMs = 0,
    [string]$LogFile = "p2p_register_load.log",
    [string]$DoneFile = ""
)

$ErrorActionPreference = "Continue"

function Get-LocalIP {
    if ($IsWindows -or $PSVersionTable.PSVersion.Major -le 5) {
        try {
            $ip = Get-NetIPAddress -AddressFamily IPv4 |
                Where-Object { $_.IPAddress -ne "127.0.0.1" -and $_.PrefixOrigin -ne "WellKnown" } |
                Select-Object -First 1 -ExpandProperty IPAddress
            if ($ip) { return $ip }
        } catch { }
    } else {
        try {
            $ip = (hostname -I 2>$null).Trim().Split()[0]
            if ($ip -and $ip -ne "127.0.0.1") { return $ip }
        } catch { }
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

function Send-Registration {
    param(
        [int]$Port,
        [string]$Task,
        [string]$ServiceAddr
    )

    $message = "REGISTER $Task $ServiceAddr"
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

$localIP = Get-LocalIP
$timestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
"[$timestamp] ===== P2P register load start (node $NodeId) =====" | Out-File -FilePath $LogFile -Append
"[$timestamp] ProxyPort=$ProxyPort NumServices=$NumServices NumTasks=$NumTasks LocalIP=$localIP" | Out-File -FilePath $LogFile -Append

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "P2P REGISTER Load (Node $NodeId)" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Proxy: 127.0.0.1:$ProxyPort | Services: $NumServices | Tasks: $NumTasks" -ForegroundColor Yellow
Write-Host ""

$tasks = 1..$NumTasks | ForEach-Object { "task_$_" }

$ok = 0
$fail = 0
$sw = [System.Diagnostics.Stopwatch]::StartNew()

for ($i = 0; $i -lt $NumServices; $i++) {
    $task = $tasks[$i % $tasks.Count]
    $serviceAddr = "${localIP}:$($BaseServicePort + $i)"

    $resp = Send-Registration -Port $ProxyPort -Task $task -ServiceAddr $serviceAddr
    if ($resp -eq "OK") {
        $ok++
    } else {
        $fail++
    }

    if ((($i + 1) % 1000) -eq 0) {
        $elapsed = [math]::Max(0.001, $sw.Elapsed.TotalSeconds)
        $rps = [math]::Round(($i + 1) / $elapsed, 2)
        $ts = Get-Date -Format "HH:mm:ss"
        Write-Host "[$ts] Registered: $($i + 1) | OK=$ok FAIL=$fail | RPS=$rps" -ForegroundColor Cyan
        "[$ts] Registered: $($i + 1) OK=$ok FAIL=$fail RPS=$rps" | Out-File -FilePath $LogFile -Append
    }

    if ($ThinkTimeMs -gt 0) {
        Start-Sleep -Milliseconds $ThinkTimeMs
    }
}

$sw.Stop()
$duration = [math]::Round($sw.Elapsed.TotalSeconds, 2)
$rpsFinal = if ($duration -gt 0) { [math]::Round($NumServices / $duration, 2) } else { 0 }

Write-Host "" 
Write-Host "[DONE] Node $NodeId registrations complete: OK=$ok FAIL=$fail Duration=${duration}s RPS=$rpsFinal" -ForegroundColor Green

$timestamp = Get-Date -Format "yyyy-MM-dd HH:mm:ss"
"[$timestamp] ===== P2P register load done (node $NodeId) OK=$ok FAIL=$fail Duration=${duration}s RPS=$rpsFinal =====" | Out-File -FilePath $LogFile -Append

if ($DoneFile -and $DoneFile.Trim() -ne "") {
    "done" | Out-File -FilePath $DoneFile -Force
}
