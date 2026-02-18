# Legacy wrapper: this script moved to demos/cache/

param(
    [switch]$BuildOnly,
    [switch]$SkipBuild
)

$ErrorActionPreference = "Stop"

$repoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$target = Join-Path $repoRoot "demos\cache\run_cache_demo.ps1"

if (!(Test-Path $target)) {
    throw "Expected cache demo runner at: $target"
}

Write-Host "[INFO] run_cache_demo.ps1 moved to demos/cache/run_cache_demo.ps1" -ForegroundColor Yellow
& $target -BuildOnly:$BuildOnly -SkipBuild:$SkipBuild
