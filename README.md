# TDS - Task Distribution Service

A task-to-IP registry service with support for firewall-aware routing, persistent storage, and an interactive TUI.

## Features

- **Task-to-IP Registry**: Register services by task name and query for available service addresses
- **Firewall-Aware Routing**: Filter service responses based on network firewall rules
- **Round-Robin Load Balancing**: Distribute queries evenly across available services
- **Persistent Storage**: Optional PostgreSQL backend with LFU caching
- **Interactive TUI**: Real-time monitoring dashboard for registry state
- **Dual Transport**: Support for both UDP and TCP protocols
- **Service Discovery**: Automatic broadcast of server information on startup

## Quick Start

### In-Memory Mode (No Persistence)

```bash
go run ./cmd/server
```

### With Firewall Rules

```bash
go run ./cmd/server --firewall-rules firewall_rules.txt
```

### With PostgreSQL Backend

```bash
export DATABASE_URL="postgres://user:pass@localhost/tds"
go run ./cmd/server --store-url "$DATABASE_URL"
```

### Headless Mode (No TUI)

```bash
go run ./cmd/server --no-tui
```

## Firewall-Aware Routing

The server can filter service responses based on network firewall rules to ensure clients only receive addresses they can actually reach.

### How It Works

1. Client queries server for a task
2. Server identifies the client's IP address
3. Server filters available services based on firewall rules
4. Server returns only addresses the client is allowed to communicate with

### Firewall Rules File Format

```
# Format: source_ip_or_cidr destination_ip_or_cidr
# Allow client at 192.168.1.10 to reach server at 10.0.0.5
192.168.1.10 10.0.0.5

# Allow entire subnet to access server subnet
192.168.1.0/24 10.0.0.0/24
```

See [docs/FIREWALL_RULES.md](docs/FIREWALL_RULES.md) for complete documentation.

## Protocol

### Registration
```
REGISTER <task> <address>
Response: OK | ERR
```

### Query
```
QUERY <task>
Response: <address> | NOTFOUND | FORBIDDEN | ERR
```

- **address**: Service address (e.g., `10.0.0.5:8080`)
- **NOTFOUND**: No service registered for task
- **FORBIDDEN**: No service allowed by firewall rules
- **ERR**: Protocol error

## Command-Line Options

```
--firewall-rules <path>    Path to firewall rules file (optional)
--store-url <url>          PostgreSQL connection URL (optional)
--heartbeat-timeout <dur>  Timeout for service heartbeats (default: 60s)
--cleanup-interval <dur>   Cleanup interval for stale entries (default: 10s)
--no-tui                   Run without interactive TUI
--force-ui, -ui            Force TUI mode
--tcp                      Use TCP transport (default: UDP)
--udp                      Use UDP transport
```

## Architecture

- **pkg/registry**: Registry interface and implementations (in-memory, store-backed)
- **pkg/firewall**: Firewall rule parsing and filtering
- **pkg/transport**: UDP and TCP server implementations
- **pkg/store**: Persistent storage interface and PostgreSQL implementation
- **pkg/client**: Client library and proxy implementations
- **cmd/server**: Server entrypoint with TUI
- **cmd/client_demo**: Demo client application
- **cmd/client_proxy**: Client proxy for service discovery

## Documentation

- [Firewall Rules](docs/FIREWALL_RULES.md): Complete guide to firewall-aware routing
- [Database Cleanup](docs/DATABASE_CLEANUP.md): Information about database cleanup and inactive entries

## Testing

```bash
# Build all packages
go build ./...

# Run with race detection
go run -race ./cmd/server

# Test firewall functionality
./test_scripts/test_firewall.ps1
```

## Example Usage

### Server

```bash
# Start server with firewall rules
go run ./cmd/server --firewall-rules firewall_rules.txt
```

### Client Registration

```bash
# Register a service (UDP)
echo "REGISTER my_task 10.0.0.5:8080" | nc -u localhost 5000
```

### Client Query

```bash
# Query for a service (UDP)
echo "QUERY my_task" | nc -u localhost 5000
# Response: 10.0.0.5:8080 (if allowed by firewall)
# Response: FORBIDDEN (if blocked by firewall)
```

## License

See LICENSE file for details.
