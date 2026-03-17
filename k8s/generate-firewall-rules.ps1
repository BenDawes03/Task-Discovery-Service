#!/usr/bin/env powershell

param(
    [string]$Namespace = "tds-simulation",
    [string]$ConfigMapName = "tds-firewall-rules",
    [switch]$NoRestart
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$scriptDir = Split-Path -Parent $PSCommandPath
$tempRulesPath = Join-Path $env:TEMP "tds-firewall-rules.txt"

function Get-ComponentLabels {
    param(
        [string]$Namespace,
        [switch]$Proxy
    )

    $json = kubectl get deployments -n $Namespace -o json
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to query deployments in namespace '$Namespace'."
    }

    $deploymentList = $json | ConvertFrom-Json
    $labels = foreach ($item in $deploymentList.items) {
        $name = [string]$item.metadata.name
        if ($Proxy) {
            if ($name -match '^(cs|pctrbo|pa)-proxy$' -or $name -match '^station-\d+-proxy$' -or $name -match '^ticketdistributor-\d+-proxy$' -or $name -match '^gate(?:-\d+)?-proxy$') {
                $name
            }
            continue
        }

        if ($name -match '^(cs|pctrbo|pa)$' -or $name -match '^station-\d+$' -or $name -match '^ticketdistributor-\d+$' -or $name -match '^gate(?:-\d+)?$') {
            $name
        }
    }

    return @($labels | Sort-Object -Unique)
}

function Get-PodIPs {
    param(
        [string]$Label,
        [string]$Namespace
    )

    $json = kubectl get pods -n $Namespace -l "app=$Label" -o json
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to query pods for label '$Label'."
    }

    $podList = $json | ConvertFrom-Json
    $ips = @()
    foreach ($item in $podList.items) {
        if ($null -ne $item.status -and -not [string]::IsNullOrWhiteSpace($item.status.podIP)) {
            $ips += $item.status.podIP
        }
    }

    return $ips
}

function Add-AllowRules {
    param(
        [System.Collections.Generic.List[string]]$Lines,
        [string]$SourceLabel,
        [string[]]$SourceIPs,
        [string]$DestLabel,
        [string[]]$DestIPs
    )

    if ($SourceIPs.Count -eq 0 -or $DestIPs.Count -eq 0) {
        return
    }

    foreach ($sourceIP in $SourceIPs) {
        foreach ($destIP in $DestIPs) {
            if ($SourceLabel -eq "station-1-proxy" -and $DestLabel -eq "ticketdistributor-1") {
                continue
            }
            $Lines.Add("$sourceIP $destIP")
        }
    }
}

Write-Host "Generating firewall rules from live pod IPs..." -ForegroundColor Cyan

$proxyLabels = @(Get-ComponentLabels -Namespace $Namespace -Proxy)
$destinationLabels = @(Get-ComponentLabels -Namespace $Namespace)

$proxyIPsByLabel = @{}
foreach ($label in $proxyLabels) {
    $proxyIPsByLabel[$label] = @(Get-PodIPs -Label $label -Namespace $Namespace)
}

$destIPsByLabel = @{}
foreach ($label in $destinationLabels) {
    $destIPsByLabel[$label] = @(Get-PodIPs -Label $label -Namespace $Namespace)
}

$lines = [System.Collections.Generic.List[string]]::new()
$lines.Add("# Generated firewall rules for Kubernetes simulation")
$lines.Add("# station-1-proxy is intentionally blocked from ticketdistributor-1")

foreach ($proxyLabel in $proxyLabels) {
    foreach ($destLabel in $destinationLabels) {
        Add-AllowRules -Lines $lines -SourceLabel $proxyLabel -SourceIPs $proxyIPsByLabel[$proxyLabel] -DestLabel $destLabel -DestIPs $destIPsByLabel[$destLabel]
    }
}

$content = ($lines -join [Environment]::NewLine) + [Environment]::NewLine
[System.IO.File]::WriteAllText($tempRulesPath, $content)

Write-Host "Applying ConfigMap '$ConfigMapName'..." -ForegroundColor Cyan
kubectl create configmap $ConfigMapName -n $Namespace --from-file "rules.txt=$tempRulesPath" --dry-run=client -o yaml | kubectl apply -f -
if ($LASTEXITCODE -ne 0) {
    throw "Failed to apply ConfigMap '$ConfigMapName'."
}

if (-not $NoRestart) {
    Write-Host "Restarting tds-server deployment to reload firewall rules..." -ForegroundColor Cyan
    kubectl rollout restart deployment/tds-server -n $Namespace
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to restart deployment 'tds-server'."
    }
}

Write-Host "Firewall rules updated successfully." -ForegroundColor Green