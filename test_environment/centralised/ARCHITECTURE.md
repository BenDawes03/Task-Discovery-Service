# TDS Test Environment - Visual Architecture Guide

## System Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                         TDS Test Environment                         │
└─────────────────────────────────────────────────────────────────────┘

                         ┌───────────────────┐
                         │   TDS Server      │
                         │   Port: 5000      │
                         │                   │
                         │  ┌─────────────┐  │
                         │  │  Registry   │  │
                         │  │  (Memory/   │  │
                         │  │   Postgres) │  │
                         │  └─────────────┘  │
                         └─────────┬─────────┘
                                   │
                   ┌───────────────┼───────────────┐
                   │               │               │
                   │ REGISTER      │ QUERY         │
                   │               │               │
         ┌─────────▼─────────┐     └──────┐            │
         │                   │            │        │
         │  Dummy Services   │            │        │
         │  (dummy_services.ps1)          │        │
         │                   │            │        │
         │  ┌─────────────┐  │            │        │
         │  │ Service 1   │◄─┼────────────┘        │
         │  │ task_1      │  │                     │
         │  │ :8000       │  │                     │
         │  └──────▲──────┘  │                     │
         │         │          │                     │
         │  ┌──────┴──────┐  │                     │
         │  │ Service 2   │  │                     │
         │  │ task_2      │  │                     │
         │  │ :8001       │  │                     │
         │  └──────▲──────┘  │                     │
         │         │          │                     │
         │  ┌──────┴──────┐  │                     │
         │  │ Service 3   │  │                     │
         │  │ task_3      │  │                     │
         │  │ :8002       │  │                     │
         │  └──────▲──────┘  │                     │
         │         │          │                     │
         └─────────┼──────────┘                     │
                   │                                │
                   │ DATA TRANSFER                  │
                   │                                │
         ┌─────────┴──────────┐          ┌─────────▼─────────┐
         │                    │          │                   │
         │   Client Threads   │◄─────────┤  Client Queries   │
         │  (client_queries.ps1)         │  (client_queries.ps1)
         │                    │          │                   │
         │  Thread 1 ─────────┼──┐       │                   │
         │  Thread 2 ─────────┼──┼───────┤  1. Query TDS     │
         │  Thread 3 ─────────┼──┼───────┤  2. Get address   │
         │  ...               │  │       │  3. Send data     │
         │                    │  │       │  4. Get ack       │
         └────────────────────┘  │       └───────────────────┘
                                 │
                                 └─ Each thread:
                                    • Queries TDS for task
                                    • Receives service address
                                    • Sends JSON payload
                                    • Waits for acknowledgment
```

## Message Flow Diagram

```
Sequence Diagram: Complete Request Flow

Client          TDS Server      Service (Dummy)
  │                 │                 │
  │  1. REGISTER    │                 │
  │  ◄──────────────┼─────────────────┤
  │     (task, addr)│                 │
  │                 │                 │
  │  OK             │                 │
  ├─────────────────┼────────────────►│
  │                 │                 │
  │                 │   [Service starts listening on :8000]
  │                 │                 │
  ├─ 2. QUERY ─────►│                 │
  │    (task_1)     │                 │
  │                 │                 │
  │  OK + :8000 ◄───┤                 │
  │                 │                 │
  │                 │                 │
  ├─ 3. DATA ───────┼────────────────►│
  │  {task, data,   │                 │
  │   timestamp}    │                 │
  │                 │                 │
  │                 │  [Process]      │
  │                 │                 │
  │  4. ACK ◄───────┼─────────────────┤
  │  {status: OK,   │                 │
  │   service_id,   │                 │
  │   bytes}        │                 │
  │                 │                 │
```

## File Responsibilities

```
┌──────────────────────────────────────────────────────────────────┐
│ dummy_services.ps1                                                │
├──────────────────────────────────────────────────────────────────┤
│ • Registers N services with TDS                                   │
│ • Starts UDP/TCP listener for each service                        │
│ • Processes incoming data from clients                            │
│ • Sends acknowledgment responses                                  │
│ • Tracks: registrations, requests served, bytes processed        │
│ • Sends periodic heartbeats to TDS                                │
└──────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────┐
│ client_queries.ps1                                                │
├──────────────────────────────────────────────────────────────────┤
│ • Spawns N client threads                                         │
│ • Each thread:                                                    │
│   1. Queries TDS for task → gets service address                 │
│   2. Sends test payload to service address                        │
│   3. Waits for acknowledgment                                     │
│ • Tracks: query success/fail, data transfer success/fail          │
│ • Reports: QPS, success rates, data throughput                    │
└──────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────┐
│ orchestrator.ps1                                                  │
├──────────────────────────────────────────────────────────────────┤
│ • Coordinates multi-VM testing                                    │
│ • Deploys scripts to client VMs via SSH/SCP                       │
│ • Starts dummy_services.ps1 on each client VM                     │
│ • Starts client_queries.ps1 on each client VM                     │
│ • Collects logs and aggregates statistics                         │
│ • Provides per-VM and combined metrics                            │
└──────────────────────────────────────────────────────────────────┘

┌──────────────────────────────────────────────────────────────────┐
│ demo_local.ps1                                                    │
├──────────────────────────────────────────────────────────────────┤
│ • Quick demonstration on single machine                           │
│ • Starts services and clients as background jobs                  │
│ • Monitors progress                                               │
│ • Shows educational output explaining what's happening            │
│ • Good for learning and testing changes locally                   │
└──────────────────────────────────────────────────────────────────┘
```

## Data Structures

### Service Registration Message (to TDS)
```json
{
  "cmd": "REGISTER",
  "task": "task_1",
  "address": "192.168.0.180:8000"
}
```

### Query Message (to TDS)
```json
{
  "cmd": "QUERY",
  "task": "task_1"
}
```

### Query Response (from TDS)
```json
{
  "status": "OK",
  "address": "192.168.0.180:8000"
}
```

### Data Payload (Client → Service)
```json
{
  "task": "task_1",
  "data": "Base64EncodedPayload...",
  "timestamp": "2026-02-04T10:30:45.123Z",
  "client_id": "CLIENT-VM-01"
}
```

### Acknowledgment (Service → Client)
```json
{
  "status": "OK",
  "service_id": "service_42",
  "received_bytes": 256,
  "timestamp": "2026-02-04T10:30:45.234Z"
}
```

## Metrics Collected

### TDS Server (visible in TUI)
```
┌─────────────────────────────────────────┐
│ Service Registry Stats                  │
├─────────────────────────────────────────┤
│ Total Services: 150                     │
│ Total Queries:  45,823                  │
│                                         │
│ Per-Service:                            │
│ task_1 → 192.168.0.180:8000 [Q: 5,234] │
│ task_2 → 192.168.0.180:8001 [Q: 4,891] │
│ task_3 → 192.168.0.181:8000 [Q: 5,102] │
└─────────────────────────────────────────┘
```

### Service Side (dummy_services.ps1 log)
```
[10:30:45] Total registrations: 150, Success Rate: 100%
[10:30:45] Requests Served: 42,150
[10:30:45] Data Processed: 10,790,400 bytes (10.29 MB)
```

### Client Side (client_queries.ps1 log)
```
Query Statistics:
  Total Queries: 45,000
  Successful: 44,850 (99.67%)
  Failed: 150
  Overall QPS: 150.2

Data Transfer Statistics:
  Total Attempts: 44,850
  Successful: 42,150 (94.0%)
  Total Data Sent: 10.29 MB
```

## Port Assignments

```
┌────────────────────────────────────────────┐
│ Port Allocation                            │
├────────────────────────────────────────────┤
│ TDS Server:     5000 (UDP/TCP/TLS)         │
│ Services:       8000-8500 (configurable)   │
│                                            │
│ Example:                                   │
│ • 50 services → ports 8000-8049            │
│ • Each service binds to one port           │
│ • Round-robin prevents port conflicts      │
└────────────────────────────────────────────┘
```

## Multi-VM Deployment Topology

```
┌─────────────────────────────────────────────────────────────┐
│                    Network: 192.168.0.0/24                   │
└─────────────────────────────────────────────────────────────┘

    ┌──────────────────────┐
    │  Server VM           │
    │  192.168.0.181       │
    │                      │
    │  ┌────────────────┐  │
    │  │  TDS Server    │  │
    │  │  :5000         │  │
    │  └────────────────┘  │
    └───────────┬──────────┘
                │
    ┌───────────┼─────────────────────────┐
    │           │                         │
┌───▼───────────▼───┐  ┌─────────────────▼─────┐
│ Client VM 1        │  │ Client VM 2            │
│ 192.168.0.180      │  │ 192.168.0.182          │
│                    │  │                        │
│ ┌────────────────┐ │  │ ┌────────────────────┐ │
│ │ 50 Services    │ │  │ │ 50 Services        │ │
│ │ :8000-8049     │ │  │ │ :8000-8049         │ │
│ └────────────────┘ │  │ └────────────────────┘ │
│                    │  │                        │
│ ┌────────────────┐ │  │ ┌────────────────────┐ │
│ │ 10 Clients     │ │  │ │ 10 Clients         │ │
│ │ (threads)      │ │  │ │ (threads)          │ │
│ └────────────────┘ │  │ └────────────────────┘ │
└────────────────────┘  └────────────────────────┘

Orchestrator runs on developer machine:
• Deploys scripts via SSH/SCP
• Starts processes via SSH
• Collects logs via SCP
• Aggregates statistics
```

## Concurrency Model

### Service Side
```
Main Thread:
├─ Register with TDS
├─ Spawn N listener jobs (one per service)
│  └─ Each job:
│     ├─ Bind to port
│     ├─ Listen loop (non-blocking)
│     │  ├─ Receive message
│     │  ├─ Parse JSON
│     │  ├─ Process data
│     │  ├─ Update stats (synchronized)
│     │  └─ Send acknowledgment
│     └─ Repeat
└─ Heartbeat loop
   ├─ Sleep for interval
   ├─ Re-register all services
   ├─ Display stats
   └─ Repeat
```

### Client Side
```
Main Thread:
├─ Generate test payload
├─ Spawn N client jobs
│  └─ Each job:
│     ├─ Loop for X queries
│     │  ├─ Query TDS (Phase 1)
│     │  │  └─ Get service address
│     │  ├─ Send data to service (Phase 2)
│     │  │  └─ Wait for ack
│     │  ├─ Update stats
│     │  └─ Think time
│     └─ Return stats
└─ Collect and aggregate results
```

## Testing Scenarios

### Scenario 1: Basic Functionality
```powershell
# Verify end-to-end flow works
.\demo_local.ps1 -NumServices 3 -NumClients 1 -Duration 10

Expected:
✓ 3 services register
✓ 1 client queries and sends data
✓ Services receive and acknowledge
✓ All success rates > 95%
```

### Scenario 2: Load Testing
```powershell
# High concurrency
.\demo_local.ps1 -NumServices 50 -NumClients 20 -Duration 60

Expected:
✓ High QPS (>500)
✓ Success rate > 90%
✓ Services handle concurrent requests
```

### Scenario 3: Multi-VM Scale
```powershell
# Distributed load
.\orchestrator.ps1 -ClientVMs @("VM1", "VM2", "VM3") -TestDuration 300

Expected:
✓ 150 total services (50 per VM)
✓ 30 total clients (10 per VM)
✓ Combined QPS > 1000
✓ Statistics from all VMs
```

### Scenario 4: Service Discovery Accuracy
```powershell
# Verify round-robin and load distribution
.\demo_local.ps1 -NumServices 10 -NumClients 5 -Duration 120

Check in TDS TUI:
✓ Query counts roughly equal across services
✓ Round-robin working correctly
✓ No service overwhelmed
```

## Troubleshooting Flowchart

```
┌─────────────────────────┐
│ Test fails?             │
└────────────┬────────────┘
             │
             ▼
      ┌──────────────┐
      │ Check logs   │
      └──────┬───────┘
             │
      ┌──────▼───────────────────────┐
      │ Where is the failure?        │
      └──┬────────────┬──────────────┘
         │            │
         ▼            ▼
┌─────────────┐  ┌──────────────────┐
│TDS Queries  │  │Data Transfer     │
│Failing?     │  │Failing?          │
└──────┬──────┘  └────────┬─────────┘
       │                  │
       ▼                  ▼
┌─────────────────┐  ┌──────────────────────┐
│• Check TDS      │  │• Check services      │
│  server logs    │  │  are listening       │
│• Verify TDS     │  │  (netstat)           │
│  is running     │  │• Test direct         │
│• Check network  │  │  connection          │
│  to TDS         │  │  (Test-NetConnection)│
│• Firewall rules │  │• Firewall rules      │
│  for :5000      │  │  for :8000-8500      │
└─────────────────┘  └──────────────────────┘
```

## Performance Expectations

### Single Machine (Typical Developer Laptop)
```
Services:     50
Clients:      10
Expected QPS: 100-200
Success Rate: >95%
Duration:     60s
```

### Multi-VM (Lab Environment)
```
VMs:          3 clients + 1 server
Services:     150 total (50 per VM)
Clients:      30 total (10 per VM)
Expected QPS: 500-1000
Success Rate: >90%
Duration:     300s
```

### Production-Scale (Hypothetical)
```
VMs:          10 clients + 1 server
Services:     500 total
Clients:      100 total
Expected QPS: 2000-5000
Success Rate: >95%
Duration:     Long-running
```

## Summary

The test environment provides:
- ✅ Complete end-to-end testing
- ✅ Realistic service behavior
- ✅ Comprehensive metrics
- ✅ Multi-VM orchestration
- ✅ Easy local development
- ✅ Scalable architecture
- ✅ Clear documentation
