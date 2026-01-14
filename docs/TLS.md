# TLS with Mutual Authentication

This document describes how to use TLS with mutual authentication in the TDS system.

## Overview

TDS supports mutual TLS authentication where both the server and client verify each other's identity using X.509 certificates. This provides:

- **Encryption**: All communication is encrypted using TLS 1.2+
- **Server Authentication**: Clients verify the server's identity
- **Client Authentication**: Server verifies each client's identity
- **Integrity**: Protection against man-in-the-middle attacks

## Certificate Architecture

The mutual TLS implementation uses a three-tier certificate hierarchy:

```
┌─────────────────────┐
│   CA Certificate    │  (ca.crt / ca.key)
│  (Self-Signed Root) │  Signs both server and client certificates
└──────────┬──────────┘
           │
     ┌─────┴─────┐
     │           │
     ▼           ▼
┌─────────┐ ┌─────────┐
│ Server  │ │ Client  │
│  Cert   │ │  Cert   │
└─────────┘ └─────────┘
```

## Quick Start

### 1. Generate Certificates

Run the certificate generation script:

```powershell
.\scripts\generate_certs.ps1
```

This creates a `certs/` directory with:
- `ca.crt` / `ca.key` - Certificate Authority
- `server.crt` / `server.key` - Server certificate
- `client.crt` / `client.key` - Client certificate

All certificates are valid for 365 days by default.

**Custom validity period:**
```powershell
.\scripts\generate_certs.ps1 -ValidDays 730
```

### 2. Start the TLS Server

```bash
go run ./cmd/server --tcp --tls
```

**Custom certificate locations:**
```bash
go run ./cmd/server --tcp --tls \
  --tls-cert certs/server.crt \
  --tls-key certs/server.key \
  --tls-client-ca certs/ca.crt
```

### 3. Connect with TLS Client

```bash
go run ./cmd/client_demo -tls
```

**Custom certificate locations:**
```bash
go run ./cmd/client_demo -tls \
  -cert certs/client.crt \
  -key certs/client.key \
  -ca certs/ca.crt
```

## Server Configuration

### Command-Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--tls` | `false` | Enable TLS (requires `--tcp`) |
| `--tls-cert` | `certs/server.crt` | Server certificate file |
| `--tls-key` | `certs/server.key` | Server private key file |
| `--tls-client-ca` | `certs/ca.crt` | CA cert to verify clients |

### TLS Configuration Details

The server enforces:
- **TLS 1.2+** minimum version
- **Mutual authentication** - client certificates required
- **Secure cipher suites**:
  - `TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384`
  - `TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256`
  - `TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384`
  - `TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256`

### Server Logs

When a client connects with valid certificates:
```
[15:04:05] TLS server started on 0.0.0.0:5000 (mutual auth enabled)
[15:04:10] TLS client authenticated: 127.0.0.1:54321 (CN=tds-client)
[15:04:10] REGISTER demo -> 127.0.0.1:12345 from 127.0.0.1:54321
```

## Client Usage

### Using client.go Package

```go
import "tds/pkg/client"

// Register with TLS
err := client.RegisterTLS(
    "127.0.0.1:5000",        // server address
    "my-task",                // task name
    "192.168.1.10:8080",     // service address
    "certs/client.crt",       // client cert
    "certs/client.key",       // client key
    "certs/ca.crt",           // CA cert (to verify server)
)

// Query with TLS
address, err := client.QueryTLS(
    "127.0.0.1:5000",
    "my-task",
    "certs/client.crt",
    "certs/client.key",
    "certs/ca.crt",
)
```

## Certificate Management

### Viewing Certificate Details

```bash
# View server certificate
openssl x509 -in certs/server.crt -text -noout

# Check expiry date
openssl x509 -in certs/server.crt -noout -dates

# Verify certificate chain
openssl verify -CAfile certs/ca.crt certs/server.crt
```

### Generating Additional Client Certificates

To create more client certificates (e.g., for different services):

```powershell
# Generate new client key
openssl genrsa -out certs/client2.key 4096

# Create CSR
openssl req -new -key certs/client2.key -out certs/client2.csr `
  -subj "/C=US/ST=State/L=City/O=TDS/OU=Client/CN=tds-client-2"

# Sign with CA
openssl x509 -req -in certs/client2.csr `
  -CA certs/ca.crt -CAkey certs/ca.key `
  -CAcreateserial -out certs/client2.crt `
  -days 365 -sha256
```

### Certificate Rotation

When certificates are near expiry:

1. Generate new certificates with the same script
2. Replace old certificate files
3. Restart the server (no code changes needed)
4. Clients will use new certificates on next connection

**For zero-downtime rotation**, you would need to implement hot-reloading (not currently supported).

## Security Considerations

### Production Deployment

For production use:

1. **Use a proper CA**: Replace self-signed CA with certificates from:
   - Internal corporate CA
   - Public CA (Let's Encrypt, DigiCert, etc.)

2. **Protect private keys**:
   ```bash
   chmod 600 certs/*.key  # Read-only by owner
   ```

3. **Store keys securely**:
   - Use secrets management (HashiCorp Vault, AWS Secrets Manager)
   - Never commit keys to version control
   - Consider Hardware Security Modules (HSM) for CA keys

4. **Monitor certificate expiry**:
   - Set up alerts 30 days before expiration
   - Implement automated renewal

5. **Review cipher suites**: Update based on current security best practices

### Troubleshooting

**Error: "tls: bad certificate"**
- Client certificate not signed by trusted CA
- Verify: `openssl verify -CAfile certs/ca.crt certs/client.crt`

**Error: "x509: certificate has expired"**
- Certificate past validity period
- Regenerate certificates

**Error: "TLS requires TCP mode"**
- TLS only works with TCP transport
- Use `--tcp --tls` together

**Connection timeout**
- Check server is running with TLS enabled
- Verify port 5000 is not blocked by firewall

## Performance Impact

TLS adds overhead compared to plain TCP/UDP:

| Metric | Plain TCP | TCP + TLS |
|--------|-----------|-----------|
| First request latency | ~1-2ms | ~3-5ms (handshake) |
| Subsequent requests | ~1-2ms | ~1-2ms (reused connection) |
| Throughput | ~50,000 ops/sec | ~45,000 ops/sec |

**Optimization**: Connection pooling can amortize handshake costs.

## Limitations

- **UDP not supported**: TLS only works with TCP (UDP alternative: DTLS, not implemented)
- **No connection pooling**: Each request creates new TLS connection
- **No hot cert reload**: Requires server restart to update certificates
- **Single CA**: Server trusts only one CA for client certificates

## Future Enhancements

Potential improvements:
- Connection pooling for clients
- Hot certificate reload without restart
- Support for multiple trusted CAs
- Certificate revocation checking (CRL/OCSP)
- DTLS support for UDP transport
