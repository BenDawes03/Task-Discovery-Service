# TDS Certificate Generation Script
# Generates self-signed CA, server, and client certificates for mutual TLS authentication

param(
    [string]$OutputDir = "certs",
    [int]$ValidDays = 365
)

$ErrorActionPreference = "Stop"

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "TDS Mutual TLS Certificate Generator" -ForegroundColor Cyan
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""

# Check if OpenSSL is available
try {
    $null = openssl version
} catch {
    Write-Host "ERROR: OpenSSL not found in PATH" -ForegroundColor Red
    Write-Host "Please install OpenSSL or use WSL" -ForegroundColor Yellow
    exit 1
}

# Create output directory
if (-not (Test-Path $OutputDir)) {
    New-Item -ItemType Directory -Path $OutputDir | Out-Null
    Write-Host "[CREATE] Created directory: $OutputDir" -ForegroundColor Green
}

Write-Host "[INFO] Generating certificates (valid for $ValidDays days)..." -ForegroundColor Yellow
Write-Host ""

# 1. Generate CA (Certificate Authority)
Write-Host "[STEP 1/3] Generating CA certificate..." -ForegroundColor Cyan
openssl req -x509 -newkey rsa:4096 -sha256 -days $ValidDays -nodes `
    -keyout "$OutputDir\ca.key" `
    -out "$OutputDir\ca.crt" `
    -subj "/C=US/ST=State/L=City/O=TDS/OU=Development/CN=TDS-CA"

if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Failed to generate CA certificate" -ForegroundColor Red
    exit 1
}
Write-Host "  ✓ CA certificate: $OutputDir\ca.crt" -ForegroundColor Green
Write-Host "  ✓ CA private key: $OutputDir\ca.key" -ForegroundColor Green
Write-Host ""

# 2. Generate Server Certificate
Write-Host "[STEP 2/3] Generating server certificate..." -ForegroundColor Cyan

# Generate server private key
openssl genrsa -out "$OutputDir\server.key" 4096
if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Failed to generate server key" -ForegroundColor Red
    exit 1
}

# Create server CSR (Certificate Signing Request)
openssl req -new -key "$OutputDir\server.key" `
    -out "$OutputDir\server.csr" `
    -subj "/C=US/ST=State/L=City/O=TDS/OU=Server/CN=localhost"
if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Failed to generate server CSR" -ForegroundColor Red
    exit 1
}

# Create server certificate config for SANs (Subject Alternative Names)
@"
subjectAltName = @alt_names
extendedKeyUsage = serverAuth

[alt_names]
DNS.1 = localhost
DNS.2 = 127.0.0.1
IP.1 = 127.0.0.1
"@ | Out-File -FilePath "$OutputDir\server.ext" -Encoding ASCII

# Sign server certificate with CA
openssl x509 -req -in "$OutputDir\server.csr" `
    -CA "$OutputDir\ca.crt" `
    -CAkey "$OutputDir\ca.key" `
    -CAcreateserial `
    -out "$OutputDir\server.crt" `
    -days $ValidDays `
    -sha256 `
    -extfile "$OutputDir\server.ext"

if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Failed to sign server certificate" -ForegroundColor Red
    exit 1
}

Write-Host "  ✓ Server certificate: $OutputDir\server.crt" -ForegroundColor Green
Write-Host "  ✓ Server private key: $OutputDir\server.key" -ForegroundColor Green
Write-Host ""

# 3. Generate Client Certificate
Write-Host "[STEP 3/3] Generating client certificate..." -ForegroundColor Cyan

# Generate client private key
openssl genrsa -out "$OutputDir\client.key" 4096
if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Failed to generate client key" -ForegroundColor Red
    exit 1
}

# Create client CSR
openssl req -new -key "$OutputDir\client.key" `
    -out "$OutputDir\client.csr" `
    -subj "/C=US/ST=State/L=City/O=TDS/OU=Client/CN=tds-client"
if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Failed to generate client CSR" -ForegroundColor Red
    exit 1
}

# Create client certificate config
@"
extendedKeyUsage = clientAuth
"@ | Out-File -FilePath "$OutputDir\client.ext" -Encoding ASCII

# Sign client certificate with CA
openssl x509 -req -in "$OutputDir\client.csr" `
    -CA "$OutputDir\ca.crt" `
    -CAkey "$OutputDir\ca.key" `
    -CAcreateserial `
    -out "$OutputDir\client.crt" `
    -days $ValidDays `
    -sha256 `
    -extfile "$OutputDir\client.ext"

if ($LASTEXITCODE -ne 0) {
    Write-Host "ERROR: Failed to sign client certificate" -ForegroundColor Red
    exit 1
}

Write-Host "  ✓ Client certificate: $OutputDir\client.crt" -ForegroundColor Green
Write-Host "  ✓ Client private key: $OutputDir\client.key" -ForegroundColor Green
Write-Host ""

# Cleanup temporary files
Remove-Item "$OutputDir\*.csr" -ErrorAction SilentlyContinue
Remove-Item "$OutputDir\*.ext" -ErrorAction SilentlyContinue
Remove-Item "$OutputDir\*.srl" -ErrorAction SilentlyContinue

# Verify certificates
Write-Host "[VERIFY] Verifying certificate chain..." -ForegroundColor Yellow
openssl verify -CAfile "$OutputDir\ca.crt" "$OutputDir\server.crt" | Out-Null
if ($LASTEXITCODE -eq 0) {
    Write-Host "  ✓ Server certificate is valid" -ForegroundColor Green
}
openssl verify -CAfile "$OutputDir\ca.crt" "$OutputDir\client.crt" | Out-Null
if ($LASTEXITCODE -eq 0) {
    Write-Host "  ✓ Client certificate is valid" -ForegroundColor Green
}
Write-Host ""

Write-Host "========================================" -ForegroundColor Cyan
Write-Host "Certificate Generation Complete!" -ForegroundColor Green
Write-Host "========================================" -ForegroundColor Cyan
Write-Host ""
Write-Host "Generated certificates in: $OutputDir\" -ForegroundColor White
Write-Host ""
Write-Host "To start the TLS server:" -ForegroundColor Yellow
Write-Host "  go run ./cmd/server --tcp --tls" -ForegroundColor White
Write-Host ""
Write-Host "To test with client:" -ForegroundColor Yellow
Write-Host "  go run ./demos/client -tls" -ForegroundColor White
Write-Host ""
Write-Host "Certificate validity: $ValidDays days from now" -ForegroundColor Cyan
Write-Host ""
