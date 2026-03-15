# Test JSON messaging over UDP
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$projectRoot = Split-Path -Parent (Split-Path -Parent $scriptDir)
Set-Location $projectRoot

$env:TDS_SERVER_PROTO = "udp"
Write-Host "Testing JSON protocol over UDP..." -ForegroundColor Cyan
Write-Host "Make sure server is running in UDP mode (default)" -ForegroundColor Yellow
Write-Host ""
go run .\cmd\test_json_client\main.go
