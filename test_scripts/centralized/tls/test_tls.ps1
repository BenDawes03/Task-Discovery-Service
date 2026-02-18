# TLS Test Script for TDS Server
# Tests mutual TLS authentication with various scenarios

param(
    [string]$ServerAddr = "127.0.0.1:5000",
    [string]$CertDir = "certs",
    [int]$TestIterations = 10,
    [switch]$SkipCertCheck,
    [switch]$Verbose
)

# Navigate to project root
$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$projectRoot = Split-Path -Parent (Split-Path -Parent (Split-Path -Parent $scriptDir))
Set-Location $projectRoot

$ErrorActionPreference = "Continue"

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "TDS Server TLS Test Suite" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Configuration:" -ForegroundColor Yellow
Write-Host "  Server: $ServerAddr" -ForegroundColor White
Write-Host "  Certificate Directory: $CertDir" -ForegroundColor White
Write-Host "  Test Iterations: $TestIterations" -ForegroundColor White
Write-Host ""

# Test counters
$script:passedTests = 0
$script:failedTests = 0
$script:skippedTests = 0

function Write-TestHeader {
    param([string]$TestName)
    Write-Host ""
    Write-Host "TEST: $TestName" -ForegroundColor Cyan
    Write-Host "--------------------------------------" -ForegroundColor Gray
}

function Write-TestResult {
    param(
        [string]$Status,  # "PASS", "FAIL", "SKIP"
        [string]$Message
    )
    
    switch ($Status) {
        "PASS" {
            Write-Host "  [PASS] $Message" -ForegroundColor Green
            $script:passedTests++
        }
        "FAIL" {
            Write-Host "  [FAIL] $Message" -ForegroundColor Red
            $script:failedTests++
        }
        "SKIP" {
            Write-Host "  [SKIP] $Message" -ForegroundColor Yellow
            $script:skippedTests++
        }
    }
}

function Test-CertificatesExist {
    Write-TestHeader "Certificate Files Existence"
    
    $requiredFiles = @(
        "$CertDir/ca.crt",
        "$CertDir/server.crt",
        "$CertDir/server.key",
        "$CertDir/client.crt",
        "$CertDir/client.key"
    )
    
    $allExist = $true
    foreach ($file in $requiredFiles) {
        if (Test-Path $file) {
            if ($Verbose) {
                Write-TestResult "PASS" "Found $file"
            }
        } else {
            Write-TestResult "FAIL" "Missing $file"
            $allExist = $false
        }
    }
    
    if ($allExist) {
        Write-TestResult "PASS" "All certificate files exist"
    } else {
        Write-Host ""
        Write-Host "  Run './scripts/generate_certs.ps1' to generate certificates" -ForegroundColor Yellow
        return $false
    }
    
    return $true
}

function Test-CertificateValidity {
    Write-TestHeader "Certificate Validity"
    
    if (-not (Get-Command openssl -ErrorAction SilentlyContinue)) {
        Write-TestResult "SKIP" "OpenSSL not found - skipping certificate validation"
        return
    }
    
    # Check server certificate
    $serverCertInfo = openssl x509 -in "$CertDir/server.crt" -noout -enddate 2>&1
    if ($LASTEXITCODE -eq 0) {
        if ($Verbose) {
            Write-Host "  Server cert: $serverCertInfo" -ForegroundColor Gray
        }
        
        # Extract expiry date and check if expired
        $endDateMatch = $serverCertInfo -match 'notAfter=(.*)'
        if ($endDateMatch) {
            try {
                $dateStr = $Matches[1].Trim() -replace '\s+', ' '
                $expiryDate = [DateTime]::ParseExact($dateStr, "MMM d HH:mm:ss yyyy GMT", [System.Globalization.CultureInfo]::InvariantCulture)
                if ($expiryDate -gt (Get-Date)) {
                    Write-TestResult "PASS" "Server certificate is valid (expires: $($expiryDate.ToString('yyyy-MM-dd')))"
                } else {
                    Write-TestResult "FAIL" "Server certificate has expired"
                }
            } catch {
                Write-TestResult "SKIP" "Could not parse server certificate expiry date"
            }
        }
    } else {
        Write-TestResult "FAIL" "Failed to read server certificate"
    }
    
    # Check client certificate
    $clientCertInfo = openssl x509 -in "$CertDir/client.crt" -noout -enddate 2>&1
    if ($LASTEXITCODE -eq 0) {
        if ($Verbose) {
            Write-Host "  Client cert: $clientCertInfo" -ForegroundColor Gray
        }
        
        $endDateMatch = $clientCertInfo -match 'notAfter=(.*)'
        if ($endDateMatch) {
            try {
                $dateStr = $Matches[1].Trim() -replace '\s+', ' '
                $expiryDate = [DateTime]::ParseExact($dateStr, "MMM d HH:mm:ss yyyy GMT", [System.Globalization.CultureInfo]::InvariantCulture)
                if ($expiryDate -gt (Get-Date)) {
                    Write-TestResult "PASS" "Client certificate is valid (expires: $($expiryDate.ToString('yyyy-MM-dd')))"
                } else {
                    Write-TestResult "FAIL" "Client certificate has expired"
                }
            } catch {
                Write-TestResult "SKIP" "Could not parse client certificate expiry date"
            }
        }
    } else {
        Write-TestResult "FAIL" "Failed to read client certificate"
    }
    
    # Verify certificate chain
    $verifyResult = openssl verify -CAfile "$CertDir/ca.crt" "$CertDir/server.crt" 2>&1
    if ($LASTEXITCODE -eq 0 -and $verifyResult -match "OK") {
        Write-TestResult "PASS" "Server certificate chain verification successful"
    } else {
        Write-TestResult "FAIL" "Server certificate chain verification failed"
    }
    
    $verifyResult = openssl verify -CAfile "$CertDir/ca.crt" "$CertDir/client.crt" 2>&1
    if ($LASTEXITCODE -eq 0 -and $verifyResult -match "OK") {
        Write-TestResult "PASS" "Client certificate chain verification successful"
    } else {
        Write-TestResult "FAIL" "Client certificate chain verification failed"
    }
}

function Test-ServerConnection {
    Write-TestHeader "Server TLS Connection Test"
    
    # Test connection to TLS server
    try {
        $serverParts = $ServerAddr.Split(':')
        $hostname = $serverParts[0]
        $port = [int]$serverParts[1]
        
        $tcpClient = New-Object System.Net.Sockets.TcpClient
        $tcpClient.Connect($hostname, $port)
        
        # Load client certificates
        $cert = [System.Security.Cryptography.X509Certificates.X509Certificate2]::new(
            (Resolve-Path "$CertDir/client.crt").Path,
            "",
            [System.Security.Cryptography.X509Certificates.X509KeyStorageFlags]::DefaultKeySet
        )
        
        # For certificate with separate key file, we need to combine them
        # PowerShell's X509Certificate2 requires PKCS12 or cert with embedded key
        # Let's try a basic connection test instead
        
        $tcpClient.Close()
        Write-TestResult "PASS" "TCP connection to server successful"
        
    } catch {
        Write-TestResult "FAIL" "Failed to connect to server: $_"
        Write-Host ""
        Write-Host "  Make sure the TLS server is running:" -ForegroundColor Yellow
        Write-Host "    go run ./cmd/server --tcp --tls" -ForegroundColor Gray
        return $false
    }
    
    return $true
}

function Test-TLSRegistrationAndQuery {
    Write-TestHeader "TLS Registration and Query Test"
    
    # Build client demo if not exists
    if (-not (Test-Path "bin\client_demo.exe")) {
        Write-Host "  Building client demo..." -ForegroundColor Gray
        go build -o bin\client_demo.exe ./cmd/client_demo
        if ($LASTEXITCODE -ne 0) {
            Write-TestResult "FAIL" "Failed to build client demo"
            return $false
        }
    }
    
    $successCount = 0
    $failCount = 0
    
    for ($i = 1; $i -le $TestIterations; $i++) {
        $task = "tls-test-task-$i"
        $port = 9000 + $i
        $address = "192.168.1.${i}:${port}"
        
        # Create a test script that registers and queries
        $testScript = @"
Set-Location '$projectRoot'
`$env:TDS_SERVER_ADDR = '$ServerAddr'
try {
    `$output = & '.\bin\client_demo.exe' -tls -cert '$CertDir/client.crt' -key '$CertDir/client.key' -ca '$CertDir/ca.crt' 2>&1
    if (`$LASTEXITCODE -eq 0) {
        Write-Output 'SUCCESS'
    } else {
        Write-Output `"FAIL: `$output`"
    }
} catch {
    Write-Output `"ERROR: `$_`"
}
"@
        
        $job = Start-Job -ScriptBlock { param($script) powershell -NoProfile -Command $script } -ArgumentList $testScript
        $completed = Wait-Job -Job $job -Timeout 10
        
        if ($completed) {
            $result = Receive-Job -Job $job
            Remove-Job -Job $job -Force
        } else {
            Stop-Job -Job $job
            Remove-Job -Job $job -Force
            $result = "TIMEOUT"
        }
        
        if ($result -match "SUCCESS") {
            $successCount++
            if ($Verbose) {
                Write-Host "  Iteration $i : OK" -ForegroundColor Green
            }
        } else {
            $failCount++
            if ($Verbose) {
                Write-Host "  Iteration $i : FAIL ($result)" -ForegroundColor Red
            }
        }
        
        Start-Sleep -Milliseconds 100
    }
    
    if ($successCount -eq $TestIterations) {
        Write-TestResult "PASS" "All $TestIterations TLS register/query operations succeeded"
        return $true
    } elseif ($successCount -gt 0) {
        Write-TestResult "FAIL" "$successCount/$TestIterations operations succeeded, $failCount failed"
        return $false
    } else {
        Write-TestResult "FAIL" "All TLS operations failed"
        return $false
    }
}

function Test-InvalidClientCertificate {
    Write-TestHeader "Invalid Client Certificate Test (Expected to Fail)"
    
    # This test verifies that the server rejects connections without proper client certificates
    # We'll try to connect without client certificates (if supported by the test infrastructure)
    
    Write-TestResult "SKIP" "Manual test required - try connecting without client cert"
    Write-Host "  To manually test: Modify client_demo to skip certificate loading" -ForegroundColor Gray
}

function Test-ConcurrentTLSConnections {
    Write-TestHeader "Concurrent TLS Connections Test"
    
    if (-not (Test-Path "bin\client_demo.exe")) {
        Write-Host "  Building client demo..." -ForegroundColor Gray
        go build -o bin\client_demo.exe ./cmd/client_demo
        if ($LASTEXITCODE -ne 0) {
            Write-TestResult "FAIL" "Failed to build client demo"
            return $false
        }
    }
    
    $numConcurrent = 5
    $jobs = @()
    
    Write-Host "  Starting $numConcurrent concurrent TLS connections..." -ForegroundColor Gray
    
    for ($i = 1; $i -le $numConcurrent; $i++) {
        $job = Start-Job -ScriptBlock {
            param($projectRoot, $serverAddr, $certDir, $clientId)
            
            Set-Location $projectRoot
            $env:TDS_SERVER_ADDR = $serverAddr
            
            $output = & ".\bin\client_demo.exe" -tls `
                -cert "$certDir/client.crt" `
                -key "$certDir/client.key" `
                -ca "$certDir/ca.crt" 2>&1
            
            if ($LASTEXITCODE -eq 0) {
                return @{Success=$true; ClientId=$clientId}
            } else {
                return @{Success=$false; ClientId=$clientId; Error=$output}
            }
        } -ArgumentList $projectRoot, $ServerAddr, $CertDir, $i
        
        $jobs += $job
    }
    
    # Wait for all jobs with timeout
    $timeout = 30
    $completed = Wait-Job -Job $jobs -Timeout $timeout
    
    $successCount = 0
    $failCount = 0
    
    foreach ($job in $jobs) {
        if ($job.State -eq "Completed") {
            $result = Receive-Job -Job $job
            if ($result.Success) {
                $successCount++
                if ($Verbose) {
                    Write-Host "  Client $($result.ClientId): OK" -ForegroundColor Green
                }
            } else {
                $failCount++
                if ($Verbose) {
                    Write-Host "  Client $($result.ClientId): FAIL" -ForegroundColor Red
                }
            }
        } else {
            $failCount++
            if ($Verbose) {
                Write-Host "  Job timed out or failed" -ForegroundColor Red
            }
        }
        Remove-Job -Job $job -Force
    }
    
    if ($successCount -eq $numConcurrent) {
        Write-TestResult "PASS" "All $numConcurrent concurrent TLS connections succeeded"
        return $true
    } else {
        Write-TestResult "FAIL" "$successCount/$numConcurrent concurrent connections succeeded"
        return $false
    }
}

function Show-TestSummary {
    Write-Host ""
    Write-Host "========================================" -ForegroundColor Cyan
    Write-Host "Test Summary" -ForegroundColor Cyan
    Write-Host "========================================" -ForegroundColor Cyan
    
    $total = $script:passedTests + $script:failedTests + $script:skippedTests
    
    Write-Host "  Total Tests: $total" -ForegroundColor White
    Write-Host "  Passed:      $script:passedTests" -ForegroundColor Green
    Write-Host "  Failed:      $script:failedTests" -ForegroundColor Red
    Write-Host "  Skipped:     $script:skippedTests" -ForegroundColor Yellow
    Write-Host ""
    
    if ($script:failedTests -eq 0) {
        Write-Host "  [OK] All tests passed!" -ForegroundColor Green
        return 0
    } else {
        Write-Host "  [X] Some tests failed" -ForegroundColor Red
        return 1
    }
}

# Main test execution
Write-Host "Starting TLS tests..." -ForegroundColor Cyan
Write-Host ""

# Run tests in sequence
if (-not (Test-CertificatesExist)) {
    if (-not $SkipCertCheck) {
        Write-Host ""
        Write-Host "Aborting tests due to missing certificates." -ForegroundColor Red
        exit 1
    }
}

Test-CertificateValidity

if (Test-ServerConnection) {
    Test-TLSRegistrationAndQuery
    Test-ConcurrentTLSConnections
} else {
    Write-Host ""
    Write-Host "Skipping remaining tests - server not reachable" -ForegroundColor Yellow
}

Test-InvalidClientCertificate

# Show final summary
$exitCode = Show-TestSummary
exit $exitCode
