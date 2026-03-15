#!/usr/bin/env powershell
# Deploy TDS simulation to Kubernetes

param(
    [Parameter(Mandatory=$false)]
    [ValidateSet("all", "infrastructure", "cs", "pctrbo", "pa", "station", "ticketdistributor", "gate")]
    [string]$Service = "all",
    
    [switch]$Delete,
    [switch]$Status
)

$scriptDir = Split-Path -Parent $PSCommandPath
$k8sDir = $scriptDir

function Deploy-Service {
    param([string]$ServiceName, [string[]]$Files)
    
    Write-Host "`nDeploying $ServiceName..." -ForegroundColor Cyan
    foreach ($file in $Files) {
        Write-Host "  Applying: $file" -ForegroundColor Gray
        kubectl apply -f "$k8sDir\$file"
    }
}

function Delete-Service {
    param([string]$ServiceName, [string[]]$Files)
    
    Write-Host "`nDeleting $ServiceName..." -ForegroundColor Red
    foreach ($file in $Files) {
        Write-Host "  Removing: $file" -ForegroundColor Gray
        kubectl delete -f "$k8sDir\$file" --ignore-not-found
    }
}

function Show-Status {
    Write-Host "`nTDS Simulation Status:" -ForegroundColor Green
    Write-Host "Namespace: tds-simulation" -ForegroundColor Cyan
    kubectl get all -n tds-simulation
    
    Write-Host "`n`nPersistent Volumes:" -ForegroundColor Cyan
    kubectl get pvc -n tds-simulation
}

# Define service dependencies
$infrastructure = @("00-namespace.yaml", "01-configmap.yaml", "02-storage-class.yaml", "03-tds-server.yaml", "04-client-proxy.yaml")
$databases = @("05-cs-db.yaml", "06-pctrbo-db.yaml", "07-pa-db.yaml")
$services = @("08-cs-service.yaml", "09-pctrbo-service.yaml", "10-pa-service.yaml", "11-station-service.yaml", "12-ticketdistributor-service.yaml", "13-gate-service.yaml")

if ($Delete) {
    Write-Host "Deleting TDS Simulation..." -ForegroundColor Red
    
    if ($Service -eq "all" -or $Service -eq "gate") {
        Delete-Service "gate" @("13-gate-service.yaml")
    }
    if ($Service -eq "all" -or $Service -eq "ticketdistributor") {
        Delete-Service "ticketdistributor" @("12-ticketdistributor-service.yaml")
    }
    if ($Service -eq "all" -or $Service -eq "station") {
        Delete-Service "station" @("11-station-service.yaml")
    }
    if ($Service -eq "all" -or $Service -eq "pa") {
        Delete-Service "pa" @("10-pa-service.yaml")
    }
    if ($Service -eq "all" -or $Service -eq "pctrbo") {
        Delete-Service "pctrbo" @("09-pctrbo-service.yaml")
    }
    if ($Service -eq "all" -or $Service -eq "cs") {
        Delete-Service "cs" @("08-cs-service.yaml")
    }
    if ($Service -eq "all" -or $Service -eq "infrastructure") {
        Delete-Service "databases" $databases
        Delete-Service "infrastructure" $infrastructure
    }
} 
elseif ($Status) {
    Show-Status
}
else {
    Write-Host "Deploying TDS Simulation to Kubernetes" -ForegroundColor Green
    
    # Deploy infrastructure first
    if ($Service -eq "all" -or $Service -eq "infrastructure") {
        Deploy-Service "Infrastructure" $infrastructure
        Deploy-Service "Databases" $databases
        
        Write-Host "`n⏳ Waiting for infrastructure to be ready..." -ForegroundColor Yellow
        Start-Sleep -Seconds 5
    }
    
    # Deploy services
    if ($Service -eq "all") {
        Deploy-Service "Simulation Services" $services
    }
    elseif ($Service -eq "cs") {
        Deploy-Service "CS Service" @("08-cs-service.yaml")
    }
    elseif ($Service -eq "pctrbo") {
        Deploy-Service "PCTRBO Service" @("09-pctrbo-service.yaml")
    }
    elseif ($Service -eq "pa") {
        Deploy-Service "PA Service" @("10-pa-service.yaml")
    }
    elseif ($Service -eq "station") {
        Deploy-Service "Station Service" @("11-station-service.yaml")
    }
    elseif ($Service -eq "ticketdistributor") {
        Deploy-Service "Ticket Distributor Service" @("12-ticketdistributor-service.yaml")
    }
    elseif ($Service -eq "gate") {
        Deploy-Service "Gate Service" @("13-gate-service.yaml")
    }
    
    Write-Host "`n[+] Deployment complete!" -ForegroundColor Green
    Write-Host "Check status with: .\deploy.ps1 -Status" -ForegroundColor Yellow
}
