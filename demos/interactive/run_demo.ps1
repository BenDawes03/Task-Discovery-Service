# Quick launcher for TDS demonstration
# Checks if server is running and starts the demo

param(
    [switch]$BuildFirst,
    [switch]$StartServer
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

$repoRoot = Get-RepoRoot -StartDir $PSScriptRoot
$serverPath = Join-Path $repoRoot "cmd\server"
$demoPath = Join-Path $repoRoot "cmd\demo"
$selfName = Split-Path -Leaf $PSCommandPath

Write-Host "=====================================" -ForegroundColor Cyan
Write-Host "  TDS Demonstration Launcher" -ForegroundColor Cyan
Write-Host "=====================================" -ForegroundColor Cyan
Write-Host ""

# Check if we should start the server
if ($StartServer) {
    Write-Host "[INFO] Starting TDS server in background..." -ForegroundColor Yellow
    
    
    # Start server in new window
    if ($IsWindows -or $PSVersionTable.PSVersion.Major -le 5) {
        Start-Process powershell -ArgumentList "-NoExit", "-Command", "cd '$serverPath'; go run main.go" -WindowStyle Normal
    } else {
        # Linux/Mac
        Start-Process pwsh -ArgumentList "-NoExit", "-Command", "cd '$serverPath'; go run main.go"
    }
    
    Write-Host "[INFO] Waiting for server to start..." -ForegroundColor Yellow
    Start-Sleep -Seconds 3
}

# Check if server is accessible
Write-Host "[CHECK] Testing connection to localhost:5000..." -ForegroundColor Yellow
try {
    $udpClient = New-Object System.Net.Sockets.UdpClient
    $udpClient.Connect("localhost", 5000)
    $udpClient.Close()
    Write-Host "[OK] Server is accessible!" -ForegroundColor Green
} catch {
    Write-Host "[WARN] Cannot connect to server on localhost:5000" -ForegroundColor Yellow
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
        & (Join-Path $repoRoot "bin\demo.exe")
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
    go run .
    Pop-Location
}

Write-Host ""
Write-Host "[DONE] Demo complete!" -ForegroundColor Green
