# Test Firewall Functionality Script
# This script demonstrates how firewall rules work with the TDS server

Write-Host "=== TDS Firewall Rules Test ===" -ForegroundColor Cyan
Write-Host ""

# Create a test firewall rules file
$rulesFile = "firewall_rules_test.txt"
$rulesContent = @"
# Test firewall rules
# Allow localhost to localhost
127.0.0.1 127.0.0.1

# Allow local network
192.168.1.0/24 10.0.0.0/24
"@

Write-Host "Creating test firewall rules file: $rulesFile" -ForegroundColor Yellow
Set-Content -Path $rulesFile -Value $rulesContent
Write-Host "Rules file created:" -ForegroundColor Green
Get-Content $rulesFile | ForEach-Object { Write-Host "  $_" }
Write-Host ""

Write-Host "Starting TDS server with firewall rules..." -ForegroundColor Yellow
Write-Host "Command: go run ./cmd/server --firewall-rules $rulesFile --no-tui" -ForegroundColor Gray
Write-Host ""
Write-Host "The server will:" -ForegroundColor Cyan
Write-Host "  1. Load firewall rules from $rulesFile" -ForegroundColor White
Write-Host "  2. Filter service responses based on requestor IP" -ForegroundColor White
Write-Host "  3. Return FORBIDDEN if no allowed services exist" -ForegroundColor White
Write-Host ""

Write-Host "To test manually:" -ForegroundColor Yellow
Write-Host "  1. Start the server: go run ./cmd/server --firewall-rules $rulesFile --no-tui" -ForegroundColor White
Write-Host "  2. Register a service: echo 'REGISTER task1 127.0.0.1:8080' | nc -u localhost 5000" -ForegroundColor White
Write-Host "  3. Query from localhost: echo 'QUERY task1' | nc -u localhost 5000" -ForegroundColor White
Write-Host "     Expected: 127.0.0.1:8080 (allowed by firewall)" -ForegroundColor Green
Write-Host ""
Write-Host "  4. Register another service: echo 'REGISTER task1 10.0.0.5:8080' | nc -u localhost 5000" -ForegroundColor White
Write-Host "  5. Query again: echo 'QUERY task1' | nc -u localhost 5000" -ForegroundColor White
Write-Host "     Expected: Only 127.0.0.1:8080 returned (10.0.0.5 blocked by firewall)" -ForegroundColor Green
Write-Host ""

Write-Host "Press Enter to start the server or Ctrl+C to exit..." -ForegroundColor Yellow
Read-Host

# Start the server (UDP is the default transport)
go run ./cmd/server --firewall-rules $rulesFile --no-tui
