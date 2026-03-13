#!/usr/bin/env powershell
# Build Docker images for Kubernetes deployment

Write-Host "Building Docker images for Kubernetes..." -ForegroundColor Green

# Go to repo root
Push-Location (Split-Path -Parent $PSScriptRoot)

Write-Host "`n[1/5] Building tds-server image..." -ForegroundColor Cyan
docker build -f cmd/server/Dockerfile -t tds-server:latest .

Write-Host "`n[2/5] Building tds-client-proxy image..." -ForegroundColor Cyan
docker build -f cmd/client_proxy/Dockerfile -t tds-client-proxy:latest .

Write-Host "`n[3/5] Building sim-cs image..." -ForegroundColor Cyan
docker build -f Simulation/cs/Dockerfile -t sim-cs:latest .

Write-Host "`n[4/5] Building sim-pctrbo image..." -ForegroundColor Cyan
docker build -f Simulation/pctrbo/Dockerfile -t sim-pctrbo:latest .

Write-Host "`n[5/5] Building sim-pa image..." -ForegroundColor Cyan
docker build -f Simulation/pa/Dockerfile -t sim-pa:latest .

Write-Host "`nBuilding remaining simulation images..." -ForegroundColor Cyan
docker build -f Simulation/station_computer/Dockerfile -t sim-station:latest .
docker build -f Simulation/ticketdistributor/Dockerfile -t sim-ticketdistributor:latest .
docker build -f Simulation/gate/Dockerfile -t sim-gate:latest .

Pop-Location

Write-Host "`n[+] All images built successfully!" -ForegroundColor Green
Write-Host "Run: docker images" -ForegroundColor Yellow
