# seed_tickets.ps1
# Continuously seeds CS with tickets that depart in the next 15 minutes.
# This ensures distributors will fetch new manifests every manifest-interval (default 5 minutes).
#
# Usage:
#   .\seed_tickets.ps1 -CSBaseUrl "http://localhost:9101" -IntervalSeconds 300 -TicketsPerBatch 5
#

param(
    [string]$CSBaseUrl = "http://localhost:9101",
    [int]$IntervalSeconds = 300,  # 5 minutes = 300 seconds
    [int]$TicketsPerBatch = 5,
    [int]$DurationMinutes = 120   # how long to run (default 2 hours for testing)
)

$ErrorActionPreference = "Stop"

# Stations available in the demo
$stations = @(
    "station-1",
    "station-2",
    "station-3"
)

# Sample passengers
$passengers = @(
    "Alice",
    "Bob",
    "Carol",
    "David",
    "Eve",
    "Frank",
    "Grace",
    "Henry"
)

function New-TicketJSON {
    param(
        [int]$Index,
        [string]$StationId
    )
    
    # Train departs 1-14 minutes from now (ensures it falls in the 15-minute window)
    $departureMinutes = (Get-Random -Minimum 1 -Maximum 15)
    $trainTime = (Get-Date).AddMinutes($departureMinutes).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ss.ffffffZ")
    
    $passenger = $passengers[(Get-Random -Minimum 0 -Maximum $passengers.Count)]
    
    $body = @{
        station_id = $StationId
        train_time = $trainTime
        passenger = "$passenger-$Index"
    } | ConvertTo-Json -Compress
    
    return $body
}

Write-Host "Ticket Seeding Script"
Write-Host "===================="
Write-Host "CS URL: $CSBaseUrl"
Write-Host "Seed interval: $IntervalSeconds seconds"
Write-Host "Tickets per batch: $TicketsPerBatch"
Write-Host "Duration: $DurationMinutes minutes"
Write-Host ""

$startTime = Get-Date
$endTime = $startTime.AddMinutes($DurationMinutes)
$ticketCount = 0

try {
    Write-Host "Testing connection to CS..."
    $healthResp = Invoke-RestMethod -Method Get -Uri "$CSBaseUrl/health" -ErrorAction SilentlyContinue
    Write-Host "CS is online."
} catch {
    Write-Host "ERROR: Could not reach CS at $CSBaseUrl"
    Write-Host "Make sure CS is running and accessible."
    exit 1
}

Write-Host ""
Write-Host "Starting ticket seeding loop. Press Ctrl+C to stop."
Write-Host ""

$batchNumber = 0

while ((Get-Date) -lt $endTime) {
    $batchNumber++
    Write-Host "[$(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')] Batch #$batchNumber - Inserting $TicketsPerBatch tickets..."
    
    for ($i = 0; $i -lt $TicketsPerBatch; $i++) {
        $station = $stations[(Get-Random -Minimum 0 -Maximum $stations.Count)]
        $ticketBody = New-TicketJSON -Index $ticketCount -StationId $station
        
        try {
            $resp = Invoke-RestMethod -Method Post -ContentType "application/json" -Body $ticketBody -Uri "$CSBaseUrl/ticket"
            $ticketCount++
            Write-Host "  ✓ Inserted ticket (id=$($resp.id), station=$station)"
        } catch {
            Write-Host "  ✗ Failed to insert ticket: $_" -ForegroundColor Red
        }
    }
    
    Write-Host "  Total tickets inserted: $ticketCount"
    Write-Host "  Sleeping for $IntervalSeconds seconds..."
    Start-Sleep -Seconds $IntervalSeconds
}

Write-Host ""
Write-Host "Seeding completed after $DurationMinutes minutes."
Write-Host "Total tickets inserted: $ticketCount"
Write-Host "You can now verify manifests were generated:"
Write-Host "  curl '$CSBaseUrl/manifests'"
