# TDS - Task Discovery Service

A distributed task-to-IP registry service with support for both centralized and peer-to-peer (P2P) modes. TDS enables dynamic service discovery for microservices, load balancers, and distributed systems.

## Features

- **Dual Architecture**: Choose between centralized or decentralized operation modes
- **Centralized Mode**: Traditional client-server architecture with UDP/TCP transport and TUI monitoring
- **P2P Mode**: Decentralized distributed hash table (DHT) for fault-tolerant task discovery
- **Client Proxy**: Gateway between clients and the registry (supports both modes)
- **Round-Robin Load Balancing**: Automatic distribution of queries across multiple service instances
- **Heartbeat & Cleanup**: Automatic removal of stale service registrations
- **Interactive Monitoring**: Real-time TUI dashboard and command interface
- **Firewall-Aware Routing**: Filter query responses based on firewall rules
- **Persistent Storage (Optional)**: PostgreSQL backend with cache (store-backed registry)

## Architecture Overview

TDS supports two distinct operational modes, each optimized for different use cases:

### Centralized Mode

The traditional client-server architecture provides simplicity and strong consistency:

```
┌─────────┐      ┌──────────────┐      ┌────────────────┐
│ Client  │─────►│ Client Proxy │─────►│  TDS Server    │
└─────────┘ UDP  └──────────────┘ UDP  │   (UDP/TCP)    │
                                        └────────┬───────┘
                                                 │
                                                 ▼
                                        ┌────────────────┐
                                        │   Registry     │
                                        │  (In-Memory)   │
                                        └────────────────┘
```

**Key Characteristics:**
- Single authoritative server with complete view of all services
- Strong consistency - all clients see the same state immediately
- Built-in TUI (Terminal User Interface) for real-time monitoring
- UDP broadcast for server discovery
- Automatic cleanup of stale registrations (configurable timeout)
- Round-robin load balancing across multiple service instances
- Low latency (~1-2ms on local networks)
- Simple to deploy and debug

**When to Use Centralized Mode:**
- Development and testing environments
- Small to medium deployments (< 10k services)
- When strong consistency is required
- Environments with reliable central infrastructure
- When you need real-time monitoring via TUI

### P2P Mode (Distributed Hash Table)

Decentralized architecture using a DHT for scalability and fault tolerance:

```
┌─────────┐      ┌──────────────────┐
│ Client  │─────►│ Client Proxy A   │◄──┐
└─────────┘ UDP  │ + DHT Node       │   │
                 └──────────────────┘   │ TCP
                          │             │ (Peer
                          ▼             │ Gossip)
                 ┌──────────────────┐   │
                 │ Client Proxy B   │◄──┤
                 │ + DHT Node       │   │
                 └──────────────────┘   │
                          │             │
                          ▼             │
                 ┌──────────────────┐   │
                 │ Client Proxy C   │◄──┘
                 │ + DHT Node       │
                 └──────────────────┘

         DHT Ring (Consistent Hashing):
    Node A (ID: 0x1a3f...) ─┐
                            │
    Node C (ID: 0xf4e2...) ─┼─ Hash Ring
                            │
    Node B (ID: 0x7b9c...) ─┘
```

**Key Characteristics:**
- No central server - nodes form a self-organizing ring
- SHA256-based consistent hashing for task-to-node mapping
- Each node stores tasks it's responsible for (closest node ID)
- Automatic peer discovery via bootstrap nodes
- JSON-based inter-node protocol over TCP
- Eventual consistency - updates propagate through the network
- Scales horizontally - add nodes to increase capacity
- Fault tolerant - survives individual node failures

**DHT Consistent Hashing:**
```
Task: "web-api" → SHA256 → 0x3a7f... → Stored on Node B (closest ID)
Node: "192.168.1.10:6000" → SHA256 → 0x7b9c... → Position in ring
```

**When to Use P2P Mode:**
- Large-scale distributed systems (> 10k services)
- Edge computing and IoT networks
- Environments requiring high availability
- No reliable central infrastructure
- Geographic distribution across data centers
- When you need automatic failover without coordination

## Components

### Server (`cmd/server`) - Centralized Mode Only
The centralized TDS server with an interactive terminal UI:
- **Registry Management**: Stores task-to-address mappings in memory
- **Round-Robin Selection**: Distributes queries across multiple service instances
- **Heartbeat Processing**: Updates LastHeartbeat timestamp for registered services
- **Automatic Cleanup**: Removes stale registrations after configurable timeout (default: 60s)
- **UDP Broadcast**: Announces server presence on startup for discovery
- **TUI Dashboard**: Real-time visualization of:
  - All registered services and their addresses
  - Query statistics and distribution
  - Last heartbeat times
  - Interactive commands (stats, help, quit)
- **Dual Transport**: Supports both UDP and TCP connections

### Client Proxy (`cmd/client_proxy`)
Intelligent gateway that routes requests based on operational mode:

**Centralized Mode:**
- Discovers TDS server via UDP broadcast or static address
- Forwards REGISTER/QUERY/HEARTBEAT to server
- Maintains connection health checks
- Simple pass-through proxy

**P2P Mode (with `-p2p` flag):**
- Embeds a full DHT node implementation
- Performs consistent hashing to find responsible node
- Routes requests to correct peer via TCP
- Handles network topology changes
- Participates in peer discovery and maintenance
- Stores tasks for which it's responsible

**Interactive Commands:**
- `stats`: Display registration and query statistics
- `dht`: Show DHT ring information (P2P mode only)
- `peers`: List known peer nodes (P2P mode only)
- `help`: Show available commands
- `quit`: Graceful shutdown

### Client Library (`pkg/client`)
Simple Go client library for TDS integration:
```go
client := client.NewClient("localhost:5100")

// Register a service
err := client.Register("api-service", "192.168.1.50:8080")

// Query for a service
addr, err := client.Query("api-service")

// Send heartbeat
err := client.Heartbeat("api-service", "192.168.1.50:8080")
```

Supports both UDP and TCP transports with automatic timeout handling.

### DHT Package (`pkg/dht`)
Distributed hash table implementation for P2P mode:

**Core Features:**
- **Consistent Hashing**: SHA256-based node and task IDs
- **K-Replication**: Data replicated on k=3 closest nodes for fault tolerance
- **Ring Maintenance**: Automatic peer addition/removal
- **Task Storage**: Local storage for responsible tasks
- **Peer Discovery**: Join network via bootstrap nodes
- **Closest Node Lookup**: XOR distance metric for responsibility calculation
- **JSON Protocol**: Inter-node communication messages:
  - `PING/PONG`: Health checks and keepalive
  - `JOIN`: Network join requests
  - `PEERLIST`: Peer information exchange
  - `STORE`: Task registration to k-closest nodes
  - `FIND`: Task query from k-closest nodes
  - `FOUND/NOTFOUND`: Query responses

**Concurrency Model:**

The DHT uses a `sync.RWMutex` to protect shared state (ring, peers, storage). To avoid deadlocks and improve performance, we use the **`*Locked` pattern**:

```go
// Public methods - thread-safe, take lock
func (dht *DHT) AmIInKClosest(task string, k int) bool {
    dht.mutex.RLock()  // Takes lock
    defer dht.mutex.RUnlock()
    return dht.amIInKClosestLocked(task, k)
}

// Private *Locked methods - NOT thread-safe, assume lock held
func (dht *DHT) amIInKClosestLocked(task string, k int) bool {
    // No lock - caller must hold lock
    // Safe to access dht.ring directly
}
```

**Why `*Locked` methods?**

1. **Avoid Deadlock**: Go's `sync.RWMutex` doesn't support recursive locking. If a method holding a write lock called another method that tried to take a read lock, it would deadlock.

2. **Performance**: When already holding a lock (e.g., in `CleanupStaleData` iterating over storage), avoid repeated lock/unlock overhead.

3. **Atomicity**: Keep entire operation under one lock for consistent view of data structures.

```go
// ✅ Safe - one lock for entire operation
func (dht *DHT) CleanupStaleData() int {
    dht.mutex.Lock()
    defer dht.mutex.Unlock()
    
    for task := range dht.storage {
        if !dht.amIInKClosestLocked(task, k) {  // No lock
            delete(dht.storage, task)
        }
    }
}

// ❌ Would deadlock
func (dht *DHT) CleanupStaleData() int {
    dht.mutex.Lock()
    defer dht.mutex.Unlock()
    
    for task := range dht.storage {
        if !dht.AmIInKClosest(task, k) {  // Tries to RLock() → DEADLOCK!
            delete(dht.storage, task)
        }
    }
}
```

This is a standard Go idiom used throughout the standard library (e.g., `container/list`, `container/heap`).

### Registry Package (`pkg/registry`)
Core registry abstraction layer:
- **Interface**: `Registry` interface for pluggable implementations
- **MemoryRegistry**: In-memory implementation with mutex protection
- **ServiceEntry**: Metadata for registered services (address, heartbeat, query count)
- **Round-Robin**: Per-task round-robin index tracking
- **Statistics**: Total queries, service counts

### Transport Package (`pkg/transport`)
Network communication layer:
- **UDP Server**: Connectionless transport for low-latency operations
- **TCP Server**: Reliable transport for DHT peer communication
- **Protocol Parsing**: Message parsing and validation
- **Broadcast**: UDP broadcast for server discovery (Windows/Unix compatible)

## Quick Start

### Centralized Mode

**1. Start the server:**
```bash
go run ./cmd/server
```

Optional flags:
```bash
# Firewall-aware routing
go run ./cmd/server --firewall-rules firewall_rules.example

# PostgreSQL backend (also checks DATABASE_URL if --store-url is omitted)
go run ./cmd/server --store-url "postgres://user:pass@localhost:5432/tds?sslmode=disable"

# Headless mode (no TUI)
go run ./cmd/server --no-ui
```

**2. Start the client proxy (in another terminal):**
```bash
go run ./cmd/client_proxy
# Select UDP or TCP when prompted
```

**3. Test with the client:**
```bash
# Register a service
go run ./cmd/test_client REGISTER my-api 192.168.1.50:8080

# Query the service
go run ./cmd/test_client QUERY my-api
# Returns: 192.168.1.50:8080
```

### P2P Mode

**1. Start first P2P node (creates the network):**
```bash
go run ./cmd/client_proxy -p2p -p2p-port :6000
```

**2. Start second node (joins via bootstrap):**
```bash
TDS_PROXY_LISTEN=:5101 go run ./cmd/client_proxy -p2p -p2p-port :6001 -bootstrap localhost:6000
```

**3. Start third node (can bootstrap from any existing node):**
```bash
TDS_PROXY_LISTEN=:5102 go run ./cmd/client_proxy -p2p -p2p-port :6002 -bootstrap localhost:6000,localhost:6001
```

**4. Test distributed registration and query:**
```bash
# Register on node A (client proxy listens on 5100)
go run ./cmd/test_client -server localhost:5100 REGISTER web-service 10.0.0.5:8080

# Query from node B (automatically finds data via DHT routing)
go run ./cmd/test_client -server localhost:5101 QUERY web-service
# Returns: 10.0.0.5:8080

# Query from node C (also finds data regardless of registration point)
go run ./cmd/test_client -server localhost:5102 QUERY web-service
# Returns: 10.0.0.5:8080
```

**How P2P Lookup Works:**
1. Task name "web-service" is hashed → `0x3a7f...`
2. DHT finds closest node ID to `0x3a7f...` (e.g., Node B at `0x7b9c...`)
3. Request is routed to Node B (even if you queried from Node A or C)
4. Node B returns the stored address

### Using the Demo Scripts

Automated test scripts are provided in [test_scripts](test_scripts):

**P2P Network Demo:**
```powershell
# Start a 5-node P2P network automatically
.\demos\p2p\demo_p2p.ps1

# Or start nodes individually
.\test_scripts\p2p\node_1_start.ps1  # Bootstrap node
.\test_scripts\p2p\node_2_start.ps1  # Joins node 1
.\test_scripts\p2p\node_3_start.ps1  # Joins nodes 1,2
# ... etc

# Test the full network
.\test_scripts\p2p\test_p2p_network.ps1
```

## Building

```bash
# Build all components
go build ./...

# Build specific components
go build -o bin/server.exe ./cmd/server
go build -o bin/client_proxy.exe ./cmd/client_proxy
go build -o bin/test_client.exe ./cmd/test_client

# Build with race detector (useful for debugging concurrency issues)
go build -race -o bin/client_proxy_race.exe ./cmd/client_proxy
```

## Performance Testing

### Load Testing the Centralized Server

A comprehensive load test script is provided to test server performance under concurrent load:

```powershell
# Run with default settings (10 clients, 100 ops each, UDP)
.\test_scripts\centralized\load_test_server.ps1

# Custom configuration
.\test_scripts\centralized\load_test_server.ps1 -NumClients 20 -RegistrationsPerClient 500 -QueriesPerClient 500 -Protocol tcp

# Test parameters:
#   -NumClients: Number of concurrent client simulations (default: 10)
#   -RegistrationsPerClient: Number of registrations per client (default: 100)
#   -QueriesPerClient: Number of queries per client (default: 100)
#   -Protocol: "udp" or "tcp" (default: "udp")
#   -ServerAddr: Server address (default: "127.0.0.1:5000")
```

**What it tests:**
- **Phase 1**: Concurrent registration load - all clients register tasks simultaneously
- **Phase 2**: Concurrent query load - all clients query tasks simultaneously
- Reports throughput, success rates, latency, and error details

**Example output:**
```
========================================
Registration Results:
-------------------------------------
  Total Successful: 1000
  Total Failed: 0
  Total Duration: 1.32s
  Overall Throughput: 758.42 registrations/sec
  Avg Client Throughput: 1241.01 registrations/sec

========================================
Query Results:
-------------------------------------
  Total Successful: 1000
  Total Not Found: 0
  Total Failed: 0
  Total Duration: 1.45s
  Overall Throughput: 689.66 queries/sec
  Avg Client Throughput: 1180.25 queries/sec
```

**Prerequisites:**
- Server must be running: `go run ./cmd/server`
- Script uses PowerShell jobs for concurrent client simulation
- No external dependencies required

## Firewall-Aware Routing

The server can filter service responses based on network firewall rules to ensure clients only receive addresses they can reach.

### Firewall Rules File Format

```
# Format: source_ip_or_cidr destination_ip_or_cidr
192.168.1.10 10.0.0.5
192.168.1.0/24 10.0.0.0/24
```

See [docs/FIREWALL_RULES.md](docs/FIREWALL_RULES.md) for complete documentation.

## Centralized Mode Protocol (JSON)

The centralized UDP/TCP servers use a simple JSON message protocol.

### Registration
```json
{"cmd":"REGISTER","task":"my_task","address":"10.0.0.5:8080"}
```

### Query
```json
{"cmd":"QUERY","task":"my_task"}
```

### Responses
- `{"status":"OK"}` (REGISTER success)
- `{"status":"OK","address":"10.0.0.5:8080"}` (QUERY success)
- `{"status":"NOTFOUND"}` (no service registered)
- `{"status":"FORBIDDEN"}` (no service allowed by firewall rules)
- `{"status":"ERR","error":"..."}`

## Server Flags

```
--port <n>                 Listen port (default: 5000)
--firewall-rules <path>    Path to firewall rules file (optional)
--store-url <url>          PostgreSQL connection URL (optional; falls back to DATABASE_URL)
--cache-max-size <n>       Max tasks to keep in cache (0 = unlimited)
--heartbeat-timeout <dur>  Timeout for service heartbeats (default: 60s)
--cleanup-interval <dur>   Cleanup interval for stale entries (default: 10s)
--no-ui                    Run without interactive TUI (alias: --no-tui)
--force-ui, --ui           Force TUI mode without prompting
--udp                      Use UDP transport
--tcp                      Use TCP transport
--tls                      Use TLS transport (mutual auth)
```

## Testing

```bash
# Run all tests
go test ./...

# Run tests from tests/ directory
go test ./tests/...

# Run with verbose output
go test -v ./tests/...

# Run with race detector
go test -race ./...

# Run specific test
go test ./tests -run TestDHTCreation
```

## Configuration

### Environment Variables

- **`TDS_SERVER_ADDR`**: Server address for client proxy to connect to (default: `127.0.0.1:5000`)
  - Used in centralized mode only
  - Example: `export TDS_SERVER_ADDR=10.0.1.5:5000`

- **`TDS_PROXY_LISTEN`**: Address for client proxy to listen on (default: `:5100`)
  - Used in both centralized and P2P modes
  - Example: `export TDS_PROXY_LISTEN=:5200`

### Command-Line Flags (client_proxy)

- **`-p2p`**: Enable peer-to-peer mode (default: `false`)
  - When disabled, operates in centralized mode

- **`-p2p-port <port>`**: DHT peer communication port (default: `:6000`)
  - Only used in P2P mode
  - Each node must use a unique port
  - Example: `-p2p-port :6001`

- **`-bootstrap <addresses>`**: Comma-separated bootstrap node addresses
  - Required for P2P mode (except for the first/bootstrap node)
  - Example: `-bootstrap localhost:6000,192.168.1.10:6000`

### Server Configuration (cmd/server/main.go)

Centralized server settings:
- **`ListenPort`**: Server UDP/TCP port (default: `5000`)
- **`HeartbeatTimeout`**: Time before removing stale entries (default: `60s`)
- **`CleanupInterval`**: Cleanup task frequency (default: `10s`)
- **`BroadcastInterval`**: Server discovery broadcast frequency (default: `5s`)

## Protocol

### Client ↔ Proxy/Server (UDP)

Simple text-based protocol for client operations:

**REGISTER** - Register a service
```
Request:  REGISTER <task> <address>
Response: OK | ERR <message>

Example:
  → REGISTER web-api 192.168.1.50:8080
  ← OK
```

**QUERY** - Query for a service
```
Request:  QUERY <task>
Response: <address> | NOTFOUND | ERR <message>

  # Run with default settings (10 clients, 100 ops each, UDP)
  .\test_scripts\centralized\load_test_server.ps1
  ← 192.168.1.50:8080
```

**HEARTBEAT** - Keep registration alive
```
Request:  HEARTBEAT <task> <address>
Response: OK | ERR <message>

Example:
  → HEARTBEAT web-api 192.168.1.50:8080
  ← OK
```

### Proxy ↔ Server (Centralized Mode - UDP/TCP)

Same protocol as Client ↔ Proxy, forwarded transparently.

### DHT Inter-Node Protocol (P2P Mode - JSON over TCP)

Nodes communicate using JSON-encoded messages for structured data exchange:

```json
{
  "type": "PING|PONG|JOIN|PEERLIST|STORE|FIND|FOUND|NOTFOUND|REDIRECT",
  "sender": "192.168.1.10:6000",
  "payload": { ... }
}
```

**Message Types:**

- **PING/PONG**: Health checks and keepalive
  ```json
  {"type": "PING", "sender": "192.168.1.10:6000"}
  {"type": "PONG", "sender": "192.168.1.11:6000"}
  ```

- **JOIN**: New node joining the network
  ```json
  {
    "type": "JOIN",
    "sender": "192.168.1.12:6000",
    "payload": {"nodeID": "0x3a7f...", "address": "192.168.1.12:6000"}
  }
  ```

- **PEERLIST**: Share known peers
  ```json
  {
    "type": "PEERLIST",
    "sender": "192.168.1.10:6000",
    "payload": {
      "peers": [
        {"id": "0x1a3f...", "address": "192.168.1.10:6000"},
        {"id": "0x7b9c...", "address": "192.168.1.11:6000"}
      ]
    }
  }
  ```

- **STORE**: Replicate task registration
  ```json
  {
    "type": "STORE",
    "sender": "192.168.1.10:6000",
    "payload": {"task": "web-api", "address": "10.0.0.5:8080"}
  }
  ```

- **FIND**: Query for task
  ```json
  {
    "type": "FIND",
    "sender": "192.168.1.10:6000",
    "payload": {"task": "web-api"}
  }
  ```

- **FOUND**: Task found response
  ```json
  {
    "type": "FOUND",
    "sender": "192.168.1.11:6000",
    "payload": {"task": "web-api", "addresses": ["10.0.0.5:8080", "10.0.0.6:8080"]}
  }
  ```

- **NOTFOUND**: Task not found
  ```json
  {
    "type": "NOTFOUND",
    "sender": "192.168.1.11:6000",
    "payload": {"task": "web-api"}
  }
  ```

- **REDIRECT**: Forward to responsible node
  ```json
  {
    "type": "REDIRECT",
    "sender": "192.168.1.10:6000",
    "payload": {"node": "192.168.1.11:6000", "nodeID": "0x7b9c..."}
  }
  ```

## Use Cases

### Microservices Discovery
Register microservices and allow other services to discover them dynamically:
```bash
# Service startup registers itself
REGISTER user-service 10.0.1.5:8080
REGISTER user-service 10.0.1.6:8080  # second instance

# API gateway queries for available instances
QUERY user-service  # returns 10.0.1.5:8080 (round-robin)
QUERY user-service  # returns 10.0.1.6:8080
```

### Load Balancer Backend Pool
Use TDS as a simple service registry for load balancers:
```bash
REGISTER web-backend 10.0.2.10:80
REGISTER web-backend 10.0.2.11:80
REGISTER web-backend 10.0.2.12:80

# Load balancer queries to get next backend
QUERY web-backend  # automatically rotates through instances
```

### Edge Computing / IoT
P2P mode is ideal for edge networks where centralized servers are impractical:
- Nodes automatically discover each other
- No single point of failure
- Tasks distributed across edge nodes

## Performance

### Centralized Mode
- **Latency**: ~1-2ms for local network queries
- **Throughput**: ~10k+ queries/sec on single server
- **Scalability**: Vertical (single server bottleneck)
- **Consistency**: Strong (immediate)
- **Network Overhead**: Minimal (direct client→server)
- **Best For**: Small to medium deployments, development, testing

### P2P Mode
- **Latency**: ~5-10ms (includes DHT lookup and potential multi-hop)
- **Throughput**: Scales linearly with node count
- **Scalability**: Horizontal (add more nodes)
- **Consistency**: Eventual (propagation delay)
- **Network Overhead**: Medium (peer gossip, DHT maintenance)
- **Best For**: Large-scale deployments, high availability requirements

## Comparison: Centralized vs P2P

| Feature | Centralized Mode | P2P Mode (DHT) |
|---------|------------------|----------------|
| **Setup Complexity** | Low - single server | Medium - bootstrap coordination |
| **Deployment** | 1 server + N proxies | N peer nodes |
| **Scalability** | Limited (single server) | High (horizontal) |
| **Single Point of Failure** | Yes | No |
| **Consistency** | Strong | Eventual |
| **Latency** | Very Low (1-2ms) | Low (5-10ms) |
| **Network Overhead** | Minimal | Medium (gossip) |
| **Discovery** | UDP broadcast | Bootstrap nodes |
| **Monitoring** | Built-in TUI | Distributed (per-node) |
| **Debugging** | Easy (centralized logs) | Complex (distributed traces) |
| **Fault Tolerance** | Restart server | Automatic (ring rebalance) |
| **Load Distribution** | Server decides | Hash-based automatic |
| **Best For** | Dev/test, small scale | Production, large scale |

## Limitations & Considerations

### Current Limitations
- **No authentication/authorization**: Trusts all clients and nodes
- **No encryption**: All communication in plaintext (UDP/TCP)
- **No persistent storage**: All data in-memory only (lost on restart)
- **P2P eventual consistency**: Updates take time to propagate
- **No data replication**: Single copy of each task registration (P2P mode)
- **Limited network partition handling**: Split-brain scenarios not fully resolved

### Centralized Mode Specific
- **Single point of failure**: Server restart loses all registrations
- **Vertical scaling limits**: One server handles all load
- **No geographic distribution**: All data in one location

### P2P Mode Specific
- **Bootstrap dependency**: Need at least one reachable bootstrap node
- **Network churn overhead**: Frequent node joins/leaves impact performance
- **Debugging complexity**: Distributed logs and traces
- **Consistency delays**: Registration may not be immediately queryable

## Monitoring & Operations

### Centralized Mode - TUI Dashboard

The server includes a real-time terminal interface:
- **Services View**: All registered tasks and their addresses
- **Statistics**: Total registrations, queries, query distribution
- **Heartbeat Monitoring**: Last seen timestamp for each service
- **Interactive Commands**: Type commands directly in the TUI
  - `stats` - Show detailed statistics
  - `help` - List all commands
  - `quit` - Graceful shutdown

### P2P Mode - Interactive Console

Each client proxy node provides a command console:
- `stats` - Registration and query counters
- `dht` - DHT ring information:
  - Current node ID and position
  - Number of peers in ring
  - Tasks stored locally
  - Ring size and distribution
- `peers` - List of known peer nodes with IDs and addresses
- `help` - Available commands
- `quit` - Graceful shutdown

### Troubleshooting

**Centralized Mode:**
- **Server not found**: Check UDP broadcast works on your network, or set `TDS_SERVER_ADDR` manually
- **Services timing out**: Increase `HeartbeatTimeout` or implement automatic heartbeat in clients
- **High latency**: Check network between client→proxy→server

**P2P Mode:**
- **Node can't join**: Verify bootstrap node(s) are reachable via TCP on p2p-port
- **Tasks not found after registration**: Wait for DHT propagation (typically < 1 second)
- **High error rate**: Check inter-node network latency, ensure 3+ nodes in ring
- **Ring instability**: Reduce node churn, implement retry logic for transient failures

## Future Enhancements

### Known Issues & TODOs

#### P2P DHT Data Consistency
**Problem:** When a node joins the DHT network with incomplete peer knowledge, it may store data on incorrect nodes:

1. **Incomplete Ring Discovery**: A newly joined node may only know about bootstrap nodes initially
2. **Incorrect Storage Location**: The node calculates k-closest based on incomplete ring, storing data on wrong nodes
3. **Data Loss During Cleanup**: The `CleanupStaleData()` function currently **deletes** misplaced data instead of transferring it to correct nodes
4. **Query Failures**: Queries to the correct nodes return NOTFOUND because data is stored elsewhere

**Current Mitigation:**
- K-closest replication (k=3) provides some redundancy
- Passive peer discovery via message exchange gradually improves ring knowledge
- No forwarding prevents infinite loops

**TODO - High Priority:**
- [ ] **Implement data transfer in CleanupStaleData()**: Transfer misplaced data to correct k-closest nodes instead of deleting
- [ ] **Add TTL/hop limit**: Prevent potential forwarding storms if forwarding is re-enabled
- [ ] **Implement visited-list tracking**: Detect and break forwarding loops
- [ ] **Add stabilization protocol**: Periodic data redistribution when ring membership changes
- [ ] **Implement anti-entropy**: Background sync to fix inconsistencies between replicas

**Workaround for Production:**
- Ensure workers send periodic heartbeat/re-registrations
- Use larger replication factor (k=5 or higher) for critical tasks
- Pre-seed new nodes with complete peer list before allowing them to store data

---

### Planned Features
- [ ] **TLS/Encryption**: Secure communication for production use
- [ ] **Authentication & Authorization**: Token-based access control
- [ ] **Persistent Storage**: PostgreSQL backend implementation (started in `pkg/store/postgres`)
- [ ] **Data Replication**: Store tasks on multiple nodes (P2P mode) ✅ **Implemented (k=3)**
- [ ] **DHT Stabilization**: Improved handling of node churn

- [ ] **Health Checks**: Active probing of registered services
- [ ] **Service Metadata**: Tags, versions, weights for advanced routing


### Under Consideration
- Hybrid mode (centralized discovery + P2P failover)
- Raft consensus for strong consistency in P2P
- 

## Documentation

### Project Files
- [README.md](README.md) - This file (comprehensive guide)


