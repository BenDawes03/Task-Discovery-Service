# Quick Cache Miss Demo Runner
# This demo shows cache behavior with cache-max-size=1

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
            throw "Could not locate repo root"
        }
        $dir = $parent
    }
}

Write-Host "=======================================================" -ForegroundColor Cyan
Write-Host "     Quick Cache Miss Demo - Cache Size 1" -ForegroundColor Cyan
Write-Host "=======================================================" -ForegroundColor Cyan
Write-Host ""

$repoRoot = Get-RepoRoot -StartDir $PSScriptRoot
Set-Location $repoRoot

if (-not $SkipBuild) {
    Write-Host "[1/2] Building demo..." -ForegroundColor Yellow
    go build -o bin\quick_cache_miss_demo.exe .\demos\centralised\quick_cache_miss_demo
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

Write-Host "[2/2] Starting demo..." -ForegroundColor Yellow
Write-Host ""
Write-Host "SETUP REQUIRED:" -ForegroundColor Red
Write-Host "  Start the server in another terminal with cache-max-size=1:" -ForegroundColor Yellow
Write-Host ""
Write-Host "  ./server.exe --store-url=""postgresql://user:pass@localhost:5432/tds"" --cache-max-size=1 --tcp" -ForegroundColor Cyan
Write-Host ""
Write-Host "  Or:" -ForegroundColor Yellow
Write-Host "  ./server.exe --cache-max-size=1 --tcp  (if using default DB URL)" -ForegroundColor Cyan
Write-Host ""
Write-Host "=======================================================" -ForegroundColor Cyan
Write-Host ""

& .\bin\quick_cache_miss_demo.exe
