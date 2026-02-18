# Test JSON messaging over TCP
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$projectRoot = Split-Path -Parent (Split-Path -Parent $scriptDir)
Set-Location $projectRoot

$env:TDS_SERVER_PROTO = "tcp"
Write-Host "Testing JSON protocol over TCP..." -ForegroundColor Cyan
Write-Host "Make sure server is running with TDS_SERVER_PROTO=tcp" -ForegroundColor Yellow
Write-Host ""
go run .\cmd\test_json_client\main.go
