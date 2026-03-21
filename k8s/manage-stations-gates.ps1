#!/usr/bin/env powershell
<#
.SYNOPSIS
    Manage station and gate instances in the TDS Kubernetes simulation.

.DESCRIPTION
    Dynamically adds or removes numbered station and gate instances.

    The base instances (station-1, gate/gate-1) are deployed by deploy.ps1.
    Use this script for any additional instances (station-2, station-3,
    gate-2, gate-3, etc.) or to re-apply station-1 / gate-1 with the correct IDs.

    Each station registers itself with the TDS proxy under the task name
    "station-Computer-<id>". Gates resolve their station's address via
    that same task name, so a gate attached to station-2 will automatically
    find station-2 through the discovery mechanism.

.PARAMETER Action
    list            – Show all station and gate deployments/services.
    add-station     – Create or update station-<ID>.
    remove-station  – Delete station-<ID> deployment and service.
    add-gate        – Create or update gate-<ID>, wired to station-<StationID>.
    remove-gate     – Delete gate-<ID> deployment and service.

.PARAMETER ID
    Numeric ID for the instance to add or remove (e.g. 2 → station-2).

.PARAMETER StationID
    For add-gate: the station number this gate is attached to (default 1).

.PARAMETER Namespace
    Kubernetes namespace. Default: tds-simulation.

.EXAMPLE
    # See what is currently running
    .\manage-stations-gates.ps1

    # Add a second station
    .\manage-stations-gates.ps1 -Action add-station -ID 2

    # Add a gate on station 2
    .\manage-stations-gates.ps1 -Action add-gate -ID 2 -StationID 2

    # Add a third station and two more gates
    .\manage-stations-gates.ps1 -Action add-station -ID 3
    .\manage-stations-gates.ps1 -Action add-gate -ID 3 -StationID 1
    .\manage-stations-gates.ps1 -Action add-gate -ID 4 -StationID 3

    # Remove station 2 (remove its gates first to avoid dangling init-containers)
    .\manage-stations-gates.ps1 -Action remove-gate -ID 2
    .\manage-stations-gates.ps1 -Action remove-station -ID 2
#>
param(
    [Parameter(Mandatory = $false)]
    [ValidateSet("list", "add-station", "remove-station", "add-gate", "remove-gate")]
    [string]$Action = "list",

    [Parameter(Mandatory = $false)]
    [int]$ID = 0,

    [Parameter(Mandatory = $false)]
    [int]$StationID = 1,

    [Parameter(Mandatory = $false)]
    [string]$Namespace = "tds-simulation"
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

$scriptDir = Split-Path -Parent $PSCommandPath

function Update-FirewallRules {
  $ruleScript = Join-Path $scriptDir "generate-firewall-rules.ps1"
  if (-not (Test-Path $ruleScript)) {
    throw "Firewall rule generator '$ruleScript' was not found."
  }

  & $ruleScript -Namespace $Namespace
  if ($LASTEXITCODE -ne 0) {
    throw "Failed to refresh firewall rules."
  }
}

# ---------------------------------------------------------------------------
# YAML generators
# ---------------------------------------------------------------------------

function Get-StationYAML([int]$N) {
    $name = "station-$N"
  $proxyName = "$name-proxy"
    @"
apiVersion: apps/v1
kind: Deployment
metadata:
  name: $name
  namespace: $Namespace
  labels:
    tds-role: station
spec:
  replicas: 1
  selector:
    matchLabels:
      app: $name
  template:
    metadata:
      labels:
        app: $name
        tds-role: station
    spec:
      containers:
      - name: station
        image: sim-station:latest
        imagePullPolicy: IfNotPresent
        args:
        - "-station-id"
        - "$N"
        - "-proxy"
        - "${proxyName}:5100"
        - "-proxy-proto"
        - "tcp"
        ports:
        - containerPort: 9100
        env:
        - name: TDS_SERVER_ADDR
          valueFrom:
            configMapKeyRef:
              name: tds-config
              key: TDS_SERVER_ADDR
      initContainers:
      - name: wait-for-station-proxy
        image: busybox:1.28
        command: ['sh', '-c', 'until nslookup $proxyName; do echo waiting for $proxyName; sleep 1; done']
---
apiVersion: v1
kind: Service
metadata:
  name: $name
  namespace: $Namespace
  labels:
    tds-role: station
spec:
  selector:
    app: $name
  ports:
  - port: 9100
    targetPort: 9100
  type: NodePort
"@
}

function Get-ProxyYAML([string]$ProxyName, [string]$Role) {
    @"
apiVersion: apps/v1
kind: Deployment
metadata:
  name: $ProxyName
  namespace: $Namespace
  labels:
    tds-role: $Role
spec:
  replicas: 1
  selector:
    matchLabels:
      app: $ProxyName
  template:
    metadata:
      labels:
        app: $ProxyName
        tds-role: $Role
    spec:
      containers:
      - name: client-proxy
        image: tds-client-proxy:latest
        imagePullPolicy: IfNotPresent
        args:
        - "-background"
        - "-tcp"
        ports:
        - containerPort: 5100
          name: proxy
          protocol: TCP
        env:
        - name: TDS_SERVER_ADDR
          valueFrom:
            configMapKeyRef:
              name: tds-config
              key: TDS_SERVER_ADDR
        - name: TDS_PROXY_LISTEN
          valueFrom:
            configMapKeyRef:
              name: tds-config
              key: TDS_PROXY_LISTEN
        - name: TDS_SERVER_PROTO
          value: "tls"
        - name: TDS_TLS_CERT_FILE
          value: "/run/tds-tls/client.crt"
        - name: TDS_TLS_KEY_FILE
          value: "/run/tds-tls/client.key"
        - name: TDS_TLS_CA_FILE
          value: "/run/tds-tls/ca.crt"
        volumeMounts:
        - name: tds-tls
          mountPath: /run/tds-tls
          readOnly: true
      volumes:
      - name: tds-tls
        secret:
          secretName: tds-tls-certs
      initContainers:
      - name: wait-for-tds-server
        image: busybox:1.28
        command: ['sh', '-c', 'until nslookup tds-server; do echo waiting for tds-server; sleep 1; done']
---
apiVersion: v1
kind: Service
metadata:
  name: $ProxyName
  namespace: $Namespace
  labels:
    tds-role: $Role
spec:
  selector:
    app: $ProxyName
  ports:
  - port: 5100
    targetPort: 5100
    name: proxy
    protocol: TCP
  type: ClusterIP
"@
}

function Get-GateYAML([int]$N, [int]$ForStationID) {
    $name      = "gate-$N"
    $stationSvc = "station-$ForStationID"
    $proxyName = "$name-proxy"
    @"
apiVersion: apps/v1
kind: Deployment
metadata:
  name: $name
  namespace: $Namespace
  labels:
    tds-role: gate
spec:
  replicas: 1
  selector:
    matchLabels:
      app: $name
  template:
    metadata:
      labels:
        app: $name
        tds-role: gate
    spec:
      containers:
      - name: gate
        image: sim-gate:latest
        imagePullPolicy: IfNotPresent
        args:
        - "-id"
        - "$name"
        - "-station-id"
        - "$ForStationID"
        - "-proxy"
        - "${proxyName}:5100"
        - "-proxy-proto"
        - "tcp"
        ports:
        - containerPort: 9200
        env:
        - name: PCTR_PUBLIC_KEY
          value: "/run/keys/pctr_public.pem"
        - name: TDS_SERVER_ADDR
          valueFrom:
            configMapKeyRef:
              name: tds-config
              key: TDS_SERVER_ADDR
        volumeMounts:
        - name: gate-keys
          mountPath: /run/keys
          readOnly: true
      volumes:
      - name: gate-keys
        configMap:
          name: gate-keys
      initContainers:
      - name: wait-for-gate-proxy
        image: busybox:1.28
        command: ['sh', '-c', 'until nslookup $proxyName; do echo waiting for $proxyName; sleep 1; done']
      - name: wait-for-station
        image: busybox:1.28
        command: ['sh', '-c', 'until nslookup $stationSvc; do echo waiting for $stationSvc; sleep 1; done']
      - name: wait-for-cs
        image: busybox:1.28
        command: ['sh', '-c', 'until nslookup cs; do echo waiting for cs; sleep 1; done']
      - name: wait-for-pa
        image: busybox:1.28
        command: ['sh', '-c', 'until nslookup pa; do echo waiting for pa; sleep 1; done']
---
apiVersion: v1
kind: Service
metadata:
  name: $name
  namespace: $Namespace
  labels:
    tds-role: gate
spec:
  selector:
    app: $name
  ports:
  - port: 9200
    targetPort: 9200
  type: NodePort
"@
}

# ---------------------------------------------------------------------------
# Actions
# ---------------------------------------------------------------------------

function Show-List {
    Write-Host "`nStations in namespace '$Namespace':" -ForegroundColor Cyan
    $deployments = kubectl get deployment -n $Namespace --no-headers 2>$null
    $services    = kubectl get svc        -n $Namespace --no-headers 2>$null

    $stationDeps = $deployments | Where-Object { $_ -match "^station-" }
    $stationSvcs = $services    | Where-Object { $_ -match "^station-" }

    if ($stationDeps) {
        Write-Host "  Deployments:" -ForegroundColor Gray
        $stationDeps | ForEach-Object { Write-Host "    $_" }
    }
    if ($stationSvcs) {
        Write-Host "  Services:" -ForegroundColor Gray
        $stationSvcs | ForEach-Object { Write-Host "    $_" }
    }
    if (-not $stationDeps -and -not $stationSvcs) {
        Write-Host "  (none)" -ForegroundColor DarkGray
    }

    Write-Host "`nGates in namespace '$Namespace':" -ForegroundColor Cyan
    # Include both "gate" (legacy name) and "gate-N" instances
    $gateDeps = $deployments | Where-Object { $_ -match "^gate" }
    $gateSvcs = $services    | Where-Object { $_ -match "^gate" }

    if ($gateDeps) {
        Write-Host "  Deployments:" -ForegroundColor Gray
        $gateDeps | ForEach-Object { Write-Host "    $_" }
    }
    if ($gateSvcs) {
        Write-Host "  Services:" -ForegroundColor Gray
        $gateSvcs | ForEach-Object { Write-Host "    $_" }
    }
    if (-not $gateDeps -and -not $gateSvcs) {
        Write-Host "  (none)" -ForegroundColor DarkGray
    }
}

function Add-Station([int]$N) {
  $proxyName = "station-$N-proxy"
  Write-Host "`nDeploying $proxyName..." -ForegroundColor Cyan
  Get-ProxyYAML -ProxyName $proxyName -Role "station-proxy" | kubectl apply -f -
  if ($LASTEXITCODE -ne 0) { throw "kubectl apply failed for $proxyName" }

    Write-Host "`nDeploying station-$N..." -ForegroundColor Cyan
    Get-StationYAML -N $N | kubectl apply -f -
    if ($LASTEXITCODE -ne 0) { throw "kubectl apply failed for station-$N" }
  Update-FirewallRules
    Write-Host "[+] station-$N deployed. It will register as task 'station-Computer-$N'." -ForegroundColor Green
}

function Remove-Station([int]$N) {
    Write-Host "`nRemoving station-$N..." -ForegroundColor Red

    # Warn if any gate deployment references this station
    $refs = kubectl get deployment -n $Namespace -o jsonpath="{range .items[*]}{.metadata.name}{' '}{.spec.template.spec.containers[0].args}{'\n'}{end}" 2>$null |
            Where-Object { $_ -match "gate" -and $_ -match [regex]::Escape("-station-id") -and $_ -match "\b$N\b" }
    if ($refs) {
        Write-Warning "The following gate(s) reference station-$N and may fail to resolve it after removal:"
        $refs | ForEach-Object { Write-Warning "  $_" }
        Write-Warning "Consider removing those gates first."
    }

    kubectl delete deployment "station-$N" -n $Namespace --ignore-not-found
    kubectl delete svc        "station-$N" -n $Namespace --ignore-not-found
    kubectl delete deployment "station-$N-proxy" -n $Namespace --ignore-not-found
    kubectl delete svc        "station-$N-proxy" -n $Namespace --ignore-not-found
    Update-FirewallRules
    Write-Host "[+] station-$N removed." -ForegroundColor Green
}

function Add-Gate([int]$N, [int]$ForStationID) {
    # Verify the target station service exists
    $existingSvc = kubectl get svc "station-$ForStationID" -n $Namespace --ignore-not-found --no-headers 2>$null
    if (-not $existingSvc) {
        Write-Warning "Service 'station-$ForStationID' not found in namespace '$Namespace'."
        Write-Warning "The gate's init container will block until that service is available."
        Write-Warning "Deploy station-$ForStationID first:  .\manage-stations-gates.ps1 -Action add-station -ID $ForStationID"
    }

    $proxyName = "gate-$N-proxy"
    Write-Host "`nDeploying $proxyName..." -ForegroundColor Cyan
    Get-ProxyYAML -ProxyName $proxyName -Role "gate-proxy" | kubectl apply -f -
    if ($LASTEXITCODE -ne 0) { throw "kubectl apply failed for $proxyName" }

    Write-Host "`nDeploying gate-$N (station: $ForStationID)..." -ForegroundColor Cyan
    Get-GateYAML -N $N -ForStationID $ForStationID | kubectl apply -f -
    if ($LASTEXITCODE -ne 0) { throw "kubectl apply failed for gate-$N" }
    Update-FirewallRules
    Write-Host "[+] gate-$N deployed (id=gate-$N, station-id=$ForStationID)." -ForegroundColor Green
}

function Remove-Gate([int]$N) {
    Write-Host "`nRemoving gate-$N..." -ForegroundColor Red
    kubectl delete deployment "gate-$N" -n $Namespace --ignore-not-found
    kubectl delete svc        "gate-$N" -n $Namespace --ignore-not-found
    kubectl delete deployment "gate-$N-proxy" -n $Namespace --ignore-not-found
    kubectl delete svc        "gate-$N-proxy" -n $Namespace --ignore-not-found
    Update-FirewallRules
    Write-Host "[+] gate-$N removed." -ForegroundColor Green
}

# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------

switch ($Action) {
    "list" {
        Show-List
    }
    "add-station" {
        if ($ID -le 0) { throw "-ID must be a positive integer (e.g. -ID 2)" }
        Add-Station -N $ID
    }
    "remove-station" {
        if ($ID -le 0) { throw "-ID must be a positive integer (e.g. -ID 2)" }
        Remove-Station -N $ID
    }
    "add-gate" {
        if ($ID -le 0)        { throw "-ID must be a positive integer (e.g. -ID 2)" }
        if ($StationID -le 0) { throw "-StationID must be a positive integer (e.g. -StationID 1)" }
        Add-Gate -N $ID -ForStationID $StationID
    }
    "remove-gate" {
        if ($ID -le 0) { throw "-ID must be a positive integer (e.g. -ID 2)" }
        Remove-Gate -N $ID
    }
}
