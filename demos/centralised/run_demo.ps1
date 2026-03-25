# Quick launcher for TDS centralised demo
# Checks if server is running and starts the demo

param(
    [switch]$BuildFirst,
    [switch]$StartServer,
    [switch]$UDP,
    [switch]$TCP,
    [string]$Server = "127.0.0.1:5000"
)

function Get-RepoRoot {
    param([string]$StartDir)

    $dir = $StartDir
    while ($true) {
        if (Test-Path (Join-Path $dir "go.mod")) {
            return $dir
        }
        $parent = Split-Path -Parent $dir
        if ($parent -eq $dir -or $parent -eq "") {
            throw "Could not locate repo root (go.mod not found) starting from: $StartDir"
        }
        $dir = $parent
    }
}

function Test-TDSJsonEndpoint {
    param(
        [string]$Protocol,
        [string]$ServerHost = "127.0.0.1",
        [int]$Port = 5000
    )

    $probe = '{"cmd":"QUERY","task":"__demo_probe__"}'

    try {
        if ($Protocol -eq "udp") {
            $udpClient = New-Object System.Net.Sockets.UdpClient
            $udpClient.Client.ReceiveTimeout = 2000
            $udpClient.Connect($ServerHost, $Port)

            $bytes = [System.Text.Encoding]::UTF8.GetBytes($probe)
            [void]$udpClient.Send($bytes, $bytes.Length)

            $remote = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
            $responseBytes = $udpClient.Receive([ref]$remote)
            $udpClient.Close()

            $raw = [System.Text.Encoding]::UTF8.GetString($responseBytes)
        } else {
            $tcpClient = New-Object System.Net.Sockets.TcpClient
            $connectTask = $tcpClient.ConnectAsync($ServerHost, $Port)
            if (-not $connectTask.Wait(2000)) {
                throw "TCP connection timed out"
            }

            $stream = $tcpClient.GetStream()
            $stream.ReadTimeout = 2000
            $stream.WriteTimeout = 2000

            $writer = New-Object System.IO.StreamWriter($stream)
            $writer.AutoFlush = $true
            $writer.NewLine = "`n"
            $writer.WriteLine($probe)

            $reader = New-Object System.IO.StreamReader($stream)
            $raw = $reader.ReadLine()
            $tcpClient.Close()
        }

        if ([string]::IsNullOrWhiteSpace($raw)) {
            return @{ Ok = $false; Error = "Empty response"; Raw = $raw }
        }

        $json = $raw | ConvertFrom-Json -ErrorAction Stop
        if ($null -eq $json.status) {
            return @{ Ok = $false; Error = "Missing JSON field 'status'"; Raw = $raw }
        }

        return @{ Ok = $true; Raw = $raw }
    } catch {
        return @{ Ok = $false; Error = $_.Exception.Message; Raw = "" }
    }
}

$repoRoot = Get-RepoRoot -StartDir $PSScriptRoot
$serverPath = Join-Path $repoRoot "cmd\server"
$demoPath = $PSScriptRoot
$selfName = Split-Path -Leaf $PSCommandPath

$serverParts = $Server.Split(':')
if ($serverParts.Length -ne 2) {
    Write-Host "[ERROR] -Server must be in host:port format, e.g. 127.0.0.1:5000" -ForegroundColor Red
    exit 1
}

$serverHost = $serverParts[0]
$serverPort = 0
if (-not [int]::TryParse($serverParts[1], [ref]$serverPort)) {
    Write-Host "[ERROR] Invalid port in -Server '$Server'" -ForegroundColor Red
    exit 1
}

if ($serverPort -lt 1 -or $serverPort -gt 65535) {
    Write-Host "[ERROR] Port in -Server must be between 1 and 65535" -ForegroundColor Red
    exit 1
}

if ($UDP -and $TCP) {
    Write-Host "[ERROR] Use either -UDP or -TCP, not both." -ForegroundColor Red
    exit 1
}

$protocol = "tcp"
if ($UDP) {
    $protocol = "udp"
}

Write-Host "=====================================" -ForegroundColor Cyan
Write-Host "  TDS Centralised Demo Launcher" -ForegroundColor Cyan
Write-Host "=====================================" -ForegroundColor Cyan
Write-Host "  Protocol: $($protocol.ToUpper())" -ForegroundColor White
Write-Host "  Server:   $Server" -ForegroundColor White
Write-Host ""

# Check if we should start the server
if ($StartServer) {
    Write-Host "[INFO] Starting TDS server in background..." -ForegroundColor Yellow
    
    
    # Start server in new window
    $serverCommand = "cd '$serverPath'; go run main.go --$protocol --port $serverPort"
    if ($IsWindows -or $PSVersionTable.PSVersion.Major -le 5) {
        Start-Process powershell -ArgumentList "-NoExit", "-Command", $serverCommand -WindowStyle Normal
    } else {
        # Linux/Mac
        Start-Process pwsh -ArgumentList "-NoExit", "-Command", $serverCommand
    }
    
    Write-Host "[INFO] Waiting for server to start..." -ForegroundColor Yellow
    Start-Sleep -Seconds 3
}

# Check if server is accessible and speaks TDS JSON protocol
Write-Host "[CHECK] Testing $($protocol.ToUpper()) connection to $Server..." -ForegroundColor Yellow
try {
    if ($protocol -eq "udp") {
        $udpClient = New-Object System.Net.Sockets.UdpClient
        $udpClient.Connect($serverHost, $serverPort)
        $udpClient.Close()
    } else {
        $tcpClient = New-Object System.Net.Sockets.TcpClient
        $connectTask = $tcpClient.ConnectAsync($serverHost, $serverPort)
        if (-not $connectTask.Wait(2000)) {
            throw "TCP connection timed out"
        }
        $tcpClient.Close()
    }
    Write-Host "[OK] Port is reachable." -ForegroundColor Green
} catch {
    Write-Host "[WARN] Cannot connect to server on $Server via $($protocol.ToUpper())" -ForegroundColor Yellow
    Write-Host "" 
    Write-Host "Please start the TDS server first:" -ForegroundColor White
    Write-Host "  1. Open a new terminal" -ForegroundColor White
    Write-Host "  2. Run: cd cmd\server" -ForegroundColor White
    Write-Host "  3. Run: go run main.go" -ForegroundColor White
    Write-Host ""
    Write-Host "Or run this script with -StartServer flag:" -ForegroundColor White
    Write-Host "  .\$selfName -StartServer" -ForegroundColor Cyan
    Write-Host ""
    
    $response = Read-Host "Start demo anyway? (y/N)"
    if ($response -ne 'y' -and $response -ne 'Y') {
        Write-Host "[EXIT] Demo cancelled" -ForegroundColor Red
        exit 1
    }
}

$probe = Test-TDSJsonEndpoint -Protocol $protocol -ServerHost $serverHost -Port $serverPort
if (-not $probe.Ok) {
    Write-Host "[WARN] Endpoint on $Server did not return valid TDS JSON over $($protocol.ToUpper())." -ForegroundColor Yellow
    if ($probe.Raw) {
        Write-Host "[WARN] Raw response: $($probe.Raw)" -ForegroundColor Yellow
    }
    Write-Host "[WARN] Probe error: $($probe.Error)" -ForegroundColor Yellow
    Write-Host ""
    Write-Host "Likely causes:" -ForegroundColor White
    Write-Host "  - A different service is bound to port 5000" -ForegroundColor White
    Write-Host "  - Server is running in a different transport mode" -ForegroundColor White
    Write-Host "  - Server is in TLS mode while demo is using plain TCP/UDP" -ForegroundColor White
    Write-Host ""
    Write-Host "Quick fix:" -ForegroundColor White
    Write-Host "  .\\$selfName -StartServer -$($protocol.ToUpper()) -Server $Server" -ForegroundColor Cyan
    exit 1
}
Write-Host "[OK] TDS JSON protocol probe passed." -ForegroundColor Green

Write-Host ""

# Build if requested
if ($BuildFirst) {
    Write-Host "[BUILD] Building demo executable..." -ForegroundColor Yellow
    Push-Location $demoPath
    if (-not (Test-Path (Join-Path $repoRoot "bin"))) {
        New-Item -Path (Join-Path $repoRoot "bin") -ItemType Directory | Out-Null
    }
    go build -o (Join-Path $repoRoot "bin\demo.exe") .
    if ($LASTEXITCODE -eq 0) {
        Write-Host "[OK] Build successful!" -ForegroundColor Green
        Write-Host ""
        Write-Host "[RUN] Starting demo..." -ForegroundColor Cyan
        Write-Host ""
        & (Join-Path $repoRoot "bin\demo.exe") -protocol $protocol -server $Server -step-by-step
    } else {
        Write-Host "[ERROR] Build failed!" -ForegroundColor Red
        Pop-Location
        exit 1
    }
    Pop-Location
} else {
    Write-Host "[RUN] Starting demo with 'go run'..." -ForegroundColor Cyan
    Write-Host ""
    Push-Location $demoPath
    go run . -protocol $protocol -server $Server -step-by-step
    Pop-Location
}

Write-Host ""
Write-Host "[DONE] Demo complete!" -ForegroundColor Green
