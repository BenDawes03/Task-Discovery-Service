# VM Load Testing Guide for TDS

This directory contains scripts for distributed load testing of the TDS server across multiple VMs.

## Overview

The test environment consists of:
- **1 Server VM**: Runs the TDS server
- **3 Client VMs**: Generate load by running dummy services and queries

## Scripts

### 1. `vm_dummy_services.ps1`
Runs multiple dummy services that continuously register with the TDS server.

**Usage:**
```powershell
# On each client VM
pwsh -File vm_dummy_services.ps1 -ServerAddr "192.168.1.100:5000" -NumServices 50 -NumTasks 10
```

**Parameters:**
- `-ServerAddr`: TDS server address (default: `192.168.1.100:5000`)
- `-NumServices`: Number of dummy services (default: 50)
- `-NumTasks`: Number of task types (default: 10)
- `-Protocol`: Transport protocol - `udp`, `tcp`, or `tls` (default: `udp`)
- `-HeartbeatInterval`: Seconds between registrations (default: 30)
- `-ServicePortRange`: Port range for services (default: `8000-8500`)
- `-LogFile`: Log file path (default: `dummy_services.log`)

**Example:**
```powershell
# Run 100 services across 20 tasks on UDP
pwsh vm_dummy_services.ps1 -ServerAddr "10.0.1.50:5000" -NumServices 100 -NumTasks 20 -Protocol udp
```

### 2. `vm_query_load.ps1`
Generates continuous query load against the TDS server.

**Usage:**
```powershell
# On each client VM or server VM
pwsh -File vm_query_load.ps1 -ServerAddr "192.168.1.100:5000" -NumThreads 10 -QueriesPerThread 1000
```

**Parameters:**
- `-ServerAddr`: TDS server address (default: `192.168.1.100:5000`)
- `-NumThreads`: Number of concurrent query threads (default: 10)
- `-QueriesPerThread`: Queries per thread, 0 = infinite (default: 1000)
- `-ThinkTime`: Milliseconds between queries per thread (default: 0)
- `-Protocol`: Transport protocol - `udp`, `tcp`, or `tls` (default: `udp`)
- `-NumTasks`: Number of tasks to query (default: 10)
- `-LogFile`: Log file path (default: `query_load.log`)
- `-StatsInterval`: Stats display interval in seconds (default: 5)

**Example:**
```powershell
# Run infinite queries with 20 threads and 100ms think time
pwsh vm_query_load.ps1 -ServerAddr "10.0.1.50:5000" -NumThreads 20 -QueriesPerThread 0 -ThinkTime 100
```

### 3. `vm_orchestrator.ps1`
Orchestrates distributed testing across multiple VMs via SSH.

**Usage:**
```powershell
# From your control machine
pwsh -File vm_orchestrator.ps1 -ServerVM "192.168.1.100" -ClientVMs @("192.168.1.101", "192.168.1.102", "192.168.1.103")
```

**Parameters:**
- `-ServerVM`: TDS server VM IP (default: `192.168.1.100`)
- `-ClientVMs`: Array of client VM IPs (default: `@("192.168.1.101", "192.168.1.102", "192.168.1.103")`)
- `-Username`: SSH username (default: `testuser`)
- `-Protocol`: Transport protocol (default: `udp`)
- `-ServicesPerClient`: Dummy services per client (default: 50)
- `-QueryThreadsPerClient`: Query threads per client (default: 10)
- `-TestDuration`: Test duration in seconds, 0 = infinite (default: 300)
- `-RemoteScriptPath`: Path on remote VMs (default: `/tmp/tds-test`)
- `-CleanupOnly`: Only cleanup, don't start test

**Example:**
```powershell
# Run 5-minute test with custom configuration
pwsh vm_orchestrator.ps1 `
    -ServerVM "10.0.1.50" `
    -ClientVMs @("10.0.1.51", "10.0.1.52", "10.0.1.53") `
    -Username "ubuntu" `
    -ServicesPerClient 100 `
    -QueryThreadsPerClient 20 `
    -TestDuration 300

# Cleanup all processes on client VMs
pwsh vm_orchestrator.ps1 -CleanupOnly
```

## Test Scenarios

### Scenario 1: Basic Load Test
Test with moderate load to verify stability.

```powershell
# 3 Client VMs × 50 services = 150 total services
# 3 Client VMs × 10 threads = 30 concurrent query threads
pwsh vm_orchestrator.ps1 -TestDuration 300
```

**Expected Load:**
- ~150 service registrations every 30 seconds
- ~300-500 queries/second (depending on think time)

### Scenario 2: High Load Test
Stress test with heavy concurrent load.

```powershell
# 3 Client VMs × 200 services = 600 total services
# 3 Client VMs × 50 threads = 150 concurrent query threads
pwsh vm_orchestrator.ps1 `
    -ServicesPerClient 200 `
    -QueryThreadsPerClient 50 `
    -TestDuration 600
```

**Expected Load:**
- ~600 service registrations every 30 seconds
- ~5,000-10,000 queries/second

### Scenario 3: Sustained Test
Long-running test to check for memory leaks and degradation.

```powershell
# Run for 1 hour
pwsh vm_orchestrator.ps1 `
    -ServicesPerClient 100 `
    -QueryThreadsPerClient 20 `
    -TestDuration 3600
```

### Scenario 4: TCP/TLS Protocol Test
Test with TCP or TLS transport.

```powershell
# TCP test
pwsh vm_orchestrator.ps1 -Protocol tcp -TestDuration 300

# TLS test (requires certificates configured on server)
pwsh vm_orchestrator.ps1 -Protocol tls -TestDuration 300
```

## Manual Testing (Without Orchestrator)

### On Server VM:
```powershell
# Start TDS server
go run ./cmd/server --no-ui
```

### On Each Client VM:

**Terminal 1 - Dummy Services:**
```powershell
pwsh vm_dummy_services.ps1 -ServerAddr "192.168.1.100:5000" -NumServices 50
```

**Terminal 2 - Query Load:**
```powershell
pwsh vm_query_load.ps1 -ServerAddr "192.168.1.100:5000" -NumThreads 10 -QueriesPerThread 0
```

## Monitoring

### Server Metrics
Monitor the TDS server TUI or logs for:
- Total registered services
- Query rate (queries/second)
- Success/failure rates
- Response times

### Client Logs
Check logs on each client VM:
```bash
# Service registration logs
tail -f /tmp/tds-test/dummy_services.log

# Query load logs
tail -f /tmp/tds-test/query_load.log
```

### Key Metrics to Track

**Registration Metrics:**
- Registration success rate (should be >99%)
- Registration failures per minute
- Time to register all services

**Query Metrics:**
- Queries per second (QPS)
- Average latency (ms)
- P50, P95, P99 latencies
- Success rate (should be >99%)
- NOTFOUND rate (should be <1% after initial registration)

**Server Metrics:**
- CPU usage
- Memory usage
- Network throughput
- Open connections

## Troubleshooting

### SSH Connection Issues
```powershell
# Test SSH connectivity
ssh testuser@192.168.1.101 "echo OK"

# Check SSH key authentication
ssh-add -l

# Copy SSH key to VM
ssh-copy-id testuser@192.168.1.101
```

### PowerShell Not Found on Linux VMs
```bash
# Install PowerShell on Debian 11 (Bullseye)
wget https://packages.microsoft.com/config/debian/11/packages-microsoft-prod.deb
sudo dpkg -i packages-microsoft-prod.deb
sudo apt-get update
sudo apt-get install -y powershell

# Install PowerShell on Debian 12 (Bookworm)
wget https://packages.microsoft.com/config/debian/12/packages-microsoft-prod.deb
sudo dpkg -i packages-microsoft-prod.deb
sudo apt-get update
sudo apt-get install -y powershell

# Install PowerShell on Ubuntu 22.04
wget https://packages.microsoft.com/config/ubuntu/22.04/packages-microsoft-prod.deb
sudo dpkg -i packages-microsoft-prod.deb
sudo apt-get update
sudo apt-get install -y powershell

# Alternative: Install via snap (works on most distributions)
sudo snap install powershell --classic

# Verify installation
pwsh --version
```

### High Failure Rates
- Check network connectivity between client VMs and server
- Verify server is running and listening on correct port
- Check firewall rules
- Increase server resources (CPU/RAM)
- Reduce load (fewer threads/services)

### Services Not Showing in TUI
- Wait 30 seconds for initial heartbeat
- Check service registration logs for errors
- Verify correct server address
- Test manual registration with `client_demo`

### Query Timeouts
- Reduce number of concurrent threads
- Increase think time between queries
- Check server CPU/memory usage
- Verify network latency is acceptable

## Performance Baseline

With the optimized atomic operations implementation, expected performance on modern hardware:

| Configuration | Expected QPS | Expected Latency (P95) |
|---------------|--------------|------------------------|
| Low Load      | 1,000-5,000  | <10ms                  |
| Medium Load   | 10,000-50,000| <20ms                  |
| High Load     | 50,000-100,000| <50ms                 |
| Stress Test   | >100,000     | <100ms                 |

**Notes:**
- UDP is typically 2-5x faster than TCP
- TLS adds 5-15ms overhead due to encryption
- Network latency between VMs adds to baseline
- Memory usage scales linearly with number of services

## Cleanup

### Stop All Tests
```powershell
# Using orchestrator
pwsh vm_orchestrator.ps1 -CleanupOnly

# Manually on each client VM
ssh testuser@192.168.1.101 "pkill -f vm_dummy_services.ps1; pkill -f vm_query_load.ps1"
```

### Collect Results
```powershell
# Logs are automatically collected by orchestrator at test end
# Or manually copy from VMs
scp testuser@192.168.1.101:/tmp/tds-test/*.log ./results/
```

## Advanced Testing

### Mixed Protocol Test
Run different protocols simultaneously:
```powershell
# Client 1: UDP
ssh testuser@192.168.1.101 "cd /tmp/tds-test && pwsh vm_query_load.ps1 -Protocol udp" &

# Client 2: TCP
ssh testuser@192.168.1.102 "cd /tmp/tds-test && pwsh vm_query_load.ps1 -Protocol tcp" &
```

### Gradual Load Ramp
Incrementally increase load to find breaking point:
```powershell
# Start with 10 threads
pwsh vm_orchestrator.ps1 -QueryThreadsPerClient 10 -TestDuration 60

# Increase to 20 threads
pwsh vm_orchestrator.ps1 -QueryThreadsPerClient 20 -TestDuration 60

# Continue increasing...
```

### Database Performance Test
Compare memory vs PostgreSQL performance:
```bash
# On server VM - Memory mode
./server --no-ui

# On server VM - PostgreSQL mode
./server --no-ui --store-url "postgresql://user:pass@localhost/tds"
```
