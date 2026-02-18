# All-in-One TDS Demo Launcher
# Starts the server and runs the demo automatically

param(
    [int]$ServerStartDelay = 3,  # Seconds to wait for server to start
    [switch]$CleanBuild          # Force rebuild of binaries
)

$ErrorActionPreference = "Stop"

Write-Host "╔════════════════════════════════════════╗" -ForegroundColor Cyan
Write-Host "║   TDS All-in-One Demo Launcher        ║" -ForegroundColor Cyan
Write-Host "╚════════════════════════════════════════╝" -ForegroundColor Cyan
Write-Host ""

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
Push-Location $repoRoot

try {
    # Build binaries if needed
    if ($CleanBuild -or -not (Test-Path ".\bin\server.exe") -or -not (Test-Path ".\bin\demo.exe")) {
        Write-Host "[BUILD] Building TDS server..." -ForegroundColor Yellow
        go build -o .\bin\server.exe .\cmd\server
        if ($LASTEXITCODE -ne 0) {
            throw "Server build failed"
        }
        Write-Host "[OK] Server built successfully" -ForegroundColor Green
        
        Write-Host "[BUILD] Building demo program..." -ForegroundColor Yellow
        go build -o .\bin\demo.exe .\cmd\demo
        if ($LASTEXITCODE -ne 0) {
            throw "Demo build failed"
        }
        Write-Host "[OK] Demo built successfully" -ForegroundColor Green
        Write-Host ""
    } else {
        Write-Host "[INFO] Using existing binaries (use -CleanBuild to rebuild)" -ForegroundColor Gray
        Write-Host ""
    }
    
    # Start server in background
    Write-Host "[START] Launching TDS server in new window..." -ForegroundColor Yellow
    
    $serverExe = Join-Path $repoRoot "bin\server.exe"
    
    # Start server in new window (stays open)
    $serverProcess = Start-Process -FilePath $serverExe -WorkingDirectory $repoRoot -PassThru -WindowStyle Normal
    
    Write-Host "[OK] Server started (PID: $($serverProcess.Id))" -ForegroundColor Green
    Write-Host "[WAIT] Waiting ${ServerStartDelay}s for server initialization..." -ForegroundColor Yellow
    Start-Sleep -Seconds $ServerStartDelay
    
    # Check if server is still running
    if ($serverProcess.HasExited) {
        throw "Server process exited unexpectedly"
    }
    
    # Verify server is listening
    Write-Host "[CHECK] Verifying server is responding..." -ForegroundColor Yellow
    $connected = $false
    for ($i = 0; $i -lt 3; $i++) {
        try {
            $udpClient = New-Object System.Net.Sockets.UdpClient
            $udpClient.Connect("localhost", 5000)
            $udpClient.Close()
            $connected = $true
            break
        } catch {
            Start-Sleep -Seconds 1
        }
    }
    
    if (-not $connected) {
        Write-Host "[WARN] Cannot verify server connection, but continuing anyway..." -ForegroundColor Yellow
    } else {
        Write-Host "[OK] Server is responding on port 5000" -ForegroundColor Green
    }
    
    Write-Host ""
    Write-Host "════════════════════════════════════════" -ForegroundColor Cyan
    Write-Host ""
    
    # Run the demo
    Write-Host "[RUN] Starting demo program..." -ForegroundColor Cyan
    Write-Host ""
    
    $demoExe = Join-Path $repoRoot "bin\demo.exe"
    & $demoExe
    
    Write-Host ""
    Write-Host "════════════════════════════════════════" -ForegroundColor Cyan
    Write-Host ""
    
    # Ask if user wants to stop the server
    Write-Host "[CLEANUP] Demo complete!" -ForegroundColor Green
    $response = Read-Host "Stop the TDS server? (Y/n)"
    
    if ($response -eq '' -or $response -eq 'y' -or $response -eq 'Y') {
        Write-Host "[STOP] Stopping server (PID: $($serverProcess.Id))..." -ForegroundColor Yellow
        Stop-Process -Id $serverProcess.Id -Force
        Write-Host "[OK] Server stopped" -ForegroundColor Green
    } else {
        Write-Host "[INFO] Server left running (PID: $($serverProcess.Id))" -ForegroundColor Cyan
        Write-Host "[INFO] To stop it later: Stop-Process -Id $($serverProcess.Id)" -ForegroundColor Gray
    }
    
} catch {
    Write-Host ""
    Write-Host "[ERROR] $($_.Exception.Message)" -ForegroundColor Red
    
    # Try to clean up server if it started
    if ($serverProcess -and -not $serverProcess.HasExited) {
        Write-Host "[CLEANUP] Stopping server..." -ForegroundColor Yellow
        Stop-Process -Id $serverProcess.Id -Force
    }
    
    Pop-Location
    exit 1
}

Pop-Location

Write-Host ""
Write-Host "[DONE] All done! Thank you for trying TDS." -ForegroundColor Green
Write-Host ""
