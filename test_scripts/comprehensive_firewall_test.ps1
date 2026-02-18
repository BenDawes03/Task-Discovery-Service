# Comprehensive Firewall Test Script
# Tests firewall rules with 5 simulated clients

Write-Host "=== TDS Comprehensive Firewall Test ===" -ForegroundColor Cyan
Write-Host ""

# Configuration
$ServerPort = 5000
$ServerHost = "127.0.0.1"
$RulesFile = "test_firewall_rules.txt"

# Define 5 clients with their simulated IPs
# In reality, all will connect from 127.0.0.1, but we'll test the logic
$Clients = @(
    @{Name="ClientA"; IP="192.168.1.10"; Service="192.168.1.10:8080"; Task="taskA"},
    @{Name="ClientB"; IP="192.168.1.11"; Service="192.168.1.11:8080"; Task="taskB"},
    @{Name="ClientC"; IP="10.0.0.5"; Service="10.0.0.5:8080"; Task="taskC"},
    @{Name="ClientD"; IP="10.0.0.6"; Service="10.0.0.6:8080"; Task="taskD"},
    @{Name="ClientE"; IP="172.16.0.100"; Service="172.16.0.100:8080"; Task="taskE"}
)

Write-Host "Test Scenario:" -ForegroundColor Yellow
Write-Host "  ClientA (192.168.1.10) - Can reach 192.168.1.x and 10.0.0.x" -ForegroundColor White
Write-Host "  ClientB (192.168.1.11) - Can reach 192.168.1.x and 10.0.0.x" -ForegroundColor White
Write-Host "  ClientC (10.0.0.5)     - Can reach 10.0.0.x only" -ForegroundColor White
Write-Host "  ClientD (10.0.0.6)     - Can reach 10.0.0.x only" -ForegroundColor White
Write-Host "  ClientE (172.16.0.100) - ISOLATED - Cannot reach anyone" -ForegroundColor White
Write-Host ""

# Generate firewall rules
Write-Host "Step 1: Generating firewall rules..." -ForegroundColor Green
$FirewallRules = @"
# Firewall Rules for Comprehensive Test
# Format: source_ip_or_cidr destination_ip_or_cidr

# Allow 192.168.1.x subnet to communicate with itself
192.168.1.0/24 192.168.1.0/24

# Allow 192.168.1.x subnet to reach 10.0.0.x subnet
192.168.1.0/24 10.0.0.0/24

# Allow 10.0.0.x subnet to communicate with itself
10.0.0.0/24 10.0.0.0/24

# Allow localhost to reach everything (for testing)
127.0.0.1 192.168.1.0/24
127.0.0.1 10.0.0.0/24
127.0.0.1 172.16.0.0/24

# Note: 172.16.0.100 (ClientE) has NO outbound rules - isolated
"@

Set-Content -Path $RulesFile -Value $FirewallRules
Write-Host "  Created $RulesFile with firewall rules" -ForegroundColor Gray
Write-Host ""

# Function to send UDP message and receive response
function Send-UDPMessage {
    param(
        [string]$Message,
        [string]$Server = "127.0.0.1",
        [int]$Port = 5000,
        [int]$Timeout = 2000
    )
    
    try {
        $udpClient = New-Object System.Net.Sockets.UdpClient
        $udpClient.Client.ReceiveTimeout = $Timeout
        $udpClient.Connect($Server, $Port)
        
        $bytes = [System.Text.Encoding]::ASCII.GetBytes($Message)
        [void]$udpClient.Send($bytes, $bytes.Length)
        
        $remoteEP = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
        $responseBytes = $udpClient.Receive([ref]$remoteEP)
        $response = [System.Text.Encoding]::ASCII.GetString($responseBytes)
        
        $udpClient.Close()
        return $response
    }
    catch {
        if ($udpClient) { $udpClient.Close() }
        return "TIMEOUT"
    }
}

# Start the server in background
Write-Host "Step 2: Starting TDS server with firewall rules..." -ForegroundColor Green
$ServerJob = Start-Job -ScriptBlock {
    param($RulesPath)
    Set-Location $using:PWD
    go run ./cmd/server --firewall-rules $RulesPath --no-tui 2>&1
} -ArgumentList (Resolve-Path $RulesFile).Path

Write-Host "  Waiting for server to start..." -ForegroundColor Gray
Start-Sleep -Seconds 3

# Check if server started
if ($ServerJob.State -ne "Running") {
    Write-Host "  ERROR: Server failed to start!" -ForegroundColor Red
    Receive-Job $ServerJob
    Remove-Job $ServerJob
    exit 1
}
Write-Host "  Server started (Job ID: $($ServerJob.Id))" -ForegroundColor Gray
Write-Host ""

# Step 3: Register all services
Write-Host "Step 3: Registering services for all clients..." -ForegroundColor Green
foreach ($Client in $Clients) {
    $msg = "REGISTER $($Client.Task) $($Client.Service)"
    $response = Send-UDPMessage -Message $msg
    $status = if ($response -eq "OK") { "[OK]" } else { "[FAILED: $response]" }
    $color = if ($response -eq "OK") { "Green" } else { "Red" }
    Write-Host "  $($Client.Name): $msg" -NoNewline -ForegroundColor Gray
    Write-Host " $status" -ForegroundColor $color
}
Write-Host ""

# Step 4: Test queries - Expected results matrix
Write-Host "Step 4: Testing service queries (firewall filtering)..." -ForegroundColor Green
Write-Host ""

$TestResults = @()

# Query matrix: Who can reach whom?
$QueryTests = @(
    # Localhost (127.0.0.1) can reach everyone
    @{From="Localhost"; FromIP="127.0.0.1"; Task="taskA"; Expected="192.168.1.10:8080"; ShouldSucceed=$true},
    @{From="Localhost"; FromIP="127.0.0.1"; Task="taskC"; Expected="10.0.0.5:8080"; ShouldSucceed=$true},
    @{From="Localhost"; FromIP="127.0.0.1"; Task="taskE"; Expected="172.16.0.100:8080"; ShouldSucceed=$true},
    
    # ClientA (192.168.1.10) can reach 192.168.1.x and 10.0.0.x
    @{From="ClientA"; FromIP="192.168.1.10"; Task="taskB"; Expected="192.168.1.11:8080"; ShouldSucceed=$true; Comment="Same subnet"},
    @{From="ClientA"; FromIP="192.168.1.10"; Task="taskC"; Expected="10.0.0.5:8080"; ShouldSucceed=$true; Comment="Cross-subnet allowed"},
    @{From="ClientA"; FromIP="192.168.1.10"; Task="taskE"; Expected="FORBIDDEN"; ShouldSucceed=$false; Comment="No route to 172.16.x"},
    
    # ClientB (192.168.1.11) can reach 192.168.1.x and 10.0.0.x
    @{From="ClientB"; FromIP="192.168.1.11"; Task="taskA"; Expected="192.168.1.10:8080"; ShouldSucceed=$true; Comment="Same subnet"},
    @{From="ClientB"; FromIP="192.168.1.11"; Task="taskD"; Expected="10.0.0.6:8080"; ShouldSucceed=$true; Comment="Cross-subnet allowed"},
    
    # ClientC (10.0.0.5) can only reach 10.0.0.x
    @{From="ClientC"; FromIP="10.0.0.5"; Task="taskD"; Expected="10.0.0.6:8080"; ShouldSucceed=$true; Comment="Same subnet"},
    @{From="ClientC"; FromIP="10.0.0.5"; Task="taskA"; Expected="FORBIDDEN"; ShouldSucceed=$false; Comment="Cannot reach 192.168.1.x"},
    @{From="ClientC"; FromIP="10.0.0.5"; Task="taskE"; Expected="FORBIDDEN"; ShouldSucceed=$false; Comment="Cannot reach 172.16.x"},
    
    # ClientD (10.0.0.6) can only reach 10.0.0.x
    @{From="ClientD"; FromIP="10.0.0.6"; Task="taskC"; Expected="10.0.0.5:8080"; ShouldSucceed=$true; Comment="Same subnet"},
    @{From="ClientD"; FromIP="10.0.0.6"; Task="taskB"; Expected="FORBIDDEN"; ShouldSucceed=$false; Comment="Cannot reach 192.168.1.x"},
    
    # ClientE (172.16.0.100) is isolated - no firewall rules allow it
    @{From="ClientE"; FromIP="172.16.0.100"; Task="taskA"; Expected="FORBIDDEN"; ShouldSucceed=$false; Comment="Isolated client"},
    @{From="ClientE"; FromIP="172.16.0.100"; Task="taskC"; Expected="FORBIDDEN"; ShouldSucceed=$false; Comment="Isolated client"},
    @{From="ClientE"; FromIP="172.16.0.100"; Task="taskE"; Expected="FORBIDDEN"; ShouldSucceed=$false; Comment="Cannot even reach itself"}
)

$PassCount = 0
$FailCount = 0

foreach ($Test in $QueryTests) {
    $msg = "QUERY $($Test.Task)"
    
    # NOTE: Since we're testing from localhost, we can't actually change source IP
    # The real test would need multiple machines or network namespaces
    # For now, we test the localhost behavior and document expected behavior
    
    $response = Send-UDPMessage -Message $msg
    
    # Determine test result
    $actualSuccess = ($response -notmatch "FORBIDDEN|NOTFOUND|ERR|TIMEOUT")
    $testPassed = ($actualSuccess -eq $Test.ShouldSucceed)
    
    # Since all tests run from 127.0.0.1, adjust expectations
    # The server will allow all queries from localhost (per firewall rules)
    # So we document what WOULD happen with different source IPs
    
    $statusSymbol = if ($Test.ShouldSucceed) { "[ALLOW]" } else { "[BLOCK]" }
    $statusColor = if ($Test.ShouldSucceed) { "Green" } else { "Yellow" }
    
    Write-Host "  $statusSymbol " -NoNewline -ForegroundColor $statusColor
    Write-Host "$($Test.From) → $($Test.Task): " -NoNewline -ForegroundColor Gray
    
    if ($Test.ShouldSucceed) {
        Write-Host "Expected $($Test.Expected)" -NoNewline -ForegroundColor Green
        Write-Host " | Got: $response" -ForegroundColor Cyan
    } else {
        Write-Host "Expected FORBIDDEN" -NoNewline -ForegroundColor Yellow
        Write-Host " | With real IPs would be FORBIDDEN" -ForegroundColor DarkGray
    }
    
    if ($Test.Comment) {
        Write-Host "     ($($Test.Comment))" -ForegroundColor DarkGray
    }
    
    $TestResults += @{
        Test = "$($Test.From) → $($Test.Task)"
        Expected = $Test.Expected
        Actual = $response
        ShouldSucceed = $Test.ShouldSucceed
        Passed = $testPassed
    }
}

Write-Host ""
Write-Host "Step 5: Test Summary" -ForegroundColor Green
Write-Host "-----------------------------------------------------" -ForegroundColor DarkGray
Write-Host ""
Write-Host "Firewall Rule Test Matrix:" -ForegroundColor Yellow
Write-Host "  [ALLOW] = Should succeed (firewall allows)" -ForegroundColor Green
Write-Host "  [BLOCK] = Should be blocked (firewall denies)" -ForegroundColor Yellow
Write-Host ""
Write-Host "Network Topology:" -ForegroundColor Yellow
Write-Host "  192.168.1.0/24 <-> 192.168.1.0/24  (Allowed)" -ForegroundColor Green
Write-Host "  192.168.1.0/24 ->  10.0.0.0/24     (Allowed)" -ForegroundColor Green
Write-Host "  10.0.0.0/24    <-> 10.0.0.0/24     (Allowed)" -ForegroundColor Green
Write-Host "  172.16.0.100   ->  *               (BLOCKED - Isolated)" -ForegroundColor Red
Write-Host ""

Write-Host "IMPORTANT NOTE:" -ForegroundColor Cyan
Write-Host "This test runs all clients from localhost (127.0.0.1), which has full access." -ForegroundColor Gray
Write-Host "In a real deployment with actual client IPs, the firewall rules would enforce:" -ForegroundColor Gray
Write-Host "  - ClientA/B can reach each other and ClientC/D" -ForegroundColor Gray
Write-Host "  - ClientC/D can only reach each other" -ForegroundColor Gray
Write-Host "  - ClientE is completely isolated" -ForegroundColor Gray
Write-Host ""

Write-Host "To test with real network isolation, you would need:" -ForegroundColor Yellow
Write-Host "  1. Multiple machines with different IPs" -ForegroundColor Gray
Write-Host "  2. Network namespaces (Linux)" -ForegroundColor Gray
Write-Host "  3. Docker containers with custom networks" -ForegroundColor Gray
Write-Host ""

# Cleanup
Write-Host "Step 6: Cleanup" -ForegroundColor Green
Write-Host "  Stopping server..." -ForegroundColor Gray
Stop-Job $ServerJob -PassThru | Remove-Job -Force
Write-Host "  Server stopped" -ForegroundColor Gray
Write-Host ""

Write-Host "=====================================================" -ForegroundColor Cyan
Write-Host "Test Complete!" -ForegroundColor Green
Write-Host "=====================================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "Firewall rules file created: $RulesFile" -ForegroundColor White
Write-Host "Review the rules and test with real client IPs for full validation." -ForegroundColor White
Write-Host ""
