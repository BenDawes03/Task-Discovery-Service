# Cache Performance Demo Runner
# This script helps run the cache demo with proper server setup.

param(
    [switch]$BuildOnly,
    [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"

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

Write-Host "=======================================================" -ForegroundColor Cyan
Write-Host "     TDS Cache Performance Demo Setup" -ForegroundColor Cyan
Write-Host "=======================================================" -ForegroundColor Cyan
Write-Host ""

$repoRoot = Get-RepoRoot -StartDir $PSScriptRoot
Set-Location $repoRoot

if (-not $SkipBuild) {
    Write-Host "[1/2] Building cache demo..." -ForegroundColor Yellow
    go build -o bin\cache_demo.exe .\cmd\cache_demo
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[X] Build failed" -ForegroundColor Red
        exit 1
    }
    Write-Host "[OK] Build complete" -ForegroundColor Green
    Write-Host ""
}

if ($BuildOnly) {
    Write-Host "Build complete. Use -SkipBuild to skip building next time." -ForegroundColor Cyan
    exit 0
}

Write-Host "[2/2] Checking server..." -ForegroundColor Yellow
if (-not (Test-Path "bin\server.exe")) {
    Write-Host "Server not built. Building now..." -ForegroundColor Yellow
    go build -o bin\server.exe .\cmd\server
    if ($LASTEXITCODE -ne 0) {
        Write-Host "[X] Server build failed" -ForegroundColor Red
        exit 1
    }
}
Write-Host "[OK] Server ready" -ForegroundColor Green
Write-Host ""

Write-Host "=======================================================" -ForegroundColor Cyan
Write-Host "  Setup Instructions" -ForegroundColor Cyan
Write-Host "=======================================================" -ForegroundColor Cyan
Write-Host ""

Write-Host "STEP 1: Start PostgreSQL" -ForegroundColor Yellow
Write-Host "If not already running, execute:" -ForegroundColor White
Write-Host "  docker run --name tds-postgres -e POSTGRES_PASSWORD=mysecretpassword \" -ForegroundColor Gray
Write-Host "    -e POSTGRES_DB=tds -p 5432:5432 -d postgres:15" -ForegroundColor Gray
Write-Host ""

Write-Host "STEP 2: Start TDS Server with cache enabled" -ForegroundColor Yellow
Write-Host "In a separate terminal, execute:" -ForegroundColor White
Write-Host "  .\bin\server.exe --store-url=\"postgresql://postgres:mysecretpassword@localhost:5432/tds?sslmode=disable\" --cache-max-size=10 --tcp" -ForegroundColor Gray
Write-Host ""

Write-Host "STEP 3: Run the cache demo" -ForegroundColor Yellow
Write-Host "Ready to run! Press ENTER when the server is started..." -ForegroundColor White
Read-Host | Out-Null

Write-Host ""
Write-Host "Starting cache performance demo..." -ForegroundColor Cyan
Write-Host ""

.\bin\cache_demo.exe

Write-Host ""
Write-Host "=======================================================" -ForegroundColor Cyan
Write-Host "  Demo Complete" -ForegroundColor Green
Write-Host "=======================================================" -ForegroundColor Cyan
