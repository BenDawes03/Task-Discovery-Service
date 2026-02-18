# Generate Firewall Rules Script
# This script helps generate firewall rules based on your network topology

Write-Host "=== TDS Firewall Rules Generator ===" -ForegroundColor Cyan
Write-Host ""

# Interactive mode
Write-Host "This script will help you create firewall rules for TDS." -ForegroundColor Yellow
Write-Host ""

$rules = @()

Write-Host "Enter firewall rules (press Enter with empty input to finish):" -ForegroundColor Green
Write-Host "Format: <source_ip_or_cidr> <destination_ip_or_cidr>" -ForegroundColor Gray
Write-Host "Example: 192.168.1.10 10.0.0.5" -ForegroundColor Gray
Write-Host "Example: 192.168.1.0/24 10.0.0.0/24" -ForegroundColor Gray
Write-Host ""

$ruleNum = 1
while ($true) {
    $input = Read-Host "Rule $ruleNum"
    if ([string]::IsNullOrWhiteSpace($input)) {
        break
    }
    
    # Validate format
    $parts = $input -split '\s+'
    if ($parts.Count -ne 2) {
        Write-Host "  Invalid format! Expected: <source> <destination>" -ForegroundColor Red
        continue
    }
    
    $rules += $input
    $ruleNum++
}

if ($rules.Count -eq 0) {
    Write-Host ""
    Write-Host "No rules entered. Creating example rules file..." -ForegroundColor Yellow
    $rules = @(
        "# Example firewall rules",
        "",
        "# Allow localhost to localhost",
        "127.0.0.1 127.0.0.1",
        "",
        "# Allow local network clients to access server network",
        "192.168.1.0/24 10.0.0.0/24",
        "",
        "# Allow specific client to specific server",
        "192.168.1.100 10.0.0.5"
    )
}

# Output file
$outputFile = "firewall_rules.txt"
Write-Host ""
$customFile = Read-Host "Output file name [firewall_rules.txt]"
if (-not [string]::IsNullOrWhiteSpace($customFile)) {
    $outputFile = $customFile
}

# Write rules to file
Set-Content -Path $outputFile -Value ($rules -join "`n")

Write-Host ""
Write-Host "Firewall rules saved to: $outputFile" -ForegroundColor Green
Write-Host ""
Write-Host "Contents:" -ForegroundColor Cyan
Get-Content $outputFile | ForEach-Object { 
    if ($_ -match '^#' -or [string]::IsNullOrWhiteSpace($_)) {
        Write-Host "  $_" -ForegroundColor DarkGray
    } else {
        Write-Host "  $_" -ForegroundColor White
    }
}

Write-Host ""
Write-Host "To use these rules, start the server with:" -ForegroundColor Yellow
Write-Host "  go run ./cmd/server --firewall-rules $outputFile" -ForegroundColor White
Write-Host ""

# Ask if user wants to start the server
$start = Read-Host "Start server with these rules now? [y/N]"
if ($start -eq 'y' -or $start -eq 'Y') {
    Write-Host ""
    Write-Host "Starting server..." -ForegroundColor Green
    go run ./cmd/server --firewall-rules $outputFile
}
