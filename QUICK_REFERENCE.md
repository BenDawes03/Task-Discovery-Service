# P2P Quick Reference

## Start Nodes

### First Node (Bootstrap)
```bash
go run ./cmd/client_proxy -p2p -p2p-port :6000
```

### Additional Nodes
```bash
# Node 2
TDS_PROXY_LISTEN=:5101 go run ./cmd/client_proxy -p2p -p2p-port :6001 -bootstrap localhost:6000

# Node 3
TDS_PROXY_LISTEN=:5102 go run ./cmd/client_proxy -p2p -p2p-port :6002 -bootstrap localhost:6000

# Node 4 (can use multiple bootstraps)
TDS_PROXY_LISTEN=:5103 go run ./cmd/client_proxy -p2p -p2p-port :6003 -bootstrap localhost:6000,localhost:6001
```

## Test Operations

### Using client_demo (Interactive)
```bash
# Build
go build -o client_demo.exe ./cmd/client_demo

# Interactive mode selection
./client_demo
# Select mode:
# 1. Centralized (default)
# 2. P2P
# Enter choice [1]: 2
# Enter proxy address [default: localhost:5100]: 
# 
# > REGISTER web-api 192.168.1.50:8080
# Response: OK
# > QUERY web-api
# Response: 192.168.1.50:8080
# > quit

# Command-line flags
./client_demo -mode p2p -server localhost:5100
./client_demo -mode centralized -server localhost:5100
```

### Using test_client (Non-Interactive)
```bash
# Build first
go build -o test_client.exe ./cmd/test_client

# Register a service
./test_client -server localhost:5100 REGISTER web-api 192.168.1.50:8080
# Response: OK

# Query a service
./test_client -server localhost:5100 QUERY web-api
# Response: 192.168.1.50:8080

# Register multiple instances
./test_client -server localhost:5101 REGISTER api 10.0.1.5:8080
./test_client -server localhost:5101 REGISTER api 10.0.1.6:8080

# Query (returns first instance)
./test_client -server localhost:5102 QUERY api
# Response: 10.0.1.5:8080
```

### Using PowerShell UDP
```powershell
# Register
$client = New-Object System.Net.Sockets.UdpClient
$client.Connect("localhost", 5100)
$bytes = [Text.Encoding]::ASCII.GetBytes("REGISTER my-service 10.0.0.1:9000")
$client.Send($bytes, $bytes.Length)
$endpoint = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
$response = $client.Receive([ref]$endpoint)
[Text.Encoding]::ASCII.GetString($response)
# Output: OK

# Query
$client2 = New-Object System.Net.Sockets.UdpClient
$client2.Connect("localhost", 5101)
$bytes2 = [Text.Encoding]::ASCII.GetBytes("QUERY my-service")
$client2.Send($bytes2, $bytes2.Length)
$endpoint2 = New-Object System.Net.IPEndPoint([System.Net.IPAddress]::Any, 0)
$response2 = $client2.Receive([ref]$endpoint2)
[Text.Encoding]::ASCII.GetString($response2)
# Output: 10.0.0.1:9000
```

## Ports

| Port | Purpose |
|------|---------|
| 5100, 5101, 5102... | Client proxy UDP listeners (clients connect here) |
| 6000, 6001, 6002... | DHT peer-to-peer TCP (nodes talk to each other) |

## Interactive Commands

Once the proxy is running, you can use these commands:

```
> help
commands: help, stats, dht, quit

> stats
stats: registers=5 queries=12 errors=0

> quit
exiting
```

## Environment Variables

```bash
# Set client proxy listen address
export TDS_PROXY_LISTEN=:5100

# For Windows PowerShell
$env:TDS_PROXY_LISTEN=":5100"
```

## Common Patterns

### Local Testing (3 nodes)
```bash
# Terminal 1
go run ./cmd/client_proxy -p2p -p2p-port :6000

# Terminal 2
TDS_PROXY_LISTEN=:5101 go run ./cmd/client_proxy -p2p -p2p-port :6001 -bootstrap localhost:6000

# Terminal 3
TDS_PROXY_LISTEN=:5102 go run ./cmd/client_proxy -p2p -p2p-port :6002 -bootstrap localhost:6000

# Terminal 4 (test client)
./test_client -server localhost:5100 REGISTER app1 10.0.0.1:8080
./test_client -server localhost:5101 REGISTER app2 10.0.0.2:8080
./test_client -server localhost:5102 REGISTER app3 10.0.0.3:8080
./test_client -server localhost:5100 QUERY app2  # Query from different node
./test_client -server localhost:5101 QUERY app3  # Works across the DHT!
```

### Production (Multiple Hosts)

**Host A (192.168.1.10):**
```bash
go run ./cmd/client_proxy -p2p -p2p-port 192.168.1.10:6000
```

**Host B (192.168.1.11):**
```bash
go run ./cmd/client_proxy -p2p -p2p-port 192.168.1.11:6000 -bootstrap 192.168.1.10:6000
```

**Host C (192.168.1.12):**
```bash
go run ./cmd/client_proxy -p2p -p2p-port 192.168.1.12:6000 -bootstrap 192.168.1.10:6000,192.168.1.11:6000
```

**Client (any host):**
```bash
./test_client -server 192.168.1.10:5100 REGISTER my-api 10.0.5.50:8080
./test_client -server 192.168.1.11:5100 QUERY my-api
```

## Troubleshooting

### "connection refused" on bootstrap
- Make sure bootstrap node is running
- Check firewall allows TCP on port 6000
- Verify correct IP address

### "NOTFOUND" even after registering
- Wait 1-2 seconds for DHT to sync
- Check if registration was successful (OK response)
- Verify you're querying the correct task name

### Node can't join network
- Ensure bootstrap node is reachable via TCP
- Check network connectivity with: `Test-NetConnection -ComputerName <host> -Port 6000`
- Verify no NAT/firewall blocking

### High error count
- Check `stats` command output
- Review console logs for specific errors
- Ensure nodes aren't crashing/restarting

## Log Messages

Normal operation:
```
[dht] DHT listening on :6000 (node ID: 0a3f12d4)
[dht] joined network, 2 peers known
[dht-registry] registering web-api -> 192.168.1.50:8080
[dht] stored on localhost:6001: web-api -> 192.168.1.50:8080
[client-proxy] P2P QUERY web-api (from 127.0.0.1:54321)
[dht] found on localhost:6001: web-api -> 1 addresses
```

## Performance Tips

1. **Use multiple bootstrap nodes** for redundancy
2. **Spread nodes across network** for better distribution
3. **Monitor stats regularly** to detect issues
4. **Start with 3+ nodes** for proper DHT behavior
5. **Use local IPs** in production, not localhost

## Differences from Centralized Mode

| Aspect | Centralized | P2P |
|--------|-------------|-----|
| Start command | `go run ./cmd/server` + proxy | `go run ./cmd/client_proxy -p2p` |
| Bootstrap | Not needed | Required (except first node) |
| Ports | 5000 (server), 5100 (proxy) | 6000+ (DHT), 5100 (proxy) |
| Consistency | Immediate | Eventual (~1-2s) |
| Fault tolerance | Single point of failure | No single point |
