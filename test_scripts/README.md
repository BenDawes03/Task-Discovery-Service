# Firewall Testing Guide

This directory contains comprehensive test scripts for validating the TDS firewall functionality.

## Available Test Scripts

### 1. Basic Firewall Test (`test_firewall.ps1`)

**Purpose:** Quick demonstration of firewall functionality  
**Requirements:** Go compiler  
**Network:** Localhost only

```powershell
.\test_scripts\test_firewall.ps1
```

Creates example firewall rules and starts the server. Provides manual testing instructions.

---

### 2. Comprehensive Firewall Test (`comprehensive_firewall_test.ps1`)

**Purpose:** Automated test with 5 simulated clients  
**Requirements:** Go compiler, PowerShell  
**Network:** Localhost (simulated IPs)

```powershell
.\test_scripts\comprehensive_firewall_test.ps1
```

#### Test Scenario

**Network Topology:**
```
192.168.1.0/24 ←→ 192.168.1.0/24  (Allowed)
192.168.1.0/24 →  10.0.0.0/24     (Allowed)
10.0.0.0/24    ←→ 10.0.0.0/24     (Allowed)
172.16.0.100   →  *               (BLOCKED - Isolated)
```

**Clients:**
- **ClientA** (192.168.1.10) - Can reach ClientB, ClientC, ClientD
- **ClientB** (192.168.1.11) - Can reach ClientA, ClientC, ClientD
- **ClientC** (10.0.0.5) - Can only reach ClientD
- **ClientD** (10.0.0.6) - Can only reach ClientC
- **ClientE** (172.16.0.100) - **ISOLATED** - Cannot reach anyone

#### Expected Test Results

| From → To | ClientA | ClientB | ClientC | ClientD | ClientE |
|-----------|---------|---------|---------|---------|---------|
| **ClientA** | ✓ | ✓ | ✓ | ✓ | ✗ |
| **ClientB** | ✓ | ✓ | ✓ | ✓ | ✗ |
| **ClientC** | ✗ | ✗ | ✓ | ✓ | ✗ |
| **ClientD** | ✗ | ✗ | ✓ | ✓ | ✗ |
| **ClientE** | ✗ | ✗ | ✗ | ✗ | ✗ |

✓ = Allowed by firewall  
✗ = Blocked by firewall (returns FORBIDDEN)

#### Limitations

This test runs all clients from localhost (127.0.0.1), which has full firewall access. The script **documents** what would happen with real client IPs but cannot enforce source IP filtering from a single machine.

For true source IP testing, use the Docker-based test.

---

### 3. Docker-Based Firewall Test (`docker_firewall_test.ps1`)

**Purpose:** Real network isolation testing with actual different IPs  
**Requirements:** Docker Desktop, Go compiler  
**Network:** Docker network with custom IPs

```powershell
.\test_scripts\docker_firewall_test.ps1
```

#### What This Test Does

1. Creates Docker network `10.0.0.0/24`
2. Starts TDS server at `10.0.0.10`
3. Creates 5 client containers with different IPs:
   - client-a: `10.0.0.20`
   - client-b: `10.0.0.21`
   - client-c: `10.0.0.30`
   - client-d: `10.0.0.31`
   - client-e: `10.0.0.40`
4. Each client registers its service
5. Each client queries for all services
6. Server applies firewall rules based on actual source IP
7. Results show which queries succeed vs. get FORBIDDEN

#### Firewall Rules (Docker Test)

```
# Client A and B can reach each other and C/D
10.0.0.20 → 10.0.0.20, 10.0.0.21, 10.0.0.30, 10.0.0.31
10.0.0.21 → 10.0.0.20, 10.0.0.21, 10.0.0.30, 10.0.0.31

# Client C and D can only reach each other
10.0.0.30 → 10.0.0.30, 10.0.0.31
10.0.0.31 → 10.0.0.30, 10.0.0.31

# Client E has NO rules (isolated)
```

#### Expected Output

```
CLIENT-A (10.0.0.20) Querying Services:
  taskA: 10.0.0.20:8080    ✓
  taskB: 10.0.0.21:8080    ✓
  taskC: 10.0.0.30:8080    ✓
  taskD: 10.0.0.31:8080    ✓
  taskE: FORBIDDEN         ✗

CLIENT-C (10.0.0.30) Querying Services:
  taskA: FORBIDDEN         ✗
  taskB: FORBIDDEN         ✗
  taskC: 10.0.0.30:8080    ✓
  taskD: 10.0.0.31:8080    ✓
  taskE: FORBIDDEN         ✗

CLIENT-E (10.0.0.40) Querying Services:
  taskA: FORBIDDEN         ✗
  taskB: FORBIDDEN         ✗
  taskC: FORBIDDEN         ✗
  taskD: FORBIDDEN         ✗
  taskE: FORBIDDEN         ✗
```

#### Prerequisites

Install Docker Desktop:
- Windows: https://www.docker.com/products/docker-desktop
- Ensure WSL2 backend is enabled
- Docker daemon must be running

---

## Comparison Matrix

| Feature | Basic Test | Comprehensive Test | Docker Test |
|---------|-----------|-------------------|-------------|
| **Setup Complexity** | Low | Low | Medium |
| **Real IP Testing** | No | No | Yes |
| **Automated** | Manual | Fully Automated | Fully Automated |
| **Prerequisites** | Go | Go | Go + Docker |
| **Test Coverage** | Demo | Full matrix | Full matrix |
| **Runtime** | Manual | ~10 seconds | ~30 seconds |

## Troubleshooting

### PowerShell Execution Policy Error

```powershell
powershell -ExecutionPolicy Bypass -File .\test_scripts\<script_name>.ps1
```

### Docker Test Fails to Start

1. Ensure Docker Desktop is running
2. Check Docker daemon status: `docker ps`
3. Verify network doesn't exist: `docker network ls`
4. Clean up manually: `docker network rm tds-test-network`

### Server Won't Start

Check if port 5000 is already in use:
```powershell
netstat -ano | findstr :5000
```

Kill the process or use a different port.

### Timeout Errors

Increase timeout in the script or check firewall/antivirus blocking UDP port 5000.

## Custom Testing

### Create Your Own Firewall Rules

```powershell
.\test_scripts\generate_firewall_rules.ps1
```

Interactive wizard to create custom firewall rules.

### Test with Real Clients

For production testing:

1. Deploy TDS server with firewall rules
2. Use client machines with actual IPs
3. Register services from each client
4. Query and verify firewall enforcement

## Protocol Reference

### Register Service
```
REGISTER <task> <address>
Response: OK | ERR
```

### Query Service
```
QUERY <task>
Response: <address> | NOTFOUND | FORBIDDEN | ERR
```

- **address**: Service endpoint (e.g., `10.0.0.5:8080`)
- **NOTFOUND**: No service registered for task
- **FORBIDDEN**: No service allowed by firewall rules
- **ERR**: Protocol error

## See Also

- [Firewall Rules Documentation](../docs/FIREWALL_RULES.md)
- [README](../README.md)
