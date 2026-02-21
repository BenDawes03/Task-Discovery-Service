# TDS Test Environment Scripts

This directory contains scripts for setting up and running a complete test environment for the Task Discovery Service (TDS).

## Scripts Overview

### Service Scripts
- **dummy_services.ps1**: Runs dummy services that register with TDS and accept data from clients
- **client_queries.ps1**: Queries TDS for services and sends test data to discovered services

### Orchestration Scripts
- **orchestrator.ps1**: Coordinates multi-VM load testing across server and client VMs

### Legacy Load Testing
- **load_test_server.ps1**: Direct server load testing (legacy)

## Quick Start

### Single Machine Testing

1. Start dummy services:
```powershell
.\dummy_services.ps1 -ServerAddr "localhost:5000" -NumServices 10
```

2. Run client queries:
```powershell
.\client_queries.ps1 -ServerAddr "localhost:5000" -NumThreads 5
```

### Multi-VM Testing

Run the orchestrator to coordinate testing across VMs:
```powershell
.\orchestrator.ps1 -ServerVM "192.168.0.181" -ClientVMs @("192.168.0.180", "192.168.0.182") -TestDuration 300
```

## Architecture

```
┌─────────────┐
│  TDS Server │ (Registry)
└──────┬──────┘
       │
       ├─── REGISTER ───┐
       │                │
       └─── QUERY ──────┼───────┐
                        │       │
                   ┌────▼────┐  │
                   │ Service │  │
                   │ (dummy) │  │
                   └────▲────┘  │
                        │       │
                   DATA │       │ GET address
                        │       │
                   ┌────┴────┐  │
                   │ Client  ├──┘
                   │ (query) │
                   └─────────┘
```

## Test Flow

1. **Services Register**: Dummy services register their task and address with TDS server
2. **Clients Query**: Client scripts query TDS for a specific task
3. **TDS Responds**: Server returns an available service address
4. **Clients Send Data**: Client sends test data to the discovered service
5. **Services Process**: Dummy service receives and acknowledges data
6. **Repeat**: Continuous loop with configurable think time

## Configuration

All scripts support common parameters:
- `ServerAddr`: TDS server address (default: "192.168.0.181:5000")
- `Protocol`: Communication protocol - udp, tcp, or tls (default: "udp")
- `NumServices/NumThreads`: Concurrency level
- `LogFile`: Output log file path

See individual script help for detailed parameters:
```powershell
Get-Help .\dummy_services.ps1 -Detailed
```
