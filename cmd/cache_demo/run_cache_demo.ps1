# Cache Performance Demo Runner
# This script helps run the cache demo with proper server setup

param(
    [switch]$BuildOnly,
    [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"

Write-Host "═══════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host "     TDS Cache Performance Demo Setup" -ForegroundColor Cyan
Write-Host "═══════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host ""

# Navigate to repo root
$repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
Set-Location $repoRoot

# Build the demo if needed
if (-not $SkipBuild) {
    Write-Host "[1/2] Building cache demo..." -ForegroundColor Yellow
    go build -o cmd\cache_demo\cache_demo.exe .\cmd\cache_demo
    if ($LASTEXITCODE -ne 0) {
        Write-Host "✗ Build failed!" -ForegroundColor Red
        exit 1
    }
    Write-Host "✓ Build complete!" -ForegroundColor Green
    Write-Host ""
}

if ($BuildOnly) {
    Write-Host "Build complete. Use -SkipBuild to skip building next time." -ForegroundColor Cyan
    exit 0
}

# Check if server is already built
Write-Host "[2/2] Checking server..." -ForegroundColor Yellow
if (-not (Test-Path "cmd\server\server.exe")) {
    Write-Host "Server not built. Building now..." -ForegroundColor Yellow
    go build -o cmd\server\server.exe .\cmd\server
    if ($LASTEXITCODE -ne 0) {
        Write-Host "✗ Server build failed!" -ForegroundColor Red
        exit 1
    }
}
Write-Host "✓ Server ready!" -ForegroundColor Green
Write-Host ""

Write-Host "═══════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host "  Setup Instructions" -ForegroundColor Cyan
Write-Host "═══════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host ""

Write-Host "STEP 1: Start PostgreSQL" -ForegroundColor Yellow
Write-Host "If not already running, execute:" -ForegroundColor White
Write-Host '  docker run --name tds-postgres -e POSTGRES_PASSWORD=mysecretpassword \' -ForegroundColor Gray
Write-Host '    -e POSTGRES_DB=tds -p 5432:5432 -d postgres:15' -ForegroundColor Gray
Write-Host ""

Write-Host "STEP 2: Start TDS Server with cache enabled" -ForegroundColor Yellow
Write-Host "In a separate terminal, execute:" -ForegroundColor White
Write-Host '  .\cmd\server\server.exe --store-url="postgresql://postgres:mysecretpassword@localhost:5432/tds?sslmode=disable" --cache-max-size=10 --tcp' -ForegroundColor Gray
Write-Host ""

Write-Host "STEP 3: Run the cache demo" -ForegroundColor Yellow
Write-Host "Ready to run! Press ENTER when the server is started..." -ForegroundColor White
Read-Host

Write-Host ""
Write-Host "Starting cache performance demo..." -ForegroundColor Cyan
Write-Host ""

# Run the demo
.\cmd\cache_demo\cache_demo.exe

Write-Host ""
Write-Host "═══════════════════════════════════════════════════════" -ForegroundColor Cyan
Write-Host "  Demo Complete!" -ForegroundColor Green
Write-Host "═══════════════════════════════════════════════════════" -ForegroundColor Cyan
