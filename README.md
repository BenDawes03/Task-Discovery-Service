# TDS — Task Discovery Service

TDS is a task-to-IP registry built as a Final Year Project. Services register themselves under a task name; clients query to get an address back, with round-robin load balancing across multiple instances.

Two operational modes are supported:

- **Centralized** — a single authoritative server (UDP/TCP/TLS), interactive TUI, optional PostgreSQL persistence with LFU cache.
- **P2P (DHT)** — a self-organising ring of peers using consistent hashing, no central server needed.

An end-to-end transit simulation (`Simulation/`) uses TDS as its service registry, and a Kubernetes deployment (`k8s/`) wraps the whole thing in containers.

---

## Repository Layout

```
cmd/
  server/               Centralized TDS server (TUI, UDP/TCP/TLS, optional Postgres)
  client_proxy/         Gateway: relays client requests to server or DHT network
  ticketdistributor/    Simulation component
  cache_demo/           Interactive demo: in-memory cache vs DB-backed latency
  client_demo/          Simple client smoke-test binary
  test_client/          CLI for manual REGISTER/QUERY/HEARTBEAT
  test_json_client/     JSON-protocol variant of test_client
  demo/                 Small stand-alone demo binary

pkg/
  registry/             Registry interface + MemoryRegistry + StoreBackedRegistry
  transport/            UDP and TCP server implementations
  client/               Go client library (Register, Query, Heartbeat)
  dht/                  DHT (consistent hashing, k=3 replication)
  firewall/             Firewall rule parser and IP-range filter
  store/                Store interface + PostgreSQL implementation
  netutil/              Network utilities

Simulation/             End-to-end transit simulation (Gate, Station, CS, PCTRBO, PA)
k8s/                    Kubernetes manifests + build/deploy scripts
demos/
  centralised/          Demo scripts for centralized mode
  p2p/                  Demo scripts for P2P mode
  cache/                Cache performance demo
test_scripts/           Go integration tests + PowerShell test harnesses
```

---

## Architecture

### Centralized Mode

```
Client
  │  UDP / TCP / TLS (JSON)
  ▼
Client Proxy  ──────►  TDS Server
(cmd/client_proxy)      (cmd/server)
                            │
                     ┌──────┴──────┐
                     │             │
                 MemoryRegistry  StoreBackedRegistry
                 (in-memory)     (Postgres + LFU cache)
```

- One authoritative server holds all task→address mappings.
- Round-robin load balances across multiple instances of the same task.
- Optional LFU cache keeps the top-N most-queried tasks in memory; misses fall through to Postgres.
- Firewall rules restrict which addresses a requestor is allowed to receive.
- Real-time TUI dashboard shows live registrations, query counts, and heartbeat ages.

### P2P Mode (DHT)

```
Client
  │  UDP (text protocol)
  ▼
Client Proxy (+ DHT node)  ◄──TCP──►  Other DHT peers
```

- Each `client_proxy` embeds a DHT node.
- Task names are hashed (SHA-256) onto a consistent-hash ring.
- The k=3 closest nodes store each task for fault tolerance.
- Peers share their peer lists on join; no central coordinator needed.

---

## Quick Start

### Prerequisites

```bash
go build ./...
```

### Centralized — in-memory

```bash
# Terminal 1
go run ./cmd/server --tcp --port 5000

# Terminal 2
go run ./cmd/client_proxy

# Terminal 3
go run ./cmd/test_client REGISTER my-api 10.0.0.5:8080
go run ./cmd/test_client QUERY my-api
# → 10.0.0.5:8080
```

### Centralized — firewall demo startup

Use this command when running the interactive demo in `cmd/demo` so the firewall step has matching rules:

```bash
go run ./cmd/server --tcp --firewall --firewall-rules demos/centralised/firewall_demo.rules
```

Rules file location: `demos/centralised/firewall_demo.rules`

### Centralized — with PostgreSQL

```bash
go run ./cmd/server \
  --tcp \
  --store-url "postgres://user:pass@localhost:5432/tds?sslmode=disable" \
  --cache-max-size 100
```

Or set `DATABASE_URL` in the environment; the server reads it automatically.

### P2P Mode

```bash
# Node 1 – bootstrap
go run ./cmd/client_proxy -p2p -p2p-port :6000

# Node 2
TDS_PROXY_LISTEN=:5101 go run ./cmd/client_proxy -p2p -p2p-port :6001 -bootstrap localhost:6000

# Node 3
TDS_PROXY_LISTEN=:5102 go run ./cmd/client_proxy -p2p -p2p-port :6002 -bootstrap localhost:6000

# Register on node 1, query from node 3 — DHT routes automatically
go run ./cmd/test_client -server localhost:5100 REGISTER web-api 10.0.0.5:8080
go run ./cmd/test_client -server localhost:5102 QUERY web-api
# → 10.0.0.5:8080
```

---

## Server Flags (`cmd/server`)

| Flag | Default | Description |
|---|---|---|
| `--port` | `5000` | Listen port |
| `--tcp` / `--udp` / `--tls` | — | Transport (default: prompted interactively) |
| `--tls-cert` | `certs/server.crt` | TLS certificate |
| `--tls-key` | `certs/server.key` | TLS private key |
| `--tls-client-ca` | `certs/ca.crt` | CA cert for mutual TLS |
| `--heartbeat-timeout` | `1m` | Remove entries silent for longer than this |
| `--cleanup-interval` | `10s` | Frequency of stale-entry scans |
| `--max-udp-handlers` | `1000` | Concurrent UDP goroutine cap |
| `--max-tcp-connections` | `5000` | Concurrent TCP connection cap |
| `--store-url` | — | PostgreSQL URL (also reads `DATABASE_URL`) |
| `--cache-max-size` | `0` | LFU cache size in tasks (0 = unlimited) |
| `--firewall` | — | Enable firewall-aware routing |
| `--no-firewall` | — | Disable firewall routing (overrides `--firewall-rules`) |
| `--firewall-rules` | — | Path to firewall rules file |
| `--log-dir` | `logs` | Directory for log files |
| `--no-ui` / `--no-tui` | — | Headless mode |
| `--force-ui` / `--ui` | — | Always start TUI without prompting |

---

## Protocol (JSON over UDP or TCP)

All messages are newline-terminated JSON.

### Client → Server

```json
{"cmd":"REGISTER","task":"my-api","address":"10.0.0.5:8080"}
{"cmd":"QUERY","task":"my-api"}
{"cmd":"HEARTBEAT","task":"my-api","address":"10.0.0.5:8080"}
```

### Server → Client

```json
{"status":"OK"}                              // REGISTER / HEARTBEAT success
{"status":"OK","address":"10.0.0.5:8080"}    // QUERY success
{"status":"NOTFOUND"}                        // task unknown
{"status":"FORBIDDEN"}                       // firewall blocked all candidates
{"status":"ERR","error":"..."}               // protocol error
```

### DHT Inter-Node (JSON over TCP)

```json
{"type":"PING|PONG|JOIN|PEERLIST|STORE|FIND|FOUND|NOTFOUND","sender":"host:port","payload":{...}}
```

---

## Firewall Rules

Enable with `--firewall --firewall-rules <file>`. Format:

```
# source_ip_or_cidr  destination_ip_or_cidr
192.168.1.10         10.0.0.5
192.168.1.0/24       10.0.0.0/24
```

A query from `requestorIP` only returns addresses that satisfy at least one rule. See [firewall_rules.example](firewall_rules.example).

---

## Client Library (`pkg/client`)

```go
c := client.NewClient("localhost:5000")

err  := c.Register("api-service", "10.0.0.5:8080")
addr, err := c.Query("api-service")
err  = c.Heartbeat("api-service", "10.0.0.5:8080")
```

---

## Cache Demo (`cmd/cache_demo`)

Demonstrates in-memory cache hits vs Postgres fallback latency. Requires the server running with `--store-url` and `--cache-max-size`.

```powershell
.\demos\cache\run_cache_demo.ps1
```

---

## Simulation (`Simulation/`)

A transit payment simulation that uses TDS as its service registry:

| Component | Role |
|---|---|
| **Gate** | Card reader; contacts CS (OY) or PA (PCTR) to authorise taps |
| **Station Computer** | Batches tap events and forwards to CS / PCTRBO |
| **CS** | OY card ledger; constructs journeys every 5 minutes |
| **PCTRBO** | Token-based transaction ledger |
| **PA** | PCTR private key holder; generates restitution files |
| **Ticket Distributor** | Issues PCTR tokens |
| **Card DB** | Shared card data store |

Quick start (single machine):

```powershell
.\Simulation\orchestrate_windows.ps1
```

See [Simulation/README.md](Simulation/README.md) for database setup and per-component instructions.

### Kubernetes

```powershell
cd k8s
.\build-images.ps1   # build Docker images
.\deploy.ps1         # apply all manifests
```

See [k8s/README.md](k8s/README.md) for per-service deployment options.

---

## Testing

```bash
# All tests
go test ./...

# DHT / P2P integration tests
go test ./test_scripts -run "DHT" -count=1
go test ./test_scripts -run "P2P" -count=1

# Core packages with coverage
go test ./pkg/registry ./pkg/transport ./pkg/firewall ./pkg/client -cover

# Race detector
go test -race ./...
```

Current coverage (core packages):

| Package | Coverage |
|---|---|
| `pkg/client` | ~75% |
| `pkg/transport` | ~64% |
| `pkg/firewall` | ~54% |
| `pkg/registry` | ~38% |

---

## Building

```bash
go build ./...

go build -o bin/server.exe       ./cmd/server
go build -o bin/client_proxy.exe ./cmd/client_proxy
go build -o bin/test_client.exe  ./cmd/test_client
```

---

## Environment Variables

| Variable | Used by | Description |
|---|---|---|
| `DATABASE_URL` | `cmd/server` | PostgreSQL URL (fallback if `--store-url` not set) |
| `TDS_SERVER_ADDR` | `cmd/client_proxy` | Centralized server address (default `127.0.0.1:5000`) |
| `TDS_PROXY_LISTEN` | `cmd/client_proxy` | Proxy listen address (default `:5100`) |
