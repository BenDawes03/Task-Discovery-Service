# Docker-based Comprehensive Firewall Test
# Uses Docker networks to simulate different client IPs

Write-Host "=== TDS Docker-Based Firewall Test ===" -ForegroundColor Cyan
Write-Host ""

# Check if Docker is available
$dockerAvailable = $false
try {
    $null = docker --version 2>$null
    $dockerAvailable = $true
} catch {
    Write-Host "Docker is not available. This test requires Docker." -ForegroundColor Red
    Write-Host "Install Docker Desktop from: https://www.docker.com/products/docker-desktop" -ForegroundColor Yellow
    Write-Host ""
    Write-Host "Falling back to localhost-based test..." -ForegroundColor Yellow
    Write-Host ""
    exit 1
}

Write-Host "Docker detected. Setting up test environment..." -ForegroundColor Green
Write-Host ""

# Configuration
$NetworkName = "tds-test-network"
$ServerContainer = "tds-server"
$ServerIP = "10.0.0.10"
$RulesFile = "test_firewall_rules.txt"

# Define test clients with their IPs
$Clients = @(
    @{Name="client-a"; IP="10.0.0.20"; Service="10.0.0.20:8080"; Task="taskA"; Subnet="10.0.0.0/24"},
    @{Name="client-b"; IP="10.0.0.21"; Service="10.0.0.21:8080"; Task="taskB"; Subnet="10.0.0.0/24"},
    @{Name="client-c"; IP="10.0.0.30"; Service="10.0.0.30:8080"; Task="taskC"; Subnet="10.0.0.0/24"},
    @{Name="client-d"; IP="10.0.0.31"; Service="10.0.0.31:8080"; Task="taskD"; Subnet="10.0.0.0/24"},
    @{Name="client-e"; IP="10.0.0.40"; Service="10.0.0.40:8080"; Task="taskE"; Subnet="10.0.0.0/24"}
)

# Generate firewall rules for Docker environment
Write-Host "Creating firewall rules..." -ForegroundColor Yellow
$FirewallRules = @"
# Docker Test Firewall Rules
# Subnet is 10.0.0.0/24, so we use last octet to distinguish clients

# Client A (10.0.0.20) and Client B (10.0.0.21) can reach each other and C/D
10.0.0.20 10.0.0.20
10.0.0.20 10.0.0.21
10.0.0.20 10.0.0.30
10.0.0.20 10.0.0.31

10.0.0.21 10.0.0.20
10.0.0.21 10.0.0.21
10.0.0.21 10.0.0.30
10.0.0.21 10.0.0.31

# Client C (10.0.0.30) and Client D (10.0.0.31) can only reach each other
10.0.0.30 10.0.0.30
10.0.0.30 10.0.0.31

10.0.0.31 10.0.0.30
10.0.0.31 10.0.0.31

# Client E (10.0.0.40) is ISOLATED - no rules
"@

Set-Content -Path $RulesFile -Value $FirewallRules
Write-Host "  Firewall rules created" -ForegroundColor Gray
Write-Host ""

# Build Dockerfile for testing
Write-Host "Creating Dockerfile..." -ForegroundColor Yellow
$Dockerfile = @"
FROM golang:1.21-alpine
WORKDIR /app
RUN apk add --no-cache netcat-openbsd bash
COPY . .
RUN go build -o /usr/local/bin/tds-server ./cmd/server
CMD ["/usr/local/bin/tds-server"]
"@
Set-Content -Path "Dockerfile.test" -Value $Dockerfile

# Create test client script
$ClientScript = @"
#!/bin/bash
# Client script to test TDS server

SERVER_IP=`$1
SERVER_PORT=5000
TASK=`$2
SERVICE_ADDR=`$3

echo "Registering service: `$TASK -> `$SERVICE_ADDR"
echo "REGISTER `$TASK `$SERVICE_ADDR" | nc -u -w 1 `$SERVER_IP `$SERVER_PORT

sleep 2

echo "Querying all tasks..."
for t in taskA taskB taskC taskD taskE; do
    echo -n "  `$t: "
    echo "QUERY `$t" | nc -u -w 1 `$SERVER_IP `$SERVER_PORT
done
"@
Set-Content -Path "client_test.sh" -Value $ClientScript -NoNewline

Write-Host "  Build files created" -ForegroundColor Gray
Write-Host ""

Write-Host "Building Docker image..." -ForegroundColor Yellow
docker build -t tds-test:latest -f Dockerfile.test . 2>&1 | Out-Null
if ($LASTEXITCODE -ne 0) {
    Write-Host "  Failed to build Docker image" -ForegroundColor Red
    exit 1
}
Write-Host "  Image built successfully" -ForegroundColor Gray
Write-Host ""

# Cleanup function
function Cleanup-TestEnvironment {
    Write-Host "Cleaning up Docker resources..." -ForegroundColor Yellow
    docker rm -f $ServerContainer 2>$null | Out-Null
    foreach ($Client in $Clients) {
        docker rm -f $Client.Name 2>$null | Out-Null
    }
    docker network rm $NetworkName 2>$null | Out-Null
    Write-Host "  Cleanup complete" -ForegroundColor Gray
}

# Cleanup any existing test resources
Cleanup-TestEnvironment

Write-Host ""
Write-Host "Setting up Docker network..." -ForegroundColor Green
docker network create --subnet=10.0.0.0/24 $NetworkName 2>&1 | Out-Null
Write-Host "  Network created: $NetworkName (10.0.0.0/24)" -ForegroundColor Gray
Write-Host ""

Write-Host "Starting TDS server..." -ForegroundColor Green
docker run -d `
    --name $ServerContainer `
    --network $NetworkName `
    --ip $ServerIP `
    -v "${PWD}:/app" `
    -w /app `
    tds-test:latest `
    /usr/local/bin/tds-server --firewall-rules /app/$RulesFile --no-tui 2>&1 | Out-Null

Start-Sleep -Seconds 3
$serverRunning = docker ps --filter "name=$ServerContainer" --format "{{.Names}}"
if ($serverRunning -ne $ServerContainer) {
    Write-Host "  ERROR: Server failed to start" -ForegroundColor Red
    docker logs $ServerContainer
    Cleanup-TestEnvironment
    exit 1
}
Write-Host "  Server started at $ServerIP" -ForegroundColor Gray
Write-Host ""

Write-Host "Test will demonstrate:" -ForegroundColor Cyan
Write-Host "  [ALLOW] Client A/B can reach A/B/C/D" -ForegroundColor Green
Write-Host "  [ALLOW] Client C/D can only reach C/D" -ForegroundColor Green
Write-Host "  [BLOCK] Client E is isolated" -ForegroundColor Red
Write-Host ""
Write-Host "Press Enter to continue..."
Read-Host

Write-Host ""
Write-Host "===============================================" -ForegroundColor Cyan
Write-Host "Starting Client Tests" -ForegroundColor Cyan
Write-Host "===============================================" -ForegroundColor Cyan
Write-Host ""

# Register all services first
Write-Host "Registering all client services..." -ForegroundColor Yellow
foreach ($Client in $Clients) {
    $cmd = "echo 'REGISTER $($Client.Task) $($Client.Service)' | nc -u -w 1 $ServerIP 5000"
    docker run --rm --network $NetworkName --ip $Client.IP alpine sh -c "apk add --no-cache netcat-openbsd > /dev/null 2>&1; $cmd"
}
Write-Host "  All services registered" -ForegroundColor Gray
Write-Host ""

Start-Sleep -Seconds 2

# Test each client
foreach ($Client in $Clients) {
    Write-Host "---------------------------------------------" -ForegroundColor DarkGray
    Write-Host "$($Client.Name.ToUpper()) ($($Client.IP)) Querying Services:" -ForegroundColor Yellow
    Write-Host ""
    
    foreach ($TargetClient in $Clients) {
        Write-Host "  $($TargetClient.Task): " -NoNewline
        
        $cmd = "echo 'QUERY $($TargetClient.Task)' | nc -u -w 1 $ServerIP 5000"
        $result = docker run --rm --network $NetworkName --ip $Client.IP alpine sh -c "apk add --no-cache netcat-openbsd > /dev/null 2>&1; $cmd" 2>$null
        
        if ($result -match "FORBIDDEN") {
            Write-Host "FORBIDDEN" -ForegroundColor Red
        } elseif ($result -match "NOTFOUND") {
            Write-Host "NOTFOUND" -ForegroundColor Yellow
        } elseif ($result) {
            Write-Host "$result" -ForegroundColor Green
        } else {
            Write-Host "TIMEOUT" -ForegroundColor DarkGray
        }
    }
    Write-Host ""
}

Write-Host "===============================================" -ForegroundColor Cyan
Write-Host "Test Complete!" -ForegroundColor Green
Write-Host "===============================================" -ForegroundColor Cyan
Write-Host ""

Write-Host "Server logs:" -ForegroundColor Yellow
docker logs $ServerContainer --tail 20
Write-Host ""

# Cleanup
Cleanup-TestEnvironment

Write-Host ""
Write-Host "Test environment cleaned up." -ForegroundColor Green
Write-Host ""
