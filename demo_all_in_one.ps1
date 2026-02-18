# Legacy wrapper: this script moved to demos/interactive/

param(
    [int]$ServerStartDelay = 3,
    [switch]$CleanBuild
)

$ErrorActionPreference = "Stop"

$target = Join-Path $PSScriptRoot "demos\interactive\demo_all_in_one.ps1"
if (!(Test-Path $target)) {
    throw "Expected demo launcher at: $target"
}

Write-Host "[INFO] demo_all_in_one.ps1 moved to demos/interactive/demo_all_in_one.ps1" -ForegroundColor Yellow
& $target -ServerStartDelay $ServerStartDelay -CleanBuild:$CleanBuild
