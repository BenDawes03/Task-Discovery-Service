#!/usr/bin/env powershell
# Build Docker images for Kubernetes deployment

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Build-Image {
	param(
		[Parameter(Mandatory=$true)][string]$Step,
		[Parameter(Mandatory=$true)][string]$Dockerfile,
		[Parameter(Mandatory=$true)][string]$Tag
	)

	Write-Host "`n[$Step] Building $Tag image..." -ForegroundColor Cyan
	docker build -f $Dockerfile -t "$Tag`:latest" .
	if ($LASTEXITCODE -ne 0) {
		throw "Failed building image '$Tag' from Dockerfile '$Dockerfile'."
	}
}

Write-Host "Building Docker images for Kubernetes..." -ForegroundColor Green

# Go to repo root
Push-Location (Split-Path -Parent $PSScriptRoot)

try {
	Build-Image -Step "1/8" -Dockerfile "cmd/server/Dockerfile" -Tag "tds-server"
	Build-Image -Step "2/8" -Dockerfile "cmd/client_proxy/Dockerfile" -Tag "tds-client-proxy"
	Build-Image -Step "3/8" -Dockerfile "Simulation/cs/Dockerfile" -Tag "sim-cs"
	Build-Image -Step "4/8" -Dockerfile "Simulation/pctrbo/Dockerfile" -Tag "sim-pctrbo"
	Build-Image -Step "5/8" -Dockerfile "Simulation/pa/Dockerfile" -Tag "sim-pa"
	Build-Image -Step "6/8" -Dockerfile "Simulation/station_computer/Dockerfile" -Tag "sim-station"
	Build-Image -Step "7/8" -Dockerfile "Simulation/ticketdistributor/Dockerfile" -Tag "sim-ticketdistributor"
	Build-Image -Step "8/8" -Dockerfile "Simulation/gate/Dockerfile" -Tag "sim-gate"

	Write-Host "`n[+] All images built successfully!" -ForegroundColor Green
	Write-Host "Run: docker images" -ForegroundColor Yellow
}
finally {
	Pop-Location
}
