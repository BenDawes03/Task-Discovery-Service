# Test P2P Network with 5 Client Proxies
# This script starts 5 client proxies in P2P mode and fills them with registrations

# Navigate to project root
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$projectRoot = Split-Path -Parent (Split-Path -Parent $scriptDir)
Set-Location $projectRoot

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "P2P Network Test - 5 Node Cluster" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

# Configuration
$NumNodes = 5
$BaseP2PPort = 6000
$BaseProxyPort = 5100
$BootstrapDelay = 3  # seconds between node starts
$PropagationDelay = 5  # seconds to wait for DHT propagation

# Arrays to store process info
$ProxyProcesses = @()
$NodeAddresses = @()
$StartupScripts = @()

# Directory for generated per-node startup scripts (keeps repo root clean)
$GeneratedDir = ".\test_scripts\p2p\generated"
if (!(Test-Path $GeneratedDir)) {
    New-Item -ItemType Directory -Path $GeneratedDir | Out-Null
}

# Build the executables first
Write-Host "[BUILD] Building executables..." -ForegroundColor Yellow
go build -o bin\client_proxy.exe ./cmd/client_proxy
go build -o bin\client_demo.exe ./cmd/client_demo
if ($LASTEXITCODE -ne 0) {
    Write-Host "[ERROR] Build failed!" -ForegroundColor Red
    exit 1
}
Write-Host "[BUILD] Build successful!" -ForegroundColor Green
Write-Host ""

# Function to start a client proxy
function Start-ClientProxy {
    param (
        [int]$NodeId,
        [string]$P2PPort,
        [string]$ProxyPort,
        [string]$Bootstrap
    )
    
    # Create a startup script for this node
    $startupScript = @"
`$env:TDS_PROXY_LISTEN = ":$ProxyPort"
if ("$Bootstrap" -eq "") {
    .\bin\client_proxy.exe -p2p -p2p-port :$P2PPort
} else {
    .\bin\client_proxy.exe -p2p -p2p-port :$P2PPort -bootstrap $Bootstrap
}
"@
    
    $scriptPath = Join-Path $GeneratedDir "node_${NodeId}_start.ps1"
    $startupScript | Out-File -FilePath $scriptPath -Encoding ASCII
    
    if ($Bootstrap -eq "") {
        Write-Host "[NODE $NodeId] Starting bootstrap node on P2P port $P2PPort, proxy port $ProxyPort" -ForegroundColor Green
    } else {
        Write-Host "[NODE $NodeId] Starting node on P2P port $P2PPort, proxy port $ProxyPort, bootstrap: $Bootstrap" -ForegroundColor Green
    }
    
    # Start in a new window with custom title
    $process = Start-Process -FilePath "powershell.exe" -ArgumentList "-NoExit", "-ExecutionPolicy", "Bypass", "-File", $scriptPath -PassThru -WindowStyle Normal
    
    return @{Process=$process; ScriptPath=$scriptPath}
}

# Function to send registration
function Send-Registration {
    param (
        [string]$ProxyAddr,
        [string]$Task,
        [string]$ServiceAddr
    )
    
    $message = "REGISTER $Task $ServiceAddr"
    $bytes = [System.Text.Encoding]::ASCII.GetBytes($message)
    
    try {
        $udpClient = New-Object System.Net.Sockets.UdpClient
        $udpClient.Client.ReceiveTimeout = 5000
        $udpClient.Connect("127.0.0.1", [int]$ProxyAddr)
        $udpClient.Send($bytes, $bytes.Length) | Out-Null
        
        $remoteEP = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
        $response = $udpClient.Receive([ref]$remoteEP)
        $responseText = [System.Text.Encoding]::ASCII.GetString($response)
        $udpClient.Close()
        
        return $responseText
    } catch {
        Write-Host "[ERROR] Failed to register: $_" -ForegroundColor Red
        return "ERROR"
    }
}

# Function to send query
function Send-Query {
    param (
        [string]$ProxyAddr,
        [string]$Task
    )
    
    $message = "QUERY $Task"
    $bytes = [System.Text.Encoding]::ASCII.GetBytes($message)
    
    try {
        $udpClient = New-Object System.Net.Sockets.UdpClient
        $udpClient.Client.ReceiveTimeout = 5000
        $udpClient.Connect("127.0.0.1", [int]$ProxyAddr)
        $udpClient.Send($bytes, $bytes.Length) | Out-Null
        
        $remoteEP = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
        $response = $udpClient.Receive([ref]$remoteEP)
        $responseText = [System.Text.Encoding]::ASCII.GetString($response)
        $udpClient.Close()
        
        return $responseText
    } catch {
        Write-Host "[ERROR] Failed to query: $_" -ForegroundColor Red
        return "ERROR"
    }
}

# Cleanup function
function Cleanup {
    Write-Host ""
    Write-Host "[CLEANUP] Stopping all proxy processes..." -ForegroundColor Yellow
    foreach ($proc in $ProxyProcesses) {
        if ($proc -and !$proc.HasExited) {
            Stop-Process -Id $proc.Id -Force -ErrorAction SilentlyContinue
        }
    }
    
    # Clean up startup scripts
    foreach ($scriptPath in $StartupScripts) {
        if (Test-Path $scriptPath) {
            Remove-Item $scriptPath -Force -ErrorAction SilentlyContinue
        }
    }

    # Remove generated dir if empty
    try {
        if (Test-Path $GeneratedDir) {
            $remaining = Get-ChildItem -Path $GeneratedDir -ErrorAction SilentlyContinue
            if ($null -eq $remaining -or $remaining.Count -eq 0) {
                Remove-Item $GeneratedDir -Force -ErrorAction SilentlyContinue
            }
        }
    } catch {
        # best-effort cleanup only
    }
    
    Write-Host "[CLEANUP] All processes stopped" -ForegroundColor Green
}

# Register cleanup on script exit
trap {
    Cleanup
    break
}

# Start the nodes
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "PHASE 1: Starting P2P Nodes" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

# Start bootstrap node (Node 1)
$p2pPort = $BaseP2PPort
$proxyPort = $BaseProxyPort
$result = Start-ClientProxy -NodeId 1 -P2PPort $p2pPort -ProxyPort $proxyPort -Bootstrap ""
$ProxyProcesses += $result.Process
$StartupScripts += $result.ScriptPath
$NodeAddresses += "127.0.0.1:$p2pPort"
Start-Sleep -Seconds $BootstrapDelay

# Start remaining nodes
for ($i = 2; $i -le $NumNodes; $i++) {
    $p2pPort = $BaseP2PPort + $i - 1
    $proxyPort = $BaseProxyPort + $i - 1
    $bootstrap = "127.0.0.1:$BaseP2PPort"  # All nodes bootstrap to the first node
    
    $result = Start-ClientProxy -NodeId $i -P2PPort $p2pPort -ProxyPort $proxyPort -Bootstrap $bootstrap
    $ProxyProcesses += $result.Process
    $StartupScripts += $result.ScriptPath
    $NodeAddresses += "127.0.0.1:$p2pPort"
    Start-Sleep -Seconds $BootstrapDelay
}

Write-Host ""
Write-Host "[INFO] Waiting $PropagationDelay seconds for DHT to stabilize..." -ForegroundColor Yellow
Start-Sleep -Seconds $PropagationDelay

# Verify nodes are running
Write-Host "[INFO] Verifying nodes are running..." -ForegroundColor Yellow
$runningCount = 0
for ($i = 0; $i -lt $ProxyProcesses.Count; $i++) {
    if (!$ProxyProcesses[$i].HasExited) {
        $runningCount++
        Write-Host "  Node $($i+1): Running" -ForegroundColor Green
    } else {
        Write-Host "  Node $($i+1): NOT RUNNING" -ForegroundColor Red
    }
}
Write-Host "[INFO] $runningCount of $NumNodes nodes are running" -ForegroundColor $(if ($runningCount -eq $NumNodes) {"Green"} else {"Yellow"})
Write-Host ""

# Register services on different nodes
Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "PHASE 2: Registering Services" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

$Services = @(
    @{Task="web-api"; Addresses=@("192.168.1.10:8080", "192.168.1.11:8080", "192.168.1.12:8080")},
    @{Task="auth-service"; Addresses=@("192.168.1.20:9000", "192.168.1.21:9000")},
    @{Task="database"; Addresses=@("192.168.1.30:5432", "192.168.1.31:5432", "192.168.1.32:5432", "192.168.1.33:5432")},
    @{Task="cache-service"; Addresses=@("192.168.1.40:6379", "192.168.1.41:6379")},
    @{Task="message-queue"; Addresses=@("192.168.1.50:5672")},
    @{Task="file-storage"; Addresses=@("192.168.1.60:9000", "192.168.1.61:9000", "192.168.1.62:9000")},
    @{Task="analytics"; Addresses=@("192.168.1.70:3000")},
    @{Task="monitoring"; Addresses=@("192.168.1.80:9090", "192.168.1.81:9090")},
    @{Task="logging"; Addresses=@("192.168.1.90:5000", "192.168.1.91:5000", "192.168.1.92:5000")},
    @{Task="notification"; Addresses=@("192.168.1.100:8000")}
)

$TotalRegistrations = 0
foreach ($service in $Services) {
    Write-Host "[REGISTER] Task: $($service.Task)" -ForegroundColor Cyan
    
    foreach ($address in $service.Addresses) {
        # Round-robin across nodes for registration
        $nodeIndex = $TotalRegistrations % $NumNodes
        $proxyPort = $BaseProxyPort + $nodeIndex
        
        Write-Host "  -> Registering $address on Node $($nodeIndex + 1) (port $proxyPort)" -ForegroundColor White
        $response = Send-Registration -ProxyAddr $proxyPort -Task $service.Task -ServiceAddr $address
        Write-Host "     Response: $response" -ForegroundColor Gray
        
        $TotalRegistrations++
        Start-Sleep -Milliseconds 200
    }
}

Write-Host ""
Write-Host "[INFO] Total registrations: $TotalRegistrations" -ForegroundColor Green
Write-Host "[INFO] Waiting $PropagationDelay seconds for DHT propagation..." -ForegroundColor Yellow
Start-Sleep -Seconds $PropagationDelay

# Query services from different nodes
Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "PHASE 3: Querying Services from All Nodes" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

$TestTasks = @("web-api", "database", "auth-service", "cache-service", "logging")

foreach ($task in $TestTasks) {
    Write-Host "[QUERY] Task: $task" -ForegroundColor Cyan
    
    for ($i = 0; $i -lt $NumNodes; $i++) {
        $proxyPort = $BaseProxyPort + $i
        $nodeId = $i + 1
        
        Write-Host "  -> Querying from Node $nodeId (port $proxyPort)" -ForegroundColor White
        $response = Send-Query -ProxyAddr $proxyPort -Task $task
        Write-Host "     Response: $response" -ForegroundColor Gray
        Start-Sleep -Milliseconds 200
    }
    Write-Host ""
}

# Summary
Write-Host ""
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Test Summary" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Nodes Started:        $NumNodes" -ForegroundColor Green
Write-Host "Services Registered:  $($Services.Count)" -ForegroundColor Green
Write-Host "Total Registrations:  $TotalRegistrations" -ForegroundColor Green
Write-Host "Queries Performed:    $($TestTasks.Count * $NumNodes)" -ForegroundColor Green
Write-Host ""

# Keep running and allow manual interaction
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Network is running!" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "Node ports:" -ForegroundColor Yellow
for ($i = 0; $i -lt $NumNodes; $i++) {
    $p2pPort = $BaseP2PPort + $i
    $proxyPort = $BaseProxyPort + $i
    Write-Host "  Node $($i+1): P2P=:$p2pPort, Proxy=:$proxyPort" -ForegroundColor White
}
Write-Host ""
Write-Host "You can now manually test with client_demo:" -ForegroundColor Yellow
Write-Host "  .\bin\client_demo.exe -mode p2p -server localhost:5100" -ForegroundColor White
Write-Host "  .\bin\client_demo.exe -mode p2p -server localhost:5101" -ForegroundColor White
Write-Host "  etc..." -ForegroundColor White
Write-Host ""
Write-Host "Press Ctrl+C to stop all nodes and exit" -ForegroundColor Yellow
Write-Host ""

# Wait for user interruption
try {
    while ($true) {
        Start-Sleep -Seconds 1
        
        # Check if any process has died
        for ($i = 0; $i -lt $ProxyProcesses.Count; $i++) {
            if ($ProxyProcesses[$i].HasExited) {
                Write-Host "[WARNING] Node $($i+1) has exited unexpectedly" -ForegroundColor Red
            }
        }
    }
} finally {
    Cleanup
}
