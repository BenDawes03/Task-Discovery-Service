#!/usr/bin/env powershell
# Deploy TDS simulation to Kubernetes

param(
    [Parameter(Mandatory=$false)]
    [ValidateSet("all", "infrastructure", "cs", "pctrbo", "pa", "station", "ticketdistributor", "gate")]
    [string]$Service = "all",
    
    [switch]$Delete,
    [switch]$Status
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

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

function Ensure-PCTRKeyConfigMaps {
    param([string]$Namespace = "tds-simulation")

    # Ensure namespace exists before writing namespaced ConfigMaps.
    kubectl get namespace $Namespace *> $null
    if ($LASTEXITCODE -ne 0) {
        throw "Namespace '$Namespace' does not exist. Deploy infrastructure first: .\deploy.ps1 -Service infrastructure"
    }

    $generatedDir = Join-Path $k8sDir ".generated"
    $privateKeyPath = Join-Path $generatedDir "pctr_private.pem"
    $publicKeyPath = Join-Path $generatedDir "pctr_public.pem"

    if (-not (Test-Path $generatedDir)) {
        New-Item -ItemType Directory -Path $generatedDir | Out-Null
    }

    $openssl = Get-Command openssl -ErrorAction SilentlyContinue
    if (-not $openssl) {
        throw "OpenSSL is required to generate PCTR RSA keys. Install OpenSSL or manually place '$privateKeyPath' and '$publicKeyPath'."
    }

    if (-not (Test-Path $privateKeyPath)) {
        Write-Host "Generating PCTR private key..." -ForegroundColor Cyan
        & openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out $privateKeyPath
        if ($LASTEXITCODE -ne 0) {
            throw "Failed to generate private key at '$privateKeyPath'."
        }
    }

    if (-not (Test-Path $publicKeyPath)) {
        Write-Host "Generating PCTR public key..." -ForegroundColor Cyan
        & openssl rsa -in $privateKeyPath -pubout -out $publicKeyPath
        if ($LASTEXITCODE -ne 0) {
            throw "Failed to derive public key at '$publicKeyPath'."
        }
    }

    Write-Host "Syncing key ConfigMaps (pa-keys, gate-keys)..." -ForegroundColor Cyan
    kubectl create configmap pa-keys -n $Namespace --from-file "pctr_private.pem=$privateKeyPath" --dry-run=client -o yaml | kubectl apply -f -
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to apply ConfigMap 'pa-keys'."
    }

    kubectl create configmap gate-keys -n $Namespace --from-file "pctr_public.pem=$publicKeyPath" --dry-run=client -o yaml | kubectl apply -f -
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to apply ConfigMap 'gate-keys'."
    }
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
        Ensure-PCTRKeyConfigMaps
    }
    elseif ($Service -eq "cs") {
        Deploy-Service "CS Service" @("08-cs-service.yaml")
    }
    elseif ($Service -eq "pctrbo") {
        Deploy-Service "PCTRBO Service" @("09-pctrbo-service.yaml")
    }
    elseif ($Service -eq "pa") {
        Deploy-Service "PA Service" @("10-pa-service.yaml")
        Ensure-PCTRKeyConfigMaps
    }
    elseif ($Service -eq "station") {
        Deploy-Service "Station Service" @("11-station-service.yaml")
    }
    elseif ($Service -eq "ticketdistributor") {
        Deploy-Service "Ticket Distributor Service" @("12-ticketdistributor-service.yaml")
    }
    elseif ($Service -eq "gate") {
        Deploy-Service "Gate Service" @("13-gate-service.yaml")
        Ensure-PCTRKeyConfigMaps
    }
    
    Write-Host "`n[+] Deployment complete!" -ForegroundColor Green
    Write-Host "Check status with: .\deploy.ps1 -Status" -ForegroundColor Yellow
}

    # Remind user about multi-instance management
    if (-not $Delete -and -not $Status) {
        Write-Host ""
        Write-Host "To add more station/gate instances use manage-stations-gates.ps1, e.g.:" -ForegroundColor DarkCyan
        Write-Host "  .\manage-stations-gates.ps1 -Action add-station -ID 2" -ForegroundColor DarkCyan
        Write-Host "  .\manage-stations-gates.ps1 -Action add-gate -ID 2 -StationID 2" -ForegroundColor DarkCyan
        Write-Host "  .\manage-stations-gates.ps1            (list all stations & gates)" -ForegroundColor DarkCyan
    }
